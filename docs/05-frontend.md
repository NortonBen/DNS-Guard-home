# 05 — Frontend

React 19 + TypeScript 5.6 + Vite 6.

## 1. Thư viện

React 19 + TypeScript 5.7 + Vite 6.

| Mục đích | Chọn | Ghi chú |
|---|---|---|
| Định tuyến | TanStack Router | Tham số URL có kiểu, định nghĩa route bằng code |
| Dữ liệu máy chủ | TanStack Query v5 | Cache, vô hiệu hóa, cập nhật lạc quan |
| Ảo hóa danh sách | `@tanstack/react-virtual` | Bắt buộc, có bảng nửa triệu dòng |
| Style | Tailwind CSS v4 | Qua plugin `@tailwindcss/vite` |
| Biểu đồ | Recharts | Nạp động, chỉ dùng ở dashboard |
| Đồ thị quan hệ | tự cài đặt | Xem ghi chú bên dưới |

Danh sách này cố ý ngắn. Ba thứ **không** dùng, và lý do:

**Không dùng thư viện component.** Các thành phần giao diện ở đây đơn giản — nút, thẻ,
huy hiệu, khung xương chờ — và viết tay khoảng 150 dòng trong
`components/ui/primitives.tsx`. Một bộ component đầy đủ mang theo nhiều thứ không dùng
tới và một lớp API nữa phải học.

**Không dùng thư viện đồ thị.** Đồ thị quan hệ tối đa vài trăm nút, còn một thư viện
đồ thị đầy đủ nặng hơn toàn bộ phần còn lại của bundle cộng lại. Mô phỏng lực ở dạng
đơn giản nhất — đẩy nhau theo nghịch đảo bình phương, cạnh kéo như lò xo, dừng sau
220 bước — gọn trong một file và cho kết quả đủ tốt ở quy mô này.

**Không dùng thư viện phím tắt và thư viện form.** Màn duyệt cần đúng một trình xử lý
`keydown` ở mức cửa sổ; form ở đây không có validation phức tạp nào mà backend chưa
làm. Cả hai thư viện đó sẽ chỉ là một lớp gián tiếp.

Không dùng thư viện quản lý state toàn cục. Gần như mọi state là state máy chủ và
thuộc về TanStack Query; phần còn lại là state cục bộ của component hoặc nằm trong URL.

### Ngân sách bundle

| Phần | gzip |
|---|---|
| Bundle ban đầu (JS + CSS) | ~112 KB |
| Biểu đồ (nạp động ở dashboard) | ~111 KB |
| Mỗi màn hình còn lại | 1–10 KB |

Ngân sách là 300 KB gzip cho bundle ban đầu. Mọi màn hình ngoài dashboard và đăng nhập
đều nạp động, nên phần lớn phiên làm việc chỉ tải một phần nhỏ.

## 2. Cây thư mục

```
web/
├── src/
│   ├── main.tsx
│   ├── router.tsx               định nghĩa route bằng code, màn hình nạp động
│   ├── styles.css               Tailwind v4 + biến thể dark thủ công
│   ├── api/
│   │   ├── client.ts            bọc fetch: CSRF, lỗi có kiểu, xử lý 401
│   │   ├── types.ts             kiểu phản chiếu struct Go
│   │   └── hooks.ts             toàn bộ hook TanStack Query
│   ├── components/
│   │   ├── ui/primitives.tsx    Button, Card, StatusBadge, Skeleton…
│   │   ├── ui/logo.tsx          biểu tượng nội tuyến + chữ DNSGuard
│   │   ├── layout/app-shell.tsx điều hướng, băng cảnh báo sức khỏe, chuyển sáng/tối
│   │   ├── domain/
│   │   │   ├── signal-badges.tsx
│   │   │   ├── cname-chain.tsx
│   │   │   ├── relation-graph.tsx
│   │   │   └── decision-history.tsx
│   │   └── charts/query-chart.tsx
│   ├── routes/                  một file mỗi màn hình
│   │   ├── dashboard.tsx  triage.tsx  domains.tsx  domain-detail.tsx
│   │   ├── categories.tsx manual.tsx  sources.tsx  publish.tsx
│   │   └── lookup.tsx     login.tsx
│   └── lib/
│       ├── format.ts            số, ngày giờ, tên miền theo quy ước Việt Nam
│       └── strings.ts           chuỗi giao diện gom một chỗ
├── public/                      chép nguyên trạng vào bản build
│   ├── favicon.svg  favicon.ico
│   ├── apple-touch-icon.png  icon-192.png  icon-512.png
│   ├── site.webmanifest
│   └── brand/                   file gốc của bộ nhận diện
├── index.html
├── vite.config.ts               xuất vào ../internal/web/assets
└── package.json
```

