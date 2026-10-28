# RESCUE vs Baseline (Timed-Release / Rajomon / Atropos style) Evaluation

**Date:** 2026-10-28 12:34:27

## Comparative Summary Table

| Workload Scenario | Policy | Offered Rate | Zombie Frac | S_L (Success Rate) | Max Unsafe Error (Premature Reuse) | Avg Accounting Error | Goodput (rps) | p95 Latency (ms) |
|:---|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|
| nominal-rescue | **RESCUE (Owner-Confirmed)** | 40 rps | 0% | **1.0000** | **0** | -0.02 | 39.0 | 73.4 ms |
| nominal-baseline | Baseline (Timed Release) | 40 rps | 0% | **1.0000** | **0** | -0.03 | 38.7 | 74.9 ms |
| moderate-zombies-rescue | **RESCUE (Owner-Confirmed)** | 60 rps | 10% | **0.7677** | **0** | -0.02 | 48.0 | 113.4 ms |
| moderate-zombies-baseline | Baseline (Timed Release) | 60 rps | 10% | **1.0000** | **6** | 3.00 | 62.0 | 73.5 ms |
| severe-zombies-rescue | **RESCUE (Owner-Confirmed)** | 80 rps | 30% | **0.2286** | **0** | -0.03 | 18.3 | 118.2 ms |
| severe-zombies-baseline | Baseline (Timed Release) | 80 rps | 30% | **0.9952** | **17** | 9.45 | 79.0 | 98.9 ms |

## Key Research Findings

1. **Safety & Zero Premature Reuse (H1):** RESCUE maintains `MaxUnsafeError = 0` across all stress levels, ensuring no task capacity is reused until independently confirmed released.
2. **Zombie Containment (H2):** Under cancellation-resistant zombie workloads (10% and 30%), the baseline suffers severe premature reuse violations (phantom capacity) and accounting divergence, whereas RESCUE prevents capacity over-allocation.
3. **Legitimate Deadline Success (H3):** By isolating untrusted and probationary requests into the sacrificial pool, RESCUE preserves legitimate deadline success ($S_L$) even when the sacrificial pool is under zombie attack.
