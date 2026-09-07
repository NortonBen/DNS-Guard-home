package store

import (
	"context"
	"fmt"
)

// RetentionResult là số dòng đã xóa của mỗi bảng.
type RetentionResult struct {
	QueryEvents  int64 `json:"query_events"`
	DomainHourly int64 `json:"domain_hourly"`
	Sessions     int64 `json:"sessions"`
	Snapshots    int64 `json:"snapshots"`
	Jobs         int64 `json:"jobs"`
}

// ApplyRetention xóa dữ liệu quá hạn theo chính sách ở docs/03-data-model.md §9.
//
// Bảng decisions không bao giờ bị xóa. Query log tái tạo được từ mạng, danh sách
// công khai tải lại được, nhưng lịch sử quyết định của con người thì mất là mất.
func (s *Store) ApplyRetention(ctx context.Context, logDays, hourlyDays, keepSnapshots int) (RetentionResult, error) {
	var out RetentionResult

	// Xóa theo lô: một DELETE trên nhiều triệu dòng giữ khóa ghi quá lâu và làm
	// ingest nghẽn. Vòng lặp nhả khóa giữa các lô.
	for {
		res, err := s.w.ExecContext(ctx, `
			DELETE FROM query_events WHERE id IN (
			  SELECT id FROM query_events WHERE occurred_at < ? LIMIT 20000
			)`, cutoff(logDays))
		if err != nil {
			return out, fmt.Errorf("prune query_events: %w", err)
		}
		n, _ := res.RowsAffected()
		out.QueryEvents += n
		if n == 0 {
			break
		}
	}

	res, err := s.w.ExecContext(ctx, `DELETE FROM domain_hourly WHERE hour < ?`, cutoff(hourlyDays))
	if err != nil {
		return out, fmt.Errorf("prune domain_hourly: %w", err)
	}
	out.DomainHourly, _ = res.RowsAffected()

	if out.Sessions, err = s.PruneSessions(ctx); err != nil {
		return out, err
	}
	if out.Snapshots, err = s.PruneSnapshots(ctx, keepSnapshots); err != nil {
		return out, err
	}
	if out.Jobs, err = s.PruneJobs(ctx, 7); err != nil {
		return out, err
	}
	return out, nil
}
