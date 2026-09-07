# Điều tra theo IP: nhật ký pháp chứng, vị trí, phát hiện độc hại

Nguồn IP là **bản ghi trả lời DNS** mà router đang gửi tới cổng 37008 nhưng code
đang vứt đi. Không đổi cấu hình MikroTik.

---

## 1. Hiện trạng

Cổng 37008 nhận đúng những gì `filter-port=53 filter-ip-protocol=udp` cho qua
(README.md:95). Trong đó có **cả gói trả lời**, vì `filter-port` khớp cả hai chiều.
Code loại chúng ở hai chỗ độc lập:

| Vị trí | Điều kiện | Hệ quả |
|---|---|---|
| `ingest/tzsp.go:178` | `dstPort != 53` | Gói trả lời (src 53) bị loại ở tầng UDP |
| `ingest/dns.go:57` | `msg[2]&0x80 != 0` | `parseQuestion` từ chối thông điệp có bit QR |

Không có bảng nào lưu ánh xạ domain → IP. `enrich/asn.go:140` có tự phân giải
domain để tra ASN, nhưng đó là tra cứu **lúc khác, từ máy khác**, không phải câu
trả lời mà client thật sự nhận được.

`query_events` đã có `client_id + domain_id + occurred_at`. Thiếu đúng một mảnh:
domain trỏ tới IP nào.

---

## 2. Quyết định nền: không dùng IP đích của gói

Gói trả lời đi hai chiều và IP đích **không đồng nhất về ý nghĩa**:

| Chiều | IP nguồn | IP đích | Ghi chú |
|---|---|---|---|
| router → client | router | **client thật** | trả lời từ cache hoặc sau khi phân giải |
| upstream → router | 1.1.1.1 | **router** | không phải client nào cả |

Lấy IP đích làm client sẽ gán nhầm mọi lượt phân giải upstream cho chính router.

**Chọn:** không đọc IP đích. Ánh xạ domain → IP không phụ thuộc client. Quy kết
client lấy bằng cách nối bảng sẵn có:

```
query_events (client → domain, thời điểm)  ⋈  domain_ips (domain → IP)
```

Kết quả: *"lúc 14:32, client 192.168.1.50 hỏi ads.example.com, tên này trỏ tới
104.21.5.6 — Cloudflare, US"*. Đây chính là câu hỏi điều tra cần trả lời, và nó
đúng cho cả gói trả lời từ cache lẫn từ upstream.

---

## 3. Việc phải làm

### Giai đoạn 1 — Bắt bản ghi trả lời (nền móng)

- `ingest/dns.go`: thêm `parseAnswer` — đọc qname từ phần câu hỏi, duyệt answer
  section, lấy A/AAAA và chuỗi CNAME kèm TTL.
  **Bắt buộc:** phần answer *có* dùng nén tên, khác phần câu hỏi. Đã giải bằng cách
  bỏ qua tên mà không đi theo con trỏ — xem §5, cách đó không có bước nhảy nào nên
  không cần bộ đếm chống lặp.
- `ingest/tzsp.go`: nhận thêm gói `srcPort == 53`, đánh dấu chiều. Không đọc IP đích.
- `ingest/listener.go`: kiểu `Resolution{Domain, IPs, TTL, At}`, gom lô riêng,
  ghi qua sink thứ hai.
- `store/migrations/0006_*.sql`: bảng `domain_ips`, khóa duy nhất `(domain_id, ip)`.
- `store/resolutions.go`: `WriteResolutions`, upsert `first_seen/last_seen/hits`.
- Test: gói trả lời IPv4/IPv6, có nén tên, chuỗi CNAME, gói dị dạng, vòng lặp con trỏ.

### Giai đoạn 2 — Vị trí (location)

- `enrich/asn.go`: thêm `LookupIP(netip.Addr) (ASNFacts, bool)` — `Lookup` đã có
  nhưng trả `asnRange` không xuất khẩu nên ngoài package không đọc được trường.
- Điền `asn/country/org` cho từng dòng `domain_ips` (bảng iptoasn đã có sẵn cột quốc gia).
- API + UI: một domain trỏ tới những IP nào, thuộc mạng nào, quốc gia nào.

### Giai đoạn 3 — Phát hiện truy cập độc hại

- Nguồn IP blacklist (Feodo Tracker, Spamhaus DROP…) — theo đúng khuôn `catalog`
  đang dùng cho danh sách domain.
- Đối chiếu IP trong `domain_ips`; kết hợp tín hiệu beacon sẵn có
  (`classify/signals.go:129`) để phân biệt C2 với lượt truy cập ngẫu nhiên.
- Cảnh báo đẩy qua bus SSE sẵn có (`internal/events`). **Không tự chặn** — giữ
  nguyên tắc "con người ra quyết định" của M3.

### Giai đoạn 4 — Nhật ký pháp chứng

- Ghi pcap **gói thật** nhận được, xoay vòng theo dung lượng, tắt mặc định.
  Không dựng lại pcap từ CSDL: gói bịa ra mà trình bày như bằng chứng là sai.
