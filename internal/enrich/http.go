package enrich

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

// HTTPFacts là kết quả phân tích một lần tải trang gốc.
type HTTPFacts struct {
	Status      int    `json:"status"`
	Scheme      string `json:"scheme"`
	ContentType string `json:"content_type,omitempty"`
	BodyLen     int    `json:"body_len"`

	// RedirectTo là đích cuối nếu có chuyển hướng sang host khác.
	RedirectTo string `json:"redirect_to,omitempty"`

	// Header đáng quan tâm, giữ nguyên giá trị để làm bằng chứng.
	P3P            string `json:"p3p,omitempty"`
	CORS           string `json:"cors,omitempty"`
	TrackingCookie string `json:"tracking_cookie,omitempty"`
	CookieMaxDays  int    `json:"cookie_max_days,omitempty"`

	// Kết quả bóc HTML.
	Title     string `json:"title,omitempty"`
	TextLen   int    `json:"text_len"`
	Parking   string `json:"parking,omitempty"`
	IsHTML    bool   `json:"is_html"`
	IsPixel   bool   `json:"is_pixel"`
	FetchedAt string `json:"fetched_at"`
}

// Lỗi phân loại được, để tầng trên chọn TTL phù hợp cho từng kết cục.
var (
	ErrPrivateAddress = errors.New("domain phân giải về địa chỉ nội bộ")
	ErrNoAddress      = errors.New("không phân giải được địa chỉ")
)

const (
	httpMaxBody      = 256 << 10
	httpTimeout      = 8 * time.Second
	httpMaxRedirects = 3
)

// HTTPEnricher tải trang gốc của domain và phân tích header cùng HTML tĩnh.
//
// Không chạy JavaScript, không tải tài nguyên con, không đi quá trang gốc. Mục tiêu
// là trả lời "domain này là cái gì", không phải "trang này trông ra sao".
type HTTPEnricher struct {
	client *http.Client
}

// NewHTTP dựng bộ phân tích.
func NewHTTP() *HTTPEnricher {
	// DialContext tự chặn địa chỉ nội bộ: kiểm tra ở tầng quay số bắt được cả trường
	// hợp chuyển hướng sang một host khác lại trỏ về mạng nội bộ, thứ mà kiểm tra
	// một lần trước khi gọi sẽ bỏ sót.
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if ip, err := netip.ParseAddr(host); err == nil && !isPublicAddr(ip) {
				return nil, fmt.Errorf("%w: %s", ErrPrivateAddress, host)
			}
			return dialer.DialContext(ctx, network, addr)
		},
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
		DisableKeepAlives:     true,
		MaxIdleConns:          4,
	}

	return &HTTPEnricher{
		client: &http.Client{
			Transport: transport,
			Timeout:   httpTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= httpMaxRedirects {
					return http.ErrUseLastResponse
				}
				return nil
			},
			// Không giữ cookie: không tạo phiên với máy chủ đích.
			Jar: nil,
		},
	}
}

func (e *HTTPEnricher) Name() string { return "http" }

// Enrich tải trang gốc và trả về kết quả phân tích.
func (e *HTTPEnricher) Enrich(ctx context.Context, domain string) (any, error) {
	// Chặn địa chỉ nội bộ trước khi chạm mạng. Không có bước này, một domain độc hại
	// chỉ cần trỏ bản ghi A về 192.168.88.1 là biến DNSGuard thành công cụ gọi vào
	// trang quản trị của chính router trong mạng.
	if err := assertPublicHost(ctx, domain); err != nil {
		return nil, err
	}

	facts, err := e.fetch(ctx, "https://"+domain+"/")
	if err == nil {
		return facts, nil
	}
	// Lỗi an toàn không thử lại bằng giao thức khác.
	if errors.Is(err, ErrPrivateAddress) {
		return nil, err
	}

	// Nhiều domain hạ tầng quảng cáo chỉ phục vụ HTTP. Thử lại một lần, không nhiều hơn.
	facts, httpErr := e.fetch(ctx, "http://"+domain+"/")
	if httpErr != nil {
		return nil, err // trả lỗi HTTPS: nó mô tả đúng nguyên nhân hơn
	}
	return facts, nil
}

