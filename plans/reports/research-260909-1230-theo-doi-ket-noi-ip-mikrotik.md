# Nghiên cứu: theo dõi kết nối IP trong mạng, lấy dữ liệu từ router MikroTik

Trạng thái: nghiên cứu, chưa có quyết định. Không sửa code nào.

---

## 1. Mảnh còn thiếu

DNSGuard hiện có hai nửa của một câu, thiếu nửa thứ ba:

| Đã có | Bảng | Nguồn |
|---|---|---|
| client nào hỏi tên nào, lúc nào | `query_events` | TZSP mirror cổng 53 |
| tên nào trỏ tới IP nào | `domain_ips` (migration 0006) | gói trả lời DNS trong cùng luồng mirror |
| **client nào thật sự nối tới IP nào** | **chưa có** | **chưa lấy** |

Suy ra kết nối từ hai bảng đầu là **suy đoán**: thấy client hỏi `ads.example.com` lúc
14:32 và tên đó trỏ `104.21.5.6` thì vẫn *không biết* client có mở kết nối hay không.
Bốn câu hỏi mà nhật ký DNS không bao giờ trả lời được:

1. **Chặn có ăn không.** Domain đã nằm trong `ads.txt`, adlist đã tải — nhưng thiết bị
   vẫn nối tới đúng IP đó thì việc chặn vô hiệu (IP cứng trong firmware, DoH, hoặc
   adlist chưa được tải lại).
2. **Kết nối không có DNS đi trước.** Không có truy vấn nào, mà vẫn có kết nối ra
   ngoài — IP nhúng cứng, DoH tới địa chỉ cố định, hoặc mã độc. Đây là tín hiệu mạnh
   nhất mà DNSGuard đang hoàn toàn mù.
3. **Khối lượng thật.** Hiện xếp hạng domain theo *số lần hỏi*. Một domain hỏi 3 lần
   nhưng kéo 400 MB khác hẳn một domain hỏi 300 lần và kéo 0 byte.
4. **Kết nối tới hạ tầng độc hại.** Cột `domain_ips.threat` (migration 0007) hiện chỉ
   nói "tên này *trỏ tới* IP nằm trong Spamhaus DROP/ThreatFox", không nói được "thiết
   bị nào *đã nối vào*". Cảnh báo mất phần lớn giá trị vì thiếu đúng chỗ đó.

Nói cách khác: đây là nửa còn lại của kế hoạch `plans/260907-2333-dieu-tra-ip-forensics.md`.

---

## 2. MikroTik cho được gì

| Nguồn | Cơ chế | Có | Không có | Đánh giá |
|---|---|---|---|---|
| **Traffic Flow** (NetFlow v5/v9/IPFIX) | **router đẩy** UDP tới collector | mọi flow kể cả rất ngắn; src/dst IP+port; MAC nguồn/đích; địa chỉ+cổng trước và sau NAT; first/last-forwarded; in/out interface; TCP flags; bytes/packets | byte sai khi bật FastTrack (§3) | **phù hợp nhất** |
| `/ip/firewall/connection` qua **REST API** | DNSGuard **kéo** HTTPS | trạng thái hiện tại; `orig-bytes`/`repl-bytes` **đúng cả khi FastTrack** nhờ `orig-fasttrack-bytes`; `tcp-state` | ảnh chụp chứ không phải luồng sự kiện — kết nối ngắn hơn chu kỳ poll biến mất không dấu vết (UDP timeout 30s, TCP time-wait 10s) | bổ sung, không thay thế |
| `/ip/firewall/connection` qua **API nhị phân + `listen`** | router đẩy qua TCP 8728/8729 | sự kiện thêm/xóa theo thời gian thực, không bỏ sót | phải tự viết giao thức hoặc thêm dependency | mạnh nhưng đắt |
| `/tool sniffer` mirror **toàn bộ** lưu lượng | đẩy TZSP (đang dùng cho DNS) | tất cả | băng thông mirror = băng thông mạng; không khả thi | loại |
| `/ip accounting` | — | — | **đã bị gỡ khỏi RouterOS 7** | loại |
| SNMP | kéo | tổng theo interface | không có đích, không có flow | loại |
| Kid Control | kéo | tổng theo thiết bị | không có IP đích | loại |
| `/tool torch` | tương tác | tức thời | không có API ổn định để chạy nền | loại |

---

## 3. Cạm bẫy quyết định toàn bộ: FastTrack

