package pcap

import (
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newRecorder(t *testing.T, opts Options) *Recorder {
	t.Helper()
	if opts.Dir == "" {
		opts.Dir = t.TempDir()
	}
	return New(opts, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// runUntilWritten chạy bộ ghi tới khi đủ số khung được ghi xuống, rồi dừng.
func runUntilWritten(t *testing.T, r *Recorder, want int64) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.Run(ctx)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for r.Stats().FramesWritten < want && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done

	if got := r.Stats().FramesWritten; got < want {
		t.Fatalf("ghi được %d khung, muốn %d", got, want)
	}
}

// frame dựng một khung Ethernet có độ dài cho trước.
func frame(n int) []byte {
	f := make([]byte, n)
	for i := range f {
		f[i] = byte(i)
	}
	return f
}

func TestWritesValidPcapFile(t *testing.T) {
	dir := t.TempDir()
	r := newRecorder(t, Options{Dir: dir})

	r.Write(frame(64))
	r.Write(frame(128))
	runUntilWritten(t, r, 2)

	files, err := r.Files()
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("có %d file, muốn 1", len(files))
	}

	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("đọc file pcap: %v", err)
	}
	if len(data) != globalHeaderLen+2*recordHeaderLen+64+128 {
		t.Fatalf("file dài %d byte, không khớp kích thước mong đợi", len(data))
	}

	if magic := binary.LittleEndian.Uint32(data[0:4]); magic != magicMicroseconds {
		t.Errorf("magic = %#x, muốn %#x", magic, magicMicroseconds)
	}
	if v := binary.LittleEndian.Uint16(data[4:6]); v != versionMajor {
		t.Errorf("version major = %d, muốn %d", v, versionMajor)
	}
	if link := binary.LittleEndian.Uint32(data[20:24]); link != linkTypeEthernet {
		t.Errorf("link type = %d, muốn %d (Ethernet)", link, linkTypeEthernet)
	}

	// Bản ghi đầu tiên: incl_len và orig_len phải bằng nhau và bằng độ dài khung.
	off := globalHeaderLen
	incl := binary.LittleEndian.Uint32(data[off+8 : off+12])
	orig := binary.LittleEndian.Uint32(data[off+12 : off+16])
	if incl != 64 || orig != 64 {
		t.Errorf("bản ghi đầu: incl=%d orig=%d, muốn cả hai bằng 64", incl, orig)
	}
	if ts := binary.LittleEndian.Uint32(data[off : off+4]); ts == 0 {
		t.Error("dấu thời gian bằng 0")
	}
}

func TestRotatesOnSize(t *testing.T) {
	dir := t.TempDir()
	// Trần nhỏ để hai khung không vừa cùng một file.
	r := newRecorder(t, Options{Dir: dir, MaxFileBytes: globalHeaderLen + recordHeaderLen + 100})

	// Ghi hết vào hàng đợi rồi mới chạy: cả ba lần xoay sẽ rơi vào cùng một giây, đúng
	// trường hợp mà tên file chỉ có dấu thời gian sẽ đụng độ.
	for range 3 {
		r.Write(frame(80))
	}
	runUntilWritten(t, r, 3)

	files, err := r.Files()
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if len(files) < 2 {
		t.Fatalf("có %d file, muốn ít nhất 2 sau khi xoay vòng", len(files))
	}
	// Mỗi file phải tự mở được: file thứ hai cũng phải có header toàn cục riêng.
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("đọc %s: %v", f, err)
		}
		if len(data) < globalHeaderLen {
			t.Errorf("%s ngắn hơn header toàn cục", filepath.Base(f))
			continue
		}
		if magic := binary.LittleEndian.Uint32(data[0:4]); magic != magicMicroseconds {
			t.Errorf("%s thiếu header toàn cục riêng", filepath.Base(f))
		}
	}
}

// Hàng đợi đầy phải bỏ khung chứ không chặn người gọi: bộ ghi nằm ngay sau vòng đọc
// socket, chặn ở đó sẽ mất gói ở tầng nhân.
func TestWriteNeverBlocks(t *testing.T) {
	r := newRecorder(t, Options{QueueSize: 2})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 100 {
			r.Write(frame(64))
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Write bị chặn khi hàng đợi đầy")
	}
	if r.Stats().FramesDropped == 0 {
		t.Error("không đếm khung bị bỏ dù hàng đợi chỉ chứa được 2")
	}
}

// Người gọi dùng lại bộ đệm đọc cho gói kế tiếp, nên bộ ghi phải tự sao chép.
func TestWriteCopiesFrame(t *testing.T) {
	dir := t.TempDir()
	r := newRecorder(t, Options{Dir: dir})

	buf := frame(64)
	r.Write(buf)
	for i := range buf {
		buf[i] = 0xFF // ghi đè ngay sau khi gọi, như vòng đọc socket vẫn làm
	}
	runUntilWritten(t, r, 1)

	files, _ := r.Files()
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("đọc file pcap: %v", err)
	}
	payload := data[globalHeaderLen+recordHeaderLen:]
	if payload[0] != 0x00 || payload[1] != 0x01 {
		t.Errorf("nội dung khung bị hỏng: %x — bộ ghi không sao chép bộ đệm", payload[:4])
	}
}
