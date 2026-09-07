// Package worker chạy các job nền và lịch định kỳ.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/benji/dnsguard/internal/catalog"
	"github.com/benji/dnsguard/internal/classify"
	"github.com/benji/dnsguard/internal/config"
	"github.com/benji/dnsguard/internal/enrich"
	"github.com/benji/dnsguard/internal/events"
	"github.com/benji/dnsguard/internal/graph"
	"github.com/benji/dnsguard/internal/publish"
	"github.com/benji/dnsguard/internal/store"
)

// Loại job.
const (
	JobEnrich    = "enrich"
	JobClassify  = "classify"
	JobLifecycle = "lifecycle"
	JobCatalog   = "catalog_sync"
	JobPublish   = "publish"
	JobGraph     = "graph"
	JobBehavior  = "behavior"
	JobRetention = "retention"
	JobRescore   = "rescore"
	JobRefresh   = "refresh_lookup"
)

// Runner nhận job từ hàng đợi và thi hành.
type Runner struct {
	store     *store.Store
	cfg       config.Config
	enrichers *enrich.Registry
	publisher *publish.Publisher
	syncer    *catalog.Syncer
	graph     *graph.Builder
	bus       *events.Broker
	log       *slog.Logger
}

// New dựng Runner.
func New(s *store.Store, cfg config.Config, enrichers *enrich.Registry,
	publisher *publish.Publisher, syncer *catalog.Syncer, builder *graph.Builder,
	bus *events.Broker, log *slog.Logger) *Runner {

	return &Runner{
		store: s, cfg: cfg, enrichers: enrichers, publisher: publisher,
		syncer: syncer, graph: builder, bus: bus, log: log,
	}
}

// Run chạy các worker và bộ lập lịch tới khi ctx bị hủy.
func (r *Runner) Run(ctx context.Context) {
	var wg sync.WaitGroup

	wg.Go(func() { r.schedule(ctx) })

	// Số worker giữ nhỏ: hầu hết job là truy vấn CSDL, mà pool ghi chỉ có một kết
	// nối. Nhiều worker hơn chỉ tạo thêm tranh chấp chứ không tăng thông lượng.
	for range 2 {
		wg.Go(func() { r.workLoop(ctx) })
	}

	wg.Wait()
}

// workLoop nhận và thi hành job.
func (r *Runner) workLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		for {
			job, err := r.store.ClaimJob(ctx)
			if err != nil {
				r.log.Error("nhận job thất bại", "err", err)
				break
			}
			if job == nil {
				break
			}
			r.execute(ctx, *job)
		}
	}
}

// RunPending chạy hết job đang chờ rồi trả về.
//
// Vòng lặp nền chạy theo nhịp hai giây, nên chờ nó là chờ một khoảng không xác định.
// Hàm này cho phép rút cạn hàng đợi đồng bộ — cần cho test đầu-cuối, và cũng là thứ
// một lệnh CLI "chạy job ngay" sẽ dùng.
func (r *Runner) RunPending(ctx context.Context) error {
	for {
		job, err := r.store.ClaimJob(ctx)
		if err != nil {
			return fmt.Errorf("nhận job: %w", err)
		}
		if job == nil {
			return nil
		}
		r.execute(ctx, *job)
	}
}

func (r *Runner) execute(ctx context.Context, job store.Job) {
	start := time.Now()
	err := r.handle(ctx, job)

	if finishErr := r.store.FinishJob(ctx, job.ID, err); finishErr != nil {
		r.log.Error("kết thúc job thất bại", "job", job.ID, "err", finishErr)
	}
	if err != nil {
		r.log.Error("job thất bại", "kind", job.Kind, "job", job.ID, "err", err)
		return
	}
	r.log.Info("job xong", "kind", job.Kind, "job", job.ID, "took", time.Since(start).Round(time.Millisecond))
}

// handle điều phối theo loại job.
func (r *Runner) handle(ctx context.Context, job store.Job) error {
	switch job.Kind {
	case JobEnrich:
		return r.runEnrich(ctx, job)
	case JobClassify, JobRescore:
		return r.runClassify(ctx, job)
	case JobLifecycle:
		return r.runLifecycle(ctx)
	case JobCatalog:
		return r.runCatalog(ctx, job)
	case JobPublish:
		return r.runPublish(ctx, job)
	case JobGraph:
		return r.runGraph(ctx)
	case JobBehavior:
		return r.runBehavior(ctx)
	case JobRetention:
		return r.runRetention(ctx)
	case JobRefresh:
		return r.runRefreshLookup(ctx, job)
	default:
		return fmt.Errorf("loại job không rõ: %q", job.Kind)
	}
}