Cấu hình firewall mặc định của MikroTik cho mạng gia đình có
`action=fasttrack-connection` cho `connection-state=established,related`. Gói đi đường
FastTrack **bỏ qua**: firewall filter/mangle, cập nhật connection tracking, queue,
`/ip traffic-flow`, và IP accounting.

Hệ quả cụ thể, và nó không giống nhau ở hai nguồn:

- **NetFlow:** gói đầu tiên của kết nối luôn ở trạng thái `new` nên đi đường chậm và
  *có* được đếm; RouterOS cũng đẩy ngẫu nhiên một số gói xuống đường chậm để giữ entry
  conntrack sống. Kết quả: **bản ghi flow vẫn tồn tại, nhưng số byte thiếu nghiêm
  trọng** — có thể chỉ vài phần trăm lượng thật.
- **Bảng connection:** entry vẫn nằm trong bảng, mang cờ `fasttrack` và có cặp cột
  riêng `orig-fasttrack-bytes` / `repl-fasttrack-bytes`. **Số byte ở đây đúng.**

> Tài liệu MikroTik tự mâu thuẫn ở điểm này: trang tổng hợp viết lưu lượng được
> FastTrack "sẽ không xuất hiện trong bảng connection", trong khi trang FastTrack lại
> mô tả entry mang cờ fast-tracked và có bộ đếm riêng. Câu đầu gần như chắc chắn nói
> về **hardware offload** (switch chip) chứ không phải FastTrack phần mềm. **Phải kiểm
> chứng trên đúng router đang dùng trước khi chốt phương án**: chạy
> `/ip firewall connection print where fasttrack=yes` và xem có dòng nào không.

Bảng hệ quả theo mục tiêu:

| Mục tiêu (§1) | NetFlow khi bật FastTrack | Bảng connection |
|---|---|---|
| 1. Chặn có ăn không | **đúng** (flow tồn tại là đủ) | đúng, nếu poll kịp |
| 2. Kết nối không có DNS | **đúng** | đúng, nếu poll kịp |
| 3. Khối lượng byte | **sai** | **đúng** |
| 4. Nối tới IP độc hại | **đúng** | đúng, nếu poll kịp |

Ba trong bốn mục tiêu — gồm cả hai mục tiêu quan trọng nhất — không bị FastTrack làm
hỏng. Chỉ mục tiêu byte bị hỏng, và nó là mục tiêu duy nhất mang tính "hay có thì tốt".

Đường thoát nếu cần byte chính xác: tắt FastTrack cho dải LAN cần đo. Cái giá là CPU
và throughput — trên hAP ac²/hEX đời thấp thì đây là khác biệt thật sự (routing đường
chậm ăn CPU gấp nhiều lần), nên đây là quyết định của người vận hành, không phải mặc
định của phần mềm.

---

## 4. So sánh ba phương án khả thi

### A. Bộ thu NetFlow v9/IPFIX — router đẩy sang

**Khớp kiến trúc gần như hoàn hảo.** README tuyên bố ranh giới trách nhiệm của DNSGuard
là đúng hai mũi tên: *nhận* dữ liệu quan sát, *trả* danh sách quyết định. NetFlow là mũi
tên thứ nhất đúng loại — router đẩy sang một cổng UDP, y hệt TZSP. Không credential,
không kết nối ra ngoài, DNSGuard chết thì router không hề biết.

- Thêm một listener UDP cạnh listener 37008 hiện có. Tái dùng nguyên khuôn `ingest`:
  `Options`/`Sink`/`Stats` đã có sẵn đúng hình dạng cần dùng.
- Phải tự viết decoder v9/IPFIX. Định dạng dựa trên template: router gửi template
  FlowSet định kỳ, data FlowSet tham chiếu template ID → collector phải nhớ template
  theo (source ID, template ID) và xử lý trường hợp data đến trước template. Ước
  **500–700 dòng Go + test**, **không thêm dependency**. Repo đã tự viết decoder TZSP
  và DNS nên đây đúng loại việc đã làm quen.
- v9 có sequence number → **đo được tỉ lệ mất gói UDP**, đưa thẳng vào `Stats` cạnh
  `PacketsDropped` hiện có.
- **Cấu hình traffic-flow là cấu hình bền, sống qua reboot.** Đây là ưu điểm thật so
  với `/tool sniffer start` — README đang phải dặn thêm một `system scheduler` chỉ để
  bộ mirror sống lại sau khi router khởi động. Chế độ hỏng âm thầm đó không lặp lại ở
  đây.
