# Nghiên cứu bổ sung nguồn cho "Dữ liệu tra cứu"

> **Trạng thái 2026-09-08**: §4.1, §4.2, §4.4 và §5.1 đã triển khai. Chi tiết ở cuối
> tài liệu, mục 11. §4.3 (lane DoH) chưa làm.

Ngày 2026-09-08 · nhánh `main` · mọi số liệu dưới đây đo trực tiếp trong ngày, bằng
chính parser của dự án (`threat.Set.LoadTable`, `enrich.RankEnricher.LoadTable`,
`enrich.ASNEnricher.Lookup`).

## 1. Kết luận

Ba việc, xếp theo mức khẩn:

1. **Nguồn `ipthreat` mặc định đã chết.** Feodo Tracker đóng băng từ 2026-03-04, còn
   **4 mục**. Tính năng "cảnh báo phân giải tới hạ tầng độc hại" hiện gần như không
   hoạt động. Đây là lỗi vận hành đang diễn ra, không phải thiếu sót tính năng.
2. **Phần lớn danh sách IP phổ biến sai trục đối với DNSGuard** — chúng liệt kê IP
   *tấn công vào* mạng, còn DNSGuard đối chiếu IP mà mạng *phân giải tới*. Chọn nhầm
   trục thì thêm nguồn chỉ làm tăng báo nhầm.
3. **`rank` và `asn` nhận file rỗng / trang lỗi HTML là "nạp thành công"**, và
   `RefreshTable` sẽ ghi đè bảng đang dùng bằng file hỏng đó. Lỗ hổng này nguy hiểm
   *tỉ lệ thuận* với số nguồn thêm vào, vì thêm nguồn nghĩa là dùng "Đổi URL" nhiều hơn.

## 2. Bằng chứng: nguồn `ipthreat` hiện tại

```
https://feodotracker.abuse.ch/downloads/ipblocklist.txt
# Last updated: 2026-03-04 14:28:39 UTC   → 6 tháng không đổi
565 byte · 4 mục
```

Biến thể `ipblocklist_aggressive.txt` có 7.606 mục nhưng **cùng dấu thời gian
2026-03-04** — tức toàn bộ Feodo Tracker dừng cập nhật, không phải danh sách thường
bị co lại. Kiểm chứng đối chứng: ThreatFox (cùng abuse.ch) cập nhật 2026-09-08 —
abuse.ch còn sống, riêng Feodo Tracker thì không.

Giấy phép abuse.ch: **CC0**, dùng thương mại lẫn phi thương mại không giới hạn.

## 3. Bằng chứng: rủi ro báo nhầm

Đo bằng bảng ip2asn của chính dự án (`data/ip2asn.tsv.gz`, 578k dải): tỉ lệ mục nằm
trong ASN hạ tầng dùng chung (Cloudflare, Google, AWS, Akamai, Fastly, Microsoft,
DigitalOcean, OVH, Hetzner, Contabo, Alibaba, Tencent, Apple, Meta…).

| Danh sách | Số mục | Hạ tầng dùng chung | Trục | Parser hiện tại |
|---|---:|---:|---|---|
| **Spamhaus DROP** | 1.707 | **0,29 %** | ra (dải rogue) | ✅ 0 dòng code |
| Tor exit list | 1.340 | 1,34 % | chính sách | ✅ 0 dòng code |
| Feodo aggressive | 7.606 | 7,53 % | ra (C2) | ✅ nhưng chết |
| blocklist.de | 23.981 | 14,27 % | **vào** | ✅ |
| hagezi ips/tif | 67.517 | 14,89 % | ra (hỗn hợp) | ✅ |
| GreenSnow | 5.049 | 19,81 % | **vào** | ✅ |
| **ThreatFox (full)** | 19.023 | 25,74 % | ra (C2) | ❌ cần parser CSV |
| hagezi ips/doh | 1.456 | 27,82 % | chính sách | ✅ |
| ipsum L3 | 17.689 | 33,16 % | **vào** | ✅ |
| ET compromised | 580 | 44,48 % | **vào** | ✅ |
| CINS Army | 15.000 | 45,38 % | **vào** | ✅ |
| Feodo (mặc định) | 4 | — | ra (C2) | ✅ nhưng chết |

