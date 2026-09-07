package ai

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/benji/dnsguard/internal/enrich"
	"github.com/benji/dnsguard/internal/llm"
	"github.com/benji/dnsguard/internal/store"
)

// ErrNoAPIKey báo rằng chưa cấu hình nhà cung cấp nào.
//
// Bọc enrich.ErrNoCredentials để tầng worker nhận ra đây là chuyện của nguồn chứ
// không phải của domain, và không ghi nó thành kết luận trong domain_facts.
var ErrNoAPIKey = fmt.Errorf("chưa cấu hình model AI: %w", enrich.ErrNoCredentials)

// DefaultBatchSize là số domain hỏi trong một lượt.
//
// Bốn mươi chọn theo hai ràng buộc gặp nhau: đủ lớn để phần mô tả bài toán —
// khoảng 400 token, gửi lại nguyên vẹn ở mọi lượt — được chia đều cho nhiều
// domain, và đủ nhỏ để cả câu hỏi lẫn câu trả lời nằm gọn trong cửa sổ ngữ cảnh
// của những model rẻ nhất. Lô càng lớn thì một lần trả lời sai khuôn càng đắt.
const DefaultBatchSize = 40

// Giới hạn cứng cho kích thước lô đặt từ giao diện.
const (
	MinBatchSize = 1
	MaxBatchSize = 200
)

// SourceName là khóa lưu trong domain_facts.
const SourceName = "ai"

// EvidenceFunc tra những gì DNSGuard đã biết về một tập domain.
//
// Truyền vào dưới dạng hàm thay vì cho Classifier giữ *store.Store: gói ai đã
// mở CSDL riêng của nó, và cho nó thêm một tay cầm vào CSDL chính sẽ làm ranh
// giới "AI ghi ở đâu" nhòe đi. Cùng khuôn với cách enrich.NewASN nhận hàm phân
// giải DNS.
type EvidenceFunc func(ctx context.Context, domains []string) ([]Evidence, error)

// Assessment là kết luận của model về một domain, dạng lưu trong domain_facts.
type Assessment struct {
	Category   string  `json:"category"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason,omitempty"`
	Model      string  `json:"model,omitempty"`
	// RequestID trỏ về dòng trong CSDL nhật ký AI. Không có nó thì sáu tháng sau
	// không truy ngược được một quyết định về đúng lượt gọi đã sinh ra nó.
	RequestID int64  `json:"request_id,omitempty"`
	FetchedAt string `json:"fetched_at"`
}

// Classifier hỏi model về nhiều domain trong một lượt gọi.
//
// Cài enrich.BatchEnricher nên nó nằm trong registry cùng các nguồn khác và thừa
// hưởng nguyên ba lớp bảo vệ đã có ở đó: công tắc bật/tắt lúc chạy, giới hạn tốc
// độ, và circuit breaker khi nhà cung cấp hỏng.
type Classifier struct {
	client   *llm.Client
	history  *Store
	evidence EvidenceFunc
	log      *slog.Logger

	// batchSize đổi được lúc chạy từ giao diện, trong khi worker có thể đang gửi
	// một lô ở luồng khác.
	batchSize atomic.Int64
}

// NewClassifier dựng bộ phân loại.
func NewClassifier(client *llm.Client, history *Store, evidence EvidenceFunc, log *slog.Logger) *Classifier {
	c := &Classifier{client: client, history: history, evidence: evidence, log: log}
	c.SetBatchSize(DefaultBatchSize)
	return c
}

func (c *Classifier) Name() string { return SourceName }

// SetBatchSize đổi kích thước lô lúc chạy, kẹp vào khoảng cho phép.
func (c *Classifier) SetBatchSize(n int) {
	switch {
	case n < MinBatchSize:
		n = DefaultBatchSize
	case n > MaxBatchSize:
		n = MaxBatchSize
	}
	c.batchSize.Store(int64(n))
}

// BatchSize là số domain tối đa gửi trong một lượt.
func (c *Classifier) BatchSize() int { return int(c.batchSize.Load()) }

// Configured cho biết đã có khóa API chưa.
func (c *Classifier) Configured() bool { return c.client != nil && c.client.Enabled() }

// Enrich hỏi về đúng một domain — dùng khi người vận hành bấm "hỏi lại".
//
// Vẫn đi qua đường lô một phần tử thay vì có một đường riêng: prompt, cách đọc
// CSV và cách ghi nhật ký phải giống hệt, nếu không thì kết quả hỏi lại sẽ khác
// kết quả tự động một cách khó giải thích.
func (c *Classifier) Enrich(ctx context.Context, domain string) (any, error) {
	out, err := c.EnrichBatch(ctx, []string{domain})
	if err != nil {
		return nil, err
	}
	data, found := out[domain]
	if !found {
		return nil, fmt.Errorf("model không kết luận được về %q", domain)
	}
	return data, nil
}

