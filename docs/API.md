# NDHIS AI Prototype API Guide

## Scope

This document describes the HTTP and WebSocket contracts exposed by the NDHIS AI prototype. The system is intended for local/on-premises clinical-operations prototyping and currently uses synthetic/public/de-identified data paths.

Current public deployment hostname:

```text
https://ai.itsjosiahdavis.dev
```

If a particular deployment exposes only the browser UI through that hostname, the same endpoint paths apply at the reverse-proxy/gateway origin configured by that deployment.

> Important: the current `X-NDHIS-Demo-Key`, `X-NDHIS-User`, and `X-NDHIS-Role` model is a prototype authentication boundary, not production identity. See `SECURITY_AUDIT.md` before integrating real users or patient data.

## Common authenticated headers

All guarded HTTP endpoints require:

```http
X-NDHIS-Demo-Key: <demo-key>
X-NDHIS-User: <user-id-or-name>
X-NDHIS-Role: doctor
```

The gateway currently accepts only the `doctor` role for protected prototype endpoints.

Example shell variables:

```bash
export BASE_URL='https://ai.itsjosiahdavis.dev'
export NDHIS_DEMO_KEY='...'
export NDHIS_USER='doctor-demo'
```

Reusable cURL headers:

```bash
-H "X-NDHIS-Demo-Key: $NDHIS_DEMO_KEY" \
-H "X-NDHIS-User: $NDHIS_USER" \
-H 'X-NDHIS-Role: doctor'
```

The gateway applies per-user request-rate limits and a global concurrency limit configured by the active runtime profile. When the concurrency limit is reached it returns `503` with a short `Retry-After` value.

## Request IDs

Gateway tool/chat requests return:

```http
X-NDHIS-Request-ID: req-...
```

The same request ID appears in gateway audit metadata. Calling systems should preserve it in logs for troubleshooting.

---

# Health and system information

## `GET /api/health`

This endpoint is intentionally unguarded so health monitoring can check the local specialist services.

Example:

```bash
curl -sS "$BASE_URL/api/health"
```

The response includes an overall status and service states for the agent, forecasting, radiology, and translation components.

## `GET /api/system`

Requires the common authenticated headers.

Example:

```bash
curl -sS \
  -H "X-NDHIS-Demo-Key: $NDHIS_DEMO_KEY" \
  -H "X-NDHIS-User: $NDHIS_USER" \
  -H 'X-NDHIS-Role: doctor' \
  "$BASE_URL/api/system"
```

The response reports:

- processing mode (`local`);
- runtime profile;
- specialist service status;
- active model labels; and
- configured request/concurrency/body-size limits.

## `GET /api/audit/recent?limit=N`

Requires authentication. `limit` must be between 1 and 100 and defaults to 12.

The browser-facing audit response intentionally omits user and role identity. It includes operational metadata such as:

- timestamp;
- request ID;
- route;
- tool;
- model;
- status; and
- latency.

---

# Conversational/tool-routing API

## `POST /api/chat`

Request body:

```json
{
  "messages": [
    {"role":"user","content":"Forecast A&E patient volume for the next 7 days"}
  ]
}
```

Example:

```bash
curl -sS \
  -H 'Content-Type: application/json' \
  -H "X-NDHIS-Demo-Key: $NDHIS_DEMO_KEY" \
  -H "X-NDHIS-User: $NDHIS_USER" \
  -H 'X-NDHIS-Role: doctor' \
  -d '{"messages":[{"role":"user","content":"Forecast A&E patient volume for the next 7 days"}]}' \
  "$BASE_URL/api/chat"
```

Typical response shape:

```json
{
  "answer": "...",
  "tool": "forecast_patient_volume",
  "arguments": {
    "facility": "JNF",
    "department": "A&E",
    "horizon": 7,
    "horizon_unit": "days",
    "resolution": "auto",
    "include_actuals": true
  },
  "latency_ms": 240,
  "request_id": "req-...",
  "routing": "deterministic",
  "intent": "...",
  "llm_calls": 0,
  "trace": []
}
```

The gateway may route a request in one of three ways:

- **deterministic** — supported intent is recognized without an LLM call;
- **agent-router** — the local model resolves an ambiguous operational request into one bounded tool call; or
- **assistant** — the local model generates a conversational response without executing a specialist tool.

Supported operational tools are currently:

```text
forecast_patient_volume
forecast_bed_occupancy
forecast_disease_incidence
get_radiology_result
get_service_status
```

Tool arguments are validated against the prototype capability domain before execution.

Current supported facility:

```text
JNF
```

Supported departments:

```text
A&E
Outpatient
Medical Ward
Surgical Ward
Pediatrics
```

Supported disease-incidence categories:

```text
respiratory
gastro
diabetes
hypertension
```

Forecast horizons are limited to approximately two years.

## `POST /api/chat/stream`

Uses the same request body and authentication as `/api/chat` but responds as newline-delimited JSON:

```http
Content-Type: application/x-ndjson
```

Events can include:

```json
{"type":"stage","stage":{"name":"interpret","status":"running"}}
{"type":"delta","delta":"partial response text"}
{"type":"result","response":{"answer":"...","request_id":"req-..."}}
```

or:

```json
{"type":"error","error":"..."}
```

