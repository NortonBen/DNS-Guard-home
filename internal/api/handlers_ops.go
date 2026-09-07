package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/benji/dnsguard/internal/classify"
	"github.com/benji/dnsguard/internal/store"
	"github.com/benji/dnsguard/internal/worker"
)

// handleListCategories trả về taxonomy kèm số domain mỗi loại.
func (s *Server) handleListCategories(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.Reader().QueryContext(r.Context(), `
		SELECT c.id, c.key, c.label_vi, c.label_en, c.description, c.color, c.enabled,
		       c.score_threshold, c.publish_path, c.sort_order,
		       (SELECT count(*) FROM domains d WHERE d.category_id = c.id) AS domain_count
		FROM categories c ORDER BY c.sort_order LIMIT 50`)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	defer rows.Close()

	items := []map[string]any{}
	for rows.Next() {
		var (
			id, sortOrder, domainCount               int64
			key, labelVi, labelEn, desc, color, path string
			enabled                                  bool
			threshold                                float64
		)
		if err := rows.Scan(&id, &key, &labelVi, &labelEn, &desc, &color, &enabled,
			&threshold, &path, &sortOrder, &domainCount); err != nil {
			fail(w, s.log, err)
			return
		}
		items = append(items, map[string]any{
			"id": id, "key": key, "label_vi": labelVi, "label_en": labelEn,
			"description": desc, "color": color, "enabled": enabled,
			"score_threshold": threshold, "publish_path": path,
			"sort_order": sortOrder, "domain_count": domainCount,
		})
	}
	if err := rows.Err(); err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

type updateCategoryRequest struct {
	Enabled        *bool    `json:"enabled"`
	ScoreThreshold *float64 `json:"score_threshold"`
	LabelVi        *string  `json:"label_vi"`
	Color          *string  `json:"color"`
}

// handleUpdateCategory sửa ngưỡng, nhãn và trạng thái bật/tắt của một phân loại.
func (s *Server) handleUpdateCategory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req updateCategoryRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidInput, "Dữ liệu không hợp lệ", nil)
		return
	}

	// key không sửa được: nó là khóa mà cả code lẫn API dùng để định danh phân loại.
	_, err := s.store.Writer().ExecContext(r.Context(), `
		UPDATE categories SET
		  enabled         = coalesce(?, enabled),
		  score_threshold = coalesce(?, score_threshold),
		  label_vi        = coalesce(?, label_vi),
		  color           = coalesce(?, color)
		WHERE id = ?`, req.Enabled, req.ScoreThreshold, req.LabelVi, req.Color, id)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	s.handleListCategories(w, r)
}

// handleGetWeights trả về bảng trọng số hiện dùng.
func (s *Server) handleGetWeights(w http.ResponseWriter, r *http.Request) {
	weights, err := s.currentWeights(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}

	items := make([]map[string]any, 0, len(classify.DefaultWeights))
	for kind, def := range classify.DefaultWeights {
		items = append(items, map[string]any{
			"kind": kind, "weight": weights.Get(kind), "default": def,
			"label_vi": classify.SignalLabels[kind],
			"is_infra": classify.IsInfraKind(kind),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"weights": items})
}

type updateWeightsRequest struct {
	Weights []struct {
		Kind   string  `json:"kind"`
		Weight float64 `json:"weight"`
	} `json:"weights"`
	DryRun bool `json:"dry_run"`
}

// handleUpdateWeights sửa trọng số, có chế độ tính trước tác động.
//
// Giao diện bắt buộc phải chạy dry_run trước khi lưu. Đổi trọng số mà không xem tác
// động là cách nhanh nhất để chặn nhầm hàng loạt, và vì Score là hàm thuần túy nên
// con số xem trước khớp chính xác với kết quả sau khi áp dụng.
func (s *Server) handleUpdateWeights(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFrom(r.Context())
	ctx := r.Context()

	var req updateWeightsRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidInput, "Dữ liệu không hợp lệ", nil)
		return
	}

	current, err := s.currentWeights(ctx)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	proposed := classify.Weights{}
	for kind, v := range current {
		proposed[kind] = v
	}
	for _, item := range req.Weights {
		if _, known := classify.DefaultWeights[item.Kind]; !known {
			writeError(w, http.StatusBadRequest, CodeInvalidInput,
				"Loại tín hiệu không rõ", map[string]any{"kind": item.Kind})
			return
		}
		proposed[item.Kind] = item.Weight
	}

	rules, err := s.currentRules(ctx)
	if err != nil {
		fail(w, s.log, err)
		return
	}

	if req.DryRun {
		impact, err := s.simulate(ctx,
			scoringConfig{weights: current, rules: rules},
			scoringConfig{weights: proposed, rules: rules})
		if err != nil {
			fail(w, s.log, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"impact": impact})
		return
	}

	if err := s.store.SetSetting(ctx, store.SettingWeights, proposed, sess.Username); err != nil {
		fail(w, s.log, err)
		return
	}
	jobID, err := s.worker.Enqueue(ctx, worker.JobRescore, nil)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": jobID})
}

