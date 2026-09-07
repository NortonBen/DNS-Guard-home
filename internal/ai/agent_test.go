package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/benji/dnsguard/internal/llm"
)

// toolCallProvider đóng vai một model biết gọi công cụ: lượt đầu yêu cầu gọi
// công cụ, lượt sau trả lời bằng chữ dựa trên kết quả nhận được.
func toolCallProvider(t *testing.T, tool string, args string) (*httptest.Server, *atomic.Pointer[string]) {
	t.Helper()

	var turn atomic.Int64
	var lastToolResult atomic.Pointer[string]

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("giải mã request: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")

		if turn.Add(1) == 1 {
			// Lượt đầu phải thấy danh sách công cụ, nếu không thì registry chưa
			// được gửi đi và cả vòng lặp vô nghĩa.
			if len(req.Tools) == 0 {
				t.Error("lượt đầu không kèm định nghĩa công cụ nào")
			}
			json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"message": map[string]any{
						"role": "assistant",
						"tool_calls": []map[string]any{{
							"id": "call-1", "type": "function",
							"function": map[string]any{"name": tool, "arguments": args},
						}},
					},
					"finish_reason": "tool_calls",
				}},
			})
			return
		}

		// Lượt sau: kết quả công cụ phải có trong hội thoại.
		for _, m := range req.Messages {
			if m.Role == llm.RoleTool {
				content := m.Content
				lastToolResult.Store(&content)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message":       map[string]any{"role": "assistant", "content": "Đã tra xong."},
				"finish_reason": "stop",
			}},
		})
	}))
	t.Cleanup(srv.Close)

	return srv, &lastToolResult
}

// Vòng lặp gọi công cụ phải chạy đủ: gửi định nghĩa đi, chạy công cụ, đưa kết quả
// trở lại hội thoại, rồi mới lấy câu trả lời chữ.
func TestAskRunsToolLoop(t *testing.T) {
	srv, lastToolResult := toolCallProvider(t, "cong_cu_thu", `{"x":"gia-tri"}`)

	reg := &Registry{}
	reg.add(Tool{
		Name:        "cong_cu_thu",
		Description: "công cụ dùng cho test",
		Run: func(_ context.Context, args map[string]any) (string, error) {
			return "ket-qua-cho-" + argString(args, "x"), nil
		},
	})

	answer, err := Ask(context.Background(), llm.New(srv.URL, "k", "m", 0), reg,
		[]llm.Message{{Role: llm.RoleUser, Content: "hỏi thử"}}, nil)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}

	if answer.Content != "Đã tra xong." {
		t.Errorf("câu trả lời = %q", answer.Content)
	}
	if len(answer.Steps) != 1 {
		t.Fatalf("số bước = %d, muốn 1", len(answer.Steps))
	}
	if answer.Steps[0].Tool != "cong_cu_thu" {
		t.Errorf("tên công cụ = %q", answer.Steps[0].Tool)
	}
	// Dấu vết phải mang kết quả thật, vì đó là toàn bộ giá trị của nó với người đọc.
	if !strings.Contains(answer.Steps[0].Result, "ket-qua-cho-gia-tri") {
		t.Errorf("dấu vết = %q, muốn chứa kết quả công cụ", answer.Steps[0].Result)
	}
	// Và model phải thật sự nhận được kết quả đó, không chỉ giao diện thấy.
	if got := lastToolResult.Load(); got == nil || !strings.Contains(*got, "ket-qua-cho-gia-tri") {
		t.Error("kết quả công cụ không được đưa trở lại hội thoại")
	}
}

// Công cụ hỏng phải báo cho model bằng văn bản, không làm dừng cả lượt hỏi: model
// cần đọc được lý do để tự chuyển hướng — gõ sai tên domain thì thử tìm kiếm.
func TestToolErrorReachesModelAsText(t *testing.T) {
	reg := &Registry{}
	reg.add(Tool{
		Name: "hong",
		Run: func(context.Context, map[string]any) (string, error) {
			return "", errNotFound
		},
	})

	out := reg.Call(context.Background(), "hong", "{}")
	if !strings.Contains(out, "Lỗi khi chạy hong") {
		t.Errorf("kết quả = %q, muốn mô tả lỗi bằng văn bản", out)
	}

	// Công cụ không tồn tại cũng vậy: model tự thử tên khác được.
	if out := reg.Call(context.Background(), "khong-co", "{}"); !strings.Contains(out, "không có công cụ") {
		t.Errorf("kết quả = %q", out)
	}

	// Tham số không phải JSON hợp lệ là lỗi model hay mắc nhất.
	if out := reg.Call(context.Background(), "hong", "{khong-phai-json"); !strings.Contains(out, "JSON hợp lệ") {
		t.Errorf("kết quả = %q", out)
	}
}

