# Radiology evaluation pack

This evaluation pack is designed to answer two different questions:

1. Does the local three-stage ONNX pipeline run deterministically and preserve its inference contract?
2. On a small labeled public sample, how often does the configured screening threshold agree with the source label?

It is not a clinical validation study and must not be presented as one.

## Source

The fetcher uses `umairinayat/fyp-dataset`, pinned to revision `640d49fc9c51643b4e8c83657c395d25889c8627`. The mirrored COVID-19 Radiography Database contains four classes that match the current subtype scope:

- Normal
- COVID
- Viral Pneumonia
- Lung Opacity

The source dataset is licensed CC BY-NC-SA 4.0. The downloaded images stay under `data/evals/radiology/` and are not committed to this repository.

Training-data overlap with the current research model has not been ruled out. Therefore this pack is suitable for runtime verification, threshold behavior, latency measurement, prompt-invariance checks and a small labeled smoke test. It is not evidence of out-of-distribution generalization or deployment-level medical accuracy.

## Build the local sample

```bash
python scripts/fetch_radiology_eval.py --count-per-class 5
```

The default produces 20 images, five per class, selected deterministically by lexicographic source path from the pinned dataset revision. The generated manifest records the source path, expected screening class, expected subtype and prompt identifier.

## Run the evaluation

```bash
python scripts/eval_radiology.py \
  --base http://127.0.0.1:8080 \
  --key ndhis-local-demo \
  --manifest data/evals/radiology/manifest.json
```

To verify that review-instruction wording does not change ONNX scores:

```bash
python scripts/eval_radiology.py \
  --base http://127.0.0.1:8080 \
  --key ndhis-local-demo \
  --manifest data/evals/radiology/manifest.json \
  --prompt-all
```

The evaluator reports screening accuracy on the small sample, abnormal sensitivity, normal specificity, subtype accuracy among cases where the screening threshold triggers subtype inference, median/p95 latency, failures and maximum prompt-induced unhealthy-score drift.

A below-threshold output is not equivalent to a clinically normal radiograph. The current model has a narrow research label scope and all outputs require qualified clinician review.
