import { useState } from 'react';

import { useUpdateAISettings } from '@/api/hooks';
import type { AIStatus } from '@/api/types';
import { Checkbox, Field, TextInput } from '@/components/ui/form';
import { Button, Card, ErrorState } from '@/components/ui/primitives';
import { formatNumber, formatRelative } from '@/lib/format';

/**
 * Gợi ý nhà cung cấp.
 *
 * Ba dòng này là toàn bộ thứ người vận hành cần biết để bắt đầu: endpoint nào đi
 * với tên model nào. Mọi dịch vụ nói được giao thức Chat Completions đều dùng được,
 * nên danh sách này là gợi ý chứ không phải giới hạn.
 */
const presets = [
  { label: 'DeepSeek', base_url: 'https://api.deepseek.com/v1', model: 'deepseek-chat' },
  { label: 'OpenAI', base_url: 'https://api.openai.com/v1', model: 'gpt-4o-mini' },
  { label: 'Ollama (máy nội bộ)', base_url: 'http://localhost:11434/v1', model: 'llama3.1' },
];

/**
 * Cấu hình nhà cung cấp model.
 *
 * Khóa API không bao giờ đọc lại được sau khi lưu: máy chủ chỉ trả về "đã có khóa"
 * hay chưa. Ô nhập vì thế để trống mỗi lần mở màn — bỏ trống nghĩa là giữ nguyên
 * khóa cũ, và nhập một dấu cách rồi lưu là cách gỡ khóa.
 */
export function ProviderPanel({ status }: { status: AIStatus }) {
  const update = useUpdateAISettings();

  const [baseURL, setBaseURL] = useState(status.base_url ?? '');
  const [model, setModel] = useState(status.model ?? '');
  const [apiKey, setApiKey] = useState('');
  const [batchSize, setBatchSize] = useState(String(status.batch_size ?? 40));
  const [enabled, setEnabled] = useState(status.enabled ?? false);

  function save() {
    update.mutate(
      {
        base_url: baseURL,
        model,
        // Trường vắng mặt nghĩa là "không đụng tới"; chuỗi rỗng là lệnh gỡ khóa.
        api_key: apiKey === '' ? undefined : apiKey.trim(),
        batch_size: Number(batchSize) || undefined,
        enabled,
      },
      { onSuccess: () => setApiKey('') },
    );
  }

  const usage = status.usage_30d;

  return (
    <div className="space-y-3">
      <Card
        title="Nhà cung cấp model"
        actions={
          <Button variant="primary" disabled={update.isPending} onClick={save}>
            {update.isPending ? 'Đang lưu…' : 'Lưu'}
          </Button>
        }
      >
        <div className="mb-4 flex flex-wrap gap-2">
          {presets.map((preset) => (
            <Button
              key={preset.label}
              variant="ghost"
              onClick={() => {
                setBaseURL(preset.base_url);
                setModel(preset.model);
              }}
            >
              {preset.label}
            </Button>
          ))}
        </div>

        <div className="grid gap-4 lg:grid-cols-2">
          <Field
            label="Base URL"
            htmlFor="ai-base-url"
            hint="Endpoint theo giao thức Chat Completions, thường kết thúc bằng /v1."
          >
            <TextInput
              id="ai-base-url"
              mono
              value={baseURL}
              onChange={(e) => setBaseURL(e.target.value)}
            />
          </Field>

          <Field label="Tên model" htmlFor="ai-model">
            <TextInput
              id="ai-model"
              mono
              value={model}
              onChange={(e) => setModel(e.target.value)}
            />
          </Field>

          <Field
            label="Khóa API"
            htmlFor="ai-key"
            hint={
              status.configured
                ? 'Đã lưu khóa. Bỏ trống để giữ nguyên; nhập khóa mới để thay.'
                : 'Chưa có khóa. Chưa nhập thì mọi tính năng AI đều tắt.'
            }
          >
            <TextInput
              id="ai-key"
              mono
              type="password"
              autoComplete="off"
              placeholder={status.configured ? '••••••••' : 'sk-…'}
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
            />
          </Field>

          <Field
            label="Số domain mỗi lượt hỏi"
            htmlFor="ai-batch"
            hint="Lô càng lớn càng rẻ trên mỗi domain, nhưng một lần trả lời sai khuôn càng đắt."
          >
            <TextInput
              id="ai-batch"
              type="number"
              min={1}
              max={200}
              value={batchSize}
              onChange={(e) => setBatchSize(e.target.value)}
            />
          </Field>
        </div>

        <label className="mt-4 flex items-center gap-2 text-sm">
          <Checkbox
            checked={enabled}
            disabled={status.external_enabled === false}
            onChange={(e) => setEnabled(e.target.checked)}
          />
          Bật phân loại tự động theo lịch
        </label>
        {status.external_enabled === false && (
          <p className="mt-1 text-xs text-amber-600 dark:text-amber-400">
            Không bật được khi <code>DNSGUARD_EXTERNAL_ENABLED=false</code>. Biến môi
            trường là công tắc cứng và thắng cấu hình trên giao diện.
          </p>
        )}

        {update.isError && (
          <div className="mt-3">
            <ErrorState error={update.error} />
          </div>
        )}
      </Card>

      <Card title="Mức tiêu thụ 30 ngày">
        {usage ? (
          <dl className="grid grid-cols-2 gap-4 text-sm md:grid-cols-4">
            <Stat label="Lượt gọi" value={formatNumber(usage.requests)} />
            <Stat label="Domain đã hỏi" value={formatNumber(usage.domains)} />
            <Stat label="Kết luận thu được" value={formatNumber(usage.verdicts)} />
            <Stat
              label="Lượt hỏng"
              value={formatNumber(usage.failed)}
              warn={usage.failed > 0}
            />
            <Stat label="Ký tự đã gửi" value={formatNumber(usage.prompt_chars)} />
            <Stat label="Ký tự nhận về" value={formatNumber(usage.reply_chars)} />
            <Stat label="Lần gần nhất" value={formatRelative(usage.last_at)} />
            <Stat label="File nhật ký" value={status.db_path ?? '—'} mono />
          </dl>
        ) : (
          <p className="text-sm text-slate-500 dark:text-slate-400">Chưa có lượt gọi nào.</p>
        )}
        <p className="mt-4 text-xs text-slate-500 dark:text-slate-400">
          Đếm ký tự chứ không đếm token: mỗi nhà cung cấp tính token theo cách riêng và
          không phải phản hồi nào cũng kèm số đếm, còn ký tự thì luôn đo được và đủ để
          thấy xu hướng.
        </p>
      </Card>
    </div>
  );
}

function Stat({
  label,
  value,
  warn,
  mono,
}: {
  label: string;
  value: string;
  warn?: boolean;
  mono?: boolean;
}) {
  return (
    <div>
      <dt className="text-xs text-slate-500 dark:text-slate-400">{label}</dt>
      <dd
        className={
          (warn ? 'text-amber-600 dark:text-amber-400' : 'text-slate-800 dark:text-slate-100') +
          (mono ? ' truncate font-mono text-xs' : ' text-lg font-semibold')
        }
        title={mono ? value : undefined}
      >
        {value}
      </dd>
    </div>
  );
}
