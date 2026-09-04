import argparse
import json
import os
import subprocess
import sys
import threading
from datetime import datetime, timezone
from pathlib import Path

from bench_agent import run as run_gateway
from bench_vllm import run_level as run_vllm


def sample_gpu(stop: threading.Event, samples: list[dict]):
    while not stop.wait(0.5):
        try:
            result = subprocess.run(
                [
                    "nvidia-smi",
                    "--query-gpu=utilization.gpu,memory.used,memory.total,temperature.gpu,power.draw",
                    "--format=csv,noheader,nounits",
                ],
                capture_output=True,
                text=True,
                timeout=3,
                check=False,
            )
            line = result.stdout.strip().splitlines()[0]
            util, used, total, temp, power = [float(value.strip()) for value in line.split(",")]
            samples.append({"utilization": util, "memory_used_mb": used, "memory_total_mb": total, "temperature_c": temp, "power_w": power})
        except Exception:
            return


def cpu_times():
    line = Path("/proc/stat").read_text().splitlines()[0].split()[1:]
    values = [int(value) for value in line]
    idle = values[3] + values[4]
    return sum(values), idle


def memory_usage():
    values = {}
    for line in Path("/proc/meminfo").read_text().splitlines():
        key, value = line.split(":", 1)
        values[key] = int(value.strip().split()[0])
    total = values["MemTotal"]
    available = values["MemAvailable"]
    return total - available, total


def sample_system(stop: threading.Event, samples: list[dict]):
    try:
        previous_total, previous_idle = cpu_times()
    except Exception:
        return
    while not stop.wait(0.5):
        try:
            total, idle = cpu_times()
            delta_total = total - previous_total
            delta_idle = idle - previous_idle
            utilization = 100 * (delta_total - delta_idle) / delta_total if delta_total else 0
            used_kb, total_kb = memory_usage()
            load1 = os.getloadavg()[0]
            samples.append({"cpu_pct": utilization, "memory_used_gb": used_kb / 1024 / 1024, "memory_total_gb": total_kb / 1024 / 1024, "load1": load1})
            previous_total = total
            previous_idle = idle
        except Exception:
            return


def summarize_gpu(samples: list[dict]):
    if not samples:
        return None
    return {
        "peak_utilization_pct": max(item["utilization"] for item in samples),
        "peak_memory_used_mb": max(item["memory_used_mb"] for item in samples),
        "memory_total_mb": max(item["memory_total_mb"] for item in samples),
        "peak_temperature_c": max(item["temperature_c"] for item in samples),
        "peak_power_w": max(item["power_w"] for item in samples),
    }


def summarize_system(samples: list[dict]):
    if not samples:
        return None
    return {
        "peak_cpu_pct": round(max(item["cpu_pct"] for item in samples), 1),
        "peak_memory_used_gb": round(max(item["memory_used_gb"] for item in samples), 2),
        "memory_total_gb": round(max(item["memory_total_gb"] for item in samples), 2),
        "peak_load1": round(max(item["load1"] for item in samples), 2),
    }


def hardware_snapshot():
    try:
        result = subprocess.run([sys.executable, "scripts/hardware_probe.py"], capture_output=True, text=True, timeout=10, check=False)
        return json.loads(result.stdout)
    except Exception as exc:
        return {"error": str(exc)}


def measured(fn):
    stop = threading.Event()
    gpu_samples = []
    system_samples = []
    gpu_thread = threading.Thread(target=sample_gpu, args=(stop, gpu_samples), daemon=True)
    system_thread = threading.Thread(target=sample_system, args=(stop, system_samples), daemon=True)
    gpu_thread.start()
    system_thread.start()
    try:
        result = fn()
    finally:
        stop.set()
        gpu_thread.join(timeout=2)
        system_thread.join(timeout=2)
    result["gpu"] = summarize_gpu(gpu_samples)
    result["system"] = summarize_system(system_samples)
    return result


