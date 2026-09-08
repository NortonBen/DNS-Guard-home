# 03 — Mô hình dữ liệu

SQLite ở chế độ WAL. Lược đồ đầy đủ nằm ở `internal/store/migrations/`.

## 1. Quy ước chung

**Thời gian lưu dạng chuỗi RFC3339 UTC**: `2026-09-07T06:18:18Z`. Chọn định dạng này
vì thứ tự từ điển của chuỗi trùng với thứ tự thời gian, nên `BETWEEN` và `ORDER BY`
dùng index bình thường mà không cần chuyển kiểu.

> **Cạm bẫy quan trọng.** Hàm `datetime()` của SQLite trả về `2026-09-07 08:15:10`
> với dấu cách, không phải chữ `T`. Ký tự `T` (0x54) lớn hơn dấu cách (0x20) trong
> bảng mã, nên mọi phép so sánh giữa cột đã lưu và kết quả `datetime()` đều **sai âm
> thầm** — không báo lỗi, chỉ trả về kết quả rỗng hoặc thừa. Trong SQL phải dùng
> `strftime('%Y-%m-%dT%H:%M:%SZ', …)`; trong Go phải tính mốc thời gian bằng
> `store.TimeAt()` rồi truyền vào làm tham số. Có test riêng khóa tính chất này.

**Kiểu dữ liệu**: SQLite không có ENUM, `TIMESTAMPTZ`, `INET` hay `JSONB`. Tương ứng:

| Khái niệm | Cách lưu |
|---|---|
| ENUM | `TEXT` kèm `CHECK (col IN (…))` |
| Thời điểm | `TEXT` RFC3339 UTC |
| Địa chỉ IP | `TEXT`, hoặc khóa ngoại tới bảng `clients` |
| Dữ liệu có cấu trúc | `TEXT` chứa JSON |
| Boolean | `INTEGER` 0/1 |

## 2. Bảng lõi

### `domains`

Bảng trung tâm. Mỗi tên miền đầy đủ một dòng.

```sql
CREATE TABLE domains (
  id           INTEGER PRIMARY KEY,
  name         TEXT    NOT NULL UNIQUE,
  name_rev     TEXT    NOT NULL,
  etld1        TEXT    NOT NULL,
  status       TEXT    NOT NULL DEFAULT 'new'
               CHECK (status IN ('new','staging','blocked','allowed','ignored')),
  origin       TEXT    NOT NULL
               CHECK (origin IN ('discovered','manual','public_list','related')),
  category_id  INTEGER REFERENCES categories(id),

  score        REAL,
  confidence   REAL,

  is_manual    INTEGER NOT NULL DEFAULT 0,
  is_wildcard  INTEGER NOT NULL DEFAULT 0,

  first_seen   TEXT NOT NULL,
  last_seen    TEXT NOT NULL,
  staged_at    TEXT,
  blocked_at   TEXT,
  scored_at    TEXT,
  enriched_at  TEXT,

  query_count  INTEGER NOT NULL DEFAULT 0,
  client_count INTEGER NOT NULL DEFAULT 0,

  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,

  CONSTRAINT domains_name_lower CHECK (name = lower(name)),
  CONSTRAINT domains_name_len   CHECK (length(name) BETWEEN 1 AND 253)
);

CREATE INDEX domains_status_score ON domains (status, score DESC);
CREATE INDEX domains_etld1        ON domains (etld1);
CREATE INDEX domains_last_seen    ON domains (last_seen DESC);
CREATE INDEX domains_name_rev     ON domains (name_rev);
CREATE INDEX domains_needs_enrich ON domains (enriched_at) WHERE status IN ('new','staging');
```

Ghi chú thiết kế:

**`is_manual` là cờ bất khả xâm phạm.** Khi bằng 1, job `classify` bỏ qua dòng này
hoàn toàn. Đây là cách thực thi bất biến "con người thắng máy" ở tầng dữ liệu chứ
không phải tầng ứng dụng, và nó xuất hiện trong mệnh đề `WHERE` của cả truy vấn lấy
ứng viên lẫn câu lệnh chuyển trạng thái tự động.

