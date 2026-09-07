package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/benji/dnsguard/internal/classify"
	"github.com/benji/dnsguard/internal/ingest"
	"github.com/benji/dnsguard/internal/store"
	"github.com/benji/dnsguard/internal/worker"
)

// handleListDomains trả về một trang domain đã lọc.
func (s *Server) handleListDomains(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	filter := store.DomainFilter{
		Statuses:   splitCSV(q.Get("status")),
		Categories: splitCSV(q.Get("category")),
		Query:      q.Get("q"),
		SeenAfter:  q.Get("seen_after"),
		Cursor:     q.Get("cursor"),
		Limit:      atoiOr(q.Get("limit"), 50),
	}
	if raw := q.Get("min_score"); raw != "" {
		if v, err := strconv.ParseFloat(raw, 64); err == nil {
			filter.MinScore = &v
		}
	}
	filter.Sort, filter.Desc = parseSort(q.Get("sort"))

	items, next, hasMore, err := s.store.ListDomains(r.Context(), filter)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	if items == nil {
		items = []store.Domain{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items": items, "next_cursor": next, "has_more": hasMore,
	})
}

// handleGetDomain trả về toàn bộ dữ liệu của một domain cho trang chi tiết.
func (s *Server) handleGetDomain(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	ctx := r.Context()

	domain, err := s.store.GetDomain(ctx, id)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	facts, err := s.store.Facts(ctx, id)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	siblings, err := s.store.Siblings(ctx, id, 50)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	history, err := s.store.History(ctx, id, 50)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	lists, err := s.store.PublicListsFor(ctx, domain.Name)
	if err != nil {
		fail(w, s.log, err)
		return
	}

	soft, err := s.store.SoftAllowList(ctx)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	protectRule, protected := classify.IsProtected(domain.Name, soft)

	// Mọi trường dạng danh sách trả về [] thay vì null: hợp đồng API nói đây là mảng,
	// và một null lọt ra sẽ làm hỏng phía client ở chỗ khó lần ra nhất.
	writeJSON(w, http.StatusOK, map[string]any{
		"domain":       domain,
		"facts":        facts,
		"siblings":     orEmpty(siblings),
		"history":      orEmpty(history),
		"public_lists": orEmpty(lists),
		"protected":    protected,
		"protect_rule": protectRule,
	})
}

// handleDomainGraph trả về đồ thị quan hệ.
func (s *Server) handleDomainGraph(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	q := r.URL.Query()

	minStrength := 0.0
	if v, err := strconv.ParseFloat(q.Get("min_strength"), 64); err == nil {
		minStrength = v
	}

	g, err := s.store.GraphFor(r.Context(), id, splitCSV(q.Get("kinds")),
		minStrength, atoiOr(q.Get("limit"), 60))
	if err != nil {
		fail(w, s.log, err)
		return
	}
	if g.Edges == nil {
		g.Edges = []store.GraphEdge{}
	}
	writeJSON(w, http.StatusOK, g)
}

// handleDomainTimeline trả về chuỗi thời gian của một domain.
func (s *Server) handleDomainTimeline(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	from, to := timeRange(r)

	buckets, byClient, err := s.store.Timeline(r.Context(), id, from, to)
	if err != nil {
		fail(w, s.log, err)
		return
	}

	payload := map[string]any{"buckets": orEmpty(buckets)}
	// Chỉ quản trị thấy được phân bố theo client: query log chứa dữ liệu về hành vi
	// của từng người trong mạng, và vai trò viewer không được xem của người khác.
	if sess, ok := sessionFrom(r.Context()); ok && sess.Role == store.RoleAdmin {
		payload["by_client"] = orEmpty(byClient)
	}
	writeJSON(w, http.StatusOK, payload)
}

type decisionRequest struct {
	Action   string `json:"action"`
	Reason   string `json:"reason"`
	Category string `json:"category"`
}

// handleDecision thay đổi trạng thái một domain. Endpoint quan trọng nhất của hệ thống.
func (s *Server) handleDecision(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	sess, _ := sessionFrom(r.Context())

	var req decisionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidInput, "Dữ liệu không hợp lệ", nil)
		return
	}
	if !validAction(req.Action) {
		writeError(w, http.StatusBadRequest, CodeInvalidInput,
			"Hành động không hợp lệ", map[string]any{"action": req.Action})
		return
	}
	// Phân loại bắt buộc khi chặn hoặc đổi nhãn: danh sách xuất bản chia theo phân
	// loại, nên một domain bị chặn mà không có nhãn sẽ không nằm trong file nào cả.
	if (req.Action == store.ActionBlock || req.Action == store.ActionRecategorize) && req.Category == "" {
		writeError(w, http.StatusUnprocessableEntity, CodeValidation,
			"Cần chọn phân loại khi chặn hoặc đổi nhãn", nil)
		return
	}

	userID := sess.UserID
	domain, err := s.store.ApplyDecision(r.Context(), store.DecisionInput{
		DomainID: id, Action: req.Action, ActorID: &userID, ActorLabel: sess.Username,
		Reason: req.Reason, CategoryKey: req.Category, Manual: true,
	})
	if err != nil {
		fail(w, s.log, err)
		return
	}

	s.schedulePublish(r.Context())
	writeJSON(w, http.StatusOK, domain)
}

