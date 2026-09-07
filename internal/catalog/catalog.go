// Package catalog đồng bộ blocklist công khai.
package catalog

import (
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/benji/dnsguard/internal/store"
)

// ErrSuspiciousDrop báo rằng nguồn trả về ít hơn nhiều so với lần trước.
var ErrSuspiciousDrop = errors.New("nguồn sụt giảm bất thường")

// maxBodySize chặn trên kích thước tải về. Một blocklist lớn nhất hiện nay khoảng
// 20 MB; 128 MB là ngưỡng an toàn mà vẫn không cho một nguồn hỏng ăn hết bộ nhớ.
const maxBodySize = 128 << 20

// minKeepRatio là tỉ lệ tối thiểu so với lần đồng bộ trước. Dưới mức đó thì giữ
// nguyên dữ liệu cũ và cảnh báo: nguồn ngoài hỏng là chuyện thường xuyên, và một
// lần trả về 404 kèm trang lỗi HTML không được xóa sạch danh sách đang dùng.
const minKeepRatio = 0.5

// Syncer tải và cập nhật các nguồn.
type Syncer struct {
	store  *store.Store
	client *http.Client
	log    *slog.Logger
}

// New dựng Syncer.
func New(s *store.Store, log *slog.Logger) *Syncer {
	return &Syncer{
		store: s,
		// Timeout rộng: một số nguồn lớn tải chậm trên đường truyền gia đình.
		client: &http.Client{Timeout: 5 * time.Minute},
		log:    log,
	}
}

// SyncDue đồng bộ mọi nguồn đã tới hạn.
func (s *Syncer) SyncDue(ctx context.Context) (int, error) {
	sources, err := s.store.SourcesDue(ctx)
	if err != nil {
		return 0, err
	}
	synced := 0
	for _, src := range sources {
		if err := s.SyncOne(ctx, src.ID); err != nil {
			// Một nguồn hỏng không dừng các nguồn còn lại.
			s.log.Warn("đồng bộ nguồn thất bại", "source", src.Name, "err", err)
			continue
		}
		synced++
	}
	return synced, nil
}

// SyncOne tải một nguồn và thay toàn bộ mục của nó.
func (s *Syncer) SyncOne(ctx context.Context, sourceID int64) error {
	src, err := s.store.GetSource(ctx, sourceID)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return s.fail(ctx, sourceID, "invalid_url", err)
	}
	req.Header.Set("User-Agent", "DNSGuard/1.0")
	req.Header.Set("Accept-Encoding", "gzip")
	if src.ETag != "" {
		req.Header.Set("If-None-Match", src.ETag)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return s.fail(ctx, sourceID, "network_error", err)
	}
	defer resp.Body.Close()

	// 304 nghĩa là nội dung không đổi: không tải lại, không ghi lại, chỉ đánh dấu
	// đã kiểm tra. Đây là lý do lưu ETag.
	if resp.StatusCode == http.StatusNotModified {
		return s.store.MarkSourceError(ctx, sourceID, "not_modified", nil)
	}
	if resp.StatusCode != http.StatusOK {
		return s.fail(ctx, sourceID, "http_error",
			fmt.Errorf("HTTP %d", resp.StatusCode))
	}

	body := io.Reader(io.LimitReader(resp.Body, maxBodySize))
	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		gz, err := gzip.NewReader(body)
		if err != nil {
			return s.fail(ctx, sourceID, "decode_error", err)
		}
		defer gz.Close()
		body = gz
	}

	domains, err := Parse(body, src.Format)
	if err != nil {
		return s.fail(ctx, sourceID, "parse_error", err)
	}

	// Bảo vệ sụt giảm.
	if src.EntryCount > 0 {
		if ratio := float64(len(domains)) / float64(src.EntryCount); ratio < minKeepRatio {
			cause := fmt.Errorf("%w: %d mục so với %d lần trước (%.0f%%)",
				ErrSuspiciousDrop, len(domains), src.EntryCount, ratio*100)
			s.log.Warn("giữ nguyên dữ liệu cũ", "source", src.Name, "err", cause)
			return s.fail(ctx, sourceID, "suspicious_drop", cause)
		}
	}

	if err := s.store.ReplaceEntries(ctx, sourceID, domains); err != nil {
		return err
	}
	if etag := resp.Header.Get("ETag"); etag != "" {
		if err := s.store.SetSourceETag(ctx, sourceID, etag); err != nil {
			return err
		}
	}

	s.log.Info("đã đồng bộ nguồn", "source", src.Name, "entries", len(domains))
	return nil
}

func (s *Syncer) fail(ctx context.Context, sourceID int64, status string, cause error) error {
	if err := s.store.MarkSourceError(ctx, sourceID, status, cause); err != nil {
		return err
	}
	return cause
}

// Parse đọc một blocklist và trả về danh sách domain đã chuẩn hóa.
//
// Các phép biến đổi áp dụng theo thứ tự: bỏ chú thích → cắt khoảng trắng → chuyển
// về ASCII → kiểm tra hợp lệ → khử trùng lặp.
func Parse(r io.Reader, format string) ([]string, error) {
	scanner := bufio.NewScanner(r)
	// Dòng dài bất thường vẫn có thể gặp ở định dạng adblock; nới bộ đệm để không
	// dừng giữa chừng.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	seen := make(map[string]struct{}, 1024)
	out := make([]string, 0, 1024)

	for scanner.Scan() {
		domain, ok := parseLine(scanner.Text(), format)
		if !ok {
			continue
		}
		if _, dup := seen[domain]; dup {
			continue
		}
		seen[domain] = struct{}{}
		out = append(out, domain)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read list: %w", err)
	}
	return out, nil
}

// parseLine bóc một dòng theo định dạng nguồn.
func parseLine(line, format string) (string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
		return "", false
	}

	switch format {
	case "adblock":
		// Cú pháp adblock: ||example.com^ chặn domain. Bỏ qua các luật khác vì
		// chúng nói về đường dẫn hoặc phần tử trang, không dịch được sang tầng DNS.
		if !strings.HasPrefix(line, "||") {
			return "", false
		}
		line = strings.TrimPrefix(line, "||")
		if i := strings.IndexAny(line, "^$/"); i >= 0 {
			line = line[:i]
		}

	case "plain":
		if i := strings.IndexAny(line, " \t#"); i >= 0 {
			line = line[:i]
		}

	default: // hosts
		// Bỏ chú thích cuối dòng trước khi tách trường.
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		switch len(fields) {
		case 0:
			return "", false
		case 1:
			line = fields[0]
		default:
			// "0.0.0.0 ads.example.com" — trường đầu là địa chỉ đích, bỏ đi.
			line = fields[1]
		}
	}

	domain := strings.ToLower(strings.TrimSpace(line))
	domain = strings.TrimSuffix(domain, ".")
	if !validDomain(domain) {
		return "", false
	}
	// localhost và các bản ghi vòng lặp là phần khung của file hosts, không phải mục
	// cần chặn.
	switch domain {
	case "localhost", "localhost.localdomain", "broadcasthost", "ip6-localhost",
		"ip6-loopback", "ip6-localnet", "ip6-mcastprefix", "ip6-allnodes", "ip6-allrouters":
		return "", false
	}
	return domain, true
}

func validDomain(name string) bool {
	if len(name) == 0 || len(name) > 253 || !strings.Contains(name, ".") {
		return false
	}
	if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			// Chỉ nhận ASCII: tên quốc tế hóa phải ở dạng punycode trước khi tới đây.
			ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_'
			if !ok {
				return false
			}
		}
	}
	return true
}
