# 04 — Đặc tả API

Base URL: `/api/v1`. Toàn bộ request và response dùng `application/json` trừ endpoint
xuất bản.

## 1. Quy ước chung

### Xác thực

Cookie phiên `dnsguard_session`, `HttpOnly`, `SameSite=Lax`. Mọi endpoint trừ
`/auth/login`, `/health` và `/lists/*` đều yêu cầu phiên hợp lệ.

Request thay đổi trạng thái (POST, PATCH, DELETE) phải kèm header `X-CSRF-Token` lấy
từ `GET /auth/me`.

### Định dạng lỗi

Mọi lỗi trả về cùng cấu trúc:

```json
{
  "error": {
    "code": "domain_protected",
    "message": "Domain nằm trong danh sách bảo vệ và không thể chặn",
    "details": { "domain": "vnpayment.vn", "rule": "hard_allow" }
  }
}
```

| HTTP | `code` tiêu biểu | Khi nào |
|---|---|---|
| 400 | `invalid_input`, `invalid_domain` | Đầu vào sai định dạng |
| 401 | `unauthenticated` | Thiếu phiên hoặc phiên hết hạn |
| 403 | `forbidden`, `domain_protected` | Không đủ quyền, hoặc vi phạm quy tắc nghiệp vụ |
| 404 | `not_found` | Không tìm thấy tài nguyên |
| 409 | `conflict`, `already_exists` | Xung đột trạng thái |
| 422 | `validation_failed` | Hợp lệ về cú pháp nhưng sai nghiệp vụ |
| 429 | `rate_limited` | Vượt giới hạn |
| 500 | `internal` | Lỗi máy chủ, đã ghi log kèm trace id |

Response lỗi 500 kèm `details.trace_id` để đối chiếu với log.

### Phân trang

Con trỏ, không phải offset — bảng domain lớn và offset lớn rất chậm.

```
GET /api/v1/domains?limit=50&cursor=eyJzIjo1LjUsImkiOjEyMzR9
```

```json
{
  "items": [ ... ],
  "next_cursor": "eyJzIjo0LjIsImkiOjU2Nzh9",
  "has_more": true
}
```

`limit` mặc định 50, tối đa 200. Con trỏ là base64 của `{sort_key, id}`, đục với client.

### Sắp xếp và lọc

```
?sort=score:desc,last_seen:desc
?status=staging,new
?category=ads,tracking
?q=doubleclick
?min_score=5.0
?seen_after=2026-09-01T00:00:00Z
```

Nhiều giá trị cho một tham số ngăn bằng dấu phẩy, hiểu là OR. Nhiều tham số khác nhau
hiểu là AND.

---

## 2. Xác thực

### `POST /auth/login`

```json
{ "username": "admin", "password": "..." }
```

**200**
```json
{
  "user": { "id": 1, "username": "admin", "role": "admin" },
  "csrf_token": "…",
  "expires_at": "2026-09-14T10:00:00Z"
}
```

**401** `invalid_credentials`. Trả về sau độ trễ cố định 500 ms bất kể lý do, để không
lộ tài khoản nào tồn tại.

Giới hạn: 5 lần thất bại / IP / 15 phút.

### `POST /auth/logout` → **204**

### `GET /auth/me`

```json
{
  "user": { "id": 1, "username": "admin", "role": "admin" },
  "csrf_token": "…"
}
```

---

## 3. Domain

### `GET /domains`

Danh sách có lọc và phân trang. Tham số như §1.

```json
{
  "items": [
    {
      "id": 4821,
      "name": "metrics.trangweb.vn",
      "etld1": "trangweb.vn",
      "status": "staging",
      "origin": "discovered",
      "category": { "key": "tracking", "label_vi": "Theo dõi" },
      "score": 7.5,
      "confidence": 0.82,
      "is_manual": false,
      "query_count": 1284,
      "client_count": 7,
      "first_seen": "2026-08-28T03:12:00Z",
      "last_seen": "2026-09-07T09:41:00Z",
      "signals": [
        { "kind": "cname_adtech", "weight": 6.0, "detail": { "cname": "x.eulerian.net" } },
        { "kind": "third_party",  "weight": 2.5, "detail": { "ratio": 0.91 } }
      ]
    }
  ],
  "next_cursor": "…",
  "has_more": true
}
```

