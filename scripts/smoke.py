import argparse
import json
import urllib.request


def request(url, key, method="GET", body=None, auth=False):
    headers = {"Content-Type": "application/json"}
    if auth:
        headers.update({"X-NDHIS-Demo-Key": key, "X-NDHIS-User": "smoke-doctor", "X-NDHIS-Role": "doctor"})
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    with urllib.request.urlopen(req, timeout=120) as response:
        return response.status, json.loads(response.read())


def check(name, fn):
    try:
        status, payload = fn()
        ok = 200 <= status < 300
        print(json.dumps({"check": name, "ok": ok, "status": status, "payload": payload}, default=str))
        return ok
    except Exception as exc:
        print(json.dumps({"check": name, "ok": False, "error": str(exc)}))
        return False


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--gateway", default="http://localhost:8080")
    parser.add_argument("--translation", default="http://localhost:8101")
    parser.add_argument("--key", default="ndhis-local-demo")
    args = parser.parse_args()

    checks = [
        check("gateway_health", lambda: request(f"{args.gateway}/api/health", args.key)),
        check("system", lambda: request(f"{args.gateway}/api/system", args.key, auth=True)),
        check("translation_health", lambda: request(f"{args.translation}/health", args.key)),
        check(
            "forecast",
            lambda: request(
                f"{args.gateway}/api/forecast",
                args.key,
                method="POST",
                body={"facility": "JNF", "department": "A&E", "metric": "patient_arrivals", "horizon_days": 30},
                auth=True,
            ),
        ),
        check(
            "agent_route",
            lambda: request(
                f"{args.gateway}/api/chat",
                args.key,
                method="POST",
                body={"messages": [{"role": "user", "content": "Are the local AI services online?"}]},
                auth=True,
            ),
        ),
    ]
    if not all(checks):
        raise SystemExit(1)


if __name__ == "__main__":
    main()
