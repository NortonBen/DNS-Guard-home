package threat

import (
	"net/netip"
	"strings"
)

// tfIOCValueColumn là vị trí cột "ioc_value" trong CSV của ThreatFox.
//
// Hai cột trước nó là dấu thời gian và số hiệu IOC — cả hai đều không chứa dấu phẩy,
// nên tách thô theo dấu phẩy là an toàn tới đúng cột này. Các cột sau (bí danh mã độc,
// nhãn) thì có thể chứa dấu phẩy, và đó là lý do hàm này không đọc gì quá cột thứ ba.
const tfIOCValueColumn = 2

// parseThreatFoxLine đọc một dòng CSV của ThreatFox thành dải chứa đúng một địa chỉ.
//
// Định dạng mỗi dòng:
//
//	"2026-09-08 02:28:59", "1904097", "26.150.54.213:6606", "ip:port", "botnet_cc", ...
//
// Bỏ cổng đi chứ không giữ: DNSGuard đối chiếu địa chỉ trong bản ghi trả lời DNS, mà
// bản ghi DNS không mang cổng. Giữ cổng lại sẽ khiến không mục nào khớp được.
func parseThreatFoxLine(line string) (netip.Prefix, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return netip.Prefix{}, false
	}

	fields := strings.Split(line, ",")
	if len(fields) <= tfIOCValueColumn {
		return netip.Prefix{}, false
	}

	value := strings.Trim(strings.TrimSpace(fields[tfIOCValueColumn]), `"`)
	return parseField(stripPort(value))
}

// stripPort bỏ phần cổng khỏi một mục "ip:port", giữ nguyên chuỗi khi không có cổng.
//
// Ba dạng phải phân biệt được, vì đoán sai thì hoặc mất mục hoặc nạp rác:
//   - "[2001:db8::1]:443" — IPv6 có cổng, địa chỉ nằm trong ngoặc vuông
//   - "2001:db8::1"       — IPv6 trần, mọi dấu hai chấm đều thuộc địa chỉ
//   - "1.2.3.4:8080"      — IPv4 có cổng, đúng một dấu hai chấm
func stripPort(value string) string {
	if after, ok := strings.CutPrefix(value, "["); ok {
		if host, _, found := strings.Cut(after, "]"); found {
			return host
		}
		return after
	}
	// Nhiều hơn một dấu hai chấm nghĩa là IPv6 trần: không có gì để cắt.
	if strings.Count(value, ":") != 1 {
		return value
	}
	host, _, _ := strings.Cut(value, ":")
	return host
}
