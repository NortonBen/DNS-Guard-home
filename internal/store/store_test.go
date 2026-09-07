package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestStore mở một CSDL tạm đã chạy migration. Test dùng file thật chứ không
// mock: mock CSDL chỉ chứng minh được rằng mock hoạt động.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"), true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMigrateSeedsCategories(t *testing.T) {
	s := newTestStore(t)

	var n int
	if err := s.Reader().QueryRow(`SELECT count(*) FROM categories`).Scan(&n); err != nil {
		t.Fatalf("count categories: %v", err)
	}
	if n != 8 {
		t.Errorf("categories = %d, muốn 8 (taxonomy ở docs/06-classification.md §1)", n)
	}

	var path string
	if err := s.Reader().QueryRow(
		`SELECT publish_path FROM categories WHERE key = 'ads'`).Scan(&path); err != nil {
		t.Fatalf("read ads category: %v", err)
	}
	if path != "ads.txt" {
		t.Errorf("publish_path của ads = %q, muốn ads.txt", path)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	s := newTestStore(t)

	// Chạy lại phải là no-op, không nhân đôi dữ liệu khởi tạo.
	if err := Migrate(s.Writer()); err != nil {
		t.Fatalf("migrate lần hai: %v", err)
	}
	var n int
	if err := s.Reader().QueryRow(`SELECT count(*) FROM categories`).Scan(&n); err != nil {
		t.Fatalf("count categories: %v", err)
	}
	if n != 8 {
		t.Errorf("categories = %d sau khi migrate lại, muốn 8", n)
	}
}

// Nhật ký quyết định phải bất biến ở tầng CSDL (docs/03-data-model.md §2). Trên
// PostgreSQL việc này do REVOKE đảm nhiệm; trên SQLite là trigger.
func TestDecisionsAreAppendOnly(t *testing.T) {
	s := newTestStore(t)
	w := s.Writer()

	if _, err := w.Exec(`INSERT INTO domains
		(name, name_rev, etld1, origin, first_seen, last_seen, created_at, updated_at)
		VALUES ('ads.example.com', 'moc.elpmaxe.sda', 'example.com', 'manual',
		        '2026-09-07T00:00:00Z','2026-09-07T00:00:00Z',
		        '2026-09-07T00:00:00Z','2026-09-07T00:00:00Z')`); err != nil {
		t.Fatalf("insert domain: %v", err)
	}
	if _, err := w.Exec(`INSERT INTO decisions
		(domain_id, action, actor_label, reason, created_at)
		VALUES (1, 'block', 'admin', 'lý do gốc', '2026-09-07T00:00:00Z')`); err != nil {
		t.Fatalf("insert decision: %v", err)
	}

	if _, err := w.Exec(`UPDATE decisions SET reason = 'sửa trộm' WHERE id = 1`); err == nil {
		t.Error("UPDATE trên decisions phải bị từ chối")
	}
	if _, err := w.Exec(`DELETE FROM decisions WHERE id = 1`); err == nil {
		t.Error("DELETE trên decisions phải bị từ chối")
	}

	var reason string
	if err := s.Reader().QueryRow(`SELECT reason FROM decisions WHERE id = 1`).Scan(&reason); err != nil {
		t.Fatalf("read decision: %v", err)
	}
	if reason != "lý do gốc" {
		t.Errorf("reason = %q, nhật ký đã bị thay đổi", reason)
	}
}

