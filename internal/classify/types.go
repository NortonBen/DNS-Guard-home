// Package classify chấm điểm và gán nhãn cho domain.
//
// Toàn bộ package này thuần túy: không I/O, không CSDL, không đọc đồng hồ hệ thống.
// Đó là ràng buộc thiết kế chứ không phải phong cách (docs/06-classification.md §3).
// Nhờ nó, chấm điểm lại toàn bộ CSDL sau khi đổi trọng số không cần tra lại dịch vụ
// ngoài, và bảng xem trước tác động (dry_run) chính xác tuyệt đối vì cùng đầu vào
// luôn cho cùng đầu ra.
package classify

// Category là nhãn phân loại. Tám giá trị ở docs/06-classification.md §1.
type Category string

const (
	CategoryAds          Category = "ads"
	CategoryTracking     Category = "tracking"
	CategoryTelemetry    Category = "telemetry"
	CategoryMalware      Category = "malware"
	CategoryCryptomining Category = "cryptomining"
	CategoryAdult        Category = "adult"
	CategoryCDN          Category = "cdn"
	CategoryContent      Category = "content"
)

// AllCategories liệt kê theo đúng thứ tự hiển thị.
var AllCategories = []Category{
	CategoryAds, CategoryTracking, CategoryTelemetry, CategoryMalware,
	CategoryCryptomining, CategoryAdult, CategoryCDN, CategoryContent,
}

// Domain là những gì quan sát được từ chính mạng: tên miền và hành vi truy vấn.
// Không chứa dữ liệu tra cứu bên ngoài — thứ đó nằm ở Facts.
type Domain struct {
	Name  string
	ETLD1 string

	QueryCount  int64
	ClientCount int

	// SubdomainCount là số subdomain phân biệt dưới cùng eTLD+1 đã thấy trong mạng.
	SubdomainCount int

	// ThirdPartyRatio là tỉ lệ lần xuất hiện nằm trong vòng 3 giây sau một domain
	// khác gốc. Nằm trong [0,1]; bằng 0 khi chưa đủ dữ liệu.
	ThirdPartyRatio float64

	// QueryIntervalCV là hệ số biến thiên của khoảng cách giữa các truy vấn liên
	// tiếp. Phần mềm báo cáo định kỳ cho giá trị thấp; con người cho giá trị cao.
	QueryIntervalCV float64
}

// Facts là kết quả làm giàu từ bên ngoài. Trường rỗng nghĩa là chưa tra được, và
// tín hiệu tương ứng đơn giản là không kích hoạt — thiếu dữ liệu không bao giờ tự
// nó trở thành bằng chứng buộc tội.
type Facts struct {
	// CNAMEChain là toàn bộ mắt xích, không kể chính domain gốc.
	CNAMEChain []string
	IPs        []string

	ASN        int
	ASNOrg     string
	ASNCountry string

	CertIssuer string
	CertSANs   []string

	RegisteredAt string // RFC3339, rỗng nếu chưa tra được
	AgeDays      int

	// TrancoRank bằng 0 nghĩa là không xếp hạng.
	TrancoRank int

	// InPublicList cho biết chính domain này có trong blocklist công khai nào đó.
	InPublicList bool
	// ETLD1InPublicList cho biết eTLD+1 của nó có trong blocklist công khai.
	ETLD1InPublicList bool
	// CNAMEETLD1InPublicList là eTLD+1 của đích CNAME nếu đích đó nằm trong
	// blocklist công khai; rỗng nghĩa là không.
	CNAMEETLD1InPublicList string
	// PublicListCategories là các phân loại của những nguồn đã chứa domain này,
	// dùng cho các quy tắc gán nhãn malware / cryptomining / adult.
	PublicListCategories []Category

	// HTTP là kết quả phân tích header và HTML tĩnh của trang gốc. Chỉ có khi đã tải
	// được; Fetched bằng false nghĩa là chưa tra hoặc tra hỏng.
	HTTP HTTPAnalysis

	// VT là kết quả tra VirusTotal. Chỉ tra cho domain đã đáng ngờ.
	VT VirusTotalResult
}

// HTTPAnalysis là những gì đọc được từ một lần tải trang gốc.
//
// Thuần dữ liệu, không có phương thức và không biết gì về mạng: nhờ vậy hàm chấm
// điểm vẫn thuần túy và test được bằng bảng.
type HTTPAnalysis struct {
	Fetched     bool
	Status      int
	ContentType string
	BodyLen     int
	IsHTML      bool
	IsPixel     bool

	RedirectTo string

	P3P            string
	CORS           string
	TrackingCookie string
	CookieMaxDays  int

	Title   string
	TextLen int
	Parking string
}

// VirusTotalResult là kết luận tổng hợp của các engine trên VirusTotal.
type VirusTotalResult struct {
	Checked    bool
	Known      bool
	Malicious  int
	Suspicious int
	Harmless   int
	Undetected int
}

// Signal là một mẩu bằng chứng đã kích hoạt, kèm trọng số tại thời điểm chấm điểm.
type Signal struct {
	Kind   string         `json:"kind"`
	Weight float64        `json:"weight"`
	Detail map[string]any `json:"detail,omitempty"`
}

// Result là toàn bộ đầu ra của một lần chấm điểm.
type Result struct {
	Score      float64
	Signals    []Signal
	Category   Category
	Confidence float64
}

// HasSignal cho biết một loại tín hiệu có trong kết quả không.
func (r Result) HasSignal(kind string) bool {
	for _, s := range r.Signals {
		if s.Kind == kind {
			return true
		}
	}
	return false
}

// SignalKinds trả về danh sách loại tín hiệu theo đúng thứ tự đã sinh.
func (r Result) SignalKinds() []string {
	kinds := make([]string, len(r.Signals))
	for i, s := range r.Signals {
		kinds[i] = s.Kind
	}
	return kinds
}