// vtMinScore là điểm tối thiểu để một domain đáng tiêu một lượt quota VirusTotal.
const vtMinScore = 3.0

// candidatesFor chọn domain cho một nguồn làm giàu.
//
// Hai nguồn chạm trực tiếp tới máy chủ đích hoặc tiêu quota có hạn nên đi qua cổng
// lọc hẹp hơn hẳn; các nguồn còn lại chỉ cần tới hạn TTL.
func (r *Runner) candidatesFor(ctx context.Context, source string) ([]store.EnrichCandidate, error) {
	switch source {
	case "http":
		return r.store.GatedCandidates(ctx, store.AnalysisGate{
			Source: "http", MinQueries: 5, MaxNegativeScore: -4.0, Limit: 50,
		})
	case "vt":
		minScore := vtMinScore
		return r.store.GatedCandidates(ctx, store.AnalysisGate{
			Source: "vt", MinQueries: 5, MinScore: &minScore,
			MaxNegativeScore: -4.0, Limit: 20,
		})
	default:
		return r.store.EnrichCandidates(ctx, source, 200)
	}
}

// runEnrich chạy từng nguồn làm giàu cho các domain tới hạn.
func (r *Runner) runEnrich(ctx context.Context, job store.Job) error {
	for _, enricher := range r.enrichers.Sources() {
		candidates, err := r.candidatesFor(ctx, enricher.Name())
		if err != nil {
			return err
		}
		if len(candidates) == 0 {
			continue
		}

		// Chạy song song có giới hạn: các nguồn ngoài đều có giới hạn tốc độ riêng,
		// và bộ bọc guarded đã lo phần đó.
		sem := make(chan struct{}, r.cfg.EnrichConcurrency)
		var wg sync.WaitGroup

		for _, c := range candidates {
			wg.Add(1)
			go func(c store.EnrichCandidate) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				data, err := enricher.Enrich(ctx, c.Name)
				if err != nil {
					// Ba lỗi này nói về *nguồn*, không nói gì về domain, nên không được
					// ghi vào domain_facts.
					//
					// Circuit breaker là trường hợp nguy hiểm nhất: một loạt domain
					// chết làm nó mở ra, rồi mọi domain xếp sau — kể cả domain hoàn
					// toàn khỏe mạnh — bị đánh dấu thất bại và treo cache nhiều ngày
					// dù chưa hề được thử. Bỏ qua và để vòng sau tra lại.
					if errors.Is(err, enrich.ErrDisabled) ||
						errors.Is(err, enrich.ErrCircuitOpen) ||
						errors.Is(err, enrich.ErrVTNoAPIKey) {
						return
					}
					// Ghi cả khi hỏng, kèm kết cục: chính dòng thất bại này ngăn hệ
					// thống tra lại ngay vòng sau. Không có nó, một domain không kết
					// nối được sẽ bị hỏi lại mỗi mười lăm phút mãi mãi.
					outcome := enrich.ClassifyOutcome(err)
					if saveErr := r.store.SaveFactErrorWithOutcome(
						ctx, c.ID, enricher.Name(), err, outcome); saveErr != nil {
						r.log.Error("ghi lỗi làm giàu thất bại", "err", saveErr)
					}
					return
				}
				// Trang đỗ tên miền giữ được lâu hơn trang thường, nên chọn TTL theo
				// nội dung đọc được chứ không chỉ theo nguồn.
				outcome := enrich.OutcomeOK
				if facts, ok := data.(enrich.HTTPFacts); ok && facts.Parking != "" {
					outcome = enrich.OutcomeParking
				}
				if err := r.store.SaveFactWithOutcome(
					ctx, c.ID, enricher.Name(), data, outcome); err != nil {
					r.log.Error("lưu kết quả làm giàu thất bại", "domain", c.Name, "err", err)
				}
			}(c)
		}
		wg.Wait()

		if err := r.store.UpdateJobProgress(ctx, job.ID, int64(len(candidates)), int64(len(candidates))); err != nil {
			r.log.Warn("cập nhật tiến độ thất bại", "err", err)
		}
	}
	return nil
}

