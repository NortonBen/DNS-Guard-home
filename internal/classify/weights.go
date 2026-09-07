package classify

// Loại tín hiệu. Chuỗi này đi vào CSDL, vào API và vào giao diện, nên coi như hằng
// số công khai: đổi tên là thay đổi phá vỡ.
const (
	KindCNAMEAdtech  = "cname_adtech"
	KindCNAMEBlocked = "cname_blocked"
	KindASNAdtech    = "asn_adtech"
	KindCertAdtech   = "cert_adtech"

	KindKeyword   = "keyword"
	KindRandomSub = "random_sub"

	KindThirdParty     = "third_party"
	KindThirdPartyWeak = "third_party_weak"
	KindFanOut         = "fan_out"
	KindBeacon         = "beacon"

	KindSpreadHigh   = "spread_high"
	KindSpreadMid    = "spread_mid"
	KindETLD1Blocked = "etld1_blocked"

	KindHighRank         = "high_rank"
	KindLongLivedContent = "long_lived_content"
	KindSharedCDN        = "shared_cdn"

	// Từ phân tích header và HTML tĩnh của trang gốc.
	KindHTTPBeacon         = "http_beacon"
	KindHTTPRedirectAdtech = "http_redirect_adtech"
	KindHTTPParking        = "http_parking"
	KindHTTPP3P            = "http_p3p"
	KindHTTPTrackingCookie = "http_tracking_cookie"
	KindHTTPCORSWildcard   = "http_cors_wildcard"
	KindHTTPEmptyPage      = "http_empty_page"
	KindHTTPRealSite       = "http_real_site"

	// Từ VirusTotal.
	KindVTMalicious = "vt_malicious"
	KindVTClean     = "vt_clean"
)

// Ngưỡng kích hoạt tín hiệu. Khác với trọng số, chúng không sửa được qua giao diện:
// trọng số trả lời "bằng chứng này nặng bao nhiêu", còn ngưỡng định nghĩa bằng chứng
// là gì. Đổi định nghĩa thì phải đổi cả bộ test đi kèm.
const (
	// Ngưỡng entropy 3,4 chọn từ đo đạc thực tế: "a7f3k9x2m1qz" đạt 3,58 trong khi
	// "tracking" chỉ 3,00 và "cdn" là 1,58.
	randomLabelMinLen     = 12
	randomLabelMinEntropy = 3.4

	thirdPartyStrong = 0.80
	thirdPartyWeak   = 0.50
	fanOutMinClients = 5
	fanOutMinRatio   = 0.60
	beaconMinQueries = 50
	beaconMaxCV      = 0.35

	spreadHighMin = 50
	spreadMidMin  = 15

	// Trang gần như không có chữ để đọc.
	emptyPageMaxText = 200
	// Đủ chữ để coi là một trang người ta thật sự đọc.
	realSiteMinText = 1500
	// Cookie xuyên trang sống lâu hơn ngần này ngày là định danh theo dõi.
	trackingCookieMinDays = 90
	// Số engine tối thiểu đồng ý thì kết luận của VirusTotal mới đáng tin. Một engine
	// đơn lẻ báo động là nhiễu nổi tiếng của dịch vụ này.
	vtMaliciousMinEngines = 3
	// Đủ engine đã chấm thì "sạch" mới có nghĩa.
	vtCleanMinEngines = 60

	highRankMax      = 50000
	longLivedMinDays = 5 * 365
)

// Weights là bảng trọng số dùng cho một lần chấm điểm. Sửa được qua giao diện
// (FR-3.5) nên phải truyền vào Score chứ không đọc từ biến toàn cục.
type Weights map[string]float64

// Get trả về trọng số của một loại tín hiệu, lùi về mặc định khi chưa cấu hình.
func (w Weights) Get(kind string) float64 {
	if v, ok := w[kind]; ok {
		return v
	}
	return DefaultWeights[kind]
}

