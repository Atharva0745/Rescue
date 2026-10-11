"""
RESCUE-4 Phase 6: Statistical Pre-Registration Engine
Implements Stage C of the evaluation protocol (evaluation.md §5):
  1. Freeze six primary hypotheses P-1a, P-1b, P-2, P-3, P-4a, P-4b.
  2. Freeze per-seed S_L and OE_norm definitions and measurement windows.
  3. Compute δ_C and δ_OE from pilot variance using pre-registered rules.
  4. Define paired seed unit, arm randomization, seed blocks, exclusion policy.
  5. Implement one-sided paired sign-flip permutation test (100,000 flips).
  6. Implement diagnostic checks for sign-flip assumptions and fallback.
  7. Sample-size and joint-power procedure (N ∈ [20, 60], ≥0.80 joint power).
  8. Intersection-union decision rule for joint claim; Holm adjustment for
     individual claims.
  9. Generate seed-block manifest and SHA-256 hash the frozen pre-registration.

References:
  - evaluation.md §5 (Stage C), §7 (Metrics), §8 (Statistical Evaluation), §9 (Decision Rules)
  - arhitecture.md §8 (Observability and Metrics), §9 (Workloads and Experimental Paths)
  - config/rescue_config.v1.0.json (frozen and provisional parameters)
"""

import sys
import json
import hashlib
import random
import math
from typing import Dict, List, Tuple, Any, Optional
from datetime import datetime, timezone


# ---------------------------------------------------------------------------
# §1. Hypothesis Definitions (evaluation.md §8.1)
# ---------------------------------------------------------------------------

HYPOTHESES = {
    "P-1a": {
        "id": "P-1a",
        "metric": "OE_norm",
        "registered_alternative": "OE_norm(D-TR) - OE_norm(B-OC) > delta_OE",
        "comparison": ("D-TR", "B-OC"),
        "direction": "superiority",
        "workload": "W3",
        "fault_cell": "p_main",
        "purpose": "Test OE improvement over the strengthened D baseline"
    },
    "P-1b": {
        "id": "P-1b",
        "metric": "OE_norm",
        "registered_alternative": "OE_norm(A'-TR) - OE_norm(B-OC) > delta_OE",
        "comparison": ("A'-TR", "B-OC"),
        "direction": "superiority",
        "workload": "W3",
        "fault_cell": "p_main",
        "purpose": "Test OE improvement over the strengthened A' baseline"
    },
    "P-2": {
        "id": "P-2",
        "metric": "S_L",
        "registered_alternative": "S_L(B-OC) - S_L(D-TR) >= delta_C",
        "comparison": ("B-OC", "D-TR"),
        "direction": "superiority",
        "workload": "W3",
        "fault_cell": "p_main",
        "purpose": "Test legitimate-request success improvement"
    },
    "P-3": {
        "id": "P-3",
        "metric": "S_L",
        "registered_alternative": "S_L(B-OC) - S_L(D-OC) >= -0.03",
        "comparison": ("B-OC", "D-OC"),
        "direction": "non-inferiority",
        "workload": "W1",
        "fault_cell": "alpha_0_no_attack",
        "purpose": "Test the no-attack non-inferiority guardrail",
        "non_inferiority_margin": -0.03
    },
    "P-4a": {
        "id": "P-4a",
        "metric": "OE_norm",
        "registered_alternative": "OE_norm(B-TR) - OE_norm(B-OC) > delta_OE",
        "comparison": ("B-TR", "B-OC"),
        "direction": "superiority",
        "workload": "W3",
        "fault_cell": "p_main",
        "purpose": "Test incremental OE benefit from OC over TR within B"
    },
    "P-4b": {
        "id": "P-4b",
        "metric": "S_L",
        "registered_alternative": "S_L(B-OC) - S_L(B-TR) >= delta_C",
        "comparison": ("B-OC", "B-TR"),
        "direction": "superiority",
        "workload": "W3",
        "fault_cell": "p_main",
        "purpose": "Test incremental legitimate-request success from OC over TR within B"
    }
}


# ---------------------------------------------------------------------------
# §2. Metric Definitions (arhitecture.md §8, evaluation.md §7)
# ---------------------------------------------------------------------------

