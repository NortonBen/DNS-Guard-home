// Package api là lớp HTTP. Mỏng theo chủ ý: chỉ xác thực, kiểm tra đầu vào, gọi
// service và dựng phản hồi. Không có logic nghiệp vụ ở đây.
package api

import (
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/benji/dnsguard/internal/ai"
	"github.com/benji/dnsguard/internal/auth"
	"github.com/benji/dnsguard/internal/config"
	"github.com/benji/dnsguard/internal/enrich"
	"github.com/benji/dnsguard/internal/events"
	"github.com/benji/dnsguard/internal/ingest"
	"github.com/benji/dnsguard/internal/llm"
	"github.com/benji/dnsguard/internal/pcap"
	"github.com/benji/dnsguard/internal/publish"
	"github.com/benji/dnsguard/internal/store"
	"github.com/benji/dnsguard/internal/threat"
	"github.com/benji/dnsguard/internal/worker"
)

// Server gom các phụ thuộc của tầng HTTP.
type Server struct {
	store     *store.Store
	auth      *auth.Service
	cfg       config.Config
	publisher *publish.Publisher
	worker    *worker.Runner
	bus       *events.Broker
	listener  *ingest.Listener
	enrichers *enrich.Registry
	threats   *threat.Set
	pcap      *pcap.Recorder
	webFS     fs.FS
	version   string
	log       *slog.Logger

	// Cụm AI là tuỳ chọn: cả ba con trỏ này rỗng khi máy chủ chạy không có model
	// nào, và các endpoint /ai trả 503 thay vì hỏng.
	aiStore    *ai.Store
	llm        *llm.Client
	classifier *ai.Classifier

	logins *loginLimiter
}

// Options là các phụ thuộc dựng Server.
type Options struct {
	Store     *store.Store
	Auth      *auth.Service
	Config    config.Config
	Publisher *publish.Publisher
	Worker    *worker.Runner
	Bus       *events.Broker
	Listener  *ingest.Listener
	Enrichers *enrich.Registry
	Threats   *threat.Set
	Pcap      *pcap.Recorder
	WebFS     fs.FS
	Version   string
	Log       *slog.Logger

	AIStore    *ai.Store
	LLM        *llm.Client
	Classifier *ai.Classifier
}

// New dựng Server.
func New(o Options) *Server {
	return &Server{
		store: o.Store, auth: o.Auth, cfg: o.Config, publisher: o.Publisher,
		worker: o.Worker, bus: o.Bus, listener: o.Listener, enrichers: o.Enrichers,
		threats: o.Threats, pcap: o.Pcap,
		webFS: o.WebFS, version: o.Version, log: o.Log,
		aiStore: o.AIStore, llm: o.LLM, classifier: o.Classifier,
		logins: newLoginLimiter(5, 15*time.Minute),
	}
}

