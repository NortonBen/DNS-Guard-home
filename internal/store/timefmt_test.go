package store

import "testing"

// Định dạng thời gian của CSDL phải đi qua được các hàm ngày giờ của SQLite. Nếu
// không, mọi phép số học thời gian trong SQL sẽ trả NULL hoặc sai định dạng, và so
// sánh chuỗi sẽ hỏng âm thầm thay vì báo lỗi.
func TestSQLiteUnderstandsStoredTimeFormat(t *testing.T) {
	s := newTestStore(t)

	var shifted, roundTrip string
	err := s.Reader().QueryRow(
		`SELECT strftime('%Y-%m-%dT%H:%M:%SZ', ?, '-3 seconds'),
		        strftime('%Y-%m-%dT%H:%M:%SZ', ?)`,
		"2026-09-07T08:15:10Z", "2026-09-07T08:15:10Z").Scan(&shifted, &roundTrip)
	if err != nil {
		t.Fatalf("strftime: %v", err)
	}
	if roundTrip != "2026-09-07T08:15:10Z" {
		t.Errorf("khứ hồi = %q, muốn 2026-09-07T08:15:10Z", roundTrip)
	}
	if shifted != "2026-09-07T08:15:07Z" {
		t.Errorf("lùi 3 giây = %q, muốn 2026-09-07T08:15:07Z", shifted)
	}

	// datetime() trả về định dạng có dấu cách, KHÔNG dùng để so sánh với cột đã lưu.
	var legacy string
	s.Reader().QueryRow(`SELECT datetime(?)`, "2026-09-07T08:15:10Z").Scan(&legacy)
	if legacy == roundTrip {
		t.Error("datetime() và định dạng lưu trữ trùng nhau — chú thích cảnh báo đã lỗi thời")
	}
	t.Logf("datetime() trả %q, định dạng lưu trữ là %q", legacy, roundTrip)
}
