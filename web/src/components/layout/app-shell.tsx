import { useEffect, useState } from 'react';
import { Link, Outlet, useNavigate, useRouterState } from '@tanstack/react-router';

import { setCsrfToken, setUnauthenticatedHandler } from '@/api/client';
import { useHealth, useLogout, useOverview, useSession } from '@/api/hooks';
import { Button, Spinner, cx } from '@/components/ui/primitives';
import { formatDuration, formatNumber } from '@/lib/format';
import { useTheme } from '@/lib/theme';

interface NavItem {
  to: string;
  label: string;
  adminOnly?: boolean;
  /** Khóa dùng để gắn số đếm bên phải mục, ví dụ số ứng viên chờ duyệt. */
  badge?: 'pending';
}

interface NavGroup {
  label: string;
  items: NavItem[];
}

/**
 * Menu gom theo trình tự công việc thật, không theo bảng chữ cái.
 *
 * Quan sát → điều tra → quyết định → xuất bản → cấu hình. Người dùng hằng ngày chỉ đi
 * hết nhóm hai; nhóm bốn và năm là việc thỉnh thoảng mới đụng tới.
 */
const navigation: NavGroup[] = [
  {
    label: 'Quan sát',
    items: [{ to: '/', label: 'Tổng quan' }],
  },
  {
    label: 'Điều tra',
    items: [
      { to: '/triage', label: 'Hàng đợi duyệt', adminOnly: true, badge: 'pending' },
      { to: '/domains', label: 'Danh sách domain' },
      { to: '/lookup', label: 'Tra cứu nhanh' },
    ],
  },
  {
    label: 'Quyết định',
    items: [
      { to: '/manual', label: 'Thêm thủ công', adminOnly: true },
      { to: '/categories', label: 'Phân loại & trọng số', adminOnly: true },
      { to: '/sources', label: 'Nguồn ngoài', adminOnly: true },
    ],
  },
  {
    label: 'Phát hành',
    items: [{ to: '/publish', label: 'Xuất bản', adminOnly: true }],
  },
  {
    label: 'Hệ thống',
    items: [{ to: '/settings', label: 'Cài đặt', adminOnly: true }],
  },
];

/** Khung ứng dụng: thanh bên, cảnh báo sức khỏe, và cổng đăng nhập. */
export function AppShell() {
  const navigate = useNavigate();
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const session = useSession();
  const [drawerOpen, setDrawerOpen] = useState(false);

  useEffect(() => {
    if (session.data) setCsrfToken(session.data.csrf_token);
  }, [session.data]);

  useEffect(() => {
    setUnauthenticatedHandler(() => {
      setCsrfToken('');
      navigate({ to: '/login' });
    });
  }, [navigate]);

  // Chuyển trang thì đóng ngăn kéo: trên điện thoại nó che hết nội dung.
  useEffect(() => {
    setDrawerOpen(false);
  }, [pathname]);

  if (pathname === '/login') return <Outlet />;

  if (session.isPending) {
    return (
      <div className="grid min-h-dvh place-items-center">
        <Spinner label="Đang kiểm tra phiên" />
      </div>
    );
  }
  if (session.isError || !session.data) return <LoginRedirect />;

  return (
    <div className="min-h-dvh lg:flex">
      {/* Lớp phủ khi mở ngăn kéo trên màn hình nhỏ. */}
      {drawerOpen && (
        <button
          type="button"
          aria-label="Đóng menu"
          onClick={() => setDrawerOpen(false)}
          className="fixed inset-0 z-30 bg-slate-900/50 lg:hidden"
        />
      )}

      <Sidebar
        role={session.data.user.role}
        username={session.data.user.username}
        pathname={pathname}
        open={drawerOpen}
        onClose={() => setDrawerOpen(false)}
      />

      <div className="min-w-0 flex-1">
        <MobileHeader onOpenMenu={() => setDrawerOpen(true)} />
        <HealthBanner />

        <main className="mx-auto max-w-[1500px] px-4 py-5">
          <Outlet />
        </main>
      </div>
    </div>
  );
}

interface SidebarProps {
  role: string;
  username: string;
  pathname: string;
  open: boolean;
  onClose: () => void;
}

