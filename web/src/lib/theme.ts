import { useCallback, useEffect, useState } from 'react';

/**
 * Chế độ sáng/tối.
 *
 * Lựa chọn lưu trong localStorage; khi chưa chọn thì theo thiết lập hệ thống. Lần áp
 * dụng *đầu tiên* không nằm ở đây mà ở script đồng bộ trong index.html — nó chạy
 * trước khi trình duyệt vẽ, nên không có nháy màu lúc tải lại trang.
 */

const STORAGE_KEY = 'dnsguard.theme';

function systemPrefersDark(): boolean {
  return window.matchMedia('(prefers-color-scheme: dark)').matches;
}

function readStoredTheme(): 'dark' | 'light' | null {
  try {
    const saved = localStorage.getItem(STORAGE_KEY);
    return saved === 'dark' || saved === 'light' ? saved : null;
  } catch {
    // Chế độ riêng tư của trình duyệt có thể chặn localStorage.
    return null;
  }
}

export function useTheme() {
  // Đọc từ chính thẻ <html>: script trong index.html đã đặt class trước khi React
  // chạy, nên đây là nguồn sự thật duy nhất và không lệch nhau.
  const [dark, setDark] = useState(() => document.documentElement.classList.contains('dark'));

  useEffect(() => {
    document.documentElement.classList.toggle('dark', dark);
    try {
      localStorage.setItem(STORAGE_KEY, dark ? 'dark' : 'light');
    } catch {
      /* Không lưu được thì vẫn dùng được trong phiên hiện tại. */
    }
  }, [dark]);

  // Khi người dùng chưa chọn thủ công, đổi thiết lập hệ thống phải đổi theo.
  useEffect(() => {
    if (readStoredTheme() !== null) return;

    const media = window.matchMedia('(prefers-color-scheme: dark)');
    const onChange = (e: MediaQueryListEvent) => setDark(e.matches);
    media.addEventListener('change', onChange);
    return () => media.removeEventListener('change', onChange);
  }, []);

  const toggle = useCallback(() => setDark((v) => !v), []);

  return { dark, toggle, systemPrefersDark };
}
