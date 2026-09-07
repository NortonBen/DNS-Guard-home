package ai

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/benji/dnsguard/internal/store"
)

// Nhật ký AI phải nằm ở một file khác hẳn CSDL chính.
func TestDefaultPathIsSeparateFile(t *testing.T) {
	cases := map[string]string{
		"/var/lib/dnsguard/dnsguard.db": "/var/lib/dnsguard/dnsguard-ai.db",
		"./data/dnsguard.db":            "data/dnsguard-ai.db",
		"/tmp/custom.sqlite":            "/tmp/custom-ai.sqlite",
		"/tmp/khong-duoi":               "/tmp/khong-duoi-ai.db",
	}
	for main, want := range cases {
		if got := DefaultPath(main); got != want {
			t.Errorf("DefaultPath(%q) = %q, muốn %q", main, got, want)
		}
		if DefaultPath(main) == main {
			t.Errorf("DefaultPath(%q) trùng CSDL chính", main)
		}
	}
}

// Lịch sử một domain là thứ trả lời câu "kết luận có đổi theo thời gian không".
func TestVerdictHistoryPerDomain(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()

	first, err := db.SaveRequest(ctx, Request{Kind: KindClassify, Model: "m1"},
		[]Verdict{{Domain: "example.com", Category: "content", Confidence: 0.8}})
	if err != nil {
		t.Fatalf("SaveRequest: %v", err)
	}
	second, err := db.SaveRequest(ctx, Request{Kind: KindRecheck, Model: "m2"},
		[]Verdict{{Domain: "example.com", Category: "ads", Confidence: 0.9}})
	if err != nil {
		t.Fatalf("SaveRequest: %v", err)
	}
	if first == second {
		t.Fatal("hai lượt gọi nhận cùng một id")
	}

	verdicts, err := db.VerdictsFor(ctx, "EXAMPLE.COM", 10)
	if err != nil {
		t.Fatalf("VerdictsFor: %v", err)
	}
	if len(verdicts) != 2 {
		t.Fatalf("số kết luận = %d, muốn 2", len(verdicts))
	}
	// Mới nhất trước: người đọc lịch sử quan tâm kết luận hiện hành.
	if verdicts[0].Category != "ads" {
		t.Errorf("kết luận mới nhất = %q, muốn ads", verdicts[0].Category)
	}
}

// Xoá một lượt phải kéo theo kết luận của nó, không để lại dòng mồ côi.
func TestPruneCascadesToVerdicts(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()

	if _, err := db.SaveRequest(ctx, Request{
		Kind: KindClassify, CreatedAt: "2020-01-01T00:00:00Z",
	}, []Verdict{{Domain: "cu.com", Category: "ads"}}); err != nil {
		t.Fatalf("SaveRequest: %v", err)
	}

	n, err := db.PruneRequests(ctx, 30)
	if err != nil {
		t.Fatalf("PruneRequests: %v", err)
	}
	if n != 1 {
		t.Fatalf("số lượt đã xoá = %d, muốn 1", n)
	}

	verdicts, err := db.VerdictsFor(ctx, "cu.com", 10)
	if err != nil {
		t.Fatalf("VerdictsFor: %v", err)
	}
	if len(verdicts) != 0 {
		t.Errorf("còn %d kết luận mồ côi sau khi xoá lượt gọi", len(verdicts))
	}
}

