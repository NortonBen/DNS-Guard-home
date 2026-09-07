package store

import (
	"context"
	"encoding/json"
	"fmt"
)

// Loại quan hệ giữa các domain.
const (
	RelCNAME    = "cname_to"
	RelSameASN  = "same_asn"
	RelSameCert = "same_cert"
	RelCoOccurs = "co_occurs"
)

// GraphNode là một nút trong đồ thị quan hệ.
type GraphNode struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Category string `json:"category,omitempty"`
	Root     bool   `json:"root,omitempty"`
}

// GraphEdge là một cạnh trong đồ thị quan hệ.
type GraphEdge struct {
	From     int64   `json:"from"`
	To       int64   `json:"to"`
	Kind     string  `json:"kind"`
	Strength float64 `json:"strength"`
}

// Graph là kết quả trả về cho trang chi tiết domain.
type Graph struct {
	Nodes     []GraphNode `json:"nodes"`
	Edges     []GraphEdge `json:"edges"`
	Truncated bool        `json:"truncated"`
}

// SaveRelation ghi một cạnh. Ba loại vô hướng lưu cả hai chiều để truy vấn đọc chỉ
// cần nhìn một cột — đánh đổi dung lượng lấy tốc độ đọc, hợp lý vì đọc nhiều hơn
// ghi rất nhiều.
func (s *Store) SaveRelation(ctx context.Context, from, to int64, kind string, strength float64, detail any) error {
	if from == to {
		return nil
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("encode relation detail: %w", err)
	}

	pairs := [][2]int64{{from, to}}
	if kind != RelCNAME {
		pairs = append(pairs, [2]int64{to, from})
	}

	for _, p := range pairs {
		if _, err := s.w.ExecContext(ctx, `
			INSERT INTO relations (from_id, to_id, kind, strength, detail, computed_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (from_id, to_id, kind) DO UPDATE SET
			  strength = excluded.strength, detail = excluded.detail,
			  computed_at = excluded.computed_at`,
			p[0], p[1], kind, strength, string(raw), Now()); err != nil {
			return fmt.Errorf("save relation %s: %w", kind, err)
		}
	}
	return nil
}

// GraphFor dựng đồ thị quan hệ quanh một domain.
//
// Tách riêng khỏi endpoint chi tiết vì tốn kém và không phải lúc nào cũng cần.
func (s *Store) GraphFor(ctx context.Context, rootID int64, kinds []string, minStrength float64, limit int) (Graph, error) {
	if limit <= 0 || limit > 300 {
		limit = 60
	}
	if len(kinds) == 0 {
		kinds = []string{RelCNAME, RelSameASN, RelSameCert, RelCoOccurs}
	}

	args := []any{rootID}
	for _, k := range kinds {
		args = append(args, k)
	}
	args = append(args, minStrength, limit+1)

	rows, err := s.r.QueryContext(ctx, `
		SELECT r.from_id, r.to_id, r.kind, r.strength,
		       d.name, d.status, coalesce(c.key, '')
		FROM relations r
		JOIN domains d ON d.id = r.to_id
		LEFT JOIN categories c ON c.id = d.category_id
		WHERE r.from_id = ? AND r.kind IN (`+placeholders(len(kinds))+`) AND r.strength >= ?
		ORDER BY r.strength DESC
		LIMIT ?`, args...)
	if err != nil {
		return Graph{}, fmt.Errorf("list relations: %w", err)
	}
	defer rows.Close()

	g := Graph{}
	seen := map[int64]bool{}

	for rows.Next() {
		var e GraphEdge
		var n GraphNode
		if err := rows.Scan(&e.From, &e.To, &e.Kind, &e.Strength,
			&n.Name, &n.Status, &n.Category); err != nil {
			return Graph{}, fmt.Errorf("scan relation: %w", err)
		}
		n.ID = e.To
		if len(g.Edges) >= limit {
			// Cắt bớt phải nói ra chứ không im lặng: giao diện hiển thị
			// "60 / 143 nút liên quan" kèm nút mở rộng.
			g.Truncated = true
			break
		}
		g.Edges = append(g.Edges, e)
		if !seen[n.ID] {
			seen[n.ID] = true
			g.Nodes = append(g.Nodes, n)
		}
	}
	if err := rows.Err(); err != nil {
		return Graph{}, err
	}

	root, err := s.GetDomain(ctx, rootID)
	if err != nil {
		return Graph{}, err
	}
	rootNode := GraphNode{ID: root.ID, Name: root.Name, Status: root.Status, Root: true}
	if root.Category != nil {
		rootNode.Category = root.Category.Key
	}
	g.Nodes = append([]GraphNode{rootNode}, g.Nodes...)

	return g, nil
}

