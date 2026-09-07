package worker

import (
	"context"
	"io"
	"log/slog"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/benji/dnsguard/internal/catalog"
	"github.com/benji/dnsguard/internal/config"
	"github.com/benji/dnsguard/internal/enrich"
	"github.com/benji/dnsguard/internal/events"
	"github.com/benji/dnsguard/internal/graph"
	"github.com/benji/dnsguard/internal/ingest"
	"github.com/benji/dnsguard/internal/publish"
	"github.com/benji/dnsguard/internal/store"
)

// stubLocator đóng vai bảng tra ASN đã nạp. Bản thân việc tra dải địa chỉ đã có test
// riêng ở package enrich; ở đây chỉ cần kiểm phần điều phối của job.
type stubLocator struct {
	loaded bool
	table  map[string]enrich.ASNFacts
}

func (s *stubLocator) Name() string { return "asn" }
func (s *stubLocator) Enrich(context.Context, string) (any, error) {
	return enrich.ASNFacts{}, nil
}
func (s *stubLocator) LoadTable(string) error     { return nil }
func (s *stubLocator) Status() enrich.TableStatus { return enrich.TableStatus{Kind: "asn"} }
func (s *stubLocator) Loaded() bool               { return s.loaded }
func (s *stubLocator) LookupIP(ip netip.Addr) (enrich.ASNFacts, bool) {
	f, ok := s.table[ip.String()]
	return f, ok
}

// newGeoRunner dựng một Runner tối thiểu kèm CSDL đã có sẵn một domain và các địa
// chỉ nó trỏ tới.
func newGeoRunner(t *testing.T, loc *stubLocator, ips ...string) (*Runner, *store.Store, int64) {
	t.Helper()

	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "geo.db"), true)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ctx := context.Background()
	base := time.Date(2026, 9, 7, 8, 0, 0, 0, time.UTC)
	if err := db.WriteEvents(ctx, []ingest.Event{{
		Domain: "ads.example.com", ETLD1: "example.com",
		Client: "192.168.88.20", QType: ingest.TypeA, At: base,
	}}); err != nil {
		t.Fatalf("WriteEvents: %v", err)
	}

	addrs := make([]netip.Addr, len(ips))
	for i, s := range ips {
		addrs[i] = netip.MustParseAddr(s)
	}
	if err := db.WriteResolutions(ctx, []ingest.Resolution{{
		Domain: "ads.example.com", ETLD1: "example.com",
		IPs: addrs, TTL: 300, At: base.Add(time.Second),
	}}); err != nil {
		t.Fatalf("WriteResolutions: %v", err)
	}

	var id int64
	if err := db.Reader().QueryRow(`SELECT id FROM domains WHERE name = ?`,
		"ads.example.com").Scan(&id); err != nil {
		t.Fatalf("tra domain: %v", err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := enrich.NewRegistry(log)
	registry.Register(loc, 100, 100, true)

	cfg := config.Config{EnrichConcurrency: 1, ListsDir: dir}
	r := New(db, cfg, registry,
		publish.New(db, dir, cfg.PublishMinRatio, cfg.PublishSink, log),
		catalog.New(db, log), graph.New(db, log), events.NewBroker(log), log)
	return r, db, id
}

func TestRunGeoIPFillsLocation(t *testing.T) {
	loc := &stubLocator{loaded: true, table: map[string]enrich.ASNFacts{
		"104.21.5.6": {ASN: 13335, Org: "CLOUDFLARENET", Country: "US"},
	}}
	r, db, id := newGeoRunner(t, loc, "104.21.5.6")
	ctx := context.Background()

	if err := r.runGeoIP(ctx); err != nil {
		t.Fatalf("runGeoIP: %v", err)
	}

	ips, err := db.DomainIPs(ctx, id, 10)
	if err != nil {
		t.Fatalf("DomainIPs: %v", err)
	}
	if len(ips) != 1 {
		t.Fatalf("có %d địa chỉ, muốn 1", len(ips))
	}
	if ips[0].ASN != 13335 || ips[0].Country != "US" || ips[0].Org != "CLOUDFLARENET" {
		t.Errorf("vị trí = AS%d %s %q; muốn AS13335 US CLOUDFLARENET",
			ips[0].ASN, ips[0].Country, ips[0].Org)
	}
}

// Địa chỉ không có trong bảng tra vẫn phải được đánh dấu đã tra, nếu không nó nằm
// lại hàng đợi và mọi lượt chạy sau đều xử lý lại chính nó.
func TestRunGeoIPMarksUnknownAddressesDone(t *testing.T) {
	loc := &stubLocator{loaded: true, table: map[string]enrich.ASNFacts{}}
	r, db, _ := newGeoRunner(t, loc, "203.0.113.9")
	ctx := context.Background()

	if err := r.runGeoIP(ctx); err != nil {
		t.Fatalf("runGeoIP: %v", err)
	}

	pending, err := db.PendingGeoIPs(ctx, 10)
	if err != nil {
		t.Fatalf("PendingGeoIPs: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("còn %v trong hàng đợi, muốn rỗng", pending)
	}
}

// Chưa nạp bảng ip2asn là chuyện bình thường: bảng là tuỳ chọn. Job phải im lặng
// bỏ qua chứ không báo hỏng mỗi giờ.
func TestRunGeoIPSkipsWhenTableMissing(t *testing.T) {
	loc := &stubLocator{loaded: false}
	r, db, _ := newGeoRunner(t, loc, "104.21.5.6")
	ctx := context.Background()

	if err := r.runGeoIP(ctx); err != nil {
		t.Fatalf("runGeoIP phải bỏ qua chứ không lỗi: %v", err)
	}

	pending, err := db.PendingGeoIPs(ctx, 10)
	if err != nil {
		t.Fatalf("PendingGeoIPs: %v", err)
	}
	if len(pending) != 1 {
		t.Errorf("còn %d địa chỉ chờ, muốn giữ nguyên 1", len(pending))
	}
}
