import { Suspense, lazy, useState } from 'react';
import { useNavigate } from '@tanstack/react-router';

import { useNetworkGraph } from '@/api/hooks';
import { Field, FilterBar, Select } from '@/components/ui/form';
import { Button, Card, EmptyState, ErrorState, Skeleton, cx } from '@/components/ui/primitives';
import { formatNumber } from '@/lib/format';
import { relationLabels } from '@/lib/strings';

// Canvas và mô phỏng lực chỉ nạp khi mở màn này.
const NetworkGraphView = lazy(() =>
  import('@/components/domain/network-graph').then((m) => ({ default: m.NetworkGraphView })),
);
const NetworkGraphTable = lazy(() =>
  import('@/components/domain/network-graph').then((m) => ({ default: m.NetworkGraphTable })),
);

const allKinds = ['cname_to', 'same_asn', 'same_cert', 'co_occurs'];

/**
 * Bản đồ quan hệ của toàn mạng.
 *
 * Khác trang chi tiết domain ở câu hỏi nó trả lời. Trang chi tiết hỏi "domain này
 * liên quan tới gì"; màn này hỏi "mạng này có những cụm hạ tầng nào" — và câu trả
 * lời là các chùm nút dính chặt vào nhau, thường là toàn bộ hạ tầng của một nhà
 * quảng cáo hiện ra cùng lúc.
 */
export function NetworkScreen() {
  const navigate = useNavigate();
  const [kinds, setKinds] = useState<string[]>(allKinds);
  const [status, setStatus] = useState('');
  const [limit, setLimit] = useState(300);
  const [minDegree, setMinDegree] = useState(2);

  const graph = useNetworkGraph({
    kinds: kinds.join(','),
    status,
    limit,
    min_degree: minDegree,
  });

  return (
    <div className="space-y-3">
      <Card title="Bộ lọc">
        <FilterBar>
          <Field
            label="Loại quan hệ"
            htmlFor="kinds"
            className="sm:col-span-2 xl:col-span-1"
            hint="Bỏ bớt loại cạnh để nhìn rõ một kiểu quan hệ. Đồng xuất hiện thường dày nhất."
          >
            <div id="kinds" className="flex flex-wrap gap-1">
              {allKinds.map((kind) => {
                const active = kinds.includes(kind);
                return (
                  <Button
                    key={kind}
                    variant={active ? 'primary' : 'secondary'}
                    aria-pressed={active}
                    className="px-2 py-1 text-xs"
                    onClick={() =>
                      setKinds((prev) =>
                        active ? prev.filter((k) => k !== kind) : [...prev, kind],
                      )
                    }
                  >
                    {relationLabels[kind]}
                  </Button>
                );
              })}
            </div>
          </Field>

          <Field label="Trạng thái" htmlFor="net-status">
            <Select id="net-status" value={status} onChange={(e) => setStatus(e.target.value)}>
              <option value="">Tất cả</option>
              <option value="new">Mới</option>
              <option value="staging">Chờ duyệt</option>
              <option value="blocked">Đã chặn</option>
              <option value="allowed">Cho qua</option>
            </Select>
          </Field>

          <Field
            label="Số quan hệ tối thiểu"
            htmlFor="min-degree"
            hint="Nút chỉ có một cạnh không cho biết gì về cụm."
          >
            <Select
              id="min-degree"
              value={String(minDegree)}
              onChange={(e) => setMinDegree(Number(e.target.value))}
            >
              <option value="1">1 — hiện tất cả</option>
              <option value="2">2</option>
              <option value="3">3</option>
              <option value="5">5 — chỉ các hub</option>
            </Select>
          </Field>

          <Field
            label="Số nút tối đa"
            htmlFor="limit"
            hint="Càng nhiều nút càng khó đọc và càng chậm."
          >
            <Select id="limit" value={String(limit)} onChange={(e) => setLimit(Number(e.target.value))}>
              <option value="100">100</option>
              <option value="300">300</option>
              <option value="600">600</option>
              <option value="1000">1000</option>
            </Select>
          </Field>
        </FilterBar>
      </Card>

      <Card
        title="Bản đồ quan hệ"
        actions={
          graph.data && (
            <span className="text-xs text-slate-500 dark:text-slate-400">
              {formatNumber(graph.data.nodes.length)} nút ·{' '}
              {formatNumber(graph.data.edges.length)} liên kết
            </span>
          )
        }
      >
        {graph.isPending ? (
          <Skeleton className="h-96 w-full" />
        ) : graph.isError ? (
          <ErrorState error={graph.error} />
        ) : !graph.data || graph.data.nodes.length === 0 ? (
          <EmptyState>
            Chưa có quan hệ nào để vẽ. Quan hệ được dựng bởi job chạy hằng đêm sau khi đã làm giàu
            dữ liệu — cần vài ngày lưu lượng thật trước khi bản đồ có nội dung.
          </EmptyState>
        ) : (
          <>
            {/* Cắt bớt phải nói ra chứ không im lặng: người dùng cần biết mình đang
                nhìn toàn bộ hay chỉ một phần. */}
            {graph.data.truncated && (
              <p
                className={cx(
                  'mb-2 rounded px-2 py-1 text-xs',
                  'bg-amber-50 text-amber-800 dark:bg-amber-950 dark:text-amber-300',
                )}
              >
                Hiển thị {formatNumber(graph.data.nodes.length)} trong tổng số{' '}
                {formatNumber(graph.data.total_nodes)} domain có quan hệ. Tăng giới hạn hoặc siết
                bộ lọc để thấy phần còn lại.
              </p>
            )}

            <Suspense fallback={<Skeleton className="h-96 w-full" />}>
              <NetworkGraphView
                nodes={graph.data.nodes}
                edges={graph.data.edges}
                onOpen={(id) =>
                  navigate({ to: '/domains/$domainId', params: { domainId: String(id) } })
                }
              />
              <NetworkGraphTable nodes={graph.data.nodes} edges={graph.data.edges} />
            </Suspense>
          </>
        )}
      </Card>
    </div>
  );
}
