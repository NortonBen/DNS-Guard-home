package enrich

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
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
	reader, closer, err := openRankFile(path)
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

	e.mu.Lock()
	defer e.mu.Unlock()
	e.ranks, e.loaded = ranks, true
	e.path, e.loadedAt = path, time.Now().UTC()
	return nil
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

func openRankFile(path string) (io.Reader, func(), error) {
	if strings.HasSuffix(path, ".zip") {
		zr, err := zip.OpenReader(path)
		if err != nil {
			return nil, nil, fmt.Errorf("mở %q: %w", path, err)
		}
		for _, f := range zr.File {
			if !strings.HasSuffix(f.Name, ".csv") {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				zr.Close()
				return nil, nil, fmt.Errorf("mở %q trong zip: %w", f.Name, err)
			}
			return rc, func() { rc.Close(); zr.Close() }, nil
		}
		zr.Close()
		return nil, nil, fmt.Errorf("không tìm thấy file .csv trong %q", path)
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("mở %q: %w", path, err)
	}
	return f, func() { f.Close() }, nil
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