def markdown(report: dict) -> str:
    lines = [
        "# NDHIS AI Benchmark",
        "",
        f"Generated: {report['timestamp']}",
        f"Profile: {report['profile']}",
        f"CPU: {report['hardware'].get('cpu_model', 'unknown')}",
        f"Sockets: {report['hardware'].get('sockets', 'unknown')} · Logical CPUs: {report['hardware'].get('logical_cpus', 'unknown')} · NUMA nodes: {report['hardware'].get('numa_nodes', 'unknown')}",
        f"RAM: {report['hardware'].get('memory_gb', 'unknown')} GB · CPU tier: {report['hardware'].get('vllm_cpu_tier', 'unknown')}",
        "",
        "## Raw agent server",
        "",
        "| Concurrency | OK | Errors | RPS | p50 ms | p95 ms | Peak CPU % | Peak RAM GB | Peak VRAM MB |",
        "| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |",
    ]
    for item in report["vllm"]:
        gpu = item.get("gpu") or {}
        system = item.get("system") or {}
        lines.append(f"| {item['concurrency']} | {item['ok']} | {item['errors']} | {item['rps']} | {item['p50_ms']} | {item['p95_ms']} | {system.get('peak_cpu_pct', 'n/a')} | {system.get('peak_memory_used_gb', 'n/a')} | {gpu.get('peak_memory_used_mb', 'n/a')} |")
    lines.extend([
        "",
        "## Full gateway + tools",
        "",
        "| Concurrency | OK | Errors | RPS | p50 s | p95 s | Peak CPU % | Peak RAM GB | Peak VRAM MB |",
        "| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |",
    ])
    for item in report["gateway"]:
        gpu = item.get("gpu") or {}
        system = item.get("system") or {}
        lines.append(f"| {item['concurrency']} | {item['ok']} | {item['errors']} | {item['rps']:.2f} | {item['p50']:.2f} | {item['p95']:.2f} | {system.get('peak_cpu_pct', 'n/a')} | {system.get('peak_memory_used_gb', 'n/a')} | {gpu.get('peak_memory_used_mb', 'n/a')} |")
    lines.extend(["", "Use the highest concurrency that still meets the chosen latency and error-rate target as the reference capacity for this hardware.", ""])
    return "\n".join(lines)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--profile", choices=["gpu", "cpu", "legacy-cpu", "westmere"], default="gpu")
    parser.add_argument("--vllm-url", default="http://localhost:8000/v1/chat/completions")
    parser.add_argument("--gateway-url", default="http://localhost:8080/api/chat")
    parser.add_argument("--model")
    parser.add_argument("--key", default="ndhis-local-demo")
    parser.add_argument("--concurrency", nargs="+", type=int)
    parser.add_argument("--requests", type=int, default=40)
    parser.add_argument("--output", default="data/benchmarks")
    args = parser.parse_args()

    if args.model:
        model = args.model
    elif args.profile == "gpu":
        model = "ndhis-agent"
    elif args.profile == "westmere":
        model = "ndhis-agent-westmere"
    else:
        model = "ndhis-agent-cpu"
    if args.profile == "gpu":
        concurrency = args.concurrency or [1, 2, 4, 8, 16, 32]
    elif args.profile == "cpu":
        concurrency = args.concurrency or [1, 2, 4, 8]
    elif args.profile == "westmere":
        concurrency = args.concurrency or [1, 2]
    else:
        concurrency = args.concurrency or [1, 2, 4]
    report = {"timestamp": datetime.now(timezone.utc).isoformat(), "profile": args.profile, "model": model, "hardware": hardware_snapshot(), "vllm": [], "gateway": []}
    for level in concurrency:
        report["vllm"].append(measured(lambda c=level: run_vllm(args.vllm_url, model, c, args.requests)))
        report["gateway"].append(measured(lambda c=level: run_gateway(args.gateway_url, args.key, c, args.requests)))

    output = Path(args.output)
    output.mkdir(parents=True, exist_ok=True)
    stamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    json_path = output / f"bench-{args.profile}-{stamp}.json"
    md_path = output / f"bench-{args.profile}-{stamp}.md"
    json_path.write_text(json.dumps(report, indent=2), encoding="utf-8")
    md_path.write_text(markdown(report), encoding="utf-8")
    print(json_path)
    print(md_path)


if __name__ == "__main__":
    main()
