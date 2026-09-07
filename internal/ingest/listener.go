package ingest

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/publicsuffix"
)

// Event là một truy vấn DNS đã bóc tách xong.
type Event struct {
	Domain string
	ETLD1  string
	Client string
	QType  uint16
	At     time.Time
}

// Resolution là một lượt phân giải đã bóc tách xong: tên được hỏi và các địa chỉ
// mà câu trả lời trả về.
//
// Không có trường client. IP nguồn của một câu trả lời là resolver chứ không phải
// thiết bị đã hỏi, và IP đích chỉ đúng ở chiều router → client, không đúng ở chiều
// upstream → router. Việc quy kết client lấy bằng cách nối với truy vấn tương ứng
// đã ghi trong query_events, nên ánh xạ ở đây cố ý không phụ thuộc client.
type Resolution struct {
	Domain string
	ETLD1  string
	IPs    []netip.Addr
	TTL    uint32
	At     time.Time
}

// Sink là nơi ingest ghi sự kiện xuống.
//
// Interface khai báo ở đây, nơi tiêu thụ, chứ không ở package store: ingest không
// cần biết gì về CSDL ngoài việc "đưa được một lô sự kiện đi đâu đó".
type Sink interface {
	WriteEvents(ctx context.Context, events []Event) error
	WriteResolutions(ctx context.Context, resolutions []Resolution) error
}

// FrameRecorder nhận bản sao khung Ethernet để ghi ra file điều tra.
//
// Khai báo ở đây, nơi tiêu thụ, cùng lý do như Sink: ingest không cần biết gì về định
// dạng pcap ngoài việc "đưa được một khung đi đâu đó". Cài đặt phải không bao giờ
// chặn — nó nằm ngay trên đường đọc socket.
type FrameRecorder interface {
	Write(frame []byte)
}

// Stats là số liệu vận hành, phục vụ /health và dashboard.
type Stats struct {
	PacketsReceived     int64 `json:"packets_received"`
	EventsAccepted      int64 `json:"events_accepted"`
	ResolutionsAccepted int64 `json:"resolutions_accepted"`
	PacketsDropped      int64 `json:"packets_dropped"`
	DecodeErrors        int64 `json:"decode_errors"`
	LastEventAgeSec     int64 `json:"last_event_age_s"`

	// ListenError khác rỗng nghĩa là bộ nhận không mở được cổng và sẽ không bao giờ
	// nhận được gì. Phân biệt trường hợp này với "cổng mở nhưng không có lưu lượng"
	// là quan trọng: hai triệu chứng giống hệt nhau ở dashboard nhưng nguyên nhân
	// nằm ở hai máy khác nhau.
	ListenError string `json:"listen_error,omitempty"`
}

// Options cấu hình bộ nhận.
type Options struct {
	Addr       string        // địa chỉ UDP lắng nghe, ví dụ ":37008"
	BatchSize  int           // số sự kiện gộp trước khi ghi
	FlushEvery time.Duration // hoặc ghi sau khoảng thời gian này, tùy cái nào đến trước
	QueueSize  int           // hàng đợi giữa vòng đọc gói và bộ ghi
}

func (o *Options) withDefaults() {
	if o.Addr == "" {
		o.Addr = ":37008"
	}
	if o.BatchSize <= 0 {
		o.BatchSize = 1000
	}
	if o.FlushEvery <= 0 {
		o.FlushEvery = 2 * time.Second
	}
	if o.QueueSize <= 0 {
		o.QueueSize = 16384
	}
}

// Listener nhận luồng TZSP và đẩy sự kiện đã bóc tách xuống Sink.
type Listener struct {
	opts Options
	sink Sink
	log  *slog.Logger

	packets  atomic.Int64
	accepted atomic.Int64
	resolved atomic.Int64
	dropped  atomic.Int64
	errs     atomic.Int64
	lastAt   atomic.Int64 // Unix giây của sự kiện gần nhất

	// listenErr giữ lỗi khiến bộ nhận không chạy được, để /health nói đúng nguyên nhân.
	listenErr atomic.Pointer[string]

	// localAddr là địa chỉ thật sau khi bind, khác cấu hình khi cổng đặt là 0.
	localAddr atomic.Pointer[string]

	// recorder ghi khung thô ra file pcap. Nil nghĩa là tắt, và đó là mặc định.
	recorder atomic.Pointer[FrameRecorder]

	// etld1Cache tránh gọi Public Suffix List cho mỗi gói. Một mạng gia đình chỉ
	// gặp vài chục nghìn tên miền phân biệt, nên cache đầy đủ là rẻ.
	etld1Cache sync.Map
}

// NewListener dựng bộ nhận. Chưa mở socket cho tới khi gọi Run.
func NewListener(opts Options, sink Sink, log *slog.Logger) *Listener {
	opts.withDefaults()
	return &Listener{opts: opts, sink: sink, log: log}
}

// SetRecorder bật ghi pcap. Gọi trước Run.
//
// Đặt sau khi dựng chứ không qua Options: ghi pcap mặc định tắt, và phần lớn lời gọi
// NewListener — kể cả trong test — không quan tâm tới nó.
func (l *Listener) SetRecorder(rec FrameRecorder) { l.recorder.Store(&rec) }

