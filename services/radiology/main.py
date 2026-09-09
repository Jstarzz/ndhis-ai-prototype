import hashlib
import io
import os
import time
from collections import OrderedDict
from contextlib import asynccontextmanager
from pathlib import Path

import numpy as np
import pydicom
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
ONNX_FILE = os.environ.get("RADIOLOGY_ONNX_FILE", "model.onnx")
ONNX_SIZE = int(os.environ.get("RADIOLOGY_ONNX_SIZE", "224"))
ONNX_OUTPUT = os.environ.get("RADIOLOGY_ONNX_OUTPUT", "logits")
ONNX_LABELS = [
    item.strip()
    for item in os.environ.get(
        "RADIOLOGY_ONNX_LABELS",
        "No Finding,Enlarged Cardiomediastinum,Cardiomegaly,Lung Opacity,Lung Lesion,Edema,Consolidation,Pneumonia,Atelectasis,Pneumothorax,Pleural Effusion,Pleural Other,Fracture,Support Devices",
    ).split(",")
    if item.strip()
]
PIPELINE_SIZE = int(os.environ.get("RADIOLOGY_PIPELINE_SIZE", "256"))
PIPELINE_SEGMENTATION_FILE = os.environ.get("RADIOLOGY_SEGMENTATION_FILE", "lung-segmentation.onnx")
PIPELINE_SCREENING_FILE = os.environ.get("RADIOLOGY_SCREENING_FILE", "healthy-unhealthy-densenet.onnx")
PIPELINE_DISEASE_FILE = os.environ.get("RADIOLOGY_DISEASE_FILE", "disease-densenet.onnx")
PIPELINE_SEGMENTATION_THRESHOLD = float(os.environ.get("RADIOLOGY_SEGMENTATION_THRESHOLD", "0.5"))
PIPELINE_SCREENING_THRESHOLD = float(os.environ.get("RADIOLOGY_SCREENING_THRESHOLD", "0.91"))
PIPELINE_OPENCV_THREADS = int(os.environ.get("RADIOLOGY_OPENCV_THREADS", "6"))
PIPELINE_SOURCE = "a1mohamadd/lung-disease-detection"
PIPELINE_LICENSE = "MIT"
PIPELINE_DISEASE_LABELS = ["COVID", "Viral Pneumonia", "Lung Opacity"]

processor = None
model = None
model_ready = False
model_error: str | None = None
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
    import torch
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


def configure_cv_net(cv2, net):
    net.setPreferableBackend(cv2.dnn.DNN_BACKEND_OPENCV)
    net.setPreferableTarget(cv2.dnn.DNN_TARGET_CPU)
    return net


def load_onnx():
    import cv2

    model_file = Path(MODEL_PATH) / ONNX_FILE
    if not model_file.is_file():
        raise RuntimeError(f"required local ONNX model missing: {model_file}")
    cv2.setNumThreads(max(1, PIPELINE_OPENCV_THREADS))
    cv2.ocl.setUseOpenCL(False)
    return cv2, configure_cv_net(cv2, cv2.dnn.readNetFromONNX(str(model_file)))


def load_lung_pipeline():
    import cv2

    root = Path(MODEL_PATH)
    files = {
        "segmentation": root / PIPELINE_SEGMENTATION_FILE,
        "screening": root / PIPELINE_SCREENING_FILE,
        "disease": root / PIPELINE_DISEASE_FILE,
    }
    missing = [str(path) for path in files.values() if not path.is_file()]
    if missing:
        raise RuntimeError("required radiology pipeline model missing: " + ", ".join(missing))
    cv2.setNumThreads(max(1, PIPELINE_OPENCV_THREADS))
    cv2.ocl.setUseOpenCL(False)
    nets = {name: configure_cv_net(cv2, cv2.dnn.readNetFromONNX(str(path))) for name, path in files.items()}
    return cv2, nets