// Công cụ MCP trùng tên không được đè lên công cụ dựng sẵn: một máy chủ ngoài
// không được phép chiếm chỗ của công cụ đọc dữ liệu trong máy.
func TestBuiltinToolWinsNameCollision(t *testing.T) {
	reg := &Registry{}
	reg.add(Tool{
		Name: "domain_lookup",
		Run:  func(context.Context, map[string]any) (string, error) { return "cua-app", nil },
	})
	reg.add(Tool{
		Name: "domain_lookup",
		Run:  func(context.Context, map[string]any) (string, error) { return "cua-mcp", nil },
	})

	if reg.Len() != 1 {
		t.Fatalf("số công cụ = %d, muốn 1", reg.Len())
	}
	if out := reg.Call(context.Background(), "domain_lookup", "{}"); out != "cua-app" {
		t.Errorf("chạy %q, muốn công cụ của app", out)
	}
}

// Kết quả công cụ khổng lồ bị cắt để không tràn cửa sổ ngữ cảnh.
func TestToolOutputIsCapped(t *testing.T) {
	reg := &Registry{}
	reg.add(Tool{
		Name: "dai",
		Run: func(context.Context, map[string]any) (string, error) {
			return strings.Repeat("x", maxToolOutput*2), nil
		},
	})

	out := reg.Call(context.Background(), "dai", "{}")
	if len(out) > maxToolOutput+200 {
		t.Errorf("độ dài kết quả = %d, muốn bị cắt quanh %d", len(out), maxToolOutput)
	}
	if !strings.Contains(out, "bị cắt") {
		t.Error("không nói rõ kết quả đã bị cắt")
	}
}

// Skill khớp không phân biệt dấu: người gõ "ha tang" phải khớp trigger "hạ tầng".
func TestSkillMatchIgnoresDiacritics(t *testing.T) {
	all := []Skill{
		{Name: "nen", Always: true, Enabled: true, Content: "luôn dùng"},
		{Name: "ha-tang", Enabled: true, Triggers: []string{"hạ tầng"}, Content: "về hạ tầng"},
		{Name: "khac", Enabled: true, Triggers: []string{"xuất bản"}, Content: "về xuất bản"},
		{Name: "da-tat", Enabled: false, Triggers: []string{"hạ tầng"}, Content: "đã tắt"},
	}

	matched := MatchSkills(all, "cho toi biet ve ha tang cua domain nay")
	names := SkillNames(matched)

	if len(names) != 2 {
		t.Fatalf("skill khớp = %v, muốn 2", names)
	}
	// Skill "luôn dùng" lên trước: đó là quy ước nền, đọc trước rồi mới tới cái riêng.
	if names[0] != "nen" {
		t.Errorf("thứ tự = %v, muốn skill always lên trước", names)
	}
	if names[1] != "ha-tang" {
		t.Errorf("skill khớp theo từ khoá = %q, muốn ha-tang", names[1])
	}
}

// Phần chèn vượt trần thì dừng hẳn thay vì cắt giữa chừng: một skill bị cắt dở
// dễ khiến model hiểu ngược ý hơn là không có nó.
func TestRenderSkillsStopsAtBudget(t *testing.T) {
	all := []Skill{
		{Name: "mot", Enabled: true, Always: true, Content: strings.Repeat("a", 500)},
		{Name: "hai", Enabled: true, Always: true, Content: strings.Repeat("b", 500)},
	}

	// Ngân sách chỉ đủ cho một skill. Không khẳng định skill NÀO bị bỏ — thứ tự do
	// hàm khớp quyết định — mà khẳng định đúng một cái lọt vào và người đọc được
	// báo là có phần bị bỏ.
	out := RenderSkills(MatchSkills(all, "bất kỳ"), 600)

	included := 0
	for _, body := range []string{"aaaa", "bbbb"} {
		if strings.Contains(out, body) {
			included++
		}
	}
	if included != 1 {
		t.Errorf("số skill lọt vào = %d, muốn 1 — ngân sách 600 chỉ đủ cho một cái", included)
	}
	if !strings.Contains(out, "bị bỏ qua do giới hạn độ dài") {
		t.Error("không nói rõ có skill bị bỏ qua")
	}
	if len(out) > 700 {
		t.Errorf("độ dài phần chèn = %d, vượt xa ngân sách 600", len(out))
	}
}

