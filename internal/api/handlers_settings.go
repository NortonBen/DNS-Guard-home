package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/benji/dnsguard/internal/classify"
	"github.com/benji/dnsguard/internal/enrich"
	"github.com/benji/dnsguard/internal/store"
	"github.com/benji/dnsguard/internal/worker"
)

// handleGetSettings gom mọi thứ màn Cài đặt cần vào một lần gọi.
//
// Một payload thay vì năm endpoint: màn này luôn hiển thị toàn bộ các mục cùng lúc,
// nên tách nhỏ chỉ tạo ra năm trạng thái tải khác nhau phải xử lý riêng.
func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	soft, err := s.store.SoftAllowList(ctx)
	if err != nil {
		fail(w, s.log, err)
		return
	}

	staging, confirm := s.lifecycleDays(ctx)

	var tables []enrich.TableStatus
	if s.enrichers != nil {
		tables = s.enrichers.Statuses()
	}
	if tables == nil {
		tables = []enrich.TableStatus{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"protect": map[string]any{
			// Danh sách cứng nằm trong code và chỉ đổi được qua pull request; trả về
			// để giao diện hiển thị chứ không phải để sửa.
			"hard": classify.HardAllowList(),
			"soft": orEmpty(soft),
		},
		"lifecycle": map[string]any{
			"staging_days":             staging,
			"confirm_ttl_days":         confirm,
			"staging_days_default":     s.cfg.StagingDays,
			"confirm_ttl_days_default": s.cfg.ConfirmTTLDays,
		},
		"analysis": s.analysisSettings(ctx),
		"publish": map[string]any{
			"sink_address": s.publisher.SinkAddress(ctx),
			"sink_default": s.cfg.PublishSink,
		},
		"lookup_tables": tables,
		"system":        s.systemInfo(),
	})
}

// analysisSettings mô tả trạng thái hai nguồn chạm trực tiếp ra ngoài.
func (s *Server) analysisSettings(ctx context.Context) map[string]any {
	enabled := s.httpAnalysisEnabled(ctx)

	vtConfigured, vtHint := false, ""
	if vt := s.virusTotal(); vt != nil {
		vtConfigured = vt.Configured()
		vtHint = vt.KeyHint()
	}

	return map[string]any{
		"http_enabled": enabled,
		// Biến môi trường là công tắc cứng: bật trong giao diện cũng không thắng được
		// nó. Trả ra để giao diện giải thích được vì sao nút bị khóa.
		"external_enabled": s.cfg.ExternalEnabled,
		"http_effective":   enabled && s.cfg.ExternalEnabled,
		"vt_configured":    vtConfigured,
		// Chỉ bốn ký tự cuối. Khóa đầy đủ không bao giờ rời khỏi máy chủ — kể cả với
		// người quản trị đã đăng nhập, vì không có việc gì trên giao diện cần tới nó.
		"vt_key_hint": vtHint,
		// Khóa đặt bằng biến môi trường thì giao diện không sửa được; nói ra để người
		// dùng không loay hoay với một ô nhập không có tác dụng.
		"vt_from_env": s.cfg.VTAPIKey != "" && !s.vtKeyStored(ctx),
	}
}

// virusTotal lấy nguồn VirusTotal đang chạy, hoặc nil nếu chưa đăng ký.
func (s *Server) virusTotal() *enrich.VTEnricher {
	if s.enrichers == nil {
		return nil
	}
	src, ok := s.enrichers.Get("vt")
	if !ok {
		return nil
	}
	// Registry bọc nguồn trong lớp giới hạn tốc độ, nên phải hỏi lớp bọc trả về ruột.
	vt, _ := enrich.Unwrap(src).(*enrich.VTEnricher)
	return vt
}

// vtKeyStored cho biết khóa đang dùng đến từ CSDL hay từ biến môi trường.
func (s *Server) vtKeyStored(ctx context.Context) bool {
	var stored string
	ok, err := s.store.GetSetting(ctx, store.SettingVTAPIKey, &stored)
	return err == nil && ok && stored != ""
}

// httpAnalysisEnabled đọc công tắc từ settings, lùi về biến môi trường khi chưa đặt.
func (s *Server) httpAnalysisEnabled(ctx context.Context) bool {
	var v bool
	if ok, err := s.store.GetSetting(ctx, store.SettingHTTPAnalysis, &v); err == nil && ok {
		return v
	}
	return s.cfg.HTTPAnalysisEnabled
}

