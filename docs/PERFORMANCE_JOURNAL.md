# NDHIS AI performance journal

This document separates measured results from architectural changes and unverified experiments. Do not convert planned optimizations into performance claims until they are benchmarked on the dual-X5650 target.

## Hardware constraint

The Westmere target is dual Xeon X5650: 12 physical cores total, 24 logical threads with Hyper-Threading, SSE4.2, no AVX/AVX2/F16C. RAM capacity is adequate; instruction set, memory bandwidth, NUMA behavior and autoregressive token generation are the important constraints.

## Optimization timeline

| Stage | Change | Measured result | Why it mattered |
| --- | --- | --- | --- |
| Baseline | Qwen3-8B-class local agent | about 1.7 decode tok/s; full gateway chat about 5-8 min | Two large LLM passes plus slow Westmere decoding made interactive use impractical. |
| Model reduction | Gemma 4 E2B-class runtime | materially higher decode throughput | Smaller effective compute fit the hardware better. |
| Reasoning removal | thinking disabled | full gateway chat fell to roughly 30 s in the first measured configuration | Hidden reasoning was expensive autoregressive work. |
| Architecture | deterministic Go fast path and one-call ceiling | status/forecast operations became milliseconds to sub-second with zero LLM calls | The fastest token is the token not generated. |
| Tool finalization | removed second LLM formatter pass | supported tool routes use at most one model call | Avoided paying decode twice. |
| Prompt reduction | bounded history and compact router state | generation became the dominant remaining model latency | Reduced prompt ingestion and distraction. |
| OpenBLAS | OpenBLAS llama.cpp build with split prefill/decode threads | TTFT roughly 1.7 s to 0.3-0.6 s; large-prompt prefill roughly 15 to 19 tok/s; decode roughly unchanged | BLAS accelerated matrix-heavy prefill as expected. |
| Multiscale forecasting | 2020-2026 hourly synthetic history | forecasts roughly 230-335 ms in recent target runs | More realistic history remains comfortably sub-second. |
| Radiology | three-stage local OpenCV ONNX pipeline | roughly 0.85-0.9 s median in recent smoke runs | Replaced the broken placeholder without PyTorch runtime overhead. |
| Real streaming | actual Gemma token deltas through gateway/UI | latest assistant benchmark about 634 ms first text vs about 28 s median completion | Perceived latency follows TTFT rather than full decode time. |
| Conversational routing | deterministic normalization + constrained router | colloquial deterministic route about 76 microseconds before specialist; ambiguous router about 9 s median | Preserved natural language without sending obvious operations through the LLM. |

## Latest target-host baseline before PR #8

The latest three-run assistant benchmark recorded deterministic status/capability requests below roughly 15 ms, deterministic forecasts around 230-241 ms, a streamed general assistant median completion around 28.0 s with p95 around 53.3 s, and first visible assistant text around 634 ms. The constrained operational router measured about 9.0 s median and 22.2 s p95.

Those results show that warm TTFT is already close to the raw model's measured 0.3-0.6 s range. The largest remaining conversational cost after first text is autoregressive decode. PR #8 therefore focuses on startup cold state, TTFT consistency and tail behavior rather than claiming a large reduction below the model's own first-token floor.

The latest radiology smoke evaluation completed 20/20 cases with about 850 ms median inference, 1.00 screening ROC AUC on the tiny sample and 11/11 subtype accuracy when the configured screening gate triggered. Prompt-invariance repeated all requested variants with zero score drift. These remain research smoke metrics, not clinical validation.

## PR #8: warm-state and TTFT engineering

### Coordinated startup warm-up

The previous warm-up ran in a goroutine after the gateway was initialized. With llama.cpp configured as `parallel=1`, a real request could arrive while warm-up inference was occupying the only model slot and wait behind it.

PR #8 changes startup semantics: the gateway polls the model endpoint, runs a bounded two-token assistant-prefix warm inference and only then begins listening for user traffic. Startup warm-up has a hard timeout so a failed model cannot block the gateway indefinitely.

This converts cold-start latency from an unpredictable first-user penalty into an explicit service-readiness cost. The new startup benchmark measures both values separately.

### Warm the prefix that matters

The old warm-up sequentially exercised the operational router and then the conversational assistant. With one llama.cpp slot, the second request replaces most of the first cached prompt state. PR #8 warms the general-assistant prefix once instead. That still touches the model weights and execution path for all inference while leaving the most user-visible conversational prefix hot.

The constrained router remains compact enough that its prompt evaluation is a smaller part of its total latency than token generation.

### Smaller context window

The Westmere default context moves from 4096 to 2048. Current NDHIS assistant history is capped to four bounded messages and the operational router receives compact structured state. Reducing context lowers KV allocation and cache pressure without changing the current contract.

This is an expected optimization until the target-host comparison is run. If 2048 changes output quality or truncates a valid workflow, 4096 remains the fallback.

### Idle residency controls

The agent explicitly keeps `sleep-idle-seconds=-1`, so llama.cpp will not intentionally unload the model during demo idle periods.

`WESTMERE_AGENT_LOAD_MODE` defaults to `mmap`. `mmap+mlock` is exposed as an experiment for preventing model pages from being reclaimed after idle periods. It is not enabled by default because it consumes locked resident memory and may require LXC/Docker memlock policy changes.

### Cache reuse, scheduler and speculative knobs

PR #8 exposes cache-reuse, process-priority and polling controls without changing them aggressively by default:

```text
WESTMERE_AGENT_CACHE_REUSE=0
WESTMERE_AGENT_PRIO=0
WESTMERE_AGENT_POLL=50
WESTMERE_AGENT_SPEC_TYPE=none
```

Prompt caching remains enabled, while shared RAM/idle-slot cache stays disabled for cross-session isolation. Cache reuse values, higher polling, n-gram speculation and process priority are experiments only until target-host numbers show a useful end-to-end improvement.

### Better tail-latency measurement

The main assistant harness now reports median and p95 for first execution stage, first streamed text and server/wall completion latency. A new `benchmark_westmere_startup.py` restarts only agent+gateway, waits for coordinated readiness, measures the first post-ready request and then repeated steady-state requests.

The script deliberately does not drop Linux filesystem caches. Its cold result means process/container cold, not physical-disk cold.

## Next target-host sweep

Run the baseline first, then change one variable at a time:

1. 2048 vs 4096 context.
2. `mmap` vs `mmap+mlock` if memlock is supported.
3. cache reuse 0, 32, 64, 128.
4. decode threads 6 vs 12.
5. batch threads 6 vs 12.
6. NUMA distribute vs one-socket CPU+memory binding.
7. spec type none, ngram-simple, ngram-cache.
8. priority/poll only after the above are stable.
9. compatible Gemma quant variants on the exact X5650 host.

A two-slot experiment may later test whether retaining separate router and assistant prefixes helps enough to justify the extra KV/cache footprint and memory-bandwidth contention. Keep `parallel=1` as the baseline until measured otherwise.

## Measurement rules

- Report process-cold gateway readiness separately from first post-ready TTFT.
- Report TTFT median and p95, not only median.
- Record full completion separately because decode dominates long answers.
- Do not interpret prompt-cache hits as raw uncached prompt-eval throughput.
- Keep deterministic specialist latency separate from model routing latency.
- Do not automate host-wide filesystem cache dropping.
- Keep output correctness fixed while comparing model-serving settings.
- Do not re-enable shared cross-session cache for a small latency gain.
- Treat radiology metrics as research smoke evaluation unless a larger independent validation protocol is established.
