package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/benji/dnsguard/internal/store"
)

// BuildRegistry dựng bộ công cụ cho một lượt hỏi đáp.
//
// Ba vòng an toàn, theo đúng thứ tự tin cậy giảm dần:
//
//   - Công cụ ĐỌC luôn có, cho mọi vai trò. Chúng chỉ chạm vào dữ liệu mà người
//     dùng đó đã xem được trên giao diện.
//   - Công cụ hỏi lại chỉ được đăng ký khi host khác nil. Tầng API truyền nil cho
//     vai trò không phải quản trị, nên hỏi đáp không mở được đường vòng qua chốt
//     phân quyền của REST.
//   - Công cụ từ máy chủ MCP ngoài do quản trị viên tự chịu trách nhiệm khi kết
//     nối. Chúng không bao giờ đè được lên công cụ dựng sẵn.
//
// Không có công cụ nào đổi trạng thái domain. Chặn và bỏ chặn vẫn phải bấm trên
// giao diện, nơi đã có kiểm tra danh sách bảo vệ và ghi nhật ký quyết định.
func BuildRegistry(ctx context.Context, db *store.Store, hist *Store, host Host) (*Registry, []string) {
	r := &Registry{}
	addReadTools(r, db, hist)
	if host != nil {
		addRecheckTool(r, host)
	}
	return r, addMCPTools(ctx, r, hist)
}