type analysisRequest struct {
	HTTPEnabled *bool `json:"http_enabled"`
	// VTAPIKey: con trỏ để phân biệt "không đụng tới" với "xóa đi". Chuỗi rỗng là
	// lệnh gỡ khóa, còn trường vắng mặt thì giữ nguyên khóa cũ.
	VTAPIKey *string `json:"vt_api_key"`
}

// vtKeyPattern là dạng khóa API VirusTotal: sáu mươi tư ký tự hex thường.
//
// Kiểm tra dạng trước khi gọi mạng để bắt lỗi dán thiếu ngay lập tức, thay vì chờ
// một vòng đi về VirusTotal mới báo.
var vtKeyPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// handleUpdateAnalysis bật hoặc tắt việc phân tích HTTP ngay lúc chạy.
func (s *Server) handleUpdateAnalysis(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFrom(r.Context())
	ctx := r.Context()

	var req analysisRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidInput, "Dữ liệu không hợp lệ", nil)
		return
	}

	if req.HTTPEnabled != nil {
		if *req.HTTPEnabled && !s.cfg.ExternalEnabled {
			// Công tắc cứng ở biến môi trường thắng. Nói rõ thay vì lưu một giá trị
			// không có tác dụng gì.
			writeError(w, http.StatusUnprocessableEntity, CodeValidation,
				"Không bật được khi DNSGUARD_EXTERNAL_ENABLED=false", nil)
			return
		}
		if err := s.store.SetSetting(ctx, store.SettingHTTPAnalysis,
			*req.HTTPEnabled, sess.Username); err != nil {
			fail(w, s.log, err)
			return
		}
		// Áp dụng ngay vào registry đang chạy: không bắt khởi động lại dịch vụ.
		if s.enrichers != nil {
			s.enrichers.SetEnabled("http", *req.HTTPEnabled && s.cfg.ExternalEnabled)
		}
		s.log.Info("đổi cấu hình phân tích HTTP",
			"enabled", *req.HTTPEnabled, "by", sess.Username)
	}

	if req.VTAPIKey != nil {
		if !s.updateVTKey(w, r, strings.TrimSpace(*req.VTAPIKey), sess.Username) {
			return
		}
	}

	writeJSON(w, http.StatusOK, s.analysisSettings(ctx))
}

// updateVTKey lưu hoặc gỡ khóa API VirusTotal. Trả về false khi đã ghi lỗi ra client.
//
// Khóa không bao giờ đi vào log hay phản hồi: nơi duy nhất nó tồn tại là bảng settings
// và bộ nhớ của tiến trình.
func (s *Server) updateVTKey(w http.ResponseWriter, r *http.Request, key, actor string) bool {
	ctx := r.Context()

	vt := s.virusTotal()
	if vt == nil {
		writeError(w, http.StatusServiceUnavailable, CodeInternal,
			"Nguồn VirusTotal chưa được đăng ký", nil)
		return false
	}

	if key == "" {
		if err := s.store.SetSetting(ctx, store.SettingVTAPIKey, "", actor); err != nil {
			fail(w, s.log, err)
			return false
		}
		// Lùi về khóa ở biến môi trường nếu có: gỡ khóa nhập tay không nên vô hiệu
		// hóa luôn cấu hình sẵn của máy chủ.
		vt.SetAPIKey(s.cfg.VTAPIKey)
		s.enrichers.SetEnabled("vt", s.cfg.ExternalEnabled && vt.Configured())
		s.log.Info("gỡ khóa API VirusTotal", "by", actor)
		return true
	}

	if !vtKeyPattern.MatchString(key) {
		writeError(w, http.StatusUnprocessableEntity, CodeValidation,
			"Khóa API VirusTotal phải là 64 ký tự hex thường", nil)
		return false
	}

	// Kiểm tra bằng một lượt gọi thật trước khi lưu. Thời gian chờ ngắn hơn timeout
	// của enricher: người dùng đang đợi trước màn hình, không phải một job nền.
	verifyCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	switch err := vt.VerifyKey(verifyCtx, key); {
	case err == nil:
		// hợp lệ, đi tiếp
	case errors.Is(err, enrich.ErrVTBadKey):
		writeError(w, http.StatusUnprocessableEntity, CodeValidation,
			"VirusTotal từ chối khóa này", nil)
		return false
	default:
		// Không gọi được VirusTotal không có nghĩa khóa sai. Nói đúng nguyên nhân,
		// đừng đổ cho khóa.
		s.log.Warn("không kiểm tra được khóa VirusTotal", "err", err, "by", actor)
		writeError(w, http.StatusBadGateway, CodeInternal,
			"Không kết nối được VirusTotal để kiểm tra khóa", nil)
		return false
	}

	if err := s.store.SetSetting(ctx, store.SettingVTAPIKey, key, actor); err != nil {
		fail(w, s.log, err)
		return false
	}
	vt.SetAPIKey(key)
	s.enrichers.SetEnabled("vt", s.cfg.ExternalEnabled)
	s.log.Info("cập nhật khóa API VirusTotal", "by", actor)
	return true
}

