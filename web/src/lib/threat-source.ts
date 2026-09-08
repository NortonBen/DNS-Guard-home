/**
 * Tên hiển thị của các danh sách hạ tầng độc hại.
 *
 * Khóa nguồn là thứ API trả về; người vận hành cần tên đọc được để biết mức phản ứng.
 * Khớp một dải bị chiếm đoạt nghĩa là "địa chỉ này nằm trong khu phố xấu"; khớp một
 * máy chủ điều khiển nghĩa là "có máy trong mạng đang nói chuyện với kẻ tấn công".
 */
const LABELS: Record<string, string> = {
  spamhaus_drop: 'Spamhaus DROP',
  threatfox: 'ThreatFox',
};

/** threatSourceLabel trả về tên đọc được, lùi về chính khóa khi nguồn còn lạ. */
export function threatSourceLabel(source: string | undefined): string {
  if (!source) return '';
  return LABELS[source] ?? source;
}
