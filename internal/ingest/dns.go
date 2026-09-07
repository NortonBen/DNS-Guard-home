package ingest

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
)

// Loại bản ghi được ghi nhận. Chỉ ba loại này phản ánh ý định truy cập thật của
// thiết bị; PTR, SRV, TXT… là nhiễu nền của mDNS và dịch vụ nội bộ.
const (
	TypeA     uint16 = 1
	TypeAAAA  uint16 = 28
	TypeHTTPS uint16 = 65
)

const dnsHeaderLen = 12

// classIN là lớp Internet. Bản ghi lớp khác (CH, HS) không nói gì về mạng thật.
const classIN uint16 = 1

var (
	errNotAQuestion    = errors.New("thông điệp DNS không có câu hỏi")
	errNotDNSAnswer    = errors.New("không phải câu trả lời DNS")
	errNoAnswerRecords = errors.New("câu trả lời DNS không có địa chỉ")
)

// TypeName đổi mã loại bản ghi sang tên để hiển thị.
func TypeName(t uint16) string {
	switch t {
	case TypeA:
		return "A"
	case TypeAAAA:
		return "AAAA"
	case TypeHTTPS:
		return "HTTPS"
	default:
		return fmt.Sprintf("TYPE%d", t)
	}
}

// TypeCode đổi tên loại bản ghi sang mã. Dùng khi nhập log dạng JSONL.
func TypeCode(name string) uint16 {
	switch strings.ToUpper(name) {
	case "A":
		return TypeA
	case "AAAA":
		return TypeAAAA
	case "HTTPS":
		return TypeHTTPS
	default:
		return 0
	}
}

// readQName đọc một tên miền không nén bắt đầu ở off, trả về tên đã hạ chữ thường
// và vị trí ngay sau tên.
//
// Chỉ dùng cho phần câu hỏi. Phần câu hỏi nằm ngay sau header nên không có gì ở
// trước để trỏ tới, vì thế nén tên ở đây là dấu hiệu gói dị dạng chứ không phải
// một dạng hợp lệ cần hỗ trợ.
func readQName(msg []byte, off int) (string, int, error) {
	var sb strings.Builder
	sb.Grow(64)

	for {
		if off >= len(msg) {
			return "", 0, fmt.Errorf("dns: %w khi đọc tên", errShortPacket)
		}
		l := int(msg[off])
		if l == 0 {
			off++
			break
		}
		if l&0xC0 != 0 {
			return "", 0, errors.New("dns: tên nén trong phần câu hỏi")
		}
		off++
		if off+l > len(msg) {
			return "", 0, fmt.Errorf("dns: %w trong nhãn", errShortPacket)
		}
		if sb.Len() > 0 {
			sb.WriteByte('.')
		}
		sb.Write(msg[off : off+l])
		off += l
	}

	// Hạ chữ thường ngay tại đây: CSDL có ràng buộc name = lower(name), và DNS vốn
	// không phân biệt hoa thường nên "ADS.example.com" phải gộp cùng một domain.
	return strings.ToLower(sb.String()), off, nil
}

// skipName bỏ qua một tên miền ở off và trả về vị trí ngay sau nó.
//
// Khác readQName ở chỗ chấp nhận con trỏ nén, vì phần trả lời dùng nén rất nhiều.
// Không cần đi theo con trỏ: chỉ cần biết tên kết thúc ở đâu để đọc các trường phía
// sau, mà một con trỏ luôn là hai byte cuối cùng của tên trên dây.
//
// Nhờ vậy hàm không bao giờ nhảy, nên gói dị dạng có con trỏ trỏ vòng lại chính nó
// cũng không tạo được vòng lặp vô hạn ở đây.
func skipName(msg []byte, off int) (int, error) {
	for {
		if off >= len(msg) {
			return 0, fmt.Errorf("dns: %w khi bỏ qua tên", errShortPacket)
		}
		l := int(msg[off])
		switch {
		case l == 0:
			return off + 1, nil
		case l&0xC0 == 0xC0:
			if off+2 > len(msg) {
				return 0, fmt.Errorf("dns: %w ở con trỏ nén", errShortPacket)
			}
			return off + 2, nil
		case l&0xC0 != 0:
			return 0, errors.New("dns: nhãn có hai bit cao không hợp lệ")
		default:
			// l ≥ 1 nên off luôn tăng: vòng lặp chắc chắn dừng.
			off += 1 + l
		}
	}
}

