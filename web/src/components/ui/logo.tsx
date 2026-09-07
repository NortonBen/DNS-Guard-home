import { useId } from 'react';

/**
 * Nhận diện DNSGuard: một chiếc khiên bọc lấy cây phân cấp domain.
 *
 * Khiên là phần "guard", cây ba nút là phần "domain làm trung tâm" — đúng thứ phân
 * biệt sản phẩm này với một resolver. Hình vẽ nội tuyến chứ không dùng <img>: không
 * tốn thêm một lượt tải, và nét vẽ luôn sắc ở mọi tỉ lệ màn hình.
 *
 * Bản dùng cho favicon và biểu tượng ứng dụng nằm ở `web/public/`; sửa hình ở đây thì
 * phải sửa cả ở đó.
 */

interface LogoMarkProps {
  className?: string;
}

export function LogoMark({ className }: LogoMarkProps) {
  // Thanh bên và thanh trên cùng có thể hiện đồng thời. Hai thẻ <svg> trùng id
  // gradient thì trình duyệt lấy định nghĩa đầu tiên, và bản bị ẩn đi làm hỏng bản
  // còn lại khi nó bị gỡ khỏi DOM — nên mỗi lần vẽ phải có id riêng.
  const gradientId = useId();

  return (
    <svg viewBox="0 0 64 64" className={className} aria-hidden="true" focusable="false">
      <defs>
        <linearGradient id={gradientId} x1="10" y1="6" x2="54" y2="58" gradientUnits="userSpaceOnUse">
          <stop offset="0" stopColor="#38bdf8" />
          <stop offset="0.55" stopColor="#0ea5e9" />
          <stop offset="1" stopColor="#0369a1" />
        </linearGradient>
      </defs>
      <path
        d="M32 6.4 53 13.9V31.5C53 43.4 44.6 52.3 32 57.1 19.4 52.3 11 43.4 11 31.5V13.9Z"
        fill={`url(#${gradientId})`}
        stroke={`url(#${gradientId})`}
        strokeWidth="3.4"
        strokeLinejoin="round"
      />
      <g fill="none" stroke="#fff" strokeWidth="3.2" strokeLinecap="round" strokeLinejoin="round">
        <path d="M32 29v6.6" />
        <path d="M21.6 38v-2.4h20.8V38" />
      </g>
      <g fill="#fff">
        <circle cx="32" cy="24.4" r="4.9" />
        <circle cx="21.6" cy="41.6" r="4.1" />
        <circle cx="42.4" cy="41.6" r="4.1" />
      </g>
    </svg>
  );
}

interface LogoProps {
  /** Cỡ chữ đặt ở đây; biểu tượng đo bằng `em` nên tự lớn nhỏ theo. */
  className?: string;
}

/** Biểu tượng kèm chữ. Dùng ở thanh bên, thanh trên cùng và màn hình đăng nhập. */
export function Logo({ className }: LogoProps) {
  return (
    <span className={className}>
      <span className="inline-flex items-center gap-2 font-semibold tracking-tight">
        <LogoMark className="h-[1.35em] w-[1.35em] shrink-0" />
        <span>
          DNS<span className="text-sky-600 dark:text-sky-400">Guard</span>
        </span>
      </span>
    </span>
  );
}
