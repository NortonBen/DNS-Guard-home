package auth

import (
	"strings"
	"testing"
)

func TestHashPasswordRoundTrip(t *testing.T) {
	const password = "mật-khẩu-rất-dài-và-khó-đoán-42"

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !VerifyPassword(password, hash) {
		t.Error("mật khẩu đúng không xác thực được")
	}
	if VerifyPassword(password+"x", hash) {
		t.Error("mật khẩu sai lại xác thực thành công")
	}
	if strings.Contains(hash, password) {
		t.Error("chuỗi băm chứa mật khẩu gốc")
	}
}

// Hai lần băm cùng một mật khẩu phải cho kết quả khác nhau: muối ngẫu nhiên là thứ
// khiến bảng cầu vồng vô dụng. Nếu chúng giống nhau thì muối không được sinh.
func TestHashPasswordUsesRandomSalt(t *testing.T) {
	first, err := HashPassword("cùng-một-mật-khẩu")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	second, err := HashPassword("cùng-một-mật-khẩu")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if first == second {
		t.Error("hai lần băm cho kết quả giống nhau — muối không ngẫu nhiên")
	}
	if !VerifyPassword("cùng-một-mật-khẩu", first) || !VerifyPassword("cùng-một-mật-khẩu", second) {
		t.Error("cả hai chuỗi băm đều phải xác thực được")
	}
}

// Chuỗi băm nhúng tham số của chính nó, để sau này tăng chi phí mà vẫn xác thực
// được mật khẩu đã băm bằng tham số cũ.
func TestHashEmbedsItsOwnParameters(t *testing.T) {
	hash, err := HashPassword("thử")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	for _, want := range []string{"$argon2id$", "m=65536", "t=2", "p=1"} {
		if !strings.Contains(hash, want) {
			t.Errorf("chuỗi băm thiếu %q: %s", want, hash)
		}
	}
}

// Chuỗi băm hỏng phải bị từ chối gọn gàng chứ không panic: dữ liệu trong CSDL có
// thể bị hỏng, và một panic ở đường đăng nhập sẽ làm sập cả dịch vụ.
func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	malformed := []string{
		"",
		"không-phải-băm",
		"$argon2id$v=19$m=65536,t=2,p=1$muối$",
		"$argon2id$v=19$thiếu-tham-số$muối$khóa",
		"$bcrypt$v=19$m=65536,t=2,p=1$c2FsdA$a2V5",
		"$argon2id$v=19$m=65536,t=2,p=1$!!!không-base64!!!$a2V5",
	}
	for _, hash := range malformed {
		if VerifyPassword("bất-kỳ", hash) {
			t.Errorf("chuỗi băm hỏng %q lại xác thực thành công", hash)
		}
	}
}

func TestHashTokenIsStableAndOpaque(t *testing.T) {
	const token = "một-token-phiên"

	first, second := HashToken(token), HashToken(token)
	if first != second {
		t.Error("băm token không ổn định")
	}
	if strings.Contains(first, token) {
		t.Error("băm token chứa token gốc")
	}
	if HashToken(token) == HashToken(token+"x") {
		t.Error("hai token khác nhau cho cùng một băm")
	}
	// SHA-256 dạng hex: 64 ký tự.
	if len(first) != 64 {
		t.Errorf("độ dài băm = %d, muốn 64", len(first))
	}
}
