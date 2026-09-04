# NDHIS AI Prototype

Fully local prototype for NDHIS-style AI capabilities: live translation, hospital forecasting, radiology assistance, and a tool-routing local agent.

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

The gateway owns identity, doctor-only prototype access, rate limits, concurrency limits, request IDs, tool execution, and audit metadata. Translation uses a direct WebSocket because it is latency-sensitive and performs its own identity/session audit.

## Languages and runtimes

- Go: gateway and tool execution
- TypeScript/React: demo UI
- Python: ASR, forecasting, radiology
- vLLM: GPU and modern-x86 CPU agent serving
- Node/QVAC: ultra-light CPU translation engine
- Docker Compose: reproducible local deployment

## GPU profile

```text
agent        LiquidAI/LFM2.5-350M
asr          faster-whisper large-v3-turbo
translation  NLLB-200 distilled 600M
forecast     TimesFM 3
radiology    MedGemma 1.5 4B
```

Model repositories total about 14.8 GB. Keep at least 40 GB free for weights plus Docker/CUDA/Python/vLLM layers.

```bash
cp .env.example .env
python scripts/generate_synthetic_data.py
python scripts/verify_models.py --profile gpu
docker compose up --build
```

## CPU profile

The CPU profile is intentionally smaller instead of moving the GPU models unchanged onto CPU.

```text
agent        Qwen3-1.7B
asr          faster-whisper base INT8
translation  TranslatePsy-EuroNano Tiny INT8
forecast     Chronos-2 120M
radiology    TorchXRayVision DenseNet121
```

The CPU model set is about 4.8 GB on disk. A 48 GB host has substantial memory headroom; CPU ISA, memory bandwidth, NUMA behavior, and model throughput are the important limits.

Current vLLM x86 CPU serving requires Linux and AVX2 at minimum. AVX-512 is preferred. Run the hardware probe before downloading anything:

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

The CPU translation profile supports English, German, Spanish, French, Italian, Portuguese, Finnish, Czech, Dutch, and Swedish. Unsupported languages fail explicitly rather than switching models.

For pre-AVX2 Xeons, the separate legacy CPU profile uses native llama.cpp + Qwen3-0.6B Q8 and only about 1.35 GB of model weights. See `docs/CPU.md` for details.

## Local model contract

Runtime model downloads are disabled. Missing or incompatible model weights are startup failures. Compatible model repositories can be replaced in their role folders without changing the NDHIS-facing APIs.

```text
models/                  GPU profile
models/cpu/              CPU profile
```

The GPU TimesFM/NLLB selections have licensing restrictions and are prototype choices. Production model selection requires a separate licensing and validation pass.

No custom fine-tuning is required to run this prototype. The agent can later be fine-tuned on the fixed NDHIS tool schema without putting patient records into model weights.

## UI and API

Open `http://localhost:3000`.

```text
POST /api/chat
POST /api/forecast
POST /api/radiology
GET  /api/health
GET  /api/system
WS   ws://localhost:8101/ws/translate
```

HTTP requests require the demo key plus doctor identity headers. Translation uses the same identity boundary over WebSocket. Audit metadata does not persist translation transcript text.

## Benchmarking

GPU:

```bash
make bench
```

CPU:

```bash
make bench-cpu
```

The benchmark suite records throughput, p50/p95 latency, CPU saturation, RAM, system load, and GPU telemetry when available. Raw vLLM capacity and the full gateway/tool path are measured separately so hardware sizing is based on the actual bottleneck.

Tool routing accuracy:

```bash
python scripts/eval_agent.py
```

Run the same eval after any NDHIS-specific tool-calling fine-tune.

## Prototype boundary

Forecasting uses deterministic synthetic hospital operations data. Radiology tests should use public de-identified images. The prototype demonstrates local software architecture and measurable inference behavior; it is not clinically validated and does not autonomously write diagnoses or treatments into a medical record.
