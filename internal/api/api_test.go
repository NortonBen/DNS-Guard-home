package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/benji/dnsguard/internal/auth"
	"github.com/benji/dnsguard/internal/catalog"
	"github.com/benji/dnsguard/internal/classify"
	"github.com/benji/dnsguard/internal/config"
	"github.com/benji/dnsguard/internal/enrich"
	"github.com/benji/dnsguard/internal/events"
	"github.com/benji/dnsguard/internal/graph"
	"github.com/benji/dnsguard/internal/publish"
	"github.com/benji/dnsguard/internal/store"
	"github.com/benji/dnsguard/internal/worker"
)

// harness là một máy chủ hoàn chỉnh chạy trên CSDL thật.
//
// Không mock CSDL: mock chỉ chứng minh được rằng mock hoạt động. Toàn bộ đường đi
// từ HTTP xuống SQLite đều chạy thật, chỉ có mạng ra ngoài là không.
type harness struct {
	t        *testing.T
	server   *httptest.Server
	store    *store.Store
	srv      *Server
	listsDir string

	cookie string
	csrf   string
}

// setListsAllowCIDR đổi danh sách dải IP cho phép rồi dựng lại router.
func (h *harness) setListsAllowCIDR(cidrs ...string) {
	h.t.Helper()
	h.srv.cfg.ListsAllowCIDR = cidrs
	h.server.Config.Handler = h.srv.Handler()
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "api.db"), true)
	if err != nil {
		t.Fatalf("mở store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	listsDir := filepath.Join(dir, "lists")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfg := config.Config{
		ListsDir: listsDir, PublishMinRatio: 0.5, StagingDays: 7,
		ConfirmTTLDays: 180, KeepSnapshots: 30, EnrichConcurrency: 1,
		SessionTTL: time.Hour,
	}

	bus := events.NewBroker(log)
	publisher := publish.New(db, listsDir, cfg.PublishMinRatio, cfg.PublishSink, log)
	runner := worker.New(db, cfg, enrich.NewRegistry(log), publisher,
		catalog.New(db, log), graph.New(db, log), bus, log)

	// Registry thật, có đăng ký VirusTotal nhưng chưa có khóa: đây đúng là trạng thái
	// một máy chủ mới cài, và cũng là điều kiện các test cấu hình khóa cần.
	enrichers := enrich.NewRegistry(log)
	enrichers.Register(enrich.NewVirusTotal(""), 1, 1, false)

	srv := New(Options{
		Store: db, Auth: auth.NewService(db, cfg.SessionTTL), Config: cfg,
		Publisher: publisher, Worker: runner, Bus: bus, Log: log,
		Enrichers: enrichers,
	})

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	h := &harness{t: t, server: ts, store: db, srv: srv, listsDir: listsDir}

	hash, err := auth.HashPassword("mật-khẩu-thử")
	if err != nil {
		t.Fatalf("băm mật khẩu: %v", err)
	}
	if _, err := db.CreateUser(context.Background(), "admin", hash, store.RoleAdmin); err != nil {
		t.Fatalf("tạo tài khoản: %v", err)
	}
	return h
}

// do gửi một request kèm cookie phiên và CSRF token nếu đã đăng nhập.
func (h *harness) do(method, path string, body any) (*http.Response, []byte) {
	h.t.Helper()

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			h.t.Fatalf("mã hóa body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		h.t.Fatalf("dựng request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if h.cookie != "" {
		req.Header.Set("Cookie", h.cookie)
	}
	if h.csrf != "" {
		req.Header.Set("X-CSRF-Token", h.csrf)
	}

	resp, err := h.server.Client().Do(req)
	if err != nil {
		h.t.Fatalf("gửi request: %v", err)
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		h.t.Fatalf("đọc phản hồi: %v", err)
	}
	return resp, payload
}

func (h *harness) login() {
	h.t.Helper()

	resp, body := h.do(http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "admin", "password": "mật-khẩu-thử"})
	if resp.StatusCode != http.StatusOK {
		h.t.Fatalf("đăng nhập = %d: %s", resp.StatusCode, body)
	}

	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie {
			h.cookie = c.Name + "=" + c.Value
		}
	}
	var out struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		h.t.Fatalf("giải mã phản hồi đăng nhập: %v", err)
	}
	h.csrf = out.CSRFToken

	if h.cookie == "" || h.csrf == "" {
		h.t.Fatal("đăng nhập không trả về cookie hoặc CSRF token")
	}
}