- Bẫy cấu hình: `active-flow-timeout` mặc định **30 phút** — kết nối dài không sinh bản
  ghi nào suốt 30 phút. Đặt xuống 1 phút. `cache-entries` mặc định 4k, đủ cho mạng gia
  đình.

### B. Poll `/ip/firewall/connection` qua REST API — DNSGuard kéo về

- Rẻ nhất để viết: `net/http` + `encoding/json`, không dependency, ước **~200 dòng**.
  `POST /rest/ip/firewall/connection/print` nhận `.proplist` để chỉ lấy cột cần.
- Số byte **đúng kể cả khi bật FastTrack** — đây là lý do duy nhất đáng chọn nó.
- Cái giá kiến trúc thật sự: **thêm mũi tên thứ ba, chiều đi ra, mang theo credential
  quản trị router.** Hiện DNSGuard không giữ bí mật nào liên quan tới router. Nó cũng
  phải bật `www-ssl` trên router và xử lý chứng chỉ tự ký.
- Yếu điểm kỹ thuật: đây là **ảnh chụp, không phải luồng sự kiện**. Phải tự so sánh hai
  lượt poll liên tiếp để dựng lại flow, và mọi kết nối kết thúc giữa hai lượt biến mất
  hoàn toàn. Với UDP timeout 30s và TCP time-wait 10s, chu kỳ poll phải dưới 10s mới
  tạm ổn — nghĩa là `print` toàn bảng mỗi 10 giây, mãi mãi. REST cũng có giới hạn
  timeout 60 giây cho mỗi lệnh.

### C. API nhị phân + `listen`

- Đẩy theo thời gian thực, không bỏ sót, có cả byte đúng. Về mặt dữ liệu là tốt nhất.
- Cần `github.com/go-routeros/routeros/v3` (dependency mới, ngược với nếp `go.mod` hiện
  tại) hoặc tự viết giao thức: mã hóa độ dài từ + đăng nhập, ước ~300 dòng.
- Vẫn cần credential, vẫn là mũi tên chiều ra, cộng thêm một kết nối TCP dài hạn phải
  tự lo reconnect/backoff.
- Nhiều công hơn A, thêm chi phí kiến trúc của B, đổi lại chỉ hơn A ở mỗi số byte.

### Khuyến nghị

**Chọn A.** Nó là phương án duy nhất giữ nguyên mô hình tin cậy hiện có, không thêm
dependency, và trả lời đúng ba trong bốn câu hỏi mà không bị FastTrack làm hỏng.

Nếu sau này số byte trở nên quan trọng, có hai lựa chọn tách bạch — tắt FastTrack cho
dải LAN cần đo (quyết định vận hành, không phải code), hoặc thêm B như một nguồn *bổ
sung tùy chọn* chỉ để lấy byte. Không nên chọn B làm nguồn chính.

**Đường tắt kiểm chứng trước khi đầu tư:** NetFlow **v5** có định dạng cố định, không
template, decode được trong ~80 dòng. Một buổi làm bản v5 tối giản là đủ chứng minh
toàn tuyến router → cổng UDP → phân tích chạy thông, trước khi bỏ công viết decoder v9.
Không dùng v5 làm bản chính: nó không mang được IPv6 lẫn địa chỉ MAC.

---

## 5. Mô hình dữ liệu đề xuất

Nguyên tắc: bám đúng nếp đã có trong repo, không tạo khái niệm mới.

- **Tái dùng bảng `clients`.** Nó đã ánh xạ IP → id nguyên nhỏ, đúng thứ cần.
- **Khóa nối sang domain đã có sẵn:** `domain_ips.ip`, đã có index `domain_ips_ip`
  được tạo ra đúng cho truy vấn ngược "IP này phục vụ domain nào". Tính năng này chính
  là người tiêu dùng mà index đó được thiết kế để phục vụ.
- **Hai tầng lưu, theo đúng cặp `query_events` + `domain_hourly` đang có:**
  - `flow_events` — thô, giữ ngắn (3–7 ngày), phục vụ điều tra: `client_id`, `ip`,
    `port`, `proto`, `bytes_up`, `bytes_down`, `started_at`, `ended_at`.
  - `client_ip_hourly` — cuộn theo giờ, giữ dài, phục vụ biểu đồ và xếp hạng.
- **Tín hiệu mới** đưa vào bảng `signals` sẵn có, không tạo cơ chế riêng — ví dụ
  `no_dns` (có kết nối tới IP mà client đó không hề phân giải tên nào trỏ tới IP đó
  trong cửa sổ TTL) và `blocked_but_connected`.
