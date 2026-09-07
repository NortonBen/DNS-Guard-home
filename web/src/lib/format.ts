/**
 * Định dạng hiển thị theo quy ước Việt Nam.
 *
 * Tập trung ở một chỗ để số và ngày giờ hiện giống nhau trên toàn bộ giao diện: người
 * vận hành so sánh các con số giữa nhiều màn hình, và hai kiểu định dạng khác nhau
 * cho cùng một đại lượng là nguồn nhầm lẫn không đáng có.
 */

const numberFormatter = new Intl.NumberFormat('vi-VN');

/** Số lượng lớn dùng dấu chấm phân cách nghìn: 182.451 */
export function formatNumber(value: number | null | undefined): string {
  if (value === null || value === undefined) return '—';
  return numberFormatter.format(value);
}

/** Điểm số hiện một chữ số thập phân, dấu phẩy thập phân theo quy ước Việt Nam. */
export function formatScore(value: number | null | undefined): string {
  if (value === null || value === undefined) return '—';
  return value.toFixed(1).replace('.', ',');
}

/** Trọng số hiện kèm dấu để đọc được ngay là cộng hay trừ điểm. */
export function formatWeight(value: number): string {
  const sign = value > 0 ? '+' : '';
  return `${sign}${value.toFixed(1).replace('.', ',')}`;
}

export function formatPercent(ratio: number | null | undefined): string {
  if (ratio === null || ratio === undefined) return '—';
  return `${(ratio * 100).toFixed(1).replace('.', ',')}%`;
}

const dateTimeFormatter = new Intl.DateTimeFormat('vi-VN', {
  day: '2-digit',
  month: '2-digit',
  year: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
});

const timeFormatter = new Intl.DateTimeFormat('vi-VN', {
  hour: '2-digit',
  minute: '2-digit',
});

/** Thời gian hiển thị theo múi giờ trình duyệt. */
export function formatDateTime(iso: string | null | undefined): string {
  if (!iso) return '—';
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return '—';
  return dateTimeFormatter.format(date);
}

export function formatTime(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return '';
  return timeFormatter.format(date);
}

/** Tooltip hiện UTC để đối chiếu với log của máy chủ. */
export function formatUTC(iso: string | null | undefined): string {
  if (!iso) return '';
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return '';
  return `${date.toISOString().slice(0, 19).replace('T', ' ')} UTC`;
}

/** Khoảng thời gian tương đối, dùng cho cột "thấy lần cuối". */
export function formatRelative(iso: string | null | undefined): string {
  if (!iso) return '—';
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return '—';

  const seconds = Math.floor((Date.now() - date.getTime()) / 1000);
  if (seconds < 60) return 'vừa xong';
  if (seconds < 3600) return `${Math.floor(seconds / 60)} phút trước`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)} giờ trước`;
  if (seconds < 2592000) return `${Math.floor(seconds / 86400)} ngày trước`;
  return formatDateTime(iso);
}

/** Khoảng thời gian dạng ngắn cho các thẻ sức khỏe. */
export function formatDuration(seconds: number): string {
  if (seconds < 0) return '—';
  if (seconds < 60) return `${seconds} giây`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)} phút`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)} giờ`;
  return `${Math.floor(seconds / 86400)} ngày`;
}

/** Checksum rút gọn cho bảng lịch sử xuất bản. */
export function shortChecksum(checksum: string): string {
  const value = checksum.replace(/^sha256:/, '');
  return value.slice(0, 10);
}

/** Kích thước file dạng người đọc được. */
export function formatBytes(bytes: number | null | undefined): string {
  if (bytes === null || bytes === undefined) return '—';
  if (bytes < 1024) return `${bytes} B`;

  const units = ['KB', 'MB', 'GB', 'TB'];
  let value = bytes / 1024;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return `${value.toFixed(1).replace('.', ',')} ${units[unit]}`;
}
