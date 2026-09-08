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
	"time"

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
	return New(s, dir, minRatio, "", log), s, dir
}

// readFile đọc một danh sách đã xuất bản.
func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("đọc %s: %v", path, err)
	}
	return string(raw)
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

func TestRenderUsesConfiguredSinkAddress(t *testing.T) {
	p, s, dir := newTestPublisher(t, 0.5)
	ctx := context.Background()

	blockDomains(t, s, "quangcao.vn", "theodoi.vn")

	if err := s.SetSetting(ctx, store.SettingPublishSink, "127.0.0.1", "admin"); err != nil {
		t.Fatalf("lưu địa chỉ: %v", err)
	}
	if _, err := p.PublishAll(ctx, nil, "test"); err != nil {
		t.Fatalf("xuất bản: %v", err)
	}

	body := readFile(t, filepath.Join(dir, "all.txt"))
	if !strings.Contains(body, "127.0.0.1 quangcao.vn") {
		t.Errorf("không dùng địa chỉ đã cấu hình:\n%s", body)
	}
	if strings.Contains(body, "0.0.0.0 quangcao.vn") {
		t.Errorf("vẫn còn địa chỉ mặc định:\n%s", body)
	}
}

func TestSinkAddressFallsBackWhenSettingIsCorrupt(t *testing.T) {
	// Một giá trị hỏng trong CSDL không được biến file hosts thành rác. Ghi ra
	// "khong-phai-ip domain.vn" nghĩa là phần lớn phần mềm bỏ qua cả file, và mạng
	// mất chặn hoàn toàn mà không có lỗi nào.
	p, s, dir := newTestPublisher(t, 0.5)
	ctx := context.Background()

	blockDomains(t, s, "quangcao.vn")
	if err := s.SetSetting(ctx, store.SettingPublishSink, "khong-phai-ip", "admin"); err != nil {
		t.Fatalf("lưu địa chỉ: %v", err)
	}

	if got := p.SinkAddress(ctx); got != DefaultSink {
		t.Errorf("địa chỉ = %q, muốn lùi về %q", got, DefaultSink)
	}
	if _, err := p.PublishAll(ctx, nil, "test"); err != nil {
		t.Fatalf("xuất bản: %v", err)
	}
	if body := readFile(t, filepath.Join(dir, "all.txt")); !strings.Contains(body, DefaultSink+" quangcao.vn") {
		t.Errorf("không lùi về mặc định:\n%s", body)
	}
}

func TestChangingSinkAddressRepublishes(t *testing.T) {
	// Đổi địa chỉ phải làm nội dung đổi theo. Nếu checksum không tính cả địa chỉ thì
	// lần xuất bản sau bị coi là "không có gì thay đổi" và file cũ nằm nguyên.
	p, s, dir := newTestPublisher(t, 0.5)
	ctx := context.Background()

	blockDomains(t, s, "quangcao.vn")
	if _, err := p.PublishAll(ctx, nil, "test"); err != nil {
		t.Fatalf("xuất bản lần đầu: %v", err)
	}

	if err := s.SetSetting(ctx, store.SettingPublishSink, "127.0.0.1", "admin"); err != nil {
		t.Fatalf("lưu địa chỉ: %v", err)
	}
	results, err := p.PublishAll(ctx, nil, "test")
	if err != nil {
		t.Fatalf("xuất bản lần hai: %v", err)
	}

	changed := false
	for _, r := range results {
		if r.Category == "all" {
			changed = r.Changed
		}
	}
	if !changed {
		t.Error("đổi địa chỉ mà vẫn báo không có thay đổi")
	}
	if body := readFile(t, filepath.Join(dir, "all.txt")); !strings.Contains(body, "127.0.0.1 quangcao.vn") {
		t.Errorf("file không được ghi lại:\n%s", body)
	}
}

