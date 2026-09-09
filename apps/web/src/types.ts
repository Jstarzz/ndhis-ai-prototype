export type Tab = "overview" | "assistant" | "translation" | "forecasting" | "radiology" | "system"
export type ServiceState = "ready" | "checking" | "idle" | "offline" | "error" | string

export type ChatMessage = {
  role: "user" | "assistant"
  content: string
  latencyMs?: number
  tool?: string
  arguments?: Record<string, unknown>
  requestId?: string
}

export type ForecastPoint = { date: string; forecast: number; p10: number; p90: number }
export type ForecastResult = {
  expected: number
  p10: number
  p90: number
  metric: string
  department?: string
  disease?: string
  latency_ms: number
  series: ForecastPoint[]
  data_mode: string
}

export type RadiologyResult = {
  result_id: string
  filename: string
  findings: string
  review_required: boolean
  latency_ms: number
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
