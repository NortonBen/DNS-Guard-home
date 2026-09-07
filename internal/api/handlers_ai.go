package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/benji/dnsguard/internal/ai"
	"github.com/benji/dnsguard/internal/llm"
	"github.com/benji/dnsguard/internal/store"
	"github.com/benji/dnsguard/internal/worker"
)

// maxQuestionBytes chặn một câu hỏi khổng lồ được dùng làm đòn bẩy tiêu hạn mức.
const maxQuestionBytes = 8 << 10

// aiReady kiểm tra tính năng AI đã dựng xong chưa. Trả về false khi đã ghi lỗi.
//
// Cả cụm AI là tuỳ chọn: chưa cấu hình gì thì các endpoint dưới đây trả 503 kèm
// thông điệp nói rõ phải làm gì, thay vì 500 hoặc panic vì con trỏ rỗng.
func (s *Server) aiReady(w http.ResponseWriter) bool {
	if s.aiStore == nil || s.llm == nil {
		writeError(w, http.StatusServiceUnavailable, CodeInternal,
			"Chưa bật tính năng AI trên máy chủ này", nil)
		return false
	}
	return true
}

// handleAIStatus mô tả cấu hình đang chạy. Không bao giờ trả khóa API.
func (s *Server) handleAIStatus(w http.ResponseWriter, r *http.Request) {
	if s.llm == nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false})
		return
	}

	cfg := s.llm.Config()
	payload := map[string]any{
		"available":  true,
		"enabled":    cfg.HasAPIKey && s.enrichers != nil && s.enrichers.IsEnabled(ai.SourceName),
		"configured": cfg.HasAPIKey,
		"base_url":   cfg.BaseURL,
		"model":      cfg.Model,
		"max_tokens": cfg.MaxTokens,
		"batch_size": s.cfg.AIBatchSize,
		// Công tắc cứng ở biến môi trường: bật trên giao diện cũng không thắng được nó.
		"external_enabled": s.cfg.ExternalEnabled,
	}
	if s.classifier != nil {
		payload["batch_size"] = s.classifier.BatchSize()
	}
	if s.aiStore != nil {
		payload["db_path"] = s.aiStore.Path()
		if usage, err := s.aiStore.Usage(r.Context(), 30); err == nil {
			payload["usage_30d"] = usage
		}
	}
	writeJSON(w, http.StatusOK, payload)
}

type aiSettingsRequest struct {
	BaseURL *string `json:"base_url"`
	Model   *string `json:"model"`
	// APIKey: con trỏ để phân biệt "không đụng tới" với "xóa đi". Chuỗi rỗng là
	// lệnh gỡ khóa, còn trường vắng mặt thì giữ nguyên khóa cũ — giao diện không
	// đọc lại được khóa nên không thể gửi lại nó.
	APIKey    *string `json:"api_key"`
	BatchSize *int    `json:"batch_size"`
	Enabled   *bool   `json:"enabled"`
}