`signals` trả kèm trong danh sách vì màn hình Triage cần hiển thị ngay, và số lượng
tín hiệu mỗi domain luôn nhỏ (dưới 10).

### `GET /domains/:id`

Chi tiết đầy đủ cho US-2. Đây là endpoint nặng nhất, có ngân sách 500 ms.

```json
{
  "domain": { /* như trên */ },
  "facts": {
    "dns": {
      "cname_chain": ["metrics.trangweb.vn", "cdn.eulerian.net", "x.eulerian.net"],
      "a": ["185.11.22.33"],
      "fetched_at": "2026-09-07T04:00:00Z"
    },
    "asn": { "asn": 62597, "org": "Adform", "country": "DK" },
    "cert": { "issuer": "Let's Encrypt", "sans": ["*.eulerian.net"], "not_after": "2026-11-01" },
    "rdap": { "registered_at": "2019-04-12", "registrar": "Gandi", "age_days": 2704 },
    "rank": { "tranco": null }
  },
  "public_lists": [
    { "source_id": 1, "name": "Hagezi Pro", "present": true }
  ],
  "siblings": [
    { "id": 4822, "name": "cdn.trangweb.vn", "query_count": 4021, "status": "allowed" }
  ],
  "history": [
    {
      "action": "stage",
      "actor_label": "system",
      "reason": "score 7.5 ≥ threshold 5.5",
      "created_at": "2026-08-30T03:30:00Z"
    }
  ]
}
```

`siblings` là subdomain cùng `etld1` đã thấy trong mạng, giới hạn 50 dòng đầu theo
lưu lượng.

### `GET /domains/:id/graph`

Đồ thị quan hệ (FR-5.4). Tách riêng khỏi `GET /domains/:id` vì tốn kém và không phải
lúc nào cũng cần.

```
?depth=1&kinds=cname_to,same_asn,same_cert,co_occurs&min_strength=0.3&limit=60
```

```json
{
  "nodes": [
    { "id": 4821, "name": "metrics.trangweb.vn", "status": "staging", "category": "tracking", "root": true },
    { "id": 9012, "name": "x.eulerian.net",      "status": "blocked", "category": "tracking" }
  ],
  "edges": [
    { "from": 4821, "to": 9012, "kind": "cname_to", "strength": 1.0 }
  ],
  "truncated": false
}
```

`depth` tối đa 2. `truncated` bằng `true` khi số nút vượt `limit` — UI phải hiển thị
điều này chứ không im lặng cắt bớt.

### `GET /domains/:id/timeline`

```
?from=2026-09-01T00:00:00Z&to=2026-09-07T00:00:00Z&bucket=hour
```

```json
{
  "buckets": [
    { "t": "2026-09-07T08:00:00Z", "queries": 42, "clients": 3 }
  ],
  "by_client": [
    { "client_ip": "192.168.88.20", "queries": 890 }
  ]
}
```

Chỉ vai trò `admin` thấy `by_client` (NFR bảo mật, US-6).

### `POST /domains/:id/decision`

Thay đổi trạng thái. Đây là endpoint quan trọng nhất của hệ thống.

```json
{
  "action": "block",
  "reason": "Adform tracker, xác nhận qua CNAME",
  "category": "tracking"
}
```

**200** trả về domain sau khi cập nhật.

Quy tắc:

- Ghi một dòng `decisions` kèm `snapshot` trạng thái hiện tại
- Đặt `is_manual = true` — từ đây job `classify` bỏ qua domain này
- Đánh dấu cần xuất bản lại
- `action: "block"` trên domain trong danh sách bảo vệ → **403** `domain_protected`
- `category` chỉ bắt buộc với `action` là `block` hoặc `recategorize`

### `POST /domains/bulk-decision`