type bulkDecisionRequest struct {
	DomainIDs []int64 `json:"domain_ids"`
	Action    string  `json:"action"`
	Reason    string  `json:"reason"`
	Category  string  `json:"category"`
}

// handleBulkDecision áp dụng một hành động cho nhiều domain.
//
// Không phải all-or-nothing: áp dụng được cái nào thì áp dụng, báo lại cái nào bỏ
// qua. Một domain được bảo vệ nằm giữa danh sách không được làm hỏng cả thao tác.
func (s *Server) handleBulkDecision(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFrom(r.Context())

	var req bulkDecisionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidInput, "Dữ liệu không hợp lệ", nil)
		return
	}
	if !validAction(req.Action) {
		writeError(w, http.StatusBadRequest, CodeInvalidInput, "Hành động không hợp lệ", nil)
		return
	}
	if len(req.DomainIDs) > 500 {
		writeError(w, http.StatusBadRequest, CodeInvalidInput,
			"Tối đa 500 domain một lần", map[string]any{"count": len(req.DomainIDs)})
		return
	}

	userID := sess.UserID
	applied := 0
	skipped := []map[string]any{}

	for _, id := range req.DomainIDs {
		_, err := s.store.ApplyDecision(r.Context(), store.DecisionInput{
			DomainID: id, Action: req.Action, ActorID: &userID, ActorLabel: sess.Username,
			Reason: req.Reason, CategoryKey: req.Category, Manual: true,
		})
		if err != nil {
			skipped = append(skipped, map[string]any{
				"id": id, "code": codeFor(err), "message": err.Error(),
			})
			continue
		}
		applied++
	}

	if applied > 0 {
		s.schedulePublish(r.Context())
	}
	writeJSON(w, http.StatusOK, map[string]any{"applied": applied, "skipped": skipped})
}

type addDomainsRequest struct {
	Names    []string `json:"names"`
	Category string   `json:"category"`
	Reason   string   `json:"reason"`
	Status   string   `json:"status"`
	DryRun   bool     `json:"dry_run"`
	Force    bool     `json:"force"`
}

// handleAddDomains thêm domain thủ công, có chế độ xem trước.
func (s *Server) handleAddDomains(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFrom(r.Context())
	ctx := r.Context()

	var req addDomainsRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidInput, "Dữ liệu không hợp lệ", nil)
		return
	}
	if len(req.Names) > 5000 {
		writeError(w, http.StatusBadRequest, CodeInvalidInput, "Tối đa 5000 dòng một lần", nil)
		return
	}
	if req.Force && strings.TrimSpace(req.Reason) == "" {
		writeError(w, http.StatusUnprocessableEntity, CodeValidation,
			"Bắt buộc nêu lý do khi dùng force", nil)
		return
	}
	if req.Status == "" {
		req.Status = store.StatusBlocked
	}

	soft, err := s.store.SoftAllowList(ctx)
	if err != nil {
		fail(w, s.log, err)
		return
	}

	results := make([]map[string]any, 0, len(req.Names))
	summary := map[string]int{"created": 0, "duplicate": 0, "rejected": 0}
	seen := map[string]bool{}

	for _, raw := range req.Names {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" {
			continue
		}

		isWildcard := strings.HasPrefix(name, "*.")
		base := strings.TrimPrefix(name, "*.")

		if !validName(base) {
			results = append(results, reject(name, CodeInvalidDomain, "Tên miền không hợp lệ"))
			summary["rejected"]++
			continue
		}
		if seen[name] {
			results = append(results, map[string]any{"name": name, "action": "duplicate"})
			summary["duplicate"]++
			continue
		}
		seen[name] = true

		if rule, protected := classify.IsProtected(base, soft); protected {
			results = append(results, reject(name, CodeDomainProtected,
				"Nằm trong danh sách bảo vệ ("+rule+")"))
			summary["rejected"]++
			continue
		}

		// Cảnh báo khi domain có thứ hạng cao: chặn nhầm một tên miền phổ biến là
		// cách nhanh nhất làm mất niềm tin vào cả hệ thống.
		if rank, err := s.trancoRank(ctx, base); err == nil && rank > 0 && rank <= 50000 && !req.Force {
			results = append(results, map[string]any{
				"name": name, "action": "rejected", "code": CodeHighRank,
				"message": "Tranco #" + strconv.Itoa(rank) + ", cần xác nhận thêm",
			})
			summary["rejected"]++
			continue
		}

		existing, err := s.store.GetDomainByName(ctx, base)
		if err == nil && !isWildcard {
			results = append(results, map[string]any{
				"name": name, "action": "duplicate", "id": existing.ID,
			})
			summary["duplicate"]++
			continue
		}

		entry := map[string]any{"name": name, "action": "created"}
		if isWildcard {
			n, err := s.store.CountSubdomains(ctx, base)
			if err != nil {
				fail(w, s.log, err)
				return
			}
			entry["matches_known"] = n
		}

		if !req.DryRun {
			id, err := s.store.EnsureDomain(ctx, base, ingest.ETLD1(base), "manual")
			if err != nil {
				fail(w, s.log, err)
				return
			}
			if isWildcard {
				if _, err := s.store.Writer().ExecContext(ctx,
					`UPDATE domains SET is_wildcard = 1 WHERE id = ?`, id); err != nil {
					fail(w, s.log, err)
					return
				}
			}
			userID := sess.UserID
			if _, err := s.store.ApplyDecision(ctx, store.DecisionInput{
				DomainID: id, Action: actionFor(req.Status), ActorID: &userID,
				ActorLabel: sess.Username, Reason: req.Reason,
				CategoryKey: req.Category, Manual: true,
			}); err != nil {
				results = append(results, reject(name, codeFor(err), err.Error()))
				summary["rejected"]++
				continue
			}
			entry["id"] = id
		}

		results = append(results, entry)
		summary["created"]++
	}

	if !req.DryRun && summary["created"] > 0 {
		s.schedulePublish(ctx)
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results, "summary": summary})
}

