# NDHIS AI evaluation and benchmark workflow

The prototype has four different evaluation layers. Keep correctness, steady-state latency, startup latency and clinical-model smoke evaluation separate.

## 1. Deterministic gateway correctness

Run the complete Go suite:

```bash
cd services/gateway
go test ./...
```

The suite includes regression coverage for canonical forecasts, multi-turn slot filling, capability queries, startup warm-up behavior and the colloquial phrasing that previously failed:

```text
Gimme forecasts for A and E patients for the next minute and a half
```

Run the parser/router microbenchmarks separately:

```bash
cd services/gateway
go test -run '^$' -bench 'V2' -benchmem
```

These benchmarks measure gateway language normalization and deterministic route resolution only. They do not measure model or forecast-service latency.

## 2. End-to-end assistant benchmark

The prompt set lives in `evals/assistant/cases.json` and includes deterministic routes, colloquial routes, a general streamed assistant request and a nuanced operational request intended for the constrained local router.

```bash
python scripts/benchmark_ndhis.py \
  --base http://127.0.0.1:8080 \
  --key ndhis-local-demo \
  --runs 5 \
  --output data/benchmarks/assistant.json
```

For each case the harness records wall latency, gateway-reported latency, routing mode, LLM call count, tool selection, first execution-stage latency and first streamed assistant delta. It reports median and p95 values for both execution start and first text so TTFT variance is visible instead of only the median.

Useful focused runs:

```bash
python scripts/benchmark_ndhis.py --only general_local_inference --runs 10
python scripts/benchmark_ndhis.py --only nuanced_emergency_forecast --runs 10
python scripts/benchmark_ndhis.py --only colloquial_minute_and_half --runs 20
```

## 3. Process-cold and warm-state benchmark

The Westmere gateway now completes its model warm-up before it begins accepting traffic. Measure startup readiness separately from the first user request:

```bash
python scripts/benchmark_westmere_startup.py \
  --base http://127.0.0.1:8080 \
  --key ndhis-local-demo \
  --runs 3 \
  --output data/benchmarks/westmere-startup.json
```

Each run restarts only the `agent` and `gateway` containers, waits for `/api/health`, then measures the first post-ready streamed assistant request and repeated steady-state requests. The report separates:

- container restart command duration;
- time until the gateway becomes reachable after coordinated warm-up;
- first post-ready TTFT and completion time;
- steady-state TTFT and completion time.

This is a process-cold benchmark, not a physical-disk cold benchmark. The script intentionally does not drop the Linux filesystem page cache because doing so is host-wide, privileged and would contaminate other services.

When comparing Westmere model-serving settings, change one variable at a time and rerun both the startup and steady-state benchmarks. Candidate settings live in `.env.westmere` and include context size, load mode, cache reuse, priority, polling, speculative mode, decode threads, prompt threads and NUMA placement.

## 4. Radiology runtime and labeled smoke evaluation

Build the public local sample:

```bash
python scripts/fetch_radiology_eval.py --count-per-class 5
```

The pinned dataset revision currently packages the radiographs inside `covid19_radiography.zip`. The fetcher supports both directly listed image files and ZIP-packaged images, selects the same deterministic lexicographic sample in either layout, and records a SHA-256 for every extracted test image in the manifest.

Run it against the gateway:

```bash
python scripts/eval_radiology.py \
  --base http://127.0.0.1:8080 \
  --key ndhis-local-demo \
  --manifest data/evals/radiology/manifest.json \
  --output data/benchmarks/radiology.json
```

The output includes configured-threshold screening metrics, ROC AUC, score separation, subtype accuracy when the threshold triggers stage three, contract-failure checks and exploratory threshold analysis. These are descriptive smoke metrics only; do not tune the deployed threshold from this 20-image sample.

Run all review-instruction variants to test prompt invariance:

```bash
python scripts/eval_radiology.py \
  --base http://127.0.0.1:8080 \
  --key ndhis-local-demo \
  --manifest data/evals/radiology/manifest.json \
  --prompt-all \
  --output data/benchmarks/radiology-prompt-invariance.json
```

`--prompt-all` automatically uses a dedicated evaluation user and spaces gateway requests by 2.25 seconds unless `--pace-seconds` is supplied. This keeps the test inside the normal 30 RPM per-user gateway policy rather than turning an invariance run into a rate-limit benchmark. HTTP 429 responses can be retried with `--retry-429`, which defaults to 2.

Because the Westmere ONNX path is deterministic and does not use the text prompt to alter CNN inference, the unhealthy score should remain stable across prompt variants. Check `prompt_invariance_complete_cases` before interpreting the drift metric.

The labeled sample is a research smoke test. Training-data overlap with the current radiology pipeline has not been excluded, and the label scope is narrow. Do not report these results as clinical validation, diagnostic accuracy for JNF, or evidence that a below-threshold image is clinically normal.

## Performance acceptance targets

| Path | Target |
| --- | ---: |
| deterministic service status | under 100 ms |
| deterministic forecast | under 750 ms |
| radiology pipeline | under 2 s |
| deterministic colloquial route | under 750 ms including forecast service |
| general assistant steady-state first text | median under 750 ms, p95 under 1.5 s |
| first post-ready assistant text | under 1.5 s |
| operational LLM router | one LLM call, no raw 502 on bad model output |

The TTFT targets are engineering goals based on the measured Gemma/OpenBLAS range, not clinical service-level guarantees. Total conversational completion remains constrained by Westmere autoregressive decode throughput.
