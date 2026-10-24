# RESCUE project context

This page records the model assumptions and repository scope. It is not a
phase ledger. The authoritative workflow, status, validation results, and
remaining-work order are in [`phase.md`](phase.md).

## Project objective

RESCUE is a research prototype for completion-backed admission control:
capacity remains charged until the resource owner confirms release; work whose
release cannot be verified remains quarantined. The research also models
admission policies, leases, fencing, and stale-write detection.

The repository contains two separate evidence tracks:

- A Python research harness for simulations, model checking, and statistical
  analysis.
- A Go in-memory gateway prototype with a local demo, timer-workload
  benchmark, and package tests.

Neither track is a production emergency-service system. The Go prototype has
no durable state or real operating-system/database/provider integration.
Simulation and generated-stream mock results cannot establish live-system
behavior.

## Model assumptions

The scheduling claims are conditional on the following assumptions. They are
not guarantees outside the modeled scope.

1. **Work-conserving dispatch:** eligible queued work is not left waiting
   while an executor is idle.
2. **Identical executors:** executors within a stage have equal nominal
   processing capacity.
3. **FIFO order:** work is dispatched in first-in, first-out order within the
   modeled queue.
4. **Stable database primary:** the model assumes one PostgreSQL primary;
   failover is outside the current scope.
5. **Non-Byzantine workers:** workers may fail, pause, or become unresponsive,
   but do not forge audit records or fence versions.
6. **Single-gateway bounds:** deterministic scheduling bounds apply to one
   gateway's lease ledger, not a distributed multi-gateway scheduler.
7. **Bounded cancellation model:** cancellation failures are represented by a
   configured probability. The existing calibrator samples that probability
   synthetically; it does not measure real cancellation attempts.
8. **Bounded dispatch overhead:** the mathematical model includes a declared
   dispatch-overhead bound.
9. **Empirical service floors:** useful-completion claims depend on measured
   service-rate floors; the current simulation does not establish deployed
   service rates.

## Configuration and evidence

The stage/class parameters and run-manifest schema are in
[`../config/rescue_config.v1.0.json`](../config/rescue_config.v1.0.json) and
[`../schemas/run_manifest.schema.json`](../schemas/run_manifest.schema.json).
These are research-model inputs, not measured production limits.

Read [`phase.md`](phase.md) for the current status of each phase and the
interpretation of these artifacts:

- Repaired simulation preregistration and result:
  [`../config/preregistration_repaired.json`](../config/preregistration_repaired.json),
  [`../config/confirmatory_results_repaired_confirmatory.json`](../config/confirmatory_results_repaired_confirmatory.json).
- Historical fault-calibration output: [`../config/calibration_report.json`](../config/calibration_report.json).
  It is generated from simulated trials and is not field calibration.
- Mock-only database reports:
  [`../config/database_testbed_report_mock.json`](../config/database_testbed_report_mock.json),
  [`../config/database_testbed_report.json`](../config/database_testbed_report.json).
- Superseded positive report manifest:
  [`../config/technical_report_manifest.json`](../config/technical_report_manifest.json).

Historical artifacts are kept for provenance. A file's presence or a prior
“PASSED” label is not current validation evidence; check the environment and
status stated in [`phase.md`](phase.md).

## BR-F01–BR-F16 compliance matrix

This is a requirement-level assessment against Section 7 acceptance evidence
in [`new_br.md`](new_br.md), not an estimate of overall project completion.
“Implementation” means relevant code exists; “test coverage” means a relevant
test is present; neither means the acceptance criterion passed.

### Status definitions and assessment method

- **Verified:** the stated BR acceptance criterion is fully exercised by an
  automated test and that test was run successfully.
- **Partial:** some relevant implementation or tests exist, but at least one
  part of the BR requirement or its acceptance evidence is missing,
  incomplete, or unverified.
- **Missing:** no implementation of the required capability is present.

The current local validation ran the Go package suite, the Go race detector,
and `go vet`; all passed. The Python unit suite and the documented checker,
scheduling, and baseline-mechanism checks also passed. These results validate
the tested in-memory and simulated paths only. No requirement is marked
**Verified** unless its complete BR acceptance evidence was run.

