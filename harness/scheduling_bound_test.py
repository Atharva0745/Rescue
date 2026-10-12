"""
RESCUE-4 Scheduling-Bound Test Suite
Executes:
  1. Analytical Tightness Verification for m in {2, 4, 8, 16} (T-D2)
  2. 1,000,000 Valid Exact-Rational Instances across 6 distributions (T-D1)
  3. Mutant Counterexample Generation for NWC, LIFO, and Ignored Loss (T-D3)
"""

import sys
import time
import json
import random
import heapq
from fractions import Fraction
from typing import List, Dict, Any, Tuple


def verify_tightness_instances(target_m_list: List[int]) -> List[Dict[str, Any]]:
    print("\n--- Analytical Tightness Verification ---")
    results = []
    for m in target_m_list:
        durations = [1] * (m * (m - 1)) + [m]
        beta_max = m
        total_work = sum(durations)
        assert total_work == m * m

        exec_heap = [0] * m
        heapq.heapify(exec_heap)
        
        start_times = []
        for p in durations:
            avail = heapq.heappop(exec_heap)
            start_times.append(avail)
            heapq.heappush(exec_heap, avail + p)

        makespan = max(exec_heap)
        bound_numerator = total_work + (m - 1) * beta_max
        assert bound_numerator % m == 0, "Bound should be an exact integer for I(m)"
        t2_bound = bound_numerator // m
        slack = t2_bound - makespan

        w_accum = 0
        t1_valid_all = True
        for j_idx, p in enumerate(durations):
            if m * start_times[j_idx] > w_accum:
                t1_valid_all = False
            w_accum += p

        is_tight = (slack == 0) and (makespan == 2 * m - 1)
        print(f"m={m:2d} | W={total_work:4d} | beta_max={beta_max:2d} | C_max={makespan:3d} | Bound={t2_bound:3d} | Slack={slack:2d} | Tight: {is_tight} | T1 valid: {t1_valid_all}")

        results.append({
            "m": m,
            "job_count": len(durations),
            "total_work": total_work,
            "beta_max": beta_max,
            "makespan": makespan,
            "t2_bound": t2_bound,
            "slack": slack,
            "is_tight": is_tight,
            "t1_all_valid": t1_valid_all
        })
        assert is_tight, f"Tightness failed for m={m}"

    return results


def run_valid_batch_simulation(m: int, durations: List[int]) -> Tuple[bool, bool, int, int]:
    total_w = sum(durations)
    beta_max = max(durations) if durations else 0

    exec_heap = [0] * m
    heapq.heapify(exec_heap)

    w_ahead = 0
    t1_valid = True

    for p in durations:
        avail = heapq.heappop(exec_heap)
        if m * avail > w_ahead:
            t1_valid = False
            break
        w_ahead += p
        heapq.heappush(exec_heap, avail + p)

    makespan = max(exec_heap)
    t2_bound_scaled = total_w + (m - 1) * beta_max
    c_max_scaled = m * makespan
    t2_slack_scaled = t2_bound_scaled - c_max_scaled
    t2_valid = (t2_slack_scaled >= 0)

    return (t1_valid, t2_valid, t2_slack_scaled, makespan)


