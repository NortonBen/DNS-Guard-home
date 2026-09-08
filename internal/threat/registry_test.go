package threat

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"
)

// buildSet ghi nội dung ra file rồi nạp vào nguồn cho sẵn.
func buildSet(t *testing.T, s *Set, name, body string) *Set {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("ghi %s: %v", name, err)
	}
	if err := s.LoadTable(path); err != nil {
		t.Fatalf("LoadTable %s: %v", name, err)
	}
	return s
}

func TestRegistryReportsMatchingSource(t *testing.T) {
	drop := buildSet(t, SpamhausDROP(""), "drop.txt", "45.66.0.0/16 ; SBL123\n")
	fox := buildSet(t, ThreatFox(""), "tf.csv",
		tfHeader+`"2026-09-08 02:00:00", "1", "203.0.113.9:443", "ip:port", "botnet_cc"`+"\n")
	reg := NewRegistry(drop, fox)

	for _, c := range []struct{ ip, source, prefix string }{
		{"45.66.230.7", "spamhaus_drop", "45.66.0.0/16"},
		{"203.0.113.9", "threatfox", "203.0.113.9/32"},
	} {
		m, ok := reg.Lookup(netip.MustParseAddr(c.ip))
		if !ok {
			t.Fatalf("%s: không khớp", c.ip)
		}
		if m.Source != c.source || m.Prefix != c.prefix {
			t.Errorf("%s khớp %s/%s; muốn %s/%s", c.ip, m.Source, m.Prefix, c.source, c.prefix)
		}
	}

	if _, ok := reg.Lookup(netip.MustParseAddr("8.8.8.8")); ok {
		t.Error("địa chỉ sạch lại khớp")
	}
}

func TestRegistryPrefersFirstSource(t *testing.T) {
	// Cùng một địa chỉ nằm trong hai danh sách: cảnh báo phải mang tên nguồn đứng
	// trước, vì thứ tự đăng ký là thứ tự tin cậy.
	drop := buildSet(t, SpamhausDROP(""), "drop.txt", "203.0.113.0/24\n")
	fox := buildSet(t, ThreatFox(""), "tf.csv",
		tfHeader+`"2026-09-08 02:00:00", "1", "203.0.113.9:443", "ip:port", "botnet_cc"`+"\n")

	m, ok := NewRegistry(drop, fox).Lookup(netip.MustParseAddr("203.0.113.9"))
	if !ok {
		t.Fatal("không khớp")
	}
	if m.Source != "spamhaus_drop" {
		t.Errorf("nguồn = %q, muốn spamhaus_drop (nguồn đăng ký trước)", m.Source)
	}
}

func TestRegistryLoadedWhenAnySourceLoaded(t *testing.T) {
	// Một nguồn chưa tải về không được làm câm những nguồn còn lại.
	drop := buildSet(t, SpamhausDROP(""), "drop.txt", "45.66.0.0/16\n")
	reg := NewRegistry(drop, ThreatFox(""))

	if !reg.Loaded() {
		t.Error("sổ đăng ký báo chưa nạp dù đã có một nguồn nạp được")
	}
	if _, ok := reg.Lookup(netip.MustParseAddr("45.66.230.7")); !ok {
		t.Error("nguồn chưa nạp lại chặn mất kết quả của nguồn đã nạp")
	}
	if got := len(reg.Statuses()); got != 2 {
		t.Errorf("báo %d trạng thái, muốn 2 — màn Cài đặt phải thấy cả nguồn chưa tải", got)
	}
}

func TestNilRegistryIsSafe(t *testing.T) {
	// Runner và Server đều có thể chưa được gán sổ đăng ký lúc test hoặc lúc khởi động.
	var reg *Registry
	if reg.Loaded() || reg.Has("spamhaus_drop") || reg.Statuses() != nil || reg.Sources() != nil {
		t.Error("sổ đăng ký nil phải im lặng báo rỗng")
	}
	if _, ok := reg.Lookup(netip.MustParseAddr("1.2.3.4")); ok {
		t.Error("sổ đăng ký nil lại khớp")
	}
}

func TestSourceKindsAreDistinct(t *testing.T) {
	// Trùng khóa thì Get trả về nhầm nguồn và endpoint cập nhật ghi đè nhầm file.
	reg := NewRegistry(SpamhausDROP("/a"), ThreatFox("/b"))
	for kind, want := range map[string]string{"spamhaus_drop": "/a", "threatfox": "/b"} {
		src, ok := reg.Get(kind)
		if !ok {
			t.Fatalf("thiếu nguồn %q", kind)
		}
		if src.Dest() != want {
			t.Errorf("%s lưu ở %q, muốn %q", kind, src.Dest(), want)
		}
	}
}

// Trùng khóa phải nổ ngay lúc dựng. Để lọt thì Lookup báo nguồn thứ nhất trong khi
// endpoint cập nhật ghi đè file của nguồn thứ hai — hỏng âm thầm, khó lần ra.
func TestNewRegistryRejectsDuplicateKind(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("nhận hai nguồn trùng khóa, muốn panic")
		}
	}()
	NewRegistry(SpamhausDROP("/a"), SpamhausDROP("/b"))
}

func TestNewRegistryRejectsNilSource(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("nhận nguồn nil, muốn panic")
		}
	}()
	NewRegistry(SpamhausDROP("/a"), nil)
}