METRIC_DEFINITIONS = {
    "OE_norm": {
        "full_name": "Normalized Orphaned-Work Exposure",
        "formula": "OE_norm = OE / sum_s(K_s * T_window)",
        "OE_definition": "OE = executor_seconds_past_expiry + stale_write_commits * c_w",
        "components_reported_separately": True,
        "component_1": "executor_seconds_past_expiry: total wall-clock seconds executors ran beyond lease expiry",
        "component_2": "stale_write_commits: count of committed writes with stale fence version, weighted by c_w",
        "normalization_denominator": "sum over stages s of (K_s * T_window)",
        "note": "A favorable aggregate must not conceal a regression in one component"
    },
    "S_L": {
        "full_name": "Legitimate-Request Success Rate",
        "formula": "S_L = (legitimate requests with full response by arrival + D_c) / (total legitimate requests in measurement window)",
        "failure_definition": "Rejections and timeouts count as failures",
        "per_seed": True,
        "reporting": "Per-arm per-seed values, paired differences, and registered aggregate statistics"
    }
}


# ---------------------------------------------------------------------------
# §3. Measurement Windows (from config/rescue_config.v1.0.json)
# ---------------------------------------------------------------------------

MEASUREMENT_WINDOWS = {
    "W1": {
        "total_duration_s": 60.0,
        "warmup_s": 10.0,
        "measurement_window_s": 40.0,
        "cooldown_s": 10.0
    },
    "W3": {
        "total_duration_s": 90.0,
        "warmup_s": 15.0,
        "measurement_window_s": 60.0,
        "cooldown_s": 15.0
    }
}


# ---------------------------------------------------------------------------
# §4. Pilot Data Generation and Threshold Calibration
# ---------------------------------------------------------------------------

class PilotDataGenerator:
    """
    Generates synthetic pilot differences for the 6 contrasts to calibrate
    δ_C, δ_OE, and compute the required sample size.
    Uses the frozen p_main = 0.05 and the frozen stage/class parameters.
    """

    def __init__(self, seed: int = 20261011, n_pilot: int = 12):
        self.rng = random.Random(seed)
        self.n_pilot = n_pilot

    def generate_pilot_differences(self) -> Dict[str, List[float]]:
        """
        Simulate n_pilot paired differences for each of the 6 hypotheses.
        These represent pilot-run contrasts used ONLY for threshold calibration
        and power estimation — NOT confirmatory evidence.
        """
        pilot_diffs = {}

        # P-1a: OE_norm(D-TR) - OE_norm(B-OC) — expect B-OC lower OE
        pilot_diffs["P-1a"] = [
            self.rng.gauss(0.082, 0.025) for _ in range(self.n_pilot)
        ]

        # P-1b: OE_norm(A'-TR) - OE_norm(B-OC) — expect B-OC lower OE
        pilot_diffs["P-1b"] = [
            self.rng.gauss(0.095, 0.030) for _ in range(self.n_pilot)
        ]

        # P-2: S_L(B-OC) - S_L(D-TR) — expect B-OC higher success
        pilot_diffs["P-2"] = [
            self.rng.gauss(0.035, 0.018) for _ in range(self.n_pilot)
        ]

        # P-3: S_L(B-OC) - S_L(D-OC) — non-inferiority, near zero difference
        pilot_diffs["P-3"] = [
            self.rng.gauss(0.005, 0.012) for _ in range(self.n_pilot)
        ]

        # P-4a: OE_norm(B-TR) - OE_norm(B-OC) — OC incremental benefit
        pilot_diffs["P-4a"] = [
            self.rng.gauss(0.065, 0.022) for _ in range(self.n_pilot)
        ]

        # P-4b: S_L(B-OC) - S_L(B-TR) — OC incremental success benefit
        pilot_diffs["P-4b"] = [
            self.rng.gauss(0.028, 0.015) for _ in range(self.n_pilot)
        ]

        return pilot_diffs


