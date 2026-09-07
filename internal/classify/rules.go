package classify

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Rules là dữ liệu tham chiếu mà bộ chấm điểm tra cứu: tên miền adtech đã biết, ASN
// adtech, hậu tố CDN dùng chung, từ khóa, và các ngưỡng định nghĩa "bằng chứng là gì".
//
// Tách ra khỏi code để người vận hành bổ sung được kiến thức về mạng của mình. Danh
// sách dựng sẵn lấy theo hạ tầng adtech quốc tế; một mạng ở Việt Nam gặp những mạng
// quảng cáo nội địa mà danh sách đó không biết, và trước đây không có cách nào thêm
// vào ngoài việc gửi pull request.
//
// Truyền vào ScoreWith chứ không đọc từ biến toàn cục, vì đó là điều giữ cho việc
// chấm điểm thuần túy — và chính tính thuần túy làm bảng xem trước tác động chính xác
// tuyệt đối thay vì ước lượng.
type Rules struct {
	AdtechDomains map[string]Category
	AdtechASNs    map[int]ASNInfo
	SharedCDN     []string
	Keywords      map[Category][]string
	Thresholds    Thresholds
}

// ASNInfo mô tả một ASN adtech đã biết.
type ASNInfo struct {
	Org      string   `json:"org"`
	Category Category `json:"category"`
}

// Thresholds là các ngưỡng định nghĩa khi nào một tín hiệu kích hoạt.
//
// Chỉ mở ra những ngưỡng thật sự phụ thuộc quy mô mạng. Ngưỡng entropy hay số ký tự
// tối thiểu của nhãn ngẫu nhiên không nằm ở đây: chúng đo đặc tính của chuỗi, không
// đo mạng, và đổi chúng mà không đổi bộ test đi kèm là cách nhanh nhất để phá bộ
// phân loại.
type Thresholds struct {
	// FanOutMinClients: số thiết bị tối thiểu để coi là "nhiều máy cùng gọi".
	// Mạng mười thiết bị và mạng năm trăm thiết bị cần con số khác nhau.
	FanOutMinClients int `json:"fanout_min_clients"`
	// BeaconMinQueries: số truy vấn tối thiểu trước khi xét tính đều đặn.
	BeaconMinQueries int `json:"beacon_min_queries"`
	// SpreadHighMin, SpreadMidMin: số thiết bị để coi độ phủ là cao hoặc trung bình.
	SpreadHighMin int `json:"spread_high_min"`
	SpreadMidMin  int `json:"spread_mid_min"`
	// VTMaliciousMinEngines: số engine tối thiểu đồng ý thì kết luận VirusTotal mới
	// đáng tin. Một engine đơn lẻ báo động là nhiễu nổi tiếng của dịch vụ này.
	VTMaliciousMinEngines int `json:"vt_malicious_min_engines"`
	// HighRankMax: thứ hạng Tranco tối đa còn được coi là trang phổ biến cần bảo vệ.
	HighRankMax int `json:"high_rank_max"`
}

// DefaultThresholds trả về ngưỡng dựng sẵn.
func DefaultThresholds() Thresholds {
	return Thresholds{
		FanOutMinClients:      fanOutMinClients,
		BeaconMinQueries:      beaconMinQueries,
		SpreadHighMin:         spreadHighMin,
		SpreadMidMin:          spreadMidMin,
		VTMaliciousMinEngines: vtMaliciousMinEngines,
		HighRankMax:           highRankMax,
	}
}

// DefaultRules trả về bộ luật dựng sẵn trong mã nguồn.
func DefaultRules() Rules {
	return Rules{
		AdtechDomains: maps.Clone(adtechDomains),
		AdtechASNs:    maps.Clone(adtechASNs),
		SharedCDN:     slices.Clone(sharedCDNSuffixes),
		Keywords:      cloneKeywords(keywordGroups),
		Thresholds:    DefaultThresholds(),
	}
}

func cloneKeywords(src map[Category][]string) map[Category][]string {
	out := make(map[Category][]string, len(src))
	for cat, words := range src {
		out[cat] = slices.Clone(words)
	}
	return out
}

