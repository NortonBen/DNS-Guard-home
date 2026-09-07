import {
  Area,
  AreaChart,
  CartesianGrid,
  Line,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';

import type { ResourcePoint } from '@/api/types';
import { formatBytes } from '@/lib/format';

/**
 * Nhãn trục ngang đổi theo khoảng xem.
 *
 * Chỉ hiện giờ phút thì ở khoảng ba mươi ngày mọi nhãn đều lặp lại và không cho biết
 * đang xem ngày nào. Chỉ hiện ngày thì ở khoảng mười hai giờ tất cả nhãn giống hệt
 * nhau. Nên phải chọn theo độ rộng khoảng gộp máy chủ trả về.
 */
function axisLabel(iso: string, bucketSeconds: number): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return '';

  if (bucketSeconds >= 3600) {
    return date.toLocaleDateString('vi-VN', { day: '2-digit', month: '2-digit' });
  }
  if (bucketSeconds >= 900) {
    return date.toLocaleString('vi-VN', {
      day: '2-digit',
      month: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    });
  }
  return date.toLocaleTimeString('vi-VN', { hour: '2-digit', minute: '2-digit' });
}

const gridClass = 'text-slate-200 dark:text-slate-800';

interface ChartProps {
  points: ResourcePoint[];
  bucketSeconds: number;
}

/**
 * Biểu đồ CPU: vùng tô là trung bình, đường mảnh là đỉnh.
 *
 * Vẽ cả hai vì chúng trả lời hai câu khác nhau. Trung bình cho biết máy có đang gánh
 * nặng lâu dài không; đỉnh cho biết có lúc nào chạm trần không — và sau khi gộp
 * khoảng, đỉnh là thứ duy nhất còn giữ lại được các nhịp tăng ngắn.
 */
export function CPUChart({ points, bucketSeconds }: ChartProps) {
  const data = points.map((p) => ({
    time: axisLabel(p.t, bucketSeconds),
    'Trung bình': Number(p.cpu_avg.toFixed(2)),
    'Đỉnh': Number(p.cpu_max.toFixed(2)),
  }));

  return (
    <ResponsiveContainer width="100%" height={224}>
      <AreaChart data={data} margin={{ top: 4, right: 4, bottom: 0, left: -12 }}>
        <defs>
          <linearGradient id="cpuFill" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="#0284c7" stopOpacity={0.35} />
            <stop offset="100%" stopColor="#0284c7" stopOpacity={0.02} />
          </linearGradient>
        </defs>

        <CartesianGrid strokeDasharray="3 3" stroke="currentColor" className={gridClass} />
        <XAxis
          dataKey="time"
          tick={{ fontSize: 11 }}
          stroke="currentColor"
          className="text-slate-400"
          minTickGap={24}
        />
        <YAxis
          tick={{ fontSize: 11 }}
          stroke="currentColor"
          className="text-slate-400"
          width={48}
          unit="%"
        />
        <Tooltip
          formatter={(value) => `${Number(value).toFixed(2).replace('.', ',')} %`}
          contentStyle={{ fontSize: 12, borderRadius: 6 }}
        />
        <Area
          type="monotone"
          dataKey="Trung bình"
          stroke="#0284c7"
          strokeWidth={1.5}
          fill="url(#cpuFill)"
        />
        <Line type="monotone" dataKey="Đỉnh" stroke="#f59e0b" strokeWidth={1} dot={false} />
      </AreaChart>
    </ResponsiveContainer>
  );
}

/**
 * Biểu đồ bộ nhớ: thường trú (hệ điều hành thấy) và heap (Go tự quản).
 *
 * Hai đường tách nhau ra là dấu hiệu đáng chú ý: heap phẳng mà thường trú tăng đều
 * nghĩa là runtime đang giữ lại bộ nhớ đã trả, chứ không phải mã nguồn rò rỉ.
 */
export function MemoryChart({ points, bucketSeconds }: ChartProps) {
  const data = points.map((p) => ({
    time: axisLabel(p.t, bucketSeconds),
    'Thường trú': p.rss_avg,
    'Đỉnh thường trú': p.rss_max,
    'Heap Go': p.heap_avg,
  }));

  return (
    <ResponsiveContainer width="100%" height={224}>
      <AreaChart data={data} margin={{ top: 4, right: 4, bottom: 0, left: -12 }}>
        <defs>
          <linearGradient id="rssFill" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="#7c3aed" stopOpacity={0.35} />
            <stop offset="100%" stopColor="#7c3aed" stopOpacity={0.02} />
          </linearGradient>
        </defs>

        <CartesianGrid strokeDasharray="3 3" stroke="currentColor" className={gridClass} />
        <XAxis
          dataKey="time"
          tick={{ fontSize: 11 }}
          stroke="currentColor"
          className="text-slate-400"
          minTickGap={24}
        />
        <YAxis
          tick={{ fontSize: 11 }}
          stroke="currentColor"
          className="text-slate-400"
          width={64}
          tickFormatter={(value) => formatBytes(Number(value))}
        />
        <Tooltip
          formatter={(value) => formatBytes(Number(value))}
          contentStyle={{ fontSize: 12, borderRadius: 6 }}
        />
        <Area
          type="monotone"
          dataKey="Thường trú"
          stroke="#7c3aed"
          strokeWidth={1.5}
          fill="url(#rssFill)"
        />
        <Line
          type="monotone"
          dataKey="Đỉnh thường trú"
          stroke="#f59e0b"
          strokeWidth={1}
          dot={false}
        />
        <Line type="monotone" dataKey="Heap Go" stroke="#059669" strokeWidth={1} dot={false} />
      </AreaChart>
    </ResponsiveContainer>
  );
}