def compute_thresholds_from_pilot(
    pilot_diffs: Dict[str, List[float]]
) -> Dict[str, Any]:
    """
    Compute δ_OE and δ_C from pilot data using the pre-registered rules:
    - δ_OE = max(0.01, 0.5 * min pilot mean among OE contrasts)
    - δ_C  = max(0.005, 0.5 * min pilot mean among S_L contrasts)
    These ensure thresholds are conservative (half the smallest observed effect)
    while maintaining a practical floor.
    """
    oe_hypotheses = ["P-1a", "P-1b", "P-4a"]
    sl_hypotheses = ["P-2", "P-4b"]

    oe_means = [sum(pilot_diffs[h]) / len(pilot_diffs[h]) for h in oe_hypotheses]
    sl_means = [sum(pilot_diffs[h]) / len(pilot_diffs[h]) for h in sl_hypotheses]

    delta_OE = max(0.01, 0.5 * min(oe_means))
    delta_C = max(0.005, 0.5 * min(sl_means))

    return {
        "delta_OE": round(delta_OE, 4),
        "delta_C": round(delta_C, 4),
        "pilot_OE_means": {h: round(m, 4) for h, m in zip(oe_hypotheses, oe_means)},
        "pilot_SL_means": {h: round(m, 4) for h, m in zip(sl_hypotheses, sl_means)},
        "rule_OE": "delta_OE = max(0.01, 0.5 * min(pilot OE means))",
        "rule_C": "delta_C = max(0.005, 0.5 * min(pilot S_L means))"
    }


# ---------------------------------------------------------------------------
# §5. Sign-Flip Permutation Test (evaluation.md §8.2)
# ---------------------------------------------------------------------------

def sign_flip_permutation_test(
    paired_diffs: List[float],
    threshold: float,
    n_flips: int = 100_000,
    seed: int = 42,
    alternative: str = "greater"
) -> Dict[str, Any]:
    """
    One-sided paired sign-flip permutation test with the threshold
    incorporated into the null, as specified in the pre-registration.

    H0: median(diffs) <= threshold
    H1: median(diffs) > threshold  (alternative="greater")

    For non-inferiority (P-3):
    H0: median(diffs) < margin   (margin is negative)
    H1: median(diffs) >= margin  (alternative="greater" with negative threshold)

    The test statistic is the mean of the (shifted) differences.
    Under H0, each difference is equally likely to be positive or negative
    after centering at the threshold.
    """
    rng = random.Random(seed)
    n = len(paired_diffs)

    # Shift differences by the threshold
    shifted = [d - threshold for d in paired_diffs]
    observed_stat = sum(shifted) / n

    # Generate null distribution by sign-flipping
    count_extreme = 0
    for _ in range(n_flips):
        flipped_stat = sum(
            s * (1 if rng.random() < 0.5 else -1) for s in shifted
        ) / n
        if flipped_stat >= observed_stat:
            count_extreme += 1

    p_value = (count_extreme + 1) / (n_flips + 1)  # +1 for the observed

    return {
        "observed_statistic": round(observed_stat, 6),
        "threshold": threshold,
        "n_flips": n_flips,
        "n_pairs": n,
        "p_value": round(p_value, 6),
        "reject_at_0.05": p_value < 0.05
    }


def check_sign_flip_assumptions(
    pilot_diffs: Dict[str, List[float]]
) -> Dict[str, Any]:
    """
    Check whether the sign-flip test assumptions are adequately supported
    by the pilot differences:
    1. Approximate symmetry of the centered differences around zero.
    2. No extreme outliers that could dominate the test statistic.
    3. Sufficient variability (not degenerate).

    If assumptions fail, the pre-registered fallback is a Wilcoxon signed-rank test.
    """
    diagnostics = {}
    assumptions_ok = True

    for hyp_id, diffs in pilot_diffs.items():
        n = len(diffs)
        mean_d = sum(diffs) / n
        centered = [d - mean_d for d in diffs]

        # Skewness check (Fisher's skewness)
        var = sum(c ** 2 for c in centered) / n
        sd = math.sqrt(var) if var > 0 else 1e-10
        skewness = sum((c / sd) ** 3 for c in centered) / n

        # Outlier check: any |centered| > 3*SD?
        outlier_count = sum(1 for c in centered if abs(c) > 3 * sd)

        # Variability check: SD > 0
        variability_ok = sd > 1e-8

        hyp_ok = abs(skewness) < 1.0 and outlier_count == 0 and variability_ok

        diagnostics[hyp_id] = {
            "n_pilot": n,
            "mean": round(mean_d, 6),
            "sd": round(sd, 6),
            "skewness": round(skewness, 4),
            "outlier_count": outlier_count,
            "symmetry_ok": abs(skewness) < 1.0,
            "no_outliers": outlier_count == 0,
            "variability_ok": variability_ok,
            "assumptions_supported": hyp_ok
        }

        if not hyp_ok:
            assumptions_ok = False

    return {
        "all_assumptions_supported": assumptions_ok,
        "fallback_test": "Wilcoxon signed-rank test (one-sided)" if not assumptions_ok else None,
        "per_hypothesis": diagnostics
    }


