package api

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/benji/dnsguard/internal/store"
)

// sessionCookie là tên cookie phiên.
const sessionCookie = "dnsguard_session"

type contextKey string

const sessionKey contextKey = "session"

// sessionFrom lấy phiên từ context. ok bằng false nghĩa là chưa đăng nhập.
func sessionFrom(ctx context.Context) (store.Session, bool) {
	sess, ok := ctx.Value(sessionKey).(store.Session)
	return sess, ok
}

// requireAuth từ chối request không có phiên hợp lệ.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			writeError(w, http.StatusUnauthorized, CodeUnauthenticated, "Cần đăng nhập", nil)
			return
		}
		sess, err := s.auth.Session(r.Context(), cookie.Value)
		if err != nil {
			writeError(w, http.StatusUnauthorized, CodeUnauthenticated, "Phiên hết hạn", nil)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey, sess)))
	})
}

// requireAdmin từ chối vai trò viewer.
func requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, ok := sessionFrom(r.Context())
		if !ok || sess.Role != store.RoleAdmin {
			writeError(w, http.StatusForbidden, CodeForbidden, "Cần quyền quản trị", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireCSRF bảo vệ các phương thức thay đổi trạng thái.
//
// Cookie phiên dùng SameSite=Lax, đã chặn phần lớn CSRF, nhưng token riêng là lớp
// thứ hai: SameSite phụ thuộc vào trình duyệt làm đúng, còn token thì không.
func requireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}

		sess, ok := sessionFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, CodeUnauthenticated, "Cần đăng nhập", nil)
			return
		}
		got := r.Header.Get("X-CSRF-Token")
		if subtle.ConstantTimeCompare([]byte(got), []byte(sess.CSRFToken)) != 1 {
			writeError(w, http.StatusForbidden, CodeForbidden, "Thiếu hoặc sai CSRF token", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requestLogger ghi log ở biên hệ thống.
//
// Chỉ log ở đây và ở job runner, không log ở tầng giữa: tầng giữa bọc lỗi kèm ngữ
// cảnh rồi trả lên, nếu tầng nào cũng log thì một lỗi sẽ xuất hiện năm lần.
func requestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rec, r)

			level := slog.LevelInfo
			if rec.status >= 500 {
				level = slog.LevelError
			}
			log.Log(r.Context(), level, "http",
				"method", r.Method, "path", r.URL.Path, "status", rec.status,
				"took", time.Since(start).Round(time.Millisecond))
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// Flush chuyển tiếp cho SSE hoạt động qua lớp bọc này.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// recoverPanic biến panic thành 500 thay vì làm sập tiến trình.
func recoverPanic(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					traceID := newTraceID()
					log.Error("panic trong handler",
						"trace_id", traceID, "path", r.URL.Path, "panic", rec)
					writeError(w, http.StatusInternalServerError, CodeInternal,
						"Lỗi máy chủ", map[string]any{"trace_id": traceID})
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// loginLimiter giới hạn số lần đăng nhập sai theo IP.
type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
	limit    int
	window   time.Duration
}

func newLoginLimiter(limit int, window time.Duration) *loginLimiter {
	return &loginLimiter{attempts: make(map[string][]time.Time), limit: limit, window: window}
}

// allow cho biết IP còn được thử không.
func (l *loginLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := time.Now().Add(-l.window)
	kept := l.attempts[ip][:0]
	for _, t := range l.attempts[ip] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	l.attempts[ip] = kept
	return len(kept) < l.limit
}

// record ghi nhận một lần thử thất bại.
func (l *loginLimiter) record(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempts[ip] = append(l.attempts[ip], time.Now())
}

// clientIP lấy địa chỉ người gọi.
//
// Không tin X-Forwarded-For: hệ thống chạy trong mạng nội bộ, không sau proxy, nên
// header đó chỉ có thể do client tự đặt và sẽ dùng để lách giới hạn đăng nhập.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// allowedByCIDR kiểm tra IP nguồn với danh sách dải cho phép.
//
// Endpoint danh sách không yêu cầu xác thực vì router không gửi credential tiện lợi;
// giới hạn theo dải IP là biện pháp bù lại.
func allowedByCIDR(remote string, cidrs []string) bool {
	if len(cidrs) == 0 {
		return true
	}
	ip := net.ParseIP(remote)
	if ip == nil {
		return false
	}
	for _, raw := range cidrs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		_, network, err := net.ParseCIDR(raw)
		if err != nil {
			// Cũng chấp nhận một địa chỉ đơn lẻ, không bắt buộc phải viết /32.
			if raw == remote {
				return true
			}
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
