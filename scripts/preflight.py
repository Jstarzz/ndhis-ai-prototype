import argparse
import json
import os
import shutil
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def size_bytes(path: Path) -> int:
    return sum(item.stat().st_size for item in path.rglob("*") if item.is_file())


def format_size(value: int) -> str:
    units = ["B", "KB", "MB", "GB", "TB"]
    size = float(value)
    for unit in units:
        if size < 1024 or unit == units[-1]:
            return f"{size:.2f} {unit}"
        size /= 1024
    return f"{size:.2f} TB"


def command_version(command: list[str]) -> str:
    try:
        result = subprocess.run(command, capture_output=True, text=True, timeout=5, check=False)
        output = (result.stdout or result.stderr).strip().splitlines()
        return output[0] if output else "available"
    except Exception:
        return "unavailable"


def cpu_probe():
    result = subprocess.run([sys.executable, str(ROOT / "scripts" / "hardware_probe.py")], capture_output=True, text=True, check=False)
    try:
        report = json.loads(result.stdout)
    except Exception:
        report = {"vllm_cpu_ready": False, "error": (result.stderr or result.stdout).strip()}
    return report


def model_checks(profile: str):
    models = ROOT / "models"
    if profile == "westmere":
        models = Path(os.environ.get("WESTMERE_MODEL_ROOT", str(models))).expanduser().resolve()
    if profile == "gpu":
        return [(name, models / name) for name in ["agent", "asr", "translation", "forecast", "radiology"]]
    if profile in {"cpu", "legacy-cpu"}:
        cpu = models / "cpu"
        return [(name, cpu / name) for name in ["agent", "asr", "translation", "forecast", "radiology"]]
    cpu = models / "cpu"
    return [
        ("agent", cpu / "agent"),
        ("asr", cpu / "asr"),
        ("translation", cpu / "translation"),
        ("radiology", models / "westmere" / "radiology"),
    ]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--profile", choices=["gpu", "cpu", "legacy-cpu", "westmere"], default="gpu")
    args = parser.parse_args()

    rows = []
    missing = []
    total = 0
    for name, path in model_checks(args.profile):
        files = [item for item in path.iterdir() if item.name != ".gitkeep"] if path.exists() else []
        size = size_bytes(path) if files else 0
        total += size
        state = "ready" if files else "missing"
        rows.append({"model": name, "state": state, "size_bytes": size, "size": format_size(size), "path": str(path)})
        if not files:
            missing.append(name)
    if args.profile == "westmere":
        rows.append({"model": "forecast", "state": "built-in", "size_bytes": 0, "size": "0 B", "path": "autoregressive ridge"})

    environment = {
        "docker": command_version(["docker", "--version"]) if shutil.which("docker") else "unavailable",
        "compose": command_version(["docker", "compose", "version"]) if shutil.which("docker") else "unavailable",
    }
    hardware_ready = True
    if args.profile == "gpu":
        environment["nvidia_smi"] = command_version(["nvidia-smi", "--query-gpu=name,driver_version,memory.total", "--format=csv,noheader"]) if shutil.which("nvidia-smi") else "unavailable"
        hardware_ready = environment["nvidia_smi"] != "unavailable"
    else:
        environment["cpu"] = cpu_probe()
        readiness_key = {
            "cpu": "vllm_cpu_ready",
            "legacy-cpu": "legacy_cpu_ready",
            "westmere": "westmere_cpu_ready",
        }[args.profile]
        hardware_ready = bool(environment["cpu"].get(readiness_key))

    minimum_free = 15 * 1024**3 if args.profile != "gpu" else 40 * 1024**3
    report = {
        "profile": args.profile,
        "models": rows,
        "model_storage_bytes": total,
        "model_storage": format_size(total),
        "recommended_free_disk": format_size(max(total * 2, minimum_free)),
        "environment": environment,
        "ready": not missing and environment["docker"] != "unavailable" and hardware_ready,
    }
    print(json.dumps(report, indent=2))
    if missing:
        raise SystemExit("missing model directories: " + ", ".join(missing))
    if not hardware_ready:
        raise SystemExit(f"hardware does not meet the {args.profile} profile requirements")


if __name__ == "__main__":
    main()
