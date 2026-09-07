// Package enrich bổ sung thông tin ngoài cho domain ứng viên.
package enrich

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

// ErrDisabled báo rằng nguồn này bị tắt bằng cấu hình.
var ErrDisabled = errors.New("nguồn làm giàu bị tắt")

// ErrCircuitOpen báo rằng nguồn đang tạm ngưng vì lỗi liên tiếp.
var ErrCircuitOpen = errors.New("nguồn đang tạm ngưng sau nhiều lỗi liên tiếp")

// Enricher là một nguồn làm giàu. Mọi nguồn dùng chung interface này để runner
// không cần biết chúng khác nhau ở đâu.
type Enricher interface {
	// Name là khóa lưu trong domain_facts: dns, asn, cert, rdap, rank.
	Name() string
	// Enrich tra cứu một domain. Trả về dữ liệu sẽ được mã hóa JSON và lưu lại.
	Enrich(ctx context.Context, domain string) (any, error)
}

// breaker là circuit breaker cho một nguồn.
//
// Năm lỗi liên tiếp thì tạm ngưng nguồn đó 15 phút. Không có nó, một dịch vụ ngoài
// đang hỏng sẽ ăn hết ngân sách thời gian của mọi job làm giàu và làm chậm cả những
// nguồn đang khỏe.
type breaker struct {
	mu       sync.Mutex
	failures int
	openTill time.Time
}

const (
	breakerThreshold = 5
	breakerCooldown  = 15 * time.Minute
)

func (b *breaker) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return time.Now().After(b.openTill)
}

func (b *breaker) record(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err == nil {
		b.failures = 0
		return
	}
	b.failures++
	if b.failures >= breakerThreshold {
		b.openTill = time.Now().Add(breakerCooldown)
		b.failures = 0
	}
}

// guarded bọc một Enricher bằng giới hạn tốc độ và circuit breaker.
type guarded struct {
	inner   Enricher
	limiter *rate.Limiter
	breaker *breaker

	// enabled đọc và ghi được lúc chạy: một số nguồn bật/tắt từ giao diện, và bắt
	// khởi động lại dịch vụ chỉ để đổi một công tắc là không chấp nhận được.
	enabled atomic.Bool
}

func (g *guarded) Name() string { return g.inner.Name() }

func (g *guarded) Enrich(ctx context.Context, domain string) (any, error) {
	if !g.enabled.Load() {
		return nil, ErrDisabled
	}
	if !g.breaker.allow() {
		return nil, ErrCircuitOpen
	}
	if err := g.limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("chờ giới hạn tốc độ: %w", err)
	}

	data, err := g.inner.Enrich(ctx, domain)
	g.breaker.record(err)
	return data, err
}

// Registry giữ tập nguồn đang hoạt động.
type Registry struct {
	sources []*guarded
	log     *slog.Logger
}

// NewRegistry dựng registry rỗng.
func NewRegistry(log *slog.Logger) *Registry {
	return &Registry{log: log}
}

// Register thêm một nguồn kèm giới hạn tốc độ riêng.
//
// Giới hạn đặt riêng từng nguồn vì chúng khác nhau rất xa: phân giải DNS cục bộ chịu
// được hàng chục truy vấn mỗi giây, còn crt.sh sẽ chặn nếu vượt vài truy vấn mỗi phút.
func (r *Registry) Register(e Enricher, perSecond float64, burst int, enabled bool) {
	g := &guarded{
		inner:   e,
		limiter: rate.NewLimiter(rate.Limit(perSecond), burst),
		breaker: &breaker{},
	}
	g.enabled.Store(enabled)
	r.sources = append(r.sources, g)
}

// SetEnabled bật hoặc tắt một nguồn lúc chạy. Trả về false nếu không có nguồn đó.
func (r *Registry) SetEnabled(name string, enabled bool) bool {
	for _, s := range r.sources {
		if s.Name() == name {
			s.enabled.Store(enabled)
			return true
		}
	}
	return false
}

// IsEnabled cho biết một nguồn có đang hoạt động không.
func (r *Registry) IsEnabled(name string) bool {
	for _, s := range r.sources {
		if s.Name() == name {
			return s.enabled.Load()
		}
	}
	return false
}

// Has cho biết một nguồn đã được đăng ký chưa.
func (r *Registry) Has(name string) bool {
	for _, s := range r.sources {
		if s.Name() == name {
			return true
		}
	}
	return false
}

// Sources trả về danh sách nguồn đã đăng ký.
func (r *Registry) Sources() []Enricher {
	out := make([]Enricher, len(r.sources))
	for i, s := range r.sources {
		out[i] = s
	}
	return out
}

// Get tìm một nguồn theo tên.
func (r *Registry) Get(name string) (Enricher, bool) {
	for _, s := range r.sources {
		if s.Name() == name {
			return s, true
		}
	}
	return nil, false
}
