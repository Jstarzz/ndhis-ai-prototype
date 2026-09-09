from __future__ import annotations

import json
from pathlib import Path

import numpy as np
import pandas as pd

RNG_SEED = 42
START = "2020-01-01 00:00:00"
END = "2026-12-31 23:00:00"
FACILITY = "JNF"

rng = np.random.default_rng(RNG_SEED)
root = Path(__file__).resolve().parents[1]
out = root / "data" / "synthetic_hospital.csv"
meta_out = root / "data" / "synthetic_hospital.meta.json"
out.parent.mkdir(parents=True, exist_ok=True)

hours = pd.date_range(START, END, freq="h")
departments = {
    "A&E": {"daily_base": 58, "beds": 24, "staff": 9},
    "Outpatient": {"daily_base": 72, "beds": 8, "staff": 11},
    "Medical Ward": {"daily_base": 25, "beds": 36, "staff": 10},
    "Surgical Ward": {"daily_base": 19, "beds": 28, "staff": 8},
    "Pediatrics": {"daily_base": 22, "beds": 18, "staff": 7},
}

# Hourly demand curves are normalized to sum to one day of activity. A&E is
# intentionally flatter overnight, while outpatient demand is concentrated in
# normal clinic hours. These are synthetic operational patterns, not JNF data.
def hourly_profile(department: str, hour: int) -> float:
    if department == "A&E":
        curve = np.array([
            0.025, 0.022, 0.020, 0.020, 0.021, 0.025,
            0.032, 0.041, 0.049, 0.053, 0.056, 0.057,
            0.055, 0.054, 0.055, 0.057, 0.060, 0.061,
            0.060, 0.056, 0.050, 0.044, 0.036, 0.031,
        ], dtype=float)
    elif department == "Outpatient":
        curve = np.array([
            0.001, 0.001, 0.001, 0.001, 0.001, 0.003,
            0.015, 0.055, 0.105, 0.125, 0.130, 0.125,
            0.110, 0.100, 0.090, 0.070, 0.045, 0.015,
            0.003, 0.001, 0.001, 0.001, 0.001, 0.001,
        ], dtype=float)
    else:
        curve = np.array([
            0.025, 0.020, 0.018, 0.018, 0.020, 0.026,
            0.035, 0.050, 0.060, 0.060, 0.055, 0.050,
            0.050, 0.052, 0.055, 0.058, 0.060, 0.060,
            0.055, 0.050, 0.045, 0.038, 0.030, 0.025,
        ], dtype=float)
    return float(curve[hour] / curve.sum())


def outbreak_multiplier(timestamp: pd.Timestamp, department: str) -> float:
    # A few deterministic synthetic respiratory/gastro waves make historical
    # backtesting less trivial than a perfectly stationary sinusoid.
    respiratory_windows = [
        (pd.Timestamp("2021-12-15"), pd.Timestamp("2022-02-20"), 1.18),
        (pd.Timestamp("2023-01-05"), pd.Timestamp("2023-03-01"), 1.14),
        (pd.Timestamp("2025-11-20"), pd.Timestamp("2026-02-10"), 1.20),
    ]
    gastro_windows = [
        (pd.Timestamp("2020-07-01"), pd.Timestamp("2020-08-10"), 1.08),
        (pd.Timestamp("2024-06-10"), pd.Timestamp("2024-07-20"), 1.10),
    ]
    factor = 1.0
    for start, end, scale in respiratory_windows:
        if start <= timestamp <= end:
            factor *= scale if department in {"A&E", "Pediatrics", "Medical Ward"} else 1.04
    for start, end, scale in gastro_windows:
        if start <= timestamp <= end:
            factor *= scale if department in {"A&E", "Pediatrics"} else 1.03
    return factor


rows: list[dict[str, object]] = []
start_ts = hours[0]
span_hours = max(len(hours) - 1, 1)

# Daily surge values are generated once and reused across departments/hours so
# a synthetic high-pressure day affects the hospital coherently.
day_index = pd.date_range(hours[0].normalize(), hours[-1].normalize(), freq="D")
daily_surge: dict[pd.Timestamp, float] = {}
for day in day_index:
    daily_surge[day] = 1.0 + (float(rng.uniform(0.15, 0.42)) if rng.random() < 0.022 else 0.0)