var errNotFound = &toolError{"không tìm thấy"}

type toolError struct{ msg string }

func (e *toolError) Error() string { return e.msg }

// Bộ skill dựng sẵn phải giữ được ba tính chất, nếu không nó âm thầm đắt lên hoặc
// im lặng đi mà không ai nhận ra cho tới khi đọc hoá đơn.
func TestBuiltinSkillsStayCheapAndReachable(t *testing.T) {
	all := BuiltinSkills()

	// 1. Phần "luôn dùng" đi kèm MỌI câu hỏi, nên nó là chi phí cố định. Giữ nhỏ.
	alwaysBudget := RenderSkills(MatchSkills(all, "một câu hỏi không khớp từ khoá nào"), 0)
	if n := len(alwaysBudget); n > 1500 {
		t.Errorf("phần luôn chèn = %d ký tự, quá đắt cho mọi lượt hỏi", n)
	}

	// 2. Skill có từ khoá mà không khớp được câu hỏi nào là skill chết. Mỗi cái phải
	//    tự kích hoạt được bằng chính từ khoá của nó.
	for _, sk := range all {
		if sk.Always {
			continue
		}
		if len(sk.Triggers) == 0 {
			t.Errorf("skill %q không always mà cũng không có từ khoá — không bao giờ được chèn", sk.Name)
			continue
		}
		for _, trigger := range sk.Triggers {
			matched := SkillNames(MatchSkills(all, "cho tôi biết về "+trigger))
			if !slices.Contains(matched, sk.Name) {
				t.Errorf("skill %q không tự kích hoạt bằng từ khoá %q của chính nó", sk.Name, trigger)
			}
		}
	}

	// 3. Kịch bản tệ nhất — câu hỏi chạm nhiều chủ đề — vẫn phải lọt trần ngữ cảnh.
	//    Vượt trần nghĩa là có skill bị bỏ, và cái bị bỏ thì im lặng.
	broad := "vì sao domain này có điểm cao, cname trỏ về đâu, phân loại thế nào, " +
		"ngưỡng bao nhiêu, dữ kiện virustotal ra sao, xuất bản khi nào, có chặn nhầm không"
	wide := RenderSkills(MatchSkills(all, broad), MaxInjectedChars)
	if strings.Contains(wide, "bị bỏ qua do giới hạn độ dài") {
		t.Errorf("câu hỏi rộng làm tràn trần %d ký tự — có skill bị bỏ im lặng", MaxInjectedChars)
	}
}

// Từ khoá tiếng Anh phải khớp được câu hỏi viết bằng tiếng Việt và ngược lại: người
// vận hành gõ tiếng Việt, còn nội dung skill viết bằng tiếng Anh cho model.
func TestBuiltinSkillsMatchBothLanguages(t *testing.T) {
	all := BuiltinSkills()

	cases := map[string]string{
		"tại sao trang này không vào được nữa":   "false-positive-triage",
		"why is this site broken":                "false-positive-triage",
		"cname tro ve dau":                       "cname-cloaking",
		"đã chặn rồi mà router vẫn phân giải":    "publishing-pipeline",
		"virustotal noi gi ve domain nay":        "enrichment-limits",
		"ads hay tracking thi khac nhau the nao": "category-boundaries",
		"can bao nhieu diem thi bi chan":         "scoring-thresholds",
	}
	for question, want := range cases {
		got := SkillNames(MatchSkills(all, question))
		if !slices.Contains(got, want) {
			t.Errorf("câu hỏi %q khớp %v, muốn có %q", question, got, want)
		}
	}
}
