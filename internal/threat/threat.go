// Package threat đối chiếu địa chỉ IP với các danh sách hạ tầng độc hại tải về được.
//
// Tra cứu bằng bảng cục bộ chứ không gọi API, cùng lý do như bảng ASN: việc này chạy
// cho mọi địa chỉ mà mạng phân giải tới, và một dịch vụ ngoài ở đó vừa chậm vừa lộ
// toàn bộ hoạt động của mạng ra bên thứ ba.
//
// Nhiều nguồn chứ không một, vì "hạ tầng độc hại" không phải một khái niệm đồng nhất:
// một dải bị chiếm đoạt và một máy chủ C2 đang sống đòi hai mức phản ứng khác nhau.
// Mỗi nguồn tải, nạp và hiện trạng thái riêng, nên người vận hành thấy ngay nguồn nào
// đang mục — đúng loại hỏng hóc mà nguồn mặc định cũ mắc phải suốt sáu tháng.
package threat

import (
	"bufio"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/benji/dnsguard/internal/enrich"
)

// timeLayout là định dạng dấu thời gian báo ra API.
const timeLayout = time.RFC3339

// Giới hạn độ rộng của một dải được phép nạp.
//
// Một dòng hỏng kiểu "0.0.0.0/0" sẽ đánh dấu mọi địa chỉ là độc hại, biến cảnh báo
// thành nhiễu trắng đúng lúc người vận hành cần tin nó nhất. Danh sách thật hiếm khi
// rộng hơn /8 với IPv4; dải rộng hơn thế gần như chắc chắn là lỗi định dạng.
//
// Hai hằng này cũng chặn trên số bước của Lookup, nên chúng vừa là rào an toàn vừa
// là ràng buộc hiệu năng.
const (
	minPrefixV4 = 8
	minPrefixV6 = 19
)

// Match là kết quả khớp một địa chỉ với một danh sách.
type Match struct {
	// Prefix là dải đã khớp, ví dụ "1.2.3.4/32" hoặc "45.66.0.0/16".
	Prefix string
	// Source là khóa nguồn đã khớp, ví dụ "spamhaus_drop" hoặc "threatfox".
	//
	// Khóa tự mô tả chứ không phải mã nội bộ: nó được ghi xuống CSDL và xuất ra hồ sơ
	// điều tra, nơi người đọc phải hiểu được xuất xứ mà không cần tra bảng ánh xạ.
	Source string
}

// Set là một danh sách hạ tầng độc hại đã nạp vào bộ nhớ.
//
// Cài enrich.Table nên dùng lại được cơ chế tải về nguyên tử ở package enrich, dù
// bản thân nó không phải nguồn làm giàu: nó tra theo địa chỉ, không theo tên miền.
type Set struct {
	def Definition

	mu       sync.RWMutex
	prefixes map[netip.Prefix]struct{}
	loaded   bool
	path     string
	loadedAt time.Time
}

// New dựng một Set rỗng theo định nghĩa nguồn. Chưa nạp gì cho tới khi gọi LoadTable.
func New(def Definition) *Set { return &Set{def: def} }

// Name là khóa dùng ở API cập nhật bảng tra cứu.
func (s *Set) Name() string { return s.def.Kind }

// Dest là nơi lưu file tải về của nguồn này.
func (s *Set) Dest() string { return s.def.Dest }

// Loaded cho biết danh sách đã nạp chưa.
func (s *Set) Loaded() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loaded
}

