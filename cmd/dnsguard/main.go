// Command dnsguard chạy API, bộ nhận TZSP và các job nền trong một tiến trình.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/benji/dnsguard/internal/api"
	"github.com/benji/dnsguard/internal/auth"
	"github.com/benji/dnsguard/internal/catalog"
	"github.com/benji/dnsguard/internal/config"
	"github.com/benji/dnsguard/internal/enrich"
	"github.com/benji/dnsguard/internal/events"
	"github.com/benji/dnsguard/internal/graph"
	"github.com/benji/dnsguard/internal/ingest"
	"github.com/benji/dnsguard/internal/publish"
	"github.com/benji/dnsguard/internal/store"
	"github.com/benji/dnsguard/internal/web"
	"github.com/benji/dnsguard/internal/worker"
)

// version được đặt lúc biên dịch bằng -ldflags.
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "dnsguard:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := newLogger(cfg.LogLevel)
	log.Info("khởi động DNSGuard", "version", version, "db", cfg.DBPath)

	db, err := store.Open(cfg.DBPath, cfg.AutoMigrate)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := ensureFirstAdmin(context.Background(), db, log); err != nil {
		return err
	}

	// Bắt tín hiệu dừng trước khi mở cổng: Ctrl+C lúc đang khởi động vẫn phải thoát.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	bus := events.NewBroker(log)
	publisher := publish.New(db, cfg.ListsDir, cfg.PublishMinRatio, log)
	syncer := catalog.New(db, log)
	builder := graph.New(db, log)
	enrichers := buildEnrichers(cfg, log)

	runner := worker.New(db, cfg, enrichers, publisher, syncer, builder, bus, log)
	listener := ingest.NewListener(ingest.Options{Addr: cfg.TZSPListen}, db, log)

	server := api.New(api.Options{
		Store: db, Auth: auth.NewService(db, cfg.SessionTTL), Config: cfg,
		Publisher: publisher, Worker: runner, Bus: bus, Listener: listener,
		Enrichers: enrichers, WebFS: web.FS(), Version: version, Log: log,
	})

	httpServer := &http.Server{
		Addr:    cfg.Listen,
		Handler: server.Handler(),
		// Không đặt WriteTimeout: luồng SSE là kết nối dài, và một timeout ghi sẽ
		// cắt nó giữa chừng. Thời gian đọc header vẫn giới hạn để chống kết nối treo.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	var wg sync.WaitGroup

	wg.Go(func() {
		log.Info("phục vụ HTTP", "addr", cfg.Listen)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("máy chủ HTTP dừng", "err", err)
			stop()
		}
	})

	wg.Go(func() {
		if err := listener.Run(ctx); err != nil {
			log.Error("bộ nhận TZSP dừng", "err", err)
		}
	})

	wg.Go(func() {
		runner.Run(ctx)
	})

	<-ctx.Done()
	log.Info("đang dừng")

	// Cho các kết nối đang xử lý thời gian kết thúc, nhưng không chờ mãi: lô sự kiện
	// cuối của ingest cần vài giây để ghi xong.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Warn("dừng HTTP không sạch", "err", err)
	}

	wg.Wait()
	if err := db.Checkpoint(); err != nil {
		log.Warn("gộp WAL thất bại", "err", err)
	}
	log.Info("đã dừng")
	return nil
}

