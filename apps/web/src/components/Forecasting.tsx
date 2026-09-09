import { useEffect, useMemo, useState } from "react"
import { API_BASE, fetchWithTimeout, headers, safeJson } from "../api"
import type { ForecastCapabilities, ForecastHistory, ForecastPoint, ForecastResult, HistoryPoint } from "../types"
import { formatMetric, metricLabel, titleCase } from "./shared"

const defaultDepartments = ["A&E", "Outpatient", "Medical Ward", "Surgical Ward", "Pediatrics"]
const defaultResolutions = ["auto", "1ms", "10ms", "100ms", "1s", "5s", "30s", "1min", "5min", "15min", "1h", "6h", "1d", "1w", "1mo"]

export function Forecasting() {
  const [capabilities, setCapabilities] = useState<ForecastCapabilities | null>(null)
  const [department, setDepartment] = useState("A&E")
  const [metric, setMetric] = useState("patient_arrivals")
  const [disease, setDisease] = useState("respiratory")
  const [horizon, setHorizon] = useState(2)
  const [horizonUnit, setHorizonUnit] = useState("hours")
  const [resolution, setResolution] = useState("5min")
  const [asOfMode, setAsOfMode] = useState<"now" | "historical">("now")
  const [asOf, setAsOf] = useState("2024-06-01T12:00")
  const [result, setResult] = useState<ForecastResult | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [history, setHistory] = useState<ForecastHistory | null>(null)
  const [historyBusy, setHistoryBusy] = useState(false)
  const [historyError, setHistoryError] = useState("")

  useEffect(() => {
    fetchWithTimeout(`${API_BASE}/api/forecast/capabilities`, { headers }, 8_000)
      .then(async (response) => {
        const payload = await safeJson(response)
        if (response.ok) setCapabilities(payload as ForecastCapabilities)
      })
      .catch(() => undefined)
  }, [])

  const departments = capabilities?.facilities?.JNF ?? defaultDepartments
  const resolutions = capabilities?.resolutions ? ["auto", ...capabilities.resolutions] : defaultResolutions

  async function run() {
    setBusy(true)
    setError("")
    try {
      const body: Record<string, unknown> = {
        facility: "JNF",
        department,
        metric,
        horizon,
        horizon_unit: horizonUnit,
        resolution,
        include_actuals: true,
      }
      if (metric === "disease_incidence") body.disease = disease
      if (asOfMode === "historical" && asOf) body.as_of = new Date(asOf).toISOString()
      const response = await fetchWithTimeout(`${API_BASE}/api/forecast`, { method: "POST", headers, body: JSON.stringify(body) }, 95_000)
      const payload = await safeJson(response)
      if (!response.ok) throw new Error(payload.detail ?? payload.error ?? "forecast failed")
      setResult(payload)
    } catch (err) { setError(err instanceof Error ? err.message : "forecast failed") }
    finally { setBusy(false) }
  }

  async function loadHistory() {
    setHistoryBusy(true)
    setHistoryError("")
    try {
      const body: Record<string, unknown> = {
        facility: "JNF",
        department,
        metric,
        disease: metric === "disease_incidence" ? disease : undefined,
        start: "2020-01-01T00:00:00Z",
        end: "2026-12-31T23:59:59Z",
        resolution: "1mo",
      }
      const response = await fetchWithTimeout(`${API_BASE}/api/forecast/history`, { method: "POST", headers, body: JSON.stringify(body) }, 30_000)
      const payload = await safeJson(response)
      if (!response.ok) throw new Error(payload.detail ?? payload.error ?? "history failed")
      setHistory(payload)
    } catch (err) { setHistoryError(err instanceof Error ? err.message : "history failed") }
    finally { setHistoryBusy(false) }
  }

  return (
    <section className="work-panel">
      <div className="workspace-head"><div><span className="section-label">Synthetic JNF operations · 2020–2026</span><h2>Multi-timescale forecasting</h2><p>Forecast from millisecond-rate views through two-year planning horizons, with explicit source resolution, uncertainty and historical backtesting.</p></div><div className="model-tag">Local forecast engine</div></div>

      <div className="control-strip wrap forecast-controls">
        <label>Department<select value={department} onChange={(event) => setDepartment(event.target.value)}>{departments.map((item) => <option key={item}>{item}</option>)}</select></label>
        <label>Metric<select value={metric} onChange={(event) => setMetric(event.target.value)}><option value="patient_arrivals">Patient arrivals</option><option value="bed_occupancy">Bed occupancy</option><option value="disease_incidence">Disease incidence</option></select></label>
        {metric === "disease_incidence" && <label>Disease<select value={disease} onChange={(event) => setDisease(event.target.value)}>{(capabilities?.diseases ?? ["respiratory", "gastro", "diabetes", "hypertension"]).map((item) => <option value={item} key={item}>{titleCase(item)}</option>)}</select></label>}
        <label>Horizon<div className="inline-inputs"><input type="number" min="0.001" step="any" value={horizon} onChange={(event) => setHorizon(Number(event.target.value))} /><select value={horizonUnit} onChange={(event) => setHorizonUnit(event.target.value)}><option>milliseconds</option><option>seconds</option><option>minutes</option><option>hours</option><option>days</option><option>weeks</option><option>months</option><option>years</option></select></div></label>
        <label>Resolution<select value={resolution} onChange={(event) => setResolution(event.target.value)}>{resolutions.map((item) => <option value={item} key={item}>{item === "auto" ? "Auto" : item}</option>)}</select></label>
        <label>Forecast origin<select value={asOfMode} onChange={(event) => setAsOfMode(event.target.value as "now" | "historical")}><option value="now">Now / latest available</option><option value="historical">Historical backtest</option></select></label>
        {asOfMode === "historical" && <label>As of<input type="datetime-local" value={asOf} onChange={(event) => setAsOf(event.target.value)} /></label>}
        <button type="button" onClick={run} disabled={busy || !Number.isFinite(horizon) || horizon <= 0}>{busy ? "Running…" : "Run forecast"}</button>
      </div>

      {capabilities && <div className="forecast-contract"><span>Source history: {formatDate(capabilities.data_start)} → {formatDate(capabilities.data_end)}</span><span>Source cadence: {capabilities.source_resolution}</span><span>Max chart points: {capabilities.max_points.toLocaleString()}</span><span>Sub-hourly: derived intensity/interpolation</span></div>}
      {error && <div className="error-box" role="alert"><strong>Forecast unavailable</strong><span>{error}</span></div>}
      {!result && !error && <div className="forecast-placeholder"><span className="section-label">Scenario output</span><h3>Choose any practical time scale</h3><p>Examples: 2 hours at 5-minute resolution, 30 days daily, 6 months weekly, 2 years monthly, or a one-second horizon at 1 ms to inspect derived arrival intensity.</p></div>}
      {result && <ForecastView result={result} />}

      <div className="history-explorer">
        <div><span className="section-label">Historical explorer</span><h3>2020–2026 synthetic actuals</h3><p>Load monthly history for the current department/metric. Use historical forecast origin above to run a genuine as-of backtest without leaking later synthetic observations into the model input.</p></div>
        <button type="button" className="secondary" disabled={historyBusy} onClick={loadHistory}>{historyBusy ? "Loading…" : "Load 2020–2026 history"}</button>
      </div>
      {historyError && <div className="error-box" role="alert"><strong>History unavailable</strong><span>{historyError}</span></div>}
      {history && <HistoryView history={history} />}
    </section>
  )
}

