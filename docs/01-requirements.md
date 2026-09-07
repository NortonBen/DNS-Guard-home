# 01 — Yêu cầu

## 1. Vấn đề

Việc chặn quảng cáo ở tầng DNS trong mạng gia đình hoặc văn phòng nhỏ hiện có hai
lựa chọn, và cả hai đều thiếu.

**Dùng blocklist công khai.** Nhanh, nhưng danh sách như Hagezi hay OISD chỉ chứa
domain mà cộng đồng quốc tế đã phát hiện. Quảng cáo trên các trang tiếng Việt, các
ứng dụng nội địa, hoặc hạ tầng tracking mới dựng thường không có trong đó. Người
dùng không có cách nào bổ sung một cách hệ thống.

**Dùng Pi-hole / AdGuard Home.** Có giao diện và thống kê, nhưng chúng là *resolver*:
phải nằm trên đường truy vấn, và giao diện của chúng xoay quanh câu hỏi "hệ thống
đang chạy thế nào", chứ không phải "domain này là gì và có nên chặn không". Chúng
không phân loại domain, không tra cứu domain liên quan, không lưu lý do của quyết
định chặn.

Kết quả là người quản trị làm việc mù: thấy một domain lạ trong log, không biết nó
thuộc về ai, phục vụ gì, có bao nhiêu anh em cùng cụm, và nếu chặn thì hỏng cái gì.

## 2. Mục tiêu

DNSGuard tồn tại để trả lời bốn câu hỏi về mỗi domain xuất hiện trong mạng:

1. **Đây là gì?** — phân loại: quảng cáo, theo dõi, telemetry, độc hại, CDN, nội dung
2. **Có liên quan tới cái gì?** — CNAME, cùng ASN, cùng chứng chỉ, cùng xuất hiện
3. **Có nên chặn không?** — điểm số kèm bằng chứng cụ thể, không phải hộp đen
4. **Ai đã quyết định gì, khi nào?** — nhật ký bất biến, có thể truy vết ngược

Và biến câu trả lời thành các file blocklist mà thiết bị mạng tải về được.

## 3. Ngoài phạm vi

Những thứ DNSGuard **không** làm, và không nên làm:

| Không làm | Lý do |
|---|---|
| Phân giải DNS | Đã có MikroTik/Unbound làm tốt hơn |
| Chặn truy vấn | Việc của thiết bị mạng, DNSGuard chỉ cung cấp danh sách |
| Phân giải DNS đệ quy | Không cạnh tranh với resolver của thiết bị mạng |
| Lọc theo nội dung trang | Ngoài tầng DNS |
| Quản lý DHCP, zone, bản ghi DNS | Technitium làm tốt việc này, không cạnh tranh |
| Chặn quảng cáo trong app / YouTube | Bất khả thi ở tầng DNS |
| Đa tenant, SaaS | Thiết kế cho một mạng, một tổ chức |

## 4. Người dùng

**Quản trị mạng (chính).** Kỹ thuật, tự vận hành hạ tầng. Muốn kiểm soát chi tiết và
hiểu vì sao hệ thống ra quyết định đó. Dùng bàn phím nhiều hơn chuột. Sẽ mở công cụ
này vài lần một tuần, mỗi lần vài phút, chủ yếu để duyệt hàng đợi ứng viên.

**Người dùng trong mạng (phụ, chỉ đọc).** Không kỹ thuật. Vào khi một trang bị hỏng,
cần tự tra xem domain nào bị chặn và gửi yêu cầu mở. Không có quyền thay đổi gì.

## 5. Yêu cầu chức năng

Ký hiệu: **P0** bắt buộc cho bản dùng được · **P1** cần cho bản hoàn chỉnh ·
**P2** tốt nếu có.

### FR-1 Thu thập dữ liệu

