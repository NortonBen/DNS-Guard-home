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

	"github.com/benji/dnsguard/internal/ai"
	"github.com/benji/dnsguard/internal/api"
	"github.com/benji/dnsguard/internal/auth"
	"github.com/benji/dnsguard/internal/catalog"
	"github.com/benji/dnsguard/internal/config"
	"github.com/benji/dnsguard/internal/enrich"
	"github.com/benji/dnsguard/internal/events"
	"github.com/benji/dnsguard/internal/graph"
	"github.com/benji/dnsguard/internal/ingest"
	"github.com/benji/dnsguard/internal/llm"
	"github.com/benji/dnsguard/internal/monitor"
	"github.com/benji/dnsguard/internal/pcap"
	"github.com/benji/dnsguard/internal/publish"
	"github.com/benji/dnsguard/internal/store"
	"github.com/benji/dnsguard/internal/threat"
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

	aiDB, err := openAIStore(cfg, log)
	if err != nil {
		return err
	}
	if aiDB != nil {
		defer aiDB.Close()
	}

	bus := events.NewBroker(log)
	publisher := publish.New(db, cfg.ListsDir, cfg.PublishMinRatio, cfg.PublishSink, log)
	syncer := catalog.New(db, log)
	builder := graph.New(db, log)
	enrichers := buildEnrichers(cfg, log)

	llmClient := llm.New(cfg.AIBaseURL, cfg.AIAPIKey, cfg.AIModel, cfg.AIMaxTokens)
	classifier := registerAI(context.Background(), db, aiDB, cfg, llmClient, enrichers, log)

	// Công tắc phân tích HTTP lưu trong CSDL và sửa được từ giao diện; biến môi trường
	// chỉ còn là giá trị mặc định cho lần chạy đầu.
	applyStoredAnalysisSetting(context.Background(), db, cfg, enrichers, log)

	runner := worker.New(db, cfg, enrichers, publisher, syncer, builder, bus, log)
	if aiDB != nil {
		runner.SetHistoryPruner(aiDB)
	}
	threats := buildThreatSet(cfg, log)
	runner.SetThreats(threats)
	listener := ingest.NewListener(ingest.Options{Addr: cfg.TZSPListen}, db, log)

	// Bộ ghi pcap là tuỳ chọn và mặc định tắt. Khi bật, nó nhận bản sao mọi khung
	// bóc được từ TZSP — kể cả khung mà tầng DNS không hiểu, vì một cuộc tấn công
	// dùng gói dị dạng sẽ biến mất khỏi bằng chứng nếu chỉ ghi thứ ta bóc được.
	var recorder *pcap.Recorder
	if cfg.PcapEnabled {
		recorder = pcap.New(pcap.Options{
			Dir:           cfg.PcapDir,
			MaxFileBytes:  int64(cfg.PcapMaxFileMB) << 20,
			MaxTotalBytes: int64(cfg.PcapMaxTotalMB) << 20,
		}, log)
		listener.SetRecorder(recorder)
		log.Info("ghi pcap phục vụ điều tra",
			"dir", cfg.PcapDir, "max_file_mb", cfg.PcapMaxFileMB, "max_total_mb", cfg.PcapMaxTotalMB)
	}

	server := api.New(api.Options{
		Store: db, Auth: auth.NewService(db, cfg.SessionTTL), Config: cfg,
		Publisher: publisher, Worker: runner, Bus: bus, Listener: listener,
		Enrichers: enrichers, Threats: threats, Pcap: recorder,
		WebFS: web.FS(), Version: version, Log: log,
		AIStore: aiDB, LLM: llmClient, Classifier: classifier,
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

	if recorder != nil {
		wg.Go(func() {
			if err := recorder.Run(ctx); err != nil {
				log.Error("bộ ghi pcap dừng", "err", err)
			}
		})
	}

	wg.Go(func() {
		runner.Run(ctx)
	})

	wg.Go(func() {
		monitor.Run(ctx, monitor.New(), db, cfg.ResourceSampleEvery, log)
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

// openAIStore mở CSDL nhật ký AI. Trả về nil khi không mở được.
//
// Không mở được thì cả cụm AI tắt, nhưng dịch vụ vẫn chạy: phân loại bằng luật,
// thu thập và xuất danh sách không phụ thuộc vào model nào. Đánh đổi này là cố ý —
// một tính năng phụ hỏng không được kéo theo chức năng chính.
func openAIStore(cfg config.Config, log *slog.Logger) (*ai.Store, error) {
	path := cfg.AIDBPath
	if path == "" {
		path = ai.DefaultPath(cfg.DBPath)
	}

	db, err := ai.OpenStore(path, cfg.AutoMigrate)
	if err != nil {
		log.Error("không mở được CSDL nhật ký AI, tắt tính năng AI", "path", path, "err", err)
		return nil, nil
	}
	if err := db.EnsureBuiltinSkills(context.Background()); err != nil {
		log.Warn("không nạp được skill dựng sẵn", "err", err)
	}
	log.Info("đã mở CSDL nhật ký AI", "path", path)
	return db, nil
}

// registerAI đăng ký nguồn phân loại bằng model vào registry.
//
// Giới hạn tốc độ đặt ở 1 lượt/giây, burst 2: nhà cung cấp nào cũng chịu được mức
// đó, và mỗi lượt đã gói bốn mươi domain nên đây không phải nút thắt. Thứ chặn
// thật là cổng lọc ứng viên ở tầng worker.
func registerAI(ctx context.Context, db *store.Store, aiDB *ai.Store, cfg config.Config,
	client *llm.Client, enrichers *enrich.Registry, log *slog.Logger) *ai.Classifier {

	if aiDB == nil {
		return nil
	}

	// Dữ kiện lấy từ CSDL chính qua một hàm, không phải qua một tay cầm: gói ai giữ
	// ranh giới "chỉ ghi vào CSDL của mình". Cùng khuôn với cách enrich.NewASN nhận
	// hàm phân giải DNS.
	evidence := func(ctx context.Context, domains []string) ([]ai.Evidence, error) {
		found, err := db.ScoringCandidatesByName(ctx, domains)
		if err != nil {
			return nil, err
		}
		out := make([]ai.Evidence, 0, len(found))
		for _, c := range found {
			out = append(out, ai.EvidenceFrom(c.Domain, c.Facts))
		}
		return out, nil
	}

	classifier := ai.NewClassifier(client, aiDB, evidence, log)
	classifier.SetBatchSize(cfg.AIBatchSize)
	enrichers.Register(classifier, 1, 2, false)

	// Đọc cấu hình đã lưu trên giao diện và áp ngay: khóa nhập trên giao diện thắng
	// biến môi trường, và chính lời gọi này bật nguồn lên khi đủ điều kiện.
	if err := ai.ApplySettings(ctx, db, ai.Defaults{
		BaseURL: cfg.AIBaseURL, APIKey: cfg.AIAPIKey, Model: cfg.AIModel,
		MaxTokens: cfg.AIMaxTokens, BatchSize: cfg.AIBatchSize,
		ExternalEnabled: cfg.ExternalEnabled,
	}, client, classifier, enrichers, log); err != nil {
		log.Warn("không áp được cấu hình AI đã lưu", "err", err)
	}
	return classifier
}

// buildEnrichers dựng registry các nguồn làm giàu.
//
// Giới hạn tốc độ đặt riêng từng nguồn vì chúng khác nhau rất xa: phân giải DNS chịu
// được hàng chục truy vấn mỗi giây, còn crt.sh sẽ chặn nếu vượt vài truy vấn mỗi phút.
// buildThreatSet nạp mọi danh sách hạ tầng độc hại từ đĩa.
//
// Không nằm trong buildEnrichers vì chúng không phải nguồn làm giàu: chúng tra theo
// địa chỉ chứ không theo tên miền, và không đóng góp tín hiệu nào vào điểm phân loại.
func buildThreatSet(cfg config.Config, log *slog.Logger) *threat.Registry {
	// Thứ tự có nghĩa: Spamhaus DROP trước vì nó nói về *dải* hạ tầng bị chiếm đoạt —
	// bằng chứng bền hơn và ít báo nhầm hơn một địa chỉ C2 có thể đã bị thu hồi.
	reg := threat.NewRegistry(
		threat.SpamhausDROP(cfg.DROPPath),
		threat.ThreatFox(cfg.ThreatFoxPath),
	)

	for _, src := range reg.Sources() {
		if err := src.LoadTable(src.Dest()); err != nil {
			// Thiếu một danh sách chỉ làm mất cảnh báo của riêng nó, không làm hỏng gì
			// khác. Job đối chiếu vẫn chạy với những nguồn nạp được.
			log.Warn("không nạp được danh sách hạ tầng độc hại",
				"nguồn", src.Name(), "path", src.Dest(), "err", err)
			continue
		}
		log.Info("đã nạp danh sách hạ tầng độc hại",
			"nguồn", src.Name(), "path", src.Dest(), "entries", src.Status().Entries)
	}
	return reg
}

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

	// Tải trang gốc của domain để phân tích header và HTML tĩnh. Mặc định tắt: đây là
	// nguồn duy nhất gõ cửa trực tiếp máy chủ đích, nên người vận hành phải chủ động
	// bật. Giới hạn 2 trang/giây để không tạo ra một đợt quét dễ nhận ra.
	httpAnalysis := cfg.HTTPAnalysisEnabled && cfg.ExternalEnabled
	registry.Register(enrich.NewHTTP(), 2, 4, httpAnalysis)
	if cfg.HTTPAnalysisEnabled && !cfg.ExternalEnabled {
		log.Warn("phân tích HTTP bị tắt vì DNSGUARD_EXTERNAL_ENABLED=false")
	}

	// VirusTotal: bậc miễn phí khoảng 4 lượt/phút, đặt 3 để chừa biên. Cổng lọc theo
	// điểm ở tầng worker giữ lượng gọi ở mức vài chục mỗi ngày.
	vt := enrich.NewVirusTotal(cfg.VTAPIKey)
	registry.Register(vt, 3.0/60.0, 1, cfg.ExternalEnabled && vt.Configured())
	if cfg.VTAPIKey == "" {
		log.Info("chưa có khóa API VirusTotal, bỏ qua nguồn xác thực này")
	}

	return registry
}

// applyStoredAnalysisSetting đọc công tắc đã lưu và áp vào registry.
func applyStoredAnalysisSetting(ctx context.Context, db *store.Store, cfg config.Config,
	enrichers *enrich.Registry, log *slog.Logger) {

	enabled := cfg.HTTPAnalysisEnabled
	var stored bool
	if ok, err := db.GetSetting(ctx, store.SettingHTTPAnalysis, &stored); err != nil {
		log.Warn("không đọc được cấu hình phân tích HTTP", "err", err)
	} else if ok {
		enabled = stored
	}

	// DNSGUARD_EXTERNAL_ENABLED là công tắc cứng: bật trong giao diện cũng không
	// thắng được nó.
	effective := enabled && cfg.ExternalEnabled
	enrichers.SetEnabled("http", effective)

	log.Info("phân tích HTTP", "enabled", effective,
		"nguon", map[bool]string{true: "cài đặt", false: "mặc định"}[enabled != cfg.HTTPAnalysisEnabled])

	applyStoredVTKey(ctx, db, cfg, enrichers, log)
}

// applyStoredVTKey áp khóa VirusTotal đã lưu trên giao diện vào nguồn đang chạy.
//
// Khóa nhập trên giao diện thắng biến môi trường: nó mới hơn và là hành động có chủ
// ý của người quản trị, trong khi biến môi trường thường nằm trong file compose từ
// lần cài đặt đầu.
func applyStoredVTKey(ctx context.Context, db *store.Store, cfg config.Config,
	enrichers *enrich.Registry, log *slog.Logger) {

	src, ok := enrichers.Get("vt")
	if !ok {
		return
	}
	vt, ok := enrich.Unwrap(src).(*enrich.VTEnricher)
	if !ok {
		return
	}

	var stored string
	if ok, err := db.GetSetting(ctx, store.SettingVTAPIKey, &stored); err != nil {
		log.Warn("không đọc được khóa VirusTotal đã lưu", "err", err)
		return
	} else if !ok || stored == "" {
		return
	}

	vt.SetAPIKey(stored)
	enrichers.SetEnabled("vt", cfg.ExternalEnabled)
	// Không log khóa, chỉ log việc đã nạp.
	log.Info("đã nạp khóa API VirusTotal từ cài đặt")
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
