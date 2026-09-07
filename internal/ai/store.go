// Package ai là tầng hỏi model ngôn ngữ của DNSGuard: gom domain thành lô để hỏi,
// giữ nhật ký từng lượt gọi, và cho người vận hành hỏi đáp trực tiếp bằng công cụ
// đọc dữ liệu trong máy.
//
// Ranh giới: gói này biết về domain và blocklist; internal/llm chỉ biết giao thức.
// Mọi thứ AI ghi ra đều nằm ở một file CSDL riêng, trừ đúng một chỗ — kết luận cho
// từng domain vẫn đi vào domain_facts của CSDL chính, vì nó là bằng chứng dùng để
// chấm điểm.
package ai

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/benji/dnsguard/internal/store"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Store là CSDL nhật ký AI — một file SQLite tách hẳn khỏi dnsguard.db.
//
// Tách file là quyết định vận hành, không phải sở thích: nhật ký AI phình theo số
// lượt hỏi chứ không theo số domain, người vận hành cần xoá được nó mà không chạm
// vào dữ liệu quyết định, và một bản sao lưu CSDL chính không nên phải mang theo
// hàng megabyte prompt.
type Store struct {
	w    *sql.DB // đúng một kết nối, giống CSDL chính
	r    *sql.DB
	path string
}

// DefaultPath đặt file nhật ký cạnh CSDL chính, chỉ khác tên.
//
// Cùng thư mục nghĩa là cùng chính sách sao lưu và cùng quyền truy cập — người vận
// hành đã cấp quyền cho một chỗ thì không phải nhớ thêm chỗ thứ hai.
func DefaultPath(mainDBPath string) string {
	dir := filepath.Dir(mainDBPath)
	ext := filepath.Ext(mainDBPath)
	base := strings.TrimSuffix(filepath.Base(mainDBPath), ext)
	if base == "" {
		base = "dnsguard"
	}
	// Giữ nguyên phần mở rộng của CSDL chính: người trỏ vào một file .sqlite mong
	// file thứ hai cũng là .sqlite, không phải một đuôi khác tự nhiên xuất hiện.
	if ext == "" {
		ext = ".db"
	}
	return filepath.Join(dir, base+"-ai"+ext)
}

// OpenStore mở CSDL nhật ký AI và chạy migration còn thiếu.
func OpenStore(path string, autoMigrate bool) (*Store, error) {
	w, err := store.OpenPool(path, true)
	if err != nil {
		return nil, err
	}
	r, err := store.OpenPool(path, false)
	if err != nil {
		w.Close()
		return nil, err
	}

	s := &Store{w: w, r: r, path: path}
	if autoMigrate {
		if err := store.MigrateFS(w, migrationFS, "migrations"); err != nil {
			s.Close()
			return nil, fmt.Errorf("migrate ai db: %w", err)
		}
	}
	return s, nil
}

// Path trả về đường dẫn file.
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

// ---- nhật ký request ----

// Loại lượt gọi.
const (
	KindClassify = "classify"
	KindRecheck  = "recheck"
	KindAsk      = "ask"
)

// Request là một lượt gọi model đã ghi lại.
type Request struct {
	ID           int64  `json:"id"`
	Kind         string `json:"kind"`
	Model        string `json:"model"`
	BaseURL      string `json:"base_url"`
	DomainCount  int    `json:"domain_count"`
	ParsedCount  int    `json:"parsed_count"`
	SkippedCount int    `json:"skipped_count"`
	Prompt       string `json:"prompt,omitempty"`
	Response     string `json:"response,omitempty"`
	PromptChars  int    `json:"prompt_chars"`
	ReplyChars   int    `json:"reply_chars"`
	LatencyMS    int64  `json:"latency_ms"`
	Error        string `json:"error,omitempty"`
	Actor        string `json:"actor"`
	CreatedAt    string `json:"created_at"`
}

