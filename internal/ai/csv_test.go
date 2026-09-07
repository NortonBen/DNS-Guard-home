package ai

import (
	"strings"
	"testing"
)

// Bộ đọc CSV là nơi duy nhất tiếp xúc với văn bản model tự do sinh ra, nên nó
// phải chịu được mọi kiểu sai khuôn thường gặp mà vẫn từ chối đúng thứ nguy hiểm:
// domain không nằm trong lô.
func TestParseCSV(t *testing.T) {
	allowed := []string{"doubleclick.net", "example.com", "cdn.jsdelivr.net"}

	cases := []struct {
		name         string
		body         string
		wantVerdicts map[string]string // domain → category
		wantSkipped  int
		wantUnknown  []string
	}{
		{
			name: "khuôn chuẩn",
			body: "doubleclick.net,ads,0.97,sàn quảng cáo\n" +
				"example.com,content,0.8,trang bình thường\n",
			wantVerdicts: map[string]string{
				"doubleclick.net": "ads", "example.com": "content",
			},
		},
		{
			name: "bọc trong rào code kèm lời dẫn",
			body: "Đây là kết quả phân loại:\n```csv\n" +
				"doubleclick.net,ads,0.9,quảng cáo\n" +
				"```\nHy vọng giúp được bạn.",
			wantVerdicts: map[string]string{"doubleclick.net": "ads"},
		},
		{
			name: "có dòng tiêu đề model tự thêm",
			body: "domain,category,confidence,reason\n" +
				"example.com,content,0.7,bình thường\n",
			wantVerdicts: map[string]string{"example.com": "content"},
		},
		{
			// Domain model bịa ra không bao giờ được thành bằng chứng: nó sẽ đi
			// thẳng vào domain_facts của một domain chưa ai hỏi.
			name:         "domain ngoài lô bị loại",
			body:         "khong-co-trong-lo.com,ads,0.99,bịa\nexample.com,content,0.8,thật\n",
			wantVerdicts: map[string]string{"example.com": "content"},
			wantSkipped:  1,
			wantUnknown:  []string{"khong-co-trong-lo.com"},
		},
		{
			name:         "nhãn lạ bị loại",
			body:         "example.com,spam,0.9,nhãn không có thật\n",
			wantVerdicts: map[string]string{},
			wantSkipped:  1,
		},
		{
			name: "tên gọi khác của cùng một nhãn được quy về",
			body: "doubleclick.net,advertising,0.9,x\n" +
				"example.com,legitimate,0.9,y\n" +
				"cdn.jsdelivr.net,CDN,0.9,z\n",
			wantVerdicts: map[string]string{
				"doubleclick.net": "ads", "example.com": "content", "cdn.jsdelivr.net": "cdn",
			},
		},
		{
			name:         "thiếu cột thì bỏ dòng",
			body:         "example.com,content\ndoubleclick.net,ads,0.9,ok\n",
			wantVerdicts: map[string]string{"doubleclick.net": "ads"},
			wantSkipped:  1,
		},
		{
			// Model trả hai nhãn khác nhau cho cùng một domain: giữ dòng đầu, vì
			// chọn ngẫu nhiên thì kết quả không lặp lại được.
			name:         "domain lặp thì giữ dòng đầu",
			body:         "example.com,content,0.8,lần một\nexample.com,ads,0.9,lần hai\n",
			wantVerdicts: map[string]string{"example.com": "content"},
			wantSkipped:  1,
		},
		{
			name:         "chữ hoa và khoảng trắng thừa",
			body:         "  DoubleClick.NET , ADS , 0.9 , quảng cáo \n",
			wantVerdicts: map[string]string{"doubleclick.net": "ads"},
		},
		{
			name:         "phản hồi rỗng",
			body:         "",
			wantVerdicts: map[string]string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseCSV(tc.body, allowed)

			if len(got.Verdicts) != len(tc.wantVerdicts) {
				t.Fatalf("số kết luận = %d, muốn %d (%+v)",
					len(got.Verdicts), len(tc.wantVerdicts), got.Verdicts)
			}
			for _, v := range got.Verdicts {
				want, found := tc.wantVerdicts[v.Domain]
				if !found {
					t.Errorf("kết luận thừa cho %q", v.Domain)
					continue
				}
				if v.Category != want {
					t.Errorf("%s: nhãn = %q, muốn %q", v.Domain, v.Category, want)
				}
			}
			if got.Skipped != tc.wantSkipped {
				t.Errorf("skipped = %d, muốn %d", got.Skipped, tc.wantSkipped)
			}
			if len(got.Unknown) != len(tc.wantUnknown) {
				t.Errorf("unknown = %v, muốn %v", got.Unknown, tc.wantUnknown)
			}
		})
	}
}

// Lý do có dấu phẩy mà model quên bọc nháy phải ghép lại được, không được cắt cụt.
func TestParseCSVJoinsUnquotedReason(t *testing.T) {
	got := parseCSV("example.com,ads,0.9,quảng cáo, theo dõi, và nhiều thứ khác\n",
		[]string{"example.com"})

	if len(got.Verdicts) != 1 {
		t.Fatalf("số kết luận = %d, muốn 1", len(got.Verdicts))
	}
	if reason := got.Verdicts[0].Reason; !strings.Contains(reason, "nhiều thứ khác") {
		t.Errorf("lý do = %q, muốn giữ nguyên phần sau dấu phẩy", reason)
	}
}

// Lý do dài bị cắt để một model nói nhiều không phình CSDL.
func TestParseCSVClipsLongReason(t *testing.T) {
	long := strings.Repeat("dài ", 200)
	got := parseCSV("example.com,ads,0.9,"+long+"\n", []string{"example.com"})

	if len(got.Verdicts) != 1 {
		t.Fatalf("số kết luận = %d, muốn 1", len(got.Verdicts))
	}
	if n := len(got.Verdicts[0].Reason); n > maxReasonLen {
		t.Errorf("độ dài lý do = %d, muốn ≤ %d", n, maxReasonLen)
	}
}

func TestParseConfidence(t *testing.T) {
	cases := map[string]float64{
		"0.85":   0.85,
		"85%":    0.85,
		"85":     0.85,
		" 0.5 ":  0.5,
		"1":      1,
		"1.5":    0.015, // >1 thì hiểu là phần trăm
		"-0.3":   0,
		"high":   0.9,
		"medium": 0.6,
		"low":    0.3,
		"cao":    0.9,
		"":       0,
		"xyz":    0,
	}
	for raw, want := range cases {
		if got := parseConfidence(raw); got != want {
			t.Errorf("parseConfidence(%q) = %v, muốn %v", raw, got, want)
		}
	}
}

// Ba mươi ký tự dữ liệu thật cho một domain phải rẻ hơn hẳn JSON tương đương.
// Test này khoá lý do tồn tại của định dạng CSV, không phải một con số cụ thể.
func TestCSVIsCheaperThanJSON(t *testing.T) {
	const rows = 40

	var csv, json strings.Builder
	for range rows {
		csv.WriteString("example.com,ads,0.9,quảng cáo\n")
		json.WriteString(
			`{"domain":"example.com","category":"ads","confidence":0.9,"reason":"quảng cáo"},`)
	}

	if csv.Len()*2 > json.Len() {
		t.Errorf("CSV %d byte so với JSON %d byte — không tiết kiệm được một nửa",
			csv.Len(), json.Len())
	}
}
