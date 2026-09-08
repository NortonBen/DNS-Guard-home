import { useState } from 'react';
import { Link } from '@tanstack/react-router';

import { useIPInvestigation, useThreats } from '@/api/hooks';
import { Button, Card, EmptyState, ErrorState, Spinner, StatusBadge } from '@/components/ui/primitives';
import { threatSourceLabel } from '@/lib/threat-source';

/**
 * Màn điều tra theo địa chỉ IP.
 *
 * Gộp ba việc vốn luôn đi cùng nhau trong một vụ điều tra: xem cảnh báo, tra ngược
 * một địa chỉ, và xuất hồ sơ cho khoảng thời gian liên quan. Tách chúng ra ba trang
 * sẽ bắt người vận hành chép địa chỉ qua lại giữa các màn.
 */
export function IPForensicsScreen() {
  const [addr, setAddr] = useState('');
  const [submitted, setSubmitted] = useState('');

  const threats = useThreats();
  const investigation = useIPInvestigation(submitted);

  return (
    <div className="mx-auto max-w-5xl space-y-3">
      <ThreatPanel
        onInvestigate={(ip) => {
          setAddr(ip);
          setSubmitted(ip);
        }}
        query={threats}
      />

      <Card title="Tra ngược một địa chỉ">
        <form
          onSubmit={(e) => {
            e.preventDefault();
            setSubmitted(addr.trim());
          }}
          className="flex gap-2"
        >
          <input
            value={addr}
            onChange={(e) => setAddr(e.target.value)}
            placeholder="104.21.5.6"
            aria-label="Địa chỉ IP cần điều tra"
            className="flex-1 rounded-md px-3 py-2 font-mono text-sm ring-1 ring-slate-300 dark:bg-slate-800 dark:ring-slate-700"
          />
          <Button type="submit" variant="primary">
            Điều tra
          </Button>
        </form>

        {investigation.isError && (
          <div className="mt-3">
            <ErrorState error={investigation.error} />
          </div>
        )}
        {investigation.isPending && submitted !== '' && (
          <div className="mt-3">
            <Spinner />
          </div>
        )}
        {investigation.data && <InvestigationResult data={investigation.data} />}
      </Card>

      <ExportPanel />
    </div>
  );
}