Kết quả biên dịch đi thẳng vào `internal/web/assets/` để gói Go nhúng nó vào binary.

## 3. Quản lý state

### Ba loại state, ba nơi chứa

| Loại | Ví dụ | Nơi chứa |
|---|---|---|
| State máy chủ | danh sách domain, thống kê, phân loại | TanStack Query |
| State điều hướng | bộ lọc, sắp xếp, trang hiện tại, tab | URL search params |
| State giao diện | modal đang mở, dòng đang chọn | `useState` cục bộ |

Đặt bộ lọc vào URL là chủ ý: người quản trị cần gửi link "xem cái này" cho chính mình
sau, hoặc bookmark một truy vấn hay dùng. Nó cũng làm nút back của trình duyệt hoạt
động đúng.

### Khóa cache

```ts
export const qk = {
  domains: (filters: DomainFilters) => ['domains', filters] as const,
  domain:  (id: number)             => ['domain', id] as const,
  graph:   (id: number, o: GraphOpts) => ['domain', id, 'graph', o] as const,
  stats:   (range: TimeRange)       => ['stats', range] as const,
  categories: ()                    => ['categories'] as const,
} as const;
```

Sau một quyết định, invalidate `['domains']` và `['domain', id]`, không invalidate
toàn bộ.

### Optimistic update

Bắt buộc ở màn Triage — thao tác phím phải phản hồi tức thì:

```ts
const decide = useMutation({
  mutationFn: postDecision,
  onMutate: async (vars) => {
    await qc.cancelQueries({ queryKey: qk.domain(vars.id) });
    const prev = qc.getQueryData(qk.domain(vars.id));
    qc.setQueryData(qk.domain(vars.id), (old) => ({
      ...old, status: vars.action === 'block' ? 'blocked' : 'allowed',
    }));
    return { prev };
  },
  onError: (_e, vars, ctx) => {
    qc.setQueryData(qk.domain(vars.id), ctx.prev);
    toast.error('Không lưu được quyết định');
  },
  onSettled: (_d, _e, vars) => {
    qc.invalidateQueries({ queryKey: qk.domain(vars.id) });
  },
});
```

## 4. Các màn hình

### 4.1 Dashboard `/`

Trả lời một câu: hệ thống có đang khỏe không, và có gì cần tôi làm.

**Bố cục**

```
┌─────────────┬─────────────┬─────────────┬─────────────┐
│ Truy vấn 24h│ Tỉ lệ chặn  │ Chờ duyệt   │ Sức khỏe    │
│   198.432   │    20,8%    │     23      │  suy giảm   │
└─────────────┴─────────────┴─────────────┴─────────────┘
┌───────────────────────────────────────────────────────┐
│ Biểu đồ truy vấn / chặn theo giờ, 24h                 │
└───────────────────────────────────────────────────────┘
┌───────────────────────┬───────────────────────────────┐
│ Top domain bị chặn    │ Top client                    │
└───────────────────────┴───────────────────────────────┘
┌───────────────────────────────────────────────────────┐
│ Yêu cầu mở chặn đang chờ (nếu có)                     │
└───────────────────────────────────────────────────────┘
```

- Thẻ **Chờ duyệt** bấm vào là sang Triage. Đây là lối vào chính của người dùng hằng ngày.
- Thẻ **Sức khỏe** đổi màu theo `/health`. Khi `degraded`, hiện luôn nguyên nhân chứ không bắt bấm vào mới thấy.
- Nếu `querylog.last_event_age_s > 600`, hiện banner đỏ trên cùng: *"Không nhận được truy vấn mới trong N phút — kiểm tra sniffer trên MikroTik"*. Đây là lỗi hay gặp nhất và im lặng nhất.

### 4.2 Triage `/triage`