```json
{
  "domain_ids": [4821, 4822, 9012],
  "action": "block",
  "reason": "Cụm Adform, chặn từ trang chi tiết"
}
```

**200**
```json
{
  "applied": 2,
  "skipped": [
    { "id": 4822, "code": "domain_protected", "message": "…" }
  ]
}
```

Không phải all-or-nothing: áp dụng được cái nào thì áp dụng, báo lại cái nào bỏ qua.
Mỗi domain vẫn có một dòng nhật ký riêng.

Tối đa 500 domain một lần.

### `POST /domains`

Thêm thủ công (FR-6.1).

```json
{
  "names": ["ads.example.com", "*.tracker.io"],
  "category": "ads",
  "reason": "Từ diễn đàn X",
  "status": "blocked",
  "dry_run": false
}
```

Với `dry_run: true`, không ghi gì, chỉ trả về kết quả phân tích — đây là cách UI dựng
bảng xem trước ở US-4.

```json
{
  "results": [
    { "name": "ads.example.com", "action": "created", "id": 10233 },
    { "name": "*.tracker.io",    "action": "created", "id": 10234, "matches_known": 12 },
    { "name": "google.com",      "action": "rejected", "code": "high_rank",
      "message": "Tranco #1, cần xác nhận thêm" },
    { "name": "bad domain",      "action": "rejected", "code": "invalid_domain" },
    { "name": "ads.example.com", "action": "duplicate", "id": 10233 }
  ],
  "summary": { "created": 2, "duplicate": 1, "rejected": 2 }
}
```

Để chặn domain có thứ hạng Tranco cao, phải gửi lại kèm `"force": true`. Bắt buộc phải
có `reason` khi `force`.

### `GET /domains/lookup?name=...`

Endpoint tra cứu cho vai trò `viewer` (US-6). Không lộ thông tin nội bộ.

```json
{
  "name": "ads.example.com",
  "blocked": true,
  "category": { "key": "ads", "label_vi": "Quảng cáo" }
}
```

### `POST /unblock-requests`

`viewer` gửi yêu cầu mở chặn.

```json
{ "domain": "ads.example.com", "note": "Trang đặt vé không thanh toán được" }
```

**201**. Tạo một mục chờ, hiện trong dashboard của admin.

---

## 4. Phân loại

### `GET /categories`

```json
{
  "items": [
    {
      "id": 1, "key": "ads", "label_vi": "Quảng cáo", "label_en": "Ads",
      "color": "#B24A16", "enabled": true,
      "score_threshold": 5.5, "publish_path": "ads.txt",
      "domain_count": 182043
    }
  ]
}
```

### `PATCH /categories/:id`

Sửa `enabled`, `score_threshold`, `label_*`, `color`. Không sửa được `key`.

Đổi `score_threshold` **không** tự chạy lại chấm điểm — phải gọi endpoint bên dưới.

---

## 5. Chấm điểm

### `GET /scoring/weights`

```json
{
  "weights": [
    { "kind": "cname_adtech", "weight": 6.0, "label_vi": "CNAME tới hạ tầng adtech" },
    { "kind": "asn_adtech",   "weight": 4.0, "label_vi": "ASN thuần adtech" }
  ],
  "updated_at": "2026-09-01T00:00:00Z",
  "updated_by": "admin"
}
```

### `PUT /scoring/weights`

```json
{
  "weights": [{ "kind": "cname_adtech", "weight": 6.5 }],
  "dry_run": true
}
```

Với `dry_run: true` (FR-3.6), tính lại điểm trong bộ nhớ và trả về tác động, không ghi:

```json
{
  "impact": {
    "would_block":   { "count": 47, "sample": ["a.example.com", "b.example.com"] },
    "would_unblock": { "count": 3,  "sample": ["c.example.com"] },
    "unchanged": 198234
  }
}
```

UI **phải** hiển thị màn hình này trước khi cho phép lưu. Đổi trọng số mà không xem
tác động là cách nhanh nhất để chặn nhầm hàng loạt.

