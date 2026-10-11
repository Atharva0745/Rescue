"""
RESCUE-4 Transaction-Level Checker Differential Test Suite
Executes:
  1. Differential testing on 100,000 random histories (Checker vs Reference Oracle)
  2. Planted Stale-Write Detection (100% must be detected)
  3. Legitimate Late-Completion Verification (0% false positives)
  4. Mutant Detection (M1, M2, M3, M4 must all be detected)
  5. Edge cases: Replay deduplication, Multi-gateway bumps, Mixed transactions
"""

import sys
import time
import random
from typing import List, Dict, Any, Tuple
from transaction_checker import TransactionChecker, ReferenceOracle, TransactionRecord, FenceVersion


def generate_history(n_transactions: int = 100_000, seed: int = 20261011) -> List[TransactionRecord]:
    """
    Generates a realistic stream of committed transactions covering:
    - Valid fenced writes
    - Periodic fence bumps (incarnation and epoch increments)
    - Planted stale writes
    - Legitimate late completions
    - Mixed transactions
    - Replay transactions
    - Multiple gateways
    """
    rng = random.Random(seed)
    gateways = ["gw-alpha", "gw-beta"]
    
    # Active ground-truth versions per gateway
    ground_truth_ver: Dict[str, Tuple[int, int]] = {gw: (1, 0) for gw in gateways}
    ground_truth_seq: Dict[str, int] = {gw: 0 for gw in gateways}

    history: List[TransactionRecord] = []
    current_lsn = 1000
    write_counter = 1

    for tx_id in range(1, n_transactions + 1):
        current_lsn += rng.randint(10, 50)
        gw = rng.choice(gateways)
        cur_inc, cur_epoch = ground_truth_ver[gw]

        rand_val = rng.random()

        if rand_val < 0.05:
            # 5% chance: Legitimate fence bump transaction
            ground_truth_seq[gw] += 1
            seq = ground_truth_seq[gw]
            
            # 20% incarnation bump, 80% epoch bump
            if rng.random() < 0.20:
                new_inc = cur_inc + 1
                new_epoch = 0
            else:
                new_inc = cur_inc
                new_epoch = cur_epoch + 1
                
            ground_truth_ver[gw] = (new_inc, new_epoch)
            
            tx = TransactionRecord(
                tx_id=tx_id,
                lsn=current_lsn,
                gateway_id=gw,
                fence_bumps=[{
                    "gw": gw,
                    "seq": seq,
                    "inc_new": new_inc,
                    "epoch_new": new_epoch
                }]
            )

        elif rand_val < 0.10:
            # 5% chance: Planted stale write (violates fence)
            # Uses an older epoch/incarnation
            stale_epoch = max(0, cur_epoch - 1) if cur_epoch > 0 else 0
            stale_inc = max(1, cur_inc - 1) if stale_epoch == 0 and cur_inc > 1 else cur_inc
            if stale_inc == cur_inc and stale_epoch == cur_epoch:
                stale_epoch = 0  # Force stale if we can't decrement normally
                stale_inc = max(0, cur_inc - 1)

            w_id = f"w-{write_counter}"
            write_counter += 1
            tx = TransactionRecord(
                tx_id=tx_id,
                lsn=current_lsn,
                gateway_id=gw,
                audit_records=[{"write_id": w_id, "gw": gw, "inc": stale_inc, "epoch": stale_epoch}],
                data_mutations=[{"write_id": w_id, "table": "ledger", "key": "k1", "val": 100}],
                is_planted_stale=True
            )

        elif rand_val < 0.15:
            # 5% chance: Legitimate late completion
            # Write committed under valid current version; client ack happens later
            w_id = f"w-{write_counter}"
            write_counter += 1
            tx = TransactionRecord(
                tx_id=tx_id,
                lsn=current_lsn,
                gateway_id=gw,
                audit_records=[{"write_id": w_id, "gw": gw, "inc": cur_inc, "epoch": cur_epoch}],
                data_mutations=[{"write_id": w_id, "table": "ledger", "key": "k2", "val": 200}],
                is_legitimate_late=True
            )

        elif rand_val < 0.18:
            # 3% chance: Replay transaction (same LSN as an earlier transaction)
            if history:
                sample_prior = rng.choice(history[-20:])
                tx = TransactionRecord(
                    tx_id=tx_id,
                    lsn=sample_prior.lsn,
                    gateway_id=sample_prior.gateway_id,
                    fence_bumps=list(sample_prior.fence_bumps),
                    audit_records=list(sample_prior.audit_records),
                    data_mutations=list(sample_prior.data_mutations)
                )
            else:
                w_id = f"w-{write_counter}"
                write_counter += 1
                tx = TransactionRecord(
                    tx_id=tx_id,
                    lsn=current_lsn,
                    gateway_id=gw,
                    audit_records=[{"write_id": w_id, "gw": gw, "inc": cur_inc, "epoch": cur_epoch}],
                    data_mutations=[{"write_id": w_id, "table": "ledger", "key": "k3", "val": 300}]
                )

        elif rand_val < 0.20:
            # 2% chance: Mixed transaction (both bump and data write)
            ground_truth_seq[gw] += 1
            seq = ground_truth_seq[gw]
            new_inc = cur_inc
            new_epoch = cur_epoch + 1
            ground_truth_ver[gw] = (new_inc, new_epoch)
            w_id = f"w-{write_counter}"
            write_counter += 1

            tx = TransactionRecord(
                tx_id=tx_id,
                lsn=current_lsn,
                gateway_id=gw,
                fence_bumps=[{"gw": gw, "seq": seq, "inc_new": new_inc, "epoch_new": new_epoch}],
                audit_records=[{"write_id": w_id, "gw": gw, "inc": cur_inc, "epoch": cur_epoch}],
                data_mutations=[{"write_id": w_id, "table": "ledger", "key": "k_mix", "val": 400}]
            )

        else:
            # 80% chance: Standard valid fenced write
            w_id = f"w-{write_counter}"
            write_counter += 1
            tx = TransactionRecord(
                tx_id=tx_id,
                lsn=current_lsn,
                gateway_id=gw,
                audit_records=[{"write_id": w_id, "gw": gw, "inc": cur_inc, "epoch": cur_epoch}],
                data_mutations=[{"write_id": w_id, "table": "ledger", "key": "k_std", "val": 500}]
            )

        history.append(tx)

    return history


