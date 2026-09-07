-- Kết quả đối chiếu địa chỉ với danh sách hạ tầng độc hại.
--
-- Đặt thành cột của domain_ips chứ không thành bảng riêng, cùng lý do như asn/country
-- /org ở migration trước: mọi truy vấn cần tới nó đều đã đọc bảng này rồi, và một
-- bảng riêng chỉ thêm một phép nối vào đúng những chỗ nóng nhất.
--
-- NULL và chuỗi rỗng ở đây **cùng nghĩa** là "không nằm trong danh sách" — khác với
-- cột country. Trạng thái đe dọa của một địa chỉ thay đổi theo thời gian vì danh sách
-- được cập nhật, nên job đối chiếu quét lại toàn bộ ở mỗi lượt chạy thay vì dựa vào
-- một dấu "đã kiểm tra". Nhờ vậy một địa chỉ bị gỡ khỏi danh sách cũng tự hết cảnh báo.
ALTER TABLE domain_ips ADD COLUMN threat TEXT;

-- Danh sách cảnh báo: chỉ vài dòng trong hàng chục nghìn, nên index bộ phận giữ nó
-- rẻ bất kể bảng lớn tới đâu.
CREATE INDEX domain_ips_threat ON domain_ips (last_seen DESC) WHERE threat IS NOT NULL;
