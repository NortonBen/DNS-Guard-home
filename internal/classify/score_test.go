package classify

import (
	"slices"
	"testing"
)

// Bảng chấm điểm. Mỗi lần sửa trọng số hoặc thêm tín hiệu phải thêm ít nhất một
// case vào đây — đây là nơi test có giá trị cao nhất, vì logic phức tạp và hậu quả
// sai lớn.
func TestScore(t *testing.T) {
	tests := []struct {
		name       string
		domain     Domain
		facts      Facts
		wantScore  float64
		wantCat    Category
		wantKinds  []string
		wantConfid float64
	}{
		{
			// CNAME cloaking rõ ràng: tên miền trông như của chính trang chủ nhà,
			// nhưng bản ghi CNAME dẫn về hạ tầng của Eulerian.
			name:   "CNAME cloaking rõ ràng",
			domain: Domain{Name: "metrics.trangweb.vn", ETLD1: "trangweb.vn"},
			facts: Facts{
				CNAMEChain: []string{"cdn.eulerian.net", "x.eulerian.net"},
				ASN:        62597,
			},
			// 6,0 cname + 4,0 asn + 3,0 keyword. Từ khóa "metric" trong danh sách
			// mặc định khớp chuỗi con với nhãn "metrics", nên tín hiệu từ vựng cũng
			// kích hoạt bên cạnh hai tín hiệu hạ tầng.
			wantScore:  13.0,
			wantCat:    CategoryTracking,
			wantKinds:  []string{KindCNAMEAdtech, KindASNAdtech, KindKeyword},
			wantConfid: 0.95,
		},
		{
			name:       "domain top Tranco không bị chặn",
			domain:     Domain{Name: "google.com", ETLD1: "google.com"},
			facts:      Facts{TrancoRank: 1, ASN: 15169},
			wantScore:  -8.0,
			wantCat:    CategoryCDN,
			wantKinds:  []string{KindHighRank},
			wantConfid: 0.40,
		},
		{
			// Từ khóa đơn lẻ không đủ để gán nhãn chặn. ASN 45899 là một ISP trong
			// nước, không phải hạ tầng adtech.
			name:       "từ khóa đơn lẻ không đủ để chặn",
			domain:     Domain{Name: "analytics.congty.vn", ETLD1: "congty.vn"},
			facts:      Facts{ASN: 45899},
			wantScore:  3.0,
			wantCat:    CategoryContent,
			wantKinds:  []string{KindKeyword},
			wantConfid: 0.40,
		},
		{
			// ASN của Google chứa cả doubleclick lẫn google.com. Coi nó là adtech
			// sẽ chặn nửa Internet, nên asn_adtech không được kích hoạt ở đây.
			name:       "ASN trung tính không kích hoạt tín hiệu hạ tầng",
			domain:     Domain{Name: "static.trangweb.vn", ETLD1: "trangweb.vn"},
			facts:      Facts{ASN: 15169},
			wantScore:  0,
			wantCat:    CategoryContent,
			wantKinds:  nil,
			wantConfid: 0.40,
		},
		{
			// Truy vấn đều đặn như máy, không có tín hiệu hạ tầng nào.
			name: "beacon đều đặn là telemetry",
			domain: Domain{
				Name: "check.thietbi.vn", ETLD1: "thietbi.vn",
				QueryCount: 2880, QueryIntervalCV: 0.04, ClientCount: 1,
			},
			wantScore:  2.0,
			wantCat:    CategoryTelemetry,
			wantKinds:  []string{KindBeacon},
			wantConfid: 0.40,
		},
		{
			// Máy chủ quảng cáo sinh subdomain theo chiến dịch: hàng trăm cái dưới
			// một gốc, cộng với hành vi third-party trên nhiều client.
			name: "cụm subdomain rộng kèm hành vi third-party",
			domain: Domain{
				Name: "c12.quangcao.vn", ETLD1: "quangcao.vn",
				SubdomainCount: 120, ClientCount: 9, ThirdPartyRatio: 0.94,
			},
			facts: Facts{ETLD1InPublicList: true},
			// 2,5 third_party + 1,5 fan_out + 2,5 spread_high + 4,0 etld1_blocked
			wantScore:  10.5,
			wantCat:    CategoryContent,
			wantKinds:  []string{KindThirdParty, KindFanOut, KindSpreadHigh, KindETLD1Blocked},
			wantConfid: 0.60,
		},
		{
			// Phân giải về CDN dùng chung: điểm âm lớn kéo ra khỏi vùng nguy hiểm
			// kể cả khi tên miền chứa từ khóa.
			name:   "CDN dùng chung được bảo vệ bằng điểm âm",
			domain: Domain{Name: "ads-assets.trangweb.vn", ETLD1: "trangweb.vn"},
			facts:  Facts{CNAMEChain: []string{"d1234.cloudfront.net"}},
			// Danh sách từ khóa mặc định dùng "adserv", "advert"… chứ không có "ads"
			// trần, để không khớp nhầm "downloads" hay "threads". Nên ở đây chỉ
			// tín hiệu âm kích hoạt.
			wantScore:  -6.0,
			wantCat:    CategoryCDN,
			wantKinds:  []string{KindSharedCDN},
			wantConfid: 0.40,
		},
		{
			// Nhãn dài và ngẫu nhiên: subdomain sinh tự động theo phiên.
			name:       "subdomain ngẫu nhiên có entropy cao",
			domain:     Domain{Name: "a7f3k9x2m1qz.tracker.io", ETLD1: "tracker.io"},
			facts:      Facts{},
			wantScore:  5.0, // 3,0 keyword ("tracker") + 2,0 random_sub
			wantCat:    CategoryContent,
			wantKinds:  []string{KindKeyword, KindRandomSub},
			wantConfid: 0.40,
		},
		{
			// Nhãn ngắn tuy entropy cao vẫn không đủ bằng chứng.
			name:      "nhãn ngắn không kích hoạt random_sub",
			domain:    Domain{Name: "x7q.trangweb.vn", ETLD1: "trangweb.vn"},
			facts:     Facts{},
			wantScore: 0,
			wantCat:   CategoryContent,
			wantKinds: nil,
		},
		{
			// Nguồn ngoài đã phân loại thì tin theo nguồn, bất kể tín hiệu khác.
			name:       "nguồn ngoài phân loại malware thắng mọi quy tắc sau",
			domain:     Domain{Name: "verify.example.tk", ETLD1: "example.tk"},
			facts:      Facts{InPublicList: true, PublicListCategories: []Category{CategoryMalware}},
			wantScore:  0,
			wantCat:    CategoryMalware,
			wantKinds:  nil,
			wantConfid: 0.40,
		},
		{
			// Domain lâu đời, không dấu hiệu hạ tầng: kéo xuống bằng điểm âm.
			name:       "domain lâu đời được giảm điểm",
			domain:     Domain{Name: "www.baocu.vn", ETLD1: "baocu.vn"},
			facts:      Facts{AgeDays: 7000},
			wantScore:  -2.0,
			wantCat:    CategoryContent,
			wantKinds:  []string{KindLongLivedContent},
			wantConfid: 0.40,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Score(tc.domain, tc.facts, DefaultWeights)

			if got.Score != tc.wantScore {
				t.Errorf("Score = %.2f, muốn %.2f (tín hiệu: %v)",
					got.Score, tc.wantScore, got.SignalKinds())
			}
			if got.Category != tc.wantCat {
				t.Errorf("Category = %q, muốn %q", got.Category, tc.wantCat)
			}
			if kinds := got.SignalKinds(); !slices.Equal(kinds, tc.wantKinds) {
				t.Errorf("tín hiệu = %v, muốn %v", kinds, tc.wantKinds)
			}
			if tc.wantConfid != 0 && got.Confidence != tc.wantConfid {
				t.Errorf("Confidence = %.2f, muốn %.2f", got.Confidence, tc.wantConfid)
			}
		})
	}
}

