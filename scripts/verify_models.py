import argparse
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument("--profile", choices=["gpu", "cpu", "legacy-cpu"], default="gpu")
args = parser.parse_args()

root = Path(__file__).resolve().parents[1] / "models"
if args.profile != "gpu":
    root = root / "cpu"
required = ["agent", "asr", "translation", "forecast", "radiology"]
missing = []
for name in required:
    path = root / name
    files = [item for item in path.iterdir() if item.name != ".gitkeep"] if path.exists() else []
    size = sum(item.stat().st_size for item in path.rglob("*") if item.is_file()) if files else 0
    state = "ready" if files else "missing"
    print(f"{name:12} {state:8} {size / 1024**3:7.2f} GiB  {path}")
    if not files:
        missing.append(name)
if missing:
    raise SystemExit("missing model directories: " + ", ".join(missing))