// buildEnrichers dựng registry các nguồn làm giàu.
//
// Giới hạn tốc độ đặt riêng từng nguồn vì chúng khác nhau rất xa: phân giải DNS chịu
// được hàng chục truy vấn mỗi giây, còn crt.sh sẽ chặn nếu vượt vài truy vấn mỗi phút.
func buildEnrichers(cfg config.Config, log *slog.Logger) *enrich.Registry {
	registry := enrich.NewRegistry(log)
	resolver := enrich.NewDNS(nil)

	// DNS chạy kể cả khi đã tắt truy vấn ra ngoài: phân giải tên là việc mà mọi thiết
	// bị trong mạng vẫn làm, không phải chia sẻ dữ liệu với bên thứ ba.
	registry.Register(resolver, 20, 40, true)

	asn := enrich.NewASN(func(ctx context.Context, domain string) (netip.Addr, error) {
		data, err := resolver.Enrich(ctx, domain)
		if err != nil {
			return netip.Addr{}, err
		}
		facts, ok := data.(enrich.DNSFacts)
		if !ok || len(facts.A) == 0 {
			return netip.Addr{}, fmt.Errorf("không phân giải được địa chỉ cho %q", domain)
		}
		return netip.ParseAddr(facts.A[0])
	})
	if err := asn.LoadTable(cfg.IP2ASNPath); err != nil {
		// Thiếu bảng ASN làm mất một tín hiệu, không làm hỏng hệ thống: các tín hiệu
		// còn lại vẫn chạy và enricher tự báo là chưa sẵn sàng.
		log.Warn("không nạp được bảng ip2asn, tín hiệu ASN sẽ không hoạt động",
			"path", cfg.IP2ASNPath, "err", err)
	} else {
		log.Info("đã nạp bảng ip2asn", "path", cfg.IP2ASNPath)
	}
	registry.Register(asn, 20, 40, true)

	// Hai nguồn dưới gửi tên miền ra Internet, nên tắt được bằng một biến duy nhất.
	registry.Register(enrich.NewCert(), 0.2, 2, cfg.ExternalEnabled)
	registry.Register(enrich.NewRDAP(), 1, 5, cfg.ExternalEnabled)

	rank := enrich.NewRank()
	if err := rank.LoadTable(cfg.TrancoPath); err != nil {
		// Thiếu Tranco là mất tín hiệu bảo vệ mạnh nhất (−8,0). Cảnh báo rõ để người
		// vận hành biết mà tải về từ màn Cài đặt.
		log.Warn("không nạp được danh sách Tranco, tín hiệu bảo vệ high_rank sẽ không hoạt động",
			"path", cfg.TrancoPath, "err", err)
	} else {
		log.Info("đã nạp danh sách Tranco", "path", cfg.TrancoPath)
	}
	registry.Register(rank, 100, 100, true)

	return registry
}

// ensureFirstAdmin tạo tài khoản quản trị đầu tiên khi CSDL còn trống.
func ensureFirstAdmin(ctx context.Context, db *store.Store, log *slog.Logger) error {
	n, err := db.CountUsers(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}

	password := os.Getenv("DNSGUARD_ADMIN_PASSWORD")
	generated := false
	if password == "" {
		if password, err = randomPassword(); err != nil {
			return err
		}
		generated = true
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	if _, err := db.CreateUser(ctx, "admin", hash, store.RoleAdmin); err != nil {
		return err
	}

	if generated {
		// In một lần duy nhất, lúc khởi tạo. Mật khẩu không bao giờ được lưu ở dạng
		// đọc được, nên đây là cơ hội duy nhất để lấy nó.
		log.Warn("đã tạo tài khoản quản trị đầu tiên",
			"username", "admin", "password", password,
			"ghi_chu", "lưu lại ngay, mật khẩu này không hiện lại lần nữa")
	} else {
		log.Info("đã tạo tài khoản quản trị đầu tiên từ DNSGUARD_ADMIN_PASSWORD",
			"username", "admin")
	}
	return nil
}

// randomPassword sinh mật khẩu ngẫu nhiên dễ đọc lại từ log.
func randomPassword() (string, error) {
	// Bỏ các ký tự dễ nhầm lẫn: 0/O, 1/l/I. Mật khẩu này thường được chép tay từ log.
	const alphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("sinh mật khẩu: %w", err)
	}
	for i, b := range buf {
		buf[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(buf), nil
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		l = slog.LevelInfo
	}
	// Log cấu trúc JSON: dễ lọc bằng jq khi gỡ lỗi trên máy đích.
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l}))
}