// scoringConfig là một cấu hình chấm điểm hoàn chỉnh: trọng số cộng bộ luật.
//
// Gộp hai thứ lại vì chúng vào cùng một hàm và luôn đi cùng nhau khi so sánh trước
// với sau.
type scoringConfig struct {
	weights classify.Weights
	rules   classify.Rules
}

// simulate tính tác động của một cấu hình chấm điểm mới mà không ghi gì.
//
// Chính xác tuyệt đối chứ không phải ước lượng: ScoreWith là hàm thuần túy, nên chạy
// nó với cấu hình sắp lưu cho đúng kết quả sẽ xảy ra sau khi lưu.
func (s *Server) simulate(ctx context.Context, current, proposed scoringConfig) (map[string]any, error) {
	candidates, err := s.store.ScoringCandidates(ctx, 50000)
	if err != nil {
		return nil, err
	}

	thresholds, err := s.thresholds(ctx)
	if err != nil {
		return nil, err
	}

	// Mẫu giới hạn hai mươi tên, nhưng số đếm là toàn bộ: người dùng cần con số thật
	// để quyết định, còn danh sách chỉ để nhận ra kiểu domain nào bị ảnh hưởng.
	const sampleSize = 20

	var wouldBlock, wouldUnblock []string
	var countBlock, countUnblock, unchanged int

	for _, c := range candidates {
		before := classify.ScoreWith(c.Domain, c.Facts, current.weights, current.rules)
		after := classify.ScoreWith(c.Domain, c.Facts, proposed.weights, proposed.rules)

		wasOver := before.Score >= thresholds[string(before.Category)]
		isOver := after.Score >= thresholds[string(after.Category)]

		switch {
		case !wasOver && isOver:
			countBlock++
			if len(wouldBlock) < sampleSize {
				wouldBlock = append(wouldBlock, c.Domain.Name)
			}
		case wasOver && !isOver:
			countUnblock++
			if len(wouldUnblock) < sampleSize {
				wouldUnblock = append(wouldUnblock, c.Domain.Name)
			}
		default:
			unchanged++
		}
	}

	return map[string]any{
		"would_block":   map[string]any{"count": countBlock, "sample": orEmpty(wouldBlock)},
		"would_unblock": map[string]any{"count": countUnblock, "sample": orEmpty(wouldUnblock)},
		"unchanged":     unchanged,
		"evaluated":     len(candidates),
	}, nil
}

func (s *Server) thresholds(ctx context.Context) (map[string]float64, error) {
	rows, err := s.store.Reader().QueryContext(ctx,
		`SELECT key, score_threshold FROM categories LIMIT 50`)
	if err != nil {
		return nil, fmt.Errorf("read thresholds: %w", err)
	}
	defer rows.Close()

	out := make(map[string]float64, 8)
	for rows.Next() {
		var key string
		var threshold float64
		if err := rows.Scan(&key, &threshold); err != nil {
			return nil, fmt.Errorf("scan threshold: %w", err)
		}
		out[key] = threshold
	}
	return out, rows.Err()
}

func (s *Server) currentWeights(ctx context.Context) (classify.Weights, error) {
	var weights classify.Weights
	found, err := s.store.GetSetting(ctx, store.SettingWeights, &weights)
	if err != nil {
		return nil, err
	}
	if !found || len(weights) == 0 {
		return classify.DefaultWeights, nil
	}
	return weights, nil
}