This endpoint is appropriate when the UI should display execution stages and model text as it becomes available.

---

# Forecasting API

## `POST /api/forecast`

The gateway proxies a structured forecast request to the local forecasting service.

Example:

```json
{
  "facility": "JNF",
  "department": "A&E",
  "metric": "patient_arrivals",
  "horizon": 7,
  "horizon_unit": "days",
  "resolution": "auto",
  "include_actuals": true
}
```

Alternative compatibility field:

```json
{"horizon_days":7}
```

`horizon_days` must be between 1 and 730.

Common metrics used by the gateway include:

```text
patient_arrivals
bed_occupancy
disease_incidence
```

Disease-incidence requests may also include:

```json
{"disease":"respiratory"}
```

The prototype forecast data is synthetic. Do not represent these outputs as production JNF predictions unless the data/model pipeline has separately been validated on authorized real operational data.

## `GET /api/forecast/capabilities`

Returns the active forecasting service's supported dimensions/capabilities.

## `POST /api/forecast/history`

Structured historical-data request:

```json
{
  "facility": "JNF",
  "department": "A&E",
  "metric": "patient_arrivals",
  "start": "2026-08-01T00:00:00Z",
  "end": "2026-09-01T00:00:00Z",
  "resolution": "1d"
}
```

This endpoint operates on the prototype's local data source.

---

# Radiology API

## `POST /api/radiology`

Accepts multipart form data and proxies it to the local radiology service.

Required form field:

```text
file=<image>
```

Optional form field:

```text
prompt=<text up to 2000 characters>
```

Example:

```bash
curl -sS \
  -H "X-NDHIS-Demo-Key: $NDHIS_DEMO_KEY" \
  -H "X-NDHIS-User: $NDHIS_USER" \
  -H 'X-NDHIS-Role: doctor' \
  -F 'file=@chest-xray.png' \
  -F 'prompt=Describe the research screening output and uncertainty.' \
  "$BASE_URL/api/radiology"
```

Typical response fields include:

```json
{
  "result_id": "0123456789abcdef",
  "filename": "chest-xray.png",
  "findings": "...",
  "predictions": [],
  "review_required": true,
  "latency_ms": 733,
  "data_mode": "public_or_deidentified_demo",
  "model": "...",
  "backend": "...",
  "device": "cpu"
}
```

Radiology output is explicitly a research/prototype screening aid. `review_required` is always part of the intended clinical-safety boundary. The service is not an autonomous diagnostic system.

Empty/invalid/oversized images are rejected. The active upload limit can be inspected through service/system configuration.

## `GET /api/radiology/{id}`

Returns an in-memory radiology result by result ID when still available.

Example:

```bash
curl -sS \
  -H "X-NDHIS-Demo-Key: $NDHIS_DEMO_KEY" \
  -H "X-NDHIS-User: $NDHIS_USER" \
  -H 'X-NDHIS-Role: doctor' \
  "$BASE_URL/api/radiology/0123456789abcdef"
```

A missing result returns `404`.

---

# Live translation WebSocket

Translation is intentionally a direct WebSocket service because it is latency-sensitive.

Local/default endpoint:

```text
ws://localhost:8101/ws/translate
```

A reverse-proxied deployment should use the deployment-specific secure `wss://` URL.

Prototype authentication/identity is currently supplied as WebSocket query parameters:

```text
?key=<demo-key>&user=<user>&role=doctor
```

After connection, send one text configuration message:

```json
{
  "source": "auto",
  "target": "spa_Latn"
}
```

Then send raw binary PCM audio chunks. The service expects 16-kHz, signed 16-bit mono PCM samples and processes them according to the configured chunk duration.

Example response per processed segment:

```json
{
  "transcript": "Good morning",
  "translation": "Buenos días",
  "source_language": "en",
  "target_language": "spa_Latn",
  "latency_ms": 420
}
```

The active backend reports its supported target languages from its `/health` endpoint.

Translation-session audit records contain metadata such as user, role, target language, segment count, duration, status, and model names. Transcript text is not intentionally written into the translation audit log.

> The query-string demo key/identity scheme is prototype-only. Query strings can be captured by infrastructure logs/history, and the user/role values are self-asserted. Replace this with authenticated session/token claims before production use.

---

# Gateway limits and common errors

The exact numerical values are runtime-profile configuration, available through `/api/system`.

Common responses:

| Status | Meaning |
|---:|---|
| 200 | Successful request/tool result |
| 400 | Invalid body, missing identity headers, invalid arguments |
| 401 | Missing/incorrect demo key |
| 403 | Prototype role is not `doctor` |
| 413 | Body/image exceeds configured limit |
| 429 | Per-user prototype rate limit exceeded |
| 502 | Local model/specialist dependency failed during a routed operation |
| 503 | Gateway concurrency limit reached or specialist model unavailable |

The gateway returns `Cache-Control: no-store` for responses and applies a global maximum request-body size.

## Safety/clinical boundary

The current prototype must not be described as clinically validated or as an autonomous diagnosis/treatment system.

- Forecasting uses synthetic operational data.
- Radiology requires clinician review.
- The assistant must not claim access to production NDHIS records.
- Model/tool changes require re-running evaluation and performance tests.
- Real patient-data integration requires a separate security, privacy, clinical-validation, and governance review.
