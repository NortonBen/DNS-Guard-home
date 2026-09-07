import { Link } from '@tanstack/react-router';
import { lazy, Suspense, useState } from 'react';

import { useHealth, useOverview, useSession, useTop, useUnblockRequests, useResolveUnblockRequest } from '@/api/hooks';
import { Button, Card, EmptyState, Skeleton, StatusBadge, cx } from '@/components/ui/primitives';
import { formatDuration, formatNumber, formatPercent, formatRelative } from '@/lib/format';

// Recharts là phần nặng nhất của bundle; nạp động để các trang khác không phải tải.
const QueryChart = lazy(() =>
  import('@/components/charts/query-chart').then((m) => ({ default: m.QueryChart })),
);

/**
 * Dashboard trả lời đúng một câu hỏi: hệ thống có đang khỏe không, và có gì cần tôi làm.
 */
export function DashboardScreen() {
  const session = useSession();
  const overview = useOverview(24);
  const health = useHealth();
  const isAdmin = session.data?.user.role === 'admin';

  // Khoảng xem của ba bảng xếp hạng. Các thẻ số và biểu đồ theo giờ giữ nguyên 24 giờ:
  // chúng ghi rõ "24 giờ" trên nhãn, và một biểu đồ theo giờ trải cả năm thì không
  // đọc được.
  const [topHours, setTopHours] = useState(24);

  const topQueried = useTop('domain', topHours);
  const topBlocked = useTop('blocked', topHours);
  const topClients = useTop('client', topHours);
  const unblockRequests = useUnblockRequests(isAdmin);

  const data = overview.data;

  return (
    <div className="space-y-4">
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard
          label="Truy vấn 24 giờ"
          value={data ? formatNumber(data.total_queries) : null}
          hint={data ? `${formatNumber(data.unique_domains)} domain phân biệt` : ''}
        />
        <StatCard
          label="Tỉ lệ chặn"
          value={data ? formatPercent(data.blocked_ratio) : null}
          hint={data ? `${formatNumber(data.blocked_queries)} truy vấn bị chặn` : ''}
        />
        <StatCard
          label="Chờ duyệt"
          value={data ? formatNumber(data.pending_review) : null}
          hint={isAdmin ? 'Bấm để mở màn duyệt' : ''}
          // Đây là lối vào chính của người dùng hằng ngày.
          to={isAdmin ? '/triage' : undefined}
          highlight={(data?.pending_review ?? 0) > 0}
        />
        <HealthCard />
      </div>

      <Card title="Truy vấn theo giờ, 24 giờ gần nhất">
        {overview.isPending ? (
          <Skeleton className="h-56 w-full" />
        ) : data && data.timeseries.length > 0 ? (
          <Suspense fallback={<Skeleton className="h-56 w-full" />}>
            <QueryChart buckets={data.timeseries} />
          </Suspense>
        ) : (
          <EmptyState>Chưa có dữ liệu truy vấn trong 24 giờ qua</EmptyState>
        )}
      </Card>

      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-sm font-semibold">Bảng xếp hạng</h2>
        <RangePicker value={topHours} onChange={setTopHours} />
      </div>

      <div className="grid gap-4 lg:grid-cols-2 xl:grid-cols-3">
        {/* Truy cập nhiều nhất đứng trước bị chặn nhiều nhất: nó trả lời câu hỏi
            "mạng này thật sự đang dùng gì", còn bảng bị chặn chỉ nói về phần đã
            quyết định rồi. */}
        <Card title="Domain truy cập nhiều nhất">
          <TopTable
            rows={topQueried.data?.items ?? []}
            loading={topQueried.isPending}
            linkToDomain
          />
        </Card>

        <Card title="Domain bị chặn nhiều nhất">
          <TopTable
            rows={topBlocked.data?.items ?? []}
            loading={topBlocked.isPending}
            linkToDomain
          />
        </Card>

        {isAdmin ? (
          <Card title="Client hoạt động nhiều nhất">
            <TopTable rows={topClients.data?.items ?? []} loading={topClients.isPending} />
          </Card>
        ) : (
          <Card title="Phân bố theo phân loại">
            <CategoryBreakdown items={data?.by_category ?? []} total={data?.total_queries ?? 0} />
          </Card>
        )}
      </div>

      {isAdmin && (unblockRequests.data?.items.length ?? 0) > 0 && (
        <UnblockRequestQueue items={unblockRequests.data?.items ?? []} />
      )}

      {health.data && health.data.status !== 'ok' && <HealthDetails checks={health.data.checks} />}
    </div>
  );
}