Màn hình được dùng nhiều nhất. Thiết kế cho tốc độ, không cho vẻ đẹp.

**Bố cục hai cột.** Trái là danh sách ứng viên, phải là chi tiết mục đang chọn. Không
điều hướng, không modal — mọi thứ trong một màn hình.

**Phím tắt**

| Phím | Hành động |
|---|---|
| `J` / `↓` | Mục kế tiếp |
| `K` / `↑` | Mục trước |
| `B` | Chặn, sang mục kế tiếp |
| `A` | Cho qua, sang mục kế tiếp |
| `I` | Bỏ qua, sang mục kế tiếp |
| `Enter` | Mở trang chi tiết đầy đủ |
| `Ctrl+Z` | Hoàn tác trong 10 giây |
| `?` | Bảng phím tắt |

**Chi tiết bên phải** hiển thị đủ để quyết định mà không cần mở trang khác: điểm số,
các huy hiệu tín hiệu kèm giá trị cụ thể, chuỗi CNAME, ASN và tổ chức, lưu lượng,
danh sách client, và các subdomain cùng gốc.

**Hoàn tác** làm bằng cách hoãn: giữ hành động trong hàng đợi cục bộ 10 giây trước khi
gửi, hiện toast đếm ngược. Nếu người dùng bấm `Ctrl+Z`, hủy khỏi hàng đợi. Cách này
đơn giản hơn nhiều so với đảo ngược một quyết định đã ghi vào nhật ký.

**Chọn nhiều** bằng `Shift+J/K`, sau đó `B` áp dụng cho cả nhóm qua `bulk-decision`.

### 4.3 Danh sách domain `/domains`

Bảng ảo hóa cho toàn bộ CSDL.

**Cột:** tên, trạng thái, phân loại, điểm, truy vấn, client, thấy lần cuối, nguồn gốc.

**Bộ lọc** trên thanh công cụ, đồng bộ với URL: trạng thái (nhiều lựa chọn), phân
loại, khoảng điểm, khoảng thời gian, ô tìm kiếm.

**Tìm kiếm** hỗ trợ ba chế độ, tự nhận dạng:
- `doubleclick` → chứa chuỗi
- `*.eulerian.net` → khớp hậu tố
- `/^ad[0-9]+\./` → biểu thức chính quy

**Yêu cầu hiệu năng:** cuộn mượt với 500k dòng. Bắt buộc dùng `react-virtual`, chỉ
render dòng trong khung nhìn. Không bao giờ tải toàn bộ vào bộ nhớ — dùng
`useInfiniteQuery` với con trỏ.

### 4.4 Chi tiết domain `/domains/:id`

Màn hình quan trọng nhất về mặt sản phẩm (US-2).

```
┌───────────────────────────────────────────────────────┐
│ metrics.trangweb.vn        [staging]  điểm 7,5        │
│ [Chặn] [Cho qua] [Bỏ qua]        [Chặn cả cụm →]      │
└───────────────────────────────────────────────────────┘
┌─────────────────────────┬─────────────────────────────┐
│ Tín hiệu                │ Hạ tầng                     │
│ • CNAME adtech    +6,0  │ IP    185.11.22.33          │
│ • Third-party     +2,5  │ ASN   62597 Adform (DK)     │
│                         │ Tuổi  2704 ngày             │
│                         │ Tranco  không xếp hạng      │
├─────────────────────────┴─────────────────────────────┤
│ Chuỗi CNAME                                           │
│ metrics.trangweb.vn → cdn.eulerian.net → x.eulerian…  │
│                                          ▲ adtech     │
├───────────────────────────────────────────────────────┤
│ Đồ thị domain liên quan          [lọc theo loại cạnh] │
│                                                       │
│         ○ cùng cert      ● gốc                        │
│              ╲          ╱                             │
│               ●────────●  cname                       │
├───────────────────────────────────────────────────────┤
│ Subdomain cùng gốc          │ Truy vấn theo thời gian │
├───────────────────────────────────────────────────────┤
│ Lịch sử quyết định                                    │
└───────────────────────────────────────────────────────┘
```

**Đồ thị quan hệ** là thành phần phức tạp nhất:

