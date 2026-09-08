package enrich

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// writeZip đóng gói một file duy nhất vào ZIP, trả về đường dẫn.
func writeZip(t *testing.T, name, inner, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("tạo %s: %v", name, err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	w, err := zw.Create(inner)
	if err != nil {
		t.Fatalf("tạo mục zip: %v", err)
	}
	if _, err := w.Write([]byte(body)); err != nil {
		t.Fatalf("ghi mục zip: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("đóng zip: %v", err)
	}
	return path
}

func readAll(t *testing.T, path, inner string) string {
	t.Helper()
	r, closer, err := OpenMaybeZip(path, inner)
	if err != nil {
		t.Fatalf("OpenMaybeZip(%s): %v", filepath.Base(path), err)
	}
	defer closer()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("đọc %s: %v", filepath.Base(path), err)
	}
	return string(b)
}

// Đây là ca đã làm hỏng mọi lần cập nhật nguồn ZIP qua giao diện: RefreshTable tải về
// file tạm tên ".tmp-tranco.csv.zip-1234567", nên đuôi ".zip" nằm giữa tên chứ không
// ở cuối. Nhận dạng theo đuôi thì file nén bị đọc như văn bản trần, ra 0 mục, và lỗi
// hiện ra dưới dạng "không đọc được nội dung" chứ không chỉ tới nguyên nhân thật.
func TestOpenMaybeZipDetectsByContentNotName(t *testing.T) {
	path := writeZip(t, ".tmp-tranco.csv.zip-1234567", "top-1m.csv", "1,google.com\n")

	if got := readAll(t, path, ".csv"); got != "1,google.com\n" {
		t.Errorf("đọc ra %q; muốn nội dung đã giải nén", got)
	}
}

func TestOpenMaybeZipReadsPlainFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "drop.txt")
	if err := os.WriteFile(path, []byte("45.66.0.0/16\n"), 0o644); err != nil {
		t.Fatalf("ghi file: %v", err)
	}

	if got := readAll(t, path, ".txt"); got != "45.66.0.0/16\n" {
		t.Errorf("đọc ra %q; muốn nội dung nguyên bản", got)
	}
}

// File rỗng ngắn hơn cả bốn byte nhận dạng. Không được coi là ZIP, và không được
// hỏng — người gọi tự có guard từ chối nội dung rỗng.
func TestOpenMaybeZipHandlesShortFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.txt")
	if err := os.WriteFile(path, []byte("ab"), 0o644); err != nil {
		t.Fatalf("ghi file: %v", err)
	}

	if got := readAll(t, path, ".txt"); got != "ab" {
		t.Errorf("đọc ra %q; muốn \"ab\"", got)
	}
}

func TestOpenMaybeZipReportsMissingInnerFile(t *testing.T) {
	path := writeZip(t, "data.zip", "readme.md", "khong phai du lieu\n")

	if _, closer, err := OpenMaybeZip(path, ".csv"); err == nil {
		closer()
		t.Error("nhận zip không có file .csv, muốn báo lỗi")
	}
}
