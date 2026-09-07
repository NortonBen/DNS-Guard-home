package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/benji/dnsguard/internal/classify"
	"github.com/benji/dnsguard/internal/store"
	"github.com/benji/dnsguard/internal/worker"
)

// handleListSources trả về các nguồn blocklist công khai.
func (s *Server) handleListSources(w http.ResponseWriter, r *http.Request) {
	sources, err := s.store.ListSources(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": orEmpty(sources)})
}

type sourceRequest struct {
	Name         string `json:"name"`
	URL          string `json:"url"`
	Format       string `json:"format"`
	Category     string `json:"category"`
	Enabled      *bool  `json:"enabled"`
	SyncInterval int    `json:"sync_interval"`
}

func (req sourceRequest) toStore() store.ListSource {
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	format := req.Format
	if format == "" {
		format = "hosts"
	}
	return store.ListSource{
		Name: req.Name, URL: req.URL, Format: format, CategoryKey: req.Category,
		Enabled: enabled, SyncInterval: req.SyncInterval,
	}
}

// handleCreateSource đăng ký một nguồn mới.
func (s *Server) handleCreateSource(w http.ResponseWriter, r *http.Request) {
	var req sourceRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidInput, "Dữ liệu không hợp lệ", nil)
		return
	}
	if !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
		writeError(w, http.StatusUnprocessableEntity, CodeValidation,
			"URL phải bắt đầu bằng http:// hoặc https://", nil)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusUnprocessableEntity, CodeValidation, "Cần đặt tên cho nguồn", nil)
		return
	}

	src, err := s.store.CreateSource(r.Context(), req.toStore())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	// Đồng bộ ngay để người dùng thấy kết quả thay vì đợi chu kỳ kế tiếp.
	if _, err := s.worker.Enqueue(r.Context(), worker.JobCatalog,
		map[string]any{"source_id": src.ID}); err != nil {
		s.log.Error("xếp hàng đồng bộ thất bại", "err", err)
	}
	writeJSON(w, http.StatusCreated, src)
}

// handleUpdateSource sửa một nguồn.
func (s *Server) handleUpdateSource(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	current, err := s.store.GetSource(r.Context(), id)
	if err != nil {
		fail(w, s.log, err)
		return
	}

	req := sourceRequest{
		Name: current.Name, URL: current.URL, Format: current.Format,
		Category: current.CategoryKey, Enabled: &current.Enabled,
		SyncInterval: current.SyncInterval,
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidInput, "Dữ liệu không hợp lệ", nil)
		return
	}

	src, err := s.store.UpdateSource(r.Context(), id, req.toStore())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, src)
}

// handleDeleteSource xóa một nguồn.
func (s *Server) handleDeleteSource(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.store.DeleteSource(r.Context(), id); err != nil {
		fail(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSyncSource đồng bộ một nguồn ngay.
func (s *Server) handleSyncSource(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	jobID, err := s.worker.Enqueue(r.Context(), worker.JobCatalog, map[string]any{"source_id": id})
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": jobID})
}

// handleSourceOverlap phân tích chồng lấn giữa các nguồn.
func (s *Server) handleSourceOverlap(w http.ResponseWriter, r *http.Request) {
	pairs, unique, err := s.store.Overlap(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	sources, err := s.store.ListSources(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sources": orEmpty(sources), "pairs": orEmpty(pairs), "unique": orEmpty(unique),
	})
}

// handleListSnapshots trả về lịch sử xuất bản.
func (s *Server) handleListSnapshots(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, err := s.store.ListSnapshots(r.Context(), q.Get("category"), atoiOr(q.Get("limit"), 30))
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": orEmpty(items)})
}

// handleDiffSnapshots so sánh hai bản xuất bản.
func (s *Server) handleDiffSnapshots(w http.ResponseWriter, r *http.Request) {
	a, err1 := strconv.ParseInt(chi.URLParam(r, "a"), 10, 64)
	b, err2 := strconv.ParseInt(chi.URLParam(r, "b"), 10, 64)
	if err1 != nil || err2 != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidInput, "Mã snapshot không hợp lệ", nil)
		return
	}

	added, removed, err := s.store.DiffSnapshots(r.Context(), a, b)
	if err != nil {
		fail(w, s.log, err)
		return
	}

	if r.URL.Query().Get("full") == "true" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		for _, d := range added {
			w.Write([]byte("+ " + d + "\n"))
		}
		for _, d := range removed {
			w.Write([]byte("- " + d + "\n"))
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"added":   map[string]any{"count": len(added), "sample": sample(added, 100)},
		"removed": map[string]any{"count": len(removed), "sample": sample(removed, 100)},
	})
}

type publishRequest struct {
	Categories []string `json:"categories"`
}

