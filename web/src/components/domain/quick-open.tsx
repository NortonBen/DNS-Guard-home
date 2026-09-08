import { useEffect, useState } from 'react';
import { Link, useNavigate } from '@tanstack/react-router';
import { useQuery } from '@tanstack/react-query';

import { api, query } from '@/api/client';
import type { Domain, Page } from '@/api/types';
import { Button, StatusBadge, cx } from '@/components/ui/primitives';

/**
 * Chuẩn hóa thứ người dùng dán vào thành một tên miền.
 *
 * Người ta hiếm khi dán đúng một tên miền trần. Thứ nằm trong clipboard thường là một
 * URL đầy đủ lấy từ thanh địa chỉ, đôi khi kèm scheme, cổng, đường dẫn và tham số.
 * Bắt họ tự cắt bỏ những phần đó là đúng loại ma sát khiến người ta thôi không dùng
 * ô nhập này nữa.
 */
export function normalizeDomainInput(raw: string): string {
  let text = raw.trim().toLowerCase();
  if (!text) return '';

  text = text.replace(/^[a-z][a-z0-9+.-]*:\/\//, ''); // https:// , ftp:// …
  text = text.replace(/^[^/@]*@/, ''); // thông tin đăng nhập trong URL
  text = text.split(/[/?#]/)[0] ?? ''; // đường dẫn, query, fragment
  text = text.split(':')[0] ?? ''; // cổng
  text = text.replace(/^\.+|\.+$/g, ''); // dấu chấm thừa hai đầu

  // Dòng của file hosts: "0.0.0.0 quangcao.vn" — lấy phần cuối.
  const parts = text.split(/\s+/).filter(Boolean);
  return parts[parts.length - 1] ?? '';
}

function looksLikeDomain(name: string): boolean {
  return name.length > 3 && name.includes('.') && !name.includes(' ');
}

/**
 * Ô dán nhanh một domain để mở thẳng trang chi tiết của nó.
 *
 * Có nó thì việc xem xét nhiều domain liên tiếp không phải đi vòng qua màn danh sách
 * mỗi lần: dán, Enter, sang domain tiếp theo.
 */
export function QuickOpenDomain({ className }: { className?: string }) {
  const navigate = useNavigate();
  const [input, setInput] = useState('');
  const [submitted, setSubmitted] = useState('');

  const search = useQuery({
    queryKey: ['quick-open', submitted],
    queryFn: () => api<Page<Domain>>(`/domains${query({ q: submitted, limit: 8 })}`),
    enabled: looksLikeDomain(submitted),
  });

  const exact = search.data?.items.find((d) => d.name === submitted);
  const partial = (search.data?.items ?? []).filter((d) => d.name !== submitted);

  function submit(raw: string) {
    const name = normalizeDomainInput(raw);
    setInput(name);
    setSubmitted(name);
  }

  // Trùng khớp chính xác thì đi thẳng, không bắt bấm thêm một lần nữa.
  //
  // Trong effect chứ không trong thân render: điều hướng và setState lúc đang render
  // là tác dụng phụ, React sẽ cảnh báo và vòng lặp render có thể không bao giờ dừng.
  const exactId = exact?.id;
  useEffect(() => {
    if (exactId === undefined) return;
    setInput('');
    setSubmitted('');
    void navigate({ to: '/domains/$domainId', params: { domainId: String(exactId) } });
  }, [exactId, navigate]);

  return (
    <div className={className}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          submit(input);
        }}
        className="flex gap-2"
      >
        <input
          value={input}
          onChange={(e) => setInput(e.target.value)}
          // Dán rồi Enter là luồng thường gặp; dán xong đi luôn khi rõ ràng là một
          // tên miền thì bớt được một thao tác nữa.
          onPaste={(e) => {
            const text = e.clipboardData.getData('text');
            const name = normalizeDomainInput(text);
            if (looksLikeDomain(name)) {
              e.preventDefault();
              submit(text);
            }
          }}
          placeholder="Dán domain khác để mở nhanh…"
          aria-label="Dán domain để mở nhanh"
          spellCheck={false}
          className={cx(
            'flex-1 rounded-md px-2.5 py-1.5 font-mono text-sm ring-1',
            'ring-slate-300 dark:bg-slate-800 dark:ring-slate-700',
          )}
        />
        <Button type="submit" className="shrink-0 px-3 py-1.5 text-sm" disabled={!input.trim()}>
          Mở
        </Button>
      </form>

      {search.isFetching && (
        <p className="mt-1 text-xs text-slate-500 dark:text-slate-400">Đang tìm…</p>
      )}

      {search.data && !exact && (
        <div className="mt-1.5 text-xs">
          {partial.length > 0 ? (
            <div className="flex flex-wrap items-center gap-1">
              <span className="text-slate-500 dark:text-slate-400">Gần đúng:</span>
              {partial.slice(0, 5).map((d) => (
                <Link
                  key={d.id}
                  to="/domains/$domainId"
                  params={{ domainId: String(d.id) }}
                  onClick={() => {
                    setInput('');
                    setSubmitted('');
                  }}
                  className={cx(
                    'domain-name rounded px-1.5 py-0.5 ring-1',
                    'ring-slate-200 hover:ring-sky-500 dark:ring-slate-700',
                  )}
                >
                  {d.name}
                  <StatusBadge status={d.status} />
                </Link>
              ))}
            </div>
          ) : (
            <p className="text-slate-500 dark:text-slate-400">
              Chưa thấy <code className="font-mono">{submitted}</code> trong dữ liệu.{' '}
              <Link to="/manual" className="text-sky-700 underline dark:text-sky-400">
                Thêm thủ công
              </Link>
            </p>
          )}
        </div>
      )}
    </div>
  );
}
