# RESCUE-4 --- Architecture

**Source:** RESCUE-4 Technical Specification v1.5\
**Status:** Proposed architecture; implementation and validation are
pending.

> This document describes the target architecture, not current completion
> status. See [`phase.md`](phase.md) for verified work and remaining gates.

## 1. Architectural Goal

RESCUE-4 investigates containment of work that continues after its lease
expires. Its architecture separates admission policy from the mechanism
that releases capacity and enforces write safety. This separation
enables controlled comparisons and helps identify whether owner
confirmation, quarantine, and fencing add value beyond shared
concurrency controls.

## 2. High-Level Components

``` text
                    +-------------------------+
Incoming requests ->| Admission policy        |
                    | A' / D / B               |
                    +------------+------------+
                                 |
                                 v
                    +-------------------------+
                    | Lease / reservation     |
                    | ledger and stage limits |
                    +------------+------------+
                                 |
                                 v
                    +-------------------------+
                    | Stage dispatcher        |
                    | FIFO or policy-specific |
                    +------------+------------+
                                 |
                                 v
                    +-------------------------+
                    | Executor                |
                    | wall-clock budget       |
                    | cooperative cancel     |
                    +------------+------------+
                                 |
                    +------------+-------------+
                    |                          |
                    v                          v
        +---------------------+    +--------------------------+
        | Release controller  |    | Fenced database write    |
        | TR or OC             |    | incarnation + epoch       |
        +----------+----------+    +-------------+------------+
                   |                             |
                   v                             v
        +---------------------+    +--------------------------+
        | Capacity / quarantine|   | PostgreSQL data + audit  |
        | state                |   | + fence_log              |
        +---------------------+    +-------------+------------+
                                                 |
                                                 v
                                    +--------------------------+
                                    | Logical decoding checker |
                                    | commit-order validation  |
                                    +--------------------------+
```

This diagram is conceptual. It does not imply that components have been
implemented or that the proposed interactions have been validated.

## 3. Admission Policies

The three admission policies are:

-   **A′:** adaptive limiter.
-   **D:** DRACO-style datastore-aware admission policy. It may be
    called a DRACO comparison only after the full-text traceability and
    reproduction protocol passes; otherwise label it "DRACO-inspired."
-   **B:** RESCUE-4 reservation policy.

Each policy is evaluated under three enforcement/release levels.

  -----------------------------------------------------------------------
  Level             Shared stage      Capacity release  Quarantine, owner
                    semaphore and                       confirmation,
                    cgroup budgets                      database fence
  ----------------- ----------------- ----------------- -----------------
  NS                No                At lease expiry   No

  TR                Yes               At lease expiry   No

  OC                Yes               After owner       Yes
                                      confirmation      
  -----------------------------------------------------------------------

The ladder supports two separate comparisons: 1. NS → TR estimates the
effect of the shared semaphore and execution budgets. 2. TR → OC
estimates the incremental effect of confirmation, quarantine, and
fencing.

The novelty claim rests on the second comparison, not on the semaphore
alone.

## 4. Lease and Capacity Model

For request class `c` at stage `s`:

-   `β_s[c]` is the slot-occupancy cap, including the wall-clock cap,
    maximum stop-confirmation time, and dispatch overhead allowance.
-   `w[c][s] = β_s[c]` is the reservation weight.
-   `K_s` is the configured executor count.
-   `K_avail_s` is the number of executors not quarantined or being
    killed.
-   `held_s` is the sum of caps for admitted, unreleased leases.
-   `held_live_s` excludes quarantined leases.
-   `Cmax_s = K_s · τ_s`, with admission constrained by
    `limit_s ≤ Cmax_s`.

The design distinguishes: - **Occupancy bounds:** conditional on
enforced wall-clock caps, work-conserving dispatch and the stated
executor assumptions. - **Useful completion:** conditional on measured
service-rate floors. CPU placement and bandwidth caps do not guarantee a
minimum useful-completion rate.

The FIFO start-time bound, makespan bound, adaptive-LIFO limitation, and
executor-loss handling are specified in v1.5 and require independent
review and testing before use as validated guarantees.

## 5. Release and Quarantine

### TR --- Release at expiry

Capacity is released at lease expiry. This is the strengthened baseline
with shared stage semaphores and cgroup budgets, but without
owner-confirmed release, quarantine, or database fencing.

### OC --- Owner-confirmed release

Capacity is not treated as safely reusable merely because the lease
expires. The design requires owner confirmation and includes quarantine
and database fencing. If stop or kill completion cannot be confirmed,
the executor is treated as UNCONFIRMED and removed from available
capacity according to the specified policy.

Exact state transitions, timeout handling, and retry behavior must be
implemented consistently with the technical specification. This
architecture summary does not invent additional transitions.

## 6. Database Fencing and Audit

### Data model

-   `fence_log(gw, seq, inc_new, epoch_new)` records fence bumps in the
    same transaction as each bump.
-   `audit(write_id, gw, inc, epoch)` records the fence version
    associated with a fenced write.
-   A version is the lexicographically ordered pair `(inc, epoch)`.
-   A new incarnation registers as `(inc + 1, 0)`.