// runClassify chấm điểm lại các domain ứng viên.
func (r *Runner) runClassify(ctx context.Context, job store.Job) error {
	weights, err := r.loadWeights(ctx)
	if err != nil {
		return err
	}
	rules, err := r.loadRules(ctx)
	if err != nil {
		return err
	}

	limit := 2000
	if job.Kind == JobRescore {
		// Chạy lại toàn bộ: giới hạn cao hơn nhiều, nhưng vẫn có giới hạn.
		limit = 100000
	}

	candidates, err := r.store.ScoringCandidates(ctx, limit)
	if err != nil {
		return err
	}

	for i, c := range candidates {
		result := classify.ScoreWith(c.Domain, c.Facts, weights, rules)
		if err := r.store.SaveScore(ctx, c.ID, result); err != nil {
			return err
		}

		if c.Status == store.StatusNew && result.Score > 0 {
			// Báo cho giao diện biết có ứng viên mới cần duyệt.
			r.bus.Publish(events.KindDomainStaged, map[string]any{
				"id": c.ID, "name": c.Domain.Name, "score": result.Score,
			})
		}
		if i%500 == 0 {
			if err := r.store.UpdateJobProgress(ctx, job.ID, int64(i), int64(len(candidates))); err != nil {
				r.log.Warn("cập nhật tiến độ thất bại", "err", err)
			}
			r.bus.Publish(events.KindJobProgress, map[string]any{
				"id": job.ID, "done": i, "total": len(candidates),
			})
		}
	}
	return r.store.UpdateJobProgress(ctx, job.ID, int64(len(candidates)), int64(len(candidates)))
}

// loadWeights đọc trọng số đã cấu hình, lùi về mặc định khi chưa đặt.
func (r *Runner) loadWeights(ctx context.Context) (classify.Weights, error) {
	var weights classify.Weights
	found, err := r.store.GetSetting(ctx, store.SettingWeights, &weights)
	if err != nil {
		return nil, err
	}
	if !found || len(weights) == 0 {
		return classify.DefaultWeights, nil
	}
	return weights, nil
}

// loadRules đọc luật tự đặt rồi gộp lên luật dựng sẵn.
//
// Đọc một lần cho cả lượt chấm chứ không mỗi domain một lần: gộp luật sao chép mấy
// map dữ liệu, và làm việc đó một trăm nghìn lần là lãng phí thuần túy.
func (r *Runner) loadRules(ctx context.Context) (classify.Rules, error) {
	var custom classify.Custom
	if _, err := r.store.GetSetting(ctx, store.SettingRules, &custom); err != nil {
		return classify.Rules{}, err
	}
	return classify.Merge(custom), nil
}

// LookupPath trả về nơi lưu bảng tra cứu của một nguồn.
func (r *Runner) LookupPath(kind string) string {
	switch kind {
	case "asn":
		return r.cfg.IP2ASNPath
	case "rank":
		return r.cfg.TrancoPath
	default:
		return ""
	}
}

// runRefreshLookup tải lại một bảng tra cứu cục bộ và nạp vào bộ nhớ ngay.
//
// Nạp lại lúc chạy chứ không đòi khởi động lại: cập nhật bảng ASN là việc hằng tháng,
// và bắt dừng dịch vụ để làm việc đó sẽ khiến không ai làm.
func (r *Runner) runRefreshLookup(ctx context.Context, job store.Job) error {
	var args struct {
		Kind string `json:"kind"`
		URL  string `json:"url"`
	}
	if err := json.Unmarshal(job.Args, &args); err != nil {
		return fmt.Errorf("giải mã tham số job: %w", err)
	}

	target, ok := r.enrichers.Reloadables()[args.Kind]
	if !ok {
		return fmt.Errorf("không có bảng tra cứu %q", args.Kind)
	}

	dest := r.LookupPath(args.Kind)
	size, err := enrich.RefreshTable(ctx, target, args.URL, dest)
	if err != nil {
		return err
	}

	status := target.Status()
	r.log.Info("đã cập nhật bảng tra cứu",
		"kind", args.Kind, "bytes", size, "entries", status.Entries, "path", dest)

	// Bảng mới có thể đổi điểm của nhiều domain — nhất là Tranco, vì nó nuôi tín hiệu
	// bảo vệ mạnh nhất. Chấm điểm lại ngay thay vì đợi tới chu kỳ kế tiếp.
	if _, err := r.store.EnqueueJob(ctx, JobClassify, nil); err != nil {
		return err
	}
	return nil
}