- **Job cuộn số** thêm vào `worker/schedule.go` cạnh các job đã có.
- **Sức khỏe bộ nhận** đưa vào `monitor` giống hệt cách TZSP đang làm: "cổng mở nhưng
  không có lưu lượng" phải phân biệt được với "không mở được cổng".

Lượng ghi: mạng gia đình sinh flow nhiều hơn truy vấn DNS khoảng 2–5 lần. Trên máy chạy
thẻ SD, chính sách xóa phải có **ngay từ migration đầu tiên**, không để lại sau.

---

## 6. Rủi ro và cạm bẫy đã nhận diện

1. **FastTrack làm sai số byte** — §3. Rủi ro lớn nhất, và là thứ phải kiểm chứng trên
   router thật trước tiên.
2. **`active-flow-timeout` 30 phút** — không chỉnh thì kết nối dài vô hình rất lâu.
3. **Đếm trùng do NAT.** Đặt `interfaces=all` sẽ thấy cùng một kết nối hai lần: một lần
   trước NAT ở interface LAN, một lần sau NAT ở interface WAN. Đặt đúng bridge LAN,
   hoặc dùng các trường `nat-src-address` mà MikroTik có xuất để khử trùng.
4. **Xác định đâu là client.** Bản ghi flow có cả chiều đi lẫn chiều về; phải quyết định
   phía nào là thiết bị trong mạng — dùng cùng lối suy luận dải RFC1918 mà code đang
   dùng cho tín hiệu `third_party`.
5. **Bỏ chính DNSGuard ra khỏi thống kê.** Nó nằm trong mạng, các lượt tải danh sách
   Tranco/ip2asn/Spamhaus và gọi AI của chính nó sẽ hiện lên như lưu lượng thiết bị.
6. **UDP không có truyền lại.** Mất gói flow là mất dữ liệu vĩnh viễn; dùng sequence
   number của v9 để *đo* và hiển thị, không giấu.
7. **CPU router.** Trên model đời thấp, export flow tốn CPU thật. Đo trước khi bật rộng.
8. **Bề mặt riêng tư lớn hơn hẳn.** Nhật ký DNS đã nhạy cảm; nhật ký *mọi kết nối của
   mọi thiết bị kèm khối lượng* nhạy cảm hơn nhiều bậc, trong một mạng có người nhà
   dùng chung. Việc này đáng được nói rõ trong tài liệu và mặc định giữ dữ liệu ngắn.

---

## 7. Đề xuất chia giai đoạn

| GĐ | Nội dung | Kết quả kiểm chứng được |
|---|---|---|
| 0 | Kiểm chứng FastTrack trên router thật; bật traffic-flow v5 tới một cổng UDP, decode tối thiểu | Biết chắc số byte có dùng được không; chứng minh toàn tuyến chạy thông |
| 1 | Decoder v9/IPFIX + listener + `Stats` + sức khỏe trong `monitor` | Đếm được flow nhận/hỏng/mất, hiện trên dashboard |
| 2 | Migration + writer + cuộn theo giờ + chính sách xóa | Dữ liệu vào CSDL, không phình vô hạn |
| 3 | Nối `domain_ips`: xác minh chặn, phát hiện kết nối không DNS, cảnh báo IP độc hại | Trả lời được 4 câu hỏi ở §1 |
| 4 | Giao diện — mở rộng `web/src/routes/ip-forensics.tsx` đã có | Điều tra được từ giao diện |

Giai đoạn 0 nên chạy trước khi cam kết bất cứ điều gì: kết quả của nó có thể đổi khuyến
nghị ở §4.

---

## Câu hỏi chưa có lời đáp

1. Router model gì? Quyết định việc tắt FastTrack có khả thi không (§3).
2. Số byte theo domain có phải mục tiêu thật, hay chỉ cần biết "ai nối tới đâu"? Nếu là
   vế sau thì FastTrack không còn là vấn đề và phương án A thắng tuyệt đối.
3. Mạng có chạy IPv6 không? Nếu có thì loại hẳn NetFlow v5 kể cả cho giai đoạn 0.
4. Giữ `flow_events` thô bao nhiêu ngày là chấp nhận được, xét cả dung lượng lẫn riêng tư?
5. Có thiết bị nào trong nhà đang dùng DoH/DoT không? Nếu có, đó vừa là lý do mạnh nhất
   để làm tính năng này, vừa là thứ cần dựng test-case trước tiên.
