// Package graph dựng quan hệ giữa các domain.
//
// Bốn loại cạnh trả lời cùng một câu hỏi từ bốn hướng: "domain này còn anh em nào
// nữa". Một mình mỗi loại đều có thể sai; cùng nhau chúng cho thấy cả cụm hạ tầng,
// và đó là thứ cho phép chặn cả cụm bằng một thao tác thay vì lần lượt từng cái.
package graph

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"strings"

	"github.com/benji/dnsguard/internal/ingest"
	"github.com/benji/dnsguard/internal/store"
)

// Builder dựng các loại cạnh.
type Builder struct {
	store *store.Store
	log   *slog.Logger
}

// New dựng Builder.
func New(s *store.Store, log *slog.Logger) *Builder {
	return &Builder{store: s, log: log}
}

// BuildCNAME dựng cạnh cname_to từ chuỗi CNAME đã phân giải.
//
// Cạnh này có hướng: A trỏ tới B không có nghĩa B trỏ về A.
func (b *Builder) BuildCNAME(ctx context.Context) (int, error) {
	rows, err := b.store.FactsBySource(ctx, "dns", 20000)
	if err != nil {
		return 0, err
	}

	edges := 0
	for _, row := range rows {
		var facts struct {
			CNAMEChain []string `json:"cname_chain"`
		}
		if err := json.Unmarshal(row.Data, &facts); err != nil {
			continue
		}

		from := row.DomainID
		for _, hop := range facts.CNAMEChain {
			hop = strings.ToLower(strings.TrimSuffix(hop, "."))
			if hop == "" || hop == row.Name {
				continue
			}
			to, err := b.store.EnsureDomain(ctx, hop, ingest.ETLD1(hop), "related")
			if err != nil {
				return edges, err
			}
			if err := b.store.SaveRelation(ctx, from, to, store.RelCNAME, 1.0,
				map[string]any{"chain_position": len(facts.CNAMEChain)}); err != nil {
				return edges, err
			}
			edges++
			// Nối theo từng mắt xích chứ không nối thẳng gốc tới đích cuối: mắt
			// xích ở giữa mới là thứ tiết lộ nhà cung cấp thật.
			from = to
		}
	}
	return edges, nil
}

// BuildSameASN dựng cạnh same_asn giữa các domain cùng một ASN.
//
// Bỏ qua ASN trung tính: Google chứa cả doubleclick lẫn google.com, Cloudflare chứa
// gần như mọi thứ. Nối tất cả domain trong những ASN đó sẽ tạo ra một đồ thị dày đặc
// mà không nói lên điều gì.
func (b *Builder) BuildSameASN(ctx context.Context) (int, error) {
	rows, err := b.store.FactsBySource(ctx, "asn", 20000)
	if err != nil {
		return 0, err
	}

	byASN := map[int][]int64{}
	for _, row := range rows {
		var facts struct {
			ASN int `json:"asn"`
		}
		if err := json.Unmarshal(row.Data, &facts); err != nil || facts.ASN == 0 {
			continue
		}
		if isNeutralASN(facts.ASN) {
			continue
		}
		byASN[facts.ASN] = append(byASN[facts.ASN], row.DomainID)
	}

	edges := 0
	for asn, ids := range byASN {
		// Một ASN chứa quá nhiều domain của mạng này thì nhiều khả năng là hạ tầng
		// dùng chung chưa có trong danh sách trung tính, không phải một cụm adtech.
		if len(ids) < 2 || len(ids) > 50 {
			continue
		}
		for i := range ids {
			for j := i + 1; j < len(ids); j++ {
				if err := b.store.SaveRelation(ctx, ids[i], ids[j], store.RelSameASN, 1.0,
					map[string]any{"asn": asn}); err != nil {
					return edges, err
				}
				edges++
			}
		}
	}
	return edges, nil
}

// BuildSameCert dựng cạnh same_cert giữa các domain xuất hiện cùng một chứng chỉ.
func (b *Builder) BuildSameCert(ctx context.Context) (int, error) {
	rows, err := b.store.FactsBySource(ctx, "cert", 20000)
	if err != nil {
		return 0, err
	}

	edges := 0
	for _, row := range rows {
		var facts struct {
			SANs []string `json:"sans"`
		}
		if err := json.Unmarshal(row.Data, &facts); err != nil {
			continue
		}
		for _, san := range facts.SANs {
			san = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(san)), "*.")
			if san == "" || san == row.Name || strings.ContainsAny(san, " \t") {
				continue
			}
			to, err := b.store.EnsureDomain(ctx, san, ingest.ETLD1(san), "related")
			if err != nil {
				continue
			}
			if err := b.store.SaveRelation(ctx, row.DomainID, to, store.RelSameCert, 1.0, nil); err != nil {
				return edges, err
			}
			edges++
		}
	}
	return edges, nil
}

// BuildCoOccurrence dựng cạnh co_occurs từ log truy vấn.
//
// Hai domain được coi là đồng xuất hiện khi cùng một client truy vấn chúng cách nhau
// dưới ba giây. Độ mạnh tính bằng PMI chuẩn hóa: đếm thô sẽ cho điểm cao nhất cho
// những domain phổ biến nhất, còn PMI trả lời đúng câu hỏi cần hỏi — "hai domain này
// xuất hiện cùng nhau nhiều hơn mức ngẫu nhiên bao nhiêu lần".
//
// Tính lại theo lô hằng đêm chứ không realtime: nó cần quét cả cửa sổ nhiều ngày.
func (b *Builder) BuildCoOccurrence(ctx context.Context, windowDays, minCount int) (int, error) {
	if err := b.store.ClearRelations(ctx, store.RelCoOccurs); err != nil {
		return 0, err
	}

	pairs, totals, grand, err := b.store.CoOccurrences(ctx, windowDays, minCount)
	if err != nil {
		return 0, err
	}
	if grand == 0 {
		return 0, nil
	}

	edges := 0
	for _, p := range pairs {
		pa := float64(totals[p.A]) / float64(grand)
		pb := float64(totals[p.B]) / float64(grand)
		pab := float64(p.Count) / float64(grand)
		if pa <= 0 || pb <= 0 || pab <= 0 {
			continue
		}

		pmi := math.Log2(pab / (pa * pb))
		// Chuẩn hóa về [0,1] bằng PMI chuẩn hóa: chia cho -log2(P(a,b)), giá trị 1
		// nghĩa là hai domain luôn xuất hiện cùng nhau.
		denom := -math.Log2(pab)
		if denom <= 0 {
			continue
		}
		strength := pmi / denom
		if strength <= 0 {
			continue // xuất hiện cùng nhau ít hơn mức ngẫu nhiên: không phải bằng chứng
		}
		if strength > 1 {
			strength = 1
		}

		if err := b.store.SaveRelation(ctx, p.A, p.B, store.RelCoOccurs, strength,
			map[string]any{"count": p.Count}); err != nil {
			return edges, err
		}
		edges++
	}
	return edges, nil
}

// isNeutralASN lặp lại tập ASN trung tính của bộ phân loại. Giữ riêng ở đây để
// package graph không phụ thuộc vào chi tiết nội bộ của classify.
func isNeutralASN(asn int) bool {
	switch asn {
	case 15169, 13335, 16509, 14618, 8075, 32934, 714, 54113, 20940, 16625, 13414, 2906, 46489:
		return true
	}
	return false
}
