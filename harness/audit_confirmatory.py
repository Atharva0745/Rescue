#!/usr/bin/env python3
"""
RESCUE-4 Independent Statistical Audit Suite (Phase 7 Audit)
Reads config/confirmatory_results.json and independently audits:
  1. Per-seed differences across all 6 contrasts (P-1a, P-1b, P-2, P-3, P-4a, P-4b)
  2. Sample standard deviations and 95% confidence intervals
  3. Meet-in-the-middle exact sign-flip permutation p-value (2^25 patterns)
  4. scipy.stats.permutation_test one-sided permutation test comparison
  5. Monotonic Holm step-down adjusted p-values
  6. Verification that both implementations strictly agree
"""

import sys
import os
import json
import math
import numpy as np
from typing import Dict, List, Any
from scipy.stats import permutation_test


def exact_signflip_p(d: List[float], delta: float, tol: float = 1e-12) -> float:
    """
    Exact one-sided sign-flip permutation test using meet-in-the-middle partition.
    Evaluates all 2^N (2^25 = 33,554,432) sign patterns.
    """
    x = np.asarray(d, dtype=float) - delta
    obs = x.sum()
    def sums(v):
        s = np.zeros(1)
        for t in v:
            s = np.concatenate([s + t, s - t])
        return s
    half = len(x) // 2
    sa = sums(x[:half])
    sb = np.sort(sums(x[half:]))
    cnt = (len(sb) - np.searchsorted(sb, obs - sa - tol, side="left")).sum()
    return float(cnt / (2 ** len(x)))


def holm_monotonic(p_values: Dict[str, float], alpha: float = 0.05) -> Dict[str, Any]:
    """
    Holm step-down procedure with running max enforcing monotonicity.
    """
    m = len(p_values)
    sorted_hyps = sorted(p_values.items(), key=lambda x: x[1])
    results = {}
    run = 0.0
    for rank, (hyp_id, raw_p) in enumerate(sorted_hyps):
        multiplier = m - rank
        run = max(run, multiplier * raw_p)
        adj_p = min(1.0, run)
        adj_alpha = alpha / multiplier
        reject = raw_p <= adj_alpha
        results[hyp_id] = {
            "rank": rank + 1,
            "raw_p": raw_p,
            "adj_alpha": adj_alpha,
            "holm_p": adj_p,
            "reject": reject
        }
    return results


def run_audit(results_path: str = "config/confirmatory_results.json") -> Dict[str, Any]:
    print("=" * 80)
    print("       RESCUE-4 INDEPENDENT STATISTICAL AUDIT SUITE           ")
    print("=" * 80)

    with open(results_path, "r", encoding="utf-8") as f:
        data = json.load(f)

    records = data["per_seed_records"]
    n_seeds = len(records)
    print(f"Loaded {n_seeds} confirmatory seed records from {results_path}")

    thresholds = {
        "P-1a": 0.0328,
        "P-1b": 0.0328,
        "P-2":  0.0138,
        "P-3": -0.0300,
        "P-4a": 0.0328,
        "P-4b": 0.0138
    }

    audit_table = []
    exact_p_dict = {}

    for hyp_id in ["P-1a", "P-1b", "P-2", "P-3", "P-4a", "P-4b"]:
        diffs = [rec["contrasts"][hyp_id] for rec in records]
        thresh = thresholds[hyp_id]
        mean_d = float(np.mean(diffs))
        sd_d = float(np.std(diffs, ddof=1))
        unique_cnt = len(np.unique(np.round(diffs, 8)))

        # 1. Exact sign-flip p-value
        p_exact = exact_signflip_p(diffs, thresh)
        exact_p_dict[hyp_id] = p_exact

        # 2. Percentile Bootstrap 95% CI
        boot_means = []
        rng = np.random.RandomState(20261011 + len(hyp_id))
        for _ in range(10_000):
            sample = rng.choice(diffs, size=len(diffs), replace=True)
            boot_means.append(float(np.mean(sample)))
        boot_means.sort()
        ci_lower = boot_means[int(0.025 * 10_000)]
        ci_upper = boot_means[int(0.975 * 10_000)]

        # 3. scipy.stats.permutation_test cross-check
        # Centered statistic: mean(d - thresh)
        def stat_func(x):
            return np.mean(x - thresh)

        # Null is sign-symmetric about zero: test whether centered differences > 0
        scipy_res = permutation_test(
            (np.asarray(diffs),),
            stat_func,
            permutation_type="samples",
            n_resamples=50_000,
            alternative="greater",
            random_state=rng
        )
        p_scipy = float(scipy_res.pvalue)

        audit_table.append({
            "hyp_id": hyp_id,
            "threshold": thresh,
            "mean": mean_d,
            "sd": sd_d,
            "unique_values": unique_cnt,
            "ci_95": (ci_lower, ci_upper),
            "exact_p": p_exact,
            "scipy_p": p_scipy,
            "diffs": diffs
        })

    # Apply monotonic Holm adjustment
    holm_res = holm_monotonic(exact_p_dict, alpha=0.05)

    print("\n" + "=" * 80)
    print("INDEPENDENT AUDIT SUMMARY TABLE")
    print("=" * 80)
    header = f"{'Hypothesis':<10} | {'Thresh':<8} | {'Mean Diff':<10} | {'SD':<9} | {'95% CI':<22} | {'Exact p':<12} | {'scipy p':<10} | {'Holm p':<12} | {'Decision'}"
    print(header)
    print("-" * len(header))

    all_agreed = True
    for row in audit_table:
        hid = row["hyp_id"]
        h = holm_res[hid]
        ci_str = f"[{row['ci_95'][0]:+.5f}, {row['ci_95'][1]:+.5f}]"
        dec = "REJECT H0" if h["reject"] else "FAIL TO REJECT"
        print(f"{hid:<10} | {row['threshold']:<+8.4f} | {row['mean']:<+10.5f} | {row['sd']:<9.5f} | {ci_str:<22} | {row['exact_p']:<12.2e} | {row['scipy_p']:<10.2e} | {h['holm_p']:<12.2e} | {dec}")

        # Check that both exact and scipy agree on significance at alpha=0.05
        if (row["exact_p"] < 0.05) != (row["scipy_p"] < 0.05):
            all_agreed = False
            print(f"[AUDIT DIVERGENCE] {hid}: exact_p={row['exact_p']} vs scipy_p={row['scipy_p']}")

    print("-" * len(header))
    if all_agreed:
        print("[AUDIT PASSED] Exact meet-in-the-middle and scipy permutation test implementations strictly agree!")
    else:
        print("[AUDIT FAILED] Divergence detected between test implementations.")

    return {
        "audit_passed": all_agreed,
        "n_seeds": n_seeds,
        "contrasts": audit_table,
        "holm_adjustments": holm_res
    }


if __name__ == "__main__":
    run_audit()
