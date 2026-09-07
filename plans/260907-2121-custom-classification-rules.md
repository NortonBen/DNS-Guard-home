# Rule phân loại domain do người dùng tự đặt

Trạng thái: **đã cài đặt theo phương án B** · Nghiên cứu 2026-09-07, cài đặt 2026-09-07

## 1. Hiện trạng

Màn **Phân loại & trọng số** cho sửa hai thứ: trọng số của từng tín hiệu, và ngưỡng
điểm của từng phân loại. Nhưng **bản thân các luật thì nằm cứng trong mã nguồn** —
người vận hành không thêm được từ khóa hay tên miền adtech của riêng mạng mình.

| Dữ liệu | Số mục | Ảnh hưởng |
|---|---|---|
| `adtechDomains` | 67 | Nuôi `cname_adtech` +6,0 và `cert_adtech` +3,0 |
| `keywordGroups` | 30 | Nuôi `keyword` +3,0 và quyết định nhãn |
| `sharedCDNSuffixes` | 19 | Nuôi `shared_cdn` −6,0 (bảo vệ) |
| `adtechASNs` | 13 | Nuôi `asn_adtech` +4,0 |
| `neutralASNs` | 10 | Chặn `asn_adtech` kích hoạt nhầm (bảo vệ) |
| `hardAllow` | 35 | Không bao giờ chặn (bảo vệ) |
| Ngưỡng tín hiệu | 17 hằng số | Định nghĩa "bằng chứng là gì" |

Khoảng trống thực tế: danh sách mặc định lấy theo hạ tầng adtech quốc tế. Mạng Việt
Nam gặp `admicro.vn`, `adtima.vn`, `eclick.vn` — ba cái đó đã có, nhưng còn nhiều
mạng nội địa khác và người vận hành không có cách nào bổ sung.

## 2. Phân loại theo mức rủi ro

Không phải luật nào cũng nên mở ra giao diện.

| Dữ liệu | Cho sửa? | Lý do |
|---|---|---|
| Từ khóa | **Có** | Giá trị cao nhất, rủi ro thấp nhất. Từ khóa một mình chỉ +3,0 và không đủ để gán nhãn chặn nếu không có bằng chứng khác |
| Tên miền adtech | **Có, kèm bảo vệ** | Giá trị cao. Nhưng xem §3 — một mục sai là đủ để chặn |
| ASN adtech | **Có, kèm bảo vệ** | Phải từ chối mọi ASN nằm trong danh sách trung tính |
| CDN dùng chung | **Chỉ cho thêm** | Đây là danh sách *bảo vệ*. Thêm thì an toàn, bớt thì mất lớp phòng vệ |
| ASN trung tính | **Không** | Danh sách bảo vệ. Gỡ Google khỏi đây là chặn nửa Internet |
| Danh sách bảo vệ cứng | **Không** | Đã quyết từ đầu, sửa phải qua pull request |
| Ngưỡng tín hiệu | **Có, kèm chặn biên** | Hữu ích khi hiệu chỉnh theo quy mô mạng |

## 3. Quyết định cần chốt

`cname_adtech` có trọng số **+6,0**, trong khi ngưỡng `ads` là **5,5**.

Nghĩa là: **một tên miền thêm nhầm vào danh sách adtech là đủ để chặn một domain, tự
nó, không cần bằng chứng nào khác.** Với danh sách nằm trong mã nguồn thì chấp nhận
được — sửa phải qua pull request và review. Với một ô nhập trên giao diện thì đây
đúng là kiểu hỏng mà cả hệ thống đang tránh ở mọi chỗ khác.

Ba cách xử lý:

**A. Luật tự đặt dùng loại tín hiệu riêng, trọng số thấp hơn.** Thêm
`cname_adtech_custom` +4,0. Gõ nhầm không đủ chặn một mình; cần thêm bằng chứng.
*Đánh đổi:* taxonomy tín hiệu phình ra, và luật tự đặt yếu hơn luật có sẵn dù người
dùng có thể biết rõ hơn về mạng của mình.

**B. Dùng chung loại tín hiệu, bắt buộc xem trước tác động.** Giống hệt luồng đổi
trọng số đang có: sửa luật → xem "sẽ chặn thêm N domain" kèm danh sách mẫu → mới cho
lưu. Cộng thêm giai đoạn chờ bảy ngày vốn đã có, nên vẫn còn một tuần để can thiệp.
*Đánh đổi:* dựa vào việc người dùng thật sự đọc bảng xem trước.

**C. Cả hai.** Trọng số thấp hơn *và* bắt buộc xem trước.

**Đã chọn B.** Lý do: xem trước tác động là cơ chế đã có, đã hoạt động, và
chính xác tuyệt đối vì hàm chấm điểm thuần túy. Thêm một loại tín hiệu song song
làm bảng tín hiệu khó đọc hơn, mà người vận hành thêm `quangcaoabc.vn` vào danh sách
thường biết rõ hơn bất kỳ danh sách mặc định nào.