// handleUpdateAISettings lưu cấu hình nhà cung cấp và áp ngay lúc chạy.
func (s *Server) handleUpdateAISettings(w http.ResponseWriter, r *http.Request) {
	if !s.aiReady(w) {
		return
	}
	sess, _ := sessionFrom(r.Context())
	ctx := r.Context()

	var req aiSettingsRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidInput, "Dữ liệu không hợp lệ", nil)
		return
	}

	if req.BaseURL != nil {
		url := strings.TrimSpace(*req.BaseURL)
		if url != "" && !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			writeError(w, http.StatusUnprocessableEntity, CodeValidation,
				"Base URL phải bắt đầu bằng http:// hoặc https://", nil)
			return
		}
		if err := s.store.SetSetting(ctx, store.SettingAIBaseURL, url, sess.Username); err != nil {
			fail(w, s.log, err)
			return
		}
	}
	if req.Model != nil {
		if err := s.store.SetSetting(ctx, store.SettingAIModel,
			strings.TrimSpace(*req.Model), sess.Username); err != nil {
			fail(w, s.log, err)
			return
		}
	}
	if req.APIKey != nil {
		// Khóa không bao giờ đi vào log hay phản hồi. Nơi duy nhất nó tồn tại là
		// bảng settings và bộ nhớ của tiến trình.
		if err := s.store.SetSetting(ctx, store.SettingAIAPIKey,
			strings.TrimSpace(*req.APIKey), sess.Username); err != nil {
			fail(w, s.log, err)
			return
		}
	}
	if req.BatchSize != nil {
		if *req.BatchSize < ai.MinBatchSize || *req.BatchSize > ai.MaxBatchSize {
			writeError(w, http.StatusUnprocessableEntity, CodeValidation,
				fmt.Sprintf("Kích thước lô phải trong [%d,%d]", ai.MinBatchSize, ai.MaxBatchSize), nil)
			return
		}
		if err := s.store.SetSetting(ctx, store.SettingAIBatchSize,
			*req.BatchSize, sess.Username); err != nil {
			fail(w, s.log, err)
			return
		}
		if s.classifier != nil {
			s.classifier.SetBatchSize(*req.BatchSize)
		}
	}
	if req.Enabled != nil {
		if *req.Enabled && !s.cfg.ExternalEnabled {
			writeError(w, http.StatusUnprocessableEntity, CodeValidation,
				"Không bật được khi DNSGUARD_EXTERNAL_ENABLED=false", nil)
			return
		}
		if err := s.store.SetSetting(ctx, store.SettingAIEnabled,
			*req.Enabled, sess.Username); err != nil {
			fail(w, s.log, err)
			return
		}
	}

	// Áp toàn bộ cấu hình đã lưu vào client đang chạy, không bắt khởi động lại.
	if err := s.applyAISettings(ctx); err != nil {
		fail(w, s.log, err)
		return
	}
	s.log.Info("đổi cấu hình AI", "by", sess.Username)

	s.handleAIStatus(w, r)
}

// applyAISettings đọc cấu hình đã lưu và áp vào client cùng registry.
//
// Đọc lại toàn bộ thay vì áp từng trường vừa sửa: llm.Configure nhận cả bộ giá trị
// một lượt, và ghép từng phần sẽ sinh ra trạng thái nửa vời khi hai trường được
// sửa trong hai request liên tiếp.
func (s *Server) applyAISettings(ctx context.Context) error {
	return ai.ApplySettings(ctx, s.store, ai.Defaults{
		BaseURL: s.cfg.AIBaseURL, APIKey: s.cfg.AIAPIKey, Model: s.cfg.AIModel,
		MaxTokens: s.cfg.AIMaxTokens, BatchSize: s.cfg.AIBatchSize,
		ExternalEnabled: s.cfg.ExternalEnabled,
	}, s.llm, s.classifier, s.enrichers, s.log)
}

// handleAITools liệt kê công cụ model gọi được, kèm cảnh báo từ máy chủ MCP hỏng.
func (s *Server) handleAITools(w http.ResponseWriter, r *http.Request) {
	if !s.aiReady(w) {
		return
	}
	reg, warnings := ai.BuildRegistry(r.Context(), s.store, s.aiStore, s.aiHost(r))
	writeJSON(w, http.StatusOK, map[string]any{
		"tools":    reg.Describe(),
		"warnings": orEmpty(warnings),
	})
}

// aiHost trả về cầu nối các thao tác ghi, hoặc nil cho vai trò không phải quản trị.
//
// Trả nil chính là chốt phân quyền: gói ai chỉ đăng ký công cụ ghi khi host khác
// nil, nên hỏi đáp của viewer không mở được đường vòng qua chốt của REST.
func (s *Server) aiHost(r *http.Request) ai.Host {
	sess, ok := sessionFrom(r.Context())
	if !ok || sess.Role != store.RoleAdmin || s.worker == nil {
		return nil
	}
	return &workerHost{worker: s.worker}
}

// workerHost nối công cụ hỏi lại với hàng đợi job.
type workerHost struct{ worker *worker.Runner }

func (h *workerHost) Recheck(ctx context.Context, domain string) (string, error) {
	return h.worker.Enqueue(ctx, worker.JobAIClassify,
		map[string]any{"domains": []string{domain}})
}

type askRequest struct {
	Question string `json:"question"`
	// ChatID nối tiếp một cuộc hỏi đáp đã có. 0 nghĩa là mở cuộc mới.
	ChatID int64 `json:"chat_id"`
	// Domain gắn câu hỏi vào một domain cụ thể, dùng cho nút "hỏi AI" ở màn chi tiết.
	Domain string `json:"domain"`
}