// handleRescore chạy lại chấm điểm toàn bộ.
func (s *Server) handleRescore(w http.ResponseWriter, r *http.Request) {
	jobID, err := s.worker.Enqueue(r.Context(), worker.JobRescore, nil)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": jobID})
}

// handleGetJob trả về trạng thái một job.
func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	job, err := s.store.GetJob(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// handleStatsOverview trả về số liệu dashboard.
func (s *Server) handleStatsOverview(w http.ResponseWriter, r *http.Request) {
	from, to := timeRange(r)
	overview, err := s.store.StatsOverview(r.Context(), from, to)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	overview.ByCategory = orEmpty(overview.ByCategory)
	overview.Timeseries = orEmpty(overview.Timeseries)
	writeJSON(w, http.StatusOK, overview)
}

// handleStatsTop trả về bảng xếp hạng.
func (s *Server) handleStatsTop(w http.ResponseWriter, r *http.Request) {
	from, to := timeRange(r)
	dimension := r.URL.Query().Get("dimension")

	// Vai trò viewer không được xem phân bố theo client.
	if dimension == "client" {
		if sess, ok := sessionFrom(r.Context()); !ok || sess.Role != store.RoleAdmin {
			writeError(w, http.StatusForbidden, CodeForbidden, "Cần quyền quản trị", nil)
			return
		}
	}

	items, err := s.store.StatsTop(r.Context(), dimension, from, to, atoiOr(r.URL.Query().Get("limit"), 20))
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": orEmpty(items)})
}

// handleHealth là kiểm tra sức khỏe cho giám sát. Không cần xác thực.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	checks := map[string]any{}
	status := "ok"

	degrade := func(level string) {
		if level == "down" || status == "ok" && level == "degraded" {
			status = level
		}
	}

	start := time.Now()
	var one int
	dbErr := s.store.Reader().QueryRowContext(ctx, `SELECT 1`).Scan(&one)
	checks["database"] = map[string]any{
		"ok": dbErr == nil, "latency_ms": time.Since(start).Milliseconds(),
	}
	if dbErr != nil {
		degrade("down")
	}

	// Kiểm tra quan trọng nhất của toàn hệ thống: nếu không nhận được truy vấn mới,
	// bộ mirror trên router đã dừng. Hệ thống vẫn chặn bình thường bằng danh sách cũ
	// nên không có triệu chứng nào khác — đây là lỗi im lặng nguy hiểm nhất.
	age, err := s.store.LastEventAge(ctx)
	querylog := map[string]any{"ok": err == nil && age >= 0 && age < 600, "last_event_age_s": age}

	listenError := ""
	if s.listener != nil {
		stats := s.listener.Stats()
		querylog["packets_received"] = stats.PacketsReceived
		querylog["packets_dropped"] = stats.PacketsDropped
		querylog["decode_errors"] = stats.DecodeErrors
		listenError = stats.ListenError

		// Hàng đợi đầy nghĩa là CSDL không theo kịp lưu lượng. Sự kiện bị bỏ không
		// bao giờ lấy lại được, nên nó phải hiện ra chứ không chỉ nằm trong bộ đếm.
		if stats.PacketsReceived > 0 {
			dropRatio := float64(stats.PacketsDropped) / float64(stats.PacketsReceived)
			if dropRatio > 0.01 {
				querylog["drop_ratio"] = dropRatio
				querylog["ok"] = false
				querylog["message"] = fmt.Sprintf(
					"Bỏ %.1f%% gói vì ghi CSDL không theo kịp — kiểm tra tốc độ đĩa",
					dropRatio*100)
				degrade("degraded")
			}
		}
	}

	switch {
	case listenError != "":
		// Phân biệt rõ với trường hợp không có lưu lượng: ở đây cổng còn không mở
		// được, nên chỉ người vận hành máy chủ này sửa được, không phải thiết bị mạng.
		querylog["ok"] = false
		querylog["listen_error"] = listenError
		querylog["message"] = "Không mở được cổng nhận TZSP: " + listenError
		degrade("down")

	case age < 0 || age >= 600:
		querylog["message"] = "Không nhận được truy vấn DNS mới — kiểm tra bộ mirror trên router"
		degrade("degraded")
	}
	checks["querylog"] = querylog

	pending, failed24h, err := s.store.JobHealth(ctx)
	checks["jobs"] = map[string]any{"ok": err == nil && failed24h == 0, "pending": pending, "failed_24h": failed24h}
	if failed24h > 0 {
		degrade("degraded")
	}

	sources, err := s.store.ListSources(ctx)
	sourcesOK, message := true, ""
	if err == nil {
		for _, src := range sources {
			if src.Enabled && src.LastStatus != "" && src.LastStatus != "ok" && src.LastStatus != "not_modified" {
				sourcesOK = false
				message = src.Name + ": " + src.LastStatus
				break
			}
		}
	}
	checks["sources"] = map[string]any{"ok": sourcesOK, "message": message}
	if !sourcesOK {
		degrade("degraded")
	}

	publishAge := int64(-1)
	if snap, err := s.store.LastSnapshot(ctx, 0); err == nil {
		if t, err := store.ParseTime(snap.PublishedAt); err == nil {
			publishAge = int64(time.Since(t).Seconds())
		}
	}
	checks["publish"] = map[string]any{"ok": publishAge >= 0, "last_publish_age_s": publishAge}

	code := http.StatusOK
	if status == "down" {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]any{"status": status, "checks": checks})
}

