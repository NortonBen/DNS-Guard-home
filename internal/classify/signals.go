package classify

import (
	"math"
	"strings"
)

// Bốn nhóm tín hiệu dương và một nhóm âm, theo docs/06-classification.md §2.
// Mỗi hàm là một hàm thuần túy trên (Domain, Facts) trả về các tín hiệu đã kích
// hoạt kèm chi tiết bằng chứng.

// infraSignals — nhóm mạnh nhất. Hạ tầng không nói dối: tên miền có thể trông vô
// hại, nhưng CNAME phải trỏ về đâu đó thật.
func infraSignals(d Domain, f Facts, w Weights, r Rules) []Signal {
	var out []Signal

	// CNAME cloaking là kỹ thuật né blocklist phổ biến nhất hiện nay. Nhà quảng cáo
	// cho khách hàng trỏ một subdomain trông như của chính họ, nhưng bản ghi CNAME
	// dẫn về hạ tầng adtech. Chỉ phân giải động mới thấy đích thật.
	for _, hop := range f.CNAMEChain {
		if _, suffix, ok := r.adtechSuffix(hop); ok {
			out = append(out, Signal{
				Kind:   KindCNAMEAdtech,
				Weight: w.Get(KindCNAMEAdtech),
				Detail: map[string]any{"cname": hop, "matched": suffix},
			})
			break // một lần cho toàn chuỗi: cùng một bằng chứng
		}
	}

	if f.CNAMEETLD1InPublicList != "" {
		out = append(out, Signal{
			Kind:   KindCNAMEBlocked,
			Weight: w.Get(KindCNAMEBlocked),
			Detail: map[string]any{"cname_etld1": f.CNAMEETLD1InPublicList},
		})
	}

	if info, ok := r.asnInfo(f.ASN); ok {
		out = append(out, Signal{
			Kind:   KindASNAdtech,
			Weight: w.Get(KindASNAdtech),
			Detail: map[string]any{"asn": f.ASN, "org": info.Org},
		})
	}

	for _, san := range f.CertSANs {
		san = strings.TrimPrefix(san, "*.")
		if _, suffix, ok := r.adtechSuffix(san); ok {
			out = append(out, Signal{
				Kind:   KindCertAdtech,
				Weight: w.Get(KindCertAdtech),
				Detail: map[string]any{"san": san, "matched": suffix},
			})
			break
		}
	}

	return out
}

// lexicalSignals — bằng chứng từ chính tên miền.
func lexicalSignals(d Domain, w Weights, r Rules) []Signal {
	var out []Signal

	// Chỉ tính một lần dù khớp nhiều từ khóa. Nếu không, một domain như
	// "ads-tracking-analytics.com" được cộng ba lần cho cùng một bằng chứng.
	if cat, word, ok := r.matchKeyword(d.Name); ok {
		out = append(out, Signal{
			Kind:   KindKeyword,
			Weight: w.Get(KindKeyword),
			Detail: map[string]any{"keyword": word, "group": string(cat)},
		})
	}

	// Nhãn trái nhất dài và ngẫu nhiên là dấu hiệu subdomain sinh tự động theo
	// chiến dịch hoặc theo phiên. Cần đồng thời cả độ dài lẫn entropy: "cdn" có
	// entropy thấp, còn nhãn ngắn như "x7q" tuy entropy cao nhưng không đủ bằng chứng.
	label := leftmostLabel(d.Name)
	if len(label) >= randomLabelMinLen {
		if e := shannonEntropy(label); e >= randomLabelMinEntropy {
			out = append(out, Signal{
				Kind:   KindRandomSub,
				Weight: w.Get(KindRandomSub),
				Detail: map[string]any{"label": label, "entropy": round2(e)},
			})
		}
	}

	return out
}