// handleAIAsk trả lời một câu hỏi, có dùng công cụ để tự tra dữ liệu.
//
// Trả JSON một lần chứ không stream: câu trả lời ở đây đi kèm dấu vết gọi công cụ,
// và một luồng SSE phải mã hóa hai loại sự kiện đan xen để nói cùng chừng đó thông
// tin. Vòng lặp công cụ vốn đã không stream được phần tra cứu, nên cái giá thật là
// vài giây chờ chứ không phải trải nghiệm khác hẳn.
func (s *Server) handleAIAsk(w http.ResponseWriter, r *http.Request) {
	if !s.aiReady(w) {
		return
	}
	sess, _ := sessionFrom(r.Context())
	ctx := r.Context()

	var req askRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxQuestionBytes)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidInput, "Dữ liệu không hợp lệ", nil)
		return
	}
	req.Question = strings.TrimSpace(req.Question)
	if req.Question == "" {
		writeError(w, http.StatusBadRequest, CodeInvalidInput, "Câu hỏi trống", nil)
		return
	}
	if !s.llm.Enabled() {
		writeError(w, http.StatusServiceUnavailable, CodeValidation,
			"Chưa cấu hình khóa API. Vào Cài đặt để nhập khóa.", nil)
		return
	}

	chatID, history, err := s.resumeChat(ctx, req, sess.Username)
	if err != nil {
		fail(w, s.log, err)
		return
	}

	messages, skills, err := s.buildAskMessages(ctx, req, history)
	if err != nil {
		fail(w, s.log, err)
		return
	}

	reg, warnings := ai.BuildRegistry(ctx, s.store, s.aiStore, s.aiHost(r))
	answer, err := ai.Ask(ctx, s.llm, reg, messages, nil)
	if err != nil {
		if errors.Is(err, ai.ErrNoAPIKey) || errors.Is(err, llm.ErrDisabled) {
			writeError(w, http.StatusServiceUnavailable, CodeValidation, err.Error(), nil)
			return
		}
		writeError(w, http.StatusBadGateway, CodeInternal,
			"Model không trả lời được: "+err.Error(), nil)
		return
	}
	answer.Skills = skills

	// Ghi nhật ký sau khi đã có câu trả lời. Lỗi ghi không được nuốt mất câu trả
	// lời — người dùng đã chờ và đã tiêu hạn mức cho nó rồi.
	s.recordAsk(ctx, chatID, req.Question, answer, sess.Username)

	writeJSON(w, http.StatusOK, map[string]any{
		"chat_id":  chatID,
		"answer":   answer.Content,
		"steps":    answer.Steps,
		"skills":   orEmpty(answer.Skills),
		"warnings": orEmpty(warnings),
	})
}

// resumeChat mở cuộc hỏi đáp mới hoặc nối tiếp cuộc đã có.
func (s *Server) resumeChat(ctx context.Context, req askRequest, actor string) (int64, []ai.ChatMessage, error) {
	if req.ChatID <= 0 {
		id, err := s.aiStore.CreateChat(ctx, req.Question, actor)
		return id, nil, err
	}
	history, err := s.aiStore.Messages(ctx, req.ChatID)
	if err != nil {
		return 0, nil, err
	}
	return req.ChatID, history, nil
}

// askHistoryTurns là số lượt cũ gửi lại cho model.
//
// Sáu lượt là ba cặp hỏi–đáp: đủ để "còn domain kia thì sao" hiểu được, và đủ
// ngắn để một cuộc hỏi đáp dài không biến mỗi câu hỏi mới thành một hoá đơn lớn
// hơn câu trước.
const askHistoryTurns = 6

