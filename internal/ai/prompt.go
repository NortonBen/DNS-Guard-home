package ai

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/benji/dnsguard/internal/classify"
)

// Evidence là những gì DNSGuard đã biết về một domain, gói lại để hỏi model.
//
// Cố ý mỏng: chỉ những dữ kiện mà model không tự suy ra được từ cái tên. Nhồi
// thêm trường vào đây là nhồi thêm token vào mỗi dòng của mọi lô, nên mỗi trường
// phải trả lời được câu "nếu bỏ nó đi thì kết luận có đổi không".
type Evidence struct {
	Name  string
	ETLD1 string

	QueryCount  int64
	ClientCount int

	CNAME  string
	ASNOrg string

	// InPublicList và ETLD1InPublicList nói rằng blocklist công khai đã có sẵn kết
	// luận. Vẫn gửi đi: model cần biết để không mâu thuẫn với thứ hệ thống đã chắc.
	InPublicList      bool
	ETLD1InPublicList bool

	HTTPTitle   string
	HTTPParking string
	// HTTPEmpty đúng khi trang gốc gần như không có chữ — dấu hiệu của endpoint
	// thu thập chứ không phải trang để đọc.
	HTTPEmpty bool

	VTMalicious int
	AgeDays     int
	TrancoRank  int
}

// EvidenceFrom rút gọn dữ liệu chấm điểm thành phần gửi cho model.
func EvidenceFrom(d classify.Domain, f classify.Facts) Evidence {
	e := Evidence{
		Name:              d.Name,
		ETLD1:             d.ETLD1,
		QueryCount:        d.QueryCount,
		ClientCount:       d.ClientCount,
		ASNOrg:            f.ASNOrg,
		InPublicList:      f.InPublicList,
		ETLD1InPublicList: f.ETLD1InPublicList,
		HTTPTitle:         f.HTTP.Title,
		HTTPParking:       f.HTTP.Parking,
		AgeDays:           f.AgeDays,
		TrancoRank:        f.TrancoRank,
	}
	// Chỉ mắt xích cuối: đó là nơi domain thật sự trỏ tới, và các mắt trung gian
	// hiếm khi đổi kết luận nhưng luôn tốn token.
	if n := len(f.CNAMEChain); n > 0 {
		e.CNAME = f.CNAMEChain[n-1]
	}
	if f.HTTP.Fetched {
		e.HTTPEmpty = f.HTTP.IsPixel || f.HTTP.TextLen < 200
	}
	if f.VT.Checked && f.VT.Known {
		e.VTMalicious = f.VT.Malicious
	}
	return e
}

// systemPrompt đặt luật chơi cho lượt phân loại lô.
//
// Ba điều quan trọng nhất trong prompt này: định dạng ra là CSV thuần, mỗi domain
// đầu vào đúng một dòng ra, và "không chắc thì trả content". Điều thứ ba là chốt
// an toàn — thiếu nó, model sẽ đoán bừa một nhãn adtech cho mọi tên miền lạ, và
// tín hiệu AI biến thành nguồn dương tính giả thay vì nguồn bằng chứng.
const systemPrompt = `Bạn phân loại tên miền cho một hệ quản trị blocklist DNS trong mạng gia đình.

Với mỗi domain được cho, chọn ĐÚNG MỘT nhãn:
ads          hạ tầng quảng cáo: sàn đấu giá, mạng phân phối quảng cáo
tracking     thu thập hành vi người dùng xuyên trang, analytics bên thứ ba
telemetry    ứng dụng/thiết bị gửi số liệu về nhà sản xuất
malware      phát tán mã độc, lừa đảo, chỉ huy botnet
cryptomining đào tiền mã hoá trong trình duyệt
adult        nội dung người lớn
cdn          hạ tầng phân phối nội dung dùng chung, không theo dõi
content      trang bình thường, hoặc không đủ căn cứ kết luận

QUY TẮC BẮT BUỘC:
1. Trả về CSV thuần. Không rào code, không tiêu đề, không lời dẫn, không giải thích ngoài CSV.
2. Mỗi domain đầu vào đúng một dòng ra. Không thêm domain nào không có trong danh sách.
3. Mỗi dòng: domain,category,confidence,reason
   - confidence là số thập phân trong khoảng 0 đến 1
   - reason tối đa 12 từ, tiếng Việt, không chứa dấu phẩy
4. KHÔNG CHẮC THÌ TRẢ content với confidence thấp. Chặn nhầm một domain thật gây
   hỏng dịch vụ cho cả nhà; bỏ sót một domain quảng cáo thì không.
5. Domain của ngân hàng, cơ quan nhà nước, y tế, giáo dục, email, họp trực tuyến,
   cập nhật hệ điều hành: luôn là content.

Ví dụ định dạng đầu ra:
doubleclick.net,ads,0.97,sàn quảng cáo của Google
cdn.jsdelivr.net,cdn,0.9,mạng phân phối thư viện mã nguồn mở
vietcombank.com.vn,content,0.95,ngân hàng`

