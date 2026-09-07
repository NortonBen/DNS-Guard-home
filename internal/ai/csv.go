package ai

import (
	"encoding/csv"
	"strconv"
	"strings"

	"github.com/benji/dnsguard/internal/classify"
)

// Phản hồi phân loại là CSV chứ không phải JSON.
//
// Với một lô bốn mươi domain, JSON lặp lại bốn tên khoá ở mỗi dòng — khoảng bốn
// mươi token thừa mỗi dòng, một nghìn sáu trăm token cho cả lô, chỉ để nói lại
// thứ mà lược đồ đã nói một lần. CSV bỏ hẳn phần đó. Đổi lại phải chấp nhận rằng
// model đôi khi trả về sai khuôn, nên bộ đọc dưới đây khoan dung có chủ đích.
const (
	// csvColumns là số cột tối thiểu một dòng phải có để dùng được.
	csvColumns = 3
	// maxReasonLen cắt phần lý do. Model đôi khi viết cả đoạn văn; giao diện chỉ
	// hiển thị một dòng, và phần thừa đi thẳng vào CSDL mà không ai đọc.
	maxReasonLen = 200
)

// Parsed là kết quả đọc một phản hồi CSV.
type Parsed struct {
	Verdicts []Verdict
	// Skipped là số dòng bị bỏ: sai khuôn, nhãn lạ, hoặc domain không có trong lô.
	Skipped int
	// Unknown là các domain model tự bịa ra. Giữ lại để ghi log — một model hay bịa
	// domain là tín hiệu nên đổi model, và không có danh sách này thì không ai biết.
	Unknown []string
}

// parseCSV đọc phản hồi của model thành danh sách kết luận.
//
// allowed là tập domain đã hỏi. Dòng nào nói về domain ngoài tập đó bị bỏ: model
// bịa ra một tên miền không được phép trở thành bằng chứng chấm điểm.
func parseCSV(body string, allowed []string) Parsed {
	var out Parsed

	index := make(map[string]string, len(allowed))
	for _, d := range allowed {
		index[strings.ToLower(strings.TrimSpace(d))] = d
	}

	reader := csv.NewReader(strings.NewReader(stripFences(body)))
	// Số cột thay đổi được: lý do có dấu phẩy mà model quên bọc nháy là chuyện
	// thường, và một dòng thừa cột vẫn dùng được ba cột đầu.
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	records, err := reader.ReadAll()
	if err != nil && len(records) == 0 {
		// Không đọc nổi dòng nào bằng bộ đọc CSV thì thử lại thô bằng cách tách
		// dấu phẩy: một dấu nháy lạc giữa chừng không nên làm hỏng cả lô.
		records = splitRough(stripFences(body))
	}

	seen := make(map[string]bool, len(records))
	for _, rec := range records {
		if len(rec) < csvColumns {
			out.Skipped++
			continue
		}

		name := strings.ToLower(strings.Trim(strings.TrimSpace(rec[0]), `"'`))
		if name == "" || name == "domain" { // dòng tiêu đề model tự thêm vào
			continue
		}

		original, known := index[name]
		if !known {
			out.Skipped++
			if len(out.Unknown) < 20 {
				out.Unknown = append(out.Unknown, name)
			}
			continue
		}
		// Model đôi khi lặp lại cùng một domain hai lần với hai nhãn khác nhau.
		// Giữ dòng đầu: dòng sau không đáng tin hơn, và chọn ngẫu nhiên thì kết
		// quả không lặp lại được.
		if seen[name] {
			out.Skipped++
			continue
		}

		category := normalizeCategory(rec[1])
		if category == "" {
			out.Skipped++
			continue
		}

		reason := ""
		if len(rec) > 3 {
			// Model quên bọc nháy thì lý do bị tách thành nhiều cột — ghép lại.
			reason = strings.Join(rec[3:], ", ")
		}

		seen[name] = true
		out.Verdicts = append(out.Verdicts, Verdict{
			Domain:     original,
			Category:   category,
			Confidence: parseConfidence(rec[2]),
			Reason:     clip(strings.Trim(reason, `"'`), maxReasonLen),
		})
	}
	return out
}

// stripFences bóc rào code mà model hay bọc quanh phản hồi dù đã dặn đừng làm.
//
// Có rào thì CHỈ giữ phần bên trong. Model bọc rào gần như luôn kèm một câu dẫn
// trước và một câu kết sau — giữ chúng lại sẽ thành hai dòng rác mà bộ đọc đếm
// vào skipped, làm số liệu "model trả sai khuôn bao nhiêu dòng" mất ý nghĩa.
func stripFences(body string) string {
	body = strings.TrimSpace(body)
	if !strings.Contains(body, "```") {
		return body
	}

	var kept []string
	inside := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inside = !inside
			continue
		}
		if inside {
			kept = append(kept, line)
		}
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// splitRough là đường lùi khi bộ đọc CSV chuẩn bó tay.
func splitRough(body string) [][]string {
	var out [][]string
	for _, line := range strings.Split(body, "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		out = append(out, strings.SplitN(line, ",", 4))
	}
	return out
}

// normalizeCategory đưa nhãn model trả về đúng một trong tám phân loại đã biết.
// Trả chuỗi rỗng nếu không khớp — nhãn lạ là dòng bỏ đi, không phải nhãn mới.
func normalizeCategory(raw string) string {
	v := strings.ToLower(strings.Trim(strings.TrimSpace(raw), `"'`))
	v = strings.ReplaceAll(v, " ", "")
	v = strings.ReplaceAll(v, "-", "")
	v = strings.ReplaceAll(v, "_", "")

	// Vài cách gọi khác mà model hay dùng cho cùng một thứ.
	switch v {
	case "advertising", "advertisement", "ad", "adtech":
		v = "ads"
	case "tracker", "analytics", "trackers":
		v = "tracking"
	case "telemetrics", "metrics":
		v = "telemetry"
	case "phishing", "malicious":
		v = "malware"
	case "mining", "cryptomining", "crypto", "coinmining":
		v = "cryptomining"
	case "porn", "nsfw", "adultcontent":
		v = "adult"
	case "benign", "legitimate", "normal", "clean", "safe":
		v = "content"
	}

	for _, c := range classify.AllCategories {
		if v == string(c) {
			return v
		}
	}
	return ""
}

// parseConfidence đọc độ tin cậy và kẹp vào [0,1].
//
// Model trả "85%", "0.85" hay "high" tuỳ hôm. Hai dạng đầu đọc được; dạng chữ quy
// về ba mức thay vì bỏ cả dòng — nhãn vẫn dùng được kể cả khi con số thì không.
func parseConfidence(raw string) float64 {
	v := strings.ToLower(strings.Trim(strings.TrimSpace(raw), `"'`))

	if pct, found := strings.CutSuffix(v, "%"); found {
		if f, err := strconv.ParseFloat(strings.TrimSpace(pct), 64); err == nil {
			return clamp01(f / 100)
		}
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		// Model đôi khi trả 85 thay vì 0,85 dù không kèm dấu phần trăm.
		if f > 1 {
			f /= 100
		}
		return clamp01(f)
	}

	switch v {
	case "high", "cao":
		return 0.9
	case "medium", "mid", "trungbinh", "trung bình":
		return 0.6
	case "low", "thap", "thấp":
		return 0.3
	}
	return 0
}

func clamp01(f float64) float64 {
	switch {
	case f < 0:
		return 0
	case f > 1:
		return 1
	default:
		return f
	}
}