- Màu nút theo trạng thái: đỏ `blocked`, vàng `staging`, xanh `allowed`, xám `new`
- Kiểu cạnh theo quan hệ: liền `cname_to`, đứt `same_cert`, chấm `same_asn`, mảnh `co_occurs`
- Bấm nút → chọn; `Shift`+bấm → chọn nhiều
- Nút **Chặn cả cụm** áp dụng cho mọi nút đang chọn (FR-5.6)
- Khi `truncated`, hiện rõ *"hiển thị 60 / 143 nút liên quan"* kèm nút mở rộng
- Có nút tắt đồ thị cho ai thấy rối

**Tải theo lớp:** thông tin cơ bản và tín hiệu tải trước và render ngay; đồ thị và
timeline tải sau, có skeleton. Không để cả trang chờ phần chậm nhất.

**Thẻ "Địa chỉ quan sát trên dây".** Địa chỉ lấy từ bản ghi trả lời DNS bắt được, kèm
mạng, TTL nhỏ nhất từng thấy, và nhãn đỏ khi khớp danh sách hạ tầng độc hại.

Đừng nhầm với dòng IP ở thẻ "Hạ tầng": chỗ đó là kết quả DNSGuard tự phân giải lúc làm
giàu, còn đây là câu trả lời thiết bị trong mạng thật sự nhận được. Hai chỗ lệch nhau
là bình thường với CDN, và chính khoảng lệch đó mới đáng để ý.

### 4.5 Phân loại `/categories`

Danh sách phân loại, sửa được ngưỡng điểm, bật/tắt, đổi màu. Mỗi dòng hiện số domain
đang thuộc phân loại đó và đường dẫn xuất bản.

Bên dưới là khu vực **trọng số tín hiệu**. Sửa xong bấm **Xem tác động** →
`PUT /scoring/weights` với `dry_run: true` → hiện bảng "sẽ chặn thêm 47, gỡ 3" kèm
mẫu. Chỉ khi đó nút **Áp dụng** mới bật.

Đây là chỗ dễ gây thiệt hại nhất trong toàn bộ ứng dụng, nên luồng bắt buộc phải có
bước xem trước, không cho phép bỏ qua.

### 4.6 Thêm thủ công `/manual`

Ô textarea lớn, mỗi dòng một domain. Vừa gõ vừa gọi `dry_run` có debounce 500 ms.

Bảng xem trước tô màu theo `action`:

| Màu | `action` | Ý nghĩa |
|---|---|---|
| Xanh | `created` | Sẽ thêm mới |
| Xám | `duplicate` | Đã có, bỏ qua |
| Vàng | `rejected` + `high_rank` | Thứ hạng cao, cần `force` |
| Đỏ | `rejected` + khác | Không hợp lệ hoặc bị bảo vệ |

Bỏ chọn từng dòng được. Wildcard hiện thêm `matches_known: 12` — số domain đã biết sẽ
bị ảnh hưởng, giúp người dùng hình dung phạm vi trước khi lưu.

Trường **lý do** bắt buộc. Nó đi vào nhật ký và là thứ duy nhất giải thích được quyết
định sau này.

### 4.7 Nguồn ngoài `/sources`

Bảng nguồn: tên, URL, phân loại, số mục, lần đồng bộ cuối, trạng thái. Nguồn lỗi tô
đỏ kèm thông báo.

Nút **Thêm từ danh mục** mở dialog liệt kê `GET /sources/registry`, lọc theo tag.

Tab **Chồng lấn** hiển thị ma trận Jaccard giữa các nguồn cộng số lượng "chỉ có ở
nguồn này". Trả lời câu hỏi thực tế: có nên bỏ bớt nguồn nào không.

### 4.8 Xuất bản `/publish`

Danh sách snapshot theo phân loại. Mỗi dòng: thời điểm, số lượng, thay đổi so với bản
trước (`+149 / −8`), checksum rút gọn, người thực hiện.

Chọn hai dòng → **So sánh** → hiện danh sách thêm và bớt.

Nút **Xuất bản ngay** và **Rollback** (có hộp thoại xác nhận, phải gõ tên phân loại).

Trên cùng hiển thị URL để dán vào MikroTik, kèm nút sao chép:

```
http://192.168.88.10:8080/lists/ads.txt
```

