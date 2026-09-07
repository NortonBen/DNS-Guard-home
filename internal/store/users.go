package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// User là một tài khoản.
type User struct {
	ID          int64   `json:"id"`
	Username    string  `json:"username"`
	Role        string  `json:"role"`
	Active      bool    `json:"active"`
	LastLoginAt *string `json:"last_login_at,omitempty"`
}

// Vai trò.
const (
	RoleAdmin  = "admin"
	RoleViewer = "viewer"
)

// Session là một phiên đăng nhập đang hoạt động.
type Session struct {
	UserID    int64
	Username  string
	Role      string
	CSRFToken string
	ExpiresAt time.Time
}

// CreateUser tạo tài khoản với mật khẩu đã băm sẵn.
func (s *Store) CreateUser(ctx context.Context, username, passwordHash, role string) (User, error) {
	res, err := s.w.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, role, created_at) VALUES (?, ?, ?, ?)`,
		username, passwordHash, role, Now())
	if err != nil {
		return User{}, fmt.Errorf("create user %q: %w", username, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return User{}, fmt.Errorf("user id: %w", err)
	}
	return User{ID: id, Username: username, Role: role, Active: true}, nil
}

// UserByName trả về tài khoản kèm băm mật khẩu để xác thực.
func (s *Store) UserByName(ctx context.Context, username string) (User, string, error) {
	var u User
	var hash string
	err := s.r.QueryRowContext(ctx,
		`SELECT id, username, role, active, last_login_at, password_hash FROM users WHERE username = ?`,
		username).Scan(&u.ID, &u.Username, &u.Role, &u.Active, &u.LastLoginAt, &hash)
	if err == sql.ErrNoRows {
		return User{}, "", ErrNotFound
	}
	if err != nil {
		return User{}, "", fmt.Errorf("lookup user %q: %w", username, err)
	}
	return u, hash, nil
}

// CountUsers đếm số tài khoản, dùng để quyết định có cần tạo admin đầu tiên không.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	if err := s.r.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return n, nil
}

// TouchLogin ghi nhận lần đăng nhập thành công.
func (s *Store) TouchLogin(ctx context.Context, userID int64) error {
	if _, err := s.w.ExecContext(ctx,
		`UPDATE users SET last_login_at = ? WHERE id = ?`, Now(), userID); err != nil {
		return fmt.Errorf("touch login: %w", err)
	}
	return nil
}

// CreateSession lưu một phiên. tokenHash là băm của token thật.
//
// Lưu băm chứ không lưu token gốc: rò rỉ CSDL không cho phép mạo danh phiên đang
// hoạt động.
func (s *Store) CreateSession(ctx context.Context, tokenHash string, userID int64,
	csrf string, expiresAt time.Time, userAgent, ip string) error {

	_, err := s.w.ExecContext(ctx, `
		INSERT INTO sessions (token, user_id, csrf_token, expires_at, created_at, user_agent, ip)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		tokenHash, userID, csrf, TimeAt(expiresAt), Now(), userAgent, ip)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// SessionByToken tra phiên theo băm token, trả ErrNotFound nếu hết hạn.
func (s *Store) SessionByToken(ctx context.Context, tokenHash string) (Session, error) {
	var sess Session
	var expires string
	err := s.r.QueryRowContext(ctx, `
		SELECT s.user_id, u.username, u.role, s.csrf_token, s.expires_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token = ? AND s.expires_at > ? AND u.active = 1`,
		tokenHash, Now()).Scan(&sess.UserID, &sess.Username, &sess.Role, &sess.CSRFToken, &expires)
	if err == sql.ErrNoRows {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("lookup session: %w", err)
	}
	sess.ExpiresAt, _ = ParseTime(expires)
	return sess, nil
}

// DeleteSession xóa một phiên khi đăng xuất.
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	if _, err := s.w.ExecContext(ctx, `DELETE FROM sessions WHERE token = ?`, tokenHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// PruneSessions xóa phiên hết hạn.
func (s *Store) PruneSessions(ctx context.Context) (int64, error) {
	res, err := s.w.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, Now())
	if err != nil {
		return 0, fmt.Errorf("prune sessions: %w", err)
	}
	return res.RowsAffected()
}