// handleMetrics phơi chỉ số dạng Prometheus.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	// Bảo vệ bằng token nếu có cấu hình: chỉ số tiết lộ hình dạng lưu lượng của mạng.
	if s.cfg.MetricsToken != "" {
		if r.Header.Get("Authorization") != "Bearer "+s.cfg.MetricsToken {
			writeError(w, http.StatusUnauthorized, CodeUnauthenticated, "Cần token", nil)
			return
		}
	}

	ctx := r.Context()
	counts, err := s.store.CountByStatus(ctx)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	age, _ := s.store.LastEventAge(ctx)
	pending, failed, _ := s.store.JobHealth(ctx)

	var sb strings.Builder
	sb.WriteString("# HELP dnsguard_domains_total Số domain theo trạng thái\n")
	sb.WriteString("# TYPE dnsguard_domains_total gauge\n")
	for _, status := range []string{"new", "staging", "blocked", "allowed", "ignored"} {
		fmt.Fprintf(&sb, "dnsguard_domains_total{status=%q} %d\n", status, counts[status])
	}
	sb.WriteString("# HELP dnsguard_last_event_age_seconds Số giây kể từ truy vấn DNS gần nhất\n")
	sb.WriteString("# TYPE dnsguard_last_event_age_seconds gauge\n")
	fmt.Fprintf(&sb, "dnsguard_last_event_age_seconds %d\n", age)
	sb.WriteString("# HELP dnsguard_jobs Số job theo trạng thái\n")
	sb.WriteString("# TYPE dnsguard_jobs gauge\n")
	fmt.Fprintf(&sb, "dnsguard_jobs{state=\"pending\"} %d\n", pending)
	fmt.Fprintf(&sb, "dnsguard_jobs{state=\"failed_24h\"} %d\n", failed)

	if s.listener != nil {
		stats := s.listener.Stats()
		sb.WriteString("# HELP dnsguard_packets_total Số gói TZSP đã nhận\n")
		sb.WriteString("# TYPE dnsguard_packets_total counter\n")
		fmt.Fprintf(&sb, "dnsguard_packets_total %d\n", stats.PacketsReceived)
		fmt.Fprintf(&sb, "dnsguard_packets_dropped_total %d\n", stats.PacketsDropped)
		fmt.Fprintf(&sb, "dnsguard_events_accepted_total %d\n", stats.EventsAccepted)
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Write([]byte(sb.String()))
}

