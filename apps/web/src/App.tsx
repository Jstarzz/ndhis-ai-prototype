import { FormEvent, useEffect, useMemo, useRef, useState } from "react"

const API_BASE = import.meta.env.VITE_API_BASE ?? "http://localhost:8080"
const DEMO_KEY = import.meta.env.VITE_DEMO_API_KEY ?? "ndhis-local-demo"
const TRANSLATION_WS = import.meta.env.VITE_TRANSLATION_WS ?? "ws://localhost:8101/ws/translate"
const TRANSLATION_HEALTH = TRANSLATION_WS.replace(/^ws/, "http").replace(/\/ws\/translate$/, "/health")

type Tab = "assistant" | "translation" | "forecasting" | "radiology" | "system"
type ChatMessage = { role: "user" | "assistant"; content: string; meta?: string }
type ForecastPoint = { date: string; forecast: number; p10: number; p90: number }
type ForecastResult = {
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
type RadiologyResult = {
  result_id: string
  filename: string
  findings: string
  review_required: boolean
  latency_ms: number
}
type SystemInfo = {
  processing: string
  profile: string
  services: Record<string, string>
  models: Record<string, string>
  limits: { requests_per_minute_per_user: number; max_concurrent_requests: number; max_body_bytes: number }
}
type TranslationHealth = { supported_targets?: string[] }
type TranslationResult = {
  transcript?: string
  translation?: string
  source_language?: string
  target_language?: string
  latency_ms?: number
  error?: string
}

const headers = {
  "Content-Type": "application/json",
  "X-NDHIS-Demo-Key": DEMO_KEY,
  "X-NDHIS-User": "demo-doctor",
  "X-NDHIS-Role": "doctor",
}

function App() {
  const [tab, setTab] = useState<Tab>("assistant")
  const [health, setHealth] = useState<Record<string, string>>({})

  useEffect(() => {
    let active = true
    const refresh = () => {
      fetch(`${API_BASE}/api/health`)
        .then((response) => response.json())
        .then((payload) => active && setHealth(payload.services ?? {}))
        .catch(() => active && setHealth({ gateway: "offline" }))
    }
    refresh()
    const timer = window.setInterval(refresh, 5000)
    return () => {
      active = false
      window.clearInterval(timer)
    }
  }, [])

  return (
    <main className="shell">
      <header className="topbar">
        <div>
          <p className="eyebrow">LOCAL CLINICAL AI PLATFORM</p>
          <h1>NDHIS Intelligence</h1>
          <p className="subtitle">Translation, forecasting, radiology and tool-routed assistance running on local infrastructure.</p>
        </div>
        <div className="privacy-pill"><span className="dot" /> Local inference</div>
      </header>

      <div className="demo-banner">DEMO MODE · Synthetic operational data · Public or de-identified radiology only</div>

      <section className="status-grid">
        {["agent", "translation", "forecasting", "radiology"].map((name) => (
          <div className="status-card" key={name}>
            <span>{name}</span>
            <strong className={health[name] === "ready" ? "ready" : "pending"}>{health[name] ?? "checking"}</strong>
          </div>
        ))}
      </section>

      <nav className="tabs">
        {(["assistant", "translation", "forecasting", "radiology", "system"] as Tab[]).map((item) => (
          <button className={tab === item ? "active" : ""} onClick={() => setTab(item)} key={item}>{item}</button>
        ))}
      </nav>

      <section className="workspace">
        {tab === "assistant" && <Assistant />}
        {tab === "translation" && <Translation />}
        {tab === "forecasting" && <Forecasting />}
        {tab === "radiology" && <Radiology />}
        {tab === "system" && <System />}
      </section>
    </main>
  )
}

function System() {
  const [info, setInfo] = useState<SystemInfo | null>(null)
  const [error, setError] = useState("")

  useEffect(() => {
    fetch(`${API_BASE}/api/system`, { headers })
      .then(async (response) => {
        const payload = await response.json()
        if (!response.ok) throw new Error(payload.error ?? "system request failed")
        setInfo(payload)
      })
      .catch((err) => setError(err instanceof Error ? err.message : "system request failed"))
  }, [])

  return (
    <div className="panel">
      <div className="panel-heading">
        <div><p className="eyebrow">RUNTIME + GOVERNANCE</p><h2>AI System</h2></div>
        <span className="model-badge">{info?.processing ?? "local"}</span>
      </div>
      {error && <div className="error-box">{error}</div>}
      {!info && !error && <div className="empty">Loading local runtime metadata…</div>}
      {info && (
        <>
          <div className="system-grid">
            {Object.entries(info.models).map(([role, model]) => (
              <div className="system-card" key={role}><span>{role}</span><strong>{model}</strong></div>
            ))}
          </div>
          <div className="metric-grid">
            <Metric label="Per-user RPM" value={String(info.limits.requests_per_minute_per_user)} />
            <Metric label="Max concurrent" value={String(info.limits.max_concurrent_requests)} />
            <Metric label="Max request" value={`${Math.round(info.limits.max_body_bytes / 1024 / 1024)} MB`} />
            <Metric label="Processing" value={info.processing} />
            <Metric label="Profile" value={info.profile} />
          </div>
        </>
      )}
    </div>
  )
}

function Assistant() {
  const [messages, setMessages] = useState<ChatMessage[]>([
    { role: "assistant", content: "Ask me about the local AI services or synthetic JNF forecasts. I route requests to specialist models instead of inventing results." },
  ])
  const [input, setInput] = useState("")
  const [busy, setBusy] = useState(false)

  async function submit(event: FormEvent) {
    event.preventDefault()
    const text = input.trim()
    if (!text || busy) return
    const next = [...messages, { role: "user" as const, content: text }]
    setMessages(next)
    setInput("")
    setBusy(true)
    try {
      const response = await fetch(`${API_BASE}/api/chat`, {
        method: "POST",
        headers,
        body: JSON.stringify({ messages: next.map(({ role, content }) => ({ role, content })) }),
      })
      const payload = await response.json()
      if (!response.ok) throw new Error(payload.error ?? "assistant request failed")
      setMessages([...next, { role: "assistant", content: payload.answer, meta: payload.tool ? `${payload.tool} · ${payload.latency_ms} ms` : `${payload.latency_ms} ms` }])
    } catch (error) {
      setMessages([...next, { role: "assistant", content: error instanceof Error ? error.message : "request failed" }])
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="panel assistant-panel">
      <div className="panel-heading">
        <div><p className="eyebrow">TOOL ROUTER</p><h2>Ask NDHIS AI</h2></div>
        <span className="model-badge">local tool agent</span>
      </div>
      <div className="chat-log">
        {messages.map((message, index) => (
          <div className={`bubble ${message.role}`} key={index}>
            <p>{message.content}</p>
            {message.meta && <small>{message.meta}</small>}
          </div>
        ))}
        {busy && <div className="bubble assistant"><p>Routing…</p></div>}
      </div>
      <form className="chat-form" onSubmit={submit}>
        <input value={input} onChange={(event) => setInput(event.target.value)} placeholder="Forecast A&E patient volume for the next 30 days" />
        <button disabled={busy}>Send</button>
      </form>
    </div>
  )
}

function Translation() {
  const languageLabels: Record<string, string> = { eng_Latn: "English", spa_Latn: "Spanish", fra_Latn: "French", hat_Latn: "Haitian Creole", por_Latn: "Portuguese", deu_Latn: "German", ita_Latn: "Italian", fin_Latn: "Finnish", ces_Latn: "Czech", nld_Latn: "Dutch", swe_Latn: "Swedish" }
  const [running, setRunning] = useState(false)
  const [targets, setTargets] = useState<string[]>(["spa_Latn", "eng_Latn", "fra_Latn", "hat_Latn"])
  const [target, setTarget] = useState("spa_Latn")
  const [results, setResults] = useState<TranslationResult[]>([])
  const socketRef = useRef<WebSocket | null>(null)
  const contextRef = useRef<AudioContext | null>(null)
  const streamRef = useRef<MediaStream | null>(null)
  const processorRef = useRef<ScriptProcessorNode | null>(null)

  useEffect(() => {
    fetch(TRANSLATION_HEALTH)
      .then((response) => response.json() as Promise<TranslationHealth>)
      .then((payload) => {
        if (!payload.supported_targets?.length) return
        setTargets(payload.supported_targets)
        setTarget((current) => payload.supported_targets?.includes(current) ? current : payload.supported_targets![0])
      })
      .catch(() => undefined)
    return () => stop()
  }, [])

  async function start() {
    const ws = new WebSocket(`${TRANSLATION_WS}?key=${encodeURIComponent(DEMO_KEY)}&user=${encodeURIComponent("demo-doctor")}&role=${encodeURIComponent("doctor")}`)
    ws.binaryType = "arraybuffer"
    await new Promise<void>((resolve, reject) => {
      ws.onopen = () => resolve()
      ws.onerror = () => reject(new Error("translation socket failed"))
    })
    ws.send(JSON.stringify({ source: "auto", target }))
    ws.onmessage = (event) => {
      const payload = JSON.parse(event.data) as TranslationResult
      setResults((current) => [payload, ...current].slice(0, 12))
    }

    const stream = await navigator.mediaDevices.getUserMedia({ audio: { echoCancellation: true, noiseSuppression: true } })
    const context = new AudioContext()
    const source = context.createMediaStreamSource(stream)
    const processor = context.createScriptProcessor(4096, 1, 1)
    processor.onaudioprocess = (event) => {
      if (ws.readyState !== WebSocket.OPEN) return
      const input = event.inputBuffer.getChannelData(0)
      const downsampled = downsample(input, context.sampleRate, 16000)
      const pcm = new Int16Array(downsampled.length)
      for (let i = 0; i < downsampled.length; i++) pcm[i] = Math.max(-1, Math.min(1, downsampled[i])) * 32767
      ws.send(pcm.buffer)
    }
    source.connect(processor)
    processor.connect(context.destination)
    socketRef.current = ws
    contextRef.current = context
    streamRef.current = stream
    processorRef.current = processor
    setRunning(true)
  }

  function stop() {
    processorRef.current?.disconnect()
    contextRef.current?.close()
    streamRef.current?.getTracks().forEach((track) => track.stop())
    socketRef.current?.close()
    processorRef.current = null
    contextRef.current = null
    streamRef.current = null
    socketRef.current = null
    setRunning(false)
  }

  return (
    <div className="panel">
      <div className="panel-heading">
        <div><p className="eyebrow">STREAMING AUDIO</p><h2>Live Translation</h2></div>
        <span className="model-badge">local ASR + translator</span>
      </div>
      <div className="controls-row">
        <label>Target language
          <select disabled={running} value={target} onChange={(event) => setTarget(event.target.value)}>
            {targets.map((code) => <option value={code} key={code}>{languageLabels[code] ?? code}</option>)}
          </select>
        </label>
        <button className={running ? "danger" : ""} onClick={running ? stop : start}>{running ? "Stop microphone" : "Start microphone"}</button>
      </div>
      <div className="translation-feed">
        {results.length === 0 && <div className="empty">Start the microphone and speak. Audio is processed locally in short PCM chunks.</div>}
        {results.map((result, index) => (
          <div className="translation-item" key={index}>
            {result.error ? <strong>{result.error}</strong> : <><small>{result.source_language} · {result.latency_ms} ms</small><p>{result.transcript}</p><h3>{result.translation}</h3></>}
          </div>
        ))}
      </div>
    </div>
  )
}

function downsample(input: Float32Array, inputRate: number, outputRate: number) {
  if (inputRate === outputRate) return input
  const ratio = inputRate / outputRate
  const length = Math.round(input.length / ratio)
  const output = new Float32Array(length)
  for (let i = 0; i < length; i++) {
    const start = Math.floor(i * ratio)
    const end = Math.min(Math.floor((i + 1) * ratio), input.length)
    let sum = 0
    for (let j = start; j < end; j++) sum += input[j]
    output[i] = sum / Math.max(1, end - start)
  }
  return output
}

function Forecasting() {
  const [department, setDepartment] = useState("A&E")
  const [metric, setMetric] = useState("patient_arrivals")
  const [disease, setDisease] = useState("respiratory")
  const [result, setResult] = useState<ForecastResult | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")

  async function run() {
    setBusy(true)
    setError("")
    try {
      const body: Record<string, unknown> = { facility: "JNF", department, metric, horizon_days: 30 }
      if (metric === "disease_incidence") body.disease = disease
      const response = await fetch(`${API_BASE}/api/forecast`, { method: "POST", headers, body: JSON.stringify(body) })
      const payload = await response.json()
      if (!response.ok) throw new Error(payload.detail ?? payload.error ?? "forecast failed")
      setResult(payload)
    } catch (err) {
      setError(err instanceof Error ? err.message : "forecast failed")
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="panel">
      <div className="panel-heading">
        <div><p className="eyebrow">SYNTHETIC JNF OPERATIONS</p><h2>30-day Forecast</h2></div>
        <span className="model-badge">local forecast model</span>
      </div>
      <div className="controls-row wrap">
        <label>Department<select value={department} onChange={(event) => setDepartment(event.target.value)}><option>A&E</option><option>Outpatient</option><option>Medical Ward</option><option>Surgical Ward</option><option>Pediatrics</option></select></label>
        <label>Metric<select value={metric} onChange={(event) => setMetric(event.target.value)}><option value="patient_arrivals">Patient arrivals</option><option value="bed_occupancy">Bed occupancy</option><option value="disease_incidence">Disease incidence</option></select></label>
        {metric === "disease_incidence" && <label>Disease<select value={disease} onChange={(event) => setDisease(event.target.value)}><option value="respiratory">Respiratory</option><option value="gastro">Gastro</option><option value="diabetes">Diabetes</option><option value="hypertension">Hypertension</option></select></label>}
        <button onClick={run} disabled={busy}>{busy ? "Forecasting…" : "Run forecast"}</button>
      </div>
      {error && <div className="error-box">{error}</div>}
      {result && <ForecastView result={result} />}
    </div>
  )
}

function ForecastView({ result }: { result: ForecastResult }) {
  const path = useMemo(() => {
    const values = result.series.map((point) => point.forecast)
    const min = Math.min(...values)
    const max = Math.max(...values)
    return values.map((value, index) => {
      const x = values.length === 1 ? 0 : (index / (values.length - 1)) * 100
      const y = 36 - ((value - min) / Math.max(max - min, 1)) * 32
      return `${x},${y}`
    }).join(" ")
  }, [result])

  return (
    <div className="forecast-output">
      <div className="metric-grid">
        <Metric label="Expected" value={result.expected.toLocaleString()} />
        <Metric label="P10" value={result.p10.toLocaleString()} />
        <Metric label="P90" value={result.p90.toLocaleString()} />
        <Metric label="Latency" value={`${result.latency_ms} ms`} />
      </div>
      <div className="chart-card">
        <svg viewBox="0 0 100 40" preserveAspectRatio="none"><polyline points={path} fill="none" vectorEffect="non-scaling-stroke" /></svg>
        <div className="chart-caption"><span>{result.series[0]?.date}</span><strong>30 days</strong><span>{result.series.at(-1)?.date}</span></div>
      </div>
    </div>
  )
}

function Metric({ label, value }: { label: string; value: string }) {
  return <div className="metric"><span>{label}</span><strong>{value}</strong></div>
}

function Radiology() {
  const [file, setFile] = useState<File | null>(null)
  const [prompt, setPrompt] = useState("Describe the clinically relevant findings in this chest radiograph concisely. State uncertainty.")
  const [result, setResult] = useState<RadiologyResult | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")

  async function run() {
    if (!file) return
    setBusy(true)
    setError("")
    const body = new FormData()
    body.append("file", file)
    body.append("prompt", prompt)
    try {
      const response = await fetch(`${API_BASE}/api/radiology`, {
        method: "POST",
        headers: { "X-NDHIS-Demo-Key": DEMO_KEY, "X-NDHIS-User": "demo-doctor", "X-NDHIS-Role": "doctor" },
        body,
      })
      const payload = await response.json()
      if (!response.ok) throw new Error(payload.detail ?? payload.error ?? "radiology analysis failed")
      setResult(payload)
    } catch (err) {
      setError(err instanceof Error ? err.message : "radiology analysis failed")
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="panel">
      <div className="panel-heading">
        <div><p className="eyebrow">MEDICAL VISION</p><h2>Radiology Assistance</h2></div>
        <span className="model-badge">local radiology model</span>
      </div>
      <div className="radiology-layout">
        <div className="upload-card">
          <input type="file" accept="image/png,image/jpeg,.dcm" onChange={(event) => setFile(event.target.files?.[0] ?? null)} />
          <textarea value={prompt} onChange={(event) => setPrompt(event.target.value)} rows={5} />
          <button disabled={!file || busy} onClick={run}>{busy ? "Analyzing…" : "Analyze locally"}</button>
        </div>
        <div className="findings-card">
          {!result && <div className="empty">Upload a public or de-identified radiology image. The prototype never treats model output as an autonomous diagnosis.</div>}
          {error && <div className="error-box">{error}</div>}
          {result && <><small>Result {result.result_id} · {result.latency_ms} ms</small><h3>AI-assisted findings</h3><p>{result.findings}</p><div className="review-pill">Human review required</div></>}
        </div>
      </div>
    </div>
  )
}

export default App