function Sidebar({ role, username, pathname, open, onClose }: SidebarProps) {
  const logout = useLogout();
  const navigate = useNavigate();
  const { dark, toggle } = useTheme();

  // Số ứng viên chờ duyệt hiện ngay trên menu: đây là con số quyết định người dùng
  // có cần mở ứng dụng hôm nay hay không.
  const overview = useOverview(24);
  const pending = overview.data?.pending_review ?? 0;

  return (
    <aside
      className={cx(
        'fixed inset-y-0 left-0 z-40 flex w-64 flex-col',
        'border-r border-slate-200 bg-white dark:border-slate-800 dark:bg-slate-900',
        'transition-transform duration-200 lg:static lg:translate-x-0',
        open ? 'translate-x-0' : '-translate-x-full',
      )}
    >
      <div className="flex items-center justify-between px-4 py-3">
        <Link to="/" className="text-base font-semibold tracking-tight">
          DNS<span className="text-sky-600">Guard</span>
        </Link>
        <Button variant="ghost" className="lg:hidden" onClick={onClose} aria-label="Đóng menu">
          ✕
        </Button>
      </div>

      <nav aria-label="Điều hướng chính" className="min-h-0 flex-1 overflow-y-auto px-2 pb-3">
        {navigation.map((group) => {
          const items = group.items.filter((item) => !item.adminOnly || role === 'admin');
          if (items.length === 0) return null;

          return (
            <div key={group.label} className="mb-3">
              <p className="px-2 py-1 text-[11px] font-semibold uppercase tracking-wider text-slate-400 dark:text-slate-500">
                {group.label}
              </p>
              <ul className="space-y-0.5">
                {items.map((item) => {
                  const active = pathname === item.to;
                  const count = item.badge === 'pending' ? pending : 0;

                  return (
                    <li key={item.to}>
                      <Link
                        to={item.to}
                        aria-current={active ? 'page' : undefined}
                        className={cx(
                          'flex items-center justify-between gap-2 rounded-md px-2 py-1.5',
                          'text-sm font-medium transition-colors',
                          active
                            ? 'bg-sky-50 text-sky-700 dark:bg-sky-950 dark:text-sky-300'
                            : 'text-slate-600 hover:bg-slate-100 dark:text-slate-300 dark:hover:bg-slate-800',
                        )}
                      >
                        <span className="truncate">{item.label}</span>
                        {count > 0 && (
                          <span className="shrink-0 rounded-full bg-amber-100 px-1.5 py-0.5 text-xs font-semibold tabular-nums text-amber-800 dark:bg-amber-950 dark:text-amber-300">
                            {formatNumber(count)}
                          </span>
                        )}
                      </Link>
                    </li>
                  );
                })}
              </ul>
            </div>
          );
        })}
      </nav>

      <div className="border-t border-slate-200 px-2 py-2 dark:border-slate-800">
        <div className="flex items-center justify-between gap-2 px-2 py-1">
          <div className="min-w-0">
            <p className="truncate text-sm font-medium">{username}</p>
            <p className="text-xs text-slate-500 dark:text-slate-400">
              {role === 'admin' ? 'Quản trị' : 'Chỉ đọc'}
            </p>
          </div>
          <Button
            variant="ghost"
            onClick={toggle}
            aria-label={dark ? 'Chuyển sang chế độ sáng' : 'Chuyển sang chế độ tối'}
            title={dark ? 'Chế độ sáng' : 'Chế độ tối'}
          >
            {dark ? '☀' : '☾'}
          </Button>
        </div>
        <Button
          variant="ghost"
          className="w-full justify-start"
          onClick={() => logout.mutate(undefined, { onSuccess: () => navigate({ to: '/login' }) })}
        >
          Đăng xuất
        </Button>
      </div>
    </aside>
  );
}

/** Thanh trên cùng, chỉ hiện khi thanh bên bị thu lại. */
function MobileHeader({ onOpenMenu }: { onOpenMenu: () => void }) {
  return (
    <header className="sticky top-0 z-20 flex items-center gap-2 border-b border-slate-200 bg-white/90 px-3 py-2 backdrop-blur lg:hidden dark:border-slate-800 dark:bg-slate-900/90">
      <Button variant="ghost" onClick={onOpenMenu} aria-label="Mở menu">
        ☰
      </Button>
      <span className="text-base font-semibold tracking-tight">
        DNS<span className="text-sky-600">Guard</span>
      </span>
    </header>
  );
}

function LoginRedirect() {
  const navigate = useNavigate();
  useEffect(() => {
    navigate({ to: '/login' });
  }, [navigate]);
  return null;
}

/**
 * Băng cảnh báo khi bộ nhận không hoạt động.
 *
 * Đây là lỗi im lặng nguy hiểm nhất của hệ thống: nếu ngừng nhận truy vấn, mọi thứ
 * vẫn chạy và vẫn chặn bằng danh sách cũ — chỉ là ngừng học. Vì thế nó phải hiện
 * trên đầu mọi trang chứ không nằm sau một cú bấm.
 */
function HealthBanner() {
  const health = useHealth();
  const querylog = health.data?.checks.querylog;

  if (!querylog || querylog.ok) return null;

  // Không mở được cổng là lỗi của chính máy chủ này; không có lưu lượng là lỗi ở
  // thiết bị mạng. Hai nguyên nhân khác nhau nên phải chỉ đúng chỗ cần sửa.
  const listenError = (querylog as { listen_error?: string }).listen_error;
  const age = querylog.last_event_age_s ?? -1;

  return (
    <div
      role="alert"
      className="border-b border-red-200 bg-red-50 px-4 py-2 text-sm text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-200"
    >
      <div className="mx-auto max-w-[1500px]">
        {listenError ? (
          <>
            <strong className="font-semibold">Không mở được cổng nhận TZSP</strong> —{' '}
            <code className="font-mono text-xs">{listenError}</code>. Cổng có thể đang bị tiến
            trình khác chiếm; kiểm tra trên chính máy chủ này.
          </>
        ) : (
          <>
            <strong className="font-semibold">Không nhận được truy vấn DNS mới</strong> —{' '}
            {age < 0 ? 'chưa nhận được truy vấn nào' : `truy vấn gần nhất ${formatDuration(age)} trước`}.
            Kiểm tra bộ mirror trên thiết bị mạng:{' '}
            <code className="font-mono text-xs">/tool sniffer print</code>
          </>
        )}
      </div>
    </div>
  );
}