**`name_rev` là tên đảo ngược**: `ads.example.com` lưu thành `moc.elpmaxe.sda`. SQLite
không có `pg_trgm`, nên tìm kiếm hậu tố `*.example.com` được viết lại thành tìm kiếm
tiền tố `moc.elpmaxe.` trên cột này — dùng được index thay vì quét toàn bảng. Đây là
cột dư thừa có chủ ý duy nhất phục vụ hiệu năng đọc.

**`etld1` lưu dư thừa** thay vì tính lúc truy vấn. Nó xuất hiện trong hầu hết truy vấn
gom nhóm, và việc tính eTLD+1 cần bảng Public Suffix List nên không làm được trong SQL.

**Không lưu `is_blocked` riêng.** Trạng thái chặn suy ra từ `status = 'blocked'`. Hai
nguồn sự thật cho cùng một khái niệm luôn dẫn tới lệch nhau.

### `categories`

```sql
CREATE TABLE categories (
  id              INTEGER PRIMARY KEY,
  key             TEXT NOT NULL UNIQUE,
  label_vi        TEXT NOT NULL,
  label_en        TEXT NOT NULL,
  description     TEXT NOT NULL DEFAULT '',
  color           TEXT NOT NULL DEFAULT '#6B7280',
  enabled         INTEGER NOT NULL DEFAULT 1,
  score_threshold REAL NOT NULL DEFAULT 5.5,
  publish_path    TEXT NOT NULL,
  sort_order      INTEGER NOT NULL DEFAULT 0
);
```

Dữ liệu khởi tạo ở [06 §1](06-classification.md). `enabled = 0` nghĩa là phân loại đó
vẫn được gán nhưng không xuất bản — hữu ích khi gỡ lỗi mà không muốn mất dữ liệu đã
phân loại.

### `signals`

Bằng chứng cho điểm số. Mỗi lần chấm điểm xóa và ghi lại toàn bộ tín hiệu của domain:
tín hiệu là ảnh chụp của lần chấm điểm gần nhất, không phải sổ tích lũy.

```sql
CREATE TABLE signals (
  id         INTEGER PRIMARY KEY,
  domain_id  INTEGER NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
  kind       TEXT NOT NULL,
  weight     REAL NOT NULL,
  detail     TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);
```

`detail` chứa dữ liệu cụ thể của tín hiệu: `{"cname": "x.eulerian.net"}` hoặc
`{"asn": 62597, "org": "Adform"}`. Dạng JSON vì mỗi loại tín hiệu có cấu trúc khác
nhau và tập loại sẽ còn mở rộng.

### `domain_facts`

Kết quả làm giàu, có TTL.

```sql
CREATE TABLE domain_facts (
  domain_id  INTEGER NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
  source     TEXT NOT NULL
             CHECK (source IN ('dns','asn','cert','rdap','rank','http','vt','ai')),
  data       TEXT NOT NULL DEFAULT '{}',
  fetched_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  error      TEXT,
  PRIMARY KEY (domain_id, source)
) WITHOUT ROWID;
```

TTL theo nguồn:

| `source` | TTL | Lý do |
|---|---|---|
| `dns` | 6 giờ | CNAME và IP đổi thường xuyên |
| `asn` | 7 ngày | Ánh xạ IP→ASN ổn định |
| `cert` | 30 ngày | Chứng chỉ đổi hiếm |
| `rdap` | 90 ngày | Ngày đăng ký không đổi |
| `rank` | 7 ngày | Tranco cập nhật hằng ngày nhưng thứ hạng ổn định |
| `http` | 14 ngày | Nội dung trang đổi chậm |
| `vt` | 30 ngày | Kết luận của các engine đổi chậm |
| `ai` | 21 ngày | Kết luận của model đổi khi model đổi, không khi domain đổi |

TTL còn phụ thuộc **kết cục**, không chỉ nguồn. Đây là phần trả lời yêu cầu "nhớ đã
kiểm tra chưa, đừng kiểm tra lại ngay, bao lâu thì kiểm tra lại": bảng này lo phần
"nhớ" — có dòng thì `EnrichCandidates` bỏ qua domain đó cho tới khi hết hạn, kể cả
khi nó bị truy vấn liên tục — còn bảng dưới lo phần nhịp thử lại.

