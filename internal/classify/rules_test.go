package classify

import (
	"slices"
	"strings"
	"testing"
)

func TestCustomAdtechDomainChangesScore(t *testing.T) {
	// Lý do tồn tại của cả tính năng: một mạng quảng cáo nội địa không có trong danh
	// sách dựng sẵn phải thêm vào được, và thêm xong thì phải thật sự đổi kết quả.
	d := Domain{Name: "tracker.trangweb.vn", ETLD1: "trangweb.vn"}
	f := Facts{CNAMEChain: []string{"edge.quangcaonoidia.vn"}}

	before := Score(d, f, DefaultWeights)
	if hasSignal(before, KindCNAMEAdtech) {
		t.Fatal("luật dựng sẵn đã biết tên miền này — chọn tên khác cho test")
	}

	rules := Merge(Custom{
		AdtechDomains: map[string]Category{"quangcaonoidia.vn": CategoryAds},
	})
	after := ScoreWith(d, f, DefaultWeights, rules)

	if !hasSignal(after, KindCNAMEAdtech) {
		t.Fatalf("luật tự đặt không kích hoạt cname_adtech: %+v", after.Signals)
	}
	if after.Score <= before.Score {
		t.Errorf("điểm sau = %v, trước = %v — luật tự đặt phải làm tăng điểm",
			after.Score, before.Score)
	}
	if after.Category != CategoryAds {
		t.Errorf("nhãn = %q, muốn ads (nhãn đi theo đích CNAME)", after.Category)
	}
}

func TestCustomKeywordNeedsCorroboration(t *testing.T) {
	// Từ khóa một mình không đủ để gán nhãn chặn, kể cả từ khóa tự đặt. Đây là lớp
	// bảo vệ chính khiến một từ khóa gõ rộng tay không tự nó chặn nhầm hàng loạt.
	rules := Merge(Custom{
		Keywords: map[Category][]string{CategoryAds: {"quangcao"}},
	})

	d := Domain{Name: "quangcao.congty.vn", ETLD1: "congty.vn"}
	got := ScoreWith(d, Facts{}, DefaultWeights, rules)

	if !hasSignal(got, KindKeyword) {
		t.Fatalf("từ khóa tự đặt không kích hoạt: %+v", got.Signals)
	}
	if got.Category == CategoryAds {
		t.Error("gán nhãn ads chỉ dựa vào từ khóa, không có bằng chứng nào khác")
	}
}

func TestCustomSharedCDNProtects(t *testing.T) {
	// Danh sách CDN dùng chung là danh sách bảo vệ. Thêm vào phải kéo điểm xuống.
	d := Domain{Name: "asset.noibo.vn", ETLD1: "noibo.vn"}
	f := Facts{CNAMEChain: []string{"cdn-noi-bo.vn"}}

	before := Score(d, f, DefaultWeights)
	rules := Merge(Custom{SharedCDN: []string{"cdn-noi-bo.vn"}})
	after := ScoreWith(d, f, DefaultWeights, rules)

	if !hasSignal(after, KindSharedCDN) {
		t.Fatalf("không kích hoạt shared_cdn: %+v", after.Signals)
	}
	if after.Score >= before.Score {
		t.Errorf("điểm sau = %v, trước = %v — CDN dùng chung phải kéo điểm xuống",
			after.Score, before.Score)
	}
}

func TestCustomThresholdChangesSignal(t *testing.T) {
	// Ngưỡng độ phủ phụ thuộc quy mô mạng: năm mươi subdomain là nhiều với mạng gia
	// đình nhưng bình thường với một văn phòng lớn.
	d := Domain{Name: "a.vidu.vn", ETLD1: "vidu.vn", SubdomainCount: 30}

	before := Score(d, Facts{}, DefaultWeights)
	if hasSignal(before, KindSpreadHigh) {
		t.Fatal("30 subdomain đã vượt ngưỡng mặc định — test không còn ý nghĩa")
	}

	rules := Merge(Custom{Thresholds: &Thresholds{SpreadHighMin: 20}})
	after := ScoreWith(d, Facts{}, DefaultWeights, rules)

	if !hasSignal(after, KindSpreadHigh) {
		t.Errorf("hạ ngưỡng xuống 20 mà spread_high không kích hoạt: %+v", after.Signals)
	}
}

func TestMergeAddsWithoutRemovingBuiltins(t *testing.T) {
	// Chỉ thêm, không bớt. Nếu gộp thay thế thì bản nâng cấp bổ sung tên miền adtech
	// mới sẽ bị một bản chụp cũ trong CSDL đè lên, và không ai nhận ra.
	defaults := DefaultRules()
	merged := Merge(Custom{
		AdtechDomains: map[string]Category{"quangcaoabc.vn": CategoryAds},
		SharedCDN:     []string{"cdn-noi-bo.vn"},
		Keywords:      map[Category][]string{CategoryAds: {"quangcao"}},
	})

	if _, ok := merged.AdtechDomains["doubleclick.net"]; !ok {
		t.Error("gộp làm mất tên miền dựng sẵn")
	}
	if len(merged.AdtechDomains) != len(defaults.AdtechDomains)+1 {
		t.Errorf("số tên miền = %d, muốn %d", len(merged.AdtechDomains), len(defaults.AdtechDomains)+1)
	}
	if !slices.Contains(merged.SharedCDN, "akamai.net") {
		t.Error("gộp làm mất hậu tố CDN dựng sẵn")
	}
	if !slices.Contains(merged.Keywords[CategoryAds], "adserv") {
		t.Error("gộp làm mất từ khóa dựng sẵn")
	}
}

