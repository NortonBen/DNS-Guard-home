import { useState } from 'react';

import { useAIStatus, useSession } from '@/api/hooks';
import { AskPanel } from '@/components/ai/ask-panel';
import { HistoryPanel } from '@/components/ai/history-panel';
import { ProviderPanel } from '@/components/ai/provider-panel';
import { SkillsPanel } from '@/components/ai/skills-panel';
import { ToolsPanel } from '@/components/ai/tools-panel';
import { Card, EmptyState, ErrorState, Spinner, cx } from '@/components/ui/primitives';

type Tab = 'ask' | 'history' | 'provider' | 'skills' | 'tools';

interface TabDef {
  key: Tab;
  label: string;
  adminOnly?: boolean;
}

/**
 * Thứ tự thẻ theo tần suất dùng, không theo trình tự cài đặt.
 *
 * Người vận hành hằng ngày chỉ mở thẻ đầu; ba thẻ cuối là việc cấu hình một lần rồi
 * thỉnh thoảng mới đụng lại.
 */
const tabs: TabDef[] = [
  { key: 'ask', label: 'Hỏi AI' },
  { key: 'history', label: 'Lịch sử' },
  { key: 'provider', label: 'Nhà cung cấp', adminOnly: true },
  { key: 'skills', label: 'Skill', adminOnly: true },
  { key: 'tools', label: 'Công cụ & MCP', adminOnly: true },
];

/**
 * Màn AI.
 *
 * Gom cả năm mặt của tính năng vào một chỗ: hỏi đáp, nhật ký từng lượt gọi, cấu
 * hình nhà cung cấp, quy ước vận hành, và công cụ. Tách thành năm mục trên thanh
 * điều hướng sẽ làm menu dài gấp đôi cho một tính năng tuỳ chọn.
 */
export function AIScreen() {
  const session = useSession();
  const status = useAIStatus();
  const [tab, setTab] = useState<Tab>('ask');

  const isAdmin = session.data?.user.role === 'admin';
  const visible = tabs.filter((t) => !t.adminOnly || isAdmin);

  if (status.isPending) return <Spinner label="Đang tải trạng thái AI" />;
  if (status.isError) return <ErrorState error={status.error} />;
  if (!status.data) return null;

  // Máy chủ không mở được CSDL nhật ký AI: cả cụm tắt, nhưng phần còn lại của hệ
  // thống vẫn chạy bình thường. Nói rõ điều đó thay vì hiện một màn trống.
  if (!status.data.available) {
    return (
      <Card title="Hỏi AI">
        <EmptyState>
          Tính năng AI không khả dụng trên máy chủ này — không mở được CSDL nhật ký AI.
          Kiểm tra log máy chủ và quyền ghi tại đường dẫn <code>DNSGUARD_AI_DB_PATH</code>.
        </EmptyState>
      </Card>
    );
  }

  const active = visible.some((t) => t.key === tab) ? tab : 'ask';

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-1 border-b border-slate-200 dark:border-slate-800">
        {visible.map((item) => (
          <button
            key={item.key}
            type="button"
            onClick={() => setTab(item.key)}
            aria-current={item.key === active ? 'page' : undefined}
            className={cx(
              '-mb-px border-b-2 px-3 py-2 text-sm',
              item.key === active
                ? 'border-sky-600 font-medium text-sky-700 dark:text-sky-400'
                : 'border-transparent text-slate-500 hover:text-slate-700 dark:text-slate-400 dark:hover:text-slate-200',
            )}
          >
            {item.label}
          </button>
        ))}

        <span className="ml-auto pb-2 text-xs text-slate-500 dark:text-slate-400">
          {status.data.configured ? (
            <>
              <span className="font-mono">{status.data.model}</span>
              {status.data.enabled ? (
                <span className="ml-2 text-emerald-600 dark:text-emerald-400">đang bật</span>
              ) : (
                <span className="ml-2 text-slate-400">tự động đang tắt</span>
              )}
            </>
          ) : (
            <span className="text-amber-600 dark:text-amber-400">chưa cấu hình khóa API</span>
          )}
        </span>
      </div>

      {active === 'ask' && <AskPanel configured={status.data.configured ?? false} />}
      {active === 'history' && <HistoryPanel />}
      {active === 'provider' && <ProviderPanel status={status.data} />}
      {active === 'skills' && <SkillsPanel />}
      {active === 'tools' && <ToolsPanel />}
    </div>
  );
}
