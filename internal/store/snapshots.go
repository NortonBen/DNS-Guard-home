package store

import (
	"context"
	"database/sql"
	"fmt"
)

// Snapshot là một lần xuất bản đã ghi.
type Snapshot struct {
	ID          int64  `json:"id"`
	CategoryKey string `json:"category"`
	EntryCount  int    `json:"entry_count"`
	Checksum    string `json:"checksum"`
	FilePath    string `json:"file_path"`
	PublishedAt string `json:"published_at"`
	PublishedBy string `json:"published_by"`
}

// PublishTarget là một phân loại cần xuất bản.
type PublishTarget struct {
	CategoryID  int64
	CategoryKey string
	PublishPath string
}

// PublishTargets trả về các phân loại đang bật, kèm file gộp.
func (s *Store) PublishTargets(ctx context.Context) ([]PublishTarget, error) {
	rows, err := s.r.QueryContext(ctx,
		`SELECT id, key, publish_path FROM categories WHERE enabled = 1 ORDER BY sort_order LIMIT 50`)
	if err != nil {
		return nil, fmt.Errorf("list publish targets: %w", err)
	}
	defer rows.Close()

	var out []PublishTarget
	for rows.Next() {
		var t PublishTarget
		if err := rows.Scan(&t.CategoryID, &t.CategoryKey, &t.PublishPath); err != nil {
			return nil, fmt.Errorf("scan publish target: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// BlockedDomains trả về các domain sẽ nằm trong danh sách xuất bản.
//
// categoryID bằng 0 nghĩa là lấy tất cả phân loại đang bật, dùng cho file gộp.
func (s *Store) BlockedDomains(ctx context.Context, categoryID int64) ([]string, error) {
	query := `
		SELECT d.name FROM domains d
		JOIN categories c ON c.id = d.category_id
		WHERE d.status = 'blocked' AND c.enabled = 1`
	args := []any{}
	if categoryID > 0 {
		query += ` AND d.category_id = ?`
		args = append(args, categoryID)
	}
	// Sắp xếp theo tên để nội dung file ổn định giữa các lần chạy: nhờ đó checksum
	// chỉ đổi khi tập domain thật sự đổi, chứ không đổi vì thứ tự dòng khác đi.
	query += ` ORDER BY d.name LIMIT 1000000`

	rows, err := s.r.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list blocked domains: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan blocked domain: %w", err)
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// LastSnapshot trả về bản xuất bản gần nhất của một phân loại.
func (s *Store) LastSnapshot(ctx context.Context, categoryID int64) (Snapshot, error) {
	var (
		snap Snapshot
		cat  sql.NullString
	)
	query := `
		SELECT s.id, coalesce(c.key, ''), s.entry_count, s.checksum, s.file_path,
		       s.published_at, s.published_by
		FROM snapshots s LEFT JOIN categories c ON c.id = s.category_id
		WHERE ` + categoryClause(categoryID) + `
		ORDER BY s.published_at DESC, s.id DESC LIMIT 1`

	args := []any{}
	if categoryID > 0 {
		args = append(args, categoryID)
	}
	err := s.r.QueryRowContext(ctx, query, args...).Scan(
		&snap.ID, &cat, &snap.EntryCount, &snap.Checksum, &snap.FilePath,
		&snap.PublishedAt, &snap.PublishedBy)
	if err == sql.ErrNoRows {
		return Snapshot{}, ErrNotFound
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("last snapshot: %w", err)
	}
	snap.CategoryKey = cat.String
	return snap, nil
}

func categoryClause(categoryID int64) string {
	if categoryID > 0 {
		return "s.category_id = ?"
	}
	return "s.category_id IS NULL"
}

// SaveSnapshot ghi một bản xuất bản cùng toàn bộ mục của nó.
//
// snapshot_entries cho phép so sánh hai lần xuất bản bất kỳ và quay lại bản cũ mà
// không cần dựng lại từ trạng thái hiện tại của CSDL.
func (s *Store) SaveSnapshot(ctx context.Context, categoryID int64, domains []string,
	checksum, filePath, publishedBy string) (int64, error) {

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin save snapshot: %w", err)
	}
	defer tx.Rollback()

	var catArg any
	if categoryID > 0 {
		catArg = categoryID
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO snapshots (category_id, entry_count, checksum, file_path, published_at, published_by)
		VALUES (?, ?, ?, ?, ?, ?)`,
		catArg, len(domains), checksum, filePath, Now(), publishedBy)
	if err != nil {
		return 0, fmt.Errorf("insert snapshot: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("snapshot id: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx,
		`INSERT OR IGNORE INTO snapshot_entries (snapshot_id, domain) VALUES (?, ?)`)
	if err != nil {
		return 0, fmt.Errorf("prepare snapshot entry: %w", err)
	}
	defer stmt.Close()

	for _, d := range domains {
		if _, err := stmt.ExecContext(ctx, id, d); err != nil {
			return 0, fmt.Errorf("insert snapshot entry: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit snapshot: %w", err)
	}
	return id, nil
}

// ListSnapshots trả về lịch sử xuất bản.
func (s *Store) ListSnapshots(ctx context.Context, categoryKey string, limit int) ([]Snapshot, error) {
	if limit <= 0 || limit > 200 {
		limit = 30
	}
	where, args := "1=1", []any{}
	if categoryKey != "" {
		where, args = "c.key = ?", []any{categoryKey}
	}

	rows, err := s.r.QueryContext(ctx, `
		SELECT s.id, coalesce(c.key, ''), s.entry_count, s.checksum, s.file_path,
		       s.published_at, s.published_by
		FROM snapshots s LEFT JOIN categories c ON c.id = s.category_id
		WHERE `+where+`
		ORDER BY s.published_at DESC, s.id DESC
		LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	defer rows.Close()

	var out []Snapshot
	for rows.Next() {
		var snap Snapshot
		if err := rows.Scan(&snap.ID, &snap.CategoryKey, &snap.EntryCount, &snap.Checksum,
			&snap.FilePath, &snap.PublishedAt, &snap.PublishedBy); err != nil {
			return nil, fmt.Errorf("scan snapshot: %w", err)
		}
		out = append(out, snap)
	}
	return out, rows.Err()
}

// SnapshotDomains trả về nội dung của một bản xuất bản, dùng cho diff và rollback.
func (s *Store) SnapshotDomains(ctx context.Context, snapshotID int64) ([]string, error) {
	rows, err := s.r.QueryContext(ctx,
		`SELECT domain FROM snapshot_entries WHERE snapshot_id = ? ORDER BY domain LIMIT 1000000`,
		snapshotID)
	if err != nil {
		return nil, fmt.Errorf("read snapshot entries: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, fmt.Errorf("scan snapshot entry: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DiffSnapshots so sánh hai bản xuất bản.
func (s *Store) DiffSnapshots(ctx context.Context, a, b int64) (added, removed []string, err error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT domain, 'added' AS change FROM snapshot_entries WHERE snapshot_id = ?
		  AND domain NOT IN (SELECT domain FROM snapshot_entries WHERE snapshot_id = ?)
		UNION ALL
		SELECT domain, 'removed' FROM snapshot_entries WHERE snapshot_id = ?
		  AND domain NOT IN (SELECT domain FROM snapshot_entries WHERE snapshot_id = ?)
		LIMIT 200000`, b, a, a, b)
	if err != nil {
		return nil, nil, fmt.Errorf("diff snapshots: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var domain, change string
		if err := rows.Scan(&domain, &change); err != nil {
			return nil, nil, fmt.Errorf("scan diff: %w", err)
		}
		if change == "added" {
			added = append(added, domain)
		} else {
			removed = append(removed, domain)
		}
	}
	return added, removed, rows.Err()
}

// PruneSnapshots giữ lại N bản gần nhất cho mỗi phân loại.
func (s *Store) PruneSnapshots(ctx context.Context, keep int) (int64, error) {
	res, err := s.w.ExecContext(ctx, `
		DELETE FROM snapshots WHERE id NOT IN (
		  SELECT id FROM (
		    SELECT id, row_number() OVER (
		      PARTITION BY coalesce(category_id, -1) ORDER BY published_at DESC, id DESC
		    ) AS rn FROM snapshots
		  ) WHERE rn <= ?
		)`, keep)
	if err != nil {
		return 0, fmt.Errorf("prune snapshots: %w", err)
	}
	return res.RowsAffected()
}
