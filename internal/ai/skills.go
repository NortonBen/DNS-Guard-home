package ai

import (
	"sort"
	"strings"
)

// MaxInjectedChars chặn phần skill chèn vào phình to nuốt hết cửa sổ ngữ cảnh.
//
// Mười skill dựng sẵn cộng lại khoảng 8.700 ký tự. Trần phải cao hơn con số đó, cộng
// chỗ cho skill người vận hành tự thêm: một câu hỏi chạm nhiều chủ đề sẽ kéo gần hết
// bộ skill, và trần quá chặt thì phần thừa bị bỏ *im lặng* — model trả lời thiếu căn
// cứ mà không ai biết vì sao. Mười hai nghìn ký tự khoảng 3.000 token, không đáng kể
// với cửa sổ ngữ cảnh của mọi model dùng được cho việc này.
const MaxInjectedChars = 12000

// Skill là một mẩu quy ước vận hành có từ khoá kích hoạt riêng.
//
// Ý tưởng: thay vì nhồi mọi quy ước vào một system prompt khổng lồ gửi lại ở mọi
// lượt, mỗi mẩu kiến thức chỉ được chèn khi câu hỏi khớp từ khoá của nó. Ngữ cảnh
// gọn hơn, và câu trả lời sát việc hơn vì model không phải lọc qua mười quy ước
// không liên quan.

