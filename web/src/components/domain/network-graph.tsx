import { useCallback, useEffect, useMemo, useRef, useState } from 'react';

import type { GraphEdge } from '@/api/types';
import { Button, cx } from '@/components/ui/primitives';
import { boundsOf, runForceLayout, seedPositions, type LayoutNode } from '@/lib/force-layout';
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

const statusLabels: Record<string, string> = {
  blocked: 'Đã chặn',
  staging: 'Chờ duyệt',
  allowed: 'Cho qua',
  new: 'Mới',
  ignored: 'Bỏ qua',
};

/** Kiểu nét theo loại quan hệ. */
const edgeStyle: Record<string, { dash: number[]; width: number; alpha: number }> = {
  cname_to: { dash: [], width: 1.4, alpha: 0.75 },
  same_cert: { dash: [6, 3], width: 1.0, alpha: 0.5 },
  same_asn: { dash: [2, 3], width: 1.0, alpha: 0.45 },
  co_occurs: { dash: [], width: 0.6, alpha: 0.3 },
};

const canvasWidth = 1200;
const canvasHeight = 720;

/**
 * Số nhãn tối đa vẽ cùng lúc.
 *
 * Kể cả khi còn chỗ trống, quá ngần này chữ trên một màn hình là thứ không ai đọc.
 * Cái cần là nhận ra vài cụm chính, không phải đọc hết ba trăm cái tên.
 */
const maxLabels = 45;

const labelFont = '11px ui-monospace, SFMono-Regular, Menlo, monospace';