| ID | Yêu cầu | Ưu tiên |
|---|---|---|
| FR-1.1 | Nhận trực tiếp luồng mirror TZSP trên cổng UDP cấu hình được | P0 |
| FR-1.2 | Bóc TZSP → Ethernet (kể cả VLAN/QinQ) → IPv4/IPv6 → UDP → DNS, không phụ thuộc thư viện native | P0 |
| FR-1.3 | Ghi nhận mỗi truy vấn: domain, loại bản ghi, IP client thật, thời điểm | P0 |
| FR-1.4 | Tổng hợp theo domain và theo khung giờ để phục vụ biểu đồ mà không quét lại log thô | P0 |
| FR-1.5 | Chỉ nhận truy vấn (cổng đích 53, cờ QR = 0); bỏ qua câu trả lời của thiết bị mạng | P0 |
| FR-1.6 | Gói méo hoặc không phải DNS bị bỏ qua và đếm lại, không làm dừng bộ nhận | P0 |
| FR-1.7 | Số gói bị bỏ khi hàng đợi đầy phải hiện ở kiểm tra sức khỏe, không im lặng | P0 |
| FR-1.8 | Nhập danh sách chặn có sẵn từ file định dạng hosts qua CLI | P2 |

### FR-2 Làm giàu dữ liệu

| ID | Yêu cầu | Ưu tiên |
|---|---|---|
| FR-2.1 | Phân giải chuỗi CNAME của domain ứng viên, lưu toàn bộ mắt xích | P0 |
| FR-2.2 | Tra ASN và tổ chức sở hữu từ IP đích, dùng dữ liệu iptoasn cục bộ | P0 |
| FR-2.3 | Đối chiếu với các blocklist công khai đã đồng bộ | P0 |
| FR-2.4 | Tra thứ hạng Tranco | P1 |
| FR-2.5 | Truy vấn crt.sh lấy SAN để tìm domain anh em cùng chứng chỉ | P1 |
| FR-2.6 | Tra tuổi domain qua WHOIS / RDAP | P1 |
| FR-2.7 | Reverse lookup tìm domain khác cùng IP | P2 |
| FR-2.8 | Cache kết quả làm giàu, TTL cấu hình được, không tra lại trong TTL | P0 |
| FR-2.9 | Giới hạn tốc độ với mọi dịch vụ ngoài, hỏng một dịch vụ không làm hỏng cả pipeline | P0 |

### FR-3 Phân loại và chấm điểm

| ID | Yêu cầu | Ưu tiên |
|---|---|---|
| FR-3.1 | Chấm điểm domain theo tập tín hiệu có trọng số, chi tiết ở [06](06-classification.md) | P0 |
| FR-3.2 | Lưu từng tín hiệu riêng lẻ kèm trọng số, không chỉ lưu điểm tổng | P0 |
| FR-3.3 | Gán nhãn phân loại: ads, tracking, telemetry, malware, cryptomining, adult, cdn, content | P0 |
| FR-3.4 | Ngưỡng điểm cấu hình được riêng cho từng phân loại | P1 |
| FR-3.5 | Trọng số tín hiệu sửa được qua UI, không cần build lại | P1 |
| FR-3.6 | Chạy lại toàn bộ chấm điểm sau khi đổi trọng số, hiển thị số domain bị ảnh hưởng trước khi áp dụng | P1 |
| FR-3.7 | Bảo vệ: không bao giờ chặn domain trong danh sách bảo vệ cứng hoặc top 50k Tranco ở mức eTLD+1 | P0 |

### FR-4 Vòng đời và quyết định

| ID | Yêu cầu | Ưu tiên |
|---|---|---|
| FR-4.1 | Trạng thái domain: `new`, `staging`, `blocked`, `allowed`, `ignored` | P0 |
| FR-4.2 | Domain mới vượt ngưỡng vào `staging`, tự lên `blocked` sau N ngày nếu không bị can thiệp | P0 |
| FR-4.3 | Mọi thay đổi trạng thái ghi vào nhật ký bất biến kèm người thực hiện, thời điểm, lý do | P0 |
| FR-4.4 | Nhật ký không sửa, không xóa được qua API | P0 |
| FR-4.5 | Quyết định thủ công luôn thắng quyết định tự động, và không bị lần chạy sau ghi đè | P0 |
| FR-4.6 | Domain `blocked` không còn xuất hiện trong log vẫn giữ nguyên trạng thái trong N ngày | P0 |
| FR-4.7 | Thao tác hàng loạt: chọn nhiều domain, áp dụng một hành động, một dòng nhật ký cho mỗi domain | P1 |

### FR-5 Điều tra domain