func TestMergeIsIdempotent(t *testing.T) {
	// Lưu cùng một luật hai lần không được nhân đôi danh sách.
	custom := Custom{
		SharedCDN: []string{"akamai.net", "cdn-noi-bo.vn", "cdn-noi-bo.vn"},
		Keywords:  map[Category][]string{CategoryAds: {"adserv", "quangcao"}},
	}
	a, b := Merge(custom), Merge(custom)

	if len(a.SharedCDN) != len(b.SharedCDN) {
		t.Errorf("gộp hai lần cho số lượng khác nhau: %d và %d", len(a.SharedCDN), len(b.SharedCDN))
	}
	if n := countOccurrences(a.SharedCDN, "akamai.net"); n != 1 {
		t.Errorf("akamai.net xuất hiện %d lần, muốn 1", n)
	}
	if n := countOccurrences(a.Keywords[CategoryAds], "adserv"); n != 1 {
		t.Errorf("adserv xuất hiện %d lần, muốn 1", n)
	}
}

func TestMergeDoesNotMutateDefaults(t *testing.T) {
	// DefaultRules trả về bản sao. Không sao chép thì luật tự đặt của một lần gọi
	// rò rỉ sang mọi lần gọi sau — kể cả sau khi người dùng đã xóa nó đi.
	Merge(Custom{
		AdtechDomains: map[string]Category{"quangcaoabc.vn": CategoryAds},
		SharedCDN:     []string{"cdn-noi-bo.vn"},
		Keywords:      map[Category][]string{CategoryAds: {"quangcao"}},
	})

	fresh := DefaultRules()
	if _, leaked := fresh.AdtechDomains["quangcaoabc.vn"]; leaked {
		t.Error("luật tự đặt rò rỉ vào danh sách dựng sẵn")
	}
	if slices.Contains(fresh.SharedCDN, "cdn-noi-bo.vn") {
		t.Error("hậu tố tự đặt rò rỉ vào danh sách dựng sẵn")
	}
	if slices.Contains(fresh.Keywords[CategoryAds], "quangcao") {
		t.Error("từ khóa tự đặt rò rỉ vào danh sách dựng sẵn")
	}
}

func TestValidateRejectsDangerousRules(t *testing.T) {
	cases := []struct {
		name   string
		custom Custom
		field  string
	}{
		{
			"ASN trung tính",
			Custom{AdtechASNs: map[int]ASNInfo{15169: {Org: "Google", Category: CategoryAds}}},
			"adtech_asns",
		},
		{
			"tên miền được bảo vệ cứng",
			Custom{AdtechDomains: map[string]Category{"paypal.com": CategoryAds}},
			"adtech_domains",
		},
		{
			"tên miền trùng CDN dùng chung",
			Custom{AdtechDomains: map[string]Category{"jsdelivr.net": CategoryAds}},
			"adtech_domains",
		},
		{
			"từ khóa quá ngắn",
			Custom{Keywords: map[Category][]string{CategoryAds: {"ad"}}},
			"keywords",
		},
		{
			"phân loại không tồn tại",
			Custom{AdtechDomains: map[string]Category{"quangcaoabc.vn": "khong-ton-tai"}},
			"adtech_domains",
		},
		{
			"ngưỡng ngoài khoảng",
			Custom{Thresholds: &Thresholds{VTMaliciousMinEngines: 999}},
			"thresholds",
		},
		{
			"ngưỡng trung bình lớn hơn ngưỡng cao",
			Custom{Thresholds: &Thresholds{SpreadMidMin: 100, SpreadHighMin: 50}},
			"thresholds",
		},
		{
			"không phải tên miền",
			Custom{AdtechDomains: map[string]Category{"khongcodaucham": CategoryAds}},
			"adtech_domains",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := tc.custom.Validate()
			if len(errs) == 0 {
				t.Fatal("chấp nhận luật đáng lẽ phải từ chối")
			}
			if errs[0].Field != tc.field {
				t.Errorf("trường = %q, muốn %q", errs[0].Field, tc.field)
			}
			if errs[0].Reason == "" {
				t.Error("thiếu lý do — người dùng cần biết vì sao bị từ chối")
			}
		})
	}
}

