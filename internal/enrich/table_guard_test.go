package enrich

import (
	"archive/zip"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
)

// Ba thứ mà một URL sai trả về trong thực tế. Cả ba đều tải xuống "thành công", nên
// nếu LoadTable cũng nhận chúng thì RefreshTable sẽ ghi đè bảng đang dùng bằng rác —
// và giao diện báo xanh với 0 mục thay vì báo hỏng.
var badPayloads = map[string]string{
	"rỗng":             "",
	"trang lỗi":        "<!DOCTYPE html><html><body>404 Not Found</body></html>",
	"chỉ khoảng trắng": "\n\n   \n",
}

func writeTemp(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("ghi %s: %v", name, err)
	}
	return path
}

func TestASNRejectsUnusableTable(t *testing.T) {
	for name, body := range badPayloads {
		t.Run(name, func(t *testing.T) {
			e := NewASN(nil)
			if err := e.LoadTable(writeTemp(t, "ip2asn.tsv", body)); err == nil {
				t.Fatalf("nhận bảng %s, muốn báo lỗi", name)
			}
			if e.Loaded() {
				t.Error("đánh dấu đã nạp dù bảng không dùng được")
			}
		})
	}
}

func TestRankRejectsUnusableTable(t *testing.T) {
	for name, body := range badPayloads {
		t.Run(name, func(t *testing.T) {
			e := NewRank()
			if err := e.LoadTable(writeTemp(t, "tranco.csv", body)); err == nil {
				t.Fatalf("nhận bảng %s, muốn báo lỗi", name)
			}
			if e.Loaded() {
				t.Error("đánh dấu đã nạp dù bảng không dùng được")
			}
		})
	}
}

// Định dạng đúng thì vẫn phải nạp được — guard không được chặn nhầm bảng thật.
func TestASNAcceptsWellFormedTable(t *testing.T) {
	body := "1.0.0.0\t1.0.0.255\t13335\tUS\tCLOUDFLARENET\n" +
		"8.8.8.0\t8.8.8.255\t15169\tUS\tGOOGLE\n"

	e := NewASN(nil)
	if err := e.LoadTable(writeTemp(t, "ip2asn.tsv", body)); err != nil {
		t.Fatalf("từ chối bảng hợp lệ: %v", err)
	}
	if got := e.Status().Entries; got != 2 {
		t.Fatalf("nạp %d dải, muốn 2", got)
	}
	facts, ok := e.LookupIP(netip.MustParseAddr("8.8.8.8"))
	if !ok || facts.ASN != 15169 {
		t.Errorf("tra 8.8.8.8 = %+v (ok=%v); muốn AS15169", facts, ok)
	}
}

// Majestic Million là CSV hợp lệ nhưng cột thứ hai là TldRank chứ không phải tên miền.
// Parser đọc ra 50.000 "mục" trông rất bình thường, và tín hiệu bảo vệ high_rank —
// lớp chống chặn nhầm mạnh nhất — im lặng tắt hẳn. Chỉ kiểm tra "khác rỗng" không bắt
// được ca này, nên phải kiểm tra cả ngữ nghĩa.
func TestRankRejectsWrongColumnOrder(t *testing.T) {
	body := "GlobalRank,TldRank,Domain,TLD\n" +
		"1,1,google.com,com\n2,2,facebook.com,com\n3,1,youtube.com,com\n"

	e := NewRank()
	err := e.LoadTable(writeTemp(t, "majestic.csv", body))
	if err == nil {
		t.Fatal("nhận bảng sai thứ tự cột, muốn báo lỗi")
	}
	if e.Loaded() {
		t.Error("đánh dấu đã nạp dù không tra được tên miền nào")
	}
}

// Định dạng đúng thì vẫn phải nạp được — guard không được chặn nhầm bảng thật.
func TestRankAcceptsWellFormedList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tranco.csv.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("tạo zip: %v", err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("top-1m.csv")
	if err != nil {
		t.Fatalf("tạo mục trong zip: %v", err)
	}
	if _, err := w.Write([]byte("1,google.com\n2,example.com\n")); err != nil {
		t.Fatalf("ghi nội dung zip: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("đóng zip: %v", err)
	}
	f.Close()

	e := NewRank()
	if err := e.LoadTable(path); err != nil {
		t.Fatalf("từ chối bảng hợp lệ: %v", err)
	}
	if got := e.Status().Entries; got != 2 {
		t.Errorf("nạp %d mục, muốn 2", got)
	}
}
