# DNSGuard

Hệ quản trị blocklist DNS lấy **domain** làm trung tâm, không phải resolver.

Nó nhận bản sao lưu lượng DNS của mạng, phát hiện và phân loại domain quảng cáo /
theo dõi, cho phép người quản trị điều tra từng domain cùng các domain liên quan, rồi
xuất ra các file blocklist theo phân loại để MikroTik (hoặc thiết bị bất kỳ) tải về
và chặn.

**Điểm khác biệt so với Pi-hole / AdGuard Home / Technitium:** DNSGuard *không* nằm
trên đường truy vấn DNS. Nó không phân giải, không chặn. Nó phân tích và ra quyết
định; việc chặn do thiết bị mạng đảm nhiệm. Nếu DNSGuard chết, DNS của mạng vẫn chạy.

---

## Kiến trúc

```
Thiết bị LAN ──DNS :53──> MikroTik ──upstream──> 1.1.1.1
                             │  (tự phân giải, chặn bằng adlist)
                             │ mirror TZSP :37008
                             ▼
                          DNSGuard
                       (một binary Go)
                             │
                        /lists/*.txt
                             ▼
                    MikroTik tải về định kỳ
```

Hai mũi tên ra vào DNSGuard là toàn bộ ranh giới trách nhiệm của nó: **nhận** dữ liệu
quan sát, **trả** danh sách quyết định.

---

## Thành phần

Toàn bộ hệ thống là **một binary Go duy nhất** cộng một file CSDL. Không có tiến
trình phụ trợ, không có ngôn ngữ thứ hai, không có dịch vụ ngoài phải vận hành.

| | |
|---|---|
| Backend | Go 1.26+ — một binary tĩnh, không runtime |
| Frontend | React 19 + TypeScript + Vite, nhúng sẵn trong binary |
| CSDL | SQLite (WAL), một file |
| Thu thập | Bộ nhận TZSP tích hợp, cổng UDP 37008 |
| Hàng đợi job | Chạy trên chính CSDL |
| Đóng gói | Image Docker multi-arch (amd64, arm64) |

---

## Cài đặt nhanh

**Linux** — một lệnh, tự tải binary, tạo dịch vụ systemd:

```bash
curl -fsSL https://raw.githubusercontent.com/benji/dnsguard/main/scripts/install.sh | sudo sh
```

**Docker** — không cần cài gì lên máy chủ:

```bash
docker run -d --name dnsguard --restart unless-stopped \
  -p 8080:8080 -p 37008:37008/udp \
  -v dnsguard-data:/var/lib/dnsguard \
  ghcr.io/benji/dnsguard:latest
```

**Docker Compose** — nếu muốn sửa cấu hình trong file:

```bash
docker compose -f deploy/docker-compose.yml up -d
```

Lần khởi động đầu tiên in ra mật khẩu quản trị sinh ngẫu nhiên:

```bash
docker logs dnsguard 2>&1 | grep -m1 password    # hoặc: journalctl -u dnsguard | grep -m1 password
```

Mở http://localhost:8080 và đăng nhập bằng tài khoản `admin`.

---

## Hai bước trên thiết bị mạng

**1. Gửi bản sao lưu lượng DNS sang DNSGuard**

```routeros
/tool sniffer set filter-port=53 filter-ip-protocol=udp \
    streaming-enabled=yes streaming-server=<ip-máy-chủ>:37008 \
    filter-stream=yes memory-limit=100KiB
/tool sniffer start
/system scheduler add name=sniffer-boot start-time=startup \
    on-event="/tool sniffer start"
```

Dòng `scheduler` là bắt buộc, không phải tùy chọn: không có nó, bộ mirror dừng sau mỗi
lần router khởi động lại và hệ thống ngừng học mà **không có triệu chứng gì** — danh
sách cũ vẫn chặn bình thường. DNSGuard giám sát tuổi của truy vấn gần nhất và cảnh báo
trên dashboard khi gặp tình trạng này.