## 4. Thiết kế nếu chọn B

**Khóa cấu hình mới**, tất cả **gộp thêm** vào dữ liệu có sẵn chứ không thay thế —
nhờ vậy bản nâng cấp thêm mục mới vẫn có tác dụng, và người dùng không xóa nhầm được
lớp bảo vệ dựng sẵn:

```
rules.keywords        {ads: [...], tracking: [...], telemetry: [...]}
rules.adtech_domains  {"quangcaoabc.vn": "ads"}
rules.adtech_asns     {123456: {org: "...", category: "ads"}}
rules.shared_cdn      ["cdn-noi-bo.vn"]        chỉ cho thêm
rules.thresholds      {spread_high_min: 40, ...}
```

**Kiểm tra đầu vào — phần quan trọng nhất:**

- ASN nằm trong danh sách trung tính → từ chối, nêu rõ tên tổ chức
- Tên miền nằm trong danh sách bảo vệ cứng → từ chối
- Tên miền trùng hậu tố CDN dùng chung → cảnh báo, đòi xác nhận
- Từ khóa dưới ba ký tự → từ chối, vì nó khớp gần như mọi tên miền
- Ngưỡng vượt khoảng hợp lý → từ chối kèm khoảng cho phép

**Thay đổi ở `classify`:** hàm chấm điểm phải nhận thêm bộ luật mà vẫn thuần túy.
Giữ `Score(d, f, w)` gọi sang `ScoreWith(d, f, w, DefaultRules())` để toàn bộ test
hiện có không phải sửa.

**Giao diện:** thêm vào màn Phân loại & trọng số, dưới khu vực trọng số. Cùng luồng
bắt buộc: sửa → **Xem tác động** → mới bật được **Áp dụng**.

## 5. Việc phải làm

1. `internal/classify/rules.go` — kiểu `Rules`, `DefaultRules()`, hàm gộp
2. `ScoreWith` nhận `Rules`; `Score` giữ nguyên chữ ký cũ
3. Các hàm tra cứu (`hasAdtechSuffix`, `matchKeyword`, `isSharedCDN`) đọc từ `Rules`
4. `internal/store` — đọc ghi năm khóa cấu hình mới
5. `internal/api` — `GET/PUT /scoring/rules`, dùng lại cơ chế `dry_run` của trọng số
6. Kiểm tra đầu vào theo §4
7. Giao diện trong màn Phân loại & trọng số
8. Test: mỗi luật kiểm tra bị từ chối đúng, và luật tự đặt thật sự đổi kết quả chấm điểm

Ước lượng: khoảng 600 dòng Go, 250 dòng TypeScript, cộng test.


## 6. Kết quả cài đặt

Làm theo phương án B: dùng chung loại tín hiệu, bắt buộc xem trước tác động.

| Việc | Ở đâu |
|---|---|
| Kiểu `Rules`, `Custom`, `DefaultRules`, `Merge` | `internal/classify/rules.go` |
| `ScoreWith(d, f, w, r)`; `Score` giữ nguyên chữ ký | `internal/classify/score.go` |
| Tra cứu thành phương thức của `Rules` | `internal/classify/rules.go`, `signals.go` |
| `GET/PUT /scoring/rules`, dùng lại `dry_run` | `internal/api/handlers_ops.go` |
| Kiểm tra đầu vào theo §4 | `Custom.Validate` |
| Giao diện trong màn Phân loại & trọng số | `web/src/components/domain/rules-editor.tsx` |

Khác kế hoạch ở ba chỗ:

- **Kiểm tra trả về mọi lỗi cùng lúc** thay vì dừng ở lỗi đầu. Người dùng dán vào một
  danh sách dài thì cần biết hết vấn đề trong một lần.
- **Lưu xong tự xếp hàng chấm điểm lại.** Kế hoạch không nói tới; nhưng luật mới chỉ
  có tác dụng khi domain được chấm lại, và để giao diện nói một đằng dữ liệu một nẻo
  là lỗi tệ hơn một job chạy nền.
- **Gộp `adtechASN` vào `ASNInfo`** — hai kiểu giống hệt nhau, giữ cả hai là thừa.

Một phát hiện trong lúc làm: lớp bảo vệ `high_rank` **−8,0** thắng được `cname_adtech`
**+6,0**, nên một tên miền top Tranco thêm nhầm vẫn không bị chặn (điểm ra −2, nhãn
`cdn`). Nhưng lớp đó đòi phải có bảng Tranco; chưa tải bảng thì bảng xem trước là lớp
bảo vệ duy nhất. Cả hai đều có test khẳng định.
