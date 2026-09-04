import os
import time
from contextlib import asynccontextmanager
from pathlib import Path

import numpy as np
import pandas as pd
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field

MODEL_PATH = os.environ["FORECAST_MODEL_PATH"]
DEVICE = os.environ["FORECAST_DEVICE"]
DATA_PATH = os.environ["FORECAST_DATA_PATH"]
MODEL_NAME = os.environ["FORECAST_MODEL_NAME"]
BACKEND = os.environ.get("FORECAST_BACKEND", "timesfm3")
HISTORY_DAYS = int(os.environ["FORECAST_HISTORY_DAYS"])
RIDGE_LAGS = int(os.environ.get("FORECAST_RIDGE_LAGS", "14"))
RIDGE_ALPHA = float(os.environ.get("FORECAST_RIDGE_ALPHA", "10"))

forecaster = None
data = None

DISEASE_COLUMNS = {
    "respiratory": "respiratory_cases",
    "gastro": "gastro_cases",
    "diabetes": "diabetes_cases",
    "hypertension": "hypertension_cases",
}


class ForecastRequest(BaseModel):
    facility: str = "JNF"
    department: str | None = None
    metric: str
    disease: str | None = None
    horizon_days: int = Field(default=30, ge=1, le=90)


def require_model(path: str) -> None:
    root = Path(path)
    if not root.exists():
        raise RuntimeError(f"model directory not found: {path}")
    files = [item for item in root.iterdir() if item.name != ".gitkeep"]
    if not files:
        raise RuntimeError(f"model directory is empty: {path}")


def load_forecaster():
    if BACKEND == "timesfm3":
        from timesfm3 import ModelConfig, TimesFM3Evaluator

        return TimesFM3Evaluator(
            ModelConfig(
                checkpoint_path=MODEL_PATH,
                per_core_batch_size=8,
                device=DEVICE,
            )
        )
    if BACKEND == "chronos2":
        from chronos import Chronos2Pipeline

        return Chronos2Pipeline.from_pretrained(MODEL_PATH, device_map=DEVICE)
    if BACKEND == "ridge":
        return {"lags": RIDGE_LAGS, "alpha": RIDGE_ALPHA}
    raise RuntimeError(f"unsupported forecast backend: {BACKEND}")


@asynccontextmanager
async def lifespan(app: FastAPI):
    global forecaster, data
    if BACKEND != "ridge":
        require_model(MODEL_PATH)
    if not Path(DATA_PATH).exists():
        raise RuntimeError(f"data file not found: {DATA_PATH}")
    data = pd.read_csv(DATA_PATH, parse_dates=["date"])
    forecaster = load_forecaster()
    yield


app = FastAPI(lifespan=lifespan)


@app.get("/health")
def health():
    return {
        "status": "ready",
        "local": True,
        "rows": len(data),
        "model": MODEL_NAME,
        "backend": BACKEND,
        "device": DEVICE,
        "data_mode": "synthetic",
        "history_days": HISTORY_DAYS,
    }


def series_for(request: ForecastRequest):
    frame = data[data["facility"].str.casefold() == request.facility.casefold()].copy()
    if request.department:
        frame = frame[frame["department"].str.casefold() == request.department.casefold()]
    if frame.empty:
        raise HTTPException(404, "no synthetic data for selection")

    if request.metric == "disease_incidence":
        if not request.disease:
            raise HTTPException(400, "disease is required")
        column = DISEASE_COLUMNS.get(request.disease.casefold())
        if not column:
            raise HTTPException(400, "unsupported disease category")
        grouped = frame.groupby("date", as_index=False)[column].sum()
        values = grouped[column].to_numpy(dtype=np.float32)
        aggregate = "sum"
    elif request.metric == "patient_arrivals":
        grouped = frame.groupby("date", as_index=False)["patient_arrivals"].sum()
        values = grouped["patient_arrivals"].to_numpy(dtype=np.float32)
        aggregate = "sum"
    elif request.metric == "bed_occupancy":
        grouped = frame.groupby("date", as_index=False)["bed_occupancy"].mean()
        values = grouped["bed_occupancy"].to_numpy(dtype=np.float32)
        aggregate = "mean"
    else:
        raise HTTPException(400, "unsupported metric")

    return grouped["date"].iloc[-HISTORY_DAYS:], values[-HISTORY_DAYS:], aggregate