function radiusOf(degree: number): number {
  return Math.min(4 + Math.sqrt(degree) * 1.8, 16);
}

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

  // Nút đang trỏ tới và nút đang chọn giữ ở cả ref lẫn state. Ref cho vòng vẽ, vì nó
  // chạy mỗi khung hình và không được phụ thuộc chu kỳ render của React; state chỉ
  // để dựng bảng thông tin bên dưới.
  const hoverRef = useRef<NetworkNode | null>(null);
  const selectedRef = useRef<NetworkNode | null>(null);
  const [hovered, setHovered] = useState<NetworkNode | null>(null);
  const [selected, setSelected] = useState<NetworkNode | null>(null);

  // Khung nhìn: kéo để di chuyển, lăn chuột để phóng to. Không có nó, một đồ thị vài
  // trăm nút chỉ là một đám rối không đọc được.
  const viewRef = useRef({ scale: 1, offsetX: 0, offsetY: 0 });
  const dragRef = useRef<{ x: number; y: number; moved: boolean } | null>(null);
  const drawHandle = useRef(0);

  const nodeById = useMemo(() => new Map(nodes.map((n) => [n.id, n])), [nodes]);

  /** Hàng xóm kèm loại quan hệ, dùng cho cả tô sáng lẫn bảng chi tiết. */
  const neighbours = useMemo(() => {
    const adj = new Map<number, Map<number, string>>();
    const add = (a: number, b: number, kind: string) => {
      let row = adj.get(a);
      if (!row) adj.set(a, (row = new Map()));
      // Giữ loại quan hệ mạnh nhất khi hai domain nối nhau bằng nhiều kiểu.
      if (!row.has(b) || kind === 'cname_to') row.set(b, kind);
    };
    for (const e of edges) {
      add(e.from, e.to, e.kind);
      add(e.to, e.from, e.kind);
    }
    return adj;
  }, [edges]);

  /** Cạnh gom theo loại, để vẽ mỗi loại bằng một lệnh stroke thay vì một lệnh mỗi cạnh. */
  const edgesByKind = useMemo(() => {
    const groups = new Map<string, GraphEdge[]>();
    for (const e of edges) {
      const list = groups.get(e.kind);
      if (list) list.push(e);
      else groups.set(e.kind, [e]);
    }
    return groups;
  }, [edges]);

  /** Nút xếp theo bậc giảm dần, làm thứ tự ưu tiên khi đặt nhãn. */
  const byDegree = useMemo(() => [...nodes].sort((a, b) => b.degree - a.degree), [nodes]);

  const draw = useCallback(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext('2d');
    if (!ctx) return;

    const dpr = window.devicePixelRatio || 1;
    const view = viewRef.current;
    const dark = document.documentElement.classList.contains('dark');

    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, canvasWidth, canvasHeight);

    const positions = new Map(layoutRef.current.map((n) => [n.id, n]));

    // Nút đang chọn thắng nút đang trỏ tới: chọn là hành động có chủ ý, còn con trỏ
    // chỉ tình cờ đi ngang qua.
    const focusNode = selectedRef.current ?? hoverRef.current;
    const focus = focusNode?.id;
    const near = focus !== undefined ? neighbours.get(focus) : undefined;

    ctx.save();
    ctx.translate(view.offsetX, view.offsetY);
    ctx.scale(view.scale, view.scale);

    // ── Cạnh ────────────────────────────────────────────────────────────────
    // Vẽ trước để nút nằm đè lên trên. Gom theo loại và stroke một lần cho cả nhóm:
    // hai nghìn lệnh stroke riêng lẻ là phần tốn nhất của cả khung hình.
    ctx.strokeStyle = dark ? '#475569' : '#cbd5e1';

    for (const [kind, list] of edgesByKind) {
      const style = edgeStyle[kind] ?? edgeStyle.co_occurs!;
      ctx.setLineDash(style.dash.map((d) => d / view.scale));
      ctx.lineWidth = style.width / view.scale;

      // Hai lượt: cạnh mờ gom chung một đường, cạnh chạm nút đang chọn gom riêng để
      // nổi lên. Khi không chọn gì thì lượt mờ rỗng và chỉ còn một stroke mỗi loại.
      for (const related of [false, true]) {
        ctx.globalAlpha = related ? style.alpha : style.alpha * 0.12;
        ctx.beginPath();
        let any = false;

        for (const e of list) {
          const touches = focus === undefined || e.from === focus || e.to === focus;
          if (touches !== related) continue;

          const a = positions.get(e.from);
          const b = positions.get(e.to);
          if (!a || !b) continue;

          ctx.moveTo(a.x, a.y);
          ctx.lineTo(b.x, b.y);
          any = true;
        }
        if (any) ctx.stroke();
      }
    }

    ctx.setLineDash([]);
    ctx.globalAlpha = 1;

    // ── Nút ─────────────────────────────────────────────────────────────────
    for (const node of nodes) {
      const pos = positions.get(node.id);
      if (!pos) continue;

      const radius = radiusOf(node.degree);
      const isFocus = node.id === focus;
      const dimmed = focus !== undefined && !isFocus && !near?.has(node.id);

      ctx.globalAlpha = dimmed ? 0.15 : 1;
      ctx.beginPath();
      ctx.arc(pos.x, pos.y, radius, 0, Math.PI * 2);
      ctx.fillStyle = statusFill[node.status] ?? '#94a3b8';
      ctx.fill();

      // Vòng ngoài dày cho nút đang chọn: phải thấy ngay mình đang giữ cái nào, kể cả
      // sau khi kéo đồ thị đi chỗ khác.
      ctx.lineWidth = (isFocus ? 3 : 1.5) / view.scale;
      ctx.strokeStyle = isFocus
        ? dark
          ? '#f8fafc'
          : '#0f172a'
        : dark
          ? '#0f172a'
          : '#ffffff';
      ctx.stroke();
    }

    ctx.restore();
    ctx.globalAlpha = 1;

    // ── Nhãn ────────────────────────────────────────────────────────────────
    // Vẽ trong tọa độ màn hình, sau khi đã bỏ phép biến đổi: nhờ vậy chữ giữ nguyên
    // cỡ ở mọi mức phóng to, và quan trọng hơn là đo được bề rộng thật để tránh chồng.
    ctx.font = labelFont;
    ctx.textAlign = 'center';
    ctx.textBaseline = 'bottom';

    const toScreenX = (x: number) => x * view.scale + view.offsetX;
    const toScreenY = (y: number) => y * view.scale + view.offsetY;

    // Thứ tự ưu tiên: nút đang chọn, rồi hàng xóm của nó, rồi các nút nhiều quan hệ
    // nhất. Thứ vừa bấm vào luôn có nhãn, kể cả khi nó là một nút lá.
    const candidates: NetworkNode[] = [];
    if (focusNode) {
      candidates.push(focusNode);
      if (near) {
        for (const id of near.keys()) {
          const n = nodeById.get(id);
          if (n) candidates.push(n);
        }
      }
    }
    for (const n of byDegree) {
      if (focus !== undefined && (n.id === focus || near?.has(n.id))) continue;
      candidates.push(n);
    }

    // Hộp đã đặt, để kiểm tra chồng lấn. Đây là chỗ sửa thật sự: ngưỡng theo bậc
    // không biết hai nhãn có đè lên nhau hay không, nên ở đồ thị nhiều hub thì hàng
    // trăm nhãn cùng vượt ngưỡng và chồng thành một đám chữ không đọc được.
    const placed: { x1: number; y1: number; x2: number; y2: number }[] = [];
    let drawn = 0;

    for (const node of candidates) {
      if (drawn >= maxLabels) break;

      const pos = positions.get(node.id);
      if (!pos) continue;

      const isFocus = node.id === focus;
      const dimmed = focus !== undefined && !isFocus && !near?.has(node.id);
      if (dimmed) continue;

      const sx = toScreenX(pos.x);
      const sy = toScreenY(pos.y);
      const radius = radiusOf(node.degree) * view.scale;

      // Bỏ qua nút ngoài khung: đo và đặt nhãn cho chúng chỉ chiếm chỗ của nhãn đang
      // thật sự nhìn thấy.
      if (sx < -40 || sx > canvasWidth + 40 || sy < -20 || sy > canvasHeight + 20) continue;

      const label = node.name.length > 34 ? `${node.name.slice(0, 32)}…` : node.name;
      const w = ctx.measureText(label).width;
      const x1 = sx - w / 2 - 2;
      const x2 = sx + w / 2 + 2;
      const y2 = sy - radius - 3;
      const y1 = y2 - 12;

      let collides = false;
      for (const box of placed) {
        if (x1 < box.x2 && x2 > box.x1 && y1 < box.y2 && y2 > box.y1) {
          collides = true;
          break;
        }
      }
      // Nút đang chọn và hàng xóm luôn được vẽ: đó chính là thứ đang cần đọc.
      const mustShow = isFocus || near?.has(node.id) === true;
      if (collides && !mustShow) continue;

      placed.push({ x1, y1, x2, y2 });
      drawn++;

      // Nền mờ phía sau chữ để nó đọc được khi nằm trên một búi cạnh dày.
      ctx.globalAlpha = 0.82;
      ctx.fillStyle = dark ? '#020617' : '#f8fafc';
      ctx.fillRect(x1, y1, x2 - x1, y2 - y1);

      ctx.globalAlpha = 1;
      ctx.fillStyle = isFocus
        ? dark
          ? '#f8fafc'
          : '#0f172a'
        : dark
          ? '#cbd5e1'
          : '#475569';
      ctx.fillText(label, sx, y2);
    }
  }, [nodes, edgesByKind, neighbours, nodeById, byDegree]);

  /** Gom nhiều yêu cầu vẽ trong cùng một khung hình thành một lần. */
  const scheduleDraw = useCallback(() => {
    if (drawHandle.current) return;
    drawHandle.current = requestAnimationFrame(() => {
      drawHandle.current = 0;
      draw();
    });
  }, [draw]);

  /** Căn đồ thị vừa khít khung nhìn. */
  const fitToView = useCallback(() => {
    const bounds = boundsOf(layoutRef.current);
    if (!bounds) return;

    const pad = 56;
    const w = Math.max(bounds.maxX - bounds.minX, 1);
    const h = Math.max(bounds.maxY - bounds.minY, 1);
    const scale = Math.min((canvasWidth - pad * 2) / w, (canvasHeight - pad * 2) / h, 2.5);

    viewRef.current = {
      scale,
      offsetX: canvasWidth / 2 - ((bounds.minX + bounds.maxX) / 2) * scale,
      offsetY: canvasHeight / 2 - ((bounds.minY + bounds.maxY) / 2) * scale,
    };
    scheduleDraw();
  }, [scheduleDraw]);

  // Mô phỏng chạy lại khi dữ liệu đổi, không chạy lại khi chỉ đổi nút đang trỏ tới.
  useEffect(() => {
    if (nodes.length === 0) return;

    // Không gian mô phỏng nở theo số nút. Nhồi ba trăm nút vào đúng khung dành cho
    // sáu mươi thì lực đẩy không đủ chỗ tách chúng ra, và kết quả là một cục ở giữa.
    const spread = Math.sqrt(Math.max(nodes.length, 60) / 60);
    const worldWidth = canvasWidth * spread;
    const worldHeight = canvasHeight * spread;

    layoutRef.current = seedPositions(
      nodes.map((n) => n.id),
      worldWidth,
      worldHeight,
    );

    hoverRef.current = null;
    selectedRef.current = null;
    setHovered(null);
    setSelected(null);

    const cancel = runForceLayout(
      layoutRef.current,
      edges.map((e) => ({ from: e.from, to: e.to, strength: e.strength })),
      {
        width: worldWidth,
        height: worldHeight,
        steps: nodes.length > 200 ? 320 : 240,
        linkDistance: nodes.length > 200 ? 70 : 90,
      },
      // Căn khung sau mỗi bước, để người dùng thấy đồ thị nở dần trong khung thay vì
      // trôi ra ngoài mép rồi biến mất.
      () => fitToView(),
    );
    return cancel;
  }, [nodes, edges, fitToView]);

  useEffect(() => {
    scheduleDraw();
  }, [scheduleDraw]);

  // Bỏ chọn bằng phím Esc: chọn xong mà chỉ thoát được bằng cách tìm đúng một khoảng
  // trống để bấm là kiểu bế tắc nhỏ khiến người dùng phải tải lại trang.
  useEffect(() => {
    if (!selected) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        selectedRef.current = null;
        setSelected(null);
        scheduleDraw();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [selected, scheduleDraw]);

  useEffect(() => () => cancelAnimationFrame(drawHandle.current), []);

  /** Đổi tọa độ con trỏ trên màn hình sang tọa độ trong đồ thị. */
  const toGraphSpace = useCallback((clientX: number, clientY: number) => {
    const canvas = canvasRef.current;
    if (!canvas) return null;
    const rect = canvas.getBoundingClientRect();
    const view = viewRef.current;
    return {
      x: ((clientX - rect.left) * (canvasWidth / rect.width) - view.offsetX) / view.scale,
      y: ((clientY - rect.top) * (canvasHeight / rect.height) - view.offsetY) / view.scale,
    };
  }, []);

  /**
   * Nút nằm dưới con trỏ.
   *
   * Trả về nút *gần nhất* chứ không phải nút đầu tiên khớp. Ở đồ thị dày, nhiều nút
   * chồng lên nhau dưới cùng một điểm, và lấy cái đầu tiên trong mảng nghĩa là bấm
   * vào một chấm rồi chọn trúng một chấm khác.
   */
  const nodeAt = useCallback(
    (gx: number, gy: number): NetworkNode | null => {
      let best: NetworkNode | null = null;
      let bestDist = Infinity;
      // Vùng bấm nới thêm vài pixel màn hình, quy về tọa độ đồ thị nên vẫn dễ bấm khi
      // đang thu nhỏ.
      const slack = 6 / viewRef.current.scale;

      for (const pos of layoutRef.current) {
        const node = nodeById.get(pos.id);
        if (!node) continue;

        const reach = radiusOf(node.degree) + slack;
        const dx = pos.x - gx;
        const dy = pos.y - gy;
        const distSq = dx * dx + dy * dy;
        if (distSq <= reach * reach && distSq < bestDist) {
          best = node;
          bestDist = distSq;
        }
      }
      return best;
    },
    [nodeById],
  );

  const zoomBy = useCallback(
    (factor: number) => {
      const view = viewRef.current;
      const next = Math.min(Math.max(view.scale * factor, 0.2), 5);
      const cx = canvasWidth / 2;
      const cy = canvasHeight / 2;
      view.offsetX = cx - ((cx - view.offsetX) / view.scale) * next;
      view.offsetY = cy - ((cy - view.offsetY) / view.scale) * next;
      view.scale = next;
      scheduleDraw();
    },
    [scheduleDraw],
  );

  const selectedNeighbours = useMemo(() => {
    if (!selected) return [];
    const row = neighbours.get(selected.id);
    if (!row) return [];
    return [...row.entries()]
      .map(([id, kind]) => ({ node: nodeById.get(id), kind }))
      .filter((x): x is { node: NetworkNode; kind: string } => Boolean(x.node))
      .sort((a, b) => b.node.degree - a.node.degree);
  }, [selected, neighbours, nodeById]);

  return (
    <div>
      <div className="relative">
        <canvas
          ref={canvasRef}
          width={canvasWidth * (window.devicePixelRatio || 1)}
          height={canvasHeight * (window.devicePixelRatio || 1)}
          style={{ width: '100%', aspectRatio: `${canvasWidth} / ${canvasHeight}` }}
          className={cx(
            'touch-none rounded-md bg-slate-50 dark:bg-slate-950',
            hovered ? 'cursor-pointer' : 'cursor-grab',
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
              if (Math.abs(dx) > 3 || Math.abs(dy) > 3) drag.moved = true;

              const rect = e.currentTarget.getBoundingClientRect();
              viewRef.current.offsetX += dx * (canvasWidth / rect.width);
              viewRef.current.offsetY += dy * (canvasHeight / rect.height);
              drag.x = e.clientX;
              drag.y = e.clientY;
              scheduleDraw();
              return;
            }

            const p = toGraphSpace(e.clientX, e.clientY);
            if (!p) return;
            const found = nodeAt(p.x, p.y);
            if (found?.id !== hoverRef.current?.id) {
              hoverRef.current = found;
              setHovered(found);
              scheduleDraw();
            }
          }}
          onPointerUp={(e) => {
            const drag = dragRef.current;
            dragRef.current = null;
            e.currentTarget.releasePointerCapture(e.pointerId);

            // Kéo để di chuyển thì không tính là bấm chọn.
            if (!drag || drag.moved) return;

            const p = toGraphSpace(e.clientX, e.clientY);
            const hit = p ? nodeAt(p.x, p.y) : null;

            // Bấm để *chọn*, không phải để rời trang. Mở chi tiết là một cú bấm riêng
            // ở bảng bên dưới: rời trang ngay khi chạm vào một chấm khiến không thể
            // xem xét đồ thị, mà xem xét mới là việc của màn này.
            selectedRef.current = hit && hit.id === selectedRef.current?.id ? null : hit;
            setSelected(selectedRef.current);
            scheduleDraw();
          }}
          onPointerLeave={() => {
            dragRef.current = null;
            hoverRef.current = null;
            setHovered(null);
            scheduleDraw();
          }}
          onDoubleClick={(e) => {
            const p = toGraphSpace(e.clientX, e.clientY);
            const hit = p ? nodeAt(p.x, p.y) : null;
            if (hit) onOpen(hit.id);
          }}
          onWheel={(e) => {
            const view = viewRef.current;
            const factor = e.deltaY < 0 ? 1.12 : 1 / 1.12;
            const next = Math.min(Math.max(view.scale * factor, 0.2), 5);

            // Phóng to quanh vị trí con trỏ chứ không quanh gốc tọa độ, để thứ đang
            // nhìn không trượt ra khỏi khung.
            const rect = e.currentTarget.getBoundingClientRect();
            const px = (e.clientX - rect.left) * (canvasWidth / rect.width);
            const py = (e.clientY - rect.top) * (canvasHeight / rect.height);
            view.offsetX = px - ((px - view.offsetX) / view.scale) * next;
            view.offsetY = py - ((py - view.offsetY) / view.scale) * next;
            view.scale = next;
            scheduleDraw();
          }}
        />

        <div className="absolute right-2 top-2 flex flex-col gap-1">
          <Button className="px-2 py-1 text-xs" aria-label="Phóng to" onClick={() => zoomBy(1.3)}>
            +
          </Button>
          <Button className="px-2 py-1 text-xs" aria-label="Thu nhỏ" onClick={() => zoomBy(1 / 1.3)}>
            −
          </Button>
          <Button className="px-2 py-1 text-xs" aria-label="Vừa khung" onClick={fitToView}>
            ⤢
          </Button>
        </div>
      </div>

      <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-slate-500 dark:text-slate-400">
        {Object.entries(statusLabels).map(([key, label]) => (
          <span key={key}>
            <span
              aria-hidden
              className="mr-1 inline-block size-2 rounded-full"
              style={{ backgroundColor: statusFill[key] }}
            />
            {label}
          </span>
        ))}
        <span className="ml-auto">
          Bấm để chọn · bấm đúp để mở · kéo để di chuyển · lăn chuột để phóng to
        </span>
      </div>

      {selected ? (
        <SelectedPanel
          node={selected}
          neighbours={selectedNeighbours}
          onOpen={onOpen}
          onClear={() => {
            selectedRef.current = null;
            setSelected(null);
            scheduleDraw();
          }}
          onSelect={(node) => {
            selectedRef.current = node;
            setSelected(node);
            scheduleDraw();
          }}
        />
      ) : (
        <p className="mt-2 rounded-md bg-slate-100 px-3 py-2 text-sm text-slate-600 dark:bg-slate-800 dark:text-slate-300">
          {hovered ? (
            <>
              <span className="domain-name font-medium">{hovered.name}</span>
              <span className="ml-2 text-xs text-slate-500 dark:text-slate-400">
                {hovered.degree} quan hệ — bấm để xem
              </span>
            </>
          ) : (
            'Bấm vào một nút để xem quan hệ của nó. Nhãn chỉ hiện ở chỗ còn trống, nên phóng to sẽ thấy thêm tên.'
          )}
        </p>
      )}
    </div>
  );
}