// Handler dựng router.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	// Cố ý KHÔNG dùng middleware.RealIP: nó ghi đè r.RemoteAddr bằng giá trị lấy từ
	// X-Forwarded-For / X-Real-IP, mà những header đó do chính client gửi lên. Hệ
	// thống này chạy trong mạng nội bộ và không nằm sau proxy, nên tin chúng nghĩa là
	// bất kỳ ai cũng lách được giới hạn đăng nhập sai (chỉ cần đổi header mỗi lần) và
	// vượt được danh sách dải IP cho phép tải /lists (chỉ cần khai mình là 192.168.x).
	r.Use(recoverPanic(s.log))
	r.Use(requestLogger(s.log))
	r.Use(middleware.Compress(5, "application/json", "text/plain", "text/html", "text/css", "application/javascript"))

	// Không cần xác thực: giám sát và endpoint danh sách cho router.
	r.Get("/health", s.handleHealth)
	r.Get("/metrics", s.handleMetrics)
	r.Get("/lists/{file}", s.handleList)
	// RouterOS dò HEAD trước khi tải adlist về, và bỏ luôn danh sách khi request
	// đầu tiên không phải 2xx. Chi trả 405 cho method chưa đăng ký, nên thiếu dòng
	// này router nạp về đúng 0 tên mà không báo gì ngoài log dns.
	r.Head("/lists/{file}", s.handleList)

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/auth/login", s.handleLogin)

		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Use(requireCSRF)

			r.Get("/auth/me", s.handleMe)
			r.Post("/auth/logout", s.handleLogout)

			r.Get("/events", s.handleEvents)

			// Cả hai vai trò đều đọc được.
			r.Get("/domains", s.handleListDomains)
			r.Get("/domains/lookup", s.handleLookup)
			r.Get("/domains/{id}", s.handleGetDomain)
			r.Get("/domains/{id}/graph", s.handleDomainGraph)
			r.Get("/graph", s.handleNetworkGraph)
			r.Get("/domains/{id}/timeline", s.handleDomainTimeline)
			r.Get("/investigate/ip", s.handleInvestigateIP)
			r.Get("/threats", s.handleListThreats)
			r.Get("/forensics/export", s.handleForensicsExport)
			r.Get("/categories", s.handleListCategories)
			r.Get("/sources", s.handleListSources)
			r.Get("/sources/overlap", s.handleSourceOverlap)
			r.Get("/snapshots", s.handleListSnapshots)
			r.Get("/snapshots/{a}/diff/{b}", s.handleDiffSnapshots)
			r.Get("/scoring/weights", s.handleGetWeights)
			r.Get("/scoring/rules", s.handleGetRules)
			r.Get("/stats/overview", s.handleStatsOverview)
			r.Get("/stats/top", s.handleStatsTop)
			r.Get("/stats/resources", s.handleStatsResources)
			r.Get("/jobs/{id}", s.handleGetJob)
			r.Post("/unblock-requests", s.handleCreateUnblockRequest)

			// Hỏi đáp mở cho cả hai vai trò; bộ công cụ mới là thứ phân quyền.
			// Vai trò không phải quản trị chỉ nhận được công cụ đọc — xem aiHost.
			r.Get("/ai/status", s.handleAIStatus)
			r.Get("/ai/tools", s.handleAITools)
			r.Post("/ai/ask", s.handleAIAsk)
			r.Get("/ai/chats", s.handleAIChats)
			r.Get("/ai/chats/{id}", s.handleAIChat)
			r.Get("/ai/history", s.handleAIHistory)
			r.Get("/ai/history/{id}", s.handleAIRequest)
			r.Get("/ai/verdicts", s.handleAIVerdicts)

			// Chỉ quản trị mới thay đổi được trạng thái.
			r.Group(func(r chi.Router) {
				r.Use(requireAdmin)

				r.Post("/domains", s.handleAddDomains)
				r.Post("/domains/{id}/decision", s.handleDecision)
				r.Post("/domains/bulk-decision", s.handleBulkDecision)
				r.Patch("/categories/{id}", s.handleUpdateCategory)
				r.Put("/scoring/weights", s.handleUpdateWeights)
				r.Put("/scoring/rules", s.handleUpdateRules)
				r.Post("/scoring/rescore", s.handleRescore)
				r.Post("/sources", s.handleCreateSource)
				r.Patch("/sources/{id}", s.handleUpdateSource)
				r.Delete("/sources/{id}", s.handleDeleteSource)
				r.Post("/sources/{id}/sync", s.handleSyncSource)
				r.Post("/publish", s.handlePublish)
				r.Post("/snapshots/{id}/rollback", s.handleRollback)
				r.Get("/unblock-requests", s.handleListUnblockRequests)
				r.Post("/unblock-requests/{id}/resolve", s.handleResolveUnblockRequest)
				r.Get("/settings", s.handleGetSettings)
				r.Get("/settings/protect", s.handleGetProtect)
				r.Put("/settings/protect", s.handleUpdateProtect)
				r.Put("/settings/lifecycle", s.handleUpdateLifecycle)
				r.Put("/settings/analysis", s.handleUpdateAnalysis)
				r.Put("/settings/publish", s.handleUpdatePublish)
				r.Post("/settings/lookup/{kind}/refresh", s.handleRefreshLookup)

				r.Put("/ai/settings", s.handleUpdateAISettings)
				r.Post("/ai/classify", s.handleAIClassify)
				r.Post("/domains/{id}/ai-recheck", s.handleAIRecheck)
				r.Delete("/ai/chats/{id}", s.handleDeleteAIChat)
				r.Get("/ai/skills", s.handleListAISkills)
				r.Put("/ai/skills", s.handleSaveAISkill)
				r.Delete("/ai/skills/{id}", s.handleDeleteAISkill)
				r.Get("/ai/mcp-servers", s.handleListMCPServers)
				r.Put("/ai/mcp-servers", s.handleSaveMCPServer)
				r.Delete("/ai/mcp-servers/{id}", s.handleDeleteMCPServer)
			})
		})
	})

	// Giao diện: mọi đường dẫn còn lại trả về ứng dụng React.
	if s.webFS != nil {
		r.NotFound(s.handleWeb)
	}
	return r
}