def predict_timesfm(values: np.ndarray, horizon: int):
    output = list(
        forecaster.predict_batch(
            [values],
            horizon=horizon,
            return_quantiles=True,
            use_symmetric_averaging=False,
        )
    )[0]
    points = np.asarray(output.forecast, dtype=float)
    quantiles = np.asarray(output.quantiles, dtype=float)
    return points, quantiles[:, 0], quantiles[:, -1]


def predict_chronos(dates: pd.Series, values: np.ndarray, horizon: int):
    context = pd.DataFrame({"item_id": "series", "timestamp": dates.to_numpy(), "target": values})
    output = forecaster.predict_df(
        context,
        prediction_length=horizon,
        quantile_levels=[0.1, 0.5, 0.9],
        id_column="item_id",
        timestamp_column="timestamp",
        target="target",
        batch_size=1,
        freq="D",
    )
    return (
        output["predictions"].to_numpy(dtype=float),
        output["0.1"].to_numpy(dtype=float),
        output["0.9"].to_numpy(dtype=float),
    )


def predict_ridge(values: np.ndarray, horizon: int):
    lags = max(2, min(int(forecaster["lags"]), len(values) // 3))
    if len(values) <= lags + 2:
        raise HTTPException(400, "not enough history for ridge forecast")
    rows = []
    targets = []
    for index in range(lags, len(values)):
        rows.append(values[index - lags:index])
        targets.append(values[index])
    x = np.asarray(rows, dtype=np.float64)
    y = np.asarray(targets, dtype=np.float64)
    x = np.column_stack([np.ones(len(x)), x])
    penalty = np.eye(x.shape[1], dtype=np.float64) * float(forecaster["alpha"])
    penalty[0, 0] = 0
    weights = np.linalg.solve(x.T @ x + penalty, x.T @ y)
    fitted = x @ weights
    residual_std = float(np.std(y - fitted))
    history = list(np.asarray(values, dtype=np.float64))
    points = []
    for _ in range(horizon):
        features = np.asarray([1.0, *history[-lags:]], dtype=np.float64)
        value = float(features @ weights)
        points.append(value)
        history.append(value)
    points = np.asarray(points, dtype=float)
    spread = 1.645 * max(residual_std, 1e-6)
    return points, points - spread, points + spread


@app.post("/forecast")
def forecast(request: ForecastRequest):
    started = time.perf_counter()
    dates, values, aggregate = series_for(request)
    if BACKEND == "timesfm3":
        points, lower, upper = predict_timesfm(values, request.horizon_days)
    elif BACKEND == "chronos2":
        points, lower, upper = predict_chronos(dates, values, request.horizon_days)
    else:
        points, lower, upper = predict_ridge(values, request.horizon_days)
    points = np.maximum(points, 0)
    lower = np.maximum(lower, 0)
    upper = np.maximum(upper, 0)
    start_date = dates.iloc[-1] + pd.Timedelta(days=1)
    future_dates = pd.date_range(start_date, periods=request.horizon_days, freq="D")

    if aggregate == "sum":
        expected = float(points.sum())
        p10 = float(lower.sum())
        p90 = float(upper.sum())
    else:
        expected = float(points.mean())
        p10 = float(lower.mean())
        p90 = float(upper.mean())

    return {
        "facility": request.facility,
        "department": request.department,
        "metric": request.metric,
        "disease": request.disease,
        "horizon_days": request.horizon_days,
        "expected": round(expected, 2),
        "p10": round(p10, 2),
        "p90": round(p90, 2),
        "series": [
            {
                "date": date.date().isoformat(),
                "forecast": round(float(point), 2),
                "p10": round(float(lo), 2),
                "p90": round(float(hi), 2),
            }
            for date, point, lo, hi in zip(future_dates, points, lower, upper)
        ],
        "latency_ms": int((time.perf_counter() - started) * 1000),
        "data_mode": "synthetic",
        "model": MODEL_NAME,
        "backend": BACKEND,
        "history_days_used": min(HISTORY_DAYS, len(values)),
        "history_start": dates.iloc[0].date().isoformat(),
        "history_end": dates.iloc[-1].date().isoformat(),
    }
