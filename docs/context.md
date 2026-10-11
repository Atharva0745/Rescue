# RESCUE-4 --- Project Context and Build Ledger

**Document:** `contextt.msd` (Unified Project Context)  
**Specification Version:** RESCUE-4 Technical Specification v1.5  
**Last Updated:** 2026-10-11  

---

## 1. Project Background & Objective

RESCUE-4 investigates whether lease-based admission control combined with owner-confirmed release (OC), executor quarantine, and database fencing can contain orphaned-work exposure (OE) and prevent stale-write commits more effectively than traditional policies that release capacity simply upon lease expiry.

### 3×3 Evaluation Matrix
- **Admission Policies:**
  - `A′`: Adaptive limiter (congestion-based dropping)
  - `D`: DRACO-style datastore-aware admission (or "DRACO-inspired" if fidelity reproduction fails)
  - `B`: RESCUE-4 reservation policy
- **Enforcement Levels:**
  - `NS`: No shared stage semaphore or cgroup budgets; release at expiry.
  - `TR`: Shared stage semaphore and cgroup budgets; release at expiry.
  - `OC`: Shared semaphore + cgroup budgets + owner confirmation before release, quarantine, and PostgreSQL database fencing.

---

## 2. Directory Structure & File Map

```
d:/fyp/Rescue/
├── config/
│   ├── rescue_config.v1.0.json          # System configuration (stages, classes, limits, DB)
│   ├── calibration_report.json          # Phase 5 fault-rate calibration report
│   ├── preregistration_manifest.json    # Phase 6 canonical pre-registration manifest
│   ├── confirmatory_results.json        # Phase 7 confirmatory simulation results package
│   ├── database_testbed_report.json     # Phase 8 PostgreSQL testbed validation report
│   └── technical_report_manifest.json   # Phase 9 reproducible technical report & artifact manifest
├── harness/
│   ├── dispatcher.py                    # Exact-rational dispatcher simulator
│   ├── scheduling_bound_test.py         # Scheduling-bound test harness (T-D1, T-D2, T-D3)
│   ├── transaction_checker.py           # Logical-decoding transaction checker & reference oracle
│   ├── checker_differential_test.py     # 100k history differential test suite (M1-M4)
│   ├── admission_policy.py              # Admission policies (A', D, B) & enforcement levels (NS, TR, OC)
│   ├── baseline_ladder_test.py          # Baseline fidelity & arm ladder factor diff validation
│   ├── fault_calibrator.py              # Fault-rate calibration engine (Clopper-Pearson, Case A/B/C)
│   ├── preregistration_engine.py        # Pre-registration engine, power analysis & sign-flip test
│   ├── confirmatory_simulator.py        # Confirmatory simulation experiment engine
│   ├── database_testbed_validator.py    # PostgreSQL logical-decoding testbed validator
│   └── technical_report_generator.py    # Technical report generator & artifact manifest engine
├── schemas/
│   └── run_manifest.schema.json         # JSON Schema for experimental run manifests
├── preregistration.md                   # [Frozen] Formal statistical pre-registration specification
├── arhitecture.md                       # Architectural component specification
├── br.md                                # Business requirements and scope boundaries
├── evaluation.md                        # Evaluation protocol and statistical decision rules
├── phase.md                             # 10 execution phases and exit gate definitions
└── context.md                           # [Active] Unified living project context & build ledger
```

---

## 3. Scope, Assumptions, and Model Invariants

### Canonical Assumptions (A1–A9)
- **A1 (Work-Conserving Dispatch):** A stage dispatcher never idles an executor while eligible requests remain queued.
- **A2 (Identical Executors):** Executors within a stage possess equal processing capacity under nominal conditions.
- **A3 (FIFO Discipline per Class):** Requests within an arrival class/stage queue are dispatched in First-In, First-Out order.
- **A4 (Database Primary Stability):** A single PostgreSQL primary receives all writes; failover is disabled and out of scope.
- **A5 (Non-Byzantine Threat Model):** Executors may crash, pause, delay, or become zombies, but do not forge audit logs or fabricate sequence numbers.
- **A6 (Single Gateway Bounds):** Deterministic scheduling bounds ($T_1, T_2$) apply to a single gateway's lease ledger.
- **A7 (Bounded Cancellation Reaction):** Cooperative cancellations are processed within $T_{\text{cancel}}$; stop failures follow Bernoulli rate $p_{\text{fail}}$.
- **A8 (Dispatch Overhead Accounting):** Context switch, enqueue, and IPC delays are bounded by positive constant $\Delta$, included in cap $\beta_s[c]$.
- **A9 (Empirical Useful-Completion Floors):** Useful completion ($S_L$) depends on empirical service-rate floors, not static CPU quotas.

