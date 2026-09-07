import { useAIRecheck, useAIStatus, useAIVerdicts } from '@/api/hooks';
import { Button, Card, EmptyState, ErrorState, Spinner } from '@/components/ui/primitives';
import { formatRelative } from '@/lib/format';

interface AIVerdictCardProps {
  domainId: number;
  domainName: string;
  isAdmin: boolean;
}

/**
 * Kết luận của AI về một domain, kèm nút hỏi lại.
 *
 * Hiện cả lịch sử chứ không chỉ kết luận mới nhất: một domain đổi nhãn giữa hai lần
 * hỏi là thông tin đáng giá — hoặc dữ kiện đã thay đổi, hoặc model không ổn định, và
 * hai trường hợp đó dẫn tới hai hành động khác nhau.
 */
export function AIVerdictCard({ domainId, domainName, isAdmin }: AIVerdictCardProps) {
  const status = useAIStatus();
  const available = status.data?.available === true && status.data.configured === true;

  const verdicts = useAIVerdicts(domainName, available);
  const recheck = useAIRecheck();

  // Chưa cấu hình AI thì không hiện thẻ này: một thẻ rỗng ở màn quan trọng nhất chỉ
  // làm loãng thứ người dùng đang cần đọc.
  if (!available) return null;

  return (
    <Card
      title="Kết luận của AI"
      actions={
        isAdmin && (
          <Button
            variant="ghost"
            disabled={recheck.isPending}
            onClick={() => recheck.mutate(domainId)}
            title="Xếp hàng một lượt hỏi lại model về domain này"
          >
            {recheck.isPending ? 'Đang xếp hàng…' : 'Hỏi lại AI'}
          </Button>
        )
      }
    >
      {recheck.isSuccess && (
        <p className="mb-3 rounded-md bg-emerald-50 px-3 py-2 text-sm text-emerald-800 dark:bg-emerald-950 dark:text-emerald-300">
          Đã xếp hàng hỏi lại. Kết quả xuất hiện ở đây sau khi job chạy xong — tải lại
          trang sau ít phút.
        </p>
      )}
      {recheck.isError && <ErrorState error={recheck.error} />}

      {verdicts.isPending && <Spinner />}
      {verdicts.isError && <ErrorState error={verdicts.error} />}

      {verdicts.data && verdicts.data.items.length === 0 && (
        <EmptyState>Chưa từng hỏi AI về domain này.</EmptyState>
      )}

      {verdicts.data && verdicts.data.items.length > 0 && (
        <ul className="divide-y divide-slate-100 text-sm dark:divide-slate-800">
          {verdicts.data.items.map((verdict) => (
            <li key={verdict.id} className="flex items-start gap-3 py-2">
              <span className="font-medium text-slate-800 dark:text-slate-100">
                {verdict.category}
              </span>
              <span className="tabular-nums text-slate-500 dark:text-slate-400">
                {Math.round(verdict.confidence * 100)}%
              </span>
              <span className="min-w-0 flex-1 text-slate-600 dark:text-slate-300">
                {verdict.reason || '—'}
              </span>
              <span className="shrink-0 text-xs text-slate-400">
                {formatRelative(verdict.created_at)}
              </span>
            </li>
          ))}
        </ul>
      )}

      <p className="mt-3 text-xs text-slate-500 dark:text-slate-400">
        Kết luận của AI chỉ là một tín hiệu, trọng số +2,5 — dưới ngưỡng chặn 5,5. Một
        mình nó không bao giờ đủ để chặn.
      </p>
    </Card>
  );
}
