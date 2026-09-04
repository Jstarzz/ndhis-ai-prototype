import argparse
import json
import shutil
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
REQUIRED = ["agent", "asr", "translation", "forecast", "radiology"]


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


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--profile", choices=["gpu", "cpu", "legacy-cpu"], default="gpu")
    args = parser.parse_args()

    models = ROOT / "models"
    if args.profile != "gpu":
        models = models / "cpu"
    rows = []
    missing = []
    total = 0
    for name in REQUIRED:
        path = models / name
        files = [item for item in path.iterdir() if item.name != ".gitkeep"] if path.exists() else []
        size = size_bytes(path) if files else 0
        total += size
        state = "ready" if files else "missing"
        rows.append({"model": name, "state": state, "size_bytes": size, "size": format_size(size), "path": str(path)})
        if not files:
            missing.append(name)

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
        readiness_key = "vllm_cpu_ready" if args.profile == "cpu" else "legacy_cpu_ready"
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