func (e *HTTPEnricher) fetch(ctx context.Context, url string) (HTTPFacts, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return HTTPFacts{}, fmt.Errorf("dựng request %q: %w", url, err)
	}
	// User-Agent trung thực: không giả làm trình duyệt. Đổi lại một số máy chủ trả
	// nội dung khác, nhưng giả mạo để moi dữ liệu là việc không nên làm.
	req.Header.Set("User-Agent", "DNSGuard/1.0 (+phan-tich-blocklist)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.5")
	req.Header.Set("Accept-Encoding", "identity")

	resp, err := e.client.Do(req)
	if err != nil {
		return HTTPFacts{}, err
	}
	defer resp.Body.Close()

	facts := HTTPFacts{
		Status:    resp.StatusCode,
		Scheme:    strings.SplitN(url, ":", 2)[0],
		FetchedAt: time.Now().UTC().Format(time.RFC3339),
	}

	facts.ContentType = strings.ToLower(strings.TrimSpace(
		strings.SplitN(resp.Header.Get("Content-Type"), ";", 2)[0]))
	facts.IsHTML = strings.HasPrefix(facts.ContentType, "text/html")
	facts.P3P = resp.Header.Get("P3P")
	facts.CORS = resp.Header.Get("Access-Control-Allow-Origin")

	// Chuyển hướng sang host khác là bằng chứng: nó cho thấy domain này thật ra
	// thuộc về ai.
	if loc := resp.Header.Get("Location"); loc != "" && resp.StatusCode >= 300 && resp.StatusCode < 400 {
		facts.RedirectTo = loc
	} else if resp.Request != nil && resp.Request.URL != nil && resp.Request.URL.String() != url {
		facts.RedirectTo = resp.Request.URL.String()
	}

	facts.TrackingCookie, facts.CookieMaxDays = trackingCookie(resp)

	body, err := io.ReadAll(io.LimitReader(resp.Body, httpMaxBody))
	if err != nil {
		return facts, fmt.Errorf("đọc thân phản hồi: %w", err)
	}
	facts.BodyLen = len(body)
	facts.IsPixel = isTrackingPixel(facts.ContentType, body)

	if facts.IsHTML {
		facts.Title, facts.TextLen = parseHTML(string(body))
		facts.Parking = matchParking(string(body))
	}
	return facts, nil
}

// trackingCookie tìm cookie được thiết kế để dùng xuyên trang và sống lâu.
//
// SameSite=None nghĩa là cookie cố ý gửi kèm trong ngữ cảnh bên thứ ba; cộng với
// thời hạn dài thì đó là một định danh theo dõi, không phải cookie phiên.
func trackingCookie(resp *http.Response) (name string, maxDays int) {
	for _, c := range resp.Cookies() {
		if c.SameSite != http.SameSiteNoneMode || !c.Secure {
			continue
		}
		days := 0
		switch {
		case c.MaxAge > 0:
			days = c.MaxAge / 86400
		case !c.Expires.IsZero():
			days = int(time.Until(c.Expires).Hours() / 24)
		}
		if days >= 90 {
			return c.Name, days
		}
	}
	return "", 0
}

// Chữ ký nhị phân của ảnh một điểm ảnh, thứ mà máy chủ thu thập trả về để trình
// duyệt coi như đã tải xong.
var (
	gifHeader = []byte("GIF8")
	pngHeader = []byte("\x89PNG\r\n\x1a\n")
)

// isTrackingPixel nhận ra ảnh bé xíu dùng làm điểm thu thập.
func isTrackingPixel(contentType string, body []byte) bool {
	if !strings.HasPrefix(contentType, "image/") {
		return false
	}
	// Một điểm ảnh GIF khoảng 35–43 byte, PNG khoảng 67–95. Trên 100 byte thì đã là
	// ảnh thật, dù nhỏ.
	if len(body) == 0 || len(body) > 100 {
		return false
	}
	return strings.HasPrefix(string(body), string(gifHeader)) ||
		strings.HasPrefix(string(body), string(pngHeader))
}

// parkingProviders là dấu vân tay của các nhà cung cấp trang đỗ tên miền.
var parkingProviders = map[string]string{
	"sedoparking.com": "Sedo",
	"parkingcrew.net": "ParkingCrew",
	"bodis.com":       "Bodis",
	"afternic.com":    "Afternic",
	"dan.com":         "Dan.com",
	"above.com":       "Above",
	"uniregistry.com": "Uniregistry",
	"cashparking.com": "GoDaddy CashParking",
	"parkingpage":     "parking chung",
}

