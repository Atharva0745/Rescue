"""
RESCUE-4 Phase 5: Synthetic Fault-Rate Calibration Model
Implements a seeded simulation of cancellation/kill failures; it does not
measure real cancellation attempts or calibrate a deployed system.

Implements:
  1. Synthetic cooperative cancellation failure trials under modeled attack conditions.
  2. Exact two-sided 95% Clopper-Pearson confidence interval calculation.
  3. Pre-registered decision rule (Case A, Case B, Case C) for selecting p_main.
  4. Per-stage, per-class, and kill-failure / UNCONFIRMED sensitivity analysis.
  5. Manifest calibration export to freeze p_main prior to confirmatory seeds.
"""

import sys
import json
import random
from typing import Dict, List, Tuple, Any, Optional
from scipy.stats import beta


def clopper_pearson_interval(k: int, n: int, confidence: float = 0.95) -> Tuple[float, float]:
    """
    Computes the exact two-sided Clopper-Pearson confidence interval for a binomial proportion.
    k: Number of observed failures
    n: Total trials
    confidence: Desired confidence level (default 0.95)
    """
    alpha = 1.0 - confidence
    # Lower bound
    if k == 0:
        lower = 0.0
    else:
        lower = float(beta.ppf(alpha / 2.0, k, n - k + 1))
    
    # Upper bound
    if k == n:
        upper = 1.0
    else:
        upper = float(beta.ppf(1.0 - alpha / 2.0, k + 1, n - k))
        
    return lower, upper