@asynccontextmanager
async def lifespan(app: FastAPI):
    global processor, model, model_ready, model_error
    model_ready = False
    model_error = None
    try:
        require_model(MODEL_PATH)
        if BACKEND == "medgemma":
            processor, model = load_medgemma()
        elif BACKEND == "torchxrayvision":
            processor, model = load_xrv()
        elif BACKEND == "opencv_onnx":
            processor, model = load_onnx()
        elif BACKEND == "opencv_onnx_lung_pipeline":
            processor, model = load_lung_pipeline()
        else:
            raise RuntimeError(f"unsupported radiology backend: {BACKEND}")
        self_test_model()
        model_ready = True
    except Exception as exc:
        model_error = sanitize_model_error(exc)
    yield


app = FastAPI(lifespan=lifespan)


def sanitize_model_error(exc: Exception) -> str:
    text = " ".join(str(exc).split())
    if len(text) > 500:
        text = text[:500] + "…"
    return text or exc.__class__.__name__


def self_test_model() -> None:
    size = PIPELINE_SIZE if BACKEND == "opencv_onnx_lung_pipeline" else ONNX_SIZE
    image = Image.new("RGB", (size, size), color=(127, 127, 127))
    if BACKEND == "opencv_onnx":
        findings, predictions = analyze_onnx(image)
        if not findings or predictions is None or len(predictions) != len(ONNX_LABELS):
            raise RuntimeError("ONNX self-test returned an invalid prediction contract")
    elif BACKEND == "opencv_onnx_lung_pipeline":
        findings, predictions, details = analyze_lung_pipeline(image, force_disease=True)
        if not findings or len(predictions) < 5 or details.get("segmentation_fraction") is None:
            raise RuntimeError("lung pipeline self-test returned an invalid prediction contract")
    elif BACKEND == "torchxrayvision":
        findings, predictions = analyze_xrv(image)
        if not findings or predictions is None:
            raise RuntimeError("TorchXRayVision self-test returned an invalid prediction contract")


@app.get("/health")
def health():
    payload = {
        "status": "ready" if model_ready else "degraded",
        "local": True,
        "model": MODEL_NAME,
        "backend": BACKEND,
        "device": DEVICE,
        "model_ready": model_ready,
        "model_error": model_error,
        "max_upload_bytes": MAX_UPLOAD_BYTES,
        "score_threshold": SCORE_THRESHOLD if BACKEND not in {"medgemma", "opencv_onnx_lung_pipeline"} else None,
    }
    if BACKEND == "opencv_onnx_lung_pipeline":
        payload.update(
            {
                "model_source": PIPELINE_SOURCE,
                "model_license": PIPELINE_LICENSE,
                "screening_threshold": PIPELINE_SCREENING_THRESHOLD,
                "opencv_threads": PIPELINE_OPENCV_THREADS,
                "pipeline": ["lung_segmentation", "healthy_unhealthy_screen", "disease_subtype_if_unhealthy"],
            }
        )
    return payload


def analyze_medgemma(image: Image.Image, prompt: str):
    import torch

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
        generation = model.generate(**inputs, max_new_tokens=192, do_sample=False)[0][input_len:]
    return processor.decode(generation, skip_special_tokens=True).strip(), None


def analyze_xrv(image: Image.Image):
    import torch
    import torchxrayvision as xrv

    array = np.asarray(image.convert("L"), dtype=np.float32)
    array = xrv.utils.normalize(array, maxval=255, reshape=True)
    array = xrv.datasets.XRayCenterCrop()(array)
    array = xrv.datasets.XRayResizer(224)(array)
    tensor = torch.from_numpy(array).unsqueeze(0).to(DEVICE)
    with torch.inference_mode():
        scores = model(tensor)[0].detach().cpu().numpy()
    return format_predictions(model.pathologies, scores)


def run_cv_net(net, array: np.ndarray) -> np.ndarray:
    contiguous = np.ascontiguousarray(array, dtype=np.float32)
    net.setInput(contiguous)
    return np.asarray(net.forward(), dtype=np.float32)


def image_tensor(image: Image.Image, size: int) -> np.ndarray:
    return np.asarray(image.convert("RGB").resize((size, size), Image.BILINEAR), dtype=np.float32)