// Danh sách không được kéo theo prompt: hai mươi dòng sẽ thành vài trăm kilobyte.
func TestListRequestsOmitsBulkText(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()

	big := strings.Repeat("x", 5000)
	if _, err := db.SaveRequest(ctx, Request{
		Kind: KindClassify, Prompt: big, Response: big,
	}, nil); err != nil {
		t.Fatalf("SaveRequest: %v", err)
	}

	items, err := db.ListRequests(ctx, "", 10, 0)
	if err != nil {
		t.Fatalf("ListRequests: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("số dòng = %d, muốn 1", len(items))
	}
	if items[0].Prompt != "" || items[0].Response != "" {
		t.Error("danh sách trả cả prompt và phản hồi thô")
	}
	if items[0].PromptChars != len(big) {
		t.Errorf("prompt_chars = %d, muốn %d", items[0].PromptChars, len(big))
	}
}

// Skill dựng sẵn chỉ nạp khi thiếu: bản sửa của người vận hành phải sống sót qua
// lần khởi động sau.
func TestBuiltinSkillsDoNotOverwriteEdits(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()

	if err := db.EnsureBuiltinSkills(ctx); err != nil {
		t.Fatalf("EnsureBuiltinSkills: %v", err)
	}
	skills, err := db.ListSkills(ctx)
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	if len(skills) != len(BuiltinSkills()) {
		t.Fatalf("số skill = %d, muốn %d", len(skills), len(BuiltinSkills()))
	}

	edited := skills[0]
	edited.Content = "nội dung do người vận hành sửa"
	if _, err := db.SaveSkill(ctx, edited); err != nil {
		t.Fatalf("SaveSkill: %v", err)
	}

	if err := db.EnsureBuiltinSkills(ctx); err != nil {
		t.Fatalf("EnsureBuiltinSkills lần hai: %v", err)
	}
	after, err := db.ListSkills(ctx)
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	if len(after) != len(skills) {
		t.Errorf("số skill = %d, muốn %d — đã tạo trùng", len(after), len(skills))
	}
	for _, sk := range after {
		if sk.Name == edited.Name && sk.Content != edited.Content {
			t.Error("bản sửa của người vận hành bị ghi đè")
		}
	}
}

// Skill dựng sẵn tắt được nhưng không xoá được: xoá nó thì lần khởi động sau sẽ
// lặng lẽ tạo lại và người dùng tưởng mình xoá hụt.
func TestBuiltinSkillCannotBeDeleted(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()

	if err := db.EnsureBuiltinSkills(ctx); err != nil {
		t.Fatalf("EnsureBuiltinSkills: %v", err)
	}
	skills, err := db.ListSkills(ctx)
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	if err := db.DeleteSkill(ctx, skills[0].ID); err == nil {
		t.Error("xoá được skill dựng sẵn")
	}

	// Skill người dùng tự thêm thì xoá được.
	id, err := db.SaveSkill(ctx, Skill{Name: "cua-toi", Enabled: true, Content: "x"})
	if err != nil {
		t.Fatalf("SaveSkill: %v", err)
	}
	if err := db.DeleteSkill(ctx, id); err != nil {
		t.Errorf("không xoá được skill tự thêm: %v", err)
	}
}

// Cập nhật máy chủ MCP mà không gửi token thì giữ nguyên token cũ: giao diện không
// đọc lại được token nên không thể gửi lại nó.
func TestMCPServerKeepsTokenOnUpdate(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()

	if _, err := db.SaveMCPServer(ctx, MCPServer{
		Name: "noi-bo", URL: "https://mcp.noi-bo.vn", AuthHeader: "Authorization:Bearer bi-mat",
		Enabled: true,
	}); err != nil {
		t.Fatalf("SaveMCPServer: %v", err)
	}

	// Cập nhật chỉ đổi ghi chú, không gửi token.
	if _, err := db.SaveMCPServer(ctx, MCPServer{
		Name: "noi-bo", URL: "https://mcp.noi-bo.vn", Note: "đổi ghi chú", Enabled: true,
	}); err != nil {
		t.Fatalf("SaveMCPServer lần hai: %v", err)
	}

	servers, err := db.ListMCPServers(ctx, false)
	if err != nil {
		t.Fatalf("ListMCPServers: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("số máy chủ = %d, muốn 1", len(servers))
	}
	if servers[0].AuthHeader == "" {
		t.Error("token bị xoá khi cập nhật mà không gửi lại nó")
	}
	if !servers[0].HasAuth {
		t.Error("HasAuth sai")
	}
	if servers[0].Note != "đổi ghi chú" {
		t.Errorf("ghi chú = %q, muốn cập nhật", servers[0].Note)
	}
}

// URL không phải http(s) bị từ chối ngay: một máy chủ MCP khai sai chỉ hỏng lúc
// gọi, và lúc đó nó đã kịp làm hỏng một lượt hỏi đáp của người dùng.
func TestMCPServerRejectsBadURL(t *testing.T) {
	db := newTestStore(t)
	cases := []MCPServer{
		{Name: "thieu-url"},
		{Name: "sai-giao-thuc", URL: "ftp://vi-du.vn"},
		{URL: "https://vi-du.vn"},
	}
	for _, tc := range cases {
		if _, err := db.SaveMCPServer(context.Background(), tc); err == nil {
			t.Errorf("chấp nhận máy chủ không hợp lệ: %+v", tc)
		}
	}
}

// Cuộc hỏi đáp giữ được dấu vết gọi công cụ: không có nó thì agent là hộp đen.
func TestChatKeepsToolTrace(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()

	chatID, err := db.CreateChat(ctx, "domain này là gì", "admin")
	if err != nil {
		t.Fatalf("CreateChat: %v", err)
	}
	if err := db.AddMessage(ctx, chatID, "user", "criteo.com là gì", nil); err != nil {
		t.Fatalf("AddMessage: %v", err)
	}
	steps := []Step{{Tool: "domain_lookup", Args: `{"name":"criteo.com"}`, Result: "..."}}
	if err := db.AddMessage(ctx, chatID, "assistant", "Đó là mạng quảng cáo.", steps); err != nil {
		t.Fatalf("AddMessage: %v", err)
	}

	messages, err := db.Messages(ctx, chatID)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("số lượt = %d, muốn 2", len(messages))
	}
	if len(messages[1].Steps) != 1 || messages[1].Steps[0].Tool != "domain_lookup" {
		t.Errorf("dấu vết công cụ = %+v, muốn giữ nguyên", messages[1].Steps)
	}
	// Lượt của người dùng không có công cụ, nhưng vẫn phải là mảng rỗng chứ không
	// phải null: giao diện lặp qua nó không cần kiểm tra thêm.
	if messages[0].Steps == nil {
		t.Error("steps của lượt người dùng là null thay vì mảng rỗng")
	}
}

// Migration của CSDL AI chạy lại được trên file đã có mà không hỏng.
func TestMigrationIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ai.db")

	for range 2 {
		db, err := OpenStore(path, true)
		if err != nil {
			t.Fatalf("OpenStore: %v", err)
		}
		if _, err := db.SaveRequest(context.Background(),
			Request{Kind: KindAsk, Actor: store.ActorSystem}, nil); err != nil {
			t.Fatalf("SaveRequest: %v", err)
		}
		db.Close()
	}
}
