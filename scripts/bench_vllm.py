import argparse
import concurrent.futures
import json
import statistics
import time
import urllib.request

TOOLS = [
    {"name": "forecast_patient_volume", "arguments": {"facility": "string", "department": "string", "horizon_days": "integer"}},
    {"name": "forecast_bed_occupancy", "arguments": {"facility": "string", "department": "string", "horizon_days": "integer"}},
    {"name": "forecast_disease_incidence", "arguments": {"facility": "string", "disease": "string", "horizon_days": "integer"}},
    {"name": "get_radiology_result", "arguments": {"result_id": "string"}},
    {"name": "get_service_status", "arguments": {}},
]

PROMPTS = [
    "Forecast A&E patient volume for the next 30 days at JNF.",
    "What will Medical Ward bed occupancy look like next month?",
    "Forecast respiratory disease incidence at JNF for 30 days.",
    "Are the local AI services online?",
    "Get the radiology analysis with result id 7c0f0b151a909f17.",
]

SYSTEM = "You are the NDHIS local AI router. Select at most one tool. Return exactly one JSON object and no markdown. For a tool call use {\"type\":\"tool\",\"name\":\"tool_name\",\"arguments\":{...}}. If no tool is needed use {\"type\":\"answer\",\"content\":\"answer\"}. Available tools: " + json.dumps(TOOLS, separators=(",", ":"))


def percentile(values, p):
    ordered = sorted(values)
    index = min(len(ordered) - 1, max(0, round((len(ordered) - 1) * p)))
    return ordered[index]


def request_once(url, model, prompt):
    payload = json.dumps(
        {
            "model": model,
            "messages": [
                {"role": "system", "content": SYSTEM},
                {"role": "user", "content": prompt},
            ],
            "temperature": 0,
            "max_tokens": 160,
            "response_format": {"type": "json_object"},
        }
    ).encode()
    req = urllib.request.Request(url, data=payload, headers={"Content-Type": "application/json"}, method="POST")
    started = time.perf_counter()
    try:
        with urllib.request.urlopen(req, timeout=90) as response:
            body = json.loads(response.read())
        content = body["choices"][0]["message"]["content"]
        json.loads(content)
        return True, time.perf_counter() - started, None
    except Exception as exc:
        return False, time.perf_counter() - started, str(exc)


def run_level(url, model, concurrency, requests):
    jobs = [PROMPTS[i % len(PROMPTS)] for i in range(requests)]
    started = time.perf_counter()
    with concurrent.futures.ThreadPoolExecutor(max_workers=concurrency) as pool:
        results = list(pool.map(lambda prompt: request_once(url, model, prompt), jobs))
    elapsed = time.perf_counter() - started
    latencies = [latency for ok, latency, _ in results if ok]
    errors = [error for ok, _, error in results if not ok]
    if not latencies:
        return {
            "concurrency": concurrency,
            "ok": 0,
            "errors": len(errors),
            "rps": 0,
            "p50_ms": 0,
            "p95_ms": 0,
            "max_ms": 0,
            "error": errors[0] if errors else "no successful requests",
        }
    return {
        "concurrency": concurrency,
        "ok": len(latencies),
        "errors": len(errors),
        "rps": round(len(latencies) / elapsed, 2),
        "p50_ms": round(statistics.median(latencies) * 1000),
        "p95_ms": round(percentile(latencies, 0.95) * 1000),
        "max_ms": round(max(latencies) * 1000),
        "error": errors[0] if errors else None,
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--url", default="http://localhost:8000/v1/chat/completions")
    parser.add_argument("--model", default="ndhis-agent")
    parser.add_argument("--concurrency", nargs="+", type=int, default=[1, 2, 4, 8, 16, 32])
    parser.add_argument("--requests", type=int, default=40)
    args = parser.parse_args()

    for concurrency in args.concurrency:
        print(json.dumps(run_level(args.url, args.model, concurrency, args.requests)))


if __name__ == "__main__":
    main()
