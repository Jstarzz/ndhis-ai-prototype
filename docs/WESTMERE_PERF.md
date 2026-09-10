# Westmere inference performance

The dual-X5650 deployment is constrained by CPU ISA, memory bandwidth and NUMA topology rather than RAM capacity. The X5650 exposes SSE4.x but not AVX/AVX2/F16C, so modern transformer decode remains expensive even when a quantized model fits comfortably in memory.

## Measured evolution

The original 8B-class agent produced about 1.7-1.9 decode tok/s and multi-minute tool-backed chat. After moving to the smaller Gemma E2B-class runtime, disabling thinking, removing duplicate model passes, adding deterministic specialist routing and rebuilding llama.cpp with OpenBLAS, the target now measures roughly:

| Path | Current target-host result |
| --- | ---: |
| raw warm model TTFT | about 0.3-0.6 s |
| streamed application TTFT | about 0.63 s median in the latest 3-run assistant benchmark |
| general assistant completion | about 28 s median, 53.3 s p95 |
| constrained operational router | about 9 s median, 22.2 s p95 |
| deterministic forecasts | about 230-241 ms |
| deterministic language routing | tens of microseconds before specialist execution |

The application TTFT is already close to the raw model TTFT. PR #8 therefore targets consistency, startup cold behavior and tail latency rather than claiming a large reduction below the model's own first-token floor.

## Serving architecture

Supported specialist operations avoid model work whenever they can be resolved safely:

```text
obvious operation -> deterministic Go -> specialist
ambiguous operation -> constrained Gemma router -> specialist
general conversation -> streamed Gemma assistant
```

Tool output is formatted by the gateway. There is no second LLM finalization pass.

## Coordinated startup warm state

The earlier gateway started listening immediately and warmed the model in a background goroutine. Because the Westmere agent runs with `--parallel 1`, a real user request could race that warm-up and wait behind it.

The gateway now waits for the llama.cpp `/v1/models` endpoint, performs one bounded assistant-prefix inference, and only then starts accepting HTTP traffic. This deliberately moves cold model-page, allocator and prefix initialization into service readiness instead of the first doctor interaction.

The startup warm-up is bounded by `AGENT_STARTUP_WARMUP_TIMEOUT_SECONDS`. If the model never becomes ready, the gateway eventually starts rather than hanging indefinitely; later requests still receive the normal upstream failure behavior.

The warm request uses the exact general-assistant system prefix, `cache_prompt: true`, thinking disabled and only two output tokens. With a single llama.cpp slot this is preferable to warming two long prefixes sequentially, because the second warm would replace most of the first slot's cached prefix anyway.

## Model residency and idle cold behavior

Two different cold conditions should be measured separately:

- process cold: the llama.cpp process/container was restarted;
- idle cold: the process stayed alive but mmap-backed model pages were reclaimed under host memory pressure.

The default remains `WESTMERE_AGENT_LOAD_MODE=mmap`. The pinned llama.cpp build also supports `mmap+mlock`, which is worth benchmarking if the LXC/Docker memlock policy permits it. Locking pages can reduce page-reclamation surprises, but it reserves resident memory and may require container/host limits to change. Do not enable it globally without measuring startup time, RSS, TTFT and impact on the other local services.

`WESTMERE_AGENT_SLEEP_IDLE_SECONDS=-1` is explicit so the model server never intentionally unloads its model during demo idle time.

## Context and KV-cache pressure

The Westmere default context is now 2048 instead of 4096. NDHIS sends a short system prefix plus at most four bounded conversational turns, while operational routing uses compact structured state. The smaller context reduces KV allocation and cache pressure without affecting the current prompt contracts.

Keep 4096 as a comparison point. If future workflows require materially longer clinical context, restore capacity based on actual token accounting instead of silently truncating useful input.

## llama.cpp tuning surface

The current starting profile is:

```text
WESTMERE_AGENT_THREADS=6
WESTMERE_AGENT_BATCH_THREADS=12
WESTMERE_AGENT_BATCH_SIZE=2048
WESTMERE_AGENT_UBATCH_SIZE=512
WESTMERE_AGENT_PARALLEL=1
WESTMERE_AGENT_NUMA=distribute
WESTMERE_AGENT_LOAD_MODE=mmap
WESTMERE_AGENT_CACHE_REUSE=0
WESTMERE_AGENT_PRIO=0
WESTMERE_AGENT_POLL=50
WESTMERE_AGENT_SLEEP_IDLE_SECONDS=-1
WESTMERE_AGENT_SPEC_TYPE=none
```

OpenBLAS improves prompt/prefill matrix work but does not materially improve sequential token generation. That matches the measured change: TTFT/prefill improved substantially while decode remained around the same range.

`--cache-prompt` stays enabled. Shared RAM/idle-slot cache remains disabled with `--cache-ram 0 --no-cache-idle-slots` because cross-session cache isolation matters more than speculative latency savings in this healthcare-oriented prototype.

`WESTMERE_AGENT_CACHE_REUSE` is exposed but defaults to zero. Benchmark small values such as 32, 64 and 128 only with repeated prompt shapes and verify output correctness. It is not a substitute for the existing stable-prefix prompt cache.

Priority and polling are also exposed. Higher polling can reduce scheduler wake latency at the cost of burning more CPU while waiting; on a shared old host that tradeoff can make other services worse. Keep priority 0 / poll 50 as the baseline and measure changes under mixed load.

## Target-host benchmark order

Change one variable at a time and run both `scripts/benchmark_ndhis.py` and `scripts/benchmark_westmere_startup.py`.

1. Baseline: current defaults.
2. Context: 2048 vs 4096.
3. Load mode: `mmap` vs `mmap+mlock` if memlock is supported.
4. Cache reuse: 0, 32, 64, 128.
5. Decode threads: 6 vs 12.
6. Batch threads: 6 vs 12.
7. NUMA: distribute vs one-socket CPU+memory binding.
8. Speculative mode: none, ngram-simple, ngram-cache.
9. Priority/poll only after the above are stable.
10. Quant variants on the exact Gemma artifact if compatible builds are available.

For one-socket tests, bind CPU and memory to the same NUMA node. Inspect `lscpu -e=CPU,SOCKET,NODE,CORE` first; do not assume logical CPU numbering.

A future two-slot experiment can test whether keeping separate router and assistant prefixes resident is worth the extra KV/cache and concurrent bandwidth pressure. Do not enable `parallel=2` by default merely to preserve two prefixes; dual Westmere memory bandwidth can make concurrent decode slower than a single well-tuned slot.

## Measurement rules

- Report process-cold readiness separately from first post-ready TTFT.
- Report median and p95 TTFT, not only median.
- Record total completion separately from TTFT because decode dominates long responses.
- Do not interpret prompt-cache hits as raw uncached prompt-eval throughput.
- Do not drop the host filesystem page cache as part of an automated benchmark.
- Keep deterministic tool latency separate from model-routing latency.
- Benchmark mixed workload impact before raising process priority or polling aggressively.
- Keep output correctness fixed while comparing performance settings.
