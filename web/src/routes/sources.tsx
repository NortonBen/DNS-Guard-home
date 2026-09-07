import { useState, type FormEvent } from 'react';

import {
  useCategories,
  useCreateSource,
  useDeleteSource,
  useSourceOverlap,
  useSources,
  useSyncSource,
  useUpdateSource,
} from '@/api/hooks';
import { Button, Card, EmptyState, ErrorState, Spinner, cx } from '@/components/ui/primitives';
import { formatNumber, formatRelative } from '@/lib/format';
import { sourceStatusLabels } from '@/lib/strings';

/** Nguồn blocklist công khai: đăng ký, đồng bộ, và phân tích chồng lấn. */
export function SourcesScreen() {
  const [tab, setTab] = useState<'list' | 'overlap'>('list');

  return (
    <div className="space-y-3">
      <div className="flex gap-1">
        <Button
          variant="ghost"
          className={cx(tab === 'list' && 'bg-sky-50 text-sky-700 dark:bg-sky-950 dark:text-sky-300')}
          onClick={() => setTab('list')}
        >
          Danh sách nguồn
        </Button>
        <Button
          variant="ghost"
          className={cx(tab === 'overlap' && 'bg-sky-50 text-sky-700 dark:bg-sky-950 dark:text-sky-300')}
          onClick={() => setTab('overlap')}
        >
          Chồng lấn
        </Button>
      </div>

      {tab === 'list' ? <SourceList /> : <OverlapMatrix />}
    </div>
  );
}