func TestBlockedListCatchesDomainsNoOtherListWould(t *testing.T) {
	// Trường hợp thật khiến danh sách này ra đời: chặn thủ công một domain rơi vào
	// phân loại đã tắt xuất bản. Giao diện ghi "đã chặn", nhưng domain không nằm
	// trong file nào cả và router không bao giờ thấy nó.
	p, s, dir := newTestPublisher(t, 0)
	ctx := context.Background()

	if _, err := s.Writer().Exec(
		`UPDATE categories SET enabled = 0 WHERE key = 'content'`); err != nil {
		t.Fatalf("tắt xuất bản content: %v", err)
	}
	blockDomainsIn(t, s, "content", "bblaa.com")
	blockDomains(t, s, "quangcao.vn") // phân loại ads, vẫn đang bật

	if _, err := p.PublishAll(ctx, nil, "test"); err != nil {
		t.Fatalf("xuất bản: %v", err)
	}

	all := readFile(t, filepath.Join(dir, "all.txt"))
	if strings.Contains(all, "bblaa.com") {
		t.Error("all.txt chứa domain thuộc phân loại đã tắt — sai theo thiết kế của nó")
	}

	blocked := readFile(t, filepath.Join(dir, "blocked.txt"))
	for _, want := range []string{"bblaa.com", "quangcao.vn"} {
		if !strings.Contains(blocked, want) {
			t.Errorf("blocked.txt thiếu %q:\n%s", want, blocked)
		}
	}
}

func TestBlockedListIncludesDomainsWithoutCategory(t *testing.T) {
	// Domain chặn nhưng chưa có phân loại cũng rơi ra ngoài mọi file, vì truy vấn cũ
	// dùng INNER JOIN sang bảng categories.
	p, s, dir := newTestPublisher(t, 0)
	ctx := context.Background()

	now := store.Now()
	if _, err := s.Writer().Exec(`
		INSERT INTO domains (name, name_rev, etld1, status, origin,
		                     first_seen, last_seen, created_at, updated_at)
		VALUES ('khongphanloai.vn', 'khongphanloai.vn', 'khongphanloai.vn',
		        'blocked', 'manual', ?, ?, ?, ?)`, now, now, now, now); err != nil {
		t.Fatalf("tạo domain: %v", err)
	}

	if _, err := p.PublishAll(ctx, nil, "test"); err != nil {
		t.Fatalf("xuất bản: %v", err)
	}
	if blocked := readFile(t, filepath.Join(dir, "blocked.txt")); !strings.Contains(blocked, "khongphanloai.vn") {
		t.Errorf("blocked.txt thiếu domain chưa có phân loại:\n%s", blocked)
	}
}

func TestAggregateListsKeepSeparateHistories(t *testing.T) {
	// Cả hai file tổng hợp lưu snapshot với category_id NULL. Không phân biệt theo
	// đường dẫn thì chúng đọc nhầm lịch sử của nhau, và lớp bảo vệ sụt giảm so sai
	// bảng — đúng lúc nó cần chính xác nhất.
	p, s, _ := newTestPublisher(t, 0.5)
	ctx := context.Background()

	if _, err := s.Writer().Exec(
		`UPDATE categories SET enabled = 0 WHERE key = 'content'`); err != nil {
		t.Fatalf("tắt content: %v", err)
	}
	blockDomains(t, s, "a.vn", "b.vn")                      // ads, vào cả hai file
	blockDomainsIn(t, s, "content", "c.vn", "d.vn", "e.vn") // chỉ vào blocked.txt

	results, err := p.PublishAll(ctx, nil, "test")
	if err != nil {
		t.Fatalf("xuất bản: %v", err)
	}

	counts := map[string]int{}
	for _, r := range results {
		counts[r.Category] = r.EntryCount
	}
	if counts["all"] != 2 {
		t.Errorf("all.txt = %d mục, muốn 2", counts["all"])
	}
	if counts["blocked"] != 5 {
		t.Errorf("blocked.txt = %d mục, muốn 5", counts["blocked"])
	}

	// Lần hai không đổi gì: cả hai phải báo không thay đổi, chứng tỏ mỗi file so với
	// đúng lịch sử của chính nó.
	results, err = p.PublishAll(ctx, nil, "test")
	if err != nil {
		t.Fatalf("xuất bản lần hai: %v", err)
	}
	for _, r := range results {
		if (r.Category == "all" || r.Category == "blocked") && r.Changed {
			t.Errorf("%s báo có thay đổi dù dữ liệu không đổi", r.Category)
		}
	}
}

