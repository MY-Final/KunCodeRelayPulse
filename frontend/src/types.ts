export type StatusCode = 0 | 1 | 2

export interface DailyBucket {
  day: number
  total: number
  up: number
  yellow: number
  avg_latency_ms: number
}

export interface WindowStat {
  window: string
  total: number
  up: number
  yellow: number
  avg_latency_ms: number
}

export interface ProbePoint {
  status: StatusCode
  sub_status?: string
  http_code?: number
  latency_ms?: number
  ts: number
}

export interface StatusStreak {
  kind: "success" | "degraded" | "anomaly" | string
  count: number
  consecutive_successes: number
  consecutive_degraded: number
  consecutive_failures: number
  consecutive_anomalies: number
  failure_threshold: number
  recovery_threshold: number
  updated_at: number
}

export interface TargetStatus {
  model: string
  status?: StatusCode
  current_status?: StatusCode
  streak?: StatusStreak
  sub_status?: string
  http_code?: number
  latency_ms?: number
  checked_at?: number
  error?: string
  windows: WindowStat[] | null
  daily: DailyBucket[] | null
  history: ProbePoint[] | null
}

export type TrendWindow = "24h" | "7d" | "90d"
export type TrendMetric = "p50_latency_ms" | "p95_latency_ms" | "p99_latency_ms" | "avg_latency_ms"

export interface LatencyPoint {
  ts: number
  samples: number
  avg_latency_ms: number | null
  p50_latency_ms: number | null
  p95_latency_ms: number | null
  p99_latency_ms: number | null
}

export interface LatencyTrend {
  channel_id: string
  model: string
  window: TrendWindow
  bucket_seconds: number
  points: LatencyPoint[]
}

export interface ChannelStatus {
  id: string
  name: string
  template?: string
  hidden?: boolean
  disabled?: boolean
  targets: TargetStatus[] | null
}

export interface ProviderStatus {
  name: string
  channels: ChannelStatus[] | null
}

export interface StatusEvent {
  type: "down" | "up"
  provider: string
  channel: string
  model?: string
  detail?: string
  ts: number
}

export interface StatusPayload {
  view: "public" | "admin"
  generated_at: number
  site_title: string
  providers: ProviderStatus[]
  events: StatusEvent[] | null
}

export interface AdminChannel {
  id: string
  provider: string
  name: string
  hidden: boolean
  disabled: boolean
  interval: string
  template: string
  base_url: string
  proxy?: string
  proxy_name?: string
  api_key_env?: string
  api_key_set: boolean
  models: string[]
  revision: number
}

export interface AdminChannelsPayload {
  channels: AdminChannel[]
  templates: string[]
  proxies: AdminProxy[]
}

export interface AdminProxy {
  id: string
  name: string
  url_preview?: string
  url_set: boolean
  has_auth: boolean
  revision: number
}

export interface ProxyTestResult {
  ok: boolean
  http_code?: number
  latency_ms?: number
  error?: string
}

export interface ChannelWriteInput {
  provider: string
  name: string
  hidden: boolean
  disabled: boolean
  interval: string
  template: string
  base_url: string
  proxy: string
  api_key_env: string
  models: string[]
  api_key?: string
  clear_api_key?: boolean
  revision?: number
}

export interface ProbeResult {
  channel_id: string
  model: string
  status: StatusCode
  sub_status?: string
  http_code?: number
  latency_ms?: number
  error?: string
  ts: number
}

export interface ProxyWriteInput {
  name: string
  url?: string
  revision?: number
}

export interface AdminSettings {
  interval: string
  timeout: string
  slow_latency: string
  failure_threshold: number
  recovery_threshold: number
}
