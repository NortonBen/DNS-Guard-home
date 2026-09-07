import { useEffect, useRef, useState } from 'react';

import { useAIChat, useAIChats, useAITools, useAskAI, useDeleteAIChat } from '@/api/hooks';
import type { AIChatMessage, AIStep } from '@/api/types';
import { Textarea } from '@/components/ui/form';
import { Button, Card, EmptyState, ErrorState, Spinner, cx } from '@/components/ui/primitives';
import { formatRelative } from '@/lib/format';

/**
 * Màn hỏi đáp.
 *
 * Ba phần: danh sách cuộc trò chuyện bên trái, khung hội thoại ở giữa, ô nhập ở
 * dưới. Mỗi câu trả lời kèm dấu vết gọi công cụ mở ra được — không có phần đó thì
 * người vận hành không biết câu trả lời dựa trên dữ liệu nào, và một câu trả lời
 * nghe hợp lý về domain không tồn tại sẽ không bị phát hiện.
 */
export function AskPanel({ configured }: { configured: boolean }) {
  const [chatID, setChatID] = useState<number | null>(null);
  const [draft, setDraft] = useState('');

  const chats = useAIChats(configured);
  const chat = useAIChat(chatID);
  const ask = useAskAI();

  // Lượt vừa hỏi giữ ở state cục bộ để hiện ngay, thay vì đợi tải lại cả cuộc.
  const [pending, setPending] = useState<AIChatMessage[]>([]);
  const bottom = useRef<HTMLDivElement>(null);

  const messages = [...(chat.data?.messages ?? []), ...pending];

  useEffect(() => {
    bottom.current?.scrollIntoView({ behavior: 'smooth', block: 'end' });
  }, [messages.length, ask.isPending]);

  function send() {
    const question = draft.trim();
    if (!question || ask.isPending) return;

    setDraft('');
    setPending((prev) => [...prev, localMessage('user', question)]);

    ask.mutate(
      { question, chat_id: chatID ?? undefined },
      {
        onSuccess: (data) => {
          // Cuộc mới: gắn id để lượt sau nối tiếp đúng ngữ cảnh.
          if (chatID === null) setChatID(data.chat_id);
          setPending((prev) => [
            ...prev,
            localMessage('assistant', data.answer, data.steps),
          ]);
        },
        onError: () => {
          // Trả lại câu hỏi vào ô nhập: người dùng gõ xong rồi, bắt gõ lại là mất công.
          setDraft(question);
          setPending((prev) => prev.slice(0, -1));
        },
      },
    );
  }

  function openChat(id: number | null) {
    setChatID(id);
    setPending([]);
  }

  if (!configured) {
    return (
      <Card title="Hỏi AI">
        <EmptyState>
          Chưa cấu hình khóa API. Vào thẻ <strong>Nhà cung cấp</strong> để nhập khóa.
        </EmptyState>
      </Card>
    );
  }

  return (
    <div className="grid gap-3 lg:grid-cols-[16rem_1fr]">
      <ChatList
        activeID={chatID}
        onOpen={openChat}
        items={chats.data?.items ?? []}
        loading={chats.isPending}
      />

      <Card
        title={chatID === null ? 'Cuộc hỏi đáp mới' : `Cuộc hỏi đáp #${chatID}`}
        actions={<ToolCount />}
      >
        <div className="flex max-h-[28rem] min-h-[16rem] flex-col gap-3 overflow-y-auto pr-1">
          {messages.length === 0 && !ask.isPending && (
            <EmptyState>
              Hỏi bất cứ điều gì về mạng của bạn. AI tự tra dữ liệu bằng các công cụ
              đọc, ví dụ “criteo.com là gì và có nên chặn không?”.
            </EmptyState>
          )}
          {messages.map((message, index) => (
            <MessageBubble key={`${message.id}-${index}`} message={message} />
          ))}
          {ask.isPending && <Spinner label="AI đang tra cứu" />}
          <div ref={bottom} />
        </div>

        {ask.isError && (
          <div className="mt-3">
            <ErrorState error={ask.error} />
          </div>
        )}

        <div className="mt-3 flex items-end gap-2">
          <Textarea
            rows={2}
            value={draft}
            placeholder="Nhập câu hỏi… (Ctrl+Enter để gửi)"
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
                e.preventDefault();
                send();
              }
            }}
          />
          <Button variant="primary" disabled={!draft.trim() || ask.isPending} onClick={send}>
            Gửi
          </Button>
        </div>
      </Card>
    </div>
  );
}

