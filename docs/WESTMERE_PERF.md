# Westmere inference performance

The dual-X5650 deployment is constrained by CPU ISA and memory topology, not RAM capacity. The X5650 exposes SSE4.x but not AVX/AVX2/F16C, so modern transformer inference remains expensive even when a quantized model fits comfortably in memory.

## Measured baseline

The initial 8B-class benchmark on this host produced roughly:

| Output | Prompt/TTFT | Decode | Total |
| --- | ---: | ---: | ---: |
| 64 tokens | 8.1 s on a tiny prompt | 1.92 tok/s | 41 s |
| 256 tokens | 8.0 s on a tiny prompt | 1.84 tok/s | 146 s |
| 700 tokens | 6.9 s on a tiny prompt | 1.66 tok/s | 428 s |

Prompt ingestion was about 2.7 tok/s. A large system/tool prompt therefore dominates latency before decoding even starts.

## What the gateway now does

`POST /api/chat` avoids model work whenever the request is safely recognizable:

- service-status questions -> deterministic Go router -> tool -> deterministic formatter
- common JNF patient-volume, bed-occupancy, and supported disease-incidence forecasts -> deterministic Go router -> tool -> deterministic formatter
- ambiguous/general requests -> one compact LLM routing/answer call
- tool results are formatted by the gateway; there is no second LLM "finalize" call

The chat response reports `routing` (`deterministic` or `agent`) and `llm_calls` so demo latency can be audited directly.

## llama.cpp defaults in this profile

The Westmere image is built with `GGML_NATIVE=ON` and OpenBLAS. llama.cpp documents BLAS as potentially improving prompt processing for batches above 32; it does not improve token generation.

The starting profile intentionally separates decode and prefill thread counts:

```text
WESTMERE_AGENT_THREADS=6
WESTMERE_AGENT_BATCH_THREADS=12
WESTMERE_AGENT_BATCH_SIZE=2048
WESTMERE_AGENT_UBATCH_SIZE=512
WESTMERE_AGENT_PARALLEL=1
```

Do not treat those numbers as universal winners. Benchmark them on the actual host.

## Prompt caching safety

The gateway sends `cache_prompt: true` and keeps the router system prefix byte-stable. The server keeps prompt caching enabled.

The profile explicitly disables llama.cpp's shared RAM/idle-slot cache (`--cache-ram 0 --no-cache-idle-slots`). In August 2026 a llama.cpp issue reported cross-slot restoration of unrelated conversation state from that cache. For a healthcare prototype, avoiding cross-user cache contamination is more important than squeezing out an unsafe cache hit. With `--parallel 1`, the shared cross-slot cache is not useful anyway.

## Benchmark matrix

Run each configuration with the same model, quant, prompt, and output cap. Record prompt tok/s, TTFT, decode tok/s, total latency, RSS and CPU utilization.

1. Decode threads: `6`, `12`
2. Batch threads: `6`, `12`
3. Batch size: `256`, `512`, `1024`, `2048`
4. Microbatch: `64`, `128`, `256`, `512`
5. NUMA: one-socket CPU+memory binding versus `--numa distribute`

For one-socket tests, bind both CPU and memory to the same NUMA node. Do not assume Linux CPU numbering maps cleanly to socket numbers; inspect `lscpu -e=CPU,SOCKET,NODE,CORE` first.

## Practical target

The prototype should be architected so specialist features do not wait on the LLM. Forecasting, radiology, translation and service status have direct paths. The conversational model is an enhancement and should not be a synchronous dependency for every operation.