| ID | Yêu cầu | Ưu tiên |
|---|---|---|
| FR-5.1 | Trang chi tiết gom mọi dữ liệu về một domain vào một màn hình | P0 |
| FR-5.2 | Hiển thị chuỗi CNAME dạng đồ thị, đánh dấu mắt xích thuộc hạ tầng adtech | P0 |
| FR-5.3 | Liệt kê subdomain cùng gốc đã thấy trong mạng, kèm lưu lượng từng cái | P0 |
| FR-5.4 | Đồ thị domain liên quan theo bốn loại quan hệ: CNAME, cùng ASN, cùng chứng chỉ, đồng xuất hiện | P1 |
| FR-5.5 | Timeline truy vấn theo client | P1 |
| FR-5.6 | Từ trang chi tiết, chọn nhiều domain liên quan và chặn cả cụm trong một thao tác | P1 |
| FR-5.7 | Tìm kiếm domain hỗ trợ tiền tố, hậu tố và biểu thức chính quy | P1 |

### FR-6 Thêm thủ công

| ID | Yêu cầu | Ưu tiên |
|---|---|---|
| FR-6.1 | Thêm một domain kèm phân loại và lý do | P0 |
| FR-6.2 | Dán hàng loạt, mỗi dòng một domain, xem trước trước khi lưu | P0 |
| FR-6.3 | Hỗ trợ wildcard `*.example.com`, hiển thị số domain đã biết sẽ khớp | P1 |
| FR-6.4 | Cảnh báo khi domain thêm tay có thứ hạng Tranco cao hoặc nằm trong danh sách bảo vệ | P0 |
| FR-6.5 | Nhập từ file, xuất ra file các mục thêm tay | P2 |

### FR-7 Quản lý nguồn ngoài

| ID | Yêu cầu | Ưu tiên |
|---|---|---|
| FR-7.1 | Đăng ký blocklist công khai theo URL, đồng bộ định kỳ | P0 |
| FR-7.2 | Nạp danh mục nguồn có sẵn từ AdGuard HostlistsRegistry | P1 |
| FR-7.3 | Phân tích chồng lấn giữa các nguồn: bao nhiêu domain trùng, bao nhiêu chỉ có ở một nguồn | P1 |
| FR-7.4 | Bật/tắt từng nguồn mà không mất cấu hình | P0 |
| FR-7.5 | Nguồn hỏng hoặc trả về ít hơn 50% lần trước thì giữ dữ liệu cũ và cảnh báo | P0 |

### FR-8 Xuất bản

| ID | Yêu cầu | Ưu tiên |
|---|---|---|
| FR-8.1 | Endpoint HTTP cho từng phân loại, định dạng hosts `0.0.0.0 domain` | P0 |
| FR-8.2 | Endpoint gộp tất cả phân loại đang bật | P0 |
| FR-8.3 | Trả `ETag` và `Last-Modified`, hỗ trợ `If-None-Match` | P0 |
| FR-8.4 | Ghi snapshot mỗi lần nội dung đổi: số lượng, checksum, thời điểm | P0 |
| FR-8.5 | So sánh hai snapshot bất kỳ, hiển thị domain thêm và bớt | P1 |
| FR-8.6 | Rollback về snapshot cũ | P1 |
| FR-8.7 | Chặn xuất bản nếu số lượng giảm quá ngưỡng cấu hình so với lần trước | P0 |
| FR-8.8 | Định dạng thay thế: AdGuard, dnsmasq, RPZ, JSON | P2 |

### FR-9 Vận hành

| ID | Yêu cầu | Ưu tiên |
|---|---|---|
| FR-9.1 | Dashboard: truy vấn theo giờ, tỉ lệ chặn, top domain, top client, số ứng viên chờ duyệt | P0 |
| FR-9.2 | Đăng nhập bằng mật khẩu, phiên có thời hạn | P0 |
| FR-9.3 | Hai vai trò: `admin` toàn quyền, `viewer` chỉ đọc | P0 |
| FR-9.4 | Kiểm tra sức khỏe: nguồn log còn cập nhật không, job có chạy không, nguồn ngoài có lỗi không | P0 |
| FR-9.5 | Sao lưu và phục hồi toàn bộ trạng thái bằng một lệnh | P1 |
| FR-9.6 | Chỉ số Prometheus tại `/metrics` | P2 |

## 6. Yêu cầu phi chức năng

### Hiệu năng