func matchParking(body string) string {
	lower := strings.ToLower(body)
	for fingerprint, provider := range parkingProviders {
		if strings.Contains(lower, fingerprint) {
			return provider
		}
	}
	return ""
}

// parseHTML lấy tiêu đề và độ dài văn bản hiển thị, không dùng thư viện phân tích.
//
// Không cần cây DOM đầy đủ: chỉ cần biết trang có tiêu đề không và có bao nhiêu chữ
// để đọc. Bóc thô bằng chuỗi rẻ hơn nhiều và không thêm phụ thuộc nào.
func parseHTML(body string) (title string, textLen int) {
	lower := strings.ToLower(body)

	if start := strings.Index(lower, "<title"); start >= 0 {
		if open := strings.IndexByte(body[start:], '>'); open >= 0 {
			from := start + open + 1
			if end := strings.Index(lower[from:], "</title>"); end >= 0 {
				title = strings.TrimSpace(body[from : from+end])
			}
		}
	}

	// Bỏ script và style trước khi đếm: nội dung của chúng không phải chữ để đọc, và
	// một trang chỉ có mã quảng cáo sẽ trông "dài" nếu tính cả chúng.
	stripped := removeElement(removeElement(body, "script"), "style")

	var text strings.Builder
	inTag := false
	for _, r := range stripped {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			text.WriteRune(r)
		}
	}
	return title, len(strings.Join(strings.Fields(text.String()), " "))
}

// removeElement cắt bỏ toàn bộ phần tử cùng nội dung của nó.
func removeElement(body, tag string) string {
	lower := strings.ToLower(body)
	open, close := "<"+tag, "</"+tag+">"

	var out strings.Builder
	for {
		start := strings.Index(lower, open)
		if start < 0 {
			out.WriteString(body)
			return out.String()
		}
		end := strings.Index(lower[start:], close)
		if end < 0 {
			out.WriteString(body[:start])
			return out.String()
		}
		out.WriteString(body[:start])
		cut := start + end + len(close)
		body, lower = body[cut:], lower[cut:]
	}
}

// assertPublicHost phân giải domain và từ chối nếu mọi địa chỉ đều không công khai.
func assertPublicHost(ctx context.Context, domain string) error {
	resolver := &net.Resolver{}
	addrs, err := resolver.LookupNetIP(ctx, "ip", domain)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNoAddress, err)
	}
	for _, addr := range addrs {
		if isPublicAddr(addr) {
			return nil
		}
	}
	return fmt.Errorf("%w: %v", ErrPrivateAddress, addrs)
}

// isPublicAddr cho biết địa chỉ có nằm ngoài mọi dải nội bộ không.
//
// Danh sách phải đầy đủ: bỏ sót một dải là mở đường cho domain độc hại trỏ vào thiết
// bị trong chính mạng đang được bảo vệ.
func isPublicAddr(ip netip.Addr) bool {
	ip = ip.Unmap()

	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() ||
		ip.IsUnspecified() {
		return false
	}

	// Các dải mà thư viện chuẩn không xếp vào IsPrivate nhưng vẫn không phải Internet
	// công cộng.
	for _, cidr := range nonPublicRanges {
		if cidr.Contains(ip) {
			return false
		}
	}
	return true
}

var nonPublicRanges = func() []netip.Prefix {
	raw := []string{
		"100.64.0.0/10",   // CGNAT — thường là mạng của nhà mạng
		"192.0.0.0/24",    // giao thức IETF
		"192.0.2.0/24",    // tài liệu
		"198.18.0.0/15",   // đo kiểm
		"198.51.100.0/24", // tài liệu
		"203.0.113.0/24",  // tài liệu
		"240.0.0.0/4",     // dành riêng
		"::/128",          // không xác định
		"64:ff9b::/96",    // NAT64
		"100::/64",        // hố đen
		"2001:db8::/32",   // tài liệu
		"fc00::/7",        // địa chỉ cục bộ duy nhất
	}
	out := make([]netip.Prefix, 0, len(raw))
	for _, s := range raw {
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p)
		}
	}
	return out
}()
