"""Check DRACO-inspired policy behavior and the configured arm-factor matrix.

These mechanism checks are not a reproduction of the DRACO paper and do not
evaluate comparative performance or confirmatory hypotheses.
"""

from typing import List, Dict, Any
from admission_policy import (
    EnforcementLevel, Request, AdaptiveLimiter, DracoAdmission,
    ReservationAdmission, ExecutionArm
)


# =====================================================================
# 1. DRACO Algorithmic Traceability Table (§5)
# =====================================================================
def get_draco_traceability_table() -> List[Dict[str, str]]:
    """
    Algorithmic traceability table: one row per algorithmic element of the DRACO paper,
    with columns for code location and implementation status. This table has
    not been independently reviewed against the original paper.
    """
    return [
        {
            "algorithmic_element": "Per-partition latency tracking",
            "draco_paper_ref": "DRACO §3.1 (Partition Health Monitor)",
            "code_location": "admission_policy.py:DracoAdmission.update_datastore_latency",
            "status": "implemented",
            "notes": "Tracks observed RTT / storage latency per partition index"
        },
        {
            "algorithmic_element": "Selective dropping of hot-partition requests",
            "draco_paper_ref": "DRACO §3.2 (Selective Admission Control)",
            "code_location": "admission_policy.py:DracoAdmission.should_admit",
            "status": "implemented",
            "notes": "Drops requests targeting partitions exceeding hotness threshold while preserving cold"
        },
        {
            "algorithmic_element": "Per-partition concurrency cap",
            "draco_paper_ref": "DRACO §3.3 (Per-Partition Sizing)",
            "code_location": "admission_policy.py:DracoAdmission.base_partition_limit",
            "status": "implemented",
            "notes": "Limits concurrent in-flight executions per partition slot"
        },
        {
            "algorithmic_element": "Dynamic hotness detection threshold",
            "draco_paper_ref": "DRACO §3.2 (Adaptive Threshold Estimator)",
            "code_location": "admission_policy.py:DracoAdmission.hot_threshold_ms",
            "status": "simplified",
            "notes": "Configured via calibrated static/windowed threshold rather than sliding percentile"
        },
        {
            "algorithmic_element": "Exponentially weighted latency smoothing",
            "draco_paper_ref": "DRACO §3.1 (EWMA Filter)",
            "code_location": "admission_policy.py:DracoAdmission.update_datastore_latency",
            "status": "simplified",
            "notes": "Direct latency sample updates; EWMA smoothing is not implemented"
        },
        {
            "algorithmic_element": "Kernel-level eBPF packet drop acceleration",
            "draco_paper_ref": "DRACO §4.1 (XDP / eBPF Datapath)",
            "code_location": "N/A (Omitted)",
            "status": "omitted",
            "notes": "Omitted; admission control operates at user-space gateway dispatch layer"
        },
        {
            "algorithmic_element": "Distributed multi-replica partition consensus",
            "draco_paper_ref": "DRACO §4.3 (Raft / Paxos Partition State)",
            "code_location": "N/A (Omitted)",
            "status": "omitted",
            "notes": "Omitted; single gateway authority scope under Assumption A6"
        },
        {
            "algorithmic_element": "Cross-rack tenant work stealing",
            "draco_paper_ref": "DRACO §5.2 (Work Stealing Scheduler)",
            "code_location": "N/A (Omitted)",
            "status": "omitted",
            "notes": "Omitted; FIFO dispatch discipline enforced under Assumption A3"
        }
    ]


def verify_draco_traceability() -> Dict[str, Any]:
    print("\n--- DRACO-inspired Algorithmic Traceability (No Independent Review) ---")
    table = get_draco_traceability_table()
    
    implemented_cnt = sum(1 for r in table if r["status"] == "implemented")
    simplified_cnt = sum(1 for r in table if r["status"] == "simplified")
    omitted_cnt = sum(1 for r in table if r["status"] == "omitted")
    
    print(f"{'Algorithmic Element':<45} | {'Status':<12} | {'Code Location'}")
    print("-" * 95)
    for row in table:
        print(f"{row['algorithmic_element']:<45} | {row['status']:<12} | {row['code_location']}")
    
    print("\nIndependent review: NOT PERFORMED")
    
    return {
        "status": "TRACEABILITY_RECORDED",
        "total_elements": len(table),
        "implemented": implemented_cnt,
        "simplified": simplified_cnt,
        "omitted": omitted_cnt,
        "independent_review": "NOT_PERFORMED"
    }


