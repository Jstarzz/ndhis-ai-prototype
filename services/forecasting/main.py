from __future__ import annotations

import math
import os
import re
import time
from contextlib import asynccontextmanager
from datetime import datetime
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
RIDGE_ALPHA = float(os.environ.get("FORECAST_RIDGE_ALPHA", "10"))
MAX_POINTS = int(os.environ.get("FORECAST_MAX_POINTS", "2000"))

forecaster = None
data: pd.DataFrame | None = None
source_resolution_seconds = 3600.0

FACILITIES = {"JNF": ["A&E", "Outpatient", "Medical Ward", "Surgical Ward", "Pediatrics"]}
DISEASE_COLUMNS = {
    "respiratory": "respiratory_cases",
    "gastro": "gastro_cases",
    "diabetes": "diabetes_cases",
    "hypertension": "hypertension_cases",
}
COUNT_METRICS = {"patient_arrivals", "disease_incidence"}
RESOLUTION_PATTERN = re.compile(r"^(\d+)(ms|s|min|h|d|w|mo|y)$", re.IGNORECASE)
HORIZON_UNITS = {
    "milliseconds": 0.001,
    "millisecond": 0.001,
    "ms": 0.001,
    "seconds": 1.0,
    "second": 1.0,
    "s": 1.0,
    "minutes": 60.0,
    "minute": 60.0,
    "min": 60.0,
    "hours": 3600.0,
    "hour": 3600.0,
    "h": 3600.0,
    "days": 86400.0,
    "day": 86400.0,
    "d": 86400.0,
    "weeks": 604800.0,
    "week": 604800.0,
    "w": 604800.0,
    "months": 2629800.0,
    "month": 2629800.0,
    "mo": 2629800.0,
    "years": 31557600.0,
    "year": 31557600.0,
    "y": 31557600.0,
}


class ForecastRequest(BaseModel):
    facility: str = "JNF"
    department: str | None = None
    metric: str
    disease: str | None = None
    horizon: float = Field(default=30, gt=0)
    horizon_unit: str = "days"
    resolution: str = "auto"
    as_of: datetime | None = None
    include_actuals: bool = True
    # Backward compatibility for the original API and existing gateway clients.
    horizon_days: int | None = Field(default=None, ge=1, le=730)


class HistoryRequest(BaseModel):
    facility: str = "JNF"
    department: str | None = None
    metric: str
    disease: str | None = None
    start: datetime
    end: datetime
    resolution: str = "1d"


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
        return {"alpha": RIDGE_ALPHA}
    raise RuntimeError(f"unsupported forecast backend: {BACKEND}")


def normalize_timestamp(value: datetime | pd.Timestamp) -> pd.Timestamp:
    stamp = pd.Timestamp(value)
    if stamp.tzinfo is not None:
        stamp = stamp.tz_convert("UTC").tz_localize(None)
    return stamp


def infer_source_resolution(frame: pd.DataFrame) -> float:
    sample = frame[["timestamp", "department"]].drop_duplicates().sort_values("timestamp")
    if sample.empty:
        return 3600.0
    one_department = sample[sample["department"] == sample["department"].iloc[0]]
    diffs = one_department["timestamp"].diff().dropna().dt.total_seconds()
    if diffs.empty:
        return 86400.0
    return max(float(diffs.median()), 0.001)


@asynccontextmanager
async def lifespan(app: FastAPI):
    global forecaster, data, source_resolution_seconds
    if BACKEND != "ridge":
        require_model(MODEL_PATH)
    if not Path(DATA_PATH).exists():
        raise RuntimeError(f"data file not found: {DATA_PATH}")
    loaded = pd.read_csv(DATA_PATH)
    if "timestamp" in loaded.columns:
        loaded["timestamp"] = pd.to_datetime(loaded["timestamp"])
    elif "date" in loaded.columns:
        loaded["timestamp"] = pd.to_datetime(loaded["date"])
    else:
        raise RuntimeError("forecast data requires timestamp or date column")
    loaded = loaded.sort_values("timestamp").reset_index(drop=True)
    data = loaded
    source_resolution_seconds = infer_source_resolution(loaded)
    forecaster = load_forecaster()
    yield


