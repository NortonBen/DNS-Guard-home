import { useCallback, useEffect, useRef, useState } from 'react';

import type { GraphEdge } from '@/api/types';
import { cx } from '@/components/ui/primitives';
import { runForceLayout, seedPositions, type LayoutNode } from '@/lib/force-layout';
import { relationLabels } from '@/lib/strings';

export interface NetworkNode {
  id: number;
  name: string;
  etld1: string;
  status: string;
  category?: string;
  degree: number;
}

interface Props {
  nodes: NetworkNode[];
  edges: GraphEdge[];
  onOpen: (id: number) => void;
}

/** Màu nút theo trạng thái, cùng quy ước với đồ thị của một domain. */
const statusFill: Record<string, string> = {
  blocked: '#dc2626',
  staging: '#d97706',
  allowed: '#059669',
  new: '#94a3b8',
  ignored: '#cbd5e1',
};

/** Kiểu nét theo loại quan hệ. */
const edgeStyle: Record<string, { dash: number[]; width: number; alpha: number }> = {
  cname_to: { dash: [], width: 1.4, alpha: 0.75 },
  same_cert: { dash: [6, 3], width: 1.0, alpha: 0.5 },
  same_asn: { dash: [2, 3], width: 1.0, alpha: 0.45 },
  co_occurs: { dash: [], width: 0.6, alpha: 0.3 },
};

/**
 * Bản đồ quan hệ của toàn mạng.
 *
 * Vẽ bằng canvas chứ không phải SVG: vài trăm nút kèm cạnh nghĩa là vài nghìn phần
 * tử DOM, và trình duyệt bò khi cần cập nhật chúng mỗi khung hình. Canvas vẽ lại
 * toàn bộ trong một lượt và không giữ trạng thái DOM nào.
 */
