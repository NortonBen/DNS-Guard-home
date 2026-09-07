import {
  useMutation,
  useQuery,
  useQueryClient,
  useInfiniteQuery,
  type UseQueryOptions,
} from '@tanstack/react-query';

import { api, query } from './client';
import type {
  AddDomainResponse,
  CategoryDetail,
  Domain,
  DomainDetail,
  Health,
  Job,
  ListSource,
  Overview,
  Page,
  RelationGraph,
  AnalysisSettings,
  NetworkGraph,
  Session,
  Settings,
  Snapshot,
  SnapshotDiff,
  SourceOverlap,
  TopEntry,
  UnblockRequest,
  WeightEntry,
  WeightImpact,
} from './types';

/**
 * Khóa cache. Gom về một chỗ để việc vô hiệu hóa sau mỗi thay đổi nhắm đúng phạm vi
 * thay vì xóa sạch cache — xóa sạch làm cả giao diện nháy lại một lượt.
 */
export const qk = {
  session: () => ['session'] as const,
  domains: (filters: DomainFilters) => ['domains', filters] as const,
  domain: (id: number) => ['domain', id] as const,
  graph: (id: number, kinds: string) => ['domain', id, 'graph', kinds] as const,
  network: (filters: NetworkFilters) => ['network-graph', filters] as const,
  timeline: (id: number) => ['domain', id, 'timeline'] as const,
  categories: () => ['categories'] as const,
  weights: () => ['weights'] as const,
  sources: () => ['sources'] as const,
  overlap: () => ['sources', 'overlap'] as const,
  snapshots: (category: string) => ['snapshots', category] as const,
  overview: (hours: number) => ['stats', 'overview', hours] as const,
  top: (dimension: string, hours: number) => ['stats', 'top', dimension, hours] as const,
  health: () => ['health'] as const,
  unblockRequests: () => ['unblock-requests'] as const,
  settings: () => ['settings'] as const,
  job: (id: string) => ['job', id] as const,
};

export interface NetworkFilters {
  kinds: string;
  status: string;
  limit: number;
  min_degree: number;
}

export interface DomainFilters {
  status?: string;
  category?: string;
  q?: string;
  min_score?: number;
  sort?: string;
  limit?: number;
}

// ————— Phiên —————

export function useSession(options?: Partial<UseQueryOptions<Session>>) {
  return useQuery({
    queryKey: qk.session(),
    queryFn: () => api<Session>('/auth/me'),
    retry: false,
    staleTime: 5 * 60_000,
    ...options,
  });
}

export function useLogin() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (vars: { username: string; password: string }) =>
      api<Session & { expires_at: string }>('/auth/login', { method: 'POST', body: vars }),
    onSuccess: () => client.invalidateQueries({ queryKey: qk.session() }),
  });
}

export function useLogout() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: () => api<void>('/auth/logout', { method: 'POST' }),
    onSuccess: () => client.clear(),
  });
}

// ————— Domain —————

/** Danh sách domain với phân trang con trỏ, dùng cho bảng ảo hóa. */
export function useDomains(filters: DomainFilters) {
  return useInfiniteQuery({
    queryKey: qk.domains(filters),
    initialPageParam: '',
    queryFn: ({ pageParam }) =>
      api<Page<Domain>>(`/domains${query({ ...filters, cursor: pageParam as string })}`),
    getNextPageParam: (last) => (last.has_more ? last.next_cursor : undefined),
  });
}

export function useDomain(id: number, enabled = true) {
  return useQuery({
    queryKey: qk.domain(id),
    queryFn: () => api<DomainDetail>(`/domains/${id}`),
    enabled: enabled && id > 0,
  });
}

export function useDomainGraph(id: number, kinds: string[], enabled = true) {
  const joined = kinds.join(',');
  return useQuery({
    queryKey: qk.graph(id, joined),
    queryFn: () => api<RelationGraph>(`/domains/${id}/graph${query({ kinds: joined, limit: 60 })}`),
    enabled: enabled && id > 0,
  });
}

export function useDomainTimeline(id: number, enabled = true) {
  return useQuery({
    queryKey: qk.timeline(id),
    queryFn: () =>
      api<{ buckets: { t: string; queries: number; clients: number }[]; by_client?: TopEntry[] }>(
        `/domains/${id}/timeline`,
      ),
    enabled: enabled && id > 0,
  });
}

export interface DecisionVars {
  id: number;
  action: 'block' | 'allow' | 'ignore' | 'recategorize';
  reason: string;
  category?: string;
}

/**
 * Ghi một quyết định.
 *
 * Cập nhật lạc quan là bắt buộc ở màn duyệt: thao tác phím phải phản hồi tức thì,
 * chờ máy chủ trả lời rồi mới đổi giao diện là đủ chậm để phá nhịp gõ phím.
 */
export function useDecision() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: DecisionVars) =>
      api<Domain>(`/domains/${id}/decision`, { method: 'POST', body }),
    onSettled: (_data, _error, vars) => {
      client.invalidateQueries({ queryKey: qk.domain(vars.id) });
      client.invalidateQueries({ queryKey: ['domains'] });
      client.invalidateQueries({ queryKey: ['stats'] });
    },
  });
}

