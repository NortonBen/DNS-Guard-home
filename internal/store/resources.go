package store

import (
	"context"
	"fmt"
	"time"

	"github.com/benji/dnsguard/internal/monitor"
)

// ResourcePoint là một khoảng đã gộp trên biểu đồ tài nguyên.
//
// Có cả trung bình lẫn đỉnh: trung bình cho thấy xu hướng, còn đỉnh mới cho thấy lúc
// nào suýt chạm trần bộ nhớ. Gộp chỉ giữ trung bình sẽ giấu mất đúng thứ cần tìm.
type ResourcePoint struct {
	T          string  `json:"t"`
	CPUAvg     float64 `json:"cpu_avg"`
	CPUMax     float64 `json:"cpu_max"`
	RSSAvg     int64   `json:"rss_avg"`
	RSSMax     int64   `json:"rss_max"`
	HeapAvg    int64   `json:"heap_avg"`
	Goroutines int     `json:"goroutines"`
}

// WriteResourceSamples ghi một lô mẫu đo.
//
// Ghi theo lô chứ không từng mẫu một: máy chủ có thể chạy thẻ nhớ, và 8.640 giao dịch
// mỗi ngày chỉ để lưu vài con số là lãng phí vòng ghi.
func (s *Store) WriteResourceSamples(ctx context.Context, samples []monitor.Sample) error {
	if len(samples) == 0 {
		return nil
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin write resource samples: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO resource_samples (at, cpu_percent, rss_bytes, heap_bytes, goroutines)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (at) DO UPDATE SET
		  cpu_percent = excluded.cpu_percent, rss_bytes = excluded.rss_bytes,
		  heap_bytes = excluded.heap_bytes, goroutines = excluded.goroutines`)
	if err != nil {
		return fmt.Errorf("prepare insert resource sample: %w", err)
	}
	defer stmt.Close()

	for _, sample := range samples {
		if _, err := stmt.ExecContext(ctx, TimeAt(sample.At), sample.CPUPercent,
			sample.RSSBytes, sample.HeapBytes, sample.Goroutines); err != nil {
			return fmt.Errorf("insert resource sample: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit resource samples: %w", err)
	}
	return nil
}

// BucketSeconds chọn độ rộng khoảng gộp cho một khoảng thời gian.
//
// Mục tiêu là giữ số điểm dưới khoảng bảy trăm ở mọi khoảng xem: nhiều hơn thế thì
// biểu đồ không đọc thêm được gì mà tải về lại nặng. Ba mươi ngày ở nhịp mười giây
// là 260 nghìn mẫu — vẽ thẳng ra thì trình duyệt đứng hình.
func BucketSeconds(span time.Duration) int {
	// Nới biên một phút. Client tính mốc from rồi mới gửi đi, còn máy chủ lấy mốc to
	// lúc nhận — nên "đúng mười hai giờ" luôn tới nơi dài hơn mười hai giờ vài trăm
	// mili giây. Không nới thì mọi lựa chọn trên giao diện đều rơi xuống mức gộp thô
	// hơn một bậc, và biểu đồ thưa gấp năm lần dự định mà không ai biết vì sao.
	span -= time.Minute

	switch {
	case span <= 12*time.Hour:
		return 60 // một phút → tối đa 720 điểm
	case span <= 48*time.Hour:
		return 300 // năm phút → tối đa 576 điểm
	case span <= 7*24*time.Hour:
		return 900 // mười lăm phút → tối đa 672 điểm
	default:
		return 3600 // một giờ → 720 điểm cho ba mươi ngày
	}
}

// Resources trả về mức tiêu thụ tài nguyên đã gộp theo khoảng.
func (s *Store) Resources(ctx context.Context, from, to string, bucket int) ([]ResourcePoint, error) {
	if bucket <= 0 {
		bucket = 60
	}

	// Gộp bằng cách chia mốc unix cho độ rộng khoảng rồi nhân lại — cách này chạy với
	// mọi độ rộng, khác strftime vốn chỉ cắt được theo phút, giờ hay ngày.
	rows, err := s.r.QueryContext(ctx, `
		SELECT
		  strftime('%Y-%m-%dT%H:%M:%SZ', (strftime('%s', at) / ?) * ?, 'unixepoch') AS bucket,
		  avg(cpu_percent), max(cpu_percent),
		  avg(rss_bytes),   max(rss_bytes),
		  avg(heap_bytes),  max(goroutines)
		FROM resource_samples
		WHERE at >= ? AND at <= ?
		GROUP BY bucket
		ORDER BY bucket
		LIMIT 1000`, bucket, bucket, from, to)
	if err != nil {
		return nil, fmt.Errorf("read resource samples: %w", err)
	}
	defer rows.Close()

	var out []ResourcePoint
	for rows.Next() {
		var p ResourcePoint
		var rssAvg, heapAvg float64
		if err := rows.Scan(&p.T, &p.CPUAvg, &p.CPUMax, &rssAvg, &p.RSSMax,
			&heapAvg, &p.Goroutines); err != nil {
			return nil, fmt.Errorf("scan resource point: %w", err)
		}
		p.RSSAvg = int64(rssAvg)
		p.HeapAvg = int64(heapAvg)
		out = append(out, p)
	}
	return out, rows.Err()
}

// ResourceSummary là các con số tổng hợp kèm biểu đồ.
type ResourceSummary struct {
	Samples    int     `json:"samples"`
	CPUAvg     float64 `json:"cpu_avg"`
	CPUMax     float64 `json:"cpu_max"`
	RSSAvg     int64   `json:"rss_avg"`
	RSSMax     int64   `json:"rss_max"`
	OldestAt   string  `json:"oldest_at,omitempty"`
	RetainDays int     `json:"retain_days"`
}

// ResourceStats tổng hợp toàn khoảng, không gộp theo bucket.
func (s *Store) ResourceStats(ctx context.Context, from, to string) (ResourceSummary, error) {
	var sum ResourceSummary
	var cpuAvg, cpuMax, rssAvg, rssMax *float64
	var oldest *string

	err := s.r.QueryRowContext(ctx, `
		SELECT count(*), avg(cpu_percent), max(cpu_percent),
		       avg(rss_bytes), max(rss_bytes), min(at)
		FROM resource_samples WHERE at >= ? AND at <= ?`, from, to).
		Scan(&sum.Samples, &cpuAvg, &cpuMax, &rssAvg, &rssMax, &oldest)
	if err != nil {
		return sum, fmt.Errorf("resource stats: %w", err)
	}

	if cpuAvg != nil {
		sum.CPUAvg, sum.CPUMax = *cpuAvg, *cpuMax
		sum.RSSAvg, sum.RSSMax = int64(*rssAvg), int64(*rssMax)
	}
	if oldest != nil {
		sum.OldestAt = *oldest
	}
	return sum, nil
}

// PruneResourceSamples xóa mẫu đo quá hạn giữ.
func (s *Store) PruneResourceSamples(ctx context.Context, days int) (int64, error) {
	res, err := s.w.ExecContext(ctx,
		`DELETE FROM resource_samples WHERE at < ?`, cutoff(days))
	if err != nil {
		return 0, fmt.Errorf("prune resource samples: %w", err)
	}
	return res.RowsAffected()
}