// handleLookup là endpoint tra cứu cho vai trò viewer. Không lộ thông tin nội bộ.
func (s *Server) handleLookup(w http.ResponseWriter, r *http.Request) {
	name := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("name")))
	if !validName(strings.TrimPrefix(name, "*.")) {
		writeError(w, http.StatusBadRequest, CodeInvalidDomain, "Tên miền không hợp lệ", nil)
		return
	}

	domain, err := s.store.GetDomainByName(r.Context(), name)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"name": name, "blocked": false})
		return
	}

	payload := map[string]any{"name": name, "blocked": domain.Status == store.StatusBlocked}
	if domain.Category != nil {
		payload["category"] = domain.Category
	}
	writeJSON(w, http.StatusOK, payload)
}

// trancoRank đọc thứ hạng đã lưu của một domain.
func (s *Server) trancoRank(ctx context.Context, name string) (int, error) {
	d, err := s.store.GetDomainByName(ctx, name)
	if err != nil {
		return 0, err
	}
	facts, err := s.store.Facts(ctx, d.ID)
	if err != nil {
		return 0, err
	}
	raw, ok := facts["rank"]
	if !ok {
		return 0, nil
	}
	var v struct {
		Tranco int `json:"tranco"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0, nil
	}
	return v.Tranco, nil
}

// schedulePublish xếp hàng một lần xuất bản sau khi có quyết định thủ công.
//
// Quyết định của con người phải có hiệu lực nhanh: job chạy trong vòng vài giây thay
// vì đợi tới chu kỳ xuất bản định kỳ kế tiếp.
func (s *Server) schedulePublish(ctx context.Context) {
	if _, err := s.worker.Enqueue(ctx, worker.JobPublish, nil); err != nil {
		s.log.Error("xếp hàng xuất bản thất bại", "err", err)
	}
}

func reject(name, code, message string) map[string]any {
	return map[string]any{"name": name, "action": "rejected", "code": code, "message": message}
}

func validAction(action string) bool {
	switch action {
	case store.ActionBlock, store.ActionAllow, store.ActionIgnore, store.ActionRecategorize:
		return true
	}
	return false
}

func actionFor(status string) string {
	switch status {
	case store.StatusAllowed:
		return store.ActionAllow
	case store.StatusIgnored:
		return store.ActionIgnore
	default:
		return store.ActionBlock
	}
}

func pathID(w http.ResponseWriter, r *http.Request, key string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, key), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, CodeInvalidInput, "Mã không hợp lệ", nil)
		return 0, false
	}
	return id, true
}

// handleNetworkGraph trả về đồ thị quan hệ của toàn mạng.
//
// Khác endpoint đồ thị của một domain: ở đây không có nút gốc, câu hỏi là "mạng này
// có những cụm hạ tầng nào" chứ không phải "domain này liên quan tới gì".
func (s *Server) handleNetworkGraph(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	minStrength := 0.0
	if v, err := strconv.ParseFloat(q.Get("min_strength"), 64); err == nil {
		minStrength = v
	}

	g, err := s.store.Network(r.Context(), store.NetworkFilter{
		Kinds:       splitCSV(q.Get("kinds")),
		Statuses:    splitCSV(q.Get("status")),
		MinStrength: minStrength,
		MinDegree:   atoiOr(q.Get("min_degree"), 1),
		Limit:       atoiOr(q.Get("limit"), 300),
	})
	if err != nil {
		fail(w, s.log, err)
		return
	}

	g.Nodes = orEmpty(g.Nodes)
	g.Edges = orEmpty(g.Edges)
	writeJSON(w, http.StatusOK, g)
}
