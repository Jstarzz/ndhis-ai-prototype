import { FormEvent, useState } from "react"
import { API_BASE, fetchWithTimeout, headers, safeJson } from "../api"
import type { ChatMessage } from "../types"

export function Assistant() {
  const [messages, setMessages] = useState<ChatMessage[]>([{ role: "assistant", content: "I can explain this prototype and route supported operational requests to local tools. Try a JNF patient-volume forecast or ask for service status." }])
  const [input, setInput] = useState("")
  const [busy, setBusy] = useState(false)
  const examples = ["Forecast A&E patient arrivals for the next 30 days.", "What AI services are currently available?", "Forecast respiratory disease incidence at JNF for 14 days."]

  async function submit(event: FormEvent) {
    event.preventDefault()
    const text = input.trim()
    if (!text || busy) return
    const next = [...messages, { role: "user" as const, content: text }]
    setMessages(next)
    setInput("")
    setBusy(true)
    try {
      const response = await fetchWithTimeout(`${API_BASE}/api/chat`, { method: "POST", headers, body: JSON.stringify({ messages: next.map(({ role, content }) => ({ role, content })) }) }, 95_000)
      const payload = await safeJson(response)
      if (!response.ok) throw new Error(payload.error ?? "assistant request failed")
      setMessages([...next, { role: "assistant", content: payload.answer, latencyMs: payload.latency_ms, tool: payload.tool, arguments: payload.arguments, requestId: payload.request_id ?? response.headers.get("X-NDHIS-Request-ID") ?? undefined }])
    } catch (error) {
      setMessages([...next, { role: "assistant", content: error instanceof Error ? error.message : "request failed" }])
    } finally { setBusy(false) }
  }

  return (
    <section className="work-panel assistant-workspace">
      <div className="workspace-head"><div><span className="section-label">Grounded local agent</span><h2>Clinical operations assistant</h2><p>Routes supported requests to specialist services and returns tool-grounded results.</p></div><div className="model-tag">Local agent</div></div>
      <div className="suggestion-row" aria-label="Example prompts">{examples.map((example) => <button type="button" key={example} onClick={() => setInput(example)}>{example}</button>)}</div>
      <div className="chat-log" aria-live="polite">
        {messages.map((message, index) => <div className={`message ${message.role}`} key={`${message.role}-${index}`}><div className="message-label">{message.role === "assistant" ? "NDHIS AI" : "Doctor"}</div><div className="message-body"><p>{message.content}</p>{message.tool && <details className="tool-trace"><summary>Tool trace · {message.tool}</summary><dl><div><dt>Latency</dt><dd>{message.latencyMs ?? "—"} ms</dd></div>{message.requestId && <div><dt>Request</dt><dd>{message.requestId}</dd></div>}<div><dt>Arguments</dt><dd><code>{JSON.stringify(message.arguments ?? {})}</code></dd></div></dl></details>}</div></div>)}
        {busy && <div className="message assistant"><div className="message-label">NDHIS AI</div><div className="message-body thinking">Routing request to local services…</div></div>}
      </div>
      <form className="composer" onSubmit={submit}><label htmlFor="assistant-input">Message</label><textarea id="assistant-input" rows={3} value={input} maxLength={8_000} onChange={(event) => setInput(event.target.value)} placeholder="Ask for a supported forecast or local service status…" /><div className="composer-foot"><span>{input.length}/8000</span><button disabled={busy || !input.trim()}>Send request</button></div></form>
    </section>
  )
}
