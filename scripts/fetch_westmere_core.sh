#!/usr/bin/env bash
set -euo pipefail
root="${WESTMERE_MODEL_ROOT:-./models}"
agent_file="${AGENT_MODEL_FILE:-Qwen3.5-4B-Q4_K_M.gguf}"
mkdir -p "$root/cpu/agent" "$root/cpu/asr" "$root/cpu/translation" "$root/westmere/radiology"

case "$agent_file" in
  Qwen3.5-4B-Q4_K_M.gguf)
    hf download unsloth/Qwen3.5-4B-GGUF --include "$agent_file" --local-dir "$root/cpu/agent"
    ;;
  Qwen3.5-9B-Q4_K_M.gguf)
    hf download unsloth/Qwen3.5-9B-GGUF --include "$agent_file" --local-dir "$root/cpu/agent"
    ;;
  *)
    printf 'Unsupported AGENT_MODEL_FILE=%s\n' "$agent_file" >&2
    printf 'Supported: Qwen3.5-4B-Q4_K_M.gguf, Qwen3.5-9B-Q4_K_M.gguf\n' >&2
    exit 2
    ;;
esac

hf download Systran/faster-whisper-base --local-dir "$root/cpu/asr"
hf download qvac/TranslatePsy-EuroNano --include "en-xx/Tiny/intgemm/*" "xx-en/Tiny/intgemm/*" --local-dir "$root/cpu/translation"
printf '%s\n' "Place a compatible chest-X-ray ONNX classifier at $root/westmere/radiology/model.onnx"
