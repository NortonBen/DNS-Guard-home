import type { Signal } from '@/api/types';
import { cx } from '@/components/ui/primitives';
import { formatWeight } from '@/lib/format';

/**
 * Nhãn tiếng Việt cho từng loại tín hiệu.
 *
 * Nhân bản từ classify.SignalLabels ở backend: giao diện phải hiển thị được tín hiệu
 * ngay khi tải, không đợi thêm một vòng gọi API chỉ để lấy nhãn.
 */
const signalLabels: Record<string, string> = {
  cname_adtech: 'CNAME tới hạ tầng adtech',
  cname_blocked: 'CNAME tới domain đã bị chặn',
  asn_adtech: 'ASN thuần adtech',
  cert_adtech: 'Chứng chỉ chung với adtech',
  keyword: 'Tên chứa từ khóa quảng cáo',
  random_sub: 'Subdomain sinh ngẫu nhiên',
  third_party: 'Luôn xuất hiện sau domain khác',
  third_party_weak: 'Thường xuất hiện sau domain khác',
  fan_out: 'Xuất hiện trên nhiều client',
  beacon: 'Truy vấn đều đặn như máy',
  spread_high: 'Rất nhiều subdomain cùng gốc',
  spread_mid: 'Nhiều subdomain cùng gốc',
  etld1_blocked: 'Tên miền gốc đã bị chặn',
  high_rank: 'Thứ hạng Tranco cao',
  long_lived_content: 'Domain lâu đời, không dấu hiệu hạ tầng',
  shared_cdn: 'Phân giải về CDN dùng chung',
};

/** Chi tiết bằng chứng, dựng thành câu đọc được thay vì đổ JSON thô ra màn hình. */
function describeDetail(kind: string, detail?: Record<string, unknown>): string {
  if (!detail) return '';
  const value = (key: string) => detail[key];

  switch (kind) {
    case 'cname_adtech':
    case 'cname_blocked':
      return String(value('cname') ?? value('cname_etld1') ?? '');
    case 'asn_adtech':
      return `AS${value('asn')} ${value('org') ?? ''}`.trim();
    case 'cert_adtech':
      return String(value('san') ?? '');
    case 'keyword':
      return `"${value('keyword')}"`;
    case 'random_sub':
      return `${value('label')} · entropy ${value('entropy')}`;
    case 'third_party':
    case 'third_party_weak':
      return `${Math.round(Number(value('ratio')) * 100)}% lần xuất hiện`;
    case 'fan_out':
      return `${value('clients')} client`;
    case 'beacon':
      return `hệ số biến thiên ${value('cv')}`;
    case 'spread_high':
    case 'spread_mid':
      return `${value('subdomains')} subdomain`;
    case 'etld1_blocked':
      return String(value('etld1') ?? '');
    case 'high_rank':
      return `Tranco #${value('tranco')}`;
    case 'long_lived_content':
      return `${value('age_days')} ngày tuổi`;
    case 'shared_cdn':
      return String(value('cdn') ?? '');
    default:
      return '';
  }
}

/**
 * Danh sách bằng chứng dẫn tới điểm số.
 *
 * Hiển thị cả trọng số lẫn chi tiết cụ thể: người vận hành nhìn 7,5 phải thấy ngay
 * đó là 6,0 + 1,5 từ đâu ra. Một điểm số không giải thích được thì vô dụng.
 */
export function SignalBadges({ signals, compact }: { signals: Signal[] | null; compact?: boolean }) {
  if (!signals || signals.length === 0) {
    return <p className="text-sm text-slate-500 dark:text-slate-400">Chưa chấm điểm</p>;
  }

  return (
    <ul className={cx('space-y-1', compact && 'flex flex-wrap gap-1 space-y-0')}>
      {signals.map((signal, index) => {
        const positive = signal.weight > 0;
        const detail = describeDetail(signal.kind, signal.detail);

        if (compact) {
          return (
            <li
              key={`${signal.kind}-${index}`}
              title={`${signalLabels[signal.kind] ?? signal.kind}${detail ? ` — ${detail}` : ''}`}
              className={cx(
                'inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-xs font-medium',
                positive
                  ? 'bg-red-50 text-red-700 dark:bg-red-950 dark:text-red-300'
                  : 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300',
              )}
            >
              {signalLabels[signal.kind] ?? signal.kind}
              <span className="tabular-nums opacity-75">{formatWeight(signal.weight)}</span>
            </li>
          );
        }

        return (
          <li key={`${signal.kind}-${index}`} className="flex items-baseline gap-2 text-sm">
            <span
              className={cx(
                'w-12 shrink-0 text-right font-medium tabular-nums',
                positive ? 'text-red-600 dark:text-red-400' : 'text-emerald-600 dark:text-emerald-400',
              )}
            >
              {formatWeight(signal.weight)}
            </span>
            <span className="flex-1">
              {signalLabels[signal.kind] ?? signal.kind}
              {detail && (
                <span className="ml-1.5 domain-name text-slate-500 dark:text-slate-400">{detail}</span>
              )}
            </span>
          </li>
        );
      })}
    </ul>
  );
}