def run_differential_test(history: List[TransactionRecord]) -> Dict[str, Any]:
    print(f"\n--- Running Differential Test on {len(history):,} Committed Transactions ---")
    
    gateways = {"gw-alpha", "gw-beta"}
    checker = TransactionChecker(known_gateways=gateways)
    oracle = ReferenceOracle(known_gateways=gateways)

    disagreements = 0
    planted_stale_total = 0
    planted_stale_detected = 0
    legitimate_late_total = 0
    legitimate_late_flagged = 0

    t0 = time.time()
    last_print = t0

    for i, tx in enumerate(history):
        c_res = checker.process_transaction(tx)
        o_res = oracle.evaluate_transaction(tx)

        # Assert zero disagreement on overall validity
        if c_res["is_valid"] != o_res["is_valid"]:
            disagreements += 1
            print(f"Disagreement at tx {tx.tx_id} (LSN {tx.lsn}): Checker={c_res['is_valid']}, Oracle={o_res['is_valid']}")
            print(f"  Checker flags: {c_res['flags']}, Oracle flags: {o_res['flags']}")

        # Track planted stale writes
        if tx.is_planted_stale:
            planted_stale_total += 1
            if "FLAG_STALE_WRITE" in c_res["flags"]:
                planted_stale_detected += 1

        # Track legitimate late completions
        if tx.is_legitimate_late:
            legitimate_late_total += 1
            if not c_res["is_valid"]:
                legitimate_late_flagged += 1

        if (i + 1) % 25_000 == 0 or (time.time() - last_print > 3.0):
            elapsed = time.time() - t0
            rate = (i + 1) / elapsed
            print(f"Progress: {i+1:,}/{len(history):,} ({(i+1)/len(history)*100:.1f}%) | Rate: {rate:,.0f} tx/sec | Disagreements: {disagreements}")
            last_print = time.time()

    elapsed = time.time() - t0
    print(f"\nDifferential Verification Completed in {elapsed:.2f}s ({len(history)/elapsed:,.0f} tx/sec).")
    print(f"Total Disagreements: {disagreements}")
    print(f"Planted Stale Writes: {planted_stale_detected}/{planted_stale_total} detected ({planted_stale_detected/max(1, planted_stale_total)*100:.1f}%)")
    print(f"Legitimate Late Completions Flagged: {legitimate_late_flagged}/{legitimate_late_total} (0 expected)")

    assert disagreements == 0, f"Checker/Oracle had {disagreements} disagreements!"
    assert planted_stale_detected == planted_stale_total, "Not all planted stale writes were detected!"
    assert legitimate_late_flagged == 0, "Legitimate late completions were falsely flagged!"

    return {
        "transactions_tested": len(history),
        "disagreements": disagreements,
        "planted_stale_total": planted_stale_total,
        "planted_stale_detected": planted_stale_detected,
        "legitimate_late_total": legitimate_late_total,
        "legitimate_late_flagged": legitimate_late_flagged,
        "elapsed_seconds": elapsed,
        "throughput_tx_per_sec": len(history) / elapsed
    }


