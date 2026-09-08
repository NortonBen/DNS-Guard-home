package api

import (
	"net/http"
)

// threatListLimit là số cảnh báo tối đa trả về một lần.
const threatListLimit = 200

// beaconMaxCV là ngưỡng hệ số biến thiên khoảng cách truy vấn: dưới ngưỡng nghĩa là
// nhịp đều bất thường, dấu hiệu của kênh điều khiển tự động chứ không phải người dùng.
//
// Lặp lại giá trị của package classify thay vì xuất khẩu nó ra: ở đây nó chỉ là cách
// gắn nhãn một dòng trong danh sách cảnh báo, không tham gia tính điểm. Buộc hai chỗ
// dùng chung một hằng sẽ tạo ràng buộc giữa hai thứ không thật sự liên quan.
const beaconMaxCV = 0.35

// handleListThreats trả về các domain đã phân giải tới địa chỉ nằm trong danh sách
// hạ tầng độc hại.
//
// Không tự chặn gì cả. Danh sách theo IP dễ dương tính giả hơn danh sách theo domain —
// một địa chỉ dùng chung có thể phục vụ cả thứ hợp pháp — nên quyết định vẫn thuộc về
// người vận hành, đúng như mọi quyết định chặn khác trong hệ thống.
func (s *Server) handleListThreats(w http.ResponseWriter, r *http.Request) {
	matches, err := s.store.ThreatMatches(r.Context(), beaconMaxCV, threatListLimit)
	if err != nil {
		fail(w, s.log, err)
		return
	}

	loaded := s.threats.Loaded()
	writeJSON(w, http.StatusOK, map[string]any{
		"matches": orEmpty(matches),
		// Danh sách rỗng có hai nguyên nhân trái ngược nhau: không có gì đáng báo,
		// hoặc chưa tải danh sách về nên không thể báo. Giao diện phải phân biệt được.
		"list_loaded": loaded,
	})
}
