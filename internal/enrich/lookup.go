package enrich

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Nguồn tải mặc định cho hai bảng tra cứu cục bộ.
//
// Cả hai đều là dữ liệu công khai, tải một lần rồi dùng offline. Đặt URL ở đây thay
// vì bắt người dùng tự tìm: đây là loại việc mà một cài đặt mới nào cũng phải làm,
// và tự tìm thì dễ lấy nhầm định dạng.
const (
	DefaultASNURL    = "https://iptoasn.com/data/ip2asn-combined.tsv.gz"
	DefaultTrancoURL = "https://tranco-list.eu/top-1m.csv.zip"
)

// maxTableSize chặn trên kích thước tải về. Bảng ASN khoảng 10 MB nén, Tranco khoảng
// 10 MB; 256 MB là ngưỡng an toàn mà một URL sai không thể ăn hết đĩa.
const maxTableSize = 256 << 20

// TableStatus mô tả trạng thái một bảng tra cứu, cho màn Cài đặt.
type TableStatus struct {
	Kind       string `json:"kind"`
	Label      string `json:"label"`
	Describes  string `json:"describes"`
	Loaded     bool   `json:"loaded"`
	Entries    int    `json:"entries"`
	Path       string `json:"path"`
	LoadedAt   string `json:"loaded_at,omitempty"`
	DefaultURL string `json:"default_url"`
}

// Reloadable là nguồn làm giàu đọc dữ liệu từ một file cục bộ và nạp lại được lúc
// chạy, không cần khởi động lại tiến trình.
type Reloadable interface {
	Enricher
	LoadTable(path string) error
	Status() TableStatus
}

// Reloadables trả về các nguồn nạp lại được, theo tên.
func (r *Registry) Reloadables() map[string]Reloadable {
	out := make(map[string]Reloadable, 2)
	for _, s := range r.sources {
		if rl, ok := s.inner.(Reloadable); ok {
			out[s.Name()] = rl
		}
	}
	return out
}

// Statuses trả về trạng thái mọi bảng tra cứu, theo thứ tự xác định.
func (r *Registry) Statuses() []TableStatus {
	var out []TableStatus
	// Duyệt theo thứ tự đăng ký chứ không theo map, để giao diện không nhảy chỗ.
	for _, s := range r.sources {
		if rl, ok := s.inner.(Reloadable); ok {
			out = append(out, rl.Status())
		}
	}
	return out
}

// RefreshTable tải bảng tra cứu về rồi nạp lại vào bộ nhớ.
//
// Ghi ra file tạm cùng thư mục rồi đổi tên: nếu tải hỏng giữa chừng, bảng đang dùng
// vẫn còn nguyên. Chỉ khi tải xong và nạp được thì file cũ mới bị thay.
func RefreshTable(ctx context.Context, target Reloadable, url, dest string) (int64, error) {
	if dest == "" {
		return 0, fmt.Errorf("chưa cấu hình đường dẫn lưu bảng %q", target.Name())
	}
	if url == "" {
		url = target.Status().DefaultURL
	}

	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, fmt.Errorf("tạo thư mục %q: %w", dir, err)
	}

	size, tmpName, err := download(ctx, url, dir, filepath.Base(dest))
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmpName) // no-op nếu Rename đã thành công

	// Nạp thử từ file tạm trước khi thay file thật: một URL trả về trang lỗi HTML sẽ
	// tải xuống thành công nhưng không nạp được, và lúc đó bảng cũ phải còn nguyên.
	if err := target.LoadTable(tmpName); err != nil {
		return 0, fmt.Errorf("tải về được nhưng không đọc được nội dung: %w", err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return 0, fmt.Errorf("đổi tên %q → %q: %w", tmpName, dest, err)
	}
	// Nạp lại từ đường dẫn cuối để Status báo đúng file đang dùng.
	if err := target.LoadTable(dest); err != nil {
		return 0, fmt.Errorf("nạp lại từ %q: %w", dest, err)
	}
	return size, nil
}

// download tải một URL vào file tạm trong dir và trả về số byte đã ghi.
func download(ctx context.Context, url, dir, base string) (int64, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, "", fmt.Errorf("dựng request %q: %w", url, err)
	}
	req.Header.Set("User-Agent", "DNSGuard/1.0")

	// Timeout rộng: hai bảng này cỡ chục megabyte và đường truyền gia đình có thể chậm.
	client := &http.Client{Timeout: 15 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("tải %q: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("tải %q: HTTP %d", url, resp.StatusCode)
	}

	tmp, err := os.CreateTemp(dir, ".tmp-"+base+"-*")
	if err != nil {
		return 0, "", fmt.Errorf("tạo file tạm trong %q: %w", dir, err)
	}
	tmpName := tmp.Name()

	size, err := io.Copy(tmp, io.LimitReader(resp.Body, maxTableSize))
	if err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return 0, "", fmt.Errorf("ghi file tạm: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return 0, "", fmt.Errorf("đóng file tạm: %w", err)
	}
	if size == 0 {
		os.Remove(tmpName)
		return 0, "", fmt.Errorf("tải %q: nội dung rỗng", url)
	}
	return size, tmpName, nil
}

// Kết cục của một lần làm giàu, dùng để chọn thời gian sống cho bản ghi cache.
//
// Phân loại đủ chi tiết để mỗi kết cục có nhịp thử lại riêng: một host đã chết thì
// đừng hỏi lại mỗi giờ, còn hết quota thì nên thử lại ngay trong ngày.
const (
	OutcomeOK          = "ok"
	OutcomeParking     = "parking"
	OutcomeDNSFail     = "dns_fail"
	OutcomeRefused     = "refused"
	OutcomeTimeout     = "timeout"
	OutcomeTLSError    = "tls_error"
	OutcomeHTTPError   = "http_error"
	OutcomeBlockedHost = "blocked_host"
	OutcomeNotFound    = "not_found"
	OutcomeQuota       = "quota"
	OutcomeOther       = "other"
)

// ClassifyOutcome quy một lỗi về một kết cục đã biết.
func ClassifyOutcome(err error) string {
	if err == nil {
		return OutcomeOK
	}

	switch {
	case errors.Is(err, ErrPrivateAddress):
		return OutcomeBlockedHost
	case errors.Is(err, ErrNoAddress):
		return OutcomeDNSFail
	case errors.Is(err, ErrVTQuota):
		return OutcomeQuota
	case errors.Is(err, ErrVTNotFound):
		return OutcomeNotFound
	case errors.Is(err, context.DeadlineExceeded), os.IsTimeout(err):
		return OutcomeTimeout
	case errors.Is(err, syscall.ECONNREFUSED), errors.Is(err, syscall.EHOSTUNREACH),
		errors.Is(err, syscall.ENETUNREACH):
		return OutcomeRefused
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return OutcomeDNSFail
	}
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return OutcomeTLSError
	}
	var recordErr tls.RecordHeaderError
	if errors.As(err, &recordErr) {
		return OutcomeTLSError
	}

	// net.Error có Timeout() riêng, không phải lúc nào cũng khớp os.IsTimeout.
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return OutcomeTimeout
	}

	if strings.Contains(err.Error(), "tls:") || strings.Contains(err.Error(), "x509:") {
		return OutcomeTLSError
	}
	return OutcomeOther
}