// buildAskMessages dựng hội thoại gửi cho model: vai, quy ước, lịch sử, câu hỏi.
func (s *Server) buildAskMessages(ctx context.Context, req askRequest,
	history []ai.ChatMessage) ([]llm.Message, []string, error) {

	messages := []llm.Message{{Role: llm.RoleSystem, Content: ai.AskSystemPrompt}}

	// Skill khớp theo câu hỏi: chỉ quy ước liên quan mới được chèn, thay vì nhồi
	// mọi quy ước vào mọi lượt.
	var names []string
	all, err := s.aiStore.ListSkills(ctx)
	if err != nil {
		return nil, nil, err
	}
	if matched := ai.MatchSkills(all, req.Question); len(matched) > 0 {
		messages = append(messages, llm.Message{
			Role: llm.RoleSystem, Content: ai.RenderSkills(matched, ai.MaxInjectedChars),
		})
		names = ai.SkillNames(matched)
	}

	if start := len(history) - askHistoryTurns; start > 0 {
		history = history[start:]
	}
	for _, m := range history {
		messages = append(messages, llm.Message{Role: m.Role, Content: m.Content})
	}

	question := req.Question
	if domain := strings.TrimSpace(req.Domain); domain != "" {
		question = "Về domain " + domain + ": " + question
	}
	messages = append(messages, llm.Message{Role: llm.RoleUser, Content: question})
	return messages, names, nil
}

// recordAsk ghi lượt hỏi đáp vào nhật ký. Lỗi chỉ vào log: câu trả lời đã gửi đi.
func (s *Server) recordAsk(ctx context.Context, chatID int64, question string,
	answer ai.Answer, actor string) {

	if err := s.aiStore.AddMessage(ctx, chatID, llm.RoleUser, question, nil); err != nil {
		s.log.Error("ghi câu hỏi thất bại", "err", err)
	}
	if err := s.aiStore.AddMessage(ctx, chatID, llm.RoleAssistant,
		answer.Content, answer.Steps); err != nil {
		s.log.Error("ghi câu trả lời thất bại", "err", err)
	}

	cfg := s.llm.Config()
	if _, err := s.aiStore.SaveRequest(ctx, ai.Request{
		Kind: ai.KindAsk, Model: cfg.Model, BaseURL: cfg.BaseURL,
		Prompt: question, Response: answer.Content, Actor: actor,
	}, nil); err != nil {
		s.log.Error("ghi nhật ký hỏi đáp thất bại", "err", err)
	}
}

// handleAIChats liệt kê các cuộc hỏi đáp gần nhất.
func (s *Server) handleAIChats(w http.ResponseWriter, r *http.Request) {
	if !s.aiReady(w) {
		return
	}
	chats, err := s.aiStore.ListChats(r.Context(), atoiOr(r.URL.Query().Get("limit"), 30))
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": orEmpty(chats)})
}

// handleAIChat trả về toàn bộ lượt của một cuộc hỏi đáp.
func (s *Server) handleAIChat(w http.ResponseWriter, r *http.Request) {
	if !s.aiReady(w) {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	messages, err := s.aiStore.Messages(r.Context(), id)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "messages": orEmpty(messages)})
}

