# CPU profiles

The CPU profiles exercise the complete prototype on servers without GPUs. They are intentionally smaller than the GPU model set and are split by CPU instruction support.

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

Dual-socket systems are NUMA machines. Core count alone is not a capacity estimate; memory placement and cross-socket traffic materially affect inference. The Westmere agent therefore enables llama.cpp NUMA distribution by default and keeps model serving configurable through environment variables.

Inspect the host before tuning:

```bash
lscpu
lscpu -e=CPU,SOCKET,NODE,CORE
numactl --hardware 2>/dev/null || true
```

## Standard CPU model set

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
agent        Qwen3.5-4B Q4_K_M via llama.cpp
asr          faster-whisper base INT8
translation  TranslatePsy-EuroNano Tiny INTGEMM
forecast     autoregressive ridge
radiology    local chest-X-ray ONNX classifier via OpenCV DNN
```

The previous 0.6B routing model was useful only as a plumbing test. The default is now Qwen3.5-4B Q4_K_M: large enough to provide materially better instruction following and tool routing while remaining reasonable for an old dual-socket CPU host. The default context is 4096 tokens because long context on this class of machine costs latency and memory bandwidth for little prototype value.

A Qwen3.5-9B Q4_K_M quality option is supported through the same profile. It fits comfortably in a 42 GB RAM host, but fitting in RAM is not the same as being fast enough for an interactive demo. Benchmark it before making it the default.

To try the quality option:

```bash
AGENT_MODEL_FILE=Qwen3.5-9B-Q4_K_M.gguf \
AGENT_MODEL_NAME=ndhis-agent-westmere \
AGENT_MODEL_LABEL="Qwen3.5-9B Q4_K_M" \
make fetch-westmere-core
```

Then set the same `AGENT_MODEL_FILE` and `AGENT_MODEL_LABEL` values in `.env` before startup. Keep the served alias `AGENT_MODEL_NAME=ndhis-agent-westmere` stable so eval and benchmark tooling does not depend on which weight file is selected.

### NUMA strategy

The default Westmere agent command uses:

```text
--numa distribute
--threads 12
--threads-batch 12
```

Twelve threads matches a dual 6-core Xeon X5650-class host and deliberately targets physical cores rather than SMT threads. Change `WESTMERE_AGENT_THREADS` and `WESTMERE_AGENT_BATCH_THREADS` after checking the actual topology.

`WESTMERE_AGENT_NUMA` accepts the llama.cpp NUMA modes. `distribute` spreads execution over NUMA nodes; `isolate` keeps execution on the node where the process starts; `numactl` follows an external CPU map. Do not hardcode socket CPU IDs in the repository because Linux CPU numbering differs by machine and firmware.

For concurrent workloads, benchmark two approaches on the real host:

1. `distribute` for the agent so one request can use both sockets.
2. Pin the agent and streaming translation stack to separate NUMA nodes with `numactl` if concurrent translation is more important than peak single-request latency.

Keep whichever wins the measured end-to-end benchmark. Do not infer the winner from aggregate core count.

## Westmere specialist notes

The Westmere profile removes the two PyTorch-dependent specialist paths. CTranslate2 supports x86-64 processors with SSE4.1 or newer, so faster-whisper remains viable. The radiology ONNX contract requires a compatible chest-X-ray classifier at `models/westmere/radiology/model.onnx`. The configured labels, output type, and preprocessing must match the exported model.

The Westmere forecast is intentionally a lightweight autoregressive ridge model fitted against the synthetic operational history. It preserves the forecasting API and provides a measurable CPU baseline, but it is not the model intended for final clinical validation.

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

For the Westmere box, benchmark Qwen3.5-4B first. Only promote 9B if its end-to-end latency remains acceptable for the live demonstration.

## Legacy AVX profile

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

This compatibility profile still uses the smaller Qwen3 model and exists for testing older AVX hardware. The Westmere deployment path above is the tuned target for the dual-socket SSE4.x machine.