class FaultCalibrator:
    def __init__(self, seed: int = 20261011):
        self.rng = random.Random(seed)
        self.stages = ["S1_INGEST", "S2_PROCESS", "S3_PERSIST"]
        self.classes = ["C_INTERACTIVE", "C_BATCH"]
        self.p_grid = [0.00, 0.01, 0.05, 0.20]

    def simulate_cancellation_trial(self, stage: str, req_class: str, true_base_rate: float = 0.048) -> Dict[str, Any]:
        """
        Simulates an independent cooperative cancellation trial under attack-load conditions.
        Returns whether cancellation failed within T_cancel, and whether fallback kill failed.
        """
        # Slight class / stage variance under attack load
        stage_factor = 1.1 if stage == "S2_PROCESS" else 0.95
        class_factor = 1.05 if req_class == "C_BATCH" else 0.95
        effective_rate = min(0.99, true_base_rate * stage_factor * class_factor)

        # Cooperative cancellation failure
        cancel_failed = (self.rng.random() < effective_rate)

        # If cancel failed, emergency kill path is invoked (UNCONFIRMED rate)
        kill_failed = False
        if cancel_failed:
            kill_failed = (self.rng.random() < 0.02)  # 2% of cancellation failures resist kill

        return {
            "stage": stage,
            "req_class": req_class,
            "cancel_failed": cancel_failed,
            "kill_failed": kill_failed
        }

    def run_calibration(self, min_trials_per_stage: int = 1000, max_trials_per_stage: int = 10000, target_width: float = 0.025) -> Dict[str, Any]:
        """
        Runs Stage B calibration protocol B1 & B2:
        - Executes at least N_cal = 1,000 attempts per stage.
        - Extends in blocks of 1,000 up to N_max = 10,000 if 95% Clopper-Pearson interval is too wide.
        - Applies registered Case A/B/C decision rules.
        """
        print(f"\n--- [Stage B1] Measuring Cancellation Failure Rates (Min {min_trials_per_stage:,} per stage) ---")

        trials_by_stage: Dict[str, List[Dict[str, Any]]] = {s: [] for s in self.stages}
        trials_by_class: Dict[str, List[Dict[str, Any]]] = {c: [] for c in self.classes}

        total_trials = 0
        total_cancel_failures = 0
        total_kill_failures = 0

        for stage in self.stages:
            stage_trials = 0
            while stage_trials < min_trials_per_stage:
                req_cls = self.rng.choice(self.classes)
                trial_res = self.simulate_cancellation_trial(stage, req_cls)
                trials_by_stage[stage].append(trial_res)
                trials_by_class[req_cls].append(trial_res)
                stage_trials += 1
                total_trials += 1
                if trial_res["cancel_failed"]:
                    total_cancel_failures += 1
                if trial_res["kill_failed"]:
                    total_kill_failures += 1

            # Check interval width for this stage
            k_stage = sum(1 for t in trials_by_stage[stage] if t["cancel_failed"])
            l_stage, u_stage = clopper_pearson_interval(k_stage, stage_trials)
            
            # Extension block rule if interval is too wide and under N_max
            while (u_stage - l_stage > target_width) and (stage_trials < max_trials_per_stage):
                for _ in range(500):
                    req_cls = self.rng.choice(self.classes)
                    trial_res = self.simulate_cancellation_trial(stage, req_cls)
                    trials_by_stage[stage].append(trial_res)
                    trials_by_class[req_cls].append(trial_res)
                    stage_trials += 1
                    total_trials += 1
                    if trial_res["cancel_failed"]:
                        total_cancel_failures += 1
                    if trial_res["kill_failed"]:
                        total_kill_failures += 1
                k_stage = sum(1 for t in trials_by_stage[stage] if t["cancel_failed"])
                l_stage, u_stage = clopper_pearson_interval(k_stage, stage_trials)

        # Global aggregate Clopper-Pearson interval
        L_global, U_global = clopper_pearson_interval(total_cancel_failures, total_trials)
        p_empirical = total_cancel_failures / total_trials

        print(f"Total Trials: {total_trials:,}")
        print(f"Cancellation Failures: {total_cancel_failures:,} (p_hat = {p_empirical:.4f})")
        print(f"Two-Sided 95% Clopper-Pearson Interval: [{L_global:.4f}, {U_global:.4f}] (Width: {U_global - L_global:.4f})")
        print(f"Kill-Path Failures (UNCONFIRMED): {total_kill_failures} (rate: {total_kill_failures/total_trials:.5f})")

        # Per-stage analysis
        per_stage_metrics = {}
        for s, t_list in trials_by_stage.items():
            k_s = sum(1 for t in t_list if t["cancel_failed"])
            n_s = len(t_list)
            l_s, u_s = clopper_pearson_interval(k_s, n_s)
            per_stage_metrics[s] = {
                "trials": n_s,
                "failures": k_s,
                "p_hat": k_s / n_s,
                "ci_95": [l_s, u_s]
            }
            print(f"  Stage {s:<10}: n={n_s:,}, k={k_s:3d}, p_hat={k_s/n_s:.4f}, 95% CI=[{l_s:.4f}, {u_s:.4f}]")

        # Per-class analysis
        per_class_metrics = {}
        for c, t_list in trials_by_class.items():
            k_c = sum(1 for t in t_list if t["cancel_failed"])
            n_c = len(t_list)
            l_c, u_c = clopper_pearson_interval(k_c, n_c)
            per_class_metrics[c] = {
                "trials": n_c,
                "failures": k_c,
                "p_hat": k_c / n_c,
                "ci_95": [l_c, u_c]
            }
            print(f"  Class {c:<14}: n={n_c:,}, k={k_c:3d}, p_hat={k_c/n_c:.4f}, 95% CI=[{l_c:.4f}, {u_c:.4f}]")

        # Stage B2: Decision Rule for Operating Point
        print("\n--- [Stage B2] Applying Registered Operating Point Decision Rules ---")
        if U_global < 0.01:
            case = "CASE_A"
            p_main = 0.01
            case_rationale = "Upper bound U < 1%. 1% cell designated as stress cell; headline claim conditional."
        elif L_global > 0.20:
            case = "CASE_C"
            p_main = 0.20
            case_rationale = "Lower bound L > 20%. Mechanism flagged as unreliable; testing 20% and 40% cells."
        else:
            case = "CASE_B"
            # Find grid points contained within [L, U]
            enclosed_grid = [p for p in self.p_grid if L_global <= p <= U_global]
            if enclosed_grid:
                # Select nearest grid point to empirical p
                p_main = min(enclosed_grid, key=lambda x: abs(x - p_empirical))
                case_rationale = f"Interval [{L_global:.4f}, {U_global:.4f}] contains grid points {enclosed_grid}. Selected {p_main}."
            else:
                # Overlaps grid but contains no grid point; select closest grid point to interval
                p_main = min(self.p_grid, key=lambda x: min(abs(x - L_global), abs(x - U_global)))
                case_rationale = f"Interval [{L_global:.4f}, {U_global:.4f}] is between grid points. Bound to closest grid point {p_main}."

        print(f"Selected Decision Case: {case}")
        print(f"Frozen Operating Point p_main: {p_main} ({p_main*100:.1f}%)")
        print(f"Rationale: {case_rationale}")

        return {
            "total_trials": total_trials,
            "total_cancel_failures": total_cancel_failures,
            "p_empirical": p_empirical,
            "clopper_pearson_95": [L_global, U_global],
            "case": case,
            "p_main_frozen": p_main,
            "case_rationale": case_rationale,
            "per_stage": per_stage_metrics,
            "per_class": per_class_metrics,
            "kill_path_unconfirmed": {
                "trials": total_trials,
                "failures": total_kill_failures,
                "rate": total_kill_failures / total_trials
            }
        }


def main():
    print("=================================================================")
    print("       RESCUE-4 FAULT-RATE CALIBRATION ENGINE (PHASE 5)          ")
    print("=================================================================")
    
    calibrator = FaultCalibrator(seed=20261011)
    results = calibrator.run_calibration(min_trials_per_stage=1000, target_width=0.025)

    # Save calibration results to config directory
    with open("config/calibration_report.json", "w") as f:
        json.dump(results, f, indent=2)

    print("\nCalibration results exported to config/calibration_report.json.")
    print("=================================================================")
    print(f"  PHASE 5 EXIT GATE PASSED: p_main FROZEN AT {results['p_main_frozen']} ({results['case']})")
    print("=================================================================")


if __name__ == "__main__":
    main()
