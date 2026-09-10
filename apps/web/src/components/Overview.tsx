import { useEffect, useState } from "react"
import { API_BASE, fetchWithTimeout, headers } from "../api"
import type { AuditEvent, ServiceState, SystemInfo, Tab } from "../types"
import { AuditList, Status } from "./shared"

const serviceNames: Record<string, string> = {
  agent: "Operations assistant",
  translation: "Speech interpreter",
  forecasting: "Demand forecasting",
  radiology: "Imaging screening",
}

export function Overview({ health, onNavigate }: { health: Record<string, ServiceState>; onNavigate: (tab: Tab) => void }) {
  const [info, setInfo] = useState<SystemInfo | null>(null)
  const [audit, setAudit] = useState<AuditEvent[]>([])

  useEffect(() => {
    let active = true
    Promise.all([
      fetchWithTimeout(`${API_BASE}/api/system`, { headers, cache: "no-store" }).then((response) => response.ok ? response.json() : null),
      fetchWithTimeout(`${API_BASE}/api/audit/recent?limit=6`, { headers, cache: "no-store" }).then((response) => response.ok ? response.json() : { events: [] }),
    ]).then(([system, auditPayload]) => {
      if (!active) return
      if (system) setInfo(system)
      setAudit(auditPayload?.events ?? [])
    }).catch(() => undefined)
    return () => { active = false }
  }, [])

  const ready = Object.values(health).filter((state) => state === "ready").length

  return (
    <div className="overview-layout">
      <section className="ops-summary" aria-label="Current environment">
        <div><span>Facility</span><strong>JNF General Hospital</strong><small>Demo environment</small></div>
        <div><span>Services</span><strong>{ready}/4 ready</strong><small>Refreshes every 8 seconds</small></div>
        <div><span>Processing</span><strong>{info?.processing ?? "Local"}</strong><small>{info?.profile ?? "Westmere"} profile</small></div>
        <div><span>Data mode</span><strong>Synthetic</strong><small>No production patient feed</small></div>
      </section>

      <div className="overview-primary-grid">
        <section className="section-block">
          <div className="section-heading"><div><span className="section-label">Services</span><h2>Operational status</h2></div><span className="muted">Live</span></div>
          <div className="service-table">
            {Object.entries(serviceNames).map(([key, label]) => <div className="service-row" key={key}><div><strong>{label}</strong><span>{serviceDescription(key)}</span></div><Status state={health[key] ?? "checking"} /></div>)}
          </div>
        </section>
        <section className="section-block">
          <div className="section-heading"><div><span className="section-label">Activity</span><h2>Recent requests</h2></div><span className="muted">Identity redacted</span></div>
          <AuditList events={audit} empty="No recent gateway activity." />
        </section>
      </div>

      <section className="section-block workflow-section">
        <div className="section-heading"><div><span className="section-label">Workflows</span><h2>Clinical support modules</h2></div><span className="muted">Local execution</span></div>
        <div className="workflow-table">
          <WorkflowRow code="OPS-01" title="Operations query" detail="Ask for forecasts, service state, historical comparisons or supported capabilities in plain language." state={health.agent} action="Open" onClick={() => onNavigate("assistant")} />
          <WorkflowRow code="OPS-02" title="Demand forecast" detail="Inspect patient-arrival, bed-occupancy and disease-incidence scenarios across configurable horizons." state={health.forecasting} action="Open" onClick={() => onNavigate("forecasting")} />
          <WorkflowRow code="OPS-03" title="Speech interpreter" detail="Capture local speech, transcribe it and translate supported languages without a cloud inference dependency." state={health.translation} action="Open" onClick={() => onNavigate("translation")} />
          <WorkflowRow code="OPS-04" title="Chest X-ray screening" detail="Run the local research screening pipeline and review model scores, provenance and required human-review status." state={health.radiology} action="Open" onClick={() => onNavigate("radiology")} />
        </div>
      </section>

      <section className="section-block governance-section">
        <div className="section-heading"><div><span className="section-label">Controls</span><h2>Environment safeguards</h2></div></div>
        <div className="governance-grid">
          <div><span>Processing boundary</span><strong>Local specialist services</strong></div>
          <div><span>Access</span><strong>Doctor demo role</strong></div>
          <div><span>Request audit</span><strong>Route, tool, model, latency</strong></div>
          <div><span>Capacity</span><strong>{info ? `${info.limits.requests_per_minute_per_user} RPM · ${info.limits.max_concurrent_requests} concurrent` : "Rate limited"}</strong></div>
        </div>
      </section>
    </div>
  )
}

function WorkflowRow({ code, title, detail, state, action, onClick }: { code: string; title: string; detail: string; state?: ServiceState; action: string; onClick: () => void }) {
  return <div className="workflow-row"><span className="workflow-code">{code}</span><div><strong>{title}</strong><span>{detail}</span></div><Status state={state ?? "checking"} /><button type="button" className="secondary small" onClick={onClick}>{action}</button></div>
}

function serviceDescription(key: string) {
  if (key === "agent") return "Natural-language requests and tool routing"
  if (key === "translation") return "Local ASR and translation"
  if (key === "forecasting") return "Operational demand and historical backtests"
  if (key === "radiology") return "Local chest-radiograph research pipeline"
  return "Local specialist service"
}
