import { useEffect, useState } from 'react';

import {
  useJob,
  useRefreshLookup,
  useSettings,
  useUpdateAnalysis,
  useUpdateLifecycle,
  useUpdatePublishSettings,
  useUpdateProtectList,
} from '@/api/hooks';
import type { AnalysisSettings, LookupTable, PublishSettings, SystemInfo } from '@/api/types';
import { Checkbox, Field, Select, TextInput, Textarea } from '@/components/ui/form';
import { Button, Card, ErrorState, Spinner, cx } from '@/components/ui/primitives';
import { formatBytes, formatNumber, formatRelative } from '@/lib/format';

/**
 * Màn Cài đặt.
 *
 * Bốn nhóm, xếp theo mức độ nguy hiểm giảm dần: danh sách bảo vệ (sai là chặn nhầm
 * thứ quan trọng), vòng đời (sai là chặn quá sớm hoặc dao động), dữ liệu tra cứu
 * (thiếu là mất tín hiệu), và thông tin hệ thống (chỉ đọc).
 */
export function SettingsScreen() {
  const settings = useSettings();

  if (settings.isPending) return <Spinner label="Đang tải cài đặt" />;
  if (settings.isError) return <ErrorState error={settings.error} />;
  if (!settings.data) return null;

  return (
    <div className="space-y-3">
      <ProtectSection hard={settings.data.protect.hard} soft={settings.data.protect.soft} />
      <LifecycleSection lifecycle={settings.data.lifecycle} />
      <AnalysisSection analysis={settings.data.analysis} />
      <PublishSection publish={settings.data.publish} />
      <LookupSection tables={settings.data.lookup_tables} />
      <SystemSection system={settings.data.system} />
    </div>
  );
}

/** Danh sách bảo vệ: mềm sửa được, cứng chỉ đọc. */
function ProtectSection({ hard, soft }: { hard: string[]; soft: string[] }) {
  const update = useUpdateProtectList();
  const [text, setText] = useState(soft.join('\n'));
  const [saved, setSaved] = useState(false);

  // Đồng bộ lại khi máy chủ trả về danh sách khác, ví dụ sau khi lưu.
  useEffect(() => {
    setText(soft.join('\n'));
  }, [soft]);

  const dirty = text !== soft.join('\n');

  function save() {
    const list = text
      .split('\n')
      .map((line) => line.trim())
      .filter(Boolean);
    update.mutate(list, { onSuccess: () => setSaved(true) });
  }

  return (
    <Card
      title="Danh sách bảo vệ"
      actions={
        <div className="flex items-center gap-2">
          {dirty && (
            <Button variant="ghost" onClick={() => setText(soft.join('\n'))}>
              Hủy thay đổi
            </Button>
          )}
          <Button variant="primary" disabled={!dirty || update.isPending} onClick={save}>
            {update.isPending ? 'Đang lưu…' : 'Lưu'}
          </Button>
        </div>
      }
    >
      <div className="grid gap-4 lg:grid-cols-2">
        <Field
          label="Danh sách mềm — bạn tự quản lý"
          htmlFor="soft-allow"
          hint="Mỗi dòng một tên miền. Khớp theo hậu tố: congty.vn bảo vệ luôn mọi subdomain của nó."
        >
          <Textarea
            id="soft-allow"
            rows={8}
            value={text}
            onChange={(e) => {
              setText(e.target.value);
              setSaved(false);
            }}
            className="font-mono"
            placeholder={'noibo.congty.vn\nmaychu.vn'}
          />
        </Field>

        <div>
          <p className="mb-1 text-xs font-medium text-slate-600 dark:text-slate-300">
            Danh sách cứng — nằm trong mã nguồn ({hard.length} mục)
          </p>
          {/* Cố ý không cho sửa từ giao diện: đây là những tên miền mà chặn nhầm sẽ
              hỏng thông báo đẩy, cập nhật hệ điều hành hoặc cổng thanh toán. Sửa
              phải qua pull request và review. */}
          <div className="h-[9.5rem] overflow-auto rounded-md bg-slate-50 p-2 ring-1 ring-slate-200 dark:bg-slate-950 dark:ring-slate-800">
            <ul className="grid gap-x-4 sm:grid-cols-2">
              {hard.map((name) => (
                <li key={name} className="domain-name text-xs text-slate-600 dark:text-slate-400">
                  {name}
                </li>
              ))}
            </ul>
          </div>
          <p className="mt-1 text-xs text-slate-500 dark:text-slate-400">
            Không sửa được từ giao diện. Chặn nhầm những tên miền này sẽ hỏng thông báo đẩy, cập
            nhật hệ điều hành hoặc thanh toán.
          </p>
        </div>
      </div>

      {update.isError && (
        <div className="mt-3">
          <ErrorState error={update.error} />
        </div>
      )}
      {saved && !dirty && (
        <p
          role="status"
          className="mt-3 rounded-md bg-emerald-50 px-3 py-2 text-sm text-emerald-800 dark:bg-emerald-950 dark:text-emerald-300"
        >
          Đã lưu danh sách bảo vệ mềm.
        </p>
      )}
    </Card>
  );
}

