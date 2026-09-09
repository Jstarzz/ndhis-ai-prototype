import { useEffect, useState } from "react"
import { API_BASE, DEMO_KEY, fetchWithTimeout, safeJson } from "../api"
import type { RadiologyResult } from "../types"

export function Radiology() {
  const [file, setFile] = useState<File | null>(null)
  const [preview, setPreview] = useState<string | null>(null)
  const [prompt, setPrompt] = useState("Describe the clinically relevant findings in this chest radiograph concisely. State uncertainty.")
  const [result, setResult] = useState<RadiologyResult | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")

  useEffect(() => {
    if (!file || file.type === "application/dicom" || file.name.toLowerCase().endsWith(".dcm")) { setPreview(null); return }
    const objectUrl = URL.createObjectURL(file)
    setPreview(objectUrl)
    return () => URL.revokeObjectURL(objectUrl)
  }, [file])

  async function run() {
    if (!file) return
    setBusy(true); setError(""); setResult(null)
    const body = new FormData(); body.append("file", file); body.append("prompt", prompt)
    try {
      const response = await fetchWithTimeout(`${API_BASE}/api/radiology`, { method: "POST", headers: { "X-NDHIS-Demo-Key": DEMO_KEY, "X-NDHIS-User": "demo-doctor", "X-NDHIS-Role": "doctor" }, body }, 95_000)
      const payload = await safeJson(response)
      if (!response.ok) throw new Error(payload.detail ?? payload.error ?? "radiology analysis failed")
      setResult(payload)
    } catch (err) { setError(cleanRadiologyError(err instanceof Error ? err.message : "radiology analysis failed")) }
    finally { setBusy(false) }
  }

  return (
    <section className="work-panel">
      <div className="workspace-head"><div><span className="section-label">Medical vision prototype</span><h2>Radiology assistance</h2><p>Analyze public or de-identified chest imaging with explicit model provenance, score-level output and clinician review.</p></div><div className="review-state">Human review required</div></div>
      <div className="radiology-layout">
        <div className="study-pane"><label className="file-drop"><span>Study file</span><strong>{file ? file.name : "Choose PNG, JPEG or DICOM"}</strong><small>Public or de-identified demo material only</small><input type="file" accept="image/png,image/jpeg,.dcm,application/dicom" onChange={(event) => setFile(event.target.files?.[0] ?? null)} /></label><div className="image-preview">{preview ? <img src={preview} alt="Selected radiology study preview" /> : <div><strong>{file ? "DICOM selected" : "No study selected"}</strong><span>{file ? "Preview unavailable; analysis can still run if the local model is healthy." : "Select a public or de-identified image to begin."}</span></div>}</div><label className="prompt-field">Review instruction<textarea rows={4} maxLength={2_000} value={prompt} onChange={(event) => setPrompt(event.target.value)} /></label><button type="button" disabled={!file || busy || !prompt.trim()} onClick={run}>{busy ? "Validating model and analyzing…" : "Analyze locally"}</button></div>
        <div className="findings-pane" aria-live="polite"><div className="findings-head"><div><span className="section-label">Assistant output</span><h3>AI-assisted findings</h3></div>{result && <span>{result.latency_ms} ms</span>}</div>{error && <div className="error-box" role="alert"><strong>Analysis unavailable</strong><span>{error}</span><small>No radiology finding was produced from this failure.</small></div>}{!result && !error && <div className="empty-state large"><strong>No analysis yet</strong><span>The service self-tests its configured model before accepting studies. Output is not an autonomous diagnosis and must be reviewed by a qualified clinician.</span></div>}{result && <><div className="findings-text">{result.findings}</div>{result.predictions?.length ? <div className="radiology-predictions"><span className="section-label">Model scores</span>{result.predictions.slice(0, 8).map((prediction) => <div className="radiology-score" key={prediction.label}><span>{prediction.label}</span><strong>{(prediction.score * 100).toFixed(1)}%</strong></div>)}</div> : null}<div className="review-callout"><strong>Clinician review required</strong><span>These are model detections/scores, not a signed radiology report, diagnosis or treatment decision.</span></div><dl className="result-trace"><div><dt>Result ID</dt><dd>{result.result_id}</dd></div><div><dt>Source file</dt><dd>{result.filename}</dd></div>{result.model && <div><dt>Model</dt><dd>{result.model}</dd></div>}{result.backend && <div><dt>Backend</dt><dd>{result.backend}</dd></div>}{result.device && <div><dt>Device</dt><dd>{result.device}</dd></div>}<div><dt>Review flag</dt><dd>{result.review_required ? "Required" : "Not reported"}</dd></div></dl></>}</div>
      </div>
    </section>
  )
}

function cleanRadiologyError(value: string) {
  if (value.includes("radiology model unavailable")) return value
  if (value.includes("GatherLayerImpl") || value.includes("cv2.error")) return "The configured local ONNX classifier failed its inference contract. Replace it with a compatible chest-X-ray model before using radiology analysis."
  return value
}
