package store

import (
	"context"
	"testing"
	"time"

	"github.com/benji/dnsguard/internal/ingest"
)

func TestWriteEventsCreatesDomainsAndAggregates(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	base := time.Date(2026, 9, 7, 8, 15, 0, 0, time.UTC)

	events := []ingest.Event{
		{Domain: "ads.example.com", ETLD1: "example.com", Client: "192.168.88.20", QType: ingest.TypeA, At: base},
		{Domain: "ads.example.com", ETLD1: "example.com", Client: "192.168.88.21", QType: ingest.TypeA, At: base.Add(time.Second)},
		{Domain: "cdn.example.com", ETLD1: "example.com", Client: "192.168.88.20", QType: ingest.TypeAAAA, At: base.Add(2 * time.Second)},
	}
	if err := s.WriteEvents(ctx, events); err != nil {
		t.Fatalf("WriteEvents: %v", err)
	}

	var domains, rawEvents, clientRows int
	s.Reader().QueryRow(`SELECT count(*) FROM domains`).Scan(&domains)
	s.Reader().QueryRow(`SELECT count(*) FROM query_events`).Scan(&rawEvents)
	s.Reader().QueryRow(`SELECT count(*) FROM clients`).Scan(&clientRows)

	if domains != 2 {
		t.Errorf("domains = %d, muốn 2", domains)
	}
	if rawEvents != 3 {
		t.Errorf("query_events = %d, muốn 3", rawEvents)
	}
	if clientRows != 2 {
		t.Errorf("clients = %d, muốn 2", clientRows)
	}

	// Domain mới phải vào trạng thái 'new' với nguồn gốc 'discovered'.
	var status, origin, etld1 string
	var queryCount int
	if err := s.Reader().QueryRow(
		`SELECT status, origin, etld1, query_count FROM domains WHERE name = 'ads.example.com'`,
	).Scan(&status, &origin, &etld1, &queryCount); err != nil {
		t.Fatalf("read domain: %v", err)
	}
	if status != "new" || origin != "discovered" {
		t.Errorf("domain mới = (%s, %s), muốn (new, discovered)", status, origin)
	}
	if etld1 != "example.com" {
		t.Errorf("etld1 = %q, muốn example.com", etld1)
	}
	if queryCount != 2 {
		t.Errorf("query_count = %d, muốn 2", queryCount)
	}

	// Tổng hợp theo giờ phải khớp với log thô.
	var hourQueries, hourClients int
	if err := s.Reader().QueryRow(
		`SELECT query_count, client_count FROM domain_hourly
		 WHERE hour = '2026-09-07T08:00:00Z'
		   AND domain_id = (SELECT id FROM domains WHERE name = 'ads.example.com')`,
	).Scan(&hourQueries, &hourClients); err != nil {
		t.Fatalf("read domain_hourly: %v", err)
	}
	if hourQueries != 2 || hourClients != 2 {
		t.Errorf("domain_hourly = (%d truy vấn, %d client), muốn (2, 2)", hourQueries, hourClients)
	}
}

// Bitmap client phải đếm phân biệt qua nhiều lô. Đây là điểm khác biệt so với
// HyperLogLog: kết quả chính xác tuyệt đối, không phải xấp xỉ.
func TestWriteEventsCountsDistinctClientsAcrossBatches(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	base := time.Date(2026, 9, 7, 8, 0, 0, 0, time.UTC)

	// Ba lô riêng biệt, hai client trùng nhau giữa các lô.
	batches := [][]string{
		{"192.168.88.20", "192.168.88.21"},
		{"192.168.88.20", "192.168.88.22"},
		{"192.168.88.21", "192.168.88.22", "192.168.88.23"},
	}
	for i, ips := range batches {
		var events []ingest.Event
		for _, ip := range ips {
			events = append(events, ingest.Event{
				Domain: "tracker.example.com", ETLD1: "example.com",
				Client: ip, QType: ingest.TypeA,
				At: base.Add(time.Duration(i) * time.Minute),
			})
		}
		if err := s.WriteEvents(ctx, events); err != nil {
			t.Fatalf("WriteEvents lô %d: %v", i, err)
		}
	}

	var queries, distinct int
	if err := s.Reader().QueryRow(
		`SELECT query_count, client_count FROM domain_hourly
		 WHERE hour = '2026-09-07T08:00:00Z'`,
	).Scan(&queries, &distinct); err != nil {
		t.Fatalf("read domain_hourly: %v", err)
	}
	if queries != 7 {
		t.Errorf("query_count = %d, muốn 7", queries)
	}
	if distinct != 4 {
		t.Errorf("client_count = %d, muốn 4 client phân biệt", distinct)
	}

	// client_count trên domains là đỉnh theo giờ, dùng cho tín hiệu fan_out.
	var peak int
	s.Reader().QueryRow(`SELECT client_count FROM domains WHERE name = 'tracker.example.com'`).Scan(&peak)
	if peak != 4 {
		t.Errorf("domains.client_count = %d, muốn 4", peak)
	}
}

// Ghi lô là đường nóng: cần đủ nhanh để theo kịp một mạng vài trăm thiết bị.
func BenchmarkWriteEvents(b *testing.B) {
	s, err := Open(b.TempDir()+"/bench.db", true)
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	base := time.Date(2026, 9, 7, 8, 0, 0, 0, time.UTC)
	const batchSize = 1000

	batch := make([]ingest.Event, batchSize)
	for i := range batch {
		batch[i] = ingest.Event{
			Domain: "d" + string(rune('a'+i%26)) + ".example.com",
			ETLD1:  "example.com",
			Client: "192.168.88." + string(rune('0'+i%10)),
			QType:  ingest.TypeA,
			At:     base.Add(time.Duration(i) * time.Millisecond),
		}
	}

	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		if err := s.WriteEvents(ctx, batch); err != nil {
			b.Fatalf("WriteEvents: %v", err)
		}
	}
	b.SetBytes(batchSize)
}
