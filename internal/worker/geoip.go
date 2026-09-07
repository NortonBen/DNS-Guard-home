package worker

import (
	"context"
	"net/netip"

	"github.com/benji/dnsguard/internal/enrich"
	"github.com/benji/dnsguard/internal/store"
)

// ipLocator là phần bảng tra ASN mà job này cần.
//
// Khai báo ở đây, nơi tiêu thụ, chứ không ở package enrich: worker không cần biết
// gì về nguồn làm giàu ngoài việc "tra được vị trí của một địa chỉ".
type ipLocator interface {
	LookupIP(netip.Addr) (enrich.ASNFacts, bool)
	Loaded() bool
}

// geoIPBatch là số địa chỉ xử lý trong một lượt đọc.
//
// Để rộng tay được vì tra cứu là tìm nhị phân trên bảng trong bộ nhớ, không gọi
// mạng. Ràng buộc thật nằm ở lượt ghi: mỗi lô là một transaction trên pool ghi một
// kết nối, nên lô quá lớn sẽ giữ khóa ghi lâu và làm ingest nghẽn.
const geoIPBatch = 2000

// runGeoIP điền vị trí mạng cho các địa chỉ quan sát được từ bản ghi trả lời DNS.
//
// Chạy nền theo lô thay vì điền lúc ghi: đường ghi phân giải nằm trên đường nóng của
// ingest, và bảng ip2asn là tuỳ chọn — người vận hành có thể chưa tải nó về.
func (r *Runner) runGeoIP(ctx context.Context) error {
	loc, ok := r.ipLocator()
	if !ok || !loc.Loaded() {
		// Chưa nạp bảng không phải lỗi của job: bảng ip2asn là tuỳ chọn, và báo hỏng
		// mỗi giờ sẽ chôn những job hỏng thật trong nhật ký.
		r.log.Info("bỏ qua tra vị trí IP: chưa nạp bảng ip2asn")
		return nil
	}

	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		ips, err := r.store.PendingGeoIPs(ctx, geoIPBatch)
		if err != nil {
			return err
		}
		if len(ips) == 0 {
			break
		}

		geos := make([]store.IPGeo, 0, len(ips))
		for _, raw := range ips {
			addr, err := netip.ParseAddr(raw)
			if err != nil {
				// Địa chỉ không đọc được vẫn phải đánh dấu là đã tra, nếu không nó
				// quay lại hàng đợi ở mọi lượt chạy sau.
				geos = append(geos, store.IPGeo{IP: raw})
				continue
			}
			facts, _ := loc.LookupIP(addr)
			geos = append(geos, store.IPGeo{
				IP: raw, ASN: facts.ASN, Country: facts.Country, Org: facts.Org,
			})
		}

		n, err := r.store.SetIPGeo(ctx, geos)
		if err != nil {
			return err
		}
		total += n

		// Đọc ra được mà không ghi được dòng nào nghĩa là điều kiện chờ không được
		// gỡ. Dừng lại thay vì quay vòng vô hạn trên cùng một lô.
		if n == 0 {
			break
		}
		if len(ips) < geoIPBatch {
			break
		}
	}

	if total > 0 {
		r.log.Info("tra vị trí IP xong", "rows", total)
	}
	return nil
}

// ipLocator lấy bảng tra ASN đã bóc khỏi lớp bọc giới hạn tốc độ.
//
// Dùng Reloadables chứ không dùng Get: Get trả về lớp bọc, vốn chỉ chuyển tiếp
// Enrich và không có LookupIP.
func (r *Runner) ipLocator() (ipLocator, bool) {
	rl, ok := r.enrichers.Reloadables()["asn"]
	if !ok {
		return nil, false
	}
	loc, ok := rl.(ipLocator)
	return loc, ok
}