// EnrichBatch hỏi model về cả lô và trả kết quả theo tên domain.
//
// Domain vắng mặt trong map trả về là domain model bỏ qua hoặc trả sai khuôn.
// Tầng trên không ghi gì cho chúng, nên chúng sẽ được hỏi lại ở vòng sau — đúng
// hành vi mong muốn, vì "model quên trả lời" không phải một kết luận.
func (c *Classifier) EnrichBatch(ctx context.Context, domains []string) (map[string]any, error) {
	if !c.Configured() {
		return nil, ErrNoAPIKey
	}
	domains = dedupeNames(domains)
	if len(domains) == 0 {
		return map[string]any{}, nil
	}
	if max := c.BatchSize(); len(domains) > max {
		// Cắt thay vì tự chia nhỏ và gọi nhiều lượt: người gọi kiểm soát nhịp gọi,
		// và một hàm âm thầm tiêu gấp năm lần hạn mức dự kiến là thứ khó gỡ nhất.
		domains = domains[:max]
	}

	items, err := c.buildEvidence(ctx, domains)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return map[string]any{}, nil
	}

	prompt := buildPrompt(items)
	cfg := c.client.Config()
	record := Request{
		Kind: KindClassify, Model: cfg.Model, BaseURL: cfg.BaseURL,
		DomainCount: len(items), Prompt: prompt, Actor: store.ActorSystem,
	}

	start := time.Now()
	reply, callErr := c.client.Chat(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: systemPrompt},
		{Role: llm.RoleUser, Content: prompt},
	})
	record.LatencyMS = time.Since(start).Milliseconds()
	record.Response = reply

	if callErr != nil {
		// Ghi cả lượt hỏng: một chuỗi lỗi trong lịch sử là cách duy nhất người vận
		// hành biết vì sao nguồn AI im lặng, thay vì phải đọc log máy chủ.
		record.Error = callErr.Error()
		c.record(ctx, record, nil)
		return nil, callErr
	}

	names := make([]string, len(items))
	for i, it := range items {
		names[i] = it.Name
	}
	parsed := parseCSV(reply, names)
	record.ParsedCount, record.SkippedCount = len(parsed.Verdicts), parsed.Skipped

	requestID := c.record(ctx, record, parsed.Verdicts)

	if len(parsed.Unknown) > 0 {
		// Model bịa domain là dấu hiệu prompt hoặc model có vấn đề, không phải
		// chuyện vặt. Nói ra để người vận hành thấy mà đổi model.
		c.log.Warn("model trả về domain không có trong lô",
			"count", len(parsed.Unknown), "vi_du", parsed.Unknown[:min(3, len(parsed.Unknown))])
	}
	if parsed.Skipped > 0 {
		c.log.Info("bỏ qua dòng sai khuôn từ model",
			"skipped", parsed.Skipped, "parsed", len(parsed.Verdicts), "hoi", len(items))
	}

	now := store.Now()
	out := make(map[string]any, len(parsed.Verdicts))
	for _, v := range parsed.Verdicts {
		out[v.Domain] = Assessment{
			Category:   v.Category,
			Confidence: v.Confidence,
			Reason:     v.Reason,
			Model:      cfg.Model,
			RequestID:  requestID,
			FetchedAt:  now,
		}
	}
	return out, nil
}

// buildEvidence tra dữ kiện cho cả lô, lùi về chỉ-có-tên khi tra hỏng.
//
// Không để việc tra dữ kiện làm hỏng cả lượt: model vẫn phân loại được từ tên
// miền, chỉ kém chính xác hơn. Đổi lại phải nói rõ trong log, vì một lô toàn
// domain trần mà không ai biết là một lô cho kết quả tệ không rõ lý do.
func (c *Classifier) buildEvidence(ctx context.Context, domains []string) ([]Evidence, error) {
	if c.evidence == nil {
		return bareEvidence(domains), nil
	}
	items, err := c.evidence(ctx, domains)
	if err != nil {
		c.log.Warn("không tra được dữ kiện cho lô AI, hỏi bằng tên miền trần", "err", err)
		return bareEvidence(domains), nil
	}
	if len(items) == 0 {
		return bareEvidence(domains), nil
	}
	return items, nil
}

func bareEvidence(domains []string) []Evidence {
	out := make([]Evidence, len(domains))
	for i, d := range domains {
		out[i] = Evidence{Name: d}
	}
	return out
}

// record ghi nhật ký và trả về id. Nhật ký hỏng không được làm hỏng lượt phân
// loại: kết luận vẫn đúng, chỉ là không tra ngược được.
func (c *Classifier) record(ctx context.Context, req Request, verdicts []Verdict) int64 {
	if c.history == nil {
		return 0
	}
	id, err := c.history.SaveRequest(ctx, req, verdicts)
	if err != nil {
		c.log.Error("ghi nhật ký AI thất bại", "err", err)
		return 0
	}
	return id
}

// dedupeNames chuẩn hoá và loại trùng, giữ nguyên thứ tự.
//
// Thứ tự giữ nguyên vì nó là thứ tự ưu tiên do tầng trên chọn — thường là theo
// lưu lượng — và phần bị cắt khi lô quá dài phải là phần ít quan trọng nhất.
func dedupeNames(domains []string) []string {
	seen := make(map[string]bool, len(domains))
	out := make([]string, 0, len(domains))
	for _, d := range domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}