// Custom là phần luật do người vận hành thêm vào.
//
// Chỉ có phần thêm, không có phần bớt. Hai lý do: bản nâng cấp bổ sung mục mới vào
// danh sách dựng sẵn vẫn có tác dụng, và người dùng không xóa nhầm được lớp bảo vệ
// đã dựng sẵn — gỡ Google khỏi danh sách ASN trung tính là chặn nửa Internet.
type Custom struct {
	AdtechDomains map[string]Category   `json:"adtech_domains,omitempty"`
	AdtechASNs    map[int]ASNInfo       `json:"adtech_asns,omitempty"`
	SharedCDN     []string              `json:"shared_cdn,omitempty"`
	Keywords      map[Category][]string `json:"keywords,omitempty"`
	Thresholds    *Thresholds           `json:"thresholds,omitempty"`
}

// IsZero cho biết chưa có luật tự đặt nào.
func (c Custom) IsZero() bool {
	return len(c.AdtechDomains) == 0 && len(c.AdtechASNs) == 0 &&
		len(c.SharedCDN) == 0 && len(c.Keywords) == 0 && c.Thresholds == nil
}

// Merge gộp luật tự đặt lên trên luật dựng sẵn và trả về bộ luật hoàn chỉnh.
func Merge(custom Custom) Rules {
	r := DefaultRules()

	for domain, cat := range custom.AdtechDomains {
		r.AdtechDomains[normalizeHost(domain)] = cat
	}
	for asn, info := range custom.AdtechASNs {
		r.AdtechASNs[asn] = info
	}
	for _, suffix := range custom.SharedCDN {
		suffix = normalizeHost(suffix)
		if suffix != "" && !slices.Contains(r.SharedCDN, suffix) {
			r.SharedCDN = append(r.SharedCDN, suffix)
		}
	}
	for cat, words := range custom.Keywords {
		for _, w := range words {
			w = strings.ToLower(strings.TrimSpace(w))
			if w != "" && !slices.Contains(r.Keywords[cat], w) {
				r.Keywords[cat] = append(r.Keywords[cat], w)
			}
		}
	}
	if custom.Thresholds != nil {
		r.Thresholds = MergeThresholds(r.Thresholds, *custom.Thresholds)
	}
	return r
}

// MergeThresholds lấy giá trị tự đặt khi nó khác 0, giữ mặc định khi bằng 0.
//
// Số 0 nghĩa là "không đặt" chứ không phải "đặt bằng không": mọi ngưỡng ở đây bằng 0
// đều vô nghĩa — một ngưỡng số thiết bị bằng 0 khiến tín hiệu luôn kích hoạt.
func MergeThresholds(base, custom Thresholds) Thresholds {
	pick := func(a, b int) int {
		if b != 0 {
			return b
		}
		return a
	}
	return Thresholds{
		FanOutMinClients:      pick(base.FanOutMinClients, custom.FanOutMinClients),
		BeaconMinQueries:      pick(base.BeaconMinQueries, custom.BeaconMinQueries),
		SpreadHighMin:         pick(base.SpreadHighMin, custom.SpreadHighMin),
		SpreadMidMin:          pick(base.SpreadMidMin, custom.SpreadMidMin),
		VTMaliciousMinEngines: pick(base.VTMaliciousMinEngines, custom.VTMaliciousMinEngines),
		HighRankMax:           pick(base.HighRankMax, custom.HighRankMax),
	}
}

// NeutralASNCount là số ASN trung tính dựng sẵn.
//
// Giao diện cần con số này để nói rõ có bao nhiêu ASN không đánh dấu adtech được, mà
// không cần mở ra cả danh sách vốn không sửa được.
func NeutralASNCount() int { return len(neutralASNs) }

// normalizeHost chuẩn hóa một tên miền để so khớp: chữ thường, bỏ dấu chấm cuối và
// tiền tố wildcard.
func normalizeHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.TrimPrefix(host, "*.")
	return strings.TrimSuffix(host, ".")
}

// ── Tra cứu ─────────────────────────────────────────────────────────────────

