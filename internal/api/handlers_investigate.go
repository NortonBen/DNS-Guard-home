package api

import (
	"net/http"
	"net/netip"
)

// investigateLimit là số dòng tối đa trả về cho mỗi phần của một vụ điều tra.
const investigateLimit = 200

// handleInvestigateIP trả về mọi thứ hệ thống biết về một địa chỉ: những domain đã
// trỏ tới nó, và những lượt truy vấn rơi vào khoảng thời gian ánh xạ đó có hiệu lực.
//
// Địa chỉ đi qua tham số truy vấn chứ không phải đoạn đường dẫn: địa chỉ IPv6 chứa
// dấu hai chấm, và nhét chúng vào đường dẫn là chuốc lấy rắc rối ở mọi tầng proxy
// nằm giữa.
func (s *Server) handleInvestigateIP(w http.ResponseWriter, r *http.Request) {
	addr, err := netip.ParseAddr(r.URL.Query().Get("addr"))
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidInput, "Địa chỉ IP không hợp lệ", nil)
		return
	}
	// Chuẩn hoá qua netip trước khi tra: cùng một địa chỉ IPv6 viết được nhiều cách,
	// mà trong CSDL nó đã được ghi bằng đúng dạng chuẩn này.
	ip := addr.String()
	ctx := r.Context()

	domains, err := s.store.DomainsByIP(ctx, ip, investigateLimit)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	accesses, err := s.store.AccessesByIP(ctx, ip, investigateLimit)
	if err != nil {
		fail(w, s.log, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ip":       ip,
		"domains":  orEmpty(domains),
		"accesses": orEmpty(accesses),
	})
}