// Run mở socket UDP và chạy tới khi ctx bị hủy.
//
// Ba goroutine: một chỉ đọc socket và bóc gói, hai goroutine còn lại gom lô và ghi
// CSDL — một cho truy vấn, một cho lượt phân giải. Tách ra để việc ghi đĩa chậm
// không làm đầy bộ đệm nhận của nhân và mất gói — datagram UDP mất là mất hẳn,
// không có cách nào lấy lại.
//
// Hai đường ghi tách biệt để một bên chậm không chặn bên kia: bảng phân giải có
// ràng buộc duy nhất nên lượt ghi của nó đắt hơn ghi log truy vấn.
func (l *Listener) Run(ctx context.Context) error {
	addr, err := net.ResolveUDPAddr("udp", l.opts.Addr)
	if err != nil {
		l.setListenError(err)
		return err
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		// Tiến trình vẫn chạy tiếp: API và endpoint danh sách còn phục vụ được bằng
		// dữ liệu đã có. Nhưng lỗi phải hiện ra ở /health, nếu không người vận hành
		// sẽ đi tìm nguyên nhân ở thiết bị mạng trong khi cổng ở đây mới là vấn đề.
		l.setListenError(err)
		return err
	}
	defer conn.Close()
	l.clearListenError()

	bound := conn.LocalAddr().String()
	l.localAddr.Store(&bound)

	// Bộ đệm nhận lớn: mirror có thể dồn cụm khi mạng bận.
	if err := conn.SetReadBuffer(4 << 20); err != nil {
		l.log.Warn("không đặt được bộ đệm nhận", "err", err)
	}

	l.log.Info("nhận luồng TZSP", "addr", bound)

	events := make(chan Event, l.opts.QueueSize)
	resolutions := make(chan Resolution, l.opts.QueueSize)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		batchLoop(ctx, events, l.opts.BatchSize, l.opts.FlushEvery, l.writeEvents)
	}()
	go func() {
		defer wg.Done()
		batchLoop(ctx, resolutions, l.opts.BatchSize, l.opts.FlushEvery, l.writeResolutions)
	}()

	go func() {
		<-ctx.Done()
		conn.Close() // ép ReadFromUDP thoát
	}()

	buf := make([]byte, 65535)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			close(events)
			close(resolutions)
			wg.Wait()
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			l.setListenError(err)
			return err
		}
		l.packets.Add(1)

		// Bóc TZSP tách khỏi bóc Ethernet để khung thô đi được vào bộ ghi pcap trước
		// khi bị diễn giải. File điều tra phải chứa cả gói mà tầng dưới không hiểu:
		// một cuộc tấn công dùng DNS dị dạng sẽ biến mất khỏi bằng chứng nếu ta chỉ
		// ghi những gì mình bóc được.
		frame, err := tzspPayload(buf[:n])
		if err != nil {
			l.countDecodeError(err)
			continue
		}
		if rec := l.recorder.Load(); rec != nil {
			(*rec).Write(frame)
		}

		pkt, err := decodeEthernet(frame)
		if err != nil {
			l.countDecodeError(err)
			continue
		}

		// Hàng đợi đầy nghĩa là CSDL không theo kịp. Bỏ bản ghi và đếm lại còn hơn
		// chặn vòng đọc, vì chặn ở đây làm mất gói ở tầng nhân mà không ai biết. Số
		// liệu này hiện trên /health.
		if pkt.answer {
			if res, ok := l.decodeAnswer(pkt); ok {
				select {
				case resolutions <- res:
				default:
					l.dropped.Add(1)
				}
			}
			continue
		}

		if ev, ok := l.decodeQuery(pkt); ok {
			select {
			case events <- ev:
			default:
				l.dropped.Add(1)
			}
		}
	}
}

// decodeQuery bóc payload của một gói truy vấn thành Event. Trả về false nếu gói
// không phải truy vấn quan tâm — đó là chuyện bình thường, không phải lỗi.
func (l *Listener) decodeQuery(pkt packet) (Event, bool) {
	name, qtype, err := parseQuestion(pkt.payload)
	if err != nil {
		l.countDecodeError(err)
		return Event{}, false
	}
	if !wantedType(qtype) || !validDomain(name) {
		return Event{}, false
	}

	return Event{
		Domain: name,
		ETLD1:  l.etld1(name),
		Client: pkt.client.String(),
		QType:  qtype,
		At:     time.Now().UTC(),
	}, true
}

// decodeAnswer bóc payload của một gói trả lời thành Resolution.
//
// Không lọc theo loại bản ghi như decodeQuery: parseAnswer chỉ trả về A và AAAA,
// nên tới đây đã là thứ cần. Vẫn kiểm tra tên miền, vì tên trong câu trả lời cũng
// tới từ mạng và cũng cần được coi là dữ liệu không tin cậy.
func (l *Listener) decodeAnswer(pkt packet) (Resolution, bool) {
	name, ips, ttl, err := parseAnswer(pkt.payload)
	if err != nil {
		l.countDecodeError(err)
		return Resolution{}, false
	}
	if !validDomain(name) {
		return Resolution{}, false
	}

	return Resolution{
		Domain: name,
		ETLD1:  l.etld1(name),
		IPs:    ips,
		TTL:    ttl,
		At:     time.Now().UTC(),
	}, true
}

