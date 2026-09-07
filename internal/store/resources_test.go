package store

import (
	"context"
	"testing"
	"time"

	"github.com/benji/dnsguard/internal/monitor"
)

func TestBucketSecondsKeepsChartsReadable(t *testing.T) {
	// Bất biến quan trọng hơn từng giá trị cụ thể: mọi khoảng xem phải ra ít điểm
	// hơn ngưỡng vẽ được, nếu không trình duyệt đứng hình ở khoảng ba mươi ngày.
	const maxPoints = 800

	for _, span := range []time.Duration{
		12 * time.Hour, 24 * time.Hour, 48 * time.Hour,
		7 * 24 * time.Hour, 30 * 24 * time.Hour,
	} {
		bucket := BucketSeconds(span)
		points := int(span.Seconds()) / bucket
		if points > maxPoints {
			t.Errorf("khoảng %v gộp %ds ra %d điểm, quá %d", span, bucket, points, maxPoints)
		}
		if bucket < 10 {
			t.Errorf("khoảng %v gộp %ds, nhỏ hơn nhịp lấy mẫu", span, bucket)
		}
	}
}

func TestBucketSecondsToleratesRoundTripDrift(t *testing.T) {
	// Giao diện gửi mốc from tính từ lúc bấm, máy chủ lấy mốc to lúc nhận. Chênh lệch
	// đó không được đẩy lựa chọn sang mức gộp thô hơn.
	cases := []struct {
		name string
		span time.Duration
		want int
	}{
		{"12 giờ", 12 * time.Hour, 60},
		{"12 giờ lệch 300ms", 12*time.Hour + 300*time.Millisecond, 60},
		{"12 giờ lệch 2s", 12*time.Hour + 2*time.Second, 60},
		{"48 giờ lệch 1s", 48*time.Hour + time.Second, 300},
		{"7 ngày lệch 1s", 7*24*time.Hour + time.Second, 900},
		{"30 ngày", 30 * 24 * time.Hour, 3600},
	}

	for _, tc := range cases {
		if got := BucketSeconds(tc.span); got != tc.want {
			t.Errorf("%s: gộp %ds, muốn %ds", tc.name, got, tc.want)
		}
	}
}

func TestResourcesAggregatesIntoBuckets(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Sáu mẫu cách nhau mười giây, tất cả trong cùng một phút, nên phải gộp thành
	// đúng một điểm.
	base := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	samples := make([]monitor.Sample, 0, 6)
	for i := range 6 {
		samples = append(samples, monitor.Sample{
			At:         base.Add(time.Duration(i) * 10 * time.Second),
			CPUPercent: float64(i) * 10, // 0..50, trung bình 25, đỉnh 50
			RSSBytes:   int64(100+i) << 20,
			HeapBytes:  int64(50) << 20,
			Goroutines: 20 + i,
		})
	}
	if err := s.WriteResourceSamples(ctx, samples); err != nil {
		t.Fatalf("write samples: %v", err)
	}

	points, err := s.Resources(ctx, TimeAt(base.Add(-time.Hour)), TimeAt(base.Add(time.Hour)), 60)
	if err != nil {
		t.Fatalf("read resources: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("điểm = %d, muốn 1 (sáu mẫu trong cùng một phút)", len(points))
	}

	p := points[0]
	if p.CPUAvg != 25 {
		t.Errorf("CPU trung bình = %v, muốn 25", p.CPUAvg)
	}
	// Đỉnh phải sống sót qua bước gộp — đó là lý do lưu cả hai cột.
	if p.CPUMax != 50 {
		t.Errorf("CPU đỉnh = %v, muốn 50", p.CPUMax)
	}
	if p.RSSMax != 105<<20 {
		t.Errorf("RSS đỉnh = %d, muốn %d", p.RSSMax, int64(105)<<20)
	}
}

func TestResourcesSplitsAcrossBuckets(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	base := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	err := s.WriteResourceSamples(ctx, []monitor.Sample{
		{At: base, CPUPercent: 1, RSSBytes: 1 << 20},
		{At: base.Add(90 * time.Second), CPUPercent: 2, RSSBytes: 2 << 20},
		{At: base.Add(3 * time.Minute), CPUPercent: 3, RSSBytes: 3 << 20},
	})
	if err != nil {
		t.Fatalf("write samples: %v", err)
	}

	points, err := s.Resources(ctx, TimeAt(base.Add(-time.Hour)), TimeAt(base.Add(time.Hour)), 60)
	if err != nil {
		t.Fatalf("read resources: %v", err)
	}
	if len(points) != 3 {
		t.Fatalf("điểm = %d, muốn 3 (ba phút khác nhau)", len(points))
	}
	// Mốc phải cùng định dạng với cột at, nếu không so sánh chuỗi ở nơi khác sẽ sai.
	if _, err := time.Parse(time.RFC3339, points[0].T); err != nil {
		t.Errorf("mốc %q không phải RFC3339: %v", points[0].T, err)
	}
}

func TestResourceRetentionDropsOldSamples(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	err := s.WriteResourceSamples(ctx, []monitor.Sample{
		{At: now.Add(-40 * 24 * time.Hour), CPUPercent: 1},
		{At: now.Add(-time.Hour), CPUPercent: 2},
	})
	if err != nil {
		t.Fatalf("write samples: %v", err)
	}

	deleted, err := s.PruneResourceSamples(ctx, 30)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("xóa %d dòng, muốn 1", deleted)
	}

	stats, err := s.ResourceStats(ctx, TimeAt(now.Add(-90*24*time.Hour)), TimeAt(now))
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Samples != 1 {
		t.Errorf("còn %d mẫu, muốn 1", stats.Samples)
	}
}

func TestResourceStatsHandlesEmptyRange(t *testing.T) {
	// Bảng rỗng làm avg() trả về NULL. Không xử lý thì Scan hỏng và màn hình tài
	// nguyên trắng ngay lần chạy đầu tiên, đúng lúc chưa có mẫu nào.
	s := newTestStore(t)
	now := time.Now().UTC()

	stats, err := s.ResourceStats(context.Background(),
		TimeAt(now.Add(-time.Hour)), TimeAt(now))
	if err != nil {
		t.Fatalf("stats trên bảng rỗng: %v", err)
	}
	if stats.Samples != 0 || stats.CPUAvg != 0 {
		t.Errorf("stats = %+v, muốn tất cả bằng 0", stats)
	}
}
