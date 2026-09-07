import { useEffect, useState } from 'react';

import { useApplyRules, usePreviewRules, useRules } from '@/api/hooks';
import type { CustomRules, RuleRejection, RuleThresholds, WeightImpact } from '@/api/types';
import { Field, Select, TextInput, Textarea } from '@/components/ui/form';
import { Button, Card, ErrorState, Spinner, cx } from '@/components/ui/primitives';
import { formatNumber } from '@/lib/format';

const categoryOptions = [
  { value: 'ads', label: 'Quảng cáo' },
  { value: 'tracking', label: 'Theo dõi' },
  { value: 'telemetry', label: 'Telemetry' },
  { value: 'malware', label: 'Mã độc' },
  { value: 'cryptomining', label: 'Đào tiền ảo' },
  { value: 'adult', label: 'Người lớn' },
];

const thresholdFields: { key: keyof RuleThresholds; label: string; hint: string }[] = [
  {
    key: 'fanout_min_clients',
    label: 'Số thiết bị tối thiểu (fan-out)',
    hint: 'Bao nhiêu máy cùng gọi thì coi là bất thường. Mạng lớn cần con số cao hơn.',
  },
  {
    key: 'beacon_min_queries',
    label: 'Số truy vấn tối thiểu (beacon)',
    hint: 'Cần đủ mẫu thì tính đều đặn mới có nghĩa.',
  },
  {
    key: 'spread_high_min',
    label: 'Số subdomain — mức cao',
    hint: 'Máy chủ quảng cáo sinh hàng trăm subdomain; trang nội dung hiếm khi vượt vài chục.',
  },
  { key: 'spread_mid_min', label: 'Số subdomain — mức trung bình', hint: 'Phải nhỏ hơn mức cao.' },
  {
    key: 'vt_malicious_min_engines',
    label: 'Số engine VirusTotal',
    hint: 'Một engine đơn lẻ báo động là nhiễu nổi tiếng. Ba engine đồng ý mới là bằng chứng.',
  },
  {
    key: 'high_rank_max',
    label: 'Thứ hạng Tranco được bảo vệ',
    hint: 'Trang trong khoảng này nhận điểm âm lớn. Đây là lớp bảo vệ chính chống chặn nhầm.',
  },
];

/** Chuyển một danh sách nhiều dòng thành mảng, bỏ dòng trống. */
function parseLines(text: string): string[] {
  return text
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean);
}

/**
 * Phân tích danh sách "tên_miền phân_loại" mỗi dòng một mục.
 *
 * Nhập theo dòng chứ không phải từng ô một: người vận hành thường có sẵn một danh
 * sách và muốn dán cả vào, chứ không muốn bấm "thêm dòng" ba mươi lần.
 */
function parseDomainMap(text: string, fallback: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of parseLines(text)) {
    const [domain, category] = line.split(/[\s,]+/);
    if (domain) out[domain] = category || fallback;
  }
  return out;
}

function formatDomainMap(map: Record<string, string>): string {
  return Object.entries(map)
    .map(([domain, category]) => `${domain} ${category}`)
    .join('\n');
}

function parseASNMap(text: string): Record<string, { org: string; category: string }> {
  const out: Record<string, { org: string; category: string }> = {};
  for (const line of parseLines(text)) {
    // "131429 ads Tên tổ chức" — số hiệu, phân loại, rồi phần còn lại là tên.
    const [asn, category, ...org] = line.split(/[\s,]+/);
    if (asn && /^\d+$/.test(asn)) {
      out[asn] = { org: org.join(' ') || `AS${asn}`, category: category || 'ads' };
    }
  }
  return out;
}

function formatASNMap(map: Record<string, { org: string; category: string }>): string {
  return Object.entries(map)
    .map(([asn, info]) => `${asn} ${info.category} ${info.org}`)
    .join('\n');
}

function parseKeywords(text: string, category: string): Record<string, string[]> {
  const words = parseLines(text.replace(/,/g, '\n'));
  return words.length > 0 ? { [category]: words } : {};
}

function formatKeywords(map: Record<string, string[]>): { text: string; category: string } {
  const first = Object.entries(map).find(([, words]) => words.length > 0);
  if (!first) return { text: '', category: 'ads' };
  // Giao diện chỉ sửa được một nhóm mỗi lần; nếu đã lưu nhiều nhóm thì hiện nhóm đầu.
  return { text: first[1].join('\n'), category: first[0] };
}