interface Lifecycle {
  staging_days: number;
  confirm_ttl_days: number;
  staging_days_default: number;
  confirm_ttl_days_default: number;
}

/** Ngưỡng vòng đời domain. */
function LifecycleSection({ lifecycle }: { lifecycle: Lifecycle }) {
  const update = useUpdateLifecycle();
  const [staging, setStaging] = useState(String(lifecycle.staging_days));
  const [confirm, setConfirm] = useState(String(lifecycle.confirm_ttl_days));

  useEffect(() => {
    setStaging(String(lifecycle.staging_days));
    setConfirm(String(lifecycle.confirm_ttl_days));
  }, [lifecycle]);

  const dirty =
    Number(staging) !== lifecycle.staging_days || Number(confirm) !== lifecycle.confirm_ttl_days;

  return (
    <Card
      title="Vòng đời domain"
      actions={
        <Button
          variant="primary"
          disabled={!dirty || update.isPending}
          onClick={() =>
            update.mutate({ staging_days: Number(staging), confirm_ttl_days: Number(confirm) })
          }
        >
          {update.isPending ? 'Đang lưu…' : 'Lưu'}
        </Button>
      }
    >
      <div className="grid gap-4 md:grid-cols-2">
        <Field
          label="Thời gian chờ trước khi tự chặn (ngày)"
          htmlFor="staging-days"
          hint={`Mặc định ${lifecycle.staging_days_default}. Domain vượt ngưỡng điểm nằm ở trạng thái chờ ngần này ngày để bạn kịp can thiệp. Đặt ngắn hơn thì chặn nhanh hơn nhưng dễ chặn nhầm mà không ai kịp thấy.`}
        >
          <TextInput
            id="staging-days"
            type="number"
            min={1}
            max={90}
            value={staging}
            onChange={(e) => setStaging(e.target.value)}
          />
        </Field>

        <Field
          label="Giữ trạng thái chặn khi domain im lặng (ngày)"
          htmlFor="confirm-days"
          hint={`Mặc định ${lifecycle.confirm_ttl_days_default}. Phải dài hơn hẳn thời gian chờ: nếu ngắn, domain đã chặn sẽ hết hạn rồi vào lại hàng chờ rồi bị chặn lại — dao động vĩnh viễn.`}
        >
          <TextInput
            id="confirm-days"
            type="number"
            min={30}
            max={3650}
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
          />
        </Field>
      </div>

      {update.isError && (
        <div className="mt-3">
          <ErrorState error={update.error} />
        </div>
      )}
    </Card>
  );
}

/**
 * Công tắc cho nguồn phân tích chạm trực tiếp ra ngoài.
 *
 * Tách riêng khỏi các mục khác vì nó có hệ quả về quyền riêng tư: bật lên nghĩa là
 * máy chủ sẽ tự đi gõ cửa những tên miền thấy trong mạng, và các máy chủ đó biết
 * được điều đó.
 */
