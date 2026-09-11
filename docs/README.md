# NDHIS AI Prototype Documentation

Core handoff documents:

- [API Guide](API.md) — authenticated gateway endpoints, chat/streaming, forecasting, radiology, translation WebSocket, request IDs, limits, and clinical-safety boundaries.
- [Deployed System, Architecture, and Capacity](DEPLOYED_SYSTEM.md) — actual CT 110 / Westmere configuration, deployed model/runtime, measured startup and inference results, specialist latency, and defensible current capacity statement.
- [Internal Security Audit](SECURITY_AUDIT.md) — trust-boundary review, findings/severity, production blockers, and production security gate.
- [Architecture](ARCHITECTURE.md) — generic service-level architecture and profile model.
- [Evaluation](EVALUATION.md) — model/task evaluation notes.
- [Benchmarking](BENCHMARKING.md) — benchmark methodology.
- [Westmere Performance](WESTMERE_PERF.md) — legacy CPU performance investigation.
- [Prototype Scope](PROTOTYPE_SCOPE.md) — intended prototype boundary.

The deployed-system document intentionally distinguishes the exact model/configuration tested on the current server from generic/default repository profiles.

Nothing in these documents should be interpreted as clinical validation, regulatory certification, or proof of production/national-scale capacity.
