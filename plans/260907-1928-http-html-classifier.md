# Phân tích HTTP header + HTML tĩnh để phân loại domain

Trạng thái: thiết kế · Ngày 2026-09-07

Bốn yêu cầu và nơi giải quyết:

| # | Yêu cầu | Mục |
|---|---|---|
| 1 | Phân tích header và HTML tĩnh để phân loại | §1, §2 |
| 2 | Bộ rule cho việc phân tích | §3 |
| 3 | Một số domain check VirusTotal để xác thực thêm | §4 |
| 4 | Cache: nhớ đã check chưa, không check lại ngay, bao lâu thì check lại | §5 |

---

## 1. Phạm vi và nguyên tắc

Thêm hai nguồn làm giàu vào kiến trúc `Enricher` sẵn có:

- **`http`** — tải trang gốc của domain, đọc header phản hồi và HTML **tĩnh**.
- **`vt`** — tra VirusTotal cho một số ít domain đã có dấu hiệu đáng ngờ.

Bốn ràng buộc quyết định mọi thứ bên dưới:

**Không chạy JavaScript.** Không headless browser. Chỉ tải một lần rồi đọc chuỗi.
Hệ thống phải giữ nguyên hình dạng một binary tĩnh chạy được trên Raspberry Pi.

**Tải một trang là để lộ.** Máy chủ đích biết được rằng mạng này đang soi nó. Vì vậy
chỉ tải những domain thật sự cần, dùng User-Agent trung thực, và tắt được bằng
`DNSGUARD_EXTERNAL_ENABLED=false` như mọi nguồn ngoài khác.

**Precision quan trọng hơn recall.** Không tín hiệu HTTP nào một mình được phép đẩy
domain vượt ngưỡng `ads` (5,5). Chúng là bằng chứng bổ trợ cho tín hiệu hạ tầng
(CNAME, ASN) chứ không thay thế.

**Phân biệt "trang này bán quảng cáo" với "domain này LÀ hạ tầng quảng cáo".** Một
tờ báo nhúng doubleclick vẫn là `content`. Đây là cạm bẫy lớn nhất của cả tính năng:
phần lớn tín hiệu HTML trực quan chỉ nói lên cách trang kiếm tiền, không nói lên
danh tính của domain. Bộ rule dưới đây cố ý bỏ qua chúng.

## 2. Chính sách tải

| Mục | Giá trị | Lý do |
|---|---|---|
| URL | `https://<domain>/`, hỏng TLS thì thử `http://` | Trang gốc là thứ duy nhất đoán được |
| Timeout | 8 giây toàn bộ | Domain chết không được giữ worker |
| Chuyển hướng | tối đa 3, có ghi lại đích | Chuyển hướng tới adtech là bằng chứng |
| Giới hạn thân | 256 KB sau giải nén | Đủ cho `<head>` và đầu `<body>` |
| User-Agent | `DNSGuard/1.0 (+phân tích blocklist)` | Trung thực; không giả làm trình duyệt |
| Cookie | không lưu, không gửi lại | Không tạo phiên với máy chủ đích |
| Tài nguyên con | **không tải** | Chỉ đọc thuộc tính `src`, không lấy nội dung |
| Gửi đi | chỉ tên miền | Không bao giờ có IP client, không Referer |

**Chặn địa chỉ nội bộ.** Trước khi tải, phân giải domain và **từ chối nếu địa chỉ
nằm trong dải riêng tư, loopback, link-local hoặc CGNAT**. Không có bước này, một
domain độc hại chỉ cần trỏ A record về `192.168.88.1` là biến DNSGuard thành công cụ
gọi vào trang quản trị của chính router — máy chủ tự tấn công mạng của mình. Đây là
lỗ hổng SSRF kinh điển và là rủi ro an ninh lớn nhất của tính năng này.

## 3. Bộ rule

Trọng số cùng thang với bộ tín hiệu hiện có (`cname_adtech` +6,0 là bằng chứng gần
như chắc chắn; ngưỡng `ads` là 5,5).

### 3.1 Tín hiệu từ header

| kind | trọng số | điều kiện | ghi vào detail |
|---|---|---|---|
| `http_beacon` | +4,0 | Trang gốc trả 204, **hoặc** ảnh ≤ 100 byte (GIF/PNG 1×1), **hoặc** 200 với thân rỗng và content-type không phải HTML | `status`, `content_type`, `length` |
| `http_p3p` | +2,5 | Có header `P3P` | `p3p` |
| `http_tracking_cookie` | +2,0 | `Set-Cookie` có `SameSite=None; Secure` và sống ≥ 90 ngày | `cookie_name`, `max_age_days` |
| `http_cors_wildcard` | +1,0 | `Access-Control-Allow-Origin: *` **và** content-type không phải HTML | `content_type` |

