.PHONY: verify verify-cpu verify-legacy-cpu preflight preflight-cpu preflight-legacy-cpu cpu-probe fetch-cpu fetch-legacy-cpu generate up up-cpu up-legacy-cpu down down-cpu down-legacy-cpu logs logs-cpu eval eval-cpu eval-legacy-cpu smoke bench bench-cpu bench-legacy-cpu check

verify:
	python scripts/verify_models.py --profile gpu

verify-cpu:
	python scripts/verify_models.py --profile cpu

verify-legacy-cpu:
	python scripts/verify_models.py --profile legacy-cpu

preflight:
	python scripts/preflight.py --profile gpu

preflight-cpu:
	python scripts/preflight.py --profile cpu

preflight-legacy-cpu:
	python scripts/preflight.py --profile legacy-cpu

cpu-probe:
	python scripts/hardware_probe.py

fetch-cpu:
	./scripts/fetch_cpu_models.sh

fetch-legacy-cpu:
	./scripts/fetch_legacy_models.sh

generate:
	python scripts/generate_synthetic_data.py

up:
	docker compose up --build

up-cpu:
	docker compose -f compose.cpu.yaml up --build

up-legacy-cpu:
	docker compose -f compose.cpu.yaml -f compose.legacy-cpu.yaml up --build

down:
	docker compose down

down-cpu:
	docker compose -f compose.cpu.yaml down

down-legacy-cpu:
	docker compose -f compose.cpu.yaml -f compose.legacy-cpu.yaml down

logs:
	docker compose logs -f --tail=200

logs-cpu:
	docker compose -f compose.cpu.yaml logs -f --tail=200

eval:
	python scripts/eval_agent.py

eval-cpu:
	python scripts/eval_agent.py --model ndhis-agent-cpu

eval-legacy-cpu:
	python scripts/eval_agent.py --model ndhis-agent-cpu

smoke:
	python scripts/smoke.py

bench:
	python scripts/bench_suite.py --profile gpu --concurrency 1 2 4 8 16 32 --requests 40

bench-cpu:
	python scripts/bench_suite.py --profile cpu --concurrency 1 2 4 8 --requests 20

bench-legacy-cpu:
	python scripts/bench_suite.py --profile legacy-cpu --concurrency 1 2 4 --requests 20

check:
	python -m compileall -q scripts services
	python scripts/check_comments.py
	python -m unittest discover -s scripts -p 'test_*.py'
	cd services/gateway && go test ./...
	node --check services/qvac-translation/index.mjs
