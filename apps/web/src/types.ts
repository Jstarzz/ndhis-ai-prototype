export type Tab = "overview" | "assistant" | "translation" | "forecasting" | "radiology" | "system"
export type ServiceState = "ready" | "checking" | "idle" | "offline" | "error" | "degraded" | string

export type ExecutionStage = {
  name: string
  status: "running" | "complete" | "failed" | string
  detail?: string
  duration_ms?: number
  tool?: string
  model?: string
}

export type ChatMessage = {
  role: "user" | "assistant"
  content: string
  latencyMs?: number
  tool?: string
  arguments?: Record<string, unknown>
  requestId?: string
  routing?: "deterministic" | "agent" | string
  intent?: string
  llmCalls?: number
  trace?: ExecutionStage[]
  model?: string
}

export type ForecastPoint = {
  timestamp: string
  date?: string
  forecast: number
  p10: number
  p90: number
  actual?: number
}

export type ForecastBacktest = {
  available: boolean
  points_compared: number
  mae?: number
  rmse?: number
  bias?: number
  interval_coverage?: number
  reason?: string
}

export type ForecastIntensity = {
  expected_per_second: number
  expected_per_minute: number
  expected_per_hour: number
  probability_at_least_one_next_minute: number
  interpretation: string
}

export type ForecastResult = {
  facility: string
  expected: number
  p10: number
  p90: number
  metric: string
  department?: string
  disease?: string
  horizon: { value: number; unit: string; seconds: number }
  horizon_days: number
  resolution: string
  resolution_seconds: number
  resolution_semantics: string
  source_resolution: string
  as_of: string
  effective_history_end: string
  latency_ms: number
  series: ForecastPoint[]
  data_mode: string
  model: string
  backend: string
  intensity?: ForecastIntensity | null
  backtest: ForecastBacktest
  history_points_used: number
  history_start: string
  history_end: string
  data_start: string
  data_end: string
}

export type ForecastCapabilities = {
  facilities: Record<string, string[]>
  metrics: string[]
  diseases: string[]
  horizon_units: string[]
  resolutions: string[]
  max_points: number
  source_resolution: string
  sub_source_resolution_semantics: string
  data_start: string
  data_end: string
  data_mode: string
}

export type HistoryPoint = { timestamp: string; actual: number }
export type ForecastHistory = {
  facility: string
  department?: string
  metric: string
  disease?: string
  start: string
  end: string
  resolution: string
  source_resolution: string
  data_mode: string
  series: HistoryPoint[]
}

export type RadiologyPrediction = { label: string; score: number }
export type RadiologyResult = {
  result_id: string
  filename: string
  findings: string
  predictions?: RadiologyPrediction[] | null
  review_required: boolean
  latency_ms: number
  model?: string
  backend?: string
  device?: string
  data_mode?: string
}

export type SystemInfo = {
  processing: string
  profile: string
  services: Record<string, string>
  models: Record<string, string>
  limits: {
    requests_per_minute_per_user: number
    max_concurrent_requests: number
    max_body_bytes: number
  }
}

export type AuditEvent = {
  timestamp: string
  request_id: string
  route: string
  tool?: string
  model?: string
  status: number
  latency_ms: number
}

export type TranslationHealth = { supported_targets?: string[] }
export type TranslationResult = {
  transcript?: string
  translation?: string
  source_language?: string
  target_language?: string
  latency_ms?: number
  error?: string
}