### `POST /scoring/rescore`

Chạy lại chấm điểm toàn bộ. Trả **202** kèm `job_id`, theo dõi qua `/jobs/:id`.

Domain có `is_manual = true` không bị ảnh hưởng.

---

## 6. Nguồn ngoài

### `GET /sources` · `POST /sources` · `PATCH /sources/:id` · `DELETE /sources/:id`

```json
{
  "id": 1,
  "name": "Hagezi Pro",
  "url": "https://raw.githubusercontent.com/hagezi/dns-blocklists/main/hosts/pro.txt",
  "format": "hosts",
  "category": "ads",
  "enabled": true,
  "sync_interval": "24h",
  "last_sync_at": "2026-09-07T03:00:00Z",
  "last_status": "ok",
  "entry_count": 182043,
  "prev_count": 181902
}
```

### `POST /sources/:id/sync`

Đồng bộ ngay. **202** kèm `job_id`.

### `GET /sources/overlap`

Phân tích chồng lấn (FR-7.3).

```json
{
  "sources": [{ "id": 1, "name": "Hagezi Pro", "total": 182043 }],
  "pairs": [
    { "a": 1, "b": 2, "shared": 94021, "jaccard": 0.41 }
  ],
  "unique": [
    { "source_id": 1, "only_here": 61304 }
  ]
}
```

### `GET /sources/registry` — *chưa cài đặt*

Danh mục nguồn có sẵn nạp từ AdGuard HostlistsRegistry (FR-7.2, ưu tiên P1). Hiện tại
người dùng nhập URL nguồn trực tiếp. Xem [08](08-roadmap.md).

---

## 7. Xuất bản

### `GET /snapshots?category=ads&limit=30`

```json
{
  "items": [
    {
      "id": 812, "category": "ads", "entry_count": 182451,
      "checksum": "sha256:9f2c…", "published_at": "2026-09-07T03:35:00Z",
      "published_by": "system"
    }
  ]
}
```

### `GET /snapshots/:a/diff/:b`

```json
{
  "added":   { "count": 149, "sample": ["new1.example.com"] },
  "removed": { "count": 8,   "sample": ["old1.example.com"] }
}
```

`sample` giới hạn 100 dòng; tải đầy đủ qua `?full=true` trả về text/plain.

### `POST /publish`

Xuất bản ngay, không đợi lịch (US-3).

```json
{ "categories": ["ads", "tracking"] }
```

**202** kèm `job_id`. Bỏ trống `categories` nghĩa là tất cả.

Nếu vi phạm ngưỡng sụt giảm (FR-8.7), job thất bại với `code: "publish_blocked"` và
giữ nguyên file cũ.

### `POST /snapshots/:id/rollback`

Ghi lại file từ `snapshot_entries` của bản đó. Tạo một snapshot mới với
`published_by = "<user> (rollback từ #812)"` — không xóa lịch sử.

### Endpoint file — **không cần xác thực**

```
GET /lists/ads.txt
GET /lists/tracking.txt
GET /lists/all.txt
```

```
# DNSGuard — ads
# 182451 domain · sha256:9f2c… · 2026-09-07T03:35:00Z
0.0.0.0 ads.example.com
0.0.0.0 doubleclick.net
```

Header: `ETag`, `Last-Modified`, `Cache-Control: no-cache`, `Content-Type: text/plain`.
Hỗ trợ `If-None-Match` trả **304**.

Không xác thực vì MikroTik không gửi credential tiện lợi. Bù lại bằng giới hạn theo
dải IP nguồn, cấu hình qua `DNSGUARD_LISTS_ALLOW_CIDR`.

---

## 8. Vận hành

### `GET /stats/overview`

```
?from=2026-09-06T00:00:00Z&to=2026-09-07T00:00:00Z
```

```json
{
  "total_queries": 198432,
  "blocked_queries": 41203,
  "blocked_ratio": 0.208,
  "unique_domains": 14203,
  "unique_clients": 47,
  "pending_review": 23,
  "unblock_requests": 2,
  "by_category": [{ "key": "ads", "queries": 28104 }],
  "timeseries": [{ "t": "2026-09-06T08:00:00Z", "queries": 8102, "blocked": 1704 }]
}
```