def torch_normalize(array: np.ndarray) -> np.ndarray:
    mean = np.asarray([0.485, 0.456, 0.406], dtype=np.float32)
    std = np.asarray([0.229, 0.224, 0.225], dtype=np.float32)
    return (array / 255.0 - mean) / std


def probability(value: float) -> float:
    if 0.0 <= value <= 1.0:
        return value
    return float(1.0 / (1.0 + np.exp(-np.clip(value, -30.0, 30.0))))


def multiclass_probabilities(values: np.ndarray) -> np.ndarray:
    scores = np.asarray(values, dtype=np.float64).reshape(-1)
    if len(scores) == 0:
        raise RuntimeError("disease model returned no scores")
    if np.all(scores >= 0) and np.all(scores <= 1) and 0.95 <= float(scores.sum()) <= 1.05:
        return scores.astype(np.float32)
    shifted = scores - np.max(scores)
    exp = np.exp(np.clip(shifted, -60.0, 60.0))
    return (exp / max(float(exp.sum()), 1e-12)).astype(np.float32)


def lung_roi(array: np.ndarray, mask: np.ndarray, size: int) -> np.ndarray:
    binary = np.asarray(mask) > PIPELINE_SEGMENTATION_THRESHOLD
    indices = np.argwhere(binary)
    if indices.size == 0:
        cropped = array
    else:
        y_min, x_min = indices.min(axis=0)[:2]
        y_max, x_max = indices.max(axis=0)[:2]
        height = max(int(y_max - y_min), 1)
        width = max(int(x_max - x_min), 1)
        margin_y = max(int(height * 0.1), 1)
        margin_x = max(int(width * 0.1), 1)
        y_start = max(0, int(y_min) - margin_y)
        x_start = max(0, int(x_min) - margin_x)
        y_end = min(array.shape[0], int(y_max) + margin_y + 1)
        x_end = min(array.shape[1], int(x_max) + margin_x + 1)
        cropped = array[y_start:y_end, x_start:x_end]
    pil = Image.fromarray(np.clip(cropped, 0, 255).astype(np.uint8))
    return np.asarray(pil.resize((size, size), Image.BILINEAR), dtype=np.float32)


def segmentation_mask(array: np.ndarray) -> np.ndarray:
    scores = np.squeeze(run_cv_net(model["segmentation"], (array / 255.0)[None]))
    if scores.ndim == 3 and scores.shape[-1] == 1:
        scores = scores[..., 0]
    elif scores.ndim == 3 and scores.shape[0] == 1:
        scores = scores[0]
    if scores.ndim != 2:
        raise RuntimeError(f"segmentation model returned unsupported shape {scores.shape}")
    return scores


def screening_probability(roi: np.ndarray) -> float:
    output = run_cv_net(model["screening"], torch_normalize(roi)[None]).reshape(-1)
    if len(output) == 1:
        return probability(float(output[0]))
    if len(output) == 2:
        probs = multiclass_probabilities(output)
        return float(probs[1])
    raise RuntimeError(f"screening model returned {len(output)} scores; expected 1 or 2")


def disease_probabilities(roi: np.ndarray) -> np.ndarray:
    output = run_cv_net(model["disease"], torch_normalize(roi)[None]).reshape(-1)
    if len(output) != len(PIPELINE_DISEASE_LABELS):
        raise RuntimeError(f"disease model returned {len(output)} scores; expected {len(PIPELINE_DISEASE_LABELS)}")
    return multiclass_probabilities(output)


