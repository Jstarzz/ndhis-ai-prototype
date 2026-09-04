#!/usr/bin/env bash
set -euo pipefail
hf download Systran/faster-whisper-base --local-dir models/cpu/asr
hf download qvac/TranslatePsy-EuroNano --include "en-xx/Tiny/intgemm/*" "xx-en/Tiny/intgemm/*" --local-dir models/cpu/translation
hf download amazon/chronos-2 --local-dir models/cpu/forecast
curl -fL "https://github.com/mlmed/torchxrayvision/releases/download/v1/nih-pc-chex-mimic_ch-google-openi-kaggle-densenet121-d121-tw-lr001-rot45-tr15-sc15-seed0-best.pt" -o models/cpu/radiology/nih-pc-chex-mimic_ch-google-openi-kaggle-densenet121-d121-tw-lr001-rot45-tr15-sc15-seed0-best.pt
