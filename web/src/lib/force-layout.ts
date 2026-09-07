/**
 * Bố cục lực cho đồ thị quan hệ.
 *
 * Tự cài đặt thay vì dùng thư viện đồ thị: một thư viện đầy đủ nặng hơn toàn bộ phần
 * còn lại của bundle cộng lại, trong khi thứ cần ở đây chỉ là đẩy nhau và kéo theo
 * cạnh.
 *
 * Điểm khác biệt so với bản viết thẳng trong component: lực đẩy tính qua một lưới
 * không gian thay vì duyệt mọi cặp. Duyệt mọi cặp là O(n²) — ở 60 nút thì không sao,
 * nhưng đồ thị toàn mạng có vài trăm nút và 300 nút nghĩa là 45.000 cặp mỗi bước,
 * nhân 220 bước là mười triệu phép tính. Lưới chỉ so với các ô lân cận nên chi phí
 * gần như tuyến tính theo số nút.
 */

export interface LayoutNode {
  id: number;
  x: number;
  y: number;
  vx: number;
  vy: number;
  /** Nút được ghim không bị lực làm dịch chuyển, dùng cho nút gốc. */
  pinned?: boolean;
}

export interface LayoutEdge {
  from: number;
  to: number;
  strength: number;
}

export interface LayoutOptions {
  width: number;
  height: number;
  /** Số bước mô phỏng trước khi dừng. Đồ thị đã ổn định thì chạy tiếp chỉ tốn pin. */
  steps?: number;
  /** Khoảng cách nghỉ của một cạnh. */
  linkDistance?: number;
  /** Cường độ đẩy nhau giữa hai nút. */
  repulsion?: number;
  /** Bán kính ảnh hưởng của lực đẩy; ngoài khoảng này coi như không tác dụng. */
  repulsionRadius?: number;
}

/**
 * Chạy mô phỏng tới khi ổn định, gọi onTick sau mỗi bước để vẽ lại.
 *
 * Trả về hàm hủy; gọi nó khi component bị gỡ để khung hình đang chờ không chạy tiếp.
 */
export function runForceLayout(
  nodes: LayoutNode[],
  edges: LayoutEdge[],
  options: LayoutOptions,
  onTick: (nodes: LayoutNode[], done: boolean) => void,
): () => void {
  const {
    width,
    height,
    steps = 240,
    linkDistance = 90,
    repulsion = 4200,
    repulsionRadius = 220,
  } = options;

  const byId = new Map<number, LayoutNode>();
  for (const n of nodes) byId.set(n.id, n);

  const cellSize = repulsionRadius;
  const cols = Math.max(1, Math.ceil(width / cellSize));

  let frame = 0;
  let cancelled = false;
  let step = 0;

  function tick() {
    if (cancelled) return;

    // Lưới không gian dựng lại mỗi bước: các nút di chuyển nên ô của chúng đổi theo.
    const grid = new Map<number, LayoutNode[]>();
    for (const n of nodes) {
      const cx = Math.max(0, Math.min(cols - 1, Math.floor(n.x / cellSize)));
      const cy = Math.max(0, Math.floor(n.y / cellSize));
      const key = cy * cols + cx;
      const bucket = grid.get(key);
      if (bucket) bucket.push(n);
      else grid.set(key, [n]);
    }

    // Lực đẩy: chỉ so với các nút trong ô của mình và tám ô kề. Nút ở xa hơn thế
    // đóng góp không đáng kể vì lực giảm theo bình phương khoảng cách.
    for (const n of nodes) {
      const cx = Math.max(0, Math.min(cols - 1, Math.floor(n.x / cellSize)));
      const cy = Math.max(0, Math.floor(n.y / cellSize));

      for (let dy = -1; dy <= 1; dy++) {
        for (let dx = -1; dx <= 1; dx++) {
          const bucket = grid.get((cy + dy) * cols + (cx + dx));
          if (!bucket) continue;

          for (const other of bucket) {
            if (other === n) continue;

            const ox = other.x - n.x;
            const oy = other.y - n.y;
            const distSq = Math.max(ox * ox + oy * oy, 40);
            if (distSq > repulsionRadius * repulsionRadius) continue;

            const dist = Math.sqrt(distSq);
            const force = repulsion / distSq;
            n.vx -= (ox / dist) * force;
            n.vy -= (oy / dist) * force;
          }
        }
      }
    }

    // Lực kéo theo cạnh: cạnh mạnh kéo chặt hơn, nên cụm quan hệ thật tụm lại.
    for (const e of edges) {
      const a = byId.get(e.from);
      const b = byId.get(e.to);
      if (!a || !b) continue;

      const dx = b.x - a.x;
      const dy = b.y - a.y;
      const dist = Math.max(Math.sqrt(dx * dx + dy * dy), 1);
      const pull = (dist - linkDistance) * 0.01 * (0.4 + e.strength * 0.6);
      const fx = (dx / dist) * pull;
      const fy = (dy / dist) * pull;

      a.vx += fx;
      a.vy += fy;
      b.vx -= fx;
      b.vy -= fy;
    }

    // Lực hướng tâm nhẹ: không có nó, các cụm rời rạc trôi ra vô hạn và người dùng
    // phải cuộn đi tìm.
    const centerX = width / 2;
    const centerY = height / 2;

    for (const n of nodes) {
      if (n.pinned) {
        n.x = centerX;
        n.y = centerY;
        n.vx = 0;
        n.vy = 0;
        continue;
      }

      n.vx += (centerX - n.x) * 0.002;
      n.vy += (centerY - n.y) * 0.002;

      n.vx *= 0.82;
      n.vy *= 0.82;
      n.x += n.vx;
      n.y += n.vy;
    }

    step++;
    const done = step >= steps;
    onTick(nodes, done);

    if (!done) frame = requestAnimationFrame(tick);
  }

  frame = requestAnimationFrame(tick);
  return () => {
    cancelled = true;
    cancelAnimationFrame(frame);
  };
}

/**
 * Xếp nút ban đầu theo vòng tròn.
 *
 * Khởi tạo ngẫu nhiên làm đồ thị nhảy khác nhau mỗi lần mở cùng một dữ liệu, và
 * người dùng không nhận ra mình đang xem đúng thứ vừa xem.
 */
export function seedPositions(
  ids: number[],
  width: number,
  height: number,
  rootId?: number,
): LayoutNode[] {
  const cx = width / 2;
  const cy = height / 2;
  const radius = Math.min(width, height) * 0.36;

  return ids.map((id, index) => {
    if (id === rootId) return { id, x: cx, y: cy, vx: 0, vy: 0, pinned: true };

    // Xoắn ốc thay vì vòng tròn đơn: với vài trăm nút, một vòng tròn làm chúng chồng
    // lên nhau và lực đẩy phải mất nhiều bước mới gỡ ra được.
    const angle = index * 2.399963; // góc vàng, trải đều không tạo vệt
    const r = radius * Math.sqrt((index + 1) / ids.length);
    return { id, x: cx + Math.cos(angle) * r, y: cy + Math.sin(angle) * r, vx: 0, vy: 0 };
  });
}
