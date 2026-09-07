-- Dữ liệu khởi tạo bắt buộc: taxonomy tám phân loại ở docs/06-classification.md §1.
-- Đây là dữ liệu duy nhất được phép nằm trong migration; mọi thứ khác do người dùng tạo.

INSERT INTO categories (key, label_vi, label_en, description, color, enabled, score_threshold, publish_path, sort_order) VALUES
  ('ads',          'Quảng cáo',         'Ads',          'Máy chủ phục vụ banner, video ad, ad exchange, RTB', '#B24A16', 1, 5.5, 'ads.txt',          1),
  ('tracking',     'Theo dõi',          'Tracking',     'Analytics, pixel, đo lường hành vi người dùng',      '#8B5CF6', 1, 5.5, 'tracking.txt',     2),
  ('telemetry',    'Telemetry',         'Telemetry',    'Báo cáo trạng thái thiết bị và ứng dụng về nhà sản xuất', '#0891B2', 1, 5.5, 'telemetry.txt', 3),
  ('malware',      'Độc hại',           'Malware',      'Phishing, C2, phát tán mã độc',                      '#DC2626', 1, 3.0, 'malware.txt',      4),
  ('cryptomining', 'Đào tiền mã hóa',   'Cryptomining', 'Script đào trên trình duyệt',                        '#EA580C', 1, 3.0, 'cryptomining.txt', 5),
  ('adult',        'Nội dung người lớn','Adult',        'Nội dung người lớn',                                 '#BE185D', 0, 5.5, 'adult.txt',        6),
  ('cdn',          'CDN dùng chung',    'Shared CDN',   'Hạ tầng phục vụ cả nội dung lẫn quảng cáo — không bao giờ chặn', '#059669', 0, 99.0, 'cdn.txt', 7),
  ('content',      'Nội dung',          'Content',      'Domain first-party bình thường',                     '#64748B', 0, 99.0, 'content.txt',      8);
