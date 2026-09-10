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

## Post-merge target-host verification

After the assistant-streaming/evaluation work was merged and deployed to the dual-X5650 target, the deterministic Go suite passed all 33 tests. Go microbenchmarks measured spoken-duration normalization at roughly 40 microseconds per operation and the complete deterministic colloquial forecast route at roughly 82 microseconds per operation.

The end-to-end assistant benchmark confirmed that deterministic forecast/status/capability cases remained on the zero-LLM path. The streamed general assistant path produced first visible text at roughly 0.6 seconds while the complete response took roughly 28 seconds in a cold-ish post-restart run. This confirms that streaming moves perceived latency close to TTFT even though autoregressive decode remains the dominant total-time cost.

The 20-image radiology smoke evaluation reported 0.80 screening accuracy, 1.00 normal specificity, 0.73 abnormal sensitivity at the configured 0.91 unhealthy threshold, zero primary-run failures and about 884 ms median model latency. These are research smoke-test results only. The sample is small and potential training-data overlap has not been ruled out.

The initial prompt-invariance run showed zero unhealthy-score drift on successful repeated cases, but most repeated requests encountered gateway HTTP 429 responses because the evaluator generated traffic faster than the normal per-user limit. That run is not a complete invariance benchmark. The follow-up evaluator now rate-paces prompt variants and reports completeness explicitly before the drift metric should be interpreted.

The first public-sample fetch also exposed a packaging assumption: the pinned dataset revision now presents the image corpus inside `covid19_radiography.zip` rather than as individually listed image objects. The follow-up fetcher supports both repository layouts and hashes extracted samples for reproducibility.

## Conversational quality without giving back speed

### Natural-language normalization

Common colloquial expressions are normalized before the deterministic router sees them. Examples include `A and E` to `A&E`, `minute and a half` to `1.5 minutes`, `half an hour` to `0.5 hours`, and spoken one-through-ten durations/resolutions.

The target-host microbenchmarks show that this normalization layer is negligible compared with specialist inference or LLM generation, so natural-language convenience does not materially compromise the fast path.

### Separate router and assistant jobs

The previous fallback asked one prompt to be both chatbot and tool router. The current design separates them:

```text
obvious operation -> deterministic Go -> specialist
ambiguous operation -> constrained Gemma router -> specialist
real conversation -> streamed Gemma assistant
```

The operational router defaults to 40 generated tokens. The conversational assistant defaults to 96. `AGENT_MAX_TOKENS` remains the hard upper bound.

### Schema-constrained routing

The pinned llama.cpp revision supports schema-constrained JSON responses. The router schema limits tool names and domain fields, while Go still performs authoritative validation before execution.

This narrows the model's output space and reduces malformed or hallucinated tool arguments without trusting model output as authoritative application state.

### Structured state instead of replaying chat

The ambiguous operational router receives a bounded state summary plus the latest user request rather than four full turns. Forecast state includes only known metric, department, disease, horizon, resolution and as-of values.

This reduces uncached prompt suffix size, improves stable-prefix reuse and removes irrelevant prose from the tiny routing model's context.

### Real token streaming

General assistant responses stream actual model deltas through the gateway NDJSON stream and the React UI renders them immediately.

Target-host measurement now confirms the intended effect: approximately 0.6-second first streamed text versus roughly 28 seconds for the complete cold-ish response. Streaming does not improve decode throughput, but it materially reduces perceived latency.

### Background warm-up

The gateway warms both the constrained router prefix and the conversational assistant prefix in the background after startup.

The intent is to move cold model-page, allocator and prefix setup work away from the first real doctor request. Cold and warm measurements must still be reported separately because the first post-restart run can remain materially slower.

### Direct radiology result lookup

A known 16-character radiology result identifier routes directly to `get_radiology_result` instead of spending model time deciding an obvious tool call.

This is another example of selective inference: deterministic identifiers stay deterministic, while the LLM is reserved for language ambiguity.

## Radiology threshold analysis

The configured unhealthy threshold of 0.91 produced perfect normal specificity but only about 0.73 abnormal sensitivity on the first 20-image smoke sample. That does not justify changing the deployed threshold by itself. The follow-up evaluation harness derives a fixed threshold sweep from the same recorded model scores and reports accuracy, sensitivity, specificity and balanced accuracy without issuing additional model requests.

Any apparent best threshold from this sweep is exploratory. Threshold selection requires a larger independent validation set, explicit clinical operating objectives and preferably confidence intervals. The smoke sample is useful for understanding the direction of the tradeoff, not for claiming calibration or choosing a production clinical cutoff.

## Next target-host performance sweep

The pinned llama.cpp build exposes speculative n-gram modes. The compose profile exposes `WESTMERE_AGENT_SPEC_TYPE` but keeps `none` as the default until measurement.

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
- Do not tune the radiology screening threshold from the same 20-image smoke set used to characterize it.
- Treat prompt-invariance results as valid only when all requested prompt variants completed without rate-limit or transport failures.
