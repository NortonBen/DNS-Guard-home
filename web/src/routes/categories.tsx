import { useState } from 'react';

import {
  useApplyWeights,
  useCategories,
  usePreviewWeights,
  useUpdateCategory,
  useWeights,
} from '@/api/hooks';
import type { WeightImpact } from '@/api/types';
import { Button, Card, ErrorState, Spinner, TableScroll, cx } from '@/components/ui/primitives';
import { formatNumber, formatWeight } from '@/lib/format';

/**
 * Phân loại và trọng số chấm điểm.
 *
 * Đây là chỗ dễ gây thiệt hại nhất trong toàn bộ ứng dụng: một lần trượt tay có thể
 * chặn nhầm hàng nghìn domain. Vì thế luồng bắt buộc phải đi qua bước xem trước tác
 * động, và nút áp dụng chỉ bật sau khi đã xem.
 */
export function CategoriesScreen() {
  const categories = useCategories();
  const weights = useWeights();
  const updateCategory = useUpdateCategory();
  const preview = usePreviewWeights();
  const apply = useApplyWeights();

  // Giữ nguyên chuỗi người dùng đang gõ: ép sang số ngay sẽ biến ô rỗng thành 0 và
  // chặn luôn việc gõ dấu trừ hay số thập phân dở dang.
  const [draft, setDraft] = useState<Record<string, string>>({});
  const [impact, setImpact] = useState<WeightImpact | null>(null);

  const dirty = Object.keys(draft).length > 0;

  function setWeight(kind: string, value: string) {
    setDraft((prev) => ({ ...prev, [kind]: value }));
    // Mọi thay đổi làm kết quả xem trước cũ hết giá trị: buộc xem lại trước khi lưu.
    setImpact(null);
  }

  /** Chỉ gửi đi những ô đã gõ thành số hợp lệ. */
  function changedWeights() {
    return Object.entries(draft)
      .map(([kind, raw]) => ({ kind, weight: Number(raw) }))
      .filter((c) => Number.isFinite(c.weight));
  }

  function runPreview() {
    const changes = changedWeights();
    preview.mutate(changes, { onSuccess: (result) => setImpact(result.impact) });
  }

  function applyChanges() {
    const changes = changedWeights();
    apply.mutate(changes, {
      onSuccess: () => {
        setDraft({});
        setImpact(null);
      },
    });
  }

  return (
    <div className="space-y-3">
      <Card title="Phân loại">
        {categories.isPending ? (
          <Spinner />
        ) : (
          <TableScroll minWidth="44rem">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-slate-200 text-left text-xs uppercase tracking-wide text-slate-500 dark:border-slate-800 dark:text-slate-400">
                <th scope="col" className="py-1.5">Phân loại</th>
                <th scope="col" className="py-1.5">Đường dẫn xuất bản</th>
                <th scope="col" className="py-1.5 text-right">Số domain</th>
                <th scope="col" className="py-1.5 text-right">Ngưỡng điểm</th>
                <th scope="col" className="py-1.5 text-center">Xuất bản</th>
              </tr>
            </thead>
            <tbody>
              {categories.data?.items.map((category) => (
                <tr key={category.id} className="border-b border-slate-100 dark:border-slate-800">
                  <td className="py-2">
                    <span
                      aria-hidden
                      className="mr-2 inline-block size-2.5 rounded-full align-middle"
                      style={{ backgroundColor: category.color }}
                    />
                    {category.label_vi}
                    <span className="ml-1.5 text-xs text-slate-500 dark:text-slate-400">
                      {category.key}
                    </span>
                  </td>
                  <td className="py-2 font-mono text-xs text-slate-600 dark:text-slate-300">
                    /lists/{category.publish_path}
                  </td>
                  <td className="py-2 text-right tabular-nums">
                    {formatNumber(category.domain_count)}
                  </td>
                  <td className="py-2 text-right">
                    <input
                      type="number"
                      step="0.5"
                      defaultValue={category.score_threshold}
                      aria-label={`Ngưỡng điểm cho ${category.label_vi}`}
                      onBlur={(e) => {
                        const value = Number(e.target.value);
                        if (value !== category.score_threshold) {
                          updateCategory.mutate({ id: category.id, score_threshold: value });
                        }
                      }}
                      className="w-20 rounded-md px-2 py-1 text-right text-sm tabular-nums ring-1 ring-slate-300 dark:bg-slate-800 dark:ring-slate-700"
                    />
                  </td>
                  <td className="py-2 text-center">
                    <input
                      type="checkbox"
                      checked={category.enabled}
                      aria-label={`Xuất bản phân loại ${category.label_vi}`}
                      onChange={(e) =>
                        updateCategory.mutate({ id: category.id, enabled: e.target.checked })
                      }
                      className="size-4"
                    />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          </TableScroll>
        )}
        <p className="mt-3 text-xs text-slate-500 dark:text-slate-400">
          Tắt xuất bản không xóa nhãn phân loại — domain vẫn được gán nhãn nhưng không nằm trong
          file nào. Hữu ích khi gỡ lỗi mà không muốn mất dữ liệu phân loại.
        </p>
      </Card>

      <Card
        title="Trọng số tín hiệu"
        actions={
          <div className="flex items-center gap-2">
            {dirty && (
              <Button variant="ghost" onClick={() => { setDraft({}); setImpact(null); }}>
                Hủy thay đổi
              </Button>
            )}
            <Button
              variant="secondary"
              disabled={!dirty || preview.isPending}
              onClick={runPreview}
            >
              {preview.isPending ? 'Đang tính…' : 'Xem tác động'}
            </Button>
            <Button
              variant="primary"
              // Không cho lưu khi chưa xem tác động: đổi trọng số mà không xem trước
              // là cách nhanh nhất để chặn nhầm hàng loạt.
              disabled={!dirty || impact === null || apply.isPending}
              onClick={applyChanges}
            >
              Áp dụng
            </Button>
          </div>
        }
      >
        {weights.isPending ? (
          <Spinner />
        ) : weights.isError ? (
          <ErrorState error={weights.error} />
        ) : (
          <>
            <div className="grid gap-x-6 gap-y-2 sm:grid-cols-2">
              {weights.data?.weights.map((entry) => {
                const value = draft[entry.kind] ?? String(entry.weight);
                const changed = draft[entry.kind] !== undefined;

                return (
                  <div key={entry.kind} className="flex items-center gap-3">
                    <label
                      htmlFor={`weight-${entry.kind}`}
                      className="flex-1 text-sm"
                      title={entry.kind}
                    >
                      {entry.label_vi}
                      {entry.is_infra && (
                        <span className="ml-1.5 rounded bg-slate-100 px-1 py-0.5 text-[10px] uppercase tracking-wide text-slate-500 dark:bg-slate-800 dark:text-slate-400">
                          hạ tầng
                        </span>
                      )}
                    </label>
                    <input
                      id={`weight-${entry.kind}`}
                      type="number"
                      step="0.5"
                      value={value}
                      onChange={(e) => setWeight(entry.kind, e.target.value)}
                      className={cx(
                        'w-20 rounded-md px-2 py-1 text-right text-sm tabular-nums ring-1',
                        changed
                          ? 'ring-2 ring-sky-500 dark:bg-slate-800'
                          : 'ring-slate-300 dark:bg-slate-800 dark:ring-slate-700',
                      )}
                    />
                    {Number(value) !== entry.default && (
                      <button
                        type="button"
                        onClick={() => setWeight(entry.kind, String(entry.default))}
                        title={`Trả về mặc định ${formatWeight(entry.default)}`}
                        aria-label={`Trả trọng số ${entry.label_vi} về mặc định ${formatWeight(entry.default)}`}
                        className="text-xs text-slate-400 hover:text-slate-600 dark:hover:text-slate-200"
                      >
                        ↺
                      </button>
                    )}
                  </div>
                );
              })}
            </div>

            {preview.isError && (
              <div className="mt-4">
                <ErrorState error={preview.error} />
              </div>
            )}

            {apply.isError && (
              <div className="mt-4">
                <ErrorState error={apply.error} />
              </div>
            )}

            {impact && <ImpactPanel impact={impact} />}
          </>
        )}
      </Card>
    </div>
  );
}

/**
 * Bảng tác động.
 *
 * Con số ở đây khớp chính xác với kết quả sau khi áp dụng, không phải ước lượng: hàm
 * chấm điểm là hàm thuần túy, nên chạy trước trên cùng dữ liệu cho cùng kết quả.
 */
function ImpactPanel({ impact }: { impact: WeightImpact }) {
  return (
    <div className="mt-4 rounded-md bg-slate-50 p-3 dark:bg-slate-950">
      <h3 className="text-sm font-semibold">Tác động nếu áp dụng</h3>
      <div className="mt-2 grid gap-3 sm:grid-cols-3">
        <ImpactStat
          label="Sẽ bị chặn thêm"
          count={impact.would_block.count}
          sample={impact.would_block.sample}
          tone="text-red-600 dark:text-red-400"
        />
        <ImpactStat
          label="Sẽ được gỡ chặn"
          count={impact.would_unblock.count}
          sample={impact.would_unblock.sample}
          tone="text-emerald-600 dark:text-emerald-400"
        />
        <div>
          <p className="text-xs text-slate-500 dark:text-slate-400">Không đổi</p>
          <p className="text-xl font-semibold tabular-nums">{formatNumber(impact.unchanged)}</p>
        </div>
      </div>
    </div>
  );
}

function ImpactStat({
  label,
  count,
  sample,
  tone,
}: {
  label: string;
  count: number;
  sample: string[];
  tone: string;
}) {
  return (
    <div>
      <p className="text-xs text-slate-500 dark:text-slate-400">{label}</p>
      <p className={cx('text-xl font-semibold tabular-nums', tone)}>{formatNumber(count)}</p>
      {sample.length > 0 && (
        <ul className="mt-1 space-y-0.5">
          {sample.slice(0, 5).map((name) => (
            <li key={name} className="domain-name truncate text-xs text-slate-500 dark:text-slate-400">
              {name}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
