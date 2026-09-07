-- Lược đồ khởi tạo DNSGuard trên SQLite.
--
-- Chuyển thể từ thiết kế PostgreSQL ở docs/03-data-model.md. Bốn khác biệt đáng kể:
--   1. ENUM      -> TEXT kèm CHECK.
--   2. Phân mảnh -> một bảng phẳng, xóa theo khoảng thời gian trên cột đã đánh index.
--   3. pg_trgm   -> cột name_rev cho tìm kiếm hậu tố; LIKE cho tìm kiếm chứa chuỗi.
--   4. REVOKE    -> trigger RAISE(ABORT) để khóa sửa/xóa nhật ký quyết định.
--
-- Thời điểm lưu dạng chuỗi RFC3339 UTC ('2026-09-07T06:18:18Z'): so sánh theo thứ tự
-- từ điển trùng với thứ tự thời gian, nên BETWEEN và ORDER BY dùng index bình thường.

CREATE TABLE categories (
  id              INTEGER PRIMARY KEY,
  key             TEXT    NOT NULL UNIQUE,
  label_vi        TEXT    NOT NULL,
  label_en        TEXT    NOT NULL,
  description     TEXT    NOT NULL DEFAULT '',
  color           TEXT    NOT NULL DEFAULT '#6B7280',
  enabled         INTEGER NOT NULL DEFAULT 1,
  score_threshold REAL    NOT NULL DEFAULT 5.5,
  publish_path    TEXT    NOT NULL,
  sort_order      INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE domains (
  id           INTEGER PRIMARY KEY,
  name         TEXT    NOT NULL UNIQUE,
  -- Tên đảo ngược ('moc.elpmaxe.sda'): tìm kiếm hậu tố '*.example.com' trở thành
  -- tiền tố 'moc.elpmaxe.' nên dùng được index thay vì quét toàn bảng.
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

  first_seen   TEXT    NOT NULL,
  last_seen    TEXT    NOT NULL,
  staged_at    TEXT,
  blocked_at   TEXT,
  scored_at    TEXT,
  enriched_at  TEXT,

  query_count  INTEGER NOT NULL DEFAULT 0,
  client_count INTEGER NOT NULL DEFAULT 0,

  created_at   TEXT    NOT NULL,
  updated_at   TEXT    NOT NULL,

  CONSTRAINT domains_name_lower CHECK (name = lower(name)),
  CONSTRAINT domains_name_len   CHECK (length(name) BETWEEN 1 AND 253)
);

CREATE INDEX domains_status_score ON domains (status, score DESC);
CREATE INDEX domains_etld1        ON domains (etld1);
CREATE INDEX domains_last_seen    ON domains (last_seen DESC);
CREATE INDEX domains_name_rev     ON domains (name_rev);
CREATE INDEX domains_needs_enrich ON domains (enriched_at) WHERE status IN ('new','staging');

CREATE TABLE signals (
  id         INTEGER PRIMARY KEY,
  domain_id  INTEGER NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
  kind       TEXT    NOT NULL,
  weight     REAL    NOT NULL,
  detail     TEXT    NOT NULL DEFAULT '{}',
  created_at TEXT    NOT NULL
);

CREATE INDEX signals_domain ON signals (domain_id);
CREATE INDEX signals_kind   ON signals (kind);

CREATE TABLE domain_facts (
  domain_id  INTEGER NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
  source     TEXT    NOT NULL CHECK (source IN ('dns','asn','cert','rdap','rank')),
  data       TEXT    NOT NULL DEFAULT '{}',
  fetched_at TEXT    NOT NULL,
  expires_at TEXT    NOT NULL,
  error      TEXT,
  PRIMARY KEY (domain_id, source)
) WITHOUT ROWID;

CREATE INDEX domain_facts_expiry ON domain_facts (expires_at) WHERE error IS NULL;

CREATE TABLE relations (
  from_id     INTEGER NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
  to_id       INTEGER NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
  kind        TEXT    NOT NULL
              CHECK (kind IN ('cname_to','same_asn','same_cert','co_occurs')),
  strength    REAL    NOT NULL DEFAULT 1.0,
  detail      TEXT    NOT NULL DEFAULT '{}',
  computed_at TEXT    NOT NULL,
  PRIMARY KEY (from_id, to_id, kind),
  CONSTRAINT relations_no_self CHECK (from_id <> to_id)
) WITHOUT ROWID;

CREATE INDEX relations_to ON relations (to_id, kind);

-- Nhật ký bất biến. Không UPDATE, không DELETE — trigger bên dưới thay cho REVOKE
-- của PostgreSQL, và chặn ở tầng CSDL kể cả khi ứng dụng có bug.
CREATE TABLE decisions (
  id          INTEGER PRIMARY KEY,
  domain_id   INTEGER NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
  action      TEXT    NOT NULL
              CHECK (action IN ('block','allow','ignore','recategorize','stage','expire')),
  actor_id    INTEGER REFERENCES users(id),
  actor_label TEXT    NOT NULL,
  reason      TEXT    NOT NULL DEFAULT '',
  snapshot    TEXT    NOT NULL DEFAULT '{}',
  created_at  TEXT    NOT NULL
);

CREATE INDEX decisions_domain ON decisions (domain_id, created_at DESC);
CREATE INDEX decisions_time   ON decisions (created_at DESC);

CREATE TRIGGER decisions_no_update BEFORE UPDATE ON decisions
BEGIN
  SELECT RAISE(ABORT, 'decisions is append-only');
END;

CREATE TRIGGER decisions_no_delete BEFORE DELETE ON decisions
BEGIN
  SELECT RAISE(ABORT, 'decisions is append-only');
END;

-- Bảng lớn nhất. Không phân mảnh được như PostgreSQL, nên xóa theo lô trên
-- occurred_at đã đánh index; job dọn dẹp chạy hằng ngày.
CREATE TABLE query_events (
  id          INTEGER PRIMARY KEY,
  domain_id   INTEGER NOT NULL,
  client_id   INTEGER NOT NULL,
  qtype       INTEGER NOT NULL,
  occurred_at TEXT    NOT NULL
);

CREATE INDEX query_events_time        ON query_events (occurred_at);
CREATE INDEX query_events_domain_time ON query_events (domain_id, occurred_at DESC);
CREATE INDEX query_events_client_time ON query_events (client_id, occurred_at DESC);

-- Ánh xạ IP client sang số nguyên nhỏ. Mạng gia đình có vài trăm client, nên id vừa
-- trong một bitmap vài chục byte — thay cho HyperLogLog của bản PostgreSQL, và cho
-- kết quả đếm chính xác tuyệt đối thay vì xấp xỉ 2%.
CREATE TABLE clients (
  id        INTEGER PRIMARY KEY,
  ip        TEXT NOT NULL UNIQUE,
  label     TEXT NOT NULL DEFAULT '',
  first_seen TEXT NOT NULL,
  last_seen  TEXT NOT NULL
);

CREATE TABLE domain_hourly (
  domain_id    INTEGER NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
  hour         TEXT    NOT NULL,
  query_count  INTEGER NOT NULL DEFAULT 0,
  client_count INTEGER NOT NULL DEFAULT 0,
  clients      BLOB,
  PRIMARY KEY (domain_id, hour)
) WITHOUT ROWID;

CREATE INDEX domain_hourly_hour ON domain_hourly (hour DESC);

-- Đặc trưng hành vi tính lại theo lô từ query_events. Tách khỏi bảng domains vì
-- chúng được tính lại định kỳ chứ không cập nhật theo từng truy vấn.
CREATE TABLE domain_behavior (
  domain_id         INTEGER PRIMARY KEY REFERENCES domains(id) ON DELETE CASCADE,
  third_party_ratio REAL    NOT NULL DEFAULT 0,
  interval_cv       REAL    NOT NULL DEFAULT 0,
  sample_size       INTEGER NOT NULL DEFAULT 0,
  computed_at       TEXT    NOT NULL
);

CREATE TABLE ingest_cursor (
  path       TEXT    PRIMARY KEY,
  inode      INTEGER NOT NULL,
  offset_pos INTEGER NOT NULL,
  updated_at TEXT    NOT NULL
) WITHOUT ROWID;

CREATE TABLE list_sources (
  id            INTEGER PRIMARY KEY,
  name          TEXT    NOT NULL,
  url           TEXT    NOT NULL UNIQUE,
  format        TEXT    NOT NULL DEFAULT 'hosts'
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
  domain    TEXT    NOT NULL,
  PRIMARY KEY (source_id, domain)
) WITHOUT ROWID;

CREATE INDEX list_entries_domain ON list_entries (domain);

CREATE TABLE snapshots (
  id           INTEGER PRIMARY KEY,
  category_id  INTEGER REFERENCES categories(id),   -- NULL = file gộp
  entry_count  INTEGER NOT NULL,
  checksum     TEXT    NOT NULL,
  file_path    TEXT    NOT NULL,
  published_at TEXT    NOT NULL,
  published_by TEXT    NOT NULL DEFAULT 'system'
);

CREATE INDEX snapshots_cat_time ON snapshots (category_id, published_at DESC);

CREATE TABLE snapshot_entries (
  snapshot_id INTEGER NOT NULL REFERENCES snapshots(id) ON DELETE CASCADE,
  domain      TEXT    NOT NULL,
  PRIMARY KEY (snapshot_id, domain)
) WITHOUT ROWID;

CREATE TABLE users (
  id            INTEGER PRIMARY KEY,
  username      TEXT    NOT NULL UNIQUE,
  password_hash TEXT    NOT NULL,
  role          TEXT    NOT NULL DEFAULT 'viewer' CHECK (role IN ('admin','viewer')),
  active        INTEGER NOT NULL DEFAULT 1,
  last_login_at TEXT,
  created_at    TEXT    NOT NULL
);

CREATE TABLE sessions (
  token      TEXT    PRIMARY KEY,   -- băm SHA-256 của token thật
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  csrf_token TEXT    NOT NULL,
  expires_at TEXT    NOT NULL,
  created_at TEXT    NOT NULL,
  user_agent TEXT    NOT NULL DEFAULT '',
  ip         TEXT    NOT NULL DEFAULT ''
) WITHOUT ROWID;

CREATE INDEX sessions_user   ON sessions (user_id);
CREATE INDEX sessions_expiry ON sessions (expires_at);

CREATE TABLE settings (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  updated_by TEXT NOT NULL DEFAULT 'system'
) WITHOUT ROWID;

CREATE TABLE unblock_requests (
  id          INTEGER PRIMARY KEY,
  domain      TEXT    NOT NULL,
  note        TEXT    NOT NULL DEFAULT '',
  requested_by TEXT   NOT NULL,
  state       TEXT    NOT NULL DEFAULT 'pending'
              CHECK (state IN ('pending','accepted','rejected')),
  created_at  TEXT    NOT NULL,
  resolved_at TEXT
);

CREATE INDEX unblock_requests_state ON unblock_requests (state, created_at DESC);

-- Hàng đợi job chạy trên chính CSDL, thay cho river (river chỉ hỗ trợ PostgreSQL).
CREATE TABLE jobs (
  id           TEXT    PRIMARY KEY,
  kind         TEXT    NOT NULL,
  args         TEXT    NOT NULL DEFAULT '{}',
  state        TEXT    NOT NULL DEFAULT 'pending'
               CHECK (state IN ('pending','running','done','failed','cancelled')),
  attempt      INTEGER NOT NULL DEFAULT 0,
  max_attempts INTEGER NOT NULL DEFAULT 3,
  progress_done  INTEGER NOT NULL DEFAULT 0,
  progress_total INTEGER NOT NULL DEFAULT 0,
  error        TEXT    NOT NULL DEFAULT '',
  run_at       TEXT    NOT NULL,
  started_at   TEXT,
  finished_at  TEXT,
  created_at   TEXT    NOT NULL
) WITHOUT ROWID;

CREATE INDEX jobs_claim ON jobs (state, run_at);
CREATE INDEX jobs_time  ON jobs (created_at DESC);
