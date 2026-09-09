// Package auth lo mật khẩu, phiên và phân quyền.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"

	"github.com/benji/dnsguard/internal/store"
)

// ErrInvalidCredentials là lỗi duy nhất trả về khi đăng nhập hỏng, bất kể nguyên
// nhân là sai tên hay sai mật khẩu. Phân biệt hai trường hợp sẽ để lộ tài khoản nào
// tồn tại.
var ErrInvalidCredentials = errors.New("invalid credentials")

// Lỗi khi đổi mật khẩu. Tách khỏi ErrInvalidCredentials vì người gọi cần phân biệt
// "mật khẩu mới không đạt" với "mật khẩu hiện tại sai" để báo đúng chỗ trên biểu mẫu.
var (
	ErrWeakPassword = errors.New("password too short")
	ErrSamePassword = errors.New("new password matches current")
)

// MinPasswordLen là độ dài tối thiểu của mật khẩu do người dùng tự đặt.
//
// Argon2id đã làm việc dò từng mật khẩu tốn kém, nên phòng tuyến còn lại chỉ cần
// chặn những mật khẩu ngắn tới mức vét cạn được bất chấp chi phí băm.
const MinPasswordLen = 12

// Tham số argon2id theo khuyến nghị OWASP. 64 MB bộ nhớ là ngưỡng cân bằng: đủ để
// tấn công từ điển tốn kém, nhưng vẫn nằm trong ngân sách RAM của một Raspberry Pi.
const (
	argonTime    = 2
	argonMemory  = 64 * 1024
	argonThreads = 1
	argonKeyLen  = 32
	saltLen      = 16
)

// HashPassword băm mật khẩu bằng argon2id, trả về chuỗi mã hóa đầy đủ tham số.
//
// Nhúng tham số vào chuỗi băm để sau này tăng chi phí mà vẫn xác thực được mật khẩu
// đã băm bằng tham số cũ.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword kiểm tra mật khẩu với chuỗi băm đã lưu.
func VerifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}

	var memory, iterations uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}

	got := argon2.IDKey([]byte(password), salt, iterations, memory, threads, uint32(len(want)))
	// So sánh thời gian hằng định: so sánh thường sẽ rò rỉ độ dài tiền tố khớp.
	return subtle.ConstantTimeCompare(got, want) == 1
}

// Service dựng và kiểm tra phiên.
type Service struct {
	store *store.Store
	ttl   time.Duration
}

// NewService dựng dịch vụ xác thực.
func NewService(s *store.Store, ttl time.Duration) *Service {
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour
	}
	return &Service{store: s, ttl: ttl}
}

// Login xác thực và tạo phiên. Trả về token gốc để đặt vào cookie.
func (s *Service) Login(ctx context.Context, username, password, userAgent, ip string) (
	user store.User, token, csrf string, expiresAt time.Time, err error) {

	u, hash, err := s.store.UserByName(ctx, username)
	if err != nil || !u.Active {
		// Vẫn băm một lần khi không tìm thấy tài khoản: nếu không, thời gian phản
		// hồi sẽ tiết lộ tài khoản nào tồn tại.
		_, _ = HashPassword(password)
		return store.User{}, "", "", time.Time{}, ErrInvalidCredentials
	}
	if !VerifyPassword(password, hash) {
		return store.User{}, "", "", time.Time{}, ErrInvalidCredentials
	}

	token, err = randomToken()
	if err != nil {
		return store.User{}, "", "", time.Time{}, err
	}
	csrf, err = randomToken()
	if err != nil {
		return store.User{}, "", "", time.Time{}, err
	}

	expiresAt = time.Now().Add(s.ttl)
	if err := s.store.CreateSession(ctx, HashToken(token), u.ID, csrf, expiresAt, userAgent, ip); err != nil {
		return store.User{}, "", "", time.Time{}, err
	}
	if err := s.store.TouchLogin(ctx, u.ID); err != nil {
		return store.User{}, "", "", time.Time{}, err
	}
	return u, token, csrf, expiresAt, nil
}

// Session tra phiên từ token gốc.
func (s *Service) Session(ctx context.Context, token string) (store.Session, error) {
	return s.store.SessionByToken(ctx, HashToken(token))
}

// Logout hủy phiên.
func (s *Service) Logout(ctx context.Context, token string) error {
	return s.store.DeleteSession(ctx, HashToken(token))
}

// HashToken băm token phiên. CSDL chỉ lưu băm, không bao giờ lưu token gốc.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ValidatePassword kiểm tra mật khẩu mới có đạt chính sách không.
func ValidatePassword(password string) error {
	// Đếm theo rune chứ không theo byte: mật khẩu tiếng Việt có dấu tốn 2-3 byte mỗi
	// ký tự, đếm byte sẽ cho qua một mật khẩu ngắn hơn ý định của chính sách.
	if utf8.RuneCountInString(password) < MinPasswordLen {
		return ErrWeakPassword
	}
	return nil
}

// ChangePassword đổi mật khẩu sau khi xác minh mật khẩu hiện tại, rồi hủy mọi phiên
// khác của tài khoản đó.
//
// keepTokenHash là băm token của phiên đang gọi: nó sống sót để người dùng không bị
// đá ra ngay khi vừa đổi mật khẩu thành công.
// Trả về số phiên khác đã bị hủy, để giao diện nói rõ vừa có bao nhiêu thiết bị bị
// đăng xuất — người dùng cần thấy hệ quả đó chứ không chỉ thấy "đã đổi".
func (s *Service) ChangePassword(ctx context.Context, userID int64, current, next, keepTokenHash string) (int64, error) {
	if err := ValidatePassword(next); err != nil {
		return 0, err
	}

	u, hash, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return 0, err
	}
	if !u.Active {
		return 0, ErrInvalidCredentials
	}
	if !VerifyPassword(current, hash) {
		return 0, ErrInvalidCredentials
	}
	// Chặn sau khi đã xác minh mật khẩu cũ: kiểm tra trước sẽ cho biết mật khẩu đoán
	// có trùng mật khẩu thật hay không mà không cần biết mật khẩu hiện tại.
	if VerifyPassword(next, hash) {
		return 0, ErrSamePassword
	}

	newHash, err := HashPassword(next)
	if err != nil {
		return 0, err
	}
	if err := s.store.UpdatePassword(ctx, userID, newHash); err != nil {
		return 0, err
	}
	revoked, err := s.store.DeleteUserSessionsExcept(ctx, userID, keepTokenHash)
	if err != nil {
		return 0, err
	}
	return revoked, nil
}
