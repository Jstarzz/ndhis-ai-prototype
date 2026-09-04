.PHONY: verify verify-cpu verify-legacy-cpu verify-westmere preflight preflight-cpu preflight-legacy-cpu preflight-westmere cpu-probe fetch-cpu fetch-legacy-cpu fetch-westmere-core generate up up-cpu up-legacy-cpu up-westmere down down-cpu down-legacy-cpu down-westmere logs logs-cpu logs-westmere eval eval-cpu eval-legacy-cpu eval-westmere smoke bench bench-cpu bench-legacy-cpu bench-westmere check

verify:
	python scripts/verify_models.py --profile gpu

verify-cpu:
	python scripts/verify_models.py --profile cpu

verify-legacy-cpu:
	python scripts/verify_models.py --profile legacy-cpu

verify-westmere:
	set -a; . ./.env; set +a; python scripts/verify_models.py --profile westmere

preflight:
	python scripts/preflight.py --profile gpu

preflight-cpu:
	python scripts/preflight.py --profile cpu

preflight-legacy-cpu:
	python scripts/preflight.py --profile legacy-cpu

preflight-westmere:
	set -a; . ./.env; set +a; python scripts/preflight.py --profile westmere

cpu-probe:
	python scripts/hardware_probe.py

fetch-cpu:
	./scripts/fetch_cpu_models.sh

fetch-legacy-cpu:
	./scripts/fetch_legacy_models.sh

fetch-westmere-core:
	set -a; . ./.env; set +a; bash ./scripts/fetch_westmere_core.sh

generate:
	python scripts/generate_synthetic_data.py

up:
	docker compose up --build

up-cpu:
	docker compose -f compose.cpu.yaml up --build

up-legacy-cpu:
	docker compose -f compose.cpu.yaml -f compose.legacy-cpu.yaml up --build

up-westmere:
	docker compose -f compose.cpu.yaml -f compose.westmere.yaml up --build

down:
	docker compose down

down-cpu:
	docker compose -f compose.cpu.yaml down

down-legacy-cpu:
	docker compose -f compose.cpu.yaml -f compose.legacy-cpu.yaml down

down-westmere:
	docker compose -f compose.cpu.yaml -f compose.westmere.yaml down

logs:
	docker compose logs -f --tail=200

logs-cpu:
	docker compose -f compose.cpu.yaml logs -f --tail=200

logs-westmere:
	docker compose -f compose.cpu.yaml -f compose.westmere.yaml logs -f --tail=200

eval:
	python scripts/eval_agent.py

eval-cpu:
	python scripts/eval_agent.py --model ndhis-agent-cpu

eval-legacy-cpu:
	python scripts/eval_agent.py --model ndhis-agent-cpu

eval-westmere:
	python scripts/eval_agent.py --model ndhis-agent-westmere

smoke:
	python scripts/smoke.py

bench:
	python scripts/bench_suite.py --profile gpu --concurrency 1 2 4 8 16 32 --requests 40

bench-cpu:
	python scripts/bench_suite.py --profile cpu --concurrency 1 2 4 8 --requests 20

bench-legacy-cpu:
	python scripts/bench_suite.py --profile legacy-cpu --concurrency 1 2 4 --requests 20

bench-westmere:
	python scripts/bench_suite.py --profile westmere --concurrency 1 2 --requests 12

check:
	python -m compileall -q scripts services
	python scripts/check_comments.py
	python -m unittest discover -s scripts -p 'test_*.py'
	cd services/gateway && go test ./...
	node --check services/qvac-translation/index.mjs
