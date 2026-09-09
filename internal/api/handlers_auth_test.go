package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/benji/dnsguard/internal/auth"
	"github.com/benji/dnsguard/internal/store"
)

// doAs gửi request dưới một phiên cụ thể thay vì phiên đang gắn vào harness.
//
// Cần thiết cho các test đổi mật khẩu: chúng phải giữ hai phiên cùng lúc để kiểm tra
// phiên nào chết và phiên nào sống sót.
func (h *harness) doAs(cookie, csrf, method, path string, body any) (*http.Response, []byte) {
	h.t.Helper()

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			h.t.Fatalf("mã hóa body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		h.t.Fatalf("dựng request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}

	resp, err := h.server.Client().Do(req)
	if err != nil {
		h.t.Fatalf("gửi request: %v", err)
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		h.t.Fatalf("đọc phản hồi: %v", err)
	}
	return resp, payload
}

// loginAs mở một phiên mới và trả về cookie kèm CSRF token, không đụng vào phiên
// đang gắn trên harness.
func (h *harness) loginAs(username, password string) (cookie, csrf string) {
	h.t.Helper()

	resp, body := h.doAs("", "", http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": username, "password": password})
	if resp.StatusCode != http.StatusOK {
		h.t.Fatalf("đăng nhập %q = %d: %s", username, resp.StatusCode, body)
	}

	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie {
			cookie = c.Name + "=" + c.Value
		}
	}
	var out struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		h.t.Fatalf("giải mã phản hồi đăng nhập: %v", err)
	}
	if cookie == "" || out.CSRFToken == "" {
		h.t.Fatal("đăng nhập không trả về cookie hoặc CSRF token")
	}
	return cookie, out.CSRFToken
}

// seedUser tạo một tài khoản có mật khẩu đặt sẵn.
func (h *harness) seedUser(username, password, role string) {
	h.t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		h.t.Fatalf("băm mật khẩu: %v", err)
	}
	if _, err := h.store.CreateUser(context.Background(), username, hash, role); err != nil {
		h.t.Fatalf("tạo tài khoản %q: %v", username, err)
	}
}

// errorCode bóc mã lỗi để test so theo hợp đồng API chứ không theo câu chữ thông báo.
func errorCode(t *testing.T, payload []byte) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("giải mã lỗi: %v (%s)", err, payload)
	}
	return body.Error.Code
}

func TestChangePasswordSwapsCredentials(t *testing.T) {
	h := newHarness(t)
	h.login()

	resp, body := h.do(http.MethodPost, "/api/v1/auth/password", map[string]string{
		"current_password": "mật-khẩu-thử",
		"new_password":     "mật-khẩu-mới-dài-hơn",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("đổi mật khẩu = %d: %s", resp.StatusCode, body)
	}

	resp, _ = h.doAs("", "", http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "admin", "password": "mật-khẩu-thử"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("mật khẩu cũ vẫn đăng nhập được = %d, muốn 401", resp.StatusCode)
	}

	resp, body = h.doAs("", "", http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "admin", "password": "mật-khẩu-mới-dài-hơn"})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("mật khẩu mới không đăng nhập được = %d: %s", resp.StatusCode, body)
	}
}

// Gõ nhầm mật khẩu hiện tại không được trả 401: giao diện coi mọi 401 là phiên hết
// hạn và đá người dùng về trang đăng nhập, nên một lỗi chính tả sẽ làm mất cả phiên.
func TestWrongCurrentPasswordDoesNotEndSession(t *testing.T) {
	h := newHarness(t)
	h.login()

	resp, body := h.do(http.MethodPost, "/api/v1/auth/password", map[string]string{
		"current_password": "sai-bét",
		"new_password":     "mật-khẩu-mới-dài-hơn",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("mật khẩu hiện tại sai = %d, muốn 400: %s", resp.StatusCode, body)
	}
	if got := errorCode(t, body); got != CodeInvalidPassword {
		t.Errorf("mã lỗi = %q, muốn %q", got, CodeInvalidPassword)
	}

	// Phiên phải còn sống sau lần thử hỏng.
	resp, body = h.do(http.MethodGet, "/api/v1/auth/me", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("phiên chết sau khi nhập sai mật khẩu = %d: %s", resp.StatusCode, body)
	}

	// Và mật khẩu cũ vẫn phải nguyên vẹn.
	resp, _ = h.doAs("", "", http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "admin", "password": "mật-khẩu-thử"})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("mật khẩu cũ hỏng sau lần đổi thất bại = %d", resp.StatusCode)
	}
}

