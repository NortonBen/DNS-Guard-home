package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/benji/dnsguard/internal/classify"
)

// TTL cho từng nguồn làm giàu (docs/03-data-model.md §2).
var factTTL = map[string]time.Duration{
	"dns":  6 * time.Hour,       // CNAME và IP đổi thường xuyên
	"asn":  7 * 24 * time.Hour,  // ánh xạ IP→ASN ổn định
	"cert": 30 * 24 * time.Hour, // chứng chỉ đổi hiếm
	"rdap": 90 * 24 * time.Hour, // ngày đăng ký không đổi
	"rank": 7 * 24 * time.Hour,  // thứ hạng Tranco ổn định
	"http": 14 * 24 * time.Hour, // nội dung trang đổi chậm
	"vt":   30 * 24 * time.Hour, // kết luận của các engine đổi chậm
}

const day = 24 * time.Hour

// outcomeTTL là thời gian sống theo *kết cục*, không chỉ theo nguồn.
//
// Đây là phần trả lời yêu cầu "nhớ đã check chưa, đừng check lại ngay, bao lâu thì
// check lại". Bảng domain_facts đã lo phần "nhớ": có dòng thì EnrichCandidates không
// chọn domain đó nữa cho tới khi hết hạn, kể cả khi nó bị truy vấn liên tục. Phần
// còn lại là chọn đúng nhịp cho từng kiểu thất bại — một host đã chết thì đừng hỏi
// lại mỗi giờ, còn hết quota thì nên thử lại ngay trong ngày.
var outcomeTTL = map[string]map[string]time.Duration{
	"http": {
		"parking":      30 * day, // domain đã đỗ thì đỗ lâu
		"dns_fail":     7 * day,  // host chết thì cứ chết
		"refused":      7 * day,  //
		"tls_error":    7 * day,  // cấu hình TLS hiếm khi đổi
		"http_error":   3 * day,  //
		"timeout":      2 * day,  // có thể chỉ là tạm thời
		"blocked_host": 90 * day, // quyết định an toàn, không đổi theo thời gian
		"other":        3 * day,  //
	},
	"vt": {
		"not_found": 7 * day,       // có thể được lập chỉ mục sau
		"quota":     time.Hour,     // thử lại trong ngày
		"other":     6 * time.Hour, //
	},
}

// TTLFor trả về thời gian sống cho một cặp (nguồn, kết cục).
func TTLFor(source, outcome string) time.Duration {
	if outcome == "" || outcome == "ok" {
		if ttl, ok := factTTL[source]; ok {
			return ttl
		}
		return 24 * time.Hour
	}
	if bySource, ok := outcomeTTL[source]; ok {
		if ttl, ok := bySource[outcome]; ok {
			return ttl
		}
	}
	return errorRetryAfter
}

// errorRetryAfter là TTL mặc định khi tra cứu thất bại mà không rơi vào kết cục nào
// đã biết: thử lại sớm, nhưng vẫn tôn trọng circuit breaker ở tầng enrich.
const errorRetryAfter = 15 * time.Minute

// EnrichCandidate là một domain cần làm giàu.
type EnrichCandidate struct {
	ID    int64
	Name  string
	ETLD1 string
}