// behaviorSignals — bằng chứng từ cách domain xuất hiện trong log.
//
// Trực giác: người dùng chủ động gõ tên miền nội dung vào trình duyệt, không ai gõ
// tên một máy chủ quảng cáo. Nên domain quảng cáo luôn xuất hiện *sau* một domain
// khác, và xuất hiện trên nhiều máy đang xem những thứ chẳng liên quan gì tới nhau.
//
// Nhóm này chỉ có nghĩa khi query log giữ được IP của từng client. Kiến trúc mirror
// gói tin giữ được IP thật, nên các tín hiệu này dùng được.
func behaviorSignals(d Domain, w Weights, r Rules) []Signal {
	var out []Signal

	switch {
	case d.ThirdPartyRatio > thirdPartyStrong:
		out = append(out, Signal{
			Kind:   KindThirdParty,
			Weight: w.Get(KindThirdParty),
			Detail: map[string]any{"ratio": round2(d.ThirdPartyRatio)},
		})
	case d.ThirdPartyRatio >= thirdPartyWeak:
		out = append(out, Signal{
			Kind:   KindThirdPartyWeak,
			Weight: w.Get(KindThirdPartyWeak),
			Detail: map[string]any{"ratio": round2(d.ThirdPartyRatio)},
		})
	}

	if d.ClientCount >= r.Thresholds.FanOutMinClients && d.ThirdPartyRatio > fanOutMinRatio {
		out = append(out, Signal{
			Kind:   KindFanOut,
			Weight: w.Get(KindFanOut),
			Detail: map[string]any{"clients": d.ClientCount, "ratio": round2(d.ThirdPartyRatio)},
		})
	}

	// Con người tạo ra khoảng cách truy vấn không đều; phần mềm báo cáo định kỳ thì
	// đều một cách máy móc. Cần đủ số mẫu để hệ số biến thiên có nghĩa.
	if d.QueryCount >= int64(r.Thresholds.BeaconMinQueries) && d.QueryIntervalCV > 0 && d.QueryIntervalCV < beaconMaxCV {
		out = append(out, Signal{
			Kind:   KindBeacon,
			Weight: w.Get(KindBeacon),
			Detail: map[string]any{"cv": round2(d.QueryIntervalCV), "queries": d.QueryCount},
		})
	}

	return out
}

// structureSignals — bằng chứng từ hình dạng của cụm domain.
//
// Máy chủ quảng cáo sinh subdomain theo chiến dịch, theo khách hàng, theo phiên —
// hàng trăm cái dưới một gốc. Trang nội dung hiếm khi vượt vài chục. Nhóm này không
// cần IP client nên hoạt động ở mọi kiến trúc thu thập.
func structureSignals(d Domain, f Facts, w Weights, r Rules) []Signal {
	var out []Signal

	switch {
	case d.SubdomainCount >= r.Thresholds.SpreadHighMin:
		out = append(out, Signal{
			Kind:   KindSpreadHigh,
			Weight: w.Get(KindSpreadHigh),
			Detail: map[string]any{"subdomains": d.SubdomainCount, "etld1": d.ETLD1},
		})
	case d.SubdomainCount >= r.Thresholds.SpreadMidMin:
		out = append(out, Signal{
			Kind:   KindSpreadMid,
			Weight: w.Get(KindSpreadMid),
			Detail: map[string]any{"subdomains": d.SubdomainCount, "etld1": d.ETLD1},
		})
	}

	if f.ETLD1InPublicList {
		out = append(out, Signal{
			Kind:   KindETLD1Blocked,
			Weight: w.Get(KindETLD1Blocked),
			Detail: map[string]any{"etld1": d.ETLD1},
		})
	}

	return out
}

