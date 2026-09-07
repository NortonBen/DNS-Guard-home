package threat

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"
)

func writeList(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ipthreat.txt")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("ghi file danh sách: %v", err)
	}
	return path
}

func loadSet(t *testing.T, body string) *Set {
	t.Helper()
	s := New()
	if err := s.LoadTable(writeList(t, body)); err != nil {
		t.Fatalf("LoadTable: %v", err)
	}
	return s
}

func TestLookup(t *testing.T) {
	// Trộn hai định dạng thật: abuse.ch dùng địa chỉ trần và chú thích "#",
	// Spamhaus DROP dùng CIDR và chú thích ";".
	s := loadSet(t, `# abuse.ch Feodo Tracker
1.2.3.4
2606:4700::1111

45.66.0.0/16 ; SBL123
45.66.230.7/32 ; mục hẹp nằm trong dải rộng phía trên
`)

	tests := []struct {
		ip         string
		wantPrefix string
	}{
		{"1.2.3.4", "1.2.3.4/32"},
		{"2606:4700::1111", "2606:4700::1111/128"},
		{"45.66.9.9", "45.66.0.0/16"},
		// Dải lồng nhau: phải ra dải hẹp nhất, tức đúng dòng người vận hành sẽ tìm thấy.
		{"45.66.230.7", "45.66.230.7/32"},
	}
	for _, tc := range tests {
		t.Run(tc.ip, func(t *testing.T) {
			m, ok := s.Lookup(netip.MustParseAddr(tc.ip))
			if !ok {
				t.Fatalf("%s không khớp, muốn %s", tc.ip, tc.wantPrefix)
			}
			if m.Prefix != tc.wantPrefix {
				t.Errorf("khớp %s, muốn %s", m.Prefix, tc.wantPrefix)
			}
		})
	}

	for _, clean := range []string{"8.8.8.8", "1.2.3.5", "45.67.0.1", "2606:4700::2222"} {
		if m, ok := s.Lookup(netip.MustParseAddr(clean)); ok {
			t.Errorf("%s bị đánh dấu độc hại (khớp %s)", clean, m.Prefix)
		}
	}
}

// Một dòng hỏng không được biến cả Internet thành độc hại.
func TestLoadTableRejectsOverbroadRanges(t *testing.T) {
	s := loadSet(t, `0.0.0.0/0
10.0.0.0/7
::/0
1.2.3.4
`)
	for _, ip := range []string{"8.8.8.8", "10.0.0.1", "2606:4700::1"} {
		if m, ok := s.Lookup(netip.MustParseAddr(ip)); ok {
			t.Errorf("%s khớp %s — dải quá rộng lẽ ra phải bị loại", ip, m.Prefix)
		}
	}
	// Dòng hợp lệ trong cùng file vẫn phải nạp được.
	if _, ok := s.Lookup(netip.MustParseAddr("1.2.3.4")); !ok {
		t.Error("mục hợp lệ bị mất khi cùng file có dòng quá rộng")
	}
}

// Địa chỉ trong dải viết không chuẩn phải được che lại, nếu không nó tạo ra khóa mà
// Lookup không bao giờ dựng lại được.
func TestLoadTableNormalisesUnmaskedPrefix(t *testing.T) {
	s := loadSet(t, "45.66.230.7/16\n")
	m, ok := s.Lookup(netip.MustParseAddr("45.66.0.1"))
	if !ok {
		t.Fatal("dải viết không chuẩn không khớp gì cả")
	}
	if m.Prefix != "45.66.0.0/16" {
		t.Errorf("khớp %s, muốn 45.66.0.0/16", m.Prefix)
	}
}

// Tải về được một trang lỗi HTML là chuyện thường. Nạp nó phải hỏng chứ không được
// thay bảng đang dùng bằng danh sách rỗng.
func TestLoadTableRejectsEmptyList(t *testing.T) {
	s := New()
	err := s.LoadTable(writeList(t, "<html><body>404 Not Found</body></html>\n"))
	if err == nil {
		t.Fatal("nạp trang HTML lại thành công")
	}
	if s.Loaded() {
		t.Error("Set báo đã nạp sau khi nạp hỏng")
	}
}

// Chưa nạp thì không khớp gì, và không được sập.
func TestLookupOnEmptySet(t *testing.T) {
	if _, ok := New().Lookup(netip.MustParseAddr("1.2.3.4")); ok {
		t.Error("Set rỗng lại khớp")
	}
}

func TestStatusReportsEntries(t *testing.T) {
	s := loadSet(t, "1.2.3.4\n5.6.7.0/24\n")
	st := s.Status()
	if !st.Loaded || st.Entries != 2 {
		t.Errorf("trạng thái = loaded %v, %d mục; muốn true, 2", st.Loaded, st.Entries)
	}
	if st.Kind != "ipthreat" || st.DefaultURL == "" {
		t.Errorf("trạng thái thiếu kind hoặc URL mặc định: %+v", st)
	}
}
