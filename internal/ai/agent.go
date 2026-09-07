package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/benji/dnsguard/internal/llm"
	"github.com/benji/dnsguard/internal/mcp"
)

// MaxSteps là số vòng gọi công cụ tối đa cho một câu hỏi.
//
// Chặn ở đây để một model đi lạc không gọi công cụ vô hạn và đốt hết hạn mức.
// Sáu vòng đủ cho chuỗi tra cứu sâu nhất mà bộ công cụ này dựng được: tìm domain
// → xem chi tiết → xem dữ kiện → xem quan hệ → xem lịch sử AI → kết luận.
const MaxSteps = 6

// maxToolOutput chặn một công cụ trả về hàng megabyte làm tràn ngữ cảnh.
const maxToolOutput = 12000

// maxTraceResult giới hạn phần kết quả giữ lại để hiển thị. Bản đầy đủ vẫn được
// gửi cho model; chỉ dấu vết cho người đọc mới bị cắt.
const maxTraceResult = 1500

// Step là một lần gọi công cụ, giữ lại để người dùng thấy câu trả lời dựa trên
// dữ liệu nào. Không có phần này thì agent là hộp đen.
type Step struct {
	Tool   string `json:"tool"`
	Args   string `json:"args"`
	Result string `json:"result"`
}

// Answer là kết quả một lượt hỏi.
type Answer struct {
	Content string   `json:"content"`
	Steps   []Step   `json:"steps"`
	Skills  []string `json:"skills"`
}

// Host là các thao tác cần logic nằm ngoài gói ai.
//
// Chỉ có một: xếp một job hỏi lại. Ranh giới này là cố ý — model không có công
// cụ nào đổi được trạng thái domain. Chặn hay bỏ chặn vẫn phải bấm trên giao
// diện, nơi đã có kiểm tra danh sách bảo vệ và ghi nhật ký quyết định.
type Host interface {
	// Recheck xếp một job hỏi model lại về một domain, trả về id job.
	Recheck(ctx context.Context, domain string) (string, error)
}

// Tool là một công cụ gọi được, đã kèm sẵn cách chạy.
type Tool struct {
	Name        string
	Description string
	Schema      map[string]any
	Run         func(ctx context.Context, args map[string]any) (string, error)
}

// Registry gom công cụ dựng sẵn và công cụ từ các máy chủ MCP.
type Registry struct {
	order []string
	tools map[string]Tool
}

func (r *Registry) add(t Tool) {
	if r.tools == nil {
		r.tools = map[string]Tool{}
	}
	// Công cụ MCP trùng tên công cụ dựng sẵn thì bỏ qua, giữ cái của app: một máy
	// chủ ngoài không được phép chiếm chỗ của công cụ đọc dữ liệu trong máy.
	if _, dup := r.tools[t.Name]; dup {
		return
	}
	r.tools[t.Name] = t
	r.order = append(r.order, t.Name)
}

// Definitions trả lược đồ công cụ để gửi cho model.
func (r *Registry) Definitions() []llm.Tool {
	out := make([]llm.Tool, 0, len(r.order))
	for _, name := range r.order {
		t := r.tools[name]
		schema := t.Schema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, llm.Tool{
			Type: "function",
			Function: llm.ToolFunction{
				Name: t.Name, Description: t.Description, Parameters: schema,
			},
		})
	}
	return out
}

func (r *Registry) Len() int { return len(r.order) }

// Names liệt kê tên công cụ theo thứ tự chữ cái, cho giao diện hiển thị.
func (r *Registry) Names() []string {
	out := append([]string(nil), r.order...)
	sort.Strings(out)
	return out
}

// Describe liệt kê công cụ kèm mô tả, cho màn cài đặt.
func (r *Registry) Describe() []map[string]string {
	out := make([]map[string]string, 0, len(r.order))
	for _, name := range r.Names() {
		out = append(out, map[string]string{
			"name": name, "description": r.tools[name].Description,
		})
	}
	return out
}

// Call chạy một công cụ.
//
// Lỗi trả về dưới dạng văn bản chứ không phải error: model cần đọc được lý do
// hỏng để tự chuyển hướng — gõ sai tên domain thì thử tìm kiếm — chứ không phải
// dừng cả lượt hỏi.
func (r *Registry) Call(ctx context.Context, name, argsJSON string) string {
	t, found := r.tools[name]
	if !found {
		return "Lỗi: không có công cụ tên " + name
	}

	args := map[string]any{}
	if s := strings.TrimSpace(argsJSON); s != "" && s != "null" {
		if err := json.Unmarshal([]byte(s), &args); err != nil {
			return fmt.Sprintf("Lỗi: tham số của %s không phải JSON hợp lệ: %v", name, err)
		}
	}

	out, err := t.Run(ctx, args)
	if err != nil {
		return "Lỗi khi chạy " + name + ": " + err.Error()
	}
	if len(out) > maxToolOutput {
		out = out[:maxToolOutput] + "\n… (kết quả bị cắt do quá dài)"
	}
	if strings.TrimSpace(out) == "" {
		return "(không có dữ liệu)"
	}
	return out
}

