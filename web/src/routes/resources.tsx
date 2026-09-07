import { Suspense, lazy, useState } from 'react';

import { useResources } from '@/api/hooks';
import { Field, FilterBar, Select } from '@/components/ui/form';
import { Card, EmptyState, ErrorState, Skeleton, cx } from '@/components/ui/primitives';
import { formatBytes, formatDateTime, formatNumber, formatRelative } from '@/lib/format';

// Recharts nặng hơn phần còn lại của màn này cộng lại, nên chỉ nạp khi mở.
const CPUChart = lazy(() =>
  import('@/components/charts/resource-chart').then((m) => ({ default: m.CPUChart })),
);
const MemoryChart = lazy(() =>
  import('@/components/charts/resource-chart').then((m) => ({ default: m.MemoryChart })),
);

const ranges = [
  { hours: 12, label: '12 giờ' },
  { hours: 24, label: '24 giờ' },
  { hours: 48, label: '2 ngày' },
  { hours: 24 * 7, label: '7 ngày' },
  { hours: 24 * 14, label: '14 ngày' },
  { hours: 24 * 30, label: '30 ngày' },
];

/**
 * Mô tả độ rộng khoảng gộp bằng lời.
 *
 * Người xem cần biết một điểm trên biểu đồ đại diện cho bao lâu; nếu không, một đỉnh
 * ở khoảng ba mươi ngày trông giống hệt một đỉnh ở khoảng mười hai giờ trong khi
 * chúng nói hai chuyện khác hẳn nhau.
 */
function bucketLabel(seconds: number): string {
  if (seconds >= 3600) return `${seconds / 3600} giờ`;
  if (seconds >= 60) return `${seconds / 60} phút`;
  return `${seconds} giây`;
}

/**
 * Tài nguyên DNSGuard tự dùng.
 *
 * Câu hỏi màn này trả lời: dịch vụ có đang phình bộ nhớ theo thời gian không, và có
 * lúc nào ngốn CPU bất thường không. Cả hai chỉ thấy được khi nhìn nhiều ngày —
 * ảnh chụp một thời điểm ở màn Cài đặt không cho biết xu hướng.
 */
