package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/benji/dnsguard/internal/publish"
	"github.com/benji/dnsguard/internal/store"
)

// splitCSV tách danh sách giá trị ngăn bằng dấu phẩy. Nhiều giá trị cho một tham số
// hiểu là OR; nhiều tham số khác nhau hiểu là AND.
func splitCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func atoiOr(raw string, fallback int) int {
	if v, err := strconv.Atoi(raw); err == nil {
		return v
	}
	return fallback
}

// parseSort đọc tham số dạng "score:desc". Chỉ lấy khóa đầu tiên: sắp xếp nhiều cấp
// không dùng được với phân trang con trỏ mà không làm con trỏ phức tạp hơn nhiều lần
// so với giá trị nó mang lại.
func parseSort(raw string) (field string, desc bool) {
	if raw == "" {
		return "score", true
	}
	first := strings.SplitN(raw, ",", 2)[0]
	parts := strings.SplitN(first, ":", 2)
	field = strings.TrimSpace(parts[0])
	desc = len(parts) < 2 || strings.EqualFold(strings.TrimSpace(parts[1]), "desc")
	return field, desc
}

// timeRange đọc khoảng thời gian từ query, mặc định 24 giờ gần nhất.
func timeRange(r *http.Request) (from, to string) {
	q := r.URL.Query()
	from, to = q.Get("from"), q.Get("to")
	if to == "" {
		to = store.TimeAt(time.Now())
	}
	if from == "" {
		from = store.TimeAt(time.Now().Add(-24 * time.Hour))
	}
	return from, to
}

// orEmpty đổi lát cắt nil thành lát cắt rỗng để JSON trả về [] chứ không phải null.
func orEmpty[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

// codeFor ánh xạ lỗi sang mã API, dùng trong các thao tác hàng loạt nơi từng phần
// tử có thể hỏng riêng.
func codeFor(err error) string {
	switch {
	case errors.Is(err, store.ErrDomainProtected):
		return CodeDomainProtected
	case errors.Is(err, store.ErrNotFound):
		return CodeNotFound
	case errors.Is(err, store.ErrAlreadyExists):
		return CodeDuplicate
	case errors.Is(err, publish.ErrPublishBlocked):
		return CodePublishBlocked
	default:
		return CodeInternal
	}
}

// validName kiểm tra tên miền theo RFC trước khi lưu.
func validName(name string) bool {
	if len(name) == 0 || len(name) > 253 || !strings.Contains(name, ".") {
		return false
	}
	if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
				return false
			}
		}
	}
	return true
}
