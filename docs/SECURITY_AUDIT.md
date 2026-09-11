# NDHIS AI Prototype Internal Security Audit

## Scope and status

This is an internal engineering security review of the current NDHIS AI prototype at repository/deployment commit `87e1a54841c2a815967291d352afc7615c23f882`.

It is **not** a penetration test, regulatory certification, HIPAA determination, clinical-safety certification, or legal opinion.

The review covers:

- gateway authentication/authorization;
- browser/API trust boundaries;
- tool routing and validation;
- request limits/rate limits;
- local model/service isolation;
- audit behavior;
- radiology/forecast/translation data handling;
- Docker/profile security choices; and
- single-node operational risk.

## Executive summary

The prototype makes several strong architectural choices: inference is local, runtime model downloads are disabled, requests and tool arguments are bounded, tool execution is allowlisted, concurrency is explicitly limited, doctor-only access is checked, request IDs/audit traces exist, browser-facing audit output redacts identity, forecasting uses synthetic data, radiology is explicitly clinician-review-required, and the assistant is instructed not to claim access to production patient records.

However, the current authentication model is intentionally **prototype-grade** and must not be mistaken for production clinical identity. The most important issue is that possession of the demo key plus self-supplied `X-NDHIS-User` / `X-NDHIS-Role: doctor` headers is sufficient to pass the gateway. The browser build can also contain the demo API key. Therefore a user who can obtain the prototype key can assert an arbitrary doctor identity. This is the primary blocker to using the current gateway as a production authorization boundary.

Other significant gaps are permissive CORS, in-memory rate limiting keyed by self-asserted user identity, best-effort local-file auditing rather than durable/tamper-evident audit storage, query-string credentials on the translation WebSocket, mutable image tags/profile privileges, and single-container/host availability limitations.

## Risk summary

| ID | Finding | Severity | Production blocker? |
|---|---|---|---|
| SEC-01 | Self-asserted user/role headers | High | Yes |
| SEC-02 | Demo API key can be embedded in browser client | High | Yes |
| SEC-03 | Translation key/identity in WebSocket query string | High | Yes |
| SEC-04 | Gateway CORS allows any origin | Medium | Yes for browser production |
| SEC-05 | Rate limits are in-memory and keyed by self-asserted user | Medium | Yes for broad deployment |
| SEC-06 | Audit logging is best-effort local JSONL | Medium/High | Yes for regulated audit requirements |
| SEC-07 | Mutable images / privileged standard CPU profile settings | Medium | No, but should be fixed |
| SEC-08 | Internal service traffic has no independent service authentication/TLS | Medium | Depends on topology |
| SEC-09 | Single-node legacy CPU deployment | Medium operational | No for demo; yes for HA requirement |
| SEC-10 | Clinical models/data are not clinically validated | High clinical risk | Yes for clinical decision use |

---

## Existing controls

### Local processing and model isolation

The system is designed to run model inference locally. Compose profiles set Hugging Face/Transformers offline modes where applicable, and missing model files are treated as startup failures rather than silently downloading/falling back.

Assessment: **Strong privacy/supply-boundary design for the prototype**.

### Gateway tool allowlist

The local agent cannot invoke arbitrary functions. The gateway restricts tool execution to a fixed allowlist:

```text
forecast_patient_volume
forecast_bed_occupancy
forecast_disease_incidence
get_radiology_result
get_service_status
```

Arguments are canonicalized and validated against supported facilities, departments, diseases, horizons, resolution formats, and bounded result IDs before specialist execution.

Assessment: **Strong**. This materially reduces prompt-to-arbitrary-action risk.

### Request and resource bounds

The gateway enforces:

- maximum request-body size;
- maximum chat message count;
- maximum message length;
- bounded model/tool response sizes;
- request-per-minute controls; and
- a global concurrent-request semaphore.

Radiology independently limits image size and prompt length. Translation has a maximum session duration.

Assessment: **Good prototype denial-of-service guardrails**.

### Request traceability

Gateway operations generate `X-NDHIS-Request-ID` values and audit records with route/tool/model/status/latency metadata.

The browser-facing recent-audit endpoint returns a redacted event shape without user/role fields.

Assessment: **Good observability pattern, but durability is insufficient for production audit requirements; see SEC-06**.

### Clinical boundary controls

The agent system prompt explicitly prohibits inventing production patient data and requires clinician deference for diagnosis/treatment. Radiology outputs mark `review_required: true` and identify themselves as research screening output. Forecasting is based on synthetic hospital operational data in the current prototype.

