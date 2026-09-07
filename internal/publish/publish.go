// Package publish render danh sách chặn ra file tĩnh trên đĩa.
//
// Vì sao file tĩnh chứ không sinh động khi có request: router tải danh sách theo
// lịch và không giữ kết nối lâu. Sinh động nghĩa là mỗi lần tải phải quét hàng trăm
// nghìn dòng từ CSDL, và nếu CSDL chậm thì router timeout giữa chừng. File tĩnh cũng
// có nghĩa là danh sách đã xuất bản vẫn phục vụ được kể cả khi backend chết — đúng
// yêu cầu độ tin cậy ở docs/01-requirements.md §6.
package publish

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/benji/dnsguard/internal/store"
)

// ErrPublishBlocked báo rằng số lượng sụt quá ngưỡng nên việc xuất bản bị chặn.
var ErrPublishBlocked = errors.New("publish blocked")

// Result là kết quả xuất bản một phân loại.
type Result struct {
	Category   string `json:"category"`
	EntryCount int    `json:"entry_count"`
	Checksum   string `json:"checksum"`
	Changed    bool   `json:"changed"`
	Path       string `json:"path"`
}

// Publisher render và ghi danh sách.
type Publisher struct {
	store    *store.Store
	dir      string
	minRatio float64
	log      *slog.Logger
}

// New dựng Publisher. minRatio là tỉ lệ tối thiểu so với lần xuất bản trước; dưới
// mức đó thì từ chối ghi.
func New(s *store.Store, dir string, minRatio float64, log *slog.Logger) *Publisher {
	return &Publisher{store: s, dir: dir, minRatio: minRatio, log: log}
}

// PublishAll xuất bản mọi phân loại đang bật cộng file gộp.
func (p *Publisher) PublishAll(ctx context.Context, only []string, actor string) ([]Result, error) {
	targets, err := p.store.PublishTargets(ctx)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		return nil, fmt.Errorf("create lists dir %q: %w", p.dir, err)
	}

	wanted := map[string]bool{}
	for _, k := range only {
		wanted[k] = true
	}

	var results []Result
	var firstErr error

	for _, t := range targets {
		if len(wanted) > 0 && !wanted[t.CategoryKey] {
			continue
		}
		r, err := p.publishOne(ctx, t.CategoryID, t.CategoryKey, t.PublishPath, actor)
		if err != nil {
			// Một phân loại hỏng không được làm hỏng các phân loại khác: danh sách
			// ads vẫn phải cập nhật được khi danh sách malware gặp vấn đề.
			p.log.Error("xuất bản thất bại", "category", t.CategoryKey, "err", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		results = append(results, r)
	}

	if len(wanted) == 0 {
		r, err := p.publishOne(ctx, 0, "all", "all.txt", actor)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
		} else {
			results = append(results, r)
		}
	}

	return results, firstErr
}

// publishOne render một danh sách và ghi ra đĩa nếu nội dung đổi.
func (p *Publisher) publishOne(ctx context.Context, categoryID int64,
	categoryKey, fileName, actor string) (Result, error) {

	domains, err := p.store.BlockedDomains(ctx, categoryID)
	if err != nil {
		return Result{}, err
	}

	body := render(categoryKey, domains)
	sum := sha256.Sum256([]byte(body))
	checksum := "sha256:" + hex.EncodeToString(sum[:])
	path := filepath.Join(p.dir, fileName)

	prev, err := p.store.LastSnapshot(ctx, categoryID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		// Lần xuất bản đầu tiên: không có gì để so sánh, luôn cho phép.
	case err != nil:
		return Result{}, err
	default:
		// Bảo vệ sụt giảm. Một nguồn ngoài hỏng hoặc một thao tác nhầm có thể xóa
		// phần lớn danh sách; nếu cứ thế ghi đè thì cả mạng mất chặn cùng lúc và
		// không ai nhận ra ngay.
		if prev.EntryCount > 0 {
			ratio := float64(len(domains)) / float64(prev.EntryCount)
			if ratio < p.minRatio {
				return Result{}, fmt.Errorf(
					"%w: %s còn %d mục so với %d lần trước (%.0f%% < %.0f%%)",
					ErrPublishBlocked, categoryKey, len(domains), prev.EntryCount,
					ratio*100, p.minRatio*100)
			}
		}
		// Nội dung không đổi thì không ghi và không tạo snapshot mới: nhờ đó lịch sử
		// xuất bản chỉ chứa những lần thật sự có thay đổi.
		if prev.Checksum == checksum {
			if _, statErr := os.Stat(path); statErr == nil {
				return Result{Category: categoryKey, EntryCount: len(domains),
					Checksum: checksum, Changed: false, Path: path}, nil
			}
			// File đã mất trên đĩa nhưng snapshot còn: ghi lại từ dữ liệu hiện có.
		}
	}

	if err := writeAtomic(path, body); err != nil {
		return Result{}, err
	}
	if _, err := p.store.SaveSnapshot(ctx, categoryID, domains, checksum, path, actor); err != nil {
		return Result{}, err
	}

	p.log.Info("đã xuất bản danh sách",
		"category", categoryKey, "entries", len(domains), "checksum", checksum)

	return Result{Category: categoryKey, EntryCount: len(domains),
		Checksum: checksum, Changed: true, Path: path}, nil
}

