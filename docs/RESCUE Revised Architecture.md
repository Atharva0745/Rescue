# RESCUE: Revised Architecture, Rebuild and Evaluation Plan

Prepared for Vishal Acharya · 26 October 2026 · Status: design proposal, not validated

## 1. Purpose and claim

This plan does two things: it defines the rebuilt RESCUE codebase (eight components only), and it defines how that codebase is evaluated against Rajomon and Atropos.

**Research question.** Under overload, especially when some tasks ignore cancellation or outlive their deadlines, does RESCUE keep resource accounting safe while preserving legitimate deadline success, compared with Rajomon's rate limiting and load shedding and Atropos's targeted task cancellation?

**Primary claim (kept narrow).** RESCUE enforces no-premature-reuse under the tested resource types, and the paper quantifies the resulting trade-off in legitimate deadline success and capacity utilization during cancellation-resistant overload. Do not claim universal verified release until every handle type in scope has an implementable, testable confirmation rule. "Maximum availability" is an objective to measure, not a guarantee.

## 2. Scope of the rebuild

**In: the eight components**

1. Intake and source classification
2. Admission control and quotas
3. Execution pools (sacrificial, normal, emergency floor)
4. Reservations, leases and bounded dispatch
5. Completion and resource-release confirmation
6. Quarantine
7. Fencing and effect protection
8. Reverse audit and measurement

**Out of the gateway code**

- The A′ and D admission policies, and the B1/B2/ZAC baselines. Only RESCUE's reservation policy (B) is rebuilt. The simulator keeps the others for history. (Confirm this; see section 9.)
- Lock handles and downstream-RPC handles, until reliable confirmation semantics exist.
- Shared pooled database connections.
- Dedicated PostgreSQL backends: stage-2 option only, if scope is extended.

**Design corrections made while folding the plan into eight components**

- The execution backend and the termination ladder are part of component 5, not separate components.
- The independent resource enumerator lives in the test harness, not in component 8. An auditor the controller can influence is not independent. Component 8 keeps the in-gateway ledger reconciler and event emission.

## 3. Target architecture

Three planes:

- **Harness plane (outside the system under test):** open-loop load generator with trace replay, ground-truth legitimate/attacker labels, fault injector (zombie fraction, hang duration), independent auditor/enumerator, event log, analyzer.
- **Gateway shell (identical for every arm):** gRPC ingress, request classes, handlers, per-task execution cells, and an `OverloadController` interface.
- **Arm implementations behind the interface:** `rajomon`, `atropos`, `rescue`, and `none` (calibration only).

End-to-end RESCUE flow: request arrives, classify and apply limits, choose pool, reserve capacity and issue a lease, run within bounded resources, on timeout request stop, confirm completion and release, release the charge or quarantine it, fence stale work, audit.

## 4. Rebuild plan by component

| # | Component | Existing Go packages | Required change |
| --- | --- | --- | --- |
| 1 | Intake and source classification | `intake`, `source` | A source remains probationary until an explicit trusted harness/operator promotion; there is no automatic trust promotion. Freeze the existing group/ASN/region/global caps (8/16/32/64) as prototype defaults, then calibrate before confirmatory trials. The gateway must never see the harness's legitimate/attacker label. An intake receipt means accepted into the intake log, not task completion; the current in-memory log is not durable. |
| 2 | Admission and quotas | `admission` | Single policy (B). Make it a deterministic function of ledger state. Preserve the bounded FIFO queue and per-source limit; queue-full or quota rejection is immediate and does not mean work was admitted. A successful enqueue acknowledges admission/queueing, never task completion. Map overload rejection to `RESOURCE_EXHAUSTED` when the gRPC shell exists; internal APIs currently return errors. Enumerate outcome classes. |
| 3 | Execution pools | `pool` | Sacrificial, normal and emergency floor. Sizes come from calibration, not hypothetical allocations. Invariant: total charges never exceed the budget. |
| 4 | Reservations, leases, bounded dispatch | `lease`, `queue`, `task` | Reserve before start. Keep the bounded queue and blocked-front statistics. Document the FIFO, work-conserving and comparable-worker assumptions behind any scheduling bound. |
| 5 | Release confirmation | `worker`, `controller` | **Largest change.** Run command-backed tasks in per-task cgroup-v2 process cells; arbitrary Go closures cannot be moved into a cgroup and remain only for tests/local benchmarks. Cancellation becomes a ladder: cooperative cancel, grace T1, SIGTERM, grace T2, cgroup kill, verify. A timeout or cancel request never releases a charge. Release happens only when every manifest handle is confirmed. |
| 6 | Quarantine | `quarantine` | Keep it bounded, charged to the origin pool, with the emergency reserve. The process-cell verifier requires `cgroup.events` populated=0 and empty `cgroup.procs`. A handle that is never confirmed remains charged in quarantine for explicit operator resolution; it is never automatically reused. |
| 7 | Fencing and effect protection | `lease` fence versions, `effect` | The test effect sink rejects writes carrying a stale fence version. Count stale writes. |
| 8 | Audit and measurement | `audit`, `metrics` | Add the ledger-versus-observed reconciler and the shared event schema. The harness owns the independent enumerator. |

