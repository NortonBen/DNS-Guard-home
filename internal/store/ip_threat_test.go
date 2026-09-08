package store

import (
	"context"
	"testing"
)

// seedIP gắn một địa chỉ vào một domain để có dòng cho job đối chiếu ghi lên.
func seedIP(t *testing.T, s *Store, domainID int64, ip string) {
	t.Helper()
	_, err := s.w.ExecContext(context.Background(), `
		INSERT INTO domain_ips (domain_id, ip, first_seen, last_seen, hits, ttl)
		VALUES (?, ?, '2026-09-01T00:00:00Z', '2026-09-08T00:00:00Z', 3, 300)`,
		domainID, ip)
	if err != nil {
		t.Fatalf("seed domain_ip %s: %v", ip, err)
	}
}

// Dải và nguồn là một cặp. Test này canh cả đường ghi lẫn đường đọc, vì giữa chúng có
// một cột mới, hai câu truy vấn mới, và một invariant chỉ được phát biểu trong chú thích.
func TestIPThreatRoundTripsSource(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	id := seedDomain(t, s, "evil.example.com", StatusNew)
	seedIP(t, s, id, "45.66.230.7")

	if _, err := s.SetIPThreats(ctx, []IPThreat{
		{IP: "45.66.230.7", Threat: "45.66.0.0/16", Source: "spamhaus_drop"},
	}); err != nil {
		t.Fatalf("SetIPThreats: %v", err)
	}

	state, err := s.IPThreatState(ctx)
	if err != nil {
		t.Fatalf("IPThreatState: %v", err)
	}
	got := state["45.66.230.7"]
	if got.Threat != "45.66.0.0/16" || got.Source != "spamhaus_drop" {
		t.Errorf("đọc lại = %q/%q; muốn 45.66.0.0/16/spamhaus_drop", got.Threat, got.Source)
	}

	matches, err := s.ThreatMatches(ctx, 0.35, 10)
	if err != nil {
		t.Fatalf("ThreatMatches: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("có %d cảnh báo, muốn 1", len(matches))
	}
	if matches[0].Source != "spamhaus_drop" {
		t.Errorf("cảnh báo mang nguồn %q; muốn spamhaus_drop", matches[0].Source)
	}

	ips, err := s.DomainIPs(ctx, id, 10)
	if err != nil {
		t.Fatalf("DomainIPs: %v", err)
	}
	if len(ips) != 1 || ips[0].ThreatSource != "spamhaus_drop" {
		t.Errorf("DomainIPs không mang nguồn: %+v", ips)
	}
}

// Xoá cảnh báo phải xoá *cả hai* cột. Còn sót nguồn thì hồ sơ điều tra sẽ khai một
// xuất xứ cho một địa chỉ không còn bị đánh dấu.
func TestIPThreatClearsBothColumns(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	id := seedDomain(t, s, "evil.example.com", StatusNew)
	seedIP(t, s, id, "45.66.230.7")

	if _, err := s.SetIPThreats(ctx, []IPThreat{
		{IP: "45.66.230.7", Threat: "45.66.0.0/16", Source: "spamhaus_drop"},
	}); err != nil {
		t.Fatalf("SetIPThreats: %v", err)
	}
	if _, err := s.SetIPThreats(ctx, []IPThreat{{IP: "45.66.230.7"}}); err != nil {
		t.Fatalf("SetIPThreats xoá: %v", err)
	}

	state, err := s.IPThreatState(ctx)
	if err != nil {
		t.Fatalf("IPThreatState: %v", err)
	}
	if got := state["45.66.230.7"]; got.Threat != "" || got.Source != "" {
		t.Errorf("sau khi xoá còn %q/%q; muốn rỗng cả hai", got.Threat, got.Source)
	}

	matches, err := s.ThreatMatches(ctx, 0.35, 10)
	if err != nil {
		t.Fatalf("ThreatMatches: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("còn %d cảnh báo sau khi xoá, muốn 0", len(matches))
	}
}

// Một dải không có nguồn là trạng thái nửa vời. Ghi nó không được để lại dòng mà
// ThreatMatches báo là cảnh báo nhưng không nói được xuất xứ.
func TestIPThreatRejectsHalfState(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	id := seedDomain(t, s, "evil.example.com", StatusNew)
	seedIP(t, s, id, "45.66.230.7")

	if _, err := s.SetIPThreats(ctx, []IPThreat{
		{IP: "45.66.230.7", Threat: "45.66.0.0/16"},
	}); err != nil {
		t.Fatalf("SetIPThreats: %v", err)
	}

	matches, err := s.ThreatMatches(ctx, 0.35, 10)
	if err != nil {
		t.Fatalf("ThreatMatches: %v", err)
	}
	for _, m := range matches {
		if m.Source == "" {
			t.Errorf("cảnh báo %s khớp %s nhưng không có nguồn", m.IP, m.Threat)
		}
	}
}
