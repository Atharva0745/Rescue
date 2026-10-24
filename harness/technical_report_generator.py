#!/usr/bin/env python3
"""Generate a data-driven report for the repaired exploratory simulation."""

import hashlib
import json
import math
import os
import time
from typing import Any, Dict, List


PROJECT_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
RESULTS_PATH = os.path.join(
    PROJECT_ROOT, "config", "confirmatory_results_repaired_exploratory.json"
)
OUTPUT_PATH = os.path.join(
    PROJECT_ROOT, "config", "technical_report_exploratory.json"
)


def file_sha256(path: str) -> str:
    digest = hashlib.sha256()
    with open(path, "rb") as source:
        for chunk in iter(lambda: source.read(65536), b""):
            digest.update(chunk)
    return digest.hexdigest()


def summary(values: List[float]) -> Dict[str, float]:
    if not values:
        raise ValueError("cannot summarize an empty metric")
    mean = sum(values) / len(values)
    variance = (
        sum((value - mean) ** 2 for value in values) / (len(values) - 1)
        if len(values) > 1
        else 0.0
    )
    return {
        "n": len(values),
        "mean": round(mean, 6),
        "sd": round(math.sqrt(variance), 6),
        "min": round(min(values), 6),
        "max": round(max(values), 6),
    }


def generate_technical_report(
    results_path: str = RESULTS_PATH,
    output_path: str = OUTPUT_PATH,
) -> Dict[str, Any]:
    if not os.path.isfile(results_path):
        raise FileNotFoundError(f"exploratory results not found: {results_path}")

    with open(results_path, "r", encoding="utf-8") as source:
        results = json.load(source)
    if results.get("meta", {}).get("status") != "EXPLORATORY_NOT_CONFIRMATORY":
        raise ValueError("report input is not explicitly marked exploratory")

    records = results.get("per_seed_records")
    if not isinstance(records, list) or not records:
        raise ValueError("exploratory results contain no per-seed records")

    arm_names = ("A'-TR", "D-TR", "D-OC", "B-TR", "B-OC")
    arm_metrics: Dict[str, Dict[str, List[float]]] = {
        arm: {"S_L": [], "OE": [], "OE_norm": [], "stale_writes": []}
        for arm in arm_names
    }
    for record in records:
        arms = record.get("arms")
        if not isinstance(arms, dict):
            raise ValueError("per-seed record is missing its arm results")
        for arm in arm_names:
            if arm not in arms:
                raise ValueError(f"per-seed record is missing arm {arm}")
            values = arms[arm]
            arm_metrics[arm]["S_L"].append(float(values["S_L"]))
            arm_metrics[arm]["OE"].append(float(values["OE"]))
            arm_metrics[arm]["OE_norm"].append(float(values["OE_norm"]))
            arm_metrics[arm]["stale_writes"].append(
                float(values["stale_writes_committed"])
            )

    report = {
        "meta": {
            "document": "RESCUE-4 Exploratory Simulation Report",
            "status": "EXPLORATORY_NOT_CONFIRMATORY",
            "generated_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            "n_seeds": len(records),
            "interpretation": results["meta"]["interpretation"],
            "joint_claim_status": "NOT_ASSESSED",
        },
        "provenance": {
            "results_file": os.path.relpath(results_path, PROJECT_ROOT),
            "results_sha256": file_sha256(results_path),
            "simulator_sha256": file_sha256(
                os.path.join(PROJECT_ROOT, "harness", "confirmatory_simulator.py")
            ),
            "reference_preregistration_sha256": results["meta"].get(
                "pre_registration_sha256"
            ),
        },
        "arm_metrics": {
            arm: {
                metric: summary(values)
                for metric, values in metrics.items()
            }
            for arm, metrics in arm_metrics.items()
        },
        "hypothesis_decisions": results.get("hypothesis_decisions", {}),
        "joint_novelty_claim": results.get("joint_novelty_claim", {}),
        "fault_sweep": results.get("fault_sweep_secondary", {}),
        "database_validation": {
            "status": "NOT_ESTABLISHED_BY_THIS_RERUN",
            "note": (
                "The simulation is not a PostgreSQL integration test. "
                "The database validator uses an in-memory mock."
            ),
        },
    }

    with open(output_path, "w", encoding="utf-8") as output:
        json.dump(report, output, indent=2)
    print(f"Exploratory report written to {os.path.relpath(output_path, PROJECT_ROOT)}")
    return report


if __name__ == "__main__":
    generate_technical_report()
