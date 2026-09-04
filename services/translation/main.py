import asyncio
import json
import os
import time
import urllib.error
import urllib.request
from contextlib import asynccontextmanager
from datetime import datetime, timezone
from pathlib import Path

import numpy as np
import torch
from fastapi import FastAPI, WebSocket, WebSocketDisconnect
from faster_whisper import WhisperModel
from transformers import AutoModelForSeq2SeqLM, AutoTokenizer

ASR_MODEL_PATH = os.environ["ASR_MODEL_PATH"]
TRANSLATION_MODEL_PATH = os.environ["TRANSLATION_MODEL_PATH"]
TRANSLATION_DEVICE = os.environ["TRANSLATION_DEVICE"]
TRANSLATION_BACKEND = os.environ.get("TRANSLATION_BACKEND", "nllb")
QVAC_TRANSLATION_URL = os.environ.get("QVAC_TRANSLATION_URL", "")
ASR_DEVICE = os.environ["ASR_DEVICE"]
ASR_COMPUTE_TYPE = os.environ["ASR_COMPUTE_TYPE"]
DEMO_API_KEY = os.environ["DEMO_API_KEY"]
ASR_MODEL_NAME = os.environ["ASR_MODEL_NAME"]
TRANSLATION_MODEL_NAME = os.environ["TRANSLATION_MODEL_NAME"]
AUDIT_PATH = os.environ["TRANSLATION_AUDIT_PATH"]
MAX_SESSION_SECONDS = int(os.environ["MAX_TRANSLATION_SESSION_SECONDS"])
CHUNK_SECONDS = float(os.environ["TRANSLATION_CHUNK_SECONDS"])

LANGUAGES = {
    "en": "eng_Latn",
    "es": "spa_Latn",
    "fr": "fra_Latn",
    "ht": "hat_Latn",
    "pt": "por_Latn",
    "de": "deu_Latn",
    "it": "ita_Latn",
    "fi": "fin_Latn",
    "cs": "ces_Latn",
    "nl": "nld_Latn",
    "sv": "swe_Latn",
}

asr_model = None
translation_tokenizer = None
translation_model = None
QVAC_SUPPORTED_TARGETS = {LANGUAGES[code] for code in ["en", "de", "es", "fr", "it", "pt", "fi", "cs", "nl", "sv"]}


def require_model(path: str) -> None:
    files = [item for item in Path(path).iterdir() if item.name != ".gitkeep"]
    if not files:
        raise RuntimeError(f"model directory is empty: {path}")


def write_audit(event: dict) -> None:
    path = Path(AUDIT_PATH)
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("a", encoding="utf-8") as file:
        file.write(json.dumps(event, separators=(",", ":")) + "\n")


@asynccontextmanager
async def lifespan(app: FastAPI):
    global asr_model, translation_tokenizer, translation_model
    require_model(ASR_MODEL_PATH)
    asr_model = WhisperModel(ASR_MODEL_PATH, device=ASR_DEVICE, compute_type=ASR_COMPUTE_TYPE)
    if TRANSLATION_BACKEND == "nllb":
        require_model(TRANSLATION_MODEL_PATH)
        translation_tokenizer = AutoTokenizer.from_pretrained(TRANSLATION_MODEL_PATH, local_files_only=True)
        dtype = torch.float16 if TRANSLATION_DEVICE == "cuda" else torch.float32
        translation_model = AutoModelForSeq2SeqLM.from_pretrained(
            TRANSLATION_MODEL_PATH,
            local_files_only=True,
            torch_dtype=dtype,
        ).to(TRANSLATION_DEVICE)
        translation_model.eval()
    elif TRANSLATION_BACKEND != "qvac":
        raise RuntimeError(f"unsupported translation backend: {TRANSLATION_BACKEND}")
    yield


app = FastAPI(lifespan=lifespan)


@app.get("/health")
def health():
    return {
        "status": "ready",
        "local": True,
        "asr_model": ASR_MODEL_NAME,
        "translation_model": TRANSLATION_MODEL_NAME,
        "translation_device": TRANSLATION_DEVICE,
        "translation_backend": TRANSLATION_BACKEND,
        "asr_device": ASR_DEVICE,
        "asr_compute_type": ASR_COMPUTE_TYPE,
        "chunk_seconds": CHUNK_SECONDS,
        "supported_targets": sorted(QVAC_SUPPORTED_TARGETS if TRANSLATION_BACKEND == "qvac" else LANGUAGES.values()),
    }