Và một dòng nhắc: *"MikroTik kiểm tra cập nhật mỗi 4 giờ. Muốn áp dụng ngay, chạy
`/ip dns adlist reload` trên router."*

### 4.9 Bản đồ quan hệ `/graph`

Đồ thị quan hệ của toàn mạng, khác trang chi tiết domain ở câu hỏi nó trả lời: trang
chi tiết hỏi "domain này liên quan tới gì", màn này hỏi "mạng này có những cụm hạ tầng
nào". Lọc theo loại cạnh, trạng thái, số quan hệ tối thiểu và số nút tối đa.

Vẽ bằng canvas chứ không phải SVG, và lực đẩy tính qua lưới không gian thay vì duyệt
mọi cặp: ở ba trăm nút thì duyệt mọi cặp là 45.000 phép tính mỗi bước.

Nút chọn theo bậc giảm dần để các hub nổi lên trước, và chỉ giữ cạnh có cả hai đầu
nằm trong tập nút đã chọn. Khi bị cắt bớt thì nói ra bằng một dòng cảnh báo — im lặng
cắt sẽ khiến người xem tưởng mình đang nhìn toàn bộ.

Bậc đếm quan hệ theo **cả hai chiều**. `cname_to` là loại duy nhất lưu một chiều, nên
đếm riêng chiều đi ra sẽ cho một hub adtech có ba trăm domain trỏ vào bậc bằng không —
đúng nút đáng xem nhất lại bị bộ lọc "số quan hệ tối thiểu" giấu đi.

Nhãn đặt theo **va chạm thật** chứ không theo ngưỡng bậc: đo bề rộng từng chuỗi rồi bỏ
qua nhãn nào đè lên nhãn đã vẽ. Ngưỡng theo bậc không biết hai nhãn có chồng nhau hay
không, nên ở một mạng nhiều hub thì hàng trăm nhãn cùng vượt ngưỡng và chồng thành đám
chữ không đọc được. Cách này cũng tự tốt lên khi phóng to, vì càng phóng càng nhiều chỗ.

Bấm là **chọn**, không phải mở trang. Nút đang chọn cùng hàng xóm được tô sáng, phần
còn lại mờ đi, và một bảng bên dưới liệt kê hàng xóm để bấm chuyển tiếp mà không rời
màn hình. Mở trang chi tiết là một nút riêng, hoặc bấm đúp. Rời trang ngay khi chạm vào
một chấm khiến không thể xem xét đồ thị, mà xem xét mới là việc của màn này.

### 4.10 Tài nguyên `/resources`

RAM và CPU của chính DNSGuard, chọn khoảng từ 12 giờ tới 30 ngày. Hai biểu đồ: CPU
(trung bình và đỉnh) và bộ nhớ (thường trú, đỉnh thường trú, heap Go).

Câu hỏi màn này trả lời là dịch vụ có phình bộ nhớ theo thời gian không, và có lúc nào
ngốn CPU bất thường không. Cả hai chỉ thấy được khi nhìn nhiều ngày, nên ảnh chụp một
thời điểm ở màn Cài đặt không thay thế được.

Hai đường bộ nhớ tách nhau ra là dấu hiệu đáng chú ý: heap phẳng mà thường trú tăng
đều nghĩa là runtime đang giữ lại bộ nhớ đã trả, chứ không phải mã nguồn rò rỉ.

Khi dữ liệu không phủ hết khoảng đã chọn thì nói ra, vì một biểu đồ ngắn hơn mong đợi
trông giống hệt mất dữ liệu.

### 4.11 Điều tra IP `/ip-forensics`

Ba việc luôn đi cùng nhau trong một vụ điều tra, gộp vào một màn để không phải chép
địa chỉ qua lại giữa các trang:

1. **Cảnh báo hạ tầng độc hại** — domain đã phân giải tới địa chỉ nằm trong danh sách.
   Mỗi dòng có dải đã khớp, vị trí mạng, và cờ *nhịp đều* lấy từ tín hiệu beacon sẵn
   có. Bấm vào địa chỉ để chuyển thẳng xuống ô tra ngược.
2. **Tra ngược một địa chỉ** — địa chỉ này phục vụ những domain nào, và thiết bị nào
   đã phân giải chúng trong khoảng ánh xạ còn hiệu lực.