- API điều tra: dòng thời gian theo IP client, theo IP đích, theo khoảng thời gian.
- Xuất hồ sơ vụ việc kèm manifest sha256.
- Đường lưu trữ riêng, không bị `ApplyRetention` xóa cùng query log.

---

## 4. Giới hạn phải nói rõ

- **DoH/DoT đi vòng qua hoàn toàn.** Client dùng DNS mã hóa không xuất hiện trong
  luồng mirror. Đây là lỗ hổng lớn nhất và không vá được ở tầng này.
- **CDN xoay IP.** Một domain có thể trỏ tới hàng chục IP; `last_seen` phản ánh
  lần quan sát gần nhất chứ không phải toàn bộ tập IP.
- **IP dùng chung.** Một IP Cloudflare phục vụ hàng triệu domain — khớp blacklist
  theo IP dễ dương tính giả hơn theo domain. Cần đối chiếu ASN trước khi cảnh báo.
- **Không thấy kết nối thật.** Phân giải một tên không chứng minh có kết nối tới
  IP đó. Muốn có bằng chứng kết nối phải mirror thêm lưu lượng trên MikroTik —
  ngoài phạm vi kế hoạch này.

---

## 5. Kết quả cài đặt

### Giai đoạn 1 — xong

| Thay đổi | Nơi |
|---|---|
| `parseAnswer` + `skipName` + `readQName` dùng chung | `ingest/dns.go` |
| Nhận gói cổng nguồn 53, cờ `answer` | `ingest/tzsp.go` |
| Kiểu `Resolution`, hai đường ghi, `batchLoop` tổng quát | `ingest/listener.go` |
| Bảng `domain_ips` | `store/migrations/0006_domain_ips.sql` |
| `WriteResolutions`, `DomainIPs` | `store/resolutions.go` |
| Tách `lookupDomainIDs` khỏi `resolveDomains` | `store/events.go` |

Test: bóc gói trả lời IPv4/IPv6, nén tên, chuỗi CNAME, TTL nhỏ nhất, gói cắt cụt,
RDLENGTH vượt gói, con trỏ tự trỏ, và một test đầu-cuối đẩy datagram thật qua socket
UDP (chạy sạch với `-race`).

**Bất biến quan trọng nhất, có test riêng:** đường ghi phân giải không chạm
`query_count` và `last_seen` của `domains`. `resolveDomains` cộng dồn `query_count`,
nên dùng lại nó sẽ đếm đôi mọi truy vấn — mà điểm quyết định chặn tính từ chính con
số đó. Đường ghi mới dùng `lookupDomainIDs`, chỉ tra chứ không tạo và không cộng.

### Giai đoạn 2 — xong

| Thay đổi | Nơi |
|---|---|
| `LookupIP` xuất khẩu (`Lookup` cũ trả kiểu không xuất khẩu) | `enrich/asn.go` |
| `PendingGeoIPs`, `SetIPGeo` | `store/resolutions.go` |
| Job `geo_ip`, nhịp một giờ, chạy lúc khởi động | `worker/geoip.go`, `worker/schedule.go` |

`country` rỗng là "đã tra mà không biết", `NULL` là "chưa tra" — không phân biệt thì
dải ngoài bảng ip2asn sẽ bị tra lại mãi mãi. Có test cho cả ba nhánh: tra được, không
tra được, và chưa nạp bảng.

### Điều tra — xong

| Thay đổi | Nơi |
|---|---|
| `DomainsByIP`, `AccessesByIP` | `store/resolutions.go` |
| `GET /investigate/ip?addr=…` | `api/handlers_investigate.go` |
| Trường `ips` trong `GET /domains/:id` | `api/handlers_domains.go` |
| `resolutions_accepted` trên `/health` | `api/handlers_ops.go` |

`/health` cảnh báo khi `events_accepted` tăng mà `resolutions_accepted` đứng yên ở 0.
Đó là cách duy nhất phát hiện router không mirror gói trả lời: không có nó, tính năng
im lặng không có dữ liệu mà không báo lỗi ở đâu cả.

### Giai đoạn 3 — xong

| Thay đổi | Nơi |
|---|---|
| Package đối chiếu địa chỉ độc hại | `internal/threat/threat.go` |
| `enrich.Table` tách khỏi `Reloadable`, `DefaultIPThreatURL` | `enrich/lookup.go` |
| Cột `threat`, `IPThreatState`, `SetIPThreats`, `ThreatMatches` | `store/migrations/0007_ip_threat.sql`, `store/resolutions.go` |
| Job `ip_threat` nhịp một giờ, cảnh báo qua bus SSE | `worker/ipthreat.go` |
| `GET /threats`, bảng đe dọa ở màn Cài đặt | `api/handlers_threats.go`, `api/handlers_settings.go` |