| Nguồn | Kết cục | TTL | Lý do |
|---|---|---|---|
| `http` | trang đỗ tên miền | 30 ngày | Domain đã đỗ thì đỗ lâu |
| `http` | DNS hỏng, từ chối kết nối | 7 ngày | Host chết thì cứ chết |
| `http` | lỗi TLS | 7 ngày | Cấu hình hiếm khi đổi |
| `http` | HTTP 4xx/5xx | 3 ngày | |
| `http` | hết thời gian chờ | 2 ngày | Có thể chỉ là tạm thời |
| `http` | địa chỉ nội bộ, bị từ chối | 90 ngày | Quyết định an toàn, không đổi theo thời gian |
| `vt` | VirusTotal chưa biết domain | 7 ngày | Có thể được lập chỉ mục sau |
| `vt` | hết quota | 1 giờ | Thử lại trong ngày |
| `ai` | hết hạn mức | 1 giờ | Hạn mức đặt lại theo ngày |

**Ghi cả khi thất bại là có chủ ý.** Dòng lỗi chính là thứ ngăn hệ thống thử lại ngay
vòng sau; không có nó, một domain không kết nối được sẽ bị hỏi lại mỗi mười lăm phút
mãi mãi.

Kết cục không nằm trong bảng trên dùng TTL mặc định 15 phút. Job vẫn phải tôn trọng
circuit breaker của nguồn đó: hết hạn cache không có nghĩa là được phép gọi ngay.

### `relations`

Đồ thị domain liên quan.

```sql
CREATE TABLE relations (
  from_id     INTEGER NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
  to_id       INTEGER NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
  kind        TEXT NOT NULL
              CHECK (kind IN ('cname_to','same_asn','same_cert','co_occurs')),
  strength    REAL NOT NULL DEFAULT 1.0,
  detail      TEXT NOT NULL DEFAULT '{}',
  computed_at TEXT NOT NULL,
  PRIMARY KEY (from_id, to_id, kind),
  CONSTRAINT relations_no_self CHECK (from_id <> to_id)
) WITHOUT ROWID;
```

`strength` mang ý nghĩa khác nhau tùy `kind`: với `co_occurs` là PMI chuẩn hóa về
[0,1]; với ba loại còn lại luôn là 1.0.

Cạnh `cname_to` có hướng; ba loại còn lại vô hướng nhưng vẫn lưu hai chiều để truy vấn
đọc chỉ cần nhìn một cột. Đánh đổi dung lượng lấy tốc độ đọc — hợp lý vì đọc nhiều hơn
ghi rất nhiều.

### `decisions`

Nhật ký bất biến. **Không có UPDATE, không có DELETE.**

```sql
CREATE TABLE decisions (
  id          INTEGER PRIMARY KEY,
  domain_id   INTEGER NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
  action      TEXT NOT NULL
              CHECK (action IN ('block','allow','ignore','recategorize','stage','expire')),
  actor_id    INTEGER REFERENCES users(id),
  actor_label TEXT NOT NULL,
  reason      TEXT NOT NULL DEFAULT '',
  snapshot    TEXT NOT NULL DEFAULT '{}',
  created_at  TEXT NOT NULL
);

CREATE TRIGGER decisions_no_update BEFORE UPDATE ON decisions
BEGIN
  SELECT RAISE(ABORT, 'decisions is append-only');
END;

CREATE TRIGGER decisions_no_delete BEFORE DELETE ON decisions
BEGIN
  SELECT RAISE(ABORT, 'decisions is append-only');
END;
```

**Trigger thay cho `REVOKE`.** SQLite không có phân quyền ở mức bảng, nên tính bất
biến được thực thi bằng trigger. Kết quả mạnh hơn: `REVOKE` chỉ ràng buộc một vai trò
CSDL cụ thể, còn trigger chặn mọi lối ghi kể cả khi ứng dụng có bug hoặc ai đó mở file
bằng `sqlite3`.

**`actor_label` lưu cứng tên người dùng** thay vì chỉ giữ khóa ngoại. Nếu tài khoản bị
xóa sau này, nhật ký vẫn đọc được. Đây là bảng duy nhất chấp nhận dư thừa dữ liệu có
chủ ý.

