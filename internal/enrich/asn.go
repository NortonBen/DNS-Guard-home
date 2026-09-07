package enrich

import (
	"bufio"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ASNFacts là kết quả tra ASN.
type ASNFacts struct {
	ASN     int    `json:"asn"`
	Org     string `json:"org"`
	Country string `json:"country"`
	IP      string `json:"ip"`
}

// asnRange là một dải địa chỉ thuộc một ASN.
type asnRange struct {
	start   netip.Addr
	end     netip.Addr
	asn     int
	country string
	org     string
}

// ASNEnricher tra ASN từ bảng iptoasn cục bộ.
//
// Dùng dữ liệu cục bộ chứ không gọi API: tra ASN xảy ra cho mọi domain ứng viên, và
// một dịch vụ ngoài ở đường nóng đó sẽ vừa chậm vừa lộ toàn bộ danh sách domain của
// mạng ra bên thứ ba.
type ASNEnricher struct {
	// resolveIP tách ra để test không cần mạng.
	resolveIP func(ctx context.Context, domain string) (netip.Addr, error)

	mu       sync.RWMutex
	v4       []asnRange
	v6       []asnRange
	loaded   bool
	path     string
	loadedAt time.Time
}

// NewASN dựng bộ tra ASN. Bảng dữ liệu nạp riêng bằng LoadTable.
func NewASN(resolve func(ctx context.Context, domain string) (netip.Addr, error)) *ASNEnricher {
	return &ASNEnricher{resolveIP: resolve}
}

func (e *ASNEnricher) Name() string { return "asn" }

// LoadTable nạp bảng iptoasn dạng TSV, nén gzip hoặc không.
//
// Định dạng: range_start<TAB>range_end<TAB>AS_number<TAB>country<TAB>AS_description
func (e *ASNEnricher) LoadTable(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("mở bảng ip2asn %q: %w", path, err)
	}
	defer f.Close()

	var src io.Reader = bufio.NewReaderSize(f, 1<<20)
	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(src)
		if err != nil {
			return fmt.Errorf("giải nén bảng ip2asn: %w", err)
		}
		defer gz.Close()
		src = gz
	}

	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	var v4, v6 []asnRange
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) < 5 {
			continue
		}
		start, err1 := netip.ParseAddr(fields[0])
		end, err2 := netip.ParseAddr(fields[1])
		asn, err3 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil || err3 != nil || asn == 0 {
			continue
		}
		r := asnRange{start: start, end: end, asn: asn, country: fields[3], org: fields[4]}
		if start.Is4() {
			v4 = append(v4, r)
		} else {
			v6 = append(v6, r)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("đọc bảng ip2asn: %w", err)
	}

	// Sắp xếp một lần để tra cứu dùng tìm kiếm nhị phân thay vì quét tuyến tính.
	cmp := func(a, b asnRange) int { return a.start.Compare(b.start) }
	slices.SortFunc(v4, cmp)
	slices.SortFunc(v6, cmp)

	e.mu.Lock()
	defer e.mu.Unlock()
	e.v4, e.v6, e.loaded = v4, v6, true
	e.path, e.loadedAt = path, time.Now().UTC()
	return nil
}

// Status mô tả trạng thái bảng cho giao diện cài đặt.
func (e *ASNEnricher) Status() TableStatus {
	e.mu.RLock()
	defer e.mu.RUnlock()

	st := TableStatus{
		Kind: "asn", Label: "Bảng tra ASN", Loaded: e.loaded,
		Entries: len(e.v4) + len(e.v6), Path: e.path, DefaultURL: DefaultASNURL,
		Describes: "Ánh xạ dải IP sang số hiệu mạng và tên tổ chức. Thiếu nó thì tín hiệu asn_adtech không hoạt động.",
	}
	if !e.loadedAt.IsZero() {
		st.LoadedAt = e.loadedAt.Format(time.RFC3339)
	}
	return st
}

// Loaded cho biết bảng đã nạp chưa.
func (e *ASNEnricher) Loaded() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.loaded
}

// Enrich phân giải domain rồi tra ASN của địa chỉ đầu tiên.
func (e *ASNEnricher) Enrich(ctx context.Context, domain string) (any, error) {
	if !e.Loaded() {
		return nil, fmt.Errorf("%w: chưa nạp bảng ip2asn", ErrDisabled)
	}
	ip, err := e.resolveIP(ctx, domain)
	if err != nil {
		return nil, err
	}
	r, ok := e.Lookup(ip)
	if !ok {
		return ASNFacts{IP: ip.String()}, nil
	}
	return ASNFacts{ASN: r.asn, Org: r.org, Country: r.country, IP: ip.String()}, nil
}

// Lookup tra ASN của một địa chỉ.
func (e *ASNEnricher) Lookup(ip netip.Addr) (asnRange, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	table := e.v4
	if !ip.Is4() {
		ip = ip.Unmap()
		if !ip.Is4() {
			table = e.v6
		}
	}

	// Tìm dải cuối cùng có start ≤ ip, rồi kiểm tra ip ≤ end. Các dải trong iptoasn
	// không chồng lấn nên một lần kiểm tra là đủ.
	i, found := slices.BinarySearchFunc(table, ip, func(r asnRange, target netip.Addr) int {
		return r.start.Compare(target)
	})
	if !found {
		i--
	}
	if i < 0 || i >= len(table) {
		return asnRange{}, false
	}
	if table[i].start.Compare(ip) <= 0 && table[i].end.Compare(ip) >= 0 {
		return table[i], true
	}
	return asnRange{}, false
}
