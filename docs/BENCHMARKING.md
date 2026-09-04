# Benchmarking

The reference hardware benchmark has two layers.

## Raw agent capacity

```bash
python scripts/bench_vllm.py --concurrency 1 2 4 8 16 32 --requests 40
```

This measures the vLLM tool-router without specialist-service latency.

## Full request path

```bash
python scripts/bench_agent.py --concurrency 1 2 4 8 16 32 --requests 40
```

This measures the Go gateway, two agent generations, and any tool execution used by the request.

## Hardware report

```bash
python scripts/bench_suite.py --concurrency 1 2 4 8 16 32 --requests 40
```

The suite writes JSON and Markdown reports into `data/benchmarks/` and samples CPU saturation, RAM, load, plus GPU utilization, VRAM, temperature, and power when `nvidia-smi` is available.

Select an interactive latency target before interpreting capacity. A useful prototype target is zero request failures with p95 time low enough that the interface still feels interactive. The highest concurrency that satisfies the chosen target is the measured reference capacity of that hardware and model configuration.

Do not extrapolate from registered users. Size around peak simultaneous generations, live translation sessions, radiology jobs, and background forecasting separately.

CPU profile:

```bash
python scripts/bench_suite.py --profile cpu --concurrency 1 2 4 8 --requests 20
```

Run `python scripts/hardware_probe.py` first. Current vLLM x86 CPU serving requires AVX2 at minimum.
## Convert a benchmark into measured capacity

Choose the latency and error thresholds you are willing to accept, then analyze the generated JSON:

```bash
python scripts/capacity_report.py data/benchmarks/bench-cpu-YYYYMMDDTHHMMSSZ.json \
  --agent-p95-ms 2000 \
  --gateway-p95-seconds 5 \
  --max-error-rate 0.01
```

The result reports the highest tested concurrency that met those thresholds. It does not extrapolate to untested hardware or user counts.