app = FastAPI(lifespan=lifespan)


@app.get("/health")
def health():
    assert data is not None
    return {
        "status": "ready",
        "local": True,
        "rows": len(data),
        "model": MODEL_NAME,
        "backend": BACKEND,
        "device": DEVICE,
        "data_mode": "synthetic",
        "history_days": HISTORY_DAYS,
        "source_resolution": human_resolution(source_resolution_seconds),
        "data_start": data["timestamp"].min().isoformat(),
        "data_end": data["timestamp"].max().isoformat(),
    }


@app.get("/capabilities")
def capabilities():
    assert data is not None
    return {
        "facilities": FACILITIES,
        "metrics": ["patient_arrivals", "bed_occupancy", "disease_incidence"],
        "diseases": list(DISEASE_COLUMNS),
        "horizon_units": ["milliseconds", "seconds", "minutes", "hours", "days", "weeks", "months", "years"],
        "resolutions": ["1ms", "10ms", "100ms", "1s", "5s", "30s", "1min", "5min", "15min", "1h", "6h", "1d", "1w", "1mo"],
        "max_points": MAX_POINTS,
        "source_resolution": human_resolution(source_resolution_seconds),
        "sub_source_resolution_semantics": "derived intensity/interpolation, not observed millisecond patient timing",
        "data_start": data["timestamp"].min().isoformat(),
        "data_end": data["timestamp"].max().isoformat(),
        "data_mode": "synthetic",
    }


def canonical_facility(value: str) -> str:
    cleaned = value.strip()
    for facility in FACILITIES:
        if cleaned.casefold() == facility.casefold():
            return facility
    raise HTTPException(400, f"unsupported facility; available: {', '.join(FACILITIES)}")


def canonical_department(value: str | None) -> str | None:
    if value is None:
        return None
    cleaned = value.strip()
    for departments in FACILITIES.values():
        for department in departments:
            if cleaned.casefold() == department.casefold():
                return department
    available = ", ".join(next(iter(FACILITIES.values())))
    raise HTTPException(400, f"unsupported department; available: {available}")


def metric_column(metric: str, disease: str | None) -> tuple[str, str]:
    metric = metric.strip().casefold()
    if metric == "patient_arrivals":
        return "patient_arrivals", "sum"
    if metric == "bed_occupancy":
        return "bed_occupancy", "mean"
    if metric == "disease_incidence":
        if not disease:
            raise HTTPException(400, "disease is required for disease_incidence")
        column = DISEASE_COLUMNS.get(disease.casefold())
        if not column:
            raise HTTPException(400, f"unsupported disease category; available: {', '.join(DISEASE_COLUMNS)}")
        return column, "sum"
    raise HTTPException(400, "unsupported metric")


def resolution_seconds(value: str) -> float:
    match = RESOLUTION_PATTERN.fullmatch(value.strip())
    if not match:
        raise HTTPException(400, "resolution must look like 1ms, 1s, 5min, 1h, 1d, 1w or 1mo")
    amount = int(match.group(1))
    if amount < 1:
        raise HTTPException(400, "resolution must be positive")
    unit = match.group(2).casefold()
    scale = {
        "ms": 0.001,
        "s": 1.0,
        "min": 60.0,
        "h": 3600.0,
        "d": 86400.0,
        "w": 604800.0,
        "mo": 2629800.0,
        "y": 31557600.0,
    }[unit]
    return amount * scale


def human_resolution(seconds: float) -> str:
    if seconds < 1:
        return f"{round(seconds * 1000):g}ms"
    if seconds < 60:
        return f"{seconds:g}s"
    if seconds < 3600:
        return f"{seconds / 60:g}min"
    if seconds < 86400:
        return f"{seconds / 3600:g}h"
    if seconds < 604800:
        return f"{seconds / 86400:g}d"
    if seconds < 2500000:
        return f"{seconds / 604800:g}w"
    return f"{seconds / 2629800:g}mo"


