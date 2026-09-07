package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"modernc.org/sqlite"
)

// Lỗi nghiệp vụ. Tầng HTTP ánh xạ chúng sang mã lỗi API; tầng dưới không tự dựng
// response và không tự ghi log.
var (
	ErrNotFound      = errors.New("not found")
	ErrAlreadyExists = errors.New("already exists")
)

// Signal là một bằng chứng đã lưu kèm domain.
type Signal struct {
	Kind   string          `json:"kind"`
	Weight float64         `json:"weight"`
	Detail json.RawMessage `json:"detail,omitempty"`
}

// Category là phân loại rút gọn nhúng trong Domain.
type Category struct {
	Key     string `json:"key"`
	LabelVi string `json:"label_vi"`
	Color   string `json:"color"`
}

// Domain là một dòng của bảng domains kèm phân loại và tín hiệu.
type Domain struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	ETLD1       string    `json:"etld1"`
	Status      string    `json:"status"`
	Origin      string    `json:"origin"`
	Category    *Category `json:"category"`
	Score       *float64  `json:"score"`
	Confidence  *float64  `json:"confidence"`
	IsManual    bool      `json:"is_manual"`
	IsWildcard  bool      `json:"is_wildcard"`
	QueryCount  int64     `json:"query_count"`
	ClientCount int       `json:"client_count"`
	FirstSeen   string    `json:"first_seen"`
	LastSeen    string    `json:"last_seen"`
	StagedAt    *string   `json:"staged_at,omitempty"`
	BlockedAt   *string   `json:"blocked_at,omitempty"`
	ScoredAt    *string   `json:"scored_at,omitempty"`
	EnrichedAt  *string   `json:"enriched_at,omitempty"`
	Signals     []Signal  `json:"signals"`
}

// DomainFilter là bộ lọc cho ListDomains. Trường rỗng nghĩa là không lọc.
type DomainFilter struct {
	Statuses   []string
	Categories []string
	Query      string
	MinScore   *float64
	SeenAfter  string
	Sort       string // "score" | "last_seen" | "query_count" | "name" | "confidence"
	Desc       bool
	Limit      int
	Cursor     string
}

// cursor là vị trí phân trang. Mã hóa base64 và đục với client: nó là chi tiết cài
// đặt, và client dựa vào nội dung của nó sẽ hỏng khi đổi cách sắp xếp.
type cursor struct {
	Key any   `json:"k"`
	ID  int64 `json:"i"`
}

func encodeCursor(key any, id int64) string {
	b, err := json.Marshal(cursor{Key: key, ID: id})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (cursor, bool) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return cursor{}, false
	}
	var c cursor
	if err := json.Unmarshal(b, &c); err != nil {
		return cursor{}, false
	}
	return c, true
}

var regexpOnce sync.Once

// registerRegexp thêm toán tử REGEXP cho SQLite. SQLite không có sẵn, và tìm kiếm
// bằng biểu thức chính quy là một trong ba chế độ mà giao diện yêu cầu.
func registerRegexp() {
	regexpOnce.Do(func() {
		// Biểu thức đã biên dịch dùng lại giữa các dòng: một truy vấn quét nhiều
		// nghìn dòng, biên dịch lại mỗi dòng sẽ chậm hơn hàng chục lần.
		cache := sync.Map{}
		_ = sqlite.RegisterDeterministicScalarFunction("regexp", 2,
			func(ctx *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
				pattern, ok := args[0].(string)
				if !ok {
					return nil, errors.New("regexp: mẫu phải là chuỗi")
				}
				value, ok := args[1].(string)
				if !ok {
					return int64(0), nil
				}
				re, cached := cache.Load(pattern)
				if !cached {
					compiled, err := regexp.Compile(pattern)
					if err != nil {
						return nil, fmt.Errorf("regexp %q: %w", pattern, err)
					}
					cache.Store(pattern, compiled)
					re = compiled
				}
				if re.(*regexp.Regexp).MatchString(value) {
					return int64(1), nil
				}
				return int64(0), nil
			})
	})
}

