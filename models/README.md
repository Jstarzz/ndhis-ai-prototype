# Local model folders

Runtime model download is disabled. A configured model directory must exist locally or its service fails.

## GPU profile

```text
models/
  agent/
  asr/
  translation/
  forecast/
  radiology/
```

Recommended GPU prototype checkpoints total about 14.8 GB on disk. The official MedGemma checkpoint is about 8.64 GB on disk even though the service quantizes it during loading.

## CPU profile

```text
models/cpu/
  agent/
  asr/
  translation/
  forecast/
  radiology/
```

The CPU model set totals about 4.8 GB on disk and is deliberately different from the GPU set. Use `./scripts/fetch_cpu_models.sh` or place compatible complete weights in each role folder.

To replace a model, stop the profile, replace the compatible role folder, update its configured model name, then restart. Loader-incompatible architectures require an explicit service-loader change. The system does not silently substitute another model.

The GPU NLLB and TimesFM prototype weights have licensing restrictions that require review before production use. The CPU profile uses Apache-2.0/MIT components for the selected model set.
