import {
  createRootRoute,
  createRoute,
  createRouter,
  lazyRouteComponent,
} from '@tanstack/react-router';

import { AppShell } from '@/components/layout/app-shell';
import { LoginScreen } from '@/routes/login';
import { DashboardScreen } from '@/routes/dashboard';

const rootRoute = createRootRoute({ component: AppShell });

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  component: DashboardScreen,
});

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/login',
  component: LoginScreen,
});

/*
 * Các màn hình còn lại nạp động.
 *
 * Ngân sách bundle ban đầu là 300 KB gzip, và phần lớn phiên làm việc chỉ dùng
 * dashboard với màn duyệt. Đồ thị quan hệ cùng biểu đồ là hai thứ nặng nhất, nên
 * chúng chỉ tải khi người dùng thực sự mở trang cần tới.
 */
const triageRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/triage',
  component: lazyRouteComponent(() => import('@/routes/triage'), 'TriageScreen'),
});

const domainsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/domains',
  component: lazyRouteComponent(() => import('@/routes/domains'), 'DomainsScreen'),
  validateSearch: (search: Record<string, unknown>) => ({
    // Bộ lọc nằm trong URL: người quản trị cần gửi link "xem cái này" cho chính mình
    // sau, hoặc lưu lại một truy vấn hay dùng. Nó cũng làm nút back hoạt động đúng.
    q: (search.q as string) || '',
    status: (search.status as string) || '',
    category: (search.category as string) || '',
    sort: (search.sort as string) || 'score:desc',
  }),
});

const domainDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/domains/$domainId',
  component: lazyRouteComponent(() => import('@/routes/domain-detail'), 'DomainDetailScreen'),
});

const categoriesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/categories',
  component: lazyRouteComponent(() => import('@/routes/categories'), 'CategoriesScreen'),
});

const manualRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/manual',
  component: lazyRouteComponent(() => import('@/routes/manual'), 'ManualScreen'),
});

const sourcesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/sources',
  component: lazyRouteComponent(() => import('@/routes/sources'), 'SourcesScreen'),
});

const publishRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/publish',
  component: lazyRouteComponent(() => import('@/routes/publish'), 'PublishScreen'),
});

const settingsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/settings',
  component: lazyRouteComponent(() => import('@/routes/settings'), 'SettingsScreen'),
});

const lookupRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/lookup',
  component: lazyRouteComponent(() => import('@/routes/lookup'), 'LookupScreen'),
});

const routeTree = rootRoute.addChildren([
  indexRoute,
  loginRoute,
  triageRoute,
  domainsRoute,
  domainDetailRoute,
  categoriesRoute,
  manualRoute,
  sourcesRoute,
  publishRoute,
  settingsRoute,
  lookupRoute,
]);

export const router = createRouter({ routeTree, defaultPreload: 'intent' });

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}