/**
 * Luật phân loại do người vận hành tự đặt.
 *
 * Danh sách dựng sẵn lấy theo hạ tầng adtech quốc tế. Một mạng ở Việt Nam gặp những
 * mạng quảng cáo nội địa mà danh sách đó không biết, và trước đây không có cách nào
 * bổ sung ngoài việc sửa mã nguồn.
 *
 * Chỉ thêm chứ không bớt được: bản nâng cấp bổ sung mục mới vào danh sách dựng sẵn
 * vẫn có tác dụng, và không ai xóa nhầm được lớp bảo vệ dựng sẵn. Danh sách ASN trung
 * tính và danh sách bảo vệ cứng không sửa được ở đây — gỡ Google khỏi danh sách trung
 * tính là chặn nửa Internet.
 */
export function RulesEditor() {
  const rules = useRules();
  const preview = usePreviewRules();
  const apply = useApplyRules();

  const [domains, setDomains] = useState('');
  const [asns, setASNs] = useState('');
  const [cdn, setCDN] = useState('');
  const [keywords, setKeywords] = useState('');
  const [keywordCategory, setKeywordCategory] = useState('ads');
  const [thresholds, setThresholds] = useState<Record<string, string>>({});

  const [dirty, setDirty] = useState(false);
  const [impact, setImpact] = useState<WeightImpact | null>(null);
  const [saved, setSaved] = useState(false);

  // Nạp lại từ máy chủ khi dữ liệu về, nhưng không đè lên thứ người dùng đang gõ.
  useEffect(() => {
    if (!rules.data || dirty) return;
    const custom = rules.data.custom;
    const kw = formatKeywords(custom.keywords);

    setDomains(formatDomainMap(custom.adtech_domains));
    setASNs(formatASNMap(custom.adtech_asns));
    setCDN(custom.shared_cdn.join('\n'));
    setKeywords(kw.text);
    setKeywordCategory(kw.category);
    setThresholds({});
  }, [rules.data, dirty]);

  /** Mọi thay đổi làm kết quả xem trước cũ hết giá trị. */
  function touch() {
    setDirty(true);
    setImpact(null);
    setSaved(false);
  }

  function build(): Partial<CustomRules> {
    const base = rules.data?.custom.thresholds;
    const merged: Record<string, number> = {};
    for (const field of thresholdFields) {
      const raw = thresholds[field.key];
      const value = raw !== undefined ? Number(raw) : base?.[field.key];
      if (Number.isFinite(value) && value !== undefined) merged[field.key] = Number(value);
    }

    return {
      adtech_domains: parseDomainMap(domains, 'ads'),
      adtech_asns: parseASNMap(asns),
      shared_cdn: parseLines(cdn),
      keywords: parseKeywords(keywords, keywordCategory),
      thresholds: merged as unknown as RuleThresholds,
    };
  }

  function reset() {
    setDirty(false);
    setImpact(null);
    setSaved(false);
    setThresholds({});
    rules.refetch();
  }

  if (rules.isPending) return <Card title="Luật phân loại"><Spinner /></Card>;
  if (rules.isError)
    return (
      <Card title="Luật phân loại">
        <ErrorState error={rules.error} />
      </Card>
    );

  const counts = rules.data.builtin_counts;
  const rejections = rejectionsOf(preview.error) ?? rejectionsOf(apply.error);

  return (
    <Card
      title="Luật phân loại"
      actions={
        <div className="flex items-center gap-2">
          {dirty && (
            <Button variant="ghost" onClick={reset}>
              Hủy thay đổi
            </Button>
          )}
          <Button
            variant="secondary"
            disabled={!dirty || preview.isPending}
            onClick={() => preview.mutate(build(), { onSuccess: (r) => setImpact(r.impact) })}
          >
            {preview.isPending ? 'Đang tính…' : 'Xem tác động'}
          </Button>
          <Button
            variant="primary"
            // Cùng cổng chặn như đổi trọng số, và ở đây cần hơn: một tên miền thêm
            // nhầm vào danh sách adtech mang +6,0 còn ngưỡng ads là 5,5, tức tự nó
            // đủ để chặn một domain mà không cần bằng chứng nào khác.
            disabled={!dirty || impact === null || apply.isPending}
            onClick={() =>
              apply.mutate(build(), {
                onSuccess: () => {
                  setDirty(false);
                  setImpact(null);
                  setSaved(true);
                },
              })
            }
          >
            Áp dụng
          </Button>
        </div>
      }
    >
      <p className="mb-3 max-w-3xl text-sm text-slate-600 dark:text-slate-300">
        Những gì nhập ở đây <strong className="font-semibold">cộng thêm</strong> vào danh sách
        dựng sẵn chứ không thay thế, nên bản cập nhật sau vẫn bổ sung được mục mới. Danh sách
        bảo vệ cứng và {counts.neutral_asns} ASN trung tính không sửa được tại đây.
      </p>

      <div className="grid gap-4 lg:grid-cols-2">
        <Field
          label={`Tên miền adtech — thêm vào ${counts.adtech_domains} mục sẵn có`}
          htmlFor="rule-domains"
          hint="Mỗi dòng: tên miền, khoảng trắng, phân loại. Khớp cả subdomain. Ví dụ: quangcaoabc.vn ads"
        >
          <Textarea
            id="rule-domains"
            rows={6}
            spellCheck={false}
            className="font-mono text-xs"
            placeholder={'quangcaoabc.vn ads\ntheodoixyz.vn tracking'}
            value={domains}
            onChange={(e) => {
              setDomains(e.target.value);
              touch();
            }}
          />
        </Field>

        <Field
          label={`ASN adtech — thêm vào ${counts.adtech_asns} mục sẵn có`}
          htmlFor="rule-asns"
          hint="Mỗi dòng: số hiệu, phân loại, tên tổ chức. ASN trung tính sẽ bị từ chối."
        >
          <Textarea
            id="rule-asns"
            rows={6}
            spellCheck={false}
            className="font-mono text-xs"
            placeholder={'131429 ads Mang quang cao noi dia'}
            value={asns}
            onChange={(e) => {
              setASNs(e.target.value);
              touch();
            }}
          />
        </Field>

        <Field
          label={`CDN dùng chung — thêm vào ${counts.shared_cdn} mục sẵn có`}
          htmlFor="rule-cdn"
          hint="Đây là danh sách bảo vệ: domain phân giải về đây nhận điểm âm lớn. Chỉ thêm được, không bớt."
        >
          <Textarea
            id="rule-cdn"
            rows={4}
            spellCheck={false}
            className="font-mono text-xs"
            placeholder="cdn-noi-bo.vn"
            value={cdn}
            onChange={(e) => {
              setCDN(e.target.value);
              touch();
            }}
          />
        </Field>

        <div className="space-y-2">
          <Field
            label="Phân loại cho từ khóa"
            htmlFor="rule-kw-cat"
            hint="Từ khóa một mình không đủ để gán nhãn chặn — luôn cần thêm bằng chứng hạ tầng hoặc hành vi."
          >
            <Select
              id="rule-kw-cat"
              value={keywordCategory}
              onChange={(e) => {
                setKeywordCategory(e.target.value);
                touch();
              }}
            >
              {categoryOptions.map((c) => (
                <option key={c.value} value={c.value}>
                  {c.label}
                </option>
              ))}
            </Select>
          </Field>

          <Field
            label={`Từ khóa — thêm vào ${counts.keywords} mục sẵn có`}
            htmlFor="rule-keywords"
            hint="Khớp theo chuỗi con trong tên miền, tối thiểu 3 ký tự. Mỗi dòng một từ."
          >
            <Textarea
              id="rule-keywords"
              rows={4}
              spellCheck={false}
              className="font-mono text-xs"
              placeholder={'quangcao\ntheodoi'}
              value={keywords}
              onChange={(e) => {
                setKeywords(e.target.value);
                touch();
              }}
            />
          </Field>
        </div>
      </div>

      <h3 className="mt-5 text-sm font-semibold">Ngưỡng tín hiệu</h3>
      <p className="mb-2 max-w-3xl text-xs text-slate-500 dark:text-slate-400">
        Trọng số trả lời "bằng chứng này nặng bao nhiêu"; ngưỡng định nghĩa bằng chứng là gì.
        Chỉ những ngưỡng phụ thuộc quy mô mạng mới mở ra ở đây.
      </p>

      <div className="grid gap-x-6 gap-y-3 sm:grid-cols-2 xl:grid-cols-3">
        {thresholdFields.map((field) => {
          const current = rules.data.custom.thresholds[field.key];
          const value = thresholds[field.key] ?? String(current);
          const def = rules.data.default_thresholds[field.key];

          return (
            <Field key={field.key} label={field.label} htmlFor={`th-${field.key}`} hint={field.hint}>
              <div className="flex items-center gap-2">
                <TextInput
                  id={`th-${field.key}`}
                  type="number"
                  value={value}
                  onChange={(e) => {
                    setThresholds((prev) => ({ ...prev, [field.key]: e.target.value }));
                    touch();
                  }}
                  className={cx(
                    'tabular-nums',
                    thresholds[field.key] !== undefined && 'ring-2 ring-sky-500',
                  )}
                />
                {Number(value) !== def && (
                  <button
                    type="button"
                    title={`Trả về mặc định ${def}`}
                    aria-label={`Trả ${field.label} về mặc định ${def}`}
                    className="shrink-0 text-xs text-slate-400 hover:text-slate-600 dark:hover:text-slate-200"
                    onClick={() => {
                      setThresholds((prev) => ({ ...prev, [field.key]: String(def) }));
                      touch();
                    }}
                  >
                    ↺
                  </button>
                )}
              </div>
            </Field>
          );
        })}
      </div>

      {rejections && <RejectionList rejections={rejections} />}

      {!rejections && preview.isError && (
        <div className="mt-4">
          <ErrorState error={preview.error} />
        </div>
      )}
      {!rejections && apply.isError && (
        <div className="mt-4">
          <ErrorState error={apply.error} />
        </div>
      )}

      {saved && (
        <p className="mt-4 rounded-md bg-emerald-50 px-3 py-2 text-sm text-emerald-800 dark:bg-emerald-950 dark:text-emerald-300">
          Đã lưu luật và xếp hàng chấm điểm lại toàn bộ. Kết quả xuất hiện dần khi job chạy xong.
        </p>
      )}

      {impact && <RuleImpact impact={impact} />}
    </Card>
  );
}

