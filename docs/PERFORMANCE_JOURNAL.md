# NDHIS AI performance journal

This document separates measured results from architectural changes and unverified experiments. Do not convert planned optimizations into performance claims until they are benchmarked on the dual-X5650 target.

## Hardware constraint

The Westmere target is dual Xeon X5650: 12 physical cores total, 24 logical threads with Hyper-Threading, SSE4.2, no AVX/AVX2/F16C. The machine has enough RAM for the current prototype; instruction set, memory bandwidth, NUMA behavior and autoregressive token generation are the important constraints.

## Optimization timeline

| Stage | Change | Measured result | Why it mattered |
| --- | --- | --- | --- |
| Baseline | Qwen3-8B-class local agent | about 1.7 decode tok/s; full gateway chat about 5-8 min | Two large LLM passes plus slow Westmere decoding made interactive use impractical. |
| Model reduction | Gemma 4 E2B | about 3.7 tok/s with thinking enabled | Smaller effective compute improved raw decode but hidden reasoning still made requests take minutes. |
| Reasoning removal | Gemma 4 E2B with thinking disabled | full gateway chat fell to about 30 s in the first measured configuration | Removing hidden reasoning cut generated-token work dramatically. |
| Architecture | deterministic Go fast path and one-call ceiling | service status about 5-10 ms; common forecasts became sub-second; zero LLM calls for obvious operations | The fastest token is the token not generated. |
| Tool finalization | removed second LLM formatter pass | supported tool routes use at most one model call and deterministic Go formatting | Avoided paying autoregressive decode twice. |
| Prompt reduction | bounded history and compact router prompt | prompt cost fell enough that model generation became the dominant remaining latency | Westmere prompt ingestion was previously a major bottleneck. |
| OpenBLAS | llama.cpp OpenBLAS build with split prefill/decode threads | TTFT roughly 1.7 s to 0.3-0.6 s; large-prompt prefill roughly 15 to 19 tok/s; decode remained about 3.7-4.3 tok/s | BLAS improved prompt processing as expected without changing autoregressive token generation. |
| Multiscale forecasting | 2020-2026 hourly synthetic history, about 306k rows | forecast tools moved from about 10 ms to roughly 270-335 ms | More realistic history costs more CPU but remains comfortably sub-second. |
| Radiology | three-stage local OpenCV ONNX pipeline | roughly 820-870 ms end-to-end on the target | Replaced a broken placeholder with a usable local screening runtime without PyTorch overhead. |

## Current PR: conversational quality without giving back speed

### Natural-language normalization

Common colloquial expressions are normalized before the deterministic router sees them. Examples include `A and E` to `A&E`, `minute and a half` to `1.5 minutes`, `half an hour` to `0.5 hours`, and spoken one-through-ten durations/resolutions.

Expected effect: requests that are semantically obvious stay on the zero-LLM path instead of failing into clarification loops. The exact route is covered by deterministic tests and microbenchmarks.

### Separate router and assistant jobs

The previous fallback asked one prompt to be both chatbot and tool router. The new design separates them:

```text
obvious operation -> deterministic Go -> specialist
ambiguous operation -> constrained Gemma router -> specialist
real conversation -> streamed Gemma assistant
```

The operational router defaults to 40 generated tokens. The conversational assistant defaults to 96. `AGENT_MAX_TOKENS` remains the hard upper bound.

Expected effect: operational LLM calls spend fewer seconds producing JSON, while general answers regain natural language instead of being forced through a routing schema.

### Schema-constrained routing

The pinned llama.cpp revision supports schema-constrained JSON responses. The router schema limits tool names and domain fields, while Go still performs authoritative validation before execution.

Expected effect: fewer malformed JSON responses, fewer hallucinated tool names and fewer raw routing failures. This must still be tested on the exact Gemma artifact and chat template used on the target host.

### Structured state instead of replaying chat

The ambiguous operational router receives a bounded state summary plus the latest user request rather than four full turns. Forecast state includes only known metric, department, disease, horizon, resolution and as-of values.

Expected effect: smaller uncached prompt suffix, more stable prompt prefix reuse and less opportunity for a tiny model to become distracted by old prose.

### Real token streaming

General assistant responses now stream actual model deltas through the gateway NDJSON stream and the React UI renders them immediately.

Expected effect: raw completion time does not magically shrink, but perceived latency becomes close to model TTFT instead of full-response latency. With the measured warm TTFT around 0.3-0.6 s, users should start reading substantially earlier than before. Target-host measurement is required after deployment.

### Background warm-up

The gateway warms both the constrained router prefix and the conversational assistant prefix in the background after startup.

Expected effect: move the observed roughly 38 s first-agent-call penalty away from the first interactive user request. Warm calls previously settled around roughly 24 s for longer agent responses, with large variance driven by generated-token count.

### Direct radiology result lookup

A known 16-character radiology result identifier now routes directly to `get_radiology_result` instead of spending roughly 14 s on an LLM route before a tool call that itself took only a few milliseconds.

Expected effect: the chat retrieval path should become essentially the local gateway/tool latency rather than model latency.

## Next target-host performance sweep

The pinned llama.cpp build exposes speculative n-gram modes. The compose profile now exposes `WESTMERE_AGENT_SPEC_TYPE` but keeps `none` as the default until measurement.

Benchmark this order:

1. `none`
2. `ngram-simple`
3. `ngram-cache`

For each mode record warm TTFT, decode tok/s, total time, output correctness and CPU utilization for both the general assistant and constrained router. Keep the mode only if it improves useful end-to-end latency rather than a synthetic token metric.

Then sweep:

- decode threads: 6 vs 12
- prompt threads: 6 vs 12
- context: 2048 vs 4096 after verifying conversation-state requirements
- available Gemma quant variants on the exact X5650 host
- one-socket NUMA placement vs distribute

Do not enable a speculative mode, smaller context or different quant globally just because it is theoretically faster. This machine is old enough that cache behavior and NUMA placement can reverse expectations from modern AVX2 systems.

## Measurement rules

- Report cold and warm model results separately.
- Record output token count when comparing LLM completion latency.
- Do not interpret prompt-cache hits as raw uncached prompt-eval throughput.
- Keep deterministic tool latency separate from model routing latency.
- Keep model inference latency separate from browser/network wall time.
- Treat radiology benchmark accuracy as a research smoke test unless an independent, properly curated validation set and clinical evaluation protocol are established.