def horizon_seconds(request: ForecastRequest) -> tuple[float, float, str]:
    if request.horizon_days is not None:
        return request.horizon_days * 86400.0, float(request.horizon_days), "days"
    unit = request.horizon_unit.strip().casefold()
    scale = HORIZON_UNITS.get(unit)
    if scale is None:
        raise HTTPException(400, f"unsupported horizon_unit: {request.horizon_unit}")
    seconds = float(request.horizon) * scale
    if seconds <= 0:
        raise HTTPException(400, "horizon must be positive")
    # Keep the demo bounded while still allowing strategic two-year scenarios.
    if seconds > 2 * HORIZON_UNITS["years"] + 86400:
        raise HTTPException(400, "forecast horizon is limited to 2 years in this prototype")
    return seconds, float(request.horizon), request.horizon_unit


def auto_resolution(total_seconds: float) -> str:
    if total_seconds <= 60:
        return "1s"
    if total_seconds <= 6 * 3600:
        return "5min"
    if total_seconds <= 2 * 86400:
        return "1h"
    if total_seconds <= 120 * 86400:
        return "1d"
    if total_seconds <= 400 * 86400:
        return "1w"
    return "1mo"


def validate_point_budget(total_seconds: float, step_seconds: float) -> int:
    points = max(1, int(math.ceil(total_seconds / step_seconds)))
    if points > MAX_POINTS:
        suggested = auto_resolution(total_seconds)
        raise HTTPException(
            400,
            f"requested horizon/resolution would produce {points:,} points; maximum is {MAX_POINTS}. "
            f"Use a coarser resolution such as {suggested}.",
        )
    return points


def filtered_frame(facility: str, department: str | None) -> pd.DataFrame:
    assert data is not None
    frame = data[data["facility"].str.casefold() == facility.casefold()].copy()
    if department:
        frame = frame[frame["department"].str.casefold() == department.casefold()]
    if frame.empty:
        raise HTTPException(404, "no synthetic data for selection")
    return frame


def aggregate_series(
    frame: pd.DataFrame,
    column: str,
    aggregate: str,
    start: pd.Timestamp | None = None,
    end: pd.Timestamp | None = None,
    step_seconds: float | None = None,
) -> pd.Series:
    selected = frame
    if start is not None:
        selected = selected[selected["timestamp"] > start]
    if end is not None:
        selected = selected[selected["timestamp"] <= end]
    grouped = selected.groupby("timestamp")[column]
    base = grouped.sum() if aggregate == "sum" else grouped.mean()
    base = base.sort_index().astype(float)
    if step_seconds is None or abs(step_seconds - source_resolution_seconds) < 1e-9:
        return base
    if step_seconds < source_resolution_seconds:
        return base
    rule = pandas_rule(step_seconds)
    if aggregate == "sum":
        return base.resample(rule).sum().astype(float)
    return base.resample(rule).mean().interpolate().ffill().bfill().astype(float)


def pandas_rule(seconds: float) -> str:
    if seconds < 1:
        return f"{max(1, round(seconds * 1000))}ms"
    if seconds < 60:
        return f"{max(1, round(seconds))}s"
    if seconds < 3600:
        return f"{max(1, round(seconds / 60))}min"
    if seconds < 86400:
        return f"{max(1, round(seconds / 3600))}h"
    if seconds < 604800:
        return f"{max(1, round(seconds / 86400))}D"
    if seconds < 2500000:
        return f"{max(1, round(seconds / 604800))}W"
    return f"{max(1, round(seconds / 2629800))}MS"


