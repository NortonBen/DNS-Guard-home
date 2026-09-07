package enrich

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// vtServer dựng một VirusTotal giả trả về mã trạng thái cho trước.
func vtServer(t *testing.T, status int, body string) (*VTEnricher, *[]string) {
	t.Helper()

	var mu sync.Mutex
	seenKeys := []string{}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seenKeys = append(seenKeys, r.Header.Get("x-apikey"))
		mu.Unlock()

		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)

	e := NewVirusTotal("")
	e.baseURL = ts.URL + "/"
	return e, &seenKeys
}

func TestVerifyKeyAcceptsWorkingKey(t *testing.T) {
	e, keys := vtServer(t, http.StatusOK, `{"data":{}}`)

	if err := e.VerifyKey(context.Background(), "a1b2"); err != nil {
		t.Fatalf("khóa hợp lệ bị từ chối: %v", err)
	}
	if len(*keys) != 1 || (*keys)[0] != "a1b2" {
		t.Errorf("khóa gửi lên = %v, muốn đúng khóa đang kiểm tra", *keys)
	}
}

func TestVerifyKeyRejectsBadKey(t *testing.T) {
	e, _ := vtServer(t, http.StatusUnauthorized, `{}`)

	if err := e.VerifyKey(context.Background(), "sai"); !errors.Is(err, ErrVTBadKey) {
		t.Errorf("lỗi = %v, muốn ErrVTBadKey", err)
	}
}

func TestVerifyKeyAcceptsKeyThatIsOutOfQuota(t *testing.T) {
	// Hết quota nghĩa là VirusTotal đã nhận ra khóa — nó phải nhận ra mới đếm được.
	// Coi đây là khóa sai thì người dùng bị chặn không lưu được một khóa hoàn toàn tốt.
	e, _ := vtServer(t, http.StatusTooManyRequests, `{}`)

	if err := e.VerifyKey(context.Background(), "het-quota"); err != nil {
		t.Errorf("khóa hết quota bị từ chối: %v", err)
	}
}

func TestVerifyKeyRefusesEmptyWithoutCallingOut(t *testing.T) {
	e, keys := vtServer(t, http.StatusOK, `{}`)

	if err := e.VerifyKey(context.Background(), ""); !errors.Is(err, ErrVTNoAPIKey) {
		t.Errorf("lỗi = %v, muốn ErrVTNoAPIKey", err)
	}
	if len(*keys) != 0 {
		t.Error("đã gọi ra ngoài dù chưa có khóa")
	}
}

func TestSetAPIKeyTakesEffectOnNextLookup(t *testing.T) {
	// Đổi khóa trên giao diện phải có tác dụng ngay, không đợi khởi động lại.
	e, keys := vtServer(t, http.StatusOK, `{"data":{"attributes":{}}}`)

	if _, err := e.Enrich(context.Background(), "vidu.vn"); !errors.Is(err, ErrVTNoAPIKey) {
		t.Fatalf("chưa có khóa mà vẫn tra: %v", err)
	}

	e.SetAPIKey("khoa-moi")
	if !e.Configured() {
		t.Fatal("Configured vẫn báo chưa có khóa sau khi đặt")
	}
	if _, err := e.Enrich(context.Background(), "vidu.vn"); err != nil {
		t.Fatalf("tra cứu sau khi đặt khóa: %v", err)
	}
	if len(*keys) != 1 || (*keys)[0] != "khoa-moi" {
		t.Errorf("khóa gửi lên = %v, muốn khóa mới", *keys)
	}

	e.SetAPIKey("")
	if e.Configured() {
		t.Error("Configured vẫn báo có khóa sau khi gỡ")
	}
}

func TestKeyHintNeverExposesEnoughToReuse(t *testing.T) {
	e := NewVirusTotal("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")

	hint := e.KeyHint()
	if len(hint) != 4 {
		t.Fatalf("gợi ý dài %d ký tự, muốn 4", len(hint))
	}
	if hint != "cdef" {
		t.Errorf("gợi ý = %q, muốn bốn ký tự cuối", hint)
	}

	// Khóa quá ngắn thì ngay cả bốn ký tự cuối cũng là phần đáng kể của nó.
	short := NewVirusTotal("abc123")
	if got := short.KeyHint(); got != "" {
		t.Errorf("gợi ý cho khóa ngắn = %q, muốn rỗng", got)
	}
}

func TestUnwrapReachesConcreteEnricher(t *testing.T) {
	// Không có Unwrap thì không đổi được khóa lúc chạy: registry chỉ đưa ra lớp bọc.
	r := NewRegistry(nil)
	vt := NewVirusTotal("")
	r.Register(vt, 1, 1, false)

	src, ok := r.Get("vt")
	if !ok {
		t.Fatal("không tìm thấy nguồn vt")
	}
	if _, ok := src.(*VTEnricher); ok {
		t.Fatal("registry trả thẳng nguồn gốc — lớp bọc giới hạn tốc độ đã mất")
	}
	if Unwrap(src) != vt {
		t.Error("Unwrap không trả về đúng nguồn gốc")
	}
}