// Verdict là kết luận cho một domain trong một lượt.
type Verdict struct {
	ID         int64   `json:"id"`
	RequestID  int64   `json:"request_id"`
	Domain     string  `json:"domain"`
	Category   string  `json:"category"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
	CreatedAt  string  `json:"created_at"`
}

// SaveRequest ghi một lượt gọi cùng toàn bộ kết luận của nó trong một transaction.
//
// Một transaction vì hai bảng phải nhất quán: một dòng request không có verdict nào
// mà lẽ ra phải có sẽ làm người đọc lịch sử tưởng model trả về rỗng.
func (s *Store) SaveRequest(ctx context.Context, req Request, verdicts []Verdict) (int64, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin save ai request: %w", err)
	}
	defer tx.Rollback()

	now := store.Now()
	if req.CreatedAt == "" {
		req.CreatedAt = now
	}
	if req.Actor == "" {
		req.Actor = store.ActorSystem
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO ai_requests (kind, model, base_url, domain_count, parsed_count,
		                         skipped_count, prompt, response, prompt_chars,
		                         reply_chars, latency_ms, error, actor, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		req.Kind, req.Model, req.BaseURL, req.DomainCount, req.ParsedCount,
		req.SkippedCount, req.Prompt, req.Response, len(req.Prompt),
		len(req.Response), req.LatencyMS, req.Error, req.Actor, req.CreatedAt)
	if err != nil {
		return 0, fmt.Errorf("insert ai request: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read ai request id: %w", err)
	}

	for _, v := range verdicts {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO ai_verdicts (request_id, domain, category, confidence, reason, created_at)
			VALUES (?,?,?,?,?,?)`,
			id, v.Domain, v.Category, v.Confidence, v.Reason, req.CreatedAt); err != nil {
			return 0, fmt.Errorf("insert ai verdict %q: %w", v.Domain, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit ai request: %w", err)
	}
	return id, nil
}