func TestChangePasswordRejectsWeakAndUnchanged(t *testing.T) {
	h := newHarness(t)
	h.login()

	cases := []struct {
		name string
		next string
	}{
		{"quá ngắn", "ngắn"},
		{"trùng mật khẩu cũ", "mật-khẩu-thử"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := h.do(http.MethodPost, "/api/v1/auth/password", map[string]string{
				"current_password": "mật-khẩu-thử",
				"new_password":     tc.next,
			})
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("= %d, muốn 400: %s", resp.StatusCode, body)
			}
			if got := errorCode(t, body); got != CodePasswordPolicy {
				t.Errorf("mã lỗi = %q, muốn %q", got, CodePasswordPolicy)
			}
		})
	}

	// Không lần nào trong số đó được phép đổi mật khẩu thật.
	resp, _ := h.doAs("", "", http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "admin", "password": "mật-khẩu-thử"})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("mật khẩu bị đổi dù yêu cầu bị từ chối = %d", resp.StatusCode)
	}
}

// Đổi mật khẩu phải giết các phiên khác, nếu không thì kẻ đang giữ cookie cũ vẫn vào
// được và việc đổi mật khẩu chẳng ngăn được gì.
func TestChangePasswordRevokesOtherSessionsButKeepsCurrent(t *testing.T) {
	h := newHarness(t)

	staleCookie, staleCSRF := h.loginAs("admin", "mật-khẩu-thử")
	currentCookie, currentCSRF := h.loginAs("admin", "mật-khẩu-thử")

	resp, body := h.doAs(currentCookie, currentCSRF, http.MethodPost, "/api/v1/auth/password",
		map[string]string{
			"current_password": "mật-khẩu-thử",
			"new_password":     "mật-khẩu-mới-dài-hơn",
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("đổi mật khẩu = %d: %s", resp.StatusCode, body)
	}

	var out struct {
		Revoked int64 `json:"revoked_sessions"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("giải mã phản hồi: %v", err)
	}
	if out.Revoked != 1 {
		t.Errorf("số phiên bị hủy = %d, muốn 1", out.Revoked)
	}

	resp, _ = h.doAs(staleCookie, staleCSRF, http.MethodGet, "/api/v1/auth/me", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("phiên cũ vẫn dùng được = %d, muốn 401", resp.StatusCode)
	}

	resp, body = h.doAs(currentCookie, currentCSRF, http.MethodGet, "/api/v1/auth/me", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("phiên hiện tại bị hủy oan = %d: %s", resp.StatusCode, body)
	}
}

// Tài khoản chỉ đọc cũng phải tự đổi được mật khẩu: nếu không, mật khẩu do quản trị
// viên đặt sẽ nằm nguyên đó mãi mãi.
func TestViewerCanChangeOwnPassword(t *testing.T) {
	h := newHarness(t)
	h.seedUser("nguoixem", "mật-khẩu-xem-ban-đầu", store.RoleViewer)

	cookie, csrf := h.loginAs("nguoixem", "mật-khẩu-xem-ban-đầu")

	resp, body := h.doAs(cookie, csrf, http.MethodPost, "/api/v1/auth/password",
		map[string]string{
			"current_password": "mật-khẩu-xem-ban-đầu",
			"new_password":     "mật-khẩu-xem-về-sau",
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("viewer đổi mật khẩu = %d, muốn 200: %s", resp.StatusCode, body)
	}

	resp, body = h.doAs("", "", http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "nguoixem", "password": "mật-khẩu-xem-về-sau"})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("viewer đăng nhập bằng mật khẩu mới = %d: %s", resp.StatusCode, body)
	}
}

// Đổi mật khẩu là thao tác thay đổi trạng thái, nên phải nằm sau lớp CSRF.
func TestChangePasswordRequiresCSRFToken(t *testing.T) {
	h := newHarness(t)
	cookie, _ := h.loginAs("admin", "mật-khẩu-thử")

	resp, _ := h.doAs(cookie, "", http.MethodPost, "/api/v1/auth/password",
		map[string]string{
			"current_password": "mật-khẩu-thử",
			"new_password":     "mật-khẩu-mới-dài-hơn",
		})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("thiếu CSRF token = %d, muốn 403", resp.StatusCode)
	}
}