// Score phải thuần túy: cùng đầu vào cho cùng đầu ra, không phụ thuộc thứ tự duyệt
// map hay đồng hồ hệ thống. Không có tính chất này thì bảng xem trước tác động khi
// đổi trọng số chỉ là ước lượng.
func TestScoreIsDeterministic(t *testing.T) {
	d := Domain{
		Name: "ads.trangweb.vn", ETLD1: "trangweb.vn",
		SubdomainCount: 60, ClientCount: 8, ThirdPartyRatio: 0.9,
		QueryCount: 500, QueryIntervalCV: 0.1,
	}
	f := Facts{
		CNAMEChain: []string{"x.criteo.com"}, ASN: 394699,
		CertSANs: []string{"*.criteo.com"}, ETLD1InPublicList: true,
	}

	first := Score(d, f, DefaultWeights)
	for i := 0; i < 200; i++ {
		got := Score(d, f, DefaultWeights)
		if got.Score != first.Score || got.Category != first.Category {
			t.Fatalf("lần %d: (%.2f, %s) khác lần đầu (%.2f, %s)",
				i, got.Score, got.Category, first.Score, first.Category)
		}
		if !slices.Equal(got.SignalKinds(), first.SignalKinds()) {
			t.Fatalf("lần %d: thứ tự tín hiệu đổi: %v so với %v",
				i, got.SignalKinds(), first.SignalKinds())
		}
	}
}