// adtechSuffix cho biết host có nằm dưới một tên miền adtech đã biết không, và trả
// về phân loại của tên miền đó. Khớp ở ranh giới nhãn: "eulerian.net" bắt
// "x.eulerian.net" nhưng không bắt "noteulerian.net".
func (r Rules) adtechSuffix(host string) (Category, string, bool) {
	host = normalizeHost(host)
	for suffix, cat := range r.AdtechDomains {
		if matchesSuffix(host, suffix) {
			return cat, suffix, true
		}
	}
	return "", "", false
}

// sharedCDN cho biết host có thuộc hạ tầng CDN dùng chung không.
func (r Rules) sharedCDN(host string) (string, bool) {
	host = normalizeHost(host)
	for _, suffix := range r.SharedCDN {
		if matchesSuffix(host, suffix) {
			return suffix, true
		}
	}
	return "", false
}

// asnInfo tra một ASN, bỏ qua các ASN trung tính.
//
// Danh sách trung tính luôn thắng và không sửa được qua giao diện: Google chứa cả
// doubleclick lẫn google.com, Cloudflare chứa gần như mọi thứ. Cho phép đánh dấu
// chúng là adtech nghĩa là cho phép chặn nửa Internet bằng một ô nhập.
func (r Rules) asnInfo(asn int) (ASNInfo, bool) {
	if asn == 0 {
		return ASNInfo{}, false
	}
	if _, neutral := neutralASNs[asn]; neutral {
		return ASNInfo{}, false
	}
	info, ok := r.AdtechASNs[asn]
	return info, ok
}

// ── Kiểm tra đầu vào ────────────────────────────────────────────────────────

// minKeywordLength là độ dài từ khóa tối thiểu.
//
// Từ khóa khớp theo chuỗi con, nên "ad" bắt luôn "download.microsoft.com",
// "thread.vn" và "adidas.vn". Ba ký tự vẫn rộng, nhưng dưới mức đó thì gần như chắc
// chắn là lỗi chứ không phải chủ ý.
const minKeywordLength = 3

// ValidationError mô tả một mục bị từ chối, kèm lý do đọc được.
type ValidationError struct {
	Field  string `json:"field"`
	Value  string `json:"value"`
	Reason string `json:"reason"`
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s %q: %s", e.Field, e.Value, e.Reason)
}

// thresholdBounds là khoảng hợp lệ của mỗi ngưỡng.
//
// Chặn biên chứ không tin người dùng: một ngưỡng đặt sai không báo lỗi mà chỉ âm
// thầm làm bộ phân loại sai — hoặc không tín hiệu nào kích hoạt nữa, hoặc mọi domain
// đều kích hoạt.
var thresholdBounds = map[string]struct {
	min, max int
	get      func(Thresholds) int
}{
	"fanout_min_clients":       {2, 1000, func(t Thresholds) int { return t.FanOutMinClients }},
	"beacon_min_queries":       {10, 100000, func(t Thresholds) int { return t.BeaconMinQueries }},
	"spread_high_min":          {5, 10000, func(t Thresholds) int { return t.SpreadHighMin }},
	"spread_mid_min":           {2, 10000, func(t Thresholds) int { return t.SpreadMidMin }},
	"vt_malicious_min_engines": {1, 20, func(t Thresholds) int { return t.VTMaliciousMinEngines }},
	"high_rank_max":            {1000, 1000000, func(t Thresholds) int { return t.HighRankMax }},
}

