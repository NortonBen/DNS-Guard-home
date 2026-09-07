package store

import (
	"context"
	"fmt"
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
	// Sáu chứ không phải ba: mỗi thành viên nối với hub bằng hai quan hệ khác loại —
	// một cname_to đi vào và một same_asn. Con số cũ là hệ quả của việc chỉ đếm chiều
	// đi ra, và chính nó giấu mất các hub khỏi bộ lọc bậc tối thiểu.
	if g.Nodes[0].Degree != 6 {
		t.Errorf("bậc của hub = %d, muốn 6 (3 cname_to đi vào + 3 same_asn)", g.Nodes[0].Degree)
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

func TestNetworkDegreeCountsIncomingCNAMEs(t *testing.T) {
	// Hub adtech là nút đáng xem nhất trên bản đồ toàn mạng, và nó lộ ra qua số domain
	// trỏ CNAME *vào* nó. Đếm riêng chiều đi ra thì hub ra bậc 0 và bị chính bộ lọc
	// "số quan hệ tối thiểu" giấu đi.
	s := newTestStore(t)
	ctx := context.Background()

	hub := seedNamedDomain(t, s, "quangcaonoidia.vn")
	for i := range 5 {
		sub := seedNamedDomain(t, s, fmt.Sprintf("px%d.trangbao.vn", i))
		if err := s.SaveRelation(ctx, sub, hub, RelCNAME, 1.0, nil); err != nil {
			t.Fatalf("lưu quan hệ: %v", err)
		}
	}

	g, err := s.Network(ctx, NetworkFilter{Limit: 50, MinDegree: 2})
	if err != nil {
		t.Fatalf("network: %v", err)
	}

	var hubDegree int
	for _, n := range g.Nodes {
		if n.ID == hub {
			hubDegree = n.Degree
		}
	}
	if hubDegree != 5 {
		t.Errorf("bậc của hub = %d, muốn 5 (năm domain trỏ CNAME vào)", hubDegree)
	}
}

func TestNetworkDegreeDoesNotDoubleCountUndirectedKinds(t *testing.T) {
	// same_asn, same_cert và co_occurs lưu sẵn cả hai chiều. Gộp hai chiều mà không
	// khử trùng thì bậc của chúng phồng gấp đôi, và thang so sánh với cname_to hỏng.
	s := newTestStore(t)
	ctx := context.Background()

	a := seedNamedDomain(t, s, "mot.vn")
	b := seedNamedDomain(t, s, "hai.vn")
	c := seedNamedDomain(t, s, "ba.vn")

	for _, peer := range []int64{b, c} {
		if err := s.SaveRelation(ctx, a, peer, RelSameASN, 1.0, nil); err != nil {
			t.Fatalf("lưu quan hệ: %v", err)
		}
	}

	g, err := s.Network(ctx, NetworkFilter{Limit: 50, MinDegree: 1})
	if err != nil {
		t.Fatalf("network: %v", err)
	}

	for _, n := range g.Nodes {
		if n.ID == a && n.Degree != 2 {
			t.Errorf("bậc = %d, muốn 2 (hai hàng xóm, không phải bốn)", n.Degree)
		}
	}
}

// seedNamedDomain tạo một domain tối thiểu và trả về id.
func seedNamedDomain(t *testing.T, s *Store, name string) int64 {
	t.Helper()
	now := Now()
	res, err := s.Writer().Exec(`
		INSERT INTO domains (name, name_rev, etld1, status, origin,
		                     first_seen, last_seen, created_at, updated_at)
		VALUES (?, ?, ?, 'new', 'discovered', ?, ?, ?, ?)`,
		name, name, name, now, now, now, now)
	if err != nil {
		t.Fatalf("tạo domain %q: %v", name, err)
	}
	id, _ := res.LastInsertId()
	return id
}
