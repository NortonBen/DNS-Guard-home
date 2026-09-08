package threat

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tfHeader là phần đầu file thật của ThreatFox: bảy dòng khung và một dòng tên cột,
// tất cả đều bắt đầu bằng "#".
const tfHeader = `################################################################
# ThreatFox IOCs: recent ip-port - CSV format                  #
# Last updated: 2026-09-08 02:28:59 UTC                        #
################################################################
#
# "first_seen_utc","ioc_id","ioc_value","ioc_type","threat_type"
`

func loadThreatFox(t *testing.T, body string) *Set {
	t.Helper()
	path := filepath.Join(t.TempDir(), "threatfox.csv")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("ghi file: %v", err)
	}
	s := ThreatFox(path)
	if err := s.LoadTable(path); err != nil {
		t.Fatalf("LoadTable: %v", err)
	}
	return s
}

func TestThreatFoxParsesIOCValue(t *testing.T) {
	s := loadThreatFox(t, tfHeader+
		`"2026-09-08 02:28:59", "1904097", "26.150.54.213:6606", "ip:port", "botnet_cc"`+"\n"+
		`"2026-09-07 11:00:00", "1904000", "203.0.113.9:443", "ip:port", "botnet_cc"`+"\n")

	if got := s.Status().Entries; got != 2 {
		t.Fatalf("nạp %d mục, muốn 2", got)
	}
	// Cổng phải bị bỏ: bản ghi DNS không mang cổng, giữ lại thì không mục nào khớp.
	m, ok := s.Lookup(netip.MustParseAddr("26.150.54.213"))
	if !ok {
		t.Fatal("không khớp địa chỉ đã liệt kê")
	}
	if m.Prefix != "26.150.54.213/32" {
		t.Errorf("dải khớp = %q, muốn 26.150.54.213/32", m.Prefix)
	}
	if m.Source != "threatfox" {
		t.Errorf("nguồn = %q, muốn threatfox", m.Source)
	}
}

func TestThreatFoxRejectsHeaderOnly(t *testing.T) {
	// Chỉ có tiêu đề nghĩa là tải về hỏng. Phải báo lỗi để RefreshTable giữ nguyên
	// danh sách đang dùng thay vì ghi đè bằng file rỗng nội dung.
	path := filepath.Join(t.TempDir(), "threatfox.csv")
	if err := os.WriteFile(path, []byte(tfHeader), 0o644); err != nil {
		t.Fatalf("ghi file: %v", err)
	}
	if err := ThreatFox(path).LoadTable(path); err == nil {
		t.Fatal("nhận file chỉ có tiêu đề, muốn báo lỗi")
	}
}

func TestThreatFoxIgnoresNonIPRows(t *testing.T) {
	// ThreatFox cũng phát hành IOC là tên miền và URL. Cùng khung CSV, nhưng cột giá
	// trị không phải địa chỉ — phải bỏ qua chứ không được hỏng cả file.
	s := loadThreatFox(t, tfHeader+
		`"2026-09-08 02:00:00", "1", "evil.example.com", "domain", "botnet_cc"`+"\n"+
		`"2026-09-08 02:00:01", "2", "198.51.100.4:80", "ip:port", "botnet_cc"`+"\n")

	if got := s.Status().Entries; got != 1 {
		t.Fatalf("nạp %d mục, muốn 1", got)
	}
	if _, ok := s.Lookup(netip.MustParseAddr("198.51.100.4")); !ok {
		t.Error("bỏ sót địa chỉ hợp lệ đứng sau một dòng không phải địa chỉ")
	}
}

func TestStripPort(t *testing.T) {
	cases := []struct{ in, want string }{
		{"1.2.3.4:8080", "1.2.3.4"},
		{"1.2.3.4", "1.2.3.4"},
		{"[2001:db8::1]:443", "2001:db8::1"},
		{"2001:db8::1", "2001:db8::1"},
	}
	for _, c := range cases {
		if got := stripPort(c.in); got != c.want {
			t.Errorf("stripPort(%q) = %q, muốn %q", c.in, got, c.want)
		}
	}
}

// Bản xuất thật của ThreatFox dùng CRLF. Parser sống sót nhờ bufio.ScanLines cắt "\r",
// chứ không nhờ chỗ nào khẳng định điều đó — nên khẳng định ở đây.
func TestThreatFoxParsesCRLF(t *testing.T) {
	body := strings.ReplaceAll(tfHeader+
		`"2026-09-08 02:00:00", "1", "198.51.100.4:80", "ip:port", "botnet_cc"`+"\n",
		"\n", "\r\n")

	s := loadThreatFox(t, body)
	if got := s.Status().Entries; got != 1 {
		t.Fatalf("nạp %d mục từ file CRLF, muốn 1", got)
	}
	if _, ok := s.Lookup(netip.MustParseAddr("198.51.100.4")); !ok {
		t.Error("không khớp địa chỉ trong file CRLF")
	}
}

func TestThreatFoxParsesIPv6(t *testing.T) {
	s := loadThreatFox(t, tfHeader+
		`"2026-09-08 02:00:00", "1", "[2001:db8::1]:443", "ip:port", "botnet_cc"`+"\n")

	if _, ok := s.Lookup(netip.MustParseAddr("2001:db8::1")); !ok {
		t.Error("không khớp địa chỉ IPv6 đã liệt kê")
	}
}