**`snapshot`** lưu trạng thái tại thời điểm quyết định: điểm số, danh sách tín hiệu,
phân loại. Nhờ đó sáu tháng sau vẫn trả lời được "lúc đó hệ thống nghĩ gì", kể cả khi
trọng số đã đổi. Đây là phần đắt giá nhất của bảng này.

## 3. Bảng sự kiện và tổng hợp

### `query_events`

Log thô, bảng lớn nhất.

```sql
CREATE TABLE query_events (
  id          INTEGER PRIMARY KEY,
  domain_id   INTEGER NOT NULL,
  client_id   INTEGER NOT NULL,
  qtype       INTEGER NOT NULL,
  occurred_at TEXT NOT NULL
);

CREATE INDEX query_events_time        ON query_events (occurred_at);
CREATE INDEX query_events_domain_time ON query_events (domain_id, occurred_at DESC);
CREATE INDEX query_events_client_time ON query_events (client_id, occurred_at DESC);
```

SQLite không có phân mảnh bảng, nên xóa dữ liệu quá hạn bằng `DELETE` theo lô 20.000
dòng trên cột `occurred_at` đã đánh index, lặp cho tới hết. Xóa theo lô là bắt buộc:
một `DELETE` trên nhiều triệu dòng giữ khóa ghi quá lâu và làm ingest nghẽn.

Không có khóa ngoại tới `domains`: chi phí kiểm tra khóa ngoại khi ghi lô là đáng kể,
và tính toàn vẹn đã được `ingest` đảm bảo trong cùng transaction.

### `clients`

```sql
CREATE TABLE clients (
  id         INTEGER PRIMARY KEY,
  ip         TEXT NOT NULL UNIQUE,
  label      TEXT NOT NULL DEFAULT '',
  first_seen TEXT NOT NULL,
  last_seen  TEXT NOT NULL
);
```

Ánh xạ IP sang số nguyên nhỏ. Bảng `query_events` lưu `client_id` thay vì chuỗi IP,
tiết kiệm đáng kể trên hàng triệu dòng, và cho phép dùng bitmap ở bảng tổng hợp.

### `domain_hourly`

```sql
CREATE TABLE domain_hourly (
  domain_id    INTEGER NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
  hour         TEXT NOT NULL,
  query_count  INTEGER NOT NULL DEFAULT 0,
  client_count INTEGER NOT NULL DEFAULT 0,
  clients      BLOB,
  PRIMARY KEY (domain_id, hour)
) WITHOUT ROWID;
```

Cập nhật bằng `INSERT … ON CONFLICT DO UPDATE` trong cùng transaction với `ingest`.

**Cột `clients` là bitmap, không phải HyperLogLog.** Mỗi client có một id nhỏ, và bit
thứ `id-1` được bật. Một mạng gia đình có vài trăm client nên bitmap chỉ vài chục
byte; gộp nhiều giờ bằng phép OR; và số đếm là **chính xác tuyệt đối** thay vì xấp xỉ
2%. HyperLogLog chỉ đáng dùng khi số phần tử phân biệt lớn tới mức bitmap không vừa,
mà quy mô ở đây thì không tới.

Giữ 400 ngày — nhỏ hơn log thô hàng trăm lần nên không cần xóa sớm.

### `domain_ips`

Ánh xạ domain → địa chỉ, lấy từ **bản ghi trả lời DNS** mà router mirror sang.

```sql
CREATE TABLE domain_ips (
  domain_id  INTEGER NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
  ip         TEXT    NOT NULL,
  first_seen TEXT    NOT NULL,
  last_seen  TEXT    NOT NULL,
  hits       INTEGER NOT NULL DEFAULT 1,
  ttl        INTEGER NOT NULL DEFAULT 0,
  asn        INTEGER,
  country    TEXT,
  org        TEXT,
  -- Kết quả đối chiếu với các danh sách hạ tầng độc hại. NULL = không nằm trong
  -- danh sách nào. Hai cột luôn được ghi cùng nhau.
  threat        TEXT,  -- dải đã khớp, ví dụ "45.66.0.0/16"
  threat_source TEXT,  -- khóa danh sách đã khớp: "spamhaus_drop" | "threatfox"
  PRIMARY KEY (domain_id, ip)
) WITHOUT ROWID;
```