// ListRequests trả về lịch sử theo thứ tự mới nhất trước.
//
// Cố ý KHÔNG trả prompt và response: một trang danh sách hai mươi dòng sẽ kéo theo
// vài trăm kilobyte văn bản mà người dùng chưa đọc tới. Chúng có ở GetRequest.
func (s *Store) ListRequests(ctx context.Context, kind string, limit, offset int) ([]Request, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	where, args := "", []any{}
	if kind != "" {
		where = " WHERE kind = ?"
		args = append(args, kind)
	}
	args = append(args, limit, offset)

	rows, err := s.r.QueryContext(ctx, `
		SELECT id, kind, model, base_url, domain_count, parsed_count, skipped_count,
		       prompt_chars, reply_chars, latency_ms, error, actor, created_at
		FROM ai_requests`+where+`
		ORDER BY id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("list ai requests: %w", err)
	}
	defer rows.Close()

	var out []Request
	for rows.Next() {
		var q Request
		if err := rows.Scan(&q.ID, &q.Kind, &q.Model, &q.BaseURL, &q.DomainCount,
			&q.ParsedCount, &q.SkippedCount, &q.PromptChars, &q.ReplyChars,
			&q.LatencyMS, &q.Error, &q.Actor, &q.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan ai request: %w", err)
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// CountRequests đếm tổng số lượt, cho phân trang.
func (s *Store) CountRequests(ctx context.Context, kind string) (int, error) {
	q := `SELECT count(*) FROM ai_requests`
	args := []any{}
	if kind != "" {
		q += ` WHERE kind = ?`
		args = append(args, kind)
	}
	var n int
	if err := s.r.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count ai requests: %w", err)
	}
	return n, nil
}

// GetRequest trả về một lượt đầy đủ kèm prompt, phản hồi thô và các kết luận.
func (s *Store) GetRequest(ctx context.Context, id int64) (Request, []Verdict, error) {
	var q Request
	err := s.r.QueryRowContext(ctx, `
		SELECT id, kind, model, base_url, domain_count, parsed_count, skipped_count,
		       prompt, response, prompt_chars, reply_chars, latency_ms, error, actor, created_at
		FROM ai_requests WHERE id = ?`, id).
		Scan(&q.ID, &q.Kind, &q.Model, &q.BaseURL, &q.DomainCount, &q.ParsedCount,
			&q.SkippedCount, &q.Prompt, &q.Response, &q.PromptChars, &q.ReplyChars,
			&q.LatencyMS, &q.Error, &q.Actor, &q.CreatedAt)
	if err == sql.ErrNoRows {
		return Request{}, nil, store.ErrNotFound
	}
	if err != nil {
		return Request{}, nil, fmt.Errorf("get ai request %d: %w", id, err)
	}

	verdicts, err := s.verdictsWhere(ctx, `request_id = ? ORDER BY id`, id)
	return q, verdicts, err
}

// VerdictsFor trả về lịch sử kết luận của một domain, mới nhất trước.
func (s *Store) VerdictsFor(ctx context.Context, domain string, limit int) ([]Verdict, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return s.verdictsWhere(ctx, `domain = ? ORDER BY id DESC LIMIT ?`,
		strings.ToLower(strings.TrimSpace(domain)), limit)
}

func (s *Store) verdictsWhere(ctx context.Context, clause string, args ...any) ([]Verdict, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT id, request_id, domain, category, confidence, reason, created_at
		FROM ai_verdicts WHERE `+clause, args...)
	if err != nil {
		return nil, fmt.Errorf("list ai verdicts: %w", err)
	}
	defer rows.Close()

	var out []Verdict
	for rows.Next() {
		var v Verdict
		if err := rows.Scan(&v.ID, &v.RequestID, &v.Domain, &v.Category,
			&v.Confidence, &v.Reason, &v.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan ai verdict: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Usage là số liệu tổng hợp cho màn lịch sử.
type Usage struct {
	Requests    int    `json:"requests"`
	Domains     int    `json:"domains"`
	Verdicts    int    `json:"verdicts"`
	Failed      int    `json:"failed"`
	PromptChars int64  `json:"prompt_chars"`
	ReplyChars  int64  `json:"reply_chars"`
	LastAt      string `json:"last_at,omitempty"`
}

// Usage tổng hợp mức tiêu thụ trong ngần ấy ngày gần đây.
//
// Đếm ký tự chứ không đếm token: nhà cung cấp nào cũng tính token theo cách riêng
// và phản hồi không phải lúc nào cũng kèm số đếm, còn ký tự thì luôn đo được và
// đủ để thấy xu hướng.
func (s *Store) Usage(ctx context.Context, days int) (Usage, error) {
	if days <= 0 {
		days = 30
	}
	var u Usage
	var last sql.NullString
	err := s.r.QueryRowContext(ctx, `
		SELECT count(*), coalesce(sum(domain_count),0), coalesce(sum(parsed_count),0),
		       coalesce(sum(CASE WHEN error <> '' THEN 1 ELSE 0 END),0),
		       coalesce(sum(prompt_chars),0), coalesce(sum(reply_chars),0), max(created_at)
		FROM ai_requests WHERE created_at >= datetime('now', ?)`,
		fmt.Sprintf("-%d days", days)).
		Scan(&u.Requests, &u.Domains, &u.Verdicts, &u.Failed,
			&u.PromptChars, &u.ReplyChars, &last)
	if err != nil {
		return Usage{}, fmt.Errorf("ai usage: %w", err)
	}
	u.LastAt = last.String
	return u, nil
}

// PruneRequests xoá lượt gọi cũ hơn ngần ấy ngày. Verdict đi theo nhờ ON DELETE CASCADE.
func (s *Store) PruneRequests(ctx context.Context, days int) (int64, error) {
	if days <= 0 {
		return 0, nil
	}
	res, err := s.w.ExecContext(ctx,
		`DELETE FROM ai_requests WHERE created_at < datetime('now', ?)`,
		fmt.Sprintf("-%d days", days))
	if err != nil {
		return 0, fmt.Errorf("prune ai requests: %w", err)
	}
	return res.RowsAffected()
}

// ---- skill ----

// Skill là một mẩu quy ước vận hành chèn vào ngữ cảnh khi câu hỏi khớp từ khoá.
type Skill struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Triggers    []string `json:"triggers"`
	Content     string   `json:"content"`
	Always      bool     `json:"always"`
	Enabled     bool     `json:"enabled"`
	Builtin     bool     `json:"builtin"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

// ListSkills trả về mọi skill theo tên.
func (s *Store) ListSkills(ctx context.Context) ([]Skill, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT id, name, description, triggers, content, always, enabled, builtin,
		       created_at, updated_at
		FROM ai_skills ORDER BY always DESC, name`)
	if err != nil {
		return nil, fmt.Errorf("list ai skills: %w", err)
	}
	defer rows.Close()

	var out []Skill
	for rows.Next() {
		var sk Skill
		var triggers string
		var always, enabled, builtin int
		if err := rows.Scan(&sk.ID, &sk.Name, &sk.Description, &triggers, &sk.Content,
			&always, &enabled, &builtin, &sk.CreatedAt, &sk.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan ai skill: %w", err)
		}
		// Trigger hỏng không được làm gãy cả danh sách: một skill không khớp được
		// vẫn tốt hơn một màn hình trắng.
		_ = json.Unmarshal([]byte(triggers), &sk.Triggers)
		sk.Always, sk.Enabled, sk.Builtin = always != 0, enabled != 0, builtin != 0
		out = append(out, sk)
	}
	return out, rows.Err()
}

// SaveSkill thêm mới hoặc cập nhật theo tên.
func (s *Store) SaveSkill(ctx context.Context, sk Skill) (int64, error) {
	sk.Name = strings.TrimSpace(sk.Name)
	if sk.Name == "" {
		return 0, fmt.Errorf("skill phải có tên")
	}
	triggers, err := json.Marshal(orEmptyStrings(sk.Triggers))
	if err != nil {
		return 0, fmt.Errorf("encode triggers: %w", err)
	}

	now := store.Now()
	if _, err := s.w.ExecContext(ctx, `
		INSERT INTO ai_skills (name, description, triggers, content, always, enabled,
		                       builtin, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT (name) DO UPDATE SET
		  description = excluded.description, triggers = excluded.triggers,
		  content = excluded.content, always = excluded.always,
		  enabled = excluded.enabled, updated_at = excluded.updated_at`,
		sk.Name, sk.Description, string(triggers), sk.Content,
		boolInt(sk.Always), boolInt(sk.Enabled), boolInt(sk.Builtin), now, now); err != nil {
		return 0, fmt.Errorf("save ai skill %q: %w", sk.Name, err)
	}

	var id int64
	if err := s.r.QueryRowContext(ctx, `SELECT id FROM ai_skills WHERE name = ?`,
		sk.Name).Scan(&id); err != nil {
		return 0, fmt.Errorf("read ai skill id: %w", err)
	}
	return id, nil
}

// DeleteSkill xoá một skill. Skill dựng sẵn chỉ tắt được, không xoá được — xoá nó
// thì bản nâng cấp sau sẽ lặng lẽ tạo lại và người dùng tưởng mình xoá hụt.
func (s *Store) DeleteSkill(ctx context.Context, id int64) error {
	var builtin int
	err := s.r.QueryRowContext(ctx, `SELECT builtin FROM ai_skills WHERE id = ?`, id).Scan(&builtin)
	if err == sql.ErrNoRows {
		return store.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read ai skill %d: %w", id, err)
	}
	if builtin != 0 {
		return fmt.Errorf("skill dựng sẵn chỉ tắt được, không xoá được")
	}
	if _, err := s.w.ExecContext(ctx, `DELETE FROM ai_skills WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete ai skill %d: %w", id, err)
	}
	return nil
}

// EnsureBuiltinSkills nạp bộ skill dựng sẵn khi chúng chưa có.
//
// Chỉ chèn cái còn thiếu: người vận hành sửa nội dung một skill dựng sẵn thì bản
// sửa đó phải sống sót qua lần khởi động sau.
func (s *Store) EnsureBuiltinSkills(ctx context.Context) error {
	for _, sk := range BuiltinSkills() {
		var exists int
		if err := s.r.QueryRowContext(ctx,
			`SELECT count(*) FROM ai_skills WHERE name = ?`, sk.Name).Scan(&exists); err != nil {
			return fmt.Errorf("check ai skill %q: %w", sk.Name, err)
		}
		if exists > 0 {
			continue
		}
		if _, err := s.SaveSkill(ctx, sk); err != nil {
			return err
		}
	}
	return nil
}

// ---- máy chủ MCP ----

// MCPServer là một máy chủ MCP ngoài, cung cấp thêm công cụ cho model.
type MCPServer struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
	// AuthHeader có thể chứa token nên không bao giờ ra khỏi tiến trình.
	AuthHeader string `json:"-"`
	HasAuth    bool   `json:"has_auth"`
	Enabled    bool   `json:"enabled"`
	Note       string `json:"note"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// ListMCPServers trả về mọi máy chủ MCP đã khai báo.
func (s *Store) ListMCPServers(ctx context.Context, onlyEnabled bool) ([]MCPServer, error) {
	where := ""
	if onlyEnabled {
		where = " WHERE enabled = 1"
	}
	rows, err := s.r.QueryContext(ctx, `
		SELECT id, name, url, auth_header, enabled, note, created_at, updated_at
		FROM ai_mcp_servers`+where+` ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list mcp servers: %w", err)
	}
	defer rows.Close()

	var out []MCPServer
	for rows.Next() {
		var m MCPServer
		var enabled int
		if err := rows.Scan(&m.ID, &m.Name, &m.URL, &m.AuthHeader, &enabled,
			&m.Note, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan mcp server: %w", err)
		}
		m.Enabled, m.HasAuth = enabled != 0, m.AuthHeader != ""
		out = append(out, m)
	}
	return out, rows.Err()
}

// SaveMCPServer thêm mới hoặc cập nhật theo tên.
//
// authHeader rỗng khi cập nhật nghĩa là GIỮ NGUYÊN token cũ: giao diện không đọc
// lại được token nên không thể gửi lại nó, và bắt người dùng nhập lại mỗi lần đổi
// một dấu tích là cách chắc chắn để họ dán nhầm.
func (s *Store) SaveMCPServer(ctx context.Context, m MCPServer) (int64, error) {
	m.Name = strings.TrimSpace(m.Name)
	m.URL = strings.TrimSpace(m.URL)
	if m.Name == "" || m.URL == "" {
		return 0, fmt.Errorf("máy chủ MCP phải có tên và URL")
	}
	if !strings.HasPrefix(m.URL, "http://") && !strings.HasPrefix(m.URL, "https://") {
		return 0, fmt.Errorf("URL máy chủ MCP phải bắt đầu bằng http:// hoặc https://")
	}

	// Trong UPSERT của SQLite, tên cột trần ở vế SET là giá trị CŨ, còn excluded.*
	// là giá trị mới — nên bỏ hẳn dòng gán là cách giữ token cũ.
	setAuth := "auth_header = excluded.auth_header, "
	if m.AuthHeader == "" {
		setAuth = ""
	}

	now := store.Now()
	if _, err := s.w.ExecContext(ctx, `
		INSERT INTO ai_mcp_servers (name, url, auth_header, enabled, note, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT (name) DO UPDATE SET
		  url = excluded.url, `+setAuth+`
		  enabled = excluded.enabled, note = excluded.note, updated_at = excluded.updated_at`,
		m.Name, m.URL, m.AuthHeader, boolInt(m.Enabled), m.Note, now, now); err != nil {
		return 0, fmt.Errorf("save mcp server %q: %w", m.Name, err)
	}

	var id int64
	if err := s.r.QueryRowContext(ctx, `SELECT id FROM ai_mcp_servers WHERE name = ?`,
		m.Name).Scan(&id); err != nil {
		return 0, fmt.Errorf("read mcp server id: %w", err)
	}
	return id, nil
}

// DeleteMCPServer xoá một máy chủ MCP.
func (s *Store) DeleteMCPServer(ctx context.Context, id int64) error {
	res, err := s.w.ExecContext(ctx, `DELETE FROM ai_mcp_servers WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete mcp server %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// ---- hội thoại ----

// Chat là một cuộc hỏi đáp.
type Chat struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Actor     string `json:"actor"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// ChatMessage là một lượt trong cuộc hỏi đáp.
type ChatMessage struct {
	ID        int64  `json:"id"`
	ChatID    int64  `json:"chat_id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	Steps     []Step `json:"steps"`
	CreatedAt string `json:"created_at"`
}

// CreateChat mở một cuộc hỏi đáp mới.
func (s *Store) CreateChat(ctx context.Context, title, actor string) (int64, error) {
	now := store.Now()
	res, err := s.w.ExecContext(ctx,
		`INSERT INTO ai_chats (title, actor, created_at, updated_at) VALUES (?,?,?,?)`,
		clip(title, 120), actor, now, now)
	if err != nil {
		return 0, fmt.Errorf("create chat: %w", err)
	}
	return res.LastInsertId()
}

// ListChats trả về các cuộc hỏi đáp gần nhất.
func (s *Store) ListChats(ctx context.Context, limit int) ([]Chat, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	rows, err := s.r.QueryContext(ctx,
		`SELECT id, title, actor, created_at, updated_at FROM ai_chats
		 ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list chats: %w", err)
	}
	defer rows.Close()

	var out []Chat
	for rows.Next() {
		var c Chat
		if err := rows.Scan(&c.ID, &c.Title, &c.Actor, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan chat: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Messages trả về toàn bộ lượt của một cuộc hỏi đáp theo thứ tự.
func (s *Store) Messages(ctx context.Context, chatID int64) ([]ChatMessage, error) {
	rows, err := s.r.QueryContext(ctx,
		`SELECT id, chat_id, role, content, steps, created_at FROM ai_messages
		 WHERE chat_id = ? ORDER BY id`, chatID)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()

	var out []ChatMessage
	for rows.Next() {
		var m ChatMessage
		var steps string
		if err := rows.Scan(&m.ID, &m.ChatID, &m.Role, &m.Content, &steps, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		_ = json.Unmarshal([]byte(steps), &m.Steps)
		if m.Steps == nil {
			m.Steps = []Step{}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AddMessage ghi một lượt và đẩy mốc cập nhật của cuộc hỏi đáp lên.
func (s *Store) AddMessage(ctx context.Context, chatID int64, role, content string, steps []Step) error {
	raw, err := json.Marshal(orEmptySteps(steps))
	if err != nil {
		return fmt.Errorf("encode steps: %w", err)
	}
	now := store.Now()
	if _, err := s.w.ExecContext(ctx,
		`INSERT INTO ai_messages (chat_id, role, content, steps, created_at) VALUES (?,?,?,?,?)`,
		chatID, role, content, string(raw), now); err != nil {
		return fmt.Errorf("add message: %w", err)
	}
	if _, err := s.w.ExecContext(ctx,
		`UPDATE ai_chats SET updated_at = ? WHERE id = ?`, now, chatID); err != nil {
		return fmt.Errorf("touch chat: %w", err)
	}
	return nil
}

// DeleteChat xoá một cuộc hỏi đáp cùng các lượt của nó.
func (s *Store) DeleteChat(ctx context.Context, id int64) error {
	res, err := s.w.ExecContext(ctx, `DELETE FROM ai_chats WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete chat %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func orEmptyStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func orEmptySteps(s []Step) []Step {
	if s == nil {
		return []Step{}
	}
	return s
}

func clip(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max]
}
