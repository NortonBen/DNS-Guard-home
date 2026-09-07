// Package pcap ghi khung Ethernet nhận được ra file .pcap phục vụ điều tra.
//
// Ghi **gói thật** đã nhận, không dựng lại từ CSDL. Một file pcab dựng lại từ bản ghi
// có cấu trúc là gói do máy tự bịa: nó trông như bằng chứng nhưng không phải, và trình
// bày nó như bằng chứng là sai. File ở đây chỉ chứa đúng những byte đã đi qua dây.
//
// Nội dung file phản ánh những gì thiết bị mạng mirror sang. Với cấu hình mặc định
// (filter-port=53) thì đó là DNS và chỉ DNS — không phải toàn bộ lưu lượng.
package pcap

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Định dạng pcap cổ điển, little-endian. Xem https://wiki.wireshark.org/Development/LibpcapFileFormat
const (
	magicMicroseconds = 0xA1B2C3D4
	versionMajor      = 2
	versionMinor      = 4
	linkTypeEthernet  = 1
	snapLen           = 65535

	globalHeaderLen = 24
	recordHeaderLen = 16
)

// filePrefix và fileSuffix nhận diện file do bộ ghi này tạo ra. Vòng xoay chỉ đụng
// tới file khớp cả hai, để một thư mục dùng chung không bị xoá nhầm.
const (
	filePrefix = "dnsguard-"
	fileSuffix = ".pcap"
)

// Options cấu hình bộ ghi.
type Options struct {
	Dir string // thư mục chứa file pcap
	// MaxFileBytes là ngưỡng xoay sang file mới.
	MaxFileBytes int64
	// MaxTotalBytes là trần dung lượng của cả thư mục. Vượt thì xoá file cũ nhất.
	MaxTotalBytes int64
	// QueueSize là hàng đợi giữa vòng đọc gói và bộ ghi đĩa.
	QueueSize int
}

func (o *Options) withDefaults() {
	if o.MaxFileBytes <= 0 {
		o.MaxFileBytes = 64 << 20
	}
	if o.MaxTotalBytes <= 0 {
		o.MaxTotalBytes = 2 << 30
	}
	if o.QueueSize <= 0 {
		o.QueueSize = 4096
	}
}

// Stats là số liệu vận hành của bộ ghi.
type Stats struct {
	FramesWritten int64  `json:"frames_written"`
	FramesDropped int64  `json:"frames_dropped"`
	BytesWritten  int64  `json:"bytes_written"`
	CurrentFile   string `json:"current_file,omitempty"`
	TotalBytes    int64  `json:"total_bytes"`
	WriteError    string `json:"write_error,omitempty"`
}

// Recorder ghi khung Ethernet ra file pcap xoay vòng.
type Recorder struct {
	opts  Options
	log   *slog.Logger
	queue chan []byte

	written atomic.Int64
	dropped atomic.Int64
	bytes   atomic.Int64
	writeNo atomic.Pointer[string]

	mu       sync.Mutex
	file     *os.File
	fileSize int64
	fileName string
}

// New dựng bộ ghi. Chưa mở file cho tới khi gọi Run.
func New(opts Options, log *slog.Logger) *Recorder {
	opts.withDefaults()
	return &Recorder{
		opts:  opts,
		log:   log,
		queue: make(chan []byte, opts.QueueSize),
	}
}

// Write xếp một khung vào hàng đợi ghi. Không bao giờ chặn.
//
// Bỏ khung khi hàng đợi đầy chứ không chờ: bộ ghi nằm ngay sau vòng đọc socket, và
// chặn ở đó sẽ làm đầy bộ đệm nhận của nhân rồi mất gói ở tầng dưới — mất một dòng
// trong file điều tra còn hơn mất một truy vấn trong CSDL.
//
// Sao chép khung vì người gọi dùng lại bộ đệm đọc cho gói kế tiếp.
func (r *Recorder) Write(frame []byte) {
	if len(frame) == 0 {
		return
	}
	buf := make([]byte, len(frame))
	copy(buf, frame)

	select {
	case r.queue <- buf:
	default:
		r.dropped.Add(1)
	}
}

