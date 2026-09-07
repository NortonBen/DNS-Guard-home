import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';

import type { Bucket } from '@/api/types';
import { formatNumber, formatTime } from '@/lib/format';

/** Biểu đồ truy vấn và truy vấn bị chặn theo giờ. */
export function QueryChart({ buckets }: { buckets: Bucket[] }) {
  const data = buckets.map((b) => ({
    time: formatTime(b.t),
    'Tổng truy vấn': b.queries,
    'Bị chặn': b.blocked,
  }));

  return (
    <ResponsiveContainer width="100%" height={224}>
      <AreaChart data={data} margin={{ top: 4, right: 4, bottom: 0, left: -12 }}>
        <defs>
          <linearGradient id="totalFill" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="#0284c7" stopOpacity={0.35} />
            <stop offset="100%" stopColor="#0284c7" stopOpacity={0.02} />
          </linearGradient>
          <linearGradient id="blockedFill" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="#dc2626" stopOpacity={0.35} />
            <stop offset="100%" stopColor="#dc2626" stopOpacity={0.02} />
          </linearGradient>
        </defs>

        <CartesianGrid strokeDasharray="3 3" stroke="currentColor" className="text-slate-200 dark:text-slate-800" />
        <XAxis dataKey="time" tick={{ fontSize: 11 }} stroke="currentColor" className="text-slate-400" />
        <YAxis tick={{ fontSize: 11 }} stroke="currentColor" className="text-slate-400" width={56} />
        <Tooltip
          formatter={(value) => formatNumber(Number(value))}
          contentStyle={{ fontSize: 12, borderRadius: 6 }}
        />
        <Area
          type="monotone"
          dataKey="Tổng truy vấn"
          stroke="#0284c7"
          strokeWidth={1.5}
          fill="url(#totalFill)"
        />
        <Area
          type="monotone"
          dataKey="Bị chặn"
          stroke="#dc2626"
          strokeWidth={1.5}
          fill="url(#blockedFill)"
        />
      </AreaChart>
    </ResponsiveContainer>
  );
}
