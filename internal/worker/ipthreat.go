package worker

import (
	"context"
	"net/netip"

	"github.com/benji/dnsguard/internal/store"
)

// KindThreatAlert là loại sự kiện đẩy về giao diện khi phát hiện địa chỉ độc hại mới.
const KindThreatAlert = "threat_alert"

// runIPThreat đối chiếu mọi địa chỉ đã quan sát với danh sách hạ tầng độc hại.
//
// Quét lại toàn bộ mỗi lượt chứ không chỉ địa chỉ mới, và đó là chủ ý: danh sách thay
// đổi theo thời gian, nên một địa chỉ sạch tuần trước có thể bị liệt kê tuần này. Quét
// lại cũng khiến cảnh báo tự tắt khi địa chỉ được gỡ khỏi danh sách — không cần cơ chế
// dọn dẹp riêng nào.
//
// Chi phí chấp nhận được vì tra cứu chỉ là vài chục lần tra map trong bộ nhớ, và chỉ
// những dòng thật sự đổi trạng thái mới bị ghi xuống.
func (r *Runner) runIPThreat(ctx context.Context) error {
	if !r.threats.Loaded() {
		// Chưa tải danh sách nào không phải lỗi: các bảng đều là tuỳ chọn. Báo hỏng
		// mỗi giờ sẽ chôn những job hỏng thật trong nhật ký.
		r.log.Info("bỏ qua đối chiếu địa chỉ độc hại: chưa nạp danh sách nào")
		return nil
	}

	state, err := r.store.IPThreatState(ctx)
	if err != nil {
		return err
	}

	var changes []store.IPThreat
	var flagged []store.IPThreat
	for ip, was := range state {
		addr, err := netip.ParseAddr(ip)
		if err != nil {
			continue
		}
		now := store.IPThreat{IP: ip}
		if m, hit := r.threats.Lookup(addr); hit {
			now.Threat, now.Source = m.Prefix, m.Source
		}
		// So cả nguồn lẫn dải: một địa chỉ chuyển từ danh sách này sang danh sách khác
		// là thông tin mới, kể cả khi dải khớp tình cờ giống nhau.
		if now.Threat == was.Threat && now.Source == was.Source {
			continue
		}
		changes = append(changes, now)
		// Chỉ báo động khi *dải* đổi. Sau migration thêm cột nguồn, mọi địa chỉ đang bị
		// đánh dấu đều có nguồn rỗng và sẽ được điền ở lượt chạy này — điền lại xuất xứ
		// cho một cảnh báo đã báo rồi không phải phát hiện mới, và phát lại tất cả sẽ
		// dạy người vận hành bỏ qua chúng.
		if now.Threat != "" && now.Threat != was.Threat {
			flagged = append(flagged, now)
		}
	}

	if len(changes) == 0 {
		return nil
	}
	if _, err := r.store.SetIPThreats(ctx, changes); err != nil {
		return err
	}
	r.log.Info("đối chiếu địa chỉ độc hại xong",
		"đổi", len(changes), "mới bị đánh dấu", len(flagged))

	// Chỉ báo cho địa chỉ *mới* bị đánh dấu. Phát lại toàn bộ danh sách ở mỗi lượt
	// chạy sẽ khiến người vận hành quen với việc bỏ qua chúng.
	for _, f := range flagged {
		r.bus.Publish(KindThreatAlert, map[string]any{
			"ip":     f.IP,
			"threat": f.Threat,
			"source": f.Source,
		})
	}
	return nil
}
