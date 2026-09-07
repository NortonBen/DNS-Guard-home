# 07 — Phát triển

## 1. Yêu cầu môi trường

| Công cụ | Phiên bản | Kiểm tra |
|---|---|---|
| Go | 1.26+ | `go version` |
| Node | 22 LTS | `node -v` |
| Docker | 24+ | `docker compose version` — chỉ cần khi đóng gói |

Không cần công cụ nào khác. Migration nhúng trong binary, giao diện nhúng trong
binary, và CSDL là một file — nên không có `migrate`, `sqlc`, hay máy chủ CSDL nào
phải cài.

Tùy chọn cho vòng lặp phát triển nhanh hơn:

```bash
go install github.com/air-verse/air@latest            # nạp lại backend khi sửa code
go install honnef.co/go/tools/cmd/staticcheck@latest  # kiểm tra tĩnh
```

## 2. Dựng môi trường

```bash
git clone <repo> && cd dnsguard
cp .env.example .env

cd web && npm install && cd ..

make dev            # backend :8080 + frontend :5173, cả hai đều hot reload
```

Lần khởi động đầu tiên tự tạo CSDL, chạy migration, và in ra mật khẩu quản trị sinh
ngẫu nhiên. Mật khẩu chỉ hiện **một lần** — nó được băm ngay và không lưu ở dạng đọc
được. Đặt sẵn `DNSGUARD_ADMIN_PASSWORD` trong `.env` nếu muốn tự chọn.

Vite dev server proxy `/api`, `/lists` và `/health` sang backend, nên cookie phiên
hoạt động y như khi chạy thật.

### Có dữ liệu để làm việc

Không ai muốn chờ một tuần mới có dữ liệu để làm giao diện. Bộ phát gói tổng hợp gửi
thẳng vào cổng TZSP:

```bash
make seed-traffic   # gửi vài trăm truy vấn DNS giả tới :37008
```

Nó sinh dữ liệu **thực tế** chứ không nhạt nhẽo: một cụm CNAME cloaking, vài domain
nội dung bình thường, một domain telemetry truy vấn đều đặn, một subdomain entropy
cao, và một domain nằm trong danh sách bảo vệ. Fixtures nhạt nhẽo dẫn tới giao diện
chỉ đẹp khi dữ liệu đẹp.

Sau đó ép chấm điểm ngay thay vì đợi chu kỳ hằng giờ:

```bash
curl -X POST localhost:8080/api/v1/scoring/rescore \
  -b cookies.txt -H "X-CSRF-Token: $TOKEN"
```

## 3. Makefile

```make
dev:              ## backend + frontend, hot reload
	@make -j2 dev-api dev-web

dev-api:
	air -c .air.toml

dev-web:
	cd web && npm run dev

build:            ## một binary có nhúng giao diện
	cd web && npm run build
	CGO_ENABLED=0 go build -trimpath \
		-ldflags="-s -w -X main.version=$$(git describe --tags --always)" \
		-o bin/dnsguard ./cmd/dnsguard
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
		-o bin/dnsguard-cli ./cmd/dnsguard-cli

build-arm64:      ## cho máy ARM, ví dụ Raspberry Pi
	cd web && npm run build
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" \
		-o bin/dnsguard-arm64 ./cmd/dnsguard

test:
	go test ./... -race -count=1
	cd web && npm run typecheck

lint:
	gofmt -l . && go vet ./... && staticcheck ./...

image:            ## ảnh Docker multi-arch
	docker buildx build --platform linux/amd64,linux/arm64 \
		-f deploy/Dockerfile -t dnsguard:latest .
```

**`CGO_ENABLED=0` không phải tùy chọn.** Driver SQLite ở đây là Go thuần, nên tắt CGO
cho ra binary tĩnh chạy được trên mọi bản Linux mà không cần libc khớp phiên bản, và
biên dịch chéo sang arm64 chỉ là đổi hai biến môi trường thay vì cài cross toolchain.

## 4. Quy ước code

### Go