def run_mutant_suite() -> Dict[str, bool]:
    print("\n--- Evaluating Checker Mutants (M1--M4) ---")
    gateways = {"gw-alpha"}
    
    # 1. Mutant M1: Fails to detect stale writes
    print("Testing Mutant M1 (Stale Write Blindness)...")
    m1_checker = TransactionChecker(known_gateways=gateways, mutant_mode="M1_STALE_BLIND")
    # Bump to (1, 1)
    m1_checker.process_transaction(TransactionRecord(tx_id=1, lsn=100, gateway_id="gw-alpha", fence_bumps=[{"gw": "gw-alpha", "seq": 1, "inc_new": 1, "epoch_new": 1}]))
    # Stale write at (1, 0)
    stale_tx = TransactionRecord(tx_id=2, lsn=110, gateway_id="gw-alpha", audit_records=[{"write_id": "w1", "gw": "gw-alpha", "inc": 1, "epoch": 0}], data_mutations=[{"write_id": "w1", "table": "t", "key": "k", "val": 1}])
    res_m1 = m1_checker.process_transaction(stale_tx)
    m1_caught = (res_m1["is_valid"] is True)  # The mutant falsely marks it valid
    print(f"  M1 Behavior: Allowed stale write without error -> Mutant Caught: {m1_caught}")
    assert m1_caught, "Mutant M1 failed to be identified!"

    # 2. Mutant M2: Falsely flags legitimate late completions
    print("Testing Mutant M2 (Late Completion False Positive)...")
    m2_checker = TransactionChecker(known_gateways=gateways, mutant_mode="M2_LATE_FALSE_POS")
    late_tx = TransactionRecord(tx_id=3, lsn=120, gateway_id="gw-alpha", audit_records=[{"write_id": "w2", "gw": "gw-alpha", "inc": 1, "epoch": 0}], data_mutations=[{"write_id": "w2", "table": "t", "key": "k", "val": 2}], is_legitimate_late=True)
    res_m2 = m2_checker.process_transaction(late_tx)
    m2_caught = (res_m2["is_valid"] is False)  # The mutant falsely marks it invalid
    print(f"  M2 Behavior: Falsely flagged legitimate write -> Mutant Caught: {m2_caught}")
    assert m2_caught, "Mutant M2 failed to be identified!"

    # 3. Mutant M3: Mixed transaction blindness
    print("Testing Mutant M3 (Mixed Transaction Blindness)...")
    m3_checker = TransactionChecker(known_gateways=gateways, mutant_mode="M3_MIXED_BLIND")
    mixed_tx = TransactionRecord(tx_id=4, lsn=130, gateway_id="gw-alpha", fence_bumps=[{"gw": "gw-alpha", "seq": 2, "inc_new": 1, "epoch_new": 2}], audit_records=[{"write_id": "w3", "gw": "gw-alpha", "inc": 1, "epoch": 1}], data_mutations=[{"write_id": "w3", "table": "t", "key": "k", "val": 3}])
    res_m3 = m3_checker.process_transaction(mixed_tx)
    m3_caught = ("FLAG_MIXED_TRANSACTION" not in res_m3["flags"])
    print(f"  M3 Behavior: Allowed mixed transaction -> Mutant Caught: {m3_caught}")
    assert m3_caught, "Mutant M3 failed to be identified!"

    # 4. Mutant M4: Audit/Data desynchronization blindness
    print("Testing Mutant M4 (Audit/Data Desync Blindness)...")
    m4_checker = TransactionChecker(known_gateways=gateways, mutant_mode="M4_AUDIT_BLIND")
    unaudited_tx = TransactionRecord(tx_id=5, lsn=140, gateway_id="gw-alpha", data_mutations=[{"write_id": "w4", "table": "t", "key": "k", "val": 4}])  # No audit!
    res_m4 = m4_checker.process_transaction(unaudited_tx)
    m4_caught = ("FLAG_UNAUDITED_MUTATION" not in res_m4["flags"])
    print(f"  M4 Behavior: Allowed unaudited mutation -> Mutant Caught: {m4_caught}")
    assert m4_caught, "Mutant M4 failed to be identified!"

    return {"M1": m1_caught, "M2": m2_caught, "M3": m3_caught, "M4": m4_caught}


