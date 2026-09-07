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
}

// infraKinds là các tín hiệu hạ tầng, dùng để tính độ tin cậy và để quyết định
// tín hiệu long_lived_content có được kích hoạt hay không.
var infraKinds = map[string]bool{
	KindCNAMEAdtech:  true,
	KindCNAMEBlocked: true,
	KindASNAdtech:    true,
	KindCertAdtech:   true,
}

// IsInfraKind cho biết một loại tín hiệu có thuộc nhóm hạ tầng không.
func IsInfraKind(kind string) bool { return infraKinds[kind] }
