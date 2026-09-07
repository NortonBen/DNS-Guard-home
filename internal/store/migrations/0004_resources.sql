-- Mẫu đo tài nguyên của chính tiến trình, lấy mỗi mười giây.
--
-- Ba mươi ngày ở nhịp mười giây là khoảng 260 nghìn dòng — nhỏ so với query_events,
-- nhưng vẫn cần index trên cột thời gian vì mọi truy vấn đều lọc theo khoảng.
CREATE TABLE resource_samples (
  at          TEXT    PRIMARY KEY,
  cpu_percent REAL    NOT NULL,
  rss_bytes   INTEGER NOT NULL,
  heap_bytes  INTEGER NOT NULL,
  goroutines  INTEGER NOT NULL
) WITHOUT ROWID;
