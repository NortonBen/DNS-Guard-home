package store

import (
	"context"
	"database/sql"
	"fmt"
	"math/bits"
	"strings"
	"sync"
	"time"

	"github.com/benji/dnsguard/internal/ingest"
)

// clientCache là cache IP client → id. Một mạng có vài trăm client nên cache đầy
// đủ là rẻ, và nó cắt được một truy vấn cho mỗi gói trên đường nóng.
//
// Cache gắn vào Store chứ không phải biến toàn cục: id chỉ có nghĩa trong đúng một
// file CSDL, nên cache dùng chung giữa hai Store sẽ gán nhầm id của CSDL này cho
// client của CSDL kia.
type clientCache struct {
	mu sync.RWMutex
	m  map[string]int64
}

func (c *clientCache) get(ip string) (int64, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	id, ok := c.m[ip]
	return id, ok
}

func (c *clientCache) put(ip string, id int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = make(map[string]int64, 256)
	}
	c.m[ip] = id
}

// WriteEvents ghi một lô truy vấn. Cài đặt ingest.Sink.
//
// Toàn bộ lô nằm trong một transaction: query_events và domain_hourly luôn khớp
// nhau, và một lần mất điện không để lại tổng hợp lệch so với log thô.
func (s *Store) WriteEvents(ctx context.Context, events []ingest.Event) error {
	if len(events) == 0 {
		return nil
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin write events: %w", err)
	}
	defer tx.Rollback()

	clientIDs, err := s.resolveClients(ctx, tx, events)
	if err != nil {
		return err
	}
	domainIDs, err := resolveDomains(ctx, tx, events)
	if err != nil {
		return err
	}

	insertEvent, err := tx.PrepareContext(ctx,
		`INSERT INTO query_events (domain_id, client_id, qtype, occurred_at) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare insert event: %w", err)
	}
	defer insertEvent.Close()

	// Gộp theo (domain, giờ) trước khi chạm CSDL: một lô 1.000 sự kiện thường chỉ
	// rơi vào vài trăm ô, nên gộp trong bộ nhớ tiết kiệm phần lớn số lần ghi.
	buckets := make(map[hourCell]*hourAgg, len(events)/4+1)

	for _, ev := range events {
		did, ok := domainIDs[ev.Domain]
		if !ok {
			continue
		}
		cid := clientIDs[ev.Client]

		if _, err := insertEvent.ExecContext(ctx, did, cid, int64(ev.QType), TimeAt(ev.At)); err != nil {
			return fmt.Errorf("insert query_event %q: %w", ev.Domain, err)
		}

		k := hourCell{domainID: did, hour: TimeAt(ev.At.Truncate(time.Hour))}
		agg := buckets[k]
		if agg == nil {
			agg = &hourAgg{clients: make(map[int64]struct{}, 4)}
			buckets[k] = agg
		}
		agg.queries++
		agg.clients[cid] = struct{}{}
	}

	if err := applyHourly(ctx, tx, buckets); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit write events: %w", err)
	}
	return nil
}

// hourCell định danh một ô của bảng tổng hợp theo giờ.
type hourCell struct {
	domainID int64
	hour     string
}

// hourAgg là phần tổng hợp của một ô trong đúng lô đang ghi.
type hourAgg struct {
	queries int
	clients map[int64]struct{}
}

// resolveClients ánh xạ IP sang id, tạo mới khi cần.
func (s *Store) resolveClients(ctx context.Context, tx *sql.Tx, events []ingest.Event) (map[string]int64, error) {
	out := make(map[string]int64, 8)
	now := Now()

	for _, ev := range events {
		if _, done := out[ev.Client]; done {
			continue
		}
		if id, ok := s.clients.get(ev.Client); ok {
			out[ev.Client] = id
			continue
		}
		var id int64
		err := tx.QueryRowContext(ctx, `SELECT id FROM clients WHERE ip = ?`, ev.Client).Scan(&id)
		if err == sql.ErrNoRows {
			res, err := tx.ExecContext(ctx,
				`INSERT INTO clients (ip, first_seen, last_seen) VALUES (?, ?, ?)`,
				ev.Client, now, now)
			if err != nil {
				return nil, fmt.Errorf("insert client %q: %w", ev.Client, err)
			}
			if id, err = res.LastInsertId(); err != nil {
				return nil, fmt.Errorf("client id %q: %w", ev.Client, err)
			}
		} else if err != nil {
			return nil, fmt.Errorf("lookup client %q: %w", ev.Client, err)
		}
		s.clients.put(ev.Client, id)
		out[ev.Client] = id
	}

	for ip := range out {
		if _, err := tx.ExecContext(ctx,
			`UPDATE clients SET last_seen = ? WHERE ip = ?`, now, ip); err != nil {
			return nil, fmt.Errorf("touch client %q: %w", ip, err)
		}
	}
	return out, nil
}

// resolveDomains ánh xạ tên miền sang id, tạo dòng mới cho domain lần đầu thấy và
// cập nhật last_seen cùng query_count cho domain đã biết.
func resolveDomains(ctx context.Context, tx *sql.Tx, events []ingest.Event) (map[string]int64, error) {
	counts := make(map[string]int, len(events))
	etld1 := make(map[string]string, len(events))
	for _, ev := range events {
		counts[ev.Domain]++
		etld1[ev.Domain] = ev.ETLD1
	}

	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}

	out := make(map[string]int64, len(names))
	// Tra theo lô: một truy vấn cho tối đa 500 tên thay vì một truy vấn cho mỗi tên.
	const chunk = 500
	for start := 0; start < len(names); start += chunk {
		end := min(start+chunk, len(names))
		batch := names[start:end]

		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, len(batch))
		for i, n := range batch {
			args[i] = n
		}

		rows, err := tx.QueryContext(ctx,
			`SELECT id, name FROM domains WHERE name IN (`+placeholders+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("lookup domains: %w", err)
		}
		for rows.Next() {
			var id int64
			var name string
			if err := rows.Scan(&id, &name); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan domain: %w", err)
			}
			out[name] = id
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("iterate domains: %w", err)
		}
		rows.Close()
	}

	now := Now()
	for _, name := range names {
		if id, ok := out[name]; ok {
			if _, err := tx.ExecContext(ctx,
				`UPDATE domains SET last_seen = ?, query_count = query_count + ?, updated_at = ?
				 WHERE id = ?`, now, counts[name], now, id); err != nil {
				return nil, fmt.Errorf("touch domain %q: %w", name, err)
			}
			continue
		}
		res, err := tx.ExecContext(ctx,
			`INSERT INTO domains (name, name_rev, etld1, status, origin,
			                      first_seen, last_seen, query_count, created_at, updated_at)
			 VALUES (?, ?, ?, 'new', 'discovered', ?, ?, ?, ?, ?)`,
			name, reverseName(name), etld1[name], now, now, counts[name], now, now)
		if err != nil {
			return nil, fmt.Errorf("insert domain %q: %w", name, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("domain id %q: %w", name, err)
		}
		out[name] = id
	}
	return out, nil
}

// applyHourly cập nhật bảng tổng hợp theo giờ, gộp bitmap client bằng phép OR.
func applyHourly(ctx context.Context, tx *sql.Tx, buckets map[hourCell]*hourAgg) error {
	for k, agg := range buckets {
		var existing []byte
		err := tx.QueryRowContext(ctx,
			`SELECT clients FROM domain_hourly WHERE domain_id = ? AND hour = ?`,
			k.domainID, k.hour).Scan(&existing)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("read domain_hourly: %w", err)
		}

		// Bitmap tích lũy qua các lô, nên client_count là số đếm phân biệt thật của
		// cả giờ đó chứ không chỉ của lô này.
		bitmap := existing
		for cid := range agg.clients {
			bitmap = setBit(bitmap, cid)
		}

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO domain_hourly (domain_id, hour, query_count, client_count, clients)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT (domain_id, hour) DO UPDATE SET
			   query_count  = query_count + excluded.query_count,
			   client_count = excluded.client_count,
			   clients      = excluded.clients`,
			k.domainID, k.hour, agg.queries, popcount(bitmap), bitmap); err != nil {
			return fmt.Errorf("upsert domain_hourly: %w", err)
		}

		// client_count trên domains là đỉnh theo giờ, dùng cho tín hiệu fan_out.
		if _, err := tx.ExecContext(ctx,
			`UPDATE domains SET client_count = (
			   SELECT coalesce(max(client_count), 0) FROM domain_hourly WHERE domain_id = ?
			 ) WHERE id = ?`, k.domainID, k.domainID); err != nil {
			return fmt.Errorf("update client_count: %w", err)
		}
	}
	return nil
}

// setBit bật bit thứ id-1 trong bitmap, nới rộng khi cần.
//
// Bitmap client thay cho HyperLogLog của bản PostgreSQL: mạng gia đình có vài trăm
// client nên bitmap chỉ vài chục byte, gộp nhiều giờ bằng phép OR, và cho số đếm
// chính xác tuyệt đối thay vì xấp xỉ 2%.
func setBit(bm []byte, id int64) []byte {
	if id <= 0 {
		return bm
	}
	idx := int((id - 1) / 8)
	if idx >= len(bm) {
		grown := make([]byte, idx+1)
		copy(grown, bm)
		bm = grown
	}
	bm[idx] |= 1 << uint((id-1)%8)
	return bm
}

func popcount(bm []byte) int {
	n := 0
	for _, b := range bm {
		n += bits.OnesCount8(b)
	}
	return n
}

// reverseName đảo ngược tên miền để tìm kiếm hậu tố dùng được index.
func reverseName(name string) string {
	b := []byte(name)
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}
