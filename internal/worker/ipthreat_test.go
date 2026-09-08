package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/benji/dnsguard/internal/threat"
)

// loadThreats dựng sổ đăng ký một nguồn từ nội dung cho sẵn.
func loadThreats(t *testing.T, body string) *threat.Registry {
	t.Helper()
	path := filepath.Join(t.TempDir(), "spamhaus-drop.txt")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("ghi danh sách: %v", err)
	}
	set := threat.SpamhausDROP(path)
	if err := set.LoadTable(path); err != nil {
		t.Fatalf("LoadTable: %v", err)
	}
	return threat.NewRegistry(set)
}

func TestRunIPThreatFlagsListedAddress(t *testing.T) {
	r, db, _ := newGeoRunner(t, &stubLocator{loaded: true}, "45.66.230.7", "8.8.8.8")
	r.SetThreats(loadThreats(t, "45.66.0.0/16\n"))
	ctx := context.Background()

	if err := r.runIPThreat(ctx); err != nil {
		t.Fatalf("runIPThreat: %v", err)
	}

	matches, err := db.ThreatMatches(ctx, 0.35, 10)
	if err != nil {
		t.Fatalf("ThreatMatches: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("có %d cảnh báo, muốn 1", len(matches))
	}
	if matches[0].IP != "45.66.230.7" || matches[0].Threat != "45.66.0.0/16" {
		t.Errorf("cảnh báo = %s khớp %s; muốn 45.66.230.7 khớp 45.66.0.0/16",
			matches[0].IP, matches[0].Threat)
	}
	// Xuất xứ phải đi hết đường từ Match.Source xuống cột threat_source rồi trở lên
	// API. Thiếu khẳng định này thì cả chuỗi ghi–đọc không có gì canh.
	if matches[0].Source != "spamhaus_drop" {
		t.Errorf("nguồn cảnh báo = %q; muốn spamhaus_drop", matches[0].Source)
	}
	if matches[0].Name != "ads.example.com" {
		t.Errorf("domain = %q, muốn ads.example.com", matches[0].Name)
	}
}

// Danh sách thay đổi theo thời gian. Địa chỉ được gỡ khỏi danh sách phải tự hết cảnh
// báo ở lượt chạy sau, không cần thao tác dọn dẹp nào.
func TestRunIPThreatClearsDelistedAddress(t *testing.T) {
	r, db, _ := newGeoRunner(t, &stubLocator{loaded: true}, "45.66.230.7")
	ctx := context.Background()

	r.SetThreats(loadThreats(t, "45.66.0.0/16\n"))
	if err := r.runIPThreat(ctx); err != nil {
		t.Fatalf("runIPThreat lần đầu: %v", err)
	}
	if matches, _ := db.ThreatMatches(ctx, 0.35, 10); len(matches) != 1 {
		t.Fatalf("lần đầu có %d cảnh báo, muốn 1", len(matches))
	}

	// Danh sách mới không còn dải đó nữa.
	r.SetThreats(loadThreats(t, "203.0.113.0/24\n"))
	if err := r.runIPThreat(ctx); err != nil {
		t.Fatalf("runIPThreat lần hai: %v", err)
	}

	matches, err := db.ThreatMatches(ctx, 0.35, 10)
	if err != nil {
		t.Fatalf("ThreatMatches: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("còn %d cảnh báo sau khi gỡ khỏi danh sách, muốn 0", len(matches))
	}
}

// Chưa tải danh sách là chuyện bình thường: bảng là tuỳ chọn. Job phải im lặng bỏ qua.
func TestRunIPThreatSkipsWhenListMissing(t *testing.T) {
	r, db, _ := newGeoRunner(t, &stubLocator{loaded: true}, "45.66.230.7")
	ctx := context.Background()

	if err := r.runIPThreat(ctx); err != nil {
		t.Fatalf("runIPThreat phải bỏ qua chứ không lỗi: %v", err)
	}
	if matches, _ := db.ThreatMatches(ctx, 0.35, 10); len(matches) != 0 {
		t.Errorf("có %d cảnh báo dù chưa nạp danh sách", len(matches))
	}
}
