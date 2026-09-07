package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/benji/dnsguard/internal/auth"
	"github.com/benji/dnsguard/internal/catalog"
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
	listsDir string

	cookie string
	csrf   string
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
	publisher := publish.New(db, listsDir, cfg.PublishMinRatio, log)
	runner := worker.New(db, cfg, enrich.NewRegistry(log), publisher,
		catalog.New(db, log), graph.New(db, log), bus, log)

	srv := New(Options{
		Store: db, Auth: auth.NewService(db, cfg.SessionTTL), Config: cfg,
		Publisher: publisher, Worker: runner, Bus: bus, Log: log,
	})

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	h := &harness{t: t, server: ts, store: db, listsDir: listsDir}

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
	publisher := publish.New(h.store, h.listsDir, 0.5,
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
