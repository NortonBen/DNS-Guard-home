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

## 5. Hiển thị và ngôn ngữ

- Giao diện tiếng Việt. Chuỗi tập trung ở `src/lib/strings.ts`, chuẩn bị sẵn cho i18n nhưng chưa cần thư viện i18n.
- Thời gian hiển thị theo múi giờ trình duyệt; tooltip hiện UTC.
- Số lượng lớn dùng dấu chấm phân cách nghìn theo quy ước Việt Nam: `182.451`.
- Tên miền dùng font monospace ở mọi nơi — dễ so sánh trực quan và tránh nhầm ký tự giống nhau.
- Chế độ tối theo `prefers-color-scheme`, có nút chuyển thủ công lưu trong `localStorage`.

## 6. Tiếp cận

- Mọi hành động có phím tắt cũng phải có nút bấm được
- Focus ring rõ ràng, không tắt outline
- Bảng dùng markup `<table>` thật với `<th scope>`, không phải div
- Đồ thị quan hệ có bảng tương đương bên dưới cho người dùng trình đọc màn hình
- Tương phản màu tối thiểu 4,5:1
- Tôn trọng `prefers-reduced-motion`

## 7. Hiệu năng

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
