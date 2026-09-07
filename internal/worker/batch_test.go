package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"

	"github.com/benji/dnsguard/internal/enrich"
	"github.com/benji/dnsguard/internal/store"
)

// stubBatch đóng vai một nguồn hỏi theo lô mà không chạm mạng.
type stubBatch struct {
	mu      sync.Mutex
	batches [][]string
	reply   map[string]any
	err     error
	size    int
}

func (s *stubBatch) Name() string { return "ai" }

func (s *stubBatch) BatchSize() int {
	if s.size <= 0 {
		return 40
	}
	return s.size
}

func (s *stubBatch) Enrich(ctx context.Context, domain string) (any, error) {
	out, err := s.EnrichBatch(ctx, []string{domain})
	if err != nil {
		return nil, err
	}
	return out[domain], nil
}

func (s *stubBatch) EnrichBatch(_ context.Context, domains []string) (map[string]any, error) {
	s.mu.Lock()
	s.batches = append(s.batches, append([]string(nil), domains...))
	s.mu.Unlock()

	if s.err != nil {
		return nil, s.err
	}
	out := map[string]any{}
	for _, d := range domains {
		if reply, found := s.reply[d]; found {
			out[d] = reply
		}
	}
	return out, nil
}

func (s *stubBatch) calls() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.batches
}

