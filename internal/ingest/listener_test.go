package ingest

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"
)

// captureSink giữ lại những gì bộ nhận ghi xuống, để test kiểm chứng.
type captureSink struct {
	mu          sync.Mutex
	events      []Event
	resolutions []Resolution
}

func (c *captureSink) WriteEvents(_ context.Context, evs []Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, evs...)
	return nil
}

func (c *captureSink) WriteResolutions(_ context.Context, rs []Resolution) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resolutions = append(c.resolutions, rs...)
	return nil
}

func (c *captureSink) counts() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.events), len(c.resolutions)
}

func (c *captureSink) snapshot() ([]Event, []Resolution) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Event(nil), c.events...), append([]Resolution(nil), c.resolutions...)
}

// Đường đi đầy đủ: datagram TZSP vào socket UDP, ra thành sự kiện và lượt phân giải.
//
// Các test khác kiểm từng tầng bóc gói riêng lẻ; test này kiểm phần nối chúng lại —
// đúng chỗ mà một nhánh bị đặt nhầm sẽ khiến gói trả lời im lặng biến mất.
func TestListenerRoutesQueriesAndAnswers(t *testing.T) {
	sink := &captureSink{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	l := NewListener(Options{
		Addr: "127.0.0.1:0", BatchSize: 1, FlushEvery: 20 * time.Millisecond,
	}, sink, log)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go l.Run(ctx)

	addr := waitForBind(t, l)
	conn, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatalf("mở socket gửi: %v", err)
	}
	defer conn.Close()

	client := netip.MustParseAddr("192.168.88.20")
	router := netip.MustParseAddr("192.168.88.1")

	query := buildTZSP(t, client, dnsPort,
		buildDNSQuery(t, "ads.example.com", TypeA, false), 0)
	answer := buildTZSPPorts(t, router, dnsPort, 40000,
		buildDNSAnswer(t, "ads.example.com", TypeA, []answerRR{
			{rrType: TypeA, ttl: 300, rdata: ipRData(t, "104.21.5.6")},
		}), 0)

	if _, err := conn.Write(query); err != nil {
		t.Fatalf("gửi truy vấn: %v", err)
	}
	if _, err := conn.Write(answer); err != nil {
		t.Fatalf("gửi câu trả lời: %v", err)
	}

	waitFor(t, func() bool {
		e, r := sink.counts()
		return e >= 1 && r >= 1
	}, "bộ nhận không ghi đủ một truy vấn và một lượt phân giải")

	events, resolutions := sink.snapshot()
	if events[0].Domain != "ads.example.com" || events[0].Client != client.String() {
		t.Errorf("sự kiện = %q từ %q; muốn ads.example.com từ %s",
			events[0].Domain, events[0].Client, client)
	}
	res := resolutions[0]
	if res.Domain != "ads.example.com" || res.ETLD1 != "example.com" {
		t.Errorf("phân giải = %q (eTLD+1 %q)", res.Domain, res.ETLD1)
	}
	if len(res.IPs) != 1 || res.IPs[0].String() != "104.21.5.6" {
		t.Errorf("địa chỉ = %v, muốn [104.21.5.6]", res.IPs)
	}
	if res.TTL != 300 {
		t.Errorf("ttl = %d, muốn 300", res.TTL)
	}

	if stats := l.Stats(); stats.ResolutionsAccepted < 1 || stats.EventsAccepted < 1 {
		t.Errorf("thống kê = %d truy vấn, %d phân giải; muốn cả hai ≥ 1",
			stats.EventsAccepted, stats.ResolutionsAccepted)
	}
}

func waitForBind(t *testing.T, l *Listener) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if addr := l.LocalAddr(); addr != "" {
			return addr
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("bộ nhận không bind được trong 5 giây")
	return ""
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(msg)
}