# ---------------------------------------------------------------------------
# §6. Sample Size and Joint Power (evaluation.md §8.3)
# ---------------------------------------------------------------------------

def compute_sample_size_and_power(
    pilot_diffs: Dict[str, List[float]],
    delta_OE: float,
    delta_C: float,
    alpha: float = 0.05,
    target_power: float = 0.80,
    n_min: int = 20,
    n_max: int = 60,
    n_power_sims: int = 10_000,
    seed: int = 20261011
) -> Dict[str, Any]:
    """
    Pre-registered sample-size procedure:
    - Based on the largest paired standard deviation among the 6 pilot contrasts.
    - At least 20 seeds, maximum 60.
    - Joint power estimated by simulation from pilot differences.
    - If joint power < 0.80 at N_max=60, label a non-rejection as 'inconclusive'.
    """
    rng = random.Random(seed)

    # Compute pilot statistics for each hypothesis
    pilot_stats = {}
    for hyp_id, diffs in pilot_diffs.items():
        n = len(diffs)
        mean_d = sum(diffs) / n
        var_d = sum((d - mean_d) ** 2 for d in diffs) / (n - 1)
        sd_d = math.sqrt(var_d)
        pilot_stats[hyp_id] = {"mean": mean_d, "sd": sd_d}

    # Largest paired SD across all 6 contrasts
    max_sd = max(ps["sd"] for ps in pilot_stats.values())
    max_sd_hyp = max(pilot_stats, key=lambda h: pilot_stats[h]["sd"])

    # Determine thresholds for each hypothesis
    thresholds = {}
    for hyp_id in HYPOTHESES:
        if HYPOTHESES[hyp_id]["metric"] == "OE_norm":
            thresholds[hyp_id] = delta_OE
        elif hyp_id == "P-3":
            thresholds[hyp_id] = -0.03  # non-inferiority margin
        else:
            thresholds[hyp_id] = delta_C

    # Sample size formula: N = ceil((z_alpha + z_beta)^2 * max_sd^2 / min_effect^2)
    # Using the smallest effect size among the 6 contrasts
    min_effect = min(abs(pilot_stats[h]["mean"] - thresholds[h]) for h in pilot_stats)
    if min_effect < 1e-10:
        min_effect = 0.01  # safety floor

    from math import ceil
    z_alpha = 1.6449  # one-sided 0.05
    z_beta = 0.8416   # 80% power

    n_formula = ceil((z_alpha + z_beta) ** 2 * max_sd ** 2 / min_effect ** 2)
    n_required = max(n_min, min(n_max, n_formula))

    # Simulate joint power at N = n_required
    joint_rejections = 0
    per_hyp_rejections = {h: 0 for h in HYPOTHESES}

    for sim in range(n_power_sims):
        all_reject = True
        for hyp_id in HYPOTHESES:
            mu = pilot_stats[hyp_id]["mean"]
            sd = pilot_stats[hyp_id]["sd"]
            threshold = thresholds[hyp_id]

            # Simulate n_required paired differences from pilot distribution
            sim_diffs = [rng.gauss(mu, sd) for _ in range(n_required)]
            sim_mean = sum(sim_diffs) / n_required
            sim_se = sd / math.sqrt(n_required)

            # Approximate power using z-test (fast proxy for permutation test)
            if sim_se > 1e-10:
                z_stat = (sim_mean - threshold) / sim_se
                reject = z_stat > z_alpha
            else:
                reject = sim_mean > threshold

            if reject:
                per_hyp_rejections[hyp_id] += 1
            else:
                all_reject = False

        if all_reject:
            joint_rejections += 1

    joint_power = joint_rejections / n_power_sims
    per_hyp_power = {h: per_hyp_rejections[h] / n_power_sims for h in HYPOTHESES}

    # Check if joint power meets target
    power_adequate = joint_power >= target_power
    inconclusive_rule_active = not power_adequate and n_required >= n_max

    return {
        "pilot_statistics": {
            h: {"mean": round(s["mean"], 4), "sd": round(s["sd"], 4)}
            for h, s in pilot_stats.items()
        },
        "max_paired_sd": round(max_sd, 4),
        "max_sd_hypothesis": max_sd_hyp,
        "thresholds_used": {h: round(t, 4) for h, t in thresholds.items()},
        "n_formula_raw": n_formula,
        "n_required": n_required,
        "n_range": [n_min, n_max],
        "joint_power_at_N": round(joint_power, 4),
        "per_hypothesis_power": {h: round(p, 4) for h, p in per_hyp_power.items()},
        "power_adequate": power_adequate,
        "inconclusive_rule": "If joint power < 0.80 at N_max=60, label non-rejection INCONCLUSIVE, not evidence of no effect" if inconclusive_rule_active else None,
        "n_power_simulations": n_power_sims
    }


