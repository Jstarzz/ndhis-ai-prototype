import argparse
import json
import statistics
import subprocess
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


def wait_gateway(base, timeout):
    started = time.perf_counter()
    deadline = started + timeout
    last_error = ""
    while time.perf_counter() < deadline:
        try:
            with urllib.request.urlopen(base.rstrip("/") + "/api/health", timeout=2) as response:
                if 200 <= response.status < 300:
                    return (time.perf_counter() - started) * 1000
        except Exception as exc:
            last_error = str(exc)
        time.sleep(0.2)
    raise RuntimeError(f"gateway did not become ready within {timeout}s: {last_error}")


def streamed_assistant(base, key, prompt, timeout, user):
    payload = json.dumps({"messages": [{"role": "user", "content": prompt}]}).encode()
    request = urllib.request.Request(base.rstrip("/") + "/api/chat/stream", data=payload, method="POST")
    request.add_header("Content-Type", "application/json")
    request.add_header("X-NDHIS-Demo-Key", key)
    request.add_header("X-NDHIS-User", user)
    request.add_header("X-NDHIS-Role", "doctor")
    started = time.perf_counter()
    first_delta_ms = None
    result = None
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            for raw_line in response:
                line = raw_line.decode("utf-8", errors="replace").strip()
                if not line:
                    continue
                event = json.loads(line)
                if event.get("type") == "delta" and event.get("delta") and first_delta_ms is None:
                    first_delta_ms = (time.perf_counter() - started) * 1000
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
    if result.get("routing") != "assistant" or result.get("llm_calls") != 1:
        raise RuntimeError(f"benchmark prompt did not use streamed assistant path: {result.get('routing')} / {result.get('llm_calls')}")
    return {
        "first_delta_ms": first_delta_ms,
        "wall_ms": wall_ms,
        "server_latency_ms": result.get("latency_ms"),
        "routing": result.get("routing"),
        "model": result.get("model"),
    }


def restart_stack(env_file, compose_files):
    command = ["docker", "compose"]
    if env_file:
        command.extend(["--env-file", env_file])
    for compose_file in compose_files:
        command.extend(["-f", compose_file])
    command.extend(["restart", "agent", "gateway"])
    started = time.perf_counter()
    subprocess.run(command, check=True)
    return (time.perf_counter() - started) * 1000, command


def summarize(records, key):
    values = [float(record[key]) for record in records if record.get(key) is not None]
    if not values:
        return {"median_ms": None, "p95_ms": None}
    return {"median_ms": statistics.median(values), "p95_ms": percentile(values, 0.95)}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base", default="http://127.0.0.1:8080")
    parser.add_argument("--key", default="ndhis-local-demo")
    parser.add_argument("--env-file", default=".env.westmere")
    parser.add_argument("--compose-files", default="compose.yaml,compose.westmere.yaml")
    parser.add_argument("--runs", type=int, default=3)
    parser.add_argument("--warm-repeats", type=int, default=2)
    parser.add_argument("--startup-timeout", type=float, default=90)
    parser.add_argument("--request-timeout", type=float, default=95)
    parser.add_argument("--settle-seconds", type=float, default=0.5)
    parser.add_argument("--prompt", default="Explain why local inference matters for this prototype in one concise sentence.")
    parser.add_argument("--output", default="data/benchmarks/westmere-startup.json")
    args = parser.parse_args()
    if args.runs < 1 or args.runs > 10:
        raise SystemExit("runs must be between 1 and 10")
    if args.warm_repeats < 1 or args.warm_repeats > 10:
        raise SystemExit("warm-repeats must be between 1 and 10")

    compose_files = [item.strip() for item in args.compose_files.split(",") if item.strip()]
    records = []
    for run_index in range(1, args.runs + 1):
        restart_ms, command = restart_stack(args.env_file, compose_files)
        ready_ms = wait_gateway(args.base, args.startup_timeout)
        if args.settle_seconds > 0:
            time.sleep(args.settle_seconds)
        first = streamed_assistant(args.base, args.key, args.prompt, args.request_timeout, f"startup-bench-{run_index}")
        warm = []
        for warm_index in range(args.warm_repeats):
            warm.append(streamed_assistant(args.base, args.key, args.prompt, args.request_timeout, f"startup-bench-{run_index}-warm-{warm_index}"))
        record = {
            "run": run_index,
            "restart_command_ms": restart_ms,
            "gateway_ready_after_restart_ms": ready_ms,
            "first_post_ready": first,
            "steady_state": warm,
        }
        records.append(record)
        print(json.dumps(record, indent=2))

    first_records = [record["first_post_ready"] for record in records]
    steady_records = [sample for record in records for sample in record["steady_state"]]
    summary = {
        "runs": args.runs,
        "process_cold_scope": "container/process restart only; Linux filesystem page cache is intentionally not dropped",
        "gateway_ready_after_restart": summarize(records, "gateway_ready_after_restart_ms"),
        "first_post_ready_ttft": summarize(first_records, "first_delta_ms"),
        "first_post_ready_completion": summarize(first_records, "wall_ms"),
        "steady_state_ttft": summarize(steady_records, "first_delta_ms"),
        "steady_state_completion": summarize(steady_records, "wall_ms"),
    }
    output = {"summary": summary, "records": records, "restart_command": command}
    encoded = json.dumps(output, indent=2)
    print(encoded)
    if args.output:
        path = Path(args.output)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(encoded + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