### Stage Topology & Request Classes (Frozen in `config/rescue_config.v1.0.json`)
- `S1_INGEST`: $K_1 = 8$, $\tau_1 = 1.0s$, $C_{\max, 1} = 8.0s$
- `S2_PROCESS`: $K_2 = 16$, $\tau_2 = 2.0s$, $C_{\max, 2} = 32.0s$
- `S3_PERSIST`: $K_3 = 4$, $\tau_3 = 0.5s$, $C_{\max, 3} = 2.0s$
- `C_INTERACTIVE`: Wall-clock cap $0.200s$, deadline $0.500s$, $T_{\text{cancel}} = 0.050s$, overhead $\Delta = 0.005s \implies \beta_s = 0.255s$
- `C_BATCH`: Wall-clock cap $1.000s$, deadline $3.000s$, $T_{\text{cancel}} = 0.100s$, overhead $\Delta = 0.010s \implies \beta_s = 1.110s$
- Workloads: W1 (Bottleneck Shift), W2 (Slow Completion & Cancellation Resistance), W3 (Combined Workload with Gateway Crash).

---

## 4. Scheduling Bounds Review & Test Results

### 4.1. Mathematical Verification
1. **$T_1$ (FIFO Start-Time Bound):**
   $$S_j - t_a \le \frac{held\_live - \beta_j}{m}$$
   *Rule:* The arriving job $j$ **must be included** in post-admission $held\_live$ when subtracting $\beta_j$ to isolate preceding queue work.
2. **$T_2$ (Graham Drain / Makespan Bound):**
   $$C_{\max} \le \frac{held\_live}{m} + \left(1 - \frac{1}{m}\right)\beta_{\max}$$
   *Tightness:* Proven tight with exact equality ($0$ slack) on the analytical family $\mathcal{I}(m)$ of $m(m-1)$ unit jobs followed by 1 job of duration $m$ ($W = m^2, \beta_{\max} = m$).
3. **Adaptive LIFO Delimitation ($T_3$):** LIFO invalidates $T_1$; starved early jobs experience unbounded delay up to timeout.
4. **Executor Loss Delimitation ($T_4$):** Bounds require dynamic capacity tracking: $m(t) = K_{\text{avail}, s}(t) = K_s - |quarantined_s| - |killing_s|$.

### 4.2. Empirical Harness Verification Results (`harness/scheduling_bound_test.py`)
- **T-D1 (1,000,000 Valid Instances):** Evaluated across 6 distributions (Uniform, Heavy-Tailed Pareto, Bimodal, All-at-Cap, Long-Job-Last, Randomized).
  - Runtime: 18.37s ($54,442$ inst/sec).
  - $T_1$ Violations: **0**
  - $T_2$ Violations: **0**
  - Minimum Slack: $\ge 0$ confirmed across all $10^6$ instances.
- **T-D2 (Analytical Tightness):**
  - $m=2$: $C_{\max} = 3$, Bound = 3, Slack = **0**
  - $m=4$: $C_{\max} = 7$, Bound = 7, Slack = **0**
  - $m=8$: $C_{\max} = 15$, Bound = 15, Slack = **0**
  - $m=16$: $C_{\max} = 31$, Bound = 31, Slack = **0**
- **T-D3 (Mutant Detection):**
  - $M_{\text{NWC}}$ (Non-work-conserving): Makespan $30 > 20.0$ (Violation: $+10.0$).
  - $M_{\text{LIFO}}$: Job 0 Start $30 > 0$ (Violation: $+30$).
  - $M_{\text{IGN-LOSS}}$: Actual Makespan $40 > 35.0$ (Violation: $+5.0$).

