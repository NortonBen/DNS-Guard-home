package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/benji/dnsguard/internal/auth"
	"github.com/benji/dnsguard/internal/publish"
	"github.com/benji/dnsguard/internal/store"
)

// Mã lỗi trả về cho client. Chuỗi này là một phần hợp đồng API: giao diện phân
// nhánh theo chúng, nên đổi tên là thay đổi phá vỡ.
const (
	CodeInvalidInput    = "invalid_input"
	CodeInvalidDomain   = "invalid_domain"
	CodeUnauthenticated = "unauthenticated"
	CodeForbidden       = "forbidden"
	CodeDomainProtected = "domain_protected"
	CodeNotFound        = "not_found"
	CodeConflict        = "conflict"
	CodeValidation      = "validation_failed"
	CodeRateLimited     = "rate_limited"
	CodePublishBlocked  = "publish_blocked"
	CodeInternal        = "internal"
	CodeHighRank        = "high_rank"
	CodeDuplicate       = "duplicate"
)

// apiError là cấu trúc lỗi duy nhất mà API trả về.
type apiError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type errorEnvelope struct {
	Error apiError `json:"error"`
}

// writeJSON ghi một phản hồi JSON.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if payload == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		// Lỗi ở đây nghĩa là client đã ngắt kết nối; không còn gì để làm.
		return
	}
}

// writeError ghi một lỗi theo đúng cấu trúc chung.
func writeError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	writeJSON(w, status, errorEnvelope{Error: apiError{Code: code, Message: message, Details: details}})
}

// fail ánh xạ lỗi nghiệp vụ sang mã HTTP.
//
// Đây là chỗ duy nhất biết cả hai thế giới: tầng dưới trả về lỗi có kiểu, tầng này
// dịch sang giao thức. Nhờ vậy store và classify không cần biết HTTP tồn tại.
func fail(w http.ResponseWriter, log *slog.Logger, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, CodeNotFound, "Không tìm thấy tài nguyên", nil)

	case errors.Is(err, store.ErrDomainProtected):
		writeError(w, http.StatusForbidden, CodeDomainProtected,
			"Domain nằm trong danh sách bảo vệ và không thể chặn",
			map[string]any{"reason": err.Error()})

	case errors.Is(err, store.ErrAlreadyExists):
		writeError(w, http.StatusConflict, CodeConflict, "Tài nguyên đã tồn tại", nil)

	case errors.Is(err, auth.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, CodeUnauthenticated, "Sai tài khoản hoặc mật khẩu", nil)

	case errors.Is(err, publish.ErrPublishBlocked):
		writeError(w, http.StatusConflict, CodePublishBlocked, err.Error(), nil)

	default:
		// Lỗi không lường trước: trả về mã theo dõi cho client, chi tiết chỉ vào log.
		// Thông điệp lỗi nội bộ có thể chứa tên bảng, đường dẫn hoặc dữ liệu người
		// dùng, nên không gửi ra ngoài.
		traceID := newTraceID()
		log.Error("lỗi máy chủ", "trace_id", traceID, "err", err)
		writeError(w, http.StatusInternalServerError, CodeInternal,
			"Lỗi máy chủ", map[string]any{"trace_id": traceID})
	}
}

func newTraceID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(buf)
}