/**
 * Danh sách mục bị từ chối.
 *
 * Hiện đủ cả lý do từng mục thay vì một câu "dữ liệu không hợp lệ": người dùng dán
 * vào ba mươi tên miền thì cần biết cái nào sai và sai chỗ nào.
 */
function RejectionList({ rejections }: { rejections: RuleRejection[] }) {
  return (
    <div className="mt-4 rounded-md bg-rose-50 p-3 dark:bg-rose-950/40">
      <h3 className="text-sm font-semibold text-rose-800 dark:text-rose-300">
        {rejections.length} mục bị từ chối
      </h3>
      <ul className="mt-2 space-y-1 text-xs">
        {rejections.map((r, i) => (
          <li key={`${r.field}-${r.value}-${i}`} className="text-rose-800 dark:text-rose-300">
            <code className="font-mono">{r.value}</code> — {r.reason}
          </li>
        ))}
      </ul>
    </div>
  );
}

/** Bảng tác động — con số khớp chính xác với kết quả sau khi áp dụng. */
function RuleImpact({ impact }: { impact: WeightImpact }) {
  return (
    <div className="mt-4 rounded-md bg-slate-50 p-3 dark:bg-slate-950">
      <h3 className="text-sm font-semibold">Tác động nếu áp dụng</h3>
      <div className="mt-2 grid gap-3 sm:grid-cols-3">
        <ImpactCount
          label="Sẽ chặn thêm"
          count={impact.would_block.count}
          sample={impact.would_block.sample}
          tone="danger"
        />
        <ImpactCount
          label="Sẽ thôi chặn"
          count={impact.would_unblock.count}
          sample={impact.would_unblock.sample}
          tone="safe"
        />
        <div>
          <p className="text-xs text-slate-500 dark:text-slate-400">Không đổi</p>
          <p className="text-xl font-semibold tabular-nums">{formatNumber(impact.unchanged)}</p>
          {impact.evaluated !== undefined && (
            <p className="mt-1 text-xs text-slate-500 dark:text-slate-400">
              trên {formatNumber(impact.evaluated)} domain đã xét
            </p>
          )}
        </div>
      </div>
    </div>
  );
}

