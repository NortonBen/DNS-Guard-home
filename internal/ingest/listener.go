package ingest

import (
	"context"
	"errors"
	"log/slog"
	"net"
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

// Sink là nơi ingest ghi sự kiện xuống.
//
// Interface khai báo ở đây, nơi tiêu thụ, chứ không ở package store: ingest không
// cần biết gì về CSDL ngoài việc "đưa được một lô sự kiện đi đâu đó".
type Sink interface {
	WriteEvents(ctx context.Context, events []Event) error
}

// Stats là số liệu vận hành, phục vụ /health và dashboard.
type Stats struct {
	PacketsReceived int64 `json:"packets_received"`
	EventsAccepted  int64 `json:"events_accepted"`
	PacketsDropped  int64 `json:"packets_dropped"`
	DecodeErrors    int64 `json:"decode_errors"`
	LastEventAgeSec int64 `json:"last_event_age_s"`

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
	dropped  atomic.Int64
	errs     atomic.Int64
	lastAt   atomic.Int64 // Unix giây của sự kiện gần nhất

	// listenErr giữ lỗi khiến bộ nhận không chạy được, để /health nói đúng nguyên nhân.
	listenErr atomic.Pointer[string]

	// localAddr là địa chỉ thật sau khi bind, khác cấu hình khi cổng đặt là 0.
	localAddr atomic.Pointer[string]

	// etld1Cache tránh gọi Public Suffix List cho mỗi gói. Một mạng gia đình chỉ
	// gặp vài chục nghìn tên miền phân biệt, nên cache đầy đủ là rẻ.
	etld1Cache sync.Map
}

// NewListener dựng bộ nhận. Chưa mở socket cho tới khi gọi Run.
func NewListener(opts Options, sink Sink, log *slog.Logger) *Listener {
	opts.withDefaults()
	return &Listener{opts: opts, sink: sink, log: log}
}

// Run mở socket UDP và chạy tới khi ctx bị hủy.
//
// Hai goroutine: một chỉ đọc socket và bóc gói, một chỉ gom lô và ghi CSDL. Tách ra
// để việc ghi đĩa chậm không làm đầy bộ đệm nhận của nhân và mất gói — datagram UDP
// mất là mất hẳn, không có cách nào lấy lại.
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

	queue := make(chan Event, l.opts.QueueSize)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		l.writeLoop(ctx, queue)
	}()

	go func() {
		<-ctx.Done()
		conn.Close() // ép ReadFromUDP thoát
	}()

	buf := make([]byte, 65535)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			close(queue)
			wg.Wait()
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			l.setListenError(err)
			return err
		}
		l.packets.Add(1)

		ev, ok := l.decode(buf[:n])
		if !ok {
			continue
		}

		select {
		case queue <- ev:
		default:
			// Hàng đợi đầy nghĩa là CSDL không theo kịp. Bỏ sự kiện và đếm lại còn
			// hơn chặn vòng đọc, vì chặn ở đây làm mất gói ở tầng nhân mà không ai
			// biết. Số liệu này hiện trên /health.
			l.dropped.Add(1)
		}
	}
}

// decode bóc một datagram thành Event. Trả về false nếu gói không phải truy vấn
// DNS quan tâm — đó là chuyện bình thường, không phải lỗi.
func (l *Listener) decode(buf []byte) (Event, bool) {
	pkt, err := decodeTZSP(buf)
	if err != nil {
		l.countDecodeError(err)
		return Event{}, false
	}

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

// countDecodeError chỉ đếm những lỗi thật sự bất thường. Gói không phải DNS hoặc
// không phải IPv4/IPv6 là chuyện thường xuyên trên một cổng mirror và không đáng
// báo động.
func (l *Listener) countDecodeError(err error) {
	if errors.Is(err, errNotDNSQuery) || errors.Is(err, errUnsupportedL3) {
		return
	}
	l.errs.Add(1)
}

// writeLoop gom sự kiện thành lô rồi ghi. Ghi theo lô là bắt buộc chứ không phải
// tối ưu: Pi có thể chạy thẻ SD, và một giao dịch cho mỗi truy vấn sẽ mòn thẻ.
func (l *Listener) writeLoop(ctx context.Context, queue <-chan Event) {
	batch := make([]Event, 0, l.opts.BatchSize)
	ticker := time.NewTicker(l.opts.FlushEvery)
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		// Dùng context.WithoutCancel: khi tắt máy, lô cuối vẫn phải được ghi thay vì
		// mất trắng.
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()

		if err := l.sink.WriteEvents(writeCtx, batch); err != nil {
			l.log.Error("ghi lô sự kiện thất bại", "count", len(batch), "err", err)
		} else {
			l.accepted.Add(int64(len(batch)))
			l.lastAt.Store(time.Now().Unix())
		}
		batch = batch[:0]
	}

	for {
		select {
		case ev, ok := <-queue:
			if !ok {
				flush()
				return
			}
			batch = append(batch, ev)
			if len(batch) >= l.opts.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
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
		PacketsReceived: l.packets.Load(),
		EventsAccepted:  l.accepted.Load(),
		PacketsDropped:  l.dropped.Load(),
		DecodeErrors:    l.errs.Load(),
		LastEventAgeSec: -1,
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