type publishSettingsRequest struct {
	SinkAddress string `json:"sink_address"`
}

// handleUpdatePublish đổi địa chỉ IP mà mọi domain bị chặn trỏ về.
//
// Không xuất bản lại ngay: người dùng có thể đang đổi nhiều thứ, và một lần ghi lại
// toàn bộ danh sách là việc nặng. Địa chỉ mới có tác dụng từ lần xuất bản kế tiếp,
// và giao diện nói rõ điều đó.
func (s *Server) handleUpdatePublish(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFrom(r.Context())

	var req publishSettingsRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidInput, "Dữ liệu không hợp lệ", nil)
		return
	}

	addr := strings.TrimSpace(req.SinkAddress)
	if addr == "" {
		writeError(w, http.StatusUnprocessableEntity, CodeValidation,
			"Phải nhập địa chỉ IP", nil)
		return
	}

	ip, err := netip.ParseAddr(addr)
	if err != nil {
		// Tên miền không dùng được: định dạng hosts chỉ nhận địa chỉ IP, và một tên
		// miền ở cột đầu làm hỏng cả file với phần lớn phần mềm đọc nó.
		writeError(w, http.StatusUnprocessableEntity, CodeValidation,
			"Phải là địa chỉ IP hợp lệ, ví dụ 0.0.0.0 hoặc ::", nil)
		return
	}
	if ip.IsMulticast() {
		writeError(w, http.StatusUnprocessableEntity, CodeValidation,
			"Địa chỉ multicast không dùng được làm đích chặn", nil)
		return
	}

	// Chuẩn hóa: 0.0.0.000 và ::ffff:0.0.0.0 phải lưu về cùng một dạng, nếu không
	// checksum đổi mỗi lần lưu dù nội dung không đổi.
	if err := s.store.SetSetting(r.Context(), store.SettingPublishSink,
		ip.String(), sess.Username); err != nil {
		fail(w, s.log, err)
		return
	}
	s.log.Info("đổi địa chỉ xuất bản", "sink", ip.String(), "by", sess.Username)

	writeJSON(w, http.StatusOK, map[string]any{
		"sink_address": ip.String(),
		"sink_default": s.cfg.PublishSink,
	})
}

// lifecycleDays đọc ngưỡng vòng đời từ settings, lùi về biến môi trường khi chưa đặt.
func (s *Server) lifecycleDays(ctx context.Context) (staging, confirm int) {
	staging, confirm = s.cfg.StagingDays, s.cfg.ConfirmTTLDays

	var v int
	if ok, err := s.store.GetSetting(ctx, store.SettingStagingDays, &v); err == nil && ok && v > 0 {
		staging = v
	}
	if ok, err := s.store.GetSetting(ctx, store.SettingConfirmTTL, &v); err == nil && ok && v > 0 {
		confirm = v
	}
	return staging, confirm
}

