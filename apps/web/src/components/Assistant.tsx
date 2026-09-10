import { FormEvent, useState } from "react"
import { API_BASE, headers, safeJson } from "../api"
import type { ChatMessage, ExecutionStage } from "../types"

const STAGE_LABELS: Record<string, string> = {
  interpret: "Interpret request",
  route: "Resolve route",
  model: "Language interpretation",
  validate: "Validate inputs",
  tool: "Run specialist service",
  format: "Prepare result",
}

export function Assistant() {
  const [messages, setMessages] = useState<ChatMessage[]>([{ role: "assistant", content: "Ready. You can request JNF operational forecasts, ask about available services, review a radiology result, or ask a general question about this prototype." }])
  const [input, setInput] = useState("")
  const [busy, setBusy] = useState(false)
  const [liveStage, setLiveStage] = useState<ExecutionStage | null>(null)
  const [liveText, setLiveText] = useState("")
  const examples = [
    "Forecast A&E patient arrivals for the next 2 hours every 5 minutes.",
    "What services are available?",
    "Why can local inference be useful in a hospital?",
  ]

  async function submit(event: FormEvent) {
    event.preventDefault()
    const text = input.trim()
    if (!text || busy) return
    const next = [...messages, { role: "user" as const, content: text }]
    setMessages(next)
    setInput("")
    setBusy(true)
    setLiveText("")
    setLiveStage({ name: "interpret", status: "running", detail: "Submitting request to the local gateway" })

    const controller = new AbortController()
    const timeout = window.setTimeout(() => controller.abort(), 95_000)
    try {
      const response = await fetch(`${API_BASE}/api/chat/stream`, {
        method: "POST",
        headers,
        body: JSON.stringify({ messages: next.map(({ role, content }) => ({ role, content })) }),
        signal: controller.signal,
      })
      if (!response.ok) {
        const payload = await safeJson(response)
        throw new Error(payload.error ?? "request failed")
      }
      if (!response.body || !response.headers.get("Content-Type")?.includes("application/x-ndjson")) {
        const payload = await safeJson(response)
        if (!payload.answer) throw new Error(payload.error ?? "stream unavailable")
        setMessages([...next, responseToMessage(payload, response)])
        return
      }

      const reader = response.body.getReader()
      const decoder = new TextDecoder()
      let buffer = ""
      let streamedText = ""
      let resultMessage: ChatMessage | null = null
      while (true) {
        const { value, done } = await reader.read()
        buffer += decoder.decode(value ?? new Uint8Array(), { stream: !done })
        const lines = buffer.split("\n")
        buffer = lines.pop() ?? ""
        for (const line of lines) {
          if (!line.trim()) continue
          const event = JSON.parse(line)
          if (event.type === "stage" && event.stage) setLiveStage(event.stage as ExecutionStage)
          if (event.type === "delta" && typeof event.delta === "string") {
            streamedText += event.delta
            setLiveText(streamedText)
          }
          if (event.type === "error") throw new Error(event.error ?? "request failed")
          if (event.type === "result" && event.response) resultMessage = responseToMessage(event.response, response)
        }
        if (done) break
      }
      if (buffer.trim()) {
        const event = JSON.parse(buffer)
        if (event.type === "delta" && typeof event.delta === "string") {
          streamedText += event.delta
          setLiveText(streamedText)
        }
        if (event.type === "result" && event.response) resultMessage = responseToMessage(event.response, response)
        if (event.type === "error") throw new Error(event.error ?? "request failed")
      }
      if (!resultMessage) throw new Error("stream ended without a result")
      setMessages([...next, resultMessage])
    } catch (error) {
      const message = error instanceof DOMException && error.name === "AbortError"
        ? "The request timed out before the local service completed it."
        : error instanceof Error ? error.message : "request failed"
      setMessages([...next, { role: "assistant", content: message }])
    } finally {
      window.clearTimeout(timeout)
      setLiveText("")
      setLiveStage(null)
      setBusy(false)
    }
  }

  return (
    <section className="work-panel assistant-workspace">
      <div className="workspace-head"><div><span className="section-label">Clinical operations</span><h2>Request console</h2><p>Common operational requests execute directly against local services. Ambiguous language is interpreted locally, with route and timing details available for review.</p></div><div className="model-tag">Local processing</div></div>
      <div className="suggestion-row" aria-label="Common requests"><span className="quick-label">Common requests</span><div className="quick-actions">{examples.map((example) => <button type="button" key={example} onClick={() => setInput(example)}>{example}</button>)}</div></div>
      <div className="chat-log" aria-live="polite">
        {messages.map((message, index) => <Message key={`${message.role}-${index}`} message={message} />)}
        {busy && <div className="message assistant"><div className="message-label">System</div><div className="message-body thinking">{liveText ? <p>{liveText}<span className="stream-cursor" aria-hidden="true">▍</span></p> : <><strong>{STAGE_LABELS[liveStage?.name ?? ""] ?? "Processing request"}</strong><span>{liveStage?.detail ?? "Resolving request…"}</span></>}{liveText && <small>{STAGE_LABELS[liveStage?.name ?? ""] ?? "Generating response"}</small>}{liveStage?.model && <small>{liveStage.model}</small>}{liveStage?.tool && <small>{liveStage.tool}</small>}</div></div>}
      </div>
      <form className="composer" onSubmit={submit}><div className="composer-head"><label htmlFor="assistant-input">Clinical request</label><span>Local execution · de-identified only</span></div><textarea id="assistant-input" rows={3} value={input} maxLength={8_000} onChange={(event) => setInput(event.target.value)} placeholder="Enter an operational request or general question…" /><div className="composer-foot"><span>{input.length}/8000</span><button disabled={busy || !input.trim()}>Run</button></div></form>
    </section>
  )
}