**Vì sao lưu cả nguồn, không chỉ dải.** Hệ thống đối chiếu với nhiều danh sách cùng
lúc, và mỗi danh sách đòi một mức phản ứng khác nhau: khớp một dải của Spamhaus DROP
nghĩa là địa chỉ nằm trong khối bị tổ chức tội phạm thuê hoặc chiếm đoạt, còn khớp
ThreatFox nghĩa là có máy trong mạng đang nói chuyện với máy chủ điều khiển mã độc.
Chỉ nhìn dải thì không phân biệt được hai việc đó.

Job `ip_threat` quét lại toàn bộ mỗi giờ, nên hai cột này tự đầy sau khi thêm một
nguồn mới — không cần backfill, và cảnh báo tự tắt khi địa chỉ được gỡ khỏi danh sách.

**Khác `domain_facts` nguồn `asn` ở chỗ nào.** Ở đó là kết quả DNSGuard tự phân giải
lúc làm giàu — một tra cứu khác, từ một máy khác, vào một lúc khác. Bảng này là câu
trả lời mà thiết bị trong mạng **thật sự nhận được**, nên nó là thứ duy nhất trong hai
cái dùng làm bằng chứng điều tra được.

**Không có cột client, và đó là chủ ý.** Gói trả lời đi hai chiều: chiều
router → client có IP đích là client thật, còn chiều upstream → router có IP đích là
chính router. Lấy IP đích làm client sẽ gán nhầm mọi lượt phân giải upstream cho
router. Ánh xạ ở đây độc lập với client; việc quy kết lấy bằng cách nối với
`query_events`, vốn đã ghi đúng thiết bị nào hỏi tên nào lúc nào.

`ttl` giữ giá trị **nhỏ nhất** từng thấy: nó phân biệt một CDN xoay IP mỗi phút với
một máy chủ cố định, và một TTL thấp bất thường trên domain lạ là dấu hiệu fast-flux.

`country` bằng `NULL` nghĩa là *chưa tra*; bằng chuỗi rỗng nghĩa là *đã tra mà bảng
ip2asn không biết*. Phân biệt hai trạng thái này là bắt buộc, nếu không một dải địa
chỉ ngoài bảng sẽ nằm lại hàng đợi và bị tra lại mãi mãi.

### `domain_behavior`

```sql
CREATE TABLE domain_behavior (
  domain_id         INTEGER PRIMARY KEY REFERENCES domains(id) ON DELETE CASCADE,
  third_party_ratio REAL NOT NULL DEFAULT 0,
  interval_cv       REAL NOT NULL DEFAULT 0,
  sample_size       INTEGER NOT NULL DEFAULT 0,
  computed_at       TEXT NOT NULL
);
```

Đặc trưng hành vi tính lại theo lô hằng đêm từ `query_events`. Tách khỏi bảng
`domains` vì chúng được tính lại định kỳ chứ không cập nhật theo từng truy vấn.

### `resource_samples`

Mức tiêu thụ tài nguyên của chính tiến trình, đo mỗi mười giây.

```sql
CREATE TABLE resource_samples (
  at          TEXT    PRIMARY KEY,   -- RFC3339, giống mọi cột thời gian khác
  cpu_percent REAL    NOT NULL,      -- phần trăm một lõi kể từ lần đo trước
  rss_bytes   INTEGER NOT NULL,      -- bộ nhớ thường trú, con số hệ điều hành thấy
  heap_bytes  INTEGER NOT NULL,      -- heap Go đang dùng
  goroutines  INTEGER NOT NULL
) WITHOUT ROWID;
```

`WITHOUT ROWID` vì mốc thời gian đã là khóa chính tự nhiên và mọi truy vấn đều lọc
theo khoảng thời gian: bảng lưu trực tiếp theo thứ tự khóa thì không cần thêm chỉ mục
phụ, và ở 260 nghìn dòng cho ba mươi ngày thì chênh lệch đó là đáng kể.

