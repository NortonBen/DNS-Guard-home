package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/benji/dnsguard/internal/classify"
)

// ErrDomainProtected báo rằng domain nằm trong danh sách bảo vệ và không thể chặn,
// kể cả khi quản trị yêu cầu. Tầng HTTP ánh xạ nó thành 403 domain_protected.
var ErrDomainProtected = errors.New("domain is protected")

// Hành động ghi vào nhật ký.
const (
	ActionBlock        = "block"
	ActionAllow        = "allow"
	ActionIgnore       = "ignore"
	ActionRecategorize = "recategorize"
	ActionStage        = "stage"
	ActionExpire       = "expire"
)

// Trạng thái vòng đời.
const (
	StatusNew     = "new"
	StatusStaging = "staging"
	StatusBlocked = "blocked"
	StatusAllowed = "allowed"
	StatusIgnored = "ignored"
)

// ActorSystem là nhãn dùng cho quyết định tự động.
const ActorSystem = "system"

// Decision là một dòng của nhật ký bất biến.
type Decision struct {
	ID         int64           `json:"id"`
	DomainID   int64           `json:"domain_id"`
	Action     string          `json:"action"`
	ActorLabel string          `json:"actor_label"`
	Reason     string          `json:"reason"`
	Snapshot   json.RawMessage `json:"snapshot,omitempty"`
	CreatedAt  string          `json:"created_at"`
}

// DecisionInput mô tả một quyết định sắp ghi.
type DecisionInput struct {
	DomainID    int64
	Action      string
	ActorID     *int64
	ActorLabel  string
	Reason      string
	CategoryKey string
	// Manual bằng true cho quyết định của con người. Nó đặt cờ is_manual, và từ đó
	// job chấm điểm bỏ qua domain này vĩnh viễn.
	Manual bool
}

// statusFor ánh xạ hành động sang trạng thái đích. Chuỗi rỗng nghĩa là hành động
// không đổi trạng thái.
func statusFor(action string) string {
	switch action {
	case ActionBlock:
		return StatusBlocked
	case ActionAllow:
		return StatusAllowed
	case ActionIgnore:
		return StatusIgnored
	case ActionStage:
		return StatusStaging
	case ActionExpire:
		return StatusNew
	default:
		return ""
	}
}

