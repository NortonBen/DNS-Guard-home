package store

import (
	"context"
	"fmt"
)

// Bucket là một điểm trên biểu đồ chuỗi thời gian.
type Bucket struct {
	T       string `json:"t"`
	Queries int64  `json:"queries"`
	Blocked int64  `json:"blocked"`
	Clients int    `json:"clients,omitempty"`
}

// TopEntry là một dòng của bảng xếp hạng.
type TopEntry struct {
	Key      string `json:"key"`
	Label    string `json:"label,omitempty"`
	Queries  int64  `json:"queries"`
	Status   string `json:"status,omitempty"`
	Category string `json:"category,omitempty"`
	DomainID int64  `json:"domain_id,omitempty"`
}

// CategoryCount là số truy vấn theo phân loại.
type CategoryCount struct {
	Key     string `json:"key"`
	Queries int64  `json:"queries"`
}

// Overview là dữ liệu cho dashboard.
type Overview struct {
	TotalQueries    int64           `json:"total_queries"`
	BlockedQueries  int64           `json:"blocked_queries"`
	BlockedRatio    float64         `json:"blocked_ratio"`
	UniqueDomains   int64           `json:"unique_domains"`
	UniqueClients   int64           `json:"unique_clients"`
	PendingReview   int64           `json:"pending_review"`
	UnblockRequests int64           `json:"unblock_requests"`
	ByCategory      []CategoryCount `json:"by_category"`
	Timeseries      []Bucket        `json:"timeseries"`
}

// StatsOverview tổng hợp số liệu cho dashboard trong một khoảng thời gian.
//
// Đọc từ bảng tổng hợp theo giờ chứ không quét log thô: dashboard phải tải dưới một
// giây kể cả khi query_events có hàng chục triệu dòng.
func (s *Store) StatsOverview(ctx context.Context, from, to string) (Overview, error) {
	var o Overview

	err := s.r.QueryRowContext(ctx, `
		SELECT
		  coalesce(sum(h.query_count), 0),
		  coalesce(sum(CASE WHEN d.status = 'blocked' THEN h.query_count ELSE 0 END), 0),
		  count(DISTINCT h.domain_id)
		FROM domain_hourly h JOIN domains d ON d.id = h.domain_id
		WHERE h.hour >= ? AND h.hour <= ?`, from, to).
		Scan(&o.TotalQueries, &o.BlockedQueries, &o.UniqueDomains)
	if err != nil {
		return o, fmt.Errorf("stats totals: %w", err)
	}
	if o.TotalQueries > 0 {
		o.BlockedRatio = float64(o.BlockedQueries) / float64(o.TotalQueries)
	}

	err = s.r.QueryRowContext(ctx, `
		SELECT
		  (SELECT count(*) FROM clients WHERE last_seen >= ?),
		  (SELECT count(*) FROM domains WHERE status = 'staging'),
		  (SELECT count(*) FROM unblock_requests WHERE state = 'pending')`,
		from).Scan(&o.UniqueClients, &o.PendingReview, &o.UnblockRequests)
	if err != nil {
		return o, fmt.Errorf("stats counters: %w", err)
	}

	catRows, err := s.r.QueryContext(ctx, `
		SELECT c.key, coalesce(sum(h.query_count), 0) AS queries
		FROM domain_hourly h
		JOIN domains d ON d.id = h.domain_id
		JOIN categories c ON c.id = d.category_id
		WHERE h.hour >= ? AND h.hour <= ?
		GROUP BY c.key ORDER BY queries DESC LIMIT 20`, from, to)
	if err != nil {
		return o, fmt.Errorf("stats by category: %w", err)
	}
	defer catRows.Close()
	for catRows.Next() {
		var c CategoryCount
		if err := catRows.Scan(&c.Key, &c.Queries); err != nil {
			return o, fmt.Errorf("scan category count: %w", err)
		}
		o.ByCategory = append(o.ByCategory, c)
	}
	if err := catRows.Err(); err != nil {
		return o, err
	}

	tsRows, err := s.r.QueryContext(ctx, `
		SELECT h.hour,
		       coalesce(sum(h.query_count), 0),
		       coalesce(sum(CASE WHEN d.status = 'blocked' THEN h.query_count ELSE 0 END), 0)
		FROM domain_hourly h JOIN domains d ON d.id = h.domain_id
		WHERE h.hour >= ? AND h.hour <= ?
		GROUP BY h.hour ORDER BY h.hour LIMIT 800`, from, to)
	if err != nil {
		return o, fmt.Errorf("stats timeseries: %w", err)
	}
	defer tsRows.Close()
	for tsRows.Next() {
		var b Bucket
		if err := tsRows.Scan(&b.T, &b.Queries, &b.Blocked); err != nil {
			return o, fmt.Errorf("scan bucket: %w", err)
		}
		o.Timeseries = append(o.Timeseries, b)
	}
	return o, tsRows.Err()
}