export function useBulkDecision() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (body: {
      domain_ids: number[];
      action: string;
      reason: string;
      category?: string;
    }) =>
      api<{ applied: number; skipped: { id: number; code: string; message: string }[] }>(
        '/domains/bulk-decision',
        { method: 'POST', body },
      ),
    onSettled: () => {
      client.invalidateQueries({ queryKey: ['domains'] });
      client.invalidateQueries({ queryKey: ['domain'] });
    },
  });
}

export function useAddDomains() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (body: {
      names: string[];
      category: string;
      reason: string;
      status?: string;
      dry_run?: boolean;
      force?: boolean;
    }) => api<AddDomainResponse>('/domains', { method: 'POST', body }),
    onSettled: (_data, _error, vars) => {
      if (!vars.dry_run) client.invalidateQueries({ queryKey: ['domains'] });
    },
  });
}

export function useLookup(name: string) {
  return useQuery({
    queryKey: ['lookup', name],
    queryFn: () =>
      api<{ name: string; blocked: boolean; category?: { key: string; label_vi: string } }>(
        `/domains/lookup${query({ name })}`,
      ),
    enabled: name.length > 3 && name.includes('.'),
  });
}

// ————— Phân loại và trọng số —————

export function useCategories() {
  return useQuery({
    queryKey: qk.categories(),
    queryFn: () => api<{ items: CategoryDetail[] }>('/categories'),
    // Phân loại gần như không đổi; giữ lâu để các trang khác không phải tải lại.
    staleTime: 5 * 60_000,
  });
}

export function useUpdateCategory() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: { id: number } & Partial<CategoryDetail>) =>
      api<{ items: CategoryDetail[] }>(`/categories/${id}`, { method: 'PATCH', body }),
    onSuccess: () => client.invalidateQueries({ queryKey: qk.categories() }),
  });
}

export function useWeights() {
  return useQuery({
    queryKey: qk.weights(),
    queryFn: () => api<{ weights: WeightEntry[] }>('/scoring/weights'),
  });
}

/** Tính trước tác động của bộ trọng số mới. Không ghi gì. */
export function usePreviewWeights() {
  return useMutation({
    mutationFn: (weights: { kind: string; weight: number }[]) =>
      api<{ impact: WeightImpact }>('/scoring/weights', {
        method: 'PUT',
        body: { weights, dry_run: true },
      }),
  });
}

export function useApplyWeights() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (weights: { kind: string; weight: number }[]) =>
      api<{ job_id: string }>('/scoring/weights', {
        method: 'PUT',
        body: { weights, dry_run: false },
      }),
    onSuccess: () => {
      client.invalidateQueries({ queryKey: qk.weights() });
      client.invalidateQueries({ queryKey: ['domains'] });
    },
  });
}

// ————— Nguồn ngoài —————

export function useSources() {
  return useQuery({
    queryKey: qk.sources(),
    queryFn: () => api<{ items: ListSource[] }>('/sources'),
  });
}

export function useSourceOverlap(enabled: boolean) {
  return useQuery({
    queryKey: qk.overlap(),
    queryFn: () => api<SourceOverlap>('/sources/overlap'),
    enabled,
  });
}

export function useCreateSource() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (body: Partial<ListSource>) =>
      api<ListSource>('/sources', { method: 'POST', body }),
    onSuccess: () => client.invalidateQueries({ queryKey: qk.sources() }),
  });
}

export function useUpdateSource() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: { id: number } & Partial<ListSource>) =>
      api<ListSource>(`/sources/${id}`, { method: 'PATCH', body }),
    onSuccess: () => client.invalidateQueries({ queryKey: qk.sources() }),
  });
}

export function useDeleteSource() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api<void>(`/sources/${id}`, { method: 'DELETE' }),
    onSuccess: () => client.invalidateQueries({ queryKey: qk.sources() }),
  });
}

export function useSyncSource() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api<{ job_id: string }>(`/sources/${id}/sync`, { method: 'POST' }),
    onSuccess: () => client.invalidateQueries({ queryKey: qk.sources() }),
  });
}

// ————— Xuất bản —————

export function useSnapshots(category: string) {
  return useQuery({
    queryKey: qk.snapshots(category),
    queryFn: () => api<{ items: Snapshot[] }>(`/snapshots${query({ category, limit: 30 })}`),
  });
}

export function useSnapshotDiff(a: number | null, b: number | null) {
  return useQuery({
    queryKey: ['snapshot-diff', a, b],
    queryFn: () => api<SnapshotDiff>(`/snapshots/${a}/diff/${b}`),
    enabled: a !== null && b !== null && a !== b,
  });
}

export function usePublish() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (categories: string[]) =>
      api<{ job_id: string }>('/publish', { method: 'POST', body: { categories } }),
    onSuccess: () => client.invalidateQueries({ queryKey: ['snapshots'] }),
  });
}

export function useRollback() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api<unknown>(`/snapshots/${id}/rollback`, { method: 'POST' }),
    onSuccess: () => client.invalidateQueries({ queryKey: ['snapshots'] }),
  });
}