// handleDeleteAIChat xoá một cuộc hỏi đáp cùng các lượt của nó.
func (s *Server) handleDeleteAIChat(w http.ResponseWriter, r *http.Request) {
	if !s.aiReady(w) {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.aiStore.DeleteChat(r.Context(), id); err != nil {
		fail(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAIHistory liệt kê lịch sử lượt gọi model.
func (s *Server) handleAIHistory(w http.ResponseWriter, r *http.Request) {
	if !s.aiReady(w) {
		return
	}
	ctx := r.Context()
	q := r.URL.Query()
	kind := q.Get("kind")
	limit := atoiOr(q.Get("limit"), 50)
	offset := atoiOr(q.Get("offset"), 0)

	items, err := s.aiStore.ListRequests(ctx, kind, limit, offset)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	total, err := s.aiStore.CountRequests(ctx, kind)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": orEmpty(items), "total": total, "offset": offset, "limit": limit,
	})
}

// handleAIRequest trả về một lượt gọi đầy đủ: prompt, phản hồi thô, các kết luận.
//
// Prompt và phản hồi thô là thứ duy nhất trả lời được câu "vì sao AI kết luận như
// vậy". Không có chúng, lịch sử chỉ là một dãy con số.
func (s *Server) handleAIRequest(w http.ResponseWriter, r *http.Request) {
	if !s.aiReady(w) {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	req, verdicts, err := s.aiStore.GetRequest(r.Context(), id)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"request": req, "verdicts": orEmpty(verdicts),
	})
}

// handleAIVerdicts trả về lịch sử kết luận của một domain.
func (s *Server) handleAIVerdicts(w http.ResponseWriter, r *http.Request) {
	if !s.aiReady(w) {
		return
	}
	domain := strings.TrimSpace(r.URL.Query().Get("domain"))
	if domain == "" {
		writeError(w, http.StatusBadRequest, CodeInvalidInput, "Thiếu tham số domain", nil)
		return
	}
	verdicts, err := s.aiStore.VerdictsFor(r.Context(), domain,
		atoiOr(r.URL.Query().Get("limit"), 20))
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": orEmpty(verdicts)})
}

// handleAIClassify xếp hàng một lượt hỏi model cho các domain tới hạn.
func (s *Server) handleAIClassify(w http.ResponseWriter, r *http.Request) {
	if !s.aiReady(w) {
		return
	}
	jobID, err := s.worker.Enqueue(r.Context(), worker.JobAIClassify, nil)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": jobID})
}

// handleAIRecheck xếp hàng hỏi lại model về đúng một domain.
//
// Xếp hàng chứ không gọi thẳng: một lượt gọi model mất vài giây tới vài chục giây,
// và giữ kết nối HTTP suốt thời gian đó sẽ hết hạn ở proxy. Giao diện theo dõi job
// bằng đúng cơ chế đã có cho các job khác.
func (s *Server) handleAIRecheck(w http.ResponseWriter, r *http.Request) {
	if !s.aiReady(w) {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	domain, err := s.store.GetDomain(r.Context(), id)
	if err != nil {
		fail(w, s.log, err)
		return
	}

	jobID, err := s.worker.Enqueue(r.Context(), worker.JobAIClassify,
		map[string]any{"domains": []string{domain.Name}})
	if err != nil {
		fail(w, s.log, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.log.Info("hỏi lại AI về domain", "domain", domain.Name, "by", sess.Username)

	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": jobID, "domain": domain.Name})
}

// ---- skill ----

func (s *Server) handleListAISkills(w http.ResponseWriter, r *http.Request) {
	if !s.aiReady(w) {
		return
	}
	skills, err := s.aiStore.ListSkills(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": orEmpty(skills)})
}

func (s *Server) handleSaveAISkill(w http.ResponseWriter, r *http.Request) {
	if !s.aiReady(w) {
		return
	}
	var skill ai.Skill
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&skill); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidInput, "Dữ liệu không hợp lệ", nil)
		return
	}
	if strings.TrimSpace(skill.Name) == "" {
		writeError(w, http.StatusUnprocessableEntity, CodeValidation, "Skill phải có tên", nil)
		return
	}

	id, err := s.aiStore.SaveSkill(r.Context(), skill)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) handleDeleteAISkill(w http.ResponseWriter, r *http.Request) {
	if !s.aiReady(w) {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.aiStore.DeleteSkill(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(w, s.log, err)
			return
		}
		writeError(w, http.StatusUnprocessableEntity, CodeValidation, err.Error(), nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- máy chủ MCP ----

func (s *Server) handleListMCPServers(w http.ResponseWriter, r *http.Request) {
	if !s.aiReady(w) {
		return
	}
	servers, err := s.aiStore.ListMCPServers(r.Context(), false)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": orEmpty(servers)})
}

// mcpServerRequest tách khỏi ai.MCPServer vì auth_header có tag json:"-" ở đó —
// đúng cho đường ra, nhưng đường vào vẫn phải nhận được token người dùng nhập.
type mcpServerRequest struct {
	Name       string `json:"name"`
	URL        string `json:"url"`
	AuthHeader string `json:"auth_header"`
	Enabled    bool   `json:"enabled"`
	Note       string `json:"note"`
}

func (s *Server) handleSaveMCPServer(w http.ResponseWriter, r *http.Request) {
	if !s.aiReady(w) {
		return
	}
	var req mcpServerRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidInput, "Dữ liệu không hợp lệ", nil)
		return
	}

	id, err := s.aiStore.SaveMCPServer(r.Context(), ai.MCPServer{
		Name: req.Name, URL: req.URL, AuthHeader: strings.TrimSpace(req.AuthHeader),
		Enabled: req.Enabled, Note: req.Note,
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, CodeValidation, err.Error(), nil)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.log.Info("lưu máy chủ MCP", "name", req.Name, "by", sess.Username)

	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) handleDeleteMCPServer(w http.ResponseWriter, r *http.Request) {
	if !s.aiReady(w) {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.aiStore.DeleteMCPServer(r.Context(), id); err != nil {
		fail(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
