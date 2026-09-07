package store

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migrate áp dụng các file migration còn thiếu theo thứ tự tên file.
//
// Không dùng golang-migrate: migration nhúng thẳng vào binary giữ đúng ràng buộc
// "một binary + một file cấu hình" ở docs/01-requirements.md §6, và bỏ được một
// công cụ ngoài khỏi cả môi trường phát triển lẫn image Docker.
func Migrate(db *sql.DB) error {
	return MigrateFS(db, migrationFS, "migrations")
}

// MigrateFS chạy migration từ một hệ thống file nhúng bất kỳ.
//
// Tách khỏi Migrate vì CSDL nhật ký AI là một file riêng với bộ migration riêng,
// nhưng cần đúng những đảm bảo ở đây: mỗi file một transaction, áp theo thứ tự
// tên, và không bao giờ chạy lại thứ đã chạy.
func MigrateFS(db *sql.DB, fsys fs.FS, dir string) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied := map[string]bool{}
	rows, err := db.Query(`SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return fmt.Errorf("scan schema_migrations: %w", err)
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate schema_migrations: %w", err)
	}

	entries, err := fs.Glob(fsys, dir+"/*.sql")
	if err != nil {
		return fmt.Errorf("glob migrations: %w", err)
	}
	sort.Strings(entries)

	for _, path := range entries {
		version := strings.TrimSuffix(strings.TrimPrefix(path, dir+"/"), ".sql")
		if applied[version] {
			continue
		}
		body, err := fs.ReadFile(fsys, path)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", version, err)
		}
		// Mỗi migration chạy trong một transaction: hỏng giữa chừng thì không để
		// lại lược đồ nửa vời, kể cả khi mất điện — đúng yêu cầu độ tin cậy ở §6.
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", version, err)
		}
		if _, err := tx.Exec(string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", version, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			version, Now(),
		); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %s: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", version, err)
		}
	}
	return nil
}
