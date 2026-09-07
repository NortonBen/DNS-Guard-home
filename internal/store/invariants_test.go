package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/benji/dnsguard/internal/classify"
)

// Bốn bất biến ở docs/06-classification.md §4. Nếu chúng hỏng, người dùng mất niềm
// tin vào cả hệ thống. Test cho chúng không được xóa hay sửa cho dễ pass.

func seedDomain(t *testing.T, s *Store, name, status string) int64 {
	t.Helper()
	now := Now()
	res, err := s.Writer().Exec(`
		INSERT INTO domains (name, name_rev, etld1, status, origin,
		                     first_seen, last_seen, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'discovered', ?, ?, ?, ?)`,
		name, reverseName(name), etld1Of(name), status, now, now, now, now)
	if err != nil {
		t.Fatalf("seed domain %q: %v", name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed domain id: %v", err)
	}
	return id
}

func etld1Of(name string) string {
	parts := strings.Split(name, ".")
	if len(parts) < 2 {
		return name
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

func countDecisions(t *testing.T, s *Store, domainID int64) int {
	t.Helper()
	var n int
	if err := s.Reader().QueryRow(
		`SELECT count(*) FROM decisions WHERE domain_id = ?`, domainID).Scan(&n); err != nil {
		t.Fatalf("count decisions: %v", err)
	}
	return n
}

func statusOf(t *testing.T, s *Store, domainID int64) string {
	t.Helper()
	var status string
	if err := s.Reader().QueryRow(
		`SELECT status FROM domains WHERE id = ?`, domainID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	return status
}

// Bất biến 1: is_manual = true khiến job chấm điểm bỏ qua domain. Con người thắng
// máy, và quyết định của con người không bị lần chạy sau ghi đè.
func TestInvariant_ManualDomainNeverRescored(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id := seedDomain(t, s, "tracker.example.com", StatusNew)

	// Quản trị cho qua bằng tay.
	if _, err := s.ApplyDecision(ctx, DecisionInput{
		DomainID: id, Action: ActionAllow, ActorLabel: "admin",
		Reason: "cần cho trang nội bộ", Manual: true,
	}); err != nil {
		t.Fatalf("ApplyDecision: %v", err)
	}

	// Domain thủ công không được xuất hiện trong danh sách ứng viên chấm điểm.
	candidates, err := s.ScoringCandidates(ctx, 100)
	if err != nil {
		t.Fatalf("ScoringCandidates: %v", err)
	}
	for _, c := range candidates {
		if c.ID == id {
			t.Fatal("domain thủ công vẫn nằm trong danh sách chấm điểm")
		}
	}

	// Kể cả khi bị ép chấm điểm thẳng, SaveScore phải là no-op.
	strong := classify.Result{
		Score: 12.0, Confidence: 0.95, Category: classify.CategoryAds,
		Signals: []classify.Signal{{Kind: classify.KindCNAMEAdtech, Weight: 6.0}},
	}
	if err := s.SaveScore(ctx, id, strong); err != nil {
		t.Fatalf("SaveScore: %v", err)
	}

	if got := statusOf(t, s, id); got != StatusAllowed {
		t.Errorf("status = %q sau khi chấm điểm, muốn allowed — quyết định thủ công đã bị ghi đè", got)
	}
	var score *float64
	s.Reader().QueryRow(`SELECT score FROM domains WHERE id = ?`, id).Scan(&score)
	if score != nil {
		t.Errorf("score = %v, domain thủ công không được chấm điểm lại", *score)
	}
}

// Bất biến 2: allowed không bao giờ tự chuyển sang trạng thái khác.
func TestInvariant_AllowedNeverAutoChanges(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id := seedDomain(t, s, "payments.example.com", StatusAllowed)

	// Quyết định tự động phải bị từ chối.
	_, err := s.ApplyDecision(ctx, DecisionInput{
		DomainID: id, Action: ActionBlock, ActorLabel: ActorSystem,
		Reason: "điểm cao", Manual: false,
	})
	if err == nil {
		t.Fatal("hệ thống chặn được domain đang ở trạng thái allowed")
	}
	if got := statusOf(t, s, id); got != StatusAllowed {
		t.Errorf("status = %q, muốn allowed", got)
	}

	// Vòng đời tự động cũng không được chạm vào.
	if n, err := s.PromoteStaging(ctx, 0); err != nil {
		t.Fatalf("PromoteStaging: %v", err)
	} else if n != 0 {
		t.Errorf("PromoteStaging đổi %d domain, muốn 0", n)
	}
	if got := statusOf(t, s, id); got != StatusAllowed {
		t.Errorf("status = %q sau vòng đời tự động, muốn allowed", got)
	}

	// Nhưng con người thì gỡ được.
	if _, err := s.ApplyDecision(ctx, DecisionInput{
		DomainID: id, Action: ActionBlock, ActorLabel: "admin",
		Reason: "xác nhận là tracker", Manual: true,
	}); err != nil {
		t.Fatalf("quản trị phải gỡ được allowed: %v", err)
	}
	if got := statusOf(t, s, id); got != StatusBlocked {
		t.Errorf("status = %q sau quyết định thủ công, muốn blocked", got)
	}
}

// Bất biến 3: mọi chuyển trạng thái sinh đúng một dòng decisions.
func TestInvariant_EveryTransitionCreatesDecision(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id := seedDomain(t, s, "ads.example.com", StatusNew)
	if n := countDecisions(t, s, id); n != 0 {
		t.Fatalf("bắt đầu với %d dòng nhật ký, muốn 0", n)
	}

	// Chuyển tự động new → staging khi vượt ngưỡng.
	result := classify.Result{
		Score: 9.0, Confidence: 0.95, Category: classify.CategoryAds,
		Signals: []classify.Signal{
			{Kind: classify.KindCNAMEAdtech, Weight: 6.0},
			{Kind: classify.KindASNAdtech, Weight: 4.0},
		},
	}
	if err := s.SaveScore(ctx, id, result); err != nil {
		t.Fatalf("SaveScore: %v", err)
	}
	if got := statusOf(t, s, id); got != StatusStaging {
		t.Fatalf("status = %q, muốn staging", got)
	}
	if n := countDecisions(t, s, id); n != 1 {
		t.Errorf("sau new→staging có %d dòng nhật ký, muốn đúng 1", n)
	}

	// Chấm điểm lại với cùng kết quả không được sinh thêm dòng nào: không có
	// chuyển trạng thái thì không có quyết định.
	if err := s.SaveScore(ctx, id, result); err != nil {
		t.Fatalf("SaveScore lần hai: %v", err)
	}
	if n := countDecisions(t, s, id); n != 1 {
		t.Errorf("chấm điểm lại sinh thêm nhật ký: %d dòng, muốn 1", n)
	}

	// Chuyển staging → blocked sau khi đủ ngày canary.
	if _, err := s.Writer().Exec(
		`UPDATE domains SET staged_at = ? WHERE id = ?`,
		TimeAt(time.Now().Add(-8*24*time.Hour)), id); err != nil {
		t.Fatalf("lùi staged_at: %v", err)
	}
	n, err := s.PromoteStaging(ctx, 7)
	if err != nil {
		t.Fatalf("PromoteStaging: %v", err)
	}
	if n != 1 {
		t.Fatalf("PromoteStaging = %d, muốn 1", n)
	}
	if got := statusOf(t, s, id); got != StatusBlocked {
		t.Errorf("status = %q, muốn blocked", got)
	}
	if n := countDecisions(t, s, id); n != 2 {
		t.Errorf("sau staging→blocked có %d dòng nhật ký, muốn đúng 2", n)
	}

	// Mỗi dòng phải nêu được lý do — đó là thứ duy nhất giải thích quyết định sáu
	// tháng sau.
	history, err := s.History(ctx, id, 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	for _, d := range history {
		if d.Reason == "" {
			t.Errorf("quyết định %q không có lý do", d.Action)
		}
		if d.ActorLabel == "" {
			t.Errorf("quyết định %q không ghi người thực hiện", d.Action)
		}
		if len(d.Snapshot) <= 2 {
			t.Errorf("quyết định %q không lưu ảnh chụp trạng thái", d.Action)
		}
	}
}

// Bất biến 4: domain trong danh sách bảo vệ không bao giờ vào blocked, kể cả khi
// quản trị yêu cầu.
func TestInvariant_ProtectedDomainCannotBeBlocked_Store(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id := seedDomain(t, s, "api.vnpay.vn", StatusNew)

	_, err := s.ApplyDecision(ctx, DecisionInput{
		DomainID: id, Action: ActionBlock, ActorLabel: "admin",
		Reason: "trông giống tracker", Manual: true,
	})
	if !errors.Is(err, ErrDomainProtected) {
		t.Fatalf("lỗi = %v, muốn ErrDomainProtected", err)
	}
	if got := statusOf(t, s, id); got != StatusNew {
		t.Errorf("status = %q, muốn new", got)
	}
	if n := countDecisions(t, s, id); n != 0 {
		t.Errorf("có %d dòng nhật ký cho thao tác bị từ chối, muốn 0", n)
	}

	// Danh sách mềm cũng phải chặn được.
	if err := s.SetSetting(ctx, SettingSoftAllow, []string{"noibo.vn"}, "admin"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	soft := seedDomain(t, s, "intranet.noibo.vn", StatusNew)
	if _, err := s.ApplyDecision(ctx, DecisionInput{
		DomainID: soft, Action: ActionBlock, ActorLabel: "admin", Reason: "thử", Manual: true,
	}); !errors.Is(err, ErrDomainProtected) {
		t.Errorf("lỗi = %v, muốn ErrDomainProtected cho danh sách mềm", err)
	}

	// Vòng đời tự động cũng phải tôn trọng danh sách bảo vệ.
	staged := seedDomain(t, s, "push.apple.com", StatusStaging)
	if _, err := s.Writer().Exec(
		`UPDATE domains SET staged_at = ? WHERE id = ?`,
		TimeAt(time.Now().Add(-30*24*time.Hour)), staged); err != nil {
		t.Fatalf("lùi staged_at: %v", err)
	}
	if _, err := s.PromoteStaging(ctx, 7); err != nil {
		t.Fatalf("PromoteStaging: %v", err)
	}
	if got := statusOf(t, s, staged); got == StatusBlocked {
		t.Error("vòng đời tự động đã chặn một domain được bảo vệ")
	}
}