// handleList phục vụ file danh sách cho router. Không cần xác thực.
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	// Chặn theo dải IP nguồn thay cho xác thực: router không gửi credential tiện lợi.
	if !allowedByCIDR(clientIP(r), s.cfg.ListsAllowCIDR) {
		writeError(w, http.StatusForbidden, CodeForbidden, "IP nguồn không được phép", nil)
		return
	}

	name := chi.URLParam(r, "file")
	// Chỉ nhận đúng tên file, không nhận đường dẫn: filepath.Base loại bỏ mọi thành
	// phần thư mục nên "../../etc/passwd" trở thành "passwd".
	if name != filepath.Base(name) || !strings.HasSuffix(name, ".txt") {
		writeError(w, http.StatusBadRequest, CodeInvalidInput, "Tên file không hợp lệ", nil)
		return
	}

	path := filepath.Join(s.cfg.ListsDir, name)
	f, err := os.Open(path)
	if err != nil {
		writeError(w, http.StatusNotFound, CodeNotFound, "Chưa có danh sách này", nil)
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		fail(w, s.log, err)
		return
	}

	// ETag lấy từ checksum của snapshot gần nhất khi có; nếu không thì từ thời điểm
	// sửa file và kích thước. Router gửi If-None-Match và nhận 304 khi không đổi.
	etag := fmt.Sprintf(`"%x-%x"`, info.ModTime().UnixNano(), info.Size())
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	http.ServeContent(w, r, name, info.ModTime(), f)
}

// maxResourceSpan là khoảng xem xa nhất cho phép.
//
// Bằng đúng hạn giữ mặc định: xin xa hơn thì chỉ nhận về khoảng trống, mà truy vấn
// vẫn phải quét toàn bảng.
const maxResourceSpan = 30 * 24 * time.Hour

// handleStatsResources trả về mức tiêu thụ RAM và CPU của chính tiến trình này.
//
// Độ rộng khoảng gộp do máy chủ chọn chứ không nhận từ client: người xem quan tâm
// khoảng thời gian, còn số điểm vẽ ra là chuyện của tầng dưới. Để client tự chọn
// nghĩa là mở đường cho một truy vấn xin ba mươi ngày ở nhịp mười giây.
func (s *Server) handleStatsResources(w http.ResponseWriter, r *http.Request) {
	to := time.Now()
	from := to.Add(-12 * time.Hour)

	q := r.URL.Query()
	if raw := q.Get("to"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, CodeInvalidInput,
				"Tham số to phải theo định dạng RFC3339", nil)
			return
		}
		to = parsed
	}
	if raw := q.Get("from"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, CodeInvalidInput,
				"Tham số from phải theo định dạng RFC3339", nil)
			return
		}
		from = parsed
	}

	if !from.Before(to) {
		writeError(w, http.StatusBadRequest, CodeInvalidInput,
			"Mốc from phải trước mốc to", nil)
		return
	}
	if to.Sub(from) > maxResourceSpan {
		from = to.Add(-maxResourceSpan)
	}

	bucket := store.BucketSeconds(to.Sub(from))
	fromAt, toAt := store.TimeAt(from), store.TimeAt(to)

	points, err := s.store.Resources(r.Context(), fromAt, toAt, bucket)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	summary, err := s.store.ResourceStats(r.Context(), fromAt, toAt)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	summary.RetainDays = s.cfg.ResourceRetainDays

	writeJSON(w, http.StatusOK, map[string]any{
		"from":           fromAt,
		"to":             toAt,
		"bucket_seconds": bucket,
		"sample_seconds": int(s.cfg.ResourceSampleEvery.Seconds()),
		"summary":        summary,
		"points":         orEmpty(points),
	})
}

// currentRules đọc luật tự đặt rồi gộp lên luật dựng sẵn.
func (s *Server) currentRules(ctx context.Context) (classify.Rules, error) {
	custom, err := s.customRules(ctx)
	if err != nil {
		return classify.Rules{}, err
	}
	return classify.Merge(custom), nil
}

// customRules đọc riêng phần tự đặt, dùng khi cần hiển thị lại đúng thứ người dùng
// đã nhập chứ không phải bộ luật đã gộp.
func (s *Server) customRules(ctx context.Context) (classify.Custom, error) {
	var custom classify.Custom
	if _, err := s.store.GetSetting(ctx, store.SettingRules, &custom); err != nil {
		return classify.Custom{}, err
	}
	return custom, nil
}

