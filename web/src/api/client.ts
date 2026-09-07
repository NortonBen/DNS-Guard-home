import type { ApiErrorBody } from './types';

/**
 * Lỗi API đã bóc tách, giữ nguyên mã lỗi để nơi gọi phân nhánh theo nghiệp vụ thay
 * vì so khớp chuỗi thông báo.
 */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
    readonly details?: Record<string, unknown>,
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

/**
 * CSRF token lấy từ `GET /auth/me` và giữ trong bộ nhớ.
 *
 * Không lưu vào localStorage: token chỉ có ý nghĩa trong phiên hiện tại, và mọi thứ
 * nằm trong localStorage đều đọc được bằng script chèn vào trang.
 */
let csrfToken = '';

export function setCsrfToken(token: string): void {
  csrfToken = token;
}

/** Nơi xử lý khi phiên hết hạn, do lớp router gắn vào. */
let onUnauthenticated: (() => void) | null = null;

export function setUnauthenticatedHandler(handler: () => void): void {
  onUnauthenticated = handler;
}

interface RequestOptions {
  method?: string;
  body?: unknown;
  signal?: AbortSignal;
}

/**
 * Gọi API và trả về dữ liệu đã giải mã.
 *
 * Cookie phiên do trình duyệt gửi kèm; chỉ CSRF token phải đính tay vào các phương
 * thức thay đổi trạng thái.
 */
export async function api<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const method = options.method ?? 'GET';
  const headers: Record<string, string> = {};

  if (options.body !== undefined) {
    headers['Content-Type'] = 'application/json';
  }
  if (method !== 'GET' && method !== 'HEAD' && csrfToken) {
    headers['X-CSRF-Token'] = csrfToken;
  }

  const response = await fetch(`/api/v1${path}`, {
    method,
    headers,
    credentials: 'same-origin',
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
    signal: options.signal,
  });

  if (response.status === 401) {
    onUnauthenticated?.();
    throw new ApiError(401, 'unauthenticated', 'Phiên đã hết hạn, vui lòng đăng nhập lại');
  }

  if (!response.ok) {
    // Lỗi của backend luôn có cấu trúc; nhưng một proxy hoặc lỗi mạng ở giữa có thể
    // trả về HTML, nên vẫn phải phòng trường hợp giải mã thất bại.
    let body: ApiErrorBody | null = null;
    try {
      body = (await response.json()) as ApiErrorBody;
    } catch {
      body = null;
    }
    throw new ApiError(
      response.status,
      body?.error.code ?? 'internal',
      body?.error.message ?? `Lỗi HTTP ${response.status}`,
      body?.error.details,
    );
  }

  if (response.status === 204) {
    return undefined as T;
  }
  return (await response.json()) as T;
}

/** Dựng chuỗi truy vấn, bỏ qua giá trị rỗng để URL gọn và dễ đọc. */
export function query(params: Record<string, string | number | boolean | undefined | null>): string {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === '') continue;
    search.set(key, String(value));
  }
  const encoded = search.toString();
  return encoded ? `?${encoded}` : '';
}
