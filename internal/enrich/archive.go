package enrich

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
)

// zipMagic là bốn byte đầu của mọi file ZIP có nội dung ("PK\x03\x04").
var zipMagic = []byte{'P', 'K', 0x03, 0x04}

// OpenMaybeZip mở một file, tự giải nén nếu đó là ZIP.
//
// Tồn tại vì hai nguồn dữ liệu lớn nhất đều phát hành dạng ZIP một-file — Tranco và
// bản đầy đủ của ThreatFox — trong khi mọi thứ khác là văn bản trần.
//
// Nhận dạng bằng **bốn byte đầu file, không phải đuôi tên file**. Đuôi tên không dùng
// được ở đây: RefreshTable tải về file tạm tên ".tmp-tranco.csv.zip-1234567", tức đuôi
// nằm giữa tên. Đoán theo đuôi thì mọi lần cập nhật nguồn ZIP qua giao diện đều đọc
// nội dung nén như văn bản trần và hỏng — im lặng, vì tải về vẫn thành công.
//
// innerSuffix chọn file bên trong ZIP, ví dụ ".csv". Trả về file đầu tiên khớp: các
// nguồn này đóng gói đúng một file dữ liệu, và tên của nó thay đổi theo ngày phát
// hành nên không khoá cứng được.
//
// Người gọi phải gọi hàm dọn dẹp trả về, kể cả khi không đọc hết.
func OpenMaybeZip(path, innerSuffix string) (io.Reader, func(), error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("mở %q: %w", path, err)
	}

	var head [4]byte
	n, _ := io.ReadFull(f, head[:])
	if n < len(zipMagic) || !bytes.Equal(head[:], zipMagic) {
		// Không phải ZIP: tua về đầu và trả nguyên file.
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			f.Close()
			return nil, nil, fmt.Errorf("tua lại %q: %w", path, err)
		}
		return f, func() { f.Close() }, nil
	}
	// zip.OpenReader tự mở lại file theo đường dẫn, nên không dùng handle này nữa.
	f.Close()

	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, nil, fmt.Errorf("mở %q: %w", path, err)
	}
	for _, zf := range zr.File {
		if !strings.HasSuffix(zf.Name, innerSuffix) {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			zr.Close()
			return nil, nil, fmt.Errorf("mở %q trong zip: %w", zf.Name, err)
		}
		return rc, func() { rc.Close(); zr.Close() }, nil
	}
	zr.Close()
	return nil, nil, fmt.Errorf("không tìm thấy file %q trong %q", innerSuffix, path)
}