func TestValidateAcceptsReasonableRules(t *testing.T) {
	custom := Custom{
		AdtechDomains: map[string]Category{"quangcaoabc.vn": CategoryAds},
		AdtechASNs:    map[int]ASNInfo{131429: {Org: "Mạng nội địa", Category: CategoryTracking}},
		SharedCDN:     []string{"cdn-noi-bo.vn"},
		Keywords:      map[Category][]string{CategoryAds: {"quangcao"}},
		Thresholds:    &Thresholds{SpreadHighMin: 40, SpreadMidMin: 10},
	}
	if errs := custom.Validate(); len(errs) > 0 {
		t.Errorf("từ chối luật hợp lệ: %+v", errs)
	}
}

func TestValidateReportsEveryProblemAtOnce(t *testing.T) {
	// Người dùng dán vào một danh sách dài thì cần biết hết vấn đề trong một lần,
	// không phải sửa từng cái rồi bấm lưu lại mười lần.
	custom := Custom{
		AdtechDomains: map[string]Category{
			"paypal.com":     CategoryAds,
			"jsdelivr.net":   CategoryAds,
			"khongcodaucham": CategoryAds,
		},
	}
	if errs := custom.Validate(); len(errs) != 3 {
		t.Errorf("báo %d lỗi, muốn 3: %+v", len(errs), errs)
	}
}

func TestNeutralASNsStayProtected(t *testing.T) {
	// Kiểm tra ở tầng chấm điểm chứ không chỉ ở tầng kiểm tra đầu vào: dù bằng cách
	// nào đó một ASN trung tính lọt vào cấu hình, nó vẫn không được kích hoạt.
	rules := Merge(Custom{})
	rules.AdtechASNs[15169] = ASNInfo{Org: "Google", Category: CategoryAds}

	d := Domain{Name: "vidu.vn", ETLD1: "vidu.vn"}
	got := ScoreWith(d, Facts{ASN: 15169}, DefaultWeights, rules)

	if hasSignal(got, KindASNAdtech) {
		t.Error("ASN trung tính kích hoạt asn_adtech — chặn nửa Internet chỉ bằng một dòng cấu hình")
	}
}

func TestNormalizeHostAcceptsMessyInput(t *testing.T) {
	for input, want := range map[string]string{
		"  QuangCao.VN  ": "quangcao.vn",
		"*.quangcao.vn":   "quangcao.vn",
		"quangcao.vn.":    "quangcao.vn",
		"*.QuangCao.VN.":  "quangcao.vn",
	} {
		if got := normalizeHost(input); got != want {
			t.Errorf("normalizeHost(%q) = %q, muốn %q", input, got, want)
		}
	}
}

func TestHighRankOutweighsAMistakenAdtechEntry(t *testing.T) {
	// Trường hợp nguy hiểm nhất của tính năng này: cname_adtech mang +6,0 còn ngưỡng
	// ads là 5,5, nên một tên miền thêm nhầm về lý thuyết đủ để chặn nó một mình.
	//
	// Với trang xếp hạng cao thì lớp bảo vệ thứ hai đỡ được: high_rank mang −8,0 và
	// lớn hơn mọi tín hiệu dương đơn lẻ, đúng như thiết kế — một trang trong top
	// Tranco bị chấm điểm cao thì khả năng lớn nhất là quy tắc sai chứ không phải
	// trang đó là quảng cáo.
	rules := Merge(Custom{AdtechDomains: map[string]Category{"google.com": CategoryAds}})
	d := Domain{Name: "www.google.com", ETLD1: "google.com"}
	f := Facts{CNAMEChain: []string{"www3.l.google.com"}, TrancoRank: 1}

	got := ScoreWith(d, f, DefaultWeights, rules)
	if got.Score > 0 {
		t.Errorf("điểm = %v, muốn không dương — high_rank phải thắng cname_adtech", got.Score)
	}
	if got.Category != CategoryCDN {
		t.Errorf("nhãn = %q, muốn cdn", got.Category)
	}
}

func TestMistakenAdtechEntryIsUnprotectedWithoutTranco(t *testing.T) {
	// Mặt sau của lớp bảo vệ trên: nó đòi phải có bảng Tranco. Chưa tải bảng thì
	// TrancoRank bằng 0, high_rank không kích hoạt, và một tên miền thêm nhầm tự nó
	// vượt ngưỡng ads.
	//
	// Test này khẳng định điều đó thay vì để nó là một bất ngờ: lớp bảo vệ còn lại
	// là bảng xem trước tác động bắt buộc, và nó phải bắt buộc thật.
	rules := Merge(Custom{AdtechDomains: map[string]Category{"google.com": CategoryAds}})
	d := Domain{Name: "www.google.com", ETLD1: "google.com"}
	f := Facts{CNAMEChain: []string{"www3.l.google.com"}} // chưa có Tranco

	if got := ScoreWith(d, f, DefaultWeights, rules); got.Score < 5.5 {
		t.Errorf("điểm = %v — nếu tín hiệu này đã yếu đi thì cập nhật lại ghi chú rủi ro",
			got.Score)
	}
}

func hasSignal(r Result, kind string) bool {
	for _, s := range r.Signals {
		if s.Kind == kind {
			return true
		}
	}
	return false
}

func countOccurrences(list []string, want string) int {
	n := 0
	for _, v := range list {
		if strings.EqualFold(v, want) {
			n++
		}
	}
	return n
}
