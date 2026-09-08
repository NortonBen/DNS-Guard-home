// Kiểu dữ liệu API, phản chiếu các struct Go ở internal/store và internal/api.
//
// Khai báo tay thay vì sinh từ OpenAPI: bề mặt API vừa đủ nhỏ để giữ đồng bộ bằng
// mắt, và một bước sinh code nữa là một thứ nữa có thể hỏng trong quá trình build.
// Đổi tên trường ở Go thì phải sửa ở đây — TypeScript sẽ báo lỗi tại nơi dùng.

export type DomainStatus = 'new' | 'staging' | 'blocked' | 'allowed' | 'ignored';
export type DomainOrigin = 'discovered' | 'manual' | 'public_list' | 'related';
export type UserRole = 'admin' | 'viewer';

export type DecisionAction =
  | 'block'
  | 'allow'
  | 'ignore'
  | 'recategorize'
  | 'stage'
  | 'expire';

export interface Category {
  key: string;
  label_vi: string;
  color: string;
}

export interface Signal {
  kind: string;
  weight: number;
  detail?: Record<string, unknown>;
}

export interface Domain {
  id: number;
  name: string;
  etld1: string;
  status: DomainStatus;
  origin: DomainOrigin;
  category: Category | null;
  score: number | null;
  confidence: number | null;
  is_manual: boolean;
  is_wildcard: boolean;
  query_count: number;
  client_count: number;
  first_seen: string;
  last_seen: string;
  staged_at?: string;
  blocked_at?: string;
  scored_at?: string;
  enriched_at?: string;
  signals: Signal[] | null;
}

export interface Decision {
  id: number;
  domain_id: number;
  action: DecisionAction;
  actor_label: string;
  reason: string;
  snapshot?: Record<string, unknown>;
  created_at: string;
}

export interface DomainFacts {
  dns?: { cname_chain?: string[]; a?: string[]; fetched_at?: string; error?: string };
  asn?: { asn?: number; org?: string; country?: string; ip?: string; error?: string };
  cert?: { issuer?: string; sans?: string[]; not_after?: string; error?: string };
  rdap?: { registered_at?: string; registrar?: string; age_days?: number; error?: string };
  rank?: { tranco?: number; error?: string };
}

/**
 * Địa chỉ quan sát được trên dây từ bản ghi trả lời DNS.
 *
 * Khác `facts.asn`: chỗ đó là kết quả DNSGuard tự phân giải lúc làm giàu, còn đây là
 * câu trả lời thiết bị trong mạng thật sự nhận được.
 */
export interface DomainIP {
  ip: string;
  first_seen: string;
  last_seen: string;
  hits: number;
  ttl: number;
  asn?: number;
  country?: string;
  org?: string;
  /** Dải trong danh sách hạ tầng độc hại đã khớp. Vắng mặt nghĩa là sạch. */
  threat?: string;
}

export interface DomainDetail {
  domain: Domain;
  facts: DomainFacts;
  siblings: Domain[];
  history: Decision[];
  public_lists: { source_id: number; name: string; category: string }[];
  protected: boolean;
  protect_rule: string;
  ips: DomainIP[];
  /** Tên các file xuất bản thật sự chứa domain này. Rỗng khi nó không nằm ở đâu cả. */
  published_files: string[];
}

/** Một domain đã phân giải tới địa chỉ nằm trong danh sách hạ tầng độc hại. */
export interface ThreatMatch {
  domain_id: number;
  name: string;
  status: DomainStatus;
  ip: string;
  threat: string;
  last_seen: string;
  hits: number;
  country?: string;
  org?: string;
  /** Domain được hỏi theo nhịp đều bất thường — dấu hiệu kênh điều khiển tự động. */
  beacon: boolean;
}

export interface ThreatList {
  matches: ThreatMatch[];
  /**
   * `false` nghĩa là chưa tải danh sách về, nên `matches` rỗng vì không đối chiếu
   * được — không phải vì không có gì đáng báo. Hai trường hợp trái ngược nhau.
   */
  list_loaded: boolean;
}

export interface IPInvestigation {
  ip: string;
  domains: {
    domain_id: number;
    name: string;
    status: DomainStatus;
    first_seen: string;
    last_seen: string;
    hits: number;
  }[];
  accesses: { client: string; domain: string; occurred_at: string }[];
}

export interface GraphNode {
  id: number;
  name: string;
  status: DomainStatus;
  category?: string;
  root?: boolean;
}

export interface GraphEdge {
  from: number;
  to: number;
  kind: 'cname_to' | 'same_asn' | 'same_cert' | 'co_occurs';
  strength: number;
}

export interface RelationGraph {
  nodes: GraphNode[];
  edges: GraphEdge[];
  truncated: boolean;
}

export interface Page<T> {
  items: T[];
  next_cursor: string;
  has_more: boolean;
}