def lag_profile(step_seconds: float, length: int) -> list[int]:
    if step_seconds <= 3600:
        candidates = [1, 2, 3, 24, 48, 168]
    elif step_seconds <= 6 * 3600:
        candidates = [1, 2, 4, 28]
    elif step_seconds <= 86400:
        candidates = [1, 2, 7, 14, 28, 365]
    elif step_seconds <= 604800:
        candidates = [1, 2, 4, 8, 26, 52]
    else:
        candidates = [1, 2, 3, 6, 12, 24]
    usable = [lag for lag in candidates if lag < max(length - 3, 2)]
    return usable or [1]


def predict_ridge(values: np.ndarray, horizon: int, step_seconds: float):
    values = np.asarray(values, dtype=np.float64)
    lags = lag_profile(step_seconds, len(values))
    max_lag = max(lags)
    if len(values) <= max_lag + 3:
        level = float(np.mean(values[-min(len(values), 12):]))
        residual_std = float(np.std(values)) if len(values) > 1 else max(level * 0.1, 1e-6)
        points = np.full(horizon, level, dtype=float)
    else:
        rows = []
        targets = []
        for index in range(max_lag, len(values)):
            rows.append([values[index - lag] for lag in lags])
            targets.append(values[index])
        x = np.asarray(rows, dtype=np.float64)
        y = np.asarray(targets, dtype=np.float64)
        x = np.column_stack([np.ones(len(x)), x])
        penalty = np.eye(x.shape[1], dtype=np.float64) * float(forecaster["alpha"])
        penalty[0, 0] = 0
        weights = np.linalg.solve(x.T @ x + penalty, x.T @ y)
        fitted = x @ weights
        residual_std = float(np.std(y - fitted))
        history = list(values)
        generated = []
        for _ in range(horizon):
            features = np.asarray([1.0, *[history[-lag] for lag in lags]], dtype=np.float64)
            value = float(features @ weights)
            generated.append(value)
            history.append(value)
        points = np.asarray(generated, dtype=float)
    spread = 1.645 * max(residual_std, 1e-6) * np.sqrt(1.0 + np.arange(horizon) / max(horizon, 1) * 0.35)
    return points, points - spread, points + spread


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


def predict_chronos(dates: pd.Index, values: np.ndarray, horizon: int, step_seconds: float):
    context = pd.DataFrame({"item_id": "series", "timestamp": dates.to_numpy(), "target": values})
    output = forecaster.predict_df(
        context,
        prediction_length=horizon,
        quantile_levels=[0.1, 0.5, 0.9],
        id_column="item_id",
        timestamp_column="timestamp",
        target="target",
        batch_size=1,
        freq=pandas_rule(step_seconds),
    )
    return (
        output["predictions"].to_numpy(dtype=float),
        output["0.1"].to_numpy(dtype=float),
        output["0.9"].to_numpy(dtype=float),
    )


def predict(values: np.ndarray, dates: pd.Index, horizon: int, step_seconds: float):
    if BACKEND == "timesfm3":
        return predict_timesfm(values, horizon)
    if BACKEND == "chronos2":
        return predict_chronos(dates, values, horizon, step_seconds)
    return predict_ridge(values, horizon, step_seconds)


def output_timestamps(as_of: pd.Timestamp, step_seconds: float, points: int) -> list[pd.Timestamp]:
    return [as_of + pd.to_timedelta(step_seconds * (index + 1), unit="s") for index in range(points)]


def actual_for_intervals(
    frame: pd.DataFrame,
    column: str,
    aggregate: str,
    as_of: pd.Timestamp,
    timestamps: list[pd.Timestamp],
) -> list[float | None]:
    actuals: list[float | None] = []
    previous = as_of
    for current in timestamps:
        window = frame[(frame["timestamp"] > previous) & (frame["timestamp"] <= current)]
        if window.empty:
            actuals.append(None)
        elif aggregate == "sum":
            actuals.append(float(window[column].sum()))
        else:
            actuals.append(float(window[column].mean()))
        previous = current
    return actuals