interface StatCardProps {
  label: string;
  value: string | null;
  hint?: string;
  to?: string;
  highlight?: boolean;
}

function StatCard({ label, value, hint, to, highlight }: StatCardProps) {
  const content = (
    <>
      <p className="text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400">
        {label}
      </p>
      {value === null ? (
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
      {hint && <p className="mt-0.5 text-xs text-slate-500 dark:text-slate-400">{hint}</p>}
    </>
  );

  const className = cx(
    'block rounded-lg bg-white p-4 ring-1 ring-slate-200',
    'dark:bg-slate-900 dark:ring-slate-800',
    to && 'transition-colors hover:ring-sky-400 dark:hover:ring-sky-600',
  );

  return to ? (
    <Link to={to} className={className}>
      {content}
    </Link>
  ) : (
    <div className={className}>{content}</div>
  );
}

/** Thẻ sức khỏe đổi màu theo /health, và hiện luôn nguyên nhân chứ không bắt bấm vào. */
function HealthCard() {
  const health = useHealth();
  const status = health.data?.status;

  const tone =
    status === 'ok'
      ? 'text-emerald-600 dark:text-emerald-400'
      : status === 'degraded'
        ? 'text-amber-600 dark:text-amber-400'
        : 'text-red-600 dark:text-red-400';

  const label =
    status === 'ok' ? 'Bình thường' : status === 'degraded' ? 'Suy giảm' : status === 'down' ? 'Ngừng' : '—';

  const problems = Object.entries(health.data?.checks ?? {})
    .filter(([, check]) => !check.ok)
    .map(([name]) => name);

  return (
    <div className="rounded-lg bg-white p-4 ring-1 ring-slate-200 dark:bg-slate-900 dark:ring-slate-800">
      <p className="text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400">
        Sức khỏe
      </p>
      {health.isPending ? (
        <Skeleton className="mt-1 h-8 w-24" />
      ) : (
        <p className={cx('mt-1 text-2xl font-semibold', tone)}>{label}</p>
      )}
      <p className="mt-0.5 text-xs text-slate-500 dark:text-slate-400">
        {problems.length > 0 ? `Có vấn đề: ${problems.join(', ')}` : 'Mọi kiểm tra đều đạt'}
      </p>
    </div>
  );
}

function HealthDetails({ checks }: { checks: Record<string, { ok: boolean; message?: string; last_event_age_s?: number; pending?: number; failed_24h?: number }> }) {
  return (
    <Card title="Chi tiết kiểm tra sức khỏe">
      <ul className="space-y-1.5 text-sm">
        {Object.entries(checks).map(([name, check]) => (
          <li key={name} className="flex items-start gap-2">
            <span aria-hidden className={check.ok ? 'text-emerald-600' : 'text-red-600'}>
              {check.ok ? '●' : '▲'}
            </span>
            <span className="font-medium">{name}</span>
            <span className="text-slate-500 dark:text-slate-400">
              {check.message ||
                (check.last_event_age_s !== undefined && check.last_event_age_s >= 0
                  ? `truy vấn gần nhất ${formatDuration(check.last_event_age_s)} trước`
                  : check.failed_24h
                    ? `${check.failed_24h} job hỏng trong 24 giờ`
                    : check.ok
                      ? 'đạt'
                      : 'không đạt')}
            </span>
          </li>
        ))}
      </ul>
    </Card>
  );
}

interface TopRow {
  key: string;
  label?: string;
  queries: number;
  status?: string;
  domain_id?: number;
}

const topRanges = [
  { hours: 24, label: '24 giờ' },
  { hours: 24 * 7, label: '7 ngày' },
  { hours: 24 * 30, label: '30 ngày' },
  { hours: 0, label: 'Toàn bộ' },
];

/**
 * Chọn khoảng thời gian cho các bảng xếp hạng.
 *
 * Nút bấm chứ không phải danh sách xổ: chỉ có bốn lựa chọn, và ở đây người dùng
 * thường bấm qua lại giữa chúng để so sánh chứ không chọn một lần rồi thôi.
 */
function RangePicker({ value, onChange }: { value: number; onChange: (hours: number) => void }) {
  return (
    <div className="flex gap-1" role="group" aria-label="Khoảng thời gian xếp hạng">
      {topRanges.map((r) => (
        <Button
          key={r.hours}
          variant={value === r.hours ? 'primary' : 'secondary'}
          aria-pressed={value === r.hours}
          className="px-2 py-1 text-xs"
          onClick={() => onChange(r.hours)}
        >
          {r.label}
        </Button>
      ))}
    </div>
  );
}

function TopTable({
  rows,
  loading,
  linkToDomain,
}: {
  rows: TopRow[];
  loading: boolean;
  linkToDomain?: boolean;
}) {
  if (loading) {
    return (
      <div className="space-y-2">
        {Array.from({ length: 5 }, (_, i) => (
          <Skeleton key={i} className="h-6 w-full" />
        ))}
      </div>
    );
  }
  if (rows.length === 0) return <EmptyState>Chưa có dữ liệu</EmptyState>;

  // Cột trạng thái quyết định cho cả bảng chứ không theo từng dòng: bảng xếp hạng
  // theo lượt truy cập trộn cả domain đã chặn lẫn chưa xét, và bỏ ô ở những dòng
  // không có trạng thái sẽ làm các cột lệch nhau.
  const hasStatus = rows.some((r) => Boolean(r.status));

  return (
    <table className="w-full text-sm">
      <thead className="sr-only">
        <tr>
          <th scope="col">Tên</th>
          <th scope="col">Số truy vấn</th>
          {hasStatus && <th scope="col">Trạng thái</th>}
        </tr>
      </thead>
      <tbody>
        {rows.map((row) => (
          <tr key={row.key} className="border-b border-slate-100 last:border-0 dark:border-slate-800">
            <td className="min-w-0 max-w-0 py-1.5 pr-2">
              {linkToDomain && row.domain_id ? (
                <Link
                  to="/domains/$domainId"
                  params={{ domainId: String(row.domain_id) }}
                  className="domain-name block truncate text-sky-700 hover:underline dark:text-sky-400"
                  title={row.key}
                >
                  {row.key}
                </Link>
              ) : (
                <span className="domain-name block truncate" title={row.key}>
                  {row.key}
                </span>
              )}
              {row.label && (
                <span className="ml-2 text-xs text-slate-500 dark:text-slate-400">{row.label}</span>
              )}
            </td>
            <td className="w-24 py-1.5 text-right tabular-nums text-slate-600 dark:text-slate-300">
              {formatNumber(row.queries)}
            </td>
            {hasStatus && (
              <td className="w-24 py-1.5 text-right">
                {row.status && <StatusBadge status={row.status as never} />}
              </td>
            )}
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function CategoryBreakdown({
  items,
  total,
}: {
  items: { key: string; queries: number }[];
  total: number;
}) {
  if (items.length === 0) return <EmptyState>Chưa có dữ liệu phân loại</EmptyState>;

  return (
    <ul className="space-y-2">
      {items.map((item) => (
        <li key={item.key}>
          <div className="flex justify-between text-sm">
            <span>{item.key}</span>
            <span className="tabular-nums text-slate-500 dark:text-slate-400">
              {formatNumber(item.queries)}
            </span>
          </div>
          <div className="mt-1 h-1.5 overflow-hidden rounded-full bg-slate-100 dark:bg-slate-800">
            <div
              className="h-full rounded-full bg-sky-500"
              style={{ width: `${total > 0 ? (item.queries / total) * 100 : 0}%` }}
            />
          </div>
        </li>
      ))}
    </ul>
  );
}

function UnblockRequestQueue({
  items,
}: {
  items: { id: number; domain: string; note: string; requested_by: string; created_at: string }[];
}) {
  const resolve = useResolveUnblockRequest();

  return (
    <Card title={`Yêu cầu mở chặn đang chờ (${items.length})`}>
      <ul className="divide-y divide-slate-100 dark:divide-slate-800">
        {items.map((request) => (
          <li key={request.id} className="flex items-center gap-3 py-2">
            <div className="min-w-0 flex-1">
              <p className="domain-name truncate">{request.domain}</p>
              <p className="truncate text-xs text-slate-500 dark:text-slate-400">
                {request.requested_by} · {formatRelative(request.created_at)}
                {request.note && ` · ${request.note}`}
              </p>
            </div>
            <Button
              variant="secondary"
              onClick={() =>
                resolve.mutate({ id: request.id, accept: true, reason: 'Chấp nhận yêu cầu mở chặn' })
              }
            >
              Cho qua
            </Button>
            <Button
              variant="ghost"
              onClick={() =>
                resolve.mutate({ id: request.id, accept: false, reason: 'Từ chối yêu cầu' })
              }
            >
              Từ chối
            </Button>
          </li>
        ))}
      </ul>
    </Card>
  );
}