function AnalysisSection({ analysis }: { analysis: AnalysisSettings }) {
  const update = useUpdateAnalysis();
  const locked = !analysis.external_enabled;

  return (
    <Card title="Phân tích ngoài">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0 max-w-2xl">
          <label className="flex items-start gap-2.5">
            <Checkbox
              checked={analysis.http_enabled}
              disabled={locked || update.isPending}
              onChange={(e) => update.mutate({ http_enabled: e.target.checked })}
              className="mt-0.5"
            />
            <span>
              <span className="text-sm font-medium">Phân tích header và HTML của trang gốc</span>
              <span className="mt-0.5 block text-xs text-slate-500 dark:text-slate-400">
                Tải trang gốc của domain để tìm dấu hiệu điểm thu thập, trang đỗ tên miền,
                cookie xuyên trang. Không chạy JavaScript, không tải tài nguyên con.
              </span>
            </span>
          </label>

          {/* Hệ quả về quyền riêng tư phải nói trước, không giấu sau một cú bấm. */}
          <p className="mt-2 rounded-md bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:bg-amber-950 dark:text-amber-300">
            Bật lên nghĩa là máy chủ này sẽ tự truy cập các tên miền thấy trong mạng, và
            chúng biết được điều đó. Chỉ những domain đang chờ quyết định, có lưu lượng
            thật, và không nằm trong danh sách bảo vệ mới bị tải.
          </p>

          {locked && (
            <p className="mt-2 text-xs text-slate-500 dark:text-slate-400">
              Đang bị khóa bởi <code className="font-mono">DNSGUARD_EXTERNAL_ENABLED=false</code> —
              công tắc đó ở biến môi trường và thắng cấu hình tại đây.
            </p>
          )}
          {update.isError && (
            <div className="mt-2">
              <ErrorState error={update.error} />
            </div>
          )}
        </div>

        <dl className="grid shrink-0 grid-cols-[auto_auto] gap-x-3 gap-y-1 text-xs">
          <dt className="text-slate-500 dark:text-slate-400">Đang hoạt động</dt>
          <dd className={analysis.http_effective ? 'text-emerald-600 dark:text-emerald-400' : ''}>
            {analysis.http_effective ? 'có' : 'không'}
          </dd>
          <dt className="text-slate-500 dark:text-slate-400">VirusTotal</dt>
          <dd className={analysis.vt_configured ? 'text-emerald-600 dark:text-emerald-400' : ''}>
            {analysis.vt_configured
              ? analysis.vt_key_hint
                ? `đã có khóa …${analysis.vt_key_hint}`
                : 'đã có khóa API'
              : 'chưa cấu hình'}
          </dd>
        </dl>
      </div>

      <VirusTotalKey analysis={analysis} />
    </Card>
  );
}

/**
 * Khóa API VirusTotal.
 *
 * Ô nhập là loại password và không bao giờ được điền sẵn: máy chủ chỉ trả về bốn ký
 * tự cuối, nên không có gì để điền. Muốn đổi thì phải dán lại cả khóa — đúng như
 * mong đợi với một bí mật.
 */