function ThreatPanel({
  query,
  onInvestigate,
}: {
  query: ReturnType<typeof useThreats>;
  onInvestigate: (ip: string) => void;
}) {
  if (query.isPending) {
    return (
      <Card title="Cảnh báo hạ tầng độc hại">
        <Spinner />
      </Card>
    );
  }
  if (query.isError) {
    return (
      <Card title="Cảnh báo hạ tầng độc hại">
        <ErrorState error={query.error} />
      </Card>
    );
  }

  const { matches, list_loaded: listLoaded } = query.data!;

  /*
   * Danh sách rỗng có hai nguyên nhân trái ngược nhau — không có gì đáng báo, hoặc
   * chưa tải danh sách nên không thể báo. Hiện cùng một dòng "không có cảnh báo" cho
   * cả hai sẽ khiến người vận hành yên tâm nhầm.
   */
  if (!listLoaded) {
    return (
      <Card title="Cảnh báo hạ tầng độc hại">
        <p className="text-sm text-amber-700 dark:text-amber-400">
          Chưa tải danh sách hạ tầng độc hại, nên chưa đối chiếu được địa chỉ nào. Tải về ở{' '}
          <Link to="/settings" className="underline">
            Cài đặt
          </Link>
          .
        </p>
      </Card>
    );
  }

  return (
    <Card title={`Cảnh báo hạ tầng độc hại (${matches.length})`}>
      {matches.length === 0 ? (
        <EmptyState>Không có domain nào phân giải tới địa chỉ trong danh sách.</EmptyState>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="text-left text-xs uppercase text-slate-500">
              <tr>
                <th className="py-1 pr-3">Domain</th>
                <th className="py-1 pr-3">Địa chỉ</th>
                <th className="py-1 pr-3">Dải khớp</th>
                <th className="py-1 pr-3">Vị trí</th>
                <th className="py-1 pr-3">Nhịp đều</th>
                <th className="py-1">Thấy gần nhất</th>
              </tr>
            </thead>
            <tbody>
              {matches.map((m) => (
                <tr key={`${m.domain_id}-${m.ip}`} className="border-t border-slate-200 dark:border-slate-700">
                  <td className="py-1.5 pr-3">
                    <Link to="/domains/$domainId" params={{ domainId: String(m.domain_id) }} className="underline">
                      {m.name}
                    </Link>{' '}
                    <StatusBadge status={m.status} />
                  </td>
                  <td className="py-1.5 pr-3">
                    <button type="button" onClick={() => onInvestigate(m.ip)} className="font-mono underline">
                      {m.ip}
                    </button>
                  </td>
                  <td className="py-1.5 pr-3 text-xs">
                    <span className="font-mono">{m.threat}</span>
                    {m.source && (
                      <span className="ml-2 text-slate-500 dark:text-slate-400">
                        {threatSourceLabel(m.source)}
                      </span>
                    )}
                  </td>
                  <td className="py-1.5 pr-3 text-xs">
                    {[m.country, m.org].filter(Boolean).join(' · ') || '—'}
                  </td>
                  <td className="py-1.5 pr-3">
                    {m.beacon ? (
                      <span className="rounded bg-amber-100 px-1.5 py-0.5 text-xs text-amber-800 dark:bg-amber-900 dark:text-amber-200">
                        có
                      </span>
                    ) : (
                      <span className="text-xs text-slate-500">—</span>
                    )}
                  </td>
                  <td className="py-1.5 text-xs text-slate-500">{m.last_seen}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <p className="mt-3 text-xs text-slate-500">
        Địa chỉ dùng chung có thể phục vụ cả thứ hợp pháp, nên danh sách theo IP dễ báo nhầm hơn danh
        sách theo domain. Hệ thống không tự chặn gì ở đây — quyết định vẫn là của bạn.
      </p>
    </Card>
  );
}

function InvestigationResult({ data }: { data: ReturnType<typeof useIPInvestigation>['data'] & {} }) {
  if (data.domains.length === 0 && data.accesses.length === 0) {
    return (
      <div className="mt-3">
        <EmptyState>Chưa thấy domain nào trong mạng phân giải tới {data.ip}.</EmptyState>
      </div>
    );
  }

  return (
    <div className="mt-4 space-y-4">
      <section>
        <h3 className="mb-1 text-xs font-semibold uppercase text-slate-500">
          Domain đã trỏ tới địa chỉ này
        </h3>
        <ul className="space-y-1 text-sm">
          {data.domains.map((d) => (
            <li key={d.domain_id}>
              <Link to="/domains/$domainId" params={{ domainId: String(d.domain_id) }} className="underline">
                {d.name}
              </Link>{' '}
              <StatusBadge status={d.status} />
              <span className="ml-2 text-xs text-slate-500">
                {d.hits} lần · {d.first_seen} → {d.last_seen}
              </span>
            </li>
          ))}
        </ul>
      </section>

      <section>
        <h3 className="mb-1 text-xs font-semibold uppercase text-slate-500">
          Thiết bị đã phân giải, trong khoảng ánh xạ còn hiệu lực
        </h3>
        {data.accesses.length === 0 ? (
          <EmptyState>Không có lượt truy vấn nào rơi vào khoảng đó.</EmptyState>
        ) : (
          <div className="max-h-72 overflow-y-auto">
            <table className="w-full text-sm">
              <tbody>
                {data.accesses.map((a, i) => (
                  <tr key={i} className="border-t border-slate-200 dark:border-slate-700">
                    <td className="py-1 pr-3 font-mono text-xs">{a.client}</td>
                    <td className="py-1 pr-3">{a.domain}</td>
                    <td className="py-1 text-xs text-slate-500">{a.occurred_at}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      {/*
        Giới hạn đặt ngay dưới kết quả chứ không giấu trong tài liệu: đây là chỗ người
        ta dễ kết luận quá tay nhất.
      */}
      <p className="text-xs text-slate-500">
        Bảng trên cho biết thiết bị nào đã <em>phân giải</em> một tên trỏ tới địa chỉ này. Nó không
        chứng minh có kết nối thật tới đó — luồng mirror chỉ mang DNS. Thiết bị dùng DoH/DoT không
        xuất hiện ở đây.
      </p>
    </div>
  );
}

/** Xuất hồ sơ điều tra cho một khoảng thời gian. */
function ExportPanel() {
  const [from, setFrom] = useState(() => isoDaysAgo(7));
  const [to, setTo] = useState(() => isoDaysAgo(0));

  const href = `/api/v1/forensics/export?from=${encodeURIComponent(
    `${from}T00:00:00Z`,
  )}&to=${encodeURIComponent(`${to}T23:59:59Z`)}`;

  return (
    <Card title="Xuất hồ sơ điều tra">
      <div className="flex flex-wrap items-end gap-3">
        <label className="text-sm">
          <span className="mb-1 block text-xs text-slate-500">Từ ngày</span>
          <input
            type="date"
            value={from}
            onChange={(e) => setFrom(e.target.value)}
            className="rounded-md px-2 py-1.5 text-sm ring-1 ring-slate-300 dark:bg-slate-800 dark:ring-slate-700"
          />
        </label>
        <label className="text-sm">
          <span className="mb-1 block text-xs text-slate-500">Đến ngày</span>
          <input
            type="date"
            value={to}
            onChange={(e) => setTo(e.target.value)}
            className="rounded-md px-2 py-1.5 text-sm ring-1 ring-slate-300 dark:bg-slate-800 dark:ring-slate-700"
          />
        </label>
        {/*
          Dùng thẻ liên kết chứ không fetch rồi tạo blob: hồ sơ được sinh theo luồng và
          có thể lên tới hàng trăm megabyte, mà dựng blob sẽ giữ toàn bộ trong bộ nhớ
          trình duyệt trước khi lưu được dòng nào.
        */}
        <a
          href={href}
          download
          className="rounded-md bg-slate-900 px-3 py-1.5 text-sm text-white hover:bg-slate-700 dark:bg-slate-100 dark:text-slate-900 dark:hover:bg-white"
        >
          Tải hồ sơ .zip
        </a>
      </div>

      <p className="mt-3 text-xs text-slate-500">
        Hồ sơ gồm log truy vấn, ánh xạ domain → địa chỉ, và một manifest ghi sha256 của từng file
        cùng những gì dữ liệu này <em>không</em> chứng minh. Khoảng tối đa 400 ngày.
      </p>
    </Card>
  );
}

/** isoDaysAgo trả về ngày cách hôm nay n ngày, dạng YYYY-MM-DD. */
function isoDaysAgo(n: number): string {
  const d = new Date();
  d.setDate(d.getDate() - n);
  return d.toISOString().slice(0, 10);
}