// ————— Thống kê và vận hành —————

export function useOverview(hours = 24) {
  return useQuery({
    queryKey: qk.overview(hours),
    queryFn: () =>
      api<Overview>(
        `/stats/overview${query({ from: new Date(Date.now() - hours * 3600_000).toISOString() })}`,
      ),
    staleTime: 30_000,
  });
}

export function useTop(dimension: string, hours = 24) {
  return useQuery({
    queryKey: qk.top(dimension, hours),
    queryFn: () =>
      api<{ items: TopEntry[] }>(
        `/stats/top${query({
          dimension,
          limit: 10,
          from: new Date(Date.now() - hours * 3600_000).toISOString(),
        })}`,
      ),
    staleTime: 30_000,
  });
}

/**
 * Sức khỏe hệ thống.
 *
 * Gọi thẳng /health chứ không qua /api/v1: endpoint này phục vụ giám sát ngoài và
 * phải trả lời được kể cả khi phần còn lại của API gặp vấn đề.
 */
export function useHealth() {
  return useQuery({
    queryKey: qk.health(),
    queryFn: async (): Promise<Health> => {
      const response = await fetch('/health', { credentials: 'same-origin' });
      return (await response.json()) as Health;
    },
    refetchInterval: 60_000,
  });
}

export function useJob(id: string | null) {
  return useQuery({
    queryKey: qk.job(id ?? ''),
    queryFn: () => api<Job>(`/jobs/${id}`),
    enabled: !!id,
    // Job chạy nền: hỏi lại mỗi giây cho tới khi xong, rồi dừng.
    refetchInterval: (q) => {
      const state = q.state.data?.state;
      return state === 'pending' || state === 'running' ? 1000 : false;
    },
  });
}

export function useUnblockRequests(enabled: boolean) {
  return useQuery({
    queryKey: qk.unblockRequests(),
    queryFn: () => api<{ items: UnblockRequest[] }>('/unblock-requests'),
    enabled,
  });
}

export function useResolveUnblockRequest() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ id, accept, reason }: { id: number; accept: boolean; reason: string }) =>
      api<{ state: string }>(`/unblock-requests/${id}/resolve`, {
        method: 'POST',
        body: { accept, reason },
      }),
    onSuccess: () => {
      client.invalidateQueries({ queryKey: qk.unblockRequests() });
      client.invalidateQueries({ queryKey: ['stats'] });
    },
  });
}

export function useCreateUnblockRequest() {
  return useMutation({
    mutationFn: (body: { domain: string; note: string }) =>
      api<{ status: string }>('/unblock-requests', { method: 'POST', body }),
  });
}

export function useProtectLists() {
  return useQuery({
    queryKey: ['protect'],
    queryFn: () => api<{ hard: string[]; soft: string[] }>('/settings/protect'),
  });
}

export function useUpdateProtectList() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (soft: string[]) =>
      api<{ soft: string[] }>('/settings/protect', { method: 'PUT', body: { soft } }),
    onSuccess: () => client.invalidateQueries({ queryKey: ['protect'] }),
  });
}

// ————— Cài đặt —————

export function useSettings() {
  return useQuery({
    queryKey: qk.settings(),
    queryFn: () => api<Settings>('/settings'),
  });
}

export function useUpdateLifecycle() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (body: { staging_days?: number; confirm_ttl_days?: number }) =>
      api<{ staging_days: number; confirm_ttl_days: number }>('/settings/lifecycle', {
        method: 'PUT',
        body,
      }),
    onSuccess: () => client.invalidateQueries({ queryKey: qk.settings() }),
  });
}

/**
 * Tải lại một bảng tra cứu cục bộ.
 *
 * Trả về job_id chứ không đợi xong: bảng ASN khoảng mười megabyte và có thể mất vài
 * phút trên đường truyền chậm. Giao diện theo dõi tiến độ qua useJob.
 */
export function useUpdateAnalysis() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (body: { http_enabled: boolean }) =>
      api<AnalysisSettings>('/settings/analysis', { method: 'PUT', body }),
    onSuccess: () => client.invalidateQueries({ queryKey: qk.settings() }),
  });
}

export function useRefreshLookup() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ kind, url }: { kind: string; url?: string }) =>
      api<{ job_id: string }>(`/settings/lookup/${kind}/refresh`, {
        method: 'POST',
        body: { url: url ?? '' },
      }),
    onSuccess: () => client.invalidateQueries({ queryKey: qk.settings() }),
  });
}

/**
 * Đồ thị quan hệ của toàn mạng.
 *
 * Giữ lâu hơn các truy vấn khác: cạnh co_occurs tính lại theo lô hằng đêm, nên hỏi
 * lại sau vài chục giây cũng cho đúng kết quả cũ.
 */
export function useNetworkGraph(filters: NetworkFilters) {
  return useQuery({
    queryKey: qk.network(filters),
    queryFn: () => api<NetworkGraph>(`/graph${query({ ...filters })}`),
    staleTime: 5 * 60_000,
  });
}
