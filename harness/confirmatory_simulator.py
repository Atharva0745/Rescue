"""
RESCUE-4 Confirmatory Simulation Engine
Implements Stage D of the evaluation protocol (evaluation.md §6):
  1. Executes the pre-registered 25-seed confirmatory evaluation suite.
  2. Runs W3 at p_main = 0.05 across arms: A'-TR, D-TR, B-TR, B-OC.
  3. Runs W1 at alpha = 0 (no attack) for B-OC vs D-OC.
  4. Evaluates all six primary hypotheses:
     - P-1a: OE_norm(D-TR) - OE_norm(B-OC) > delta_OE (0.0328)
     - P-1b: OE_norm(A'-TR) - OE_norm(B-OC) > delta_OE (0.0328)
     - P-2:  S_L(B-OC) - S_L(D-TR) >= delta_C (0.0138)
     - P-3:  S_L(B-OC) - S_L(D-OC) >= -0.0300 (no-attack guardrail)
     - P-4a: OE_norm(B-TR) - OE_norm(B-OC) > delta_OE (0.0328)
     - P-4b: S_L(B-OC) - S_L(B-TR) >= delta_C (0.0138)
  5. Implements one-sided paired sign-flip permutation tests (100,000 flips).
  6. Computes BCa/percentile bootstrap 95% confidence intervals.
  7. Applies Intersection-Union Test for joint claim and Holm adjustment
     for individual claims.
  8. Executes secondary fault-rate sweep (p in {0.00, 0.01, 0.05, 0.20})
     and crossover analysis.
  9. Exports raw per-seed records and results to config/confirmatory_results.json.

References:
  - evaluation.md §6 (Stage D), §7 (Metrics), §8 (Statistical Evaluation), §9 (Decision Rules)
  - preregistration.md (Frozen specification, SHA-256: 219fcd691d3c...)
  - config/preregistration_manifest.json (Frozen 25-seed manifest)
  - config/rescue_config.v1.0.json (Frozen system topology and parameters)
"""

import sys
import os
import json
import hashlib
import math
import random
import time
from typing import Dict, List, Tuple, Any, Optional


# ---------------------------------------------------------------------------
# §1. Pipeline & Workload Models
# ---------------------------------------------------------------------------

class SimulatedRequest:
    def __init__(
        self,
        req_id: int,
        req_class: str,
        arrival_time: float,
        is_attack: bool,
        demands: Dict[str, float],
        deadline: float,
        datastore_partition: int = 1,
        fault_draw: float = 0.0,
        policy_draw: float = 0.0
    ):
        self.req_id = req_id
        self.req_class = req_class
        self.arrival_time = arrival_time
        self.is_attack = is_attack
        self.demands = demands  # Stage service times: S1, S2, S3
        self.deadline = deadline
        self.datastore_partition = datastore_partition
        self.fault_draw = fault_draw
        self.policy_draw = policy_draw
        self.admitted: bool = False
        self.completion_time: Optional[float] = None
        self.stale_write_committed: bool = False
        self.orphan_seconds: float = 0.0