// Migration 0003 dựng lại bảng domain_facts để nới ràng buộc CHECK. Việc chép dữ
// liệu phải giữ nguyên mọi dòng đã có — mất dữ liệu làm giàu nghĩa là phải tra lại
// toàn bộ dịch vụ ngoài từ đầu.
func TestMigrationPreservesExistingFacts(t *testing.T) {
	s := newTestStore(t)
	now := Now()

	if _, err := s.Writer().Exec(`
		INSERT INTO domains (name, name_rev, etld1, origin, first_seen, last_seen, created_at, updated_at)
		VALUES ('ads.example.com', 'moc.elpmaxe.sda', 'example.com', 'discovered', ?, ?, ?, ?)`,
		now, now, now, now); err != nil {
		t.Fatalf("thêm domain: %v", err)
	}
	if _, err := s.Writer().Exec(`
		INSERT INTO domain_facts (domain_id, source, data, fetched_at, expires_at)
		VALUES (1, 'dns', '{"cname_chain":["x.eulerian.net"]}', ?, ?)`,
		now, TimeAt(nowUTC().Add(6*time.Hour))); err != nil {
		t.Fatalf("thêm fact: %v", err)
	}

	// Chạy lại toàn bộ migration: 0003 đã chạy lúc mở, nên đây kiểm tra tính bất biến.
	if err := Migrate(s.Writer()); err != nil {
		t.Fatalf("migrate lại: %v", err)
	}

	var data string
	if err := s.Reader().QueryRow(
		`SELECT data FROM domain_facts WHERE domain_id = 1 AND source = 'dns'`).Scan(&data); err != nil {
		t.Fatalf("đọc lại fact: %v", err)
	}
	if !strings.Contains(data, "eulerian") {
		t.Errorf("dữ liệu làm giàu = %q, đã mất nội dung sau migration", data)
	}

	// Hai nguồn mới phải được ràng buộc CHECK chấp nhận.
	for _, source := range []string{"http", "vt"} {
		if _, err := s.Writer().Exec(`
			INSERT INTO domain_facts (domain_id, source, data, fetched_at, expires_at)
			VALUES (1, ?, '{}', ?, ?)`, source, now, now); err != nil {
			t.Errorf("nguồn %q bị ràng buộc CHECK từ chối: %v", source, err)
		}
	}

	// Nguồn lạ vẫn phải bị từ chối: ràng buộc còn nguyên tác dụng.
	if _, err := s.Writer().Exec(`
		INSERT INTO domain_facts (domain_id, source, data, fetched_at, expires_at)
		VALUES (1, 'khong-ton-tai', '{}', ?, ?)`, now, now); err == nil {
		t.Error("nguồn không hợp lệ được chấp nhận — ràng buộc CHECK đã mất")
	}
}

// TTL phải khác nhau theo kết cục, vì đó là thứ quyết định bao lâu mới tra lại.
func TestTTLForOutcome(t *testing.T) {
	tests := []struct {
		source, outcome string
		wantAtLeast     time.Duration
		wantAtMost      time.Duration
	}{
		{"http", "ok", 13 * 24 * time.Hour, 15 * 24 * time.Hour},
		{"http", "parking", 29 * 24 * time.Hour, 31 * 24 * time.Hour},
		{"http", "dns_fail", 6 * 24 * time.Hour, 8 * 24 * time.Hour},
		{"http", "timeout", 1 * 24 * time.Hour, 3 * 24 * time.Hour},
		{"http", "blocked_host", 89 * 24 * time.Hour, 91 * 24 * time.Hour},
		{"vt", "ok", 29 * 24 * time.Hour, 31 * 24 * time.Hour},
		{"vt", "quota", 30 * time.Minute, 2 * time.Hour},
		{"vt", "not_found", 6 * 24 * time.Hour, 8 * 24 * time.Hour},
	}

	for _, tc := range tests {
		got := TTLFor(tc.source, tc.outcome)
		if got < tc.wantAtLeast || got > tc.wantAtMost {
			t.Errorf("TTLFor(%q, %q) = %v, muốn trong khoảng [%v, %v]",
				tc.source, tc.outcome, got, tc.wantAtLeast, tc.wantAtMost)
		}
	}

	// Thất bại phải luôn được cache lại. Không có TTL nghĩa là tra lại ngay vòng sau,
	// và một domain chết sẽ bị hỏi mãi mãi.
	if TTLFor("http", "khong-biet") <= 0 {
		t.Error("kết cục lạ phải vẫn có TTL dương")
	}
}
