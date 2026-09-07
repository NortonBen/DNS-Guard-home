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

	"github.com/benji/dnsguard/internal/auth"
	"github.com/benji/dnsguard/internal/config"
	"github.com/benji/dnsguard/internal/enrich"
	"github.com/benji/dnsguard/internal/events"
	"github.com/benji/dnsguard/internal/ingest"
	"github.com/benji/dnsguard/internal/publish"
	"github.com/benji/dnsguard/internal/store"
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
	webFS     fs.FS
	version   string
	log       *slog.Logger

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
	WebFS     fs.FS
	Version   string
	Log       *slog.Logger
}

// New dựng Server.
func New(o Options) *Server {
	return &Server{
		store: o.Store, auth: o.Auth, cfg: o.Config, publisher: o.Publisher,
		worker: o.Worker, bus: o.Bus, listener: o.Listener, enrichers: o.Enrichers,
		webFS: o.WebFS, version: o.Version, log: o.Log,
		logins: newLoginLimiter(5, 15*time.Minute),
	}
}

// Handler dựng router.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(recoverPanic(s.log))
	r.Use(requestLogger(s.log))
	r.Use(middleware.Compress(5, "application/json", "text/plain", "text/html", "text/css", "application/javascript"))

	// Không cần xác thực: giám sát và endpoint danh sách cho router.
	r.Get("/health", s.handleHealth)
	r.Get("/metrics", s.handleMetrics)
	r.Get("/lists/{file}", s.handleList)

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
			r.Get("/domains/{id}/timeline", s.handleDomainTimeline)
			r.Get("/categories", s.handleListCategories)
			r.Get("/sources", s.handleListSources)
			r.Get("/sources/overlap", s.handleSourceOverlap)
			r.Get("/snapshots", s.handleListSnapshots)
			r.Get("/snapshots/{a}/diff/{b}", s.handleDiffSnapshots)
			r.Get("/scoring/weights", s.handleGetWeights)
			r.Get("/stats/overview", s.handleStatsOverview)
			r.Get("/stats/top", s.handleStatsTop)
			r.Get("/jobs/{id}", s.handleGetJob)
			r.Post("/unblock-requests", s.handleCreateUnblockRequest)

			// Chỉ quản trị mới thay đổi được trạng thái.
			r.Group(func(r chi.Router) {
				r.Use(requireAdmin)

				r.Post("/domains", s.handleAddDomains)
				r.Post("/domains/{id}/decision", s.handleDecision)
				r.Post("/domains/bulk-decision", s.handleBulkDecision)
				r.Patch("/categories/{id}", s.handleUpdateCategory)
				r.Put("/scoring/weights", s.handleUpdateWeights)
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
				r.Post("/settings/lookup/{kind}/refresh", s.handleRefreshLookup)
			})
		})
	})

	// Giao diện: mọi đường dẫn còn lại trả về ứng dụng React.
	if s.webFS != nil {
		r.NotFound(s.handleWeb)
	}
	return r
}