// searchClause dựng mệnh đề tìm kiếm, tự nhận dạng ba chế độ:
//
//	doubleclick        → chứa chuỗi
//	*.eulerian.net     → khớp hậu tố, dùng index trên name_rev
//	/^ad[0-9]+\./      → biểu thức chính quy
func searchClause(q string) (string, []any) {
	q = strings.TrimSpace(strings.ToLower(q))
	switch {
	case q == "":
		return "", nil

	case strings.HasPrefix(q, "/") && strings.HasSuffix(q, "/") && len(q) > 2:
		return "d.name REGEXP ?", []any{q[1 : len(q)-1]}

	case strings.HasPrefix(q, "*."):
		// Khớp hậu tố trên tên đảo ngược trở thành khớp tiền tố, nên index dùng được.
		// Bao gồm cả chính tên miền gốc: "*.example.com" khớp "example.com".
		suffix := q[2:]
		return "(d.name_rev LIKE ? OR d.name = ?)", []any{reverseName(suffix) + ".%", suffix}

	default:
		return "d.name LIKE ?", []any{"%" + q + "%"}
	}
}

// sortColumn ánh xạ tên sắp xếp từ API sang cột. Không bao giờ nối trực tiếp đầu
// vào của người dùng vào SQL.
func sortColumn(sort string) (column string, key string) {
	switch sort {
	case "last_seen":
		return "d.last_seen", "last_seen"
	case "query_count":
		return "d.query_count", "query_count"
	case "name":
		return "d.name", "name"
	case "confidence":
		return "d.confidence", "confidence"
	default:
		return "d.score", "score"
	}
}

const domainColumns = `
	d.id, d.name, d.etld1, d.status, d.origin, d.score, d.confidence,
	d.is_manual, d.is_wildcard, d.query_count, d.client_count,
	d.first_seen, d.last_seen, d.staged_at, d.blocked_at, d.scored_at, d.enriched_at,
	c.key, c.label_vi, c.color`

func scanDomain(rows interface{ Scan(...any) error }) (Domain, error) {
	var d Domain
	var catKey, catLabel, catColor sql.NullString
	err := rows.Scan(
		&d.ID, &d.Name, &d.ETLD1, &d.Status, &d.Origin, &d.Score, &d.Confidence,
		&d.IsManual, &d.IsWildcard, &d.QueryCount, &d.ClientCount,
		&d.FirstSeen, &d.LastSeen, &d.StagedAt, &d.BlockedAt, &d.ScoredAt, &d.EnrichedAt,
		&catKey, &catLabel, &catColor,
	)
	if err != nil {
		return Domain{}, err
	}
	if catKey.Valid {
		d.Category = &Category{Key: catKey.String, LabelVi: catLabel.String, Color: catColor.String}
	}
	return d, nil
}

// ListDomains trả về một trang domain đã lọc, kèm con trỏ trang kế tiếp.
//
// Phân trang bằng con trỏ chứ không phải offset: bảng domain có thể tới nửa triệu
// dòng, và OFFSET lớn buộc CSDL đếm lại từ đầu mỗi lần.
func (s *Store) ListDomains(ctx context.Context, f DomainFilter) (items []Domain, next string, hasMore bool, err error) {
	registerRegexp()

	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	col, key := sortColumn(f.Sort)
	dir, cmp := "ASC", ">"
	if f.Desc {
		dir, cmp = "DESC", "<"
	}

	where := []string{"1=1"}
	args := []any{}

	if len(f.Statuses) > 0 {
		where = append(where, "d.status IN ("+placeholders(len(f.Statuses))+")")
		for _, v := range f.Statuses {
			args = append(args, v)
		}
	}
	if len(f.Categories) > 0 {
		where = append(where, "c.key IN ("+placeholders(len(f.Categories))+")")
		for _, v := range f.Categories {
			args = append(args, v)
		}
	}
	if clause, cargs := searchClause(f.Query); clause != "" {
		where = append(where, clause)
		args = append(args, cargs...)
	}
	if f.MinScore != nil {
		where = append(where, "d.score >= ?")
		args = append(args, *f.MinScore)
	}
	if f.SeenAfter != "" {
		where = append(where, "d.last_seen >= ?")
		args = append(args, f.SeenAfter)
	}
	if c, ok := decodeCursor(f.Cursor); ok {
		// So sánh theo bộ đôi (khóa sắp xếp, id) để hai dòng cùng điểm không bị bỏ
		// sót hay lặp lại giữa hai trang.
		where = append(where, fmt.Sprintf("(%s %s ? OR (%s IS ? AND d.id %s ?))", col, cmp, col, cmp))
		args = append(args, c.Key, c.Key, c.ID)
	}

	// Mọi truy vấn có LIMIT. Không có ngoại lệ: một truy vấn không giới hạn trên
	// bảng domain sẽ kéo về nửa triệu dòng và giết tiến trình.
	query := fmt.Sprintf(`
		SELECT %s
		FROM domains d
		LEFT JOIN categories c ON c.id = d.category_id
		WHERE %s
		ORDER BY %s %s, d.id %s
		LIMIT ?`,
		domainColumns, strings.Join(where, " AND "), col, dir, dir)

	rows, err := s.r.QueryContext(ctx, query, append(args, limit+1)...)
	if err != nil {
		return nil, "", false, fmt.Errorf("list domains: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, "", false, fmt.Errorf("scan domain: %w", err)
		}
		items = append(items, d)
	}
	if err := rows.Err(); err != nil {
		return nil, "", false, fmt.Errorf("iterate domains: %w", err)
	}

	// Lấy dư một dòng để biết còn trang sau mà không phải chạy COUNT.
	if len(items) > limit {
		hasMore = true
		items = items[:limit]
		last := items[len(items)-1]
		next = encodeCursor(sortKeyOf(last, key), last.ID)
	}

	if err := s.attachSignals(ctx, items); err != nil {
		return nil, "", false, err
	}
	return items, next, hasMore, nil
}