// Run ghi hàng đợi xuống đĩa tới khi ctx bị hủy.
func (r *Recorder) Run(ctx context.Context) error {
	if err := os.MkdirAll(r.opts.Dir, 0o750); err != nil {
		err = fmt.Errorf("tạo thư mục pcap %q: %w", r.opts.Dir, err)
		r.setWriteError(err)
		return err
	}
	defer r.closeFile()

	// Nhịp đồng bộ định kỳ: file đang mở phải đọc được bằng công cụ ngoài ngay giữa
	// chừng, không phải đợi tới lúc xoay vòng.
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			r.drain()
			return nil
		case frame := <-r.queue:
			r.writeFrame(frame, time.Now())
		case <-ticker.C:
			r.sync()
		}
	}
}

// drain ghi nốt những gì còn trong hàng đợi lúc tắt máy.
func (r *Recorder) drain() {
	for {
		select {
		case frame := <-r.queue:
			r.writeFrame(frame, time.Now())
		default:
			return
		}
	}
}

// writeFrame ghi một khung, xoay file trước nếu cần.
func (r *Recorder) writeFrame(frame []byte, at time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.file == nil || r.fileSize+int64(recordHeaderLen+len(frame)) > r.opts.MaxFileBytes {
		if err := r.rotate(at); err != nil {
			r.setWriteError(err)
			return
		}
	}

	var hdr [recordHeaderLen]byte
	binary.LittleEndian.PutUint32(hdr[0:4], uint32(at.Unix()))
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(at.Nanosecond()/1000))
	binary.LittleEndian.PutUint32(hdr[8:12], uint32(len(frame)))
	// orig_len bằng incl_len: TZSP mang nguyên khung, không cắt bớt.
	binary.LittleEndian.PutUint32(hdr[12:16], uint32(len(frame)))

	if _, err := r.file.Write(hdr[:]); err != nil {
		r.setWriteError(fmt.Errorf("ghi header gói: %w", err))
		return
	}
	if _, err := r.file.Write(frame); err != nil {
		r.setWriteError(fmt.Errorf("ghi gói: %w", err))
		return
	}

	n := int64(recordHeaderLen + len(frame))
	r.fileSize += n
	r.bytes.Add(n)
	r.written.Add(1)
	r.clearWriteError()
}

// rotate đóng file hiện tại, mở file mới và dọn bớt file cũ.
func (r *Recorder) rotate(at time.Time) error {
	r.closeFileLocked()

	name, f, err := createFile(r.opts.Dir, at)
	if err != nil {
		return err
	}

	var gh [globalHeaderLen]byte
	binary.LittleEndian.PutUint32(gh[0:4], magicMicroseconds)
	binary.LittleEndian.PutUint16(gh[4:6], versionMajor)
	binary.LittleEndian.PutUint16(gh[6:8], versionMinor)
	// thiszone và sigfigs luôn bằng 0: dấu thời gian đã là UTC.
	binary.LittleEndian.PutUint32(gh[16:20], snapLen)
	binary.LittleEndian.PutUint32(gh[20:24], linkTypeEthernet)

	if _, err := f.Write(gh[:]); err != nil {
		f.Close()
		return fmt.Errorf("ghi header pcap %q: %w", name, err)
	}

	r.file, r.fileName, r.fileSize = f, name, globalHeaderLen
	r.bytes.Add(globalHeaderLen)
	return r.enforceQuotaLocked()
}

