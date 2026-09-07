package store

import (
	"path/filepath"
	"testing"
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