// ApplyDecision ghi một quyết định và cập nhật trạng thái domain trong cùng một
// transaction.
//
// Đây là điểm duy nhất thay đổi trạng thái domain. Bốn bất biến ở
// docs/06-classification.md §4 được thực thi tại đây:
//
//  1. Quyết định thủ công đặt is_manual, và job chấm điểm bỏ qua domain đó.
//  2. Trạng thái allowed không bao giờ tự đổi — chỉ con người mới gỡ được.
//  3. Mỗi lần chuyển trạng thái sinh đúng một dòng decisions.
//  4. Domain trong danh sách bảo vệ không bao giờ vào blocked.
func (s *Store) ApplyDecision(ctx context.Context, in DecisionInput) (Domain, error) {
	soft, err := s.SoftAllowList(ctx)
	if err != nil {
		return Domain{}, err
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return Domain{}, fmt.Errorf("begin decision: %w", err)
	}
	defer tx.Rollback()

	var (
		name, status string
		isManual     bool
		score        sql.NullFloat64
		confidence   sql.NullFloat64
		categoryKey  sql.NullString
	)
	err = tx.QueryRowContext(ctx, `
		SELECT d.name, d.status, d.is_manual, d.score, d.confidence, c.key
		FROM domains d LEFT JOIN categories c ON c.id = d.category_id
		WHERE d.id = ?`, in.DomainID).
		Scan(&name, &status, &isManual, &score, &confidence, &categoryKey)
	if err == sql.ErrNoRows {
		return Domain{}, ErrNotFound
	}
	if err != nil {
		return Domain{}, fmt.Errorf("load domain %d: %w", in.DomainID, err)
	}

	// Bất biến 4. Kiểm tra trước mọi thứ khác, kể cả khi người yêu cầu là quản trị.
	if in.Action == ActionBlock {
		if rule, protected := classify.IsProtected(name, soft); protected {
			return Domain{}, fmt.Errorf("%w: %s khớp luật %q", ErrDomainProtected, name, rule)
		}
	}

	// Bất biến 2. Chỉ con người mới đưa được domain ra khỏi trạng thái allowed.
	if status == StatusAllowed && !in.Manual {
		return Domain{}, fmt.Errorf("%w: allowed chỉ đổi được bằng quyết định thủ công", ErrDomainProtected)
	}

	// Ảnh chụp trạng thái tại thời điểm quyết định. Đây là phần đắt giá nhất của
	// nhật ký: sáu tháng sau vẫn trả lời được "lúc đó hệ thống nghĩ gì", kể cả khi
	// trọng số đã đổi từ lâu.
	signals, err := signalsOf(ctx, tx, in.DomainID)
	if err != nil {
		return Domain{}, err
	}
	snapshot, err := json.Marshal(map[string]any{
		"status_before": status,
		"score":         nullFloat(score),
		"confidence":    nullFloat(confidence),
		"category":      nullString(categoryKey),
		"signals":       signals,
	})
	if err != nil {
		return Domain{}, fmt.Errorf("encode snapshot: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO decisions (domain_id, action, actor_id, actor_label, reason, snapshot, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		in.DomainID, in.Action, in.ActorID, in.ActorLabel, in.Reason, string(snapshot), Now(),
	); err != nil {
		return Domain{}, fmt.Errorf("insert decision: %w", err)
	}

	if err := applyStatus(ctx, tx, in); err != nil {
		return Domain{}, err
	}

	if err := tx.Commit(); err != nil {
		return Domain{}, fmt.Errorf("commit decision: %w", err)
	}
	return s.GetDomain(ctx, in.DomainID)
}

// applyStatus cập nhật dòng domain theo hành động.
func applyStatus(ctx context.Context, tx *sql.Tx, in DecisionInput) error {
	now := Now()
	newStatus := statusFor(in.Action)

	if newStatus != "" {
		var stagedAt, blockedAt any
		if newStatus == StatusStaging {
			stagedAt = now
		}
		if newStatus == StatusBlocked {
			blockedAt = now
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE domains SET
			  status     = ?,
			  is_manual  = CASE WHEN ? THEN 1 ELSE is_manual END,
			  staged_at  = coalesce(?, staged_at),
			  blocked_at = coalesce(?, blocked_at),
			  updated_at = ?
			WHERE id = ?`,
			newStatus, in.Manual, stagedAt, blockedAt, now, in.DomainID); err != nil {
			return fmt.Errorf("update domain status: %w", err)
		}
	} else if in.Manual {
		if _, err := tx.ExecContext(ctx,
			`UPDATE domains SET is_manual = 1, updated_at = ? WHERE id = ?`,
			now, in.DomainID); err != nil {
			return fmt.Errorf("mark domain manual: %w", err)
		}
	}

	if in.CategoryKey != "" {
		if _, err := tx.ExecContext(ctx, `
			UPDATE domains
			SET category_id = (SELECT id FROM categories WHERE key = ?), updated_at = ?
			WHERE id = ?`, in.CategoryKey, now, in.DomainID); err != nil {
			return fmt.Errorf("update domain category: %w", err)
		}
	}
	return nil
}

func signalsOf(ctx context.Context, tx *sql.Tx, domainID int64) ([]Signal, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT kind, weight, detail FROM signals WHERE domain_id = ? LIMIT 50`, domainID)
	if err != nil {
		return nil, fmt.Errorf("read signals: %w", err)
	}
	defer rows.Close()

	var out []Signal
	for rows.Next() {
		var sig Signal
		var detail string
		if err := rows.Scan(&sig.Kind, &sig.Weight, &detail); err != nil {
			return nil, fmt.Errorf("scan signal: %w", err)
		}
		sig.Detail = json.RawMessage(detail)
		out = append(out, sig)
	}
	return out, rows.Err()
}

// History trả về nhật ký của một domain, mới nhất trước.
func (s *Store) History(ctx context.Context, domainID int64, limit int) ([]Decision, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.r.QueryContext(ctx, `
		SELECT id, domain_id, action, actor_label, reason, snapshot, created_at
		FROM decisions WHERE domain_id = ?
		ORDER BY created_at DESC, id DESC
		LIMIT ?`, domainID, limit)
	if err != nil {
		return nil, fmt.Errorf("list history: %w", err)
	}
	defer rows.Close()

	var out []Decision
	for rows.Next() {
		var d Decision
		var snapshot string
		if err := rows.Scan(&d.ID, &d.DomainID, &d.Action, &d.ActorLabel,
			&d.Reason, &snapshot, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan decision: %w", err)
		}
		d.Snapshot = json.RawMessage(snapshot)
		out = append(out, d)
	}
	return out, rows.Err()
}

func nullFloat(v sql.NullFloat64) any {
	if !v.Valid {
		return nil
	}
	return v.Float64
}

func nullString(v sql.NullString) any {
	if !v.Valid {
		return nil
	}
	return v.String
}