// Trọng số sửa qua giao diện phải thực sự đổi kết quả, và Score phải lùi về mặc
// định cho những loại chưa được cấu hình.
func TestScoreUsesConfiguredWeights(t *testing.T) {
	d := Domain{Name: "metrics.trangweb.vn", ETLD1: "trangweb.vn"}
	f := Facts{CNAMEChain: []string{"x.eulerian.net"}}

	base := Score(d, f, DefaultWeights)
	tuned := Score(d, f, Weights{KindCNAMEAdtech: 9.0})

	if diff := tuned.Score - base.Score; diff != 3.0 {
		t.Errorf("chênh lệch điểm = %.2f, muốn 3,0 khi cname_adtech tăng từ 6,0 lên 9,0", diff)
	}
	if !tuned.HasSignal(KindKeyword) {
		t.Error("tín hiệu keyword phải vẫn dùng trọng số mặc định khi không cấu hình")
	}
}

// Bất biến 4 (docs/06-classification.md §4): domain trong danh sách bảo vệ cứng
// không bao giờ được chặn, kể cả khi quản trị yêu cầu.
func TestInvariant_ProtectedDomainCannotBeBlocked(t *testing.T) {
	protected := []string{
		"vnpay.vn", "api.vnpay.vn", "push.apple.com", "fcm.googleapis.com",
		"gstatic.com", "ssl.gstatic.com", "pool.ntp.org", "0.pool.ntp.org",
	}
	for _, name := range protected {
		if rule, ok := IsProtected(name, nil); !ok {
			t.Errorf("%q phải được bảo vệ", name)
		} else if rule == "" {
			t.Errorf("%q được bảo vệ nhưng không nêu được luật nào", name)
		}
	}

	// Khớp hậu tố phải ở ranh giới nhãn, không phải khớp chuỗi con.
	notProtected := []string{"doubleclick.net", "notvnpay.vn", "vnpay.vn.evil.com"}
	for _, name := range notProtected {
		if rule, ok := IsProtected(name, nil); ok {
			t.Errorf("%q không được coi là bảo vệ (khớp nhầm luật %q)", name, rule)
		}
	}

	// Danh sách mềm do người dùng quản lý tuân theo cùng quy tắc.
	if _, ok := IsProtected("noibo.congty.vn", []string{"congty.vn"}); !ok {
		t.Error("danh sách mềm phải khớp theo hậu tố")
	}
}

