import { Suspense, lazy, useState } from 'react';
import { Link, useNavigate, useParams } from '@tanstack/react-router';

import {
  useBulkDecision,
  useCategories,
  useDecision,
  useDomain,
  useDomainGraph,
  useSession,
} from '@/api/hooks';
import { AIVerdictCard } from '@/components/domain/ai-verdict';
import { CnameChain } from '@/components/domain/cname-chain';
import { DecisionHistory } from '@/components/domain/decision-history';
import { QuickOpenDomain } from '@/components/domain/quick-open';
import { SignalBadges } from '@/components/domain/signal-badges';
import {
  Button,
  Card,
  EmptyState,
  ErrorState,
  Skeleton,
  Spinner,
  StatusBadge,
} from '@/components/ui/primitives';
import { formatDateTime, formatNumber, formatRelative, formatScore } from '@/lib/format';
import type { DomainIP } from '@/api/types';

// Đồ thị chỉ nạp khi mở trang chi tiết, không nằm trong bundle chung.
const RelationGraphView = lazy(() =>
  import('@/components/domain/relation-graph').then((m) => ({ default: m.RelationGraphView })),
);
const EdgeKindFilter = lazy(() =>
  import('@/components/domain/relation-graph').then((m) => ({ default: m.EdgeKindFilter })),
);

const allKinds = ['cname_to', 'same_asn', 'same_cert', 'co_occurs'];

/**
 * Trang điều tra một domain — màn hình quan trọng nhất về mặt sản phẩm.
 *
 * Gom mọi thứ cần để ra quyết định vào một màn hình: nó thuộc về ai, liên quan tới
 * cái gì, đã có ai quyết định gì. Tải theo lớp: thông tin cơ bản hiện ngay, đồ thị
 * và timeline tải sau — không để cả trang chờ phần chậm nhất.
 */
