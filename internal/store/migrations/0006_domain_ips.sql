-- Ánh xạ domain → IP quan sát được trên dây, lấy từ bản ghi trả lời DNS mà router
-- mirror sang cổng TZSP.
--
-- Khác với ASN trong domain_facts: chỗ đó là kết quả DNSGuard tự phân giải lúc làm
-- giàu, từ một máy khác vào một thời điểm khác. Bảng này là câu trả lời mà thiết bị
-- trong mạng thật sự nhận được — thứ duy nhất dùng làm bằng chứng điều tra được.
--
-- Không có cột client. IP nguồn của một câu trả lời là resolver, còn IP đích chỉ
-- đúng ở chiều router → client chứ không đúng ở chiều upstream → router; lấy một
-- trong hai làm client đều sai. Quy kết client lấy bằng cách nối với query_events,
-- vốn đã ghi đúng thiết bị nào hỏi tên nào lúc nào.
CREATE TABLE domain_ips (
  domain_id  INTEGER NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
  ip         TEXT    NOT NULL,
  first_seen TEXT    NOT NULL,
  last_seen  TEXT    NOT NULL,
  hits       INTEGER NOT NULL DEFAULT 1,
  -- TTL nhỏ nhất từng thấy. Phân biệt CDN xoay IP mỗi phút với máy chủ cố định, và
  -- một TTL rất thấp trên domain lạ là dấu hiệu của fast-flux.
  ttl        INTEGER NOT NULL DEFAULT 0,
  -- Điền sau bằng bảng tra iptoasn cục bộ, không gọi dịch vụ ngoài.
  asn        INTEGER,
  country    TEXT,
  org        TEXT,
  PRIMARY KEY (domain_id, ip)
) WITHOUT ROWID;

-- Điều tra ngược: "IP này phục vụ những domain nào trong mạng". Đây là truy vấn mở
-- đầu của mọi lần điều tra bắt đầu từ một IP trong cảnh báo.
CREATE INDEX domain_ips_ip ON domain_ips (ip);

-- Hàng đợi tra vị trí: dòng chưa có quốc gia, mới nhất trước.
CREATE INDEX domain_ips_pending_geo ON domain_ips (last_seen DESC) WHERE country IS NULL;
