# NDHIS AI performance journal

This document separates measured results from architectural changes and unverified experiments. Do not convert planned optimizations into performance claims until they are benchmarked on the dual-X5650 target.

## Hardware constraint

The physical host is dual Xeon X5650: 12 physical cores total, 24 logical threads with Hyper-Threading, SSE4.2, no AVX/AVX2/F16C. The actual NDHIS deployment is CT 110, an unprivileged LXC with 16 GiB RAM and a cpuset of eight physical cores split across both sockets. `RLIMIT_MEMLOCK` is 8 MiB inside the container, so locking the multi-gigabyte model is not available without changing the Proxmox/LXC policy.

The important transformer constraints are old CPU ISA, memory bandwidth, NUMA behavior and autoregressive decode rather than model-fit RAM capacity.

## Optimization timeline

| Stage | Change | Measured result | Why it mattered |
| --- | --- | --- | --- |
| Baseline | Qwen3-8B-class local agent | about 1.7 decode tok/s; full gateway chat about 5-8 min | Two large LLM passes plus slow Westmere decoding made interactive use impractical. |
| Model reduction | Gemma 4 E2B-class runtime | materially higher decode throughput | Smaller effective compute fit the hardware better. |
| Reasoning removal | thinking disabled | full gateway chat fell to roughly 30 s in the first measured configuration | Hidden reasoning was expensive autoregressive work. |
| Architecture | deterministic Go fast path and one-call ceiling | status/forecast operations became milliseconds to sub-second with zero LLM calls | The fastest token is the token not generated. |
| Tool finalization | removed second LLM formatter pass | supported tool routes use at most one model call | Avoided paying decode twice. |
| Prompt reduction | bounded history and compact router state | generation became the dominant remaining model latency | Reduced prompt ingestion and distraction. |
| OpenBLAS | OpenBLAS llama.cpp build with split prefill/decode threads | prompt/prefill improved materially; decode remained the main limit | BLAS accelerates matrix-heavy prefill, not sequential autoregressive generation. |
| Multiscale forecasting | 2020-2026 hourly synthetic history | forecasts about 230-335 ms in target runs | More realistic history remains comfortably sub-second. |
| Radiology | three-stage local OpenCV ONNX pipeline | about 0.85-0.9 s median in smoke runs | Replaced the broken placeholder without PyTorch runtime overhead. |
| Real streaming | actual Gemma token deltas through gateway/UI | first visible text arrives far before full completion | Perceived latency follows TTFT rather than full decode time. |
| Conversational routing | deterministic normalization + constrained router | colloquial deterministic routing about 80 microseconds before specialist execution | Preserved natural language without sending obvious operations through the LLM. |
| PR #8 warm state | coordinated startup warm-up before gateway listen | cold system-prefix prefill moved into readiness instead of the first doctor request | Removed the old warm-up/request race on a single llama.cpp slot. |

## Completed post-PR #8 target-host campaign

The full tuning campaign was run on the real CT 110 deployment at merged `main` SHA `87e1a54841c2a815967291d352afc7615c23f882`. The deployment was restored byte-for-byte to its original environment after testing because no alternative configuration produced a defensible improvement.

### Winning deployed configuration

```text
model: gemma-4-E2B_q4_0-it.gguf
ctx-size: 4096
parallel: 1
decode threads: 8
batch/prefill threads: 8
batch-size: 2048
ubatch-size: 512
numa: distribute
load-mode: mmap
cache-reuse: 0
priority: 0
poll: 50
sleep-idle-seconds: -1
spec-type: none
cache-prompt: enabled
cache-ram: 0
no-cache-idle-slots: enabled
warmup: enabled
```

The repository example may use different starting values for future deployments. These numbers describe the measured CT 110 winner, not a universal Westmere default.

### Startup and first request

Three restart runs measured a median gateway-ready time of about 15.6 s, p95 about 16.1 s. Roughly 14.7-15 s of that is the coordinated model warm-up. Logs confirmed the intended order:

```text
agent loads
-> gateway drives one bounded assistant-prefix inference
-> agent startup warm-up complete
-> gateway listening on :8080
```

Without that warm-up, the first request pays the full approximately 138-token assistant system-prefix prefill, roughly 11 s on this target. PR #8 therefore moves true cold-start work into readiness.

The first request after readiness measured about 1.66 s median TTFT and 1.81 s p95. Its completion in the startup probe was about 10.2 s median and 11.0 s p95.

### Realistic TTFT versus repeated-prompt cache hits

Repeatedly sending the exact same prompt measured about 583-601 ms TTFT because llama.cpp can reuse the user-message KV state as well as the system prefix. That is not representative of a doctor asking a new question.

With distinct realistic prompts, first text measured roughly 1.60 s median and 1.75 s p95. Direct probes across six different questions measured about 1.49-1.75 s each. The system prefix remains cached, but each new turn still introduces about 18-22 fresh prompt tokens. At about 12.5 prompt tokens/s plus first-token decode, this explains the remaining approximately 1.6 s TTFT.

