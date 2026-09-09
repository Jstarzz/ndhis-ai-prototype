import { useMemo, useState } from "react"
import { API_BASE, fetchWithTimeout, headers, safeJson } from "../api"
import type { ForecastPoint, ForecastResult } from "../types"
import { formatMetric, metricLabel, titleCase } from "./shared"

export function Forecasting() {
  const [department, setDepartment] = useState("A&E")
  const [metric, setMetric] = useState("patient_arrivals")
  const [disease, setDisease] = useState("respiratory")
  const [horizon, setHorizon] = useState(30)
  const [result, setResult] = useState<ForecastResult | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")

  async function run() {
    setBusy(true)
    setError("")
    try {
      const body: Record<string, unknown> = { facility: "JNF", department, metric, horizon_days: horizon }
      if (metric === "disease_incidence") body.disease = disease
      const response = await fetchWithTimeout(`${API_BASE}/api/forecast`, { method: "POST", headers, body: JSON.stringify(body) }, 95_000)
      const payload = await safeJson(response)
      if (!response.ok) throw new Error(payload.detail ?? payload.error ?? "forecast failed")
      setResult(payload)
    } catch (err) { setError(err instanceof Error ? err.message : "forecast failed") }
    finally { setBusy(false) }
  }

  return (
    <section className="work-panel">
      <div className="workspace-head"><div><span className="section-label">Synthetic JNF operations</span><h2>Hospital demand forecasting</h2><p>Explore operational scenarios with explicit uncertainty and synthetic-data provenance.</p></div><div className="model-tag">Local forecast model</div></div>
      <div className="control-strip wrap"><label>Department<select value={department} onChange={(event) => setDepartment(event.target.value)}><option>A&E</option><option>Outpatient</option><option>Medical Ward</option><option>Surgical Ward</option><option>Pediatrics</option></select></label><label>Metric<select value={metric} onChange={(event) => setMetric(event.target.value)}><option value="patient_arrivals">Patient arrivals</option><option value="bed_occupancy">Bed occupancy</option><option value="disease_incidence">Disease incidence</option></select></label>{metric === "disease_incidence" && <label>Disease<select value={disease} onChange={(event) => setDisease(event.target.value)}><option value="respiratory">Respiratory</option><option value="gastro">Gastro</option><option value="diabetes">Diabetes</option><option value="hypertension">Hypertension</option></select></label>}<label>Horizon<select value={horizon} onChange={(event) => setHorizon(Number(event.target.value))}><option value={7}>7 days</option><option value={14}>14 days</option><option value={30}>30 days</option></select></label><button type="button" onClick={run} disabled={busy}>{busy ? "Running…" : "Run forecast"}</button></div>
      {error && <div className="error-box" role="alert"><strong>Forecast unavailable</strong><span>{error}</span></div>}
      {!result && !error && <div className="forecast-placeholder"><span className="section-label">Scenario output</span><h3>Configure a scenario above</h3><p>The result will show expected demand, an uncertainty interval, daily trajectory, latency and data mode.</p></div>}
      {result && <ForecastView result={result} />}
    </section>
  )
}

function ForecastView({ result }: { result: ForecastResult }) {
  const chart = useMemo(() => buildForecastChart(result.series), [result.series])
  const context = result.disease ? `${titleCase(result.disease)} · ${result.department ?? "JNF"}` : result.department ?? "JNF"
  return <div className="forecast-output"><div className="forecast-summary"><div className="forecast-primary"><span>Expected</span><strong>{formatMetric(result.expected, result.metric)}</strong><small>{context}</small></div><div className="summary-stat"><span>Lower range (P10)</span><strong>{formatMetric(result.p10, result.metric)}</strong></div><div className="summary-stat"><span>Upper range (P90)</span><strong>{formatMetric(result.p90, result.metric)}</strong></div><div className="summary-stat"><span>Latency</span><strong>{result.latency_ms} ms</strong></div></div><div className="chart-panel"><div className="chart-head"><div><span className="section-label">Forecast trajectory</span><h3>{metricLabel(result.metric)}</h3></div><span className="muted">Shaded range: P10–P90</span></div><svg className="forecast-chart" viewBox="0 0 100 46" preserveAspectRatio="none" role="img" aria-label="Forecast with P10 to P90 uncertainty band"><polygon points={chart.band} className="uncertainty-band" /><polyline points={chart.line} className="forecast-line" fill="none" vectorEffect="non-scaling-stroke" /></svg><div className="chart-caption"><span>{result.series[0]?.date ?? "—"}</span><strong>{result.series.length} days</strong><span>{result.series.at(-1)?.date ?? "—"}</span></div></div><div className="provenance-row"><div><span>Data mode</span><strong>{result.data_mode || "synthetic"}</strong></div><div><span>Facility</span><strong>JNF General Hospital</strong></div><div><span>Use</span><strong>Operational prototype only</strong></div></div></div>
}

function buildForecastChart(series: ForecastPoint[]) {
  if (!series.length) return { line: "", band: "" }
  const all = series.flatMap((point) => [point.p10, point.p90, point.forecast])
  const min = Math.min(...all)
  const max = Math.max(...all)
  const span = Math.max(max - min, 1)
  const mapPoint = (value: number, index: number) => `${(series.length === 1 ? 0 : (index / (series.length - 1)) * 100).toFixed(2)},${(42 - ((value - min) / span) * 36).toFixed(2)}`
  const line = series.map((point, index) => mapPoint(point.forecast, index)).join(" ")
  const upper = series.map((point, index) => mapPoint(point.p90, index))
  const lower = [...series].reverse().map((point, reverseIndex) => mapPoint(point.p10, series.length - 1 - reverseIndex))
  return { line, band: [...upper, ...lower].join(" ") }
}