// Ask chạy vòng lặp gọi công cụ tới khi model đưa ra câu trả lời bằng chữ.
//
// onStep (có thể nil) được gọi ngay khi một công cụ chạy xong, để giao diện báo
// tiến độ thay vì im lặng suốt lúc model đang tra cứu.
func Ask(ctx context.Context, client *llm.Client, reg *Registry,
	messages []llm.Message, onStep func(Step)) (Answer, error) {

	var res Answer
	res.Steps = []Step{}

	if client == nil || !client.Enabled() {
		return res, ErrNoAPIKey
	}

	tools := reg.Definitions()
	if len(tools) == 0 {
		content, err := client.Chat(ctx, messages)
		res.Content = content
		return res, err
	}

	convo := append([]llm.Message(nil), messages...)
	for range MaxSteps {
		reply, err := client.ChatWithTools(ctx, convo, tools)
		if err != nil {
			return res, err
		}

		if len(reply.ToolCalls) == 0 {
			res.Content = reply.Content
			// Hết ngân sách token ngay khi model bắt đầu viết kết luận: hỏi lại một
			// lần và ép ngắn. Không có bước này thì công sức tra cứu mấy vòng công
			// cụ đổ đi hết, người dùng chỉ nhận một dòng "bị cắt do giới hạn độ dài".
			if reply.FinishReason == "length" {
				if short, err := concludeShort(ctx, client, convo); err == nil && short != "" {
					res.Content = short
				}
			}
			return res, nil
		}

		// Lượt assistant chứa yêu cầu gọi công cụ phải giữ nguyên trong hội thoại,
		// nếu không model không khớp được kết quả trả về với lời gọi của nó.
		convo = append(convo, llm.Message{
			Role: llm.RoleAssistant, Content: reply.Content, ToolCalls: reply.ToolCalls,
		})

		for _, call := range reply.ToolCalls {
			out := reg.Call(ctx, call.Function.Name, call.Function.Arguments)
			step := Step{
				Tool: call.Function.Name, Args: call.Function.Arguments,
				Result: clipTrace(out),
			}
			res.Steps = append(res.Steps, step)
			if onStep != nil {
				onStep(step)
			}
			convo = append(convo, llm.Message{
				Role: llm.RoleTool, ToolCallID: call.ID,
				Name: call.Function.Name, Content: out,
			})
		}
	}

	// Hết số vòng cho phép: hỏi lần cuối KHÔNG kèm công cụ để model buộc phải kết
	// luận bằng những gì đã thu thập, thay vì trả về tay trắng.
	convo = append(convo, llm.Message{
		Role: llm.RoleSystem,
		Content: "Đã đạt giới hạn số lần tra cứu. Hãy kết luận ngay bằng dữ liệu đã có, " +
			"và nói rõ phần nào còn thiếu dữ kiện.",
	})
	content, err := client.Chat(ctx, convo)
	if err != nil {
		return res, err
	}
	res.Content = content
	return res, nil
}

// concludeShort hỏi lại KHÔNG kèm công cụ và ép trả lời ngắn, để câu kết luận
// lọt vừa giới hạn độ dài.
func concludeShort(ctx context.Context, client *llm.Client, convo []llm.Message) (string, error) {
	return client.Chat(ctx, append(append([]llm.Message(nil), convo...), llm.Message{
		Role: llm.RoleSystem,
		Content: "Câu trả lời vừa rồi bị cắt vì quá dài. Hãy trả lời lại THẬT NGẮN GỌN " +
			"bằng dữ liệu đã tra cứu: kết luận trước, tối đa 10 dòng, bỏ hết phần dẫn dắt.",
	}))
}

func clipTrace(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxTraceResult {
		return s
	}
	return s[:maxTraceResult] + "\n… (rút gọn để hiển thị)"
}

// addMCPTools nối công cụ từ các máy chủ MCP đang bật.
//
// Một máy chủ hỏng không được làm gãy cả bộ: nó bị bỏ qua và tên nó vào warnings
// để giao diện báo cho người dùng. Nếu không, khai báo sai một máy chủ MCP sẽ làm
// hỏng đăng nhập của toàn bộ tính năng hỏi đáp.
func addMCPTools(ctx context.Context, r *Registry, hist *Store) []string {
	if hist == nil {
		return nil
	}
	servers, err := hist.ListMCPServers(ctx, true)
	if err != nil {
		return []string{"không đọc được danh sách máy chủ MCP: " + err.Error()}
	}

	var warnings []string
	for _, srv := range servers {
		client := mcp.New(srv.Name, srv.URL, srv.AuthHeader)
		defs, err := client.ListTools(ctx)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("máy chủ MCP %q: %v", srv.Name, err))
			continue
		}
		for _, d := range defs {
			// Tiền tố tên máy chủ để hai MCP có công cụ trùng tên không đè nhau.
			remote, c := d.Name, client
			r.add(Tool{
				Name:        srv.Name + "__" + d.Name,
				Description: "[MCP " + srv.Name + "] " + d.Description,
				Schema:      d.InputSchema,
				Run: func(ctx context.Context, args map[string]any) (string, error) {
					return c.CallTool(ctx, remote, args)
				},
			})
		}
	}
	return warnings
}