// buildPrompt dựng phần người dùng của một lô.
//
// Mỗi domain một dòng, các dữ kiện ngăn bằng dấu gạch đứng và BỎ QUA khi rỗng.
// Bỏ trường rỗng thay vì ghi "cname=" tiết kiệm khoảng một phần ba độ dài lô trên
// dữ liệu thật, vì đa số domain chỉ có vài dữ kiện.
func buildPrompt(items []Evidence) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Phân loại %d domain sau. Trả về đúng %d dòng CSV.\n\n",
		len(items), len(items))

	for _, e := range items {
		b.WriteString(e.Name)
		for _, part := range evidenceParts(e) {
			b.WriteString(" | ")
			b.WriteString(part)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// evidenceParts liệt kê các dữ kiện có giá trị của một domain.
func evidenceParts(e Evidence) []string {
	var parts []string
	add := func(format string, args ...any) {
		parts = append(parts, fmt.Sprintf(format, args...))
	}

	if e.CNAME != "" {
		add("cname=%s", e.CNAME)
	}
	if e.ASNOrg != "" {
		add("asn=%s", clip(e.ASNOrg, 40))
	}
	if e.QueryCount > 0 {
		add("truy_van=%d", e.QueryCount)
	}
	if e.ClientCount > 0 {
		add("thiet_bi=%d", e.ClientCount)
	}
	if e.InPublicList {
		add("co_trong_blocklist")
	} else if e.ETLD1InPublicList {
		add("ten_mien_goc_trong_blocklist")
	}
	if e.TrancoRank > 0 {
		add("hang_tranco=%d", e.TrancoRank)
	}
	if e.AgeDays > 0 && e.AgeDays < 180 {
		// Tuổi chỉ đáng gửi khi domain còn mới: một domain mười năm tuổi không nói
		// lên điều gì, còn một domain hai tuần tuổi thì có.
		add("tuoi_ngay=%d", e.AgeDays)
	}
	if e.HTTPParking != "" {
		add("trang_do=%s", e.HTTPParking)
	}
	if e.HTTPEmpty {
		add("trang_rong")
	}
	if t := strings.TrimSpace(e.HTTPTitle); t != "" {
		add("tieu_de=%s", clip(sanitize(t), 60))
	}
	if e.VTMalicious > 0 {
		add("virustotal_doc_hai=%d", e.VTMalicious)
	}
	return parts
}

// sanitize bỏ ký tự làm hỏng khuôn một dòng một domain.
//
// Tiêu đề trang là văn bản do bên thứ ba viết. Một tiêu đề chứa xuống dòng hoặc
// gạch đứng sẽ làm model hiểu sai ranh giới giữa các domain — và một tiêu đề cố
// ý chứa câu lệnh là một mũi tiêm prompt. Cắt ký tự cấu trúc chặn cả hai.
func sanitize(s string) string {
	s = strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t', '|':
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// AskSystemPrompt đặt vai cho phần hỏi đáp.
//
// Khác hẳn systemPrompt: ở đây model được phép nói dài, nhưng phải dựa vào công
// cụ thay vì trí nhớ. Câu "không tra được thì nói không biết" là quan trọng nhất —
// một câu trả lời tự tin về domain không tồn tại trong mạng còn tệ hơn im lặng.
const AskSystemPrompt = `Bạn là trợ lý vận hành của DNSGuard — hệ quản trị blocklist DNS lấy domain làm trung tâm.

DNSGuard nhận bản sao lưu lượng DNS của mạng, chấm điểm và phân loại domain, rồi
xuất file blocklist cho thiết bị mạng tải về. Nó KHÔNG tự chặn; việc chặn do
router đảm nhiệm.

Điểm càng cao càng đáng ngờ. Ngưỡng chặn khác nhau theo từng phân loại và người vận
hành sửa được, nên hãy tra bằng công cụ thay vì đoán một con số. Trạng thái domain đi
theo vòng đời: new → staging → blocked, hoặc allowed khi người vận hành cho qua.

CÁCH LÀM VIỆC:
- Luôn dùng công cụ để lấy dữ liệu thật trước khi kết luận. Đừng đoán từ tên miền.
- Không tra được thì nói thẳng là chưa có dữ liệu, đừng suy diễn.
- Trả lời bằng tiếng Việt, ngắn gọn, kết luận trước rồi mới tới căn cứ.
- Khi khuyên chặn hay bỏ chặn, nêu rõ bằng chứng và hệ quả nếu làm sai.
- Bạn không có quyền đổi trạng thái domain. Việc chặn hay bỏ chặn do người vận
  hành bấm trên giao diện; bạn chỉ đề xuất.`

// formatFloat in số gọn, bỏ số 0 thừa ở đuôi.
func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}