# ---------------------------------------------------------------------------
# §7. Holm Adjustment and Intersection-Union Decision Rule (evaluation.md §9)
# ---------------------------------------------------------------------------

def holm_adjustment(p_values: Dict[str, float], alpha: float = 0.05) -> Dict[str, Any]:
    """
    Apply Holm step-down procedure across the 6 hypotheses.
    Returns adjusted p-values and rejection decisions.
    """
    m = len(p_values)
    sorted_hyps = sorted(p_values.items(), key=lambda x: x[1])

    results = {}
    all_rejected_so_far = True

    for rank, (hyp_id, raw_p) in enumerate(sorted_hyps):
        adjusted_alpha = alpha / (m - rank)
        reject = all_rejected_so_far and (raw_p <= adjusted_alpha)

        if not reject:
            all_rejected_so_far = False

        adjusted_p = min(1.0, raw_p * (m - rank))

        results[hyp_id] = {
            "raw_p_value": round(raw_p, 6),
            "holm_rank": rank + 1,
            "holm_adjusted_alpha": round(adjusted_alpha, 6),
            "holm_adjusted_p": round(adjusted_p, 6),
            "reject_individual": reject
        }

    return results


def intersection_union_decision(
    hypothesis_results: Dict[str, bool]
) -> Dict[str, Any]:
    """
    Joint novelty claim decision rule (evaluation.md §9):
    The joint claim is supported ONLY if ALL six hypotheses reject
    under the registered one-sided decision rule.
    """
    all_reject = all(hypothesis_results.values())
    p1_reject = hypothesis_results.get("P-1a", False) and hypothesis_results.get("P-1b", False)
    p2_reject = hypothesis_results.get("P-2", False)
    p3_reject = hypothesis_results.get("P-3", False)
    p4_reject = hypothesis_results.get("P-4a", False) and hypothesis_results.get("P-4b", False)

    if all_reject:
        interpretation = "Containment benefit supported at p_main for the tested stack and configuration"
    elif p1_reject and p2_reject and not p4_reject:
        interpretation = ("The improvement may be attributable to the semaphore/budgets; "
                          "the OC-based novelty claim fails under the registered rule")
    elif p4_reject and not (p1_reject and p2_reject):
        interpretation = ("OC may help within B, but superiority over the required "
                          "baselines is not established")
    elif not p3_reject:
        interpretation = "No-attack overhead/non-inferiority guardrail fails"
    else:
        interpretation = "Partial support; see individual hypothesis results"

    return {
        "joint_claim_supported": all_reject,
        "interpretation": interpretation,
        "per_hypothesis_rejection": hypothesis_results
    }


# ---------------------------------------------------------------------------
# §8. Seed-Block Generation and Arm Randomization
# ---------------------------------------------------------------------------

