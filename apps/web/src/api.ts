const REQUEST_TIMEOUT_MS = 20_000

export const API_BASE = import.meta.env.VITE_API_BASE ?? "http://localhost:8080"
export const DEMO_KEY = import.meta.env.VITE_DEMO_API_KEY ?? "ndhis-local-demo"
export const TRANSLATION_WS = import.meta.env.VITE_TRANSLATION_WS ?? "ws://localhost:8101/ws/translate"
export const TRANSLATION_HEALTH = TRANSLATION_WS.replace(/^ws/, "http").replace(/\/ws\/translate$/, "/health")

export const headers = {
  "Content-Type": "application/json",
  "X-NDHIS-Demo-Key": DEMO_KEY,
  "X-NDHIS-User": "demo-doctor",
  "X-NDHIS-Role": "doctor",
}

export async function fetchWithTimeout(input: RequestInfo | URL, init: RequestInit = {}, timeoutMs = REQUEST_TIMEOUT_MS) {
  const controller = new AbortController()
  const timeout = window.setTimeout(() => controller.abort(), timeoutMs)
  try {
    return await fetch(input, { ...init, signal: controller.signal })
  } finally {
    window.clearTimeout(timeout)
  }
}

export async function safeJson(response: Response): Promise<any> {
  const text = await response.text()
  if (!text) return {}
  try { return JSON.parse(text) } catch { return { error: text.slice(0, 500) } }
}