// Rollback ghi lại file từ nội dung của một snapshot cũ.
//
// Tạo snapshot mới thay vì xóa lịch sử: việc quay lại cũng là một quyết định, và nó
// phải để lại dấu vết như mọi quyết định khác.
func (p *Publisher) Rollback(ctx context.Context, snapshotID int64, actor string) (Result, error) {
	snapshots, err := p.store.ListSnapshots(ctx, "", 200)
	if err != nil {
		return Result{}, err
	}
	var target *store.Snapshot
	for i := range snapshots {
		if snapshots[i].ID == snapshotID {
			target = &snapshots[i]
			break
		}
	}
	if target == nil {
		return Result{}, store.ErrNotFound
	}

	domains, err := p.store.SnapshotDomains(ctx, snapshotID)
	if err != nil {
		return Result{}, err
	}

	body := render(target.CategoryKey, domains)
	sum := sha256.Sum256([]byte(body))
	checksum := "sha256:" + hex.EncodeToString(sum[:])

	if err := writeAtomic(target.FilePath, body); err != nil {
		return Result{}, err
	}

	categoryID, err := p.categoryIDOf(ctx, target.CategoryKey)
	if err != nil {
		return Result{}, err
	}
	label := fmt.Sprintf("%s (rollback từ #%d)", actor, snapshotID)
	if _, err := p.store.SaveSnapshot(ctx, categoryID, domains, checksum, target.FilePath, label); err != nil {
		return Result{}, err
	}

	return Result{Category: target.CategoryKey, EntryCount: len(domains),
		Checksum: checksum, Changed: true, Path: target.FilePath}, nil
}

func (p *Publisher) categoryIDOf(ctx context.Context, key string) (int64, error) {
	if key == "" || key == "all" {
		return 0, nil
	}
	targets, err := p.store.PublishTargets(ctx)
	if err != nil {
		return 0, err
	}
	for _, t := range targets {
		if t.CategoryKey == key {
			return t.CategoryID, nil
		}
	}
	return 0, nil
}

// render dựng nội dung file theo định dạng hosts.
func render(category string, domains []string) string {
	var sb strings.Builder
	sb.Grow(len(domains)*24 + 128)

	fmt.Fprintf(&sb, "# DNSGuard — %s\n", category)
	fmt.Fprintf(&sb, "# %d domain · %s\n", len(domains), time.Now().UTC().Format(time.RFC3339))
	for _, d := range domains {
		sb.WriteString("0.0.0.0 ")
		sb.WriteString(d)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// writeAtomic ghi ra file tạm cùng thư mục rồi đổi tên.
//
// Bắt buộc, không phải tối ưu: router có thể tải file đúng lúc đang ghi, và một file
// dở dang nghĩa là danh sách chặn thiếu một nửa. os.Rename trong cùng hệ thống file
// là thao tác nguyên tử, nên đầu đọc chỉ thấy bản cũ hoặc bản mới, không bao giờ
// thấy trạng thái ở giữa.
func writeAtomic(path, body string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("create temp file in %q: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op nếu Rename đã thành công

	if _, err := tmp.WriteString(body); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	// Đẩy xuống đĩa trước khi đổi tên: mất điện ngay sau Rename không được để lại
	// một file có tên đúng nhưng nội dung rỗng.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename %q → %q: %w", tmpName, path, err)
	}
	return nil
}