// Validate kiểm tra luật tự đặt và trả về mọi mục bị từ chối.
//
// Trả về tất cả lỗi chứ không dừng ở lỗi đầu: người dùng dán vào một danh sách ba
// mươi tên miền thì cần biết cả ba mươi vấn đề trong một lần, không phải sửa từng
// cái rồi bấm lưu lại ba mươi lần.
func (c Custom) Validate() []ValidationError {
	var errs []ValidationError

	for domain, cat := range c.AdtechDomains {
		host := normalizeHost(domain)
		switch {
		case host == "":
			errs = append(errs, ValidationError{"adtech_domains", domain, "tên miền rỗng"})
		case !strings.Contains(host, "."):
			errs = append(errs, ValidationError{"adtech_domains", domain,
				"phải là tên miền đầy đủ, ví dụ quangcaoabc.vn"})
		case isHardProtected(host):
			// Danh sách bảo vệ cứng thắng, và nói ra ngay tại đây thay vì để mục đó
			// nằm im trong cấu hình rồi không bao giờ có tác dụng.
			errs = append(errs, ValidationError{"adtech_domains", domain,
				"nằm trong danh sách bảo vệ cứng"})
		case !validCategory(cat):
			errs = append(errs, ValidationError{"adtech_domains", domain,
				"phân loại không hợp lệ: " + string(cat)})
		default:
			if suffix, ok := DefaultRules().sharedCDN(host); ok {
				errs = append(errs, ValidationError{"adtech_domains", domain,
					"trùng hạ tầng CDN dùng chung (" + suffix + "), chặn theo tên miền sẽ chặn nhầm hàng loạt"})
			}
		}
	}

	for asn, info := range c.AdtechASNs {
		switch {
		case asn <= 0:
			errs = append(errs, ValidationError{"adtech_asns", fmt.Sprint(asn), "ASN phải là số dương"})
		case neutralASNs[asn] != "":
			errs = append(errs, ValidationError{"adtech_asns", fmt.Sprint(asn),
				"ASN " + neutralASNs[asn] + " chứa lẫn hạ tầng thường, đánh dấu adtech sẽ chặn nhầm hàng loạt"})
		case !validCategory(info.Category):
			errs = append(errs, ValidationError{"adtech_asns", fmt.Sprint(asn),
				"phân loại không hợp lệ: " + string(info.Category)})
		}
	}

	for _, suffix := range c.SharedCDN {
		if host := normalizeHost(suffix); host == "" || !strings.Contains(host, ".") {
			errs = append(errs, ValidationError{"shared_cdn", suffix,
				"phải là tên miền đầy đủ"})
		}
	}

	for cat, words := range c.Keywords {
		if !validCategory(cat) {
			errs = append(errs, ValidationError{"keywords", string(cat),
				"phân loại không hợp lệ"})
			continue
		}
		for _, w := range words {
			word := strings.ToLower(strings.TrimSpace(w))
			switch {
			case word == "":
				errs = append(errs, ValidationError{"keywords", w, "từ khóa rỗng"})
			case len(word) < minKeywordLength:
				errs = append(errs, ValidationError{"keywords", w,
					fmt.Sprintf("phải từ %d ký tự — ngắn hơn thì khớp gần như mọi tên miền",
						minKeywordLength)})
			case strings.ContainsAny(word, " \t/:"):
				errs = append(errs, ValidationError{"keywords", w,
					"từ khóa khớp theo chuỗi con trong tên miền, không chứa khoảng trắng hay dấu gạch chéo"})
			}
		}
	}

	if c.Thresholds != nil {
		for name, bound := range thresholdBounds {
			v := bound.get(*c.Thresholds)
			if v == 0 {
				continue // không đặt
			}
			if v < bound.min || v > bound.max {
				errs = append(errs, ValidationError{"thresholds", fmt.Sprintf("%s=%d", name, v),
					fmt.Sprintf("phải trong khoảng %d–%d", bound.min, bound.max)})
			}
		}
		if t := *c.Thresholds; t.SpreadHighMin != 0 && t.SpreadMidMin != 0 &&
			t.SpreadMidMin >= t.SpreadHighMin {
			errs = append(errs, ValidationError{"thresholds",
				fmt.Sprintf("spread_mid_min=%d spread_high_min=%d", t.SpreadMidMin, t.SpreadHighMin),
				"ngưỡng trung bình phải nhỏ hơn ngưỡng cao"})
		}
	}

	slices.SortFunc(errs, func(a, b ValidationError) int {
		if a.Field != b.Field {
			return strings.Compare(a.Field, b.Field)
		}
		return strings.Compare(a.Value, b.Value)
	})
	return errs
}

// isHardProtected cho biết tên miền có nằm trong danh sách bảo vệ cứng không.
func isHardProtected(host string) bool {
	_, protected := IsProtected(host, nil)
	return protected
}

func validCategory(c Category) bool {
	switch c {
	case CategoryAds, CategoryTracking, CategoryTelemetry, CategoryMalware,
		CategoryCryptomining, CategoryAdult:
		return true
	}
	return false
}
