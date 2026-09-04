#!/usr/bin/env bash
set -euo pipefail
./scripts/fetch_cpu_specialists.sh
hf download Qwen/Qwen3-0.6B-GGUF --include "Qwen3-0.6B-Q8_0.gguf" --local-dir models/cpu/agent
python scripts/verify_models.py --profile cpu
