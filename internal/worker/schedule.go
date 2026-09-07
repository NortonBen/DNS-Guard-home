package worker

import (
	"context"
	"time"

	"github.com/benji/dnsguard/internal/store"
)

// periodic là một job chạy theo chu kỳ.
type periodic struct {
	kind   string
	every  time.Duration
	atBoot bool
}

// Lịch chạy. Chu kỳ chọn theo tốc độ dữ liệu thật sự thay đổi, không phải theo mong
// muốn thấy kết quả sớm: làm giàu quá dày sẽ chạm giới hạn tốc độ của dịch vụ ngoài,
// còn dựng lại đồ thị quá dày sẽ chiếm CSDL suốt ngày.
var schedule = []periodic{
	{kind: JobEnrich, every: 15 * time.Minute, atBoot: true},
	{kind: JobClassify, every: time.Hour, atBoot: true},
	{kind: JobCatalog, every: time.Hour, atBoot: true},
	{kind: JobLifecycle, every: 24 * time.Hour},
	{kind: JobBehavior, every: 24 * time.Hour},
	// Nhịp dày hơn các job hằng ngày vì địa chỉ mới xuất hiện liên tục, và tra cứu
	// chỉ là tìm nhị phân trên bảng cục bộ nên gần như không tốn gì.
	{kind: JobGeoIP, every: time.Hour, atBoot: true},
	// Quét lại toàn bộ địa chỉ mỗi giờ. Không dựa vào dấu "đã kiểm tra" vì danh sách
	// đe dọa thay đổi: địa chỉ sạch hôm qua có thể bị liệt kê hôm nay, và ngược lại.
	{kind: JobIPThreat, every: time.Hour, atBoot: true},
	{kind: JobGraph, every: 24 * time.Hour},
	{kind: JobRetention, every: 24 * time.Hour},
	{kind: JobPublish, every: 6 * time.Hour, atBoot: true},
	// Nhịp thưa hơn hẳn các nguồn khác vì mỗi lượt là tiền. Không chạy lúc khởi
	// động: khởi động lại dịch vụ ba lần trong một buổi chiều gỡ lỗi không nên
	// thành ba hoá đơn.
	{kind: JobAIClassify, every: 6 * time.Hour},
}

// schedule chạy bộ lập lịch: cứ mỗi phút, kiểm tra job nào tới hạn thì xếp hàng.
//
// Lập lịch trong tiến trình thay vì cron hệ thống: một binary tự lo được lịch của
// mình thì không cần thêm thứ gì để cài đặt sai.
func (r *Runner) schedule(ctx context.Context) {
	last := make(map[string]time.Time, len(schedule))

	for _, p := range schedule {
		if p.atBoot {
			// Chạy ngay khi khởi động để một cài đặt mới có dữ liệu sớm, thay vì
			// đợi hết một chu kỳ mới thấy gì.
			r.enqueueIfIdle(ctx, p.kind)
			last[p.kind] = time.Now()
		}
	}

	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			for _, p := range schedule {
				if prev, ok := last[p.kind]; ok && now.Sub(prev) < p.every {
					continue
				}
				r.enqueueIfIdle(ctx, p.kind)
				last[p.kind] = now
			}
		}
	}
}

// enqueueIfIdle chỉ xếp hàng khi chưa có job cùng loại đang chờ.
//
// Không có bước này, một job chậm hơn chu kỳ của nó sẽ tích tụ vô hạn trong hàng đợi.
func (r *Runner) enqueueIfIdle(ctx context.Context, kind string) {
	var pending int
	err := r.store.Reader().QueryRowContext(ctx,
		`SELECT count(*) FROM jobs WHERE kind = ? AND state IN ('pending','running')`,
		kind).Scan(&pending)
	if err != nil {
		r.log.Error("kiểm tra hàng đợi thất bại", "kind", kind, "err", err)
		return
	}
	if pending > 0 {
		return
	}
	if _, err := r.store.EnqueueJob(ctx, kind, nil); err != nil {
		r.log.Error("xếp hàng job thất bại", "kind", kind, "err", err)
	}
}

// Enqueue xếp một job theo yêu cầu từ API.
func (r *Runner) Enqueue(ctx context.Context, kind string, args any) (string, error) {
	return r.store.EnqueueJob(ctx, kind, args)
}

// Store cho phép tầng API dùng chung kết nối CSDL của worker.
func (r *Runner) Store() *store.Store { return r.store }
