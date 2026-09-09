import { FormEvent, useState } from "react"
import { API_BASE, headers, safeJson } from "../api"
import type { ChatMessage, ExecutionStage } from "../types"

const STAGE_LABELS: Record<string, string> = {
  interpret: "Interpreting request",
  route: "Routing locally",
  model: "Calling local assistant model",
  validate: "Validating inputs",
  tool: "Running specialist service",
  format: "Formatting result",
}

export function Assistant() {
  const [messages, setMessages] = useState<ChatMessage[]>([{ role: "assistant", content: "I can explain this prototype and route supported operational requests to local tools. Try a JNF patient-volume forecast, ask which departments are available, or ask for service status." }])
  const [input, setInput] = useState("")
  const [busy, setBusy] = useState(false)
  const [liveStage, setLiveStage] = useState<ExecutionStage | null>(null)
  const examples = [
    "Forecast A&E patient arrivals for the next 2 hours every 5 minutes.",
    "What facilities and departments can I forecast?",
    "Backtest respiratory disease incidence at JNF for 30 days as of 2024-06-01.",
  ]

  async function submit(event: FormEvent) {
    event.preventDefault()
    const text = input.trim()
    if (!text || busy) return
    const next = [...messages, { role: "user" as const, content: text }]
    setMessages(next)
    setInput("")
    setBusy(true)
    setLiveStage({ name: "interpret", status: "running", detail: "Sending request to the local gateway" })

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
        throw new Error(payload.error ?? "assistant request failed")
      }
      if (!response.body || !response.headers.get("Content-Type")?.includes("application/x-ndjson")) {
        const payload = await safeJson(response)
        if (!payload.answer) throw new Error(payload.error ?? "assistant stream unavailable")
        setMessages([...next, responseToMessage(payload, response)])
        return
      }

      const reader = response.body.getReader()
      const decoder = new TextDecoder()
      let buffer = ""
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
          if (event.type === "error") throw new Error(event.error ?? "assistant request failed")
          if (event.type === "result" && event.response) resultMessage = responseToMessage(event.response, response)
        }
        if (done) break
      }
      if (buffer.trim()) {
        const event = JSON.parse(buffer)
        if (event.type === "result" && event.response) resultMessage = responseToMessage(event.response, response)
        if (event.type === "error") throw new Error(event.error ?? "assistant request failed")
      }
      if (!resultMessage) throw new Error("assistant stream ended without a result")
      setMessages([...next, resultMessage])
    } catch (error) {
      const message = error instanceof DOMException && error.name === "AbortError"
        ? "The local assistant timed out before completing the request."
        : error instanceof Error ? error.message : "request failed"
      setMessages([...next, { role: "assistant", content: message }])
    } finally {
      window.clearTimeout(timeout)
      setLiveStage(null)
      setBusy(false)
    }
  }

  return (
    <section className="work-panel assistant-workspace">
      <div className="workspace-head"><div><span className="section-label">Observable local agent</span><h2>Clinical operations assistant</h2><p>Resolves common workflows deterministically, uses specialist services directly, and falls back to the local model only when language reasoning is needed.</p></div><div className="model-tag">Local execution</div></div>
      <div className="suggestion-row" aria-label="Example prompts">{examples.map((example) => <button type="button" key={example} onClick={() => setInput(example)}>{example}</button>)}</div>
      <div className="chat-log" aria-live="polite">
        {messages.map((message, index) => <Message key={`${message.role}-${index}`} message={message} />)}
        {busy && <div className="message assistant"><div className="message-label">NDHIS AI</div><div className="message-body thinking"><strong>{STAGE_LABELS[liveStage?.name ?? ""] ?? "Working locally"}</strong><span>{liveStage?.detail ?? "Resolving the request…"}</span>{liveStage?.model && <small>{liveStage.model}</small>}{liveStage?.tool && <small>{liveStage.tool}</small>}</div></div>}
      </div>
      <form className="composer" onSubmit={submit}><label htmlFor="assistant-input">Message</label><textarea id="assistant-input" rows={3} value={input} maxLength={8_000} onChange={(event) => setInput(event.target.value)} placeholder="Ask for a forecast, supported departments, historical backtest, radiology result or service status…" /><div className="composer-foot"><span>{input.length}/8000</span><button disabled={busy || !input.trim()}>Send request</button></div></form>
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
  return <div className={`message ${message.role}`}><div className="message-label">{message.role === "assistant" ? "NDHIS AI" : "Doctor"}</div><div className="message-body"><p>{message.content}</p>{(message.tool || message.routing || message.trace?.length) && <details className="tool-trace"><summary>Execution trace{message.tool ? ` · ${message.tool}` : ""}</summary><dl><div><dt>Routing</dt><dd>{message.routing ?? "—"}</dd></div>{message.intent && <div><dt>Intent</dt><dd>{message.intent.replaceAll("_", " ")}</dd></div>}<div><dt>LLM calls</dt><dd>{message.llmCalls ?? 0}</dd></div>{message.model && <div><dt>Model</dt><dd>{message.model}</dd></div>}<div><dt>Total latency</dt><dd>{message.latencyMs ?? "—"} ms</dd></div>{message.requestId && <div><dt>Request</dt><dd>{message.requestId}</dd></div>}{message.tool && <div><dt>Validated arguments</dt><dd><code>{JSON.stringify(message.arguments ?? {})}</code></dd></div>}</dl>{message.trace?.length ? <div className="execution-stages">{message.trace.map((stage, index) => <div className={`execution-stage ${stage.status}`} key={`${stage.name}-${index}`}><span>{STAGE_LABELS[stage.name] ?? stage.name}</span><strong>{stage.status}</strong><small>{stage.detail}</small>{stage.duration_ms !== undefined && <em>{stage.duration_ms} ms</em>}</div>)}</div> : null}</details>}</div></div>
}