// seedBlockedDomain thêm một domain đã bị chặn để có dữ liệu xuất bản.
func (h *harness) seedDomain(name, status string) int64 {
	h.t.Helper()
	now := store.Now()
	res, err := h.store.Writer().Exec(`
		INSERT INTO domains (name, name_rev, etld1, status, origin, category_id,
		                     first_seen, last_seen, created_at, updated_at)
		VALUES (?, ?, 'example.com', ?, 'discovered',
		        (SELECT id FROM categories WHERE key = 'ads'), ?, ?, ?, ?)`,
		name, name, status, now, now, now, now)
	if err != nil {
		h.t.Fatalf("thêm domain %q: %v", name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		h.t.Fatalf("lấy id domain: %v", err)
	}
	return id
}

func TestHealthNeedsNoAuth(t *testing.T) {
	h := newHarness(t)

	resp, body := h.do(http.MethodGet, "/health", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/health = %d: %s", resp.StatusCode, body)
	}

	var out struct {
		Status string                    `json:"status"`
		Checks map[string]map[string]any `json:"checks"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("giải mã: %v", err)
	}
	if out.Checks["database"]["ok"] != true {
		t.Error("kiểm tra CSDL phải báo ok")
	}
	// Chưa nhận truy vấn nào: phải báo suy giảm chứ không báo khỏe mạnh. Đây là lỗi
	// im lặng nguy hiểm nhất của hệ thống nên nó phải hiện ra ngay.
	if out.Status != "degraded" {
		t.Errorf("status = %q, muốn degraded khi chưa có truy vấn nào", out.Status)
	}
}

func TestProtectedEndpointsRequireAuth(t *testing.T) {
	h := newHarness(t)

	for _, path := range []string{"/api/v1/domains", "/api/v1/categories", "/api/v1/stats/overview"} {
		resp, _ := h.do(http.MethodGet, path, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s = %d, muốn 401", path, resp.StatusCode)
		}
	}
}

// Request thay đổi trạng thái phải có CSRF token hợp lệ.
func TestMutationRequiresCSRFToken(t *testing.T) {
	h := newHarness(t)
	h.login()
	id := h.seedDomain("ads.example.com", store.StatusNew)

	saved := h.csrf
	h.csrf = "token-sai"
	resp, _ := h.do(http.MethodPost, "/api/v1/domains/"+itoa(id)+"/decision",
		map[string]string{"action": "block", "category": "ads", "reason": "thử"})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("CSRF sai = %d, muốn 403", resp.StatusCode)
	}

	h.csrf = saved
	resp, body := h.do(http.MethodPost, "/api/v1/domains/"+itoa(id)+"/decision",
		map[string]string{"action": "block", "category": "ads", "reason": "thử"})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("CSRF đúng = %d: %s", resp.StatusCode, body)
	}
}

// Luồng hoàn chỉnh: đăng nhập → chặn domain → xuất bản → router tải danh sách về.
func TestFullFlowFromDecisionToPublishedList(t *testing.T) {
	h := newHarness(t)
	h.login()

	id := h.seedDomain("ads.example.com", store.StatusStaging)

	resp, body := h.do(http.MethodPost, "/api/v1/domains/"+itoa(id)+"/decision",
		map[string]string{"action": "block", "category": "ads", "reason": "tracker đã xác nhận"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("quyết định = %d: %s", resp.StatusCode, body)
	}

	var domain store.Domain
	if err := json.Unmarshal(body, &domain); err != nil {
		t.Fatalf("giải mã domain: %v", err)
	}
	if domain.Status != store.StatusBlocked {
		t.Errorf("status = %q, muốn blocked", domain.Status)
	}
	if !domain.IsManual {
		t.Error("quyết định thủ công phải đặt is_manual")
	}

	// Xuất bản đồng bộ để test không phụ thuộc vào bộ lập lịch nền.
	publisher := publish.New(h.store, h.listsDir, 0.5, "",
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := publisher.PublishAll(context.Background(), []string{"ads"}, "test"); err != nil {
		t.Fatalf("xuất bản: %v", err)
	}

	// Router tải danh sách về: không cần xác thực.
	saveCookie, saveCSRF := h.cookie, h.csrf
	h.cookie, h.csrf = "", ""
	resp, listBody := h.do(http.MethodGet, "/lists/ads.txt", nil)
	h.cookie, h.csrf = saveCookie, saveCSRF

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/lists/ads.txt = %d: %s", resp.StatusCode, listBody)
	}
	if !strings.Contains(string(listBody), "0.0.0.0 ads.example.com") {
		t.Errorf("danh sách thiếu domain đã chặn:\n%s", listBody)
	}

	// HEAD phải trả 200 như GET: RouterOS dò HEAD trước, gặp 405 là bỏ danh sách.
	headReq, _ := http.NewRequest(http.MethodHead, h.server.URL+"/lists/ads.txt", nil)
	headResp, err := h.server.Client().Do(headReq)
	if err != nil {
		t.Fatalf("HEAD /lists/ads.txt: %v", err)
	}
	headResp.Body.Close()
	if headResp.StatusCode != http.StatusOK {
		t.Errorf("HEAD /lists/ads.txt = %d, muốn 200", headResp.StatusCode)
	}

	// ETag và 304: router tải lại mỗi vài giờ, và tải lại nội dung không đổi là lãng phí.
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatal("phản hồi không có ETag")
	}
	req, _ := http.NewRequest(http.MethodGet, h.server.URL+"/lists/ads.txt", nil)
	req.Header.Set("If-None-Match", etag)
	cached, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatalf("request có điều kiện: %v", err)
	}
	defer cached.Body.Close()
	if cached.StatusCode != http.StatusNotModified {
		t.Errorf("If-None-Match = %d, muốn 304", cached.StatusCode)
	}

	// Nhật ký phải ghi lại ai quyết định và vì sao.
	resp, body = h.do(http.MethodGet, "/api/v1/domains/"+itoa(id), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("chi tiết domain = %d: %s", resp.StatusCode, body)
	}
	var detail struct {
		History []store.Decision `json:"history"`
	}
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatalf("giải mã chi tiết: %v", err)
	}
	if len(detail.History) != 1 {
		t.Fatalf("có %d dòng nhật ký, muốn 1", len(detail.History))
	}
	if detail.History[0].ActorLabel != "admin" {
		t.Errorf("actor = %q, muốn admin", detail.History[0].ActorLabel)
	}
	if detail.History[0].Reason != "tracker đã xác nhận" {
		t.Errorf("reason = %q", detail.History[0].Reason)
	}
}

// Domain được bảo vệ trả 403 kèm mã lỗi đúng.
func TestBlockingProtectedDomainReturns403(t *testing.T) {
	h := newHarness(t)
	h.login()
	id := h.seedDomain("api.vnpay.vn", store.StatusNew)

	resp, body := h.do(http.MethodPost, "/api/v1/domains/"+itoa(id)+"/decision",
		map[string]string{"action": "block", "category": "ads", "reason": "thử"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("= %d, muốn 403: %s", resp.StatusCode, body)
	}

	var out errorEnvelope
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("giải mã lỗi: %v", err)
	}
	if out.Error.Code != CodeDomainProtected {
		t.Errorf("code = %q, muốn %q", out.Error.Code, CodeDomainProtected)
	}
}

// Thêm hàng loạt ở chế độ xem trước không được ghi gì.
func TestDryRunAddDoesNotWrite(t *testing.T) {
	h := newHarness(t)
	h.login()

	resp, body := h.do(http.MethodPost, "/api/v1/domains", map[string]any{
		"names":    []string{"ads.example.com", "vnpay.vn", "không hợp lệ", "ads.example.com"},
		"category": "ads",
		"reason":   "từ diễn đàn",
		"dry_run":  true,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("= %d: %s", resp.StatusCode, body)
	}

	var out struct {
		Results []map[string]any `json:"results"`
		Summary map[string]int   `json:"summary"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("giải mã: %v", err)
	}
	if out.Summary["created"] != 1 {
		t.Errorf("created = %d, muốn 1", out.Summary["created"])
	}
	if out.Summary["rejected"] != 2 {
		t.Errorf("rejected = %d, muốn 2 (bảo vệ + không hợp lệ)", out.Summary["rejected"])
	}
	if out.Summary["duplicate"] != 1 {
		t.Errorf("duplicate = %d, muốn 1", out.Summary["duplicate"])
	}

	var n int
	h.store.Reader().QueryRow(`SELECT count(*) FROM domains`).Scan(&n)
	if n != 0 {
		t.Errorf("dry_run đã ghi %d domain vào CSDL", n)
	}
}

// Thao tác hàng loạt không phải all-or-nothing.
func TestBulkDecisionSkipsProtectedAndAppliesRest(t *testing.T) {
	h := newHarness(t)
	h.login()

	ok1 := h.seedDomain("ads1.example.com", store.StatusStaging)
	protected := h.seedDomain("push.apple.com", store.StatusStaging)
	ok2 := h.seedDomain("ads2.example.com", store.StatusStaging)

	resp, body := h.do(http.MethodPost, "/api/v1/domains/bulk-decision", map[string]any{
		"domain_ids": []int64{ok1, protected, ok2},
		"action":     "block",
		"category":   "ads",
		"reason":     "cụm quảng cáo",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("= %d: %s", resp.StatusCode, body)
	}

	var out struct {
		Applied int              `json:"applied"`
		Skipped []map[string]any `json:"skipped"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("giải mã: %v", err)
	}
	if out.Applied != 2 {
		t.Errorf("applied = %d, muốn 2", out.Applied)
	}
	if len(out.Skipped) != 1 {
		t.Fatalf("skipped = %d, muốn 1", len(out.Skipped))
	}
	if out.Skipped[0]["code"] != CodeDomainProtected {
		t.Errorf("mã bỏ qua = %v, muốn %q", out.Skipped[0]["code"], CodeDomainProtected)
	}
}

// Vai trò viewer không được thay đổi trạng thái và không thấy dữ liệu client.
func TestViewerRoleIsReadOnly(t *testing.T) {
	h := newHarness(t)

	hash, err := auth.HashPassword("mật-khẩu-xem")
	if err != nil {
		t.Fatalf("băm mật khẩu: %v", err)
	}
	if _, err := h.store.CreateUser(context.Background(), "nguoixem", hash, store.RoleViewer); err != nil {
		t.Fatalf("tạo tài khoản viewer: %v", err)
	}

	resp, body := h.do(http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "nguoixem", "password": "mật-khẩu-xem"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("đăng nhập viewer = %d: %s", resp.StatusCode, body)
	}
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie {
			h.cookie = c.Name + "=" + c.Value
		}
	}
	var login struct {
		CSRFToken string `json:"csrf_token"`
	}
	json.Unmarshal(body, &login)
	h.csrf = login.CSRFToken

	id := h.seedDomain("ads.example.com", store.StatusStaging)

	resp, _ = h.do(http.MethodPost, "/api/v1/domains/"+itoa(id)+"/decision",
		map[string]string{"action": "block", "category": "ads", "reason": "thử"})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("viewer chặn domain = %d, muốn 403", resp.StatusCode)
	}

	// Nhưng tra cứu thì được.
	resp, body = h.do(http.MethodGet, "/api/v1/domains/lookup?name=ads.example.com", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("viewer tra cứu = %d: %s", resp.StatusCode, body)
	}

	// Bảng xếp hạng theo client tiết lộ hành vi của người khác trong mạng.
	resp, _ = h.do(http.MethodGet, "/api/v1/stats/top?dimension=client", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("viewer xem top client = %d, muốn 403", resp.StatusCode)
	}
}

// Đăng nhập sai nhiều lần bị chặn theo IP.
func TestLoginRateLimit(t *testing.T) {
	h := newHarness(t)

	for i := range 5 {
		resp, _ := h.do(http.MethodPost, "/api/v1/auth/login",
			map[string]string{"username": "admin", "password": "sai"})
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("lần %d = %d, muốn 401", i, resp.StatusCode)
		}
	}

	resp, _ := h.do(http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "admin", "password": "sai"})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("lần thứ sáu = %d, muốn 429", resp.StatusCode)
	}
}

// Endpoint danh sách phải từ chối đường dẫn thoát ra ngoài thư mục lists.
func TestListEndpointRejectsPathTraversal(t *testing.T) {
	h := newHarness(t)

	for _, path := range []string{
		"/lists/..%2f..%2fetc%2fpasswd",
		"/lists/%2e%2e%2f%2e%2e%2fetc%2fpasswd.txt",
		"/lists/config.yaml",
	} {
		resp, _ := h.do(http.MethodGet, path, nil)
		if resp.StatusCode == http.StatusOK {
			t.Errorf("GET %s = 200, phải bị từ chối", path)
		}
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

// Mọi trường dạng danh sách phải là mảng, không bao giờ là null.
//
// Một null lọt ra làm hỏng phía client ở chỗ rất khó lần ra: giao diện chỉ sập khi
// gặp đúng một domain chưa có lịch sử, mà trên máy phát triển thì domain nào cũng có.
func TestListFieldsAreNeverNull(t *testing.T) {
	h := newHarness(t)
	h.login()
	id := h.seedDomain("brand-new.example.com", store.StatusNew)

	resp, body := h.do(http.MethodGet, "/api/v1/domains/"+itoa(id), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("= %d: %s", resp.StatusCode, body)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("giải mã: %v", err)
	}
	for _, field := range []string{"siblings", "history", "public_lists"} {
		value, ok := raw[field]
		if !ok {
			t.Errorf("thiếu trường %q", field)
			continue
		}
		if string(value) == "null" {
			t.Errorf("trường %q trả về null, phải là []", field)
		}
	}

	// Đồ thị quan hệ của một domain chưa có quan hệ nào cũng vậy.
	resp, body = h.do(http.MethodGet, "/api/v1/domains/"+itoa(id)+"/graph", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("graph = %d: %s", resp.StatusCode, body)
	}
	var graph map[string]json.RawMessage
	if err := json.Unmarshal(body, &graph); err != nil {
		t.Fatalf("giải mã graph: %v", err)
	}
	for _, field := range []string{"nodes", "edges"} {
		if string(graph[field]) == "null" {
			t.Errorf("trường graph.%q trả về null, phải là []", field)
		}
	}
}

// Header do client gửi lên không được phép quyết định IP nguồn.
//
// chi có middleware.RealIP ghi đè r.RemoteAddr bằng X-Forwarded-For / X-Real-IP.
// Dùng nó ở đây là mở hai lỗ cùng lúc: đổi header mỗi lần là lách được giới hạn đăng
// nhập sai, và khai mình là 192.168.x là vượt được danh sách dải IP cho phép tải
// /lists. Hệ thống này không nằm sau proxy nên không có lý do gì tin những header đó.
func TestForgedForwardedHeaderCannotBypassLoginRateLimit(t *testing.T) {
	h := newHarness(t)

	// Dùng hết hạn mức bằng năm lần sai từ cùng một IP thật.
	for range 5 {
		resp, _ := h.do(http.MethodPost, "/api/v1/auth/login",
			map[string]string{"username": "admin", "password": "sai"})
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("= %d, muốn 401", resp.StatusCode)
		}
	}

	// Giả mạo header để tỏ ra là một IP khác. Nếu máy chủ tin, hạn mức sẽ được cấp
	// lại và lần thử này trả 401 thay vì 429.
	for _, header := range []string{"X-Forwarded-For", "X-Real-IP", "True-Client-IP"} {
		req, err := http.NewRequest(http.MethodPost, h.server.URL+"/api/v1/auth/login",
			strings.NewReader(`{"username":"admin","password":"sai"}`))
		if err != nil {
			t.Fatalf("dựng request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(header, "203.0.113.99")

		resp, err := h.server.Client().Do(req)
		if err != nil {
			t.Fatalf("gửi request: %v", err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusTooManyRequests {
			t.Errorf("%s giả mạo → HTTP %d, muốn 429: header do client đặt đã cấp lại hạn mức",
				header, resp.StatusCode)
		}
	}
}

// Danh sách dải IP cho phép tải /lists cũng không được tin header client.
func TestForgedForwardedHeaderCannotBypassListsAllowlist(t *testing.T) {
	h := newHarness(t)
	// Chỉ cho phép một dải không chứa loopback của httptest.
	h.setListsAllowCIDR("10.99.0.0/16")

	req, err := http.NewRequest(http.MethodGet, h.server.URL+"/lists/ads.txt", nil)
	if err != nil {
		t.Fatalf("dựng request: %v", err)
	}
	req.Header.Set("X-Forwarded-For", "10.99.0.5")

	resp, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatalf("gửi request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("X-Forwarded-For giả mạo → HTTP %d, muốn 403", resp.StatusCode)
	}
}

// bodyContains cho biết phản hồi có chứa chuỗi nào đó không.
func bodyContains(payload []byte, needle string) bool {
	return strings.Contains(string(payload), needle)
}

func TestVTKeyNeverLeavesTheServer(t *testing.T) {
	// Bí mật đã lưu không được quay ra ngoài, kể cả với người quản trị đã đăng nhập:
	// không có màn hình nào cần khóa đầy đủ, nên trả nó ra chỉ tạo thêm chỗ rò rỉ
	// qua log truy cập, lịch sử trình duyệt hay ảnh chụp màn hình.
	h := newHarness(t)
	h.login()

	const secret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := h.store.SetSetting(context.Background(),
		store.SettingVTAPIKey, secret, "admin"); err != nil {
		t.Fatalf("lưu khóa: %v", err)
	}
	// Nạp vào registry đúng như lúc khởi động.
	vt, _ := enrich.Unwrap(mustGet(t, h.srv.enrichers, "vt")).(*enrich.VTEnricher)
	vt.SetAPIKey(secret)

	resp, body := h.do(http.MethodGet, "/api/v1/settings", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /settings = %d: %s", resp.StatusCode, body)
	}
	if bodyContains(body, secret) {
		t.Fatal("khóa API đầy đủ có trong phản hồi /settings")
	}
	if !bodyContains(body, `"vt_key_hint":"cdef"`) {
		t.Errorf("thiếu gợi ý bốn ký tự cuối: %s", body)
	}
	if !bodyContains(body, `"vt_configured":true`) {
		t.Errorf("không báo là đã cấu hình: %s", body)
	}
}

func TestVTKeyRejectsMalformedKeyBeforeCallingOut(t *testing.T) {
	// Dạng khóa kiểm tra trước khi ra mạng: dán thiếu vài ký tự là lỗi thường gặp
	// nhất, và bắt nó tại chỗ nhanh hơn một vòng đi về VirusTotal.
	h := newHarness(t)
	h.login()

	for _, bad := range []string{
		"quá-ngắn",
		"0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF", // hoa
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcde",  // thiếu 1
	} {
		resp, body := h.do(http.MethodPut, "/api/v1/settings/analysis",
			map[string]any{"vt_api_key": bad})
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("khóa %q = %d, muốn 422: %s", bad, resp.StatusCode, body)
		}
	}

	// Không có gì được lưu lại.
	var stored string
	ok, err := h.store.GetSetting(context.Background(), store.SettingVTAPIKey, &stored)
	if err != nil {
		t.Fatalf("đọc setting: %v", err)
	}
	if ok && stored != "" {
		t.Error("khóa sai dạng vẫn bị lưu")
	}
}

func TestVTKeyCanBeRemoved(t *testing.T) {
	h := newHarness(t)
	h.login()

	ctx := context.Background()
	if err := h.store.SetSetting(ctx, store.SettingVTAPIKey,
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "admin"); err != nil {
		t.Fatalf("lưu khóa: %v", err)
	}
	vt, _ := enrich.Unwrap(mustGet(t, h.srv.enrichers, "vt")).(*enrich.VTEnricher)
	vt.SetAPIKey("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")

	// Chuỗi rỗng là lệnh gỡ, và phải gỡ được mà không cần gọi ra ngoài — nếu không
	// thì lúc VirusTotal sập cũng không xóa được khóa hỏng.
	resp, body := h.do(http.MethodPut, "/api/v1/settings/analysis",
		map[string]any{"vt_api_key": ""})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gỡ khóa = %d: %s", resp.StatusCode, body)
	}
	if bodyContains(body, `"vt_configured":true`) {
		t.Errorf("vẫn báo đã cấu hình sau khi gỡ: %s", body)
	}
	if vt.Configured() {
		t.Error("nguồn vẫn giữ khóa sau khi gỡ")
	}
}

func TestVTKeyNeedsAdmin(t *testing.T) {
	h := newHarness(t)

	resp, _ := h.do(http.MethodPut, "/api/v1/settings/analysis",
		map[string]any{"vt_api_key": ""})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("chưa đăng nhập = %d, muốn 401", resp.StatusCode)
	}
}

func mustGet(t *testing.T, r *enrich.Registry, name string) enrich.Enricher {
	t.Helper()
	e, ok := r.Get(name)
	if !ok {
		t.Fatalf("không có nguồn %q", name)
	}
	return e
}

func TestPublishSinkRejectsNonIPAddresses(t *testing.T) {
	// Định dạng hosts chỉ nhận địa chỉ IP ở cột đầu. Một tên miền ở đó làm phần lớn
	// phần mềm đọc file bỏ qua cả dòng, và mạng mất chặn mà không có lỗi nào.
	h := newHarness(t)
	h.login()

	for _, bad := range []string{"vidu.vn", "999.1.1.1", "0.0.0.0/8", "", "  "} {
		resp, body := h.do(http.MethodPut, "/api/v1/settings/publish",
			map[string]any{"sink_address": bad})
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("địa chỉ %q = %d, muốn 422: %s", bad, resp.StatusCode, body)
		}
	}
}

func TestPublishSinkAcceptsIPv4AndIPv6(t *testing.T) {
	h := newHarness(t)
	h.login()

	cases := map[string]string{
		"0.0.0.0":   "0.0.0.0",
		"127.0.0.1": "127.0.0.1",
		"::":        "::",
		"::1":       "::1",
		// Chuẩn hóa: hai cách viết cùng một địa chỉ phải lưu về cùng một dạng, nếu
		// không mỗi lần lưu lại sinh ra một checksum khác dù nội dung không đổi.
		"0:0:0:0:0:0:0:1": "::1",
	}

	for input, want := range cases {
		resp, body := h.do(http.MethodPut, "/api/v1/settings/publish",
			map[string]any{"sink_address": input})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("địa chỉ %q = %d: %s", input, resp.StatusCode, body)
		}
		if !bodyContains(body, `"sink_address":"`+want+`"`) {
			t.Errorf("địa chỉ %q lưu thành %s, muốn %q", input, body, want)
		}
	}
}

func TestPublishSinkAppearsInSettingsAndAffectsOutput(t *testing.T) {
	h := newHarness(t)
	h.login()

	resp, body := h.do(http.MethodPut, "/api/v1/settings/publish",
		map[string]any{"sink_address": "127.0.0.1"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("đổi địa chỉ = %d: %s", resp.StatusCode, body)
	}

	resp, body = h.do(http.MethodGet, "/api/v1/settings", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /settings = %d: %s", resp.StatusCode, body)
	}
	if !bodyContains(body, `"sink_address":"127.0.0.1"`) {
		t.Errorf("địa chỉ không có trong /settings: %s", body)
	}
}

func TestPublishSinkNeedsAdmin(t *testing.T) {
	h := newHarness(t)

	resp, _ := h.do(http.MethodPut, "/api/v1/settings/publish",
		map[string]any{"sink_address": "127.0.0.1"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("chưa đăng nhập = %d, muốn 401", resp.StatusCode)
	}
}

func TestRulesRejectDangerousEntriesWithReasons(t *testing.T) {
	h := newHarness(t)
	h.login()

	resp, body := h.do(http.MethodPut, "/api/v1/scoring/rules", map[string]any{
		"adtech_asns": map[string]any{
			"15169": map[string]any{"org": "Google", "category": "ads"},
		},
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("ASN trung tính = %d, muốn 422: %s", resp.StatusCode, body)
	}
	// Lý do phải nêu tên tổ chức, không chỉ "không hợp lệ": người dùng cần biết vì
	// sao mục của mình bị từ chối để sửa cho đúng.
	if !bodyContains(body, "Google") {
		t.Errorf("lý do không nêu tên tổ chức: %s", body)
	}

	// Không có gì được lưu lại.
	var stored classify.Custom
	found, err := h.store.GetSetting(context.Background(), store.SettingRules, &stored)
	if err != nil {
		t.Fatalf("đọc setting: %v", err)
	}
	if found && !stored.IsZero() {
		t.Error("luật bị từ chối vẫn được lưu")
	}
}

func TestRulesDryRunDoesNotPersist(t *testing.T) {
	// Xem trước phải hoàn toàn không có tác dụng phụ: nó là lớp bảo vệ trước khi lưu,
	// và một lớp bảo vệ tự nó ghi dữ liệu thì không còn là lớp bảo vệ.
	h := newHarness(t)
	h.login()

	resp, body := h.do(http.MethodPut, "/api/v1/scoring/rules", map[string]any{
		"adtech_domains": map[string]string{"quangcaoabc.vn": "ads"},
		"dry_run":        true,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dry run = %d: %s", resp.StatusCode, body)
	}
	if !bodyContains(body, `"would_block"`) {
		t.Errorf("thiếu bảng tác động: %s", body)
	}

	var stored classify.Custom
	found, err := h.store.GetSetting(context.Background(), store.SettingRules, &stored)
	if err != nil {
		t.Fatalf("đọc setting: %v", err)
	}
	if found && !stored.IsZero() {
		t.Error("dry run đã ghi luật xuống CSDL")
	}
}

func TestRulesSaveAndReadBack(t *testing.T) {
	h := newHarness(t)
	h.login()

	resp, body := h.do(http.MethodPut, "/api/v1/scoring/rules", map[string]any{
		"adtech_domains": map[string]string{"QuangCaoABC.VN": "ads"},
		"keywords":       map[string][]string{"ads": {"quangcao"}},
		"thresholds":     map[string]int{"spread_high_min": 40},
	})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("lưu luật = %d: %s", resp.StatusCode, body)
	}
	// Lưu xong phải chấm điểm lại: luật mới không có tác dụng cho tới khi domain
	// được chấm lại, và để giao diện nói một đằng dữ liệu một nẻo là lỗi tệ hơn.
	if !bodyContains(body, `"job_id"`) {
		t.Errorf("không xếp hàng job chấm lại: %s", body)
	}

	resp, body = h.do(http.MethodGet, "/api/v1/scoring/rules", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("đọc lại = %d: %s", resp.StatusCode, body)
	}
	if !bodyContains(body, "quangcao") {
		t.Errorf("không đọc lại được luật đã lưu: %s", body)
	}
	if !bodyContains(body, `"builtin_counts"`) {
		t.Errorf("thiếu số lượng dựng sẵn để đối chiếu: %s", body)
	}
}

func TestRulesActuallyChangeScoring(t *testing.T) {
	// Vòng khép kín: lưu luật rồi chấm lại thì điểm của domain phải đổi theo. Không
	// có bước này thì mọi thứ trên có thể xanh trong khi tính năng không làm gì cả.
	h := newHarness(t)
	h.login()

	ctx := context.Background()
	id := seedDomainWithCNAME(t, h, "tracker.trangweb.vn", "edge.quangcaonoidia.vn")

	// Chấm điểm một lượt TRƯỚC khi thêm luật. Không có bước này thì mốc so sánh là 0
	// của một domain chưa từng được chấm, và mọi lần chấm lại đều làm điểm tăng — test
	// xanh kể cả khi luật hoàn toàn bị bỏ qua.
	if _, err := h.srv.worker.Enqueue(ctx, worker.JobRescore, nil); err != nil {
		t.Fatalf("xếp hàng chấm điểm nền: %v", err)
	}
	if err := h.srv.worker.RunPending(ctx); err != nil {
		t.Fatalf("chạy job nền: %v", err)
	}
	before := scoreOf(t, h, id)

	resp, body := h.do(http.MethodPut, "/api/v1/scoring/rules", map[string]any{
		"adtech_domains": map[string]string{"quangcaonoidia.vn": "ads"},
	})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("lưu luật = %d: %s", resp.StatusCode, body)
	}

	// Chạy job chấm lại đồng bộ thay vì đợi bộ lập lịch nền.
	if err := h.srv.worker.RunPending(ctx); err != nil {
		t.Fatalf("chạy job: %v", err)
	}

	after := scoreOf(t, h, id)
	if after <= before {
		t.Errorf("điểm sau = %v, trước = %v — luật tự đặt không đổi được kết quả chấm điểm",
			after, before)
	}
}

// seedDomainWithCNAME tạo một domain kèm chuỗi CNAME đã làm giàu.
func seedDomainWithCNAME(t *testing.T, h *harness, name, cname string) int64 {
	t.Helper()
	ctx := context.Background()
	now := store.Now()

	res, err := h.store.Writer().ExecContext(ctx, `
		INSERT INTO domains (name, name_rev, etld1, status, origin,
		                     first_seen, last_seen, created_at, updated_at)
		VALUES (?, ?, ?, 'new', 'discovered', ?, ?, ?, ?)`,
		name, name, "trangweb.vn", now, now, now, now)
	if err != nil {
		t.Fatalf("tạo domain: %v", err)
	}
	id, _ := res.LastInsertId()

	if _, err := h.store.Writer().ExecContext(ctx, `
		INSERT INTO domain_facts (domain_id, source, data, fetched_at, expires_at)
		VALUES (?, 'dns', ?, ?, ?)`,
		id, `{"cname_chain":["`+cname+`"]}`, now, store.TimeAt(time.Now().Add(24*time.Hour))); err != nil {
		t.Fatalf("ghi facts: %v", err)
	}
	return id
}

func scoreOf(t *testing.T, h *harness, id int64) float64 {
	t.Helper()
	var score float64
	if err := h.store.Reader().QueryRow(
		`SELECT coalesce(score, 0) FROM domains WHERE id = ?`, id).Scan(&score); err != nil {
		t.Fatalf("đọc điểm: %v", err)
	}
	return score
}

func TestCategoryCountsSeparateLabelledFromPublished(t *testing.T) {
	// Cột số domain nằm ngay cạnh cột đường dẫn file, nên người vận hành đọc nó thành
	// "số dòng trong file". Nếu nó đếm cả domain chưa chặn thì file luôn ít hơn con số
	// hiển thị, và điều đó trông y hệt việc hệ thống đánh mất domain.
	h := newHarness(t)
	h.login()
	ctx := context.Background()

	var adsID int64
	if err := h.store.Reader().QueryRow(
		`SELECT id FROM categories WHERE key = 'ads'`).Scan(&adsID); err != nil {
		t.Fatalf("đọc phân loại ads: %v", err)
	}

	// Bảy domain đã chặn, bốn domain mang nhãn nhưng ở trạng thái khác.
	seedCategorised(t, h, adsID, "blocked", 7)
	seedCategorised(t, h, adsID, "staging", 2)
	seedCategorised(t, h, adsID, "new", 1)
	seedCategorised(t, h, adsID, "allowed", 1)

	ads := categoryByKey(t, h, "ads")
	if got := ads["domain_count"]; got != float64(11) {
		t.Errorf("domain_count = %v, muốn 11 (mọi domain mang nhãn)", got)
	}
	if got := ads["blocked_count"]; got != float64(7) {
		t.Errorf("blocked_count = %v, muốn 7", got)
	}
	if got := ads["published_count"]; got != float64(7) {
		t.Errorf("published_count = %v, muốn 7", got)
	}

	// Và con số đó phải khớp đúng số dòng file xuất bản ra.
	publisher := publish.New(h.store, h.listsDir, 0, "",
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := publisher.PublishAll(ctx, []string{"ads"}, "test"); err != nil {
		t.Fatalf("xuất bản: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(h.listsDir, "ads.txt"))
	if err != nil {
		t.Fatalf("đọc ads.txt: %v", err)
	}
	lines := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			lines++
		}
	}
	if lines != 7 {
		t.Errorf("file có %d dòng, muốn 7 — phải khớp published_count", lines)
	}
}

func TestPublishedCountIsZeroWhenCategoryDisabled(t *testing.T) {
	// Tắt xuất bản thì file rỗng bất kể có bao nhiêu domain đang chặn, vì truy vấn
	// xuất bản có điều kiện enabled = 1. Con số hiển thị phải nói đúng điều đó.
	h := newHarness(t)
	h.login()

	var cdnID int64
	if err := h.store.Reader().QueryRow(
		`SELECT id FROM categories WHERE key = 'cdn'`).Scan(&cdnID); err != nil {
		t.Fatalf("đọc phân loại cdn: %v", err)
	}
	seedCategorised(t, h, cdnID, "blocked", 5)

	cdn := categoryByKey(t, h, "cdn")
	if cdn["enabled"] != false {
		t.Skip("phân loại cdn mặc định đang bật, test này giả định nó tắt")
	}
	if got := cdn["blocked_count"]; got != float64(5) {
		t.Errorf("blocked_count = %v, muốn 5", got)
	}
	if got := cdn["published_count"]; got != float64(0) {
		t.Errorf("published_count = %v, muốn 0 khi tắt xuất bản", got)
	}
}

// seedCategorised tạo n domain mang một nhãn và một trạng thái.
func seedCategorised(t *testing.T, h *harness, categoryID int64, status string, n int) {
	t.Helper()
	now := store.Now()
	for i := range n {
		name := fmt.Sprintf("%s%d-%d.vidu.vn", status, categoryID, i)
		if _, err := h.store.Writer().Exec(`
			INSERT INTO domains (name, name_rev, etld1, status, origin, category_id,
			                     first_seen, last_seen, created_at, updated_at)
			VALUES (?, ?, 'vidu.vn', ?, 'discovered', ?, ?, ?, ?, ?)`,
			name, name, status, categoryID, now, now, now, now); err != nil {
			t.Fatalf("tạo domain %q: %v", name, err)
		}
	}
}

// categoryByKey đọc một phân loại từ API dưới dạng map.
func categoryByKey(t *testing.T, h *harness, key string) map[string]any {
	t.Helper()
	resp, body := h.do(http.MethodGet, "/api/v1/categories", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /categories = %d: %s", resp.StatusCode, body)
	}
	var out struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("giải mã: %v", err)
	}
	for _, item := range out.Items {
		if item["key"] == key {
			return item
		}
	}
	t.Fatalf("không tìm thấy phân loại %q", key)
	return nil
}

func TestPublishedFilesTellsWhereABlockedDomainLands(t *testing.T) {
	// Câu hỏi "chặn rồi thì nó nằm ở danh sách nào" trước đây không có chỗ nào trả
	// lời, và câu trả lời có thể là "không ở đâu cả" — domain thuộc phân loại đã tắt
	// xuất bản thì không lọt vào cả ads.txt lẫn all.txt.
	h := newHarness(t)
	h.login()
	ctx := context.Background()

	var adsID, contentID int64
	if err := h.store.Reader().QueryRow(
		`SELECT id FROM categories WHERE key = 'ads'`).Scan(&adsID); err != nil {
		t.Fatalf("đọc ads: %v", err)
	}
	if err := h.store.Reader().QueryRow(
		`SELECT id FROM categories WHERE key = 'content'`).Scan(&contentID); err != nil {
		t.Fatalf("đọc content: %v", err)
	}
	if _, err := h.store.Writer().ExecContext(ctx,
		`UPDATE categories SET enabled = 0 WHERE key = 'content'`); err != nil {
		t.Fatalf("tắt content: %v", err)
	}

	seedCategorised(t, h, adsID, "blocked", 1)
	seedCategorised(t, h, contentID, "blocked", 1)

	// Phân loại đang bật: nằm trong file riêng, file gộp, và danh sách tổng.
	files := publishedFilesOf(t, h, fmt.Sprintf("blocked%d-0.vidu.vn", adsID))
	for _, want := range []string{"blocked.txt", "ads.txt", "all.txt"} {
		if !slices.Contains(files, want) {
			t.Errorf("domain ads thiếu %q, có %v", want, files)
		}
	}

	// Phân loại đã tắt: chỉ còn lưới an toàn.
	files = publishedFilesOf(t, h, fmt.Sprintf("blocked%d-0.vidu.vn", contentID))
	if !slices.Equal(files, []string{"blocked.txt"}) {
		t.Errorf("domain thuộc phân loại đã tắt = %v, muốn chỉ [blocked.txt]", files)
	}
}

func TestPublishedFilesIsEmptyForDomainsNotBlocked(t *testing.T) {
	h := newHarness(t)
	h.login()

	var adsID int64
	if err := h.store.Reader().QueryRow(
		`SELECT id FROM categories WHERE key = 'ads'`).Scan(&adsID); err != nil {
		t.Fatalf("đọc ads: %v", err)
	}
	seedCategorised(t, h, adsID, "staging", 1)

	if files := publishedFilesOf(t, h, fmt.Sprintf("staging%d-0.vidu.vn", adsID)); len(files) != 0 {
		t.Errorf("domain chờ duyệt = %v, muốn rỗng — nó chưa được xuất bản đi đâu", files)
	}
}

// publishedFilesOf đọc trường published_files của một domain qua API.
func publishedFilesOf(t *testing.T, h *harness, name string) []string {
	t.Helper()

	resp, body := h.do(http.MethodGet, "/api/v1/domains?q="+name, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tìm %q = %d: %s", name, resp.StatusCode, body)
	}
	var page struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("giải mã danh sách: %v", err)
	}
	if len(page.Items) == 0 {
		t.Fatalf("không tìm thấy domain %q", name)
	}

	resp, body = h.do(http.MethodGet, fmt.Sprintf("/api/v1/domains/%d", page.Items[0].ID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("chi tiết = %d: %s", resp.StatusCode, body)
	}
	var detail struct {
		PublishedFiles []string `json:"published_files"`
	}
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatalf("giải mã chi tiết: %v", err)
	}
	return detail.PublishedFiles
}