// handlePublish xuất bản ngay, không đợi lịch.
func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFrom(r.Context())

	var req publishRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, CodeInvalidInput, "Dữ liệu không hợp lệ", nil)
			return
		}
	}

	jobID, err := s.worker.Enqueue(r.Context(), worker.JobPublish, map[string]any{
		"categories": req.Categories, "actor": sess.Username,
	})
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": jobID})
}

// handleRollback ghi lại danh sách từ một bản xuất bản cũ.
func (s *Server) handleRollback(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	sess, _ := sessionFrom(r.Context())

	result, err := s.publisher.Rollback(r.Context(), id, sess.Username)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type unblockRequest struct {
	Domain string `json:"domain"`
	Note   string `json:"note"`
}

// handleCreateUnblockRequest nhận yêu cầu mở chặn từ người dùng trong mạng.
func (s *Server) handleCreateUnblockRequest(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFrom(r.Context())

	var req unblockRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidInput, "Dữ liệu không hợp lệ", nil)
		return
	}
	if !validName(strings.ToLower(strings.TrimSpace(req.Domain))) {
		writeError(w, http.StatusBadRequest, CodeInvalidDomain, "Tên miền không hợp lệ", nil)
		return
	}

	_, err := s.store.Writer().ExecContext(r.Context(), `
		INSERT INTO unblock_requests (domain, note, requested_by, created_at)
		VALUES (?, ?, ?, ?)`,
		strings.ToLower(strings.TrimSpace(req.Domain)), req.Note, sess.Username, store.Now())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"status": "pending"})
}

// handleListUnblockRequests trả về các yêu cầu đang chờ.
func (s *Server) handleListUnblockRequests(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.Reader().QueryContext(r.Context(), `
		SELECT id, domain, note, requested_by, state, created_at
		FROM unblock_requests WHERE state = 'pending'
		ORDER BY created_at DESC LIMIT 200`)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	defer rows.Close()

	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var domain, note, by, state, createdAt string
		if err := rows.Scan(&id, &domain, &note, &by, &state, &createdAt); err != nil {
			fail(w, s.log, err)
			return
		}
		items = append(items, map[string]any{
			"id": id, "domain": domain, "note": note,
			"requested_by": by, "state": state, "created_at": createdAt,
		})
	}
	if err := rows.Err(); err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

type resolveRequest struct {
	Accept bool   `json:"accept"`
	Reason string `json:"reason"`
}

// handleResolveUnblockRequest xử lý một yêu cầu mở chặn.
func (s *Server) handleResolveUnblockRequest(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	sess, _ := sessionFrom(r.Context())
	ctx := r.Context()

	var req resolveRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidInput, "Dữ liệu không hợp lệ", nil)
		return
	}

	var domain string
	if err := s.store.Reader().QueryRowContext(ctx,
		`SELECT domain FROM unblock_requests WHERE id = ?`, id).Scan(&domain); err != nil {
		fail(w, s.log, store.ErrNotFound)
		return
	}

	state := "rejected"
	if req.Accept {
		state = "accepted"
		d, err := s.store.GetDomainByName(ctx, domain)
		if err != nil {
			fail(w, s.log, err)
			return
		}
		userID := sess.UserID
		if _, err := s.store.ApplyDecision(ctx, store.DecisionInput{
			DomainID: d.ID, Action: store.ActionAllow, ActorID: &userID,
			ActorLabel: sess.Username, Reason: req.Reason, Manual: true,
		}); err != nil {
			fail(w, s.log, err)
			return
		}
		s.schedulePublish(ctx)
	}

	if _, err := s.store.Writer().ExecContext(ctx,
		`UPDATE unblock_requests SET state = ?, resolved_at = ? WHERE id = ?`,
		state, store.Now(), id); err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": state})
}

// handleGetProtect trả về danh sách bảo vệ cứng và mềm.
func (s *Server) handleGetProtect(w http.ResponseWriter, r *http.Request) {
	soft, err := s.store.SoftAllowList(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"hard": classify.HardAllowList(),
		"soft": orEmpty(soft),
	})
}

type protectRequest struct {
	Soft []string `json:"soft"`
}

// handleUpdateProtect sửa danh sách bảo vệ mềm. Danh sách cứng nằm trong code và
// chỉ đổi được qua pull request.
func (s *Server) handleUpdateProtect(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFrom(r.Context())

	var req protectRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidInput, "Dữ liệu không hợp lệ", nil)
		return
	}

	cleaned := make([]string, 0, len(req.Soft))
	for _, raw := range req.Soft {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" {
			continue
		}
		if !validName(name) {
			writeError(w, http.StatusUnprocessableEntity, CodeInvalidDomain,
				"Tên miền không hợp lệ", map[string]any{"domain": name})
			return
		}
		cleaned = append(cleaned, name)
	}

	if err := s.store.SetSetting(r.Context(), store.SettingSoftAllow, cleaned, sess.Username); err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"soft": cleaned})
}

func sample[T any](items []T, n int) []T {
	if len(items) > n {
		return items[:n]
	}
	return orEmpty(items)
}
