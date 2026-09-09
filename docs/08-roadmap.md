# 08 — Lộ trình và trạng thái

Bảy mốc. Mỗi mốc có một **mục tiêu chứng minh được** — thứ demo được cho người khác
xem, không phải một danh sách task đã tick.

Trạng thái dưới đây phản ánh mã nguồn hiện tại trong repo.

---

## M0 — Bộ khung · ✅ xong

**Chứng minh:** `make dev` chạy lên, đăng nhập được, thấy trang trống.

- [x] Cây thư mục repo, Makefile, `.env.example`
- [x] Docker Compose một service, không phụ thuộc ngoài
- [x] Migration `0001_init` với toàn bộ lược đồ, nhúng trong binary
- [x] Lớp `store` với hai pool kết nối và các pragma SQLite
- [x] Đăng nhập / đăng xuất / `GET /auth/me`, argon2id, CSRF
- [x] Vite + TanStack Router, trang đăng nhập và khung ứng dụng

**Hoàn thành khi:** người mới clone repo, chạy hai lệnh, đăng nhập được. ✅

---

## M1 — Nạp dữ liệu và hiển thị · ✅ xong

**Chứng minh:** trỏ bộ mirror của thiết bị mạng vào, sau vài phút dashboard hiện số
liệu thật.

- [x] Bộ nhận TZSP: bóc Ethernet/VLAN/QinQ/IPv4/IPv6/UDP/DNS
- [x] Ghi theo lô, hai goroutine tách biệt, đếm gói bị bỏ
- [x] `query_events`, `domain_hourly` với bitmap client, `clients`
- [x] `GET /stats/overview`, `GET /stats/top`, `GET /health`
- [x] `GET /domains` có lọc, sắp xếp, phân trang con trỏ
- [x] Dashboard: 4 thẻ, biểu đồ 24 giờ, hai bảng top
- [x] Danh sách domain: bảng ảo hóa, bộ lọc đồng bộ URL, ba chế độ tìm kiếm
- [x] Băng cảnh báo khi ngừng nhận truy vấn

**Hoàn thành khi:** nạp một triệu dòng trong vài phút, dashboard tải dưới một giây. ✅
Đo được ~156.000 sự kiện/giây trên máy phát triển; ngưỡng yêu cầu là 5.000/giây.

---

## M2 — Chấm điểm và xuất bản · ✅ xong

**Chứng minh:** thiết bị mạng tải danh sách do DNSGuard sinh ra và chặn được.

- [x] `signals`, `decisions`, `snapshots`, `snapshot_entries`, `list_sources`, `list_entries`
- [x] Module `enrich`: DNS, ASN, cert, RDAP, rank — đủ cả năm nguồn
- [x] Module `classify`: 16 tín hiệu, hàm `Score` thuần túy
- [x] Bảng test chấm điểm 11 case, cộng test tính thuần túy
- [x] Module `catalog`: đồng bộ, ba định dạng, bảo vệ sụt giảm, ETag
- [x] Module `publish`: render theo phân loại, ghi nguyên tử, checksum, snapshot
- [x] `GET /lists/*.txt` với ETag và 304
- [x] Vòng đời `new → staging → blocked` với canary
- [x] Bốn test bất biến

**Hoàn thành khi:** adlist trên thiết bị mạng trỏ vào `/lists/ads.txt` hoạt động. ✅

---

## M3 — Quyết định của con người · ✅ xong

**Chứng minh:** duyệt 50 domain trong 3 phút, chỉ dùng bàn phím.

- [x] `POST /domains/:id/decision`, `POST /domains/bulk-decision`
- [x] Danh sách bảo vệ cứng, trả 403 đúng cách, áp cả cho quyết định tự động
- [x] Cờ `is_manual` khiến `classify` bỏ qua — thực thi ở ba chỗ độc lập
- [x] Nhật ký kèm ảnh chụp trạng thái, trigger chặn sửa xóa
- [x] Màn Triage: hai cột, phím tắt, hoàn tác 10 giây bằng cách hoãn gửi
- [x] `POST /domains` thêm tay có `dry_run`
- [x] Màn thêm tay: textarea, xem trước có màu, hỗ trợ wildcard
- [x] Xuất bản kích hoạt ngay sau quyết định thủ công

---

## M4 — Điều tra domain · ✅ xong

**Chứng minh:** cho xem một domain lạ, trả lời được nó thuộc về ai và có bao nhiêu
anh em, trong dưới 30 giây.

