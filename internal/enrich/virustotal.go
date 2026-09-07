package enrich

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"
)

// VTFacts là kết quả tra VirusTotal.
type VTFacts struct {
	Malicious  int `json:"malicious"`
	Suspicious int `json:"suspicious"`
	Harmless   int `json:"harmless"`
	Undetected int `json:"undetected"`
	Reputation int `json:"reputation"`
	// Known bằng false nghĩa là VirusTotal chưa từng thấy domain này.
	Known     bool   `json:"known"`
	FetchedAt string `json:"fetched_at"`
}

// Lỗi phân loại được để tầng trên chọn TTL.
var (
	ErrVTQuota    = errors.New("VirusTotal hết quota")
	ErrVTNotFound = errors.New("VirusTotal chưa biết domain này")
	ErrVTNoAPIKey = errors.New("chưa cấu hình khóa API VirusTotal")
	ErrVTBadKey   = errors.New("VirusTotal từ chối khóa API")
)

// VTEnricher tra cứu VirusTotal để xác thực thêm cho domain đã đáng ngờ.
//
// Không dùng cho mọi domain: bậc miễn phí chỉ khoảng 500 lượt mỗi ngày, và phần lớn
// domain trong một mạng gia đình không cần tới nó. Cổng lọc theo điểm nằm ở tầng
// worker, không ở đây.
type VTEnricher struct {
	client *http.Client
	// baseURL tách ra để test chỉ được vào một máy chủ giả. Mã sản xuất không bao giờ
	// đổi nó — VirusTotal chỉ có một endpoint.
	baseURL string
	// apiKey đổi được lúc chạy khi người quản trị nhập khóa mới trên giao diện, trong
	// khi worker có thể đang tra cứu ở luồng khác — nên phải là con trỏ nguyên tử.
	apiKey atomic.Pointer[string]
}

// NewVirusTotal dựng bộ tra cứu. apiKey rỗng thì nguồn tự báo chưa sẵn sàng.
func NewVirusTotal(apiKey string) *VTEnricher {
	e := &VTEnricher{
		client:  &http.Client{Timeout: 20 * time.Second},
		baseURL: "https://www.virustotal.com/api/v3/domains/",
	}
	e.SetAPIKey(apiKey)
	return e
}

func (e *VTEnricher) Name() string { return "vt" }

// SetAPIKey thay khóa API ngay lúc chạy. Chuỗi rỗng nghĩa là gỡ khóa.
func (e *VTEnricher) SetAPIKey(key string) {
	e.apiKey.Store(&key)
}

func (e *VTEnricher) key() string {
	if p := e.apiKey.Load(); p != nil {
		return *p
	}
	return ""
}

// Configured cho biết đã có khóa API chưa.
func (e *VTEnricher) Configured() bool { return e.key() != "" }

// KeyHint trả về vài ký tự cuối của khóa để người quản trị nhận ra mình đang dùng
// khóa nào, mà không lộ đủ để dùng lại.
//
// Bốn ký tự cuối trên tổng sáu mươi tư ký tự hex không thu hẹp không gian tìm kiếm
// đến mức có ý nghĩa, nhưng đủ để phân biệt hai khóa khi cần đổi.
func (e *VTEnricher) KeyHint() string {
	key := e.key()
	if len(key) < 8 {
		return ""
	}
	return key[len(key)-4:]
}

// VerifyKey thử một lượt gọi thật để biết khóa có dùng được không.
//
// Lưu một khóa gõ sai mà không kiểm tra nghĩa là nguồn im lặng hỏng: mọi lượt tra
// đều trượt, circuit breaker mở ra, và không ai biết cho tới khi đọc log.
func (e *VTEnricher) VerifyKey(ctx context.Context, key string) error {
	if key == "" {
		return ErrVTNoAPIKey
	}

	// Tra một domain chắc chắn tồn tại trong cơ sở dữ liệu VirusTotal: mục đích ở đây
	// là kiểm tra khóa, không phải kiểm tra domain.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.baseURL+"google.com", nil)
	if err != nil {
		return fmt.Errorf("dựng request kiểm tra khóa: %w", err)
	}
	req.Header.Set("x-apikey", key)
	req.Header.Set("Accept", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("gọi VirusTotal: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	switch resp.StatusCode {
	case http.StatusOK, http.StatusNotFound:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrVTBadKey
	case http.StatusTooManyRequests:
		// Hết quota nghĩa là khóa đúng — VirusTotal đã nhận ra nó mới đếm được quota.
		return nil
	default:
		return fmt.Errorf("VirusTotal trả HTTP %d", resp.StatusCode)
	}
}

func (e *VTEnricher) Enrich(ctx context.Context, domain string) (any, error) {
	key := e.key()
	if key == "" {
		return nil, ErrVTNoAPIKey
	}

	endpoint := e.baseURL + url.PathEscape(domain)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("dựng request VirusTotal: %w", err)
	}
	req.Header.Set("x-apikey", key)
	req.Header.Set("Accept", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gọi VirusTotal: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// tiếp tục bên dưới
	case http.StatusNotFound:
		// VirusTotal chưa từng thấy domain. Đây là câu trả lời hợp lệ, không phải lỗi
		// dịch vụ — nếu coi là lỗi thì circuit breaker sẽ mở ra một cách oan uổng.
		return VTFacts{Known: false, FetchedAt: time.Now().UTC().Format(time.RFC3339)}, nil
	case http.StatusTooManyRequests:
		return nil, ErrVTQuota
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, fmt.Errorf("VirusTotal từ chối khóa API (HTTP %d)", resp.StatusCode)
	default:
		return nil, fmt.Errorf("VirusTotal trả HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("đọc phản hồi VirusTotal: %w", err)
	}

	var doc struct {
		Data struct {
			Attributes struct {
				Reputation        int `json:"reputation"`
				LastAnalysisStats struct {
					Malicious  int `json:"malicious"`
					Suspicious int `json:"suspicious"`
					Harmless   int `json:"harmless"`
					Undetected int `json:"undetected"`
				} `json:"last_analysis_stats"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("giải mã phản hồi VirusTotal: %w", err)
	}

	stats := doc.Data.Attributes.LastAnalysisStats
	return VTFacts{
		Malicious:  stats.Malicious,
		Suspicious: stats.Suspicious,
		Harmless:   stats.Harmless,
		Undetected: stats.Undetected,
		Reputation: doc.Data.Attributes.Reputation,
		Known:      true,
		FetchedAt:  time.Now().UTC().Format(time.RFC3339),
	}, nil
}