func addReadTools(r *Registry, db *store.Store, hist *Store) {
	r.add(Tool{
		Name: "domain_lookup",
		Description: "Tra một domain theo tên chính xác: trạng thái, điểm, phân loại, " +
			"lưu lượng và các tín hiệu đã kích hoạt.",
		Schema: obj(map[string]any{"name": str("Tên miền đầy đủ, ví dụ doubleclick.net")}, "name"),
		Run: func(ctx context.Context, args map[string]any) (string, error) {
			name := argString(args, "name")
			if name == "" {
				return "", fmt.Errorf("thiếu tham số name")
			}
			d, err := db.GetDomainByName(ctx, name)
			if err != nil {
				return "", err
			}
			return asJSON(domainView(d))
		},
	})

	r.add(Tool{
		Name: "domain_search",
		Description: "Tìm domain theo chuỗi con trong tên. Dùng khi không biết tên đầy đủ, " +
			"hoặc để liệt kê domain theo trạng thái và phân loại.",
		Schema: obj(map[string]any{
			"query":    str("Chuỗi con trong tên miền. Để trống để liệt kê theo bộ lọc khác."),
			"status":   str("Lọc theo trạng thái: new, staging, blocked, allowed, ignored"),
			"category": str("Lọc theo phân loại: ads, tracking, telemetry, malware, cryptomining, adult, cdn, content"),
			"limit":    num("Số kết quả tối đa, mặc định 20, tối đa 100"),
		}),
		Run: func(ctx context.Context, args map[string]any) (string, error) {
			items, _, _, err := db.ListDomains(ctx, store.DomainFilter{
				Query:      argString(args, "query"),
				Statuses:   splitArg(argString(args, "status")),
				Categories: splitArg(argString(args, "category")),
				Sort:       "score",
				Desc:       true,
				Limit:      int(argInt(args, "limit", 20)),
			})
			if err != nil {
				return "", err
			}
			// Bảng gọn thay vì JSON: một danh sách hai mươi dòng dạng JSON tốn gấp
			// ba lần token mà không nói thêm điều gì.
			var b strings.Builder
			fmt.Fprintf(&b, "Tìm thấy %d domain:\n", len(items))
			for _, d := range items {
				fmt.Fprintf(&b, "%s | %s | điểm=%s | %s | truy_van=%d\n",
					d.Name, d.Status, floatOr(d.Score, "-"), categoryKey(d), d.QueryCount)
			}
			return b.String(), nil
		},
	})

	r.add(Tool{
		Name: "domain_facts",
		Description: "Xem dữ kiện làm giàu của một domain: CNAME, IP, ASN, chứng chỉ, " +
			"tuổi đăng ký, thứ hạng Tranco, kết quả tải trang và VirusTotal.",
		Schema: obj(map[string]any{"name": str("Tên miền đầy đủ")}, "name"),
		Run: func(ctx context.Context, args map[string]any) (string, error) {
			d, err := db.GetDomainByName(ctx, argString(args, "name"))
			if err != nil {
				return "", err
			}
			facts, err := db.Facts(ctx, d.ID)
			if err != nil {
				return "", err
			}
			if len(facts) == 0 {
				return "Chưa có dữ kiện làm giàu nào cho domain này.", nil
			}
			return asJSON(facts)
		},
	})

	r.add(Tool{
		Name: "domain_relations",
		Description: "Xem các domain có quan hệ với domain này: cùng CNAME, cùng ASN, " +
			"cùng chứng chỉ, hoặc hay xuất hiện cùng nhau trong lưu lượng.",
		Schema: obj(map[string]any{
			"name":  str("Tên miền đầy đủ"),
			"kinds": str("Lọc loại quan hệ: cname, same_asn, same_cert, co_occurs. Ngăn bằng dấu phẩy."),
			"limit": num("Số quan hệ tối đa, mặc định 30"),
		}, "name"),
		Run: func(ctx context.Context, args map[string]any) (string, error) {
			d, err := db.GetDomainByName(ctx, argString(args, "name"))
			if err != nil {
				return "", err
			}
			g, err := db.GraphFor(ctx, d.ID, splitArg(argString(args, "kinds")), 0,
				int(argInt(args, "limit", 30)))
			if err != nil {
				return "", err
			}
			if len(g.Edges) == 0 {
				return "Không có quan hệ nào đã dựng cho domain này.", nil
			}

			names := make(map[int64]store.GraphNode, len(g.Nodes))
			for _, n := range g.Nodes {
				names[n.ID] = n
			}
			var b strings.Builder
			fmt.Fprintf(&b, "%d quan hệ của %s:\n", len(g.Edges), d.Name)
			for _, e := range g.Edges {
				// Cạnh vô hướng lưu cả hai chiều, nên chỉ in đầu không phải gốc.
				other := e.To
				if other == d.ID {
					other = e.From
				}
				n := names[other]
				fmt.Fprintf(&b, "%s | %s | %s | do_manh=%.2f\n",
					e.Kind, n.Name, n.Status, e.Strength)
			}
			return b.String(), nil
		},
	})

	r.add(Tool{
		Name: "domain_timeline",
		Description: "Xem lưu lượng truy vấn của một domain theo thời gian và các thiết bị " +
			"đã truy vấn nó.",
		Schema: obj(map[string]any{
			"name":  str("Tên miền đầy đủ"),
			"hours": num("Số giờ nhìn lại, mặc định 168 (7 ngày)"),
		}, "name"),
		Run: func(ctx context.Context, args map[string]any) (string, error) {
			d, err := db.GetDomainByName(ctx, argString(args, "name"))
			if err != nil {
				return "", err
			}
			hours := argInt(args, "hours", 168)
			from := store.TimeAt(time.Now().Add(-time.Duration(hours) * time.Hour))
			buckets, clients, err := db.Timeline(ctx, d.ID, from, store.Now())
			if err != nil {
				return "", err
			}
			return asJSON(map[string]any{
				"domain": d.Name, "tu": from, "moc_thoi_gian": buckets, "thiet_bi": clients,
			})
		},
	})

	r.add(Tool{
		Name: "domain_decisions",
		Description: "Xem lịch sử quyết định của con người trên một domain: ai chặn, " +
			"ai bỏ chặn, vì lý do gì.",
		Schema: obj(map[string]any{
			"name":  str("Tên miền đầy đủ"),
			"limit": num("Số quyết định tối đa, mặc định 20"),
		}, "name"),
		Run: func(ctx context.Context, args map[string]any) (string, error) {
			d, err := db.GetDomainByName(ctx, argString(args, "name"))
			if err != nil {
				return "", err
			}
			history, err := db.History(ctx, d.ID, int(argInt(args, "limit", 20)))
			if err != nil {
				return "", err
			}
			if len(history) == 0 {
				return "Chưa có quyết định nào cho domain này.", nil
			}
			return asJSON(history)
		},
	})

	r.add(Tool{
		Name:        "stats_overview",
		Description: "Số liệu tổng quan của mạng: lưu lượng, tỉ lệ chặn, số domain chờ duyệt.",
		Schema: obj(map[string]any{
			"hours": num("Số giờ nhìn lại, mặc định 24"),
		}),
		Run: func(ctx context.Context, args map[string]any) (string, error) {
			hours := argInt(args, "hours", 24)
			from := store.TimeAt(time.Now().Add(-time.Duration(hours) * time.Hour))
			overview, err := db.StatsOverview(ctx, from, store.Now())
			if err != nil {
				return "", err
			}
			byStatus, err := db.CountByStatus(ctx)
			if err != nil {
				return "", err
			}
			// Chuỗi thời gian dài và không giúp trả lời câu hỏi nào mà người vận
			// hành thực sự hỏi qua chat; bỏ đi để dành token.
			overview.Timeseries = nil
			return asJSON(map[string]any{"tong_quan": overview, "theo_trang_thai": byStatus})
		},
	})

	r.add(Tool{
		Name: "top_domains",
		Description: "Bảng xếp hạng theo lưu lượng: domain nhiều truy vấn nhất, " +
			"thiết bị hỏi nhiều nhất, hoặc domain bị chặn nhiều nhất.",
		Schema: obj(map[string]any{
			"dimension": str("domains, clients hoặc blocked. Mặc định domains."),
			"hours":     num("Số giờ nhìn lại, mặc định 24"),
			"limit":     num("Số dòng, mặc định 20"),
		}),
		Run: func(ctx context.Context, args map[string]any) (string, error) {
			dimension := argString(args, "dimension")
			if dimension == "" {
				dimension = "domains"
			}
			hours := argInt(args, "hours", 24)
			from := store.TimeAt(time.Now().Add(-time.Duration(hours) * time.Hour))
			entries, err := db.StatsTop(ctx, dimension, from, store.Now(),
				int(argInt(args, "limit", 20)))
			if err != nil {
				return "", err
			}
			var b strings.Builder
			for _, e := range entries {
				fmt.Fprintf(&b, "%s | truy_van=%d | %s | %s\n",
					e.Key, e.Queries, orDash(e.Status), orDash(e.Category))
			}
			if b.Len() == 0 {
				return "Không có dữ liệu trong khoảng thời gian này.", nil
			}
			return b.String(), nil
		},
	})

	r.add(Tool{
		Name: "ai_history",
		Description: "Xem các lần AI đã kết luận về một domain trước đây: nhãn, độ tin cậy, " +
			"lý do và thời điểm. Dùng để biết kết luận có thay đổi theo thời gian không.",
		Schema: obj(map[string]any{
			"name":  str("Tên miền đầy đủ"),
			"limit": num("Số kết luận tối đa, mặc định 10"),
		}, "name"),
		Run: func(ctx context.Context, args map[string]any) (string, error) {
			if hist == nil {
				return "Chưa bật nhật ký AI.", nil
			}
			name := argString(args, "name")
			if name == "" {
				return "", fmt.Errorf("thiếu tham số name")
			}
			verdicts, err := hist.VerdictsFor(ctx, name, int(argInt(args, "limit", 10)))
			if err != nil {
				return "", err
			}
			if len(verdicts) == 0 {
				return "Chưa từng hỏi AI về domain này.", nil
			}
			var b strings.Builder
			for _, v := range verdicts {
				fmt.Fprintf(&b, "%s | %s | tin_cay=%.2f | %s\n",
					v.CreatedAt, v.Category, v.Confidence, v.Reason)
			}
			return b.String(), nil
		},
	})
}