Ghi theo lô mỗi phút chứ không từng mẫu một. Máy chủ có thể chạy trên thẻ nhớ, và
8.640 giao dịch mỗi ngày chỉ để lưu vài con số là lãng phí vòng ghi mà không đổi lại
được gì — dữ liệu này chỉ dùng để vẽ biểu đồ.

Đọc ra thì gộp theo khoảng, độ rộng chọn theo khoảng xem để mọi biểu đồ đều dưới bảy
trăm điểm. Mỗi khoảng giữ cả trung bình lẫn đỉnh: gộp chỉ giữ trung bình sẽ xóa mất
đúng thứ cần tìm, vì một nhịp tăng ba mươi giây biến mất hoàn toàn trong khoảng gộp
một giờ.

## 4. Nguồn ngoài

```sql
CREATE TABLE list_sources (
  id            INTEGER PRIMARY KEY,
  name          TEXT NOT NULL,
  url           TEXT NOT NULL UNIQUE,
  format        TEXT NOT NULL DEFAULT 'hosts'
                CHECK (format IN ('hosts','adblock','plain')),
  category_id   INTEGER REFERENCES categories(id),
  enabled       INTEGER NOT NULL DEFAULT 1,
  sync_interval INTEGER NOT NULL DEFAULT 86400,  -- giây
  last_sync_at  TEXT,
  last_status   TEXT NOT NULL DEFAULT '',
  last_error    TEXT NOT NULL DEFAULT '',
  entry_count   INTEGER NOT NULL DEFAULT 0,
  prev_count    INTEGER NOT NULL DEFAULT 0,
  etag          TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL
);

CREATE TABLE list_entries (
  source_id INTEGER NOT NULL REFERENCES list_sources(id) ON DELETE CASCADE,
  domain    TEXT NOT NULL,
  PRIMARY KEY (source_id, domain)
) WITHOUT ROWID;

CREATE INDEX list_entries_domain ON list_entries (domain);
```

Lưu `domain` dạng chuỗi chứ không phải khóa ngoại. Một nguồn có thể chứa 200k domain
mà phần lớn chưa bao giờ xuất hiện trong mạng — tạo dòng trong `domains` cho tất cả sẽ
làm bảng chính phình gấp mười lần vô ích. Khi cần biết một domain có trong nguồn nào,
join theo tên.

## 5. Xuất bản

```sql
CREATE TABLE snapshots (
  id           INTEGER PRIMARY KEY,
  category_id  INTEGER REFERENCES categories(id),  -- NULL = file gộp
  entry_count  INTEGER NOT NULL,
  checksum     TEXT NOT NULL,
  file_path    TEXT NOT NULL,
  published_at TEXT NOT NULL,
  published_by TEXT NOT NULL DEFAULT 'system'
);

CREATE TABLE snapshot_entries (
  snapshot_id INTEGER NOT NULL REFERENCES snapshots(id) ON DELETE CASCADE,
  domain      TEXT NOT NULL,
  PRIMARY KEY (snapshot_id, domain)
) WITHOUT ROWID;
```

`snapshot_entries` cho phép so sánh hai lần xuất bản bất kỳ và quay lại bản cũ mà
không cần dựng lại từ trạng thái hiện tại của CSDL. Giữ 30 bản gần nhất cho mỗi phân
loại.

## 6. Người dùng và cấu hình

```sql
CREATE TABLE users (
  id            INTEGER PRIMARY KEY,
  username      TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,       -- argon2id, tham số nhúng trong chuỗi
  role          TEXT NOT NULL DEFAULT 'viewer' CHECK (role IN ('admin','viewer')),
  active        INTEGER NOT NULL DEFAULT 1,
  last_login_at TEXT,
  created_at    TEXT NOT NULL
);

CREATE TABLE sessions (
  token      TEXT PRIMARY KEY,   -- băm SHA-256 của token thật
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  csrf_token TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL,
  user_agent TEXT NOT NULL DEFAULT '',
  ip         TEXT NOT NULL DEFAULT ''
) WITHOUT ROWID;

CREATE TABLE settings (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  updated_by TEXT NOT NULL DEFAULT 'system'
) WITHOUT ROWID;
```

`sessions.token` lưu **băm** của token phiên, không lưu token gốc. Rò rỉ CSDL không
cho phép mạo danh phiên đang hoạt động.

