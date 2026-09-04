#!/usr/bin/env bash
set -euo pipefail
hf download Qwen/Qwen3-0.6B-GGUF --include "Qwen3-0.6B-Q8_0.gguf" --local-dir models/cpu/agent
hf download Systran/faster-whisper-base --local-dir models/cpu/asr
hf download qvac/TranslatePsy-EuroNano --include "en-xx/Tiny/intgemm/*" "xx-en/Tiny/intgemm/*" --local-dir models/cpu/translation
printf '%s\n' 'Place a compatible chest-X-ray ONNX classifier at models/westmere/radiology/model.onnx'