function ForecastView({ result }: { result: ForecastResult }) {
  const chart = useMemo(() => buildForecastChart(result.series), [result.series])
  const context = result.disease ? `${titleCase(result.disease)} · ${result.department ?? "JNF"}` : result.department ?? "JNF"
  const actualCount = result.series.filter((point) => point.actual !== undefined).length
  return <div className="forecast-output">
    <div className="forecast-summary"><div className="forecast-primary"><span>Expected</span><strong>{formatMetric(result.expected, result.metric)}</strong><small>{context}</small></div><div className="summary-stat"><span>Lower range (P10)</span><strong>{formatMetric(result.p10, result.metric)}</strong></div><div className="summary-stat"><span>Upper range (P90)</span><strong>{formatMetric(result.p90, result.metric)}</strong></div><div className="summary-stat"><span>Latency</span><strong>{result.latency_ms} ms</strong></div></div>
    {result.intensity && <div className="intensity-strip"><div><span>Per second</span><strong>{result.intensity.expected_per_second}</strong></div><div><span>Per minute</span><strong>{result.intensity.expected_per_minute}</strong></div><div><span>Per hour</span><strong>{result.intensity.expected_per_hour}</strong></div><div><span>≥1 next minute</span><strong>{(result.intensity.probability_at_least_one_next_minute * 100).toFixed(2)}%</strong></div></div>}
    <div className="chart-panel"><div className="chart-head"><div><span className="section-label">Forecast trajectory</span><h3>{metricLabel(result.metric)}</h3></div><span className="muted">P10–P90{actualCount ? " · historical actuals overlaid" : ""}</span></div><svg className="forecast-chart" viewBox="0 0 100 46" preserveAspectRatio="none" role="img" aria-label="Forecast with uncertainty and optional historical actuals"><polygon points={chart.band} className="uncertainty-band" /><polyline points={chart.line} className="forecast-line" fill="none" vectorEffect="non-scaling-stroke" />{chart.actual && <polyline points={chart.actual} className="actual-line" fill="none" vectorEffect="non-scaling-stroke" />}</svg><div className="chart-caption"><span>{formatDateTime(result.series[0]?.timestamp)}</span><strong>{result.series.length} points · {result.resolution}</strong><span>{formatDateTime(result.series.at(-1)?.timestamp)}</span></div></div>
    {result.backtest?.available && <div className="backtest-grid"><div><span>MAE</span><strong>{result.backtest.mae}</strong></div><div><span>RMSE</span><strong>{result.backtest.rmse}</strong></div><div><span>Bias</span><strong>{result.backtest.bias}</strong></div><div><span>P10–P90 coverage</span><strong>{((result.backtest.interval_coverage ?? 0) * 100).toFixed(1)}%</strong></div><div><span>Compared</span><strong>{result.backtest.points_compared} points</strong></div></div>}
    {!result.backtest?.available && result.backtest?.reason && <div className="forecast-note"><strong>Backtest not fabricated</strong><span>{result.backtest.reason}</span></div>}
    <div className="provenance-row"><div><span>As of</span><strong>{formatDateTime(result.as_of)}</strong></div><div><span>Source cadence</span><strong>{result.source_resolution}</strong></div><div><span>Output semantics</span><strong>{result.resolution_semantics.replaceAll("_", " ")}</strong></div><div><span>History used</span><strong>{result.history_points_used.toLocaleString()} points</strong></div><div><span>Data mode</span><strong>{result.data_mode}</strong></div><div><span>Use</span><strong>Operational prototype only</strong></div></div>
  </div>
}