Tra cứu thử từng độ dài prefix từ hẹp tới rộng thay vì tìm nhị phân trên dải đã sắp
xếp: dải trong danh sách đe dọa lồng nhau được, mà tìm nhị phân theo điểm bắt đầu rất
khó chứng minh đúng trong trường hợp đó. Duyệt từ hẹp tới rộng còn cho ra **dải cụ thể
nhất**, tức đúng dòng người vận hành sẽ tìm thấy khi mở file danh sách ra đối chiếu.
Số bước bị chặn bởi ngưỡng độ rộng tối thiểu, nên chi phí không phụ thuộc kích thước
danh sách.

Dải rộng hơn /8 (IPv4) hoặc /19 (IPv6) bị loại lúc nạp: một dòng hỏng kiểu `0.0.0.0/0`
sẽ đánh dấu mọi địa chỉ là độc hại, biến cảnh báo thành nhiễu trắng đúng lúc cần tin
nó nhất.

Job quét lại **toàn bộ** địa chỉ mỗi lượt chứ không chỉ địa chỉ mới. Danh sách thay
đổi theo thời gian, nên địa chỉ sạch tuần trước có thể bị liệt kê tuần này — và cảnh
báo tự tắt khi địa chỉ được gỡ, không cần cơ chế dọn dẹp riêng.

**Không tự chặn.** Danh sách theo IP dễ dương tính giả hơn theo domain vì địa chỉ dùng
chung; quyết định vẫn thuộc về người vận hành, đúng nguyên tắc của M3.

### Giai đoạn 4 — làm được một nửa

| Xong | Nơi |
|---|---|
| Bộ ghi pcap xoay vòng, trần dung lượng, đường lưu trữ riêng | `internal/pcap/pcap.go` |
| Móc vào vòng đọc socket, ghi cả khung không bóc được | `ingest/listener.go` |
| Cấu hình, mặc định tắt, trạng thái trên `/health` | `config`, `cmd/dnsguard/main.go`, `api/handlers_ops.go` |

Bóc TZSP tách khỏi bóc Ethernet để khung thô vào được bộ ghi **trước khi** bị diễn
giải: một cuộc tấn công dùng DNS dị dạng sẽ biến mất khỏi bằng chứng nếu chỉ ghi
những gì tầng trên bóc được.

Tên file luôn mang số thứ tự có độ rộng cố định sau dấu thời gian. Dấu thời gian chỉ
phân giải tới giây, mà xoay vòng hai lần trong một giây là chuyện có thật khi trần
file nhỏ; không có số thứ tự thì lần xoay thứ hai trùng tên lần đầu và vòng xoay ngừng
hoạt động — đã có test giữ điều này.

File sinh ra được kiểm chứng bằng `tcpdump` thật, bóc tới tận tầng DNS, không chỉ đối
chiếu byte với hiểu biết của chính package.

### Xuất hồ sơ — xong

| Thay đổi | Nơi |
|---|---|
| `ExportQueries`, `ExportResolutions` theo luồng | `store/resolutions.go` |
| `GET /forensics/export` → zip + manifest sha256 | `api/handlers_forensics.go` |

Duyệt theo callback từng dòng chứ không trả về lát cắt: một khoảng vài tháng có thể là
hàng chục triệu dòng, và nạp hết vào bộ nhớ trên máy Pi sẽ giết tiến trình.

Băm tính **trong lúc ghi**, nên manifest là file cuối cùng trong zip. Đó cũng là lý do
hỏng giữa chừng vẫn an toàn: không có mục lục trung tâm hợp lệ, mọi công cụ giải nén
đều báo hỏng thay vì đưa ra hồ sơ thiếu dữ liệu mà trông như đầy đủ.

`limitations` nằm trong chính manifest: người mở hồ sơ sáu tháng sau không có tài liệu
nào trong tay.

### Giao diện — xong

| Thay đổi | Nơi |
|---|---|
| Màn `/ip-forensics`: cảnh báo, tra ngược, xuất hồ sơ | `web/src/routes/ip-forensics.tsx` |
| Thẻ "Địa chỉ quan sát trên dây" | `web/src/routes/domain-detail.tsx` |
| Kiểu và hook | `web/src/api/types.ts`, `web/src/api/hooks.ts` |

### Kiểm chứng trên máy chủ thật

Chạy binary thật, bắn 15 truy vấn + 15 câu trả lời TZSP vào cổng:

- `resolutions_accepted` 15, `decode_errors` 0 — tuyến bắt câu trả lời chạy.
- `tcpdump` đọc file pcap, bóc đúng cả hai chiều, đủ 30 gói.
- Đối chiếu đe dọa: `/32` khớp địa chỉ đơn, `/16` khớp dải, `8.8.8.8` sạch.
- Hồ sơ xuất ra: zip hợp lệ, sha256 trong manifest khớp nội dung thật.
- Giao diện: cảnh báo, tra ngược, và thẻ địa chỉ đều hiện đúng.

### Còn lại

Không còn hạng mục nào của kế hoạch này. Những thứ nằm ngoài phạm vi từ đầu:

- Bằng chứng **kết nối** thật — cần mirror thêm lưu lượng trên MikroTik.
- Client dùng DoH/DoT — không vá được ở tầng này.