**Invariants to write down and test before refactoring**

- I1: No slot or budget is allocated while a prior holder's required handle is unconfirmed.
- I2: The ledger never records a release that the independent auditor contradicts.
- I3: Quarantined capacity remains charged to its pool.
- I4: Queues and pools never exceed their registered bounds.
- I5: A stale-fence write is never accepted by the effect sink.

Test with randomized interleavings and fault injection, not only example-based unit tests.

**Current prototype status (from the uploaded code)**

- The Go gateway is in-memory, about 7,000 lines across 13 packages.
- The worker pool runs goroutines with context cancellation only.
- The reverse auditor compares supplied resources against registered owners and does not enumerate anything.
- `main.go` is a single-process Poisson benchmark with RNG-assigned legitimacy. Relabel it a controller microbenchmark: useful for regression, not evidence for the three-way comparison.

## 5. Comparison arms and baselines

Run three arms one at a time on the same isolated testbed, plus a no-control calibration arm used only to measure sustainable capacity and overload severity.

### 5.1 Rajomon

- Go gRPC interceptor library (pennsail/rajomon). Server-side load shedding and client-side rate limiting are separate interceptors, and admission follows a token-versus-price rule. Rejected requests return a resource-exhausted gRPC status.
- Use the real code unchanged for the stock arm. Record the exact source revision.
- Pricing and rate-limit settings are tuned on pilot seeds only, then frozen.
- Consequence: the gateway and harness speak gRPC for all arms so the wire protocol is identical, and every arm maps rejections to the same outcome class.
- Rajomon is a congestion-control baseline. Do not claim it performs per-handle reclamation unless a modification is added and separately labeled.

### 5.2 Atropos

- Atropos monitors per-task, application-defined resource use and cancels the request whose cancellation would release the most load on the contended resource. It relies on the application's own cancellation functions and only cancels tasks marked safe to cancel.
- The authors' slides link a public repository (github.com/OrderLab/Atropos). **Not yet inspected.** Before designing the adapter, check: whether it ships a reusable library or only per-application patches, its implementation language, and its license.
- Stock arm: instrument the gateway's resource usage through its APIs and provide a cancel initiator. That initiator is the same cooperative cancel that zombie tasks ignore. This is the behavior under test, not a defect to patch.
- If the repo is not usable from a Go gateway, reimplement the policy from the paper and label the arm "Atropos-style reimplementation", with a written list of what was and was not reproduced.
- An "Atropos + verification" arm (adding RESCUE-style release verification) is an optional, separately labeled ablation. It is never called stock Atropos.

### 5.3 Reading the arms fairly

|  | Rajomon | Atropos | RESCUE |
| --- | --- | --- | --- |
| Overload response | Price-based shedding and rate limiting | Cancel the culprit task | Reserve, contain, quarantine |
| If cancellation is ignored | Not addressed by design | Task may keep consuming | Charge held, ladder, quarantine |
| When capacity is reused | Policy-driven | After cancel is requested | Only after verified release |

Do not claim RESCUE wins because it runs fewer tasks or rejects more. A credible result shows both that accounting and release are correct under planted cancellation-resistant cases, and whether legitimate requests still meet deadlines under the same offered load and physical budget.

## 6. Testbed and fairness controls

