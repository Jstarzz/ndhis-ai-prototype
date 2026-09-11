# NDHIS AI Deployed System, Architecture, and Capacity

## Purpose

This document records the actual on-premises NDHIS AI prototype deployment and the performance characteristics measured on the current legacy CPU hardware. It is deliberately separate from generic model/profile documentation so design defaults are not confused with the model/configuration that was actually tested.

The prototype is a local clinical-operations demonstration. It is not a clinically validated diagnostic system and is not a national-scale capacity claim.

## Deployed infrastructure

Current deployment:

```text
Proxmox host:     pve
Guest:            CT 110 (unprivileged LXC)
Guest RAM:        16 GiB
CPU allocation:   8 physical cores
Host CPU family:  dual Intel Xeon X5650 (Westmere)
Deployment SHA:   87e1a54841c2a815967291d352afc7615c23f882
Repository path:  /root/ndhis-ai-prototype
Compose profile:  compose.cpu.yaml + compose.westmere.yaml
```

The eight assigned cores span the dual-socket host. The Westmere profile is NUMA-aware and is intentionally tuned for the older pre-AVX2 CPU generation.

The current model-level agent runtime uses one active generation slot:

```text
--parallel 1
--threads 8
--threads-batch 8
--ctx-size 4096
```

The gateway currently admits up to:

```text
MAX_CONCURRENT_REQUESTS=2
REQUESTS_PER_MINUTE=30
```

These are deliberate guardrails, not statements that every specialist service has the same cost.

## Deployed model/runtime set

The measured deployed agent is:

```text
model file: gemma-4-E2B_q4_0-it.gguf
served alias: ndhis-agent-gemma4-e2b
runtime: llama.cpp / llama-server
context: 4096 tokens
```

The repository also supports other configurable Westmere/CPU model choices. Documentation that describes a generic/default Qwen profile should not be interpreted as the exact model used for the measurements in this file.

Specialist services remain local:

```text
ASR / live translation
forecasting
radiology screening
agent/tool router
Go gateway
React workstation UI
```

Runtime model downloads are disabled; model artifacts must already be present locally.

## Deployed logical architecture

```text
                   Browser / NDHIS client
                           |
                           v
                    Go AI Gateway
             auth/rate/concurrency/audit
                 /          |          \
                /           |           \
               v            v            v
       Local agent      Forecasting     Radiology
       llama.cpp        specialist      specialist
           |                |               |
           |                |               |
           +------ bounded tool results ----+
                           |
                           v
                     grounded answer

Browser microphone
       |
       v
Translation WebSocket
       |
       +--> local ASR
       `--> local translation engine
