import argparse
import hashlib
import json
import re
import shutil
import zipfile
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
IMAGE_SUFFIXES = {".png", ".jpg", ".jpeg"}


def normalize_segment(value):
    return re.sub(r"[^a-z0-9]+", "_", value.lower()).strip("_")


def class_candidates(paths, folder):
    target = normalize_segment(folder)
    candidates = []
    for path in paths:
        if Path(path).suffix.lower() not in IMAGE_SUFFIXES:
            continue
        parts = [normalize_segment(part) for part in path.replace("\\", "/").split("/") if part]
        if target not in parts:
            continue
        class_index = parts.index(target)
        if "images" not in parts[class_index + 1 :]:
            continue
        candidates.append(path)
    return sorted(candidates)


def choose_archive(files):
    zips = [path for path in files if Path(path).suffix.lower() == ".zip"]
    preferred = [path for path in zips if "radiograph" in path.lower() or "covid" in path.lower()]
    candidates = preferred or zips
    return sorted(candidates)[0] if candidates else ""


def file_sha256(path):
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def build_direct_cases(files, output, count):
    cases = []
    for key, folder, expected_screening, expected_subtype in CLASSES:
        candidates = class_candidates(files, folder)
        if len(candidates) < count:
            return None
        class_dir = output / key
        class_dir.mkdir(parents=True, exist_ok=True)
        for index, source_path in enumerate(candidates[:count], 1):
            cached = Path(hf_hub_download(repo_id=REPO_ID, filename=source_path, repo_type="dataset", revision=REVISION))
            destination = class_dir / cached.name
            shutil.copy2(cached, destination)
            cases.append(make_case(key, index, destination, output, source_path, folder, expected_screening, expected_subtype, "files"))
    return cases


def build_archive_cases(files, output, count):
    archive_path = choose_archive(files)
    if not archive_path:
        raise SystemExit("dataset revision has no directly listed images and no ZIP archive")
    cached_archive = Path(hf_hub_download(repo_id=REPO_ID, filename=archive_path, repo_type="dataset", revision=REVISION))
    cases = []
    with zipfile.ZipFile(cached_archive) as archive:
        members = [info.filename for info in archive.infolist() if not info.is_dir()]
        for key, folder, expected_screening, expected_subtype in CLASSES:
            candidates = class_candidates(members, folder)
            if len(candidates) < count:
                raise SystemExit(f"only {len(candidates)} images found for {folder} inside {archive_path}")
            class_dir = output / key
            class_dir.mkdir(parents=True, exist_ok=True)
            for index, member in enumerate(candidates[:count], 1):
                destination = class_dir / Path(member).name
                with archive.open(member) as source, destination.open("wb") as target:
                    shutil.copyfileobj(source, target)
                source_path = f"{archive_path}::{member}"
                cases.append(make_case(key, index, destination, output, source_path, folder, expected_screening, expected_subtype, "zip"))
    return cases


def make_case(key, index, destination, output, source_path, source_class, expected_screening, expected_subtype, source_mode):
    return {
        "id": f"{key}-{index:02d}",
        "file": str(destination.relative_to(output)),
        "source_path": source_path,
        "source_class": source_class,
        "source_mode": source_mode,
        "sha256": file_sha256(destination),
        "expected_screening": expected_screening,
        "expected_subtype": expected_subtype,
        "prompt_id": "screening_conservative",
    }


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
    cases = build_direct_cases(files, output, args.count_per_class)
    source_mode = "files"
    if cases is None:
        cases = build_archive_cases(files, output, args.count_per_class)
        source_mode = "zip"

    manifest = {
        "version": 2,
        "repo_id": REPO_ID,
        "revision": REVISION,
        "license": LICENSE,
        "source_mode": source_mode,
        "scope": "runtime smoke test and small labeled research evaluation; not clinical validation",
        "cases": cases,
    }
    manifest_path = output / "manifest.json"
    manifest_path.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({"manifest": str(manifest_path), "cases": len(cases), "revision": REVISION, "source_mode": source_mode}, indent=2))


if __name__ == "__main__":
    main()
