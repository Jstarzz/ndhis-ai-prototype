import argparse
import concurrent.futures
import json
import statistics
import time
import urllib.error
import urllib.request

PROMPTS = [
    "Forecast A&E patient volume for the next 30 days.",
    "What is the projected bed occupancy for Medical Ward next month?",
    "Forecast respiratory disease incidence at JNF for the next 30 days.",
    "Are the local AI services online?",
]


def request_once(url: str, key: str, index: int):
    payload = json.dumps({"messages": [{"role": "user", "content": PROMPTS[index % len(PROMPTS)]}]}).encode()
    request = urllib.request.Request(
        url,
        data=payload,
        headers={
            "Content-Type": "application/json",
            "X-NDHIS-Demo-Key": key,
            "X-NDHIS-User": f"bench-{index}",
            "X-NDHIS-Role": "doctor",
        },
        method="POST",
    )
    started = time.perf_counter()
    try:
        with urllib.request.urlopen(request, timeout=120) as response:
            body = json.loads(response.read())
        return time.perf_counter() - started, None, body.get("tool")
    except Exception as exc:
        return time.perf_counter() - started, str(exc), None


def percentile(values, quantile):
    ordered = sorted(values)
    if not ordered:
        return 0.0
    index = min(len(ordered) - 1, int(round((len(ordered) - 1) * quantile)))
    return ordered[index]


def run(url: str, key: str, concurrency: int, requests: int):
    started = time.perf_counter()
    with concurrent.futures.ThreadPoolExecutor(max_workers=concurrency) as pool:
        results = list(pool.map(lambda index: request_once(url, key, index), range(requests)))
    elapsed = time.perf_counter() - started
    latencies = [item[0] for item in results if item[1] is None]
    errors = [item[1] for item in results if item[1] is not None]
    return {
        "concurrency": concurrency,
        "ok": len(latencies),
        "errors": len(errors),
        "rps": len(latencies) / elapsed if elapsed else 0,
        "p50": statistics.median(latencies) if latencies else 0,
        "p95": percentile(latencies, 0.95),
        "max": max(latencies, default=0),
        "first_error": errors[0] if errors else "",
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--url", default="http://localhost:8080/api/chat")
    parser.add_argument("--key", default="ndhis-local-demo")
    parser.add_argument("--concurrency", nargs="+", type=int, default=[1, 2, 4, 8, 16])
    parser.add_argument("--requests", type=int, default=20)
    args = parser.parse_args()
    print(f"{'conc':>5} {'ok':>5} {'err':>5} {'rps':>8} {'p50':>9} {'p95':>9} {'max':>9}")
    for concurrency in args.concurrency:
        result = run(args.url, args.key, concurrency, args.requests)
        print(f"{result['concurrency']:>5} {result['ok']:>5} {result['errors']:>5} {result['rps']:>8.2f} {result['p50']:>8.2f}s {result['p95']:>8.2f}s {result['max']:>8.2f}s")
        if result["first_error"]:
            print("error:", result["first_error"])


if __name__ == "__main__":
    main()
