package enrich

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
)

// VTEnricher tra cứu VirusTotal để xác thực thêm cho domain đã đáng ngờ.
//
// Không dùng cho mọi domain: bậc miễn phí chỉ khoảng 500 lượt mỗi ngày, và phần lớn
// domain trong một mạng gia đình không cần tới nó. Cổng lọc theo điểm nằm ở tầng
// worker, không ở đây.
type VTEnricher struct {
	client *http.Client
	apiKey string
}

// NewVirusTotal dựng bộ tra cứu. apiKey rỗng thì nguồn tự báo chưa sẵn sàng.
func NewVirusTotal(apiKey string) *VTEnricher {
	return &VTEnricher{
		client: &http.Client{Timeout: 20 * time.Second},
		apiKey: apiKey,
	}
}

func (e *VTEnricher) Name() string { return "vt" }

// Configured cho biết đã có khóa API chưa.
func (e *VTEnricher) Configured() bool { return e.apiKey != "" }

func (e *VTEnricher) Enrich(ctx context.Context, domain string) (any, error) {
	if e.apiKey == "" {
		return nil, ErrVTNoAPIKey
	}

	endpoint := "https://www.virustotal.com/api/v3/domains/" + url.PathEscape(domain)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("dựng request VirusTotal: %w", err)
	}
	req.Header.Set("x-apikey", e.apiKey)
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
