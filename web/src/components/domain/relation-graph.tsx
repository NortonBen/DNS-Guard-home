import { useEffect, useMemo, useRef, useState } from 'react';

import type { GraphEdge, GraphNode, RelationGraph } from '@/api/types';
import { Button, cx } from '@/components/ui/primitives';
import { relationLabels } from '@/lib/strings';

/**
 * Bố cục lực đẩy tự cài đặt thay vì dùng thư viện đồ thị.
 *
 * Đồ thị ở đây tối đa vài trăm nút, và một thư viện đồ thị đầy đủ nặng hơn toàn bộ
 * phần còn lại của bundle cộng lại. Mô phỏng lực ở dạng đơn giản nhất — đẩy nhau
 * theo nghịch đảo bình phương, cạnh kéo như lò xo — cho kết quả đủ tốt ở quy mô này.
 */
interface Positioned extends GraphNode {
  x: number;
  y: number;
  vx: number;
  vy: number;
}

const statusFill: Record<string, string> = {
  blocked: '#dc2626',
  staging: '#d97706',
  allowed: '#059669',
  new: '#94a3b8',
  ignored: '#cbd5e1',
};

const edgeStyle: Record<GraphEdge['kind'], { dash: string; width: number }> = {
  cname_to: { dash: '', width: 1.8 },
  same_cert: { dash: '6 3', width: 1.2 },
  same_asn: { dash: '2 3', width: 1.2 },
  co_occurs: { dash: '', width: 0.7 },
};

interface Props {
  graph: RelationGraph;
  selected: Set<number>;
  onToggleSelect: (id: number, additive: boolean) => void;
  onOpen: (id: number) => void;
}