// EnrichCandidates lấy các domain cần làm giàu: chưa có dữ liệu hoặc đã hết TTL.
// Ưu tiên domain có lưu lượng cao — chúng là thứ quản trị sẽ nhìn thấy trước.
func (s *Store) EnrichCandidates(ctx context.Context, source string, limit int) ([]EnrichCandidate, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.r.QueryContext(ctx, `
		SELECT d.id, d.name, d.etld1
		FROM domains d
		LEFT JOIN domain_facts f ON f.domain_id = d.id AND f.source = ?
		WHERE d.status IN ('new','staging','blocked')
		  AND (f.domain_id IS NULL OR f.expires_at <= ?)
		ORDER BY d.query_count DESC
		LIMIT ?`, source, Now(), limit)
	if err != nil {
		return nil, fmt.Errorf("list enrich candidates for %q: %w", source, err)
	}
	defer rows.Close()

	var out []EnrichCandidate
	for rows.Next() {
		var c EnrichCandidate
		if err := rows.Scan(&c.ID, &c.Name, &c.ETLD1); err != nil {
			return nil, fmt.Errorf("scan enrich candidate: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SaveFact ghi kết quả làm giàu kèm TTL của nguồn.
func (s *Store) SaveFact(ctx context.Context, domainID int64, source string, data any) error {
	return s.SaveFactWithOutcome(ctx, domainID, source, data, "")
}

// SaveFactWithOutcome ghi kết quả kèm TTL chọn theo kết cục.
//
// Dùng khi một lần tra thành công vẫn có nhiều mức "đáng tin lâu" khác nhau: trang
// đỗ tên miền giữ được ba mươi ngày, trang thường chỉ mười bốn.
func (s *Store) SaveFactWithOutcome(ctx context.Context, domainID int64, source string,
	data any, outcome string) error {

	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode fact %q: %w", source, err)
	}
	ttl := TTLFor(source, outcome)
	now := time.Now().UTC()
	_, err = s.w.ExecContext(ctx, `
		INSERT INTO domain_facts (domain_id, source, data, fetched_at, expires_at, error)
		VALUES (?, ?, ?, ?, ?, NULL)
		ON CONFLICT (domain_id, source) DO UPDATE SET
		  data = excluded.data, fetched_at = excluded.fetched_at,
		  expires_at = excluded.expires_at, error = NULL`,
		domainID, source, string(raw), TimeAt(now), TimeAt(now.Add(ttl)))
	if err != nil {
		return fmt.Errorf("save fact %q: %w", source, err)
	}

	_, err = s.w.ExecContext(ctx,
		`UPDATE domains SET enriched_at = ?, updated_at = ? WHERE id = ?`,
		TimeAt(now), TimeAt(now), domainID)
	if err != nil {
		return fmt.Errorf("touch enriched_at: %w", err)
	}
	return nil
}

// SaveFactError ghi lại một lần tra cứu thất bại.
func (s *Store) SaveFactError(ctx context.Context, domainID int64, source string, cause error) error {
	return s.SaveFactErrorWithOutcome(ctx, domainID, source, cause, "")
}

// SaveFactErrorWithOutcome ghi lỗi kèm TTL chọn theo kiểu thất bại.
//
// Ghi cả khi hỏng là có chủ ý: dòng thất bại chính là thứ ngăn hệ thống thử lại ngay
// vòng sau. Không có nó, một domain không kết nối được sẽ bị tra lại mỗi mười lăm
// phút mãi mãi.
func (s *Store) SaveFactErrorWithOutcome(ctx context.Context, domainID int64, source string,
	cause error, outcome string) error {

	now := time.Now().UTC()
	ttl := TTLFor(source, outcome)
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO domain_facts (domain_id, source, data, fetched_at, expires_at, error)
		VALUES (?, ?, '{}', ?, ?, ?)
		ON CONFLICT (domain_id, source) DO UPDATE SET
		  fetched_at = excluded.fetched_at, expires_at = excluded.expires_at,
		  error = excluded.error`,
		domainID, source, TimeAt(now), TimeAt(now.Add(ttl)), cause.Error())
	if err != nil {
		return fmt.Errorf("save fact error %q: %w", source, err)
	}
	return nil
}

// Facts trả về toàn bộ dữ liệu làm giàu của một domain cho trang chi tiết.
func (s *Store) Facts(ctx context.Context, domainID int64) (map[string]json.RawMessage, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT source, data, error FROM domain_facts WHERE domain_id = ? LIMIT 10`, domainID)
	if err != nil {
		return nil, fmt.Errorf("read facts: %w", err)
	}
	defer rows.Close()

	out := make(map[string]json.RawMessage, 5)
	for rows.Next() {
		var source, data string
		var failure *string
		if err := rows.Scan(&source, &data, &failure); err != nil {
			return nil, fmt.Errorf("scan fact: %w", err)
		}
		if failure != nil {
			// Lỗi tra cứu hiển thị được, chứ không im lặng biến mất: người vận hành
			// cần phân biệt "chưa tra" với "tra rồi nhưng hỏng".
			msg, _ := json.Marshal(map[string]string{"error": *failure})
			out[source] = msg
			continue
		}
		out[source] = json.RawMessage(data)
	}
	return out, rows.Err()
}

// AnalysisGate là điều kiện lọc domain trước khi tải trang hoặc tra VirusTotal.
type AnalysisGate struct {
	Source string
	// MinQueries loại domain chỉ xuất hiện một hai lần do gõ nhầm.
	MinQueries int
	// MinScore chỉ áp cho VirusTotal: đừng tiêu quota cho domain chưa đáng ngờ.
	MinScore *float64
	// MaxNegativeScore loại domain đã được tín hiệu bảo vệ kéo xuống rất thấp; tra
	// thêm cũng không đổi kết luận, chỉ tốn một lần lộ diện.
	MaxNegativeScore float64
	Limit            int
}

// GatedCandidates lấy domain đủ điều kiện phân tích.
//
// Cổng lọc tồn tại vì mỗi lần tải trang là một lần để máy chủ đích biết mạng này
// đang soi nó. Chỉ tải những gì thật sự cần: domain đang chờ quyết định, có lưu
// lượng thật, và không nằm trong danh sách bảo vệ.
func (s *Store) GatedCandidates(ctx context.Context, g AnalysisGate) ([]EnrichCandidate, error) {
	limit := g.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}

	args := []any{g.Source, Now(), g.MinQueries, g.MaxNegativeScore}
	scoreClause := ""
	if g.MinScore != nil {
		scoreClause = " AND d.score >= ?"
		args = append(args, *g.MinScore)
	}
	args = append(args, limit)

	rows, err := s.r.QueryContext(ctx, `
		SELECT d.id, d.name, d.etld1
		FROM domains d
		LEFT JOIN domain_facts f ON f.domain_id = d.id AND f.source = ?
		WHERE (f.domain_id IS NULL OR f.expires_at <= ?)
		  AND d.status IN ('new','staging')
		  AND d.query_count >= ?
		  AND (d.score IS NULL OR d.score > ?)`+scoreClause+`
		ORDER BY d.query_count DESC
		LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("list gated candidates for %q: %w", g.Source, err)
	}
	defer rows.Close()

	var candidates []EnrichCandidate
	for rows.Next() {
		var c EnrichCandidate
		if err := rows.Scan(&c.ID, &c.Name, &c.ETLD1); err != nil {
			return nil, fmt.Errorf("scan gated candidate: %w", err)
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Danh sách bảo vệ lọc ở tầng Go: nó khớp theo hậu tố và có phần cứng nằm trong
	// mã nguồn, không diễn đạt được bằng SQL.
	soft, err := s.SoftAllowList(ctx)
	if err != nil {
		return nil, err
	}
	out := candidates[:0]
	for _, c := range candidates {
		if _, protected := classify.IsProtected(c.Name, soft); protected {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}