**2. Sau khi có dữ liệu, trỏ adlist về DNSGuard**

```routeros
/ip dns adlist add url="http://<ip-máy-chủ>:8080/lists/ads.txt" ssl-verify=no
/ip dns adlist add url="http://<ip-máy-chủ>:8080/lists/tracking.txt" ssl-verify=no
```

---

## Sau khi cài

Vào **Cài đặt** trên giao diện để tải hai bảng tra cứu:

| Bảng | Nuôi tín hiệu | Thiếu thì sao |
|---|---|---|
| Tranco | `high_rank` −8,0 | Mất lớp bảo vệ mạnh nhất chống chặn nhầm tên miền phổ biến |
| ip2asn | `asn_adtech` +4,0 | Không nhận ra được domain trỏ về hạ tầng của Criteo, Adform… |

Cả hai tải một lần rồi dùng offline, cập nhật lại vài tháng một lần.

---

## Phát triển

```bash
git clone <repo> && cd dnsguard
cp .env.example .env

make dev            # backend :8080, frontend :5173 (hot reload)
make test           # test Go và kiểm tra kiểu TypeScript
make build          # một binary có nhúng giao diện
```

Chi tiết ở [docs/07-development.md](docs/07-development.md).

---

## Đọc theo thứ tự

| # | Tài liệu | Dành cho | Nội dung |
|---|---|---|---|
| 01 | [Yêu cầu](docs/01-requirements.md) | tất cả | Vấn đề, phạm vi, yêu cầu chức năng và phi chức năng, user story kèm tiêu chí nghiệm thu |
| 02 | [Kiến trúc](docs/02-architecture.md) | dev, kiến trúc sư | Sơ đồ hệ thống, module backend, luồng dữ liệu, các quyết định kỹ thuật và lý do |
| 03 | [Mô hình dữ liệu](docs/03-data-model.md) | dev backend | Lược đồ SQLite đầy đủ, index, migration, chính sách lưu trữ |
| 04 | [Đặc tả API](docs/04-api.md) | dev cả hai phía | Toàn bộ endpoint REST, định dạng lỗi, phân trang, xác thực |
| 05 | [Frontend](docs/05-frontend.md) | dev frontend | Cấu trúc thư mục, route, component, quản lý state, đặc tả từng màn hình |
| 06 | [Bộ phân loại](docs/06-classification.md) | dev backend | Taxonomy, tín hiệu, công thức chấm điểm, vòng đời domain, đánh giá chất lượng |
| 07 | [Phát triển](docs/07-development.md) | dev | Dựng môi trường, quy ước code, kiểm thử, triển khai |
| 08 | [Lộ trình](docs/08-roadmap.md) | tất cả | Trạng thái từng mốc và phạm vi còn lại |

Nếu bạn chỉ đọc một file: **01** để hiểu làm gì, **02** để hiểu làm thế nào.

---

## Vì sao mirror chứ không forward

Cách hiển nhiên hơn là cho router forward DNS lên một resolver do DNSGuard vận hành.
Cách đó hỏng ở ba điểm cùng lúc:

- **Mất IP client.** Mọi truy vấn đến từ IP của router, nên ba tín hiệu hành vi
  (`third_party`, `fan_out`, `beacon`) trở nên vô nghĩa.
- **Mù với truy vấn đã bị chặn.** Truy vấn bị adlist chặn không bao giờ đi tiếp, nên
  hệ thống hiểu nhầm domain đã chặn là "đã chết" và gỡ chúng ra — sinh ra dao động
  chặn/bỏ chặn vĩnh viễn.
- **Mù với cache.** Truy vấn trúng cache của router cũng không đi tiếp.

Mirror gói tin giải quyết cả ba, vì gói bắt được là gói client gửi lên router, trước
khi router quyết định làm gì với nó. Đổi lại, DNSGuard nằm ngoài đường truy vấn — nó
chết thì DNS của mạng vẫn chạy.

---

## Giấy phép

[MIT](LICENSE) © 2026 benji