Đọc bảng này cần cẩn thận ở hai chỗ:

- Với mục `/32`, "nằm trong ASN dùng chung" **không** tự nó là báo nhầm: một VPS thuê
  ở DigitalOcean để chạy C2 vẫn là mục tiêu đúng. Con số đo **độ mục nát**: IP thuê
  bị thu hồi rồi cấp lại cho người khác, còn dải bị chiếm đoạt thì không.
- Vì thế 0,29 % của Spamhaus DROP mới đáng giá: đó là *dải* bị thuê hoặc chiếm đoạt
  bởi tổ chức tội phạm — đúng luận điểm đã ghi trong `internal/enrich/lookup.go`
  ("hạ tầng chuyên dụng của kẻ tấn công, không phải dải dùng chung").

**Trục là tiêu chí loại trừ mạnh hơn cả tỉ lệ báo nhầm.** blocklist.de, CINS, GreenSnow,
ET compromised, ipsum liệt kê nguồn tấn công SSH/quét cổng — máy *gõ cửa* mạng của
bạn. DNSGuard không bao giờ tra những IP đó: nó tra IP nằm trong bản ghi trả lời DNS.
Nạp chúng vào `ipthreat` chỉ tạo nhiễu. **Không đề xuất, dù parser đọc được.**

## 4. Đề xuất

### 4.1 Thay nguồn `ipthreat` — làm ngay

**Spamhaus DROP** làm mặc định mới:

```
https://www.spamhaus.org/drop/drop.txt
1.707 dải · 47 KB · cập nhật 2026-09-04 · miễn phí cho mọi đối tượng
```

Định dạng `1.10.16.0/20 ; SBL256894` khớp chính xác `parsePrefix` hiện có — chú thích
`;`, dải CIDR, không dòng nào lỗi (1.708 dòng → 1.707 mục, chênh 1 là trùng lặp
`62.60.226.0/24`, map tự khử). **Đổi một hằng số, không sửa code.**

Lưu ý: Spamhaus báo bản `.txt` sẽ **ngừng phục vụ trong tương lai** (chuyển sang
`drop_v4.json`), có thông báo trước. Ghi vào lịch theo dõi, chưa cần làm gì.

### 4.2 Thêm ThreatFox — bù đúng thứ Feodo từng làm

```
https://threatfox.abuse.ch/export/csv/ip-port/recent/   → 3.202 IOC, CSV thuần
https://threatfox.abuse.ch/export/csv/ip-port/full/     → 25.538 IOC, ZIP
19.023 IP duy nhất · cập nhật 2026-09-08 · CC0
```

Đây là nguồn C2 botnet còn sống của abuse.ch, đúng trục "mã độc gọi ra ngoài".
Cần parser: cột 3 dạng `"26.150.54.213:6606"`, có dấu nháy và dấu phẩy.

Đã kiểm chứng CSV thô qua parser hiện tại: **0 mục + lỗi** → `RefreshTable` bỏ dở an
toàn, bảng cũ còn nguyên. Nghĩa là thêm nguồn này không thể làm hỏng dữ liệu đang chạy.

Hai bảng DROP + ThreatFox bổ sung nhau, không chồng: DROP là *dải* hạ tầng rogue,
ThreatFox là *địa chỉ* C2 đang sống.

### 4.3 Năng lực mới đáng giá nhất: phát hiện vượt DNS nội bộ

```
https://raw.githubusercontent.com/hagezi/dns-blocklists/main/ips/doh.txt
1.456 IP máy chủ DNS-over-HTTPS · GPL-3.0
```