/** Số công cụ model gọi được, kèm cảnh báo máy chủ MCP hỏng. */
function ToolCount() {
  const tools = useAITools(true);
  if (!tools.data) return null;

  const warnings = tools.data.warnings ?? [];
  return (
    <span className="text-xs text-slate-500 dark:text-slate-400">
      {tools.data.tools.length} công cụ
      {warnings.length > 0 && (
        <span className="ml-2 text-amber-600 dark:text-amber-400" title={warnings.join('\n')}>
          · {warnings.length} máy chủ MCP lỗi
        </span>
      )}
    </span>
  );
}

interface ChatListProps {
  activeID: number | null;
  onOpen: (id: number | null) => void;
  items: { id: number; title: string; updated_at: string }[];
  loading: boolean;
}

function ChatList({ activeID, onOpen, items, loading }: ChatListProps) {
  const remove = useDeleteAIChat();

  return (
    <Card
      title="Cuộc hỏi đáp"
      actions={
        <Button variant="ghost" onClick={() => onOpen(null)}>
          Mới
        </Button>
      }
    >
      {loading && <Spinner />}
      {!loading && items.length === 0 && <EmptyState>Chưa có cuộc nào.</EmptyState>}

      <ul className="space-y-1">
        {items.map((item) => (
          <li key={item.id} className="group flex items-center gap-1">
            <button
              type="button"
              onClick={() => onOpen(item.id)}
              className={cx(
                'flex-1 truncate rounded px-2 py-1.5 text-left text-sm',
                item.id === activeID
                  ? 'bg-sky-50 text-sky-800 dark:bg-sky-950 dark:text-sky-300'
                  : 'text-slate-600 hover:bg-slate-50 dark:text-slate-300 dark:hover:bg-slate-800',
              )}
            >
              <span className="block truncate">{item.title || `Cuộc #${item.id}`}</span>
              <span className="block text-xs text-slate-400">
                {formatRelative(item.updated_at)}
              </span>
            </button>
            <button
              type="button"
              aria-label={`Xóa cuộc ${item.title || item.id}`}
              onClick={() => remove.mutate(item.id)}
              className="rounded px-1 text-xs text-slate-300 opacity-0 transition group-hover:opacity-100 hover:text-red-600"
            >
              ✕
            </button>
          </li>
        ))}
      </ul>
    </Card>
  );
}

function MessageBubble({ message }: { message: AIChatMessage }) {
  const isUser = message.role === 'user';
  return (
    <div className={cx('flex', isUser ? 'justify-end' : 'justify-start')}>
      <div
        className={cx(
          'max-w-[85%] rounded-lg px-3 py-2 text-sm whitespace-pre-wrap',
          isUser
            ? 'bg-sky-600 text-white'
            : 'bg-slate-100 text-slate-800 dark:bg-slate-800 dark:text-slate-100',
        )}
      >
        {message.content}
        {message.steps.length > 0 && <StepTrace steps={message.steps} />}
      </div>
    </div>
  );
}

/**
 * Dấu vết gọi công cụ, mặc định gập lại.
 *
 * Gập vì phần lớn lượt đọc không cần tới nó; mở được vì khi câu trả lời có vẻ sai
 * thì đây là chỗ duy nhất cho biết model đã đọc gì.
 */
function StepTrace({ steps }: { steps: AIStep[] }) {
  return (
    <details className="mt-2 border-t border-slate-300/40 pt-2 text-xs dark:border-slate-600/40">
      <summary className="cursor-pointer text-slate-500 dark:text-slate-400">
        Đã tra {steps.length} lượt bằng công cụ
      </summary>
      <ul className="mt-2 space-y-2">
        {steps.map((step, index) => (
          <li key={index}>
            <p className="font-mono text-[11px] text-slate-600 dark:text-slate-300">
              {step.tool}({step.args})
            </p>
            <pre className="mt-1 max-h-32 overflow-auto rounded bg-slate-200/60 p-2 text-[11px] whitespace-pre-wrap dark:bg-slate-900/60">
              {step.result}
            </pre>
          </li>
        ))}
      </ul>
    </details>
  );
}

/** localMessage dựng một lượt chỉ tồn tại ở client, để hiện ngay trước khi tải lại. */
function localMessage(
  role: 'user' | 'assistant',
  content: string,
  steps: AIStep[] = [],
): AIChatMessage {
  return {
    id: -Date.now(),
    chat_id: 0,
    role,
    content,
    steps,
    created_at: new Date().toISOString(),
  };
}
