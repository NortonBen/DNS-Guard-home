package store

import (
	"context"
	"database/sql"
	"fmt"
)

// ListSource là một nguồn blocklist công khai.
type ListSource struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	URL          string  `json:"url"`
	Format       string  `json:"format"`
	CategoryKey  string  `json:"category"`
	Enabled      bool    `json:"enabled"`
	SyncInterval int     `json:"sync_interval"`
	LastSyncAt   *string `json:"last_sync_at"`
	LastStatus   string  `json:"last_status"`
	LastError    string  `json:"last_error,omitempty"`
	EntryCount   int     `json:"entry_count"`
	PrevCount    int     `json:"prev_count"`
	ETag         string  `json:"-"`
}

const sourceColumns = `s.id, s.name, s.url, s.format, coalesce(c.key, ''), s.enabled,
	s.sync_interval, s.last_sync_at, s.last_status, s.last_error,
	s.entry_count, s.prev_count, s.etag`

func scanSource(row interface{ Scan(...any) error }) (ListSource, error) {
	var s ListSource
	err := row.Scan(&s.ID, &s.Name, &s.URL, &s.Format, &s.CategoryKey, &s.Enabled,
		&s.SyncInterval, &s.LastSyncAt, &s.LastStatus, &s.LastError,
		&s.EntryCount, &s.PrevCount, &s.ETag)
	return s, err
}