// addRecheckTool đăng ký công cụ ghi duy nhất: xếp một job hỏi lại.
//
// An toàn vì nó không đổi gì cả — chỉ đưa domain vào hàng đợi để lượt phân loại
// sau hỏi lại model. Kết quả vẫn chỉ là một tín hiệu bị chặn trần, và quyết định
// cuối vẫn của người vận hành.
func addRecheckTool(r *Registry, host Host) {
	r.add(Tool{
		Name: "domain_recheck",
		Description: "Xếp hàng một lượt hỏi lại model về một domain. Dùng khi dữ kiện đã " +
			"thay đổi hoặc kết luận cũ có vẻ sai. Không đổi trạng thái domain.",
		Schema: obj(map[string]any{"name": str("Tên miền đầy đủ")}, "name"),
		Run: func(ctx context.Context, args map[string]any) (string, error) {
			name := argString(args, "name")
			if name == "" {
				return "", fmt.Errorf("thiếu tham số name")
			}
			jobID, err := host.Recheck(ctx, name)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Đã xếp hàng hỏi lại %s (job %s). "+
				"Kết quả sẽ có sau khi job chạy xong.", name, jobID), nil
		},
	})
}

// domainView là bản rút gọn của một domain để gửi cho model.
//
// Bỏ các mốc thời gian nội bộ và cờ kỹ thuật: chúng không giúp trả lời câu hỏi
// nào mà vẫn tốn token ở mọi lượt gọi.
func domainView(d store.Domain) map[string]any {
	view := map[string]any{
		"ten":          d.Name,
		"ten_mien_goc": d.ETLD1,
		"trang_thai":   d.Status,
		"nguon_goc":    d.Origin,
		"truy_van":     d.QueryCount,
		"thiet_bi":     d.ClientCount,
		"lan_dau":      d.FirstSeen,
		"lan_cuoi":     d.LastSeen,
		"do_nguoi_dat": d.IsManual,
	}
	if d.Score != nil {
		view["diem"] = *d.Score
	}
	if d.Confidence != nil {
		view["do_tin_cay"] = *d.Confidence
	}
	if d.Category != nil {
		view["phan_loai"] = d.Category.Key
	}
	if len(d.Signals) > 0 {
		signals := make([]string, 0, len(d.Signals))
		for _, s := range d.Signals {
			signals = append(signals, fmt.Sprintf("%s(%+.1f)", s.Kind, s.Weight))
		}
		view["tin_hieu"] = signals
	}
	return view
}