def generate_workload_trace(
    workload_id: str,
    trace_seed: int,
    total_duration: float = 90.0,
    alpha_attack: float = 0.25,
    fault_seed: Optional[int] = None
) -> List[SimulatedRequest]:
    """
    Generate synthetic request stream following W1 or W3 profile.
    Decoupled trace seed ensures identical arrival sequences across arms.
    """
    rng = random.Random(trace_seed)
    fault_rng = random.Random(fault_seed if fault_seed is not None else trace_seed ^ 0x5EED)
    policy_rng = random.Random(trace_seed ^ 0xA11CE)
    requests = []
    current_time = 0.0
    req_id = 1

    # Base arrival rates
    base_rate = 32.0 if workload_id == "W1" else 42.0

    while current_time < total_duration:
        # Time-varying rate multiplier
        if workload_id == "W1":
            rate_mult = 1.35 if (20.0 <= current_time <= 40.0) else 1.0
        else:  # W3 bursty profile
            rate_mult = 1.50 if (25.0 <= current_time <= 55.0) else 1.05

        effective_rate = base_rate * rate_mult
        inter_arrival = rng.expovariate(effective_rate)
        current_time += inter_arrival
        if current_time >= total_duration:
            break

        # Class mix: 75% interactive, 25% batch
        is_batch = rng.random() < 0.25
        req_class = "C_BATCH" if is_batch else "C_INTERACTIVE"

        # Attack status: alpha_attack fraction are adversarial slow/runaway jobs
        is_attack = (rng.random() < alpha_attack) if alpha_attack > 0 else False

        # Service demands across stages S1, S2, S3
        if req_class == "C_INTERACTIVE":
            deadline = 0.500
            if is_attack:
                d_s1 = rng.uniform(0.040, 0.070)
                d_s2 = rng.uniform(0.180, 0.300)  # Attack jobs run long
                d_s3 = rng.uniform(0.020, 0.040)
            else:
                d_s1 = rng.uniform(0.015, 0.035)
                d_s2 = rng.uniform(0.040, 0.080)
                d_s3 = rng.uniform(0.010, 0.025)
        else:  # C_BATCH
            deadline = 3.000
            if is_attack:
                d_s1 = rng.uniform(0.080, 0.160)
                d_s2 = rng.uniform(0.950, 1.500)
                d_s3 = rng.uniform(0.040, 0.100)
            else:
                d_s1 = rng.uniform(0.040, 0.090)
                d_s2 = rng.uniform(0.180, 0.450)
                d_s3 = rng.uniform(0.025, 0.060)

        # Datastore partition (1 = cold, 2 = hot)
        partition = 2 if rng.random() < 0.40 else 1

        requests.append(
            SimulatedRequest(
                req_id=req_id,
                req_class=req_class,
                arrival_time=current_time,
                is_attack=is_attack,
                demands={"S1": d_s1, "S2": d_s2, "S3": d_s3},
                deadline=deadline,
                datastore_partition=partition,
                fault_draw=fault_rng.random(),
                policy_draw=policy_rng.random()
            )
        )
        req_id += 1

    return requests


# ---------------------------------------------------------------------------
# §2. Arm Simulation Engine
# ---------------------------------------------------------------------------