// ListSources trả về mọi nguồn.
func (s *Store) ListSources(ctx context.Context) ([]ListSource, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT `+sourceColumns+`
		FROM list_sources s LEFT JOIN categories c ON c.id = s.category_id
		ORDER BY s.name LIMIT 500`)
	if err != nil {
		return nil, fmt.Errorf("list sources: %w", err)
	}
	defer rows.Close()

	var out []ListSource
	for rows.Next() {
		src, err := scanSource(rows)
		if err != nil {
			return nil, fmt.Errorf("scan source: %w", err)
		}
		out = append(out, src)
	}
	return out, rows.Err()
}

// GetSource trả về một nguồn theo id.
func (s *Store) GetSource(ctx context.Context, id int64) (ListSource, error) {
	row := s.r.QueryRowContext(ctx, `
		SELECT `+sourceColumns+`
		FROM list_sources s LEFT JOIN categories c ON c.id = s.category_id
		WHERE s.id = ?`, id)
	src, err := scanSource(row)
	if err == sql.ErrNoRows {
		return ListSource{}, ErrNotFound
	}
	if err != nil {
		return ListSource{}, fmt.Errorf("get source %d: %w", id, err)
	}
	return src, nil
}

// CreateSource thêm một nguồn mới.
func (s *Store) CreateSource(ctx context.Context, in ListSource) (ListSource, error) {
	interval := in.SyncInterval
	if interval <= 0 {
		interval = 86400
	}
	res, err := s.w.ExecContext(ctx, `
		INSERT INTO list_sources (name, url, format, category_id, enabled, sync_interval, created_at)
		VALUES (?, ?, ?, (SELECT id FROM categories WHERE key = ?), ?, ?, ?)`,
		in.Name, in.URL, in.Format, in.CategoryKey, in.Enabled, interval, Now())
	if err != nil {
		return ListSource{}, fmt.Errorf("create source %q: %w", in.URL, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return ListSource{}, fmt.Errorf("source id: %w", err)
	}
	return s.GetSource(ctx, id)
}

// UpdateSource sửa các trường cho phép của một nguồn.
func (s *Store) UpdateSource(ctx context.Context, id int64, in ListSource) (ListSource, error) {
	_, err := s.w.ExecContext(ctx, `
		UPDATE list_sources SET
		  name = ?, url = ?, format = ?,
		  category_id = (SELECT id FROM categories WHERE key = ?),
		  enabled = ?, sync_interval = ?
		WHERE id = ?`,
		in.Name, in.URL, in.Format, in.CategoryKey, in.Enabled, in.SyncInterval, id)
	if err != nil {
		return ListSource{}, fmt.Errorf("update source %d: %w", id, err)
	}
	return s.GetSource(ctx, id)
}

// DeleteSource xóa một nguồn cùng toàn bộ mục của nó.
func (s *Store) DeleteSource(ctx context.Context, id int64) error {
	res, err := s.w.ExecContext(ctx, `DELETE FROM list_sources WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete source %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ReplaceEntries thay toàn bộ mục của một nguồn trong một transaction.
//
// Bảo vệ sụt giảm nằm ở tầng catalog chứ không ở đây: quyết định "có nên thay không"
// là nghiệp vụ, còn hàm này chỉ thi hành.
func (s *Store) ReplaceEntries(ctx context.Context, sourceID int64, domains []string) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin replace entries: %w", err)
	}
	defer tx.Rollback()

	var prev int
	if err := tx.QueryRowContext(ctx,
		`SELECT entry_count FROM list_sources WHERE id = ?`, sourceID).Scan(&prev); err != nil {
		return fmt.Errorf("read entry_count: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM list_entries WHERE source_id = ?`, sourceID); err != nil {
		return fmt.Errorf("clear entries: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx,
		`INSERT OR IGNORE INTO list_entries (source_id, domain) VALUES (?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare insert entry: %w", err)
	}
	defer stmt.Close()

	for _, d := range domains {
		if _, err := stmt.ExecContext(ctx, sourceID, d); err != nil {
			return fmt.Errorf("insert entry %q: %w", d, err)
		}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE list_sources SET entry_count = ?, prev_count = ?, last_sync_at = ?,
		                        last_status = 'ok', last_error = ''
		WHERE id = ?`, len(domains), prev, Now(), sourceID); err != nil {
		return fmt.Errorf("update source counts: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit replace entries: %w", err)
	}
	return nil
}

// MarkSourceError ghi lại một lần đồng bộ thất bại mà không đụng tới dữ liệu cũ.
func (s *Store) MarkSourceError(ctx context.Context, sourceID int64, status string, cause error) error {
	msg := ""
	if cause != nil {
		msg = cause.Error()
	}
	_, err := s.w.ExecContext(ctx, `
		UPDATE list_sources SET last_sync_at = ?, last_status = ?, last_error = ? WHERE id = ?`,
		Now(), status, msg, sourceID)
	if err != nil {
		return fmt.Errorf("mark source error: %w", err)
	}
	return nil
}

// SetSourceETag lưu ETag để lần đồng bộ sau gửi If-None-Match.
func (s *Store) SetSourceETag(ctx context.Context, sourceID int64, etag string) error {
	_, err := s.w.ExecContext(ctx, `UPDATE list_sources SET etag = ? WHERE id = ?`, etag, sourceID)
	if err != nil {
		return fmt.Errorf("set source etag: %w", err)
	}
	return nil
}

// SourcesDue trả về các nguồn đã tới hạn đồng bộ.
func (s *Store) SourcesDue(ctx context.Context) ([]ListSource, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT `+sourceColumns+`
		FROM list_sources s LEFT JOIN categories c ON c.id = s.category_id
		WHERE s.enabled = 1
		  AND (s.last_sync_at IS NULL
		       OR strftime('%s', ?) - strftime('%s', s.last_sync_at) >= s.sync_interval)
		LIMIT 100`, Now())
	if err != nil {
		return nil, fmt.Errorf("list due sources: %w", err)
	}
	defer rows.Close()

	var out []ListSource
	for rows.Next() {
		src, err := scanSource(rows)
		if err != nil {
			return nil, fmt.Errorf("scan due source: %w", err)
		}
		out = append(out, src)
	}
	return out, rows.Err()
}

// SourceOverlap là mức chồng lấn giữa hai nguồn.
type SourceOverlap struct {
	A       int64   `json:"a"`
	B       int64   `json:"b"`
	Shared  int     `json:"shared"`
	Jaccard float64 `json:"jaccard"`
}

// SourceUnique là số domain chỉ có ở một nguồn.
type SourceUnique struct {
	SourceID int64 `json:"source_id"`
	OnlyHere int   `json:"only_here"`
}

// Overlap trả lời câu hỏi thực tế: có nên bỏ bớt nguồn nào không.
func (s *Store) Overlap(ctx context.Context) ([]SourceOverlap, []SourceUnique, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT a.source_id, b.source_id, count(*)
		FROM list_entries a JOIN list_entries b ON a.domain = b.domain
		WHERE a.source_id < b.source_id
		GROUP BY a.source_id, b.source_id
		LIMIT 500`)
	if err != nil {
		return nil, nil, fmt.Errorf("compute overlap: %w", err)
	}
	defer rows.Close()

	totals := map[int64]int{}
	sources, err := s.ListSources(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, src := range sources {
		totals[src.ID] = src.EntryCount
	}

	var pairs []SourceOverlap
	for rows.Next() {
		var p SourceOverlap
		if err := rows.Scan(&p.A, &p.B, &p.Shared); err != nil {
			return nil, nil, fmt.Errorf("scan overlap: %w", err)
		}
		if union := totals[p.A] + totals[p.B] - p.Shared; union > 0 {
			p.Jaccard = float64(p.Shared) / float64(union)
		}
		pairs = append(pairs, p)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	uniqueRows, err := s.r.QueryContext(ctx, `
		SELECT a.source_id, count(*)
		FROM list_entries a
		WHERE NOT EXISTS (
		  SELECT 1 FROM list_entries b
		  WHERE b.domain = a.domain AND b.source_id <> a.source_id
		)
		GROUP BY a.source_id
		LIMIT 100`)
	if err != nil {
		return nil, nil, fmt.Errorf("compute unique: %w", err)
	}
	defer uniqueRows.Close()

	var unique []SourceUnique
	for uniqueRows.Next() {
		var u SourceUnique
		if err := uniqueRows.Scan(&u.SourceID, &u.OnlyHere); err != nil {
			return nil, nil, fmt.Errorf("scan unique: %w", err)
		}
		unique = append(unique, u)
	}
	return pairs, unique, uniqueRows.Err()
}

// PublicListInfo cho biết một domain xuất hiện ở những nguồn nào.
type PublicListInfo struct {
	SourceID int64  `json:"source_id"`
	Name     string `json:"name"`
	Category string `json:"category"`
}

// PublicListsFor trả về các nguồn có chứa domain này.
func (s *Store) PublicListsFor(ctx context.Context, name string) ([]PublicListInfo, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT s.id, s.name, coalesce(c.key, '')
		FROM list_entries e
		JOIN list_sources s ON s.id = e.source_id
		LEFT JOIN categories c ON c.id = s.category_id
		WHERE e.domain = ?
		LIMIT 50`, name)
	if err != nil {
		return nil, fmt.Errorf("lookup public lists: %w", err)
	}
	defer rows.Close()

	var out []PublicListInfo
	for rows.Next() {
		var info PublicListInfo
		if err := rows.Scan(&info.SourceID, &info.Name, &info.Category); err != nil {
			return nil, fmt.Errorf("scan public list: %w", err)
		}
		out = append(out, info)
	}
	return out, rows.Err()
}