`http_beacon` là tín hiệu mạnh nhất nhóm này. Một hostname mà trình duyệt đã phân
giải nhưng không trả về trang nào, chỉ trả một pixel hoặc 204, thì không phải nơi
người ta ghé thăm — nó là điểm thu thập. Vẫn để +4,0 chứ không cao hơn: endpoint
kiểm tra sức khỏe và một số CDN cũng hành xử như vậy.

`http_p3p` rẻ mà phân biệt tốt. P3P là chuẩn đã chết, ngày nay gần như chỉ còn các
mạng quảng cáo giữ lại để lách chính sách cookie của IE cũ.

### 3.2 Tín hiệu từ HTML tĩnh

| kind | trọng số | điều kiện | ghi vào detail |
|---|---|---|---|
| `http_parking` | +3,0 | HTML khớp dấu vân tay nhà cung cấp parking (sedoparking, parkingcrew, bodis, afternic, dan.com, above.com, uniregistry) | `provider` |
| `http_redirect_adtech` | +4,0 | Chuyển hướng tới host thuộc eTLD+1 adtech đã biết | `location`, `matched` |
| `http_empty_page` | +1,0 | 200 text/html nhưng < 200 ký tự văn bản hiển thị và không có `<title>` | `text_len` |

**Cố ý không có** tín hiệu đếm số script bên thứ ba, cũng không có dấu vân tay
`gtag(` / `fbq(` / `adsbygoogle`. Chúng cho biết trang *kiếm tiền bằng quảng cáo*,
không cho biết *domain là máy chủ quảng cáo* — và mọi tờ báo đều dính. Đây là loại
tín hiệu trực quan nhưng phá precision.

### 3.3 Tín hiệu âm

| kind | trọng số | điều kiện |
|---|---|---|
| `http_real_site` | −2,0 | 200 text/html, có `<title>`, và ≥ 1500 ký tự văn bản hiển thị |

Một trang thật, có tiêu đề, có nội dung để đọc, gần như luôn là `content`. Giữ ở
−2,0 chứ không mạnh hơn: trang giới thiệu của chính công ty adtech cũng là trang
thật, và ở đó tín hiệu CNAME/ASN phải thắng.

### 3.4 Gán nhãn

Không đổi thứ tự `categorize()` hiện có. Chỉ bổ sung:

- `http_beacon` là tín hiệu dương mạnh nhất → gợi ý `telemetry` (cùng nhóm với
  `beacon` hành vi sẵn có)
- `http_parking`, `http_redirect_adtech` → gợi ý `ads`
- `vt_malicious` → `malware`, chen vào ngay sau quy tắc danh sách công khai

## 4. VirusTotal

**Endpoint** `GET https://www.virustotal.com/api/v3/domains/{domain}`, xác thực bằng
header `x-apikey`. Đọc `data.attributes.last_analysis_stats`.

| kind | trọng số | điều kiện |
|---|---|---|
| `vt_malicious` | +4,0 | ≥ 3 engine báo `malicious` |
| `vt_clean` | −1,0 | 0 engine báo malicious và ≥ 60 engine đã chấm |

Ngưỡng **3 engine** chứ không phải 1: một engine đơn lẻ báo động là nhiễu nổi tiếng
của VirusTotal. Ba engine độc lập đồng ý mới là bằng chứng.

+4,0 vượt ngưỡng `malware` (3,0), nghĩa là VirusTotal một mình đủ để đưa domain vào
hàng chờ với nhãn malware. Đó là chủ ý — ngưỡng malware được đặt thấp chính vì loại
bằng chứng này.

**Quota.** Bậc miễn phí khoảng 4 yêu cầu/phút và 500/ngày. Giới hạn tốc độ đặt 3/phút
để chừa biên. Cổng lọc bên dưới giữ lượng gọi ở mức vài chục/ngày với một mạng gia
đình, còn xa mức 500.

**Chỉ gọi khi** domain đã đạt điểm ≥ 3,0 từ các tín hiệu khác. Không tiêu quota cho
domain chưa có gì đáng ngờ.

## 5. Cache và lịch check lại