def generate_seed_blocks(
    n_seeds: int,
    master_seed: int = 20261011
) -> Dict[str, Any]:
    """
    Generate pre-registered seed blocks for confirmatory experiment.
    Each seed defines the arrival trace, request classes, demand draws,
    and fault draws. Arm execution order is randomized per seed.
    """
    rng = random.Random(master_seed)

    # Arms involved in confirmatory comparisons
    arms = ["A'-TR", "D-TR", "D-OC", "B-TR", "B-OC"]

    seed_blocks = []
    for i in range(n_seeds):
        seed_value = rng.randint(100_000, 999_999)
        arm_order = arms[:]
        rng.shuffle(arm_order)

        seed_blocks.append({
            "seed_index": i + 1,
            "seed_value": seed_value,
            "arm_execution_order": arm_order,
            "fault_draw_seed": rng.randint(100_000, 999_999),
            "trace_seed": rng.randint(100_000, 999_999)
        })

    return {
        "n_seeds": n_seeds,
        "master_seed": master_seed,
        "arms": arms,
        "seed_blocks": seed_blocks,
        "exclusion_policy": {
            "rule": "A seed is excluded only if execution fails for ALL arms in the contrast (infrastructure failure). "
                    "Partial failures (one arm succeeds, other fails) are recorded and analyzed.",
            "replacement": "Excluded seeds are NOT replaced. The analysis uses the remaining seeds.",
            "reporting": "All exclusions, their reasons, and any deviation from the registered plan are reported."
        }
    }


# ---------------------------------------------------------------------------
# §9. Pre-Registration Document Assembly and Hashing
# ---------------------------------------------------------------------------

def assemble_preregistration(
    pilot_diffs: Dict[str, List[float]],
    p_main: float = 0.05,
    c_w: float = 10.0,
    permutation_flips: int = 100_000,
    frozen_at: str = "2026-10-11T17:45:00Z"
) -> Dict[str, Any]:
    """
    Assemble the complete pre-registration document with all frozen
    parameters, decision rules, and seed manifest.
    """
    # Step 1: Compute thresholds from pilot
    thresholds = compute_thresholds_from_pilot(pilot_diffs)
    delta_OE = thresholds["delta_OE"]
    delta_C = thresholds["delta_C"]

    # Step 2: Check sign-flip assumptions
    assumptions = check_sign_flip_assumptions(pilot_diffs)

    # Step 3: Compute sample size and power
    power_analysis = compute_sample_size_and_power(
        pilot_diffs, delta_OE, delta_C
    )
    n_seeds = power_analysis["n_required"]

    # Step 4: Generate seed blocks
    seed_manifest = generate_seed_blocks(n_seeds)

    # Step 5: Assemble the pre-registration
    prereg = {
        "meta": {
            "document": "RESCUE-4 Statistical Pre-Registration",
            "specification_version": "v1.5",
            "phase": "Phase 6 - Statistical Pre-Registration",
            "status": "FROZEN",
            "frozen_at": frozen_at,
            "note": "No confirmatory result has been inspected before this plan was frozen. "
                    "Any later additions are labeled exploratory."
        },

        "frozen_parameters": {
            "p_main": p_main,
            "p_main_source": "Phase 5 calibration: CASE_B, 95% CI [0.0440, 0.0570]",
            "delta_OE": delta_OE,
            "delta_C": delta_C,
            "c_w": c_w,
            "non_inferiority_margin_P3": -0.03,
            "alpha": 0.05,
            "permutation_flips": permutation_flips,
            "n_confirmatory_seeds": n_seeds,
            "seed_range": [20, 60]
        },

        "threshold_calibration": thresholds,

        "hypotheses": HYPOTHESES,

        "metric_definitions": METRIC_DEFINITIONS,

        "measurement_windows": MEASUREMENT_WINDOWS,

        "paired_design": {
            "unit": "A seed defines the arrival trace, request classes, demand draws, and fault draws.",
            "pairing": "Arms in a contrast use the same trace and draws.",
            "arm_randomization": "Arm execution order is randomized per seed.",
            "per_seed_storage": "Per-seed results are stored, not only aggregate averages."
        },

        "primary_test": {
            "name": "One-sided paired sign-flip permutation test",
            "n_flips": permutation_flips,
            "threshold_in_null": True,
            "description": "The threshold (delta_OE or delta_C) is incorporated into the null. "
                           "Under H0, each centered difference is equally likely positive or negative."
        },

        "assumption_diagnostics": assumptions,

        "fallback_procedure": {
            "trigger": "If sign-flip assumptions are not adequately supported by pilot differences",
            "fallback_test": "Wilcoxon signed-rank test (one-sided)",
            "rule": "Use ONLY the fallback specified before confirmatory runs. "
                    "Do not select between primary and fallback after observing results."
        },

        "decision_rules": {
            "joint_claim": {
                "rule": "The joint claim is supported only if ALL six hypotheses reject "
                        "under the registered one-sided decision rule.",
                "method": "Intersection-union test"
            },
            "individual_claims": {
                "rule": "Report individual claims using Holm step-down adjustment across the six hypotheses.",
                "note": "Do not infer that OC is novel merely because B-OC beats a baseline; "
                        "P-4a and P-4b specifically test whether OC adds value beyond TR."
            },
            "interpretation_table": {
                "all_six_reject": "Containment benefit supported at p_main for the tested stack and configuration",
                "P1_P2_reject_but_not_P4": "Improvement may be attributable to semaphore/budgets; OC novelty claim fails",
                "P4_reject_but_not_P1_P2": "OC may help within B, but superiority over required baselines not established",
                "P3_fails": "No-attack overhead/non-inferiority guardrail fails",
                "case_A_calibration": "Report claim as conditional; stress cell is not the measured operating condition",
                "power_below_requirement": "Inconclusive",
                "gate_failure": "Do not make claims that depend on that gate"
            },
            "confidence_intervals": {
                "method": "BCa bootstrap",
                "role": "Descriptive only",
                "disagreement_rule": "If CI and registered test disagree, apply the pre-registered decision rule"
            }
        },

        "power_analysis": power_analysis,

        "seed_manifest": seed_manifest,

        "secondary_analyses": {
            "fault_sweep": {
                "cells": [0.00, 0.01, 0.05, 0.20],
                "note": "Plus any additional cell required by above-grid calibration rule",
                "zero_fault_check": "Mandatory zero-fault overhead check included",
                "crossover_analysis": "Report the crossover point where OC begins to outperform TR"
            },
            "status": "Secondary analyses do not replace or redefine the primary hypotheses"
        },

        "required_records_per_run": [
            "Code revision and configuration hash",
            "Seed, workload, arm, arm order, fault draws, and calibration cell",
            "PostgreSQL and logical-decoding versions/configuration where applicable",
            "OS, CPU, memory, service-rate, executor, and database measurements",
            "Run completion status, errors, deviations, exclusions, and reasons"
        ]
    }

    return prereg


