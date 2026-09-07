package classify

import "strings"

// hardAllow là danh sách bảo vệ cứng. Nằm trong code, không sửa được qua giao diện:
// sửa phải qua pull request và review. Domain ở đây không bao giờ vào trạng thái
// blocked, kể cả khi quản trị yêu cầu — API trả 403.
//
// Khớp theo hậu tố ở ranh giới nhãn: "vnpay.vn" bảo vệ luôn "api.vnpay.vn".
var hardAllow = []string{
	// Push notification — chặn là hỏng thông báo trên toàn bộ thiết bị
	"push.apple.com", "fcm.googleapis.com", "android.clients.google.com",
	"push.services.mozilla.com", "notify.windows.com",

	// Cập nhật hệ điều hành
	"windowsupdate.com", "update.microsoft.com", "swcdn.apple.com",
	"mesu.apple.com", "gvt1.com",

	// CDN dùng chung — chặn là hỏng hàng loạt trang không liên quan
	"akamai.net", "akamaiedge.net", "akamaized.net", "cloudfront.net",
	"fastly.net", "gstatic.com", "googleapis.com", "cloudflare.com",

	// Thanh toán Việt Nam
	"vnpay.vn", "vnpayment.vn", "momo.vn", "zalopay.vn",
	"napas.com.vn", "onepay.vn", "payoo.vn", "vietqr.io",

	// Thanh toán quốc tế
	"paypal.com", "stripe.com", "visa.com", "mastercard.com",

	// Hạ tầng
	"pool.ntp.org", "ntp.org", "time.apple.com", "time.windows.com",
	"root-servers.net",
}

// HardAllowList trả về bản sao của danh sách bảo vệ cứng, cho giao diện hiển thị.
func HardAllowList() []string {
	out := make([]string, len(hardAllow))
	copy(out, hardAllow)
	return out
}

// IsProtected cho biết domain có được bảo vệ không, và bởi luật nào.
//
// softAllow là danh sách mềm do người dùng quản lý trong settings; nó tuân theo
// cùng quy tắc khớp hậu tố.
func IsProtected(name string, softAllow []string) (rule string, protected bool) {
	name = strings.TrimSuffix(strings.ToLower(name), ".")

	for _, suffix := range hardAllow {
		if matchesSuffix(name, suffix) {
			return suffix, true
		}
	}
	for _, suffix := range softAllow {
		suffix = strings.TrimSpace(strings.ToLower(suffix))
		if suffix == "" {
			continue
		}
		if matchesSuffix(name, suffix) {
			return suffix, true
		}
	}
	return "", false
}
