import { useEffect, useState } from "react"
import { API_BASE, fetchWithTimeout, headers, safeJson } from "../api"
import type { AuditEvent, SystemInfo } from "../types"
import { AuditList, Status, titleCase } from "./shared"

export function System() {
  const [info, setInfo] = useState<SystemInfo | null>(null)
  const [audit, setAudit] = useState<AuditEvent[]>([])
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(true)

  async function load() {
    setLoading(true); setError("")
    try {
      const [systemResponse, auditResponse] = await Promise.all([fetchWithTimeout(`${API_BASE}/api/system`, { headers, cache: "no-store" }), fetchWithTimeout(`${API_BASE}/api/audit/recent?limit=12`, { headers, cache: "no-store" })])
      const system = await safeJson(systemResponse)
      if (!systemResponse.ok) throw new Error(system.error ?? "system request failed")
      setInfo(system)
      if (auditResponse.ok) setAudit((await safeJson(auditResponse)).events ?? [])
    } catch (err) { setError(err instanceof Error ? err.message : "system request failed") }
    finally { setLoading(false) }
  }

  useEffect(() => { void load() }, [])

  return (
    <div className="system-layout">
      <section className="section-block"><div className="section-heading"><div><span className="section-label">Runtime inventory</span><h2>Models and services</h2></div><button type="button" className="secondary small" onClick={load} disabled={loading}>Refresh</button></div>{error && <div className="error-box" role="alert"><strong>System metadata unavailable</strong><span>{error}</span></div>}{loading && !info && <div className="empty-state"><strong>Loading runtime metadata</strong><span>Querying the local gateway and specialist services.</span></div>}{info && <><div className="runtime-facts"><div><span>Processing</span><strong>{info.processing}</strong></div><div><span>Profile</span><strong>{info.profile}</strong></div><div><span>Per-user limit</span><strong>{info.limits.requests_per_minute_per_user} RPM</strong></div><div><span>Concurrency</span><strong>{info.limits.max_concurrent_requests}</strong></div><div><span>Max request</span><strong>{Math.round(info.limits.max_body_bytes / 1024 / 1024)} MB</strong></div></div><div className="model-table">{Object.entries(info.models).map(([role, model]) => <div className="model-row" key={role}><span>{titleCase(role)}</span><strong>{model}</strong><Status state={info.services[serviceKeyForModel(role)] ?? (role === "asr" ? info.services.translation : "ready")} /></div>)}</div></>}</section>
      <section className="section-block"><div className="section-heading"><div><span className="section-label">Operational trace</span><h2>Recent audit events</h2></div><span className="muted">Identity redacted in browser view</span></div><AuditList events={audit} empty="No recent gateway events." /></section>
    </div>
  )
}

function serviceKeyForModel(role: string) {
  if (role === "agent") return "agent"
  if (role === "translation" || role === "asr") return "translation"
  if (role === "forecasting") return "forecasting"
  if (role === "radiology") return "radiology"
  return role
}