def analyze_lung_pipeline(image: Image.Image, force_disease: bool = False):
    array = image_tensor(image, PIPELINE_SIZE)
    mask_scores = segmentation_mask(array)
    mask = mask_scores > PIPELINE_SEGMENTATION_THRESHOLD
    roi = lung_roi(array, mask, PIPELINE_SIZE)
    unhealthy = screening_probability(roi)
    predictions = [
        {"label": "Healthy screening", "score": round(1.0 - unhealthy, 4), "stage": "screening"},
        {"label": "Unhealthy screening", "score": round(unhealthy, 4), "stage": "screening"},
    ]
    disease_scores = None
    if unhealthy >= PIPELINE_SCREENING_THRESHOLD or force_disease:
        disease_scores = disease_probabilities(roi)
        predictions.extend(
            {"label": label, "score": round(float(score), 4), "stage": "disease_subtype"}
            for label, score in zip(PIPELINE_DISEASE_LABELS, disease_scores)
        )
    if unhealthy >= PIPELINE_SCREENING_THRESHOLD and disease_scores is not None:
        best = int(np.argmax(disease_scores))
        findings = (
            f"Research screening output: unhealthy probability {unhealthy:.3f}, above the configured {PIPELINE_SCREENING_THRESHOLD:.2f} threshold. "
            f"Within this model's limited subtype classes, the highest score is {PIPELINE_DISEASE_LABELS[best]} {float(disease_scores[best]):.3f}. "
            "This model does not evaluate the full range of chest radiograph findings."
        )
    else:
        findings = (
            f"Research screening output: unhealthy probability {unhealthy:.3f}, below the configured {PIPELINE_SCREENING_THRESHOLD:.2f} threshold. "
            "No disease subtype was selected. This does not establish a normal radiograph or exclude other findings."
        )
    details = {
        "segmentation_fraction": round(float(mask.mean()), 4),
        "screening_threshold": PIPELINE_SCREENING_THRESHOLD,
        "source": PIPELINE_SOURCE,
        "license": PIPELINE_LICENSE,
        "scope": ["healthy_unhealthy_screening", *PIPELINE_DISEASE_LABELS],
    }
    return findings, predictions, details


def analyze_onnx(image: Image.Image):
    cv2 = processor
    array = np.asarray(image.convert("RGB").resize((ONNX_SIZE, ONNX_SIZE)), dtype=np.float32) / 255.0
    mean = np.asarray([0.485, 0.456, 0.406], dtype=np.float32)
    std = np.asarray([0.229, 0.224, 0.225], dtype=np.float32)
    array = (array - mean) / std
    blob = np.transpose(array, (2, 0, 1))[None]
    model.setInput(blob)
    scores = np.asarray(model.forward(), dtype=np.float32).reshape(-1)
    if ONNX_OUTPUT == "logits":
        scores = 1.0 / (1.0 + np.exp(-scores))
    elif ONNX_OUTPUT != "probabilities":
        raise RuntimeError(f"unsupported RADIOLOGY_ONNX_OUTPUT: {ONNX_OUTPUT}")
    if len(scores) != len(ONNX_LABELS):
        raise RuntimeError(f"ONNX output count {len(scores)} does not match label count {len(ONNX_LABELS)}")
    return format_predictions(ONNX_LABELS, scores)


def format_predictions(labels, scores):
    predictions = [
        {"label": label, "score": round(float(score), 4)}
        for label, score in zip(labels, scores)
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
    if not model_ready:
        raise HTTPException(
            503,
            f"radiology model unavailable: {model_error or 'startup self-test failed'}. Install a compatible validated model before analysis.",
        )
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
        raise HTTPException(400, f"invalid image: {sanitize_model_error(exc)}") from exc

    pipeline_details = None
    try:
        if BACKEND == "medgemma":
            findings, predictions = analyze_medgemma(image, prompt)
        elif BACKEND == "torchxrayvision":
            findings, predictions = analyze_xrv(image)
        elif BACKEND == "opencv_onnx_lung_pipeline":
            findings, predictions, pipeline_details = analyze_lung_pipeline(image)
        else:
            findings, predictions = analyze_onnx(image)
    except Exception as exc:
        raise HTTPException(503, f"radiology inference failed: {sanitize_model_error(exc)}") from exc

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
        "model_source": PIPELINE_SOURCE if BACKEND == "opencv_onnx_lung_pipeline" else None,
        "model_license": PIPELINE_LICENSE if BACKEND == "opencv_onnx_lung_pipeline" else None,
        "pipeline_details": pipeline_details,
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