// countDecodeError chỉ đếm những lỗi thật sự bất thường. Gói không phải DNS hoặc
// không phải IPv4/IPv6 là chuyện thường xuyên trên một cổng mirror và không đáng
// báo động.
func (l *Listener) countDecodeError(err error) {
	switch {
	case errors.Is(err, errNotDNSQuery), errors.Is(err, errUnsupportedL3):
		return
	// Câu trả lời không có địa chỉ là chuyện thường: NXDOMAIN, hoặc câu trả lời chỉ
	// gồm CNAME và SOA. Không phải gói hỏng.
	case errors.Is(err, errNotDNSAnswer), errors.Is(err, errNoAnswerRecords):
		return
	}
	l.errs.Add(1)
}

// batchLoop gom phần tử thành lô rồi ghi. Ghi theo lô là bắt buộc chứ không phải
// tối ưu: Pi có thể chạy thẻ SD, và một giao dịch cho mỗi truy vấn sẽ mòn thẻ.
//
// Tổng quát theo kiểu phần tử vì truy vấn và lượt phân giải cần đúng cùng một cách
// gom lô, chỉ khác nhau ở hàm ghi cuối cùng.
func batchLoop[T any](ctx context.Context, queue <-chan T, size int, every time.Duration, write func(context.Context, []T)) {
	batch := make([]T, 0, size)
	ticker := time.NewTicker(every)
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		// Dùng context.WithoutCancel: khi tắt máy, lô cuối vẫn phải được ghi thay vì
		// mất trắng.
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()

		write(writeCtx, batch)
		batch = batch[:0]
	}

	for {
		select {
		case v, ok := <-queue:
			if !ok {
				flush()
				return
			}
			batch = append(batch, v)
			if len(batch) >= size {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (l *Listener) writeEvents(ctx context.Context, batch []Event) {
	if err := l.sink.WriteEvents(ctx, batch); err != nil {
		l.log.Error("ghi lô sự kiện thất bại", "count", len(batch), "err", err)
		return
	}
	l.accepted.Add(int64(len(batch)))
	// Chỉ truy vấn mới cập nhật mốc này: cảnh báo "không nhận được truy vấn mới"
	// trên dashboard phải im lặng đúng lúc luồng truy vấn im lặng.
	l.lastAt.Store(time.Now().Unix())
}

func (l *Listener) writeResolutions(ctx context.Context, batch []Resolution) {
	if err := l.sink.WriteResolutions(ctx, batch); err != nil {
		l.log.Error("ghi lô phân giải thất bại", "count", len(batch), "err", err)
		return
	}
	l.resolved.Add(int64(len(batch)))
}

func (l *Listener) etld1(name string) string {
	if v, ok := l.etld1Cache.Load(name); ok {
		return v.(string)
	}
	e := ETLD1(name)
	l.etld1Cache.Store(name, e)
	return e
}

// Stats trả về số liệu hiện tại.
func (l *Listener) Stats() Stats {
	s := Stats{
		PacketsReceived:     l.packets.Load(),
		EventsAccepted:      l.accepted.Load(),
		ResolutionsAccepted: l.resolved.Load(),
		PacketsDropped:      l.dropped.Load(),
		DecodeErrors:        l.errs.Load(),
		LastEventAgeSec:     -1,
	}
	if last := l.lastAt.Load(); last > 0 {
		s.LastEventAgeSec = time.Now().Unix() - last
	}
	if msg := l.listenErr.Load(); msg != nil {
		s.ListenError = *msg
	}
	return s
}

// LocalAddr trả về địa chỉ đang lắng nghe, rỗng nếu chưa bind được.
func (l *Listener) LocalAddr() string {
	if a := l.localAddr.Load(); a != nil {
		return *a
	}
	return ""
}

func (l *Listener) setListenError(err error) {
	msg := err.Error()
	l.listenErr.Store(&msg)
}

func (l *Listener) clearListenError() { l.listenErr.Store(nil) }

// ETLD1 trả về eTLD+1 theo Public Suffix List, ví dụ "a.b.example.co.uk" →
// "example.co.uk". Lùi về chính tên miền khi không xác định được.
func ETLD1(name string) string {
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	if e, err := publicsuffix.EffectiveTLDPlusOne(name); err == nil {
		return e
	}
	return name
}

// validDomain kiểm tra tên miền theo RFC trước khi cho vào CSDL. Cổng mirror nhận
// được đủ thứ rác, và ràng buộc ở tầng CSDL không nên là nơi đầu tiên phát hiện ra.
func validDomain(name string) bool {
	if len(name) == 0 || len(name) > 253 {
		return false
	}
	if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") {
		return false
	}
	// Truy vấn không có dấu chấm là tên nội bộ (mDNS, WPAD), không phải domain thật.
	if !strings.Contains(name, ".") {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_'
			if !ok {
				return false
			}
		}
	}
	return true
}