Assessment: **Appropriate prototype boundary**, but wording/prompts are not a substitute for clinical validation or production policy enforcement.

---

## Findings

### SEC-01 — User identity and role are self-asserted HTTP headers

**Severity: High**

Protected gateway routes authenticate the demo key, then read:

```text
X-NDHIS-User
X-NDHIS-Role
```

The only role authorization check is whether the supplied role equals `doctor` (case-insensitive). These headers are not cryptographically bound to an authenticated NDHIS identity.

Impact:

Anyone who obtains a valid demo key can send:

```http
X-NDHIS-User: arbitrary-name
X-NDHIS-Role: doctor
```

and pass the current prototype identity check. Audit records would then attribute the action to the self-asserted identity.

Recommendation before production:

- authenticate users through an authoritative NDHIS/organizational identity provider;
- accept identity/role only from verified signed token claims, a trusted authenticated reverse proxy, or mTLS-backed machine/user identity;
- reject direct client-supplied identity headers at the application boundary;
- map roles server-side from authoritative claims/directories;
- include issuer/audience/expiry validation and revocation/session policy;
- log stable authenticated subject IDs, not display names supplied by the client.

This is the most important production security change.

### SEC-02 — Browser-delivered demo API key is not a secret

**Severity: High**

The frontend build supports `VITE_DEMO_API_KEY`. Any value compiled into browser JavaScript is recoverable by the browser user and must not be treated as a confidential API secret.

Impact:

If that key is considered the primary authentication factor, every authorized browser user effectively receives a reusable shared credential. Combined with SEC-01, possession of the key permits arbitrary self-asserted doctor identity.

Recommendation:

- remove shared secret authentication from the production browser architecture;
- use normal authenticated web sessions/OIDC/JWTs issued per user;
- do not compile organization/API secrets into Vite/frontend environment variables;
- reserve service API keys for trusted server-to-server callers where secrets can actually remain secret.

### SEC-03 — Translation WebSocket credentials are carried in query parameters

**Severity: High**

The translation WebSocket currently authenticates using query parameters:

```text
?key=<demo-key>&user=<user>&role=doctor
```

Query strings are commonly captured by browser history, reverse proxies, observability tools, error reports, and access logs. The user/role values are also self-asserted.

Recommendation:

- replace query-string secrets with an authenticated session/token mechanism;
- if browser WebSocket limitations require a handshake token, issue a short-lived single-purpose token from an authenticated HTTP endpoint and validate it server-side;
- bind user/role to that token rather than accepting query assertions;
- ensure logs never record the token;
- expire/revoke tokens aggressively.

### SEC-04 — CORS permits every origin

**Severity: Medium**

The gateway currently returns:

```http
Access-Control-Allow-Origin: *
```

This is acceptable for a disposable demo key/prototype but is inappropriate as part of a browser production authorization boundary.

Recommendation:

- configure an explicit allowed-origin list for deployed NDHIS UI origins;
- emit CORS headers only for approved origins;
- keep server-to-server API access independent of browser CORS;
- test preflight and rejected-origin behavior.

### SEC-05 — Rate limiting is local, resettable, and based on client-supplied identity

**Severity: Medium**

The gateway keeps a per-user fixed-window map in process memory. Limits reset on process restart and do not coordinate across replicas. Because `X-NDHIS-User` is self-supplied, a caller with the demo key can rotate user strings to avoid the per-user limit.

Recommendation:

- first fix authoritative identity (SEC-01);
- apply shared/distributed or trusted-edge rate controls for production;
- rate-limit by authenticated subject plus source/device/session where appropriate;
- retain the local concurrency semaphore as a final overload guard;
- instrument rate-limit/queue/concurrency rejection metrics.

### SEC-06 — Gateway audit writes are best-effort local JSONL

**Severity: Medium/High**

The gateway appends audit events to a local file with mode `0600`, which is a useful prototype mechanism. However, failures opening/writing the audit file are logged and the user operation continues.

The design therefore does not provide fail-closed audit durability, external tamper evidence, replicated retention, or guaranteed delivery.

Recommendation for production/regulated use:

- send audit events to a durable append-oriented sink separate from ordinary application storage;
- define what operations must fail closed if audit persistence is unavailable;
- make request IDs globally traceable;
- protect audit data from ordinary application-user modification;
- define retention/destruction policy;
- monitor audit ingestion failures;
- test recovery/backlog behavior;
- avoid recording prompt/transcript/patient content unless explicitly required and governed.

Translation currently audits metadata and not transcript text; preserve that data-minimization behavior unless requirements explicitly change.

### SEC-07 — Runtime image reproducibility/privileges need hardening

