import { useEffect, useRef, useState } from "react"
import { DEMO_KEY, TRANSLATION_HEALTH, TRANSLATION_WS } from "../api"
import type { TranslationHealth, TranslationResult } from "../types"
import { Status } from "./shared"

const languageLabels: Record<string, string> = {
  eng_Latn: "English", spa_Latn: "Spanish", fra_Latn: "French", hat_Latn: "Haitian Creole",
  por_Latn: "Portuguese", deu_Latn: "German", ita_Latn: "Italian", fin_Latn: "Finnish",
  ces_Latn: "Czech", nld_Latn: "Dutch", swe_Latn: "Swedish",
}

export function Translation() {
  const [running, setRunning] = useState(false)
  const [connecting, setConnecting] = useState(false)
  const [targets, setTargets] = useState<string[]>(["spa_Latn", "eng_Latn", "fra_Latn", "hat_Latn"])
  const [target, setTarget] = useState("spa_Latn")
  const [results, setResults] = useState<TranslationResult[]>([])
  const [error, setError] = useState("")
  const socketRef = useRef<WebSocket | null>(null)
  const contextRef = useRef<AudioContext | null>(null)
  const streamRef = useRef<MediaStream | null>(null)
  const processorRef = useRef<ScriptProcessorNode | null>(null)

  const secureContext = window.isSecureContext
  const microphoneAPI = Boolean(navigator.mediaDevices?.getUserMedia)
  const mixedContentSocket = window.location.protocol === "https:" && TRANSLATION_WS.startsWith("ws://")
  const microphoneReady = secureContext && microphoneAPI && !mixedContentSocket

  useEffect(() => {
    fetch(TRANSLATION_HEALTH).then((response) => response.json() as Promise<TranslationHealth>).then((payload) => {
      if (!payload.supported_targets?.length) return
      setTargets(payload.supported_targets)
      setTarget((current) => payload.supported_targets?.includes(current) ? current : payload.supported_targets![0])
    }).catch(() => undefined)
    return () => stop()
  }, [])

  async function start() {
    if (running || connecting) return
    setError("")
    if (!secureContext || !microphoneAPI) {
      setError("Microphone access requires a secure browser context. Open NDHIS AI over HTTPS (or localhost) and allow microphone permission.")
      return
    }
    if (mixedContentSocket) {
      setError("This page is using HTTPS but the translation WebSocket is configured as ws://. Configure VITE_TRANSLATION_WS with wss:// through the HTTPS reverse proxy.")
      return
    }

    setConnecting(true)
    let ws: WebSocket | null = null
    let stream: MediaStream | null = null
    let context: AudioContext | null = null
    let processor: ScriptProcessorNode | null = null
    try {
      stream = await navigator.mediaDevices.getUserMedia({ audio: { echoCancellation: true, noiseSuppression: true } })
      context = new AudioContext()

      ws = new WebSocket(`${TRANSLATION_WS}?key=${encodeURIComponent(DEMO_KEY)}&user=${encodeURIComponent("demo-doctor")}&role=${encodeURIComponent("doctor")}`)
      ws.binaryType = "arraybuffer"
      await new Promise<void>((resolve, reject) => {
        const timeout = window.setTimeout(() => reject(new Error("translation service timed out")), 8_000)
        ws!.onopen = () => { window.clearTimeout(timeout); resolve() }
        ws!.onerror = () => { window.clearTimeout(timeout); reject(new Error("translation socket failed")) }
      })
      ws.send(JSON.stringify({ source: "auto", target }))
      ws.onmessage = (event) => {
        try { setResults((current) => [JSON.parse(event.data) as TranslationResult, ...current].slice(0, 20)) }
        catch { setError("Translation service returned an invalid response.") }
      }
      ws.onclose = () => setRunning(false)

      const source = context.createMediaStreamSource(stream)
      processor = context.createScriptProcessor(4096, 1, 1)
      processor.onaudioprocess = (event) => {
        if (ws?.readyState !== WebSocket.OPEN) return
        const input = event.inputBuffer.getChannelData(0)
        const downsampled = downsample(input, context!.sampleRate, 16_000)
        const pcm = new Int16Array(downsampled.length)
        for (let i = 0; i < downsampled.length; i++) pcm[i] = Math.round(Math.max(-1, Math.min(1, downsampled[i])) * 32767)
        ws.send(pcm.buffer)
      }
      source.connect(processor)
      processor.connect(context.destination)
      socketRef.current = ws
      contextRef.current = context
      streamRef.current = stream
      processorRef.current = processor
      setRunning(true)
    } catch (err) {
      processor?.disconnect()
      if (context) void context.close()
      stream?.getTracks().forEach((track) => track.stop())
      ws?.close()
      if (err instanceof DOMException && err.name === "NotAllowedError") setError("Microphone permission was denied. Allow microphone access for this HTTPS site and try again.")
      else setError(err instanceof Error ? err.message : "Unable to start microphone translation.")
    } finally { setConnecting(false) }
  }

  function stop() {
    processorRef.current?.disconnect()
    if (contextRef.current) void contextRef.current.close()
    streamRef.current?.getTracks().forEach((track) => track.stop())
    socketRef.current?.close()
    processorRef.current = null
    contextRef.current = null
    streamRef.current = null
    socketRef.current = null
    setRunning(false)
    setConnecting(false)
  }

  return (
    <section className="work-panel">
      <div className="workspace-head"><div><span className="section-label">Local speech pipeline</span><h2>Live translation</h2><p>Microphone audio is chunked locally, transcribed, then translated to the selected language.</p></div><Status state={running ? "ready" : connecting ? "checking" : microphoneReady ? "idle" : "error"} label={running ? "Microphone live" : connecting ? "Connecting" : microphoneReady ? "Stopped" : "HTTPS required"} /></div>
      {!microphoneReady && <div className="secure-context-note"><strong>Secure microphone access required</strong><div>{mixedContentSocket ? "The page is secure, but the translation socket must also use wss://." : "Browser microphone APIs are unavailable on this HTTP origin. Serve the frontend over HTTPS (or use localhost)."}</div></div>}
      <div className="control-strip"><label>Target language<select disabled={running || connecting} value={target} onChange={(event) => setTarget(event.target.value)}>{targets.map((code) => <option value={code} key={code}>{languageLabels[code] ?? code}</option>)}</select></label><button type="button" className={running ? "secondary danger" : ""} disabled={connecting || !microphoneReady} onClick={running ? stop : start}>{running ? "Stop microphone" : connecting ? "Connecting…" : "Start microphone"}</button></div>
      {error && <div className="error-box" role="alert"><strong>Translation unavailable</strong><span>{error}</span></div>}
      <div className="translation-layout">
        <div className="translation-intro"><span className="section-label">Session</span><h3>{running ? "Listening" : microphoneReady ? "Ready when you are" : "Waiting for HTTPS"}</h3><p>{running ? "Speak naturally. Recent transcript/translation pairs will appear to the right." : microphoneReady ? "Choose a target language and start the microphone. Use synthetic or non-sensitive demo speech." : "Open the HTTPS deployment before starting live speech translation."}</p><dl className="compact-facts"><div><dt>Source</dt><dd>Auto detect</dd></div><div><dt>Target</dt><dd>{languageLabels[target] ?? target}</dd></div><div><dt>Processing</dt><dd>Local</dd></div><div><dt>Browser context</dt><dd>{secureContext ? "Secure" : "Insecure"}</dd></div></dl></div>
        <div className="translation-feed" aria-live="polite">
          {results.length === 0 && <div className="empty-state"><strong>No speech segments yet</strong><span>Translated segments appear here during the session.</span></div>}
          {results.map((result, index) => <article className="translation-item" key={`${result.latency_ms ?? 0}-${index}`}>{result.error ? <div className="inline-error">{result.error}</div> : <><div className="translation-meta"><span>{result.source_language ?? "detected"} → {result.target_language ?? target}</span><span>{result.latency_ms ?? "—"} ms</span></div><div className="source-text">{result.transcript || "—"}</div><div className="translated-text">{result.translation || "—"}</div></>}</article>)}
        </div>
      </div>
    </section>
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