export function ResourcesScreen() {
  const [hours, setHours] = useState(12);
  const series = useResources(hours);

  const summary = series.data?.summary;
  const points = series.data?.points ?? [];

  return (
    <div className="space-y-3">
      <Card title="Khoảng thời gian">
        <FilterBar>
          <Field
            label="Xem lại"
            htmlFor="range"
            hint="Khoảng càng dài thì mỗi điểm càng gộp nhiều mẫu."
          >
            <Select id="range" value={String(hours)} onChange={(e) => setHours(Number(e.target.value))}>
              {ranges.map((r) => (
                <option key={r.hours} value={r.hours}>
                  {r.label}
                </option>
              ))}
            </Select>
          </Field>

          {series.data && (
            <Field label="Cách đo" htmlFor="sampling">
              <p id="sampling" className="text-sm text-slate-600 dark:text-slate-300">
                Ghi mỗi {series.data.sample_seconds} giây, gộp {bucketLabel(series.data.bucket_seconds)}{' '}
                mỗi điểm. Giữ {summary?.retain_days} ngày.
              </p>
            </Field>
          )}
        </FilterBar>
      </Card>

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <Stat label="CPU trung bình" value={summary && `${summary.cpu_avg.toFixed(1).replace('.', ',')} %`} />
        <Stat
          label="CPU đỉnh"
          value={summary && `${summary.cpu_max.toFixed(1).replace('.', ',')} %`}
          // Một lõi đầy tải kéo dài là dấu hiệu thật sự cần chú ý; dưới mức đó thì
          // đỉnh chỉ là job nền chạy xong rồi thôi.
          highlight={!!summary && summary.cpu_max >= 90}
        />
        <Stat label="RAM trung bình" value={summary && formatBytes(summary.rss_avg)} />
        <Stat label="RAM đỉnh" value={summary && formatBytes(summary.rss_max)} />
      </div>

      {series.isError ? (
        <Card title="Biểu đồ">
          <ErrorState error={series.error} />
        </Card>
      ) : series.isPending ? (
        <Card title="Biểu đồ">
          <Skeleton className="h-56 w-full" />
        </Card>
      ) : points.length === 0 ? (
        <Card title="Biểu đồ">
          <EmptyState>
            Chưa có mẫu đo nào trong khoảng này. Máy chủ ghi mỗi{' '}
            {series.data?.sample_seconds ?? 10} giây và gom một phút mới lưu xuống một lần, nên
            cần chờ khoảng một phút sau khi khởi động.
          </EmptyState>
        </Card>
      ) : (
        <>
          {/* Dữ liệu bắt đầu muộn hơn khoảng đã chọn phải nói ra: biểu đồ ngắn hơn
              mong đợi trông giống như mất dữ liệu, trong khi thật ra dịch vụ mới
              chạy chừng đó. */}
          {summary?.oldest_at && <Coverage oldestAt={summary.oldest_at} hours={hours} />}

          <Card
            title="CPU"
            actions={
              <span className="text-xs text-slate-500 dark:text-slate-400">
                {formatNumber(summary?.samples ?? 0)} mẫu
              </span>
            }
          >
            <Suspense fallback={<Skeleton className="h-56 w-full" />}>
              <CPUChart points={points} bucketSeconds={series.data.bucket_seconds} />
            </Suspense>
            <p className="mt-1 text-xs text-slate-500 dark:text-slate-400">
              Phần trăm của một lõi. Vượt 100% nghĩa là dịch vụ đang chạy trên nhiều lõi cùng lúc.
            </p>
          </Card>

          <Card title="Bộ nhớ">
            <Suspense fallback={<Skeleton className="h-56 w-full" />}>
              <MemoryChart points={points} bucketSeconds={series.data.bucket_seconds} />
            </Suspense>
            <p className="mt-1 text-xs text-slate-500 dark:text-slate-400">
              Thường trú là phần hệ điều hành thấy; heap là phần Go đang dùng. Heap phẳng mà
              thường trú tăng đều thường là runtime giữ lại bộ nhớ, không phải rò rỉ.
            </p>
          </Card>
        </>
      )}
    </div>
  );
}

function Stat({
  label,
  value,
  highlight,
}: {
  label: string;
  value: string | undefined;
  highlight?: boolean;
}) {
  return (
    <div className="rounded-lg border border-slate-200 bg-white p-3 dark:border-slate-800 dark:bg-slate-900">
      <p className="text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400">
        {label}
      </p>
      {value === undefined ? (
        <Skeleton className="mt-1 h-8 w-24" />
      ) : (
        <p
          className={cx(
            'mt-1 text-2xl font-semibold tabular-nums',
            highlight && 'text-amber-600 dark:text-amber-400',
          )}
        >
          {value}
        </p>
      )}
    </div>
  );
}

/** Cảnh báo khi dữ liệu không phủ hết khoảng đã chọn. */
function Coverage({ oldestAt, hours }: { oldestAt: string; hours: number }) {
  const oldest = new Date(oldestAt).getTime();
  const wanted = Date.now() - hours * 3600_000;

  // Nới một giờ: chênh lệch nhỏ chỉ là lúc lấy mẫu đầu tiên, không đáng báo.
  if (Number.isNaN(oldest) || oldest <= wanted + 3600_000) return null;

  return (
    <p
      className={cx(
        'rounded px-2 py-1 text-xs',
        'bg-sky-50 text-sky-800 dark:bg-sky-950 dark:text-sky-300',
      )}
    >
      Dữ liệu chỉ có từ {formatDateTime(oldestAt)} ({formatRelative(oldestAt)}). Khoảng trước đó
      trống vì dịch vụ chưa chạy hoặc mẫu đã quá hạn giữ.
    </p>
  );
}
