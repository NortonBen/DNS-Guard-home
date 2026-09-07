import { useState } from 'react';

import {
  useCategories,
  usePublish,
  useRollback,
  useSnapshotDiff,
  useSnapshots,
} from '@/api/hooks';
import { Button, Card, EmptyState, ErrorState, Spinner } from '@/components/ui/primitives';
import { formatDateTime, formatNumber, shortChecksum } from '@/lib/format';

/** Lịch sử xuất bản: so sánh, quay lại bản cũ, và URL để dán vào router. */
export function PublishScreen() {
  const categories = useCategories();
  const [category, setCategory] = useState('');
  const snapshots = useSnapshots(category);
  const publish = usePublish();
  const rollback = useRollback();

  const [compare, setCompare] = useState<[number | null, number | null]>([null, null]);
  const diff = useSnapshotDiff(compare[0], compare[1]);

  const enabled = categories.data?.items.filter((c) => c.enabled) ?? [];
  const baseUrl = `${window.location.protocol}//${window.location.host}`;

  function toggleCompare(id: number) {
    setCompare(([a, b]) => {
      if (a === id) return [b, null];
      if (b === id) return [a, null];
      if (a === null) return [id, null];
      if (b === null) return [a, id];
      return [b, id];
    });
  }

  return (
    <div className="space-y-3">
      <Card title="Địa chỉ danh sách cho router">
        <ul className="space-y-1.5">
          {enabled.map((item) => (
            <li key={item.key} className="flex items-center gap-2">
              <code className="flex-1 truncate rounded bg-slate-100 px-2 py-1 font-mono text-xs dark:bg-slate-800">
                {baseUrl}/lists/{item.publish_path}
              </code>
              <Button
                variant="ghost"
                className="px-2 py-1 text-xs"
                onClick={() =>
                  void navigator.clipboard.writeText(`${baseUrl}/lists/${item.publish_path}`)
                }
              >
                Sao chép
              </Button>
            </li>
          ))}
          <li className="flex items-center gap-2">
            <code className="flex-1 truncate rounded bg-slate-100 px-2 py-1 font-mono text-xs dark:bg-slate-800">
              {baseUrl}/lists/all.txt
            </code>
            <Button
              variant="ghost"
              className="px-2 py-1 text-xs"
              onClick={() => void navigator.clipboard.writeText(`${baseUrl}/lists/all.txt`)}
            >
              Sao chép
            </Button>
          </li>
        </ul>

        {/* Thay đổi không có hiệu lực tức thì; giao diện phải nói rõ điều này thay vì
            để người dùng tưởng đã xong rồi thắc mắc vì sao quảng cáo vẫn hiện. */}
        <p className="mt-3 rounded-md bg-sky-50 px-3 py-2 text-xs text-sky-800 dark:bg-sky-950 dark:text-sky-300">
          Router kiểm tra cập nhật theo lịch của nó, thường vài giờ một lần. Muốn áp dụng ngay, chạy{' '}
          <code className="font-mono">/ip dns adlist reload</code> trên router.
        </p>
      </Card>

      <Card
        title="Lịch sử xuất bản"
        actions={
          <div className="flex items-center gap-2">
            <select
              value={category}
              onChange={(e) => setCategory(e.target.value)}
              aria-label="Lọc theo phân loại"
              className="rounded-md px-2 py-1 text-sm ring-1 ring-slate-300 dark:bg-slate-800 dark:ring-slate-700"
            >
              <option value="">Tất cả phân loại</option>
              {enabled.map((c) => (
                <option key={c.key} value={c.key}>
                  {c.label_vi}
                </option>
              ))}
            </select>
            <Button
              variant="primary"
              disabled={publish.isPending}
              onClick={() => publish.mutate(category ? [category] : [])}
            >
              {publish.isPending ? 'Đang xuất bản…' : 'Xuất bản ngay'}
            </Button>
          </div>
        }
      >
        {publish.isError && <ErrorState error={publish.error} />}

        {snapshots.isPending ? (
          <Spinner />
        ) : snapshots.data?.items.length === 0 ? (
          <EmptyState>Chưa có lần xuất bản nào</EmptyState>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-slate-200 text-left text-xs uppercase tracking-wide text-slate-500 dark:border-slate-800 dark:text-slate-400">
                <th scope="col" className="w-8 py-1.5"></th>
                <th scope="col" className="py-1.5">Thời điểm</th>
                <th scope="col" className="py-1.5">Phân loại</th>
                <th scope="col" className="py-1.5 text-right">Số mục</th>
                <th scope="col" className="py-1.5">Checksum</th>
                <th scope="col" className="py-1.5">Người thực hiện</th>
                <th scope="col" className="py-1.5"></th>
              </tr>
            </thead>
            <tbody>
              {snapshots.data?.items.map((snapshot, index) => {
                const previous = snapshots.data.items[index + 1];
                const delta = previous ? snapshot.entry_count - previous.entry_count : 0;

                return (
                  <tr key={snapshot.id} className="border-b border-slate-100 dark:border-slate-800">
                    <td className="py-1.5">
                      <input
                        type="checkbox"
                        aria-label={`Chọn bản #${snapshot.id} để so sánh`}
                        checked={compare.includes(snapshot.id)}
                        onChange={() => toggleCompare(snapshot.id)}
                        className="size-4"
                      />
                    </td>
                    <td className="py-1.5">{formatDateTime(snapshot.published_at)}</td>
                    <td className="py-1.5">{snapshot.category || 'gộp'}</td>
                    <td className="py-1.5 text-right tabular-nums">
                      {formatNumber(snapshot.entry_count)}
                      {previous && delta !== 0 && (
                        <span
                          className={
                            delta > 0
                              ? 'ml-1 text-xs text-emerald-600 dark:text-emerald-400'
                              : 'ml-1 text-xs text-amber-600 dark:text-amber-400'
                          }
                        >
                          {delta > 0 ? '+' : ''}
                          {formatNumber(delta)}
                        </span>
                      )}
                    </td>
                    <td className="py-1.5 font-mono text-xs text-slate-500 dark:text-slate-400">
                      {shortChecksum(snapshot.checksum)}
                    </td>
                    <td className="py-1.5 text-xs">{snapshot.published_by}</td>
                    <td className="py-1.5 text-right">
                      <Button
                        variant="ghost"
                        className="px-2 py-1 text-xs"
                        onClick={() => {
                          // Buộc gõ tên phân loại: quay lại một bản cũ đổi nội dung
                          // mà cả mạng đang dùng, nên không được bấm nhầm.
                          const expected = snapshot.category || 'all';
                          const typed = window.prompt(
                            `Quay lại bản #${snapshot.id} (${formatNumber(snapshot.entry_count)} mục).\n` +
                              `Gõ "${expected}" để xác nhận:`,
                          );
                          if (typed === expected) rollback.mutate(snapshot.id);
                        }}
                      >
                        Quay lại bản này
                      </Button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </Card>

      {compare[0] !== null && compare[1] !== null && (
        <Card title={`So sánh bản #${compare[0]} với #${compare[1]}`}>
          {diff.isPending ? (
            <Spinner />
          ) : diff.isError ? (
            <ErrorState error={diff.error} />
          ) : diff.data ? (
            <div className="grid gap-4 sm:grid-cols-2">
              <div>
                <h3 className="text-sm font-semibold text-emerald-700 dark:text-emerald-400">
                  Thêm mới ({formatNumber(diff.data.added.count)})
                </h3>
                <ul className="mt-1 max-h-64 space-y-0.5 overflow-auto">
                  {diff.data.added.sample.map((name) => (
                    <li key={name} className="domain-name text-xs">
                      {name}
                    </li>
                  ))}
                </ul>
              </div>
              <div>
                <h3 className="text-sm font-semibold text-amber-700 dark:text-amber-400">
                  Đã gỡ ({formatNumber(diff.data.removed.count)})
                </h3>
                <ul className="mt-1 max-h-64 space-y-0.5 overflow-auto">
                  {diff.data.removed.sample.map((name) => (
                    <li key={name} className="domain-name text-xs">
                      {name}
                    </li>
                  ))}
                </ul>
              </div>
            </div>
          ) : null}
        </Card>
      )}
    </div>
  );
}