| Requirement and required acceptance evidence | Implementation in repository | Automated test/evidence present | Execution result and assessment | Remaining work |
| --- | --- | --- | --- | --- |
| **BR-F01 — Hierarchical quotas.** Cover source-group, ASN/region, and global caps before execution admission. | Partial quota accounting in [`source.go`](../gateway/pkg/source/source.go) (`CheckAdmission`, `Reserve`); intake/controller call into the registry. | [`source_test.go`](../gateway/pkg/source/source_test.go): shared group, ASN/region, contention, and overflow tests. | **Partial — package tests and race run passed.** The complete report-to-execution path and all hierarchy levels together are not covered by one acceptance test. | Add an end-to-end quota matrix covering all configured scopes, rejection before execution, releases, and aggregate contention. |
| **BR-F02 — Probationary sources.** New/unproven source starts in probation, is placed only in sacrificial pool, and has bounded starter capacity. | [`source.go`](../gateway/pkg/source/source.go) registers new sources as probationary; [`controller.go`](../gateway/pkg/controller/controller.go) routes them to sacrificial capacity and reserves quarantine capacity before admission. | [`intake_test.go`](../gateway/pkg/intake/intake_test.go); [`controller_test.go`](../gateway/pkg/controller/controller_test.go): probation routing, quarantine reservation, cancellation release, and full-quarantine tests. | **Partial — package tests and race run passed.** The starter-share policy is still based on quarantine reservation, not an independently configured/measured starter share. | Define and test the BR starter-share bound across multiple source identities and verify none reach normal/emergency capacity before promotion. |
| **BR-F03 — Per-resource accounting.** Ledger charges for allocated cells, database connections, and tracked downstream budgets match observed allocation. | [`controller.go`](../gateway/pkg/controller/controller.go), [`lease.go`](../gateway/pkg/lease/lease.go), and [`task.go`](../gateway/pkg/task/task.go) track worker and caller-declared handles; controller dispatch now reserves/releases the shared pool budget. Actual DB/downstream resources are not created or measured by the gateway. | [`controller_test.go`](../gateway/pkg/controller/controller_test.go): handle lifecycle and aggregate emergency-floor dispatch; [`lease_test.go`](../gateway/pkg/lease/lease_test.go): manifest membership and detached lease. | **Partial — package tests and race run passed.** Tests cover in-memory handles and configured pool accounting, not matching real allocations across required bottlenecks. | Integrate resource acquisition/release owners; compare ledger charges against real worker, DB, and downstream observations. |
| **BR-F04 — Timeout safety.** A cancellation-ignoring handler retains its charge until verified release. | [`worker/pool.go`](../gateway/pkg/worker/pool.go) keeps a slot charged until completion acknowledgement; controller reconciles completion before releasing task accounting and records worker errors distinctly. | [`worker/pool_test.go`](../gateway/pkg/worker/pool_test.go): cancellation and unacknowledged completion tests; [`controller_test.go`](../gateway/pkg/controller/controller_test.go): resistant timeout and normal-completion cancellation classification. | **Partial — package tests and race run passed.** These exercise in-process accounting, not every resource handle or a process surviving controller restart. | Extend acceptance tests to external handles and restart/reconciliation boundaries. |
| **BR-F05 — Independently verified release.** Each handle is independently confirmed; one unconfirmed handle keeps work quarantined and charged. | [`quarantine.go`](../gateway/pkg/quarantine/quarantine.go), [`admission.go`](../gateway/pkg/admission/admission.go), and [`controller.go`](../gateway/pkg/controller/controller.go) fail closed without a verifier and gate release on the manifest. | [`quarantine_test.go`](../gateway/pkg/quarantine/quarantine_test.go): verifier and reservation tests; [`controller_test.go`](../gateway/pkg/controller/controller_test.go): verified handle lifecycle; [`admission_test.go`](../gateway/pkg/admission/admission_test.go): nil-verifier rejection and concurrent confirmations. | **Partial — package tests and race run passed.** The verifier is a test double; no authoritative cgroup/DB/provider release verifier is implemented. | Implement trusted verifiers for each production resource type and test mixed handles against those owners. |
| **BR-F06 — Reverse audit.** Enumerate runtime/database resources and flag a deliberately planted backend/cgroup with no ledger owner. | [`audit.go`](../gateway/pkg/audit/audit.go) compares caller-provided resources with an in-memory owner map; it does not enumerate OS or database resources. | [`audit_test.go`](../gateway/pkg/audit/audit_test.go): blank, unknown, and mismatched owner tests. | **Partial — package tests and race run passed.** The tests verify comparison logic only; they do not plant or discover a real process, cgroup, or database backend. | Add scoped Linux cgroup and PostgreSQL enumerators, then integration-test planted resources and owner mismatches. |
| **BR-F07 — Source-specific penalties.** One attacking device must not automatically downgrade unrelated devices in its ASN. | [`source.go`](../gateway/pkg/source/source.go) stores tier by source ID and separately aggregates quota by ASN/region. No attacker penalty/downgrade policy is implemented. | No test was found that promotes/penalizes one device and asserts another device in the same ASN is unaffected. | **Partial — no acceptance test.** Per-source tier storage exists, but the punitive policy behavior is absent/unverified. | Define penalty and congestion rules; test two devices sharing an ASN under attack and assert only the targeted device's tier changes unless an aggregate cap triggers. |
| **BR-F08 — Attested/unattested elastic lanes.** Test idle-lane borrowing and both-lanes-overloaded behavior without exceeding ASN aggregate cap. | [`pool.go`](../gateway/pkg/pool/pool.go) provides named pool policy and an aggregate cap; source tiers route to normal/emergency/sacrificial pools. No attestation lanes, borrowing policy, or ASN-to-lane coupling is implemented. | [`pool_test.go`](../gateway/pkg/pool/pool_test.go) tests emergency floor selection and aggregate capacity overflow only. | **Missing for the BR-defined lanes.** Generic pool tests do not satisfy attested/unattested borrowing acceptance evidence. | Implement attestation inputs, lane minimums, borrowing rules, and ASN cap enforcement; test idle borrowing and simultaneous overload. |
| **BR-F09 — Emergency-floor isolation.** Quarantine across attacker groups must not consume the emergency floor. | [`quarantine.go`](../gateway/pkg/quarantine/quarantine.go) reserves capacity atomically and distinguishes entries from task-scoped held reservations; controller admission now reserves probation capacity before enqueue. [`pool.go`](../gateway/pkg/pool/pool.go) enforces the execution-lane floor at dispatch. | [`quarantine_test.go`](../gateway/pkg/quarantine/quarantine_test.go): concurrent reservation, transfer, and emergency reserve tests; [`controller_test.go`](../gateway/pkg/controller/controller_test.go): end-to-end execution floor isolation. | **Partial — package tests and race run passed.** The BR criterion’s across-attacker-group live quarantine scenario is not represented by real resources. | Add integration tests over real resource owners and multiple attacker groups while preserving aggregate `qmax`. |
| **BR-F10 — Durable receipts.** Restart/power-loss test proves each receipt follows a durable fsynced record and fsync failure stops receipts. | [`intake.go`](../gateway/pkg/intake/intake.go) stores receipts in maps; `fsyncOK` is a constructor boolean, not a disk write or fsync operation. | No restart/power-loss or persistence test found. | **Missing.** The in-memory flag cannot establish receipt durability. | Implement persistent append/transaction semantics with real fsync and recovery; test crash points and verify every issued receipt survives restart. |
| **BR-F11 — Idempotency and key hijack.** Same key/hash returns stored receipt; conflicting hash is rejected and logged; test replay and observed-key hijack. | [`intake.go`](../gateway/pkg/intake/intake.go) uses unambiguous source/nonce scopes, validates payload hashes and report IDs, and returns consistent receipts; state remains in-memory and conflicts are not logged. | [`intake_test.go`](../gateway/pkg/intake/intake_test.go): blank-nonce normalization, delimiter collision, payload hash, and report-ID tests; [`controller_test.go`](../gateway/pkg/controller/controller_test.go): empty/whitespace and concurrent replay without quota leakage. | **Partial — package tests and race run passed.** Key-hijack visibility, durable replay, and conflict logging are absent. | Add key-hijack and conflict-audit tests; persist idempotency state and prove replay behavior after restart. |
| **BR-F12 — Fence correctness.** Randomized interleavings and crash points must validate shared/exclusive lock protocol for claims and bumps. | [`lease.go`](../gateway/pkg/lease/lease.go) and [`effect.go`](../gateway/pkg/effect/effect.go) maintain in-memory versions/claims. The outbox enforces state transitions and claimant/token/fence checks; no database locking protocol is implemented. Python [`transaction_checker.py`](../harness/transaction_checker.py) models validation. | [`effect_test.go`](../gateway/pkg/effect/effect_test.go): sent/failed replay, duplicate IDs, expired claim-token, and fence bump tests; Python [`checker_differential_test.py`](../harness/checker_differential_test.py): 100,000 histories. | **Partial — Go suite, race suite, vet, and Python model suite passed.** No DB lock interleavings, durable sender crash points, or live stale-sender test. | Implement transactional PostgreSQL fence/claim locking and test randomized concurrent claims, bump races, and crashes before/after commit/send. |
| **BR-F13 — Effect contracts.** Each effect declares provider-keyed, naturally idempotent, or non-idempotent semantics; sender tests measure duplicate window. | [`effect.go`](../gateway/pkg/effect/effect.go) now enforces local claim/fence/state transitions but has no contract type, provider sender, or reconciliation integration. | [`effect_test.go`](../gateway/pkg/effect/effect_test.go) tests effect lifecycle and stale claim rejection. | **Partial — package tests and race run passed.** Contract declaration and provider acceptance tests remain absent. | Define typed effect contracts and provider interfaces; implement sender/reconciliation behavior and test retries, crashes, idempotency retention, and duplicate windows. |
| **BR-F14 — Equal-budget evaluation.** Run B1, B2, and ZAC-fixed with per-resource budget differences within the preregistered tolerance. | Python harness compares A′/D-inspired/B admission and NS/TR/OC factors; this is not B1/B2/ZAC-fixed. No per-resource physical-budget checker is implemented. | No CI budget-tolerance check or equal-budget acceptance test found. | **Missing.** Existing arm-manifest checks do not measure or enforce equal physical budgets. | Implement the required baselines, resource budget manifests/checks, and a CI gate using a justified, frozen tolerance. |
| **BR-F15 — Reproducible attacker search.** Preserve separate pilot/confirmatory seeds, search logs, and a planted-attacker recovery report. | [`preregistration_engine.py`](../harness/preregistration_engine.py) and result artifacts preserve simulation seed blocks; no bounded attacker-search implementation or planted-attacker validation was found. | The repaired simulation artifacts distinguish a simulation registration/result, but no attacker search log or planted-attacker test/report exists. | **Partial.** Seeded simulation exists; the BR-defined search and validation workflow is absent. | Implement bounded search with deterministic seeds, complete logs, repeated starts, held-out evaluation, and planted-attacker recovery criteria. |
| **BR-F16 — ZAC-adaptive remains exploratory.** Reports distinguish exploratory from confirmatory until the estimator and comparison protocol are validated. | The simulation/report generator labels exploratory outputs; [`phase.md`](phase.md) records the repaired joint claim as unsupported. No validated ZAC-adaptive estimator/comparison is present. | [`technical_report_generator.py`](../harness/technical_report_generator.py) refuses non-exploratory input; its tests cover exploratory status and refusal of registered input. The artifacts in `config/` preserve the simulation result. | **Partial — reporting guard tested; no adaptive estimator validation.** The Python unit tests passed in the preceding workflow update; this does not validate an estimator. | Keep adaptive results explicitly exploratory; implement and independently validate the estimator and comparison protocol before creating confirmatory claims. |