Theo [Effective Go](https://go.dev/doc/effective_go) và
[Google Go Style](https://google.github.io/styleguide/go/), cộng thêm:

**Lỗi bọc kèm ngữ cảnh, không log rồi trả về.**

```go
// Đúng
if err := s.store.CreateDomain(ctx, arg); err != nil {
    return fmt.Errorf("create domain %q: %w", arg.Name, err)
}

// Sai — log ở tầng dưới rồi trả lên tầng trên log lại
if err != nil {
    log.Error("create failed", err)
    return err
}
```

Log ở biên hệ thống (HTTP handler, job runner), không log ở tầng giữa. Nếu tầng nào
cũng log thì một lỗi sẽ xuất hiện năm lần trong log và không lần nào có đủ ngữ cảnh.

**Lỗi nghiệp vụ là kiểu riêng**, để tầng HTTP ánh xạ sang mã lỗi:

```go
var (
    ErrDomainProtected = errors.New("domain is protected")
    ErrNotFound        = errors.New("not found")
)
```

Package `api` là chỗ duy nhất biết cả hai thế giới. Nhờ vậy `store` và `classify`
không cần biết HTTP tồn tại.

**`context.Context` là tham số đầu tiên**, luôn truyền xuống, không bao giờ lưu vào
struct.

**Interface định nghĩa ở nơi tiêu thụ**, không ở nơi cài đặt. Package `ingest` khai
báo interface `Sink` mà nó cần; package `store` cài đặt interface đó mà không cần
import `ingest` cho mục đích ấy.

**Không dùng biến toàn cục** ngoài hằng số. Phụ thuộc truyền qua constructor. Cache
gắn vào struct chứ không để ở package level — một cache toàn cục ánh xạ id sẽ gán nhầm
id của CSDL này cho dữ liệu của CSDL kia khi có hai instance.

**Tên package là danh từ số ít**, không có tiền tố: `classify` chứ không `classifier`,
`store` chứ không `dbstore`.

### TypeScript

**`strict: true`, không có `any`.** Nếu buộc phải thoát kiểu, dùng `unknown` rồi thu
hẹp bằng type guard. Bật thêm `noUncheckedIndexedAccess` — truy cập mảng theo chỉ số
trả về `T | undefined`, đúng với thực tế.

**Component là hàm, props có type tường minh:**

```tsx
interface DomainRowProps {
  domain: Domain;
  selected: boolean;
  onSelect: (id: number) => void;
}

export function DomainRow({ domain, selected, onSelect }: DomainRowProps) { … }
```

**Không dùng `React.FC`** — nó thêm `children` ngầm và cản trở generic.

**Một component một file**, tên file kebab-case, tên component PascalCase.

**Hook gọi API nằm trong `src/api/hooks.ts`**, component không tự gọi `fetch`.

### SQL

**Mọi truy vấn có `LIMIT`.** Không có ngoại lệ. Một truy vấn không giới hạn trên bảng
domain sẽ kéo về nửa triệu dòng và giết tiến trình.

**Không bao giờ nối chuỗi đầu vào của người dùng vào SQL.** Danh sách giá trị dựng
bằng số dấu `?` tương ứng; tên cột sắp xếp đi qua một hàm ánh xạ có danh sách trắng.

**Không dùng `datetime()` để so sánh với cột thời gian.** Xem cảnh báo ở
[03 §1](03-data-model.md) — đây là lỗi sai âm thầm, không báo lỗi.

### Đặt tên

Dùng nhất quán một từ cho một khái niệm ở cả ba tầng.

| Khái niệm | Go | TypeScript | SQL |
|---|---|---|---|
| Tên miền đầy đủ | `Domain` | `Domain` | `domains` |
| Tín hiệu chấm điểm | `Signal` | `Signal` | `signals` |
| Quyết định | `Decision` | `Decision` | `decisions` |
| Dữ liệu làm giàu | `Facts` | `DomainFacts` | `domain_facts` |
| Bản xuất bản | `Snapshot` | `Snapshot` | `snapshots` |

Không có chỗ nào gọi là `entry`, chỗ khác gọi `record`, chỗ khác nữa gọi `item` cho
cùng một thứ.

## 5. Kiểm thử

### Kim tự tháp

| Tầng | Phạm vi | Công cụ |
|---|---|---|
| Unit | Logic thuần, không I/O | `testing` + bảng dữ liệu |
| Giao thức | Bóc gói TZSP/DNS | gói tổng hợp dựng trong test |
| Tích hợp | Có CSDL thật | file SQLite tạm trong `t.TempDir()` |
| API | HTTP end-to-end | `httptest` + CSDL thật |
| Kiểu | Toàn bộ frontend | `tsc --noEmit` |

**Không mock CSDL.** Mock CSDL chỉ chứng minh được rằng mock hoạt động. SQLite mở một
file tạm trong vài mili giây, nên test tích hợp chạy nhanh ngang test unit và không
cần container.

### Bảng chấm điểm

Đây là nơi test có giá trị cao nhất, vì logic phức tạp và hậu quả sai lớn:

```go
{
    name:   "CNAME cloaking rõ ràng",
    domain: Domain{Name: "metrics.trangweb.vn", ETLD1: "trangweb.vn"},
    facts: Facts{
        CNAMEChain: []string{"cdn.eulerian.net", "x.eulerian.net"},
        ASN:        62597,
    },
    wantScore: 13.0,  // 6,0 cname + 4,0 asn + 3,0 keyword
    wantCat:   CategoryTracking,
    wantKinds: []string{KindCNAMEAdtech, KindASNAdtech, KindKeyword},
},
```

Mỗi lần sửa trọng số hoặc thêm tín hiệu, thêm ít nhất một case vào bảng này.

Ngoài ra có một test riêng khẳng định `Score` **thuần túy**: chạy hai trăm lần trên
cùng đầu vào phải cho cùng điểm, cùng phân loại, và cùng thứ tự tín hiệu. Không có
tính chất này thì bảng xem trước tác động khi đổi trọng số chỉ là ước lượng.

### Bốn bất biến

Bốn bất biến ở [06 §4](06-classification.md) có test riêng, đặt tên rõ:

```go
func TestInvariant_ManualDomainNeverRescored(t *testing.T)
func TestInvariant_AllowedNeverAutoChanges(t *testing.T)
func TestInvariant_EveryTransitionCreatesDecision(t *testing.T)
func TestInvariant_ProtectedDomainCannotBeBlocked(t *testing.T)
```

Đây là những thứ mà nếu hỏng, người dùng sẽ mất niềm tin vào cả hệ thống. **Test cho
chúng không được xóa hay sửa cho dễ pass.**

### Bóc gói

Bóc gói tin là chỗ dễ sai và khó gỡ lỗi trên dữ liệu thật, nên test dựng gói tổng hợp
đầy đủ từ TZSP xuống DNS, phủ IPv4, IPv6, thẻ VLAN, QinQ, và tên miền viết hoa. Kèm
theo một vòng lặp cắt cụt gói hợp lệ ở mọi vị trí để khẳng định gói méo bị từ chối gọn
gàng thay vì panic — cổng mirror nhận đủ thứ rác và không có cách nào kiểm soát đầu vào.

### Chạy test

```bash
go test ./... -race -count=1
go test ./internal/store/ -bench=. -benchtime=20x   # thông lượng ghi lô
cd web && npm run typecheck
```

## 6. Git

### Nhánh

```
main            luôn triển khai được
feat/…          tính năng
fix/…           sửa lỗi
chore/…         phụ trợ
```

### Commit theo Conventional Commits

```
feat(classify): thêm tín hiệu entropy cho subdomain ngẫu nhiên
fix(publish): không ghi file khi checksum không đổi
docs(api): bổ sung ví dụ cho bulk-decision
```

### Pull request

Mô tả PR phải có:

- Liên kết tới yêu cầu (`FR-3.2`) hoặc user story (`US-2`)
- Ảnh chụp màn hình nếu đổi giao diện
- Nếu đổi trọng số chấm điểm: kết quả bảng test trước và sau

## 7. Triển khai

### Docker

```bash
docker compose -f deploy/docker-compose.yml up -d
```

Một service, một volume. Cổng 8080/tcp cho giao diện và endpoint danh sách, cổng
37008/udp cho luồng mirror.

Ảnh dựng theo ba giai đoạn: Node biên dịch giao diện, Go biên dịch binary có nhúng
giao diện đó, và ảnh cuối là Alpine chỉ chứa hai file binary. Chạy dưới người dùng
không đặc quyền.

### Binary trực tiếp

```bash
make build
sudo install -m 755 bin/dnsguard bin/dnsguard-cli /usr/local/bin/
sudo useradd --system --home /var/lib/dnsguard --create-home dnsguard
sudo cp deploy/systemd/dnsguard.service /etc/systemd/system/
sudo systemctl enable --now dnsguard
```

### Trên máy ARM

```bash
make build-arm64
scp bin/dnsguard-arm64 user@host:/usr/local/bin/dnsguard
```

Binary tĩnh nên không cần cài gì thêm trên máy đích.

### Cấu hình thiết bị mạng

Gửi bản sao lưu lượng DNS sang DNSGuard:

```routeros
/tool sniffer set filter-port=53 filter-ip-protocol=udp \
    streaming-enabled=yes streaming-server=192.168.88.10:37008 \
    filter-stream=yes memory-limit=100KiB
/tool sniffer start
/system scheduler add name=sniffer-boot start-time=startup \
    on-event="/tool sniffer start"
```

Rồi trỏ adlist về danh sách đã xuất bản:

```routeros
/ip dns adlist add url="http://192.168.88.10:8080/lists/ads.txt" ssl-verify=no
/ip dns adlist add url="http://192.168.88.10:8080/lists/tracking.txt" ssl-verify=no
/ip dns adlist add url="http://192.168.88.10:8080/lists/malware.txt" ssl-verify=no
```

Dòng `scheduler` là bắt buộc. Không có nó, bộ mirror dừng sau mỗi lần thiết bị khởi
động lại, và hệ thống ngừng học **mà không có triệu chứng gì** — danh sách cũ vẫn chặn
bình thường. DNSGuard giám sát tuổi của truy vấn gần nhất và hiện băng cảnh báo đỏ
trên mọi trang khi vượt 600 giây.

### Dữ liệu tùy chọn

Hai tín hiệu cần dữ liệu tải riêng. Thiếu chúng thì hệ thống vẫn chạy, chỉ mất tín
hiệu tương ứng.

```bash
# Bảng tra ASN — cần cho tín hiệu asn_adtech
curl -Lo /var/lib/dnsguard/ip2asn.tsv.gz \
  https://iptoasn.com/data/ip2asn-combined.tsv.gz

# Danh sách Tranco — cần cho tín hiệu bảo vệ high_rank
# Đặt DNSGUARD_TRANCO_PATH trỏ tới file đã tải
```

Tín hiệu `high_rank` là tín hiệu bảo vệ mạnh nhất (−8,0). Không có nó, hệ thống mất
một lớp phòng vệ đáng kể trước việc chặn nhầm tên miền phổ biến — nên tải Tranco là
việc nên làm ngay sau khi cài.

## 8. Sao lưu

```bash
dnsguard-cli backup -o /backup/dnsguard-$(date +%F).db
```

Đưa vào cron hằng ngày, giữ 14 bản. Lệnh này gộp WAL vào file chính trước khi chép;
chép thẳng file khi đang chạy sẽ cho bản sao thiếu giao dịch.

Thư mục `lists` không cần sao lưu — sinh lại được bằng `dnsguard-cli publish`.

## 9. Gỡ lỗi

| Triệu chứng | Kiểm tra |
|---|---|
| Không có domain mới | `/health` → `querylog.last_event_age_s`; bộ mirror trên thiết bị mạng còn chạy không |
| Nhận gói nhưng không có domain | `/health` → `querylog.decode_errors`; kiểm tra filter của sniffer có đúng cổng 53 không |
| Số gói bị bỏ tăng | `querylog.packets_dropped` — CSDL không theo kịp; kiểm tra tốc độ đĩa |
| Job không chạy | `GET /api/v1/jobs/:id`, hoặc bảng `jobs` cột `state` và `error` |
| Điểm số bất ngờ | `GET /api/v1/domains/:id` xem mảng `signals` — luôn cho biết chính xác vì sao |
| Xuất bản không chạy | Bảng `snapshots`; nếu checksum không đổi thì đúng là không ghi |
| Danh sách rỗng sau đồng bộ | Nguồn có thể đã bị chặn vì sụt giảm bất thường — xem `last_status` |
| Thiết bị mạng không cập nhật | `/ip dns adlist print detail`; thử `/tool fetch` thủ công |
| CSDL phình to | `DNSGUARD_LOG_RETENTION_DAYS`; job `retention` chạy hằng ngày |

Bật log chi tiết bằng `DNSGUARD_LOG_LEVEL=debug`. Log là JSON cấu trúc, lọc bằng `jq`:

```bash
docker compose logs dnsguard | jq 'select(.level == "ERROR")'
```