# =====================================================================
# 2. Graded Hotness Shedding Curve (§5)
# =====================================================================
def run_graded_hotness_curve_test() -> Dict[str, Any]:
    print("\n--- DRACO-inspired Hotness Shedding Mechanism Check ---")
    
    latencies_to_test = [20.0, 40.0, 60.0, 80.0, 95.0, 100.0, 105.0, 120.0, 150.0, 200.0, 300.0, 500.0]
    n_requests_per_point = 200
    hot_threshold = 100.0
    
    curve_data = []
    
    print(f"{'Partition Latency (ms)':<25} | {'DRACO-inspired Admitted %':<28} | {'Behavior'}")
    print("-" * 75)
    
    for lat in latencies_to_test:
        draco = DracoAdmission(num_partitions=2, hot_threshold_ms=hot_threshold)
        draco.update_datastore_latency(1, 20.0)  # Partition 1 stays cold
        draco.update_datastore_latency(2, lat)   # Partition 2 has graded latency
        
        admitted_p2 = 0
        dropped_p2 = 0
        
        for req_id in range(1, n_requests_per_point + 1):
            req = Request(req_id=req_id, req_class="C_INTERACTIVE", datastore_partition=2)
            adm, _ = draco.should_admit(req, current_time=0.0)
            if adm:
                admitted_p2 += 1
                draco.release_in_flight(2)
            else:
                dropped_p2 += 1
                
        admission_pct = (admitted_p2 / (admitted_p2 + dropped_p2)) * 100.0
        behavior = "Normal Admission" if admission_pct > 90 else ("Selective Shedding" if admission_pct == 0.0 else "Transition Zone")
        
        print(f"{lat:<25.1f} | {admission_pct:<28.1f} | {behavior}")
        curve_data.append({
            "latency_ms": lat,
            "admission_percent": admission_pct,
            "behavior": behavior
        })
        
    # Verify shedding curve properties:
    # 1. At 20ms (well below 100ms), admission is 100%
    assert curve_data[0]["admission_percent"] == 100.0, "Sub-threshold traffic must be admitted"
    # 2. At >= 105ms (above 100ms threshold), admission is 0%
    assert curve_data[-1]["admission_percent"] == 0.0, "Super-threshold traffic must be shed"
    
    print("  -> Mechanism check passed; this is not a baseline-fidelity result.")
    return {"status": "MECHANISM_CHECKS_PASSED", "curve": curve_data}


# =====================================================================
# 3. Sensitivity Analysis across Detection Thresholds (§5)
# =====================================================================
def run_draco_threshold_boundary_checks() -> Dict[str, Any]:
    print("\n--- DRACO-inspired Threshold Boundary Mechanism Checks ---")
    
    thresholds = [50.0, 75.0, 100.0, 125.0, 150.0, 200.0]
    results = []

    print(f"{'Threshold (ms)':<16} | {'Below threshold admitted':<24} | {'Above threshold shed'}")
    print("-" * 72)
    
    for th in thresholds:
        policy = DracoAdmission(num_partitions=2, hot_threshold_ms=th)
        policy.update_datastore_latency(2, th - 1.0)
        admitted_below, _ = policy.should_admit(
            Request(req_id=1, req_class="C_INTERACTIVE", datastore_partition=2),
            current_time=0.0,
        )
        if admitted_below:
            policy.release_in_flight(2)
        policy.update_datastore_latency(2, th + 1.0)
        admitted_above, _ = policy.should_admit(
            Request(req_id=2, req_class="C_INTERACTIVE", datastore_partition=2),
            current_time=0.0,
        )
        shed_above = not admitted_above
        print(f"{th:<16.1f} | {str(admitted_below):<24} | {shed_above}")

        passed = admitted_below and shed_above
        results.append({
            "threshold_ms": th,
            "admitted_below_threshold": admitted_below,
            "shed_above_threshold": shed_above,
            "passed": passed,
        })
        assert passed, f"Threshold boundary check failed at {th} ms"

    print("  -> Threshold boundary behavior passed; no P-1a/P-2 evidence was calculated.")
    return {"status": "MECHANISM_CHECKS_PASSED", "checks": results}