3. **Xuất hồ sơ điều tra** — tải `.zip` cho một khoảng ngày.

Danh sách cảnh báo rỗng có **hai nguyên nhân trái ngược nhau**: không có gì đáng báo,
hoặc chưa tải danh sách về nên không đối chiếu được. Màn này phân biệt hai trường hợp
đó bằng `list_loaded`; gộp chúng thành một dòng "không có cảnh báo" sẽ khiến người vận
hành yên tâm nhầm.

Giới hạn của dữ liệu nằm ngay dưới kết quả, không giấu trong tài liệu: đây là chỗ dễ
kết luận quá tay nhất. Một lượt phân giải không chứng minh có kết nối thật, và thiết bị
dùng DoH/DoT không xuất hiện ở đây chút nào.

Nút tải hồ sơ là thẻ `<a download>` chứ không phải `fetch` rồi dựng blob: hồ sơ sinh
theo luồng và có thể lên tới hàng trăm megabyte, mà dựng blob sẽ giữ toàn bộ trong bộ
nhớ trình duyệt trước khi lưu được dòng nào.

### 4.12 Cài đặt `/settings`

Xếp theo mức nguy hiểm giảm dần: danh sách bảo vệ, vòng đời, phân tích ngoài (gồm
công tắc HTTP và khóa API VirusTotal), địa chỉ xuất bản, dữ liệu tra cứu, thông tin
hệ thống.

Ô nhập khóa VirusTotal là loại password và không bao giờ được điền sẵn — máy chủ chỉ
trả về bốn ký tự cuối, nên không có gì để điền. Đổi khóa thì phải dán lại cả khóa.

Địa chỉ xuất bản có ba lựa chọn: `0.0.0.0` (kết nối hỏng ngay), `127.0.0.1` (quay về
chính máy truy vấn), hoặc một địa chỉ tự nhập. Khác biệt không nhỏ như vẻ ngoài:
`127.0.0.1` khiến máy khách tự gọi về chính nó và ngồi chờ hết thời gian nếu không có
gì lắng nghe, thấy rõ nhất trên điện thoại nơi ứng dụng treo thay vì báo lỗi ngay.

### 4.13 Hỏi AI `/ai`

Năm thẻ trên một màn: **Hỏi AI**, **Lịch sử**, **Nhà cung cấp**, **Skill**,
**Công cụ & MCP**. Ba thẻ cuối chỉ hiện với quản trị viên.

Gom vào một màn thay vì năm mục trên thanh điều hướng: đây là tính năng tuỳ chọn, và
làm menu dài gấp rưỡi cho thứ có thể chưa bật là cái giá sai.

Mỗi câu trả lời kèm phần **dấu vết gọi công cụ** gập lại được. Gập vì phần lớn lượt
đọc không cần tới; mở được vì khi câu trả lời có vẻ sai thì đây là chỗ duy nhất cho
biết model đã đọc gì. Không có phần này, một câu trả lời nghe hợp lý về domain không
tồn tại trong mạng sẽ không bị phát hiện.

Thẻ Lịch sử tải hai bước: bảng chỉ hiện số đo, bấm vào một dòng mới tải prompt và
phản hồi thô. Prompt của một lô bốn mươi domain nặng vài kilobyte, và tải hai mươi
lăm cái cùng lúc chỉ để hiện một bảng là lãng phí thuần túy.

Ô nhập khóa API là loại password và không bao giờ được điền sẵn — máy chủ chỉ trả về
`configured: true/false`, nên không có gì để điền.

Màn chi tiết domain có thêm thẻ **Kết luận của AI** kèm nút *Hỏi lại AI*. Thẻ tự ẩn
khi chưa cấu hình khóa: một thẻ rỗng ở màn hình quan trọng nhất chỉ làm loãng thứ
người dùng đang cần đọc.

## 5. Nhận diện

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="../web/public/brand/logo-dark.svg">
    <img src="../web/public/brand/logo.svg" alt="DNSGuard" width="280">
  </picture>
</p>

Biểu tượng là một chiếc khiên bọc lấy cây phân cấp ba nút. Khiên là phần *guard*; cây
ba nút là phần *lấy domain làm trung tâm* — thứ phân biệt DNSGuard với một resolver.
Không dùng hình ổ khóa hay dấu cấm: DNSGuard không chặn, nó phân loại và ra quyết định.