The v1.5 threat model includes crashes, delays, pauses, and zombie
executors. It does not include Byzantine writers that forge audit
values.

### Fenced-write requirements

-   The fence check and data mutation must occur in the same database
    transaction.
-   The fence row is held with `FOR SHARE` until transaction end, so a
    conflicting bump update cannot pass the in-flight write without
    waiting.
-   Audit records and data changes must be correlated one-to-one
    according to the checker contract.
-   A bump transaction and a write transaction may not be mixed under
    the proposed checker protocol.
-   Fence versions must be monotonic per gateway.

These are design requirements, not evidence that the implementation
already satisfies them.

## 7. Transaction-Level Checker

The checker consumes committed transactions in commit-record order from
logical decoding, with in-progress transaction streaming disabled for
the pinned configuration.

For each transaction it: 1. Deduplicates replay using the commit LSN
checkpoint. 2. Snapshots current per-gateway fence state. 3. Collects
fence-log rows, audit rows, and relevant data-table mutations. 4.
Validates audit versions against pre-transaction state. 5. Flags mixed
transactions, unknown gateways, stale versions, future versions,
non-monotone bumps, and unaudited or orphan audit records. 6. Applies
fence bumps only after validation.

A write that committed before a bump is valid even if the client
receives its acknowledgement later. Client-observed completion time is
not the safety oracle.

The implementation must define crash-consistent persistence of checker
state and checkpoint, as well as coverage of all relevant database
mutations. The testbed checker must pass planted-violation,
legitimate-late-completion, differential, edge-case, restart/replay, and
mutant tests before database-fencing claims are made.

## 8. Observability and Metrics

### Orphaned-work exposure

`OE` combines executor-seconds spent running past lease expiry and
stale-write commits weighted by the registered stale-write cost `c_w`.
Components must also be reported separately.

The registered normalized metric is:

`OE_norm = OE / (K_s · T_window)`, summed over stages.

### Legitimate-request success

Per-seed `S_L` is the fraction of harness-labeled legitimate requests
arriving in the measurement window whose full response is delivered by
`arrival + D_c`. Rejections and timeouts count as failures.

### Supporting telemetry

-   Executor occupancy and lease state.
-   Cancellation and kill outcomes.
-   Quarantined and UNCONFIRMED executors.
-   Service-rate measurements and degraded-stage windows.
-   CPU steal, frequency, throttling, memory/cache stressor regime, and
    executor affinity.
-   PostgreSQL commit/audit/fence records and logical-decoding
    checkpoint.
-   Seed, arm order, fault draws, environment, and configuration
    manifest.

## 9. Workloads and Experimental Paths

-   **W1:** bottleneck shift; includes the no-attack overhead guardrail.
-   **W2:** slow completion, withheld acknowledgement, or hung executor.
-   **W3:** W1 + W2 + gateway crash.

Confirmatory tests use paired seeds and identical traces/fault draws
across compared arms, with randomized arm order. The fault-rate sweep
includes `p_fail ∈ {0, 1%, 5%, 20%}` as secondary results, subject to
the registered calibration rules and additional cells specified for
above-grid rates.

## 10. Deployment and Runtime Assumptions

The current specification explicitly limits its claims: - Single gateway
for the stated deterministic bounds. - Failover behavior is out of scope
under A4. - Multi-gateway behavior is not claimed beyond the defined
checker model and tests. - Byzantine writers are out of scope. -
Useful-completion guarantees depend on measured service-rate
envelopes. - CPU affinity and bandwidth limits do not independently
guarantee minimum CPU service. - Memory-bandwidth and cache contention
are not isolated unless the relevant control mechanism is used.

## 11. Validation Boundaries

Three kinds of evidence must remain distinct:

1.  **Mathematical review:** independent review of T1/T2 and their
    assumptions.
2.  **Simulation evidence:** scheduling-bound tests, checker
    differential histories, arm ladder, and fault sweep.
3.  **Database testbed evidence:** real logical-decoding path,
    PostgreSQL fencing, replay/restart, and planted/mutant tests.

Simulation results that depend on database fencing remain labeled
simulation-only until the real testbed checker passes its C1-equivalent.
DRACO comparison claims remain prohibited until the baseline fidelity
protocol passes.

## 12. Threats to Validity & Baseline Fidelity Scope

The baseline admission policy D is labeled **"DRACO-inspired"** because the implementation is simplified and has not been independently verified against the original paper. The current model tracks per-partition latency samples and sheds requests above a configured threshold; this mechanism check does not establish fidelity to the original algorithm.

However, several production components from the original paper are deliberately omitted or simplified:
1. **Kernel-level eBPF Acceleration:** Omitted in favor of user-space gateway-level queue dispatch.
2. **Distributed Partition Consensus:** Omitted under Assumption A6 (single gateway authority for queue scheduling).
3. **Cross-Rack Tenant Work Stealing:** Omitted under Assumption A3 (strict FIFO queue discipline).
4. **Adaptive Sliding Thresholds:** Not implemented; the threshold is static.

The previous hard-coded threshold-sensitivity outputs were removed because they were not experiment results. The repaired registered simulation did not support its joint claim. No performance superiority claim against DRACO or a DRACO-inspired policy is established.