`settings` chứa những thứ sửa được qua giao diện: trọng số tín hiệu, ngưỡng, danh sách
bảo vệ mềm. Những thứ liên quan tới hạ tầng — đường dẫn, địa chỉ lắng nghe — nằm ở
biến môi trường và không sửa được lúc chạy.

## 7. Hàng đợi job

```sql
CREATE TABLE jobs (
  id             TEXT PRIMARY KEY,
  kind           TEXT NOT NULL,
  args           TEXT NOT NULL DEFAULT '{}',
  state          TEXT NOT NULL DEFAULT 'pending'
                 CHECK (state IN ('pending','running','done','failed','cancelled')),
  attempt        INTEGER NOT NULL DEFAULT 0,
  max_attempts   INTEGER NOT NULL DEFAULT 3,
  progress_done  INTEGER NOT NULL DEFAULT 0,
  progress_total INTEGER NOT NULL DEFAULT 0,
  error          TEXT NOT NULL DEFAULT '',
  run_at         TEXT NOT NULL,
  started_at     TEXT,
  finished_at    TEXT,
  created_at     TEXT NOT NULL
) WITHOUT ROWID;
```

Nhận job bằng một transaction đọc-rồi-cập-nhật. Pool ghi chỉ có một kết nối nên thao
tác này tự nó đã tuần tự hóa — không có hai worker nào nhận cùng một job, và không cần
khóa phân tán.

## 8. Cấu hình kết nối

```
journal_mode  = WAL        đầu đọc không chặn đầu ghi
busy_timeout  = 10000      chờ thay vì lỗi ngay khi có tranh chấp
foreign_keys  = ON
synchronous   = NORMAL     mỗi giao dịch không fsync riêng
temp_store    = MEMORY
cache_size    = -32000     32 MB
```

`synchronous = NORMAL` thay vì `FULL`: máy chủ có thể chạy trên thẻ nhớ, và fsync mỗi
giao dịch sẽ mòn thẻ. Với WAL, mức NORMAL vẫn an toàn khi tiến trình chết; chỉ mất vài
giao dịch cuối nếu mất điện đột ngột. Với dữ liệu quan sát thì chấp nhận được.

**Hai pool kết nối** trên cùng một file: pool ghi giới hạn đúng một kết nối, pool đọc
tám kết nối. Dồn mọi lệnh ghi qua một kết nối biến `SQLITE_BUSY` từ lỗi lúc chạy thành
hàng đợi trong tiến trình — cần thiết vì ingest ghi liên tục trong khi API vẫn phải
đọc.

### CSDL nhật ký AI — một file riêng

Toàn bộ dấu vết của tính năng hỏi AI nằm ở **một file SQLite thứ hai**, mặc định cạnh
CSDL chính với hậu tố `-ai` (`DNSGUARD_AI_DB_PATH` để đổi). Cùng bộ pragma, cùng cách
migration, nhưng không một khoá ngoại nào bắc qua hai file — domain được tham chiếu
bằng **tên**, không bằng id.

| Bảng | Nội dung |
|---|---|
| `ai_requests` | Một lượt gọi model: prompt, phản hồi thô, số đo, lỗi |
| `ai_verdicts` | Kết luận cho từng domain trong một lượt |
| `ai_skills` | Quy ước vận hành chèn vào ngữ cảnh theo từ khoá |
| `ai_mcp_servers` | Máy chủ MCP ngoài cung cấp thêm công cụ |
| `ai_chats`, `ai_messages` | Hội thoại hỏi đáp, kèm dấu vết gọi công cụ |

Tách file là quyết định vận hành: nhật ký AI phình theo số lượt hỏi chứ không theo số
domain, người vận hành cần xoá được nó mà không chạm vào dữ liệu quyết định, và một
bản sao lưu CSDL chính không nên phải mang theo hàng megabyte prompt. **Xoá cả file
này không mất một quyết định chặn nào.**

Chỉ đúng một thứ của AI đi vào CSDL chính: dòng `domain_facts(source='ai')`, vì nó là
bằng chứng dùng để chấm điểm. Khóa API nằm ở bảng `settings` của CSDL chính, cùng chỗ
với khóa VirusTotal — nhật ký AI giữ dữ liệu vận hành, không giữ khóa.

