import type { AuditEvent, ServiceState } from "../types"

export function Status({ state, label }: { state: ServiceState; label?: string }) {
  const normalized = state === "ready" ? "ready" : state === "checking" ? "checking" : state === "idle" ? "idle" : "offline"
  return <span className={`status-text ${normalized}`}><span className={`status-dot ${normalized}-dot`} />{label ?? titleCase(state)}</span>
}

export function AuditList({ events, empty }: { events: AuditEvent[]; empty: string }) {
  if (!events.length) return <div className="empty-state"><strong>No recent events</strong><span>{empty}</span></div>
  return (
    <div className="audit-list">
      {events.map((event) => (
        <div className="audit-row" key={`${event.request_id}-${event.timestamp}`}>
          <div><strong>{event.tool || routeLabel(event.route)}</strong><span>{new Date(event.timestamp).toLocaleTimeString()} · {event.latency_ms} ms</span></div>
          <code>{event.request_id}</code>
          <span className={event.status >= 200 && event.status < 300 ? "http-ok" : "http-error"}>{event.status}</span>
        </div>
      ))}
    </div>
  )
}

export function titleCase(value: string) {
  return value.replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase())
}

export function metricLabel(metric: string) {
  if (metric === "patient_arrivals") return "Patient arrivals"
  if (metric === "bed_occupancy") return "Bed occupancy"
  if (metric === "disease_incidence") return "Disease incidence"
  return titleCase(metric)
}

export function formatMetric(value: number, metric: string) {
  if (metric === "bed_occupancy") return `${value.toLocaleString(undefined, { maximumFractionDigits: 1 })}%`
  return value.toLocaleString(undefined, { maximumFractionDigits: 1 })
}

function routeLabel(route: string) {
  return route.replace(/^\/api\//, "").replaceAll("/", " · ") || "gateway"
}