func categoryKey(d store.Domain) string {
	if d.Category == nil {
		return "-"
	}
	return d.Category.Key
}

func floatOr(f *float64, fallback string) string {
	if f == nil {
		return fallback
	}
	return formatFloat(*f)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func asJSON(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("mã hóa kết quả: %w", err)
	}
	return string(raw), nil
}

func splitArg(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ---- đọc tham số công cụ ----
//
// Model gửi tham số dưới dạng JSON tự do, nên kiểu không bao giờ chắc chắn: một
// số nguyên có thể tới dưới dạng số hoặc chuỗi tuỳ hôm. Các hàm dưới đây nhận
// cả hai thay vì báo lỗi — sai kiểu không phải lý do đáng để hỏng cả lượt hỏi.

func argString(args map[string]any, key string) string {
	if v, found := args[key]; found {
		if s, isStr := v.(string); isStr {
			return strings.ToLower(strings.TrimSpace(s))
		}
	}
	return ""
}

func argInt(args map[string]any, key string, def int64) int64 {
	v, found := args[key]
	if !found {
		return def
	}
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case string:
		var out int64
		if _, err := fmt.Sscanf(n, "%d", &out); err == nil {
			return out
		}
	}
	return def
}

func obj(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func num(desc string) map[string]any { return map[string]any{"type": "number", "description": desc} }