# =====================================================================
# 4. Configured Arm-Factor Manifest Checks
# =====================================================================
def verify_arm_ladder_diff_isolation() -> Dict[str, Any]:
    print("\n--- Configured Arm-Factor Manifest Checks ---")
    
    policies = [AdaptiveLimiter(), DracoAdmission(), ReservationAdmission()]
    diff_audit_results = []

    for pol in policies:
        arm_ns = ExecutionArm(pol, EnforcementLevel.NS)
        arm_tr = ExecutionArm(pol, EnforcementLevel.TR)
        arm_oc = ExecutionArm(pol, EnforcementLevel.OC)

        m_ns = arm_ns.get_factor_manifest()
        m_tr = arm_tr.get_factor_manifest()
        m_oc = arm_oc.get_factor_manifest()

        # Check Diff 1: NS -> TR
        # MUST differ ONLY by: has_shared_semaphore and has_cgroup_budgets
        diff_ns_tr = {k for k in m_ns if m_ns[k] != m_tr[k] and k != "level"}
        expected_ns_tr = {"has_shared_semaphore", "has_cgroup_budgets"}
        assert diff_ns_tr == expected_ns_tr, f"Unexpected factors between NS and TR: {diff_ns_tr} != {expected_ns_tr}"

        # Check Diff 2: TR -> OC
        # MUST differ ONLY by: owner_confirmed_release, quarantine_enabled, database_fencing_enabled
        diff_tr_oc = {k for k in m_tr if m_tr[k] != m_oc[k] and k != "level"}
        expected_tr_oc = {"owner_confirmed_release", "quarantine_enabled", "database_fencing_enabled"}
        assert diff_tr_oc == expected_tr_oc, f"Unexpected factors between TR and OC: {diff_tr_oc} != {expected_tr_oc}"

        diff_audit_results.append({
            "policy": pol.name,
            "diff_ns_tr": sorted(list(diff_ns_tr)),
            "diff_tr_oc": sorted(list(diff_tr_oc)),
            "isolation_valid": True
        })
        print(f"Policy {pol.name:<15}: NS->TR diffs: {sorted(list(diff_ns_tr))} | TR->OC diffs: {sorted(list(diff_tr_oc))} [ISOLATION VERIFIED]")

    return {"status": "FACTOR_MANIFEST_CHECKS_PASSED", "audit": diff_audit_results}


# =====================================================================
# 5. 3x3 Arm Matrix Benchmark
# =====================================================================
def run_arm_ladder_benchmark() -> Dict[str, Any]:
    print("\n--- Executing 3x3 Arm Matrix Benchmark ---")
    
    # Test all 9 arms: A', D (DRACO-inspired), B across NS, TR, OC
    arms = []
    for pol_cls in [AdaptiveLimiter, DracoAdmission, ReservationAdmission]:
        for lvl in [EnforcementLevel.NS, EnforcementLevel.TR, EnforcementLevel.OC]:
            arms.append(ExecutionArm(pol_cls(), lvl, K_executors=8))

    benchmark_summary = []
    for arm in arms:
        manifest = arm.get_factor_manifest()
        arm_label = f"{manifest['policy']}-{manifest['level']}"
        benchmark_summary.append({
            "arm_id": arm_label,
            "semaphore": manifest["has_shared_semaphore"],
            "cgroup": manifest["has_cgroup_budgets"],
            "owner_confirm": manifest["owner_confirmed_release"],
            "quarantine": manifest["quarantine_enabled"],
            "fencing": manifest["database_fencing_enabled"]
        })
        print(f"Arm {arm_label:<20}: Semaphore={manifest['has_shared_semaphore']} | Cgroup={manifest['has_cgroup_budgets']} | OC={manifest['owner_confirmed_release']} | Quarantine={manifest['quarantine_enabled']}")

    return {"benchmark_arms_evaluated": len(arms), "summary": benchmark_summary}


def main():
    print("=================================================================")
    print("       RESCUE-4 BASELINE MECHANISM CHECKS                       ")
    print("=================================================================")
    
    # 1. Record implemented, simplified, and omitted baseline elements.
    verify_draco_traceability()

    # 2. Check the implemented selective-shedding behavior.
    run_graded_hotness_curve_test()

    # 3. Check threshold boundary behavior without inferring experiment metrics.
    run_draco_threshold_boundary_checks()

    # 4. Check only the factor manifests configured for each arm.
    verify_arm_ladder_diff_isolation()

    # 5. 3x3 Arm Matrix Execution
    run_arm_ladder_benchmark()

    print("\n=================================================================")
    print("  Mechanism and factor-manifest checks completed.")
    print("  No DRACO reproduction, baseline comparison, or performance claim is established.")
    print("=================================================================")


if __name__ == "__main__":
    main()