// ClearRelations xóa toàn bộ cạnh của một loại, chuẩn bị tính lại theo lô.
func (s *Store) ClearRelations(ctx context.Context, kind string) error {
	if _, err := s.w.ExecContext(ctx, `DELETE FROM relations WHERE kind = ?`, kind); err != nil {
		return fmt.Errorf("clear relations %q: %w", kind, err)
	}
	return nil
}

// CoOccurrencePair là số lần hai domain xuất hiện gần nhau.
type CoOccurrencePair struct {
	A     int64
	B     int64
	Count int
}

// CoOccurrences đếm các cặp domain được cùng một client truy vấn cách nhau dưới ba
// giây, kèm tổng số lần xuất hiện của từng domain để tầng trên tính PMI.
//
// Chỉ xét các domain có lưu lượng đáng kể: ghép cặp là phép toán bậc hai, và những
// domain chỉ xuất hiện vài lần không đủ dữ liệu để nói lên điều gì.
func (s *Store) CoOccurrences(ctx context.Context, windowDays, minCount int) (
	pairs []CoOccurrencePair, totals map[int64]int, grand int, err error) {

	if minCount <= 0 {
		minCount = 3
	}
	since := cutoff(windowDays)

	rows, err := s.r.QueryContext(ctx, `
		WITH busy AS (
		  SELECT domain_id FROM query_events
		  WHERE occurred_at >= ?
		  GROUP BY domain_id HAVING count(*) >= 5
		  LIMIT 3000
		),
		recent AS (
		  SELECT e.domain_id, e.client_id, e.occurred_at
		  FROM query_events e
		  WHERE e.occurred_at >= ? AND e.domain_id IN (SELECT domain_id FROM busy)
		)
		SELECT min(a.domain_id, b.domain_id) AS lo,
		       max(a.domain_id, b.domain_id) AS hi,
		       count(*) AS n
		FROM recent a JOIN recent b
		  ON a.client_id = b.client_id
		 AND a.domain_id < b.domain_id
		 AND b.occurred_at > a.occurred_at
		 AND b.occurred_at <= strftime('%Y-%m-%dT%H:%M:%SZ', a.occurred_at, '+3 seconds')
		GROUP BY lo, hi
		HAVING n >= ?
		ORDER BY n DESC
		LIMIT 20000`, since, since, minCount)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("count co-occurrences: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var p CoOccurrencePair
		if err := rows.Scan(&p.A, &p.B, &p.Count); err != nil {
			return nil, nil, 0, fmt.Errorf("scan co-occurrence: %w", err)
		}
		pairs = append(pairs, p)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, 0, err
	}

	totalRows, err := s.r.QueryContext(ctx, `
		SELECT domain_id, count(*) FROM query_events
		WHERE occurred_at >= ?
		GROUP BY domain_id LIMIT 20000`, since)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("count domain totals: %w", err)
	}
	defer totalRows.Close()

	totals = make(map[int64]int, 1024)
	for totalRows.Next() {
		var id int64
		var n int
		if err := totalRows.Scan(&id, &n); err != nil {
			return nil, nil, 0, fmt.Errorf("scan domain total: %w", err)
		}
		totals[id] = n
		grand += n
	}
	return pairs, totals, grand, totalRows.Err()
}
