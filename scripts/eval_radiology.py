import argparse
import json
import mimetypes
import statistics
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path


def percentile(values, fraction):
    if not values:
        return None
    ordered = sorted(values)
    index = min(len(ordered) - 1, max(0, int(round((len(ordered) - 1) * fraction))))
    return ordered[index]


def multipart_payload(file_path, prompt):
    boundary = "----ndhis-" + uuid.uuid4().hex
    mime = mimetypes.guess_type(file_path.name)[0] or "application/octet-stream"
    chunks = [
        f"--{boundary}\r\nContent-Disposition: form-data; name=\"prompt\"\r\n\r\n{prompt}\r\n".encode(),
        f"--{boundary}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"{file_path.name}\"\r\nContent-Type: {mime}\r\n\r\n".encode(),
        file_path.read_bytes(),
        f"\r\n--{boundary}--\r\n".encode(),
    ]
    return b"".join(chunks), f"multipart/form-data; boundary={boundary}"


def run_case(base, key, user, role, file_path, prompt, timeout):
    payload, content_type = multipart_payload(file_path, prompt)
    request = urllib.request.Request(base.rstrip("/") + "/api/radiology", data=payload, method="POST")
    request.add_header("Content-Type", content_type)
    request.add_header("X-NDHIS-Demo-Key", key)
    request.add_header("X-NDHIS-User", user)
    request.add_header("X-NDHIS-Role", role)
    started = time.perf_counter()
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            body = response.read()
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")
        error = RuntimeError(f"HTTP {exc.code}: {detail}")
        error.http_status = exc.code
        error.retry_after = exc.headers.get("Retry-After")
        raise error from exc
    wall_ms = (time.perf_counter() - started) * 1000
    result = json.loads(body)
    result["wall_ms"] = wall_ms
    return result


def run_with_retry(base, key, user, role, file_path, prompt, timeout, retry_429, pace_seconds):
    attempts = 0
    while True:
        try:
            return run_case(base, key, user, role, file_path, prompt, timeout)
        except RuntimeError as exc:
            if getattr(exc, "http_status", None) != 429 or attempts >= retry_429:
                raise
            attempts += 1
            retry_after = getattr(exc, "retry_after", None)
            try:
                wait = float(retry_after) if retry_after else 0.0
            except ValueError:
                wait = 0.0
            time.sleep(max(wait, pace_seconds, 2.5))


def prediction_score(result, label):
    for item in result.get("predictions") or []:
        if str(item.get("label", "")).lower() == label.lower():
            return float(item.get("score", 0))
    return None


def disease_scores(result):
    values = []
    for item in result.get("predictions") or []:
        if item.get("stage") == "disease":
            values.append((str(item.get("label", "")), float(item.get("score", 0))))
    return sorted(values, key=lambda item: item[1], reverse=True)


def threshold_metrics(samples, threshold):
    correct = 0
    abnormal_total = 0
    abnormal_correct = 0
    normal_total = 0
    normal_correct = 0
    for expected, unhealthy in samples:
        predicted = "unhealthy" if unhealthy >= threshold else "healthy"
        if predicted == expected:
            correct += 1
        if expected == "unhealthy":
            abnormal_total += 1
            if predicted == "unhealthy":
                abnormal_correct += 1
        else:
            normal_total += 1
            if predicted == "healthy":
                normal_correct += 1
    accuracy = correct / len(samples) if samples else None
    sensitivity = abnormal_correct / abnormal_total if abnormal_total else None
    specificity = normal_correct / normal_total if normal_total else None
    balanced = None
    if sensitivity is not None and specificity is not None:
        balanced = (sensitivity + specificity) / 2
    return {
        "threshold": threshold,
        "accuracy": accuracy,
        "abnormal_sensitivity": sensitivity,
        "normal_specificity": specificity,
        "balanced_accuracy": balanced,
    }