func TestShannonEntropy(t *testing.T) {
	// Các giá trị đo đạc ở docs/06-classification.md §2.2.
	tests := []struct {
		label string
		want  float64
	}{
		{"a7f3k9x2m1qz", 3.58},
		{"tracking", 3.00},
		{"cdn", 1.58},
		{"www", 0.00},
	}
	for _, tc := range tests {
		if got := round2(shannonEntropy(tc.label)); got != tc.want {
			t.Errorf("entropy(%q) = %.2f, muốn %.2f", tc.label, got, tc.want)
		}
	}
}

// Bảng tín hiệu từ phân tích HTTP và VirusTotal.
func TestScoreHTTPAndVirusTotal(t *testing.T) {
	tests := []struct {
		name      string
		domain    Domain
		facts     Facts
		wantScore float64
		wantCat   Category
		wantKinds []string
	}{
		{
			// Hostname trình duyệt đã phân giải nhưng không trả về trang nào: đó là
			// điểm thu thập, không phải nơi người ta ghé thăm.
			name:   "endpoint trả 204 là điểm thu thập",
			domain: Domain{Name: "c1.thietbi.vn", ETLD1: "thietbi.vn"},
			facts: Facts{HTTP: HTTPAnalysis{
				Fetched: true, Status: 204, ContentType: "",
			}},
			wantScore: 4.0,
			wantCat:   CategoryTelemetry,
			wantKinds: []string{KindHTTPBeacon},
		},
		{
			name:   "ảnh một điểm ảnh là điểm thu thập",
			domain: Domain{Name: "px.dothi.vn", ETLD1: "dothi.vn"},
			facts: Facts{HTTP: HTTPAnalysis{
				Fetched: true, Status: 200, ContentType: "image/gif",
				BodyLen: 43, IsPixel: true,
			}},
			wantScore: 4.0,
			wantCat:   CategoryTelemetry,
			wantKinds: []string{KindHTTPBeacon},
		},
		{
			name:   "trang đỗ tên miền",
			domain: Domain{Name: "tenmiencu.vn", ETLD1: "tenmiencu.vn"},
			facts: Facts{HTTP: HTTPAnalysis{
				Fetched: true, Status: 200, ContentType: "text/html", IsHTML: true,
				Parking: "Sedo", Title: "tenmiencu.vn", TextLen: 400,
			}},
			wantScore: 3.0,
			wantCat:   CategoryAds,
			wantKinds: []string{KindHTTPParking},
		},
		{
			// Phiên bản HTTP của CNAME cloaking: tên trông vô hại, đích thật ở nơi khác.
			name:   "chuyển hướng tới hạ tầng adtech",
			domain: Domain{Name: "go.trangweb.vn", ETLD1: "trangweb.vn"},
			facts: Facts{HTTP: HTTPAnalysis{
				Fetched: true, Status: 302, RedirectTo: "https://x.doubleclick.net/abc",
			}},
			wantScore: 4.0,
			wantCat:   CategoryAds,
			wantKinds: []string{KindHTTPRedirectAdtech},
		},
		{
			name:   "header P3P cộng cookie xuyên trang",
			domain: Domain{Name: "id.dothi.vn", ETLD1: "dothi.vn"},
			facts: Facts{HTTP: HTTPAnalysis{
				Fetched: true, Status: 200, ContentType: "text/html", IsHTML: true,
				P3P: `CP="NOI DSP"`, TrackingCookie: "uid", CookieMaxDays: 730,
				Title: "", TextLen: 50,
			}},
			// 2,5 p3p + 2,0 cookie + 1,0 trang trống
			wantScore: 5.5,
			wantCat:   CategoryContent,
			wantKinds: []string{KindHTTPP3P, KindHTTPTrackingCookie, KindHTTPEmptyPage},
		},
		{
			// Trang thật kéo điểm xuống — bảo vệ nội dung bình thường.
			name:   "trang thật được giảm điểm",
			domain: Domain{Name: "baomoi.vn", ETLD1: "baomoi.vn"},
			facts: Facts{HTTP: HTTPAnalysis{
				Fetched: true, Status: 200, ContentType: "text/html", IsHTML: true,
				Title: "Tin tức 24h", TextLen: 9000,
			}},
			wantScore: -2.0,
			wantCat:   CategoryContent,
			wantKinds: []string{KindHTTPRealSite},
		},
		{
			// Ba engine độc lập đồng ý mới là bằng chứng.
			name:   "VirusTotal ba engine báo độc hại",
			domain: Domain{Name: "verify.example.tk", ETLD1: "example.tk"},
			facts: Facts{VT: VirusTotalResult{
				Checked: true, Known: true, Malicious: 5, Harmless: 60,
			}},
			wantScore: 4.0,
			wantCat:   CategoryMalware,
			wantKinds: []string{KindVTMalicious},
		},
		{
			// Một engine đơn lẻ là nhiễu nổi tiếng của VirusTotal, không tính.
			name:   "VirusTotal một engine không đủ",
			domain: Domain{Name: "trangweb.vn", ETLD1: "trangweb.vn"},
			facts: Facts{VT: VirusTotalResult{
				Checked: true, Known: true, Malicious: 1, Harmless: 30, Undetected: 20,
			}},
			wantScore: 0,
			wantCat:   CategoryContent,
			wantKinds: nil,
		},
		{
			name:   "VirusTotal sạch cho điểm âm nhẹ",
			domain: Domain{Name: "trangweb.vn", ETLD1: "trangweb.vn"},
			facts: Facts{VT: VirusTotalResult{
				Checked: true, Known: true, Malicious: 0, Harmless: 70, Undetected: 20,
			}},
			wantScore: -1.0,
			wantCat:   CategoryContent,
			wantKinds: []string{KindVTClean},
		},
		{
			// Chưa tra thì không có tín hiệu nào, không phải suy đoán bất lợi.
			name:      "chưa phân tích thì không sinh tín hiệu",
			domain:    Domain{Name: "trangweb.vn", ETLD1: "trangweb.vn"},
			facts:     Facts{},
			wantScore: 0,
			wantCat:   CategoryContent,
			wantKinds: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Score(tc.domain, tc.facts, DefaultWeights)

			if got.Score != tc.wantScore {
				t.Errorf("Score = %.2f, muốn %.2f (tín hiệu: %v)",
					got.Score, tc.wantScore, got.SignalKinds())
			}
			if got.Category != tc.wantCat {
				t.Errorf("Category = %q, muốn %q", got.Category, tc.wantCat)
			}
			if kinds := got.SignalKinds(); !slices.Equal(kinds, tc.wantKinds) {
				t.Errorf("tín hiệu = %v, muốn %v", kinds, tc.wantKinds)
			}
		})
	}
}

