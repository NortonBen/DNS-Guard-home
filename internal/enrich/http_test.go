package enrich

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"
)

// Chặn địa chỉ nội bộ là biện pháp an ninh quan trọng nhất của bộ phân tích HTTP.
//
// Không có nó, một domain độc hại chỉ cần trỏ bản ghi A về 192.168.88.1 là biến
// DNSGuard thành công cụ gọi vào trang quản trị của chính router trong mạng đang
// được bảo vệ. Bỏ sót một dải là mở lại đúng lỗ hổng đó.
func TestIsPublicAddrRejectsInternalRanges(t *testing.T) {
	internal := []string{
		// Loopback
		"127.0.0.1", "127.1.2.3", "::1",
		// RFC1918 — mạng gia đình và văn phòng
		"10.0.0.1", "10.255.255.254",
		"172.16.0.1", "172.31.255.254",
		"192.168.0.1", "192.168.88.1",
		// Link-local, gồm cả endpoint metadata của máy ảo đám mây
		"169.254.169.254", "fe80::1",
		// CGNAT
		"100.64.0.1", "100.127.255.254",
		// Không xác định
		"0.0.0.0", "::",
		// Dải dành riêng và tài liệu
		"192.0.0.1", "192.0.2.1", "198.18.0.1", "198.51.100.1", "203.0.113.1",
		"240.0.0.1", "255.255.255.255",
		// IPv6 cục bộ duy nhất và tài liệu
		"fc00::1", "fd00::1", "2001:db8::1",
		// Multicast
		"224.0.0.1", "ff02::1",
		// IPv4 bọc trong IPv6 — cách né phổ biến nhất
		"::ffff:127.0.0.1", "::ffff:192.168.1.1", "::ffff:169.254.169.254",
	}

	for _, s := range internal {
		addr, err := netip.ParseAddr(s)
		if err != nil {
			t.Fatalf("địa chỉ thử nghiệm không hợp lệ %q: %v", s, err)
		}
		if isPublicAddr(addr) {
			t.Errorf("%s bị coi là công khai — đây là lỗ hổng SSRF", s)
		}
	}
}

func TestIsPublicAddrAcceptsRealInternet(t *testing.T) {
	public := []string{
		"1.1.1.1", "8.8.8.8", "93.184.216.34",
		"2606:4700:4700::1111", "2001:4860:4860::8888",
	}
	for _, s := range public {
		addr, err := netip.ParseAddr(s)
		if err != nil {
			t.Fatalf("địa chỉ thử nghiệm không hợp lệ %q: %v", s, err)
		}
		if !isPublicAddr(addr) {
			t.Errorf("%s bị chặn nhầm — địa chỉ Internet công cộng phải tải được", s)
		}
	}
}

func TestIsTrackingPixel(t *testing.T) {
	gif1x1 := append([]byte("GIF89a"), make([]byte, 30)...)
	png1x1 := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 60)...)

	tests := []struct {
		name        string
		contentType string
		body        []byte
		want        bool
	}{
		{"GIF một điểm ảnh", "image/gif", gif1x1, true},
		{"PNG một điểm ảnh", "image/png", png1x1, true},
		{"ảnh thật thì không", "image/png", append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 5000)...), false},
		{"HTML thì không", "text/html", []byte("<html>…</html>"), false},
		{"thân rỗng thì không", "image/gif", nil, false},
		{"kiểu ảnh nhưng nội dung khác", "image/gif", []byte("không phải ảnh"), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTrackingPixel(tc.contentType, tc.body); got != tc.want {
				t.Errorf("isTrackingPixel = %v, muốn %v", got, tc.want)
			}
		})
	}
}