## 9. Migration

Migration là các file `.sql` trong `internal/store/migrations/`, **nhúng vào binary**
bằng `go:embed` và chạy tự động khi khởi động. CSDL nhật ký AI có bộ migration riêng ở
`internal/ai/migrations/`, chạy bằng cùng một bộ máy (`store.MigrateFS`).

Quy tắc:

1. **Đặt tên theo thứ tự**: `0001_init.sql`, `0002_seed.sql`, …
2. **Mỗi migration chạy trong một transaction.** Hỏng giữa chừng thì không để lại lược
   đồ nửa vời, kể cả khi mất điện.
3. **Không sửa migration đã phát hành.** Sai thì viết migration mới sửa lại.
4. **Không có dữ liệu nghiệp vụ trong migration** ngoài dữ liệu khởi tạo bắt buộc
   (danh mục phân loại).
5. Tắt được bằng `DNSGUARD_AUTO_MIGRATE=false` cho môi trường có kiểm soát.

Nhúng migration vào binary giữ đúng ràng buộc "một binary + một file cấu hình" ở
[01 §6](01-requirements.md), và bỏ được một công cụ ngoài khỏi cả môi trường phát
triển lẫn ảnh Docker.

## 10. Chính sách lưu trữ

| Bảng | Giữ | Cách xóa |
|---|---|---|
| `query_events` | 90 ngày | `DELETE` theo lô 20.000 dòng, job hằng ngày |
| `domain_hourly` | 400 ngày | `DELETE WHERE hour < …`, job hằng ngày |
| `domain_facts` | tới khi hết TTL | ghi đè khi làm giàu lại |
| `relations` kiểu `co_occurs` | tính lại hằng đêm | xóa toàn bộ loại đó rồi ghi lại |
| `decisions` | **vĩnh viễn** | không bao giờ xóa |
| `snapshots` | 30 bản mỗi phân loại | job hằng ngày |
| `sessions` | tới hạn | job hằng ngày |
| `jobs` | 7 ngày sau khi xong | job hằng ngày |
| `resource_samples` | 30 ngày | `DELETE WHERE at < …`, job hằng ngày |

Sau mỗi lần dọn dẹp, chạy `PRAGMA wal_checkpoint(TRUNCATE)` để trả lại dung lượng.

## 11. Ước lượng dung lượng

Mạng 50 thiết bị, 200k truy vấn/ngày:

| Bảng | Sau 90 ngày | Ghi chú |
|---|---|---|
| `query_events` | ~1,1 GB | 18M dòng, ~60 byte/dòng kể cả index |
| `domain_hourly` | ~90 MB | 15k domain × 24 giờ × 90 ngày |
| `domains` | ~40 MB | 200k dòng kể cả list công khai |
| `list_entries` | ~200 MB | 5 nguồn × 200k |
| `relations` | ~80 MB | phụ thuộc mật độ đồ thị |
| Còn lại | < 50 MB | |
| **Tổng** | **~1,6 GB** | Vừa cho thẻ nhớ 32 GB, thoải mái với SSD |

Nhỏ hơn bản PostgreSQL khoảng 15% nhờ `client_id` thay chuỗi IP và không có chi phí
mỗi dòng của MVCC.

Nếu chật, giảm `DNSGUARD_LOG_RETENTION_DAYS` xuống 30 — mất `query_events` không ảnh
hưởng gì tới quyết định đã ghi, chỉ mất khả năng điều tra ngược xa.

## 12. Sao lưu

```bash
dnsguard-cli backup -o /backup/dnsguard-$(date +%F).db
```

Lệnh này gộp WAL vào file chính rồi chép — nếu chép thẳng file khi đang chạy, bản sao
sẽ thiếu những giao dịch còn nằm trong WAL.

Thư mục `lists` không cần sao lưu: sinh lại được bằng `dnsguard-cli publish`. Thứ thực
sự không thể thay thế là bảng `decisions` — query log tái tạo được từ mạng, danh sách
công khai tải lại được, nhưng lịch sử quyết định của con người thì mất là mất.
