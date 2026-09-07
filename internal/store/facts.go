package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// TTL cho từng nguồn làm giàu (docs/03-data-model.md §2).
var factTTL = map[string]time.Duration{
	"dns":  6 * time.Hour,       // CNAME và IP đổi thường xuyên
	"asn":  7 * 24 * time.Hour,  // ánh xạ IP→ASN ổn định
	"cert": 30 * 24 * time.Hour, // chứng chỉ đổi hiếm
	"rdap": 90 * 24 * time.Hour, // ngày đăng ký không đổi
	"rank": 7 * 24 * time.Hour,  // thứ hạng Tranco ổn định
}

// errorRetryAfter là TTL rút ngắn khi tra cứu thất bại: thử lại sớm, nhưng vẫn tôn
// trọng circuit breaker ở tầng enrich.
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
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode fact %q: %w", source, err)
	}
	ttl, ok := factTTL[source]
	if !ok {
		ttl = 24 * time.Hour
	}
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

// SaveFactError ghi lại một lần tra cứu thất bại với TTL ngắn.
func (s *Store) SaveFactError(ctx context.Context, domainID int64, source string, cause error) error {
	now := time.Now().UTC()
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO domain_facts (domain_id, source, data, fetched_at, expires_at, error)
		VALUES (?, ?, '{}', ?, ?, ?)
		ON CONFLICT (domain_id, source) DO UPDATE SET
		  fetched_at = excluded.fetched_at, expires_at = excluded.expires_at,
		  error = excluded.error`,
		domainID, source, TimeAt(now), TimeAt(now.Add(errorRetryAfter)), cause.Error())
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
