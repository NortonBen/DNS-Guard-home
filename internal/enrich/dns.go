package enrich

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/miekg/dns"
)

// DNSFacts là kết quả phân giải.
type DNSFacts struct {
	CNAMEChain []string `json:"cname_chain"`
	A          []string `json:"a"`
	FetchedAt  string   `json:"fetched_at"`
}

// DNSEnricher phân giải chuỗi CNAME và địa chỉ của domain.
//
// Đây là nguồn quan trọng nhất. CNAME cloaking là kỹ thuật né blocklist phổ biến
// nhất hiện nay, và chỉ phân giải động mới thấy được đích thật: blocklist tĩnh không
// bắt được vì tên miền nhìn hoàn toàn vô hại.
type DNSEnricher struct {
	client   *dns.Client
	servers  []string
	maxDepth int
}

// NewDNS dựng bộ phân giải. servers rỗng nghĩa là dùng 1.1.1.1 và 8.8.8.8.
func NewDNS(servers []string) *DNSEnricher {
	if len(servers) == 0 {
		servers = []string{"1.1.1.1:53", "8.8.8.8:53"}
	}
	return &DNSEnricher{
		client:   &dns.Client{Timeout: 5 * time.Second},
		servers:  servers,
		maxDepth: 10,
	}
}

func (e *DNSEnricher) Name() string { return "dns" }

// Enrich đi theo chuỗi CNAME tới đích cuối và thu địa chỉ.
func (e *DNSEnricher) Enrich(ctx context.Context, domain string) (any, error) {
	facts := DNSFacts{FetchedAt: time.Now().UTC().Format(time.RFC3339)}

	current := dns.Fqdn(domain)
	seen := map[string]bool{current: true}

	// Đi từng bước thay vì tin vào chuỗi trong một câu trả lời: một số resolver rút
	// gọn chuỗi, và mắt xích ở giữa mới là thứ tiết lộ hạ tầng adtech.
	for depth := 0; depth < e.maxDepth; depth++ {
		answer, err := e.query(ctx, current, dns.TypeA)
		if err != nil {
			if depth == 0 {
				return nil, err
			}
			break
		}

		var next string
		for _, rr := range answer {
			switch v := rr.(type) {
			case *dns.CNAME:
				next = v.Target
			case *dns.A:
				facts.A = append(facts.A, v.A.String())
			case *dns.AAAA:
				facts.A = append(facts.A, v.AAAA.String())
			}
		}
		if next == "" || seen[next] {
			break
		}
		seen[next] = true
		facts.CNAMEChain = append(facts.CNAMEChain, strings.TrimSuffix(next, "."))
		current = next
	}

	return facts, nil
}

// query gửi một truy vấn, thử lần lượt các máy chủ đã cấu hình.
func (e *DNSEnricher) query(ctx context.Context, name string, qtype uint16) ([]dns.RR, error) {
	msg := new(dns.Msg)
	msg.SetQuestion(name, qtype)
	msg.RecursionDesired = true

	var lastErr error
	for _, server := range e.servers {
		resp, _, err := e.client.ExchangeContext(ctx, msg, server)
		if err != nil {
			lastErr = err
			continue
		}
		switch resp.Rcode {
		case dns.RcodeSuccess:
			return resp.Answer, nil
		case dns.RcodeNameError:
			// NXDOMAIN là câu trả lời hợp lệ, không phải lỗi mạng: domain không tồn
			// tại nữa. Trả về rỗng để kết quả được cache thay vì thử lại mãi.
			return nil, nil
		default:
			lastErr = fmt.Errorf("rcode %s từ %s", dns.RcodeToString[resp.Rcode], server)
		}
	}
	if lastErr == nil {
		lastErr = net.ErrClosed
	}
	return nil, fmt.Errorf("phân giải %q: %w", name, lastErr)
}
