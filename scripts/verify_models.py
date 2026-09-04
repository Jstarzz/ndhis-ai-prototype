import argparse
import os
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument("--profile", choices=["gpu", "cpu", "legacy-cpu", "westmere"], default="gpu")
args = parser.parse_args()

root = Path(__file__).resolve().parents[1] / "models"
if args.profile == "westmere":
    root = Path(os.environ.get("WESTMERE_MODEL_ROOT", str(root))).expanduser().resolve()
checks = []
if args.profile == "gpu":
    checks = [(name, root / name) for name in ["agent", "asr", "translation", "forecast", "radiology"]]
elif args.profile in {"cpu", "legacy-cpu"}:
    cpu = root / "cpu"
    checks = [(name, cpu / name) for name in ["agent", "asr", "translation", "forecast", "radiology"]]
else:
    cpu = root / "cpu"
    checks = [
        ("agent", cpu / "agent"),
        ("asr", cpu / "asr"),
        ("translation", cpu / "translation"),
        ("radiology", root / "westmere" / "radiology"),
    ]

missing = []
for name, path in checks:
    files = [item for item in path.iterdir() if item.name != ".gitkeep"] if path.exists() else []
    size = sum(item.stat().st_size for item in path.rglob("*") if item.is_file()) if files else 0
    state = "ready" if files else "missing"
    print(f"{name:12} {state:8} {size / 1024**3:7.2f} GiB  {path}")
    if not files:
        missing.append(name)
if args.profile == "westmere":
    print(f"{'forecast':12} {'built-in':8} {'0.00':>7} GiB  autoregressive ridge")
if missing:
    raise SystemExit("missing model directories: " + ", ".join(missing))
