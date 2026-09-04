from pathlib import Path

import numpy as np
import pandas as pd

rng = np.random.default_rng(42)
out = Path(__file__).resolve().parents[1] / "data" / "synthetic_hospital.csv"
out.parent.mkdir(parents=True, exist_ok=True)

dates = pd.date_range("2023-09-01", "2026-08-31", freq="D")
departments = {
    "A&E": {"base": 58, "beds": 24, "staff": 9},
    "Outpatient": {"base": 72, "beds": 8, "staff": 11},
    "Medical Ward": {"base": 25, "beds": 36, "staff": 10},
    "Surgical Ward": {"base": 19, "beds": 28, "staff": 8},
    "Pediatrics": {"base": 22, "beds": 18, "staff": 7},
}

rows = []
for date in dates:
    day = date.dayofweek
    annual = np.sin(2 * np.pi * date.dayofyear / 365.25)
    trend = (date - dates[0]).days / len(dates)
    holiday = int((date.month, date.day) in {(1, 1), (9, 19), (12, 25), (12, 26)})
    surge = 1.0 + (rng.uniform(0.18, 0.45) if rng.random() < 0.018 else 0.0)
    for department, cfg in departments.items():
        weekend = day >= 5
        if department == "A&E":
            weekday_factor = 1.12 if weekend else 1.0
        elif department == "Outpatient":
            weekday_factor = 0.18 if weekend else 1.0
        else:
            weekday_factor = 0.92 if weekend else 1.0
        holiday_factor = 0.45 if holiday and department == "Outpatient" else 1.05 if holiday and department == "A&E" else 1.0
        seasonal = 1.0 + 0.10 * annual
        demand = cfg["base"] * weekday_factor * holiday_factor * seasonal * (1.0 + 0.06 * trend) * surge
        arrivals = max(1, int(rng.normal(demand, max(2.0, demand * 0.08))))
        admissions = max(0, int(rng.normal(arrivals * (0.22 if department == "A&E" else 0.10), 2.0)))
        discharges = max(0, int(rng.normal(admissions * 0.95, 1.5)))
        occupancy = np.clip(58 + arrivals * 0.6 + admissions * 0.9 - discharges * 0.5 + rng.normal(0, 5), 20, 100)
        staff = max(2, int(round(cfg["staff"] * (1.0 + 0.08 * annual) + rng.normal(0, 1))))
        pressure = arrivals / max(staff, 1)
        wait = max(5, int(12 + pressure * 4.5 + rng.normal(0, 5)))
        respiratory = max(0, int(rng.poisson(max(0.5, arrivals * (0.08 + 0.05 * max(annual, 0))))))
        gastro = max(0, int(rng.poisson(max(0.3, arrivals * (0.035 + 0.01 * max(-annual, 0))))))
        diabetes = max(0, int(rng.poisson(max(0.4, arrivals * 0.045))))
        hypertension = max(0, int(rng.poisson(max(0.5, arrivals * 0.06))))
        rows.append({
            "date": date.date().isoformat(),
            "facility": "JNF",
            "department": department,
            "patient_arrivals": arrivals,
            "admissions": admissions,
            "discharges": discharges,
            "bed_occupancy": round(float(occupancy), 2),
            "staff_on_shift": staff,
            "avg_wait_minutes": wait,
            "respiratory_cases": respiratory,
            "gastro_cases": gastro,
            "diabetes_cases": diabetes,
            "hypertension_cases": hypertension,
            "holiday": holiday,
        })

pd.DataFrame(rows).to_csv(out, index=False)
print(out)
