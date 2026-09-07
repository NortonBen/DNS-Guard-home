import { useState } from 'react';

import { useAIMCPServers, useAITools, useDeleteMCPServer, useSaveMCPServer } from '@/api/hooks';
import { Checkbox, Field, TextInput } from '@/components/ui/form';
import { Button, Card, EmptyState, ErrorState, Spinner } from '@/components/ui/primitives';

interface Draft {
  name: string;
  url: string;
  auth_header: string;
  enabled: boolean;
  note: string;
}

const emptyDraft: Draft = { name: '', url: '', auth_header: '', enabled: true, note: '' };

/**
 * Công cụ và máy chủ MCP.
 *
 * Hai phần trên cùng một màn vì chúng là hai nửa của một câu hỏi: "model gọi được
 * những gì". Phần trên là công cụ đọc dựng sẵn, phần dưới là nơi thêm công cụ ngoài.
 */
export function ToolsPanel() {
  const tools = useAITools(true);
  const servers = useAIMCPServers(true);
  const save = useSaveMCPServer();
  const remove = useDeleteMCPServer();

  const [draft, setDraft] = useState<Draft | null>(null);

  return (
    <div className="space-y-3">
      <Card title="Công cụ model gọi được">
        <p className="mb-3 text-xs text-slate-500 dark:text-slate-400">
          Công cụ đọc luôn có cho mọi vai trò. Công cụ <code>domain_recheck</code> chỉ
          xuất hiện với quản trị viên, và nó chỉ xếp hàng một lượt hỏi lại — không có
          công cụ nào đổi được trạng thái domain. Chặn và bỏ chặn vẫn phải bấm trên
          giao diện.
        </p>

        {tools.isPending && <Spinner />}
        {tools.isError && <ErrorState error={tools.error} />}

        {(tools.data?.warnings ?? []).length > 0 && (
          <ul className="mb-3 space-y-1 rounded-md bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:bg-amber-950 dark:text-amber-300">
            {tools.data?.warnings.map((warning, index) => <li key={index}>{warning}</li>)}
          </ul>
        )}

        <ul className="divide-y divide-slate-100 text-sm dark:divide-slate-800">
          {(tools.data?.tools ?? []).map((tool) => (
            <li key={tool.name} className="py-2">
              <p className="font-mono text-xs text-slate-700 dark:text-slate-200">{tool.name}</p>
              <p className="text-xs text-slate-500 dark:text-slate-400">{tool.description}</p>
            </li>
          ))}
        </ul>
      </Card>

      <Card
        title="Máy chủ MCP"
        actions={
          <Button variant="primary" onClick={() => setDraft({ ...emptyDraft })}>
            Thêm máy chủ
          </Button>
        }
      >
        <p className="mb-3 text-xs text-slate-500 dark:text-slate-400">
          Máy chủ MCP ngoài cung cấp thêm công cụ cho model. Công cụ của chúng được gắn
          tiền tố tên máy chủ và không bao giờ đè lên công cụ dựng sẵn. Bạn tự chịu
          trách nhiệm về những gì máy chủ đó làm được.
        </p>

        {servers.isPending && <Spinner />}
        {servers.isError && <ErrorState error={servers.error} />}

        {(servers.data?.items ?? []).length === 0 && !servers.isPending ? (
          <EmptyState>Chưa khai báo máy chủ MCP nào.</EmptyState>
        ) : (
          <ul className="divide-y divide-slate-100 dark:divide-slate-800">
            {(servers.data?.items ?? []).map((server) => (
              <li key={server.id} className="flex items-start gap-3 py-2.5">
                <div className="min-w-0 flex-1">
                  <p className="text-sm font-medium text-slate-800 dark:text-slate-100">
                    {server.name}
                    {!server.enabled && (
                      <span className="ml-2 text-xs font-normal text-slate-400">đã tắt</span>
                    )}
                  </p>
                  <p className="truncate font-mono text-xs text-slate-500 dark:text-slate-400">
                    {server.url}
                  </p>
                  {server.note && (
                    <p className="text-xs text-slate-500 dark:text-slate-400">{server.note}</p>
                  )}
                </div>
                <div className="flex shrink-0 items-center gap-2">
                  {server.has_auth && (
                    <span className="text-[10px] text-slate-400" title="Đã lưu token xác thực">
                      có token
                    </span>
                  )}
                  <Button
                    variant="ghost"
                    onClick={() =>
                      setDraft({
                        name: server.name,
                        url: server.url,
                        auth_header: '',
                        enabled: server.enabled,
                        note: server.note,
                      })
                    }
                  >
                    Sửa
                  </Button>
                  <Button variant="ghost" onClick={() => remove.mutate(server.id)}>
                    Xóa
                  </Button>
                </div>
              </li>
            ))}
          </ul>
        )}
      </Card>

      {draft && (
        <Card
          title="Khai báo máy chủ MCP"
          actions={
            <div className="flex gap-2">
              <Button variant="ghost" onClick={() => setDraft(null)}>
                Hủy
              </Button>
              <Button
                variant="primary"
                disabled={!draft.name.trim() || !draft.url.trim() || save.isPending}
                onClick={() =>
                  save.mutate(
                    {
                      name: draft.name.trim(),
                      url: draft.url.trim(),
                      auth_header: draft.auth_header.trim(),
                      enabled: draft.enabled,
                      note: draft.note,
                    },
                    { onSuccess: () => setDraft(null) },
                  )
                }
              >
                {save.isPending ? 'Đang lưu…' : 'Lưu'}
              </Button>
            </div>
          }
        >
          <div className="grid gap-4 lg:grid-cols-2">
            <Field label="Tên" htmlFor="mcp-name" required hint="Dùng làm tiền tố tên công cụ.">
              <TextInput
                id="mcp-name"
                value={draft.name}
                onChange={(e) => setDraft({ ...draft, name: e.target.value })}
              />
            </Field>
            <Field label="URL" htmlFor="mcp-url" required>
              <TextInput
                id="mcp-url"
                mono
                placeholder="https://mcp.vi-du.vn/rpc"
                value={draft.url}
                onChange={(e) => setDraft({ ...draft, url: e.target.value })}
              />
            </Field>
            <Field
              label="Header xác thực"
              htmlFor="mcp-auth"
              hint="Dạng Tên:Giá-trị. Bỏ trống khi sửa nghĩa là giữ nguyên token cũ."
            >
              <TextInput
                id="mcp-auth"
                mono
                type="password"
                autoComplete="off"
                placeholder="Authorization:Bearer …"
                value={draft.auth_header}
                onChange={(e) => setDraft({ ...draft, auth_header: e.target.value })}
              />
            </Field>
            <Field label="Ghi chú" htmlFor="mcp-note">
              <TextInput
                id="mcp-note"
                value={draft.note}
                onChange={(e) => setDraft({ ...draft, note: e.target.value })}
              />
            </Field>
          </div>

          <label className="mt-4 flex items-center gap-2 text-sm">
            <Checkbox
              checked={draft.enabled}
              onChange={(e) => setDraft({ ...draft, enabled: e.target.checked })}
            />
            Đang bật
          </label>

          {save.isError && (
            <div className="mt-3">
              <ErrorState error={save.error} />
            </div>
          )}
        </Card>
      )}
    </div>
  );
}
