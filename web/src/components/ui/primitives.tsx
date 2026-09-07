import type { ButtonHTMLAttributes, ReactNode } from 'react';

import type { DomainStatus } from '@/api/types';
import { statusColors, statusLabels } from '@/lib/strings';

/** Ghép class có điều kiện, bỏ qua giá trị rỗng. */
export function cx(...parts: (string | false | null | undefined)[]): string {
  return parts.filter(Boolean).join(' ');
}

type ButtonVariant = 'primary' | 'secondary' | 'danger' | 'ghost';

const buttonStyles: Record<ButtonVariant, string> = {
  primary: 'bg-sky-600 text-white hover:bg-sky-700 disabled:bg-sky-300 dark:disabled:bg-sky-900',
  secondary:
    'bg-white text-slate-700 ring-1 ring-slate-300 hover:bg-slate-50 ' +
    'dark:bg-slate-800 dark:text-slate-200 dark:ring-slate-700 dark:hover:bg-slate-700',
  danger: 'bg-red-600 text-white hover:bg-red-700 disabled:bg-red-300 dark:disabled:bg-red-900',
  ghost: 'text-slate-600 hover:bg-slate-100 dark:text-slate-300 dark:hover:bg-slate-800',
};

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  /** Phím tắt tương ứng, hiện trong tooltip để người dùng học dần. */
  hotkey?: string;
}

export function Button({ variant = 'secondary', hotkey, className, ...props }: ButtonProps) {
  return (
    <button
      type="button"
      // Mọi hành động có phím tắt cũng phải bấm được bằng chuột: phím tắt là lối tắt,
      // không phải cách duy nhất.
      title={hotkey ? `${props.title ?? ''} (${hotkey})`.trim() : props.title}
      className={cx(
        'inline-flex items-center justify-center gap-1.5 rounded-md px-3 py-1.5',
        'text-sm font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-60',
        buttonStyles[variant],
        className,
      )}
      {...props}
    />
  );
}

export function StatusBadge({ status }: { status: DomainStatus }) {
  return (
    <span
      className={cx(
        'inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium',
        statusColors[status],
      )}
    >
      {statusLabels[status]}
    </span>
  );
}

interface CardProps {
  title?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
}

export function Card({ title, actions, children, className }: CardProps) {
  return (
    <section
      className={cx(
        'rounded-lg bg-white ring-1 ring-slate-200',
        'dark:bg-slate-900 dark:ring-slate-800',
        className,
      )}
    >
      {(title || actions) && (
        <header className="flex items-center justify-between gap-3 border-b border-slate-200 px-4 py-2.5 dark:border-slate-800">
          <h2 className="text-sm font-semibold text-slate-700 dark:text-slate-200">{title}</h2>
          {actions}
        </header>
      )}
      <div className="p-4">{children}</div>
    </section>
  );
}

export function Spinner({ label = 'Đang tải' }: { label?: string }) {
  return (
    <div className="flex items-center gap-2 py-6 text-sm text-slate-500 dark:text-slate-400">
      <span
        aria-hidden
        className="size-4 animate-spin rounded-full border-2 border-slate-300 border-t-sky-600"
      />
      <span role="status">{label}…</span>
    </div>
  );
}

/** Khung xương chờ dữ liệu, dùng cho phần tải theo lớp ở trang chi tiết. */
export function Skeleton({ className }: { className?: string }) {
  return (
    <div
      aria-hidden
      className={cx('animate-pulse rounded bg-slate-200 dark:bg-slate-800', className)}
    />
  );
}

export function EmptyState({ children }: { children: ReactNode }) {
  return (
    <p className="py-8 text-center text-sm text-slate-500 dark:text-slate-400">{children}</p>
  );
}

export function ErrorState({ error }: { error: unknown }) {
  const message = error instanceof Error ? error.message : 'Đã có lỗi xảy ra';
  return (
    <p
      role="alert"
      className="rounded-md bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950 dark:text-red-300"
    >
      {message}
    </p>
  );
}
