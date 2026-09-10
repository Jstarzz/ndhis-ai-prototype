import argparse
import json
import shutil
from pathlib import Path

try:
    from huggingface_hub import HfApi, hf_hub_download
except ImportError as exc:
    raise SystemExit("huggingface_hub is required; install it or use the existing hf CLI environment") from exc

REPO_ID = "umairinayat/fyp-dataset"
REVISION = "640d49fc9c51643b4e8c83657c395d25889c8627"
LICENSE = "cc-by-nc-sa-4.0"
CLASSES = [
    ("normal", "Normal", "healthy", None),
    ("covid", "COVID", "unhealthy", "COVID"),
    ("viral_pneumonia", "Viral Pneumonia", "unhealthy", "Viral Pneumonia"),
    ("lung_opacity", "Lung_Opacity", "unhealthy", "Lung Opacity"),
]


def class_candidates(files, folder):
    marker = f"/{folder.lower()}/images/"
    candidates = []
    for path in files:
        lowered = "/" + path.lower().lstrip("/")
        if marker in lowered and Path(path).suffix.lower() in {".png", ".jpg", ".jpeg"}:
            candidates.append(path)
    return sorted(candidates)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--count-per-class", type=int, default=5)
    parser.add_argument("--output", default="data/evals/radiology")
    args = parser.parse_args()
    if args.count_per_class < 1 or args.count_per_class > 50:
        raise SystemExit("count-per-class must be between 1 and 50")

    output = Path(args.output)
    output.mkdir(parents=True, exist_ok=True)
    api = HfApi()
    files = list(api.list_repo_files(REPO_ID, repo_type="dataset", revision=REVISION))
    cases = []

    for key, folder, expected_screening, expected_subtype in CLASSES:
        candidates = class_candidates(files, folder)
        if len(candidates) < args.count_per_class:
            raise SystemExit(f"only {len(candidates)} images found for {folder}")
        class_dir = output / key
        class_dir.mkdir(parents=True, exist_ok=True)
        for index, source_path in enumerate(candidates[: args.count_per_class], 1):
            cached = Path(hf_hub_download(repo_id=REPO_ID, filename=source_path, repo_type="dataset", revision=REVISION))
            destination = class_dir / cached.name
            shutil.copy2(cached, destination)
            cases.append(
                {
                    "id": f"{key}-{index:02d}",
                    "file": str(destination.relative_to(output)),
                    "source_path": source_path,
                    "source_class": folder,
                    "expected_screening": expected_screening,
                    "expected_subtype": expected_subtype,
                    "prompt_id": "screening_conservative",
                }
            )

    manifest = {
        "version": 1,
        "repo_id": REPO_ID,
        "revision": REVISION,
        "license": LICENSE,
        "scope": "runtime smoke test and small labeled research evaluation; not clinical validation",
        "cases": cases,
    }
    manifest_path = output / "manifest.json"
    manifest_path.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({"manifest": str(manifest_path), "cases": len(cases), "revision": REVISION}, indent=2))


if __name__ == "__main__":
    main()