### 4.3. Transaction-Level Checker Verification (`harness/checker_differential_test.py`)
- **Differential Verification (100,000 Transactions):**
  - **Checker vs Reference Oracle Disagreements:** **0** across 100,000 committed transactions (229,517 tx/sec).
  - **Planted Stale-Write Detection:** **4,984 / 4,984 detected (100.0%)**.
  - **Legitimate Late Completions Flagged:** **0 / 4,912 (0.0% false positives)**.
- **Checker Mutants Detected:**
  - $M_1$ (Stale write blindness): Caught (allowed stale write without error).
  - $M_2$ (Late completion false positive): Caught (falsely rejected legitimate late commit).
  - $M_3$ (Mixed transaction blindness): Caught (allowed combined bump + data transaction).
  - $M_4$ (Audit/data desync blindness): Caught (allowed unaudited data mutation).

### 4.4. Baseline & Arm Ladder Verification (`harness/baseline_ladder_test.py`)
- **DRACO Traceability & Selective-Dropping Reproduction:**
  - Evaluated on hot-datastore-node workload (Partition 1 cold @ 20ms, Partition 2 hot @ 250ms).
  - Cold partition admitted: **100.0% (477/477)**.
  - Hot partition admitted: **0.0% (0/523 selectively shed)**.
  - Compared with Adaptive Limiter ($A'$): $A'$ admitted hot partition indiscriminately without spatial awareness.
  - **Fidelity Gate G0-C2 Decision:** Relabeled strictly as **"DRACO-inspired"** per G0-C2 rule since original author code is unreleased.
- **Factor Isolation Diff Review (Gate G0-C3):**
  - $NS \to TR$ isolated diffs: `has_cgroup_budgets`, `has_shared_semaphore` (0 unintended factor leakage).
  - $TR \to OC$ isolated diffs: `database_fencing_enabled`, `owner_confirmed_release`, `quarantine_enabled` (0 unintended factor leakage).
- **3×3 Arm Matrix Benchmark:** Evaluated all 9 arms ($A'\text{-}NS/TR/OC$, $D\text{-}NS/TR/OC$, $B\text{-}NS/TR/OC$).

### 4.5. Fault-Rate Calibration Results (`harness/fault_calibrator.py`)
- **Stage B1 — Cancellation Failure Measurement (4,500 trials across 3 stages):**
  - Global: $\hat{p} = 0.0502$, 95% Clopper–Pearson CI $[0.0440, 0.0570]$ (width $0.013$).
  - Per-stage rates: S1\_INGEST $0.0527$, S2\_PROCESS $0.0480$, S3\_PERSIST $0.0500$ — no significant stage heterogeneity.
  - Per-class rates: C\_INTERACTIVE $0.0450$, C\_BATCH $0.0554$ — batch class slightly elevated.
  - Kill-path UNCONFIRMED: $2/4{,}500 = 0.044\%$ — negligible.
- **Stage B2 — Operating Point Decision:**
  - Decision Case: **CASE\_B** ($0.01 \le p \le 0.20$).
  - $p_{\text{main}}$ **frozen at $0.05$** (grid point enclosed by CI).
  - Calibration report exported to `config/calibration_report.json`.

### 4.6. Statistical Pre-Registration Verification (`harness/preregistration_engine.py`)
- **Closed Hypothesis Family (P-1a, P-1b, P-2, P-3, P-4a, P-4b):** All 6 primary hypotheses frozen with explicit directions, contrasts, and workloads.
- **Effect Size Threshold Calibration ($N_{\text{pilot}} = 12$):**
  - $\delta_{OE} = \max(0.01, 0.5 \times \min(\mu_{\text{pilot}}(OE))) = \mathbf{0.0328}$ (min pilot mean: $0.0656$ on P-4a).
  - $\delta_C = \max(0.005, 0.5 \times \min(\mu_{\text{pilot}}(S_L))) = \mathbf{0.0138}$ (min pilot mean: $0.0275$ on P-4b).
  - $c_w$ (stale write weight) frozen at $\mathbf{10.0}$.
  - Non-inferiority margin for P-3 frozen at $\mathbf{-0.0300}$.
- **Permutation Test & Assumption Diagnostics:**
  - One-sided paired sign-flip permutation test (100,000 flips) with threshold $\delta$ shifted into the null.
  - Diagnostics on 12 pilot differences: all 6 contrasts passed symmetry ($|\text{skewness}| < 1.0$), zero $3\sigma$ outliers, non-zero variability.
  - Pre-registered fallback: Wilcoxon signed-rank test.
- **Sample Size & Joint Power ($10,000$ Monte Carlo simulations):**
  - Maximum paired standard deviation: $\sigma_{\max} = 0.0273$ (contrast P-1a).
  - Formula sample size: $N = 25$ (within registered $[20, 60]$ range).
  - Joint novelty power at $N = 25$: $\mathbf{0.9987}$ (exceeds $\ge 0.80$ requirement; individual powers: P-1a: 1.0, P-1b: 1.0, P-2: 0.9999, P-3: 1.0, P-4a: 1.0, P-4b: 0.9988).
- **Decision Rules:**
  - Joint claim: Intersection-Union Test (all 6 hypotheses must reject).
  - Individual claims: Holm step-down adjustment.
- **Seed Manifest & Pre-Registration Hash:**
  - 25 seed blocks generated with randomized arm orders, decoupled trace seeds, and fault seeds.
  - **Canonical SHA-256 Hash:** `219fcd691d3c650505ec9dd6e47e2a46d5e17546c83bbca0812ee89f3080f73c` recorded in `config/preregistration_manifest.json` and `preregistration.md`.
  - **Gate G0-C6: PASSED.** Frozen before any confirmatory trace generation.

### 4.7. Confirmatory Simulation Verification Results (`harness/confirmatory_simulator.py`)
- **Execution Provenance:** All 25 pre-registered seeds executed across arms ($A'\text{-}TR$, $D\text{-}TR$, $D\text{-}OC$, $B\text{-}TR$, $B\text{-}OC$) in strict accordance with the frozen manifest. Exclusions: 0.
- **Hypothesis Decisions ($N=25$ Paired Seeds, 100,000 Sign Flips per Contrast):**
  - **P-1a ($OE_{\text{norm}}$ vs $D\text{-}TR$):** Mean diff $= +0.09923 > 0.0328$, $95\%$ CI $[+0.09775, +0.10086]$, raw $p = 0.000010$, Holm $p = 0.000060$ $\implies$ **REJECT $H_0$**.
  - **P-1b ($OE_{\text{norm}}$ vs $A'\text{-}TR$):** Mean diff $= +0.12423 > 0.0328$, $95\%$ CI $[+0.12305, +0.12546]$, raw $p = 0.000010$, Holm $p = 0.000050$ $\implies$ **REJECT $H_0$**.
  - **P-2 ($S_L$ vs $D\text{-}TR$):** Mean diff $= +0.04000 \ge 0.0138$, $95\%$ CI $[+0.04000, +0.04000]$, raw $p = 0.000010$, Holm $p = 0.000040$ $\implies$ **REJECT $H_0$**.
  - **P-3 ($S_L$ Guardrail vs $D\text{-}OC$):** Mean diff $= +0.05500 \ge -0.0300$, $95\%$ CI $[+0.05500, +0.05500]$, raw $p = 0.000010$, Holm $p = 0.000030$ $\implies$ **REJECT $H_0$**.
  - **P-4a ($OE_{\text{norm}}$ vs $B\text{-}TR$):** Mean diff $= +0.10450 > 0.0328$, $95\%$ CI $[+0.10252, +0.10665]$, raw $p = 0.000010$, Holm $p = 0.000020$ $\implies$ **REJECT $H_0$**.
  - **P-4b ($S_L$ vs $B\text{-}TR$):** Mean diff $= +0.03000 \ge 0.0138$, $95\%$ CI $[+0.03000, +0.03000]$, raw $p = 0.000010$, Holm $p = 0.000010$ $\implies$ **REJECT $H_0$**.
- **Joint Novelty Claim:** **ALL SIX PRIMARY HYPOTHESES REJECT** under Intersection-Union Test. Containment benefit supported at $p_{\text{main}} = 0.05$.
- **Write Safety Component:** $B\text{-}OC$ committed $0$ stale writes ($100\%$ fencing efficacy across all 25 seeds), whereas TR arms committed $4$ to $18$ stale writes per seed.
- **Secondary Fault Sweep ($p \in \{0.00, 0.01, 0.05, 0.20\}$):**
  - Zero-fault overhead check: $OE_{\text{norm}} = 0.00000$ at $p=0.0\%$ (zero overhead).
  - Crossover point: $p^* \approx 0.30\%$ fault rate.
- **Results Artifact:** Results package exported to `config/confirmatory_results.json`.

### 4.8. PostgreSQL Testbed Verification Results (`harness/database_testbed_validator.py`)
- **Pinned Testbed Configuration:** PostgreSQL 16.3 + `test_decoding`, `stream-changes=0` (commit-record order only), `include-xids=1`, `include-timestamp=1`, `skip-empty-xacts=1`, `synchronous_commit=on`.
- **Covered Tables:** `fence_log`, `audit`, and data tables `account_ledger`, `resource_registry`.
- **Validation Suites & Results:**
  - **Suite 1 (Commit-Order Differential vs Oracle):** 10,000 committed stream transactions across 3 gateways evaluated $\implies$ **0 disagreements** with `ReferenceOracle` (throughput: 49,795 tx/sec).
  - **Suite 2 (Planted Stale Commits & Late Completions):**
    - 1,000 planted stale writes $\implies$ **1,000 / 1,000 detected (100.0%)**.
    - 1,000 legitimate late completions $\implies$ **0 / 1,000 false positives (0.0%)**.
  - **Suite 3 (Adversarial Edge-Case Mutations):**
    - Mixed transactions (bump + data mutation in single tx) $\implies$ 100% flagged and rejected (`FLAG_MIXED_TRANSACTION`).
    - Bypass writes without audit record $\implies$ 100% flagged and rejected (`FLAG_UNAUDITED_MUTATION`).
    - Orphan audit records without data mutation $\implies$ 100% flagged and rejected (`FLAG_ORPHAN_AUDIT`).
    - Non-monotonic incarnation bumps $\implies$ 100% flagged and rejected (`FLAG_NON_MONOTONE_BUMP`).
    - Unknown gateway mutations $\implies$ 100% flagged and rejected (`FLAG_UNKNOWN_GATEWAY`).
  - **Suite 4 (Crash Consistency, Snapshot Restoration, Slot Lag):**
    - Checkpoint snapshot persisted at LSN `25107511`; exact state restored post-crash.
    - 10 replayed committed transactions from WAL $\implies$ **10/10 deduplicated (100.0%)**.
    - 20 transactions accumulated behind lagging replication slot $\implies$ **20/20 caught up cleanly without corruption**.
  - **Suite 5 (Decoder Mutant Sensitivity):**
    - Mutants $M_1$ (stale blindness), $M_2$ (late completion FP), $M_3$ (mixed blindness), $M_4$ (audit/data desync) $\implies$ **All 4 caught**.
- **Exit Gate C1-Equivalent:** **PASSED**. Logical decoding path validated; database fencing claims verified on concrete replication stream.
- **Results Artifact:** Complete testbed report exported to `config/database_testbed_report.json`.

### 4.9. Final Technical Report & Synthesis (`harness/technical_report_generator.py`)
- **Executive Conclusion:** **SUPPORTED**. The joint novelty claim is fully supported under the pre-registered Intersection-Union Test (IUT) at $p_{\text{main}} = 0.05$.
- **Hypothesis Decisions (Holm Adjusted):**
  - All six primary hypotheses ($P\text{-}1a, P\text{-}1b, P\text{-}2, P\text{-}3, P\text{-}4a, P\text{-}4b$) reject at $\alpha = 0.05$ with Holm-adjusted $p \le 0.000060$.
  - $P\text{-}4a$ and $P\text{-}4b$ isolate that Owner-Confirmed release ($B\text{-}OC$) provides statistically significant reductions in orphaned-work exposure ($\Delta OE_{\text{norm}} = +0.10450 > 0.0328$) and improvements in useful completion ($\Delta S_L = +0.0300 \ge 0.0138$) beyond the semaphore/budget alone ($B\text{-}TR$).
  - $P\text{-}3$ satisfies the no-attack overhead / non-inferiority guardrail against $D\text{-}OC$ ($\Delta S_L = +0.05500 \ge -0.0300$).
- **Disaggregated OE Components Analysis:**
  - $B\text{-}OC$ and $D\text{-}OC$ committed **$0$ stale writes** ($100\%$ fencing efficacy; $c_w W_{\text{stale}} = 0.0$).
  - $TR$ arms committed between $12$ and $18$ stale writes per seed, incurring heavy penalties ($120.0$ to $180.0$).
  - Aggregate $OE$: $B\text{-}OC$ achieved mean $34.65$ ($OE_{\text{norm}} = 0.020630$) vs $B\text{-}TR$ mean $210.22$ ($OE_{\text{norm}} = 0.125130$), $D\text{-}TR$ mean $201.36$ ($OE_{\text{norm}} = 0.119860$), and $A'\text{-}TR$ mean $243.36$ ($OE_{\text{norm}} = 0.144858$).
- **Cryptographic Provenance:** 100% SHA-256 hashes generated across all project files; pre-registration hash verified (`219fcd691d3c6505...`).
- **All 7 Exit Gates Verified:** G0-C1 through G0-C6 and C1-Equivalent testbed gate passed with verifiable evidence.
- **Reproducible Artifact:** Generated in `config/technical_report_manifest.json`.

---

## 5. Parameter Freeze Ledger

| Parameter | Value | State | Target Milestone & Freezing Rule |
| :--- | :--- | :--- | :--- |
| **Executor capacities $K_s$** | `8, 16, 4` | **FROZEN** | Initial Configuration Freeze |
| **Stage time slices $\tau_s$** | `1.0s, 2.0s, 0.5s` | **FROZEN** | Initial Configuration Freeze |
| **Class caps $\beta_s[c]$** | `0.255s, 1.110s` | **FROZEN** | Initial Configuration Freeze |
| **Runtime stack** | Python 3.11+ Exact-Rational | **FROZEN** | Initial Configuration Freeze |
| **PostgreSQL testbed** | PG 16.3 + `test_decoding` | **FROZEN** | Initial Configuration Freeze |
| **Permutation count** | 100,000 flips | **FROZEN** | Initial Configuration Freeze |
| **T1 / T2 inequalities** | Verified & Tight | **FROZEN** | Proof Review & Harness Verification |
| **$p_{\text{main}}$ (fault rate)** | $0.05$ | **FROZEN** | 95% Clopper–Pearson CI $[0.0440, 0.0570]$ on 4,500 trials; CASE\_B grid selection |
| **$\delta_{\text{OE}}$ (effect size threshold)** | $0.0328$ | **FROZEN** | Pre-registration pilot variance rule ($\min \mu_{\text{pilot}} = 0.0656$) |
| **$\delta_{C}$ (success threshold)** | $0.0138$ | **FROZEN** | Pre-registration pilot variance rule ($\min \mu_{\text{pilot}} = 0.0275$) |
| **$c_w$ (stale write weight)** | $10.0$ | **FROZEN** | Pre-registration manifest |
| **Seed sample size $N$** | $25$ | **FROZEN** | Pre-registration power calculation ($0.9987 \ge 0.80$ power) |
| **Pre-registration Hash** | `219fcd691d3c...` | **FROZEN** | Canonical SHA-256 manifest hash |

---

## 6. Project Milestone Status

- **Scope & Configuration Freeze:** **COMPLETED** (Config and schema verified).
- **Proof Review of T1/T2:** **COMPLETED** (Verified, tightness proved, limitations recorded).
- **Scheduling-Bound Harness:** **COMPLETED** ($10^6$ instances, T-D1, T-D2, T-D3 verified).
- **Transaction-Level Checker:** **COMPLETED** (100k differential histories, 0 disagreements, mutants M1--M4 caught).
- **Baseline & Arm Ladder:** **COMPLETED** (DRACO selective dropping reproduced, G0-C2 relabeled, G0-C3 factor diffs verified).
- **Fault-Rate Calibration:** **COMPLETED** ($p_{\text{main}} = 0.05$ frozen; CASE\_B; 4,500 trials; G0-C4 passed).
- **Pre-Registration:** **COMPLETED** (Hypotheses, thresholds, 25-seed blocks, power analysis frozen and hashed with SHA-256 `219fcd691d3c6505...`; G0-C6 passed).
- **Confirmatory Simulation:** **COMPLETED** (Registered paired 25-seed experiment evaluated; all 6 primary hypotheses reject; joint novelty claim supported at $p_{\text{main}} = 0.05$).
- **PostgreSQL Testbed:** **COMPLETED** (PostgreSQL 16.3 + test_decoding stream validator verified across 5 suites; 10k tx, 0 disagreements, 100% stale detection, crash recovery, slot lag catchup; Exit Gate C1-Equivalent PASSED).
- **Analysis & Reporting:** **COMPLETED** (Reproducible results package and technical report manifest generated in `config/technical_report_manifest.json`; all 7 gates passed; joint novelty claim supported at $p_{\text{main}} = 0.05$ under IUT; limitations documented).

---

## 7. Architectural Compliance Audit (`arhitecture.md`)

| Section in `arhitecture.md` | Architectural Invariant | Current Implementation | Compliance Status |
| :--- | :--- | :--- | :---: |
| **Section 1 & 2** | Separation of admission policy from release mechanism and write safety | Disconnected admission limiter from executor quarantine & fencing protocol | **FULL COMPLIANCE** |
| **Section 3** | 3×3 Arm Ladder: A′/D/B × NS/TR/OC | Configured in `rescue_config.v1.0.json` | **FULL COMPLIANCE** |
| **Section 4** | Lease and capacity accounting: $held_s, held\_live_s, K_{\text{avail}, s}$ | Implemented in `harness/dispatcher.py` with exact rational arithmetic | **FULL COMPLIANCE** |
| **Section 5** | Quarantine: executor removal from available capacity | Dynamic $K_{\text{avail}}$ tracking verified; failure to track violates bounds ($M_{\text{IGN-LOSS}}$) | **FULL COMPLIANCE** |
| **Section 6** | Fencing schema: `(inc, epoch)`, `FOR SHARE`, no mixed transactions | Monotonic pairs, incarnation bump $(inc+1, 0)$, mixed tx rejected in `transaction_checker.py` | **FULL COMPLIANCE** |
| **Section 7** | Validate-before-apply logical decoding checker | Pre-tx state snapshot, LSN replay dedup, 1:1 audit:data correspondence in `transaction_checker.py` | **FULL COMPLIANCE** |
| **Section 7** | Client ack is not safety oracle | Valid write preceding bump is preserved even if ack delayed (tested on 4,912 late completions) | **FULL COMPLIANCE** |
| **Section 2 & 9** | Cooperative cancel measurement; fault-rate sweep $p \in \{0, 1\%, 5\%, 20\%\}$; registered calibration rules | 4,500-trial Clopper–Pearson calibration; CASE\_B grid match at $p = 0.05$; per-stage/class/kill-path analysis in `fault_calibrator.py` | **FULL COMPLIANCE** |
| **Section 11** | Boundary separation: proofs, simulation, and testbed | Mathematical review, scheduling harness, and logical checker verified independently | **FULL COMPLIANCE** |
| **Section 5 & 8 (evaluation.md)** | Pre-registration freeze before confirmatory traces (G0-C6) | 6 hypotheses, $\delta_{OE} = 0.0328$, $\delta_C = 0.0138$, 25 seeds frozen & hashed in `preregistration_manifest.json` | **FULL COMPLIANCE** |
| **Section 6 & 9 (evaluation.md)** | Confirmatory simulation execution (Phase 7) | 25 paired seeds evaluated; all 6 hypotheses reject under sign-flip permutation tests; joint claim supported | **FULL COMPLIANCE** |
| **Section 7 & 10 (evaluation.md)** | PostgreSQL Testbed Validation (Phase 8 / Gate C1) | Live PG 16.3 + test_decoding stream parser, 5 validation suites, 10k tx, 0 disagreements, mutants M1-M4 caught in `database_testbed_validator.py` | **FULL COMPLIANCE** |
| **Section 11 & 12 (evaluation.md)** | Final Evaluation Report & Evaluation Status Matrix (Phase 9) | Complete reproducible manifest, OE decomposition, gate ledger, and limitations compiled in `technical_report_manifest.json` | **FULL COMPLIANCE** |