| Chỉ tiêu | Mục tiêu | Ghi chú |
|---|---|---|
| Nạp query log | ≥ 5.000 dòng/giây trên Pi 4 | Đủ cho mạng vài trăm thiết bị |
| API danh sách domain | p95 < 300 ms với 500k domain | Có phân trang và lọc |
| Trang chi tiết domain | p95 < 500 ms | Bao gồm đồ thị quan hệ |
| Xuất bản list 250k dòng | < 2 giây | Render từ CSDL, không cache sẵn |
| Tải trang đầu tiên | < 1,5 giây trên LAN | Bundle < 300 KB gzip |

### Tài nguyên

Phải chạy được trên máy nhỏ, ví dụ Raspberry Pi 4 với 4 GB RAM.

| Thành phần | RAM | Ghi chú |
|---|---|---|
| Tiến trình DNSGuard | < 512 MB | Binary tĩnh, không runtime, đã gồm cả CSDL |
| Cache SQLite | 32 MB | Đặt bằng pragma `cache_size` |
| Tổng | < 600 MB | Chỉ một tiến trình duy nhất phải trông chừng |

Toàn bộ hệ thống là một tiến trình. Không có máy chủ CSDL riêng, không có bộ thu thập
riêng, nên cũng không có thứ tự khởi động nào phải đúng.

Ghi đĩa phải tính đến việc máy chủ có thể dùng thẻ nhớ: gộp ghi theo lô, không fsync
mỗi bản ghi.

### Độ tin cậy

- DNSGuard chết **không** được ảnh hưởng đến DNS của mạng. Đây là ràng buộc kiến trúc, không phải mục tiêu.
- File blocklist đã xuất bản phải còn nguyên và phục vụ được kể cả khi backend chết.
- Mất điện đột ngột không được làm hỏng CSDL hay để lại file blocklist dở dang.
- Job làm giàu dữ liệu lỗi phải thử lại có backoff, không chặn các job khác.

### Bảo mật

- Không phơi ra Internet. Thiết kế cho mạng nội bộ; nếu cần truy cập từ xa thì qua VPN.
- Mật khẩu băm bằng argon2id.
- Không lưu bí mật trong CSDL dạng rõ.
- Query log chứa dữ liệu nhạy cảm về hành vi người dùng: có chính sách xóa theo thời hạn, mặc định 90 ngày.
- Không gửi dữ liệu ra ngoài trừ các truy vấn làm giàu đã liệt kê ở FR-2, và mỗi loại phải tắt được.

### Khả năng vận hành

- Một binary + một file cấu hình + một file CSDL. Không yêu cầu Docker nhưng có sẵn image.
- Migration CSDL chạy tự động khi khởi động, có thể tắt.
- Log cấu trúc JSON, mức log đổi được lúc chạy.
- Mọi giá trị cấu hình đặt được qua biến môi trường.

## 7. User story kèm tiêu chí nghiệm thu

### US-1 Duyệt ứng viên hằng tuần

> Là quản trị mạng, tôi muốn duyệt nhanh các domain mới được phát hiện để quyết định
> chặn hay bỏ qua, mà không phải rời tay khỏi bàn phím.

**Nghiệm thu**
- Hàng đợi sắp theo điểm giảm dần, mặc định chỉ hiện trạng thái `staging`
- Mỗi mục hiện: domain, điểm, các tín hiệu, số truy vấn, số client, phân loại đề xuất
- Phím tắt: `B` chặn, `A` cho qua, `I` bỏ qua, `J`/`K` di chuyển, `Enter` mở chi tiết
- Sau mỗi hành động, con trỏ tự sang mục kế tiếp
- Có `Ctrl+Z` hoàn tác trong 10 giây
- Duyệt 50 domain mất dưới 3 phút

### US-2 Điều tra một domain lạ

> Là quản trị mạng, khi thấy một domain không nhận ra, tôi muốn biết nó thuộc về ai
> và liên quan tới những domain nào trước khi quyết định.

**Nghiệm thu**
- Một màn hình duy nhất hiển thị: chuỗi CNAME, IP hiện tại kèm ASN và tổ chức, thứ hạng Tranco, tuổi domain, thành viên list công khai
- Danh sách subdomain cùng gốc đã thấy trong mạng, kèm lưu lượng
- Đồ thị domain liên quan, mỗi cạnh ghi rõ loại quan hệ
- Chọn được nhiều nút trên đồ thị và chặn cả cụm bằng một thao tác
- Toàn bộ tải xong dưới 500 ms nếu dữ liệu đã cache

