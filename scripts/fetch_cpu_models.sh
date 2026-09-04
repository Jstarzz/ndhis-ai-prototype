#!/usr/bin/env bash
set -euo pipefail
./scripts/fetch_cpu_specialists.sh
hf download Qwen/Qwen3-1.7B --local-dir models/cpu/agent
python scripts/verify_models.py --profile cpu