export function NetworkGraphView({ nodes, edges, onOpen }: Props) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const layoutRef = useRef<LayoutNode[]>([]);
  const [hovered, setHovered] = useState<NetworkNode | null>(null);

  // Khung nhìn: người dùng kéo để di chuyển và lăn chuột để phóng to. Không có nó,
  // một đồ thị vài trăm nút chỉ là một đám rối không đọc được.
  const viewRef = useRef({ scale: 1, offsetX: 0, offsetY: 0 });
  const dragRef = useRef<{ x: number; y: number; moved: boolean } | null>(null);

  const width = 1200;
  const height = 720;

  // Bảng tra dùng lại giữa các khung hình.
  const nodeById = useRef(new Map<number, NetworkNode>());
  const neighbours = useRef(new Map<number, Set<number>>());

  useEffect(() => {
    nodeById.current = new Map(nodes.map((n) => [n.id, n]));

    const adj = new Map<number, Set<number>>();
    for (const e of edges) {
      if (!adj.has(e.from)) adj.set(e.from, new Set());
      if (!adj.has(e.to)) adj.set(e.to, new Set());
      adj.get(e.from)!.add(e.to);
      adj.get(e.to)!.add(e.from);
    }
    neighbours.current = adj;
  }, [nodes, edges]);

  const draw = useCallback(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext('2d');
    if (!ctx) return;

    const dpr = window.devicePixelRatio || 1;
    const view = viewRef.current;
    const dark = document.documentElement.classList.contains('dark');

    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, width, height);
    ctx.save();
    ctx.translate(view.offsetX, view.offsetY);
    ctx.scale(view.scale, view.scale);

    const positions = new Map(layoutRef.current.map((n) => [n.id, n]));
    const focus = hovered?.id;
    const near = focus !== undefined ? neighbours.current.get(focus) : undefined;

    // Ngưỡng hiện nhãn thích ứng theo số nút. Một ngưỡng cố định hoặc làm đồ thị nhỏ
    // thành đám chấm không tên, hoặc làm đồ thị lớn thành đống chữ chồng nhau. Với ít
    // nút thì hiện hết; đông dần thì chỉ giữ lại các nút nhiều quan hệ nhất.
    const labelMinDegree =
      nodes.length <= 40 ? 0 : nodes.length <= 120 ? 3 : nodes.length <= 300 ? 6 : 10;

    // Cạnh vẽ trước để nút nằm đè lên trên.
    for (const e of edges) {
      const a = positions.get(e.from);
      const b = positions.get(e.to);
      if (!a || !b) continue;

      const style = edgeStyle[e.kind] ?? edgeStyle.co_occurs!;
      const related =
        focus === undefined || e.from === focus || e.to === focus;

      ctx.beginPath();
      ctx.setLineDash(style.dash);
      ctx.lineWidth = style.width / view.scale;
      ctx.globalAlpha = related ? style.alpha : style.alpha * 0.15;
      ctx.strokeStyle = dark ? '#475569' : '#cbd5e1';
      ctx.moveTo(a.x, a.y);
      ctx.lineTo(b.x, b.y);
      ctx.stroke();
    }

    ctx.setLineDash([]);
    ctx.globalAlpha = 1;

    for (const node of nodes) {
      const pos = positions.get(node.id);
      if (!pos) continue;

      // Nút to nhỏ theo số quan hệ: hub của một cụm hạ tầng nổi lên ngay.
      const radius = Math.min(4 + Math.sqrt(node.degree) * 1.8, 16);
      const dimmed = focus !== undefined && node.id !== focus && !near?.has(node.id);

      ctx.globalAlpha = dimmed ? 0.2 : 1;
      ctx.beginPath();
      ctx.arc(pos.x, pos.y, radius, 0, Math.PI * 2);
      ctx.fillStyle = statusFill[node.status] ?? '#94a3b8';
      ctx.fill();
      ctx.lineWidth = 1.5 / view.scale;
      ctx.strokeStyle = dark ? '#0f172a' : '#ffffff';
      ctx.stroke();

      // Nút đang trỏ tới và hàng xóm của nó luôn có nhãn, bất kể ngưỡng: đó chính là
      // thứ người dùng đang muốn đọc.
      const showLabel =
        node.degree >= labelMinDegree || node.id === focus || near?.has(node.id) === true;
      if (showLabel && !dimmed && view.scale > 0.55) {
        ctx.globalAlpha = 1;
        ctx.font = `${11 / view.scale}px ui-monospace, monospace`;
        ctx.textAlign = 'center';
        ctx.fillStyle = dark ? '#cbd5e1' : '#475569';
        const label = node.name.length > 30 ? `${node.name.slice(0, 28)}…` : node.name;
        ctx.fillText(label, pos.x, pos.y - radius - 4 / view.scale);
      }
    }

    ctx.restore();
  }, [nodes, edges, hovered]);

  // Mô phỏng chạy lại khi dữ liệu đổi, không chạy lại khi chỉ đổi nút đang trỏ tới.
  useEffect(() => {
    if (nodes.length === 0) return;

    layoutRef.current = seedPositions(
      nodes.map((n) => n.id),
      width,
      height,
    );
    viewRef.current = { scale: 1, offsetX: 0, offsetY: 0 };

    const cancel = runForceLayout(
      layoutRef.current,
      edges.map((e) => ({ from: e.from, to: e.to, strength: e.strength })),
      { width, height, steps: nodes.length > 200 ? 180 : 240 },
      () => draw(),
    );
    return cancel;
  }, [nodes, edges, draw]);

  // Vẽ lại khi con trỏ đổi nút hoặc khi khung nhìn thay đổi.
  useEffect(() => {
    draw();
  }, [draw]);

  /** Đổi tọa độ con trỏ trên màn hình sang tọa độ trong đồ thị. */
  const toGraphSpace = useCallback((clientX: number, clientY: number) => {
    const canvas = canvasRef.current;
    if (!canvas) return null;
    const rect = canvas.getBoundingClientRect();
    const view = viewRef.current;
    const x = ((clientX - rect.left) * (width / rect.width) - view.offsetX) / view.scale;
    const y = ((clientY - rect.top) * (height / rect.height) - view.offsetY) / view.scale;
    return { x, y };
  }, []);

  const nodeAt = useCallback((gx: number, gy: number): NetworkNode | null => {
    for (const pos of layoutRef.current) {
      const node = nodeById.current.get(pos.id);
      if (!node) continue;
      const radius = Math.min(4 + Math.sqrt(node.degree) * 1.8, 16) + 4;
      const dx = pos.x - gx;
      const dy = pos.y - gy;
      if (dx * dx + dy * dy <= radius * radius) return node;
    }
    return null;
  }, []);

  return (
    <div>
      <canvas
        ref={canvasRef}
        width={width * (window.devicePixelRatio || 1)}
        height={height * (window.devicePixelRatio || 1)}
        style={{ width: '100%', aspectRatio: `${width} / ${height}` }}
        className={cx(
          'touch-none rounded-md bg-slate-50 dark:bg-slate-950',
          dragRef.current ? 'cursor-grabbing' : hovered ? 'cursor-pointer' : 'cursor-grab',
        )}
        role="img"
        aria-label={`Bản đồ quan hệ với ${nodes.length} domain và ${edges.length} liên kết`}
        onPointerDown={(e) => {
          dragRef.current = { x: e.clientX, y: e.clientY, moved: false };
          e.currentTarget.setPointerCapture(e.pointerId);
        }}
        onPointerMove={(e) => {
          const drag = dragRef.current;
          if (drag) {
            const dx = e.clientX - drag.x;
            const dy = e.clientY - drag.y;
            if (Math.abs(dx) > 2 || Math.abs(dy) > 2) drag.moved = true;

            const rect = e.currentTarget.getBoundingClientRect();
            viewRef.current.offsetX += dx * (width / rect.width);
            viewRef.current.offsetY += dy * (height / rect.height);
            drag.x = e.clientX;
            drag.y = e.clientY;
            draw();
            return;
          }

          const p = toGraphSpace(e.clientX, e.clientY);
          if (!p) return;
          const found = nodeAt(p.x, p.y);
          if (found?.id !== hovered?.id) setHovered(found);
        }}
        onPointerUp={(e) => {
          const drag = dragRef.current;
          dragRef.current = null;
          e.currentTarget.releasePointerCapture(e.pointerId);

          // Kéo để di chuyển thì không tính là bấm chọn.
          if (drag && !drag.moved && hovered) onOpen(hovered.id);
        }}
        onPointerLeave={() => {
          dragRef.current = null;
          setHovered(null);
        }}
        onWheel={(e) => {
          const view = viewRef.current;
          const factor = e.deltaY < 0 ? 1.12 : 1 / 1.12;
          const next = Math.min(Math.max(view.scale * factor, 0.3), 4);

          // Phóng to quanh vị trí con trỏ chứ không quanh gốc tọa độ, để thứ đang
          // nhìn không trượt ra khỏi khung.
          const rect = e.currentTarget.getBoundingClientRect();
          const px = (e.clientX - rect.left) * (width / rect.width);
          const py = (e.clientY - rect.top) * (height / rect.height);
          view.offsetX = px - ((px - view.offsetX) / view.scale) * next;
          view.offsetY = py - ((py - view.offsetY) / view.scale) * next;
          view.scale = next;
          draw();
        }}
      />

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
        <span>
          <span aria-hidden className="mr-1 inline-block size-2 rounded-full bg-slate-400" />
          Mới
        </span>
        <span className="ml-auto">Kéo để di chuyển · lăn chuột để phóng to · bấm để mở</span>
      </div>

      {hovered && (
        <div className="mt-2 rounded-md bg-slate-100 px-3 py-2 text-sm dark:bg-slate-800">
          <span className="domain-name font-medium">{hovered.name}</span>
          <span className="ml-2 text-xs text-slate-500 dark:text-slate-400">
            {hovered.degree} quan hệ
            {hovered.category && ` · ${hovered.category}`}
          </span>
        </div>
      )}
    </div>
  );
}

