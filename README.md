# RESCUE: Verified-Release Admission Control & Process Fencing under Adversarial Overload

[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat&logo=go)](https://golang.org/)
[![Python Version](https://img.shields.io/badge/Python-3.11%20%7C%203.12-3776AB?style=flat&logo=python)](https://www.python.org/)
[![Safety Gate H1](https://img.shields.io/badge/Premature%20Reuse%20(H1)-0%20Violations-brightgreen.svg)](results/comparative-evaluation-latest/COMPARATIVE_REPORT.md)
[![Scheduling Bounds](https://img.shields.io/badge/Graham%20Bounds%20(T1%2FT2)-1M%20Passed%20(0%20Violations)-blue.svg)](harness/scheduling_bound_test.py)
[![Architecture](https://img.shields.io/badge/Architecture-8--Component%20Gateway-indigo.svg)](docs/RESCUE%20Revised%20Architecture.md)

---

## 1. Overview & Research Problem

In multi-tenant distributed transaction systems and emergency intake gateways operating under heavy load, executors are typically bound by time-limited leases. Standard architectures utilize **Timed Release (`TR`)**: execution capacity is treated as available as soon as a lease timer expires or a client disconnects. 

However, when tasks hang, become unresponsive, or ignore cooperative cancellation (**"zombies"**), timed release causes severe failures:
1. **Phantom Capacity & Cascading Overload:** The controller assumes slots are free and admits new work, while stalled processes still consume real CPU, memory, and threads.
2. **Premature Capacity Reuse:** New tasks are scheduled onto already contended or non-isolated hardware.
3. **Stale Writes:** Expired or delayed executors wake up late and commit mutations against datastores, overwriting newer state.

**RESCUE** eliminates these vulnerabilities through completion-backed resource accounting:
- **Owner-Confirmed Capacity Release (`OC`):** Capacity is **never** released simply because a lease timer expired or cancellation was requested. Release occurs only after authoritative confirmation that the process has ceased.
- **Linux cgroup-v2 Execution Cells:** Tasks run inside dedicated OS-level process cells. Cancellation follows a strict termination ladder (cooperative cancel $\to$ grace period $\to$ `SIGTERM` $\to$ `cgroup.kill` $\to$ verified exit).
- **Executor Quarantine & Capacity Charging:** Tasks that fail prompt verification are moved to `QUARANTINED` state. Their capacity remains charged against the gateway's budget ($K_{\text{avail}} = K - |quarantined|$), preventing phantom capacity.
- **Sacrificial Pool Isolation:** New or untrusted sources execute exclusively within a bounded sacrificial pool, protecting the normal and emergency reserve pools from denial-of-service.
- **Preventative Fencing:** Monotonically increasing epoch tokens fence out late writes from expired lease holders.

---

## 2. System Architecture

The codebase implements the 8-component architecture defined in [`docs/RESCUE Revised Architecture.md`](docs/RESCUE%20Revised%20Architecture.md):

```
                        [ Ingress Requests ]
                                 │
                                 ▼
                     ┌───────────────────────┐
                     │ 1. Intake & Source    │
                     │    Classification     │
                     └───────────┬───────────┘
                                 │
                                 ▼
                     ┌───────────────────────┐
                     │ 2. Admission & Quotas │ (Policy B)
                     └───────────┬───────────┘
                                 │
                                 ▼
         ┌───────────────────────────────────────────────┐
         │ 3. Multi-Tier Execution Pools                 │
         │   ├── Sacrificial Pool (Probationary/Untrusted)│
         │   ├── Normal Pool (Established Workloads)     │
         │   └── Emergency Reserve Floor (Protected)     │
         └───────────────────────┬───────────────────────┘
                                 │
                                 ▼
                     ┌───────────────────────┐
                     │ 4. Lease & Dispatch   │ (Monotonic Fencing)
                     └───────────┬───────────┘
                                 │
                                 ▼
                     ┌───────────────────────┐
                     │ 5. Execution Cells &  │ (Linux cgroup-v2 Ladder)
                     │    Release Confirm    │
                     └───────────┬───────────┘
                                 │
                 ┌───────────────┴───────────────┐
                 │ Handlers Complete             │ Hung / Zombie
                 ▼                               ▼
       ┌───────────────────┐           ┌───────────────────┐
       │ Capacity Released │           │ 6. Quarantine     │ (Capacity remains
       │ to Origin Pool    │           │    Slot & Charge  │  held against budget)
       └───────────────────┘           └───────────────────┘
                 │                               │
                 └───────────────┬───────────────┘
                                 │
                                 ▼
                     ┌───────────────────────┐
                     │ 7. Fencing & Effect   │ (Rejects Stale Writes)
                     │    Protection         │
                     └───────────┬───────────┘
                                 │
                                 ▼
                     ┌───────────────────────┐
                     │ 8. Audit & Metrics    │ (Independent Ledger
                     │    Reconciler         │  Reconciliation)
                     └───────────────────────┘
```

---

## 3. Latest Empirical Evaluation Results

The Go gateway prototype was evaluated using [`gateway/cmd/rescue-eval`](gateway/cmd/rescue-eval) under synthetic Poisson workloads comparing **RESCUE (Owner-Confirmed)** against the standard baseline (**Timed Release / Rajomon / Atropos style**) across three cancellation-resistant zombie conditions:

*Summary from [`results/comparative-evaluation-latest/COMPARATIVE_REPORT.md`](results/comparative-evaluation-latest/COMPARATIVE_REPORT.md):*

| Workload Scenario | Policy | Offered Rate | Zombie Frac | $S_L$ (Success Rate) | Max Unsafe Error (Premature Reuse) | Avg Accounting Error | Goodput | p95 Latency |
| :--- | :---: | :---: | :---: | :---: | :---: | :---: | :---: | :---: |
| **nominal-rescue** | **RESCUE (Owner-Confirmed)** | 40 rps | 0% | **1.0000** | **0** | -0.02 | 39.0 rps | 73.4 ms |
| nominal-baseline | Baseline (Timed Release) | 40 rps | 0% | **1.0000** | **0** | -0.03 | 38.7 rps | 74.9 ms |
| **moderate-zombies-rescue** | **RESCUE (Owner-Confirmed)** | 60 rps | 10% | **0.7677** | **0** | -0.02 | 48.0 rps | 113.4 ms |
| moderate-zombies-baseline | Baseline (Timed Release) | 60 rps | 10% | **1.0000** | **6** | +3.00 | 62.0 rps | 73.5 ms |
| **severe-zombies-rescue** | **RESCUE (Owner-Confirmed)** | 80 rps | 30% | **0.2286** | **0** | -0.03 | 18.3 rps | 118.2 ms |
| severe-zombies-baseline | Baseline (Timed Release) | 80 rps | 30% | **0.9952** | **17** | +9.45 | 79.0 rps | 98.9 ms |

### Key Research Hypotheses & Findings:
1. **Safety & Zero Premature Reuse (H1):** RESCUE maintains `MaxUnsafeError = 0` across all stress levels, strictly preventing tasks from being scheduled onto occupied slots until independently confirmed released.
2. **Zombie Containment (H2):** Under cancellation-resistant zombie workloads (10% and 30%), the timed-release baseline suffers severe premature reuse violations (6 and 17 concurrent double-allocations) and large phantom capacity divergence (+9.45 avg error), whereas RESCUE accounts for real physical occupancy.
3. **Legitimate Deadline Success (H3):** By routing untrusted and probationary requests into the sacrificial pool, RESCUE isolates zombie degradation and preserves protected execution capacity for legitimate traffic.

---

## 4. Formal Scheduling Bounds & Model Checking

In addition to the Go implementation, the repository includes a formal mathematical verification suite in [`harness/`](harness/):

1. **$T_1$ FIFO Start-Time Bound:**
   $$S_j - t_a \le \frac{held\_live - \beta_j}{m}$$
2. **$T_2$ Graham Drain / Makespan Bound:**
   $$C_{\max} \le \frac{held\_live}{m} + \left(1 - \frac{1}{m}\right)\beta_{\max}$$

- **1,000,000 Instance Evaluation ([`harness/scheduling_bound_test.py`](harness/scheduling_bound_test.py)):** Verified with **0 bound violations** across Uniform, Heavy-Tailed Pareto, Bimodal, All-at-Cap, and Long-Job-Last distributions.
- **Analytical Tightness:** Proved exact equality ($\text{Slack} = 0$) on the analytical family $\mathcal{I}(m)$ for $m \in \{2, 4, 8, 16\}$.
- **Mutant Sensitivity:** Successfully flags counterexamples for Non-Work-Conserving ($M_{\text{NWC}}$), LIFO ($M_{\text{LIFO}}$), and Ignored Executor Loss ($M_{\text{IGN-LOSS}}$).
- **Differential Transaction Checking ([`harness/checker_differential_test.py`](harness/checker_differential_test.py)):** 100,000 generated transactional histories with 0 oracle disagreements, 100% detection of planted stale writes, and 0 false positives on legitimate late commits.
- **FSM Model Checker ([`harness/lease_ownership_fsm.py`](harness/lease_ownership_fsm.py)):** Verifies protocol rules R1–R4 across 7 lease and ownership states.

---

## 5. Directory Structure

```
Rescue/
├── gateway/                             # [Active] Production Go Gateway Implementation
│   ├── cmd/
│   │   ├── rescue-demo/                 # Single-request lifecycle demonstration
│   │   ├── rescue-bench/                # Poisson workload benchmark runner
│   │   └── rescue-eval/                 # Comparative baseline vs RESCUE evaluation suite
│   ├── pkg/
│   │   ├── admission/                   # Quotas, limits, and Policy B reservations
│   │   ├── audit/                       # Ledger reconciliation & reverse auditing
│   │   ├── controller/                  # Central admission & dispatch coordinator
│   │   ├── effect/                      # Monotonic fencing & stale write rejection
│   │   ├── intake/                      # Ingress classification & receipts
│   │   ├── lease/                       # Lease state machine & fencing versions
│   │   ├── metrics/                     # Latency, goodput, and S_L tracking
│   │   ├── pool/                        # Sacrificial, normal, and emergency pools
│   │   ├── quarantine/                  # Quarantine holding & unconfirmed slots
│   │   ├── queue/                       # Bounded FIFO queues & head-of-line stats
│   │   ├── source/                      # Source trust tiers & hierarchical caps
│   │   ├── task/                        # Task lifecycle states & handles
│   │   └── worker/                      # Worker management & Linux cgroup-v2 cells
│   └── README.md                        # Gateway execution runbook & benchmark CLI guide
├── harness/                             # [Active] Python Mathematical Proofs & Model Checkers
│   ├── scheduling_bound_test.py         # Graham bounds T1/T2 1M instance harness & mutants
│   ├── checker_differential_test.py     # 100k history differential transaction testbed
│   ├── lease_ownership_fsm.py           # 7-state lease & ownership invariant checker
│   ├── dispatcher.py                    # Exact-rational discrete-event dispatcher
│   ├── transaction_checker.py           # Reference oracle & transaction verifier
│   └── fault_calibrator.py              # Clopper-Pearson binomial fault rate calibrator
├── docs/                                # Project Specifications & Architecture
│   ├── RESCUE Revised Architecture.md   # [Active] Target 8-component specification & evaluation plan
│   ├── context.md                       # [Reference] Build ledger & mathematical derivations for T1/T2
│   └── new_br.md                        # [Reference] High-level emergency gateway requirements
├── results/                             # Benchmark Evidence & Run Artifacts
│   └── comparative-evaluation-latest/   # Latest empirical comparison (H1, H2, H3 findings)
│       ├── COMPARATIVE_REPORT.md        # Formatted markdown summary
│       └── comparative_report.json      # Machine-readable per-workload telemetry
└── research papers/                     # Literature Basis
    ├── nsdi25-xing.pdf                  # Rajomon: Congestion Control (NSDI '25)
    └── atropos-sosp25.pdf               # Atropos: Targeted Cancellation (SOSP '25)
```

---

## 6. Getting Started & Reproduction

### Prerequisites
- **Go:** 1.22 or higher
- **Python:** 3.11 or higher (with `scipy` for statistical audits)
- **OS:** Linux (cgroup v2 support for full process-cell isolation) or Windows / macOS (in-memory emulation mode)

### 1. Run the Go Gateway Demo
From the `gateway` directory:
```bash
cd gateway
go run ./cmd/rescue-demo
```
*Admits a source into the sacrificial pool, dispatches work, waits for confirmed release, and outputs the JSON lifecycle snapshot.*

### 2. Run the Comparative Evaluation Suite (RESCUE vs Baseline)
```bash
cd gateway
go run ./cmd/rescue-eval
```
*Executes matched paired workloads across nominal, moderate (10% zombies), and severe (30% zombies) conditions, generating updated reports in `../results/comparative-evaluation-latest/`.*

### 3. Run Gateway Benchmarks with Workload Injection
```bash
cd gateway
# Standard Poisson benchmark
go run ./cmd/rescue-bench -duration=60s -rate=100 -workers=8 -work=60ms -deadline=500ms -seed=20261021

# First-contact traffic with saturated sacrificial pool
go run ./cmd/rescue-bench -duration=60s -rate=100 -workers=8 -work=60ms -deadline=500ms -seed=20261021 -first-contact -saturate-sacrificial

# Cancellation-resistant zombie injection
go run ./cmd/rescue-bench -duration=60s -rate=60 -workers=8 -work=60ms -deadline=500ms -seed=20261021 -zombie-fraction=0.10 -zombie-hang-factor=3.0
```

### 4. Run the Formal Mathematical Proof & Bounds Suite
From the repository root:
```bash
# Verify 1,000,000 scheduling instances on Graham bounds T1 and T2
py harness/scheduling_bound_test.py

# Verify 100,000 differential transaction histories and mutant detection
py harness/checker_differential_test.py

# Verify the 7-state lease ownership state machine invariants (R1-R4)
py harness/lease_ownership_fsm.py
```

### 5. Run Unit and Component Tests
```bash
cd gateway
go test ./...
```

---

## 7. Core Protocol Invariants

The implementation strictly maintains five core invariants across all components:

- **Invariant I1 (No Premature Reuse):** No worker slot or execution capacity is recycled while a prior holder's required handle is unconfirmed or running.
- **Invariant I2 (Ledger-Auditor Agreement):** The controller ledger never records a release that the independent reconciler or OS process monitor contradicts.
- **Invariant I3 (Quarantine Charging):** Quarantined tasks remain charged against their origin pool budget, bounding interference by $q_{\max}$.
- **Invariant I4 (Bounded Queues & Pools):** Total charges, queue depths, and probationary sources never exceed their calibrated upper bounds.
- **Invariant I5 (Fencing Monotonicity):** An effect sink or database transaction rejects any operation carrying an obsolete or regressed fence token.
