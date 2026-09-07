package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/benji/dnsguard/internal/classify"
)

// cutoff trả về mốc thời gian cách hiện tại days ngày, đúng định dạng CSDL dùng.
//
// Không dùng datetime('now', '-N days') của SQLite: hàm đó trả về
// 'YYYY-MM-DD HH:MM:SS' trong khi CSDL lưu 'YYYY-MM-DDTHH:MM:SSZ'. Ký tự 'T' lớn
// hơn dấu cách trong bảng mã, nên phép so sánh giữa hai định dạng sẽ luôn sai mà
// không báo lỗi — job canary sẽ im lặng không bao giờ chạy.
func cutoff(days int) string {
	return TimeAt(time.Now().Add(-time.Duration(days) * 24 * time.Hour))
}

// ScoringCandidate là đầu vào đã gom đủ cho một lần chấm điểm.
type ScoringCandidate struct {
	ID     int64
	Domain classify.Domain
	Facts  classify.Facts
	Status string
}

// ScoringCandidates lấy các domain cần chấm điểm.
//
// Bất biến 1: mệnh đề is_manual = 0 nằm ngay trong truy vấn chứ không ở tầng ứng
// dụng. Con người thắng máy, và điều đó được thực thi ở chỗ khó bỏ sót nhất.
func (s *Store) ScoringCandidates(ctx context.Context, limit int) ([]ScoringCandidate, error) {
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	rows, err := s.r.QueryContext(ctx, `
		SELECT d.id, d.name, d.etld1, d.status, d.query_count, d.client_count,
		       (SELECT count(*) FROM domains sub WHERE sub.etld1 = d.etld1) AS subdomains,
		       EXISTS (SELECT 1 FROM list_entries le WHERE le.domain = d.name)  AS in_list,
		       EXISTS (SELECT 1 FROM list_entries le WHERE le.domain = d.etld1) AS etld1_in_list
		FROM domains d
		WHERE d.is_manual = 0
		  AND d.status IN ('new','staging')
		ORDER BY d.query_count DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list scoring candidates: %w", err)
	}
	defer rows.Close()

	var out []ScoringCandidate
	for rows.Next() {
		var c ScoringCandidate
		if err := rows.Scan(&c.ID, &c.Domain.Name, &c.Domain.ETLD1, &c.Status,
			&c.Domain.QueryCount, &c.Domain.ClientCount, &c.Domain.SubdomainCount,
			&c.Facts.InPublicList, &c.Facts.ETLD1InPublicList); err != nil {
			return nil, fmt.Errorf("scan scoring candidate: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range out {
		if err := s.loadFactsInto(ctx, out[i].ID, &out[i].Facts); err != nil {
			return nil, err
		}
		if err := s.loadBehaviorInto(ctx, out[i].ID, &out[i].Domain); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// loadFactsInto nạp kết quả làm giàu còn hạn vào Facts.
func (s *Store) loadFactsInto(ctx context.Context, domainID int64, f *classify.Facts) error {
	rows, err := s.r.QueryContext(ctx, `
		SELECT source, data FROM domain_facts
		WHERE domain_id = ? AND error IS NULL AND expires_at > ?
		LIMIT 10`, domainID, Now())
	if err != nil {
		return fmt.Errorf("load facts: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var source, data string
		if err := rows.Scan(&source, &data); err != nil {
			return fmt.Errorf("scan fact: %w", err)
		}
		if err := mergeFact(source, data, f); err != nil {
			return err
		}
	}
	return rows.Err()
}

// mergeFact giải mã một bản ghi làm giàu vào Facts. Mỗi nguồn có cấu trúc riêng,
// nên lưu JSONB và giải mã theo nguồn thay vì ép chung một lược đồ.
func mergeFact(source, data string, f *classify.Facts) error {
	switch source {
	case "dns":
		var v struct {
			CNAMEChain []string `json:"cname_chain"`
			IPs        []string `json:"a"`
		}
		if err := json.Unmarshal([]byte(data), &v); err != nil {
			return fmt.Errorf("decode dns fact: %w", err)
		}
		f.CNAMEChain, f.IPs = v.CNAMEChain, v.IPs
	case "asn":
		var v struct {
			ASN     int    `json:"asn"`
			Org     string `json:"org"`
			Country string `json:"country"`
		}
		if err := json.Unmarshal([]byte(data), &v); err != nil {
			return fmt.Errorf("decode asn fact: %w", err)
		}
		f.ASN, f.ASNOrg, f.ASNCountry = v.ASN, v.Org, v.Country
	case "cert":
		var v struct {
			Issuer string   `json:"issuer"`
			SANs   []string `json:"sans"`
		}
		if err := json.Unmarshal([]byte(data), &v); err != nil {
			return fmt.Errorf("decode cert fact: %w", err)
		}
		f.CertIssuer, f.CertSANs = v.Issuer, v.SANs
	case "rdap":
		var v struct {
			RegisteredAt string `json:"registered_at"`
			AgeDays      int    `json:"age_days"`
		}
		if err := json.Unmarshal([]byte(data), &v); err != nil {
			return fmt.Errorf("decode rdap fact: %w", err)
		}
		f.RegisteredAt, f.AgeDays = v.RegisteredAt, v.AgeDays
	case "rank":
		var v struct {
			Tranco int `json:"tranco"`
		}
		if err := json.Unmarshal([]byte(data), &v); err != nil {
			return fmt.Errorf("decode rank fact: %w", err)
		}
		f.TrancoRank = v.Tranco
	}
	return nil
}

// loadBehaviorInto nạp các đặc trưng hành vi tính từ log.
func (s *Store) loadBehaviorInto(ctx context.Context, domainID int64, d *classify.Domain) error {
	var ratio, cv sql.NullFloat64
	err := s.r.QueryRowContext(ctx, `
		SELECT third_party_ratio, interval_cv FROM domain_behavior WHERE domain_id = ?`,
		domainID).Scan(&ratio, &cv)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load behavior: %w", err)
	}
	d.ThirdPartyRatio, d.QueryIntervalCV = ratio.Float64, cv.Float64
	return nil
}

// SaveScore ghi kết quả chấm điểm và đưa domain vào staging nếu vượt ngưỡng.
//
// Ngưỡng lấy theo từng phân loại: malware chặn ở 3,0 trong khi ads chặn ở 5,5, vì
// hậu quả bỏ lọt khác nhau.
func (s *Store) SaveScore(ctx context.Context, domainID int64, r classify.Result) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save score: %w", err)
	}
	defer tx.Rollback()

	// Kiểm tra lại is_manual bên trong transaction: một quyết định thủ công có thể
	// vừa xảy ra giữa lúc chấm điểm và lúc ghi.
	var isManual bool
	var status string
	err = tx.QueryRowContext(ctx,
		`SELECT is_manual, status FROM domains WHERE id = ?`, domainID).Scan(&isManual, &status)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("load domain %d: %w", domainID, err)
	}
	if isManual {
		return nil // bất biến 1
	}

	signals := make([]Signal, len(r.Signals))
	for i, sig := range r.Signals {
		detail, err := json.Marshal(sig.Detail)
		if err != nil {
			return fmt.Errorf("encode signal detail: %w", err)
		}
		signals[i] = Signal{Kind: sig.Kind, Weight: sig.Weight, Detail: detail}
	}
	if err := s.ReplaceSignals(ctx, tx, domainID, signals); err != nil {
		return err
	}

	now := Now()
	if _, err := tx.ExecContext(ctx, `
		UPDATE domains SET
		  score       = ?,
		  confidence  = ?,
		  category_id = (SELECT id FROM categories WHERE key = ?),
		  scored_at   = ?,
		  updated_at  = ?
		WHERE id = ?`,
		r.Score, r.Confidence, string(r.Category), now, now, domainID); err != nil {
		return fmt.Errorf("update score: %w", err)
	}

	var threshold float64
	var enabled bool
	err = tx.QueryRowContext(ctx,
		`SELECT score_threshold, enabled FROM categories WHERE key = ?`,
		string(r.Category)).Scan(&threshold, &enabled)
	if err != nil {
		return fmt.Errorf("load threshold for %q: %w", r.Category, err)
	}

	switch {
	case enabled && r.Score >= threshold && status == StatusNew:
		if err := transition(ctx, tx, domainID, ActionStage, StatusStaging,
			fmt.Sprintf("điểm %.1f ≥ ngưỡng %.1f", r.Score, threshold), r); err != nil {
			return err
		}
	case status == StatusStaging && r.Score < threshold:
		// Điểm tụt lại dưới ngưỡng: trả về new thay vì chặn.
		if err := transition(ctx, tx, domainID, ActionExpire, StatusNew,
			fmt.Sprintf("điểm %.1f < ngưỡng %.1f", r.Score, threshold), r); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit save score: %w", err)
	}
	return nil
}

// transition đổi trạng thái và ghi đúng một dòng nhật ký (bất biến 3).
func transition(ctx context.Context, tx *sql.Tx, domainID int64,
	action, newStatus, reason string, r classify.Result) error {

	snapshot, err := json.Marshal(map[string]any{
		"score":      r.Score,
		"confidence": r.Confidence,
		"category":   string(r.Category),
		"signals":    r.Signals,
	})
	if err != nil {
		return fmt.Errorf("encode snapshot: %w", err)
	}

	now := Now()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO decisions (domain_id, action, actor_id, actor_label, reason, snapshot, created_at)
		VALUES (?, ?, NULL, ?, ?, ?, ?)`,
		domainID, action, ActorSystem, reason, string(snapshot), now); err != nil {
		return fmt.Errorf("insert system decision: %w", err)
	}

	var stagedAt, blockedAt any
	if newStatus == StatusStaging {
		stagedAt = now
	}
	if newStatus == StatusBlocked {
		blockedAt = now
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE domains SET status = ?, staged_at = coalesce(?, staged_at),
		                   blocked_at = coalesce(?, blocked_at), updated_at = ?
		WHERE id = ? AND is_manual = 0`,
		newStatus, stagedAt, blockedAt, now, domainID); err != nil {
		return fmt.Errorf("apply transition: %w", err)
	}
	return nil
}

// PromoteStaging đưa các domain đã ở staging đủ lâu sang blocked.
//
// Giai đoạn staging là chi phí có chủ ý: chậm bảy ngày với domain thật sự xấu, đổi
// lấy việc không bao giờ chặn nhầm một cách âm thầm. Với ad blocking, đánh đổi này
// đúng chiều.
func (s *Store) PromoteStaging(ctx context.Context, days int) (int, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT d.id, d.name, d.score, coalesce(c.key, 'ads')
		FROM domains d LEFT JOIN categories c ON c.id = d.category_id
		WHERE d.status = 'staging' AND d.is_manual = 0
		  AND d.staged_at IS NOT NULL
		  AND d.staged_at <= ?
		LIMIT 5000`, cutoff(days))
	if err != nil {
		return 0, fmt.Errorf("list staged domains: %w", err)
	}
	defer rows.Close()

	type candidate struct {
		id       int64
		name     string
		score    sql.NullFloat64
		category string
	}
	var due []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.name, &c.score, &c.category); err != nil {
			return 0, fmt.Errorf("scan staged domain: %w", err)
		}
		due = append(due, c)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	soft, err := s.SoftAllowList(ctx)
	if err != nil {
		return 0, err
	}

	promoted := 0
	for _, c := range due {
		// Bất biến 4 áp dụng cả cho quyết định tự động: danh sách bảo vệ có thể đã
		// thay đổi từ lúc domain vào staging.
		if _, protected := classify.IsProtected(c.name, soft); protected {
			continue
		}
		r := classify.Result{Score: c.score.Float64, Category: classify.Category(c.category)}
		if err := s.systemTransition(ctx, c.id, ActionBlock, StatusBlocked,
			fmt.Sprintf("đủ %d ngày canary ở staging", days), r); err != nil {
			return promoted, err
		}
		promoted++
	}
	return promoted, nil
}