/**
 * Bảng thông tin cho nút đang chọn.
 *
 * Có nó thì việc bấm vào một nút mới có ích: đồ thị dày đến mấy, danh sách hàng xóm ở
 * đây vẫn đọc được, và bấm tiếp một hàng là đi sang nút đó mà không rời màn hình.
 */
function SelectedPanel({
  node,
  neighbours,
  onOpen,
  onClear,
  onSelect,
}: {
  node: NetworkNode;
  neighbours: { node: NetworkNode; kind: string }[];
  onOpen: (id: number) => void;
  onClear: () => void;
  onSelect: (node: NetworkNode) => void;
}) {
  return (
    <div className="mt-2 rounded-md border border-slate-200 bg-slate-50 p-3 dark:border-slate-700 dark:bg-slate-900">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <span
          aria-hidden
          className="size-2.5 shrink-0 rounded-full"
          style={{ backgroundColor: statusFill[node.status] ?? '#94a3b8' }}
        />
        <span className="domain-name text-sm font-medium">{node.name}</span>
        <span className="text-xs text-slate-500 dark:text-slate-400">
          {statusLabels[node.status] ?? node.status}
          {node.category && ` · ${node.category}`} · {node.degree} quan hệ
        </span>

        <div className="ml-auto flex gap-2">
          <Button className="px-2 py-1 text-xs" onClick={() => onOpen(node.id)}>
            Mở chi tiết
          </Button>
          <Button className="px-2 py-1 text-xs" onClick={onClear}>
            Bỏ chọn
          </Button>
        </div>
      </div>

      {neighbours.length > 0 && (
        <ul className="mt-2 flex flex-wrap gap-1">
          {neighbours.slice(0, 24).map(({ node: n, kind }) => (
            <li key={n.id}>
              <button
                type="button"
                onClick={() => onSelect(n)}
                title={`${n.name} — ${relationLabels[kind] ?? kind}`}
                className={cx(
                  'domain-name rounded px-1.5 py-0.5 text-xs',
                  'bg-white ring-1 ring-slate-200 hover:ring-sky-500',
                  'dark:bg-slate-800 dark:ring-slate-700 dark:hover:ring-sky-500',
                )}
              >
                <span
                  aria-hidden
                  className="mr-1 inline-block size-1.5 rounded-full align-middle"
                  style={{ backgroundColor: statusFill[n.status] ?? '#94a3b8' }}
                />
                {n.name.length > 30 ? `${n.name.slice(0, 28)}…` : n.name}
              </button>
            </li>
          ))}
          {neighbours.length > 24 && (
            <li className="self-center text-xs text-slate-500 dark:text-slate-400">
              … và {neighbours.length - 24} nút nữa
            </li>
          )}
        </ul>
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

  const rows = [...grouped.entries()].sort((a, b) => b[1].length - a[1].length).slice(0, 60);

  return (
    <details className="mt-3">
      <summary className="cursor-pointer text-xs text-slate-500 hover:text-slate-700 dark:text-slate-400 dark:hover:text-slate-200">
        Xem dạng bảng ({edges.length} liên kết)
      </summary>
      <table className="mt-2 w-full text-sm">
        <thead>
          <tr className="border-b border-slate-200 text-left text-xs uppercase tracking-wide text-slate-500 dark:border-slate-800 dark:text-slate-400">
            <th scope="col" className="py-1">
              Domain
            </th>
            <th scope="col" className="py-1">
              Liên kết tới
            </th>
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