// createFile tạo file pcap mới chưa tồn tại và trả về tên cùng handle.
//
// Tên luôn mang số thứ tự có độ rộng cố định sau dấu thời gian, kể cả khi không đụng
// độ. Hai lý do, cả hai đều cần:
//
//   - Dấu thời gian chỉ có độ phân giải một giây, mà xoay vòng hai lần trong cùng một
//     giây là chuyện có thật khi trần file nhỏ hoặc lưu lượng dồn cụm. Không có số
//     thứ tự thì lần xoay thứ hai trùng tên lần đầu.
//   - Độ rộng cố định giữ cho sắp xếp theo tên đúng bằng sắp xếp theo thời gian, thứ
//     mà vòng dọn dẹp dựa vào để biết file nào cũ nhất.
func createFile(dir string, at time.Time) (string, *os.File, error) {
	base := filePrefix + at.UTC().Format("20060102-150405")
	for seq := range 1000 {
		name := filepath.Join(dir, fmt.Sprintf("%s-%03d%s", base, seq, fileSuffix))
		// O_EXCL: không bao giờ ghi đè hay nối thêm vào một file điều tra đã có.
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
		if err == nil {
			return name, f, nil
		}
		if !os.IsExist(err) {
			return "", nil, fmt.Errorf("mở file pcap %q: %w", name, err)
		}
	}
	return "", nil, fmt.Errorf("không tạo được file pcap mới trong %q: đã dùng hết số thứ tự của giây này", dir)
}

// enforceQuotaLocked xoá file cũ nhất cho tới khi tổng dung lượng về dưới trần.
//
// Xoá theo tên chứ không theo thời gian sửa: tên chứa dấu thời gian tạo file nên sắp
// xếp theo tên là đúng thứ tự thời gian, và nó không đổi khi file được chép đi chép lại.
func (r *Recorder) enforceQuotaLocked() error {
	files, total, err := r.listFiles()
	if err != nil {
		return err
	}
	for total > r.opts.MaxTotalBytes && len(files) > 1 {
		oldest := files[0]
		// Không bao giờ xoá file đang ghi, kể cả khi một mình nó đã vượt trần.
		if oldest.path == r.fileName {
			break
		}
		if err := os.Remove(oldest.path); err != nil {
			return fmt.Errorf("xoá file pcap cũ %q: %w", oldest.path, err)
		}
		r.log.Info("xoá file pcap cũ để giữ trần dung lượng",
			"file", filepath.Base(oldest.path), "bytes", oldest.size)
		total -= oldest.size
		files = files[1:]
	}
	return nil
}

type pcapFile struct {
	path string
	size int64
}

// listFiles trả về file pcap trong thư mục, cũ nhất trước, kèm tổng dung lượng.
func (r *Recorder) listFiles() ([]pcapFile, int64, error) {
	entries, err := os.ReadDir(r.opts.Dir)
	if err != nil {
		return nil, 0, fmt.Errorf("đọc thư mục pcap %q: %w", r.opts.Dir, err)
	}

	var out []pcapFile
	var total int64
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, filePrefix) || !strings.HasSuffix(name, fileSuffix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, pcapFile{path: filepath.Join(r.opts.Dir, name), size: info.Size()})
		total += info.Size()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, total, nil
}

// Files trả về đường dẫn các file pcap hiện có, cũ nhất trước.
func (r *Recorder) Files() ([]string, error) {
	files, _, err := r.listFiles()
	if err != nil {
		return nil, err
	}
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.path
	}
	return out, nil
}

func (r *Recorder) sync() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file != nil {
		_ = r.file.Sync()
	}
}

func (r *Recorder) closeFile() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeFileLocked()
}

func (r *Recorder) closeFileLocked() {
	if r.file == nil {
		return
	}
	_ = r.file.Sync()
	_ = r.file.Close()
	r.file, r.fileName, r.fileSize = nil, "", 0
}

// Stats trả về số liệu hiện tại.
func (r *Recorder) Stats() Stats {
	r.mu.Lock()
	current := r.fileName
	r.mu.Unlock()

	s := Stats{
		FramesWritten: r.written.Load(),
		FramesDropped: r.dropped.Load(),
		BytesWritten:  r.bytes.Load(),
	}
	if current != "" {
		s.CurrentFile = filepath.Base(current)
	}
	if _, total, err := r.listFiles(); err == nil {
		s.TotalBytes = total
	}
	if msg := r.writeNo.Load(); msg != nil {
		s.WriteError = *msg
	}
	return s
}

func (r *Recorder) setWriteError(err error) {
	msg := err.Error()
	r.writeNo.Store(&msg)
	r.log.Error("ghi pcap thất bại", "err", err)
}

func (r *Recorder) clearWriteError() {
	if r.writeNo.Load() != nil {
		r.writeNo.Store(nil)
	}
}
