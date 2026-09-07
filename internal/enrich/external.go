package enrich

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxExternalBody chặn trên kích thước phản hồi từ dịch vụ ngoài.
const maxExternalBody = 8 << 20

// CertFacts là kết quả tra chứng chỉ.
type CertFacts struct {
	Issuer   string   `json:"issuer"`
	SANs     []string `json:"sans"`
	NotAfter string   `json:"not_after"`
}

// CertEnricher tra crt.sh để tìm domain anh em cùng chứng chỉ.
//
// Chỉ gửi tên miền ra ngoài, không bao giờ gửi IP client: log truy vấn chứa dữ liệu
// nhạy cảm về hành vi người dùng, và tên miền là mức tối thiểu cần thiết để tra cứu.
type CertEnricher struct {
	client *http.Client
}

// NewCert dựng bộ tra chứng chỉ.
func NewCert() *CertEnricher {
	return &CertEnricher{client: &http.Client{Timeout: 30 * time.Second}}
}

func (e *CertEnricher) Name() string { return "cert" }

func (e *CertEnricher) Enrich(ctx context.Context, domain string) (any, error) {
	endpoint := "https://crt.sh/?q=" + url.QueryEscape(domain) + "&output=json"

	body, err := fetchJSON(ctx, e.client, endpoint)
	if err != nil {
		return nil, err
	}

	var entries []struct {
		NameValue  string `json:"name_value"`
		IssuerName string `json:"issuer_name"`
		NotAfter   string `json:"not_after"`
	}
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("giải mã phản hồi crt.sh: %w", err)
	}
	if len(entries) == 0 {
		return CertFacts{}, nil
	}

	facts := CertFacts{Issuer: entries[0].IssuerName, NotAfter: entries[0].NotAfter}
	seen := map[string]bool{}
	for _, entry := range entries {
		for _, san := range strings.Split(entry.NameValue, "\n") {
			san = strings.ToLower(strings.TrimSpace(san))
			if san == "" || seen[san] {
				continue
			}
			seen[san] = true
			facts.SANs = append(facts.SANs, san)
			// Chứng chỉ wildcard của một CDN lớn có thể liệt kê hàng nghìn tên; cắt
			// ở mức đủ để tìm anh em mà không phình dữ liệu.
			if len(facts.SANs) >= 100 {
				return facts, nil
			}
		}
	}
	return facts, nil
}

// RDAPFacts là kết quả tra tuổi domain.
type RDAPFacts struct {
	RegisteredAt string `json:"registered_at"`
	Registrar    string `json:"registrar"`
	AgeDays      int    `json:"age_days"`
}

// RDAPEnricher tra tuổi domain qua RDAP.
type RDAPEnricher struct {
	client *http.Client
}

// NewRDAP dựng bộ tra RDAP.
func NewRDAP() *RDAPEnricher {
	return &RDAPEnricher{client: &http.Client{Timeout: 20 * time.Second}}
}

func (e *RDAPEnricher) Name() string { return "rdap" }

func (e *RDAPEnricher) Enrich(ctx context.Context, domain string) (any, error) {
	body, err := fetchJSON(ctx, e.client, "https://rdap.org/domain/"+url.PathEscape(domain))
	if err != nil {
		return nil, err
	}

	var doc struct {
		Events []struct {
			Action string `json:"eventAction"`
			Date   string `json:"eventDate"`
		} `json:"events"`
		Entities []struct {
			Roles      []string `json:"roles"`
			VCardArray []any    `json:"vcardArray"`
		} `json:"entities"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("giải mã phản hồi RDAP: %w", err)
	}

	var facts RDAPFacts
	for _, ev := range doc.Events {
		if ev.Action == "registration" {
			facts.RegisteredAt = ev.Date
			if t, err := time.Parse(time.RFC3339, ev.Date); err == nil {
				facts.AgeDays = int(time.Since(t).Hours() / 24)
			}
			break
		}
	}
	return facts, nil
}

// fetchJSON gọi một endpoint và trả về thân phản hồi.
func fetchJSON(ctx context.Context, client *http.Client, endpoint string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("dựng request %q: %w", endpoint, err)
	}
	req.Header.Set("User-Agent", "DNSGuard/1.0")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gọi %q: %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// Không tìm thấy là câu trả lời hợp lệ, không phải lỗi dịch vụ: đừng để nó
		// làm circuit breaker mở ra một cách oan uổng.
		return []byte("[]"), nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gọi %q: HTTP %d", endpoint, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxExternalBody))
}