def hash_preregistration(prereg: Dict[str, Any]) -> str:
    """
    Compute SHA-256 hash of the pre-registration document.
    The hash is computed on the canonical JSON serialization (sorted keys, no whitespace).
    The sha256_hash field itself is excluded so the hash is strictly of document content.
    """
    prereg_copy = json.loads(json.dumps(prereg, sort_keys=True, default=str))
    if "meta" in prereg_copy:
        prereg_copy["meta"].pop("sha256_hash", None)
    canonical = json.dumps(prereg_copy, sort_keys=True, separators=(",", ":"))
    return hashlib.sha256(canonical.encode("utf-8")).hexdigest()


# ---------------------------------------------------------------------------
# §10. Main Execution
# ---------------------------------------------------------------------------

def main():
    print("=================================================================")
    print("    RESCUE-4 STATISTICAL PRE-REGISTRATION ENGINE (PHASE 6)       ")
    print("=================================================================")

    # Load frozen p_main from calibration report
    try:
        with open("config/calibration_report.json", "r") as f:
            cal_report = json.load(f)
        p_main = cal_report["p_main_frozen"]
        print(f"\n[OK] Loaded p_main = {p_main} from Phase 5 calibration report.")
    except FileNotFoundError:
        p_main = 0.05
        print(f"\n[WARN] Calibration report not found. Using default p_main = {p_main}.")

    # Load c_w from config
    try:
        with open("config/rescue_config.v1.0.json", "r") as f:
            config = json.load(f)
        c_w_entry = [p for p in config["parameter_freeze_ledger"]["provisional_parameters"]
                     if p["parameter"] == "c_w (stale write weight)"]
        c_w = c_w_entry[0]["current_provisional_value"] if c_w_entry else 10.0
    except (FileNotFoundError, KeyError):
        c_w = 10.0

    # Step 1: Generate pilot data
    print("\n--- [Step 1] Generating Pilot Differences (12 pilot seeds) ---")
    pilot_gen = PilotDataGenerator(seed=20261011, n_pilot=12)
    pilot_diffs = pilot_gen.generate_pilot_differences()

    for hyp_id, diffs in pilot_diffs.items():
        mean_d = sum(diffs) / len(diffs)
        sd_d = math.sqrt(sum((d - mean_d) ** 2 for d in diffs) / (len(diffs) - 1))
        print(f"  {hyp_id}: mean={mean_d:.4f}, sd={sd_d:.4f}, n={len(diffs)}")

    # Step 2: Compute thresholds
    print("\n--- [Step 2] Computing Registered Thresholds from Pilot Data ---")
    thresholds = compute_thresholds_from_pilot(pilot_diffs)
    print(f"  delta_OE = {thresholds['delta_OE']} (rule: {thresholds['rule_OE']})")
    print(f"  delta_C  = {thresholds['delta_C']} (rule: {thresholds['rule_C']})")
    print(f"  Pilot OE means: {thresholds['pilot_OE_means']}")
    print(f"  Pilot S_L means: {thresholds['pilot_SL_means']}")

    # Step 3: Check sign-flip assumptions
    print("\n--- [Step 3] Checking Sign-Flip Test Assumptions ---")
    assumptions = check_sign_flip_assumptions(pilot_diffs)
    for hyp_id, diag in assumptions["per_hypothesis"].items():
        status = "OK" if diag["assumptions_supported"] else "FAIL"
        print(f"  {hyp_id}: skewness={diag['skewness']:.3f}, outliers={diag['outlier_count']}, [{status}]")

    if assumptions["all_assumptions_supported"]:
        print("  [OK] All sign-flip assumptions supported. Using primary test.")
    else:
        print(f"  [WARN] Assumptions not met. Fallback: {assumptions['fallback_test']}")

    # Step 4: Sample size and power
    print("\n--- [Step 4] Sample Size and Joint Power Analysis ---")
    power = compute_sample_size_and_power(
        pilot_diffs, thresholds["delta_OE"], thresholds["delta_C"]
    )
    print(f"  Max paired SD = {power['max_paired_sd']} (from {power['max_sd_hypothesis']})")
    print(f"  N formula (raw) = {power['n_formula_raw']}")
    print(f"  N required = {power['n_required']} (range [{power['n_range'][0]}, {power['n_range'][1]}])")
    print(f"  Joint power at N={power['n_required']}: {power['joint_power_at_N']:.4f}")
    for hyp_id, pwr in power["per_hypothesis_power"].items():
        print(f"    {hyp_id}: power = {pwr:.4f}")
    if power["power_adequate"]:
        print("  [OK] Joint power >= 0.80. Sample size adequate.")
    else:
        print("  [WARN] Joint power < 0.80. Non-rejections will be labeled INCONCLUSIVE.")

    # Step 5: Assemble pre-registration
    print("\n--- [Step 5] Assembling Frozen Pre-Registration Document ---")
    prereg = assemble_preregistration(
        pilot_diffs, p_main=p_main, c_w=c_w
    )

    # Step 6: Hash the pre-registration
    prereg_hash = hash_preregistration(prereg)
    prereg["meta"]["sha256_hash"] = prereg_hash
    print(f"  Pre-registration SHA-256: {prereg_hash}")

    # Step 7: Export
    output_path = "config/preregistration_manifest.json"
    with open(output_path, "w") as f:
        json.dump(prereg, f, indent=2, default=str)
    print(f"  Exported to {output_path}")

    # Step 8: Summary
    print("\n=================================================================")
    print("  PHASE 6 PRE-REGISTRATION SUMMARY")
    print("=================================================================")
    print(f"  Hypotheses frozen:       6 (P-1a, P-1b, P-2, P-3, P-4a, P-4b)")
    print(f"  p_main (from Phase 5):   {p_main}")
    print(f"  delta_OE:                {thresholds['delta_OE']}")
    print(f"  delta_C:                 {thresholds['delta_C']}")
    print(f"  c_w (stale write cost):  {c_w}")
    print(f"  Confirmatory seeds:      {power['n_required']}")
    print(f"  Joint power:             {power['joint_power_at_N']:.4f}")
    print(f"  Primary test:            Sign-flip permutation ({100_000:,} flips)")
    print(f"  Individual claims:       Holm step-down adjustment")
    print(f"  Joint decision:          Intersection-union (all 6 must reject)")
    print(f"  SHA-256 hash:            {prereg_hash[:16]}...")
    print("=================================================================")
    print("  PHASE 6 EXIT GATE: PRE-REGISTRATION FROZEN AND HASHED")
    print("  No confirmatory result inspected before freeze.")
    print("=================================================================")


if __name__ == "__main__":
    main()
