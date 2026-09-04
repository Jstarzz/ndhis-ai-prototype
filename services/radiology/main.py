import hashlib
import io
import os
import time
from collections import OrderedDict
from contextlib import asynccontextmanager
from pathlib import Path

import numpy as np
import pydicom
import torch
from fastapi import FastAPI, File, Form, HTTPException, UploadFile
from PIL import Image

MODEL_PATH = os.environ["RADIOLOGY_MODEL_PATH"]
MODEL_NAME = os.environ["RADIOLOGY_MODEL_NAME"]
BACKEND = os.environ.get("RADIOLOGY_BACKEND", "medgemma")
DEVICE = os.environ.get("RADIOLOGY_DEVICE", "cuda")
MAX_UPLOAD_BYTES = int(os.environ["RADIOLOGY_MAX_UPLOAD_MB"]) * 1024 * 1024
MAX_RESULTS = int(os.environ["RADIOLOGY_MAX_RESULTS"])
SCORE_THRESHOLD = float(os.environ.get("RADIOLOGY_SCORE_THRESHOLD", "0.5"))
XRV_WEIGHTS = os.environ.get("RADIOLOGY_XRV_WEIGHTS", "densenet121-res224-all")
XRV_WEIGHT_FILE = os.environ.get(
    "RADIOLOGY_XRV_WEIGHT_FILE",
    "nih-pc-chex-mimic_ch-google-openi-kaggle-densenet121-d121-tw-lr001-rot45-tr15-sc15-seed0-best.pt",
)

processor = None
model = None
results = OrderedDict()


def require_model(path: str) -> None:
    root = Path(path)
    if not root.exists():
        raise RuntimeError(f"model directory not found: {path}")
    files = [item for item in root.iterdir() if item.name != ".gitkeep"]
    if not files:
        raise RuntimeError(f"model directory is empty: {path}")


def load_image(payload: bytes, filename: str) -> Image.Image:
    if filename.lower().endswith(".dcm"):
        dataset = pydicom.dcmread(io.BytesIO(payload))
        pixels = dataset.pixel_array.astype(np.float32)
        low = float(np.percentile(pixels, 1))
        high = float(np.percentile(pixels, 99))
        pixels = np.clip((pixels - low) / max(high - low, 1e-6), 0, 1)
        if getattr(dataset, "PhotometricInterpretation", "") == "MONOCHROME1":
            pixels = 1.0 - pixels
        return Image.fromarray((pixels * 255).astype(np.uint8)).convert("RGB")
    return Image.open(io.BytesIO(payload)).convert("RGB")


def load_medgemma():
    from transformers import AutoModelForImageTextToText, AutoProcessor, BitsAndBytesConfig

    if DEVICE != "cuda":
        raise RuntimeError("medgemma backend requires RADIOLOGY_DEVICE=cuda in this prototype")
    quantization = BitsAndBytesConfig(
        load_in_4bit=True,
        bnb_4bit_quant_type="nf4",
        bnb_4bit_compute_dtype=torch.float16,
        bnb_4bit_use_double_quant=True,
    )
    loaded_processor = AutoProcessor.from_pretrained(MODEL_PATH, local_files_only=True)
    loaded_model = AutoModelForImageTextToText.from_pretrained(
        MODEL_PATH,
        local_files_only=True,
        device_map={"": 0},
        quantization_config=quantization,
        torch_dtype=torch.float16,
    )
    loaded_model.eval()
    return loaded_processor, loaded_model


def load_xrv():
    import torchxrayvision as xrv

    weight_path = Path(MODEL_PATH) / XRV_WEIGHT_FILE
    if not weight_path.is_file():
        raise RuntimeError(f"required local TorchXRayVision weight missing: {weight_path}")
    loaded_model = xrv.models.DenseNet(weights=XRV_WEIGHTS, cache_dir=MODEL_PATH).to(DEVICE)
    loaded_model.eval()
    return xrv, loaded_model