function ImpactCount({
  label,
  count,
  sample,
  tone,
}: {
  label: string;
  count: number;
  sample: string[];
  tone: 'danger' | 'safe';
}) {
  return (
    <div>
      <p className="text-xs text-slate-500 dark:text-slate-400">{label}</p>
      <p
        className={cx(
          'text-xl font-semibold tabular-nums',
          count > 0 && tone === 'danger' && 'text-rose-600 dark:text-rose-400',
          count > 0 && tone === 'safe' && 'text-emerald-600 dark:text-emerald-400',
        )}
      >
        {formatNumber(count)}
      </p>
      {sample.length > 0 && (
        <ul className="mt-1 space-y-0.5 text-xs text-slate-500 dark:text-slate-400">
          {sample.slice(0, 8).map((name) => (
            <li key={name} className="truncate font-mono">
              {name}
            </li>
          ))}
          {count > sample.length && <li>… và {formatNumber(count - sample.length)} nữa</li>}
        </ul>
      )}
    </div>
  );
}

/** Lấy danh sách mục bị từ chối từ lỗi API, nếu có. */
function rejectionsOf(error: unknown): RuleRejection[] | null {
  if (!error || typeof error !== 'object') return null;
  const details = (error as { details?: { rejected?: RuleRejection[] } }).details;
  return details?.rejected?.length ? details.rejected : null;
}
