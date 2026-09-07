import { useState } from 'react';

import { useAIHistory, useAIRequest, useRunAIClassify } from '@/api/hooks';
import type { AIRequestKind } from '@/api/types';
import { Select } from '@/components/ui/form';
import {
  Button,
  Card,
  EmptyState,
  ErrorState,
  Spinner,
  TableScroll,
  cx,
} from '@/components/ui/primitives';
import { formatNumber, formatRelative } from '@/lib/format';

const kindLabels: Record<AIRequestKind, string> = {
  classify: 'Phân loại lô',
  recheck: 'Hỏi lại',
  ask: 'Hỏi đáp',
};

const pageSize = 25;

/**
 * Lịch sử lượt gọi model.
 *
 * Danh sách chỉ hiện số đo; bấm vào một dòng mới tải prompt và phản hồi thô. Tách
 * làm hai bước vì prompt của một lô bốn mươi domain nặng vài kilobyte, và tải hai
 * mươi lăm cái cùng lúc chỉ để hiện một bảng là lãng phí thuần túy.
 */
export function HistoryPanel() {
  const [kind, setKind] = useState('');
  const [offset, setOffset] = useState(0);
  const [openID, setOpenID] = useState<number | null>(null);

  const history = useAIHistory(kind, offset, true);
  const run = useRunAIClassify();

  if (history.isPending) return <Spinner label="Đang tải lịch sử" />;
  if (history.isError) return <ErrorState error={history.error} />;

  const items = history.data?.items ?? [];
  const total = history.data?.total ?? 0;

  return (
    <div className="space-y-3">
      <Card
        title="Lịch sử hỏi model"
        actions={
          <div className="flex items-center gap-2">
            <Select
              aria-label="Lọc theo loại lượt gọi"
              value={kind}
              onChange={(e) => {
                setKind(e.target.value);
                setOffset(0);
              }}
            >
              <option value="">Tất cả</option>
              <option value="classify">Phân loại lô</option>
              <option value="recheck">Hỏi lại</option>
              <option value="ask">Hỏi đáp</option>
            </Select>
            <Button
              variant="primary"
              disabled={run.isPending}
              onClick={() => run.mutate()}
              title="Xếp hàng một lượt hỏi cho các domain tới hạn"
            >
              {run.isPending ? 'Đang xếp hàng…' : 'Chạy ngay'}
            </Button>
          </div>
        }
      >
        {run.isSuccess && (
          <p className="mb-3 rounded-md bg-emerald-50 px-3 py-2 text-sm text-emerald-800 dark:bg-emerald-950 dark:text-emerald-300">
            Đã xếp hàng. Kết quả sẽ xuất hiện ở bảng này sau khi job chạy xong.
          </p>
        )}
        {run.isError && <ErrorState error={run.error} />}

        {items.length === 0 ? (
          <EmptyState>Chưa có lượt gọi nào.</EmptyState>
        ) : (
          <TableScroll minWidth="52rem">
            <table className="w-full text-sm">
              <thead className="text-left text-xs text-slate-500 dark:text-slate-400">
                <tr>
                  <th className="pb-2 font-medium">Thời điểm</th>
                  <th className="pb-2 font-medium">Loại</th>
                  <th className="pb-2 font-medium">Model</th>
                  <th className="pb-2 text-right font-medium">Hỏi</th>
                  <th className="pb-2 text-right font-medium">Trả lời</th>
                  <th className="pb-2 text-right font-medium">Bỏ qua</th>
                  <th className="pb-2 text-right font-medium">Ký tự</th>
                  <th className="pb-2 text-right font-medium">Thời gian</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
                {items.map((item) => (
                  <tr
                    key={item.id}
                    onClick={() => setOpenID(item.id === openID ? null : item.id)}
                    className={cx(
                      'cursor-pointer',
                      item.id === openID
                        ? 'bg-sky-50 dark:bg-sky-950'
                        : 'hover:bg-slate-50 dark:hover:bg-slate-800',
                    )}
                  >
                    <td className="py-1.5">{formatRelative(item.created_at)}</td>
                    <td className="py-1.5">{kindLabels[item.kind] ?? item.kind}</td>
                    <td className="py-1.5 font-mono text-xs">{item.model || '—'}</td>
                    <td className="py-1.5 text-right">{item.domain_count}</td>
                    <td className="py-1.5 text-right">{item.parsed_count}</td>
                    <td
                      className={cx(
                        'py-1.5 text-right',
                        item.skipped_count > 0 && 'text-amber-600 dark:text-amber-400',
                      )}
                    >
                      {item.skipped_count}
                    </td>
                    <td className="py-1.5 text-right text-xs text-slate-500">
                      {formatNumber(item.prompt_chars + item.reply_chars)}
                    </td>
                    <td className="py-1.5 text-right text-xs text-slate-500">
                      {(item.latency_ms / 1000).toFixed(1)}s
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </TableScroll>
        )}

        <div className="mt-3 flex items-center justify-between text-xs text-slate-500 dark:text-slate-400">
          <span>
            {offset + 1}–{Math.min(offset + pageSize, total)} trên {formatNumber(total)}
          </span>
          <div className="flex gap-2">
            <Button
              variant="ghost"
              disabled={offset === 0}
              onClick={() => setOffset(Math.max(0, offset - pageSize))}
            >
              Trước
            </Button>
            <Button
              variant="ghost"
              disabled={offset + pageSize >= total}
              onClick={() => setOffset(offset + pageSize)}
            >
              Sau
            </Button>
          </div>
        </div>
      </Card>

      {openID !== null && <RequestDetail id={openID} />}
    </div>
  );
}

/** Một lượt gọi đầy đủ: prompt, phản hồi thô, và các kết luận đã đọc ra được. */
function RequestDetail({ id }: { id: number }) {
  const detail = useAIRequest(id);

  if (detail.isPending) return <Spinner label="Đang tải chi tiết" />;
  if (detail.isError) return <ErrorState error={detail.error} />;
  if (!detail.data) return null;

  const { request, verdicts } = detail.data;

  return (
    <Card title={`Lượt gọi #${request.id}`}>
      {request.error && (
        <div className="mb-3">
          <ErrorState error={new Error(request.error)} />
        </div>
      )}

      {verdicts.length > 0 && (
        <TableScroll minWidth="40rem">
          <table className="mb-4 w-full text-sm">
            <thead className="text-left text-xs text-slate-500 dark:text-slate-400">
              <tr>
                <th className="pb-2 font-medium">Domain</th>
                <th className="pb-2 font-medium">Nhãn</th>
                <th className="pb-2 text-right font-medium">Tin cậy</th>
                <th className="pb-2 font-medium">Lý do</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
              {verdicts.map((v) => (
                <tr key={v.id}>
                  <td className="py-1.5 font-mono text-xs">{v.domain}</td>
                  <td className="py-1.5">{v.category}</td>
                  <td className="py-1.5 text-right">{(v.confidence * 100).toFixed(0)}%</td>
                  <td className="py-1.5 text-slate-500 dark:text-slate-400">{v.reason}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </TableScroll>
      )}

      <div className="grid gap-3 lg:grid-cols-2">
        <RawBlock label="Câu hỏi đã gửi" body={request.prompt ?? ''} />
        <RawBlock label="Phản hồi thô" body={request.response ?? ''} />
      </div>
    </Card>
  );
}

function RawBlock({ label, body }: { label: string; body: string }) {
  return (
    <div>
      <p className="mb-1 text-xs font-medium text-slate-500 dark:text-slate-400">{label}</p>
      <pre className="max-h-64 overflow-auto rounded bg-slate-50 p-3 text-xs whitespace-pre-wrap dark:bg-slate-950">
        {body || '(trống)'}
      </pre>
    </div>
  );
}