**Severity: Medium**

Some Compose profiles use mutable image tags such as `latest`. The standard CPU vLLM profile also uses host IPC, `seccomp=unconfined`, and `SYS_NICE` to satisfy runtime/performance needs.

The deployed Westmere llama.cpp profile is different and more constrained, but production documentation should not assume every supported profile has the same container security posture.

Recommendation:

- pin production images by digest;
- document every elevated runtime requirement and remove any not required by the selected profile;
- use non-root users, dropped capabilities, read-only filesystems, and restricted mounts where supported;
- scan images/dependencies in CI;
- separate model/data directories read-only from writable audit/runtime state.

### SEC-08 — Internal specialist service traffic relies on local network trust

**Severity: Medium if moved beyond one host**

The gateway talks to local agent/forecast/radiology/translation services over internal HTTP/WebSocket addresses. Independent service authentication/TLS is not the primary protection in the current single-host Compose topology.

Recommendation:

- keep specialist ports bound to loopback/private container networks in the prototype;
- never publish raw model/specialist ports to untrusted networks;
- if services move across hosts, introduce service authentication and encrypted transport (mTLS/service mesh/signed tokens as appropriate);
- apply network ACLs so only the gateway can reach specialist APIs where possible.

### SEC-09 — Single-node legacy deployment is a single availability/failure domain

**Severity: Medium operational risk**

The current instance is an unprivileged LXC on one Proxmox host using eight Westmere cores and 16 GiB RAM. An active generative request can saturate all eight cores. The model runtime uses `parallel 1`, and the gateway intentionally caps concurrency.

Recommendation:

- treat current server as prototype/pilot capacity;
- monitor CPU/RAM/latency and reject overload predictably;
- keep backup copies of configuration/models/evaluation artifacts;
- define recovery procedures for the container/host;
- for broad clinical availability, deploy modern redundant inference capacity and benchmark the actual mixed workload.

### SEC-10 — Clinical model validity is not established by software correctness

**Severity: High clinical risk**

Radiology, forecasting, translation, and conversational assistance can all produce plausible but incorrect output. Current evaluations demonstrate software/model behavior on a prototype data/model set, not clinical efficacy for a real patient population.

Recommendation before clinical decision use:

- define intended use for each feature;
- perform task-specific clinical validation with appropriate experts/data;
- validate subgroup/error behavior and thresholds;
- establish human-review workflows;
- provide model/version provenance;
- monitor post-deployment performance/drift;
- prevent autonomous diagnosis/treatment write-back;
- obtain required governance/regulatory approvals.

---

## Additional observations

### Data minimization

The current architecture's use of synthetic operational data and public/de-identified radiology images significantly reduces prototype privacy risk. Preserve this separation until a formal production data-integration plan is approved.

### Prompt injection/tool abuse

The agent's tool surface is relatively well constrained because tool names and arguments are checked server-side and the model is not given arbitrary shell/database primitives. This should remain a design invariant. Do not add generic SQL, filesystem, shell, HTTP-fetch, or administrative tools to the clinical-facing agent without a separate threat model and strong authorization.

### Model replacement

Changing model weights, prompt templates, quantization, routing logic, thresholds, or tool schemas can change safety behavior without changing HTTP contracts. Treat these as release-significant changes and re-run evaluation/performance/security checks.

## Production security gate

Before real NDHIS users/patient data are allowed, require at minimum:

```text
[ ] authoritative SSO/session identity replaces X-NDHIS-User/Role assertions
[ ] browser contains no shared secret/demo API key
[ ] translation no longer passes long-lived secret in query string
[ ] CORS restricted to approved UI origins
[ ] distributed/edge rate limiting uses authenticated identity
[ ] specialist services are not exposed to untrusted networks
[ ] production images are pinned/reviewed
[ ] audit sink is durable, monitored, access-controlled, and retention-defined
[ ] backups/recovery are tested
[ ] secrets are stored outside source control with rotation/revocation procedures
[ ] real-data minimization/authorization model is approved
[ ] model/task clinical validation completed for intended uses
[ ] clinician review remains mandatory where required
[ ] incident response and access-review procedures exist
[ ] vendor/hosting agreements and regulatory/privacy obligations are reviewed
```

## Residual risk statement

The current system is appropriate as a technically serious local prototype and architecture demonstrator under controlled access with synthetic/public/de-identified data. Its tool isolation and local-processing design are strong foundations. It should **not** be promoted to a production clinical authorization boundary until SEC-01 through SEC-06 are addressed and the clinical/governance requirements in SEC-10 are independently satisfied.
