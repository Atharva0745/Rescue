# RESCUE-4 --- Statistical Pre-Registration Specification

**Document:** `docs/preregistration.md`  
**Specification Version:** v1.5  
**Phase:** Phase 6 --- Statistical Pre-Registration  
**Status:** SUPERSEDED (HISTORICAL)  
**Frozen At:** `2026-10-11T17:45:00Z`  
**Canonical SHA-256 Digest:** `219fcd691d3c650505ec9dd6e47e2a46d5e17546c83bbca0812ee89f3080f73c`

> **Validity notice:** This frozen document is preserved as historical
> provenance, but its pilot contrasts were generated from synthetic normal
> distributions rather than the simulator. Its thresholds and power estimate
> are therefore not valid for confirmatory inference. The repaired rerun is
> explicitly exploratory. It has been superseded by the repaired,
> simulator-pilot-based registration in `config/preregistration_repaired.json`
> and the 60-seed simulation-only run in
> `config/confirmatory_results_repaired_confirmatory.json`. The registered
> joint claim was not supported; this model does not establish deployed-system
> behavior.

---

## 1. Pre-Registration Statement & Scope

This pre-registration document formally binds the statistical analysis plan, primary hypotheses, effect size thresholds ($\delta$), operating fault rate ($p_{\text{main}}$), power analysis, and 25-seed block schedules for the RESCUE-4 confirmatory study. No confirmatory traces or testbed results were inspected prior to the freeze of this protocol.

---

## 2. Frozen Parameters

| Parameter | Frozen Value | Source / Method |
| :--- | :--- | :--- |
| **Operating Point $p_{\text{main}}$** | $0.05$ (5.0%) | Phase 5 Calibration (CASE_B, 4,500 trials; 95% Clopper–Pearson CI $[0.0440, 0.0570]$) |
| **OE Threshold $\delta_{OE}$** | $0.0328$ | Pilot variance rule: $\delta_{OE} = \max(0.01, 0.5 \times \min \mu_{\text{pilot}} = 0.5 \times 0.0656)$ |
| **Completion Threshold $\delta_C$** | $0.0138$ | Pilot variance rule: $\delta_C = \max(0.005, 0.5 \times \min \mu_{\text{pilot}} = 0.5 \times 0.0275)$ |
| **Stale Write Weight $c_w$** | $10.0$ | Normalized Orphaned-Work Exposure formula |
| **Non-Inferiority Margin** | $-0.03$ | Guardrail against D-OC on no-attack workload (W1, $\alpha=0$) |
| **Family-Wise Error Rate $\alpha$** | $0.05$ | Multiplicity-controlled via monotonic Holm step-down |
| **Confirmatory Sample Size $N$** | $25$ | Paired seeds; power $> 0.80$ confirmed across all contrasts |
| **Permutation Test Method** | Exact sign-flip | $2^{25} = 33,554,432$ evaluations via meet-in-the-middle algorithm |

---

## 3. Primary Hypotheses (Intersection-Union Test)

The RESCUE-4 joint novelty claim requires rejecting the null hypothesis across the pre-registered contrast family:

1. **P-1a (Superiority over D-TR):**
   - Alternative: $\text{OE}_{\text{norm}}(\text{D-TR}) - \text{OE}_{\text{norm}}(\text{B-OC}) > \delta_{OE}$
   - Workload: W3 | Fault Cell: $p_{\text{main}} = 0.05$
   - Purpose: Demonstrates orphaned-work reduction over the strengthened D (DRACO-inspired) baseline.

2. **P-1b (Superiority over A'-TR):**
   - Alternative: $\text{OE}_{\text{norm}}(\text{A'-TR}) - \text{OE}_{\text{norm}}(\text{B-OC}) > \delta_{OE}$
   - Workload: W3 | Fault Cell: $p_{\text{main}} = 0.05$
   - Purpose: Demonstrates orphaned-work reduction over the adaptive concurrency limiter.

3. **P-2 (Useful Completion over D-TR):**
   - Alternative: $S_L(\text{B-OC}) - S_L(\text{D-TR}) \ge \delta_C$
   - Workload: W3 | Fault Cell: $p_{\text{main}} = 0.05$
   - Purpose: Demonstrates superior legitimate-request throughput compared to a DRACO-inspired policy.

4. **P-3 (No-Attack Non-Inferiority Guardrail):**
   - Alternative: $S_L(\text{B-OC}) - S_L(\text{D-OC}) \ge -0.03$
   - Workload: W1 | Fault Cell: $\alpha = 0$ (No-Attack)
   - Purpose: Confirms that reservation and fencing impose no uncompensated throughput penalty when faults are absent.

5. **P-4a (Incremental OE Benefit within RESCUE-B):**
   - Alternative: $\text{OE}_{\text{norm}}(\text{B-TR}) - \text{OE}_{\text{norm}}(\text{B-OC}) > \delta_{OE}$
   - Workload: W3 | Fault Cell: $p_{\text{main}} = 0.05$
   - Purpose: Isolates the specific contribution of Owner-Confirmed release (OC) over timed release (TR).

6. **P-4b (Incremental Completion Benefit within RESCUE-B):**
   - Alternative: $S_L(\text{B-OC}) - S_L(\text{B-TR}) \ge \delta_C$
   - Workload: W3 | Fault Cell: $p_{\text{main}} = 0.05$
   - Purpose: Proves that owner-confirmed capacity recycling improves useful completions.

---

## 4. Multiplicity Adjustment and Test Statistics

### 4.1. Monotonic Holm Step-Down Adjustment
To enforce monotonicity and avoid paradoxes under tied raw p-values:
```python
def holm_adjustment(raw_p_values):
    m = len(raw_p_values)
    order = sorted(range(m), key=lambda i: raw_p_values[i])
    adj = [0.0] * m
    run = 0.0
    for rank, i in enumerate(order):
        multiplier = m - rank
        run = max(run, multiplier * raw_p_values[i])
        adj[i] = min(1.0, run)
    return adj
```

### 4.2. Exact Meet-in-the-Middle Permutation Test
Permutation testing over $N = 25$ paired differences evaluates all $2^{25}$ combinations exactly in $O(2^{N/2} \log 2^{N/2})$ time without Monte Carlo variance. The minimum achievable p-value is $2^{-25} \approx 2.9802 \times 10^{-8}$.

---

## 5. Seed Blocks & Exclusion Protocol

- **Sample Size:** 25 paired seed blocks (master seed `20261011`).
- **Seed Manifest:** Fixed blocks with independent fault draw seeds and execution trace seeds.
- **Randomized Execution Order:** Latin-square randomized arm execution order per block to prevent thermal or memory-leak confounding.
- **Exclusion Policy:** A seed is excluded *only* if an unrecoverable infrastructure crash halts execution across all arms. Excluded seeds are never replaced.
