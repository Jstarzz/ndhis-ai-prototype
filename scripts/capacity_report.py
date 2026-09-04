import argparse
import json
from pathlib import Path


def error_rate(item):
    total = item["ok"] + item["errors"]
    return item["errors"] / total if total else 1.0


def select(rows, p95_key, max_p95, max_error_rate):
    passing = [row for row in rows if row[p95_key] <= max_p95 and error_rate(row) <= max_error_rate]
    return max(passing, key=lambda row: row["concurrency"], default=None)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("benchmark")
    parser.add_argument("--agent-p95-ms", type=float, required=True)
    parser.add_argument("--gateway-p95-seconds", type=float, required=True)
    parser.add_argument("--max-error-rate", type=float, default=0.01)
    parser.add_argument("--output")
    args = parser.parse_args()

    report = json.loads(Path(args.benchmark).read_text(encoding="utf-8"))
    agent = select(report["vllm"], "p95_ms", args.agent_p95_ms, args.max_error_rate)
    gateway = select(report["gateway"], "p95", args.gateway_p95_seconds, args.max_error_rate)
    result = {
        "benchmark": args.benchmark,
        "profile": report["profile"],
        "model": report["model"],
        "hardware": report.get("hardware", {}),
        "thresholds": {
            "agent_p95_ms": args.agent_p95_ms,
            "gateway_p95_seconds": args.gateway_p95_seconds,
            "max_error_rate": args.max_error_rate,
        },
        "measured_capacity": {
            "agent_concurrency": agent["concurrency"] if agent else 0,
            "gateway_concurrency": gateway["concurrency"] if gateway else 0,
        },
        "agent_reference": agent,
        "gateway_reference": gateway,
    }
    rendered = json.dumps(result, indent=2)
    print(rendered)
    if args.output:
        Path(args.output).write_text(rendered + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
