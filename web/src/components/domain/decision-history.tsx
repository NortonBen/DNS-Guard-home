import type { Decision } from '@/api/types';
import { EmptyState } from '@/components/ui/primitives';
import { formatDateTime, formatUTC } from '@/lib/format';
import { actionLabels } from '@/lib/strings';

/**
 * Nhật ký quyết định theo thứ tự thời gian.
 *
 * Không sửa được từ giao diện, và cũng không sửa được từ API: đây là bảng duy nhất
 * của hệ thống không bao giờ bị xóa. Sáu tháng sau, nó là thứ duy nhất trả lời được
 * "vì sao domain này bị chặn".
 */
export function DecisionHistory({ history }: { history: Decision[] }) {
  if (history.length === 0) {
    return <EmptyState>Chưa có quyết định nào được ghi</EmptyState>;
  }

  return (
    <ol className="space-y-3">
      {history.map((decision) => {
        const snapshot = decision.snapshot as
          | { score?: number; category?: string; signals?: { kind: string; weight: number }[] }
          | undefined;

        return (
          <li
            key={decision.id}
            className="border-l-2 border-slate-200 pl-3 dark:border-slate-700"
          >
            <div className="flex flex-wrap items-baseline gap-x-2 text-sm">
              <span className="font-medium">{actionLabels[decision.action] ?? decision.action}</span>
              <span className="text-slate-500 dark:text-slate-400">
                bởi {decision.actor_label === 'system' ? 'hệ thống' : decision.actor_label}
              </span>
              <time
                dateTime={decision.created_at}
                title={formatUTC(decision.created_at)}
                className="text-xs text-slate-400 dark:text-slate-500"
              >
                {formatDateTime(decision.created_at)}
              </time>
            </div>

            {decision.reason && (
              <p className="mt-0.5 text-sm text-slate-600 dark:text-slate-300">{decision.reason}</p>
            )}

            {/* Ảnh chụp tại thời điểm quyết định: trọng số có thể đã đổi từ lâu, nên
                đây là cách duy nhất biết được lúc đó hệ thống nghĩ gì. */}
            {snapshot?.signals && snapshot.signals.length > 0 && (
              <p className="mt-0.5 text-xs text-slate-500 dark:text-slate-400">
                Lúc đó: điểm {snapshot.score?.toFixed(1).replace('.', ',')} ·{' '}
                {snapshot.signals.map((s) => s.kind).join(', ')}
              </p>
            )}
          </li>
        );
      })}
    </ol>
  );
}
