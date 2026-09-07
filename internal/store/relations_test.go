package store

import (
	"context"
	"testing"
)

// seedRelatedDomains dựng một cụm: hub nối tới các thành viên, cộng một cặp lẻ đứng
// riêng để kiểm tra bộ lọc bậc tối thiểu.
func seedRelatedDomains(t *testing.T, s *Store) (hubID int64) {
	t.Helper()
	ctx := context.Background()
	now := Now()

	id := func(name, status string) int64 {
		res, err := s.Writer().Exec(`
			INSERT INTO domains (name, name_rev, etld1, status, origin, query_count,
			                     first_seen, last_seen, created_at, updated_at)
			VALUES (?, ?, ?, ?, 'discovered', 10, ?, ?, ?, ?)`,
			name, reverseName(name), etld1Of(name), status, now, now, now, now)
		if err != nil {
			t.Fatalf("thêm domain %q: %v", name, err)
		}
		v, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("id domain: %v", err)
		}
		return v
	}

	hub := id("x.adtech.net", "blocked")
	for _, name := range []string{"a.trangweb.vn", "b.trangweb.vn", "c.baomoi.vn"} {
		member := id(name, "staging")
		if err := s.SaveRelation(ctx, member, hub, RelCNAME, 1.0, nil); err != nil {
			t.Fatalf("lưu quan hệ: %v", err)
		}
		if err := s.SaveRelation(ctx, hub, member, RelSameASN, 1.0, nil); err != nil {
			t.Fatalf("lưu quan hệ: %v", err)
		}
	}

	// Cặp lẻ: mỗi nút chỉ có một quan hệ.
	lone1 := id("p.congty.vn", "new")
	lone2 := id("q.congty.vn", "new")
	if err := s.SaveRelation(ctx, lone1, lone2, RelCoOccurs, 0.4, nil); err != nil {
		t.Fatalf("lưu quan hệ lẻ: %v", err)
	}
	return hub
}

func TestNetworkReturnsClusterAndCountsDegree(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	hubID := seedRelatedDomains(t, s)

	g, err := s.Network(ctx, NetworkFilter{MinDegree: 1, Limit: 100})
	if err != nil {
		t.Fatalf("Network: %v", err)
	}

	if len(g.Nodes) == 0 {
		t.Fatal("không trả về nút nào")
	}

	// Hub phải đứng đầu: sắp theo bậc giảm dần chính là thứ làm cụm hạ tầng nổi lên.
	if g.Nodes[0].ID != hubID {
		t.Errorf("nút đầu = %q, muốn hub x.adtech.net", g.Nodes[0].Name)
	}
	if g.Nodes[0].Degree != 3 {
		t.Errorf("bậc của hub = %d, muốn 3", g.Nodes[0].Degree)
	}

	// Mọi cạnh phải có cả hai đầu nằm trong tập nút trả về; cạnh trỏ ra ngoài sẽ vẽ
	// thành đường cụt không có đích.
	ids := map[int64]bool{}
	for _, n := range g.Nodes {
		ids[n.ID] = true
	}
	for _, e := range g.Edges {
		if !ids[e.From] || !ids[e.To] {
			t.Errorf("cạnh %d→%d có đầu nằm ngoài tập nút", e.From, e.To)
		}
	}
}

// Bậc tối thiểu loại các cặp đứng lẻ.
func TestNetworkMinDegreeDropsLonePairs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedRelatedDomains(t, s)

	g, err := s.Network(ctx, NetworkFilter{MinDegree: 2, Limit: 100})
	if err != nil {
		t.Fatalf("Network: %v", err)
	}
	for _, n := range g.Nodes {
		if n.Name == "p.congty.vn" || n.Name == "q.congty.vn" {
			t.Errorf("%s chỉ có một quan hệ nhưng vẫn lọt qua bậc tối thiểu 2", n.Name)
		}
		if n.Degree < 2 {
			t.Errorf("%s có bậc %d, dưới ngưỡng đã đặt", n.Name, n.Degree)
		}
	}
}

// Lọc theo loại quan hệ phải đổi cả tập nút lẫn tập cạnh.
func TestNetworkFiltersByKind(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedRelatedDomains(t, s)

	g, err := s.Network(ctx, NetworkFilter{Kinds: []string{RelCNAME}, MinDegree: 1, Limit: 100})
	if err != nil {
		t.Fatalf("Network: %v", err)
	}
	for _, e := range g.Edges {
		if e.Kind != RelCNAME {
			t.Errorf("cạnh loại %q lọt qua bộ lọc chỉ lấy cname_to", e.Kind)
		}
	}
}

// Cắt bớt phải được báo ra, không im lặng.
func TestNetworkReportsTruncation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedRelatedDomains(t, s)

	g, err := s.Network(ctx, NetworkFilter{MinDegree: 1, Limit: 2})
	if err != nil {
		t.Fatalf("Network: %v", err)
	}
	if len(g.Nodes) != 2 {
		t.Fatalf("trả về %d nút, muốn đúng 2 theo giới hạn", len(g.Nodes))
	}
	if !g.Truncated {
		t.Error("đã cắt bớt nhưng không báo Truncated")
	}
	if g.TotalNodes <= len(g.Nodes) {
		t.Errorf("TotalNodes = %d, phải lớn hơn số nút trả về", g.TotalNodes)
	}
}
