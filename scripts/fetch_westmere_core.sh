#!/usr/bin/env bash
set -euo pipefail
root="${WESTMERE_MODEL_ROOT:-./models}"
mkdir -p "$root/cpu/agent" "$root/cpu/asr" "$root/cpu/translation" "$root/westmere/radiology"
hf download Qwen/Qwen3-0.6B-GGUF --include "Qwen3-0.6B-Q8_0.gguf" --local-dir "$root/cpu/agent"
hf download Systran/faster-whisper-base --local-dir "$root/cpu/asr"
hf download qvac/TranslatePsy-EuroNano --include "en-xx/Tiny/intgemm/*" "xx-en/Tiny/intgemm/*" --local-dir "$root/cpu/translation"
printf '%s\n' "Place a compatible chest-X-ray ONNX classifier at $root/westmere/radiology/model.onnx"