func sortKeyOf(d Domain, key string) any {
	switch key {
	case "last_seen":
		return d.LastSeen
	case "query_count":
		return d.QueryCount
	case "name":
		return d.Name
	case "confidence":
		return d.Confidence
	default:
		return d.Score
	}
}

// attachSignals nạp tín hiệu cho một trang domain bằng đúng một truy vấn.
//
// Tín hiệu trả kèm danh sách vì màn Triage cần hiển thị ngay, và mỗi domain luôn có
// dưới mười tín hiệu.
func (s *Store) attachSignals(ctx context.Context, items []Domain) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]any, len(items))
	index := make(map[int64]int, len(items))
	for i, d := range items {
		ids[i] = d.ID
		index[d.ID] = i
	}

	rows, err := s.r.QueryContext(ctx, `
		SELECT domain_id, kind, weight, detail
		FROM signals
		WHERE domain_id IN (`+placeholders(len(ids))+`)
		ORDER BY abs(weight) DESC
		LIMIT 1000`, ids...)
	if err != nil {
		return fmt.Errorf("list signals: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var domainID int64
		var sig Signal
		var detail string
		if err := rows.Scan(&domainID, &sig.Kind, &sig.Weight, &detail); err != nil {
			return fmt.Errorf("scan signal: %w", err)
		}
		sig.Detail = json.RawMessage(detail)
		if i, ok := index[domainID]; ok {
			items[i].Signals = append(items[i].Signals, sig)
		}
	}
	return rows.Err()
}

// GetDomain trả về một domain theo id.
func (s *Store) GetDomain(ctx context.Context, id int64) (Domain, error) {
	row := s.r.QueryRowContext(ctx, `
		SELECT `+domainColumns+`
		FROM domains d LEFT JOIN categories c ON c.id = d.category_id
		WHERE d.id = ?`, id)

	d, err := scanDomain(row)
	if err == sql.ErrNoRows {
		return Domain{}, ErrNotFound
	}
	if err != nil {
		return Domain{}, fmt.Errorf("get domain %d: %w", id, err)
	}
	items := []Domain{d}
	if err := s.attachSignals(ctx, items); err != nil {
		return Domain{}, err
	}
	return items[0], nil
}

// GetDomainByName trả về một domain theo tên.
func (s *Store) GetDomainByName(ctx context.Context, name string) (Domain, error) {
	row := s.r.QueryRowContext(ctx, `
		SELECT `+domainColumns+`
		FROM domains d LEFT JOIN categories c ON c.id = d.category_id
		WHERE d.name = ?`, strings.ToLower(name))

	d, err := scanDomain(row)
	if err == sql.ErrNoRows {
		return Domain{}, ErrNotFound
	}
	if err != nil {
		return Domain{}, fmt.Errorf("get domain %q: %w", name, err)
	}
	return d, nil
}