// ExpireBlocked gỡ các domain đã chặn nhưng im lặng quá lâu.
//
// Ngưỡng ở đây dài hơn nhiều so với staging, và sự bất đối xứng là chủ ý. Ở kiến
// trúc mà thiết bị chặn nằm trước điểm quan sát, domain bị chặn sẽ biến mất khỏi
// log; hiểu "không thấy" là "đã chết" sẽ tạo ra dao động vĩnh viễn: gỡ chặn → xuất
// hiện lại → vào staging → chặn lại, và mỗi chu kỳ có một khoảng quảng cáo lọt qua.
// Ở staging, biến mất nghĩa là domain ngừng hoạt động; ở blocked, biến mất là bằng
// chứng hệ thống đang làm đúng việc.
func (s *Store) ExpireBlocked(ctx context.Context, days int) (int, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT id FROM domains
		WHERE status = 'blocked' AND is_manual = 0
		  AND last_seen <= ?
		LIMIT 5000`, cutoff(days))
	if err != nil {
		return 0, fmt.Errorf("list expired domains: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return 0, fmt.Errorf("scan expired domain: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, id := range ids {
		if err := s.systemTransition(ctx, id, ActionExpire, StatusNew,
			fmt.Sprintf("không xuất hiện trong %d ngày", days), classify.Result{}); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

func (s *Store) systemTransition(ctx context.Context, domainID int64,
	action, newStatus, reason string, r classify.Result) error {

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transition: %w", err)
	}
	defer tx.Rollback()

	if err := transition(ctx, tx, domainID, action, newStatus, reason, r); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transition: %w", err)
	}
	return nil
}