function SourceList() {
  const sources = useSources();
  const categories = useCategories();
  const create = useCreateSource();
  const update = useUpdateSource();
  const remove = useDeleteSource();
  const sync = useSyncSource();

  const [showForm, setShowForm] = useState(false);

  function handleCreate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    create.mutate(
      {
        name: String(form.get('name')),
        url: String(form.get('url')),
        format: String(form.get('format')) as 'hosts' | 'adblock' | 'plain',
        category: String(form.get('category')),
        enabled: true,
        sync_interval: Number(form.get('interval')) * 3600,
      },
      { onSuccess: () => setShowForm(false) },
    );
  }

  return (
    <>
      <Card
        title="Nguồn blocklist công khai"
        actions={
          <Button variant="primary" onClick={() => setShowForm((v) => !v)}>
            {showForm ? 'Đóng' : 'Thêm nguồn'}
          </Button>
        }
      >
        {showForm && (
          <form
            onSubmit={handleCreate}
            className="mb-4 grid gap-3 rounded-md bg-slate-50 p-3 sm:grid-cols-2 dark:bg-slate-950"
          >
            <label className="block text-sm">
              <span className="mb-1 block font-medium">Tên</span>
              <input
                name="name"
                required
                placeholder="Hagezi Pro"
                className="w-full rounded-md px-3 py-1.5 text-sm ring-1 ring-slate-300 dark:bg-slate-800 dark:ring-slate-700"
              />
            </label>

            <label className="block text-sm">
              <span className="mb-1 block font-medium">Phân loại</span>
              <select
                name="category"
                className="w-full rounded-md px-3 py-1.5 text-sm ring-1 ring-slate-300 dark:bg-slate-800 dark:ring-slate-700"
              >
                {categories.data?.items.map((c) => (
                  <option key={c.key} value={c.key}>
                    {c.label_vi}
                  </option>
                ))}
              </select>
            </label>

            <label className="block text-sm sm:col-span-2">
              <span className="mb-1 block font-medium">URL</span>
              <input
                name="url"
                required
                type="url"
                placeholder="https://raw.githubusercontent.com/…/pro.txt"
                className="w-full rounded-md px-3 py-1.5 font-mono text-sm ring-1 ring-slate-300 dark:bg-slate-800 dark:ring-slate-700"
              />
            </label>

            <label className="block text-sm">
              <span className="mb-1 block font-medium">Định dạng</span>
              <select
                name="format"
                className="w-full rounded-md px-3 py-1.5 text-sm ring-1 ring-slate-300 dark:bg-slate-800 dark:ring-slate-700"
              >
                <option value="hosts">hosts (0.0.0.0 domain)</option>
                <option value="adblock">adblock (||domain^)</option>
                <option value="plain">danh sách thuần</option>
              </select>
            </label>

            <label className="block text-sm">
              <span className="mb-1 block font-medium">Đồng bộ mỗi (giờ)</span>
              <input
                name="interval"
                type="number"
                min={1}
                defaultValue={24}
                className="w-full rounded-md px-3 py-1.5 text-sm ring-1 ring-slate-300 dark:bg-slate-800 dark:ring-slate-700"
              />
            </label>

            <div className="sm:col-span-2">
              <Button type="submit" variant="primary" disabled={create.isPending}>
                Thêm và đồng bộ ngay
              </Button>
              {create.isError && (
                <div className="mt-2">
                  <ErrorState error={create.error} />
                </div>
              )}
            </div>
          </form>
        )}

        {sources.isPending ? (
          <Spinner />
        ) : sources.data?.items.length === 0 ? (
          <EmptyState>Chưa đăng ký nguồn nào</EmptyState>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-slate-200 text-left text-xs uppercase tracking-wide text-slate-500 dark:border-slate-800 dark:text-slate-400">
                <th scope="col" className="py-1.5">Tên</th>
                <th scope="col" className="py-1.5">Phân loại</th>
                <th scope="col" className="py-1.5 text-right">Số mục</th>
                <th scope="col" className="py-1.5">Đồng bộ lần cuối</th>
                <th scope="col" className="py-1.5">Trạng thái</th>
                <th scope="col" className="py-1.5"></th>
              </tr>
            </thead>
            <tbody>
              {sources.data?.items.map((source) => {
                const failing =
                  source.last_status !== '' &&
                  source.last_status !== 'ok' &&
                  source.last_status !== 'not_modified';

                return (
                  <tr
                    key={source.id}
                    className={cx(
                      'border-b border-slate-100 dark:border-slate-800',
                      failing && 'bg-red-50/60 dark:bg-red-950/30',
                    )}
                  >
                    <td className="py-2">
                      <p className="font-medium">{source.name}</p>
                      <p
                        className="max-w-md truncate font-mono text-xs text-slate-500 dark:text-slate-400"
                        title={source.url}
                      >
                        {source.url}
                      </p>
                    </td>
                    <td className="py-2">{source.category || '—'}</td>
                    <td className="py-2 text-right tabular-nums">
                      {formatNumber(source.entry_count)}
                      {source.prev_count > 0 && source.entry_count !== source.prev_count && (
                        <span
                          className={cx(
                            'ml-1 text-xs',
                            source.entry_count > source.prev_count
                              ? 'text-emerald-600 dark:text-emerald-400'
                              : 'text-amber-600 dark:text-amber-400',
                          )}
                        >
                          {source.entry_count > source.prev_count ? '+' : ''}
                          {formatNumber(source.entry_count - source.prev_count)}
                        </span>
                      )}
                    </td>
                    <td className="py-2 text-xs text-slate-500 dark:text-slate-400">
                      {formatRelative(source.last_sync_at)}
                    </td>
                    <td className="py-2 text-xs">
                      <span className={failing ? 'text-red-700 dark:text-red-400' : ''}>
                        {sourceStatusLabels[source.last_status] ?? source.last_status ?? '—'}
                      </span>
                      {source.last_error && (
                        <p className="max-w-xs truncate text-red-600 dark:text-red-400" title={source.last_error}>
                          {source.last_error}
                        </p>
                      )}
                    </td>
                    <td className="py-2">
                      <div className="flex justify-end gap-1">
                        <Button
                          variant="ghost"
                          className="px-2 py-1 text-xs"
                          onClick={() => sync.mutate(source.id)}
                        >
                          Đồng bộ
                        </Button>
                        <Button
                          variant="ghost"
                          className="px-2 py-1 text-xs"
                          onClick={() =>
                            update.mutate({ id: source.id, enabled: !source.enabled })
                          }
                        >
                          {source.enabled ? 'Tắt' : 'Bật'}
                        </Button>
                        <Button
                          variant="ghost"
                          className="px-2 py-1 text-xs text-red-600 dark:text-red-400"
                          onClick={() => {
                            if (window.confirm(`Xóa nguồn "${source.name}"?`)) {
                              remove.mutate(source.id);
                            }
                          }}
                        >
                          Xóa
                        </Button>
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}

        <p className="mt-3 text-xs text-slate-500 dark:text-slate-400">
          Nguồn trả về ít hơn 50% so với lần trước sẽ bị giữ nguyên dữ liệu cũ và đánh dấu cảnh báo —
          một nguồn hỏng không được làm sập cả danh sách.
        </p>
      </Card>
    </>
  );
}

/** Ma trận chồng lấn: trả lời câu hỏi thực tế có nên bỏ bớt nguồn nào không. */
function OverlapMatrix() {
  const overlap = useSourceOverlap(true);

  if (overlap.isPending) return <Spinner label="Đang tính chồng lấn" />;
  if (overlap.isError) return <ErrorState error={overlap.error} />;
  if (!overlap.data || overlap.data.sources.length < 2) {
    return (
      <Card title="Chồng lấn giữa các nguồn">
        <EmptyState>Cần ít nhất hai nguồn đã đồng bộ để so sánh</EmptyState>
      </Card>
    );
  }

  const nameById = new Map(overlap.data.sources.map((s) => [s.id, s.name]));

  return (
    <div className="grid gap-3 lg:grid-cols-2">
      <Card title="Mức trùng lặp giữa từng cặp">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-slate-200 text-left text-xs uppercase tracking-wide text-slate-500 dark:border-slate-800 dark:text-slate-400">
              <th scope="col" className="py-1.5">Cặp nguồn</th>
              <th scope="col" className="py-1.5 text-right">Domain chung</th>
              <th scope="col" className="py-1.5 text-right">Jaccard</th>
            </tr>
          </thead>
          <tbody>
            {overlap.data.pairs.map((pair) => (
              <tr key={`${pair.a}-${pair.b}`} className="border-b border-slate-100 dark:border-slate-800">
                <td className="py-1.5">
                  {nameById.get(pair.a)} ↔ {nameById.get(pair.b)}
                </td>
                <td className="py-1.5 text-right tabular-nums">{formatNumber(pair.shared)}</td>
                <td className="py-1.5 text-right tabular-nums">{pair.jaccard.toFixed(2)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </Card>

      <Card title="Domain chỉ có ở một nguồn">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-slate-200 text-left text-xs uppercase tracking-wide text-slate-500 dark:border-slate-800 dark:text-slate-400">
              <th scope="col" className="py-1.5">Nguồn</th>
              <th scope="col" className="py-1.5 text-right">Chỉ có ở đây</th>
            </tr>
          </thead>
          <tbody>
            {overlap.data.unique.map((entry) => (
              <tr key={entry.source_id} className="border-b border-slate-100 dark:border-slate-800">
                <td className="py-1.5">{nameById.get(entry.source_id) ?? entry.source_id}</td>
                <td className="py-1.5 text-right tabular-nums">{formatNumber(entry.only_here)}</td>
              </tr>
            ))}
          </tbody>
        </table>
        <p className="mt-3 text-xs text-slate-500 dark:text-slate-400">
          Nguồn có rất ít domain riêng là ứng viên để bỏ bớt: nó gần như chỉ lặp lại nguồn khác.
        </p>
      </Card>
    </div>
  );
}