// newBatchRunner dựng một Runner tối thiểu đủ để chạy đường làm giàu theo lô.
func newBatchRunner(t *testing.T, source enrich.Enricher) (*Runner, *store.Store) {
	t.Helper()

	db, err := store.Open(filepath.Join(t.TempDir(), "batch.db"), true)
	if err != nil {
		t.Fatalf("mở CSDL: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := enrich.NewRegistry(log)
	registry.Register(source, 1000, 1000, true)

	return &Runner{store: db, enrichers: registry, log: log}, db
}

// seedDomains thêm domain có đủ lưu lượng để lọt cổng lọc của nguồn AI.
func seedDomains(t *testing.T, db *store.Store, names ...string) {
	t.Helper()
	now := store.Now()
	for _, name := range names {
		if _, err := db.Writer().ExecContext(context.Background(), `
			INSERT INTO domains (name, name_rev, etld1, status, origin, query_count,
			                     first_seen, last_seen, created_at, updated_at)
			VALUES (?, ?, ?, 'new', 'discovered', 50, ?, ?, ?, ?)`,
			name, name, name, now, now, now, now); err != nil {
			t.Fatalf("thêm domain %q: %v", name, err)
		}
	}
}

// Lô dài hơn BatchSize phải được chia thành nhiều lượt, không cắt bớt domain.
func TestEnrichInBatchesSplitsByBatchSize(t *testing.T) {
	source := &stubBatch{size: 2, reply: map[string]any{}}
	runner, db := newBatchRunner(t, source)

	names := []string{"mot.com", "hai.com", "ba.com", "bon.com", "nam.com"}
	seedDomains(t, db, names...)

	candidates, err := runner.candidatesFor(context.Background(), "ai")
	if err != nil {
		t.Fatalf("candidatesFor: %v", err)
	}
	if len(candidates) != len(names) {
		t.Fatalf("số ứng viên = %d, muốn %d", len(candidates), len(names))
	}

	batcher, _ := enrich.AsBatch(runner.enrichers.Sources()[0])
	runner.enrichInBatches(context.Background(), batcher, candidates)

	calls := source.calls()
	if len(calls) != 3 { // 2 + 2 + 1
		t.Fatalf("số lượt gọi = %d, muốn 3", len(calls))
	}
	seen := map[string]bool{}
	for _, batch := range calls {
		if len(batch) > 2 {
			t.Errorf("một lô có %d domain, vượt BatchSize 2", len(batch))
		}
		for _, name := range batch {
			seen[name] = true
		}
	}
	if len(seen) != len(names) {
		t.Errorf("chỉ hỏi %d/%d domain — có domain bị bỏ rơi", len(seen), len(names))
	}
}

// Domain đã có kết luận còn hạn thì KHÔNG được hỏi lại.
//
// Đây là cơ chế "đã hỏi rồi thì thôi": nó không phải một bộ nhớ riêng, mà chính là
// dòng domain_facts cộng TTL — cùng cơ chế đang dùng cho HTTP và VirusTotal.
func TestAskedDomainsAreNotAskedAgain(t *testing.T) {
	source := &stubBatch{reply: map[string]any{}}
	runner, db := newBatchRunner(t, source)
	ctx := context.Background()

	seedDomains(t, db, "da-hoi.com", "chua-hoi.com")

	domain, err := db.GetDomainByName(ctx, "da-hoi.com")
	if err != nil {
		t.Fatalf("GetDomainByName: %v", err)
	}
	if err := db.SaveFact(ctx, domain.ID, "ai", map[string]any{
		"category": "ads", "confidence": 0.9,
	}); err != nil {
		t.Fatalf("SaveFact: %v", err)
	}

	candidates, err := runner.candidatesFor(ctx, "ai")
	if err != nil {
		t.Fatalf("candidatesFor: %v", err)
	}
	if len(candidates) != 1 || candidates[0].Name != "chua-hoi.com" {
		t.Fatalf("ứng viên = %v, muốn chỉ chua-hoi.com", candidates)
	}
}

// Người vận hành bấm "hỏi lại" thì phải thật sự hỏi lại, kể cả khi kết luận cũ
// còn hạn. Nút không làm gì là cách chắc chắn nhất để mất lòng tin vào tính năng.
func TestRecheckIgnoresFreshFact(t *testing.T) {
	source := &stubBatch{reply: map[string]any{}}
	runner, db := newBatchRunner(t, source)
	ctx := context.Background()

	seedDomains(t, db, "da-hoi.com")
	domain, err := db.GetDomainByName(ctx, "da-hoi.com")
	if err != nil {
		t.Fatalf("GetDomainByName: %v", err)
	}
	if err := db.SaveFact(ctx, domain.ID, "ai", map[string]any{"category": "ads"}); err != nil {
		t.Fatalf("SaveFact: %v", err)
	}

	candidates, err := runner.aiCandidates(ctx, []string{"da-hoi.com"})
	if err != nil {
		t.Fatalf("aiCandidates: %v", err)
	}
	if len(candidates) != 1 || candidates[0].Name != "da-hoi.com" {
		t.Errorf("ứng viên = %v, muốn da-hoi.com dù kết luận cũ còn hạn", candidates)
	}
}

// Lỗi thuộc về *nguồn* không được ghi thành kết luận thất bại cho từng domain.
//
// Không có phân biệt này, một khóa API sai sẽ đóng băng mọi domain trong lô suốt
// sáu tiếng, và chúng đứng im cả sau khi người vận hành đã sửa khóa.
func TestSourceErrorDoesNotPoisonDomains(t *testing.T) {
	source := &stubBatch{err: enrich.ErrNoCredentials}
	runner, db := newBatchRunner(t, source)
	ctx := context.Background()

	seedDomains(t, db, "mot.com", "hai.com")
	candidates, err := runner.candidatesFor(ctx, "ai")
	if err != nil {
		t.Fatalf("candidatesFor: %v", err)
	}

	batcher, _ := enrich.AsBatch(runner.enrichers.Sources()[0])
	runner.enrichInBatches(ctx, batcher, candidates)

	// Vẫn còn nguyên là ứng viên: không dòng thất bại nào được ghi.
	after, err := runner.candidatesFor(ctx, "ai")
	if err != nil {
		t.Fatalf("candidatesFor lần hai: %v", err)
	}
	if len(after) != len(candidates) {
		t.Errorf("còn %d ứng viên, muốn %d — lỗi của nguồn đã bị ghi thành lỗi của domain",
			len(after), len(candidates))
	}
}

// Lỗi thật của một lượt gọi thì phải ghi lại, để vòng sau không hỏi lại ngay.
func TestRealErrorIsRecorded(t *testing.T) {
	source := &stubBatch{err: errors.New("model trả HTTP 500")}
	runner, db := newBatchRunner(t, source)
	ctx := context.Background()

	seedDomains(t, db, "mot.com", "hai.com")
	candidates, err := runner.candidatesFor(ctx, "ai")
	if err != nil {
		t.Fatalf("candidatesFor: %v", err)
	}

	batcher, _ := enrich.AsBatch(runner.enrichers.Sources()[0])
	runner.enrichInBatches(ctx, batcher, candidates)

	after, err := runner.candidatesFor(ctx, "ai")
	if err != nil {
		t.Fatalf("candidatesFor lần hai: %v", err)
	}
	if len(after) != 0 {
		t.Errorf("còn %d ứng viên sau lỗi thật — sẽ bị hỏi lại ngay vòng sau", len(after))
	}
}