This is per-turn prefill, not a cold-start regression, and it cannot be pre-warmed because the doctor's next text is unknown. Benchmark reports must therefore label repeated-prompt TTFT separately from varied-prompt TTFT.

### Assistant and router

The final five-run assistant benchmark measured approximately:

| Metric | Result |
| --- | ---: |
| realistic varied-prompt TTFT | about 1.60 s median / 1.75 s p95 |
| repeated-identical-prompt TTFT | about 0.60 s median |
| general assistant completion | about 24.4 s median / 25.3 s p95 |
| decode throughput | about 3.95 tok/s |
| prompt/prefill throughput | about 12.5 tok/s |
| fresh prompt tokens per turn | about 18-22 |

The constrained router benchmark measured about 7.9 s median / 20.6 s p95 for its repeated benchmark case. A separate varied ambiguous probe that consistently reached the clarification path was about 20.8 s median. Router tail latency remains decode/output-shape work rather than startup coldness.

### Runtime footprint

- agent `VmRSS`: about 1.66 GiB with mmap;
- gateway `VmRSS`: about 9 MiB;
- idle CPU: approximately zero across containers;
- under model load the agent saturates its allocated eight physical cores;
- host `kernel.numa_balancing=1`; llama.cpp warns that this may impair performance, but changing the host-wide sysctl was intentionally out of scope.

## Tuning results

| Axis | Tested | Result | Kept |
| --- | --- | --- | --- |
| context | 2048 vs 4096 | no measurable TTFT/decode/router difference; 2048 saved only about 12 MiB RSS | 4096 for context headroom |
| decode threads | 4 / 6 / 8 | 8 was best; 6 was slightly slower and 4 roughly 6% slower | 8 |
| batch threads | 6 / 8 / 12 | 8 was best; 6 reduced prefill, 12 oversubscribed the CT's eight cores | 8 |
| cache reuse | 0 / 32 / 64 / 128 | tiny gain only for repeated-identical prompts; realistic varied prompts were slightly worse | 0 |
| load mode | mmap / mmap+mlock | mlock failed because 2.3 GiB cannot be locked under the 8 MiB LXC limit; llama.cpp fell back to mmap | mmap |
| speculative mode | none / ngram-simple / ngram-cache | no useful decode gain; ngram-cache slightly worsened router latency | none |
| NUMA | distribute vs single-socket 4-core bind | single socket caused about +67% TTFT, -39% prefill, lower decode and slower router | distribute |
| flash attention | auto vs forced on | no measurable difference on this short-context Westmere deployment | auto/default |
| poll | 50 vs 0 | no meaningful latency difference | 50 |
| warm-up | PR #8 one-pass vs experimental two-pass | two-pass added about 11.6 s readiness time with no first-request TTFT gain | one-pass |

The NUMA result is specific to CT 110: binding to one socket halves the container's usable cores from eight to four, so any locality benefit is overwhelmed by lost compute.

## Correctness after the campaign

The restored winning configuration passed the gateway suite including startup warm-up tests. V2 microbenchmarks remained around 40.5 microseconds for spoken-duration normalization and 80.4 microseconds for the complete colloquial deterministic route.

Manual target checks also confirmed:

- `Gimme forecasts for A and E patients for the next minute and a half` -> deterministic, zero LLM calls, `forecast_patient_volume`, about 244 ms;
- ambiguous emergency forecast -> constrained agent router, exactly one LLM call, valid JNF schema, no raw 502;
- general local-AI explanation -> assistant path, exactly one LLM call, real streamed deltas;
- multi-turn forecast slot filling -> deterministic, zero LLM calls;
- forecasting ready, roughly 230-244 ms for the checked deterministic request;
- radiology ready, three-stage ONNX result about 733 ms in the final check;
- translation ready;
- thinking remains disabled and shared cross-session cache remains disabled.

## Measurement rules

- Report gateway readiness separately from first post-ready TTFT.
- For user-facing TTFT, prefer varied semantically equivalent prompts; repeated-identical prompts measure an optimistic cache-hit case.
- Report TTFT median and p95.
- Record full completion separately because decode dominates long answers.
- Do not interpret prompt-cache hits as uncached prompt-eval throughput.
- Keep deterministic specialist latency separate from model routing latency.
- Do not drop host-wide filesystem caches as part of automated benchmarking.
- Keep output correctness fixed while comparing model-serving settings.
- Do not re-enable shared cross-session RAM/idle-slot cache for a small latency gain.
- Treat radiology metrics as research smoke evaluation unless a larger independent validation protocol is established.

## Current conclusion

Keep the current CT 110 deployment configuration. The coordinated one-pass warm-up already removes the true multi-second cold-prefix penalty. The remaining approximately 1.6 s on a new conversational turn is dominated by unavoidable fresh prompt prefill on Westmere, while the roughly 24 s full answer time is dominated by approximately 4 tok/s autoregressive decode.

Further optimization should focus on model/runtime changes that materially improve decode or prompt throughput, not on repeatedly warming unknown future user text or exploiting repeated-prompt cache artifacts.
