package store

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/benji/dnsguard/internal/ingest"
)

func addrs(t *testing.T, ss ...string) []netip.Addr {
	t.Helper()
	out := make([]netip.Addr, len(ss))
	for i, s := range ss {
		out[i] = netip.MustParseAddr(s)
	}
	return out
}

func TestWriteResolutionsRecordsAddresses(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	base := time.Date(2026, 9, 7, 8, 15, 0, 0, time.UTC)

	if err := s.WriteEvents(ctx, []ingest.Event{
		{Domain: "ads.example.com", ETLD1: "example.com", Client: "192.168.88.20", QType: ingest.TypeA, At: base},
	}); err != nil {
		t.Fatalf("WriteEvents: %v", err)
	}

	if err := s.WriteResolutions(ctx, []ingest.Resolution{{
		Domain: "ads.example.com", ETLD1: "example.com",
		IPs: addrs(t, "104.21.5.6", "2606:4700::1111"), TTL: 300, At: base.Add(time.Second),
	}}); err != nil {
		t.Fatalf("WriteResolutions: %v", err)
	}

	var id int64
	if err := s.Reader().QueryRow(`SELECT id FROM domains WHERE name = ?`, "ads.example.com").Scan(&id); err != nil {
		t.Fatalf("tra domain: %v", err)
	}
	ips, err := s.DomainIPs(ctx, id, 10)
	if err != nil {
		t.Fatalf("DomainIPs: %v", err)
	}
	if len(ips) != 2 {
		t.Fatalf("có %d địa chỉ, muốn 2", len(ips))
	}
	for _, ip := range ips {
		if ip.Hits != 1 || ip.TTL != 300 {
			t.Errorf("%s: hits = %d, ttl = %d; muốn 1 và 300", ip.IP, ip.Hits, ip.TTL)
		}
	}
}

// Bất biến quan trọng nhất của đường ghi này: một câu trả lời không phải một lượt
// truy cập. Cộng nó vào query_count sẽ đếm đôi mọi truy vấn, mà điểm quyết định
// chặn hay không lại tính từ chính con số đó.
func TestWriteResolutionsDoesNotCountAsQuery(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	base := time.Date(2026, 9, 7, 8, 15, 0, 0, time.UTC)

	if err := s.WriteEvents(ctx, []ingest.Event{
		{Domain: "ads.example.com", ETLD1: "example.com", Client: "192.168.88.20", QType: ingest.TypeA, At: base},
	}); err != nil {
		t.Fatalf("WriteEvents: %v", err)
	}

	var before int64
	var lastSeenBefore string
	s.Reader().QueryRow(`SELECT query_count, last_seen FROM domains WHERE name = ?`,
		"ads.example.com").Scan(&before, &lastSeenBefore)

	for range 5 {
		if err := s.WriteResolutions(ctx, []ingest.Resolution{{
			Domain: "ads.example.com", ETLD1: "example.com",
			IPs: addrs(t, "104.21.5.6"), TTL: 300, At: base.Add(time.Hour),
		}}); err != nil {
			t.Fatalf("WriteResolutions: %v", err)
		}
	}

	var after int64
	var lastSeenAfter string
	s.Reader().QueryRow(`SELECT query_count, last_seen FROM domains WHERE name = ?`,
		"ads.example.com").Scan(&after, &lastSeenAfter)

	if after != before {
		t.Errorf("query_count = %d sau khi ghi phân giải, muốn giữ nguyên %d", after, before)
	}
	if lastSeenAfter != lastSeenBefore {
		t.Errorf("last_seen bị đổi thành %q, muốn giữ %q", lastSeenAfter, lastSeenBefore)
	}
}

// Câu trả lời cho domain chưa từng thấy không được tạo domain mới: nó sẽ hiện ra ở
// màn Triage như một ứng viên có 0 lượt truy vấn.
func TestWriteResolutionsIgnoresUnknownDomain(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.WriteResolutions(ctx, []ingest.Resolution{{
		Domain: "chua-tung-thay.example.com", ETLD1: "example.com",
		IPs: addrs(t, "203.0.113.1"), TTL: 60, At: time.Now().UTC(),
	}}); err != nil {
		t.Fatalf("WriteResolutions: %v", err)
	}

	var domains, rows int
	s.Reader().QueryRow(`SELECT count(*) FROM domains`).Scan(&domains)
	s.Reader().QueryRow(`SELECT count(*) FROM domain_ips`).Scan(&rows)
	if domains != 0 || rows != 0 {
		t.Errorf("tạo ra %d domain và %d dòng domain_ips, muốn 0 và 0", domains, rows)
	}
}

// Thấy lại cùng một ánh xạ thì cộng dồn chứ không tạo dòng mới, và TTL giữ giá trị
// nhỏ nhất từng thấy.
func TestWriteResolutionsUpserts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	base := time.Date(2026, 9, 7, 8, 15, 0, 0, time.UTC)

	if err := s.WriteEvents(ctx, []ingest.Event{
		{Domain: "cdn.example.com", ETLD1: "example.com", Client: "192.168.88.20", QType: ingest.TypeA, At: base},
	}); err != nil {
		t.Fatalf("WriteEvents: %v", err)
	}

	for i, ttl := range []uint32{300, 60, 900} {
		if err := s.WriteResolutions(ctx, []ingest.Resolution{{
			Domain: "cdn.example.com", ETLD1: "example.com",
			IPs: addrs(t, "104.21.5.6"), TTL: ttl, At: base.Add(time.Duration(i) * time.Minute),
		}}); err != nil {
			t.Fatalf("WriteResolutions lần %d: %v", i, err)
		}
	}

	var id int64
	s.Reader().QueryRow(`SELECT id FROM domains WHERE name = ?`, "cdn.example.com").Scan(&id)
	ips, err := s.DomainIPs(ctx, id, 10)
	if err != nil {
		t.Fatalf("DomainIPs: %v", err)
	}
	if len(ips) != 1 {
		t.Fatalf("có %d dòng, muốn 1", len(ips))
	}
	if ips[0].Hits != 3 {
		t.Errorf("hits = %d, muốn 3", ips[0].Hits)
	}
	if ips[0].TTL != 60 {
		t.Errorf("ttl = %d, muốn 60 (nhỏ nhất từng thấy)", ips[0].TTL)
	}
	if ips[0].FirstSeen == ips[0].LastSeen {
		t.Error("last_seen không được cập nhật")
	}
}
