package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// seedSession mở một phiên giả cho tài khoản, đủ để đếm phiên bị hủy.
func seedSession(t *testing.T, s *Store, tokenHash string, userID int64) {
	t.Helper()
	err := s.CreateSession(context.Background(), tokenHash, userID, "csrf",
		time.Now().Add(time.Hour), "test", "127.0.0.1")
	if err != nil {
		t.Fatalf("create session %q: %v", tokenHash, err)
	}
}

func TestUpdatePasswordChangesOnlyTheTargetAccount(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	alice, err := s.CreateUser(ctx, "alice", "băm-của-alice", RoleAdmin)
	if err != nil {
		t.Fatalf("create alice: %v", err)
	}
	bob, err := s.CreateUser(ctx, "bob", "băm-của-bob", RoleViewer)
	if err != nil {
		t.Fatalf("create bob: %v", err)
	}

	if err := s.UpdatePassword(ctx, alice.ID, "băm-mới-của-alice"); err != nil {
		t.Fatalf("update password: %v", err)
	}

	if _, hash, err := s.UserByID(ctx, alice.ID); err != nil || hash != "băm-mới-của-alice" {
		t.Errorf("băm của alice = %q (err %v), muốn băm mới", hash, err)
	}
	if _, hash, err := s.UserByID(ctx, bob.ID); err != nil || hash != "băm-của-bob" {
		t.Errorf("băm của bob = %q (err %v), muốn nguyên vẹn", hash, err)
	}
}

// Đổi mật khẩu cho một id không tồn tại phải báo không tìm thấy chứ không im lặng
// thành công: người gọi sẽ tưởng đã đổi được.
func TestUpdatePasswordOnMissingUserReportsNotFound(t *testing.T) {
	s := newTestStore(t)

	if err := s.UpdatePassword(context.Background(), 9999, "băm"); !errors.Is(err, ErrNotFound) {
		t.Errorf("update password cho id lạ = %v, muốn ErrNotFound", err)
	}
}

// DeleteUserSessions là đường quản trị viên đặt lại mật khẩu hộ: nó phải quét sạch
// phiên của đúng tài khoản đó và không đụng vào tài khoản khác.
func TestDeleteUserSessionsClearsOnlyThatAccount(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	alice, err := s.CreateUser(ctx, "alice", "băm", RoleAdmin)
	if err != nil {
		t.Fatalf("create alice: %v", err)
	}
	bob, err := s.CreateUser(ctx, "bob", "băm", RoleViewer)
	if err != nil {
		t.Fatalf("create bob: %v", err)
	}

	seedSession(t, s, "alice-1", alice.ID)
	seedSession(t, s, "alice-2", alice.ID)
	seedSession(t, s, "bob-1", bob.ID)

	n, err := s.DeleteUserSessions(ctx, alice.ID)
	if err != nil {
		t.Fatalf("delete user sessions: %v", err)
	}
	if n != 2 {
		t.Errorf("số phiên bị hủy = %d, muốn 2", n)
	}

	if _, err := s.SessionByToken(ctx, "alice-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("phiên của alice vẫn còn: %v", err)
	}
	if _, err := s.SessionByToken(ctx, "bob-1"); err != nil {
		t.Errorf("phiên của bob bị hủy oan: %v", err)
	}
}

func TestDeleteUserSessionsExceptKeepsTheCallingSession(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	alice, err := s.CreateUser(ctx, "alice", "băm", RoleAdmin)
	if err != nil {
		t.Fatalf("create alice: %v", err)
	}
	seedSession(t, s, "giữ-lại", alice.ID)
	seedSession(t, s, "hủy-đi", alice.ID)

	n, err := s.DeleteUserSessionsExcept(ctx, alice.ID, "giữ-lại")
	if err != nil {
		t.Fatalf("delete other sessions: %v", err)
	}
	if n != 1 {
		t.Errorf("số phiên bị hủy = %d, muốn 1", n)
	}
	if _, err := s.SessionByToken(ctx, "giữ-lại"); err != nil {
		t.Errorf("phiên đang gọi bị hủy oan: %v", err)
	}
	if _, err := s.SessionByToken(ctx, "hủy-đi"); !errors.Is(err, ErrNotFound) {
		t.Errorf("phiên còn lại vẫn sống: %v", err)
	}
}