- Two isolated, identical servers, the same gateway and workload binary for all arms.
- Equal enforced budgets per arm: CPU cores, memory, worker slots, database connections, downstream-call budget. Verify the limits rather than trusting nominal specs.
- Measure sustainable capacity for the exact request mix and deadline first. The 5,000 RPS per server figure is only an example until calibration confirms it; if it does, 7,000 RPS per server is 1.4 times capacity.
- Send traffic directly to each server, or prove the load balancer holds the intended split. Also run a skewed split.
- Run arms sequentially in randomized order, reset state between runs, same machine placement, trace, arrival process and duration. Never run systems concurrently.
- Record CPU throttling, memory pressure, network, database load and background activity.
- Resource types in scope: execution cell/process group (stage 1); dedicated PostgreSQL backend (optional stage 2). The reverse auditor is confined to the testbed root and never scans or terminates unrelated host work.

## 7. Scenarios

Require at least two request classes (short and long/resource-heavy), each with a fixed deadline and resource profile. Legitimate or attacker status is assigned by the harness, not by an application-supplied field.

- **A. Capacity curve:** 0.5, 0.8, 1.0, 1.2, 1.4 times measured capacity, balanced and skewed.
- **B. Sustained overload:** warm up, ramp, sustain for a preregistered interval (for example 60 seconds), return to sub-capacity. Open-loop Poisson or the same recorded trace for every arm, plus a separate bursty trace. Avoid closed-loop clients that hide overload.
- **C. Cancellation-resistant work:** fraction of zombie tasks (candidate levels 0, 5, 20 percent) and hang duration (1 and 5 times the deadline); modes: prompt cooperative, delayed, ignored until external termination, and an isolated case where termination or verification fails so RESCUE must keep the charge and quarantine. Identical resistance in all arms. Verify that a task actually keeps consuming a measured resource after cancellation before calling it a zombie.
- **D. Pool and queue saturation:** fill the sacrificial pool to its registered `qmax`, test arrivals after it is full, and measure emergency and normal capacity separately. Queues are always bounded.
- **E. Recovery:** time to return to a registered goodput and latency band after the attacker stops, with zombies that eventually stop and with handles that stay unconfirmed longer. Verify no reuse before independent confirmation.
- **F. Stale write (added for component 7):** a paused worker resumes after its lease was replaced. Report as a mechanism-specific result, not a head-to-head win, because the baselines have no fencing.

Final levels, durations and parameters are chosen on pilot runs and registered before confirmatory runs.

## 8. Metrics, gates and hypotheses

**Primary service metric.** Legitimate deadline success: `S_L` = legitimate requests completed successfully by their deadline divided by eligible legitimate requests offered. Accepted-but-unfinished and rejected legitimate requests count in the denominator. Publish exclusions in advance. Report overall and by request class, source tier and load condition.

**Safety gates (fail the run, regardless of `S_L`)**

| Gate | Computed from | Target |
| --- | --- | --- |
| Premature reuse | Allocation event while the prior handle is unconfirmed or still observed live | 0 |
| False release | Ledger release while the auditor still sees the handle | 0 |
| Orphan handles | Observed resources with no valid ledger owner | 0, and the planted orphan must be detected |
| Collateral kills | Unrelated or legitimate work terminated by cleanup | 0 for unrelated host work; workload kills reported separately |
| Stale writes accepted (scenario F) | Effect-sink log | 0 for RESCUE |

**Performance and cost metrics**

- Offered, admitted, rejected, shed, started, completed and deadline-missed requests per second.
- Queue depth and queue wait (enqueue to actual start), p50/p95/p99, separate from end-to-end latency.
- CPU, memory, worker-slot and database occupancy, including consumption after cancel or deadline.
- Quarantine occupancy and time to confirmed release (from termination request to independent confirmation).
- Time a pool stays full and requests blocked meanwhile.
- Capacity-seconds per legitimate success.
- Recovery time after load falls.
- Controller overhead (CPU, memory, added latency).

Show time series of offered load, queue depth and wait, completions, live resources, charges and quarantine. Averages alone can hide transient collapse and brief safety violations.

**Event schema (contract between harness and arms).** Each arm emits `arrive, admit/reject(reason), enqueue, start, deadline, cancel_req, term, kill, handle_confirmed, ledger_release, quarantine_enter/exit, alloc(slot, prior_handle_state)`. The harness auditor writes its own independent `observed(resource, owner)` stream. Gates are computed offline by joining the two streams, so no arm grades itself.

**Hypotheses to preregister**

