package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/benji/dnsguard/internal/auth"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// handleLogin xác thực và đặt cookie phiên.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	// Độ trễ cố định bất kể kết quả: nếu không, thời gian phản hồi sẽ tiết lộ tài
	// khoản nào tồn tại. Đặt mốc ngay từ đầu để cả nhánh thành công lẫn thất bại
	// cùng chờ tới cùng một thời điểm.
	deadline := time.After(500 * time.Millisecond)
	defer func() { <-deadline }()

	ip := clientIP(r)
	if !s.logins.allow(ip) {
		writeError(w, http.StatusTooManyRequests, CodeRateLimited,
			"Quá nhiều lần đăng nhập sai, thử lại sau 15 phút", nil)
		return
	}

	var req loginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidInput, "Dữ liệu không hợp lệ", nil)
		return
	}

	user, token, csrf, expiresAt, err := s.auth.Login(
		r.Context(), req.Username, req.Password, r.UserAgent(), ip)
	if err != nil {
		s.logins.record(ip)
		fail(w, s.log, err)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
		// Không đặt Secure: hệ thống chạy trong LAN qua HTTP, và cookie có cờ Secure
		// sẽ không bao giờ được gửi đi. Ai cần truy cập từ xa thì đi qua VPN.
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"user":       user,
		"csrf_token": csrf,
		"expires_at": expiresAt.UTC().Format(time.RFC3339),
	})
}

// handleLogout hủy phiên.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		if err := s.auth.Logout(r.Context(), cookie.Value); err != nil {
			fail(w, s.log, err)
			return
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1,
	})
	w.WriteHeader(http.StatusNoContent)
}

// handleMe trả về người dùng hiện tại và CSRF token.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	sess, ok := sessionFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, CodeUnauthenticated, "Cần đăng nhập", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user": map[string]any{
			"id": sess.UserID, "username": sess.Username, "role": sess.Role,
		},
		"csrf_token": sess.CSRFToken,
	})
}

var _ = auth.ErrInvalidCredentials
