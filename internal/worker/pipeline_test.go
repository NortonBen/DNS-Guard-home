package worker

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/benji/dnsguard/internal/catalog"
	"github.com/benji/dnsguard/internal/config"
	"github.com/benji/dnsguard/internal/enrich"
	"github.com/benji/dnsguard/internal/events"
	"github.com/benji/dnsguard/internal/graph"
	"github.com/benji/dnsguard/internal/ingest"
	"github.com/benji/dnsguard/internal/publish"
	"github.com/benji/dnsguard/internal/store"
)

// stubHTTP đóng vai bộ phân tích HTTP mà không chạm mạng.
//
// Đường đi thật ra Internet đã có test riêng ở internal/enrich; ở đây điều cần kiểm
// tra là phần nối các mảnh với nhau, và nó phải chạy được khi không có mạng.
type stubHTTP struct {
	facts enrich.HTTPFacts
	calls chan string
}

func (s *stubHTTP) Name() string { return "http" }

func (s *stubHTTP) Enrich(_ context.Context, domain string) (any, error) {
	select {
	case s.calls <- domain:
	default:
	}
	return s.facts, nil
}

// Toàn bộ luồng: gói tin tới cổng mirror → nhận diện domain → làm giàu → chấm điểm →
// dán nhãn → chuyển trạng thái → xuất bản ra file cho thiết bị mạng tải về.
//
// Mỗi mắt xích đều đã có test riêng. Test này kiểm tra thứ mà test đơn lẻ không thấy:
// các mắt xích có nối đúng vào nhau không.
func TestFullPipelineFromPacketToPublishedList(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	db, err := store.Open(filepath.Join(dir, "pipeline.db"), true)
	if err != nil {
		t.Fatalf("mở CSDL: %v", err)
	}
	defer db.Close()

	listsDir := filepath.Join(dir, "lists")
	cfg := config.Config{
		ListsDir: listsDir, PublishMinRatio: 0.0, StagingDays: 7,
		ConfirmTTLDays: 180, KeepSnapshots: 30, EnrichConcurrency: 2,
		ExternalEnabled: true, HTTPAnalysisEnabled: true,
	}

	// ---- 1. Bộ nhận TZSP trên cổng ngẫu nhiên ----
	listener := ingest.NewListener(ingest.Options{
		Addr: "127.0.0.1:0", BatchSize: 4, FlushEvery: 200 * time.Millisecond,
	}, db, log)

	go func() {
		if err := listener.Run(ctx); err != nil {
			t.Errorf("bộ nhận dừng: %v", err)
		}
	}()

	addr := waitFor(t, 3*time.Second, func() bool { return listener.LocalAddr() != "" },
		"bộ nhận không bind được cổng")
	_ = addr

	// ---- 2. Gửi gói mirror như thiết bị mạng vẫn làm ----
	const target = "px.thietbi.vn"
	conn, err := net.Dial("udp", listener.LocalAddr())
	if err != nil {
		t.Fatalf("mở socket gửi: %v", err)
	}
	defer conn.Close()

	// Cổng lọc phân tích đòi domain có lưu lượng thật, không phải một lần gõ nhầm.
	for i := range 8 {
		if _, err := conn.Write(tzspPacket(byte(20+i%3), target)); err != nil {
			t.Fatalf("gửi gói: %v", err)
		}
	}

	var domainID int64
	waitFor(t, 5*time.Second, func() bool {
		d, err := db.GetDomainByName(ctx, target)
		if err != nil {
			return false
		}
		domainID = d.ID
		return d.QueryCount >= 8
	}, "domain không xuất hiện trong CSDL sau khi gửi gói")

	d, err := db.GetDomainByName(ctx, target)
	if err != nil {
		t.Fatalf("đọc domain: %v", err)
	}
	if d.Status != store.StatusNew {
		t.Errorf("trạng thái ban đầu = %q, muốn new", d.Status)
	}
	if d.ETLD1 != "thietbi.vn" {
		t.Errorf("etld1 = %q, muốn thietbi.vn", d.ETLD1)
	}

	// ---- 3. Worker với bộ phân tích HTTP giả lập ----
	// Trả về đặc trưng của một điểm thu thập: không phục vụ trang, có header P3P và
	// cookie xuyên trang sống lâu.
	stub := &stubHTTP{
		calls: make(chan string, 4),
		facts: enrich.HTTPFacts{
			Status: 204, ContentType: "", IsHTML: false,
			P3P: `CP="NOI DSP COR"`, TrackingCookie: "uid", CookieMaxDays: 730,
		},
	}
	registry := enrich.NewRegistry(log)
	registry.Register(stub, 100, 100, true)

	runner := New(db, cfg,
		registry,
		publish.New(db, listsDir, cfg.PublishMinRatio, log),
		catalog.New(db, log),
		graph.New(db, log),
		events.NewBroker(log),
		log)

	// ---- 4. Làm giàu ----
	if err := runner.handle(ctx, store.Job{Kind: JobEnrich, ID: "t-enrich", Args: []byte("{}")}); err != nil {
		t.Fatalf("job làm giàu: %v", err)
	}

	select {
	case called := <-stub.calls:
		if called != target {
			t.Errorf("đã phân tích %q, muốn %q", called, target)
		}
	default:
		t.Fatal("bộ phân tích HTTP không được gọi — cổng lọc đã chặn nhầm")
	}

	facts, err := db.Facts(ctx, domainID)
	if err != nil {
		t.Fatalf("đọc facts: %v", err)
	}
	if _, ok := facts["http"]; !ok {
		t.Fatal("không lưu được kết quả phân tích HTTP")
	}

	// ---- 5. Chấm điểm và dán nhãn ----
	if err := runner.handle(ctx, store.Job{Kind: JobClassify, ID: "t-classify", Args: []byte("{}")}); err != nil {
		t.Fatalf("job chấm điểm: %v", err)
	}

	d, err = db.GetDomain(ctx, domainID)
	if err != nil {
		t.Fatalf("đọc lại domain: %v", err)
	}

	// 4,0 beacon + 2,5 P3P + 2,0 cookie xuyên trang
	if d.Score == nil || *d.Score < 8.0 {
		t.Fatalf("điểm = %v, muốn ít nhất 8,0 từ ba tín hiệu HTTP", d.Score)
	}
	if d.Category == nil || d.Category.Key != "telemetry" {
		t.Errorf("nhãn = %v, muốn telemetry (beacon là tín hiệu mạnh nhất)", d.Category)
	}
	if d.Status != store.StatusStaging {
		t.Errorf("trạng thái = %q, muốn staging sau khi vượt ngưỡng", d.Status)
	}

	// Bằng chứng phải truy ngược được, không chỉ có điểm tổng.
	kinds := map[string]bool{}
	for _, sig := range d.Signals {
		kinds[sig.Kind] = true
	}
	for _, want := range []string{"http_beacon", "http_p3p", "http_tracking_cookie"} {
		if !kinds[want] {
			t.Errorf("thiếu tín hiệu %q trong bằng chứng", want)
		}
	}

	// Chuyển sang staging phải để lại đúng một dòng nhật ký.
	history, err := db.History(ctx, domainID, 10)
	if err != nil {
		t.Fatalf("đọc nhật ký: %v", err)
	}
	if len(history) != 1 || history[0].Action != store.ActionStage {
		t.Fatalf("nhật ký = %d dòng %v, muốn đúng một dòng 'stage'", len(history), history)
	}
	if history[0].ActorLabel != store.ActorSystem {
		t.Errorf("người thực hiện = %q, muốn system", history[0].ActorLabel)
	}

	// ---- 6. Vòng đời: hết hạn canary thì tự chặn ----
	if _, err := db.Writer().ExecContext(ctx,
		`UPDATE domains SET staged_at = ? WHERE id = ?`,
		store.TimeAt(time.Now().Add(-8*24*time.Hour)), domainID); err != nil {
		t.Fatalf("lùi staged_at: %v", err)
	}
	if err := runner.handle(ctx, store.Job{Kind: JobLifecycle, ID: "t-life", Args: []byte("{}")}); err != nil {
		t.Fatalf("job vòng đời: %v", err)
	}

	d, err = db.GetDomain(ctx, domainID)
	if err != nil {
		t.Fatalf("đọc lại domain: %v", err)
	}
	if d.Status != store.StatusBlocked {
		t.Fatalf("trạng thái = %q, muốn blocked sau khi đủ ngày canary", d.Status)
	}

	// ---- 7. Xuất bản ra file cho thiết bị mạng ----
	if err := runner.handle(ctx, store.Job{Kind: JobPublish, ID: "t-pub", Args: []byte("{}")}); err != nil {
		t.Fatalf("job xuất bản: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(listsDir, "telemetry.txt"))
	if err != nil {
		t.Fatalf("đọc telemetry.txt: %v", err)
	}
	if !strings.Contains(string(body), "0.0.0.0 "+target) {
		t.Errorf("danh sách xuất bản thiếu %q:\n%s", target, body)
	}
}

// Domain trong danh sách bảo vệ không bao giờ bị tải trang, dù có lưu lượng cao.
//
// Không có cổng này, DNSGuard sẽ tự đi gõ cửa trang ngân hàng mà người dùng vừa truy
// cập — thứ không ai muốn máy chủ của mình làm.
func TestAnalysisGateSkipsProtectedDomains(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	db, err := store.Open(filepath.Join(dir, "gate.db"), true)
	if err != nil {
		t.Fatalf("mở CSDL: %v", err)
	}
	defer db.Close()

	now := store.Now()
	for _, name := range []string{"api.vnpay.vn", "px.quangcao.vn"} {
		if _, err := db.Writer().ExecContext(ctx, `
			INSERT INTO domains (name, name_rev, etld1, status, origin, query_count,
			                     first_seen, last_seen, created_at, updated_at)
			VALUES (?, ?, ?, 'new', 'discovered', 50, ?, ?, ?, ?)`,
			name, name, name, now, now, now, now); err != nil {
			t.Fatalf("thêm domain %q: %v", name, err)
		}
	}

	candidates, err := db.GatedCandidates(ctx, store.AnalysisGate{
		Source: "http", MinQueries: 5, MaxNegativeScore: -4.0, Limit: 50,
	})
	if err != nil {
		t.Fatalf("GatedCandidates: %v", err)
	}

	for _, c := range candidates {
		if c.Name == "api.vnpay.vn" {
			t.Error("domain được bảo vệ lọt qua cổng lọc — máy chủ sẽ tự tải trang ngân hàng")
		}
	}
	if len(candidates) != 1 || candidates[0].Name != "px.quangcao.vn" {
		t.Errorf("ứng viên = %v, muốn chỉ px.quangcao.vn", candidates)
	}
	_ = log
}

// waitFor chờ tới khi điều kiện đúng hoặc hết giờ.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return ""
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(msg)
	return ""
}

// tzspPacket dựng một gói TZSP bọc truy vấn DNS, giống thiết bị mạng gửi sang.
func tzspPacket(lastOctet byte, domain string) []byte {
	dns := make([]byte, 12)
	binary.BigEndian.PutUint16(dns[0:2], 0x1234)
	binary.BigEndian.PutUint16(dns[4:6], 1) // QDCOUNT
	for _, label := range strings.Split(domain, ".") {
		dns = append(dns, byte(len(label)))
		dns = append(dns, label...)
	}
	dns = append(dns, 0)
	dns = binary.BigEndian.AppendUint16(dns, 1) // QTYPE A
	dns = binary.BigEndian.AppendUint16(dns, 1) // QCLASS IN

	udp := make([]byte, 8+len(dns))
	binary.BigEndian.PutUint16(udp[0:2], 40000)
	binary.BigEndian.PutUint16(udp[2:4], 53)
	binary.BigEndian.PutUint16(udp[4:6], uint16(len(udp)))
	copy(udp[8:], dns)

	ip := make([]byte, 20+len(udp))
	ip[0] = 0x45
	binary.BigEndian.PutUint16(ip[2:4], uint16(len(ip)))
	ip[8] = 64
	ip[9] = 17 // UDP
	copy(ip[12:16], []byte{192, 168, 88, lastOctet})
	copy(ip[16:20], []byte{192, 168, 88, 1})
	copy(ip[20:], udp)

	eth := make([]byte, 0, 14+len(ip))
	eth = append(eth, 0x00, 0x11, 0x22, 0x33, 0x44, 0x55)
	eth = append(eth, 0x66, 0x77, 0x88, 0x99, 0xAA, 0xBB)
	eth = binary.BigEndian.AppendUint16(eth, 0x0800)
	eth = append(eth, ip...)

	// Header TZSP: phiên bản 1, kiểu 0, đóng gói Ethernet, thẻ kết thúc.
	return append([]byte{0x01, 0x00, 0x00, 0x01, 0x01}, eth...)
}

// failingEnricher luôn hỏng, để làm circuit breaker mở ra.
type failingEnricher struct{ name string }

func (f *failingEnricher) Name() string { return f.name }
func (f *failingEnricher) Enrich(context.Context, string) (any, error) {
	return nil, errors.New("máy chủ không phản hồi")
}

// Circuit breaker mở ra không được biến thành kết luận về từng domain.
//
// Một loạt domain chết làm breaker mở, rồi mọi domain xếp sau — kể cả domain hoàn
// toàn khỏe mạnh — bị đánh dấu thất bại và treo cache nhiều ngày dù chưa hề được thử.
// Lỗi này chỉ lộ ra khi chạy thật với dữ liệu có nhiều domain không phân giải được.
func TestCircuitBreakerDoesNotPoisonUntriedDomains(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	db, err := store.Open(filepath.Join(dir, "breaker.db"), true)
	if err != nil {
		t.Fatalf("mở CSDL: %v", err)
	}
	defer db.Close()

	// Đủ domain để vượt ngưỡng năm lỗi liên tiếp của circuit breaker.
	now := store.Now()
	const total = 12
	for i := range total {
		name := "d" + string(rune('a'+i)) + ".quangcao.vn"
		if _, err := db.Writer().ExecContext(ctx, `
			INSERT INTO domains (name, name_rev, etld1, status, origin, query_count,
			                     first_seen, last_seen, created_at, updated_at)
			VALUES (?, ?, 'quangcao.vn', 'new', 'discovered', 50, ?, ?, ?, ?)`,
			name, name, now, now, now, now); err != nil {
			t.Fatalf("thêm domain: %v", err)
		}
	}

	registry := enrich.NewRegistry(log)
	registry.Register(&failingEnricher{name: "http"}, 1000, 1000, true)

	runner := New(db, config.Config{EnrichConcurrency: 1, ListsDir: dir},
		registry,
		publish.New(db, dir, 0, log),
		catalog.New(db, log),
		graph.New(db, log),
		events.NewBroker(log),
		log)

	if err := runner.handle(ctx, store.Job{Kind: JobEnrich, ID: "t", Args: []byte("{}")}); err != nil {
		t.Fatalf("job làm giàu: %v", err)
	}

	// Chỉ những domain thật sự được thử mới có bản ghi lỗi. Domain bị breaker chặn
	// phải không để lại dấu vết nào, để vòng sau còn tra lại.
	var withError int
	if err := db.Reader().QueryRowContext(ctx,
		`SELECT count(*) FROM domain_facts WHERE source='http'`).Scan(&withError); err != nil {
		t.Fatalf("đếm bản ghi: %v", err)
	}

	if withError == total {
		t.Errorf("cả %d domain đều bị ghi lỗi — circuit breaker đã đầu độc những domain chưa được thử", total)
	}
	if withError == 0 {
		t.Error("không domain nào được thử — cổng lọc chặn nhầm")
	}
	t.Logf("%d/%d domain có bản ghi lỗi, phần còn lại bị breaker chặn và để dành vòng sau",
		withError, total)
}
