package monitor

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func TestFirstSampleReportsNoCPU(t *testing.T) {
	// Lần đo đầu không có mốc để trừ. Báo bừa một con số ở đây nghĩa là điểm đầu
	// của mọi biểu đồ đều là một đỉnh giả — thường rất cao, vì nó gộp toàn bộ thời
	// gian CPU tiêu tốn lúc khởi động.
	s := New()
	if got := s.Sample().CPUPercent; got != 0 {
		t.Errorf("CPU lần đo đầu = %v, muốn 0", got)
	}
}

func TestSampleReadsRealProcessState(t *testing.T) {
	s := New()
	s.Sample()

	// Đốt CPU thật để phép trừ có gì để đo.
	deadline := time.Now().Add(60 * time.Millisecond)
	for time.Now().Before(deadline) {
	}

	got := s.Sample()
	if got.CPUPercent <= 0 {
		t.Errorf("CPU = %v sau khi bận liên tục, muốn lớn hơn 0", got.CPUPercent)
	}
	if got.RSSBytes <= 0 {
		t.Errorf("RSS = %d, muốn lớn hơn 0", got.RSSBytes)
	}
	if got.Goroutines <= 0 {
		t.Errorf("goroutine = %d, muốn lớn hơn 0", got.Goroutines)
	}
	if got.At.IsZero() {
		t.Error("mốc thời gian rỗng")
	}
}

// recordingSink thu lại các lô đã ghi.
type recordingSink struct {
	mu      sync.Mutex
	batches [][]Sample
}

func (r *recordingSink) WriteResourceSamples(_ context.Context, samples []Sample) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Sao chép: Run dùng lại đúng mảng đệm đó cho lô sau.
	r.batches = append(r.batches, append([]Sample(nil), samples...))
	return nil
}

func (r *recordingSink) total() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, b := range r.batches {
		n += len(b)
	}
	return n
}

func TestRunFlushesPartialBatchOnShutdown(t *testing.T) {
	// Lô cuối phải ghi được lúc tắt máy. Nếu flush dùng chính ctx đã hủy thì tới
	// một phút dữ liệu biến mất mỗi lần khởi động lại, và không ai để ý.
	//
	// Nhịp đo đặt chậm hơn hẳn thời gian chờ để bộ đệm chắc chắn chưa đầy một lô:
	// nhờ vậy mọi mẫu nhìn thấy ở sink đều chỉ có thể đến từ đường tắt máy, chứ
	// không phải từ lần ghi định kỳ.
	sink := &recordingSink{}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		Run(ctx, New(), sink, 25*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
		close(done)
	}()

	time.Sleep(70 * time.Millisecond) // hai, ba nhịp — chưa tới sáu
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run không dừng sau khi hủy ctx")
	}

	total := sink.total()
	if total == 0 {
		t.Fatal("bộ đệm mất trắng lúc tắt máy")
	}
	if total >= flushEvery {
		t.Fatalf("ghi %d mẫu — lô đã đầy nên test không chứng minh được đường tắt máy", total)
	}
}
