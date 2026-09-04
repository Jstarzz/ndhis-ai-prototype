import argparse
import json
import urllib.request
from pathlib import Path

from bench_vllm import SYSTEM


def load_cases(path: str):
    return [json.loads(line) for line in Path(path).read_text(encoding="utf-8").splitlines() if line.strip()]


def run(url, model, prompt):
    payload = json.dumps(
        {
            "model": model,
            "messages": [
                {"role": "system", "content": SYSTEM},
                {"role": "user", "content": prompt},
            ],
            "temperature": 0,
            "max_tokens": 160,
            "response_format": {"type": "json_object"},
        }
    ).encode()
    request = urllib.request.Request(url, data=payload, headers={"Content-Type": "application/json"}, method="POST")
    with urllib.request.urlopen(request, timeout=90) as response:
        body = json.loads(response.read())
    return json.loads(body["choices"][0]["message"]["content"])


def normalized(value):
    return value.casefold() if isinstance(value, str) else value


def argument_score(actual: dict, expected: dict):
    if not expected:
        return 1.0
    matched = sum(normalized(actual.get(key)) == normalized(value) for key, value in expected.items())
    return matched / len(expected)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--url", default="http://localhost:8000/v1/chat/completions")
    parser.add_argument("--model", default="ndhis-agent")
    parser.add_argument("--cases", default="data/evals/tool_routing.jsonl")
    args = parser.parse_args()

    cases = load_cases(args.cases)
    selection_passed = 0
    argument_total = 0.0
    argument_cases = 0
    for case in cases:
        prompt = case["prompt"]
        expected_tool = case.get("tool")
        expected_args = case.get("arguments", {})
        try:
            decision = run(args.url, args.model, prompt)
            actual_tool = decision.get("name") if decision.get("type") == "tool" else None
            selection_ok = actual_tool == expected_tool
            args_score = 1.0 if expected_tool is None else argument_score(decision.get("arguments") or {}, expected_args)
        except Exception as exc:
            decision = {"error": str(exc)}
            actual_tool = "error"
            selection_ok = False
            args_score = 0.0
        selection_passed += int(selection_ok)
        if expected_tool is not None:
            argument_total += args_score
            argument_cases += 1
        print(json.dumps({"selection_ok": selection_ok, "argument_score": round(args_score, 4), "expected": expected_tool, "actual": actual_tool, "prompt": prompt, "decision": decision}))

    print(
        json.dumps(
            {
                "cases": len(cases),
                "tool_selection_accuracy": round(selection_passed / len(cases), 4),
                "argument_accuracy": round(argument_total / argument_cases, 4) if argument_cases else 1.0,
            }
        )
    )


if __name__ == "__main__":
    main()
