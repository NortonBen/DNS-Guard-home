// Package llm gọi một endpoint kiểu OpenAI Chat Completions.
//
// Hoàn toàn TUỲ CHỌN: chưa có khóa API thì tính năng tự tắt và tầng trên trả về
// thông báo rõ ràng thay vì lỗi khó hiểu. DNSGuard không phụ thuộc vào nó —
// bộ luật phân loại vẫn chạy đủ khi không cấu hình model nào.
//
// Nhà cung cấp nào nói được giao thức Chat Completions đều dùng được, chỉ cần
// đổi base URL và tên model:
//
//	DeepSeek  https://api.deepseek.com/v1      deepseek-chat (hoặc deepseek-reasoner)
//	OpenAI    https://api.openai.com/v1        gpt-4o-mini
//	Ollama    http://localhost:11434/v1        llama3.1
//
// Gói này chỉ biết giao thức, không biết gì về domain hay blocklist. Phần nghiệp
// vụ nằm ở internal/ai.
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ErrDisabled khi chưa cấu hình LLM.
var ErrDisabled = errors.New("chưa cấu hình model AI — nhập khóa API ở màn Cài đặt, hoặc đặt DNSGUARD_AI_API_KEY")

// Client giữ cấu hình LLM và ĐỔI ĐƯỢC lúc đang chạy: người vận hành sửa model
// hay trần token trên giao diện là có hiệu lực ngay, không phải sửa biến môi
// trường rồi apply lại pod. Vì thế mọi trường cấu hình đều nằm sau mu.
type Client struct {
	mu        sync.RWMutex
	baseURL   string
	apiKey    string
	model     string
	maxTokens int
	http      *http.Client
}

// DefaultMaxTokens đủ cho một lô phân loại hoặc một câu trả lời hỏi đáp. Model
// dạng reasoning (deepseek-reasoner) tiêu token cho cả phần suy luận, nên cần nới
// rộng hơn — xem DNSGUARD_AI_MAX_TOKENS.
const DefaultMaxTokens = 4000

func New(baseURL, apiKey, model string, maxTokens int) *Client {
	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokens
	}
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		apiKey:    apiKey,
		model:     model,
		maxTokens: maxTokens,
		// Model reasoning trả lời chậm hơn hẳn — cho thời gian rộng tay.
		http: &http.Client{Timeout: 5 * time.Minute},
	}
}

// Configure thay cấu hình đang dùng. Giá trị rỗng / <=0 thì giữ nguyên mặc
// định như lúc khởi tạo: apiKey rỗng nghĩa là TẮT, còn base URL và model rỗng
// thì rơi về giá trị đang có.
func (c *Client) Configure(baseURL, apiKey, model string, maxTokens int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if baseURL != "" {
		c.baseURL = strings.TrimRight(baseURL, "/")
	}
	if model != "" {
		c.model = model
	}
	if maxTokens > 0 {
		c.maxTokens = maxTokens
	}
	c.apiKey = apiKey
}

// Config là cấu hình đang dùng, để giao diện hiện đúng thứ app đang chạy.
// KHÔNG trả apiKey ra ngoài — chỉ nói đã có hay chưa.
type Config struct {
	BaseURL   string `json:"base_url"`
	Model     string `json:"model"`
	MaxTokens int    `json:"max_tokens"`
	HasAPIKey bool   `json:"has_api_key"`
}

func (c *Client) Config() Config {
	if c == nil {
		return Config{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return Config{
		BaseURL: c.baseURL, Model: c.model, MaxTokens: c.maxTokens, HasAPIKey: c.apiKey != "",
	}
}

func (c *Client) Enabled() bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.apiKey != ""
}

// snapshot đọc cấu hình một lần cho mỗi request, để lúc đang gọi mạng mà người
// khác đổi cấu hình thì request đó vẫn dùng trọn một bộ giá trị nhất quán.
func (c *Client) snapshot() (baseURL, apiKey, model string, maxTokens int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.baseURL, c.apiKey, c.model, c.maxTokens
}

type chatReq struct {
	Model     string    `json:"model"`
	Messages  []Message `json:"messages"`
	MaxTokens int       `json:"max_tokens,omitempty"`
	Stream    bool      `json:"stream,omitempty"`
	Tools     []Tool    `json:"tools,omitempty"`
}

// Message là một lượt trong hội thoại.
// Role: system | user | assistant | tool.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// ToolCalls chỉ có ở lượt assistant khi model muốn gọi công cụ.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// ToolCallID và Name bắt buộc ở lượt role=tool, để model biết kết quả này
	// trả lời cho lời gọi nào.
	ToolCallID string `json:"tool_call_id,omitempty"`
	Name       string `json:"name,omitempty"`
}

// Các vai trò dùng khi dựng hội thoại.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Tool mô tả một công cụ cho model, theo đúng lược đồ của Chat Completions.
type Tool struct {
	Type     string       `json:"type"` // luôn là "function"
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Parameters là JSON Schema của tham số.
	Parameters map[string]any `json:"parameters"`
}

// ToolCall là một lời gọi công cụ do model phát ra.
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Name string `json:"name"`
	// Arguments là chuỗi JSON, không phải object — đây là quy ước của API.
	Arguments string `json:"arguments"`
}