function VirusTotalKey({ analysis }: { analysis: AnalysisSettings }) {
  const update = useUpdateAnalysis();
  const [key, setKey] = useState('');
  const [editing, setEditing] = useState(false);

  const save = () => {
    update.mutate(
      { vt_api_key: key.trim() },
      {
        onSuccess: () => {
          setKey('');
          setEditing(false);
        },
      },
    );
  };

  if (analysis.vt_from_env) {
    return (
      <p className="mt-3 text-xs text-slate-500 dark:text-slate-400">
        Khóa đang lấy từ <code className="font-mono">DNSGUARD_VT_API_KEY</code>. Nhập khóa tại
        đây sẽ ghi đè biến môi trường đó.{' '}
        <button
          type="button"
          className="underline underline-offset-2"
          onClick={() => setEditing(true)}
        >
          Nhập khóa khác
        </button>
      </p>
    );
  }

  if (!editing && analysis.vt_configured) {
    return (
      <div className="mt-3 flex flex-wrap items-center gap-2 text-xs">
        <span className="text-slate-500 dark:text-slate-400">
          Khóa đã lưu trên máy chủ và không hiện lại được.
        </span>
        <Button className="px-2 py-1 text-xs" onClick={() => setEditing(true)}>
          Đổi khóa
        </Button>
        <Button
          className="px-2 py-1 text-xs"
          disabled={update.isPending}
          onClick={() => update.mutate({ vt_api_key: '' })}
        >
          Gỡ khóa
        </Button>
        {update.isError && <ErrorState error={update.error} />}
      </div>
    );
  }

  return (
    <div className="mt-3 max-w-xl">
      <Field
        label="Khóa API VirusTotal"
        htmlFor="vt-key"
        hint="Chỉ domain đã đạt từ 3,0 điểm mới được tra, nên bậc miễn phí là đủ. Khóa được thử một lượt gọi thật trước khi lưu."
      >
        <div className="flex gap-2">
          <TextInput
            id="vt-key"
            type="password"
            autoComplete="off"
            spellCheck={false}
            placeholder="64 ký tự hex"
            value={key}
            onChange={(e) => setKey(e.target.value)}
          />
          <Button
            variant="primary"
            className="shrink-0"
            disabled={key.trim() === '' || update.isPending}
            onClick={save}
          >
            {update.isPending ? 'Đang kiểm tra…' : 'Lưu'}
          </Button>
          {(editing || analysis.vt_configured) && (
            <Button
              className="shrink-0"
              onClick={() => {
                setKey('');
                setEditing(false);
              }}
            >
              Hủy
            </Button>
          )}
        </div>
      </Field>
      {update.isError && (
        <div className="mt-2">
          <ErrorState error={update.error} />
        </div>
      )}
    </div>
  );
}

/**
 * Địa chỉ đích trong file hosts đã xuất bản.
 *
 * Không phải chi tiết vụn vặt: 0.0.0.0 làm kết nối hỏng ngay, còn 127.0.0.1 khiến
 * máy khách tự gọi về chính nó và ngồi chờ hết thời gian nếu không có gì lắng nghe —
 * khác biệt thấy rõ nhất trên điện thoại, nơi ứng dụng treo thay vì báo lỗi ngay.
 */
