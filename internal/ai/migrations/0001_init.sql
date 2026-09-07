-- Lược đồ của CSDL nhật ký AI — một file tách hẳn khỏi dnsguard.db.
--
-- Không bảng nào ở đây có khoá ngoại trỏ sang CSDL chính: hai file mở độc lập, và
-- SQLite không kiểm tra được ràng buộc bắc qua hai kết nối. Domain vì thế được
-- tham chiếu bằng TÊN. Đổi lại, xoá file này không làm hỏng một quyết định chặn
-- nào, và sao lưu CSDL chính không phải kéo theo hàng nghìn dòng prompt.

-- Một lượt gọi model. Lưu cả prompt lẫn phản hồi thô để người vận hành kiểm được
-- vì sao AI kết luận như vậy — không có hai cột đó thì lịch sử chỉ là một con số.
CREATE TABLE ai_requests (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  kind          TEXT    NOT NULL CHECK (kind IN ('classify','recheck','ask')),
  model         TEXT    NOT NULL DEFAULT '',
  base_url      TEXT    NOT NULL DEFAULT '',
  domain_count  INTEGER NOT NULL DEFAULT 0,
  parsed_count  INTEGER NOT NULL DEFAULT 0,
  skipped_count INTEGER NOT NULL DEFAULT 0,
  prompt        TEXT    NOT NULL DEFAULT '',
  response      TEXT    NOT NULL DEFAULT '',
  prompt_chars  INTEGER NOT NULL DEFAULT 0,
  reply_chars   INTEGER NOT NULL DEFAULT 0,
  latency_ms    INTEGER NOT NULL DEFAULT 0,
  error         TEXT    NOT NULL DEFAULT '',
  actor         TEXT    NOT NULL DEFAULT 'system',
  created_at    TEXT    NOT NULL
);

CREATE INDEX ai_requests_created ON ai_requests (created_at DESC);
CREATE INDEX ai_requests_kind ON ai_requests (kind, created_at DESC);

-- Kết luận cho từng domain trong một lượt. Tách khỏi ai_requests để tra được
-- "domain này đã bị hỏi mấy lần, và câu trả lời có đổi không".
CREATE TABLE ai_verdicts (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  request_id INTEGER NOT NULL REFERENCES ai_requests(id) ON DELETE CASCADE,
  domain     TEXT    NOT NULL,
  category   TEXT    NOT NULL,
  confidence REAL    NOT NULL DEFAULT 0,
  reason     TEXT    NOT NULL DEFAULT '',
  created_at TEXT    NOT NULL
);

CREATE INDEX ai_verdicts_domain ON ai_verdicts (domain, created_at DESC);
CREATE INDEX ai_verdicts_request ON ai_verdicts (request_id);

-- Skill là một mẩu quy ước vận hành chèn vào ngữ cảnh khi câu hỏi khớp từ khoá.
-- Rẻ hơn nhiều so với nhồi mọi quy ước vào một system prompt khổng lồ.
CREATE TABLE ai_skills (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  name        TEXT    NOT NULL UNIQUE,
  description TEXT    NOT NULL DEFAULT '',
  triggers    TEXT    NOT NULL DEFAULT '[]',
  content     TEXT    NOT NULL DEFAULT '',
  always      INTEGER NOT NULL DEFAULT 0,
  enabled     INTEGER NOT NULL DEFAULT 1,
  builtin     INTEGER NOT NULL DEFAULT 0,
  created_at  TEXT    NOT NULL,
  updated_at  TEXT    NOT NULL
);

-- Máy chủ MCP ngoài, cung cấp thêm công cụ cho model.
--
-- auth_header có thể chứa token nên không bao giờ được trả ra API.
CREATE TABLE ai_mcp_servers (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  name        TEXT    NOT NULL UNIQUE,
  url         TEXT    NOT NULL,
  auth_header TEXT    NOT NULL DEFAULT '',
  enabled     INTEGER NOT NULL DEFAULT 1,
  note        TEXT    NOT NULL DEFAULT '',
  created_at  TEXT    NOT NULL,
  updated_at  TEXT    NOT NULL
);

-- Hội thoại hỏi đáp. Giữ lại để người dùng quay lại đọc, và để một câu hỏi tiếp
-- theo có ngữ cảnh của những lượt trước.
CREATE TABLE ai_chats (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  title      TEXT    NOT NULL DEFAULT '',
  actor      TEXT    NOT NULL DEFAULT '',
  created_at TEXT    NOT NULL,
  updated_at TEXT    NOT NULL
);

CREATE INDEX ai_chats_updated ON ai_chats (updated_at DESC);

CREATE TABLE ai_messages (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  chat_id    INTEGER NOT NULL REFERENCES ai_chats(id) ON DELETE CASCADE,
  role       TEXT    NOT NULL CHECK (role IN ('user','assistant')),
  content    TEXT    NOT NULL DEFAULT '',
  -- steps là dấu vết gọi công cụ dạng JSON. Không có nó thì agent là hộp đen:
  -- người dùng thấy câu trả lời mà không biết nó dựa trên dữ liệu nào.
  steps      TEXT    NOT NULL DEFAULT '[]',
  created_at TEXT    NOT NULL
);

CREATE INDEX ai_messages_chat ON ai_messages (chat_id, id);
