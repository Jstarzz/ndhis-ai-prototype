import argparse
import json
import statistics
import time
import urllib.error
import urllib.request
from pathlib import Path


def percentile(values, fraction):
    if not values:
        return None
    ordered = sorted(values)
    index = min(len(ordered) - 1, max(0, int(round((len(ordered) - 1) * fraction))))
    return ordered[index]


def stream_request(base, key, messages, timeout):
    payload = json.dumps({"messages": messages}).encode()
    request = urllib.request.Request(base.rstrip("/") + "/api/chat/stream", data=payload, method="POST")
    request.add_header("Content-Type", "application/json")
    request.add_header("X-NDHIS-Demo-Key", key)
    request.add_header("X-NDHIS-User", "bench-doctor")
    request.add_header("X-NDHIS-Role", "doctor")
    started = time.perf_counter()
    first_stage_ms = None
    first_delta_ms = None
    result = None
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            for raw_line in response:
                line = raw_line.decode("utf-8", errors="replace").strip()
                if not line:
                    continue
                event = json.loads(line)
                elapsed_ms = (time.perf_counter() - started) * 1000
                if event.get("type") == "stage" and first_stage_ms is None:
                    first_stage_ms = elapsed_ms
                if event.get("type") == "delta" and event.get("delta") and first_delta_ms is None:
                    first_delta_ms = elapsed_ms
                if event.get("type") == "error":
                    raise RuntimeError(event.get("error") or "stream error")
                if event.get("type") == "result":
                    result = event.get("response")
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"HTTP {exc.code}: {detail}") from exc
    wall_ms = (time.perf_counter() - started) * 1000
    if result is None:
        raise RuntimeError("stream ended without result")
    return {
        "wall_ms": wall_ms,
        "first_stage_ms": first_stage_ms,
        "first_delta_ms": first_delta_ms,
        "server_latency_ms": result.get("latency_ms"),
        "routing": result.get("routing"),
        "llm_calls": result.get("llm_calls"),
        "tool": result.get("tool"),
        "intent": result.get("intent"),
    }


def summarize(samples):
    walls = [sample["wall_ms"] for sample in samples]
    server = [float(sample["server_latency_ms"]) for sample in samples if sample.get("server_latency_ms") is not None]
    stages = [sample["first_stage_ms"] for sample in samples if sample.get("first_stage_ms") is not None]
    deltas = [sample["first_delta_ms"] for sample in samples if sample.get("first_delta_ms") is not None]
    return {
        "runs": len(samples),
        "median_wall_ms": statistics.median(walls),
        "p95_wall_ms": percentile(walls, 0.95),
        "median_server_ms": statistics.median(server) if server else None,
        "p95_server_ms": percentile(server, 0.95) if server else None,
        "median_first_stage_ms": statistics.median(stages) if stages else None,
        "p95_first_stage_ms": percentile(stages, 0.95) if stages else None,
        "median_first_delta_ms": statistics.median(deltas) if deltas else None,
        "p95_first_delta_ms": percentile(deltas, 0.95) if deltas else None,
        "routing": samples[-1].get("routing"),
        "llm_calls": samples[-1].get("llm_calls"),
        "tool": samples[-1].get("tool"),
        "intent": samples[-1].get("intent"),
    }


def prompt_sets(case, mode):
    base = case["messages"]
    variants = case.get("message_variants") or []
    if mode == "repeat" or not variants:
        return [base]
    return [base, *variants]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base", default="http://127.0.0.1:8080")
    parser.add_argument("--key", default="ndhis-local-demo")
    parser.add_argument("--cases", default="evals/assistant/cases.json")
    parser.add_argument("--runs", type=int, default=3)
    parser.add_argument("--timeout", type=float, default=95)
    parser.add_argument("--only", default="")
    parser.add_argument("--prompt-mode", choices=("realistic", "repeat"), default="realistic")
    parser.add_argument("--output", default="")
    args = parser.parse_args()
    if args.runs < 1 or args.runs > 50:
        raise SystemExit("runs must be between 1 and 50")

    cases = json.loads(Path(args.cases).read_text(encoding="utf-8"))["cases"]
    if args.only:
        wanted = {item.strip() for item in args.only.split(",") if item.strip()}
        cases = [case for case in cases if case["id"] in wanted]
    results = {}
    for case in cases:
        samples = []
        sets = prompt_sets(case, args.prompt_mode)
        for run_index in range(args.runs):
            messages = sets[run_index % len(sets)]
            samples.append(stream_request(args.base, args.key, messages, args.timeout))
        summary = summarize(samples)
        summary["expected"] = case.get("expected")
        summary["prompt_mode"] = args.prompt_mode
        summary["distinct_prompt_sets_used"] = min(args.runs, len(sets))
        summary["input_pattern"] = "varied" if len(sets) > 1 else "repeated"
        results[case["id"]] = summary
        print(json.dumps({case["id"]: summary}, indent=2))

    output = {"base": args.base, "runs": args.runs, "prompt_mode": args.prompt_mode, "results": results}
    if args.output:
        path = Path(args.output)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(output, indent=2) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