def build_threshold_sweep(samples, configured_threshold):
    thresholds = {0.50, 0.60, 0.70, 0.75, 0.80, 0.85, 0.90, 0.91, 0.92, 0.95, 0.97, round(configured_threshold, 6)}
    rows = [threshold_metrics(samples, threshold) for threshold in sorted(thresholds)]
    eligible = [row for row in rows if row["balanced_accuracy"] is not None]
    best = max(eligible, key=lambda row: (row["balanced_accuracy"], row["abnormal_sensitivity"], row["normal_specificity"])) if eligible else None
    return rows, best


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base", default="http://127.0.0.1:8080")
    parser.add_argument("--key", default="ndhis-local-demo")
    parser.add_argument("--user", default="bench-doctor")
    parser.add_argument("--role", default="doctor")
    parser.add_argument("--manifest", default="data/evals/radiology/manifest.json")
    parser.add_argument("--prompts", default="evals/radiology/prompts.json")
    parser.add_argument("--prompt-id", default="screening_conservative")
    parser.add_argument("--prompt-all", action="store_true")
    parser.add_argument("--pace-seconds", type=float, default=None)
    parser.add_argument("--retry-429", type=int, default=2)
    parser.add_argument("--timeout", type=float, default=30)
    parser.add_argument("--output", default="")
    args = parser.parse_args()
    if args.retry_429 < 0 or args.retry_429 > 10:
        raise SystemExit("retry-429 must be between 0 and 10")
    if args.pace_seconds is not None and args.pace_seconds < 0:
        raise SystemExit("pace-seconds must be non-negative")

    manifest_path = Path(args.manifest)
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    prompt_map = json.loads(Path(args.prompts).read_text(encoding="utf-8"))
    if args.prompt_id not in prompt_map:
        raise SystemExit(f"unknown prompt id: {args.prompt_id}")
    prompt_ids = list(prompt_map) if args.prompt_all else [args.prompt_id]
    root = manifest_path.parent
    pace_seconds = args.pace_seconds if args.pace_seconds is not None else (2.25 if args.prompt_all else 0.0)
    evaluation_user = args.user + "-prompt-all" if args.prompt_all else args.user

    records = []
    latencies = []
    screening_correct = 0
    screening_count = 0
    abnormal_correct = 0
    abnormal_count = 0
    normal_correct = 0
    normal_count = 0
    subtype_correct = 0
    subtype_count = 0
    subtype_not_triggered = 0
    failures = 0
    prompt_variant_failures = 0
    prompt_drift = 0.0
    prompt_invariance_complete_cases = 0
    configured_threshold = None
    threshold_samples = []
    first_request = True

    for case in manifest.get("cases", []):
        file_path = root / case["file"]
        prompt_results = []
        prompt_errors = []
        for prompt_id in prompt_ids:
            if not first_request and pace_seconds > 0:
                time.sleep(pace_seconds)
            first_request = False
            try:
                result = run_with_retry(args.base, args.key, evaluation_user, args.role, file_path, prompt_map[prompt_id], args.timeout, args.retry_429, pace_seconds)
                prompt_results.append((prompt_id, result))
            except Exception as exc:
                prompt_variant_failures += 1
                prompt_errors.append({"prompt_id": prompt_id, "error": str(exc)})

        primary = next((result for prompt_id, result in prompt_results if prompt_id == args.prompt_id), None)
        if primary is None and prompt_results:
            primary = prompt_results[0][1]
        if primary is None:
            failures += 1
            records.append({"id": case["id"], "error": "no successful prompt variants", "prompt_errors": prompt_errors})
            continue

        unhealthy = prediction_score(primary, "Unhealthy screening")
        healthy = prediction_score(primary, "Healthy screening")
        if unhealthy is None or healthy is None:
            failures += 1
            records.append({"id": case["id"], "error": "missing screening scores", "prompt_errors": prompt_errors})
            continue
        threshold = float((primary.get("pipeline_details") or {}).get("screening_threshold") or 0.91)
        configured_threshold = threshold if configured_threshold is None else configured_threshold
        predicted_screening = "unhealthy" if unhealthy >= threshold else "healthy"
        expected_screening = case["expected_screening"]
        threshold_samples.append((expected_screening, unhealthy))
        screening_count += 1
        if predicted_screening == expected_screening:
            screening_correct += 1
        if expected_screening == "unhealthy":
            abnormal_count += 1
            if predicted_screening == "unhealthy":
                abnormal_correct += 1
        else:
            normal_count += 1
            if predicted_screening == "healthy":
                normal_correct += 1

        subtype = disease_scores(primary)
        expected_subtype = case.get("expected_subtype")
        predicted_subtype = subtype[0][0] if subtype else None
        if expected_subtype:
            if subtype:
                subtype_count += 1
                if predicted_subtype.lower() == expected_subtype.lower():
                    subtype_correct += 1
            else:
                subtype_not_triggered += 1

        wall_ms = float(primary.get("wall_ms", 0))
        model_ms = float(primary.get("latency_ms", 0))
        latencies.append(model_ms or wall_ms)

        scores = [prediction_score(result, "Unhealthy screening") for _, result in prompt_results]
        scores = [score for score in scores if score is not None]
        if len(scores) > 1:
            prompt_drift = max(prompt_drift, max(scores) - min(scores))
        if args.prompt_all and len(prompt_results) == len(prompt_ids):
            prompt_invariance_complete_cases += 1

        records.append(
            {
                "id": case["id"],
                "source_class": case["source_class"],
                "expected_screening": expected_screening,
                "predicted_screening": predicted_screening,
                "healthy_score": healthy,
                "unhealthy_score": unhealthy,
                "threshold": threshold,
                "expected_subtype": expected_subtype,
                "predicted_subtype": predicted_subtype,
                "model_latency_ms": model_ms,
                "wall_ms": wall_ms,
                "prompt_errors": prompt_errors,
            }
        )

    threshold_sweep = []
    exploratory_best = None
    if configured_threshold is not None and threshold_samples:
        threshold_sweep, exploratory_best = build_threshold_sweep(threshold_samples, configured_threshold)

    summary = {
        "cases": len(manifest.get("cases", [])),
        "successful_cases": screening_count,
        "failures": failures,
        "prompt_variant_failures": prompt_variant_failures,
        "screening_accuracy": screening_correct / screening_count if screening_count else None,
        "abnormal_sensitivity": abnormal_correct / abnormal_count if abnormal_count else None,
        "normal_specificity": normal_correct / normal_count if normal_count else None,
        "subtype_accuracy_when_triggered": subtype_correct / subtype_count if subtype_count else None,
        "subtype_cases_triggered": subtype_count,
        "subtype_cases_not_triggered": subtype_not_triggered,
        "median_model_latency_ms": statistics.median(latencies) if latencies else None,
        "p95_model_latency_ms": percentile(latencies, 0.95),
        "configured_screening_threshold": configured_threshold,
        "threshold_sweep": threshold_sweep,
        "exploratory_best_balanced_threshold": exploratory_best,
        "threshold_sweep_scope": "descriptive smoke-evaluation only; do not tune the production threshold from this small potentially overlapping sample",
        "prompt_invariance_max_unhealthy_score_delta": prompt_drift if args.prompt_all else None,
        "prompt_invariance_complete_cases": prompt_invariance_complete_cases if args.prompt_all else None,
        "prompt_invariance_expected_cases": len(manifest.get("cases", [])) if args.prompt_all else None,
        "evaluation_user": evaluation_user,
        "pace_seconds": pace_seconds,
        "scope": manifest.get("scope"),
        "source_revision": manifest.get("revision"),
        "source_mode": manifest.get("source_mode"),
    }
    output = {"summary": summary, "records": records}
    encoded = json.dumps(output, indent=2)
    print(encoded)
    if args.output:
        Path(args.output).parent.mkdir(parents=True, exist_ok=True)
        Path(args.output).write_text(encoded + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