// parseQuestion đọc câu hỏi đầu tiên của một thông điệp DNS.
//
// Tự bóc thay vì dùng thư viện DNS đầy đủ: đây là đường nóng chạy vài nghìn gói mỗi
// giây, mà phần cần dùng chỉ là tên miền và loại bản ghi.
func parseQuestion(msg []byte) (name string, qtype uint16, err error) {
	if len(msg) < dnsHeaderLen {
		return "", 0, fmt.Errorf("dns: %w", errShortPacket)
	}
	// Bit QR nằm ở bit cao nhất của trường cờ. Bằng 1 nghĩa là câu trả lời.
	if msg[2]&0x80 != 0 {
		return "", 0, errNotDNSQuery
	}
	if qdcount := uint16(msg[4])<<8 | uint16(msg[5]); qdcount == 0 {
		return "", 0, errNotAQuestion
	}

	name, off, err := readQName(msg, dnsHeaderLen)
	if err != nil {
		return "", 0, err
	}
	if off+4 > len(msg) {
		return "", 0, fmt.Errorf("dns: %w ở QTYPE", errShortPacket)
	}
	return name, uint16(msg[off])<<8 | uint16(msg[off+1]), nil
}

// parseAnswer đọc tên được hỏi và các địa chỉ trong phần trả lời.
//
// Mọi địa chỉ A/AAAA đều quy về **tên trong phần câu hỏi**, không phải tên chủ của
// từng bản ghi. Đó là điều đúng cần làm: khi có chuỗi CNAME, bản ghi A mang tên
// đích của chuỗi, nhưng thứ client hỏi — và thứ nằm trong bảng domains — là tên ở
// phần câu hỏi. Quy về tên được hỏi khiến chuỗi CNAME được đi hết mà không phải
// dựng lại chuỗi.
//
// ttl là giá trị nhỏ nhất trong các bản ghi lấy được: nó cho biết ánh xạ này còn
// đúng trong bao lâu, thứ phân biệt một CDN xoay IP mỗi phút với một máy chủ cố định.
func parseAnswer(msg []byte) (name string, ips []netip.Addr, ttl uint32, err error) {
	if len(msg) < dnsHeaderLen {
		return "", nil, 0, fmt.Errorf("dns: %w", errShortPacket)
	}
	if msg[2]&0x80 == 0 {
		return "", nil, 0, errNotDNSAnswer
	}
	qdcount := uint16(msg[4])<<8 | uint16(msg[5])
	if qdcount == 0 {
		return "", nil, 0, errNotAQuestion
	}
	ancount := uint16(msg[6])<<8 | uint16(msg[7])
	if ancount == 0 {
		return "", nil, 0, errNoAnswerRecords
	}

	name, off, err := readQName(msg, dnsHeaderLen)
	if err != nil {
		return "", nil, 0, err
	}
	// Bỏ QTYPE và QCLASS của câu hỏi.
	off += 4
	// Chỉ đọc câu hỏi đầu tiên. qdcount > 1 gần như không tồn tại trong thực tế và
	// không có cách diễn giải thống nhất, nên bỏ qua gói đó an toàn hơn là đoán.
	if qdcount > 1 {
		return "", nil, 0, errors.New("dns: nhiều câu hỏi trong một thông điệp")
	}

	ttl = ^uint32(0)
	for range ancount {
		off, err = skipName(msg, off)
		if err != nil {
			return "", nil, 0, err
		}
		// TYPE(2) CLASS(2) TTL(4) RDLENGTH(2) = 10 byte.
		if off+10 > len(msg) {
			return "", nil, 0, fmt.Errorf("dns: %w ở header bản ghi", errShortPacket)
		}
		rrType := uint16(msg[off])<<8 | uint16(msg[off+1])
		rrClass := uint16(msg[off+2])<<8 | uint16(msg[off+3])
		rrTTL := uint32(msg[off+4])<<24 | uint32(msg[off+5])<<16 | uint32(msg[off+6])<<8 | uint32(msg[off+7])
		rdLength := int(msg[off+8])<<8 | int(msg[off+9])
		off += 10

		if off+rdLength > len(msg) {
			return "", nil, 0, fmt.Errorf("dns: %w ở RDATA", errShortPacket)
		}
		rdata := msg[off : off+rdLength]
		off += rdLength

		if rrClass != classIN {
			continue
		}
		var ip netip.Addr
		switch {
		case rrType == TypeA && rdLength == 4:
			ip = netip.AddrFrom4([4]byte(rdata))
		case rrType == TypeAAAA && rdLength == 16:
			ip = netip.AddrFrom16([16]byte(rdata)).Unmap()
		default:
			// CNAME, HTTPS, SOA… bỏ qua. Bản ghi A phía sau chuỗi CNAME vẫn được
			// lấy vì vòng lặp không dừng ở đây.
			continue
		}
		ips = append(ips, ip)
		ttl = min(ttl, rrTTL)
	}

	if len(ips) == 0 {
		return "", nil, 0, errNoAnswerRecords
	}
	return name, ips, ttl, nil
}

// wantedType cho biết loại bản ghi có được ghi nhận không.
func wantedType(t uint16) bool {
	return t == TypeA || t == TypeAAAA || t == TypeHTTPS
}