Với một thiết bị canh DNS, đây là lỗ hổng lớn nhất chưa được đo: máy trong mạng bật
DoH thì DNSGuard mù hoàn toàn. Danh sách này biến "mù" thành "thấy được": phân giải
hoặc kết nối tới IP trong đó = thiết bị đang đi vòng.

Đây **không phải** danh sách đe dọa — 27,82 % nằm ở Apple/Cloudflare vì đó chính là
các nhà cung cấp DoH hợp pháp. Phải là lane riêng, ngữ nghĩa cảnh báo riêng ("bỏ qua
DNS nội bộ"), tuyệt đối không trộn vào `ipthreat`.

### 4.4 Dự phòng cho `rank`

```
https://s3-us-west-1.amazonaws.com/umbrella-static/top-1m.csv.zip
12,9 MB · zip chứa top-1m.csv dạng rank,domain
```

**Đã kiểm chứng nạp được bằng `RankEnricher` không sửa dòng nào**: 50.000 mục,
`google.com` → hạng 1. Dùng khi tranco-list.eu hỏng — `high_rank` là tín hiệu bảo vệ
mạnh nhất (−8,0), mất nó là mất lớp chống chặn nhầm.

### 4.5 Không nên làm: bảng dải IP cloud/CDN

Đã khảo sát AWS `ip-ranges.json`, Cloudflare `ips-v4`, Google `goog.json`/`cloud.json`,
Fastly `public-ip-list` — đều sống và tải được. **Vẫn không đề xuất**: bảng ASN hiện
có đã trả lời đúng câu hỏi đó (ASN 13335 = Cloudflare, 16509 = AWS…). Cần "IP này là
hạ tầng dùng chung" thì thêm một *luật* danh sách ASN, không phải thêm một *nguồn tải
về*. Rẻ hơn, không thêm lane, không thêm lịch cập nhật.

(Azure còn đòi lần theo trang tải động mới ra được URL JSON — thêm một lý do bỏ qua.)

## 5. Hai lỗ hổng phát hiện trong lúc khảo sát

### 5.1 `rank` và `asn` nhận file hỏng là nạp thành công

```
rank  file rỗng          → loaded=true  entries=0  err=nil
rank  trang lỗi HTML     → loaded=true  entries=0  err=nil
asn   file rỗng          → loaded=true  entries=0  err=nil
asn   trang lỗi HTML     → loaded=true  entries=0  err=nil
```

`RefreshTable` nạp thử từ file tạm rồi mới `Rename` — nhưng "nạp thử" này *thành công*,
nên bảng tốt bị ghi đè bằng trang lỗi, cả trên đĩa lẫn trong bộ nhớ. Giao diện báo
"Đã nạp" màu xanh với 0 bản ghi.

`threat.Set` **không** dính: nó từ chối danh sách rỗng (`internal/threat/threat.go`,
"danh sách không có mục nào đọc được"). Cùng một biện pháp, cần nhân ra hai bảng còn
lại. Đây là điều kiện tiên quyết trước khi mở thêm nguồn — vì mở thêm nguồn nghĩa là
người vận hành bấm "Đổi URL" nhiều hơn.

### 5.2 Majestic Million nạp "thành công" nhưng sai âm thầm

```
/tmp/majestic.csv   entries=50000   google.com → hạng 0   err=nil
```

Majestic là `GlobalRank,TldRank,Domain,…` — parser đọc cột 1 làm domain, tức nạp
50.000 con số làm tên miền. Không lỗi, giao diện xanh, và `high_rank` **im lặng không
bao giờ kích hoạt nữa**. Mất lớp phòng vệ mạnh nhất mà không có dấu hiệu nào.

Đây là hệ quả cụ thể của 5.1: kiểm tra "khác rỗng" chưa đủ, cần kiểm tra ngữ nghĩa —
ví dụ sau khi nạp, khẳng định vài tên miền phổ biến đã biết phải tra ra hạng.

## 6. Ràng buộc kiến trúc cần quyết

`ipthreat` hiện là **một** bảng, **một** đường dẫn, **một** URL. Thêm nguồn thứ hai
(DROP *và* ThreatFox) cần chọn một trong hai:

- **Nhiều kind**: mỗi nguồn một `kind` — sửa `config.go`, `main.go`,
  `Runner.LookupPath`, `Runner.lookupTable`, và danh sách kiểm tra cứng ở
  `handlers_settings.go:407`. Bốn chỗ cho mỗi nguồn mới.
- **Một kind, nhiều URL**: `threat.Set` gộp nhiều danh sách, giữ nguồn gốc theo mục
  để màn điều tra IP nói được "khớp DROP" hay "khớp ThreatFox".

Phương án 2 hợp với sản phẩm hơn — người vận hành cần biết *danh sách nào* khớp mới
đánh giá được mức tin cậy — nhưng đây là quyết định thiết kế, chưa làm.

Cả hai đều gợi ý nên có một sổ đăng ký bảng tra cứu thay cho các `switch` cứng, để
thêm nguồn không còn là sửa bốn file.

## 7. Lane "Nguồn ngoài" (blocklist tên miền) — khảo sát phụ

Không phải câu hỏi chính, nhưng đã kiểm chứng luôn:

| Nguồn | Số mục | Định dạng | Giấy phép |
|---|---:|---|---|
| oisd big | 270.854 | wildcard domain | GPL-3.0 |
| hagezi `wildcard/tif.txt` | — | wildcard domain | GPL-3.0 |
| hagezi `wildcard/doh-vpn-proxy-bypass.txt` | — | wildcard domain | GPL-3.0 |
| URLhaus hostfile | — | hosts | CC0 |
| ThreatFox hostfile | — | hosts | CC0 |
| StevenBlack hosts | — | hosts | MIT |
| Phishing Army extended | — | domain | CC BY-NC-SA |

GPL-3.0 áp cho *dữ liệu tải về*, không lây sang mã nguồn khi chỉ lưu URL — nhưng
DNSGuard là kho công khai, nên đừng cam kết đóng gói sẵn dữ liệu đó vào bản phát hành.

**Đã chết, đừng dùng** (kiểm chứng hôm nay): `ZeroDot1/CoinBlockerLists` và
`xRuffKez/NRD` — cả hai kho GitHub trả 404. BinaryDefense banlist còn sống nhưng rỗng
(7 dòng, toàn chú thích).

## 8. URL đã kiểm chứng 2026-09-08

```
# ipthreat — đề xuất
https://www.spamhaus.org/drop/drop.txt                        1.707 dải   0 dòng code
https://threatfox.abuse.ch/export/csv/ip-port/recent/         3.202 IOC   cần parser CSV
https://threatfox.abuse.ch/export/csv/ip-port/full/          25.538 IOC   ZIP + parser CSV

# lane mới — phát hiện vượt DNS nội bộ
https://raw.githubusercontent.com/hagezi/dns-blocklists/main/ips/doh.txt     1.456 IP

# rank — dự phòng
https://s3-us-west-1.amazonaws.com/umbrella-static/top-1m.csv.zip           drop-in

# đã khảo sát, KHÔNG đề xuất (sai trục hoặc thừa)
https://lists.blocklist.de/lists/all.txt              trục vào
https://cinsscore.com/list/ci-badguys.txt             trục vào
https://raw.githubusercontent.com/stamparm/ipsum/master/levels/3.txt   trục vào
https://rules.emergingthreats.net/blockrules/compromised-ips.txt       trục vào
https://blocklist.greensnow.co/greensnow.txt          trục vào
https://ip-ranges.amazonaws.com/ip-ranges.json        thừa, bảng ASN đã trả lời
https://www.cloudflare.com/ips-v4                     thừa
https://downloads.majestic.com/majestic_million.csv   sai âm thầm, xem §5.2
```

## 9. Thứ tự đề xuất

1. Vá guard nạp bảng cho `rank` và `asn` (§5.1) — chặn hỏng dữ liệu trước khi mở thêm nguồn.
2. Đổi mặc định `ipthreat` sang Spamhaus DROP (§4.1) — một hằng số, khôi phục tính năng đang chết.
3. Quyết kiến trúc nhiều nguồn (§6), rồi thêm ThreatFox (§4.2).
4. Ghi Umbrella làm URL dự phòng cho `rank` (§4.4).
5. Lane phát hiện vượt DNS nội bộ (§4.3) — tính năng mới, cần thiết kế riêng.

## 10. Câu hỏi còn để ngỏ

- Màn điều tra IP có cần nói rõ *danh sách nào* khớp không? Câu trả lời quyết định
  chọn phương án 1 hay 2 ở §6.
- Lane DoH nên cảnh báo hay chỉ ghi nhận? Chặn DoH có thể làm hỏng trình duyệt của
  người trong nhà.
- Có chấp nhận phụ thuộc nguồn GPL-3.0 (hagezi, oisd) trong kho công khai không —
  chỉ lưu URL, hay có ý định đóng gói sẵn?

## 11. Đã triển khai (2026-09-08)

Khác ba chỗ so với đề xuất ban đầu, đều do rà soát mã phát hiện:

1. **Khóa nguồn `spamhaus_drop`, không dùng lại `ipthreat`.** Đề xuất ban đầu giữ khóa
   cũ để khỏi đổi biến môi trường. Sai: file Feodo cũ còn trên đĩa sẽ được nạp lên dưới
   nhãn "Spamhaus DROP", và cột `threat_source` trong hồ sơ điều tra khai một xuất xứ
   sai. Khóa mới thì file cũ đơn giản không được đọc nữa.
   Biến môi trường: `DNSGUARD_IPTHREAT_PATH` → `DNSGUARD_DROP_PATH`.

2. **ThreatFox dùng bản `/full/`, không phải `/recent/`.** Bản recent chỉ giữ vài ngày,
   mà job đối chiếu *xoá cảnh báo khi địa chỉ rời danh sách* — nên bằng chứng "domain
   này đã phân giải tới C2" tự biến mất sau vài ngày. Với công cụ điều tra thì đó là
   mất bằng chứng. Bản full là ZIP, nên phải thêm khả năng đọc ZIP.
   Số liệu §3 (19.023 IP, 25,74% hạ tầng dùng chung) mô tả đúng bản đang chạy.

3. **Nhận dạng ZIP theo nội dung, không theo đuôi tên file** — và việc này lộ ra một lỗi
   có sẵn từ trước: `RefreshTable` tải về file tạm tên `.tmp-tranco.csv.zip-1234567`,
   tức đuôi `.zip` nằm giữa tên. Kiểm tra bằng `HasSuffix` không bao giờ khớp, nên
   **mọi lần bấm "Cập nhật" cho Tranco từ trước tới nay đều hỏng** — đúng trạng thái
   "Chưa có / Đang tải..." trong ảnh chụp màn hình mở đầu nghiên cứu này. Nay đọc bốn
   byte đầu (`PK\x03\x04`) nên cả Tranco lẫn ThreatFox cập nhật được.

Kết quả đo trên binary thật, feed thật:

| Bảng | Trước | Sau |
|---|---:|---:|
| Hạ tầng độc hại | 4 mục (Feodo, chết) | **20.733** (DROP 1.707 + ThreatFox 19.026) |
| Tranco | không cập nhật được | **50.000**, cập nhật được |

Ngoài ra: guard nạp bảng cho `rank`/`asn` (§5.1, §5.2), cột `threat_source` +
migration 0008, và `RefreshTable` khôi phục bảng cũ khi `rename` hỏng — nhánh mà máy
hết dung lượng đĩa sẽ rơi vào.
