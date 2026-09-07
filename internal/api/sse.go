package api

import (
	"fmt"
	"net/http"
	"time"
)

// handleEvents đẩy sự kiện về giao diện qua SSE.
//
// Chọn SSE thay vì WebSocket: luồng một chiều, trình duyệt tự kết nối lại, đi qua
// proxy dễ dàng, và không cần thư viện phía client.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, CodeInternal,
			"Máy chủ không hỗ trợ luồng sự kiện", nil)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Tắt đệm ở proxy: có đệm thì sự kiện tới nơi theo cụm chứ không theo thời gian thực.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	stream, unsubscribe := s.bus.Subscribe()
	defer unsubscribe()

	// Nhịp giữ kết nối: mạng và proxy thường đóng kết nối im lặng quá lâu.
	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return

		case ev, ok := <-stream:
			if !ok {
				return
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Kind, ev.Data); err != nil {
				return
			}
			flusher.Flush()

		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