def run_edge_cases_and_restart_suite() -> bool:
    print("\n--- Evaluating Edge Cases and Crash/Restart Persistence ---")
    gateways = {"gw-alpha", "gw-beta"}
    checker = TransactionChecker(known_gateways=gateways)

    # 1. Process initial bump and write
    tx1 = TransactionRecord(tx_id=1, lsn=100, gateway_id="gw-alpha", fence_bumps=[{"gw": "gw-alpha", "seq": 1, "inc_new": 1, "epoch_new": 1}])
    checker.process_transaction(tx1)

    tx2 = TransactionRecord(tx_id=2, lsn=110, gateway_id="gw-alpha", audit_records=[{"write_id": "w1", "gw": "gw-alpha", "inc": 1, "epoch": 1}], data_mutations=[{"write_id": "w1", "table": "t", "key": "k", "val": 10}])
    checker.process_transaction(tx2)

    # 2. Checkpoint creation
    checkpoint = checker.create_checkpoint()

    # 3. Simulate process crash and restart from checkpoint
    restarted_checker = TransactionChecker(known_gateways=gateways)
    restarted_checker.restore_from_checkpoint(checkpoint)

    # 4. Verify replay deduplication on tx2 after restart
    res_replay = restarted_checker.process_transaction(tx2)
    assert res_replay["is_replay"] is True, "Replay not deduplicated after restart!"
    assert "FLAG_REPLAY_DEDUPLICATED" in res_replay["flags"]

    # 5. Unknown gateway check
    unknown_tx = TransactionRecord(tx_id=3, lsn=120, gateway_id="gw-unknown", audit_records=[{"write_id": "w2", "gw": "gw-unknown", "inc": 1, "epoch": 0}], data_mutations=[{"write_id": "w2", "table": "t", "key": "k", "val": 20}])
    res_unknown = restarted_checker.process_transaction(unknown_tx)
    assert "FLAG_UNKNOWN_GATEWAY" in res_unknown["flags"], "Unknown gateway not flagged!"

    # 6. Future version check
    future_tx = TransactionRecord(tx_id=4, lsn=130, gateway_id="gw-alpha", audit_records=[{"write_id": "w3", "gw": "gw-alpha", "inc": 2, "epoch": 0}], data_mutations=[{"write_id": "w3", "table": "t", "key": "k", "val": 30}])
    res_future = restarted_checker.process_transaction(future_tx)
    assert "FLAG_FUTURE_VERSION" in res_future["flags"], "Future version not flagged!"

    # 7. Non-monotone bump check
    bad_bump_tx = TransactionRecord(tx_id=5, lsn=140, gateway_id="gw-alpha", fence_bumps=[{"gw": "gw-alpha", "seq": 2, "inc_new": 1, "epoch_new": 0}])
    res_bad_bump = restarted_checker.process_transaction(bad_bump_tx)
    assert "FLAG_NON_MONOTONE_BUMP" in res_bad_bump["flags"], "Non-monotone bump not flagged!"

    # 8. Unaudited data mutation check
    unaudited_tx = TransactionRecord(tx_id=6, lsn=150, gateway_id="gw-alpha", data_mutations=[{"write_id": "w4", "table": "t", "key": "k", "val": 40}])
    res_unaudited = restarted_checker.process_transaction(unaudited_tx)
    assert "FLAG_UNAUDITED_MUTATION" in res_unaudited["flags"], "Unaudited data mutation not flagged!"

    # 9. Orphan audit record check
    orphan_tx = TransactionRecord(tx_id=7, lsn=160, gateway_id="gw-alpha", audit_records=[{"write_id": "w5", "gw": "gw-alpha", "inc": 1, "epoch": 1}])
    res_orphan = restarted_checker.process_transaction(orphan_tx)
    assert "FLAG_ORPHAN_AUDIT" in res_orphan["flags"], "Orphan audit record not flagged!"

    print("  All Edge Cases & Crash/Restart Invariants Verified Successfully.")
    return True


def main():
    print("=================================================================")
    print("       RESCUE TRANSACTION CHECKER DIFFERENTIAL SUITE           ")
    print("=================================================================")
    
    # 1. Edge Cases & Crash Restart
    run_edge_cases_and_restart_suite()

    # 2. Generate 100,000 transaction histories
    print("\nGenerating 100,000 committed transaction histories...")
    history = generate_history(n_transactions=100_000, seed=20261011)

    # 3. Run Differential Test vs Reference Oracle
    diff_results = run_differential_test(history)

    # 4. Run Mutant Suite M1--M4
    mutant_results = run_mutant_suite()

    print("\n=================================================================")
    print("  ALL CHECKER EXIT GATES PASSED! (0 Disagreements, Mutants Caught)")
    print("=================================================================")


if __name__ == "__main__":
    main()