// MatchSkills chọn các skill nên chèn cho một câu hỏi.
func MatchSkills(all []Skill, question string) []Skill {
	folded := fold(question)

	var out []Skill
	for _, sk := range all {
		if !sk.Enabled {
			continue
		}
		if sk.Always {
			out = append(out, sk)
			continue
		}
		for _, trigger := range sk.Triggers {
			t := fold(trigger)
			if t != "" && strings.Contains(folded, t) {
				out = append(out, sk)
				break
			}
		}
	}

	// Skill "luôn dùng" lên trước: đó là quy ước nền, model nên đọc trước rồi mới
	// tới quy ước riêng của tình huống.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Always != out[j].Always {
			return out[i].Always
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// RenderSkills ghép các skill đã chọn thành một khối chèn vào ngữ cảnh.
func RenderSkills(matched []Skill, budget int) string {
	if len(matched) == 0 {
		return ""
	}
	if budget <= 0 {
		budget = MaxInjectedChars
	}

	var b strings.Builder
	b.WriteString("Quy ước vận hành của mạng này (ưu tiên hơn kiến thức chung):\n")
	used := b.Len()

	for _, sk := range matched {
		block := "\n## " + sk.Name + "\n"
		if sk.Description != "" {
			block += sk.Description + "\n"
		}
		block += strings.TrimSpace(sk.Content) + "\n"

		// Vượt trần thì dừng hẳn thay vì cắt giữa chừng: một skill bị cắt dở dễ
		// khiến model hiểu ngược ý hơn là không có nó.
		if used+len(block) > budget {
			b.WriteString("\n(còn skill khác bị bỏ qua do giới hạn độ dài)\n")
			break
		}
		b.WriteString(block)
		used += len(block)
	}
	return b.String()
}

// SkillNames liệt kê tên skill đã dùng, để giao diện cho người dùng thấy câu trả
// lời dựa trên những quy ước nào.
func SkillNames(matched []Skill) []string {
	out := make([]string, 0, len(matched))
	for _, sk := range matched {
		out = append(out, sk.Name)
	}
	return out
}

// fold đưa chuỗi về dạng so khớp: thường hoá và bỏ dấu tiếng Việt.
//
// Tự viết bảng thay vì kéo golang.org/x/text vào: go.mod của dự án này có đúng
// một phụ thuộc trực tiếp, và thêm một thư viện Unicode đầy đủ để làm mỗi việc
// bỏ dấu là cái giá sai. Bảng dưới đây phủ hết nguyên âm có dấu tiếng Việt, thứ
// duy nhất mà từ khoá skill thực tế dùng tới.
func fold(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if folded, found := diacritics[r]; found {
			b.WriteRune(folded)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// diacritics ánh xạ mọi nguyên âm có dấu tiếng Việt về chữ cái cơ bản, cộng chữ
// đ. Nhờ nó người gõ "ha tang" vẫn khớp skill đặt từ khoá "hạ tầng".
var diacritics = func() map[rune]rune {
	groups := map[rune]string{
		'a': "àáảãạăằắẳẵặâầấẩẫậ",
		'e': "èéẻẽẹêềếểễệ",
		'i': "ìíỉĩị",
		'o': "òóỏõọôồốổỗộơờớởỡợ",
		'u': "ùúủũụưừứửữự",
		'y': "ỳýỷỹỵ",
		'd': "đ",
	}
	out := map[rune]rune{}
	for base, accented := range groups {
		for _, r := range accented {
			out[r] = base
		}
	}
	return out
}()

// BuiltinSkills là bộ quy ước nạp lần đầu khi CSDL còn trống.
//
// Người vận hành sửa hay tắt được; hệ thống không ghi đè nếu skill đã tồn tại.
func BuiltinSkills() []Skill {
	return []Skill{
		{
			Name:        "nguyen-tac-an-toan",
			Description: "Quy tắc nền khi khuyên chặn hay bỏ chặn.",
			Always:      true,
			Enabled:     true,
			Builtin:     true,
			Content: `- Chặn nhầm một domain thật gây hỏng dịch vụ cho cả nhà và người dùng thường
  không biết vì sao. Bỏ sót một domain quảng cáo chỉ tốn chút băng thông.
  Khi phân vân, nghiêng về KHÔNG chặn.
- Không bao giờ khuyên chặn theo cả eTLD+1 nếu chỉ có bằng chứng ở một subdomain.
- Domain của ngân hàng, cơ quan nhà nước, y tế, giáo dục, email, họp trực tuyến,
  cập nhật hệ điều hành và hạ tầng chứng chỉ luôn phải giữ lại, kể cả khi điểm cao.
- Kết luận của AI là MỘT tín hiệu, không phải phán quyết. Điểm luật vẫn là chính.`,
		},
		{
			Name:        "doc-tin-hieu",
			Description: "Cách đọc điểm và tín hiệu của DNSGuard.",
			Triggers:    []string{"tín hiệu", "signal", "điểm", "score", "vì sao", "tại sao", "giải thích"},
			Enabled:     true,
			Builtin:     true,
			Content: `- Điểm là tổng trọng số các tín hiệu đã kích hoạt. Ngưỡng chặn là 5,5.
- Tín hiệu dương buộc tội: cname_adtech, asn_adtech, keyword, third_party,
  fan_out, beacon, etld1_blocked, http_beacon, vt_malicious, ai_adtech.
- Tín hiệu âm bảo vệ: high_rank (Tranco top 50k, −8,0), long_lived_content,
  shared_cdn, http_real_site, vt_clean, ai_clean.
- Ngưỡng chặn KHÁC NHAU theo phân loại, không phải một con số chung: xem skill
  scoring-thresholds. Nhãn được chọn trước, rồi mới áp ngưỡng của nhãn đó.
- Không tín hiệu HTTP hay AI nào tự nó đủ vượt ngưỡng chặn. Đó là thiết kế:
  một lần đoán sai không được đủ để chặn nhầm. Tín hiệu hạ tầng thì có —
  cname_adtech (+6,0) một mình đã vượt 5,5, và đó cũng là chủ ý.
- cname_adtech là bằng chứng mạnh nhất vì CNAME cloaking là kỹ thuật né chính
  mà hệ thống được xây để bắt.`,
		},
		{
			Name:        "vong-doi-domain",
			Description: "Domain đi qua những trạng thái nào.",
			Triggers:    []string{"trạng thái", "status", "vòng đời", "staging", "chờ duyệt", "canary"},
			Enabled:     true,
			Builtin:     true,
			Content: `- new: vừa thấy trong mạng, chưa chấm điểm xong.
- staging: đủ điểm để nghi ngờ, đang trong thời gian canary trước khi chặn thật.
- blocked: đang nằm trong file blocklist mà router tải về.
- allowed: người vận hành đã cho qua. Quyết định của con người thắng máy.
- ignored: bỏ qua, không chấm điểm nữa.
Chỉ blocked mới thực sự vào file danh sách. Việc chặn do router làm, không phải DNSGuard.`,
		},
		// Sáu skill dưới đây viết bằng tiếng Anh, khác bốn skill trên.
		//
		// Không phải cho vui: nội dung skill là *chỉ dẫn cho model*, không phải văn
		// bản cho người đọc, và mọi model thương mại đều được huấn luyện chủ yếu
		// trên tiếng Anh — chỉ dẫn tiếng Anh được tuân thủ ổn định hơn, nhất là ở
		// các model rẻ. Phần người vận hành đọc là `Description` cùng giao diện, và
		// chúng vẫn là tiếng Việt. Từ khoá kích hoạt cũng phải có cả hai thứ tiếng
		// vì câu hỏi thì viết bằng tiếng Việt.
		{
			Name:        "scoring-thresholds",
			Description: "Ngưỡng chặn khác nhau theo từng phân loại, không phải một con số chung.",
			Triggers: []string{
				"ngưỡng", "threshold", "bao nhiêu điểm", "đủ điểm", "chặn khi nào",
				"when blocked", "score enough",
			},
			Enabled: true,
			Builtin: true,
			Content: `NO single block threshold exists. Category is decided first, then that
category's own threshold applies. Defaults, editable by the operator — read the live
value with a tool rather than quoting these:

  ads, tracking, telemetry   5.5
  malware, cryptomining      3.0   (low on purpose: one strong signal should suffice)
  adult                      5.5   but disabled, so it never auto-blocks
  cdn, content               99.0  disabled — these labels mean "reviewed, leave alone"

A disabled category never promotes anything, whatever the score. A domain labelled cdn
scoring 8.0 is NOT pending a block. Say so plainly instead of warning about a number
that will never act.

Crossing the threshold moves a domain to staging — a canary period, not a block.`,
		},
		{
			Name:        "cname-cloaking",
			Description: "Kỹ thuật né chính mà hệ thống được xây để bắt.",
			Triggers: []string{
				"cname", "cloaking", "bí danh", "alias", "trỏ về", "points to",
				"first-party tracking", "chuỗi cname",
			},
			Enabled: true,
			Builtin: true,
			Content: `CNAME cloaking is the evasion DNSGuard exists to catch. Treat a chain as the
strongest evidence available.

A site publishes metrics.theirsite.com — first-party looking, passes every name-based
filter. DNS resolves it via CNAME to eulerian.net, a tracking company. Browsers send
first-party cookies to a third party, and no lexical rule can see it because the visible
name is genuinely innocent.

That is why cname_adtech is +6.0, above the ads threshold: it is the one signal strong
enough to act alone, because the chain is a fact about ownership, not an inference.

Only the FINAL hop attributes. Intermediate hops are CDN or DNS plumbing; ignore them.

Two things to tell the operator:
- Blocking a cloaked subdomain rarely breaks the parent site — only the beacon fails.
- Never block the parent because a subdomain is cloaked. Scope to the exact name.`,
		},
		{
			Name:        "false-positive-triage",
			Description: "Quy trình khi người dùng báo có thứ bị hỏng.",
			Triggers: []string{
				"chặn nhầm", "false positive", "không vào được", "bị hỏng", "broken",
				"not working", "bỏ chặn", "unblock", "mở chặn", "lỗi rồi",
			},
			Enabled: true,
			Builtin: true,
			Content: `A report of something broken is the highest-stakes case here. Work the steps in
order; do not jump to a verdict.

1. Confirm DNSGuard caused it. Check the domain's real status before assuming.
2. Look up the exact name AND its eTLD+1. A blocked parent breaks every subdomain —
   the usual cause of wide breakage.
3. Read the signals behind the score. Blocked on behaviour alone (fan_out, beacon,
   third_party) is far more likely wrong than blocked on cname_adtech or etld1_blocked.
4. Check when the block landed against when breakage started. Months-old block, other
   cause.

If it looks like a false positive: allow the specific name, not the parent, and add it
to the soft protect list so re-scoring cannot block it again. State that allowing takes
effect at the next publish, not instantly.

Never suggest disabling a whole category to fix one domain.`,
		},
		{
			Name:        "publishing-pipeline",
			Description: "Vì sao chặn trên giao diện chưa có tác dụng ngay.",
			Triggers: []string{
				"xuất bản", "publish", "danh sách", "blocklist", "mikrotik", "router",
				"snapshot", "rollback", "chưa có tác dụng", "not blocked yet", "adlist",
			},
			Enabled: true,
			Builtin: true,
			Content: `DNSGuard blocks nothing. It writes list files; the router downloads them and
blocks. State this first whenever the question is "why is it still resolving".

  decision -> status blocked -> publish renders /lists/<category>.txt
  -> router fetches on its own schedule -> block takes effect

Every arrow adds delay. Publish runs every 6 hours by default; the router's fetch
interval lives on the router and DNSGuard cannot see it. "Blocked" in the UI plus "still
resolving" on a device is normal for a while, not a bug.

Two safety behaviours worth explaining:
- Publishing is REFUSED when a new list falls below a fraction of the previous entry
  count (default 50%), catching a bad rule change that wipes a list. Fix the cause of
  the collapse, do not lower the ratio.
- Each content change creates a snapshot, and any snapshot can be rolled back. Rollback
  restores the file; it does not change domain statuses.`,
		},
		{
			Name:        "enrichment-limits",
			Description: "Thiếu dữ kiện không phải bằng chứng buộc tội.",
			Triggers: []string{
				"dữ kiện", "facts", "làm giàu", "enrichment", "virustotal", "tranco",
				"asn", "rdap", "chứng chỉ", "certificate", "thiếu dữ liệu", "no data",
			},
			Enabled: true,
			Builtin: true,
			Content: `MISSING DATA IS NOT EVIDENCE. An empty field means the lookup has not run, is
rate-limited, or failed — never that the domain is suspicious. Say "not looked up yet"
instead of reasoning from absence.

  dns    CNAME chain and addresses. Strongest attribution. 6h TTL.
  asn    Which network owns the address. Useless on shared hosting.
  cert   Sibling names on one certificate. A CDN wildcard proves nothing.
  rdap   Registration age only. Old is not safe; new is not bad.
  rank   Tranco popularity. Absent just means outside the list — most domains are.
  http   One root-page fetch. No JavaScript, no subresources. Off by default.
  vt     Queried only for already-suspicious domains; free quota is small.
  ai     A language model's opinion, capped low on purpose.

Facts expire. Check the fetch time before treating one as current; recommend a re-check
rather than asserting.

A recorded error describes the SOURCE, not the domain. A VirusTotal quota failure says
nothing at all about the domain.`,
		},
		{
			Name:        "category-boundaries",
			Description: "Ranh giới giữa tám nhãn, nhất là ba nhãn dễ nhầm.",
			Triggers: []string{
				"phân loại", "category", "nhãn", "taxonomy", "label",
				"ads hay tracking", "telemetry", "cdn", "khác nhau thế nào",
			},
			Enabled: true,
			Builtin: true,
			Content: `Eight labels. The boundaries that actually get confused:

ads vs tracking — ads SERVES something (banner, video, auction). tracking COLLECTS
something (pixel, analytics). A domain that only receives beacons and returns nothing is
tracking, even when an ad company owns it.

tracking vs telemetry — tracking follows a PERSON across sites; telemetry reports a
DEVICE or APP state to its own vendor. The giveaway is regularity: telemetry fires on a
timer whether or not anyone is using the device. That is why beacon points at telemetry.

cdn vs content — cdn is shared infrastructure serving unrelated sites, so blocking it
breaks things with nothing to do with each other. content is first-party and ordinary.

content is the honest fallback. It means "no sufficient evidence of anything else", not
"verified safe". Do not turn a content label into a block recommendation without naming
the new evidence that would justify it.`,
		},
		{
			Name:        "quy-uoc-tra-loi",
			Description: "Cách trình bày câu trả lời cho người vận hành.",
			Always:      true,
			Enabled:     true,
			Builtin:     true,
			Content: `- Kết luận trước, căn cứ sau. Người đọc thường chỉ đọc dòng đầu.
- Dẫn số liệu thật lấy từ công cụ, kèm tên trường. Không làm tròn thành "nhiều".
- Chưa tra được thì nói chưa có dữ liệu, đừng suy đoán từ tên miền.
- Ngắn. Ba đến mười dòng cho câu hỏi thường.`,
		},
	}
}
