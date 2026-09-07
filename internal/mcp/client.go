// Package mcp là client tối giản cho Model Context Protocol.
//
// Chỉ làm ba việc app cần: bắt tay (initialize), liệt kê công cụ (tools/list)
// và gọi công cụ (tools/call). Truyền tải là JSON-RPC 2.0 trên HTTP POST —
// máy chủ trả JSON thường hoặc bọc trong một khung SSE (Streamable HTTP), nên
// client đọc được cả hai dạng.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// ProtocolVersion là bản giao thức client khai báo lúc bắt tay.
const ProtocolVersion = "2024-11-05"

type Client struct {
	name       string
	url        string
	authHeader string
	http       *http.Client
	nextID     atomic.Int64
}

func New(name, url, authHeader string) *Client {
	return &Client{
		name:       name,
		url:        strings.TrimRight(url, "/"),
		authHeader: authHeader,
		http:       &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *Client) Name() string { return c.name }

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

// call gửi một lời gọi JSON-RPC và trả về phần result.
func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	body, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0", ID: c.nextID.Add(1), Method: method, Params: params,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	// Khai báo nhận cả hai dạng: JSON thường và khung SSE của Streamable HTTP.
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.authHeader != "" {
		name, value, found := strings.Cut(c.authHeader, ":")
		if found {
			req.Header.Set(strings.TrimSpace(name), strings.TrimSpace(value))
		} else {
			req.Header.Set("Authorization", strings.TrimSpace(c.authHeader))
		}
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gọi MCP %s thất bại: %w", c.name, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("MCP %s trả HTTP %d: %s",
			c.name, resp.StatusCode, clip(string(raw), 300))
	}

	payload := raw
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		payload = extractSSEData(raw)
		if payload == nil {
			return nil, fmt.Errorf("MCP %s trả khung SSE không có dữ liệu", c.name)
		}
	}

	var out rpcResponse
	if err := json.Unmarshal(payload, &out); err != nil {
		return nil, fmt.Errorf("MCP %s trả JSON lạ: %s", c.name, clip(string(payload), 300))
	}
	if out.Error != nil {
		return nil, fmt.Errorf("MCP %s báo lỗi %d: %s", c.name, out.Error.Code, out.Error.Message)
	}
	return out.Result, nil
}

// extractSSEData lấy phần data của sự kiện SSE đầu tiên có nội dung.
func extractSSEData(raw []byte) []byte {
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
	var buf strings.Builder
	for sc.Scan() {
		line := sc.Text()
		if data, found := strings.CutPrefix(line, "data:"); found {
			buf.WriteString(strings.TrimSpace(data))
			continue
		}
		// Dòng trống kết thúc một sự kiện.
		if strings.TrimSpace(line) == "" && buf.Len() > 0 {
			return []byte(buf.String())
		}
	}
	if buf.Len() > 0 {
		return []byte(buf.String())
	}
	return nil
}

func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// ToolDef là một công cụ do máy chủ MCP cung cấp.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// Initialize bắt tay với máy chủ. Một số máy chủ từ chối tools/list nếu chưa
// initialize, nên luôn gọi trước.
func (c *Client) Initialize(ctx context.Context) error {
	_, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "k8s-terraform-manager",
			"version": "1",
		},
	})
	return err
}

// ListTools trả danh sách công cụ máy chủ này cung cấp.
func (c *Client) ListTools(ctx context.Context) ([]ToolDef, error) {
	if err := c.Initialize(ctx); err != nil {
		return nil, err
	}
	raw, err := c.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var out struct {
		Tools []ToolDef `json:"tools"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("MCP %s: danh sách công cụ không đọc được: %w", c.name, err)
	}
	return out.Tools, nil
}

// CallTool gọi một công cụ và trả về phần văn bản trong kết quả.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	if args == nil {
		args = map[string]any{}
	}
	raw, err := c.call(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		return "", err
	}
	var out struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("MCP %s: kết quả công cụ không đọc được: %w", c.name, err)
	}
	var b strings.Builder
	for _, part := range out.Content {
		if part.Text != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(part.Text)
		}
	}
	text := b.String()
	if out.IsError {
		// Lỗi do chính công cụ báo — trả về như lỗi để agent biết đường xử lý.
		return text, fmt.Errorf("công cụ %s báo lỗi: %s", name, clip(text, 300))
	}
	if text == "" {
		return "(công cụ không trả nội dung văn bản)", nil
	}
	return text, nil
}