// negativeSignals — trọng số âm, tồn tại để kéo domain vô hại ra khỏi vùng nguy hiểm.
func negativeSignals(d Domain, f Facts, w Weights, r Rules, infra []Signal) []Signal {
	var out []Signal

	// Trọng số này lớn hơn mọi tín hiệu dương đơn lẻ, và đó là chủ ý: nếu một domain
	// trong top Tranco bị chấm điểm cao, khả năng lớn nhất là quy tắc sai chứ không
	// phải domain đó là quảng cáo. Vẫn có ngoại lệ — doubleclick.net xếp hạng rất
	// cao — nên nó là điểm âm chứ không phải chặn tuyệt đối, và các tín hiệu hạ tầng
	// cộng dồn vẫn có thể vượt qua.
	if f.TrancoRank > 0 && f.TrancoRank <= r.Thresholds.HighRankMax {
		out = append(out, Signal{
			Kind:   KindHighRank,
			Weight: w.Get(KindHighRank),
			Detail: map[string]any{"tranco": f.TrancoRank, "etld1": d.ETLD1},
		})
	}

	if f.AgeDays > longLivedMinDays && len(infra) == 0 {
		out = append(out, Signal{
			Kind:   KindLongLivedContent,
			Weight: w.Get(KindLongLivedContent),
			Detail: map[string]any{"age_days": f.AgeDays},
		})
	}

	for _, hop := range append([]string{d.Name}, f.CNAMEChain...) {
		if suffix, ok := r.sharedCDN(hop); ok {
			out = append(out, Signal{
				Kind:   KindSharedCDN,
				Weight: w.Get(KindSharedCDN),
				Detail: map[string]any{"cdn": suffix, "via": hop},
			})
			break
		}
	}

	return out
}

// matchKeyword tìm từ khóa đầu tiên khớp, ưu tiên nhóm theo thứ tự xác định để kết
// quả không phụ thuộc thứ tự duyệt map.
func (r Rules) matchKeyword(name string) (Category, string, bool) {
	name = strings.ToLower(name)
	for _, cat := range []Category{CategoryAds, CategoryTracking, CategoryTelemetry} {
		for _, kw := range r.Keywords[cat] {
			if strings.Contains(name, kw) {
				return cat, kw, true
			}
		}
	}
	return "", "", false
}

func leftmostLabel(name string) string {
	if i := strings.IndexByte(name, '.'); i >= 0 {
		return name[:i]
	}
	return name
}

// shannonEntropy tính entropy Shannon theo bit trên mỗi ký tự.
func shannonEntropy(s string) float64 {
	if s == "" {
		return 0
	}
	freq := make(map[rune]int, len(s))
	n := 0
	for _, r := range s {
		freq[r]++
		n++
	}
	var e float64
	for _, c := range freq {
		p := float64(c) / float64(n)
		e -= p * math.Log2(p)
	}
	return e
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }

// httpSignals — bằng chứng từ header phản hồi và HTML tĩnh của trang gốc.
//
// Nguyên tắc phân biệt quan trọng nhất của cả nhóm: các tín hiệu ở đây phải nói về
// *danh tính của domain*, không phải về *cách trang kiếm tiền*. Một tờ báo nhúng đầy
// mã quảng cáo vẫn là nội dung. Vì thế nhóm này cố ý không đếm số script bên thứ ba
// và không dò dấu vân tay gtag/fbq/adsbygoogle — chúng dính vào gần như mọi trang có
// quảng cáo, và dùng chúng là cách nhanh nhất phá vỡ mục tiêu precision.
func httpSignals(f Facts, w Weights, r Rules) []Signal {
	h := f.HTTP
	if !h.Fetched {
		return nil
	}

	var out []Signal

	// Một hostname mà trình duyệt đã phân giải nhưng không trả về trang nào — chỉ một
	// pixel, một 204, hoặc thân rỗng không phải HTML — thì không phải nơi người ta
	// ghé thăm. Nó là điểm thu thập.
	switch {
	case h.Status == 204:
		out = append(out, Signal{
			Kind: KindHTTPBeacon, Weight: w.Get(KindHTTPBeacon),
			Detail: map[string]any{"status": 204, "reason": "204 No Content"},
		})
	case h.IsPixel:
		out = append(out, Signal{
			Kind: KindHTTPBeacon, Weight: w.Get(KindHTTPBeacon),
			Detail: map[string]any{"content_type": h.ContentType, "length": h.BodyLen,
				"reason": "ảnh một điểm ảnh"},
		})
	case h.Status == 200 && !h.IsHTML && h.BodyLen == 0:
		out = append(out, Signal{
			Kind: KindHTTPBeacon, Weight: w.Get(KindHTTPBeacon),
			Detail: map[string]any{"content_type": h.ContentType, "reason": "200 thân rỗng"},
		})
	}

	// Chuyển hướng sang hạ tầng adtech là phiên bản HTTP của CNAME cloaking: tên miền
	// trông vô hại nhưng đích thật nằm ở nơi khác.
	if h.RedirectTo != "" {
		if host := hostOf(h.RedirectTo); host != "" {
			if _, matched, ok := r.adtechSuffix(host); ok {
				out = append(out, Signal{
					Kind: KindHTTPRedirectAdtech, Weight: w.Get(KindHTTPRedirectAdtech),
					Detail: map[string]any{"location": h.RedirectTo, "matched": matched},
				})
			}
		}
	}

	if h.Parking != "" {
		out = append(out, Signal{
			Kind: KindHTTPParking, Weight: w.Get(KindHTTPParking),
			Detail: map[string]any{"provider": h.Parking},
		})
	}

	// P3P là chuẩn đã chết, ngày nay gần như chỉ còn các mạng quảng cáo giữ lại để
	// lách chính sách cookie của trình duyệt cũ.
	if h.P3P != "" {
		out = append(out, Signal{
			Kind: KindHTTPP3P, Weight: w.Get(KindHTTPP3P),
			Detail: map[string]any{"p3p": truncate(h.P3P, 120)},
		})
	}

	// SameSite=None nghĩa là cookie cố ý gửi kèm trong ngữ cảnh bên thứ ba; cộng thời
	// hạn dài thì đó là định danh theo dõi chứ không phải cookie phiên.
	if h.TrackingCookie != "" && h.CookieMaxDays >= trackingCookieMinDays {
		out = append(out, Signal{
			Kind: KindHTTPTrackingCookie, Weight: w.Get(KindHTTPTrackingCookie),
			Detail: map[string]any{"cookie": h.TrackingCookie, "days": h.CookieMaxDays},
		})
	}

	// Mở CORS cho mọi nguồn thì bình thường với CDN và API công khai; chỉ đáng ngờ
	// khi đi kèm việc không phục vụ trang nào.
	if h.CORS == "*" && !h.IsHTML {
		out = append(out, Signal{
			Kind: KindHTTPCORSWildcard, Weight: w.Get(KindHTTPCORSWildcard),
			Detail: map[string]any{"content_type": h.ContentType},
		})
	}

	if h.IsHTML && h.Status == 200 {
		switch {
		case h.TextLen < emptyPageMaxText && h.Title == "":
			out = append(out, Signal{
				Kind: KindHTTPEmptyPage, Weight: w.Get(KindHTTPEmptyPage),
				Detail: map[string]any{"text_len": h.TextLen},
			})
		case h.TextLen >= realSiteMinText && h.Title != "":
			// Tín hiệu âm: một trang có tiêu đề và có nội dung để đọc gần như luôn là
			// nội dung thật. Giữ ở mức nhẹ vì trang giới thiệu của chính công ty
			// adtech cũng là trang thật — ở đó tín hiệu hạ tầng phải thắng.
			out = append(out, Signal{
				Kind: KindHTTPRealSite, Weight: w.Get(KindHTTPRealSite),
				Detail: map[string]any{"title": truncate(h.Title, 80), "text_len": h.TextLen},
			})
		}
	}

	return out
}