class ArmSimulator:
    """
    Simulates execution of an admission arm (A'-TR, D-TR, D-OC, B-TR, B-OC)
    under matched request streams and fault draws.
    """

    def __init__(
        self,
        arm_name: str,
        fault_rate: float,
        fault_seed: int,
        c_w: float = 10.0,
        gateway_crash_time: Optional[float] = 35.0
    ):
        self.arm_name = arm_name
        self.fault_rate = fault_rate
        self.fault_seed = fault_seed
        self.c_w = c_w
        self.gateway_crash_time = gateway_crash_time

        # Parse arm attributes
        self.is_oc = arm_name.endswith("-OC")
        self.is_tr = arm_name.endswith("-TR")
        self.policy_type = arm_name.split("-")[0]  # A', D, or B

        # Capacities: S1 (8), S2 (16), S3 (4) -> total 28
        self.capacities = {"S1": 8, "S2": 16, "S3": 4}
        self.total_K = 28

        # Reservation weights for Policy B
        self.class_weights = {"C_INTERACTIVE": 0.255, "C_BATCH": 1.110}
        self.caps = {"C_INTERACTIVE": 0.200, "C_BATCH": 1.000}

    def run_workload(
        self,
        requests: List[SimulatedRequest],
        measurement_window: Tuple[float, float]
    ) -> Dict[str, Any]:
        """
        Execute request sequence through pipeline stages.
        Calculates OE components, OE_norm, and S_L within measurement window.
        """
        t_start, t_end = measurement_window
        t_window_duration = t_end - t_start
        if t_end <= t_start:
            raise ValueError("measurement window must have positive duration")
        if not 0.0 <= self.fault_rate <= 1.0:
            raise ValueError("fault_rate must be in [0, 1]")

        stage_executors = {
            s: [0.0] * self.capacities[s] for s in ("S1", "S2", "S3")
        }
        k_avail_s2 = self.capacities["S2"]
        reservations: List[Tuple[float, str, float]] = []
        adaptive_in_flight: List[float] = []
        draco_in_flight: List[Tuple[float, int]] = []
        quarantined_executors: Dict[int, float] = {}
        peak_quarantined_executors = 0
        adaptive_limit = 22.0
        if __package__:
            from .admission_policy import DracoAdmission
        else:
            from admission_policy import DracoAdmission
        draco = DracoAdmission(num_partitions=2, hot_threshold_ms=100.0)
        total_legitimate_in_window = 0
        successful_legitimate_in_window = 0
        orphan_seconds_past_expiry = 0.0
        stale_writes_committed = 0
        admitted_count = 0
        rejected_count = 0

        for req in requests:
            t_arr = req.arrival_time
            in_window = (t_start <= t_arr <= t_end)
            if in_window and not req.is_attack:
                total_legitimate_in_window += 1
            quarantined_executors = {
                executor_idx: release_time
                for executor_idx, release_time in quarantined_executors.items()
                if release_time > t_arr
            }
            k_avail_s2 = (
                self.capacities["S2"] - len(quarantined_executors)
            )
            adaptive_in_flight = [release for release in adaptive_in_flight if release > t_arr]
            remaining_draco = []
            for release, partition in draco_in_flight:
                if release <= t_arr:
                    draco.release_in_flight(partition)
                else:
                    remaining_draco.append((release, partition))
            draco_in_flight = remaining_draco
            reservations = [entry for entry in reservations if entry[0] > t_arr]

            # -------------------------------------------------------------
            # Step 1: Admission Decision
            # -------------------------------------------------------------
            admitted = False

            if self.policy_type == "B":
                weight = self.class_weights[req.req_class]
                held = sum(entry[2] for entry in reservations)
                held_batch = sum(
                    entry[2] for entry in reservations if entry[1] == "C_BATCH"
                )
                max_capacity = k_avail_s2 * 2.0
                batch_quota = max_capacity * 0.40
                admitted = (
                    held + weight <= max_capacity
                    and (
                        req.req_class != "C_BATCH"
                        or held_batch + weight <= batch_quota
                    )
                )

            elif self.policy_type == "D":
                active_s2_delay = max(0.0, min(stage_executors["S2"]) - t_arr)
                estimated_latency_ms = (
                    active_s2_delay + req.demands["S2"]
                ) * 1000.0
                draco.update_datastore_latency(
                    req.datastore_partition, estimated_latency_ms
                )
                admitted, _ = draco.should_admit(req, t_arr)

            elif self.policy_type == "A'":
                queue_delay_ms = max(
                    0.0, min(stage_executors["S2"]) - t_arr
                ) * 1000.0
                if queue_delay_ms > 100.0:
                    adaptive_limit = max(2.0, adaptive_limit * 0.8)
                else:
                    adaptive_limit = min(32.0, adaptive_limit + 0.5)
                admitted = len(adaptive_in_flight) < int(adaptive_limit)

            req.admitted = admitted
            if not admitted:
                rejected_count += 1
                continue
            admitted_count += 1

            # -------------------------------------------------------------
            # Step 2: Multi-Stage Execution & Queueing
            # -------------------------------------------------------------
            curr_time = t_arr
            cap_limit = self.caps[req.req_class]
            exceeded_cap = False
            assigned_executors: Dict[str, int] = {}
            executor_schedule: Dict[str, Tuple[int, float, float, float]] = {}

            # Pipeline execution: S1 -> S2 -> S3
            for s in ("S1", "S2", "S3"):
                demand = req.demands[s]
                executors = stage_executors[s]
                earliest_exec = min(executors)
                idx = executors.index(earliest_exec)
                start_t = max(curr_time, earliest_exec)

                finish_t = start_t + demand
                curr_time = finish_t
                executors[idx] = finish_t
                assigned_executors[s] = idx
                executor_schedule[s] = (
                    idx,
                    earliest_exec,
                    start_t,
                    finish_t,
                )

                if (curr_time - t_arr) > cap_limit:
                    exceeded_cap = True

            req.completion_time = curr_time

            if self.policy_type == "B":
                reservations.append(
                    (curr_time, req.req_class, self.class_weights[req.req_class])
                )
            elif self.policy_type == "A'":
                adaptive_in_flight.append(curr_time)
            elif self.policy_type == "D":
                draco_in_flight.append((curr_time, req.datastore_partition))

            # -------------------------------------------------------------
            # Step 3: Cancellation & Zombie Executor Contention
            # -------------------------------------------------------------
            if exceeded_cap:
                lease_expiry = t_arr + cap_limit
                if req.fault_draw < self.fault_rate:
                    cancellation_delay = 0.12 + 0.28 * req.policy_draw
                    s2_idx = assigned_executors["S2"]
                    _, _, s2_start, s2_finish = executor_schedule["S2"]
                    quarantine_release = max(s2_finish, lease_expiry) + cancellation_delay
                    orphan_seconds_past_expiry += (
                        max(0.0, s2_finish - max(s2_start, lease_expiry))
                        + cancellation_delay
                    )
                    stage_executors["S2"][s2_idx] = quarantine_release

                    if self.policy_type == "B":
                        release_time = (
                            quarantine_release
                            if self.is_oc
                            else lease_expiry
                        )
                        reservations[-1] = (
                            release_time,
                            req.req_class,
                            self.class_weights[req.req_class],
                        )

                    if self.is_oc:
                        quarantined_executors[s2_idx] = quarantine_release
                        peak_quarantined_executors = max(
                            peak_quarantined_executors,
                            len(quarantined_executors),
                        )
                    elif self.is_tr:
                        if (
                            self.gateway_crash_time is not None
                            and curr_time > self.gateway_crash_time
                            and curr_time > t_arr + cap_limit
                        ):
                            stale_writes_committed += 1
                            req.stale_write_committed = True
                else:
                    req.completion_time = None
                    for stage, (idx, previous, start, finish) in executor_schedule.items():
                        stage_executors[stage][idx] = (
                            previous
                            if start >= lease_expiry
                            else max(previous, min(finish, lease_expiry))
                        )
                    if self.policy_type == "B":
                        reservations[-1] = (
                            lease_expiry,
                            req.req_class,
                            self.class_weights[req.req_class],
                        )
                    elif self.policy_type == "A'":
                        adaptive_in_flight[-1] = lease_expiry
                    elif self.policy_type == "D":
                        draco_in_flight[-1] = (
                            lease_expiry,
                            req.datastore_partition,
                        )

            # -------------------------------------------------------------
            # Step 4: Legitimate Success Evaluation
            # -------------------------------------------------------------
            if in_window and not req.is_attack and req.completion_time is not None:
                latency = curr_time - t_arr
                if latency <= req.deadline:
                    successful_legitimate_in_window += 1

        # -----------------------------------------------------------------
        # Step 5: Aggregate outcomes produced by the simulated mechanisms.
        # -----------------------------------------------------------------
        S_L = (
            successful_legitimate_in_window / total_legitimate_in_window
            if total_legitimate_in_window > 0 else 0.0
        )

        OE = orphan_seconds_past_expiry + self.c_w * stale_writes_committed

        # Normalization denominator: sum_s(K_s * T_window) = 28 * 60 = 1680.0
        norm_denominator = self.total_K * t_window_duration
        OE_norm = OE / norm_denominator

        return {
            "arm_name": self.arm_name,
            "total_legitimate": total_legitimate_in_window,
            "successful_legitimate": successful_legitimate_in_window,
            "S_L": round(S_L, 5),
            "executor_seconds_past_expiry": round(orphan_seconds_past_expiry, 4),
            "stale_writes_committed": stale_writes_committed,
            "admitted_requests": admitted_count,
            "rejected_requests": rejected_count,
            "peak_quarantined_executors": peak_quarantined_executors,
            "OE": round(OE, 4),
            "OE_norm": round(OE_norm, 6),
            "norm_denominator": norm_denominator
        }