Đây là yêu cầu số 4, và kiến trúc sẵn có đã đáp ứng gần hết: bảng `domain_facts`
khóa chính `(domain_id, source)` với `fetched_at` / `expires_at` / `error`, còn
`EnrichCandidates` chỉ chọn dòng `expires_at <= now`. Nghĩa là **đã check thì có
dòng, có dòng thì không check lại cho tới khi hết hạn** — kể cả khi domain được truy
vấn lại liên tục trong lúc đó.

Phần phải thêm là TTL riêng cho từng kết cục:

| nguồn | kết cục | TTL | lý do |
|---|---|---|---|
| `http` | phân tích được | 14 ngày | nội dung trang đổi chậm |
| `http` | trang parking | 30 ngày | domain đã park thì park lâu |
| `http` | DNS hỏng / từ chối kết nối | 7 ngày | host chết thì cứ chết, đừng thử lại mỗi giờ |
| `http` | hết thời gian chờ | 2 ngày | có thể là tạm thời |
| `http` | lỗi TLS | 7 ngày | cấu hình hiếm khi đổi |
| `http` | HTTP 4xx/5xx | 3 ngày | |
| `http` | địa chỉ nội bộ, bị từ chối | 90 ngày | quyết định về an toàn, không đổi theo thời gian |
| `vt` | tra được | 30 ngày | kết luận đổi chậm |
| `vt` | VirusTotal chưa biết domain | 7 ngày | có thể được lập chỉ mục sau |
| `vt` | hết quota | 1 giờ | thử lại trong ngày |

**Cổng lọc — domain nào được tải:**

1. `status` thuộc `new` hoặc `staging` — không bao giờ đụng vào `allowed` / `ignored`
2. `query_count ≥ 5` — có lưu lượng thật, không phải một lần gõ nhầm
3. **không** nằm trong danh sách bảo vệ cứng hoặc mềm — không bao giờ tải trang ngân
   hàng hay cổng thanh toán
4. điểm hiện tại **không** ≤ −4,0 — domain đã được `high_rank` bảo vệ mạnh thì tra
   thêm cũng không đổi kết luận, chỉ tốn một lần lộ diện
5. `DNSGUARD_EXTERNAL_ENABLED=true`

Với `vt`, cộng thêm điều kiện điểm ≥ 3,0 ở §4.

## 6. Cố ý không làm

- Không chạy JavaScript, không headless browser
- Không tải tài nguyên con, không thu thập nhiều trang
- Không POST, không gửi form, không đăng nhập
- Không đếm script bên thứ ba (xem §3.2)
- Không tải trang của domain được bảo vệ

## 7. Rủi ro

| # | Rủi ro | Giảm thiểu |
|---|---|---|
| 1 | **SSRF** — domain trỏ về IP nội bộ, DNSGuard gọi vào trang quản trị router | Từ chối mọi địa chỉ riêng tư/loopback/link-local/CGNAT trước khi tải |
| 2 | Thêm 8 tín hiệu làm đổi điểm mọi domain | Dùng bảng xem trước tác động sẵn có trước khi bật; hàm chấm điểm thuần túy nên con số khớp tuyệt đối |
| 3 | Lộ việc mạng đang soi domain | Cổng lọc hẹp, User-Agent trung thực, tắt được bằng một biến |
| 4 | Thân phản hồi khổng lồ hoặc bom giải nén | Giới hạn 256 KB **sau** giải nén |
| 5 | Cạn quota VirusTotal | Cổng lọc theo điểm, 3 req/phút, TTL 30 ngày |
| 6 | Máy chủ đích trả nội dung khác cho bot | Chấp nhận: đổi lại là không giả mạo User-Agent |

## 8. Việc phải làm

1. Migration `0003`: thêm `http`, `vt` vào ràng buộc `CHECK` của `domain_facts`.
   SQLite không sửa được `CHECK` tại chỗ — phải tạo bảng mới, chép dữ liệu, xóa bảng
   cũ, đổi tên.
2. `internal/enrich/http.go` — bộ tải, chặn IP nội bộ, bóc header và HTML.
3. `internal/enrich/virustotal.go` — tra VT, đọc `last_analysis_stats`.
4. `internal/classify/` — 8 loại tín hiệu mới, trọng số, và các trường trong `Facts`.
5. `internal/store/facts.go` — TTL theo kết cục thay vì chỉ theo nguồn.
6. `internal/store/scoring.go` — `mergeFact` cho `http` và `vt`.
7. `internal/worker` — cổng lọc ở §5.
8. `internal/config` — `DNSGUARD_VT_API_KEY`, `DNSGUARD_HTTP_ANALYSIS_ENABLED`.
9. Test bảng cho từng tín hiệu mới; test riêng cho việc chặn IP nội bộ.
