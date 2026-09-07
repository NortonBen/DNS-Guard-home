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
func infraSignals(d Domain, f Facts, w Weights) []Signal {
	var out []Signal

	// CNAME cloaking là kỹ thuật né blocklist phổ biến nhất hiện nay. Nhà quảng cáo
	// cho khách hàng trỏ một subdomain trông như của chính họ, nhưng bản ghi CNAME
	// dẫn về hạ tầng adtech. Chỉ phân giải động mới thấy đích thật.
	for _, hop := range f.CNAMEChain {
		if _, suffix, ok := hasAdtechSuffix(hop); ok {
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

	if f.ASN != 0 {
		if _, neutral := neutralASNs[f.ASN]; !neutral {
			if info, ok := adtechASNs[f.ASN]; ok {
				out = append(out, Signal{
					Kind:   KindASNAdtech,
					Weight: w.Get(KindASNAdtech),
					Detail: map[string]any{"asn": f.ASN, "org": info.Org},
				})
			}
		}
	}

	for _, san := range f.CertSANs {
		san = strings.TrimPrefix(san, "*.")
		if _, suffix, ok := hasAdtechSuffix(san); ok {
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
func lexicalSignals(d Domain, w Weights) []Signal {
	var out []Signal

	// Chỉ tính một lần dù khớp nhiều từ khóa. Nếu không, một domain như
	// "ads-tracking-analytics.com" được cộng ba lần cho cùng một bằng chứng.
	if cat, word, ok := matchKeyword(d.Name); ok {
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
func behaviorSignals(d Domain, w Weights) []Signal {
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

	if d.ClientCount >= fanOutMinClients && d.ThirdPartyRatio > fanOutMinRatio {
		out = append(out, Signal{
			Kind:   KindFanOut,
			Weight: w.Get(KindFanOut),
			Detail: map[string]any{"clients": d.ClientCount, "ratio": round2(d.ThirdPartyRatio)},
		})
	}

	// Con người tạo ra khoảng cách truy vấn không đều; phần mềm báo cáo định kỳ thì
	// đều một cách máy móc. Cần đủ số mẫu để hệ số biến thiên có nghĩa.
	if d.QueryCount >= beaconMinQueries && d.QueryIntervalCV > 0 && d.QueryIntervalCV < beaconMaxCV {
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
func structureSignals(d Domain, f Facts, w Weights) []Signal {
	var out []Signal

	switch {
	case d.SubdomainCount >= spreadHighMin:
		out = append(out, Signal{
			Kind:   KindSpreadHigh,
			Weight: w.Get(KindSpreadHigh),
			Detail: map[string]any{"subdomains": d.SubdomainCount, "etld1": d.ETLD1},
		})
	case d.SubdomainCount >= spreadMidMin:
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
func negativeSignals(d Domain, f Facts, w Weights, infra []Signal) []Signal {
	var out []Signal

	// Trọng số này lớn hơn mọi tín hiệu dương đơn lẻ, và đó là chủ ý: nếu một domain
	// trong top Tranco bị chấm điểm cao, khả năng lớn nhất là quy tắc sai chứ không
	// phải domain đó là quảng cáo. Vẫn có ngoại lệ — doubleclick.net xếp hạng rất
	// cao — nên nó là điểm âm chứ không phải chặn tuyệt đối, và các tín hiệu hạ tầng
	// cộng dồn vẫn có thể vượt qua.
	if f.TrancoRank > 0 && f.TrancoRank <= highRankMax {
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
		if suffix, ok := isSharedCDN(hop); ok {
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
func matchKeyword(name string) (Category, string, bool) {
	name = strings.ToLower(name)
	for _, cat := range []Category{CategoryAds, CategoryTracking, CategoryTelemetry} {
		for _, kw := range keywordGroups[cat] {
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
