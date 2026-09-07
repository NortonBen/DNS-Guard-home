// Package web nhúng giao diện đã biên dịch vào binary.
//
// Một binary duy nhất chứa cả API lẫn giao diện: không có bước cài đặt riêng cho
// frontend, không có thư mục tĩnh phải đồng bộ, và không có khả năng giao diện lệch
// phiên bản so với API.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:assets
var bundled embed.FS

// FS trả về hệ thống file của giao diện.
//
// Trả về nil khi giao diện chưa được biên dịch, để backend vẫn chạy được API và
// endpoint danh sách — hữu ích khi phát triển với Vite dev server chạy riêng.
func FS() fs.FS {
	sub, err := fs.Sub(bundled, "assets")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}
