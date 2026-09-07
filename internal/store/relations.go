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

// NetworkFilter là bộ lọc cho đồ thị toàn mạng.
type NetworkFilter struct {
	Kinds       []string
	Statuses    []string
	MinStrength float64
	// MinDegree loại các cặp đứng lẻ: một cạnh đơn độc không cho biết điều gì về cụm.
	MinDegree int
	Limit     int
}

// NetworkNode là một nút trong đồ thị toàn mạng.
type NetworkNode struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	ETLD1    string `json:"etld1"`
	Status   string `json:"status"`
	Category string `json:"category,omitempty"`
	// Degree là số quan hệ của nút. Giao diện vẽ nút to nhỏ theo giá trị này.
	Degree int `json:"degree"`
}

// NetworkGraph là ảnh chụp quan hệ của cả mạng.
type NetworkGraph struct {
	Nodes []NetworkNode `json:"nodes"`
	Edges []GraphEdge   `json:"edges"`
	// TotalNodes là số domain có quan hệ trước khi cắt bớt, để giao diện nói rõ đang
	// hiển thị bao nhiêu phần.
	TotalNodes int  `json:"total_nodes"`
	TotalEdges int  `json:"total_edges"`
	Truncated  bool `json:"truncated"`
}

// Network dựng đồ thị quan hệ của toàn mạng.
//
// Khác GraphFor ở chỗ không có nút gốc: câu hỏi ở đây không phải "domain này liên
// quan tới gì" mà "mạng này có những cụm hạ tầng nào". Vì thế cách chọn dữ liệu cũng
// khác — ưu tiên nút có nhiều quan hệ nhất, vì chúng là trung tâm của các cụm, còn
// nút chỉ có một cạnh thì nhìn vào cũng không thấy gì.
func (s *Store) Network(ctx context.Context, f NetworkFilter) (NetworkGraph, error) {
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 300
	}
	kinds := f.Kinds
	if len(kinds) == 0 {
		kinds = []string{RelCNAME, RelSameASN, RelSameCert, RelCoOccurs}
	}
	minDegree := max(f.MinDegree, 1)

	kindHolders := placeholders(len(kinds))
	kindArgs := make([]any, len(kinds))
	for i, k := range kinds {
		kindArgs[i] = k
	}

	statusClause := ""
	statusArgs := []any{}
	if len(f.Statuses) > 0 {
		statusClause = " AND d.status IN (" + placeholders(len(f.Statuses)) + ")"
		for _, st := range f.Statuses {
			statusArgs = append(statusArgs, st)
		}
	}

	// Bậc đếm quan hệ chạm vào domain theo *cả hai chiều*.
	//
	// Đếm riêng cột from_id thì hụt đúng thứ màn này sinh ra để tìm: cname_to là loại
	// duy nhất lưu một chiều, và một hub adtech có ba trăm domain trỏ CNAME vào sẽ ra
	// bậc bằng không từ những cạnh đó — tức là hub bị lọc mất ở ngay bộ lọc "số quan
	// hệ tối thiểu", trong khi nó chính là nút đáng xem nhất.
	//
	// UNION chứ không UNION ALL: ba loại vô hướng lưu sẵn cả hai chiều, nên gộp thẳng
	// sẽ đếm mỗi quan hệ hai lần và bậc của chúng phồng lên gấp đôi so với cname_to.
	args := append([]any{}, kindArgs...)
	args = append(args, f.MinStrength)
	args = append(args, kindArgs...)
	args = append(args, f.MinStrength)
	args = append(args, statusArgs...)
	args = append(args, minDegree, limit)

	rows, err := s.r.QueryContext(ctx, `
		WITH degree AS (
		  SELECT id, count(*) AS deg FROM (
		    SELECT from_id AS id, to_id AS other, kind FROM relations
		    WHERE kind IN (`+kindHolders+`) AND strength >= ?
		    UNION
		    SELECT to_id AS id, from_id AS other, kind FROM relations
		    WHERE kind IN (`+kindHolders+`) AND strength >= ?
		  )
		  GROUP BY id
		)
		SELECT d.id, d.name, d.etld1, d.status, coalesce(c.key, ''), degree.deg
		FROM degree
		JOIN domains d ON d.id = degree.id
		LEFT JOIN categories c ON c.id = d.category_id
		WHERE 1=1`+statusClause+`
		  AND degree.deg >= ?
		ORDER BY degree.deg DESC, d.query_count DESC
		LIMIT ?`, args...)
	if err != nil {
		return NetworkGraph{}, fmt.Errorf("list network nodes: %w", err)
	}
	defer rows.Close()

	var g NetworkGraph
	ids := []any{}
	for rows.Next() {
		var n NetworkNode
		if err := rows.Scan(&n.ID, &n.Name, &n.ETLD1, &n.Status, &n.Category, &n.Degree); err != nil {
			return NetworkGraph{}, fmt.Errorf("scan network node: %w", err)
		}
		g.Nodes = append(g.Nodes, n)
		ids = append(ids, n.ID)
	}
	if err := rows.Err(); err != nil {
		return NetworkGraph{}, err
	}
	if len(ids) == 0 {
		return g, nil
	}

	// Chỉ giữ cạnh mà cả hai đầu đều nằm trong tập nút đã chọn: cạnh trỏ ra ngoài tập
	// sẽ vẽ thành đường cụt không có đích.
	idHolders := placeholders(len(ids))
	edgeArgs := append([]any{}, kindArgs...)
	edgeArgs = append(edgeArgs, f.MinStrength)
	edgeArgs = append(edgeArgs, ids...)
	edgeArgs = append(edgeArgs, ids...)
	edgeArgs = append(edgeArgs, limit*8)

	edgeRows, err := s.r.QueryContext(ctx, `
		SELECT from_id, to_id, kind, strength
		FROM relations
		WHERE kind IN (`+kindHolders+`) AND strength >= ?
		  AND from_id IN (`+idHolders+`) AND to_id IN (`+idHolders+`)
		ORDER BY strength DESC
		LIMIT ?`, edgeArgs...)
	if err != nil {
		return NetworkGraph{}, fmt.Errorf("list network edges: %w", err)
	}
	defer edgeRows.Close()

	for edgeRows.Next() {
		var e GraphEdge
		if err := edgeRows.Scan(&e.From, &e.To, &e.Kind, &e.Strength); err != nil {
			return NetworkGraph{}, fmt.Errorf("scan network edge: %w", err)
		}
		g.Edges = append(g.Edges, e)
	}
	if err := edgeRows.Err(); err != nil {
		return NetworkGraph{}, err
	}

	// Tổng số thật, để giao diện nói rõ đang hiển thị bao nhiêu phần thay vì im lặng
	// cắt bớt.
	countArgs := append([]any{}, kindArgs...)
	countArgs = append(countArgs, f.MinStrength, minDegree)
	if err := s.r.QueryRowContext(ctx, `
		SELECT count(*) FROM (
		  SELECT from_id FROM relations
		  WHERE kind IN (`+kindHolders+`) AND strength >= ?
		  GROUP BY from_id HAVING count(*) >= ?
		)`, countArgs...).Scan(&g.TotalNodes); err != nil {
		return NetworkGraph{}, fmt.Errorf("count network nodes: %w", err)
	}

	totalEdgeArgs := append([]any{}, kindArgs...)
	totalEdgeArgs = append(totalEdgeArgs, f.MinStrength)
	if err := s.r.QueryRowContext(ctx, `
		SELECT count(*) FROM relations WHERE kind IN (`+kindHolders+`) AND strength >= ?`,
		totalEdgeArgs...).Scan(&g.TotalEdges); err != nil {
		return NetworkGraph{}, fmt.Errorf("count network edges: %w", err)
	}

	g.Truncated = g.TotalNodes > len(g.Nodes)
	return g, nil
}
