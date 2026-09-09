import { useEffect, useState } from "react"
import { API_BASE, fetchWithTimeout } from "./api"
import { Assistant } from "./components/Assistant"
import { Forecasting } from "./components/Forecasting"
import { Overview } from "./components/Overview"
import { Radiology } from "./components/Radiology"
import { System } from "./components/System"
import { Translation } from "./components/Translation"
import type { ServiceState, Tab } from "./types"

const navItems: Array<{ id: Tab; label: string; detail: string }> = [
  { id: "overview", label: "Overview", detail: "Readiness and demo paths" },
  { id: "assistant", label: "Assistant", detail: "Grounded tool routing" },
  { id: "translation", label: "Translation", detail: "Live local speech" },
  { id: "forecasting", label: "Forecasting", detail: "Synthetic JNF demand" },
  { id: "radiology", label: "Radiology", detail: "Human-review support" },
  { id: "system", label: "System", detail: "Models, limits and audit" },
]

function App() {
  const [tab, setTab] = useState<Tab>("overview")
  const [health, setHealth] = useState<Record<string, ServiceState>>({})

  useEffect(() => {
    let active = true
    const refresh = async () => {
      try {
        const response = await fetchWithTimeout(`${API_BASE}/api/health`, { cache: "no-store" }, 6_000)
        const payload = await response.json()
        if (active) setHealth(payload.services ?? {})
      } catch {
        if (active) setHealth({ agent: "offline", translation: "offline", forecasting: "offline", radiology: "offline" })
      }
    }
    void refresh()
    const timer = window.setInterval(refresh, 8_000)
    return () => { active = false; window.clearInterval(timer) }
  }, [])

  const readyCount = Object.values(health).filter((state) => state === "ready").length
  const current = navItems.find((item) => item.id === tab) ?? navItems[0]

  return (
    <div className="app-frame">
      <aside className="sidebar">
        <div className="brand-block"><div className="brand-mark" aria-hidden="true">N</div><div><strong>NDHIS AI</strong><span>Clinical prototype</span></div></div>
        <div className="facility-block"><span>Demo facility</span><strong>JNF General Hospital</strong><small>Local infrastructure · synthetic data</small></div>
        <nav className="side-nav" aria-label="Prototype sections">
          {navItems.map((item) => (
            <button type="button" key={item.id} className={tab === item.id ? "nav-item active" : "nav-item"} onClick={() => setTab(item.id)}>
              <span>{item.label}</span><small>{item.detail}</small>
            </button>
          ))}
        </nav>
        <div className="sidebar-foot"><div className="runtime-line"><span className="status-dot ready-dot" /> Local inference</div><span>{readyCount}/4 services ready</span></div>
      </aside>

      <main className="content-shell">
        <header className="page-header"><div><p className="kicker">NDHIS local clinical AI</p><h1>{current.label}</h1></div><div className="header-meta"><span>Doctor demo session</span><strong>De-identified only</strong></div></header>
        <div className="scope-notice" role="note">Prototype environment. No autonomous diagnosis, treatment decision, or production patient-data integration.</div>
        {tab === "overview" && <Overview health={health} onNavigate={setTab} />}
        {tab === "assistant" && <Assistant />}
        {tab === "translation" && <Translation />}
        {tab === "forecasting" && <Forecasting />}
        {tab === "radiology" && <Radiology />}
        {tab === "system" && <System />}
      </main>
    </div>
  )
}

export default App
