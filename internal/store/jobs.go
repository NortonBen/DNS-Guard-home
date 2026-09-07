package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// Trạng thái job.
const (
	JobPending   = "pending"
	JobRunning   = "running"
	JobDone      = "done"
	JobFailed    = "failed"
	JobCancelled = "cancelled"
)

// Job là một công việc chạy nền.
type Job struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	Args       json.RawMessage `json:"args,omitempty"`
	State      string          `json:"state"`
	Attempt    int             `json:"attempt"`
	Progress   JobProgress     `json:"progress"`
	Error      string          `json:"error,omitempty"`
	StartedAt  *string         `json:"started_at"`
	FinishedAt *string         `json:"finished_at"`
	CreatedAt  string          `json:"created_at"`
}

// JobProgress là tiến độ để giao diện hiển thị thanh chạy.
type JobProgress struct {
	Done  int64 `json:"done"`
	Total int64 `json:"total"`
}

// EnqueueJob thêm một job vào hàng đợi và trả về id.
//
// Hàng đợi chạy trên chính CSDL thay vì Redis: job và dữ liệu nghiệp vụ nằm cùng
// một file, nên không bao giờ có trạng thái mồ côi khi một bên chết. Thông lượng
// thấp hơn không thành vấn đề — job ở đây tính bằng nghìn mỗi ngày.
func (s *Store) EnqueueJob(ctx context.Context, kind string, args any) (string, error) {
	raw := "{}"
	if args != nil {
		b, err := json.Marshal(args)
		if err != nil {
			return "", fmt.Errorf("encode job args: %w", err)
		}
		raw = string(b)
	}

	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate job id: %w", err)
	}
	id := hex.EncodeToString(buf)

	now := Now()
	if _, err := s.w.ExecContext(ctx, `
		INSERT INTO jobs (id, kind, args, state, run_at, created_at)
		VALUES (?, ?, ?, 'pending', ?, ?)`, id, kind, raw, now, now); err != nil {
		return "", fmt.Errorf("enqueue job %q: %w", kind, err)
	}
	return id, nil
}

// ClaimJob nhận một job đang chờ. Trả về nil khi không có việc.
//
// Pool ghi chỉ có một kết nối nên UPDATE này tự nó đã tuần tự hóa: không có hai
// worker nào nhận cùng một job.
func (s *Store) ClaimJob(ctx context.Context) (*Job, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin claim: %w", err)
	}
	defer tx.Rollback()

	var j Job
	var args string
	err = tx.QueryRowContext(ctx, `
		SELECT id, kind, args, attempt FROM jobs
		WHERE state = 'pending' AND run_at <= ?
		ORDER BY run_at LIMIT 1`, Now()).Scan(&j.ID, &j.Kind, &args, &j.Attempt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim job: %w", err)
	}
	j.Args = json.RawMessage(args)

	if _, err := tx.ExecContext(ctx,
		`UPDATE jobs SET state = 'running', attempt = attempt + 1, started_at = ? WHERE id = ?`,
		Now(), j.ID); err != nil {
		return nil, fmt.Errorf("mark job running: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit claim: %w", err)
	}
	j.State = JobRunning
	return &j, nil
}

// FinishJob đánh dấu job xong hoặc hỏng. Job hỏng được lên lịch chạy lại có backoff
// cho tới khi hết số lần thử.
func (s *Store) FinishJob(ctx context.Context, id string, cause error) error {
	if cause == nil {
		_, err := s.w.ExecContext(ctx,
			`UPDATE jobs SET state = 'done', finished_at = ?, error = '' WHERE id = ?`,
			Now(), id)
		if err != nil {
			return fmt.Errorf("finish job: %w", err)
		}
		return nil
	}

	var attempt, maxAttempts int
	if err := s.r.QueryRowContext(ctx,
		`SELECT attempt, max_attempts FROM jobs WHERE id = ?`, id).Scan(&attempt, &maxAttempts); err != nil {
		return fmt.Errorf("read job attempts: %w", err)
	}

	if attempt >= maxAttempts {
		_, err := s.w.ExecContext(ctx,
			`UPDATE jobs SET state = 'failed', finished_at = ?, error = ? WHERE id = ?`,
			Now(), cause.Error(), id)
		if err != nil {
			return fmt.Errorf("fail job: %w", err)
		}
		return nil
	}

	// Backoff lũy thừa: 1, 2, 4 phút. Một job hỏng không được quay vòng liên tục làm
	// nghẽn các job khác.
	delay := time.Duration(1<<uint(attempt-1)) * time.Minute
	_, err := s.w.ExecContext(ctx,
		`UPDATE jobs SET state = 'pending', run_at = ?, error = ? WHERE id = ?`,
		TimeAt(time.Now().Add(delay)), cause.Error(), id)
	if err != nil {
		return fmt.Errorf("retry job: %w", err)
	}
	return nil
}

// UpdateJobProgress cập nhật tiến độ để giao diện theo dõi.
func (s *Store) UpdateJobProgress(ctx context.Context, id string, done, total int64) error {
	_, err := s.w.ExecContext(ctx,
		`UPDATE jobs SET progress_done = ?, progress_total = ? WHERE id = ?`, done, total, id)
	if err != nil {
		return fmt.Errorf("update job progress: %w", err)
	}
	return nil
}

// GetJob trả về một job theo id.
func (s *Store) GetJob(ctx context.Context, id string) (Job, error) {
	var j Job
	var args, failure string
	err := s.r.QueryRowContext(ctx, `
		SELECT id, kind, args, state, attempt, progress_done, progress_total,
		       error, started_at, finished_at, created_at
		FROM jobs WHERE id = ?`, id).
		Scan(&j.ID, &j.Kind, &args, &j.State, &j.Attempt,
			&j.Progress.Done, &j.Progress.Total, &failure,
			&j.StartedAt, &j.FinishedAt, &j.CreatedAt)
	if err == sql.ErrNoRows {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("get job %q: %w", id, err)
	}
	j.Args = json.RawMessage(args)
	j.Error = failure
	return j, nil
}

// JobHealth trả về số job đang chờ và số job hỏng trong 24 giờ, phục vụ /health.
func (s *Store) JobHealth(ctx context.Context) (pending, failed24h int, err error) {
	err = s.r.QueryRowContext(ctx, `
		SELECT
		  (SELECT count(*) FROM jobs WHERE state = 'pending'),
		  (SELECT count(*) FROM jobs WHERE state = 'failed' AND finished_at >= ?)`,
		cutoff(1)).Scan(&pending, &failed24h)
	if err != nil {
		return 0, 0, fmt.Errorf("job health: %w", err)
	}
	return pending, failed24h, nil
}

// PruneJobs xóa job đã kết thúc quá hạn giữ.
func (s *Store) PruneJobs(ctx context.Context, days int) (int64, error) {
	res, err := s.w.ExecContext(ctx,
		`DELETE FROM jobs WHERE state IN ('done','cancelled') AND finished_at < ?`, cutoff(days))
	if err != nil {
		return 0, fmt.Errorf("prune jobs: %w", err)
	}
	return res.RowsAffected()
}