### Assessment summary

- **Verified:** 0 of 16 requirements against their complete Section 7
  acceptance evidence in this environment.
- **Partial:** 13 requirements have some related implementation or evidence,
  but their acceptance criteria are incomplete or unverified.
- **Missing:** BR-F08's required attested/unattested lanes, BR-F10 durable
  receipts, and BR-F14 equal-budget baseline evaluation are not implemented
  to the stated requirement.

These counts are a classification of this matrix, not a completion percentage.
They must be revisited as acceptance evidence and missing integrations are
implemented. See [`phase.md`](phase.md) for current validation and the
project-level sequence and live-testbed blockers.

## Correctness review of implemented paths

The following defects were fixed and verified with regression tests in the
current worktree. Full Go unit, race-detector, and vet runs passed; detailed
commands and Python validation results are in [`phase.md`](phase.md).

| Defect fixed | Change and regression evidence |
| --- | --- |
| Outbox replay, ID replacement, and stale claim completion (BR-F12/F13) | [`effect.go`](../gateway/pkg/effect/effect.go) now rejects conflicting duplicate IDs, returns detached effects, enforces valid claim states, binds completion to claimant/fence/monotonic claim token, invalidates pending work on fence bump, and requires explicit retry for failed effects. [`effect_test.go`](../gateway/pkg/effect/effect_test.go) covers replay, expiration, stale tokens, and fences. |
| Reverse-audit false negatives and concurrent map access (BR-F06) | [`audit.go`](../gateway/pkg/audit/audit.go) now flags unknown resource IDs, blank owners, and owner mismatches, and protects its owner map with a read/write lock. Tests cover unknown/mismatched owners. |
| Ineffective emergency floor and unconnected pool accounting (BR-F03/F08/F09) | [`pool.go`](../gateway/pkg/pool/pool.go) enforces per-lane and aggregate caps while reserving the emergency floor; [`controller.go`](../gateway/pkg/controller/controller.go) reserves lane capacity on dispatch and releases it only after handle-backed task release. Unit and controller tests prove non-emergency work cannot consume protected capacity and emergency work can use it. |
| Probationary admission overbooking quarantine capacity (BR-F02/F09) | [`quarantine.go`](../gateway/pkg/quarantine/quarantine.go) supports atomic task-scoped capacity reservations. The controller reserves before enqueuing probationary work and releases or transfers the reservation on cancellation, expiry, completion, or quarantine. Concurrent, transfer, and controller lifecycle tests cover the behavior. |
| Empty-nonce quota leak and ambiguous idempotency scopes (BR-F01/F11) | [`intake.go`](../gateway/pkg/intake/intake.go) normalizes blank source/nonce values consistently, hashes length-prefixed fields, verifies payload hashes, and rejects report-ID reuse. [`AcceptReport`](../gateway/pkg/controller/controller.go) serializes receipt admission and releases its transient quota charge. Tests cover blank/whitespace replay, delimiter collisions, concurrent duplicate reports, and zero remaining usage. |
| Admission verifier fallback and mutable result exposure (BR-F05) | [`admission.go`](../gateway/pkg/admission/admission.go) now fails closed when no trusted verifier is configured, rejects empty/duplicate manifests, serializes state changes, returns detached records, and safely resumes settlement after concurrent confirmations. Tests cover nil verifier, caller mutation, and concurrent confirmations. |
| Source selection data race (BR-F02/F07/F08) | [`source.go`](../gateway/pkg/source/source.go) copies tier/pool fields and the policy pointer while holding the registry lock. Controller routing now uses the detached source snapshot and its shared policy. Concurrent promotion/selection/policy replacement is exercised by a race test. |
| Duplicate queue IDs and worker lifecycle misreporting | [`queue.go`](../gateway/pkg/queue/queue.go) rejects duplicate/blank IDs and maintains the active-ID index through dequeue/removal/expiry. Controller cleanup no longer marks normal completion as cancellation, and worker errors enter an explicit `FAILED` transition before release. Regression tests cover duplicate IDs, completion flags, and failed work. |
| Unsafe legacy helper behavior | The incomplete queue-only [`EnqueueTask`](../gateway/pkg/controller/controller.go) now returns an explicit migration error instead of success-shaped incomplete state. Standalone [`LeaseTask`](../gateway/pkg/controller/controller.go) reports reservation/lease failures, returns detached leases, and pairs with `ReleaseLease` to release the tracked source charge. |

These fixes make the covered in-memory paths correct under the tests; they do
not complete all BR requirements. Durable receipts, real process/database
enumeration, authoritative resource verifiers, PostgreSQL fencing, provider
effect contracts, and equal-budget evaluation remain unimplemented. “Partial”
in the matrix means implementation and test coverage exist but the complete
acceptance evidence is still outstanding.