```

The agent does not perform forecasting or radiology inference itself. It selects or invokes a bounded specialist tool and receives structured results.

Many common operational queries bypass LLM routing completely through deterministic routing. This is important on the Westmere host: deterministic tool calls are far cheaper and faster than invoking the local generative model.

## Identity and request boundary

The current prototype gateway requires:

```text
X-NDHIS-Demo-Key
X-NDHIS-User
X-NDHIS-Role: doctor
```

It also applies:

- request-body limits;
- per-user request-rate limits;
- a global concurrency semaphore;
- bounded chat history/message length;
- explicit supported-tool validation;
- request IDs; and
- audit metadata.

This is a prototype identity layer. `X-NDHIS-User` and `X-NDHIS-Role` are client-supplied assertions and must be replaced by authoritative authenticated identity before real clinical deployment. See `SECURITY_AUDIT.md`.

## Local-data boundary

The prototype uses:

- synthetic hospital operations data for forecasting;
- public/de-identified images for radiology testing; and
- local model weights/inference.

The architecture intentionally does not give a model unrestricted access to production NDHIS databases.

A future production integration should expose narrowly scoped, authorized tools/data views through the gateway rather than providing raw database credentials or broad record access to the model runtime.

## Measured startup behavior

Three restart/startup measurements on the current Westmere deployment produced approximately:

| Metric | Result |
|---|---:|
| container/restart operation | ~0.75 s median |
| gateway ready | ~15.6 s median |
| gateway ready p95 | ~16.1 s |
| startup warm-up | ~14.7-15.0 s |
| first post-ready TTFT | ~1.66 s |
| first post-ready TTFT p95 | ~1.81 s |
| first full completion | ~10.2 s |
| first full completion p95 | ~11.0 s |

The service should therefore be given a realistic readiness/startup budget rather than being treated as instant-on after process launch.

## Agent throughput/latency characterization

For distinct prompts on the deployed Westmere configuration, observed model behavior was approximately:

```text
TTFT:       1.49-1.75 s
prefill:    ~11-13 tokens/s
text decode ~3.76-3.90 tokens/s
```

Eight decode threads performed best in the tested configuration. Batch/prefill tuning favored eight batch threads at roughly 12.5 tokens/s over six threads at roughly 10.7 tokens/s. Twelve threads oversubscribed the assigned CPU set and performed worse.

The host cannot provide twelve physical cores to this container under the current allocation; benchmarks that assume twelve dedicated physical cores are invalid for this deployment.

## Repeated-prompt cache result

A repeated identical prompt produced steady-state TTFT around:

```text
~583 ms typical
~620 ms p95
```

This result reflects prompt/KV-cache reuse and **must not** be presented as representative latency for unrelated real user prompts. Distinct-prompt TTFT above is the more honest operational figure.

## Specialist-service latency observed

Representative tested operations:

| Operation | Observed latency |
|---|---:|
| deterministic forecast tool path | ~230-246 ms |
| Westmere radiology ONNX path | ~733 ms |
| streamed general assistant TTFT | ~1.3 s in tested flow |
| agent-router example | ~20 s end-to-end in tested case |

The wide difference is expected: a deterministic specialist operation does not pay the same generative-model cost as an ambiguous request that requires an LLM routing/generation stage.

## Routing behavior validated

The deployed gateway demonstrated:

- deterministic routing with zero LLM calls for supported, unambiguous operational requests;
- bounded tool argument validation;
- one-call local agent routing for ambiguous supported operations;
- successful specialist tool execution;
- streaming assistant output; and
- request/audit tracing.

A tested streaming assistant path produced 65 delta events over roughly 400 output characters.

## Context-size test

A 2,048-token and 4,096-token context comparison was effectively equivalent for the tested prompt set. The deployed system retains 4,096 tokens for additional conversational headroom.

This does not imply that every 4,096-token workload will have identical latency or memory behavior; it records only the tested comparison.

## Memory/CPU behavior

Observed resident memory in the deployed profile was approximately:

```text
agent:   ~1.66 GiB RSS
gateway: ~9 MiB RSS
```

Idle CPU was near zero, while active model generation saturated the eight allocated cores.

The container's available `RLIMIT_MEMLOCK` was approximately 8 MiB, so an attempted memory lock could not lock the full model. The runtime correctly continued using memory-mapped model access.

The host also reported NUMA balancing enabled. Any later latency tuning should re-test NUMA placement rather than assuming the current setting is optimal.

## Current capacity statement

The current hardware is **not** a high-concurrency generative AI server.

A defensible statement for the deployed prototype is:

- the agent runtime is optimized for **one active LLM generation at a time** (`parallel 1`);
- the gateway accepts at most **two concurrent in-flight requests** in the current profile;
- deterministic forecasting/status/tool paths can complete substantially faster than generative work;
- an active LLM generation can saturate all eight allocated Westmere cores; and
- no benchmark has demonstrated hundreds of simultaneous users or national-scale clinical load.

For demonstration and small controlled pilot workloads, this server is capable of proving the architecture end-to-end. For broad concurrent clinical usage, newer CPU hardware or GPU inference capacity would be required and must be benchmarked against the intended workload.

## Recommended concurrency benchmark

A future capacity test should separate workloads because one single "requests/second" number is misleading for heterogeneous AI services.

Measure separately:

### Gateway/deterministic tools

- 1, 2, 4, 8 concurrent callers;
- forecast/status calls;
- p50/p95/p99;
- gateway CPU/RAM;
- specialist CPU time.

### Generative assistant

- one active generation baseline;
- two queued/concurrent user requests;
- TTFT;
- total completion latency;
- decode tokens/s;
- queue wait time;
- CPU saturation.

### Radiology

- sequential and low concurrency image inference;
- image sizes up to configured limit;
- latency and RSS;
- error behavior under overload.

### Translation

- 1, 2, 4 simultaneous audio sessions;
- ASR chunk latency;
- translation latency;
- dropped/late segments;
- CPU/RAM.

The load generator should run outside CT 110 so benchmark work does not consume the same CPU allocation as the target.

## Clinical/production boundary

The current deployment demonstrates local AI architecture and measurable inference behavior. It does not demonstrate:

- clinical diagnostic validity;
- safe autonomous treatment decisions;
- production patient-record integration;
- organization-wide identity/access control;
- regulatory compliance; or
- national-scale availability/capacity.

Radiology remains clinician-review-required, forecasts use synthetic data, and the assistant must not invent production patient context.