### US-3 Gỡ chặn khẩn cấp

> Là quản trị mạng, khi ai đó báo trang bị hỏng, tôi muốn tìm và gỡ domain gây lỗi
> trong vòng một phút.

**Nghiệm thu**
- Tìm được domain bằng tên gần đúng
- Lọc query log theo IP client và khoảng thời gian, thấy ngay domain nào bị chặn
- Chuyển sang `allowed` có hiệu lực trong bản xuất bản kế tiếp
- Có nút "xuất bản ngay" không đợi lịch
- Trạng thái `allowed` không bị lần chấm điểm sau ghi đè

### US-4 Thêm hàng loạt từ nguồn ngoài

> Là quản trị mạng, tôi muốn dán một danh sách domain lấy từ diễn đàn vào hệ thống,
> nhưng phải thấy trước cái gì sẽ bị chặn.

**Nghiệm thu**
- Dán nhiều dòng, hệ thống phân tích và hiện bảng xem trước
- Xem trước đánh dấu: domain không hợp lệ, domain đã có, domain trùng danh sách bảo vệ, domain có thứ hạng Tranco cao
- Bỏ chọn được từng dòng trước khi lưu
- Sau khi lưu, mỗi domain có một dòng nhật ký riêng với cùng lý do

### US-5 Hiểu vì sao một domain bị chặn

> Là quản trị mạng, sáu tháng sau tôi muốn biết vì sao một domain nằm trong danh
> sách chặn.

**Nghiệm thu**
- Trang chi tiết có mục lịch sử theo thứ tự thời gian
- Mỗi dòng: hành động, người thực hiện (hoặc `system`), thời điểm, lý do
- Với quyết định tự động, lý do liệt kê các tín hiệu và trọng số tại thời điểm đó
- Lịch sử không sửa được từ giao diện

### US-6 Người dùng tự tra cứu

> Là người dùng trong mạng, tôi muốn tự kiểm tra xem một trang có bị chặn không mà
> không cần hỏi quản trị.

**Nghiệm thu**
- Vai trò `viewer` truy cập được trang tra cứu
- Nhập domain, nhận kết quả: bị chặn hay không, thuộc phân loại nào
- Có nút gửi yêu cầu mở kèm ghi chú, tạo một mục chờ quản trị xử lý
- `viewer` không thấy query log của client khác

## 8. Ràng buộc

| Ràng buộc | Hệ quả thiết kế |
|---|---|
| Chạy trên Pi 4 ARM64 | Go build tĩnh; tránh phụ thuộc nặng; cẩn thận với bộ nhớ khi xử lý list lớn |
| MikroTik chỉ tải file HTTP dạng hosts | Xuất bản phải là file tĩnh phục vụ qua HTTP, không phải API |
| MikroTik kiểm tra cập nhật mỗi 4 giờ | Thay đổi không tức thì; UI phải nói rõ điều này |
| Chỉ nhận được bản sao gói tin | Mất gói là mất hẳn; phải đo và báo tỉ lệ mất |
| Một người vận hành | Không cần workflow phê duyệt nhiều cấp, nhưng vẫn cần nhật ký |

## 9. Rủi ro

| Rủi ro | Ảnh hưởng | Giảm thiểu |
|---|---|---|
| Chặn nhầm domain thiết yếu | Mất niềm tin, có thể mất tiền nếu là cổng thanh toán | Danh sách bảo vệ cứng, ngưỡng Tranco, giai đoạn staging, cảnh báo khi thêm tay |
| crt.sh hoặc dịch vụ ngoài giới hạn tốc độ | Mất tính năng làm giàu | Cache lâu, hàng đợi có backoff, hệ thống chạy được khi thiếu |
| CSDL phình theo query log | Đầy đĩa trên Pi | Tổng hợp theo giờ, xóa log thô sau 90 ngày |
| Phân loại sai hàng loạt sau khi đổi trọng số | Nhiều domain bị chặn oan | Xem trước tác động trước khi áp dụng, ngưỡng chặn xuất bản |
| Người dùng bật DoH, hệ thống mù | Giảm hiệu quả, không phải lỗi phần mềm | Ghi rõ trong tài liệu, thêm cảnh báo trên dashboard khi phát hiện lưu lượng DoH |