@asynccontextmanager
async def lifespan(app: FastAPI):
    global processor, model
    require_model(MODEL_PATH)
    if BACKEND == "medgemma":
        processor, model = load_medgemma()
    elif BACKEND == "torchxrayvision":
        processor, model = load_xrv()
    else:
        raise RuntimeError(f"unsupported radiology backend: {BACKEND}")
    yield


app = FastAPI(lifespan=lifespan)


@app.get("/health")
def health():
    return {
        "status": "ready",
        "local": True,
        "model": MODEL_NAME,
        "backend": BACKEND,
        "device": DEVICE,
        "max_upload_bytes": MAX_UPLOAD_BYTES,
        "score_threshold": SCORE_THRESHOLD if BACKEND == "torchxrayvision" else None,
    }


def analyze_medgemma(image: Image.Image, prompt: str):
    messages = [
        {
            "role": "user",
            "content": [
                {"type": "image", "image": image},
                {"type": "text", "text": prompt},
            ],
        }
    ]
    inputs = processor.apply_chat_template(
        messages,
        add_generation_prompt=True,
        tokenize=True,
        return_dict=True,
        return_tensors="pt",
    ).to(model.device, dtype=torch.float16)
    input_len = inputs["input_ids"].shape[-1]
    with torch.inference_mode():
        generation = model.generate(**inputs, max_new_tokens=320, do_sample=False)[0][input_len:]
    return processor.decode(generation, skip_special_tokens=True).strip(), None


def analyze_xrv(image: Image.Image):
    import torchxrayvision as xrv

    array = np.asarray(image.convert("L"), dtype=np.float32)
    array = xrv.utils.normalize(array, maxval=255, reshape=True)
    array = xrv.datasets.XRayCenterCrop()(array)
    array = xrv.datasets.XRayResizer(224)(array)
    tensor = torch.from_numpy(array).unsqueeze(0).to(DEVICE)
    with torch.inference_mode():
        scores = model(tensor)[0].detach().cpu().numpy()
    predictions = [
        {"label": label, "score": round(float(score), 4)}
        for label, score in zip(model.pathologies, scores)
        if label
    ]
    predictions.sort(key=lambda item: item["score"], reverse=True)
    selected = [item for item in predictions if item["score"] >= SCORE_THRESHOLD][:8]
    if selected:
        findings = "Highest model scores: " + ", ".join(
            f"{item['label']} {item['score']:.3f}" for item in selected
        )
    else:
        findings = "No model score reached the configured display threshold."
    return findings, predictions


@app.post("/analyze")
async def analyze(
    file: UploadFile = File(...),
    prompt: str = Form("Describe the clinically relevant findings in this radiology image concisely. State uncertainty and do not invent patient history."),
):
    started = time.perf_counter()
    if len(prompt) > 2000:
        raise HTTPException(400, "prompt too long")
    payload = await file.read(MAX_UPLOAD_BYTES + 1)
    if not payload:
        raise HTTPException(400, "empty image")
    if len(payload) > MAX_UPLOAD_BYTES:
        raise HTTPException(413, "image exceeds upload limit")
    try:
        image = load_image(payload, file.filename or "image")
    except Exception as exc:
        raise HTTPException(400, f"invalid image: {exc}") from exc

    if BACKEND == "medgemma":
        findings, predictions = analyze_medgemma(image, prompt)
    else:
        findings, predictions = analyze_xrv(image)

    result_id = hashlib.sha256(payload + str(time.time_ns()).encode()).hexdigest()[:16]
    result = {
        "result_id": result_id,
        "filename": file.filename,
        "findings": findings,
        "predictions": predictions,
        "review_required": True,
        "latency_ms": int((time.perf_counter() - started) * 1000),
        "data_mode": "public_or_deidentified_demo",
        "model": MODEL_NAME,
        "backend": BACKEND,
        "device": DEVICE,
    }
    results[result_id] = result
    while len(results) > MAX_RESULTS:
        results.popitem(last=False)
    return result


@app.get("/results/{result_id}")
def get_result(result_id: str):
    result = results.get(result_id)
    if not result:
        raise HTTPException(404, "radiology result not found")
    return result
