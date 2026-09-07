// Package monitor đo mức tiêu thụ tài nguyên của chính tiến trình này.
//
// Không dùng thư viện đo hệ thống: chúng kéo theo phụ thuộc native, phá vỡ việc biên
// dịch tĩnh, và phần cần dùng chỉ là bộ nhớ thường trú với thời gian CPU. Cả hai đọc
// được bằng thư viện chuẩn.
package monitor

import (
	"context"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Sample là một lần đo.
type Sample struct {
	At time.Time

	// CPUPercent là phần trăm một lõi đã dùng kể từ lần đo trước. Vượt 100 khi tiến
	// trình chạy nhiều lõi cùng lúc.
	CPUPercent float64
	// RSSBytes là bộ nhớ thường trú — con số phản ánh đúng thứ hệ điều hành thấy,
	// khác với bộ nhớ heap mà Go tự quản lý.
	RSSBytes int64
	// HeapBytes là phần heap đang dùng, hữu ích khi tách rò rỉ trong Go khỏi bộ nhớ
	// do runtime giữ lại.
	HeapBytes  int64
	Goroutines int
}

// Sampler đo lặp lại theo thời gian.
//
// Phải giữ trạng thái giữa hai lần đo vì CPU chỉ đo được bằng hiệu: hệ điều hành cho
// biết tổng thời gian CPU đã dùng, còn phần trăm là hiệu chia cho thời gian trôi qua.
type Sampler struct {
	mu       sync.Mutex
	lastCPU  time.Duration
	lastAt   time.Time
	pageSize int64
}

// New dựng bộ đo.
func New() *Sampler {
	return &Sampler{pageSize: int64(os.Getpagesize())}
}

// Sample đo một lần. Lần gọi đầu trả về CPU bằng 0 vì chưa có mốc để trừ.
func (s *Sampler) Sample() Sample {
	now := time.Now()
	cpu := processCPUTime()

	s.mu.Lock()
	var percent float64
	if !s.lastAt.IsZero() {
		if elapsed := now.Sub(s.lastAt); elapsed > 0 {
			percent = float64(cpu-s.lastCPU) / float64(elapsed) * 100
		}
	}
	s.lastCPU, s.lastAt = cpu, now
	s.mu.Unlock()

	// Số âm nghĩa là đồng hồ hệ thống nhảy lùi; báo 0 thay vì một giá trị vô nghĩa.
	if percent < 0 {
		percent = 0
	}

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	return Sample{
		At:         now.UTC(),
		CPUPercent: percent,
		RSSBytes:   s.residentBytes(int64(mem.Sys)),
		HeapBytes:  int64(mem.HeapAlloc),
		Goroutines: runtime.NumGoroutine(),
	}
}

// processCPUTime trả về tổng thời gian CPU tiến trình đã dùng.
//
// Getrusage có trên cả Linux lẫn macOS nên không cần tách theo hệ điều hành.
func processCPUTime() time.Duration {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return 0
	}
	toDuration := func(tv syscall.Timeval) time.Duration {
		return time.Duration(tv.Sec)*time.Second + time.Duration(tv.Usec)*time.Microsecond
	}
	return toDuration(usage.Utime) + toDuration(usage.Stime)
}

// residentBytes đọc bộ nhớ thường trú, lùi về số do runtime báo khi không đọc được.
//
// Trên Linux đọc /proc/self/statm cho con số hiện tại chính xác. Trên hệ điều hành
// khác không có cách nào lấy RSS hiện tại mà không cần cgo, nên dùng lượng bộ nhớ
// runtime đã xin của hệ điều hành — cao hơn RSS thật nhưng cùng bậc và cùng xu hướng.
func (s *Sampler) residentBytes(fallback int64) int64 {
	raw, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return fallback
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 2 {
		return fallback
	}
	pages, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return fallback
	}
	return pages * s.pageSize
}

// Sink là nơi bộ đo ghi mẫu xuống. Khai báo ở đây, nơi tiêu thụ, để package monitor
// không cần biết gì về CSDL.
type Sink interface {
	WriteResourceSamples(ctx context.Context, samples []Sample) error
}

// flushEvery là số mẫu gom lại trước khi ghi.
//
// Sáu mẫu ở nhịp mười giây là một phút một lần ghi. Ghi từng mẫu một nghĩa là 8.640
// giao dịch mỗi ngày chỉ để lưu vài con số — lãng phí vòng ghi trên thẻ nhớ mà không
// đổi lại được gì, vì dữ liệu này chỉ để vẽ biểu đồ.
const flushEvery = 6

// Run đo lặp lại cho tới khi ctx bị hủy.
func Run(ctx context.Context, s *Sampler, sink Sink, every time.Duration, log *slog.Logger) {
	if every <= 0 {
		every = 10 * time.Second
	}

	ticker := time.NewTicker(every)
	defer ticker.Stop()

	buffer := make([]Sample, 0, flushEvery)

	flush := func() {
		if len(buffer) == 0 {
			return
		}
		// context.WithoutCancel: lúc tắt máy, lô cuối vẫn phải ghi thay vì mất trắng.
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()

		if err := sink.WriteResourceSamples(writeCtx, buffer); err != nil {
			log.Warn("ghi mẫu đo tài nguyên thất bại", "count", len(buffer), "err", err)
		}
		buffer = buffer[:0]
	}

	// Lấy một mẫu ngay để mốc CPU có điểm bắt đầu; mẫu này chưa có phần trăm hợp lệ
	// nên không lưu.
	s.Sample()

	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case <-ticker.C:
			buffer = append(buffer, s.Sample())
			if len(buffer) >= flushEvery {
				flush()
			}
		}
	}
}