export interface CategoryDetail {
  id: number;
  key: string;
  label_vi: string;
  label_en: string;
  description: string;
  color: string;
  enabled: boolean;
  score_threshold: number;
  publish_path: string;
  sort_order: number;
  /** Mọi domain mang nhãn này, ở bất kỳ trạng thái nào. */
  domain_count: number;
  /** Riêng phần đang chặn. */
  blocked_count: number;
  /** Số dòng thật sự nằm trong file xuất bản; bằng 0 khi tắt xuất bản. */
  published_count: number;
}

export interface WeightEntry {
  kind: string;
  weight: number;
  default: number;
  label_vi: string;
  is_infra: boolean;
}

export interface WeightImpact {
  would_block: { count: number; sample: string[] };
  would_unblock: { count: number; sample: string[] };
  unchanged: number;
  /** Số domain đã đưa vào tính toán; cho biết mẫu rộng tới đâu. */
  evaluated?: number;
}

export interface RuleThresholds {
  fanout_min_clients: number;
  beacon_min_queries: number;
  spread_high_min: number;
  spread_mid_min: number;
  vt_malicious_min_engines: number;
  high_rank_max: number;
}

/** Phần luật do người vận hành thêm vào, gộp lên trên luật dựng sẵn. */
export interface CustomRules {
  adtech_domains: Record<string, string>;
  adtech_asns: Record<string, { org: string; category: string }>;
  shared_cdn: string[];
  keywords: Record<string, string[]>;
  thresholds: RuleThresholds;
}

export interface RulesResponse {
  custom: CustomRules;
  /** Số mục dựng sẵn, để giao diện nói rõ "67 sẵn có, bạn thêm 3". */
  builtin_counts: Record<string, number>;
  default_thresholds: RuleThresholds;
}

/** Một mục bị từ chối kèm lý do đọc được. */
export interface RuleRejection {
  field: string;
  value: string;
  reason: string;
}

export interface ListSource {
  id: number;
  name: string;
  url: string;
  format: 'hosts' | 'adblock' | 'plain';
  category: string;
  enabled: boolean;
  sync_interval: number;
  last_sync_at: string | null;
  last_status: string;
  last_error?: string;
  entry_count: number;
  prev_count: number;
}

export interface SourceOverlap {
  sources: ListSource[];
  pairs: { a: number; b: number; shared: number; jaccard: number }[];
  unique: { source_id: number; only_here: number }[];
}

export interface Snapshot {
  id: number;
  category: string;
  entry_count: number;
  checksum: string;
  file_path: string;
  published_at: string;
  published_by: string;
}

export interface SnapshotDiff {
  added: { count: number; sample: string[] };
  removed: { count: number; sample: string[] };
}

export interface Bucket {
  t: string;
  queries: number;
  blocked: number;
  clients?: number;
}

export interface TopEntry {
  key: string;
  label?: string;
  queries: number;
  status?: string;
  category?: string;
  domain_id?: number;
}

export interface Overview {
  total_queries: number;
  blocked_queries: number;
  blocked_ratio: number;
  unique_domains: number;
  unique_clients: number;
  pending_review: number;
  unblock_requests: number;
  by_category: { key: string; queries: number }[];
  timeseries: Bucket[];
}

export interface HealthCheck {
  ok: boolean;
  message?: string;
  latency_ms?: number;
  last_event_age_s?: number;
  pending?: number;
  failed_24h?: number;
  last_publish_age_s?: number;
  packets_received?: number;
  packets_dropped?: number;
  decode_errors?: number;
}

export interface Health {
  status: 'ok' | 'degraded' | 'down';
  checks: Record<string, HealthCheck>;
}

export interface Job {
  id: string;
  kind: string;
  state: 'pending' | 'running' | 'done' | 'failed' | 'cancelled';
  attempt: number;
  progress: { done: number; total: number };
  error?: string;
  started_at: string | null;
  finished_at: string | null;
  created_at: string;
}

export interface AddDomainResult {
  name: string;
  action: 'created' | 'duplicate' | 'rejected';
  id?: number;
  code?: string;
  message?: string;
  matches_known?: number;
}

export interface AddDomainResponse {
  results: AddDomainResult[];
  summary: { created: number; duplicate: number; rejected: number };
}

export interface UnblockRequest {
  id: number;
  domain: string;
  note: string;
  requested_by: string;
  state: 'pending' | 'accepted' | 'rejected';
  created_at: string;
}

export interface Session {
  user: { id: number; username: string; role: UserRole };
  csrf_token: string;
}

export interface ApiErrorBody {
  error: { code: string; message: string; details?: Record<string, unknown> };
}

export interface LookupTable {
  kind: 'asn' | 'rank';
  label: string;
  describes: string;
  loaded: boolean;
  entries: number;
  path: string;
  loaded_at?: string;
  default_url: string;
}

export interface SystemInfo {
  version: string;
  db_path: string;
  db_size_bytes: number;
  lists_dir: string;
  listen: string;
  tzsp_listen: string;
  external_enabled: boolean;
  log_retention_days: number;
  hourly_retention_days: number;
  keep_snapshots: number;
  publish_min_ratio: number;
  lists_allow_cidr: string[];
  auto_migrate: boolean;
}

