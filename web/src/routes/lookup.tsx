import { useState } from 'react';

import { useCreateUnblockRequest, useLookup } from '@/api/hooks';
import { Button, Card, ErrorState } from '@/components/ui/primitives';

/**
 * Trang tra cứu cho người dùng trong mạng.
 *
 * Không lộ thông tin nội bộ: chỉ trả lời "có bị chặn không" và thuộc phân loại nào.
 * Điểm số, tín hiệu và lịch sử quyết định đều không hiện ở đây.
 */
export function LookupScreen() {
  const [input, setInput] = useState('');
  const [submitted, setSubmitted] = useState('');
  const [note, setNote] = useState('');
  const [sent, setSent] = useState(false);

  const result = useLookup(submitted);
  const request = useCreateUnblockRequest();

  return (
    <div className="mx-auto max-w-2xl space-y-3">
      <Card title="Kiểm tra một tên miền">
        <form
          onSubmit={(e) => {
            e.preventDefault();
            setSubmitted(input.trim().toLowerCase());
            setSent(false);
          }}
          className="flex gap-2"
        >
          <input
            value={input}
            onChange={(e) => setInput(e.target.value)}
            placeholder="ads.example.com"
            aria-label="Tên miền cần kiểm tra"
            className="flex-1 rounded-md px-3 py-2 font-mono text-sm ring-1 ring-slate-300 dark:bg-slate-800 dark:ring-slate-700"
          />
          <Button type="submit" variant="primary">
            Kiểm tra
          </Button>
        </form>

        {result.isError && (
          <div className="mt-3">
            <ErrorState error={result.error} />
          </div>
        )}

        {result.data && (
          <div className="mt-4">
            {result.data.blocked ? (
              <div className="rounded-md bg-red-50 p-3 dark:bg-red-950">
                <p className="text-sm font-semibold text-red-800 dark:text-red-300">
                  <span className="domain-name">{result.data.name}</span> đang bị chặn
                </p>
                {result.data.category && (
                  <p className="mt-0.5 text-sm text-red-700 dark:text-red-400">
                    Phân loại: {result.data.category.label_vi}
                  </p>
                )}
              </div>
            ) : (
              <div className="rounded-md bg-emerald-50 p-3 dark:bg-emerald-950">
                <p className="text-sm font-semibold text-emerald-800 dark:text-emerald-300">
                  <span className="domain-name">{result.data.name}</span> không bị chặn
                </p>
              </div>
            )}
          </div>
        )}
      </Card>

      {result.data?.blocked && (
        <Card title="Gửi yêu cầu mở chặn">
          {sent ? (
            <p
              role="status"
              className="rounded-md bg-emerald-50 px-3 py-2 text-sm text-emerald-800 dark:bg-emerald-950 dark:text-emerald-300"
            >
              Đã gửi yêu cầu. Quản trị viên sẽ xem xét và phản hồi.
            </p>
          ) : (
            <form
              onSubmit={(e) => {
                e.preventDefault();
                request.mutate(
                  { domain: result.data.name, note },
                  { onSuccess: () => { setSent(true); setNote(''); } },
                );
              }}
              className="space-y-3"
            >
              <label className="block text-sm">
                <span className="mb-1 block font-medium">Vì sao bạn cần mở chặn domain này?</span>
                <textarea
                  rows={3}
                  value={note}
                  onChange={(e) => setNote(e.target.value)}
                  placeholder="Trang đặt vé không thanh toán được"
                  className="w-full rounded-md px-3 py-2 text-sm ring-1 ring-slate-300 dark:bg-slate-800 dark:ring-slate-700"
                />
              </label>
              <Button type="submit" variant="primary" disabled={request.isPending}>
                Gửi yêu cầu
              </Button>
              {request.isError && <ErrorState error={request.error} />}
            </form>
          )}
        </Card>
      )}
    </div>
  );
}