/**
 * Bảng tương đương cho người dùng trình đọc màn hình.
 *
 * Một canvas không truyền tải được quan hệ nào, nên mọi thứ trong hình phải đọc được
 * ở dạng văn bản. Nhóm theo domain thay vì liệt kê cạnh rời rạc: đó là cách người
 * dùng thực sự đọc đồ thị này.
 */
export function NetworkGraphTable({ nodes, edges }: { nodes: NetworkNode[]; edges: GraphEdge[] }) {
  const nameById = new Map(nodes.map((n) => [n.id, n.name]));
  const grouped = new Map<number, GraphEdge[]>();

  for (const e of edges) {
    const list = grouped.get(e.from);
    if (list) list.push(e);
    else grouped.set(e.from, [e]);
  }

  const rows = [...grouped.entries()]
    .sort((a, b) => b[1].length - a[1].length)
    .slice(0, 60);

  return (
    <details className="mt-3">
      <summary className="cursor-pointer text-xs text-slate-500 hover:text-slate-700 dark:text-slate-400 dark:hover:text-slate-200">
        Xem dạng bảng ({edges.length} liên kết)
      </summary>
      <table className="mt-2 w-full text-sm">
        <thead>
          <tr className="border-b border-slate-200 text-left text-xs uppercase tracking-wide text-slate-500 dark:border-slate-800 dark:text-slate-400">
            <th scope="col" className="py-1">Domain</th>
            <th scope="col" className="py-1">Liên kết tới</th>
          </tr>
        </thead>
        <tbody>
          {rows.map(([id, list]) => (
            <tr key={id} className="border-b border-slate-100 align-top dark:border-slate-800">
              <td className="domain-name py-1 pr-3">{nameById.get(id) ?? id}</td>
              <td className="py-1 text-xs text-slate-600 dark:text-slate-300">
                {list
                  .slice(0, 8)
                  .map((e) => `${nameById.get(e.to) ?? e.to} (${relationLabels[e.kind] ?? e.kind})`)
                  .join(', ')}
                {list.length > 8 && ` … và ${list.length - 8} liên kết nữa`}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </details>
  );
}