// blockDomainsIn đưa domain vào trạng thái blocked thuộc một phân loại cho trước.
func blockDomainsIn(t *testing.T, s *store.Store, categoryKey string, names ...string) {
	t.Helper()
	now := store.Now()
	for _, name := range names {
		if _, err := s.Writer().Exec(`
			INSERT INTO domains (name, name_rev, etld1, status, origin, category_id,
			                     first_seen, last_seen, created_at, updated_at)
			VALUES (?, ?, 'example.com', 'blocked', 'manual',
			        (SELECT id FROM categories WHERE key = ?), ?, ?, ?, ?)`,
			name, name, categoryKey, now, now, now, now); err != nil {
			t.Fatalf("chặn %q: %v", name, err)
		}
	}
}

func TestChecksumIgnoresGenerationTime(t *testing.T) {
	// Header có mốc thời gian sinh file. Băm cả file khiến checksum đổi mỗi giây dù
	// tập domain không đổi — và khi đó mọi thứ dựa trên checksum đều hỏng: lần nào
	// cũng bị coi là có thay đổi, lịch sử phình lên bằng các bản giống hệt nhau, và
	// router tải lại một danh sách y nguyên sau mỗi lần chạy.
	p, s, _ := newTestPublisher(t, 0.5)
	ctx := context.Background()
	blockDomains(t, s, "quangcao.vn")

	first, err := p.PublishAll(ctx, []string{"ads"}, "admin")
	if err != nil {
		t.Fatalf("xuất bản lần đầu: %v", err)
	}

	// Vượt qua ranh giới một giây: đây chính là điều kiện làm lỗi cũ lộ ra.
	time.Sleep(1100 * time.Millisecond)

	second, err := p.PublishAll(ctx, []string{"ads"}, "admin")
	if err != nil {
		t.Fatalf("xuất bản lần hai: %v", err)
	}

	if first[0].Checksum != second[0].Checksum {
		t.Errorf("checksum đổi dù tập domain không đổi:\n  %s\n  %s",
			first[0].Checksum, second[0].Checksum)
	}
	if second[0].Changed {
		t.Error("báo có thay đổi dù tập domain không đổi")
	}

	snapshots, err := s.ListSnapshots(ctx, "ads", 10)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snapshots) != 1 {
		t.Errorf("có %d snapshot, muốn 1", len(snapshots))
	}
}

func TestChecksumStillTracksSinkAddress(t *testing.T) {
	// Mặt còn lại: địa chỉ đích là nội dung thật, đổi nó phải kích hoạt ghi lại file.
	p, s, _ := newTestPublisher(t, 0.5)
	ctx := context.Background()
	blockDomains(t, s, "quangcao.vn")

	first, err := p.PublishAll(ctx, []string{"ads"}, "admin")
	if err != nil {
		t.Fatalf("xuất bản lần đầu: %v", err)
	}
	if err := s.SetSetting(ctx, store.SettingPublishSink, "127.0.0.1", "admin"); err != nil {
		t.Fatalf("đổi địa chỉ: %v", err)
	}
	second, err := p.PublishAll(ctx, []string{"ads"}, "admin")
	if err != nil {
		t.Fatalf("xuất bản lần hai: %v", err)
	}

	if first[0].Checksum == second[0].Checksum {
		t.Error("đổi địa chỉ đích mà checksum không đổi")
	}
	if !second[0].Changed {
		t.Error("đổi địa chỉ đích mà không ghi lại file")
	}
}
