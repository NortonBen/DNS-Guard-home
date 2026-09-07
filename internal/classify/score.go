package classify

// Score chấm điểm một domain và trả về điểm, bằng chứng, nhãn phân loại, độ tin cậy.
//
// Hàm thuần túy: cùng đầu vào luôn cho cùng đầu ra. Đó là điều làm cho bảng xem
// trước tác động khi đổi trọng số (FR-3.6) chính xác tuyệt đối chứ không phải ước
// lượng, và cho phép chạy lại toàn bộ CSDL mà không tra lại dịch vụ ngoài.
func Score(d Domain, f Facts, w Weights) Result {
	if w == nil {
		w = DefaultWeights
	}

	infra := infraSignals(d, f, w)

	signals := make([]Signal, 0, 8)
	signals = append(signals, infra...)
	signals = append(signals, lexicalSignals(d, w)...)
	signals = append(signals, behaviorSignals(d, w)...)
	signals = append(signals, structureSignals(d, f, w)...)
	signals = append(signals, negativeSignals(d, f, w, infra)...)

	// Không chuẩn hóa điểm về [0,1]. Thang điểm thô dễ suy luận hơn: người vận hành
	// nhìn 7,5 và thấy ngay đó là 6,0 + 1,5. Một giá trị 0,83 không nói lên điều gì.
	var score float64
	for _, s := range signals {
		score += s.Weight
	}

	return Result{
		Score:      round2(score),
		Signals:    signals,
		Category:   categorize(d, f, signals),
		Confidence: confidence(signals),
	}
}

// confidence trả lời câu hỏi khác với điểm số. Điểm nói "khả năng là quảng cáo cao
// đến đâu"; độ tin cậy nói "bằng chứng chắc đến đâu". Một domain đạt 6,0 từ hai tín
// hiệu hạ tầng đáng tin hơn nhiều so với một domain đạt 6,0 từ bốn tín hiệu hành vi
// yếu. Giao diện Triage sắp xếp theo độ tin cậy vì bằng chứng chắc là thứ duyệt
// nhanh nhất.
func confidence(signals []Signal) float64 {
	infra, total := 0, len(signals)
	for _, s := range signals {
		if infraKinds[s.Kind] {
			infra++
		}
	}
	switch {
	case infra >= 2:
		return 0.95
	case infra == 1 && total >= 3:
		return 0.85
	case infra == 1:
		return 0.70
	case total >= 4:
		return 0.60
	default:
		return 0.40
	}
}

// categorize gán nhãn phân loại. Chạy theo thứ tự, dừng ở quy tắc khớp đầu tiên.
// Thứ tự quan trọng: quy tắc bảo vệ chạy trước, quy tắc chặn chạy sau.
func categorize(d Domain, f Facts, signals []Signal) Category {
	has := func(kind string) bool {
		for _, s := range signals {
			if s.Kind == kind {
				return true
			}
		}
		return false
	}

	// 1. Hạ tầng dùng chung hoặc thứ hạng cao — ghi lại kết luận "đã xem xét, không
	//    được chặn cái này". Có giá trị hơn nhiều so với để nó không phân loại rồi
	//    bị xem xét lại mỗi tuần.
	if has(KindSharedCDN) || has(KindHighRank) {
		return CategoryCDN
	}

	// 2-4. Nguồn ngoài đã phân loại sẵn thì tin theo nguồn.
	for _, want := range []Category{CategoryMalware, CategoryCryptomining, CategoryAdult} {
		for _, got := range f.PublicListCategories {
			if got == want {
				return want
			}
		}
	}

	// 5. beacon là tín hiệu mạnh nhất — truy vấn đều đặn như máy là đặc trưng của
	//    telemetry chứ không phải quảng cáo.
	if strongest(signals) == KindBeacon {
		return CategoryTelemetry
	}

	// 6-7. Đích CNAME hoặc ASN đã biết mang phân loại của chính nó: một domain trỏ
	//      về eulerian.net là tracking, trỏ về doubleclick.net là ads. Nhãn đi theo
	//      đích thật chứ không theo tên gọi bề ngoài.
	if cat, ok := adtechCategory(f); ok {
		return cat
	}

	// Từ khóa một mình không đủ để gán nhãn chặn: "analytics.congty.vn" chạy trên
	// hạ tầng của một ISP trong nước nhiều khả năng là trang phân tích nội bộ, không
	// phải hạ tầng theo dõi. Cần thêm bằng chứng hạ tầng hoặc hành vi đi kèm.
	if cat, _, ok := matchKeyword(d.Name); ok && hasCorroboration(signals) {
		return cat
	}

	if hasAnyInfra(signals) {
		return CategoryAds
	}

	// 8. Mặc định.
	return CategoryContent
}

// adtechCategory lấy phân loại từ đích CNAME hoặc từ ASN đã biết.
func adtechCategory(f Facts) (Category, bool) {
	for _, hop := range f.CNAMEChain {
		if cat, _, ok := hasAdtechSuffix(hop); ok {
			return cat, true
		}
	}
	if info, ok := adtechASNs[f.ASN]; ok {
		if _, neutral := neutralASNs[f.ASN]; !neutral {
			return info.Category, true
		}
	}
	return "", false
}

// hasCorroboration cho biết có bằng chứng nào ngoài từ vựng không.
func hasCorroboration(signals []Signal) bool {
	for _, s := range signals {
		switch s.Kind {
		case KindKeyword, KindRandomSub:
			continue
		default:
			if s.Weight > 0 {
				return true
			}
		}
	}
	return false
}

func hasAnyInfra(signals []Signal) bool {
	for _, s := range signals {
		if infraKinds[s.Kind] {
			return true
		}
	}
	return false
}

// strongest trả về loại tín hiệu dương có trọng số lớn nhất, rỗng nếu không có.
func strongest(signals []Signal) string {
	best, bestW := "", 0.0
	for _, s := range signals {
		if s.Weight > bestW {
			best, bestW = s.Kind, s.Weight
		}
	}
	return best
}