// Không tín hiệu HTTP nào một mình được phép đẩy domain vượt ngưỡng ads.
//
// Đây là ràng buộc thiết kế, không phải chi tiết cài đặt: phân tích HTTP là bằng
// chứng bổ trợ cho tín hiệu hạ tầng, không thay thế. Một tín hiệu tự nó đủ để chặn
// nghĩa là một lần đoán sai đủ để chặn nhầm.
func TestHTTPSignalAloneNeverCrossesAdsThreshold(t *testing.T) {
	const adsThreshold = 5.5

	httpKinds := []string{
		KindHTTPBeacon, KindHTTPRedirectAdtech, KindHTTPParking, KindHTTPP3P,
		KindHTTPTrackingCookie, KindHTTPCORSWildcard, KindHTTPEmptyPage,
	}
	for _, kind := range httpKinds {
		if w := DefaultWeights.Get(kind); w >= adsThreshold {
			t.Errorf("%s có trọng số %.1f ≥ ngưỡng ads %.1f — một mình nó đủ để chặn",
				kind, w, adsThreshold)
		}
	}

	// VirusTotal là ngoại lệ có chủ ý: nó vượt ngưỡng malware (3,0) một mình, vì
	// ngưỡng đó được đặt thấp chính vì loại bằng chứng này. Nhưng vẫn không được
	// vượt ngưỡng ads.
	if w := DefaultWeights.Get(KindVTMalicious); w >= adsThreshold {
		t.Errorf("vt_malicious có trọng số %.1f ≥ ngưỡng ads %.1f", w, adsThreshold)
	}
}

