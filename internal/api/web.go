package api

import (
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// handleWeb phục vụ ứng dụng React đã build, nhúng sẵn trong binary.
//
// Mọi đường dẫn không khớp API đều trả về index.html để định tuyến phía client hoạt
// động: người dùng mở thẳng /domains/4821 phải thấy trang chi tiết chứ không phải 404.
func (s *Server) handleWeb(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, CodeNotFound, "Không hỗ trợ", nil)
		return
	}
	// Đường dẫn API không khớp phải trả JSON, không phải trang HTML: client gọi API
	// cần lỗi có cấu trúc để xử lý.
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, http.StatusNotFound, CodeNotFound, "Không tìm thấy endpoint", nil)
		return
	}

	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "" || name == "." {
		name = "index.html"
	}

	file, err := s.webFS.Open(name)
	if err != nil {
		// Đường dẫn của ứng dụng một trang: trả về index.html để React tự định tuyến.
		file, err = s.webFS.Open("index.html")
		if err != nil {
			writeError(w, http.StatusNotFound, CodeNotFound, "Chưa build giao diện", nil)
			return
		}
		name = "index.html"
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil || info.IsDir() {
		writeError(w, http.StatusNotFound, CodeNotFound, "Không tìm thấy", nil)
		return
	}

	// Tài nguyên có băm trong tên là bất biến, cache lâu được. index.html thì không:
	// nó trỏ tới các tài nguyên đó, nên phải luôn kiểm tra lại sau mỗi lần phát hành.
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}

	seeker, ok := file.(io.ReadSeeker)
	if !ok {
		w.Header().Set("Content-Type", contentTypeOf(name))
		io.Copy(w, file)
		return
	}
	http.ServeContent(w, r, name, info.ModTime(), seeker)
}

func contentTypeOf(name string) string {
	switch path.Ext(name) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".js":
		return "application/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".json":
		return "application/json"
	default:
		return "application/octet-stream"
	}
}

var _ fs.FS
