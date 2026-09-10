# NDHIS AI evaluation and benchmark workflow

The prototype now has three different evaluation layers. They should not be mixed together.

## 1. Deterministic gateway correctness

Run the complete Go suite:

```bash
cd services/gateway
go test ./...
```

The suite includes regression coverage for canonical forecasts, multi-turn slot filling, capability queries and the colloquial phrasing that previously failed:

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
  --runs 3 \
  --output data/benchmarks/assistant.json
```

For each case the harness records wall latency, gateway-reported latency, routing mode, LLM call count, tool selection and time to first streamed assistant delta when applicable.

Run immediately after a restart if you specifically want to measure cold behavior. Run again after the background warm-up completes to measure the normal user-facing path.

Useful focused runs:

```bash
python scripts/benchmark_ndhis.py --only general_local_inference --runs 5
python scripts/benchmark_ndhis.py --only nuanced_emergency_forecast --runs 5
python scripts/benchmark_ndhis.py --only colloquial_minute_and_half --runs 20
```

## 3. Radiology runtime and labeled smoke evaluation

Build the public local sample:

```bash
python scripts/fetch_radiology_eval.py --count-per-class 5
```

Run it against the gateway:

```bash
python scripts/eval_radiology.py \
  --base http://127.0.0.1:8080 \
  --key ndhis-local-demo \
  --manifest data/evals/radiology/manifest.json \
  --output data/benchmarks/radiology.json
```

Run all review-instruction variants to test prompt invariance:

```bash
python scripts/eval_radiology.py \
  --base http://127.0.0.1:8080 \
  --key ndhis-local-demo \
  --manifest data/evals/radiology/manifest.json \
  --prompt-all
```

Because the Westmere ONNX path is deterministic and does not use the text prompt to alter CNN inference, the unhealthy score should remain stable across prompt variants. A non-zero drift is a regression signal worth investigating.

The labeled sample is a research smoke test. Training-data overlap with the current radiology pipeline has not been excluded, and the label scope is narrow. Do not report these results as clinical validation, diagnostic accuracy for JNF, or evidence that a below-threshold image is clinically normal.

## Performance acceptance targets

The current practical targets are:

| Path | Target |
| --- | ---: |
| deterministic service status | under 100 ms |
| deterministic forecast | under 750 ms |
| radiology pipeline | under 2 s |
| deterministic colloquial route | under 750 ms including forecast service |
| general assistant first streamed text | under 2 s when warm if the target model sustains the measured TTFT |
| operational LLM router | one LLM call, no raw 502 on bad model output |

These are engineering targets for the prototype, not clinical service-level guarantees.
