import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Link } from '@tanstack/react-router';

import { useDecision, useDomains } from '@/api/hooks';
import type { Domain } from '@/api/types';
import { CnameChain } from '@/components/domain/cname-chain';
import { SignalBadges } from '@/components/domain/signal-badges';
import { Button, Card, EmptyState, ErrorState, Spinner, StatusBadge, cx } from '@/components/ui/primitives';
import { formatNumber, formatRelative, formatScore } from '@/lib/format';
import { useDomain } from '@/api/hooks';

/** Thời gian giữ một hành động trước khi gửi đi, đủ để hoàn tác. */
const UNDO_WINDOW_MS = 10_000;

type Action = 'block' | 'allow' | 'ignore';

interface PendingAction {
  domain: Domain;
  action: Action;
  timer: number;
}

/**
 * Màn duyệt ứng viên — màn hình được dùng nhiều nhất.
 *
 * Thiết kế cho tốc độ, không cho vẻ đẹp: hai cột, không điều hướng, không modal.
 * Mục tiêu là duyệt 50 domain trong dưới ba phút mà không rời tay khỏi bàn phím.
 */
export function TriageScreen() {
  const decide = useDecision();

  const queue = useDomains({
    status: 'staging',
    // Sắp theo độ tin cậy chứ không theo điểm: bằng chứng chắc là thứ duyệt nhanh
    // nhất, nên đưa lên trước để mấy chục mục đầu trôi qua trong vài giây.
    sort: 'confidence:desc',
    limit: 100,
  });

  const items = useMemo(
    () => queue.data?.pages.flatMap((page) => page.items) ?? [],
    [queue.data],
  );

  const [cursor, setCursor] = useState(0);
  const [selection, setSelection] = useState<Set<number>>(new Set());
  const [pending, setPending] = useState<PendingAction | null>(null);
  const [showHelp, setShowHelp] = useState(false);
  const listRef = useRef<HTMLUListElement>(null);

  // Các mục đang chờ gửi bị ẩn khỏi hàng đợi ngay lập tức, nên con trỏ luôn trỏ vào
  // một mục thật sự còn cần quyết định.
  const visible = useMemo(
    () => items.filter((item) => item.id !== pending?.domain.id),
    [items, pending],
  );

  const current = visible[Math.min(cursor, Math.max(visible.length - 1, 0))];
  const detail = useDomain(current?.id ?? 0, !!current);

  /**
   * Hoàn tác bằng cách hoãn gửi.
   *
   * Giữ hành động trong hàng đợi cục bộ mười giây trước khi gọi API, thay vì ghi rồi
   * đảo ngược. Đơn giản hơn nhiều so với việc viết một quyết định vào nhật ký bất
   * biến rồi phải viết thêm một quyết định nữa để hủy nó.
   */
  const commit = useCallback(
    (domain: Domain, action: Action) => {
      decide.mutate({
        id: domain.id,
        action,
        reason: `Duyệt từ hàng đợi: ${action}`,
        category: domain.category?.key ?? 'ads',
      });
    },
    [decide],
  );

  const enqueue = useCallback(
    (domain: Domain, action: Action) => {
      // Hành động trước đó chưa kịp hết giờ thì gửi luôn: người dùng đã chuyển sang
      // mục khác, nghĩa là họ không định hoàn tác nữa.
      if (pending) {
        window.clearTimeout(pending.timer);
        commit(pending.domain, pending.action);
      }

      const timer = window.setTimeout(() => {
        commit(domain, action);
        setPending(null);
      }, UNDO_WINDOW_MS);

      setPending({ domain, action, timer });
      setCursor((c) => Math.min(c, Math.max(visible.length - 2, 0)));
    },
    [commit, pending, visible.length],
  );

  const undo = useCallback(() => {
    if (!pending) return;
    window.clearTimeout(pending.timer);
    setPending(null);
  }, [pending]);

  // Phím tắt. Bỏ qua khi con trỏ đang ở trong ô nhập liệu, để gõ lý do không kích
  // hoạt nhầm hành động.
  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      const target = event.target as HTMLElement;
      if (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable) {
        return;
      }
      if (event.metaKey || event.altKey) return;

      if (event.ctrlKey && event.key.toLowerCase() === 'z') {
        event.preventDefault();
        undo();
        return;
      }
      if (event.ctrlKey) return;

      switch (event.key.toLowerCase()) {
        case 'j':
        case 'arrowdown':
          event.preventDefault();
          if (event.shiftKey && current) {
            setSelection((s) => new Set(s).add(current.id));
          }
          setCursor((c) => Math.min(c + 1, visible.length - 1));
          break;
        case 'k':
        case 'arrowup':
          event.preventDefault();
          if (event.shiftKey && current) {
            setSelection((s) => new Set(s).add(current.id));
          }
          setCursor((c) => Math.max(c - 1, 0));
          break;
        case 'b':
          if (current) enqueue(current, 'block');
          break;
        case 'a':
          if (current) enqueue(current, 'allow');
          break;
        case 'i':
          if (current) enqueue(current, 'ignore');
          break;
        case '?':
          setShowHelp((v) => !v);
          break;
        case 'escape':
          setShowHelp(false);
          setSelection(new Set());
          break;
      }
    }

    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [current, enqueue, undo, visible.length]);

  // Giữ mục đang chọn trong khung nhìn khi di chuyển bằng phím.
  useEffect(() => {
    listRef.current
      ?.querySelector('[data-active="true"]')
      ?.scrollIntoView({ block: 'nearest' });
  }, [cursor]);

  if (queue.isPending) return <Spinner label="Đang tải hàng đợi" />;
  if (queue.isError) return <ErrorState error={queue.error} />;

  if (visible.length === 0 && !pending) {
    return (
      <Card title="Hàng đợi duyệt">
        <EmptyState>
          Không còn ứng viên nào chờ duyệt. Domain mới sẽ xuất hiện ở đây sau khi được chấm điểm.
        </EmptyState>
      </Card>
    );
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-sm text-slate-600 dark:text-slate-300">
          <strong className="font-semibold">{visible.length}</strong> ứng viên chờ duyệt
          {selection.size > 0 && ` · đã chọn ${selection.size}`}
        </p>
        <div className="flex items-center gap-2">
          {selection.size > 0 && current && (
            <Button
              variant="danger"
              onClick={() => {
                for (const id of selection) {
                  const domain = visible.find((d) => d.id === id);
                  if (domain) {
                    decide.mutate({
                      id,
                      action: 'block',
                      reason: 'Chặn cả nhóm từ màn duyệt',
                      category: domain.category?.key ?? 'ads',
                    });
                  }
                }
                setSelection(new Set());
              }}
            >
              Chặn {selection.size} mục đã chọn
            </Button>
          )}
          <Button variant="ghost" hotkey="?" onClick={() => setShowHelp((v) => !v)}>
            Phím tắt
          </Button>
        </div>
      </div>

      {showHelp && <HotkeyHelp />}

      <div className="grid gap-3 lg:grid-cols-[minmax(0,22rem)_1fr]">
        <Card className="lg:sticky lg:top-16 lg:self-start" title="Hàng đợi">
          <ul ref={listRef} className="max-h-[70vh] space-y-1 overflow-y-auto">
            {visible.map((domain, index) => (
              <li key={domain.id}>
                <button
                  type="button"
                  data-active={index === cursor}
                  onClick={() => setCursor(index)}
                  className={cx(
                    'w-full rounded-md px-2 py-1.5 text-left transition-colors',
                    index === cursor
                      ? 'bg-sky-50 ring-1 ring-sky-300 dark:bg-sky-950 dark:ring-sky-800'
                      : 'hover:bg-slate-50 dark:hover:bg-slate-800',
                    selection.has(domain.id) && 'ring-1 ring-amber-400',
                  )}
                >
                  <div className="flex items-baseline justify-between gap-2">
                    <span className="domain-name truncate">{domain.name}</span>
                    <span className="shrink-0 text-sm font-semibold tabular-nums text-red-600 dark:text-red-400">
                      {formatScore(domain.score)}
                    </span>
                  </div>
                  <div className="mt-0.5 flex items-center gap-2 text-xs text-slate-500 dark:text-slate-400">
                    <span>{domain.category?.label_vi ?? 'chưa phân loại'}</span>
                    <span>·</span>
                    <span>{formatNumber(domain.query_count)} truy vấn</span>
                    <span>·</span>
                    <span>{domain.client_count} client</span>
                  </div>
                </button>
              </li>
            ))}
          </ul>

          {queue.hasNextPage && (
            <Button
              variant="ghost"
              className="mt-2 w-full"
              disabled={queue.isFetchingNextPage}
              onClick={() => queue.fetchNextPage()}
            >
              Tải thêm
            </Button>
          )}
        </Card>

        {current && (
          <Card
            title={<span className="domain-name">{current.name}</span>}
            actions={
              <div className="flex items-center gap-1.5">
                <StatusBadge status={current.status} />
                <Button variant="danger" hotkey="B" onClick={() => enqueue(current, 'block')}>
                  Chặn
                </Button>
                <Button variant="secondary" hotkey="A" onClick={() => enqueue(current, 'allow')}>
                  Cho qua
                </Button>
                <Button variant="ghost" hotkey="I" onClick={() => enqueue(current, 'ignore')}>
                  Bỏ qua
                </Button>
                <Link
                  to="/domains/$domainId"
                  params={{ domainId: String(current.id) }}
                  className="rounded-md px-3 py-1.5 text-sm font-medium text-sky-700 hover:bg-sky-50 dark:text-sky-400 dark:hover:bg-sky-950"
                >
                  Chi tiết →
                </Link>
              </div>
            }
          >
            <div className="grid gap-4 md:grid-cols-2">
              <div>
                <h3 className="mb-1.5 text-xs font-semibold uppercase tracking-wide text-slate-500 dark:text-slate-400">
                  Bằng chứng · điểm {formatScore(current.score)}
                </h3>
                <SignalBadges signals={current.signals} />
                {current.confidence !== null && (
                  <p className="mt-2 text-xs text-slate-500 dark:text-slate-400">
                    Độ tin cậy {Math.round(current.confidence * 100)}% — mức độ chắc chắn của bằng
                    chứng, tách khỏi điểm số
                  </p>
                )}
              </div>

              <div className="space-y-3 text-sm">
                <div>
                  <h3 className="mb-1 text-xs font-semibold uppercase tracking-wide text-slate-500 dark:text-slate-400">
                    Lưu lượng
                  </h3>
                  <p>
                    {formatNumber(current.query_count)} truy vấn từ {current.client_count} client ·
                    thấy lần cuối {formatRelative(current.last_seen)}
                  </p>
                </div>

                <div>
                  <h3 className="mb-1 text-xs font-semibold uppercase tracking-wide text-slate-500 dark:text-slate-400">
                    Hạ tầng
                  </h3>
                  {detail.data?.facts.asn?.asn ? (
                    <p>
                      AS{detail.data.facts.asn.asn} {detail.data.facts.asn.org}
                      {detail.data.facts.asn.country && ` (${detail.data.facts.asn.country})`}
                    </p>
                  ) : (
                    <p className="text-slate-500 dark:text-slate-400">Chưa tra được ASN</p>
                  )}
                  {detail.data?.facts.rank?.tranco ? (
                    <p className="text-amber-600 dark:text-amber-400">
                      Tranco #{formatNumber(detail.data.facts.rank.tranco)} — cân nhắc kỹ trước khi chặn
                    </p>
                  ) : null}
                </div>
              </div>
            </div>

            <div className="mt-4">
              <h3 className="mb-1.5 text-xs font-semibold uppercase tracking-wide text-slate-500 dark:text-slate-400">
                Chuỗi CNAME
              </h3>
              <CnameChain domain={current.name} chain={detail.data?.facts.dns?.cname_chain} />
            </div>

            {detail.data && detail.data.siblings.length > 0 && (
              <div className="mt-4">
                <h3 className="mb-1.5 text-xs font-semibold uppercase tracking-wide text-slate-500 dark:text-slate-400">
                  Subdomain cùng gốc ({detail.data.siblings.length})
                </h3>
                <ul className="flex flex-wrap gap-1.5">
                  {detail.data.siblings.slice(0, 12).map((sibling) => (
                    <li
                      key={sibling.id}
                      className="domain-name rounded bg-slate-100 px-1.5 py-0.5 text-xs dark:bg-slate-800"
                    >
                      {sibling.name}
                      <span className="ml-1 text-slate-500 dark:text-slate-400">
                        {formatNumber(sibling.query_count)}
                      </span>
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </Card>
        )}
      </div>

      {pending && <UndoToast pending={pending} onUndo={undo} />}
    </div>
  );
}

function HotkeyHelp() {
  const rows: [string, string][] = [
    ['J / ↓', 'Mục kế tiếp'],
    ['K / ↑', 'Mục trước'],
    ['Shift + J/K', 'Chọn nhiều mục'],
    ['B', 'Chặn, sang mục kế tiếp'],
    ['A', 'Cho qua, sang mục kế tiếp'],
    ['I', 'Bỏ qua, sang mục kế tiếp'],
    ['Ctrl + Z', 'Hoàn tác trong 10 giây'],
    ['Esc', 'Bỏ chọn'],
    ['?', 'Đóng bảng này'],
  ];

  return (
    <Card title="Phím tắt">
      <dl className="grid gap-x-6 gap-y-1 text-sm sm:grid-cols-2 lg:grid-cols-3">
        {rows.map(([key, description]) => (
          <div key={key} className="flex items-baseline gap-2">
            <dt>
              <kbd className="rounded bg-slate-100 px-1.5 py-0.5 font-mono text-xs dark:bg-slate-800">
                {key}
              </kbd>
            </dt>
            <dd className="text-slate-600 dark:text-slate-300">{description}</dd>
          </div>
        ))}
      </dl>
    </Card>
  );
}

/** Thông báo đếm ngược, cho phép hủy hành động trước khi nó được gửi đi. */
function UndoToast({ pending, onUndo }: { pending: PendingAction; onUndo: () => void }) {
  const [remaining, setRemaining] = useState(Math.ceil(UNDO_WINDOW_MS / 1000));

  useEffect(() => {
    setRemaining(Math.ceil(UNDO_WINDOW_MS / 1000));
    const interval = window.setInterval(() => setRemaining((r) => Math.max(r - 1, 0)), 1000);
    return () => window.clearInterval(interval);
  }, [pending]);

  const labels: Record<Action, string> = {
    block: 'Đã chặn',
    allow: 'Đã cho qua',
    ignore: 'Đã bỏ qua',
  };

  return (
    <div
      role="status"
      className="fixed bottom-4 left-1/2 z-30 flex -translate-x-1/2 items-center gap-3 rounded-lg bg-slate-900 px-4 py-2.5 text-sm text-white shadow-lg dark:bg-slate-100 dark:text-slate-900"
    >
      <span>
        {labels[pending.action]} <span className="font-mono">{pending.domain.name}</span>
      </span>
      <button
        type="button"
        onClick={onUndo}
        className="rounded px-2 py-1 font-medium text-sky-300 hover:bg-white/10 dark:text-sky-700 dark:hover:bg-black/10"
      >
        Hoàn tác ({remaining}s)
      </button>
    </div>
  );
}