export function DomainDetailScreen() {
  const { domainId } = useParams({ from: '/domains/$domainId' });
  const id = Number(domainId);
  const navigate = useNavigate();

  const session = useSession();
  const isAdmin = session.data?.user.role === 'admin';

  const detail = useDomain(id);
  const categories = useCategories();
  const decide = useDecision();
  const bulk = useBulkDecision();

  const [kinds, setKinds] = useState(allKinds);
  const [showGraph, setShowGraph] = useState(true);
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const graph = useDomainGraph(id, kinds, showGraph);

  if (detail.isPending) return <Spinner label="Đang tải chi tiết domain" />;
  if (detail.isError) return <ErrorState error={detail.error} />;
  if (!detail.data) return <EmptyState>Không tìm thấy domain</EmptyState>;

  const { domain, facts, siblings, history, public_lists: publicLists, ips } = detail.data;

  function act(action: 'block' | 'allow' | 'ignore') {
    const reason = window.prompt(
      `Lý do ${action === 'block' ? 'chặn' : action === 'allow' ? 'cho qua' : 'bỏ qua'} ${domain.name}?`,
      '',
    );
    // Lý do là bắt buộc: nó đi vào nhật ký và là thứ duy nhất giải thích được quyết
    // định này sáu tháng sau.
    if (reason === null) return;
    decide.mutate({ id, action, reason, category: domain.category?.key ?? 'ads' });
  }

  function blockCluster() {
    if (selected.size === 0) return;
    const reason = window.prompt(
      `Chặn ${selected.size} domain trong cụm. Lý do?`,
      `Cụm liên quan tới ${domain.name}`,
    );
    if (reason === null) return;

    bulk.mutate(
      {
        domain_ids: [...selected],
        action: 'block',
        reason,
        category: domain.category?.key ?? 'ads',
      },
      {
        onSuccess: (result) => {
          setSelected(new Set());
          if (result.skipped.length > 0) {
            window.alert(
              `Đã chặn ${result.applied} domain. Bỏ qua ${result.skipped.length}:\n` +
                result.skipped.map((s) => `· ${s.message}`).join('\n'),
            );
          }
        },
      },
    );
  }

  return (
    <div className="space-y-3">
      <Card>
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            <h1 className="domain-name text-lg font-semibold">{domain.name}</h1>
            <div className="mt-1 flex flex-wrap items-center gap-2 text-sm">
              <StatusBadge status={domain.status} />
              {domain.category && (
                <span className="text-slate-600 dark:text-slate-300">
                  {domain.category.label_vi}
                </span>
              )}
              <span className="font-semibold tabular-nums">điểm {formatScore(domain.score)}</span>
              {domain.confidence !== null && (
                <span className="text-slate-500 dark:text-slate-400">
                  độ tin cậy {Math.round(domain.confidence * 100)}%
                </span>
              )}
              {domain.is_manual && (
                <span
                  title="Job chấm điểm tự động bỏ qua domain này"
                  className="rounded bg-sky-50 px-1.5 py-0.5 text-xs text-sky-700 dark:bg-sky-950 dark:text-sky-300"
                >
                  quyết định thủ công
                </span>
              )}
            </div>

            {/* Câu hỏi "chặn rồi thì nó nằm ở danh sách nào" không có chỗ nào trả lời,
                và khi câu trả lời là "không ở đâu cả" thì càng phải nói ra. */}
            {domain.status === 'blocked' && (
              <p className="mt-1.5 text-xs">
                {detail.data.published_files.length > 0 ? (
                  <span className="text-slate-500 dark:text-slate-400">
                    Nằm trong:{' '}
                    {detail.data.published_files.map((file, i) => (
                      <span key={file}>
                        {i > 0 && ', '}
                        <code className="font-mono">{file}</code>
                      </span>
                    ))}
                  </span>
                ) : (
                  <span className="rounded bg-amber-50 px-1.5 py-0.5 text-amber-800 dark:bg-amber-950 dark:text-amber-300">
                    Đã chặn nhưng chưa nằm trong file nào — router sẽ không thấy domain này.
                    Xuất bản lại để đưa nó vào <code className="font-mono">blocked.txt</code>.
                  </span>
                )}
              </p>
            )}
          </div>

          <div className="flex flex-col items-end gap-2">
            <QuickOpenDomain className="w-64" />

          {isAdmin && (
            <div className="flex flex-wrap items-center gap-1.5">
              <Button variant="danger" onClick={() => act('block')} disabled={detail.data.protected}>
                Chặn
              </Button>
              <Button variant="secondary" onClick={() => act('allow')}>
                Cho qua
              </Button>
              <Button variant="ghost" onClick={() => act('ignore')}>
                Bỏ qua
              </Button>
              {selected.size > 0 && (
                <Button variant="danger" onClick={blockCluster}>
                  Chặn cả cụm ({selected.size})
                </Button>
              )}
            </div>
          )}
          </div>
        </div>

        {/* Danh sách bảo vệ thắng cả quyết định của quản trị; phải nói rõ tại sao nút
            chặn bị vô hiệu thay vì để người dùng đoán. */}
        {detail.data.protected && (
          <p
            role="status"
            className="mt-3 rounded-md bg-emerald-50 px-3 py-2 text-sm text-emerald-800 dark:bg-emerald-950 dark:text-emerald-300"
          >
            Domain này nằm trong danh sách bảo vệ (luật <code className="font-mono">{detail.data.protect_rule}</code>)
            và không thể chặn, kể cả bằng quyền quản trị.
          </p>
        )}
      </Card>

      <AIVerdictCard domainId={id} domainName={domain.name} isAdmin={isAdmin} />

      <div className="grid gap-3 lg:grid-cols-2">
        <Card title={`Bằng chứng · điểm ${formatScore(domain.score)}`}>
          <SignalBadges signals={domain.signals} />
        </Card>

        <Card title="Hạ tầng">
          <dl className="grid grid-cols-[7rem_1fr] gap-y-1.5 text-sm">
            <dt className="text-slate-500 dark:text-slate-400">IP</dt>
            <dd className="domain-name">{facts.dns?.a?.join(', ') || '—'}</dd>

            <dt className="text-slate-500 dark:text-slate-400">ASN</dt>
            <dd>
              {facts.asn?.asn
                ? `AS${facts.asn.asn} ${facts.asn.org ?? ''} ${facts.asn.country ? `(${facts.asn.country})` : ''}`
                : '—'}
            </dd>

            <dt className="text-slate-500 dark:text-slate-400">Tuổi domain</dt>
            <dd>
              {facts.rdap?.age_days
                ? `${formatNumber(facts.rdap.age_days)} ngày · đăng ký ${facts.rdap.registered_at?.slice(0, 10) ?? ''}`
                : '—'}
            </dd>

            <dt className="text-slate-500 dark:text-slate-400">Tranco</dt>
            <dd>{facts.rank?.tranco ? `#${formatNumber(facts.rank.tranco)}` : 'không xếp hạng'}</dd>

            <dt className="text-slate-500 dark:text-slate-400">Chứng chỉ</dt>
            <dd className="truncate" title={facts.cert?.sans?.join(', ')}>
              {facts.cert?.issuer ?? '—'}
              {facts.cert?.sans && facts.cert.sans.length > 0 && (
                <span className="ml-1 text-slate-500 dark:text-slate-400">
                  · {facts.cert.sans.length} SAN
                </span>
              )}
            </dd>

            <dt className="text-slate-500 dark:text-slate-400">Lưu lượng</dt>
            <dd>
              {formatNumber(domain.query_count)} truy vấn · {domain.client_count} client
            </dd>

            <dt className="text-slate-500 dark:text-slate-400">Thấy lần đầu</dt>
            <dd title={formatDateTime(domain.first_seen)}>{formatRelative(domain.first_seen)}</dd>

            <dt className="text-slate-500 dark:text-slate-400">Thấy lần cuối</dt>
            <dd title={formatDateTime(domain.last_seen)}>{formatRelative(domain.last_seen)}</dd>
          </dl>

          {publicLists.length > 0 && (
            <div className="mt-3 border-t border-slate-100 pt-2 dark:border-slate-800">
              <p className="text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400">
                Có trong danh sách công khai
              </p>
              <p className="mt-1 text-sm">{publicLists.map((l) => l.name).join(', ')}</p>
            </div>
          )}
        </Card>
      </div>

      <Card title={`Địa chỉ quan sát trên dây (${ips.length})`}>
        <ObservedIPs ips={ips} />
      </Card>

      <Card title="Chuỗi CNAME">
        <CnameChain domain={domain.name} chain={facts.dns?.cname_chain} />
      </Card>

      <Card
        title="Đồ thị domain liên quan"
        actions={
          <div className="flex items-center gap-2">
            {showGraph && (
              <Suspense fallback={null}>
                <EdgeKindFilter kinds={kinds} onChange={setKinds} />
              </Suspense>
            )}
            <Button variant="ghost" onClick={() => setShowGraph((v) => !v)}>
              {showGraph ? 'Ẩn đồ thị' : 'Hiện đồ thị'}
            </Button>
          </div>
        }
      >
        {!showGraph ? (
          <EmptyState>Đồ thị đang ẩn</EmptyState>
        ) : graph.isPending ? (
          <Skeleton className="h-64 w-full" />
        ) : graph.isError ? (
          <ErrorState error={graph.error} />
        ) : !graph.data || graph.data.edges.length === 0 ? (
          <EmptyState>Chưa tìm thấy domain nào liên quan</EmptyState>
        ) : (
          <>
            {/* Cắt bớt phải nói ra chứ không im lặng: người dùng cần biết mình đang
                nhìn một phần hay toàn bộ. */}
            {graph.data.truncated && (
              <p className="mb-2 rounded bg-amber-50 px-2 py-1 text-xs text-amber-800 dark:bg-amber-950 dark:text-amber-300">
                Hiển thị {graph.data.nodes.length} nút đầu tiên — còn nhiều domain liên quan chưa hiện
              </p>
            )}
            <Suspense fallback={<Skeleton className="h-64 w-full" />}>
              <RelationGraphView
                graph={graph.data}
                selected={selected}
                onToggleSelect={(nodeId, additive) =>
                  setSelected((prev) => {
                    const next = additive ? new Set(prev) : new Set<number>();
                    if (prev.has(nodeId) && additive) next.delete(nodeId);
                    else next.add(nodeId);
                    return next;
                  })
                }
                onOpen={(nodeId) =>
                  navigate({ to: '/domains/$domainId', params: { domainId: String(nodeId) } })
                }
              />
            </Suspense>
          </>
        )}
      </Card>

      <div className="grid gap-3 lg:grid-cols-2">
        <Card title={`Subdomain cùng gốc (${siblings.length})`}>
          {siblings.length === 0 ? (
            <EmptyState>Không có subdomain nào khác dưới {domain.etld1}</EmptyState>
          ) : (
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-slate-200 text-left text-xs uppercase tracking-wide text-slate-500 dark:border-slate-800 dark:text-slate-400">
                  <th scope="col" className="py-1">Tên miền</th>
                  <th scope="col" className="py-1">Trạng thái</th>
                  <th scope="col" className="py-1 text-right">Truy vấn</th>
                </tr>
              </thead>
              <tbody>
                {siblings.map((sibling) => (
                  <tr key={sibling.id} className="border-b border-slate-100 dark:border-slate-800">
                    <td className="py-1">
                      <Link
                        to="/domains/$domainId"
                        params={{ domainId: String(sibling.id) }}
                        className="domain-name text-sky-700 hover:underline dark:text-sky-400"
                      >
                        {sibling.name}
                      </Link>
                    </td>
                    <td className="py-1">
                      <StatusBadge status={sibling.status} />
                    </td>
                    <td className="py-1 text-right tabular-nums">
                      {formatNumber(sibling.query_count)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Card>

        <Card title="Lịch sử quyết định">
          <DecisionHistory history={history} />
        </Card>
      </div>

      <p className="text-xs text-slate-500 dark:text-slate-400">
        Phân loại hiện có: {categories.data?.items.map((c) => c.label_vi).join(', ') ?? '—'}
      </p>
    </div>
  );
}

/**
 * Địa chỉ mà domain này đã trỏ tới, lấy từ bản ghi trả lời DNS bắt được trên dây.
 *
 * Khác thẻ "Hạ tầng" phía trên: chỗ đó là kết quả DNSGuard tự phân giải lúc làm giàu,
 * còn đây là câu trả lời thiết bị trong mạng thật sự nhận được. Hai chỗ lệch nhau là
 * bình thường với CDN, và chính khoảng lệch đó mới đáng để ý.
 */
function ObservedIPs({ ips }: { ips: DomainIP[] }) {
  if (ips.length === 0) {
    return (
      <EmptyState>
        Chưa bắt được bản ghi trả lời nào cho domain này. Nếu toàn bộ hệ thống không có địa chỉ nào,
        kiểm tra <code>filter-port=53</code> trên sniffer của router.
      </EmptyState>
    );
  }

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm">
        <thead className="text-left text-xs uppercase text-slate-500">
          <tr>
            <th className="py-1 pr-3">Địa chỉ</th>
            <th className="py-1 pr-3">Mạng</th>
            <th className="py-1 pr-3">TTL</th>
            <th className="py-1 pr-3">Số lần</th>
            <th className="py-1">Thấy gần nhất</th>
          </tr>
        </thead>
        <tbody>
          {ips.map((ip) => (
            <tr key={ip.ip} className="border-t border-slate-100 dark:border-slate-800">
              <td className="py-1.5 pr-3 font-mono text-xs">
                {ip.ip}
                {ip.threat && (
                  <span
                    className="ml-2 rounded bg-red-100 px-1.5 py-0.5 text-xs text-red-800 dark:bg-red-900 dark:text-red-200"
                    title={`Khớp dải ${ip.threat} trong danh sách hạ tầng độc hại`}
                  >
                    độc hại
                  </span>
                )}
              </td>
              <td className="py-1.5 pr-3 text-xs">
                {ip.asn ? `AS${ip.asn} ` : ''}
                {[ip.org, ip.country].filter(Boolean).join(' · ') || '—'}
              </td>
              <td className="py-1.5 pr-3 text-xs">{ip.ttl}s</td>
              <td className="py-1.5 pr-3 text-xs">{formatNumber(ip.hits)}</td>
              <td className="py-1.5 text-xs text-slate-500" title={formatDateTime(ip.last_seen)}>
                {formatRelative(ip.last_seen)}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