def backtest_metrics(forecast_values: np.ndarray, lower: np.ndarray, upper: np.ndarray, actuals: list[float | None]):
    pairs = [
        (float(pred), float(lo), float(hi), float(actual))
        for pred, lo, hi, actual in zip(forecast_values, lower, upper, actuals)
        if actual is not None
    ]
    if not pairs:
        return {"available": False, "points_compared": 0}
    pred = np.asarray([item[0] for item in pairs])
    lo = np.asarray([item[1] for item in pairs])
    hi = np.asarray([item[2] for item in pairs])
    actual = np.asarray([item[3] for item in pairs])
    errors = pred - actual
    return {
        "available": True,
        "points_compared": len(pairs),
        "mae": round(float(np.mean(np.abs(errors))), 3),
        "rmse": round(float(np.sqrt(np.mean(errors ** 2))), 3),
        "bias": round(float(np.mean(errors)), 3),
        "interval_coverage": round(float(np.mean((actual >= lo) & (actual <= hi))), 4),
    }


@app.post("/forecast")
def forecast(request: ForecastRequest):
    assert data is not None
    started = time.perf_counter()
    facility = canonical_facility(request.facility)
    department = canonical_department(request.department)
    column, aggregate = metric_column(request.metric, request.disease)
    frame = filtered_frame(facility, department)

    total_seconds, horizon_value, horizon_unit = horizon_seconds(request)
    requested_resolution = auto_resolution(total_seconds) if request.resolution == "auto" else request.resolution
    output_step = resolution_seconds(requested_resolution)
    output_points = validate_point_budget(total_seconds, output_step)

    data_start = frame["timestamp"].min()
    data_end = frame["timestamp"].max()
    if request.as_of is None:
        now = normalize_timestamp(pd.Timestamp.now(tz="UTC"))
        as_of = min(now, data_end)
    else:
        as_of = normalize_timestamp(request.as_of)
    if as_of < data_start:
        raise HTTPException(400, f"as_of predates available synthetic history ({data_start.isoformat()})")
    effective_history_end = min(as_of, data_end)

    # We cannot learn millisecond/second/minute variation from hourly source data.
    # Forecast at the source cadence and derive a continuous intensity/state for
    # finer display resolutions instead of inventing fake precise event timing.
    model_step = max(output_step, source_resolution_seconds)
    model_horizon = max(1, int(math.ceil(total_seconds / model_step)))
    history_start = effective_history_end - pd.Timedelta(days=HISTORY_DAYS)
    history = aggregate_series(
        frame,
        column,
        aggregate,
        start=history_start,
        end=effective_history_end,
        step_seconds=model_step,
    )
    if len(history) < 4:
        raise HTTPException(400, "not enough history for selected as_of time")

    model_points, model_lower, model_upper = predict(
        history.to_numpy(dtype=np.float32),
        history.index,
        model_horizon,
        model_step,
    )
    model_points = np.maximum(model_points, 0)
    model_lower = np.maximum(model_lower, 0)
    model_upper = np.maximum(model_upper, 0)

    timestamps = output_timestamps(as_of, output_step, output_points)
    points = np.empty(output_points, dtype=float)
    lower = np.empty(output_points, dtype=float)
    upper = np.empty(output_points, dtype=float)
    scale = output_step / model_step if aggregate == "sum" and output_step < model_step else 1.0
    for index in range(output_points):
        model_index = min(int((index * output_step) // model_step), model_horizon - 1)
        points[index] = model_points[model_index] * scale
        lower[index] = model_lower[model_index] * scale
        upper[index] = model_upper[model_index] * scale

    if output_step < source_resolution_seconds:
        resolution_semantics = "derived_intensity" if aggregate == "sum" else "interpolated_state"
    else:
        resolution_semantics = "native_aggregate_forecast"

    actuals: list[float | None] = [None] * output_points
    backtest = {"available": False, "points_compared": 0}
    if request.include_actuals and output_step >= source_resolution_seconds and as_of < data_end:
        actuals = actual_for_intervals(frame, column, aggregate, as_of, timestamps)
        backtest = backtest_metrics(points, lower, upper, actuals)
    elif request.include_actuals and output_step < source_resolution_seconds:
        backtest = {
            "available": False,
            "points_compared": 0,
            "reason": f"source history is {human_resolution(source_resolution_seconds)}; sub-source actuals are not fabricated",
        }

    expected = float(points.sum()) if aggregate == "sum" else float(points.mean())
    p10 = float(lower.sum()) if aggregate == "sum" else float(lower.mean())
    p90 = float(upper.sum()) if aggregate == "sum" else float(upper.mean())

    intensity = None
    if aggregate == "sum" and len(model_points):
        rate_per_second = float(model_points[0] / model_step)
        intensity = {
            "expected_per_second": round(rate_per_second, 8),
            "expected_per_minute": round(rate_per_second * 60, 6),
            "expected_per_hour": round(rate_per_second * 3600, 4),
            "probability_at_least_one_next_minute": round(1.0 - math.exp(-max(rate_per_second, 0) * 60), 6),
            "interpretation": "continuous expected event intensity, not an exact patient-arrival timestamp",
        }

    series = []
    for timestamp, point, lo, hi, actual in zip(timestamps, points, lower, upper, actuals):
        item = {
            "timestamp": timestamp.isoformat(),
            "date": timestamp.isoformat(),
            "forecast": round(float(point), 6 if output_step < 60 else 3),
            "p10": round(float(lo), 6 if output_step < 60 else 3),
            "p90": round(float(hi), 6 if output_step < 60 else 3),
        }
        if actual is not None:
            item["actual"] = round(float(actual), 3)
        series.append(item)

    return {
        "facility": facility,
        "department": department,
        "metric": request.metric,
        "disease": request.disease,
        "horizon": {"value": horizon_value, "unit": horizon_unit, "seconds": round(total_seconds, 3)},
        "horizon_days": round(total_seconds / 86400, 6),
        "resolution": requested_resolution,
        "resolution_seconds": output_step,
        "resolution_semantics": resolution_semantics,
        "source_resolution": human_resolution(source_resolution_seconds),
        "as_of": as_of.isoformat(),
        "effective_history_end": effective_history_end.isoformat(),
        "expected": round(expected, 4),
        "p10": round(p10, 4),
        "p90": round(p90, 4),
        "series": series,
        "intensity": intensity,
        "backtest": backtest,
        "latency_ms": int((time.perf_counter() - started) * 1000),
        "data_mode": "synthetic",
        "model": MODEL_NAME,
        "backend": BACKEND,
        "history_points_used": len(history),
        "history_start": history.index[0].isoformat(),
        "history_end": history.index[-1].isoformat(),
        "data_start": data_start.isoformat(),
        "data_end": data_end.isoformat(),
    }


@app.post("/history")
def history(request: HistoryRequest):
    assert data is not None
    facility = canonical_facility(request.facility)
    department = canonical_department(request.department)
    column, aggregate = metric_column(request.metric, request.disease)
    frame = filtered_frame(facility, department)
    start = normalize_timestamp(request.start)
    end = normalize_timestamp(request.end)
    if end <= start:
        raise HTTPException(400, "history end must be after start")
    step = resolution_seconds(request.resolution)
    if step < source_resolution_seconds:
        raise HTTPException(400, f"historical observations are available at {human_resolution(source_resolution_seconds)} or coarser")
    validate_point_budget((end - start).total_seconds(), step)
    series = aggregate_series(frame, column, aggregate, start=start - pd.to_timedelta(step, unit="s"), end=end, step_seconds=step)
    series = series[(series.index >= start) & (series.index <= end)]
    return {
        "facility": facility,
        "department": department,
        "metric": request.metric,
        "disease": request.disease,
        "start": start.isoformat(),
        "end": end.isoformat(),
        "resolution": request.resolution,
        "source_resolution": human_resolution(source_resolution_seconds),
        "data_mode": "synthetic",
        "series": [
            {"timestamp": timestamp.isoformat(), "actual": round(float(value), 3)}
            for timestamp, value in series.items()
        ],
    }
