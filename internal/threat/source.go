package threat

import "github.com/benji/dnsguard/internal/enrich"

// Format là cách đọc một file danh sách.
//
// Tồn tại vì hai nguồn đáng dùng nhất không cùng định dạng: Spamhaus phát hành dải
// CIDR mỗi dòng một mục, còn abuse.ch phát hành CSV có tiêu đề và cột địa chỉ kèm
// cổng. Gói lựa chọn này vào định nghĩa nguồn thay vì đoán theo đuôi file: đuôi file
// do URL quyết định, mà URL thì người vận hành sửa được.
type Format string

const (
	// FormatIPList là mỗi dòng một địa chỉ hoặc một dải CIDR. Chú thích bắt đầu
	// bằng "#" hoặc ";".
	FormatIPList Format = "iplist"
	// FormatThreatFoxCSV là CSV của abuse.ch ThreatFox, cột thứ ba dạng "ip:port".
	FormatThreatFoxCSV Format = "threatfox_csv"
)

// URL tải mặc định của từng nguồn.
const (
	// Spamhaus DROP liệt kê dải bị tổ chức tội phạm thuê hoặc chiếm đoạt. Chọn làm
	// nguồn dải mặc định vì đây là danh sách sạch nhất đo được: chỉ 0,29% số mục nằm
	// trong ASN hạ tầng dùng chung, so với 14–45% của các danh sách IP phổ biến khác.
	// Dải bị chiếm đoạt cũng không mục nát như IP thuê theo giờ ở nhà cung cấp VPS.
	//
	// Spamhaus báo bản .txt sẽ ngừng phục vụ trong tương lai, chuyển sang drop_v4.json,
	// và có thông báo trước. Khi đó phải thêm một Format mới.
	DefaultDROPURL = "https://www.spamhaus.org/drop/drop.txt"

	// ThreatFox liệt kê máy chủ điều khiển mã độc. Bổ sung chứ không chồng lấn với
	// DROP: DROP là *dải* hạ tầng rogue, ThreatFox là *địa chỉ* C2 cụ thể mà mã độc
	// gọi ra — đúng chiều mà DNSGuard quan sát được.
	//
	// Dùng bản đầy đủ chứ không phải bản "recent". Bản recent chỉ giữ vài ngày gần
	// nhất, mà job đối chiếu **xoá cảnh báo khi địa chỉ rời khỏi danh sách** — nên với
	// một nguồn xoay vòng nhanh, bằng chứng "domain này đã phân giải tới C2" sẽ tự
	// biến mất sau vài ngày dù lượt phân giải đã ghi nhận không hề thay đổi. Với một
	// công cụ điều tra thì đó là mất bằng chứng, không phải dọn nhiễu.
	DefaultThreatFoxURL = "https://threatfox.abuse.ch/export/csv/ip-port/full/"
)

// Definition mô tả một nguồn danh sách hạ tầng độc hại.
type Definition struct {
	Kind       string
	Label      string
	Describes  string
	DefaultURL string
	Format     Format
	// Dest là nơi lưu file tải về. Đến từ cấu hình, không phải từ nguồn.
	Dest string
}

// SpamhausDROP dựng nguồn dải hạ tầng rogue của Spamhaus.
//
// Khóa riêng chứ không dùng lại khóa "ipthreat" của nguồn cũ (Feodo Tracker, ngừng
// cập nhật từ 2026-03-04). Dùng lại khóa sẽ khiến file Feodo cũ còn trên đĩa được nạp
// lên dưới nhãn "Spamhaus DROP", và cột threat_source trong hồ sơ điều tra sẽ khai
// một xuất xứ sai. Khóa mới thì file cũ đơn giản là không được đọc nữa: màn Cài đặt
// báo "Chưa có" cho tới khi người vận hành bấm Cập nhật, và đó là sự thật.
func SpamhausDROP(dest string) *Set {
	return New(Definition{
		Kind:       "spamhaus_drop",
		Label:      "Spamhaus DROP",
		DefaultURL: DefaultDROPURL,
		Format:     FormatIPList,
		Dest:       dest,
		Describes: "Dải địa chỉ bị tổ chức tội phạm thuê hoặc chiếm đoạt. " +
			"Ít báo nhầm nhất trong các nguồn theo địa chỉ.",
	})
}

// ThreatFox dựng nguồn máy chủ điều khiển mã độc của abuse.ch.
func ThreatFox(dest string) *Set {
	return New(Definition{
		Kind:       "threatfox",
		Label:      "ThreatFox (C2 mã độc)",
		DefaultURL: DefaultThreatFoxURL,
		Format:     FormatThreatFoxCSV,
		Dest:       dest,
		Describes: "Địa chỉ máy chủ điều khiển mã độc. " +
			"Khớp ở đây nghĩa là có máy trong mạng gọi ra hạ tầng của kẻ tấn công.",
	})
}

// Status mô tả trạng thái nguồn, cho màn Cài đặt.
func (s *Set) Status() enrich.TableStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	st := enrich.TableStatus{
		Kind: s.def.Kind, Label: s.def.Label, Describes: s.def.Describes,
		DefaultURL: s.def.DefaultURL, Loaded: s.loaded,
		Entries: len(s.prefixes), Path: s.path,
	}
	if !s.loadedAt.IsZero() {
		st.LoadedAt = s.loadedAt.Format(timeLayout)
	}
	return st
}