function PublishSection({ publish }: { publish: PublishSettings }) {
  const update = useUpdatePublishSettings();

  const presets = ['0.0.0.0', '127.0.0.1'];
  const isPreset = presets.includes(publish.sink_address);
  const [mode, setMode] = useState(isPreset ? publish.sink_address : 'custom');
  const [custom, setCustom] = useState(isPreset ? '' : publish.sink_address);

  // Giá trị máy chủ đổi (người khác lưu, hoặc vừa lưu xong) thì đồng bộ lại ô nhập.
  useEffect(() => {
    const preset = presets.includes(publish.sink_address);
    setMode(preset ? publish.sink_address : 'custom');
    setCustom(preset ? '' : publish.sink_address);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [publish.sink_address]);

  const value = mode === 'custom' ? custom.trim() : mode;
  const dirty = value !== publish.sink_address && value !== '';

  return (
    <Card title="Địa chỉ xuất bản">
      <p className="mb-3 max-w-2xl text-sm text-slate-600 dark:text-slate-300">
        Mỗi dòng trong file hosts là một địa chỉ IP rồi tới tên miền. Địa chỉ này quyết định
        chuyện gì xảy ra khi thiết bị truy cập một domain bị chặn.
      </p>

      <div className="grid gap-3 sm:grid-cols-2 xl:max-w-2xl">
        <Field label="Đích chặn" htmlFor="sink-mode">
          <Select id="sink-mode" value={mode} onChange={(e) => setMode(e.target.value)}>
            <option value="0.0.0.0">0.0.0.0 — kết nối hỏng ngay (khuyến nghị)</option>
            <option value="127.0.0.1">127.0.0.1 — quay về chính máy truy vấn</option>
            <option value="custom">Địa chỉ khác…</option>
          </Select>
        </Field>

        {mode === 'custom' && (
          <Field
            label="Địa chỉ IP"
            htmlFor="sink-custom"
            hint="IPv4 hoặc IPv6. Trỏ vào một máy chủ thật nghĩa là toàn bộ lưu lượng bị chặn sẽ đổ về đó."
          >
            <TextInput
              id="sink-custom"
              placeholder="192.168.1.2"
              spellCheck={false}
              value={custom}
              onChange={(e) => setCustom(e.target.value)}
            />
          </Field>
        )}
      </div>

      <div className="mt-3 flex flex-wrap items-center gap-3">
        <Button
          variant="primary"
          disabled={!dirty || update.isPending}
          onClick={() => update.mutate({ sink_address: value })}
        >
          {update.isPending ? 'Đang lưu…' : 'Lưu địa chỉ'}
        </Button>
        <span className="text-xs text-slate-500 dark:text-slate-400">
          Đang dùng <code className="font-mono">{publish.sink_address}</code>. Địa chỉ mới có tác
          dụng từ lần xuất bản kế tiếp — các file đã ghi vẫn giữ địa chỉ cũ cho tới lúc đó.
        </span>
      </div>

      {update.isError && (
        <div className="mt-2">
          <ErrorState error={update.error} />
        </div>
      )}
    </Card>
  );
}

/** Các bảng tra cứu cục bộ: nuôi tín hiệu chấm điểm và đối chiếu địa chỉ. */
function LookupSection({ tables }: { tables: LookupTable[] }) {
  return (
    <Card title="Dữ liệu tra cứu">
      <p className="mb-3 text-sm text-slate-600 dark:text-slate-300">
        Dữ liệu công khai, tải về rồi dùng offline. Chúng{' '}
        <strong className="font-semibold">không liên quan</strong> tới màn Nguồn ngoài: nguồn ngoài
        là danh sách tên miền để đối chiếu, còn đây là bảng tra thông tin về hạ tầng và danh sách
        địa chỉ độc hại.
      </p>

      <div className="grid gap-3 lg:grid-cols-2">
        {tables.map((table) => (
          <LookupCard key={table.kind} table={table} />
        ))}
      </div>
    </Card>
  );
}

function LookupCard({ table }: { table: LookupTable }) {
  const refresh = useRefreshLookup();
  const [jobId, setJobId] = useState<string | null>(null);
  const [url, setUrl] = useState('');
  const [showUrl, setShowUrl] = useState(false);

  const job = useJob(jobId);
  const running = job.data?.state === 'pending' || job.data?.state === 'running';

  return (
    <div className="rounded-md p-3 ring-1 ring-slate-200 dark:ring-slate-800">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h3 className="text-sm font-semibold">{table.label}</h3>
          <p className="mt-0.5 text-xs text-slate-500 dark:text-slate-400">{table.describes}</p>
        </div>
        <span
          className={cx(
            'shrink-0 rounded-full px-2 py-0.5 text-xs font-medium',
            table.loaded
              ? 'bg-emerald-100 text-emerald-800 dark:bg-emerald-950 dark:text-emerald-300'
              : 'bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-300',
          )}
        >
          {table.loaded ? 'Đã nạp' : 'Chưa có'}
        </span>
      </div>

      <dl className="mt-2 grid grid-cols-[6rem_1fr] gap-y-1 text-xs">
        <dt className="text-slate-500 dark:text-slate-400">Số bản ghi</dt>
        <dd className="tabular-nums">{table.loaded ? formatNumber(table.entries) : '—'}</dd>

        <dt className="text-slate-500 dark:text-slate-400">Cập nhật</dt>
        <dd>{table.loaded_at ? formatRelative(table.loaded_at) : '—'}</dd>

        <dt className="text-slate-500 dark:text-slate-400">Đường dẫn</dt>
        <dd className="truncate font-mono" title={table.path}>
          {table.path || '—'}
        </dd>
      </dl>

      {showUrl && (
        <div className="mt-2">
          <TextInput
            aria-label={`URL tải ${table.label}`}
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            placeholder={table.default_url}
            className="font-mono text-xs"
          />
        </div>
      )}

      <div className="mt-2 flex items-center gap-2">
        <Button
          variant="secondary"
          disabled={refresh.isPending || running}
          onClick={() =>
            refresh.mutate(
              { kind: table.kind, url: url || undefined },
              { onSuccess: (data) => setJobId(data.job_id) },
            )
          }
        >
          {running ? 'Đang tải…' : table.loaded ? 'Cập nhật' : 'Tải về'}
        </Button>
        <Button variant="ghost" className="text-xs" onClick={() => setShowUrl((v) => !v)}>
          {showUrl ? 'Dùng URL mặc định' : 'Đổi URL'}
        </Button>
      </div>

      {/* Tải về mất vài phút với đường truyền chậm, nên phải nói rõ trạng thái thay vì
          để nút im lặng. */}
      {job.data?.state === 'failed' && (
        <p className="mt-2 rounded bg-red-50 px-2 py-1 text-xs text-red-700 dark:bg-red-950 dark:text-red-300">
          Tải thất bại: {job.data.error}
        </p>
      )}
      {job.data?.state === 'done' && (
        <p className="mt-2 rounded bg-emerald-50 px-2 py-1 text-xs text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300">
          Đã cập nhật xong. Hệ thống đang chấm điểm lại.
        </p>
      )}
      {refresh.isError && (
        <div className="mt-2">
          <ErrorState error={refresh.error} />
        </div>
      )}
    </div>
  );
}

/** Thông tin chỉ đọc, đặt bằng biến môi trường lúc khởi động. */
function SystemSection({ system }: { system: SystemInfo }) {
  const rows: [string, string][] = [
    ['Phiên bản', system.version],
    ['File CSDL', `${system.db_path} · ${formatBytes(system.db_size_bytes)}`],
    ['Thư mục danh sách', system.lists_dir],
    ['Địa chỉ HTTP', system.listen],
    ['Cổng nhận TZSP', system.tzsp_listen],
    ['Truy vấn ra Internet', system.external_enabled ? 'Bật' : 'Tắt'],
    ['Giữ log thô', `${system.log_retention_days} ngày`],
    ['Giữ tổng hợp theo giờ', `${system.hourly_retention_days} ngày`],
    ['Giữ bản xuất bản', `${system.keep_snapshots} bản mỗi phân loại`],
    ['Ngưỡng chặn xuất bản', `${Math.round(system.publish_min_ratio * 100)}% so với lần trước`],
    [
      'Dải IP tải được danh sách',
      system.lists_allow_cidr.length > 0 ? system.lists_allow_cidr.join(', ') : 'tất cả',
    ],
  ];

  return (
    <Card title="Hệ thống">
      <dl className="grid gap-x-6 gap-y-1.5 text-sm sm:grid-cols-2">
        {rows.map(([label, value]) => (
          <div key={label} className="flex flex-wrap items-baseline justify-between gap-2 border-b border-slate-100 py-1 dark:border-slate-800">
            <dt className="text-slate-500 dark:text-slate-400">{label}</dt>
            <dd className="min-w-0 truncate font-mono text-xs" title={value}>
              {value}
            </dd>
          </div>
        ))}
      </dl>
      <p className="mt-3 text-xs text-slate-500 dark:text-slate-400">
        Những giá trị này đặt bằng biến môi trường <code>DNSGUARD_*</code> và chỉ đổi được khi khởi
        động lại — chúng thuộc về hạ tầng, không phải quyết định vận hành.
      </p>
    </Card>
  );
}