// systemInfo là các giá trị chỉ đọc, đặt bằng biến môi trường lúc khởi động.
//
// Hiển thị chúng để người vận hành không phải vào máy chủ đọc file cấu hình mới biết
// hệ thống đang chạy với thiết lập nào.
func (s *Server) systemInfo() map[string]any {
	info := map[string]any{
		"version":               s.version,
		"db_path":               s.store.Path(),
		"lists_dir":             s.cfg.ListsDir,
		"listen":                s.cfg.Listen,
		"tzsp_listen":           s.cfg.TZSPListen,
		"external_enabled":      s.cfg.ExternalEnabled,
		"log_retention_days":    s.cfg.LogRetentionDays,
		"hourly_retention_days": s.cfg.HourlyRetentionDays,
		"keep_snapshots":        s.cfg.KeepSnapshots,
		"publish_min_ratio":     s.cfg.PublishMinRatio,
		"lists_allow_cidr":      orEmpty(s.cfg.ListsAllowCIDR),
		"auto_migrate":          s.cfg.AutoMigrate,
	}

	// Kích thước CSDL gồm cả file WAL: phần lớn dữ liệu vừa ghi còn nằm ở đó, nên
	// chỉ tính file chính sẽ báo thiếu.
	var total int64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if st, err := os.Stat(s.store.Path() + suffix); err == nil {
			total += st.Size()
		}
	}
	info["db_size_bytes"] = total
	return info
}

type lifecycleRequest struct {
	StagingDays    *int `json:"staging_days"`
	ConfirmTTLDays *int `json:"confirm_ttl_days"`
}

// handleUpdateLifecycle sửa ngưỡng vòng đời.
func (s *Server) handleUpdateLifecycle(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFrom(r.Context())

	var req lifecycleRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidInput, "Dữ liệu không hợp lệ", nil)
		return
	}

	if req.StagingDays != nil {
		// Không cho đặt 0: bỏ hẳn giai đoạn canary nghĩa là domain bị chặn ngay khi
		// vượt ngưỡng, và đó chính là cách chặn nhầm hàng loạt mà không ai kịp thấy.
		if *req.StagingDays < 1 || *req.StagingDays > 90 {
			writeError(w, http.StatusUnprocessableEntity, CodeValidation,
				"Thời gian chờ phải từ 1 đến 90 ngày", nil)
			return
		}
		if err := s.store.SetSetting(r.Context(), store.SettingStagingDays,
			*req.StagingDays, sess.Username); err != nil {
			fail(w, s.log, err)
			return
		}
	}

	if req.ConfirmTTLDays != nil {
		// Ngưỡng này phải dài hơn hẳn thời gian chờ. Nếu ngắn, domain đã chặn sẽ hết
		// hạn rồi vào lại hàng chờ rồi bị chặn lại — dao động vĩnh viễn, và mỗi chu
		// kỳ có một khoảng quảng cáo lọt qua.
		if *req.ConfirmTTLDays < 30 || *req.ConfirmTTLDays > 3650 {
			writeError(w, http.StatusUnprocessableEntity, CodeValidation,
				"Thời hạn giữ trạng thái chặn phải từ 30 đến 3650 ngày", nil)
			return
		}
		if err := s.store.SetSetting(r.Context(), store.SettingConfirmTTL,
			*req.ConfirmTTLDays, sess.Username); err != nil {
			fail(w, s.log, err)
			return
		}
	}

	staging, confirm := s.lifecycleDays(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"staging_days": staging, "confirm_ttl_days": confirm,
	})
}

type refreshLookupRequest struct {
	URL string `json:"url"`
}

// handleRefreshLookup tải lại một bảng tra cứu cục bộ.
func (s *Server) handleRefreshLookup(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "kind")
	if kind != "asn" && kind != "rank" {
		writeError(w, http.StatusBadRequest, CodeInvalidInput,
			"Bảng tra cứu không rõ", map[string]any{"kind": kind})
		return
	}

	var req refreshLookupRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, CodeInvalidInput, "Dữ liệu không hợp lệ", nil)
			return
		}
	}
	// Chỉ nhận HTTP(S): một đường dẫn file:// sẽ biến endpoint này thành công cụ đọc
	// file tùy ý trên máy chủ.
	if req.URL != "" && !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
		writeError(w, http.StatusUnprocessableEntity, CodeValidation,
			"URL phải bắt đầu bằng http:// hoặc https://", nil)
		return
	}

	jobID, err := s.worker.Enqueue(r.Context(), worker.JobRefresh,
		map[string]any{"kind": kind, "url": req.URL})
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": jobID})
}
