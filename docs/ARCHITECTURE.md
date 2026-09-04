# Architecture

```text
NDHIS or demo UI
        |
        v
Go AI Gateway
  |  auth identity
  |  rate limits
  |  concurrency limits
  |  audit events
  |
  +--> agent runtime
  |      GPU: vLLM + LFM2.5-350M
  |      CPU: vLLM + Qwen3-1.7B
  |      natural language -> tool decision -> grounded response
  |
  +--> Forecast service
  |      GPU: TimesFM 3
  |      CPU: Chronos-2
  |      synthetic operational data in the prototype
  |
  +--> Radiology service
         GPU: MedGemma 1.5 4B
         CPU: TorchXRayVision DenseNet121
         public or de-identified demo images

Browser microphone
        |
        v
Translation WebSocket
  |  identity + session audit
  |  GPU: faster-whisper large-v3-turbo + NLLB
  |  CPU: faster-whisper base + TranslatePsy-EuroNano Tiny
  v
Live translated text
```

The front agent does not perform forecasting or radiology inference itself. It selects a tool, receives a structured result from the specialist service, and produces a grounded natural-language response.

Translation connects directly to its WebSocket service because it is latency-sensitive. The service performs its own identity check, session limits, and audit event generation. Translation audits metadata only and does not persist transcript text.

All model loaders use local directories under `models/`. Hugging Face and Transformers runtime downloads are disabled in Compose. A missing model is a startup error.

## Data boundary

The prototype contains synthetic hospital operations data. The eventual NDHIS integration should provide authorized, scoped data through an analytics/API boundary rather than giving models unrestricted database access.

## Model replacement

Each model role has a stable service contract. Compatible replacement weights can be placed into the corresponding model folder and configured through `.env`. A model architecture that requires a different loader is an explicit service change, not an automatic fallback.

## Deployment profiles

`compose.yaml` is the GPU-oriented profile. `compose.cpu.yaml` is the explicit CPU-only profile. They share the Go gateway and browser contracts while selecting different specialist backends. No runtime automatically falls back between profiles.
