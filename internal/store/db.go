// Package store gói toàn bộ truy cập CSDL. Không tầng nào khác được mở kết nối
// hay viết SQL.
package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store giữ hai pool trên cùng một file SQLite.
//
// SQLite ở chế độ WAL cho phép nhiều đầu đọc song song với đúng một đầu ghi. Dồn
// mọi lệnh ghi qua một kết nối duy nhất biến "database is locked" từ lỗi lúc chạy
// thành hàng đợi trong tiến trình — cần thiết vì ingest ghi liên tục trong khi API
// vẫn phải đọc. Đây là khác biệt vận hành lớn nhất so với bản PostgreSQL ở ADR-2.
type Store struct {
	w    *sql.DB // đúng một kết nối: mọi lệnh ghi xếp hàng ở đây
	r    *sql.DB // pool đọc
	path string

	clients clientCache
}

// Open mở CSDL, bật các pragma cần thiết và chạy migration còn thiếu.
func Open(path string, autoMigrate bool) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db dir %q: %w", dir, err)
		}
	}

	w, err := openPool(path, true)
	if err != nil {
		return nil, err
	}
	r, err := openPool(path, false)
	if err != nil {
		w.Close()
		return nil, err
	}

	s := &Store{w: w, r: r, path: path}

	if autoMigrate {
		if err := Migrate(w); err != nil {
			s.Close()
			return nil, err
		}
	}
	return s, nil
}

func openPool(path string, writer bool) (*sql.DB, error) {
	pragmas := []string{
		"journal_mode(WAL)", // đầu đọc không chặn đầu ghi
		"busy_timeout(10000)",
		"foreign_keys(ON)",
		// NORMAL thay vì FULL: mỗi giao dịch không fsync riêng. Yêu cầu ghi đĩa ở
		// docs/01-requirements.md §6 nói rõ Pi có thể chạy thẻ SD. WAL + NORMAL
		// vẫn an toàn khi tiến trình chết; chỉ mất vài giao dịch cuối nếu mất điện.
		"synchronous(NORMAL)",
		"temp_store(MEMORY)",
		"cache_size(-32000)", // 32 MB, nằm gọn trong ngân sách 512 MB của backend
	}
	dsn := "file:" + url.PathEscape(path) + "?_pragma=" + strings.Join(pragmas, "&_pragma=")

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}
	if writer {
		db.SetMaxOpenConns(1)
	} else {
		db.SetMaxOpenConns(8)
	}
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(time.Hour)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite %q: %w", path, err)
	}
	return db, nil
}

// Writer trả về pool ghi. Dùng cho INSERT, UPDATE, DELETE và mọi transaction.
func (s *Store) Writer() *sql.DB { return s.w }

// Reader trả về pool đọc. Dùng cho SELECT.
func (s *Store) Reader() *sql.DB { return s.r }

// Path trả về đường dẫn file CSDL.
func (s *Store) Path() string { return s.path }

func (s *Store) Close() error {
	var firstErr error
	for _, db := range []*sql.DB{s.w, s.r} {
		if db == nil {
			continue
		}
		if err := db.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Checkpoint gộp WAL vào file chính. Chạy trước khi sao lưu để bản sao đủ và gọn.
func (s *Store) Checkpoint() error {
	if _, err := s.w.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("wal checkpoint: %w", err)
	}
	return nil
}

// nowUTC trả về thời điểm hiện tại theo UTC. Tách ra một chỗ để test thay được.
func nowUTC() time.Time { return time.Now().UTC() }

// Now trả về mốc thời gian dạng CSDL dùng: RFC3339 UTC, giây nguyên.
// Toàn hệ thống chỉ sinh mốc thời gian qua đây để so sánh chuỗi luôn đúng.
func Now() string { return time.Now().UTC().Format(time.RFC3339) }

// TimeAt định dạng t theo cùng quy ước với Now.
func TimeAt(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// ParseTime đọc ngược mốc thời gian đã lưu.
func ParseTime(s string) (time.Time, error) { return time.Parse(time.RFC3339, s) }