# ---------------------------------------------------------------------------
# §3. Confirmatory Experiment Suite Execution
# ---------------------------------------------------------------------------

def run_confirmatory_experiment(
    manifest_path: str = "config/preregistration_repaired.json",
    output_path: Optional[str] = None
) -> Dict[str, Any]:
    """
    Runs the complete 25-seed confirmatory evaluation suite.
    Executes W3 at p_main = 0.05 across arms and W1 at alpha = 0 for P-3.
    """
    with open(manifest_path, "r") as f:
        manifest = json.load(f)

    pilot_provenance = manifest.get("pilot_data_provenance")
    registered = pilot_provenance is not None
    if registered:
        recorded_hash = manifest.get("meta", {}).get("sha256_hash")
        if __package__:
            from .preregistration_engine import hash_preregistration
        else:
            from preregistration_engine import hash_preregistration
        if recorded_hash != hash_preregistration(manifest):
            raise ValueError("repaired pre-registration hash verification failed")
        with open(pilot_provenance["artifact"], "rb") as pilot_file:
            pilot_hash = hashlib.sha256(pilot_file.read()).hexdigest()
        if pilot_hash != pilot_provenance["sha256"]:
            raise ValueError("repaired pre-registration pilot artifact hash mismatch")
        with open(__file__, "rb") as simulator_source:
            current_simulator_hash = hashlib.sha256(simulator_source.read()).hexdigest()
        if current_simulator_hash != pilot_provenance["simulator_sha256"]:
            raise ValueError(
                "simulator code changed after pilot; create a new pre-registration"
            )

    if output_path is None:
        output_path = (
            "config/confirmatory_results_repaired_confirmatory.json"
            if registered
            else "config/confirmatory_results_repaired_exploratory.json"
        )

    p_main = manifest["frozen_parameters"]["p_main"]
    c_w = manifest["frozen_parameters"]["c_w"]
    seed_blocks = manifest["seed_manifest"]["seed_blocks"]
    n_seeds = len(seed_blocks)

    print("=================================================================")
    print(
        " RESCUE-4 REGISTERED SIMULATION RERUN"
        if registered
        else " RESCUE-4 EXPLORATORY SIMULATION RERUN"
    )
    print("=================================================================")
    print(f"  Manifest SHA-256:           {manifest['meta']['sha256_hash'][:16]}...")
    print(f"  Seeds rerun:                {n_seeds}")
    print(f"  Fault probability:          {p_main}")
    print(f"  Stale-write weight c_w:     {c_w}")
    print("=================================================================\n")

    # Storage for per-seed results
    per_seed_results = []
    contrasts = {
        "P-1a": [],  # OE_norm(D-TR) - OE_norm(B-OC)
        "P-1b": [],  # OE_norm(A'-TR) - OE_norm(B-OC)
        "P-2":  [],  # S_L(B-OC) - S_L(D-TR)
        "P-3":  [],  # S_L(B-OC) - S_L(D-OC) [W1 alpha=0]
        "P-4a": [],  # OE_norm(B-TR) - OE_norm(B-OC)
        "P-4b": []   # S_L(B-OC) - S_L(B-TR)
    }

    t0 = time.time()

    for sb in seed_blocks:
        s_idx = sb["seed_index"]
        s_val = sb["seed_value"]
        trace_seed = sb["trace_seed"]
        fault_seed = sb["fault_draw_seed"]
        arm_order = sb["arm_execution_order"]

        print(f"Processing Seed {s_idx:02d}/{n_seeds} [Seed: {s_val}, Order: {', '.join(arm_order)}]")

        # Generate matched request traces
        # W3: Duration 90s, measurement window [15s, 75s], gateway crash at 35s
        requests_w3 = generate_workload_trace(
            workload_id="W3",
            trace_seed=trace_seed,
            total_duration=90.0,
            alpha_attack=0.25,
            fault_seed=fault_seed
        )

        # W1: Duration 60s, measurement window [10s, 50s], alpha=0 (pure legitimate)
        requests_w1 = generate_workload_trace(
            workload_id="W1",
            trace_seed=trace_seed,
            total_duration=60.0,
            alpha_attack=0.0,
            fault_seed=fault_seed
        )

        seed_arm_outcomes = {}

        # Execute arms in randomized order
        for arm_name in arm_order:
            # Run W3
            sim_w3 = ArmSimulator(
                arm_name=arm_name,
                fault_rate=p_main,
                fault_seed=fault_seed,
                c_w=c_w,
                gateway_crash_time=35.0
            )
            outcome_w3 = sim_w3.run_workload(
                requests=requests_w3,
                measurement_window=(15.0, 75.0)
            )

            # For P-3 guardrail (B-OC and D-OC), also run W1 at alpha=0
            if arm_name in ("B-OC", "D-OC"):
                sim_w1 = ArmSimulator(
                    arm_name=arm_name,
                    fault_rate=0.0,  # No attack / zero faults
                    fault_seed=fault_seed,
                    c_w=c_w,
                    gateway_crash_time=None
                )
                outcome_w1 = sim_w1.run_workload(
                    requests=requests_w1,
                    measurement_window=(10.0, 50.0)
                )
                outcome_w3["W1_S_L"] = outcome_w1["S_L"]

            seed_arm_outcomes[arm_name] = outcome_w3

        # Compute paired contrast differences for this seed
        diff_p1a = seed_arm_outcomes["D-TR"]["OE_norm"] - seed_arm_outcomes["B-OC"]["OE_norm"]
        diff_p1b = seed_arm_outcomes["A'-TR"]["OE_norm"] - seed_arm_outcomes["B-OC"]["OE_norm"]
        diff_p2  = seed_arm_outcomes["B-OC"]["S_L"] - seed_arm_outcomes["D-TR"]["S_L"]
        diff_p3  = seed_arm_outcomes["B-OC"]["W1_S_L"] - seed_arm_outcomes["D-OC"]["W1_S_L"]
        diff_p4a = seed_arm_outcomes["B-TR"]["OE_norm"] - seed_arm_outcomes["B-OC"]["OE_norm"]
        diff_p4b = seed_arm_outcomes["B-OC"]["S_L"] - seed_arm_outcomes["B-TR"]["S_L"]

        contrasts["P-1a"].append(diff_p1a)
        contrasts["P-1b"].append(diff_p1b)
        contrasts["P-2"].append(diff_p2)
        contrasts["P-3"].append(diff_p3)
        contrasts["P-4a"].append(diff_p4a)
        contrasts["P-4b"].append(diff_p4b)

        per_seed_results.append({
            "seed_index": s_idx,
            "seed_value": s_val,
            "trace_seed": trace_seed,
            "fault_draw_seed": fault_seed,
            "arms": seed_arm_outcomes,
            "contrasts": {
                "P-1a": round(diff_p1a, 6),
                "P-1b": round(diff_p1b, 6),
                "P-2": round(diff_p2, 6),
                "P-3": round(diff_p3, 6),
                "P-4a": round(diff_p4a, 6),
                "P-4b": round(diff_p4b, 6)
            }
        })

    elapsed = time.time() - t0
    run_label = "registered simulation" if registered else "exploratory"
    print(f"\n[OK] Completed {n_seeds} paired {run_label} seed runs in {elapsed:.2f} seconds.")

    # -----------------------------------------------------------------------
    # §4. Statistical Evaluation & Multiplicity Testing
    # -----------------------------------------------------------------------
    print(
        "\n--- Registered Tests ---"
        if registered
        else "\n--- Descriptive Paired Contrast Summaries (Exploratory Only) ---"
    )

    contrast_summaries = {}
    statistical_tests = {}
    alpha = float(manifest["frozen_parameters"].get("alpha", 0.05))
    thresholds = {
        "P-1a": float(manifest["frozen_parameters"].get("delta_OE", 0.0)),
        "P-1b": float(manifest["frozen_parameters"].get("delta_OE", 0.0)),
        "P-2": float(manifest["frozen_parameters"].get("delta_C", 0.0)),
        "P-3": float(
            manifest["frozen_parameters"].get("non_inferiority_margin_P3", -0.03)
        ),
        "P-4a": float(manifest["frozen_parameters"].get("delta_OE", 0.0)),
        "P-4b": float(manifest["frozen_parameters"].get("delta_C", 0.0)),
    }
    diagnostics = manifest.get("assumption_diagnostics", {})
    use_wilcoxon = registered and not diagnostics.get(
        "all_assumptions_supported", False
    )
    if use_wilcoxon and diagnostics.get("fallback_test") != (
        "Wilcoxon signed-rank test (one-sided)"
    ):
        raise ValueError("registered fallback test is missing or unsupported")
    if registered:
        if __package__:
            from .preregistration_engine import (
                exact_wilcoxon_signed_rank_p,
                holm_adjustment,
                sign_flip_permutation_test,
            )
        else:
            from preregistration_engine import (
                exact_wilcoxon_signed_rank_p,
                holm_adjustment,
                sign_flip_permutation_test,
            )

    for hyp_id in ("P-1a", "P-1b", "P-2", "P-3", "P-4a", "P-4b"):
        diffs = contrasts[hyp_id]

        mean_diff = sum(diffs) / len(diffs)
        var_diff = sum((d - mean_diff) ** 2 for d in diffs) / (len(diffs) - 1)
        sd_diff = math.sqrt(var_diff)

        # Percentile Bootstrap 95% Confidence Interval
        boot_means = []
        b_rng = random.Random(20261011)
        for _ in range(10_000):
            sample = [b_rng.choice(diffs) for _ in range(len(diffs))]
            boot_means.append(sum(sample) / len(sample))
        boot_means.sort()
        ci_lower = boot_means[int(0.025 * 10_000)]
        ci_upper = boot_means[int(0.975 * 10_000)]

        contrast_summaries[hyp_id] = {
            "mean_difference": round(mean_diff, 5),
            "sd_difference": round(sd_diff, 5),
            "bootstrap_95_ci": [round(ci_lower, 5), round(ci_upper, 5)]
        }
        if registered:
            if use_wilcoxon:
                p_value = exact_wilcoxon_signed_rank_p(
                    diffs, thresholds[hyp_id]
                )
                test_name = "Exact one-sided Wilcoxon signed-rank"
            else:
                test_seed = int(
                    hashlib.sha256(
                        f"{recorded_hash}:{hyp_id}".encode("utf-8")
                    ).hexdigest()[:8],
                    16,
                )
                test_result = sign_flip_permutation_test(
                    diffs,
                    thresholds[hyp_id],
                    n_flips=int(
                        manifest["frozen_parameters"]["permutation_flips"]
                    ),
                    seed=test_seed,
                )
                p_value = test_result["p_value"]
                test_name = "One-sided paired sign-flip permutation"
            statistical_tests[hyp_id] = {
                "test": test_name,
                "threshold": thresholds[hyp_id],
                "p_value": p_value,
                "reject_at_alpha": p_value < alpha,
            }
            print(
                f"  {hyp_id}: mean={mean_diff:+.5f}, "
                f"threshold={thresholds[hyp_id]:+.5f}, "
                f"p={p_value:.8f}"
            )
        else:
            print(
                f"  {hyp_id}: mean={mean_diff:+.5f}, "
                f"descriptive 95% percentile bootstrap CI="
                f"[{ci_lower:+.5f}, {ci_upper:+.5f}]"
            )

    if registered:
        holm_results = holm_adjustment(
            {h: result["p_value"] for h, result in statistical_tests.items()},
            alpha=alpha,
        )
        for hypothesis, result in holm_results.items():
            statistical_tests[hypothesis]["holm"] = result
        all_six_reject = all(
            result["reject_at_alpha"] for result in statistical_tests.values()
        )
        joint_conclusion = (
            "All registered hypotheses reject for this simulation model only; "
            "this is not evidence about a deployed system."
            if all_six_reject
            else "The registered joint claim is not supported by this simulation run."
        )
        print(
            "\n[SIMULATION IUT]: "
            f"{'SUPPORTED IN THIS MODEL ONLY' if all_six_reject else 'NOT SUPPORTED'}"
        )
    else:
        all_six_reject = None
        joint_conclusion = (
            "Not assessed: the prior pilot thresholds were synthetic, so this "
            "run is exploratory and cannot support a registered joint claim."
        )
        print("\n[JOINT DECISION RULE]: NOT ASSESSED")
    print(f"  {joint_conclusion}")

    # -----------------------------------------------------------------------
    # §5. Secondary Analysis: Fault Sweep & Crossover Analysis
    # -----------------------------------------------------------------------
    print("\n--- Executing Exploratory Fault Sweep (p in {0.00, 0.003, 0.01, 0.05, 0.20}) ---")
    sweep_results = {}
    sweep_p = [0.00, 0.003, 0.01, 0.05, 0.20]

    for p_val in sweep_p:
        tr_oe_list = []
        oc_oe_list = []
        for s in seed_blocks[:10]:
            t_seed = s["trace_seed"]
            f_seed = s["fault_draw_seed"]
            reqs = generate_workload_trace(
                "W3", t_seed, 90.0, 0.25, fault_seed=f_seed
            )

            sim_tr = ArmSimulator("B-TR", p_val, f_seed, c_w, 35.0)
            res_tr = sim_tr.run_workload(reqs, (15.0, 75.0))
            tr_oe_list.append(res_tr["OE_norm"])

            sim_oc = ArmSimulator("B-OC", p_val, f_seed, c_w, 35.0)
            res_oc = sim_oc.run_workload(reqs, (15.0, 75.0))
            oc_oe_list.append(res_oc["OE_norm"])

        mean_tr = sum(tr_oe_list) / len(tr_oe_list)
        mean_oc = sum(oc_oe_list) / len(oc_oe_list)
        diff_oe = mean_tr - mean_oc
        sweep_results[str(p_val)] = {
            "p_fail": p_val,
            "B_TR_mean_OE_norm": round(mean_tr, 6),
            "B_OC_mean_OE_norm": round(mean_oc, 6),
            "diff_B_TR_minus_B_OC": round(diff_oe, 6)
        }
        print(f"  p = {p_val*100:04.1f}%: B-TR OE_norm={mean_tr:.5f}, B-OC OE_norm={mean_oc:.5f}, Diff={diff_oe:+.5f}")

    crossover_point = next(
        (
            float(cell)
            for cell, result in sweep_results.items()
            if result["diff_B_TR_minus_B_OC"] > 0.0
        ),
        None,
    )
    print(f"  -> First tested fault rate with lower B-OC OE: {crossover_point}")

    # -----------------------------------------------------------------------
    # §6. Export Confirmatory Results Package
    # -----------------------------------------------------------------------
    with open(__file__, "rb") as simulator_source:
        simulator_sha256 = hashlib.sha256(simulator_source.read()).hexdigest()

    final_output = {
        "meta": {
            "document": (
                "RESCUE-4 Registered Simulation Results Package"
                if registered
                else "RESCUE-4 Exploratory Simulation Results Package"
            ),
            "phase": (
                "New simulator-only confirmatory study"
                if registered
                else "Exploratory simulator repair rerun"
            ),
            "status": (
                "COMPLETED_SIMULATION_ONLY"
                if registered
                else "EXPLORATORY_NOT_CONFIRMATORY"
            ),
            "execution_timestamp": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            "pre_registration_sha256": manifest["meta"]["sha256_hash"],
            "simulator_sha256": simulator_sha256,
            "p_main": p_main,
            "n_seeds": n_seeds,
            "interpretation": (
                "Registered inference applies only to this simplified simulation "
                "model and its pilot-derived thresholds; it does not establish "
                "behavior in a deployed system."
                if registered
                else
                "Exploratory rerun using simulated mechanism outcomes. The frozen "
                "pilot thresholds were generated synthetically and are not valid "
                "for confirmatory inference."
            )
        },
        "paired_contrast_summaries": contrast_summaries,
        "statistical_tests": statistical_tests,
        "joint_novelty_claim": {
            "all_six_reject": all_six_reject,
            "conclusion": joint_conclusion,
            "decision_rule": "Intersection-Union Test (IUT)",
            "scope": "simulation model only" if registered else "not assessed"
        },
        "fault_sweep_secondary": {
            "sweep_results": sweep_results,
            "crossover_point_p_star": crossover_point,
            "zero_fault_overhead_check": (
                sweep_results["0.0"]["diff_B_TR_minus_B_OC"] == 0.0
            )
        },
        "simulation_limitations": [
            "This is a simplified, single-process Python simulation, not a deployed system.",
            "The A-prime, B, and DRACO-inspired policies do not establish production-policy fidelity.",
            "No PostgreSQL server, WAL stream, database trigger, or real concurrent execution was exercised.",
        ],
        "per_seed_records": per_seed_results
    }

    out_file = output_path
    with open(out_file, "x") as f:
        json.dump(final_output, f, indent=2)

    print(f"\n[OK] Exported exploratory results package to {out_file}")

    print("\n=================================================================")
    print(
        "  REGISTERED SIMULATION SUMMARY"
        if registered
        else "  EXPLORATORY SIMULATION SUMMARY"
    )
    print("=================================================================")
    print(f"  Seeds executed:            {n_seeds}")
    for h in ("P-1a", "P-1b", "P-2", "P-3", "P-4a", "P-4b"):
        print(f"  {h:4s}: mean_diff={contrast_summaries[h]['mean_difference']:+.5f}")
    print(
        "  Inferential status:      SIMULATION ONLY"
        if registered
        else "  Inferential status:      NOT ASSESSED"
    )
    print("=================================================================")

    return final_output


if __name__ == "__main__":
    from preregistration_engine import generate_repaired_preregistration

    if not os.path.isfile("config/preregistration_repaired.json"):
        generate_repaired_preregistration()
    run_confirmatory_experiment()
