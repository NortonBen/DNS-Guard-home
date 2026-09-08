package threat

import (
	"fmt"
	"net/netip"

	"github.com/benji/dnsguard/internal/enrich"
)

// Registry là tập các nguồn danh sách hạ tầng độc hại đang dùng.
//
// Thêm một nguồn = thêm một Set lúc dựng, không phải sửa thêm bốn nhánh switch rải
// khắp worker và API. Đó là ràng buộc thiết kế chính ở đây: nguồn đe dọa sẽ còn thay
// đổi — nguồn chết, nguồn mới xuất hiện — nên chi phí thay một nguồn phải rẻ.
type Registry struct {
	sources []*Set
	byKind  map[string]*Set
}

// NewRegistry dựng sổ đăng ký từ các nguồn, giữ nguyên thứ tự truyền vào.
//
// Thứ tự có nghĩa: Lookup trả về nguồn khớp đầu tiên, nên nguồn đáng tin hơn phải
// đứng trước để cảnh báo mang tên nó.
//
// Dựng sai thì dừng ngay lúc khởi động chứ không đợi tới lúc chạy: danh sách nguồn
// cố định lúc biên dịch, nên nguồn nil hay trùng khóa là lỗi lập trình. Trùng khóa
// đặc biệt nguy hiểm nếu để lọt — Lookup sẽ báo nguồn thứ nhất trong khi endpoint cập
// nhật ghi đè file của nguồn thứ hai.
func NewRegistry(sources ...*Set) *Registry {
	r := &Registry{sources: sources, byKind: make(map[string]*Set, len(sources))}
	for i, s := range sources {
		if s == nil {
			panic(fmt.Sprintf("threat: nguồn thứ %d là nil", i))
		}
		if _, dup := r.byKind[s.Name()]; dup {
			panic(fmt.Sprintf("threat: trùng khóa nguồn %q", s.Name()))
		}
		r.byKind[s.Name()] = s
	}
	return r
}

// Sources trả về các nguồn theo thứ tự đăng ký.
func (r *Registry) Sources() []*Set {
	if r == nil {
		return nil
	}
	return r.sources
}

// Get tra một nguồn theo khóa.
func (r *Registry) Get(kind string) (*Set, bool) {
	if r == nil {
		return nil, false
	}
	s, ok := r.byKind[kind]
	return s, ok
}

// Has cho biết khóa này có phải một nguồn đã đăng ký không.
func (r *Registry) Has(kind string) bool {
	_, ok := r.Get(kind)
	return ok
}

// Loaded cho biết có ít nhất một nguồn đã nạp được dữ liệu.
//
// "Ít nhất một" chứ không phải "tất cả": mỗi nguồn tải độc lập, và một nguồn chưa
// tải về không được làm câm những nguồn còn lại.
func (r *Registry) Loaded() bool {
	if r == nil {
		return false
	}
	for _, s := range r.sources {
		if s.Loaded() {
			return true
		}
	}
	return false
}

// Statuses trả về trạng thái mọi nguồn, theo thứ tự đăng ký.
func (r *Registry) Statuses() []enrich.TableStatus {
	if r == nil {
		return nil
	}
	out := make([]enrich.TableStatus, 0, len(r.sources))
	for _, s := range r.sources {
		out = append(out, s.Status())
	}
	return out
}

// Lookup tra một địa chỉ trong mọi nguồn, trả về khớp đầu tiên.
//
// Dừng ở nguồn đầu tiên chứ không gom hết: cảnh báo cần một câu trả lời hành động
// được ("khớp danh sách nào, dải nào"), và một địa chỉ nằm trong hai danh sách không
// nguy hiểm gấp đôi một địa chỉ nằm trong một danh sách.
func (r *Registry) Lookup(ip netip.Addr) (Match, bool) {
	if r == nil {
		return Match{}, false
	}
	for _, s := range r.sources {
		if m, ok := s.Lookup(ip); ok {
			return m, true
		}
	}
	return Match{}, false
}