export function RelationGraphView({ graph, selected, onToggleSelect, onOpen }: Props) {
  const [nodes, setNodes] = useState<Positioned[]>([]);
  const frameRef = useRef<number>(0);
  const width = 720;
  const height = 420;

  // Chỉ số theo id để tra tọa độ khi vẽ cạnh.
  const positionById = useMemo(() => {
    const map = new Map<number, Positioned>();
    for (const node of nodes) map.set(node.id, node);
    return map;
  }, [nodes]);

  useEffect(() => {
    // Xếp vòng tròn quanh nút gốc làm trạng thái ban đầu: khởi tạo ngẫu nhiên khiến
    // đồ thị nhảy khác nhau mỗi lần mở cùng một domain.
    const initial: Positioned[] = graph.nodes.map((node, index) => {
      if (node.root) return { ...node, x: width / 2, y: height / 2, vx: 0, vy: 0 };
      const angle = (index / Math.max(graph.nodes.length - 1, 1)) * Math.PI * 2;
      return {
        ...node,
        x: width / 2 + Math.cos(angle) * 150,
        y: height / 2 + Math.sin(angle) * 150,
        vx: 0,
        vy: 0,
      };
    });
    setNodes(initial);

    let current = initial;
    let iteration = 0;

    function step() {
      // Dừng sau một số bước cố định: đồ thị đã ổn định thì mô phỏng tiếp chỉ tốn pin.
      if (iteration++ > 220) return;

      const next = current.map((node) => ({ ...node }));

      for (let i = 0; i < next.length; i++) {
        const a = next[i];
        if (!a) continue;

        for (let j = i + 1; j < next.length; j++) {
          const b = next[j];
          if (!b) continue;

          const dx = b.x - a.x;
          const dy = b.y - a.y;
          const distSq = Math.max(dx * dx + dy * dy, 60);
          const force = 5200 / distSq;
          const dist = Math.sqrt(distSq);
          const fx = (dx / dist) * force;
          const fy = (dy / dist) * force;

          a.vx -= fx;
          a.vy -= fy;
          b.vx += fx;
          b.vy += fy;
        }
      }

      for (const edge of graph.edges) {
        const a = next.find((n) => n.id === edge.from);
        const b = next.find((n) => n.id === edge.to);
        if (!a || !b) continue;

        const dx = b.x - a.x;
        const dy = b.y - a.y;
        const dist = Math.max(Math.sqrt(dx * dx + dy * dy), 1);
        // Cạnh mạnh kéo chặt hơn, nên cụm quan hệ thật sẽ tụm lại với nhau.
        const force = (dist - 110) * 0.012 * (0.4 + edge.strength * 0.6);
        const fx = (dx / dist) * force;
        const fy = (dy / dist) * force;

        a.vx += fx;
        a.vy += fy;
        b.vx -= fx;
        b.vy -= fy;
      }

      for (const node of next) {
        if (node.root) {
          // Neo nút gốc ở giữa: người dùng luôn biết đang xem domain nào.
          node.x = width / 2;
          node.y = height / 2;
          node.vx = node.vy = 0;
          continue;
        }
        node.vx *= 0.82;
        node.vy *= 0.82;
        node.x = Math.min(Math.max(node.x + node.vx, 24), width - 24);
        node.y = Math.min(Math.max(node.y + node.vy, 20), height - 20);
      }

      current = next;
      setNodes(next);
      frameRef.current = requestAnimationFrame(step);
    }

    frameRef.current = requestAnimationFrame(step);
    return () => cancelAnimationFrame(frameRef.current);
  }, [graph]);

  return (
    <div>
      <svg
        viewBox={`0 0 ${width} ${height}`}
        className="w-full rounded-md bg-slate-50 dark:bg-slate-950"
        role="img"
        aria-label={`Đồ thị quan hệ với ${graph.nodes.length} domain`}
      >
        <g>
          {graph.edges.map((edge, index) => {
            const from = positionById.get(edge.from);
            const to = positionById.get(edge.to);
            if (!from || !to) return null;
            const style = edgeStyle[edge.kind];

            return (
              <line
                key={`${edge.from}-${edge.to}-${edge.kind}-${index}`}
                x1={from.x}
                y1={from.y}
                x2={to.x}
                y2={to.y}
                stroke="currentColor"
                className="text-slate-300 dark:text-slate-700"
                strokeWidth={style.width}
                strokeDasharray={style.dash}
              />
            );
          })}
        </g>

        <g>
          {nodes.map((node) => (
            <g
              key={node.id}
              transform={`translate(${node.x} ${node.y})`}
              className="cursor-pointer"
              onClick={(event) => onToggleSelect(node.id, event.shiftKey)}
              onDoubleClick={() => onOpen(node.id)}
            >
              <circle
                r={node.root ? 9 : 6}
                fill={statusFill[node.status] ?? '#94a3b8'}
                stroke={selected.has(node.id) ? '#0284c7' : 'white'}
                strokeWidth={selected.has(node.id) ? 3 : 1.5}
              />
              <text
                y={node.root ? -14 : -11}
                textAnchor="middle"
                className="fill-slate-600 font-mono text-[9px] dark:fill-slate-300"
              >
                {node.name.length > 26 ? `${node.name.slice(0, 24)}…` : node.name}
              </text>
            </g>
          ))}
        </g>
      </svg>

      <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-slate-500 dark:text-slate-400">
        <span>
          <span aria-hidden className="mr-1 inline-block size-2 rounded-full bg-red-600" />
          Đã chặn
        </span>
        <span>
          <span aria-hidden className="mr-1 inline-block size-2 rounded-full bg-amber-600" />
          Chờ duyệt
        </span>
        <span>
          <span aria-hidden className="mr-1 inline-block size-2 rounded-full bg-emerald-600" />
          Cho qua
        </span>
        <span>Bấm để chọn · Shift+bấm chọn nhiều · bấm đúp để mở</span>
      </div>

      {/*
        Bảng tương đương cho người dùng trình đọc màn hình: một đồ thị SVG không
        truyền tải được quan hệ, nên mọi thông tin trong hình phải đọc được ở dạng văn bản.
      */}
      <details className="mt-3">
        <summary className="cursor-pointer text-xs text-slate-500 hover:text-slate-700 dark:text-slate-400 dark:hover:text-slate-200">
          Xem dạng bảng ({graph.edges.length} quan hệ)
        </summary>
        <table className="mt-2 w-full text-sm">
          <thead>
            <tr className="border-b border-slate-200 text-left text-xs uppercase tracking-wide text-slate-500 dark:border-slate-800 dark:text-slate-400">
              <th scope="col" className="py-1">Domain liên quan</th>
              <th scope="col" className="py-1">Loại quan hệ</th>
              <th scope="col" className="py-1 text-right">Độ mạnh</th>
            </tr>
          </thead>
          <tbody>
            {graph.edges.map((edge, index) => {
              const target = graph.nodes.find((n) => n.id === edge.to);
              return (
                <tr key={index} className="border-b border-slate-100 dark:border-slate-800">
                  <td className="domain-name py-1">{target?.name ?? edge.to}</td>
                  <td className="py-1">{relationLabels[edge.kind] ?? edge.kind}</td>
                  <td className="py-1 text-right tabular-nums">{edge.strength.toFixed(2)}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </details>
    </div>
  );
}

/** Bộ lọc loại cạnh, đặt ngoài đồ thị để đổi lọc không dựng lại mô phỏng. */
export function EdgeKindFilter({
  kinds,
  onChange,
}: {
  kinds: string[];
  onChange: (kinds: string[]) => void;
}) {
  const all: GraphEdge['kind'][] = ['cname_to', 'same_asn', 'same_cert', 'co_occurs'];

  return (
    <div className="flex flex-wrap gap-1">
      {all.map((kind) => {
        const active = kinds.includes(kind);
        return (
          <Button
            key={kind}
            variant="ghost"
            className={cx(
              'px-2 py-1 text-xs',
              active && 'bg-sky-50 text-sky-700 dark:bg-sky-950 dark:text-sky-300',
            )}
            onClick={() =>
              onChange(active ? kinds.filter((k) => k !== kind) : [...kinds, kind])
            }
          >
            {relationLabels[kind]}
          </Button>
        );
      })}
    </div>
  );
}