def translate_qvac(text: str, source: str, target: str) -> str:
    reverse_languages = {value: key for key, value in LANGUAGES.items()}
    source_iso = reverse_languages.get(source, source)
    target_iso = reverse_languages.get(target, target)
    payload = json.dumps({"text": text, "source": source_iso, "target": target_iso}).encode()
    request = urllib.request.Request(QVAC_TRANSLATION_URL, data=payload, headers={"content-type": "application/json"}, method="POST")
    try:
        with urllib.request.urlopen(request, timeout=15) as response:
            result = json.loads(response.read())
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode(errors="replace")
        raise RuntimeError(f"CPU translation engine rejected request: {detail}") from exc
    return result["translation"]


def translate_text(text: str, source: str, target: str) -> str:
    if source == target:
        return text
    if TRANSLATION_BACKEND == "qvac":
        return translate_qvac(text, source, target)
    translation_tokenizer.src_lang = source
    encoded = translation_tokenizer(text, return_tensors="pt").to(TRANSLATION_DEVICE)
    target_id = translation_tokenizer.convert_tokens_to_ids(target)
    with torch.inference_mode():
        output = translation_model.generate(
            **encoded,
            forced_bos_token_id=target_id,
            max_new_tokens=160,
            num_beams=1,
        )
    return translation_tokenizer.batch_decode(output, skip_special_tokens=True)[0]


def process_audio(audio: np.ndarray, source_hint: str, target: str):
    started = time.perf_counter()
    language = None if source_hint == "auto" else source_hint
    segments, info = asr_model.transcribe(
        audio,
        language=language,
        beam_size=1,
        vad_filter=True,
        condition_on_previous_text=False,
    )
    transcript = " ".join(segment.text.strip() for segment in segments).strip()
    detected = info.language or source_hint
    source_code = LANGUAGES.get(detected)
    if not source_code:
        raise RuntimeError(f"unsupported detected language: {detected}")
    translated = translate_text(transcript, source_code, target) if transcript else ""
    return {
        "transcript": transcript,
        "translation": translated,
        "source_language": detected,
        "target_language": target,
        "latency_ms": int((time.perf_counter() - started) * 1000),
    }


@app.websocket("/ws/translate")
async def translate_socket(websocket: WebSocket):
    if websocket.query_params.get("key") != DEMO_API_KEY:
        await websocket.close(code=4401)
        return
    user = (websocket.query_params.get("user") or "").strip()
    role = (websocket.query_params.get("role") or "").strip()
    if not user or not role:
        await websocket.close(code=4400)
        return
    if role.casefold() != "doctor":
        await websocket.close(code=4403)
        return
    await websocket.accept()
    config = json.loads(await websocket.receive_text())
    source = config.get("source", "auto")
    target = config.get("target", "spa_Latn")
    supported_targets = QVAC_SUPPORTED_TARGETS if TRANSLATION_BACKEND == "qvac" else set(LANGUAGES.values())
    if target not in supported_targets:
        await websocket.send_json({"error": "unsupported target language for active translation backend"})
        await websocket.close(code=4400)
        return
    buffer = bytearray()
    chunk_bytes = int(16000 * 2 * CHUNK_SECONDS)
    started = time.monotonic()
    segment_count = 0
    error = ""
    try:
        while True:
            if time.monotonic() - started >= MAX_SESSION_SECONDS:
                await websocket.send_json({"error": "translation session duration limit reached"})
                await websocket.close(code=4408)
                break
            message = await websocket.receive()
            data = message.get("bytes")
            if not data:
                continue
            buffer.extend(data)
            if len(buffer) < chunk_bytes:
                continue
            chunk = bytes(buffer[:chunk_bytes])
            del buffer[:chunk_bytes]
            audio = np.frombuffer(chunk, dtype=np.int16).astype(np.float32) / 32768.0
            try:
                result = await asyncio.to_thread(process_audio, audio, source, target)
                segment_count += 1
                await websocket.send_json(result)
            except Exception as exc:
                error = str(exc)
                await websocket.send_json({"error": error})
    except WebSocketDisconnect:
        pass
    finally:
        write_audit(
            {
                "timestamp": datetime.now(timezone.utc).isoformat(),
                "user": user,
                "role": role,
                "target_language": target,
                "segments": segment_count,
                "duration_seconds": round(time.monotonic() - started, 2),
                "status": "error" if error else "closed",
                "model_asr": ASR_MODEL_NAME,
                "model_translation": TRANSLATION_MODEL_NAME,
            }
        )