// LoadTable đọc danh sách từ file, theo định dạng đã khai báo ở định nghĩa nguồn.
func (s *Set) LoadTable(path string) error {
	parse, inner := parseIPListLine, ".txt"
	if s.def.Format == FormatThreatFoxCSV {
		parse, inner = parseThreatFoxLine, ".csv"
	}

	// Bản đầy đủ của ThreatFox phát hành dạng ZIP một-file; mọi nguồn khác là văn bản
	// trần. OpenMaybeZip nhận cả hai nên định dạng đóng gói không phải mối bận tâm của
	// hàm này.
	f, closer, err := enrich.OpenMaybeZip(path, inner)
	if err != nil {
		return fmt.Errorf("mở danh sách %q: %w", path, err)
	}
	defer closer()

	prefixes := make(map[netip.Prefix]struct{}, 4096)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)

	for sc.Scan() {
		if p, ok := parse(sc.Text()); ok {
			prefixes[p] = struct{}{}
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("đọc danh sách %q: %w", path, err)
	}

	// Không nạp danh sách rỗng. Một URL trả về trang lỗi HTML sẽ tải xuống thành công
	// và phân tích ra 0 mục; báo lỗi ở đây giữ cho danh sách đang dùng còn nguyên.
	if len(prefixes) == 0 {
		return errors.New("danh sách không có mục nào đọc được")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.prefixes = prefixes
	s.loaded = true
	s.path = path
	s.loadedAt = time.Now().UTC()
	return nil
}

// Lookup tra một địa chỉ. Trả về false khi không nằm trong dải nào.
//
// Thử từng độ dài prefix từ hẹp tới rộng thay vì tìm nhị phân trên dải đã sắp xếp.
// Hai lý do: dải trong danh sách đe dọa *có thể lồng nhau* — một /32 nằm trong một
// /16 của cùng danh sách — mà tìm nhị phân theo điểm bắt đầu xử lý việc đó rất khó
// chứng minh là đúng; và duyệt từ hẹp tới rộng cho ra dải **cụ thể nhất**, tức là
// dòng mà người vận hành sẽ tìm thấy khi mở file danh sách ra đối chiếu.
//
// Số bước bị chặn bởi minPrefixV4/minPrefixV6: 25 lần tra map với IPv4, 110 với
// IPv6. Tra map là hằng số, nên chi phí không phụ thuộc kích thước danh sách.
func (s *Set) Lookup(ip netip.Addr) (Match, bool) {
	ip = ip.Unmap()

	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.loaded {
		return Match{}, false
	}

	narrowest := minPrefixV6
	if ip.Is4() {
		narrowest = minPrefixV4
	}
	for bits := ip.BitLen(); bits >= narrowest; bits-- {
		p, err := ip.Prefix(bits)
		if err != nil {
			return Match{}, false
		}
		if _, hit := s.prefixes[p]; hit {
			return Match{Prefix: p.String(), Source: s.def.Kind}, true
		}
	}
	return Match{}, false
}

// parseIPListLine đọc một dòng của danh sách mỗi dòng một mục.
//
// Chấp nhận cả địa chỉ trần lẫn ký hiệu CIDR vì hai định dạng phổ biến nhất chia nhau
// hai kiểu đó: abuse.ch liệt kê địa chỉ đơn, còn Spamhaus DROP liệt kê dải. Chú thích
// bắt đầu bằng "#" hoặc ";" — cũng là hai kiểu của hai nguồn.
//
// Trả về false cho dòng trống, dòng chú thích, dòng không đọc được, và dải rộng quá
// mức cho phép.
func parseIPListLine(line string) (netip.Prefix, bool) {
	if i := strings.IndexAny(line, "#;"); i >= 0 {
		line = line[:i]
	}
	field, _, _ := strings.Cut(strings.TrimSpace(line), " ")
	return parseField(field)
}

// parseField chuẩn hoá một chuỗi địa chỉ hoặc dải thành Prefix đã che đúng độ dài.
func parseField(field string) (netip.Prefix, bool) {
	if field == "" {
		return netip.Prefix{}, false
	}

	p, err := netip.ParsePrefix(field)
	if err != nil {
		// Địa chỉ trần là một dải chứa đúng một địa chỉ.
		addr, addrErr := netip.ParseAddr(field)
		if addrErr != nil {
			return netip.Prefix{}, false
		}
		addr = addr.Unmap()
		p = netip.PrefixFrom(addr, addr.BitLen())
	}
	// Chuẩn hoá: địa chỉ trong dải phải được che theo đúng độ dài, nếu không dòng
	// "1.2.3.4/24" sẽ tạo ra khóa mà Lookup không bao giờ dựng lại được.
	p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()).Masked()

	minBits := minPrefixV6
	if p.Addr().Is4() {
		minBits = minPrefixV4
	}
	if p.Bits() < minBits {
		return netip.Prefix{}, false
	}
	return p, true
}