// StatsTop trả về bảng xếp hạng theo chiều đã chọn.
func (s *Store) StatsTop(ctx context.Context, dimension, from, to string, limit int) ([]TopEntry, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	var query string
	switch dimension {
	case "client":
		query = `
			SELECT cl.ip, cl.label, count(*) AS queries, '', '', 0
			FROM query_events e JOIN clients cl ON cl.id = e.client_id
			WHERE e.occurred_at >= ? AND e.occurred_at <= ?
			GROUP BY cl.id ORDER BY queries DESC LIMIT ?`
	case "blocked":
		query = `
			SELECT d.name, '', coalesce(sum(h.query_count), 0) AS queries,
			       d.status, coalesce(c.key, ''), d.id
			FROM domain_hourly h
			JOIN domains d ON d.id = h.domain_id
			LEFT JOIN categories c ON c.id = d.category_id
			WHERE h.hour >= ? AND h.hour <= ? AND d.status = 'blocked'
			GROUP BY d.id ORDER BY queries DESC LIMIT ?`
	default:
		query = `
			SELECT d.name, '', coalesce(sum(h.query_count), 0) AS queries,
			       d.status, coalesce(c.key, ''), d.id
			FROM domain_hourly h
			JOIN domains d ON d.id = h.domain_id
			LEFT JOIN categories c ON c.id = d.category_id
			WHERE h.hour >= ? AND h.hour <= ?
			GROUP BY d.id ORDER BY queries DESC LIMIT ?`
	}

	rows, err := s.r.QueryContext(ctx, query, from, to, limit)
	if err != nil {
		return nil, fmt.Errorf("stats top %q: %w", dimension, err)
	}
	defer rows.Close()

	var out []TopEntry
	for rows.Next() {
		var e TopEntry
		if err := rows.Scan(&e.Key, &e.Label, &e.Queries, &e.Status, &e.Category, &e.DomainID); err != nil {
			return nil, fmt.Errorf("scan top entry: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Timeline trả về chuỗi thời gian của một domain kèm phân bố theo client.
func (s *Store) Timeline(ctx context.Context, domainID int64, from, to string) ([]Bucket, []TopEntry, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT hour, query_count, client_count FROM domain_hourly
		WHERE domain_id = ? AND hour >= ? AND hour <= ?
		ORDER BY hour LIMIT 800`, domainID, from, to)
	if err != nil {
		return nil, nil, fmt.Errorf("domain timeline: %w", err)
	}
	defer rows.Close()

	var buckets []Bucket
	for rows.Next() {
		var b Bucket
		if err := rows.Scan(&b.T, &b.Queries, &b.Clients); err != nil {
			return nil, nil, fmt.Errorf("scan timeline bucket: %w", err)
		}
		buckets = append(buckets, b)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	clientRows, err := s.r.QueryContext(ctx, `
		SELECT cl.ip, count(*) AS queries
		FROM query_events e JOIN clients cl ON cl.id = e.client_id
		WHERE e.domain_id = ? AND e.occurred_at >= ? AND e.occurred_at <= ?
		GROUP BY cl.id ORDER BY queries DESC LIMIT 50`, domainID, from, to)
	if err != nil {
		return nil, nil, fmt.Errorf("domain clients: %w", err)
	}
	defer clientRows.Close()

	var byClient []TopEntry
	for clientRows.Next() {
		var e TopEntry
		if err := clientRows.Scan(&e.Key, &e.Queries); err != nil {
			return nil, nil, fmt.Errorf("scan client row: %w", err)
		}
		byClient = append(byClient, e)
	}
	return buckets, byClient, clientRows.Err()
}

// LastEventAge trả về số giây kể từ sự kiện gần nhất, -1 nếu chưa có sự kiện nào.
//
// Đây là kiểm tra sức khỏe quan trọng nhất: nếu vượt 600 giây nghĩa là bộ mirror
// trên router đã dừng — lỗi thường gặp nhất sau khi router khởi động lại, và là lỗi
// im lặng nhất vì hệ thống vẫn chặn bình thường bằng danh sách cũ.
func (s *Store) LastEventAge(ctx context.Context) (int64, error) {
	var last *string
	if err := s.r.QueryRowContext(ctx,
		`SELECT max(occurred_at) FROM query_events`).Scan(&last); err != nil {
		return -1, fmt.Errorf("last event age: %w", err)
	}
	if last == nil {
		return -1, nil
	}
	t, err := ParseTime(*last)
	if err != nil {
		return -1, nil
	}
	return int64(nowUTC().Sub(t).Seconds()), nil
}

// CountByStatus đếm domain theo trạng thái, dùng cho thẻ trên dashboard.
func (s *Store) CountByStatus(ctx context.Context) (map[string]int64, error) {
	rows, err := s.r.QueryContext(ctx,
		`SELECT status, count(*) FROM domains GROUP BY status LIMIT 10`)
	if err != nil {
		return nil, fmt.Errorf("count by status: %w", err)
	}
	defer rows.Close()

	out := make(map[string]int64, 5)
	for rows.Next() {
		var status string
		var n int64
		if err := rows.Scan(&status, &n); err != nil {
			return nil, fmt.Errorf("scan status count: %w", err)
		}
		out[status] = n
	}
	return out, rows.Err()
}
