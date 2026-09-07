import { useMemo, useRef } from 'react';
import { Link, useNavigate, useSearch } from '@tanstack/react-router';
import { useVirtualizer } from '@tanstack/react-virtual';

import { useCategories, useDomains } from '@/api/hooks';
import { Field, FilterBar, Select, TextInput } from '@/components/ui/form';
import {
  Card,
  EmptyState,
  ErrorState,
  Spinner,
  StatusBadge,
  TableScroll,
  cx,
} from '@/components/ui/primitives';
import { formatDateTime, formatNumber, formatRelative, formatScore } from '@/lib/format';
import { originLabels, statusLabels } from '@/lib/strings';

const statuses = ['new', 'staging', 'blocked', 'allowed', 'ignored'] as const;

/**
 * Bảng ảo hóa cho toàn bộ CSDL domain.
 *
 * Chỉ render những dòng nằm trong khung nhìn: bảng có thể tới nửa triệu dòng, và tải
 * hết vào bộ nhớ sẽ giết trình duyệt. Dữ liệu lấy theo trang bằng con trỏ, tải thêm
 * khi cuộn gần cuối.
 */
export function DomainsScreen() {
  const search = useSearch({ from: '/domains' });
  const navigate = useNavigate();
  const categories = useCategories();
  const parentRef = useRef<HTMLDivElement>(null);

  const query = useDomains({
    q: search.q,
    status: search.status,
    category: search.category,
    sort: search.sort,
    limit: 100,
  });

  const rows = useMemo(
    () => query.data?.pages.flatMap((page) => page.items) ?? [],
    [query.data],
  );

  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => 44,
    overscan: 12,
  });

  // Tải trang kế tiếp khi người dùng cuộn gần cuối danh sách đã có.
  const virtualRows = virtualizer.getVirtualItems();
  const lastRendered = virtualRows.at(-1);
  if (
    lastRendered &&
    lastRendered.index >= rows.length - 20 &&
    query.hasNextPage &&
    !query.isFetchingNextPage
  ) {
    void query.fetchNextPage();
  }

  function setFilter(patch: Partial<typeof search>) {
    void navigate({ to: '/domains', search: { ...search, ...patch } });
  }

  return (
    <div className="space-y-3">
      <Card title="Bộ lọc">
        <FilterBar>
          <Field
            label="Tìm kiếm"
            htmlFor="q"
            // Ô tìm kiếm chiếm cả hàng khi lưới còn hai cột, để cú pháp dài không bị
            // ép xuống dòng.
            className="sm:col-span-2 xl:col-span-1"
            hint={
              <>
                Chứa chuỗi · <code>*.hậu-tố</code> khớp hậu tố · <code>/biểu-thức/</code> chính quy
              </>
            }
          >
            <TextInput
              id="q"
              mono
              defaultValue={search.q}
              onChange={(e) => setFilter({ q: e.target.value })}
              placeholder="doubleclick · *.eulerian.net · /^ad[0-9]+\./"
            />
          </Field>

          <Field label="Trạng thái" htmlFor="status">
            <Select
              id="status"
              value={search.status}
              onChange={(e) => setFilter({ status: e.target.value })}
            >
              <option value="">Tất cả</option>
              {statuses.map((status) => (
                <option key={status} value={status}>
                  {statusLabels[status]}
                </option>
              ))}
            </Select>
          </Field>

          <Field label="Phân loại" htmlFor="category">
            <Select
              id="category"
              value={search.category}
              onChange={(e) => setFilter({ category: e.target.value })}
            >
              <option value="">Tất cả</option>
              {categories.data?.items.map((category) => (
                <option key={category.key} value={category.key}>
                  {category.label_vi}
                </option>
              ))}
            </Select>
          </Field>

          <Field label="Sắp xếp" htmlFor="sort">
            <Select
              id="sort"
              value={search.sort}
              onChange={(e) => setFilter({ sort: e.target.value })}
            >
              <option value="score:desc">Điểm cao nhất</option>
              <option value="last_seen:desc">Thấy gần đây nhất</option>
              <option value="query_count:desc">Nhiều truy vấn nhất</option>
              <option value="name:asc">Tên A→Z</option>
            </Select>
          </Field>
        </FilterBar>
      </Card>

      <Card
        title={`Danh sách domain${rows.length > 0 ? ` (${formatNumber(rows.length)}${query.hasNextPage ? '+' : ''})` : ''}`}
        className="overflow-hidden"
      >
        {query.isPending ? (
          <Spinner />
        ) : query.isError ? (
          <ErrorState error={query.error} />
        ) : rows.length === 0 ? (
          <EmptyState>Không có domain nào khớp bộ lọc</EmptyState>
        ) : (
          <div>
          <TableScroll minWidth="52rem">
            <div className="grid grid-cols-[minmax(0,1fr)_7rem_8rem_5rem_7rem_5rem_8rem] gap-2 border-b border-slate-200 pb-1.5 text-xs font-semibold uppercase tracking-wide text-slate-500 dark:border-slate-800 dark:text-slate-400">
              <span className="min-w-0">Tên miền</span>
              <span>Trạng thái</span>
              <span>Phân loại</span>
              <span className="text-right">Điểm</span>
              <span className="text-right">Truy vấn</span>
              <span className="text-right">Client</span>
              <span>Thấy lần cuối</span>
            </div>

            <div ref={parentRef} className="max-h-[65vh] overflow-y-auto">
              <div style={{ height: virtualizer.getTotalSize(), position: 'relative' }}>
                {virtualRows.map((virtualRow) => {
                  const domain = rows[virtualRow.index];
                  if (!domain) return null;

                  return (
                    <Link
                      key={domain.id}
                      to="/domains/$domainId"
                      params={{ domainId: String(domain.id) }}
                      style={{
                        position: 'absolute',
                        top: 0,
                        left: 0,
                        width: '100%',
                        height: virtualRow.size,
                        transform: `translateY(${virtualRow.start}px)`,
                      }}
                      className={cx(
                        'grid grid-cols-[minmax(0,1fr)_7rem_8rem_5rem_7rem_5rem_8rem] items-center gap-2',
                        'border-b border-slate-100 text-sm transition-colors dark:border-slate-800',
                        'hover:bg-slate-50 dark:hover:bg-slate-800',
                      )}
                    >
                      <span className="domain-name truncate pr-2" title={domain.name}>
                        {domain.name}
                        {domain.is_manual && (
                          <span
                            title="Quyết định thủ công — job chấm điểm bỏ qua domain này"
                            className="ml-1.5 text-xs text-sky-600 dark:text-sky-400"
                          >
                            ✎
                          </span>
                        )}
                      </span>
                      <span>
                        <StatusBadge status={domain.status} />
                      </span>
                      <span className="truncate text-slate-600 dark:text-slate-300">
                        {domain.category?.label_vi ?? '—'}
                      </span>
                      <span className="text-right font-medium tabular-nums">
                        {formatScore(domain.score)}
                      </span>
                      <span className="text-right tabular-nums text-slate-600 dark:text-slate-300">
                        {formatNumber(domain.query_count)}
                      </span>
                      <span className="text-right tabular-nums text-slate-600 dark:text-slate-300">
                        {domain.client_count}
                      </span>
                      <span
                        className="truncate text-xs text-slate-500 dark:text-slate-400"
                        title={`Thấy lần cuối ${formatDateTime(domain.last_seen)} · Nguồn: ${
                          originLabels[domain.origin] ?? '—'
                        }`}
                      >
                        {formatRelative(domain.last_seen)}
                      </span>
                    </Link>
                  );
                })}
              </div>
            </div>

          </TableScroll>

            {query.isFetchingNextPage && (
              <p className="pt-2 text-center text-xs text-slate-500 dark:text-slate-400">
                Đang tải thêm…
              </p>
            )}
          </div>
        )}
      </Card>
    </div>
  );
}
