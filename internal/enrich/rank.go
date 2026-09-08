package enrich

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/publicsuffix"
)

// RankFacts là kết quả tra thứ hạng.
type RankFacts struct {
	Tranco int `json:"tranco"`
}

// rankCutoff là số dòng đầu được nạp.
//
// Tín hiệu high_rank chỉ quan tâm tới top 50.000, nên nạp cả một triệu dòng là lãng
// phí bộ nhớ trên Pi mà không thêm thông tin nào.
const rankCutoff = 50000

// RankEnricher tra thứ hạng Tranco từ file cục bộ.
type RankEnricher struct {
	mu       sync.RWMutex
	ranks    map[string]int
	loaded   bool
	path     string
	loadedAt time.Time
}

// NewRank dựng bộ tra thứ hạng.
func NewRank() *RankEnricher {
	return &RankEnricher{ranks: make(map[string]int)}
}

func (e *RankEnricher) Name() string { return "rank" }

// LoadTable nạp danh sách Tranco từ file CSV hoặc ZIP chứa CSV.
// Định dạng mỗi dòng: rank,domain
func (e *RankEnricher) LoadTable(path string) error {
	reader, closer, err := OpenMaybeZip(path, ".csv")
	if err != nil {
		return err
	}
	defer closer()

	ranks := make(map[string]int, rankCutoff)
	cr := csv.NewReader(bufio.NewReaderSize(reader, 1<<20))
	cr.FieldsPerRecord = -1
	cr.ReuseRecord = true

	for len(ranks) < rankCutoff {
		record, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("đọc danh sách Tranco: %w", err)
		}
		if len(record) < 2 {
			continue
		}
		rank, err := strconv.Atoi(strings.TrimSpace(record[0]))
		if err != nil {
			continue
		}
		ranks[strings.ToLower(strings.TrimSpace(record[1]))] = rank
	}

	// Không nhận bảng rỗng, và không nhận bảng thiếu những tên miền mà mọi bảng xếp
	// hạng đều phải có. Một URL trả về trang lỗi hoặc một file đúng kiểu CSV nhưng
	// sai thứ tự cột đều nạp "thành công" ra bảng vô dụng — và vì high_rank là tín
	// hiệu bảo vệ mạnh nhất, hỏng ở đây nghĩa là mất lớp chống chặn nhầm mà không có
	// dấu hiệu nào. Rẻ hơn nhiều so với việc phát hiện qua một domain bị chặn oan.
	if err := checkRanks(ranks); err != nil {
		return err
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.ranks, e.loaded = ranks, true
	e.path, e.loadedAt = path, time.Now().UTC()
	return nil
}

// rankSentinels là các tên miền phải có mặt trong bất kỳ bảng xếp hạng phổ biến nào.
//
// Chọn tên miền hạ tầng toàn cầu, không phải tên miền theo vùng hay theo thị hiếu:
// mọi bảng xếp hạng dựng bằng mọi phương pháp đều xếp chúng rất cao.
var rankSentinels = []string{"google.com", "microsoft.com", "amazonaws.com"}

// checkRanks từ chối bảng rỗng hoặc bảng không chứa tên miền chuẩn nào.
func checkRanks(ranks map[string]int) error {
	if len(ranks) == 0 {
		return errors.New("bảng xếp hạng không có mục nào đọc được")
	}
	for _, d := range rankSentinels {
		if ranks[d] > 0 {
			return nil
		}
	}
	return fmt.Errorf("bảng xếp hạng đọc ra %d mục nhưng không có tên miền chuẩn nào (%s) — "+
		"nhiều khả năng sai định dạng hoặc sai thứ tự cột", len(ranks), strings.Join(rankSentinels, ", "))
}

// Status mô tả trạng thái bảng cho giao diện cài đặt.
func (e *RankEnricher) Status() TableStatus {
	e.mu.RLock()
	defer e.mu.RUnlock()

	st := TableStatus{
		Kind: "rank", Label: "Danh sách Tranco", Loaded: e.loaded,
		Entries: len(e.ranks), Path: e.path, DefaultURL: DefaultTrancoURL,
		Describes: "Thứ hạng phổ biến của tên miền. Nuôi tín hiệu bảo vệ high_rank (−8,0) — lớp phòng vệ mạnh nhất chống chặn nhầm.",
	}
	if !e.loadedAt.IsZero() {
		st.LoadedAt = e.loadedAt.Format(time.RFC3339)
	}
	return st
}

// Loaded cho biết bảng đã nạp chưa.
func (e *RankEnricher) Loaded() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.loaded
}

// Enrich tra thứ hạng của eTLD+1.
//
// Tra ở mức eTLD+1 chứ không phải tên đầy đủ: Tranco xếp hạng theo tên miền đăng ký,
// và tín hiệu bảo vệ cũng phải áp ở mức đó — nếu example.com xếp hạng cao thì
// subdomain của nó cũng cần được cân nhắc cẩn thận.
func (e *RankEnricher) Enrich(ctx context.Context, domain string) (any, error) {
	if !e.Loaded() {
		return nil, fmt.Errorf("%w: chưa nạp danh sách Tranco", ErrDisabled)
	}
	etld1, err := publicsuffix.EffectiveTLDPlusOne(strings.TrimSuffix(domain, "."))
	if err != nil {
		etld1 = domain
	}

	e.mu.RLock()
	defer e.mu.RUnlock()
	return RankFacts{Tranco: e.ranks[etld1]}, nil
}