- [x] `relations`, `domain_facts`
- [x] Enricher `cert` (crt.sh), `rdap`, `rank` (Tranco), có cache và circuit breaker
- [x] Module `graph`: bốn loại cạnh, `co_occurs` tính bằng PMI chuẩn hóa
- [x] `GET /domains/:id` đầy đủ, `/graph`, `/timeline`
- [x] Trang chi tiết: tín hiệu, hạ tầng, chuỗi CNAME, anh em, lịch sử
- [x] Đồ thị quan hệ tự cài đặt, chọn nhiều nút
- [x] Nút "Chặn cả cụm"
- [x] Tải theo lớp, có khung xương chờ
- [x] Bảng tương đương cho trình đọc màn hình

**Đây là mốc có giá trị sản phẩm cao nhất.** Ba mốc trước làm cho hệ thống chạy được;
mốc này làm cho nó đáng dùng.

---

## M5 — Quản lý nguồn và hiệu chỉnh · ✅ xong

**Chứng minh:** đổi một trọng số, xem trước tác động, áp dụng, thấy kết quả đúng như
đã báo trước.

- [x] CRUD `list_sources`, đồng bộ thủ công
- [x] `GET /sources/overlap` phân tích chồng lấn, ma trận Jaccard
- [x] `GET|PUT /scoring/weights` có `dry_run` tính tác động
- [x] `POST /scoring/rescore` chạy nền có tiến độ
- [x] Màn phân loại: ngưỡng, bật tắt, khu vực trọng số có bước xem trước **bắt buộc**
- [x] Màn nguồn ngoài: bảng, tab chồng lấn
- [x] Màn xuất bản: lịch sử snapshot, so sánh, quay lại bản cũ
- [x] SSE cho tiến độ job

**Hoàn thành khi:** con số ở màn xem trước khớp chính xác với kết quả sau khi áp
dụng. ✅ Đảm bảo bằng tính thuần túy của `Score`, có test riêng.

---

## M6 — Hoàn thiện vận hành · phần lớn đã xong

**Chứng minh:** giao cho người khác vận hành mà không cần bạn giải thích.

- [x] Vai trò `viewer`, trang tra cứu, yêu cầu mở chặn
- [x] Job xóa dữ liệu quá hạn cho mọi bảng theo chính sách ở [03 §10](03-data-model.md)
- [x] `dnsguard-cli`: `migrate`, `create-user`, `reset-password`, `publish`, `backup`, `import-hosts`
- [x] Chế độ SQLite — là chế độ duy nhất, không còn là phương án thay thế
- [x] Ảnh Docker multi-arch, biên dịch chéo arm64 bằng hai biến môi trường
- [x] `/metrics` Prometheus, bảo vệ bằng bearer token tùy chọn
- [x] Chế độ tối, focus ring, bảng thật với `<th scope>`, tôn trọng `prefers-reduced-motion`
- [ ] Bộ E2E Playwright năm luồng
- [ ] Tài liệu người dùng cuối, khác với tài liệu phát triển này

---

## Chưa làm

Ghi lại để không bị quên, và để không bị cám dỗ nhét vào sớm.

| Việc | Trạng thái | Ghi chú |
|---|---|---|
| Bộ E2E Playwright | chưa | Năm luồng chính đã có test API tương đương; E2E phủ thêm phần giao diện |
| Tài liệu người dùng cuối | chưa | Tài liệu hiện tại viết cho người phát triển |
| Nạp danh mục từ AdGuard HostlistsRegistry | chưa | FR-7.2, P1. Hiện phải tự nhập URL nguồn |
| Tập đánh giá chất lượng phân loại | chưa | [06 §6](06-classification.md) mô tả cách xây; chưa có dữ liệu thật để lấy mẫu |

---

## Ngoài phạm vi phiên bản 1

| Ý tưởng | Vì sao hoãn |
|---|---|
| Tầng học máy | Cần dữ liệu đã gán nhãn từ nhiều tháng vận hành thật |
| Đa người dùng, đa mạng | Chưa có nhu cầu; làm phức tạp mọi truy vấn |
| Đẩy trực tiếp qua API thiết bị mạng | File tĩnh đủ dùng và đơn giản hơn nhiều |
| Ứng dụng di động | Web responsive là đủ |
| Chia sẻ danh sách giữa các cài đặt | Vấn đề tin cậy và quyền riêng tư, cần thiết kế riêng |
| Chặn theo lịch (giờ học, giờ ngủ) | Thuộc về thiết bị chặn, không phải nơi ra quyết định |

---

## Không được cắt trong bất kỳ hoàn cảnh nào

- **Nhật ký quyết định bất biến** — không có nó thì hệ thống không đáng tin
- **Danh sách bảo vệ cứng** — không có nó thì sớm muộn cũng chặn nhầm cổng thanh toán
- **Bước xem trước tác động khi đổi trọng số** — một lần trượt tay chặn nhầm hàng nghìn domain
- **Bảo vệ sụt giảm khi xuất bản** — một nguồn ngoài hỏng sẽ làm sập cả danh sách
