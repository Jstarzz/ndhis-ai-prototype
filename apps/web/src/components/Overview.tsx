import { useEffect, useState } from "react"
import { API_BASE, fetchWithTimeout, headers } from "../api"
import type { AuditEvent, ServiceState, SystemInfo, Tab } from "../types"
import { AuditList, Status } from "./shared"

const serviceNames: Record<string, string> = {
  agent: "Clinical assistant",
  translation: "Speech translation",
  forecasting: "Demand forecasting",
  radiology: "Radiology support",
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
      <section className="lead-panel">
        <div className="lead-copy"><span className="section-label">Prototype readiness</span><h2>Four local services, one auditable clinical workspace.</h2><p>The assistant routes supported requests to the service that owns the job. Demo forecasting, multilingual intake support, radiology assistance, and runtime governance without cloud inference.</p></div>
        <div className="readiness-score" aria-label={`${ready} of 4 services ready`}><strong>{ready}<span>/4</span></strong><small>services ready</small></div>
      </section>

      <section className="section-block">
        <div className="section-heading"><div><span className="section-label">Live services</span><h2>Runtime status</h2></div><span className="muted">Refreshes every 8 seconds</span></div>
        <div className="service-table">
          {Object.entries(serviceNames).map(([key, label]) => <div className="service-row" key={key}><div><strong>{label}</strong><span>{serviceDescription(key)}</span></div><Status state={health[key] ?? "checking"} /></div>)}
        </div>
      </section>

      <section className="demo-grid" aria-label="Suggested demo paths">
        <DemoAction number="01" title="Ask for a forecast in plain language" detail="Shows the local agent selecting a forecasting tool, executing it, and returning a grounded answer with a trace." action="Open assistant" onClick={() => onNavigate("assistant")} />
        <DemoAction number="02" title="Run a hospital demand scenario" detail="Change department and metric, then inspect the 30-day forecast range and synthetic-data provenance." action="Open forecasting" onClick={() => onNavigate("forecasting")} />
        <DemoAction number="03" title="Demonstrate local clinical speech" detail="Stream microphone audio through local ASR and translation without sending the session to a cloud inference API." action="Open translation" onClick={() => onNavigate("translation")} />
        <DemoAction number="04" title="Review radiology assistance" detail="Upload a public or de-identified study and show explicit human review plus result traceability." action="Open radiology" onClick={() => onNavigate("radiology")} />
      </section>

      <section className="split-grid">
        <div className="section-block">
          <div className="section-heading"><div><span className="section-label">Governance</span><h2>Built-in guardrails</h2></div></div>
          <ul className="guardrail-list">
            <li><strong>Local processing</strong><span>Specialist models remain on controlled infrastructure.</span></li>
            <li><strong>Doctor-only demo role</strong><span>Gateway requires identity headers and rejects other roles.</span></li>
            <li><strong>Auditable requests</strong><span>Routes, tools, model, status and latency are written to the audit log.</span></li>
            <li><strong>Capacity controls</strong><span>{info ? `${info.limits.requests_per_minute_per_user} RPM/user · ${info.limits.max_concurrent_requests} concurrent` : "Rate and concurrency limits enforced."}</span></li>
          </ul>
        </div>
        <div className="section-block"><div className="section-heading"><div><span className="section-label">Recent activity</span><h2>Audit trail</h2></div></div><AuditList events={audit} empty="Run a workflow and its request trace will appear here." /></div>
      </section>
    </div>
  )
}

function DemoAction({ number, title, detail, action, onClick }: { number: string; title: string; detail: string; action: string; onClick: () => void }) {
  return <article className="demo-action"><span className="demo-number">{number}</span><h3>{title}</h3><p>{detail}</p><button type="button" className="text-button" onClick={onClick}>{action} <span aria-hidden="true">→</span></button></article>
}

function serviceDescription(key: string) {
  if (key === "agent") return "Tool routing and grounded responses"
  if (key === "translation") return "ASR plus text translation"
  if (key === "forecasting") return "Operational demand scenarios"
  if (key === "radiology") return "Local chest-imaging assistance"
  return "Local specialist service"
}