export interface AnalysisSettings {
  http_enabled: boolean;
  /** Công tắc cứng ở biến môi trường; bật trong giao diện cũng không thắng được nó. */
  external_enabled: boolean;
  http_effective: boolean;
  vt_configured: boolean;
  /** Bốn ký tự cuối của khóa. Khóa đầy đủ không bao giờ rời khỏi máy chủ. */
  vt_key_hint: string;
  /** Khóa đến từ biến môi trường, nên giao diện không sửa được. */
  vt_from_env: boolean;
}

export interface PublishSettings {
  /** Địa chỉ mọi domain bị chặn trỏ về trong file hosts. */
  sink_address: string;
  /** Giá trị từ biến môi trường, dùng khi chưa đặt gì trên giao diện. */
  sink_default: string;
}

export interface Settings {
  protect: { hard: string[]; soft: string[] };
  analysis: AnalysisSettings;
  publish: PublishSettings;
  lifecycle: {
    staging_days: number;
    confirm_ttl_days: number;
    staging_days_default: number;
    confirm_ttl_days_default: number;
  };
  lookup_tables: LookupTable[];
  system: SystemInfo;
}

export interface NetworkGraphNode {
  id: number;
  name: string;
  etld1: string;
  status: DomainStatus;
  category?: string;
  /** Số quan hệ của nút; giao diện vẽ nút to nhỏ theo giá trị này. */
  degree: number;
}

export interface NetworkGraph {
  nodes: NetworkGraphNode[];
  edges: GraphEdge[];
  total_nodes: number;
  total_edges: number;
  truncated: boolean;
}

/** Một khoảng đã gộp trên biểu đồ tài nguyên. */
export interface ResourcePoint {
  t: string;
  cpu_avg: number;
  cpu_max: number;
  rss_avg: number;
  rss_max: number;
  heap_avg: number;
  goroutines: number;
}

export interface ResourceSummary {
  samples: number;
  cpu_avg: number;
  cpu_max: number;
  rss_avg: number;
  rss_max: number;
  oldest_at?: string;
  retain_days: number;
}

export interface ResourceSeries {
  from: string;
  to: string;
  /** Độ rộng khoảng gộp máy chủ đã chọn; hiện ra để người xem biết mình đang nhìn gì. */
  bucket_seconds: number;
  sample_seconds: number;
  summary: ResourceSummary;
  points: ResourcePoint[];
}

/* ---- Hỏi AI ---- */

/** Trạng thái nhà cung cấp model. Không bao giờ chứa khóa API. */
export interface AIStatus {
  /** false khi máy chủ chạy mà không mở được CSDL nhật ký AI. */
  available: boolean;
  enabled?: boolean;
  configured?: boolean;
  base_url?: string;
  model?: string;
  max_tokens?: number;
  batch_size?: number;
  /** Công tắc cứng ở biến môi trường: bật trên giao diện cũng không thắng được nó. */
  external_enabled?: boolean;
  db_path?: string;
  usage_30d?: AIUsage;
}

export interface AIUsage {
  requests: number;
  domains: number;
  verdicts: number;
  failed: number;
  prompt_chars: number;
  reply_chars: number;
  last_at?: string;
}

export type AIRequestKind = 'classify' | 'recheck' | 'ask';

/** Một lượt gọi model đã ghi lại. Danh sách không kèm prompt và phản hồi thô. */
export interface AIRequest {
  id: number;
  kind: AIRequestKind;
  model: string;
  base_url: string;
  domain_count: number;
  parsed_count: number;
  skipped_count: number;
  prompt?: string;
  response?: string;
  prompt_chars: number;
  reply_chars: number;
  latency_ms: number;
  error?: string;
  actor: string;
  created_at: string;
}

export interface AIVerdict {
  id: number;
  request_id: number;
  domain: string;
  category: string;
  confidence: number;
  reason: string;
  created_at: string;
}

/** Một lần model gọi công cụ, để người dùng thấy câu trả lời dựa trên dữ liệu nào. */
export interface AIStep {
  tool: string;
  args: string;
  result: string;
}

export interface AIAnswer {
  chat_id: number;
  answer: string;
  steps: AIStep[];
  skills: string[];
  warnings: string[];
}

export interface AIChat {
  id: number;
  title: string;
  actor: string;
  created_at: string;
  updated_at: string;
}

export interface AIChatMessage {
  id: number;
  chat_id: number;
  role: 'user' | 'assistant';
  content: string;
  steps: AIStep[];
  created_at: string;
}

export interface AISkill {
  id: number;
  name: string;
  description: string;
  triggers: string[];
  content: string;
  /** Skill "luôn dùng" được chèn vào mọi câu hỏi, không cần khớp từ khóa. */
  always: boolean;
  enabled: boolean;
  /** Skill dựng sẵn tắt được nhưng không xoá được. */
  builtin: boolean;
  created_at: string;
  updated_at: string;
}

export interface AIMCPServer {
  id: number;
  name: string;
  url: string;
  /** Token không bao giờ ra khỏi máy chủ; chỉ biết có hay không. */
  has_auth: boolean;
  enabled: boolean;
  note: string;
  created_at: string;
  updated_at: string;
}

export interface AITool {
  name: string;
  description: string;
}