// virusTotalSignals — kết luận tổng hợp của nhiều engine diệt mã độc.
func virusTotalSignals(f Facts, w Weights, r Rules) []Signal {
	vt := f.VT
	if !vt.Checked || !vt.Known {
		return nil
	}

	// Ngưỡng ba engine chứ không phải một: một engine đơn lẻ báo động là nhiễu nổi
	// tiếng của VirusTotal. Ba engine độc lập đồng ý mới là bằng chứng.
	if vt.Malicious >= r.Thresholds.VTMaliciousMinEngines {
		return []Signal{{
			Kind: KindVTMalicious, Weight: w.Get(KindVTMalicious),
			Detail: map[string]any{"malicious": vt.Malicious, "suspicious": vt.Suspicious},
		}}
	}

	if vt.Malicious == 0 && vt.Harmless+vt.Undetected >= vtCleanMinEngines {
		return []Signal{{
			Kind: KindVTClean, Weight: w.Get(KindVTClean),
			Detail: map[string]any{"harmless": vt.Harmless, "engines": vt.Harmless + vt.Undetected},
		}}
	}

	return nil
}

// aiConfidenceFloor là mức tin cậy tối thiểu để kết luận của model được tính.
//
// Model được dặn "không chắc thì trả content với confidence thấp", nên một kết
// luận dưới mức này chính là model đang nói nó không biết. Đếm nó vào điểm là
// biến lời thú nhận thiếu chắc chắn thành bằng chứng.
const aiConfidenceFloor = 0.6

// aiAdtechCategories là các nhãn model trả về được coi là buộc tội.
//
// cdn và content không nằm đây: cdn là hạ tầng trung tính, còn content là nhãn
// mặc định khi model không kết luận được — cả hai không phải bằng chứng buộc tội.
var aiAdtechCategories = map[string]bool{
	string(CategoryAds):          true,
	string(CategoryTracking):     true,
	string(CategoryTelemetry):    true,
	string(CategoryMalware):      true,
	string(CategoryCryptomining): true,
	string(CategoryAdult):        true,
}

// aiSignals — kết luận của model ngôn ngữ.
//
// Không nhận Rules: khác với mọi nhóm khác, nhóm này không có ngưỡng nào để người
// vận hành chỉnh. Thứ chỉnh được là trọng số, và đó là đúng chỗ — người dùng quyết
// định "tin AI đến đâu", không quyết định "AI được coi là đã kết luận khi nào".
func aiSignals(f Facts, w Weights) []Signal {
	ai := f.AI
	if !ai.Checked || ai.Category == "" || ai.Confidence < aiConfidenceFloor {
		return nil
	}

	detail := map[string]any{
		"category": ai.Category, "confidence": ai.Confidence,
	}
	if ai.Model != "" {
		detail["model"] = ai.Model
	}
	if ai.Reason != "" {
		detail["reason"] = truncate(ai.Reason, 160)
	}

	if aiAdtechCategories[ai.Category] {
		return []Signal{{
			Kind: KindAIAdtech, Weight: w.Get(KindAIAdtech), Detail: detail,
		}}
	}
	// Chỉ content mới kéo điểm xuống. cdn để trung tính: một domain CDN vẫn có thể
	// đang phục vụ hạ tầng theo dõi, và hạ điểm nó sẽ che mất tín hiệu khác.
	if ai.Category == string(CategoryContent) {
		return []Signal{{
			Kind: KindAIClean, Weight: w.Get(KindAIClean), Detail: detail,
		}}
	}
	return nil
}

// hostOf lấy phần host của một URL mà không cần phân tích đầy đủ.
func hostOf(rawURL string) string {
	s := rawURL
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndexByte(s, '@'); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.IndexByte(s, ':'); i >= 0 {
		s = s[:i]
	}
	return strings.ToLower(strings.TrimSuffix(s, "."))
}

// truncate cắt chuỗi dài để bằng chứng lưu trong nhật ký không phình ra.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
