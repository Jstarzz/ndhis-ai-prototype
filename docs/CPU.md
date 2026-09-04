# CPU profile

The CPU profiles exercise the complete prototype on servers without GPUs before running the higher-quality GPU profile.

## Hardware gate

Run:

```bash
make cpu-probe
```

The probe chooses among three explicit CPU tiers:

```text
AVX2+      cpu
AVX only   legacy-cpu
SSE4.1+    westmere
```

Current vLLM x86 CPU serving requires Linux and AVX2 at minimum. AVX-512 is preferred. The legacy profile uses llama.cpp on AVX hosts. The Westmere profile exists for older SSE4.x Xeons such as the Xeon X5650 that do not implement AVX at all.

Dual-socket systems introduce NUMA effects. Benchmark the actual machine instead of extrapolating from core count alone.

## CPU model set

```text
agent        Qwen/Qwen3-1.7B
asr          Systran/faster-whisper-base
translation  qvac/TranslatePsy-EuroNano Tiny INTGEMM
forecast     amazon/chronos-2
radiology    TorchXRayVision DenseNet121 all
```

The standard CPU model files total about 4.8 GB. Runtime RAM is higher because the vLLM agent is served as float32 and each service needs working memory.

## Westmere model set

```text
agent        Qwen3-0.6B Q8 via llama.cpp
asr          faster-whisper base INT8
translation  TranslatePsy-EuroNano Tiny INTGEMM
forecast     autoregressive ridge
radiology    local chest-X-ray ONNX classifier via OpenCV DNN
```

The Westmere profile removes the two PyTorch-dependent specialist paths. CTranslate2 supports x86-64 processors with SSE4.1 or newer, so faster-whisper remains viable. The radiology ONNX contract requires a compatible chest-X-ray classifier at `models/westmere/radiology/model.onnx`. The configured labels, output type, and preprocessing must match the exported model.

The Westmere forecast is intentionally a lightweight autoregressive ridge model fitted against the synthetic operational history. It preserves the forecasting API and provides a measurable CPU baseline, but it is not the model intended for the eventual GPU deployment.

## Storage placement

Do not place model weights on a nearly full Proxmox root filesystem. Set `WESTMERE_MODEL_ROOT` to a directory backed by the large data volume, for example:

```text
/srv/ndhis-ai-models
```

The directory must contain:

```text
cpu/agent
cpu/asr
cpu/translation
westmere/radiology/model.onnx
```

## Fetch and run standard CPU

```bash
cp .env.cpu.example .env
make cpu-probe
make fetch-cpu
make generate
make preflight-cpu
make up-cpu
```

## Run Westmere

```bash
cp .env.westmere.example .env
make cpu-probe
make fetch-westmere-core
make generate
make preflight-westmere
make up-westmere
make smoke
```

Then open `http://localhost:3000`.

## Benchmark

Standard CPU:

```bash
make eval-cpu
make bench-cpu
```

Westmere:

```bash
make eval-westmere
make bench-westmere
```

The report records request throughput, p50/p95 latency, CPU saturation, RAM use, system load, and GPU metrics when a GPU is present. Use the highest concurrency that still meets the chosen latency/error target as the measured capacity of that exact host.

## Legacy CPU profile

For AVX-capable x86 hosts that fail the AVX2 vLLM gate:

```bash
cp .env.cpu.example .env
make fetch-legacy-cpu
make generate
make preflight-legacy-cpu
make up-legacy-cpu
make smoke
make eval-legacy-cpu
make bench-legacy-cpu
```

This profile uses the same specialist CPU services but replaces vLLM/Qwen3-1.7B with a native-compiled llama.cpp server and Qwen3-0.6B Q8. It is a test-runtime choice, not an automatic fallback.
