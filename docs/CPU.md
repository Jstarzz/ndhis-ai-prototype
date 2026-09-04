# CPU profile

The CPU profile exists to exercise the complete prototype on a server without a GPU before running the higher-quality GPU profile.

## Hardware gate

Current vLLM x86 CPU serving requires Linux and AVX2 at minimum. AVX-512 is preferred. Run:

```bash
make cpu-probe
```

If `vllm_cpu_ready` is false but `legacy_cpu_ready` is true, use the explicit legacy CPU profile. It compiles llama.cpp for the host CPU and serves Qwen3-0.6B Q8 through the same OpenAI-compatible gateway contract.

A dual-socket system also introduces NUMA effects. The profile uses vLLM CPU auto thread binding by default and reserves two cores for serving. Benchmark the actual machine instead of extrapolating from core count alone. If the probe shows multiple NUMA nodes, test explicit `VLLM_CPU_OMP_THREADS_BIND` layouts after the baseline run.

## CPU model set

```text
agent        Qwen/Qwen3-1.7B
asr          Systran/faster-whisper-base
translation  qvac/TranslatePsy-EuroNano Tiny INTGEMM
forecast     amazon/chronos-2
radiology    TorchXRayVision DenseNet121 all
```

The model files total about 4.8 GB. Runtime RAM is higher because the vLLM agent is served as float32 for broad old-Xeon compatibility and each service needs working memory. A 48 GB host has substantial memory headroom for this test profile.

The CPU translation profile supports English, German, Spanish, French, Italian, Portuguese, Finnish, Czech, Dutch, and Swedish. It intentionally trades NLLB's broad language coverage for dramatically lower CPU cost. Unsupported languages fail explicitly.

The CPU radiology backend is a chest X-ray classifier for software/research demonstration only. It does not replace the richer MedGemma GPU backend and is not a clinically validated diagnostic system.

## Fetch and run

```bash
cp .env.cpu.example .env
make cpu-probe
make fetch-cpu
make generate
make preflight-cpu
make up-cpu
```

Run `make smoke` after startup, then open `http://localhost:3000`.

## Benchmark

```bash
make eval-cpu
make bench-cpu
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

This profile uses the same specialist CPU services but replaces vLLM/Qwen3-1.7B with a native-compiled llama.cpp server and Qwen3-0.6B Q8. It is a test-runtime choice, not an automatic fallback. The model set is about 1.35 GB on disk.