// Kết luận của model không bao giờ tự nó đủ để chặn, và không bao giờ đủ để vượt
// cả ngưỡng malware — ngưỡng thấp nhất trong hệ.
//
// Ràng buộc này mạnh hơn ràng buộc của nhóm HTTP, và có lý do: tín hiệu HTTP nói
// về một dữ kiện đo được (trang trả pixel, header P3P), còn tín hiệu AI là một
// lời phán đoán không kiểm chứng trực tiếp được. Thứ không giải thích được thì
// càng không được phép tự quyết.
func TestAISignalAloneNeverBlocks(t *testing.T) {
	const (
		adsThreshold     = 5.5
		malwareThreshold = 3.0
	)

	if w := DefaultWeights.Get(KindAIAdtech); w >= malwareThreshold {
		t.Errorf("ai_adtech có trọng số %.1f ≥ ngưỡng malware %.1f — một mình nó đủ để chặn",
			w, malwareThreshold)
	}
	if w := DefaultWeights.Get(KindAIAdtech); w >= adsThreshold {
		t.Errorf("ai_adtech có trọng số %.1f ≥ ngưỡng ads %.1f", w, adsThreshold)
	}
}

// Model nói "tôi không chắc" thì không được tính thành bằng chứng.
//
// Prompt dặn model trả content với độ tin cậy thấp khi không kết luận được. Nếu
// sàn tin cậy không được tôn trọng, chính lời thú nhận đó lại kéo điểm domain
// xuống và che mất tín hiệu thật.
func TestAIVerdictBelowConfidenceFloorIsIgnored(t *testing.T) {
	domain := Domain{Name: "vi-du.com", ETLD1: "vi-du.com"}

	cases := []struct {
		name       string
		verdict    AIVerdict
		wantSignal string
	}{
		{
			name:    "tin cậy thấp thì bỏ qua",
			verdict: AIVerdict{Checked: true, Category: "ads", Confidence: 0.4},
		},
		{
			name:    "chưa hỏi thì bỏ qua",
			verdict: AIVerdict{Category: "ads", Confidence: 0.95},
		},
		{
			name:       "đủ tin cậy thì buộc tội",
			verdict:    AIVerdict{Checked: true, Category: "ads", Confidence: 0.9},
			wantSignal: KindAIAdtech,
		},
		{
			name:       "nội dung bình thường thì bảo vệ",
			verdict:    AIVerdict{Checked: true, Category: "content", Confidence: 0.9},
			wantSignal: KindAIClean,
		},
		{
			// cdn là hạ tầng trung tính: nó vẫn có thể đang phục vụ mã theo dõi,
			// nên không được hạ điểm và che mất tín hiệu khác.
			name:    "cdn thì trung tính",
			verdict: AIVerdict{Checked: true, Category: "cdn", Confidence: 0.95},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Score(domain, Facts{AI: tc.verdict}, DefaultWeights)

			if tc.wantSignal == "" {
				if got.HasSignal(KindAIAdtech) || got.HasSignal(KindAIClean) {
					t.Errorf("có tín hiệu AI %v, muốn không có", got.SignalKinds())
				}
				return
			}
			if !got.HasSignal(tc.wantSignal) {
				t.Errorf("tín hiệu = %v, muốn có %s", got.SignalKinds(), tc.wantSignal)
			}
		})
	}
}