// DefaultWeights là bảng trọng số xuất xưởng, theo docs/06-classification.md §2.
var DefaultWeights = Weights{
	KindCNAMEAdtech:  6.0,
	KindCNAMEBlocked: 5.0,
	KindASNAdtech:    4.0,
	KindCertAdtech:   3.0,

	KindKeyword:   3.0,
	KindRandomSub: 2.0,

	KindThirdParty:     2.5,
	KindThirdPartyWeak: 1.0,
	KindFanOut:         1.5,
	KindBeacon:         2.0,

	KindSpreadHigh:   2.5,
	KindSpreadMid:    1.2,
	KindETLD1Blocked: 4.0,

	KindHighRank:         -8.0,
	KindLongLivedContent: -2.0,
	KindSharedCDN:        -6.0,

	// Không tín hiệu HTTP nào một mình vượt ngưỡng ads (5,5): chúng là bằng chứng bổ
	// trợ cho tín hiệu hạ tầng, không thay thế.
	KindHTTPBeacon:         4.0,
	KindHTTPRedirectAdtech: 4.0,
	KindHTTPParking:        3.0,
	KindHTTPP3P:            2.5,
	KindHTTPTrackingCookie: 2.0,
	KindHTTPCORSWildcard:   1.0,
	KindHTTPEmptyPage:      1.0,
	KindHTTPRealSite:       -2.0,

	// Vượt ngưỡng malware (3,0) một mình, và đó là chủ ý: ngưỡng đó được đặt thấp
	// chính vì loại bằng chứng này.
	KindVTMalicious: 4.0,
	KindVTClean:     -1.0,
}

// SignalLabels là nhãn tiếng Việt cho giao diện chỉnh trọng số.
var SignalLabels = map[string]string{
	KindCNAMEAdtech:      "CNAME tới hạ tầng adtech",
	KindCNAMEBlocked:     "CNAME tới domain đã bị chặn",
	KindASNAdtech:        "ASN thuần adtech",
	KindCertAdtech:       "Chứng chỉ chung với hạ tầng adtech",
	KindKeyword:          "Tên chứa từ khóa quảng cáo",
	KindRandomSub:        "Subdomain sinh ngẫu nhiên",
	KindThirdParty:       "Luôn xuất hiện sau domain khác",
	KindThirdPartyWeak:   "Thường xuất hiện sau domain khác",
	KindFanOut:           "Xuất hiện trên nhiều client",
	KindBeacon:           "Truy vấn đều đặn như máy",
	KindSpreadHigh:       "Rất nhiều subdomain cùng gốc",
	KindSpreadMid:        "Nhiều subdomain cùng gốc",
	KindETLD1Blocked:     "Tên miền gốc đã bị chặn",
	KindHighRank:         "Thứ hạng Tranco cao",
	KindLongLivedContent: "Domain lâu đời, không có dấu hiệu hạ tầng",
	KindSharedCDN:        "Phân giải về CDN dùng chung",

	KindHTTPBeacon:         "Không phục vụ trang, chỉ trả pixel hoặc rỗng",
	KindHTTPRedirectAdtech: "Chuyển hướng tới hạ tầng adtech",
	KindHTTPParking:        "Trang đỗ tên miền",
	KindHTTPP3P:            "Có header P3P (thủ thuật cookie của mạng quảng cáo)",
	KindHTTPTrackingCookie: "Cookie xuyên trang sống lâu",
	KindHTTPCORSWildcard:   "Mở CORS cho mọi nguồn, không phải trang web",
	KindHTTPEmptyPage:      "Trang trống, không có nội dung đọc được",
	KindHTTPRealSite:       "Trang thật, có tiêu đề và nội dung",

	KindVTMalicious: "VirusTotal: nhiều engine báo độc hại",
	KindVTClean:     "VirusTotal: sạch",
}

// infraKinds là các tín hiệu hạ tầng, dùng để tính độ tin cậy và để quyết định
// tín hiệu long_lived_content có được kích hoạt hay không.
var infraKinds = map[string]bool{
	KindCNAMEAdtech:  true,
	KindCNAMEBlocked: true,
	KindASNAdtech:    true,
	KindCertAdtech:   true,

	// Hai tín hiệu HTTP này nói về chính hạ tầng của domain chứ không về cách trang
	// kiếm tiền, nên chúng cũng nâng độ tin cậy như nhóm hạ tầng.
	KindHTTPBeacon:         true,
	KindHTTPRedirectAdtech: true,
}

// IsInfraKind cho biết một loại tín hiệu có thuộc nhóm hạ tầng không.
func IsInfraKind(kind string) bool { return infraKinds[kind] }