type chatResp struct {
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// streamChunk là một mẩu SSE khi bật stream.
//
// ReasoningContent là phần "suy nghĩ" của các model dạng reasoning
// (vd deepseek-reasoner). Ta CỐ Ý không hiển thị nó: người vận hành cần kết
// luận, không cần đọc dòng suy luận. Khai báo trường ở đây để
// phân biệt rõ với `content`, tránh vô tình trộn hai luồng vào nhau.
type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *Client) post(ctx context.Context, body chatReq) (*http.Response, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	baseURL, apiKey, _, _ := c.snapshot()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		baseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gọi LLM thất bại: %w", err)
	}
	return resp, nil
}

// Chat gửi cả hội thoại và trả về câu trả lời hoàn chỉnh.
func (c *Client) Chat(ctx context.Context, messages []Message) (string, error) {
	reply, err := c.ChatWithTools(ctx, messages, nil)
	if err != nil {
		return "", err
	}
	return reply.Content, nil
}

// Reply là một lượt trả lời của model: hoặc câu chữ, hoặc yêu cầu gọi công cụ.
type Reply struct {
	Content      string
	ToolCalls    []ToolCall
	FinishReason string
}

// ChatWithTools gửi hội thoại kèm danh sách công cụ model được phép gọi.
// tools rỗng thì hành xử y như Chat thường.
func (c *Client) ChatWithTools(ctx context.Context, messages []Message, tools []Tool) (Reply, error) {
	if !c.Enabled() {
		return Reply{}, ErrDisabled
	}
	_, _, model, maxTokens := c.snapshot()
	resp, err := c.post(ctx, chatReq{
		Model: model, MaxTokens: maxTokens, Messages: messages, Tools: tools,
	})
	if err != nil {
		return Reply{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return Reply{}, err
	}
	var out chatResp
	if err := json.Unmarshal(raw, &out); err != nil {
		return Reply{}, fmt.Errorf("LLM trả JSON lạ (HTTP %d)", resp.StatusCode)
	}
	if out.Error != nil {
		return Reply{}, fmt.Errorf("LLM báo lỗi: %s", out.Error.Message)
	}
	if resp.StatusCode != http.StatusOK || len(out.Choices) == 0 {
		return Reply{}, fmt.Errorf("LLM không trả kết quả (HTTP %d)", resp.StatusCode)
	}
	ch := out.Choices[0]
	text := ch.Message.Content
	// Chỉ báo bị cắt khi model thực sự định nói tiếp, không phải khi nó dừng
	// để gọi công cụ. Cắt sạch (chưa kịp viết chữ nào) là chuyện khác hẳn: nói
	// đúng cái đó, kèm cách xử lý, chứ đừng để người dùng nhìn một dòng "bị cắt"
	// mà không hiểu vừa xảy ra gì.
	if ch.FinishReason == "length" {
		if strings.TrimSpace(text) == "" {
			text = "(⚠ model chưa kịp viết chữ nào trong giới hạn độ dài — hỏi lại ngắn gọn hơn, " +
				"hoặc tăng DNSGUARD_AI_MAX_TOKENS)"
		} else {
			text += "\n\n(⚠ phân tích bị cắt do giới hạn độ dài)"
		}
	}
	return Reply{
		Content:      text,
		ToolCalls:    ch.Message.ToolCalls,
		FinishReason: ch.FinishReason,
	}, nil
}

// ChatStream gửi hội thoại và gọi onDelta cho từng mẩu chữ nhận được, đồng
// thời trả về toàn văn khi xong — để caller lưu vào DB.
func (c *Client) ChatStream(ctx context.Context, messages []Message, onDelta func(string)) (string, error) {
	if !c.Enabled() {
		return "", ErrDisabled
	}
	_, _, model, maxTokens := c.snapshot()
	resp, err := c.post(ctx, chatReq{
		Model: model, MaxTokens: maxTokens, Messages: messages, Stream: true,
	})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		var out chatResp
		if json.Unmarshal(raw, &out) == nil && out.Error != nil {
			return "", fmt.Errorf("LLM báo lỗi: %s", out.Error.Message)
		}
		return "", fmt.Errorf("LLM trả HTTP %d", resp.StatusCode)
	}

	var full strings.Builder
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		data, found := strings.CutPrefix(line, "data:")
		if !found {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			break
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Error != nil {
			return full.String(), fmt.Errorf("LLM báo lỗi: %s", chunk.Error.Message)
		}
		for _, ch := range chunk.Choices {
			if ch.Delta.Content == "" {
				continue
			}
			full.WriteString(ch.Delta.Content)
			if onDelta != nil {
				onDelta(ch.Delta.Content)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return full.String(), fmt.Errorf("đọc stream LLM dở dang: %w", err)
	}
	return full.String(), nil
}

// Explain là ca dùng một lượt: system prompt + log, không cần lịch sử.
func (c *Client) Explain(ctx context.Context, system, prompt string) (string, error) {
	return c.Chat(ctx, []Message{
		{Role: RoleSystem, Content: system},
		{Role: RoleUser, Content: prompt},
	})
}
