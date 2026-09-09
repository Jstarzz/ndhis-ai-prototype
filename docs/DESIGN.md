# NDHIS AI web design contract

## Direction

Clinical workstation, not consumer AI product and not generic SaaS dashboard. The interface should feel like controlled hospital operations software: quiet, legible, traceable, and dense enough to be useful without becoming cluttered.

## Visual rules

- No gradients, glow halos, glassmorphism, neon accents, or decorative grids.
- No giant marketing hero. Page titles are functional.
- Avoid pill-shaped containers except when a semantic status absolutely requires a compact inline state; prefer plain text with a status dot.
- Do not create equal-card grids for arbitrary content. Use tables, rows, split panes, and sections that match the information structure.
- Avoid icon tiles, sparkle/wand/brain/robot AI iconography, and decorative medical icons.
- Functional icons are optional, not required. Text labels must remain sufficient.
- Use system sans for UI copy and Georgia for restrained page/section headings. Do not add a webfont dependency without a concrete need.
- Corners stay small (0-5px) for controls and primary containers. Do not round every box.
- Primary palette: white/grey workstation surfaces, dark navy navigation, restrained clinical teal, amber for prototype warnings, red for errors.
- Use borders and whitespace before shadows. Primary containers should not float.
- Preserve visible keyboard focus and reduced-motion behavior.

## Interaction rules

- Every AI result that invokes a specialist tool must expose its tool trace and latency.
- Forecasts must show uncertainty, not only a single headline number.
- Radiology always displays the human-review requirement near the output.
- Audit views show operational trace data but do not expose doctor identity.
- Empty/loading/error states must explain what the user can do next.
- The main workflows must remain usable at mobile widths even though desktop is the primary demo surface.

## Copy rules

- No claims of diagnosis, treatment, clinical validation, production integration, or national-scale capacity.
- Prefer concrete terms such as "local inference", "tool route", "synthetic data", "request ID", and model/service names.
- Do not use "revolutionary", "seamless", "intelligent insights", "AI-powered", "next-generation", or similar marketing filler.
