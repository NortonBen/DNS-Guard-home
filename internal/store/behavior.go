package store

import (
	"context"
	"fmt"
	"math"
	"time"
)

// ComputeBehavior tính lại các đặc trưng hành vi từ log thô.
//
// Chạy theo lô hằng đêm chứ không realtime: cần quét cả cửa sổ nhiều ngày, và kết
// quả không đổi đủ nhanh để đáng cập nhật liên tục.
func (s *Store) ComputeBehavior(ctx context.Context, windowDays int) (int, error) {
	since := cutoff(windowDays)

	// Tỉ lệ third-party: bao nhiêu phần trăm lần xuất hiện nằm trong vòng 3 giây sau
	// một truy vấn của cùng client tới một eTLD+1 khác.
	//
	// Trực giác: người dùng chủ động gõ tên miền nội dung vào trình duyệt, không ai
	// gõ tên một máy chủ quảng cáo. Domain quảng cáo vì thế luôn xuất hiện *sau* một
	// domain khác.
	rows, err := s.r.QueryContext(ctx, `
		WITH recent AS (
		  SELECT e.id, e.domain_id, e.client_id, e.occurred_at, d.etld1
		  FROM query_events e JOIN domains d ON d.id = e.domain_id
		  WHERE e.occurred_at >= ?
		),
		flagged AS (
		  SELECT r.domain_id,
		         EXISTS (
		           SELECT 1 FROM recent p
		           WHERE p.client_id = r.client_id
		             AND p.etld1 <> r.etld1
		             AND p.occurred_at < r.occurred_at
		             AND p.occurred_at >= strftime('%Y-%m-%dT%H:%M:%SZ', r.occurred_at, '-3 seconds')
		         ) AS after_other
		  FROM recent r
		)
		SELECT domain_id, count(*) AS total, sum(after_other) AS third_party
		FROM flagged
		GROUP BY domain_id
		HAVING total >= 5
		LIMIT 20000`, since)
	if err != nil {
		return 0, fmt.Errorf("compute third-party ratio: %w", err)
	}
	defer rows.Close()

	type row struct {
		id    int64
		total int
		third int
	}
	var ratios []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.total, &r.third); err != nil {
			return 0, fmt.Errorf("scan third-party ratio: %w", err)
		}
		ratios = append(ratios, r)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	now := Now()
	for _, r := range ratios {
		cv, err := s.intervalCV(ctx, r.id, since)
		if err != nil {
			return 0, err
		}
		ratio := float64(r.third) / float64(r.total)
		if _, err := s.w.ExecContext(ctx, `
			INSERT INTO domain_behavior (domain_id, third_party_ratio, interval_cv, sample_size, computed_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (domain_id) DO UPDATE SET
			  third_party_ratio = excluded.third_party_ratio,
			  interval_cv       = excluded.interval_cv,
			  sample_size       = excluded.sample_size,
			  computed_at       = excluded.computed_at`,
			r.id, ratio, cv, r.total, now); err != nil {
			return 0, fmt.Errorf("save behavior: %w", err)
		}
	}
	return len(ratios), nil
}

// intervalCV tính hệ số biến thiên của khoảng cách giữa các truy vấn liên tiếp.
//
// Con người tạo ra khoảng cách không đều; phần mềm báo cáo định kỳ thì đều một cách
// máy móc. Hệ số biến thiên thấp là dấu hiệu telemetry.
func (s *Store) intervalCV(ctx context.Context, domainID int64, since string) (float64, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT occurred_at FROM query_events
		WHERE domain_id = ? AND occurred_at >= ?
		ORDER BY occurred_at
		LIMIT 2000`, domainID, since)
	if err != nil {
		return 0, fmt.Errorf("read intervals: %w", err)
	}
	defer rows.Close()

	var times []time.Time
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return 0, fmt.Errorf("scan interval: %w", err)
		}
		t, err := ParseTime(raw)
		if err != nil {
			continue
		}
		times = append(times, t)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(times) < 3 {
		return 0, nil
	}

	gaps := make([]float64, 0, len(times)-1)
	for i := 1; i < len(times); i++ {
		if g := times[i].Sub(times[i-1]).Seconds(); g > 0 {
			gaps = append(gaps, g)
		}
	}
	if len(gaps) < 2 {
		return 0, nil
	}

	var sum float64
	for _, g := range gaps {
		sum += g
	}
	mean := sum / float64(len(gaps))
	if mean == 0 {
		return 0, nil
	}

	var variance float64
	for _, g := range gaps {
		variance += (g - mean) * (g - mean)
	}
	variance /= float64(len(gaps))

	return math.Sqrt(variance) / mean, nil
}