function responseToMessage(payload: any, response: Response): ChatMessage {
  return {
    role: "assistant",
    content: payload.answer,
    latencyMs: payload.latency_ms,
    tool: payload.tool,
    arguments: payload.arguments,
    requestId: payload.request_id ?? response.headers.get("X-NDHIS-Request-ID") ?? undefined,
    routing: payload.routing,
    intent: payload.intent,
    llmCalls: payload.llm_calls,
    trace: payload.trace,
    model: payload.model,
  }
}

function Message({ message }: { message: ChatMessage }) {
  return <div className={`message ${message.role}`}><div className="message-label">{message.role === "assistant" ? "System" : "Doctor"}</div><div className="message-body"><p>{message.content}</p>{(message.tool || message.routing || message.trace?.length) && <details className="tool-trace"><summary>Execution details{message.tool ? ` · ${message.tool}` : ""}</summary><dl><div><dt>Routing</dt><dd>{message.routing ?? "—"}</dd></div>{message.intent && <div><dt>Intent</dt><dd>{message.intent.replaceAll("_", " ")}</dd></div>}<div><dt>LLM calls</dt><dd>{message.llmCalls ?? 0}</dd></div>{message.model && <div><dt>Model</dt><dd>{message.model}</dd></div>}<div><dt>Total latency</dt><dd>{message.latencyMs ?? "—"} ms</dd></div>{message.requestId && <div><dt>Request</dt><dd>{message.requestId}</dd></div>}{message.tool && <div><dt>Validated inputs</dt><dd><code>{JSON.stringify(message.arguments ?? {})}</code></dd></div>}</dl>{message.trace?.length ? <div className="execution-stages">{message.trace.map((stage, index) => <div className={`execution-stage ${stage.status}`} key={`${stage.name}-${index}`}><span>{STAGE_LABELS[stage.name] ?? stage.name}</span><strong>{stage.status}</strong><small>{stage.detail}</small>{stage.duration_ms !== undefined && <em>{stage.duration_ms} ms</em>}</div>)}</div> : null}</details>}</div></div>
}
