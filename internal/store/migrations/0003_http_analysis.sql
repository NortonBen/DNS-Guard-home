-- Thêm hai nguồn làm giàu: phân tích HTTP/HTML tĩnh và tra cứu VirusTotal.
--
-- SQLite không sửa được ràng buộc CHECK tại chỗ, nên phải dựng bảng mới, chép dữ
-- liệu sang, xóa bảng cũ rồi đổi tên. Không bảng nào tham chiếu tới domain_facts nên
-- thao tác này an toàn; migration chạy trong một transaction nên hỏng giữa chừng thì
-- không để lại bảng nửa vời.

CREATE TABLE domain_facts_new (
  domain_id  INTEGER NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
  source     TEXT    NOT NULL
             CHECK (source IN ('dns','asn','cert','rdap','rank','http','vt')),
  data       TEXT    NOT NULL DEFAULT '{}',
  fetched_at TEXT    NOT NULL,
  expires_at TEXT    NOT NULL,
  error      TEXT,
  PRIMARY KEY (domain_id, source)
) WITHOUT ROWID;

INSERT INTO domain_facts_new (domain_id, source, data, fetched_at, expires_at, error)
SELECT domain_id, source, data, fetched_at, expires_at, error FROM domain_facts;

DROP TABLE domain_facts;

ALTER TABLE domain_facts_new RENAME TO domain_facts;

CREATE INDEX domain_facts_expiry ON domain_facts (expires_at) WHERE error IS NULL;
