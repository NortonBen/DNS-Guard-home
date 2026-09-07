import { useState } from 'react';

import { useAISkills, useDeleteAISkill, useSaveAISkill } from '@/api/hooks';
import type { AISkill } from '@/api/types';
import { Checkbox, Field, TextInput, Textarea } from '@/components/ui/form';
import { Button, Card, EmptyState, ErrorState, Spinner, cx } from '@/components/ui/primitives';

/** Bản nháp một skill đang soạn. */
interface Draft {
  name: string;
  description: string;
  triggers: string;
  content: string;
  always: boolean;
  enabled: boolean;
  builtin: boolean;
}

const emptyDraft: Draft = {
  name: '',
  description: '',
  triggers: '',
  content: '',
  always: false,
  enabled: true,
  builtin: false,
};

/**
 * Quản lý skill.
 *
 * Skill là một mẩu quy ước vận hành có từ khóa kích hoạt riêng: câu hỏi khớp từ
 * khóa nào thì chỉ mẩu đó được chèn vào ngữ cảnh. Rẻ hơn nhiều so với nhồi mọi quy
 * ước vào một system prompt gửi lại ở mọi lượt.
 */
export function SkillsPanel() {
  const skills = useAISkills(true);
  const save = useSaveAISkill();
  const remove = useDeleteAISkill();

  const [draft, setDraft] = useState<Draft | null>(null);

  function edit(skill: AISkill) {
    setDraft({
      name: skill.name,
      description: skill.description,
      triggers: skill.triggers.join(', '),
      content: skill.content,
      always: skill.always,
      enabled: skill.enabled,
      builtin: skill.builtin,
    });
  }

  function submit() {
    if (!draft || !draft.name.trim()) return;
    save.mutate(
      {
        name: draft.name.trim(),
        description: draft.description,
        triggers: draft.triggers
          .split(',')
          .map((t) => t.trim())
          .filter(Boolean),
        content: draft.content,
        always: draft.always,
        enabled: draft.enabled,
        builtin: draft.builtin,
      },
      { onSuccess: () => setDraft(null) },
    );
  }

  if (skills.isPending) return <Spinner label="Đang tải skill" />;
  if (skills.isError) return <ErrorState error={skills.error} />;

  const items = skills.data?.items ?? [];

  return (
    <div className="space-y-3">
      <Card
        title="Skill — quy ước vận hành"
        actions={
          <Button variant="primary" onClick={() => setDraft({ ...emptyDraft })}>
            Thêm skill
          </Button>
        }
      >
        <p className="mb-3 text-xs text-slate-500 dark:text-slate-400">
          Skill có từ khóa kích hoạt chỉ được chèn vào ngữ cảnh khi câu hỏi khớp một
          trong các từ khóa đó. Skill đánh dấu <strong>luôn dùng</strong> được chèn vào
          mọi câu hỏi. Từ khóa không phân biệt hoa thường và không phân biệt dấu.
        </p>

        {items.length === 0 ? (
          <EmptyState>Chưa có skill nào.</EmptyState>
        ) : (
          <ul className="divide-y divide-slate-100 dark:divide-slate-800">
            {items.map((skill) => (
              <li key={skill.id} className="flex items-start gap-3 py-2.5">
                <div className="min-w-0 flex-1">
                  <p className="flex items-center gap-2 text-sm font-medium text-slate-800 dark:text-slate-100">
                    {skill.name}
                    {skill.always && <Tag>luôn dùng</Tag>}
                    {skill.builtin && <Tag>dựng sẵn</Tag>}
                    {!skill.enabled && <Tag muted>đã tắt</Tag>}
                  </p>
                  {skill.description && (
                    <p className="text-xs text-slate-500 dark:text-slate-400">
                      {skill.description}
                    </p>
                  )}
                  {skill.triggers.length > 0 && (
                    <p className="mt-1 font-mono text-[11px] text-slate-400">
                      {skill.triggers.join(' · ')}
                    </p>
                  )}
                </div>
                <div className="flex shrink-0 gap-1">
                  <Button variant="ghost" onClick={() => edit(skill)}>
                    Sửa
                  </Button>
                  {!skill.builtin && (
                    <Button variant="ghost" onClick={() => remove.mutate(skill.id)}>
                      Xóa
                    </Button>
                  )}
                </div>
              </li>
            ))}
          </ul>
        )}
        {remove.isError && <ErrorState error={remove.error} />}
      </Card>

      {draft && (
        <Card
          title={draft.builtin ? `Sửa skill dựng sẵn: ${draft.name}` : 'Soạn skill'}
          actions={
            <div className="flex gap-2">
              <Button variant="ghost" onClick={() => setDraft(null)}>
                Hủy
              </Button>
              <Button
                variant="primary"
                disabled={!draft.name.trim() || save.isPending}
                onClick={submit}
              >
                {save.isPending ? 'Đang lưu…' : 'Lưu'}
              </Button>
            </div>
          }
        >
          <div className="grid gap-4 lg:grid-cols-2">
            <Field label="Tên" htmlFor="skill-name" required>
              <TextInput
                id="skill-name"
                value={draft.name}
                // Đổi tên một skill đã có sẽ tạo skill mới thay vì sửa cái cũ, nên
                // khóa lại khi đang sửa.
                disabled={draft.builtin}
                onChange={(e) => setDraft({ ...draft, name: e.target.value })}
              />
            </Field>
            <Field label="Mô tả ngắn" htmlFor="skill-desc">
              <TextInput
                id="skill-desc"
                value={draft.description}
                onChange={(e) => setDraft({ ...draft, description: e.target.value })}
              />
            </Field>
          </div>

          <div className="mt-4">
            <Field
              label="Từ khóa kích hoạt"
              htmlFor="skill-triggers"
              hint="Ngăn bằng dấu phẩy. Bỏ trống nếu đánh dấu “luôn dùng”."
            >
              <TextInput
                id="skill-triggers"
                value={draft.triggers}
                disabled={draft.always}
                onChange={(e) => setDraft({ ...draft, triggers: e.target.value })}
              />
            </Field>
          </div>

          <div className="mt-4">
            <Field label="Nội dung" htmlFor="skill-content">
              <Textarea
                id="skill-content"
                rows={8}
                value={draft.content}
                onChange={(e) => setDraft({ ...draft, content: e.target.value })}
              />
            </Field>
          </div>

          <div className="mt-4 flex gap-6">
            <label className="flex items-center gap-2 text-sm">
              <Checkbox
                checked={draft.always}
                onChange={(e) => setDraft({ ...draft, always: e.target.checked })}
              />
              Luôn dùng
            </label>
            <label className="flex items-center gap-2 text-sm">
              <Checkbox
                checked={draft.enabled}
                onChange={(e) => setDraft({ ...draft, enabled: e.target.checked })}
              />
              Đang bật
            </label>
          </div>

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

function Tag({ children, muted }: { children: string; muted?: boolean }) {
  return (
    <span
      className={cx(
        'rounded px-1.5 py-0.5 text-[10px] font-normal',
        muted
          ? 'bg-slate-100 text-slate-500 dark:bg-slate-800 dark:text-slate-400'
          : 'bg-sky-100 text-sky-700 dark:bg-sky-950 dark:text-sky-300',
      )}
    >
      {children}
    </span>
  );
}