// Siblings trả về các subdomain cùng eTLD+1 đã thấy trong mạng, nhiều lưu lượng
// trước.
func (s *Store) Siblings(ctx context.Context, id int64, limit int) ([]Domain, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.r.QueryContext(ctx, `
		SELECT `+domainColumns+`
		FROM domains d LEFT JOIN categories c ON c.id = d.category_id
		WHERE d.etld1 = (SELECT etld1 FROM domains WHERE id = ?) AND d.id <> ?
		ORDER BY d.query_count DESC
		LIMIT ?`, id, id, limit)
	if err != nil {
		return nil, fmt.Errorf("list siblings: %w", err)
	}
	defer rows.Close()

	var out []Domain
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, fmt.Errorf("scan sibling: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// CountSubdomains đếm số subdomain phân biệt dưới cùng eTLD+1, phục vụ tín hiệu spread.
func (s *Store) CountSubdomains(ctx context.Context, etld1 string) (int, error) {
	var n int
	err := s.r.QueryRowContext(ctx,
		`SELECT count(*) FROM domains WHERE etld1 = ?`, etld1).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count subdomains %q: %w", etld1, err)
	}
	return n, nil
}

// ReplaceSignals ghi lại toàn bộ tín hiệu của một domain. Mỗi lần chấm điểm xóa và
// ghi lại chứ không cộng dồn: tín hiệu là ảnh chụp của lần chấm điểm gần nhất.
func (s *Store) ReplaceSignals(ctx context.Context, tx *sql.Tx, domainID int64, signals []Signal) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM signals WHERE domain_id = ?`, domainID); err != nil {
		return fmt.Errorf("clear signals: %w", err)
	}
	now := Now()
	for _, sig := range signals {
		detail := string(sig.Detail)
		if detail == "" {
			detail = "{}"
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO signals (domain_id, kind, weight, detail, created_at) VALUES (?, ?, ?, ?, ?)`,
			domainID, sig.Kind, sig.Weight, detail, now); err != nil {
			return fmt.Errorf("insert signal %q: %w", sig.Kind, err)
		}
	}
	return nil
}

func placeholders(n int) string {
	if n <= 0 {
		return "NULL"
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// EnsureDomain trả về id của một domain, tạo mới nếu chưa có.
//
// Dùng cho đích CNAME và domain anh em phát hiện qua chứng chỉ: chúng là một phần
// thật của đồ thị dù chưa từng được thiết bị nào trong mạng truy vấn.
func (s *Store) EnsureDomain(ctx context.Context, name, etld1, origin string) (int64, error) {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	if name == "" {
		return 0, fmt.Errorf("%w: tên miền rỗng", ErrNotFound)
	}

	var id int64
	err := s.r.QueryRowContext(ctx, `SELECT id FROM domains WHERE name = ?`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, fmt.Errorf("lookup domain %q: %w", name, err)
	}

	now := Now()
	res, err := s.w.ExecContext(ctx, `
		INSERT INTO domains (name, name_rev, etld1, status, origin,
		                     first_seen, last_seen, created_at, updated_at)
		VALUES (?, ?, ?, 'new', ?, ?, ?, ?, ?)
		ON CONFLICT (name) DO NOTHING`,
		name, reverseName(name), etld1, origin, now, now, now, now)
	if err != nil {
		return 0, fmt.Errorf("insert domain %q: %w", name, err)
	}
	if id, err := res.LastInsertId(); err == nil && id > 0 {
		return id, nil
	}
	// Có tiến trình khác vừa chèn cùng tên: đọc lại id đã có.
	if err := s.r.QueryRowContext(ctx, `SELECT id FROM domains WHERE name = ?`, name).Scan(&id); err != nil {
		return 0, fmt.Errorf("re-read domain %q: %w", name, err)
	}
	return id, nil
}

// DomainFactRow là một bản ghi làm giàu kèm domain của nó.
type DomainFactRow struct {
	DomainID int64
	Name     string
	ETLD1    string
	Data     json.RawMessage
}

// FactsBySource trả về mọi bản ghi làm giàu còn hạn của một nguồn.
func (s *Store) FactsBySource(ctx context.Context, source string, limit int) ([]DomainFactRow, error) {
	if limit <= 0 || limit > 100000 {
		limit = 20000
	}
	rows, err := s.r.QueryContext(ctx, `
		SELECT f.domain_id, d.name, d.etld1, f.data
		FROM domain_facts f JOIN domains d ON d.id = f.domain_id
		WHERE f.source = ? AND f.error IS NULL
		LIMIT ?`, source, limit)
	if err != nil {
		return nil, fmt.Errorf("list facts by source %q: %w", source, err)
	}
	defer rows.Close()

	var out []DomainFactRow
	for rows.Next() {
		var r DomainFactRow
		var data string
		if err := rows.Scan(&r.DomainID, &r.Name, &r.ETLD1, &data); err != nil {
			return nil, fmt.Errorf("scan fact row: %w", err)
		}
		r.Data = json.RawMessage(data)
		out = append(out, r)
	}
	return out, rows.Err()
}