Màu lấy thẳng từ thang màu giao diện, nên logo không cần bảng màu riêng:

| Dùng ở | Màu |
|---|---|
| Chuyển sắc của khiên | `sky-400` → `sky-500` → `sky-700` |
| Đồ thị bên trong khiên | trắng |
| Chữ "DNS" | `slate-900`, nền tối dùng `slate-50` |
| Chữ "Guard" | `sky-600`, nền tối dùng `sky-400` |

Chữ "Guard" đổi sang `sky-400` ở chế độ tối vì `sky-600` trên nền `slate-900` chỉ đạt
tương phản 3,5:1, dưới ngưỡng 4,5:1 của chính tài liệu này.

### File

| File | Dùng vào việc gì |
|---|---|
| `src/components/ui/logo.tsx` | biểu tượng vẽ nội tuyến trong giao diện — thanh bên, thanh trên cùng, màn hình đăng nhập |
| `public/brand/logo-mark.svg` | biểu tượng đứng một mình |
| `public/brand/logo.svg`, `logo-dark.svg` | biểu tượng kèm chữ, cho tài liệu và README |
| `public/brand/app-icon.svg` | bản vuông tràn viền, nền đặc — file gốc của các PNG |
| `public/favicon.svg` | favicon, nét đậm hơn để còn đọc được ở 16px |
| `public/favicon.ico` | 16/32/48px, đỡ cho trình duyệt cũ chưa đọc favicon SVG |
| `public/apple-touch-icon.png`, `icon-192.png`, `icon-512.png` | biểu tượng khi cài lên màn hình chính |
| `public/site.webmanifest` | khai báo tên, màu và biểu tượng cho trình duyệt |

Trong giao diện, biểu tượng vẽ nội tuyến chứ không nhúng `<img>`: không tốn thêm một
lượt tải và nét vẽ luôn sắc ở mọi tỉ lệ màn hình. Đổi lại, hình nằm ở hai nơi — sửa
`logo.tsx` thì phải sửa cả `public/`.

Các file PNG sinh ra từ `app-icon.svg`, không sửa tay:

```bash
for s in 180:apple-touch-icon 192:icon-192 512:icon-512; do
  rsvg-convert -w ${s%%:*} -h ${s%%:*} web/public/brand/app-icon.svg -o web/public/${s##*:}.png
done
```

## 6. Hiển thị và ngôn ngữ

- Giao diện tiếng Việt. Chuỗi tập trung ở `src/lib/strings.ts`, chuẩn bị sẵn cho i18n nhưng chưa cần thư viện i18n.
- Thời gian hiển thị theo múi giờ trình duyệt; tooltip hiện UTC.
- Số lượng lớn dùng dấu chấm phân cách nghìn theo quy ước Việt Nam: `182.451`.
- Tên miền dùng font monospace ở mọi nơi — dễ so sánh trực quan và tránh nhầm ký tự giống nhau.
- Chế độ tối theo `prefers-color-scheme`, có nút chuyển thủ công lưu trong `localStorage`.

## 7. Tiếp cận

- Mọi hành động có phím tắt cũng phải có nút bấm được
- Focus ring rõ ràng, không tắt outline
- Bảng dùng markup `<table>` thật với `<th scope>`, không phải div
- Đồ thị quan hệ có bảng tương đương bên dưới cho người dùng trình đọc màn hình
- Tương phản màu tối thiểu 4,5:1
- Tôn trọng `prefers-reduced-motion`

## 8. Hiệu năng

| Chỉ tiêu | Ngân sách |
|---|---|
| Bundle ban đầu | < 300 KB gzip |
| Time to interactive (LAN) | < 1,5 s |
| Cuộn bảng 500k dòng | 60 fps |
| Vẽ đồ thị 100 nút | < 300 ms |

Biện pháp:

- Chia mã theo route bằng `lazyRouteComponent`
- Đồ thị quan hệ nạp động, chỉ khi mở trang chi tiết
- Recharts nạp động
- Ảo hóa mọi danh sách dài
- `staleTime` 30 giây cho thống kê, 5 phút cho phân loại