def generate_instance_durations(rng: random.Random, regime: str, job_count: int, beta_cap: int) -> List[int]:
    if regime == "UNIFORM":
        return [rng.randint(1, beta_cap) for _ in range(job_count)]
    elif regime == "HEAVY_TAILED":
        vals = []
        for _ in range(job_count):
            u = rng.random()
            val = int(1.0 / ((1.0 - u * 0.95) ** (1.0 / 1.2)))
            vals.append(min(max(1, val), beta_cap))
        return vals
    elif regime == "BIMODAL":
        vals = []
        for _ in range(job_count):
            if rng.random() < 0.70:
                vals.append(rng.randint(1, max(2, beta_cap // 10)))
            else:
                vals.append(rng.randint(max(1, beta_cap - 5), beta_cap))
        return vals
    elif regime == "ALL_AT_CAP":
        return [beta_cap] * job_count
    elif regime == "LONG_JOB_LAST":
        vals = [rng.randint(1, beta_cap // 2) for _ in range(job_count - 1)]
        vals.append(beta_cap)
        return vals
    elif regime == "RANDOMIZED_ORDER":
        vals = [rng.randint(1, beta_cap) for _ in range(job_count)]
        rng.shuffle(vals)
        return vals
    else:
        raise ValueError(f"Unknown regime: {regime}")


def run_million_instance_test(total_instances: int = 1_000_000, master_seed: int = 42) -> Dict[str, Any]:
    print(f"\n--- Generating & Asserting {total_instances:,} Valid Instances ---")
    rng = random.Random(master_seed)
    
    m_choices = [2, 4, 8, 16]
    regimes = ["UNIFORM", "HEAVY_TAILED", "BIMODAL", "ALL_AT_CAP", "LONG_JOB_LAST", "RANDOMIZED_ORDER"]
    
    regime_counts = {r: 0 for r in regimes}
    t1_violations = 0
    t2_violations = 0
    
    min_t2_slack_scaled = float("inf")
    instances_per_regime = total_instances // len(regimes)
    
    t0 = time.time()
    last_print = t0
    completed = 0
    
    for r_idx, regime in enumerate(regimes):
        count_for_regime = instances_per_regime if r_idx < len(regimes) - 1 else (total_instances - completed)
        
        for _ in range(count_for_regime):
            m = rng.choice(m_choices)
            job_count = rng.randint(m, m * 10)
            beta_cap = rng.randint(5, 50)
            
            durations = generate_instance_durations(rng, regime, job_count, beta_cap)
            t1_valid, t2_valid, slack_scaled, _ = run_valid_batch_simulation(m, durations)
            
            if not t1_valid:
                t1_violations += 1
            if not t2_valid:
                t2_violations += 1
            if slack_scaled < min_t2_slack_scaled:
                min_t2_slack_scaled = slack_scaled
                
            regime_counts[regime] += 1
            completed += 1
            
            if completed % 200_000 == 0 or (time.time() - last_print > 5.0):
                elapsed = time.time() - t0
                rate = completed / elapsed if elapsed > 0 else 0
                print(f"Progress: {completed:,}/{total_instances:,} ({completed/total_instances*100:.1f}%) | Rate: {rate:,.0f} inst/sec | T1 viol: {t1_violations} | T2 viol: {t2_violations}")
                last_print = time.time()

    elapsed = time.time() - t0
    print(f"\nCompleted {completed:,} instances in {elapsed:.2f} seconds ({completed/elapsed:,.0f} instances/sec).")
    print(f"T1 Violations: {t1_violations}")
    print(f"T2 Violations: {t2_violations}")
    print(f"Minimum Scaled T2 Slack: {min_t2_slack_scaled}")
    
    assert t1_violations == 0, f"Found {t1_violations} T1 violations!"
    assert t2_violations == 0, f"Found {t2_violations} T2 violations!"

    return {
        "total_instances": total_instances,
        "master_seed": master_seed,
        "regime_distribution": regime_counts,
        "t1_violations": t1_violations,
        "t2_violations": t2_violations,
        "min_slack_scaled": min_t2_slack_scaled,
        "execution_time_seconds": elapsed,
        "throughput_inst_per_sec": completed / elapsed
    }


def evaluate_mutants() -> Dict[str, Any]:
    print("\n--- Mutant Evaluation & Counterexample Search ---")
    mutant_results = {}

    # 1. Non-Work-Conserving
    print("Evaluating Mutant 1: Non-Work-Conserving (M_NWC)...")
    m = 2
    durations = [10, 10, 10]
    nwc_pause = 5
    
    exec_heap = [0] * m
    heapq.heapify(exec_heap)
    for p in durations:
        avail = heapq.heappop(exec_heap)
        start_t = avail + nwc_pause
        heapq.heappush(exec_heap, start_t + p)
    makespan = max(exec_heap)
    t2_bound = sum(durations) / m + (1.0 - 1.0/m) * max(durations)
    nwc_violates = makespan > t2_bound
    print(f"  M_NWC: C_max = {makespan}, Bound = {t2_bound:.1f} -> Violation: {nwc_violates}")
    assert nwc_violates, "M_NWC failed to produce counterexample"
    mutant_results["M_NWC"] = {"makespan": makespan, "bound": t2_bound, "violated": nwc_violates}

    # 2. LIFO Dispatcher
    print("Evaluating Mutant 2: LIFO Dispatcher (M_LIFO)...")
    m = 1
    durations = [10, 10, 10, 10]
    lifo_order = list(reversed(range(len(durations))))
    current_time = 0
    lifo_starts = {}
    for j_id in lifo_order:
        lifo_starts[j_id] = current_time
        current_time += durations[j_id]
        
    job0_start = lifo_starts[0]
    job0_bound = 0
    lifo_violates = job0_start > job0_bound
    print(f"  M_LIFO: Job 0 Start = {job0_start}, Bound = {job0_bound} -> Violation: {lifo_violates}")
    assert lifo_violates, "M_LIFO failed to produce counterexample"
    mutant_results["M_LIFO"] = {"actual_start": job0_start, "bound": job0_bound, "violated": lifo_violates}

    # 3. Ignored Executor Loss
    print("Evaluating Mutant 3: Ignored Executor Loss (M_IGN_LOSS)...")
    nominal_m = 4
    active_m = 2
    durations = [20, 20, 20, 20]
    
    exec_heap = [0] * active_m
    heapq.heapify(exec_heap)
    for p in durations:
        avail = heapq.heappop(exec_heap)
        heapq.heappush(exec_heap, avail + p)
    actual_makespan = max(exec_heap)
    buggy_bound = sum(durations) / nominal_m + (1.0 - 1.0/nominal_m) * max(durations)
    ign_loss_violates = actual_makespan > buggy_bound
    print(f"  M_IGN_LOSS: C_max = {actual_makespan}, Buggy Bound = {buggy_bound:.1f} -> Violation: {ign_loss_violates}")
    assert ign_loss_violates, "M_IGN_LOSS failed to produce counterexample"
    mutant_results["M_IGN_LOSS"] = {"makespan": actual_makespan, "buggy_bound": buggy_bound, "violated": ign_loss_violates}

    return mutant_results


if __name__ == "__main__":
    verify_tightness_instances([2, 4, 8, 16])
    run_million_instance_test(1_000_000)
    evaluate_mutants()