// handleGetRules trả về luật tự đặt kèm phần dựng sẵn để giao diện đối chiếu.
func (s *Server) handleGetRules(w http.ResponseWriter, r *http.Request) {
	custom, err := s.customRules(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}

	defaults := classify.DefaultRules()
	writeJSON(w, http.StatusOK, map[string]any{
		"custom": map[string]any{
			"adtech_domains": orEmptyMap(custom.AdtechDomains),
			"adtech_asns":    orEmptyASNs(custom.AdtechASNs),
			"shared_cdn":     orEmpty(custom.SharedCDN),
			"keywords":       orEmptyKeywords(custom.Keywords),
			"thresholds":     thresholdsOr(custom.Thresholds, classify.DefaultThresholds()),
		},
		// Số lượng dựng sẵn chứ không phải toàn bộ nội dung: giao diện chỉ cần cho
		// biết "67 tên miền dựng sẵn, bạn thêm 3", còn danh sách đầy đủ vừa dài vừa
		// không sửa được nên hiện ra chỉ làm rối.
		"builtin_counts": map[string]int{
			"adtech_domains": len(defaults.AdtechDomains),
			"adtech_asns":    len(defaults.AdtechASNs),
			"shared_cdn":     len(defaults.SharedCDN),
			"keywords":       countKeywords(defaults.Keywords),
			"neutral_asns":   classify.NeutralASNCount(),
		},
		"default_thresholds": classify.DefaultThresholds(),
	})
}

type updateRulesRequest struct {
	classify.Custom
	DryRun bool `json:"dry_run"`
}

// handleUpdateRules sửa luật phân loại, có chế độ tính trước tác động.
//
// Cùng luồng bắt buộc như đổi trọng số, và ở đây còn quan trọng hơn: cname_adtech
// mang trọng số 6,0 trong khi ngưỡng ads là 5,5, nghĩa là một tên miền thêm nhầm vào
// danh sách adtech đủ để chặn một domain mà không cần bằng chứng nào khác. Với danh
// sách nằm trong mã nguồn thì việc đó qua được review; với một ô nhập trên giao diện
// thì bảng xem trước là lớp bảo vệ duy nhất.
func (s *Server) handleUpdateRules(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFrom(r.Context())
	ctx := r.Context()

	var req updateRulesRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidInput, "Dữ liệu không hợp lệ", nil)
		return
	}

	if errs := req.Custom.Validate(); len(errs) > 0 {
		writeError(w, http.StatusUnprocessableEntity, CodeValidation,
			"Có mục không hợp lệ", map[string]any{"rejected": errs})
		return
	}

	weights, err := s.currentWeights(ctx)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	currentRules, err := s.currentRules(ctx)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	proposedRules := classify.Merge(req.Custom)

	if req.DryRun {
		impact, err := s.simulate(ctx,
			scoringConfig{weights: weights, rules: currentRules},
			scoringConfig{weights: weights, rules: proposedRules})
		if err != nil {
			fail(w, s.log, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"impact": impact})
		return
	}

	if err := s.store.SetSetting(ctx, store.SettingRules, req.Custom, sess.Username); err != nil {
		fail(w, s.log, err)
		return
	}
	s.log.Info("cập nhật luật phân loại",
		"domains", len(req.Custom.AdtechDomains), "asns", len(req.Custom.AdtechASNs),
		"keywords", countKeywords(req.Custom.Keywords), "by", sess.Username)

	// Chấm điểm lại toàn bộ: luật mới chỉ có tác dụng khi domain được chấm lại, và
	// bắt người dùng bấm thêm một nút nữa chỉ tạo ra khoảng thời gian mà giao diện
	// nói một đằng còn dữ liệu là một nẻo.
	jobID, err := s.worker.Enqueue(ctx, worker.JobRescore, nil)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": jobID})
}

func countKeywords(groups map[classify.Category][]string) int {
	n := 0
	for _, words := range groups {
		n += len(words)
	}
	return n
}

func orEmptyMap(m map[string]classify.Category) map[string]classify.Category {
	if m == nil {
		return map[string]classify.Category{}
	}
	return m
}

func orEmptyASNs(m map[int]classify.ASNInfo) map[int]classify.ASNInfo {
	if m == nil {
		return map[int]classify.ASNInfo{}
	}
	return m
}

func orEmptyKeywords(m map[classify.Category][]string) map[classify.Category][]string {
	if m == nil {
		return map[classify.Category][]string{}
	}
	return m
}

func thresholdsOr(t *classify.Thresholds, fallback classify.Thresholds) classify.Thresholds {
	if t == nil {
		return fallback
	}
	return classify.MergeThresholds(fallback, *t)
}