function HistoryView({ history }: { history: ForecastHistory }) {
  const chart = useMemo(() => buildHistoryChart(history.series), [history.series])
  return <div className="chart-panel history-chart"><div className="chart-head"><div><span className="section-label">Observed synthetic history</span><h3>{metricLabel(history.metric)} · {history.department ?? history.facility}</h3></div><span className="muted">{history.resolution} · {history.series.length} points</span></div><svg className="forecast-chart" viewBox="0 0 100 46" preserveAspectRatio="none" role="img" aria-label="Historical synthetic time series"><polyline points={chart} className="actual-line" fill="none" vectorEffect="non-scaling-stroke" /></svg><div className="chart-caption"><span>{formatDate(history.start)}</span><strong>2020–2026</strong><span>{formatDate(history.end)}</span></div></div>
}

function buildForecastChart(series: ForecastPoint[]) {
  if (!series.length) return { line: "", band: "", actual: "" }
  const all = series.flatMap((point) => [point.p10, point.p90, point.forecast, ...(point.actual === undefined ? [] : [point.actual])])
  const min = Math.min(...all)
  const max = Math.max(...all)
  const span = Math.max(max - min, 1e-9)
  const mapPoint = (value: number, index: number) => `${(series.length === 1 ? 0 : (index / (series.length - 1)) * 100).toFixed(2)},${(42 - ((value - min) / span) * 36).toFixed(2)}`
  const line = series.map((point, index) => mapPoint(point.forecast, index)).join(" ")
  const upper = series.map((point, index) => mapPoint(point.p90, index))
  const lower = [...series].reverse().map((point, reverseIndex) => mapPoint(point.p10, series.length - 1 - reverseIndex))
  const actualPoints = series.map((point, index) => point.actual === undefined ? null : mapPoint(point.actual, index)).filter((point): point is string => Boolean(point))
  return { line, band: [...upper, ...lower].join(" "), actual: actualPoints.length > 1 ? actualPoints.join(" ") : "" }
}

function buildHistoryChart(series: HistoryPoint[]) {
  if (!series.length) return ""
  const values = series.map((point) => point.actual)
  const min = Math.min(...values)
  const max = Math.max(...values)
  const span = Math.max(max - min, 1e-9)
  return series.map((point, index) => `${(series.length === 1 ? 0 : (index / (series.length - 1)) * 100).toFixed(2)},${(42 - ((point.actual - min) / span) * 36).toFixed(2)}`).join(" ")
}

function formatDate(value?: string) {
  if (!value) return "—"
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value.slice(0, 10) : date.toLocaleDateString()
}

function formatDateTime(value?: string) {
  if (!value) return "—"
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}
