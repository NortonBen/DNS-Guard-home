package ingest

import (
	"errors"
	"fmt"
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

var errNotAQuestion = errors.New("thông điệp DNS không có câu hỏi")

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

// parseQuestion đọc câu hỏi đầu tiên của một thông điệp DNS.
//
// Tự bóc thay vì dùng thư viện DNS đầy đủ: đây là đường nóng chạy vài nghìn gói mỗi
// giây, mà phần cần dùng chỉ là tên miền và loại bản ghi. Phần câu hỏi không bao giờ
// dùng nén tên, nên việc bóc tách gọn trong vài chục dòng.
func parseQuestion(msg []byte) (name string, qtype uint16, err error) {
	if len(msg) < dnsHeaderLen {
		return "", 0, fmt.Errorf("DNS: %w", errShortPacket)
	}
	// Bit QR nằm ở bit cao nhất của trường cờ. Bằng 1 nghĩa là câu trả lời.
	if msg[2]&0x80 != 0 {
		return "", 0, errNotDNSQuery
	}
	if qdcount := uint16(msg[4])<<8 | uint16(msg[5]); qdcount == 0 {
		return "", 0, errNotAQuestion
	}

	var sb strings.Builder
	sb.Grow(64)

	i := dnsHeaderLen
	for {
		if i >= len(msg) {
			return "", 0, fmt.Errorf("DNS: %w khi đọc tên", errShortPacket)
		}
		l := int(msg[i])
		if l == 0 {
			i++
			break
		}
		// Hai bit cao bật nghĩa là con trỏ nén — không hợp lệ trong phần câu hỏi.
		if l&0xC0 != 0 {
			return "", 0, errors.New("DNS: tên nén trong phần câu hỏi")
		}
		i++
		if i+l > len(msg) {
			return "", 0, fmt.Errorf("DNS: %w trong nhãn", errShortPacket)
		}
		if sb.Len() > 0 {
			sb.WriteByte('.')
		}
		sb.Write(msg[i : i+l])
		i += l
	}

	if i+4 > len(msg) {
		return "", 0, fmt.Errorf("DNS: %w ở QTYPE", errShortPacket)
	}
	qtype = uint16(msg[i])<<8 | uint16(msg[i+1])

	// Hạ chữ thường ngay tại đây: CSDL có ràng buộc name = lower(name), và DNS vốn
	// không phân biệt hoa thường nên "ADS.example.com" phải gộp cùng một domain.
	return strings.ToLower(sb.String()), qtype, nil
}

// wantedType cho biết loại bản ghi có được ghi nhận không.
func wantedType(t uint16) bool {
	return t == TypeA || t == TypeAAAA || t == TypeHTTPS
}
