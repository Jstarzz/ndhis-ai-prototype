#!/usr/bin/env bash
set -euo pipefail
root="${WESTMERE_MODEL_ROOT:-./models}"
agent_file="${AGENT_MODEL_FILE:-Qwen3.5-4B-Q4_K_M.gguf}"
radiology_dir="$root/westmere/radiology"
radiology_source="$radiology_dir/upstream"
radiology_repo="${RADIOLOGY_MODEL_REPO:-a1mohamadd/lung-disease-detection}"
radiology_revision="${RADIOLOGY_MODEL_REVISION:-884a901}"
mkdir -p "$root/cpu/agent" "$root/cpu/asr" "$root/cpu/translation" "$radiology_dir" "$radiology_source"

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
hf download "$radiology_repo" "segmentation/unet_xception/unet_xception-segmentation.onnx" --revision "$radiology_revision" --local-dir "$radiology_source"
hf download "$radiology_repo" "healthy_unhealthy/densenet/densenet-healthy_unhealthy.onnx" --revision "$radiology_revision" --local-dir "$radiology_source"
hf download "$radiology_repo" "diseases/densenet/densenet-diseases.onnx" --revision "$radiology_revision" --local-dir "$radiology_source"
install -m 0644 "$radiology_source/segmentation/unet_xception/unet_xception-segmentation.onnx" "$radiology_dir/lung-segmentation.onnx"
install -m 0644 "$radiology_source/healthy_unhealthy/densenet/densenet-healthy_unhealthy.onnx" "$radiology_dir/healthy-unhealthy-densenet.onnx"
install -m 0644 "$radiology_source/diseases/densenet/densenet-diseases.onnx" "$radiology_dir/disease-densenet.onnx"
printf 'Radiology pipeline installed from %s@%s into %s\n' "$radiology_repo" "$radiology_revision" "$radiology_dir"