- H1, safety: within the tested resource types, RESCUE has zero premature reuse and zero false release, including with delayed, ignored or unconfirmed cancellation. This is a correctness claim and is not inferred from averages.
- H2, zombie containment: under cancellation-resistant overload, RESCUE's accounted capacity tracks independently verified live resources (no phantom free capacity), compared with stock Rajomon and stock Atropos.
- H3, service trade-off: RESCUE may reject or delay more work in the affected pool while resources are quarantined. Test whether emergency and normal traffic keeps higher `S_L` than a design that treats cancellation as release. Do not assume it beats the baselines on every throughput metric.
- H4, recovery: once handles are confirmed, RESCUE restores admission without exceeding the budget. Measure the capacity cost of conservative accounting.

Predefine practical effect thresholds for `S_L` and overhead from pilot variability. Report win/tie/loss with paired confidence intervals.

## 9. Build sequence and exit gates

| Phase | Work | Exit gate |
| --- | --- | --- |
| P0 | Freeze the spec: invariants I1 to I5, outcome classes, full-pool semantics, ladder timers, class deadlines, supported handle types, event schema | Written and reviewed |
| P1 | Refactor to the eight components on a tagged branch; remove everything else | Unit tests plus randomized-interleaving tests pass |
| P2 | Cell backend, cgroup verifier, fault injection (delayed, ignored, kill-fails) | Each case behaves as specified |
| P3 | Harness: open-loop generator, trace replay, independent auditor | Planted orphan detected before any performance run |
| P4 | Rajomon adapter, then Atropos adapter (or reimplementation); validate each arm alone | Rajomon price and shedding behavior and Atropos cancel callbacks verified; RESCUE lease, ledger and release transitions verified |
| P5 | Calibration, pilot tuning on pilot seeds, freeze code, configs, seeds and baseline revisions | Capacity and budgets recorded |
| P6 | Confirmatory matrix: randomized paired runs, 10 to 20 independent seeds per condition (more if pilot variance is high) | Raw per-request and per-resource data saved |
| P7 | Analysis: safety gates first, then performance with paired confidence intervals; publish artifacts | Reproducibility package released |

A safety assertion fails the run if any unconfirmed handle is reused, even when `S_L` is high. Tune only on pilot seeds; per-system tuning rules are public. Label simulated and real-testbed results clearly, and list unsupported handle types.

## 10. Frozen decisions and remaining decisions

The following decisions are frozen for this implementation:

1. The gateway rebuild implements RESCUE policy B only. A′, D, B1/B2 and ZAC remain in the simulator/history and are not gateway arms.
2. PostgreSQL handles are out of scope for stage 1; stage 2 may add dedicated PostgreSQL backends after the process-cell study.
3. Scenario F (stale write) is included as a mechanism-specific fencing test, not a head-to-head baseline win.
4. The controller keeps its bounded FIFO queue and configured per-source limit. Busy workers may cause bounded queueing; a full queue rejects immediately. Successful queue admission is not a completion receipt. The future gRPC mapping is `RESOURCE_EXHAUSTED`; current internal methods return errors.
5. New sources remain probationary until explicit trusted harness/operator promotion. The prototype cap defaults are group=8, ASN=16, region=32 and global=64; final experiment limits must be calibrated and frozen before confirmatory runs.
6. Never-confirmed handles remain charged in quarantine until an explicit operator action. No timeout or automatic cleanup may make them reusable.
7. WSL2 cgroup v2 is the development/integration environment. Confirmatory experiments require a controlled Linux cgroup-v2 testbed.
8. The controller provides a command-backed process-cell path; closure-backed execution is retained only for local tests and benchmarks. An application must package process-isolatable work as an executable command.

Still open: inspect Atropos for reuse, language and license; decide whether it can be a stock arm or must be labeled an Atropos-style reimplementation. Calibrate numeric resource limits, queue sizes, and deadlines from the target testbed before confirmatory runs.

## 11. Caveats

- The Go gateway is an in-memory prototype. The live PostgreSQL and resource-owner testbed is not complete, and the repaired simulation did not support the registered joint claim.
- All hypotheses are evaluation targets, not established outcomes.
- Rajomon details here come from its public documentation and Atropos details from its paper and slides; neither source tree has been built or run as part of this plan.

### References

- Rajomon: NSDI 2025 paper and github.com/pennsail/rajomon
- Atropos: SOSP 2025 paper (Hu et al., Mitigating Application Resource Overload with Targeted Task Cancellation) and github.com/OrderLab/Atropos
- Breakwater: OSDI 2020 (cited in the evaluation plan as related overload-control work)
