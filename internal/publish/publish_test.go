package publish

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/benji/dnsguard/internal/store"
)

func newTestPublisher(t *testing.T, minRatio float64) (*Publisher, *store.Store, string) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "pub.db"), true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(s, dir, minRatio, log), s, dir
}

// blockDomains đưa n domain vào trạng thái blocked thuộc phân loại ads.
func blockDomains(t *testing.T, s *store.Store, names ...string) {
	t.Helper()
	now := store.Now()
	for _, name := range names {
		if _, err := s.Writer().Exec(`
			INSERT INTO domains (name, name_rev, etld1, status, origin, category_id,
			                     first_seen, last_seen, created_at, updated_at)
			VALUES (?, ?, 'example.com', 'blocked', 'manual',
			        (SELECT id FROM categories WHERE key = 'ads'), ?, ?, ?, ?)`,
			name, name, now, now, now, now); err != nil {
			t.Fatalf("block %q: %v", name, err)
		}
	}
}

func TestPublishWritesHostsFile(t *testing.T) {
	p, s, dir := newTestPublisher(t, 0.5)
	ctx := context.Background()

	blockDomains(t, s, "ads.example.com", "track.example.com")

	results, err := p.PublishAll(ctx, []string{"ads"}, "admin")
	if err != nil {
		t.Fatalf("PublishAll: %v", err)
	}
	if len(results) != 1 || !results[0].Changed || results[0].EntryCount != 2 {
		t.Fatalf("kết quả = %+v, muốn 1 phân loại với 2 mục và Changed", results)
	}

	body, err := os.ReadFile(filepath.Join(dir, "ads.txt"))
	if err != nil {
		t.Fatalf("đọc ads.txt: %v", err)
	}
	text := string(body)
	for _, want := range []string{"0.0.0.0 ads.example.com", "0.0.0.0 track.example.com", "# DNSGuard — ads"} {
		if !strings.Contains(text, want) {
			t.Errorf("file thiếu %q\n--- nội dung ---\n%s", want, text)
		}
	}
}

// Nội dung không đổi thì không ghi lại và không tạo snapshot mới.
func TestPublishSkipsUnchangedContent(t *testing.T) {
	p, s, _ := newTestPublisher(t, 0.5)
	ctx := context.Background()
	blockDomains(t, s, "ads.example.com")

	if _, err := p.PublishAll(ctx, []string{"ads"}, "admin"); err != nil {
		t.Fatalf("lần xuất bản đầu: %v", err)
	}
	results, err := p.PublishAll(ctx, []string{"ads"}, "admin")
	if err != nil {
		t.Fatalf("lần xuất bản hai: %v", err)
	}
	if results[0].Changed {
		t.Error("lần xuất bản hai báo Changed dù nội dung không đổi")
	}

	snapshots, err := s.ListSnapshots(ctx, "ads", 10)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snapshots) != 1 {
		t.Errorf("có %d snapshot, muốn 1 — nội dung không đổi không được sinh bản mới", len(snapshots))
	}
}

// Bảo vệ sụt giảm: một nguồn hỏng không được làm sập cả danh sách.
func TestPublishBlocksLargeDrop(t *testing.T) {
	p, s, dir := newTestPublisher(t, 0.5)
	ctx := context.Background()

	names := make([]string, 10)
	for i := range names {
		names[i] = string(rune('a'+i)) + ".example.com"
	}
	blockDomains(t, s, names...)
	if _, err := p.PublishAll(ctx, []string{"ads"}, "admin"); err != nil {
		t.Fatalf("xuất bản ban đầu: %v", err)
	}

	before, err := os.ReadFile(filepath.Join(dir, "ads.txt"))
	if err != nil {
		t.Fatalf("đọc file gốc: %v", err)
	}

	// Mất 8/10 domain — dưới ngưỡng 50%.
	if _, err := s.Writer().Exec(
		`UPDATE domains SET status = 'new' WHERE name NOT IN ('a.example.com','b.example.com')`); err != nil {
		t.Fatalf("mô phỏng sụt giảm: %v", err)
	}

	_, err = p.PublishAll(ctx, []string{"ads"}, "admin")
	if !errors.Is(err, ErrPublishBlocked) {
		t.Fatalf("lỗi = %v, muốn ErrPublishBlocked", err)
	}

	// File cũ phải còn nguyên: chặn xuất bản nghĩa là giữ nguyên trạng, không phải
	// ghi một file rỗng.
	after, err := os.ReadFile(filepath.Join(dir, "ads.txt"))
	if err != nil {
		t.Fatalf("đọc file sau khi bị chặn: %v", err)
	}
	if string(before) != string(after) {
		t.Error("file đã bị thay đổi dù việc xuất bản bị chặn")
	}
}

// Ghi nguyên tử: không bao giờ để lại file dở dang cho router đọc phải.
func TestWriteAtomicLeavesNoPartialFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ads.txt")

	if err := writeAtomic(path, "0.0.0.0 first.example.com\n"); err != nil {
		t.Fatalf("writeAtomic: %v", err)
	}
	if err := writeAtomic(path, "0.0.0.0 second.example.com\n"); err != nil {
		t.Fatalf("writeAtomic lần hai: %v", err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("đọc file: %v", err)
	}
	if !strings.Contains(string(body), "second.example.com") {
		t.Error("file không chứa nội dung mới nhất")
	}

	// Không được để lại file tạm nào trong thư mục.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("đọc thư mục: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("còn sót file tạm %q", e.Name())
		}
	}
}

// Rollback ghi lại nội dung cũ và để lại dấu vết trong lịch sử.
func TestRollbackRestoresAndRecordsHistory(t *testing.T) {
	p, s, dir := newTestPublisher(t, 0.0)
	ctx := context.Background()

	blockDomains(t, s, "a.example.com", "b.example.com")
	if _, err := p.PublishAll(ctx, []string{"ads"}, "admin"); err != nil {
		t.Fatalf("xuất bản lần 1: %v", err)
	}
	first, err := s.ListSnapshots(ctx, "ads", 10)
	if err != nil || len(first) != 1 {
		t.Fatalf("ListSnapshots: %v (%d bản)", err, len(first))
	}

	blockDomains(t, s, "c.example.com")
	if _, err := p.PublishAll(ctx, []string{"ads"}, "admin"); err != nil {
		t.Fatalf("xuất bản lần 2: %v", err)
	}

	if _, err := p.Rollback(ctx, first[0].ID, "admin"); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dir, "ads.txt"))
	if err != nil {
		t.Fatalf("đọc file: %v", err)
	}
	if strings.Contains(string(body), "c.example.com") {
		t.Error("rollback không gỡ được domain thêm sau đó")
	}

	after, err := s.ListSnapshots(ctx, "ads", 10)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(after) != 3 {
		t.Errorf("có %d snapshot, muốn 3 — rollback phải thêm bản mới chứ không xóa lịch sử", len(after))
	}
	if !strings.Contains(after[0].PublishedBy, "rollback") {
		t.Errorf("published_by = %q, muốn có ghi chú rollback", after[0].PublishedBy)
	}
}
