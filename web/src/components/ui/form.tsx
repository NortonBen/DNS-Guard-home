import type {
  InputHTMLAttributes,
  ReactNode,
  SelectHTMLAttributes,
  TextareaHTMLAttributes,
} from 'react';

import { cx } from './primitives';

/**
 * Điều khiển nhập liệu dùng chung.
 *
 * Trước khi có file này, mỗi màn hình tự viết lại chuỗi class cho input và select —
 * mười chín lần, với ba chiều cao khác nhau. Hệ quả không chỉ là lặp code: hai điều
 * khiển cạnh nhau ở hai màn hình khác nhau trông lệch nhau, và không có chỗ nào để
 * sửa một lần cho tất cả.
 */

/** Class nền dùng chung cho mọi ô nhập, để input và select luôn cùng chiều cao. */
const controlBase =
  'w-full rounded-md px-3 py-1.5 text-sm ' +
  'ring-1 ring-slate-300 dark:bg-slate-800 dark:ring-slate-700 ' +
  'disabled:cursor-not-allowed disabled:opacity-60';

interface FieldProps {
  label: string;
  /** Trùng với id của điều khiển bên trong. */
  htmlFor: string;
  /** Ghi chú dưới điều khiển, ví dụ cú pháp tìm kiếm. */
  hint?: ReactNode;
  required?: boolean;
  className?: string;
  children: ReactNode;
}

/**
 * Bọc nhãn, điều khiển và ghi chú thành một khối.
 *
 * Ghi chú nằm *dưới* điều khiển, nên khối này phải được đặt trong lưới căn theo mép
 * trên (`items-start`). Nếu căn theo mép dưới, khối có ghi chú sẽ đẩy điều khiển của
 * nó lên cao hơn các điều khiển bên cạnh — đúng lỗi mà thanh bộ lọc từng mắc.
 */
export function Field({ label, htmlFor, hint, required, className, children }: FieldProps) {
  return (
    <div className={cx('min-w-0', className)}>
      <label
        htmlFor={htmlFor}
        className="mb-1 block text-xs font-medium text-slate-600 dark:text-slate-300"
      >
        {label}
        {required && (
          <span className="ml-0.5 text-red-600 dark:text-red-400" aria-hidden>
            *
          </span>
        )}
      </label>

      {children}

      {hint && <p className="mt-1 text-xs text-slate-500 dark:text-slate-400">{hint}</p>}
    </div>
  );
}

type TextInputProps = InputHTMLAttributes<HTMLInputElement> & { mono?: boolean };

/** Ô nhập một dòng. `mono` cho tên miền và những thứ cần so sánh ký tự. */
export function TextInput({ mono, className, ...props }: TextInputProps) {
  return <input className={cx(controlBase, mono && 'font-mono', className)} {...props} />;
}

/** Ô chọn. Dùng chung `controlBase` để không lệch chiều cao so với ô nhập. */
export function Select({
  className,
  children,
  ...props
}: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select className={cx(controlBase, className)} {...props}>
      {children}
    </select>
  );
}

/** Ô nhập nhiều dòng. */
export function Textarea({ className, ...props }: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea className={cx(controlBase, 'py-2', className)} {...props} />;
}

/** Ô đánh dấu, kích thước thống nhất trên toàn ứng dụng. */
export function Checkbox({ className, ...props }: InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      type="checkbox"
      className={cx(
        'size-4 shrink-0 rounded accent-sky-600 disabled:cursor-not-allowed disabled:opacity-60',
        className,
      )}
      {...props}
    />
  );
}

/**
 * Hàng điều khiển của thanh công cụ.
 *
 * Lưới chứ không phải flex, và căn theo mép trên: mọi điều khiển bắt đầu ở cùng một
 * đường, bất kể khối nào có ghi chú dài hơn. Cột đầu rộng gấp đôi vì nó chứa ô tìm
 * kiếm; các cột còn lại chia đều.
 */
export function FilterBar({ children }: { children: ReactNode }) {
  return (
    <div
      className={cx(
        'grid items-start gap-x-3 gap-y-3',
        'sm:grid-cols-2',
        'xl:grid-cols-[minmax(0,2fr)_repeat(3,minmax(0,1fr))]',
      )}
    >
      {children}
    </div>
  );
}
