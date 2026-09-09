# NDHIS AI Prototype

Fully local NDHIS-style AI prototype for clinical operations: live translation, hospital forecasting, radiology assistance, and a tool-routing local agent.

## What the prototype demonstrates

- One auditable local gateway in front of specialist AI services
- Tool-routed assistant responses instead of unsupported model improvisation
- Live local speech transcription and translation
- Synthetic JNF demand forecasting with uncertainty ranges
- Local radiology assistance with explicit clinician review
- Service/model visibility, rate limits, concurrency limits, request IDs, and recent audit traces
- CPU, legacy CPU, Westmere, and GPU deployment profiles

It does **not** claim clinical validation, autonomous diagnosis/treatment, production patient-data integration, or national-scale capacity.

## Architecture

```text
Browser / NDHIS
      |
      v
Go AI gateway
      |
      +--> local agent runtime --> specialist tool selection
      +--> forecast service
      +--> radiology service

Browser microphone
      |
      v
local ASR --> local translation engine
```

The gateway owns identity, doctor-only prototype access, rate limits, concurrency limits, bounded request contracts, request IDs, tool validation/execution, and audit metadata. Translation uses a direct WebSocket because it is latency-sensitive and performs its own identity/session audit.

## Languages and runtimes

- Go: gateway and tool execution
- TypeScript/React: clinical-workstation demo UI
- Python: ASR, forecasting, radiology
- vLLM: GPU and modern-x86 CPU agent serving
- llama.cpp: legacy and Westmere CPU serving
- Node/QVAC: ultra-light CPU translation engine
- Docker Compose: reproducible local deployment

## Westmere / dual-socket CPU target

For the older dual-socket Xeon target, the default agent is now:

```text
agent        Qwen3.5-4B Q4_K_M via llama.cpp
asr          faster-whisper base INT8
translation  TranslatePsy-EuroNano Tiny INTGEMM
forecast     autoregressive ridge
radiology    local chest-X-ray ONNX classifier
```

The previous Qwen3-0.6B agent remains relevant only as a plumbing baseline. Qwen3.5-4B is the interactive default; Qwen3.5-9B Q4_K_M is supported as a quality option after benchmarking. The Westmere llama.cpp profile enables NUMA-aware distribution so the dual-socket host is treated as a NUMA machine rather than one flat pool of threads. Memory locking is left optional for host-specific tuning.

```bash
cp .env.westmere.example .env
make cpu-probe
make fetch-westmere-core
make generate
make preflight-westmere
make up-westmere
make smoke
make eval-westmere
make bench-westmere
```

See `docs/CPU.md` for topology checks, NUMA strategy, and the 9B option.

## Standard CPU profile

```text
agent        Qwen3-1.7B
asr          faster-whisper base INT8
translation  TranslatePsy-EuroNano Tiny INT8
forecast     Chronos-2 120M
radiology    TorchXRayVision DenseNet121
```

Current vLLM x86 CPU serving requires Linux and AVX2 at minimum. Run the hardware probe before downloading anything:

```bash
make cpu-probe
```

Then:

```bash
cp .env.cpu.example .env
make fetch-cpu
make generate
make preflight-cpu
make up-cpu
make smoke
```

## GPU profile

The GPU profile remains a separate capacity/architecture path. Its model selections are prototype references, not final clinical recommendations. Production model selection requires measured task quality, licensing review, hardware sizing, and clinical validation.

```bash
cp .env.example .env
python scripts/generate_synthetic_data.py
python scripts/verify_models.py --profile gpu
docker compose up --build
```

## Local model contract

Runtime model downloads are disabled. Missing or incompatible model weights are startup failures. Compatible model repositories can be replaced in their role folders without changing the NDHIS-facing APIs.

```text
models/                  GPU profile
models/cpu/              CPU / Westmere shared specialist weights
models/westmere/         Westmere-specific radiology weight
```

No custom fine-tuning is required to run the prototype. The routing agent can later be tuned against the fixed NDHIS tool schema without putting patient records into model weights.

## UI and API

Open `http://localhost:3000`.

```text
POST /api/chat
POST /api/forecast
POST /api/radiology
GET  /api/radiology/{id}
GET  /api/health
GET  /api/system
GET  /api/audit/recent?limit=12
WS   ws://localhost:8101/ws/translate
```

HTTP requests require the demo key plus doctor identity headers. The recent-audit endpoint intentionally redacts user/role identity before returning data to the browser. Translation uses the same identity boundary over WebSocket. Audit metadata does not persist translation transcript text.

## Benchmarking

GPU:

```bash
make bench
```

CPU:

```bash
make bench-cpu
```

Westmere:

```bash
make bench-westmere
```

The benchmark suite records throughput, p50/p95 latency, CPU saturation, RAM, system load, and GPU telemetry when available. Raw model capacity and the full gateway/tool path are measured separately so hardware sizing is based on the actual bottleneck.

Tool routing accuracy:

```bash
python scripts/eval_agent.py
```

Run the same eval whenever the routing model, prompt, quantization, or tool schema changes.

## Prototype boundary

Forecasting uses deterministic synthetic hospital operations data. Radiology tests should use public or de-identified images. The prototype demonstrates local software architecture and measurable inference behavior; it is not clinically validated and does not autonomously write diagnoses or treatments into a medical record.