// lifecycleDays đọc ngưỡng vòng đời từ settings, lùi về cấu hình môi trường khi chưa đặt.
//
// Hai giá trị này sửa được qua giao diện vì chúng là quyết định vận hành, không phải
// hạ tầng: người dùng cần siết hoặc nới thời gian canary theo mức độ tin tưởng vào
// bộ phân loại của chính mạng mình.
func (r *Runner) lifecycleDays(ctx context.Context) (staging, confirm int) {
	staging, confirm = r.cfg.StagingDays, r.cfg.ConfirmTTLDays

	var v int
	if ok, err := r.store.GetSetting(ctx, store.SettingStagingDays, &v); err == nil && ok && v > 0 {
		staging = v
	}
	if ok, err := r.store.GetSetting(ctx, store.SettingConfirmTTL, &v); err == nil && ok && v > 0 {
		confirm = v
	}
	return staging, confirm
}

// runLifecycle đưa domain qua các mốc vòng đời.
func (r *Runner) runLifecycle(ctx context.Context) error {
	stagingDays, confirmDays := r.lifecycleDays(ctx)

	promoted, err := r.store.PromoteStaging(ctx, stagingDays)
	if err != nil {
		return err
	}
	expired, err := r.store.ExpireBlocked(ctx, confirmDays)
	if err != nil {
		return err
	}

	if promoted > 0 || expired > 0 {
		r.log.Info("vòng đời", "promoted", promoted, "expired", expired)
		// Trạng thái đổi nghĩa là danh sách xuất bản đã cũ.
		if _, err := r.store.EnqueueJob(ctx, JobPublish, nil); err != nil {
			return err
		}
	}
	return nil
}

// runCatalog đồng bộ các nguồn blocklist tới hạn.
func (r *Runner) runCatalog(ctx context.Context, job store.Job) error {
	var args struct {
		SourceID int64 `json:"source_id"`
	}
	if len(job.Args) > 0 {
		if err := json.Unmarshal(job.Args, &args); err != nil {
			return fmt.Errorf("giải mã tham số job: %w", err)
		}
	}

	if args.SourceID > 0 {
		if err := r.syncer.SyncOne(ctx, args.SourceID); err != nil {
			return err
		}
		r.bus.Publish(events.KindSourceSynced, map[string]any{"source_id": args.SourceID})
		return nil
	}

	n, err := r.syncer.SyncDue(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		r.bus.Publish(events.KindSourceSynced, map[string]any{"synced": n})
	}
	return nil
}

// runPublish render lại danh sách.
func (r *Runner) runPublish(ctx context.Context, job store.Job) error {
	var args struct {
		Categories []string `json:"categories"`
		Actor      string   `json:"actor"`
	}
	if len(job.Args) > 0 {
		if err := json.Unmarshal(job.Args, &args); err != nil {
			return fmt.Errorf("giải mã tham số job: %w", err)
		}
	}
	if args.Actor == "" {
		args.Actor = store.ActorSystem
	}

	results, err := r.publisher.PublishAll(ctx, args.Categories, args.Actor)
	for _, res := range results {
		if res.Changed {
			r.bus.Publish(events.KindPublishDone, map[string]any{
				"category": res.Category, "entry_count": res.EntryCount,
			})
		}
	}
	return err
}

// runGraph dựng lại các loại cạnh.
func (r *Runner) runGraph(ctx context.Context) error {
	cname, err := r.graph.BuildCNAME(ctx)
	if err != nil {
		return err
	}
	asn, err := r.graph.BuildSameASN(ctx)
	if err != nil {
		return err
	}
	cert, err := r.graph.BuildSameCert(ctx)
	if err != nil {
		return err
	}
	co, err := r.graph.BuildCoOccurrence(ctx, 7, 3)
	if err != nil {
		return err
	}

	r.log.Info("dựng lại đồ thị quan hệ",
		"cname", cname, "same_asn", asn, "same_cert", cert, "co_occurs", co)
	return nil
}

func (r *Runner) runBehavior(ctx context.Context) error {
	n, err := r.store.ComputeBehavior(ctx, 7)
	if err != nil {
		return err
	}
	r.log.Info("tính lại đặc trưng hành vi", "domains", n)
	return nil
}

func (r *Runner) runRetention(ctx context.Context) error {
	res, err := r.store.ApplyRetention(ctx,
		r.cfg.LogRetentionDays, r.cfg.HourlyRetentionDays, r.cfg.KeepSnapshots,
		r.cfg.ResourceRetainDays)
	if err != nil {
		return err
	}
	r.log.Info("dọn dữ liệu quá hạn",
		"query_events", res.QueryEvents, "domain_hourly", res.DomainHourly,
		"sessions", res.Sessions, "snapshots", res.Snapshots, "jobs", res.Jobs,
		"resources", res.Resources)

	// Xóa nhiều dòng để lại khoảng trống trong file; gộp WAL để trả lại dung lượng.
	return r.store.Checkpoint()
}