for timestamp in hours:
    annual = np.sin(2 * np.pi * timestamp.dayofyear / 365.25)
    trend = (timestamp - start_ts).total_seconds() / 3600 / span_hours
    weekend = timestamp.dayofweek >= 5
    holiday = int((timestamp.month, timestamp.day) in {(1, 1), (9, 19), (12, 25), (12, 26)})
    surge = daily_surge[timestamp.normalize()]

    for department, cfg in departments.items():
        if department == "A&E":
            weekday_factor = 1.10 if weekend else 1.0
            holiday_factor = 1.07 if holiday else 1.0
        elif department == "Outpatient":
            weekday_factor = 0.16 if weekend else 1.0
            holiday_factor = 0.30 if holiday else 1.0
        else:
            weekday_factor = 0.92 if weekend else 1.0
            holiday_factor = 0.98 if holiday else 1.0

        seasonal = 1.0 + 0.10 * annual
        profile = hourly_profile(department, timestamp.hour)
        demand = (
            cfg["daily_base"]
            * profile
            * weekday_factor
            * holiday_factor
            * seasonal
            * (1.0 + 0.08 * trend)
            * surge
            * outbreak_multiplier(timestamp, department)
        )
        arrivals = int(rng.poisson(max(demand, 0.01)))
        admission_rate = 0.22 if department == "A&E" else 0.10
        admissions = int(rng.binomial(arrivals, min(max(admission_rate, 0.0), 1.0))) if arrivals else 0
        expected_discharges = max(0.0, admissions * 0.92 + cfg["beds"] * 0.010)
        discharges = int(rng.poisson(expected_discharges))

        # Occupancy is a synthetic percentage-like operational signal. It has
        # daily seasonality and demand pressure but is intentionally bounded.
        occupancy = np.clip(
            61
            + 7 * np.sin(2 * np.pi * (timestamp.hour - 8) / 24)
            + arrivals * 1.5
            + admissions * 2.0
            - discharges * 1.1
            + 5 * annual
            + rng.normal(0, 3.0),
            15,
            100,
        )
        shift_factor = 1.18 if 7 <= timestamp.hour < 19 else 0.72
        staff = max(2, int(round(cfg["staff"] * shift_factor * (1.0 + 0.04 * annual) + rng.normal(0, 0.8))))
        pressure = arrivals / max(staff, 1)
        wait = max(3, int(8 + pressure * 14 + rng.normal(0, 4)))

        respiratory_rate = max(0.01, arrivals * (0.08 + 0.05 * max(annual, 0)))
        gastro_rate = max(0.01, arrivals * (0.035 + 0.012 * max(-annual, 0)))
        diabetes_rate = max(0.01, arrivals * 0.045)
        hypertension_rate = max(0.01, arrivals * 0.060)

        rows.append({
            "timestamp": timestamp.isoformat(),
            "date": timestamp.date().isoformat(),
            "facility": FACILITY,
            "department": department,
            "patient_arrivals": arrivals,
            "admissions": admissions,
            "discharges": discharges,
            "bed_occupancy": round(float(occupancy), 2),
            "staff_on_shift": staff,
            "avg_wait_minutes": wait,
            "respiratory_cases": int(rng.poisson(respiratory_rate)),
            "gastro_cases": int(rng.poisson(gastro_rate)),
            "diabetes_cases": int(rng.poisson(diabetes_rate)),
            "hypertension_cases": int(rng.poisson(hypertension_rate)),
            "holiday": holiday,
        })

frame = pd.DataFrame(rows)
frame.to_csv(out, index=False)
meta_out.write_text(
    json.dumps(
        {
            "mode": "synthetic",
            "seed": RNG_SEED,
            "facility": FACILITY,
            "departments": list(departments),
            "start": hours[0].isoformat(),
            "end": hours[-1].isoformat(),
            "source_resolution": "1h",
            "rows": len(frame),
            "note": "Generated synthetic operational history; not patient data or observed JNF data.",
        },
        indent=2,
    )
    + "\n",
    encoding="utf-8",
)
print(f"wrote {len(frame):,} hourly rows to {out}")
print(meta_out)