### `GET /stats/top`

```
?dimension=domain|client|blocked&limit=20&from=…&to=…
```

### `GET /health`

Không cần xác thực. Dùng cho giám sát.

```json
{
  "status": "degraded",
  "checks": {
    "database":      { "ok": true,  "latency_ms": 3 },
    "querylog":      { "ok": true,  "last_event_age_s": 12 },
    "jobs":          { "ok": true,  "pending": 4, "failed_24h": 0 },
    "sources":       { "ok": false, "message": "Hagezi Pro: 404 lần cuối" },
    "publish":       { "ok": true,  "last_publish_age_s": 3421 }
  }
}
```

`status` là `ok`, `degraded`, hoặc `down`. HTTP luôn 200 trừ khi `down` thì 503.

Kiểm tra `querylog.last_event_age_s` là quan trọng nhất: nếu vượt 600 giây nghĩa là
sniffer trên MikroTik đã dừng — lỗi thường gặp nhất sau khi router reboot.

### `GET /jobs/:id`

```json
{
  "id": "01J…", "kind": "rescore", "state": "running",
  "progress": { "done": 120340, "total": 198234 },
  "started_at": "…", "finished_at": null, "error": null
}
```

### `GET /events` — SSE

Luồng sự kiện đẩy về UI. Dùng cho dashboard thời gian thực và cập nhật tiến độ job.

```
event: job_progress
data: {"id":"01J…","done":120340,"total":198234}

event: domain_staged
data: {"id":4821,"name":"metrics.trangweb.vn","score":7.5}

event: publish_done
data: {"category":"ads","entry_count":182451}
```

Chọn SSE thay vì WebSocket: luồng một chiều, tự động kết nối lại, đi qua proxy dễ
dàng, và không cần thư viện phía client.

---

## 9. Một nguồn sự thật giữa Go và TypeScript

Kiểu dữ liệu phía TypeScript khai báo tay trong `web/src/api/types.ts`, phản chiếu các
struct Go ở `internal/store` và `internal/api`.

**Vì sao không sinh tự động từ OpenAPI.** Bề mặt API vừa đủ nhỏ để giữ đồng bộ bằng
mắt, và một bước sinh code là một thứ nữa có thể hỏng trong quá trình build, một công
cụ nữa phải cài, và một file nữa phải kiểm tra trong CI. Đánh đổi: đổi tên trường ở Go
mà quên sửa `types.ts` sẽ không bị bắt lúc biên dịch.

Bù lại bằng hai thứ:

- **Bảng đặt tên bắt buộc** ở [07 §4](07-development.md): một khái niệm dùng đúng một
  từ ở cả ba tầng, nên tên trường đoán được chứ không phải tra.
- **Test API chạy trên CSDL thật** cho mọi endpoint và mọi mã lỗi, trong đó có test
  khẳng định mọi trường dạng danh sách trả về `[]` chứ không bao giờ là `null` — đây
  là loại lệch hợp đồng khó lần ra nhất, vì nó chỉ lộ ra khi gặp đúng một bản ghi rỗng.

Nếu bề mặt API lớn thêm đáng kể, sinh code từ OpenAPI là bước tiếp theo hợp lý.

## 10. Endpoint có thêm so với đặc tả gốc

| Endpoint | Việc |
|---|---|
| `GET /unblock-requests` | Danh sách yêu cầu mở chặn đang chờ, cho dashboard quản trị |
| `POST /unblock-requests/:id/resolve` | Chấp nhận hoặc từ chối một yêu cầu |
| `GET /settings/protect` | Danh sách bảo vệ cứng (chỉ đọc) và mềm |
| `PUT /settings/protect` | Sửa danh sách bảo vệ mềm |
| `GET /metrics` | Chỉ số Prometheus; bảo vệ bằng bearer token nếu có cấu hình |