func TestParseHTML(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantTitle   string
		wantMinText int
		wantMaxText int
	}{
		{
			name:        "trang thật có tiêu đề và nội dung",
			body:        `<html><head><title>Báo Mới</title></head><body><p>` + strings.Repeat("tin tức ", 300) + `</p></body></html>`,
			wantTitle:   "Báo Mới",
			wantMinText: 1500,
		},
		{
			// Nội dung của script không phải chữ để đọc: nếu tính cả, một trang chỉ có
			// mã quảng cáo sẽ trông như trang có nội dung.
			name:        "mã script không tính là nội dung",
			body:        `<html><head><title></title><script>` + strings.Repeat("var x=1;", 500) + `</script></head><body>xin chào</body></html>`,
			wantTitle:   "",
			wantMaxText: 100,
		},
		{
			name:        "trang trống",
			body:        `<html><body></body></html>`,
			wantTitle:   "",
			wantMaxText: 10,
		},
		{
			name:        "tiêu đề có thuộc tính",
			body:        `<html><head><title lang="vi">Trang chủ</title></head><body>nội dung</body></html>`,
			wantTitle:   "Trang chủ",
			wantMaxText: 100,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			title, textLen := parseHTML(tc.body)
			if title != tc.wantTitle {
				t.Errorf("tiêu đề = %q, muốn %q", title, tc.wantTitle)
			}
			if tc.wantMinText > 0 && textLen < tc.wantMinText {
				t.Errorf("độ dài văn bản = %d, muốn ít nhất %d", textLen, tc.wantMinText)
			}
			if tc.wantMaxText > 0 && textLen > tc.wantMaxText {
				t.Errorf("độ dài văn bản = %d, muốn tối đa %d", textLen, tc.wantMaxText)
			}
		})
	}
}

func TestMatchParking(t *testing.T) {
	if got := matchParking(`<script src="https://a.sedoparking.com/x.js"></script>`); got != "Sedo" {
		t.Errorf("matchParking = %q, muốn Sedo", got)
	}
	if got := matchParking(`<html><body>trang bình thường</body></html>`); got != "" {
		t.Errorf("matchParking = %q, muốn rỗng", got)
	}
}

// Mỗi kiểu thất bại phải quy về đúng một kết cục, vì kết cục quyết định bao lâu mới
// tra lại. Phân loại sai làm hệ thống hoặc hỏi lại quá dày, hoặc bỏ quên quá lâu.
func TestClassifyOutcome(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"thành công", nil, OutcomeOK},
		{"địa chỉ nội bộ", ErrPrivateAddress, OutcomeBlockedHost},
		{"không phân giải được", ErrNoAddress, OutcomeDNSFail},
		{"hết quota", ErrVTQuota, OutcomeQuota},
		{"VirusTotal chưa biết", ErrVTNotFound, OutcomeNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyOutcome(tc.err); got != tc.want {
				t.Errorf("ClassifyOutcome = %q, muốn %q", got, tc.want)
			}
		})
	}
}

// Kiểm tra đường đi thật với một máy chủ trên Internet.
//
// Chỉ chạy khi đặt DNSGUARD_NETWORK_TESTS=1: bộ test mặc định phải chạy được khi
// không có mạng, và CI không nên phụ thuộc vào một tên miền bên ngoài còn sống.
func TestHTTPEnricherAgainstRealSite(t *testing.T) {
	if os.Getenv("DNSGUARD_NETWORK_TESTS") != "1" {
		t.Skip("đặt DNSGUARD_NETWORK_TESTS=1 để chạy test có mạng")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	data, err := NewHTTP().Enrich(ctx, "example.com")
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	facts, ok := data.(HTTPFacts)
	if !ok {
		t.Fatalf("kiểu trả về = %T, muốn HTTPFacts", data)
	}

	if facts.Status != 200 {
		t.Errorf("status = %d, muốn 200", facts.Status)
	}
	if !facts.IsHTML {
		t.Errorf("content-type = %q, muốn text/html", facts.ContentType)
	}
	if facts.Title == "" {
		t.Error("không bóc được tiêu đề trang")
	}
	if facts.TextLen == 0 {
		t.Error("không đếm được nội dung trang")
	}
	t.Logf("example.com → status %d, tiêu đề %q, %d ký tự", facts.Status, facts.Title, facts.TextLen)
}

// Domain trỏ về địa chỉ nội bộ phải bị từ chối trước khi chạm mạng.
func TestHTTPEnricherRefusesInternalTarget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// localhost luôn phân giải về loopback trên mọi máy.
	_, err := NewHTTP().Enrich(ctx, "localhost")
	if err == nil {
		t.Fatal("tải được localhost — lỗ hổng SSRF")
	}
	if !errors.Is(err, ErrPrivateAddress) && !errors.Is(err, ErrNoAddress) {
		t.Errorf("lỗi = %v, muốn ErrPrivateAddress", err)
	}
}
