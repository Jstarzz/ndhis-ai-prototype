# Westmere inference performance

The NDHIS target is a dual-X5650 host, but the actual deployment is CT 110: an unprivileged LXC with 16 GiB RAM and eight physical cores split across both sockets. Westmere exposes SSE4.x but not AVX/AVX2/F16C, so modern transformer inference is dominated by old-ISA prompt processing, memory behavior and autoregressive decode.

## Current deployed winner

The completed post-PR #8 target-host campaign found no configuration that beat the existing deployment safely enough to keep. CT 110 remains on:

```text
AGENT_MAX_MODEL_LEN=4096
WESTMERE_AGENT_PARALLEL=1
WESTMERE_AGENT_THREADS=8
WESTMERE_AGENT_BATCH_THREADS=8
WESTMERE_AGENT_BATCH_SIZE=2048
WESTMERE_AGENT_UBATCH_SIZE=512
WESTMERE_AGENT_NUMA=distribute
WESTMERE_AGENT_LOAD_MODE=mmap
WESTMERE_AGENT_CACHE_REUSE=0
WESTMERE_AGENT_PRIO=0
WESTMERE_AGENT_POLL=50
WESTMERE_AGENT_SLEEP_IDLE_SECONDS=-1
WESTMERE_AGENT_SPEC_TYPE=none
```

llama.cpp also keeps `--warmup --cache-prompt --cache-ram 0 --no-cache-idle-slots --perf --jinja`.

These are the measured CT 110 settings, not universal defaults for every Westmere deployment. The repository example configuration may intentionally differ.

## Current measured performance

| Path | Target-host result |
| --- | ---: |
| gateway ready after agent+gateway restart | about 15.6 s median / 16.1 s p95 |
| first post-ready assistant TTFT | about 1.66 s median / 1.81 s p95 |
| varied real-world assistant TTFT | about 1.60 s median / 1.75 s p95 |
| repeated-identical-prompt TTFT | about 0.58-0.60 s |
| general assistant completion | about 24.4 s median / 25.3 s p95 |
| decode throughput | about 3.95 tok/s |
| prompt/prefill throughput | about 12.5 tok/s |
| constrained router benchmark | about 7.9 s median / 20.6 s p95 |
| deterministic forecasts | about 230-244 ms |
| deterministic language routing | about 80 microseconds before specialist execution |

The repeated-prompt TTFT is an optimistic cache-hit measurement. When the same complete request is sent repeatedly, llama.cpp can reuse the user-message KV state. A real new user turn contains about 18-22 fresh prompt tokens; at roughly 12.5 prompt tok/s plus first-token decode, that naturally produces approximately 1.5-1.8 s first-text latency.

Therefore the realistic conversational TTFT floor on this deployment is currently around 1.6 s, not 0.6 s. The latter is useful only as a repeated-prompt cache-hit diagnostic.

## Coordinated startup warm state

PR #8 moved warm-up before the gateway begins accepting traffic. The measured startup order is:

```text
agent loads
-> gateway waits for the model endpoint
-> one bounded assistant-prefix warm inference
-> warm-up completes
-> gateway begins listening
```

The warm pass takes roughly 14.7-15 s on this hardware. Without it, the first doctor request would pay approximately 11 s to ingest the roughly 138-token assistant system prefix. The longer service-readiness period is intentional because it converts an unpredictable first-user stall into an explicit startup cost.

An experimental second warm pass increased gateway readiness from roughly 15.6 s to 27.2 s and did not improve first-post-ready TTFT, so the one-pass design remains correct.

## Context and KV cache

2048 and 4096 context were compared on the real deployment. No material TTFT, router or decode difference was observed. 2048 reduced RSS by only about 12 MiB.

CT 110 therefore keeps 4096 for additional conversational headroom. A smaller context is not a meaningful performance optimization for the current Gemma E2B workload because active prompts do not approach the KV limit.

## Threading

The LXC has eight physical cores available, four from each socket. Testing showed:

- decode threads 8 > 6 > 4;
- batch/prefill threads 8 > 6;
- 12 batch threads oversubscribe the eight-core cpuset and reduce performance.

The winning setting is 8 decode threads and 8 batch threads.

Do not infer the correct thread count from the physical host's 12 cores. Benchmark against the container's actual cpuset.

## NUMA

`--numa distribute` is materially better for CT 110 than a one-socket bind. The one-socket experiment left only four usable cores and measured approximately:

- TTFT +67%;
- prefill throughput -39%;
- decode about 6% slower;
- router about 19% slower;
- startup readiness about 13% slower.

This result is deployment-specific. It does not prove that distribute always beats locality; it proves that halving CT 110's available compute costs more than any one-socket locality benefit.

The host currently has `kernel.numa_balancing=1`. llama.cpp warns that this may impair performance, but changing that host-wide sysctl was intentionally outside this campaign's scope.

## Model residency

`WESTMERE_AGENT_SLEEP_IDLE_SECONDS=-1` keeps llama.cpp from intentionally unloading the model.

`mmap+mlock` was tested but cannot function in the current unprivileged LXC: the model needs roughly 2.3 GiB locked while `RLIMIT_MEMLOCK` is only 8 MiB. llama.cpp fails the lock and falls back to mmap. No Proxmox security/resource policy was changed just to force this experiment.

With mmap, the agent uses roughly 1.66 GiB `VmRSS` in the observed steady state and zero locked memory.

## Prompt caching and cache reuse

`--cache-prompt` remains enabled. Shared RAM/idle-slot cache remains disabled with:

```text
--cache-ram 0
--no-cache-idle-slots
```

That isolation policy stays in place.

`WESTMERE_AGENT_CACHE_REUSE` values 0, 32, 64 and 128 were tested. Nonzero reuse showed only a roughly 2% benefit when the identical benchmark prompt was repeated, while realistic varied prompts were slightly worse. Keep reuse at zero.

## Speculative decoding

`none`, `ngram-simple` and `ngram-cache` were tested. The workload's short prompts and novel generated prose produced negligible useful n-gram matches. Neither speculative mode improved decode, and `ngram-cache` slightly worsened router latency. Keep `none`.

## Other tested knobs

- Flash attention forced on versus auto: no measurable benefit for the current short-context workload on Westmere.
- Poll 50 versus poll 0: no meaningful latency difference; keep 50.
- Higher warm-up complexity: rejected because startup got slower with no user-facing gain.

## Serving architecture remains the main optimization

The largest speedups did not come from llama.cpp flags. They came from avoiding LLM inference where it is unnecessary:

```text
obvious operation -> deterministic Go -> specialist
ambiguous operation -> constrained Gemma router -> specialist
general conversation -> streamed Gemma assistant
```

Tool output is formatted by the gateway. There is no second LLM finalization pass.

## Measurement rules

- Measure the container's actual CPU/memory limits, not the physical host's headline specifications.
- Separate gateway-ready time from first post-ready TTFT.
- Use varied semantically equivalent prompts for realistic TTFT; label repeated-identical-prompt measurements as cache-hit diagnostics.
- Report median and p95.
- Record total completion separately from TTFT because decode dominates long responses.
- Do not interpret prompt-cache hits as uncached prompt-eval throughput.
- Do not drop the host filesystem page cache as part of an automated benchmark.
- Keep deterministic tool latency separate from model-routing latency.
- Keep output correctness fixed while comparing performance settings.
- Do not re-enable shared cross-session cache for small latency gains.

## Practical conclusion

The merged PR #8 architecture plus the existing CT 110 deployment configuration is the measured winner. The remaining roughly 1.6 s TTFT for a genuinely new question is normal fresh-token prefill on Westmere, while roughly 24 s total response time is dominated by about 4 tok/s autoregressive decode.

Future work should target a materially better model/runtime or newer hardware rather than repeatedly tuning knobs that the target-host sweep has already shown to be neutral or harmful.
