import { useEffect, useMemo, useState } from 'react';

import { useAddDomains, useCategories } from '@/api/hooks';
import type { AddDomainResult } from '@/api/types';
import { Button, Card, EmptyState, ErrorState, cx } from '@/components/ui/primitives';
import { formatNumber } from '@/lib/format';

/** Màu theo kết quả phân tích, để quét bảng bằng mắt nhanh hơn đọc từng dòng. */
function rowTone(result: AddDomainResult): string {
  if (result.action === 'created') return 'text-emerald-700 dark:text-emerald-400';
  if (result.action === 'duplicate') return 'text-slate-500 dark:text-slate-400';
  if (result.code === 'high_rank') return 'text-amber-700 dark:text-amber-400';
  return 'text-red-700 dark:text-red-400';
}

const actionLabels: Record<string, string> = {
  created: 'Sẽ thêm',
  duplicate: 'Đã có',
  rejected: 'Từ chối',
};

/**
 * Thêm domain thủ công, có bảng xem trước.
 *
 * Xem trước là bắt buộc chứ không phải tiện ích: dán một danh sách lấy từ diễn đàn
 * mà không nhìn kỹ là cách phổ biến nhất để chặn nhầm thứ quan trọng.
 */
export function ManualScreen() {
  const categories = useCategories();
  const add = useAddDomains();

  const [text, setText] = useState('');
  const [category, setCategory] = useState('ads');
  const [reason, setReason] = useState('');
  const [force, setForce] = useState(false);
  const [excluded, setExcluded] = useState<Set<string>>(new Set());
  const [results, setResults] = useState<AddDomainResult[] | null>(null);
  const [saved, setSaved] = useState<{ created: number; rejected: number } | null>(null);

  const names = useMemo(
    () =>
      text
        .split('\n')
        .map((line) => line.trim())
        .filter(Boolean),
    [text],
  );

  // Gọi xem trước có trễ: người dùng đang gõ thì không cần phân tích lại từng ký tự.
  useEffect(() => {
    if (names.length === 0) {
      setResults(null);
      return;
    }
    const timer = window.setTimeout(() => {
      add.mutate(
        { names, category, reason, dry_run: true, force },
        { onSuccess: (data) => setResults(data.results) },
      );
    }, 500);
    return () => window.clearTimeout(timer);
    // add.mutate ổn định theo tham chiếu của TanStack Query, không cần vào deps.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [names, category, reason, force]);

  const selectable = results?.filter((r) => r.action === 'created') ?? [];
  const willSave = selectable.filter((r) => !excluded.has(r.name));

  function save() {
    if (willSave.length === 0) return;
    add.mutate(
      {
        names: willSave.map((r) => r.name),
        category,
        reason,
        dry_run: false,
        force,
      },
      {
        onSuccess: (data) => {
          setSaved(data.summary);
          setText('');
          setResults(null);
          setExcluded(new Set());
        },
      },
    );
  }

  return (
    <div className="grid gap-3 lg:grid-cols-2">
      <Card title="Dán danh sách domain">
        <div className="space-y-3">
          <div>
            <label htmlFor="names" className="mb-1 block text-sm font-medium">
              Mỗi dòng một domain
            </label>
            <textarea
              id="names"
              rows={14}
              value={text}
              onChange={(e) => setText(e.target.value)}
              placeholder={'ads.example.com\n*.tracker.io\nmetrics.trangweb.vn'}
              className="w-full rounded-md px-3 py-2 font-mono text-sm ring-1 ring-slate-300 dark:bg-slate-800 dark:ring-slate-700"
            />
            <p className="mt-1 text-xs text-slate-500 dark:text-slate-400">
              Hỗ trợ wildcard <code>*.example.com</code> — bảng bên phải hiện số domain đã biết sẽ khớp
            </p>
          </div>

          <div className="grid gap-3 sm:grid-cols-2">
            <div>
              <label htmlFor="category" className="mb-1 block text-sm font-medium">
                Phân loại
              </label>
              <select
                id="category"
                value={category}
                onChange={(e) => setCategory(e.target.value)}
                className="w-full rounded-md px-3 py-1.5 text-sm ring-1 ring-slate-300 dark:bg-slate-800 dark:ring-slate-700"
              >
                {categories.data?.items.map((c) => (
                  <option key={c.key} value={c.key}>
                    {c.label_vi}
                  </option>
                ))}
              </select>
            </div>

            <div>
              <label htmlFor="reason" className="mb-1 block text-sm font-medium">
                Lý do <span className="text-red-600">*</span>
              </label>
              <input
                id="reason"
                value={reason}
                onChange={(e) => setReason(e.target.value)}
                placeholder="Từ diễn đàn X"
                className="w-full rounded-md px-3 py-1.5 text-sm ring-1 ring-slate-300 dark:bg-slate-800 dark:ring-slate-700"
              />
            </div>
          </div>

          {/* Lý do bắt buộc: nó đi vào nhật ký và là thứ duy nhất giải thích được
              quyết định này sau nhiều tháng. */}
          <p className="text-xs text-slate-500 dark:text-slate-400">
            Lý do bắt buộc — mỗi domain sẽ có một dòng nhật ký riêng với cùng lý do này.
          </p>

          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={force}
              onChange={(e) => setForce(e.target.checked)}
              className="size-4"
            />
            Cho phép chặn cả domain có thứ hạng Tranco cao
          </label>

          <Button
            variant="primary"
            disabled={willSave.length === 0 || reason.trim() === '' || add.isPending}
            onClick={save}
          >
            Lưu {willSave.length > 0 ? `${willSave.length} domain` : ''}
          </Button>

          {add.isError && <ErrorState error={add.error} />}
          {saved && (
            <p
              role="status"
              className="rounded-md bg-emerald-50 px-3 py-2 text-sm text-emerald-800 dark:bg-emerald-950 dark:text-emerald-300"
            >
              Đã thêm {saved.created} domain
              {saved.rejected > 0 && `, từ chối ${saved.rejected}`}.
            </p>
          )}
        </div>
      </Card>

      <Card title={`Xem trước${results ? ` (${results.length} dòng)` : ''}`}>
        {!results ? (
          <EmptyState>Dán danh sách bên trái để xem trước kết quả</EmptyState>
        ) : (
          <div className="max-h-[70vh] overflow-auto">
            <table className="w-full text-sm">
              <thead className="sticky top-0 bg-white dark:bg-slate-900">
                <tr className="border-b border-slate-200 text-left text-xs uppercase tracking-wide text-slate-500 dark:border-slate-800 dark:text-slate-400">
                  <th scope="col" className="w-8 py-1.5"></th>
                  <th scope="col" className="py-1.5">Tên miền</th>
                  <th scope="col" className="py-1.5">Kết quả</th>
                </tr>
              </thead>
              <tbody>
                {results.map((result, index) => (
                  <tr
                    key={`${result.name}-${index}`}
                    className="border-b border-slate-100 dark:border-slate-800"
                  >
                    <td className="py-1.5">
                      {result.action === 'created' && (
                        <input
                          type="checkbox"
                          checked={!excluded.has(result.name)}
                          aria-label={`Bao gồm ${result.name}`}
                          onChange={(e) =>
                            setExcluded((prev) => {
                              const next = new Set(prev);
                              if (e.target.checked) next.delete(result.name);
                              else next.add(result.name);
                              return next;
                            })
                          }
                          className="size-4"
                        />
                      )}
                    </td>
                    <td className="domain-name py-1.5">{result.name}</td>
                    <td className={cx('py-1.5 text-xs', rowTone(result))}>
                      {actionLabels[result.action] ?? result.action}
                      {result.message && ` — ${result.message}`}
                      {result.matches_known !== undefined && (
                        <span className="ml-1 text-slate-500 dark:text-slate-400">
                          khớp {formatNumber(result.matches_known)} domain đã biết
                        </span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </div>
  );
}
