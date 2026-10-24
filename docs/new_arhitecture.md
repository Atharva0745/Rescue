# RESCUE --- System Architecture

**Version:** 1.0\
**Status:** Proposed architecture for a research prototype\
**System:** Completion-Backed Admission Control for Emergency
Communication Gateways under Adversarial Overload

> This document is a design proposal, not an implementation or validation
> report. The repaired simulation result is simulation-only and did not
> support the registered joint claim; no live database/testbed evidence is
> established. Current status and evidence boundaries are maintained in
> [`phase.md`](phase.md).

## 1. Architectural Intent

RESCUE is an application-level gateway and execution-control system. It
combines four planes:

1.  **Intake plane** --- quota checks, identity/source scoping,
    idempotency, durable receipt.
2.  **Execution plane** --- pool selection, leases, resource ledger,
    cancellation/termination, handle verification, quarantine, reverse
    audit.
3.  **Effects plane** --- fenced outbox and downstream senders with
    explicit effect contracts.
4.  **Evaluation plane** --- simulator/testbed, baselines, adversarial
    workload generation, attacker search, metrics, and reproducibility.

The primary architectural invariant is:

> A resource charge is released only after the corresponding resource
> handle is independently confirmed gone. A timeout or cancellation
> request is not proof of release.

## 2. Context and High-Level Flow

``` mermaid
flowchart LR
    Client[Reporter / Client] --> Gateway[Intake Gateway]
    Gateway --> Quota[Hierarchical Quota + Tier Check]
    Quota -->|admitted| Idem[Idempotency and Payload Hash]
    Idem --> Log[Durable Intake Log]
    Log -->|fsync succeeds| Receipt[Receipt]
    Log --> Admission[Admission and Lease Service]
    Admission --> Pools[Execution Pools]
    Pools --> Worker[Task Worker]
    Worker --> Ledger[Per-resource Ledger]
    Worker --> Outbox[Fenced Effects Outbox]
    Outbox --> Sender[Effect Sender]
    Sender --> Provider[SMS / Dispatch Provider]
    Ledger --> Release[Release Ladder]
    Release --> Audit[Reverse Auditor]
    Audit --> Ledger
    Harness[Evaluation Harness] -. workloads, faults, measurements .-> Gateway
    Harness -. baseline and resource checks .-> Pools
```

A receipt means the report is durably recorded in the intake log. It
does **not** mean downstream dispatch has completed. Provider failures
must not erase the durable intake record; effects may be escalated to a
human dispatch queue after the configured effect deadline.

## 3. Components and Responsibilities

  ----------------------------------------------------------------------------------------------
  Component         Plane             Responsibility           Failure response
  ----------------- ----------------- ------------------------ ---------------------------------
  Intake gateway    Intake            Validate request,        Retry with same key; stop
                                      compute source scope,    receipts on fsync failure.
                                      enforce quota/tier,      
                                      perform idempotency      
                                      check, issue receipt     
                                      only after durable       
                                      append.                  

  Source-tier       Intake            Track source group,      New sources enter probation; caps
  registry                            ASN-region, and global   cannot inflate with source count.
                                      tier/cap state.          

  Durable intake    Intake            Persist accepted report  Do not issue receipts after fsync
  log                                 and separate             error; provider outage must not
                                      intake/effects-pending   block intake region indefinitely.
                                      regions.                 

  Admission and     Execution         Choose pool, allocate    Refuse admission if budget cannot
  lease service                       bounded resources,       be reserved safely.
                                      create lease, advance    
                                      fence version before     
                                      replacing a lease        
                                      holder.                  

  Execution pools   Execution         Separate protected       Quarantine is charged to the pool
                                      emergency floor, normal  where work ran; no automatic node
                                      pool, and                replacement.
                                      sacrificial/probation    
                                      pool.                    

  Worker runtime    Execution         Run work under a task    Sacrificial/expensive/untrusted
                                      lease and isolated       work uses process-per-task
                                      execution cell.          isolation.

  Resource manifest Execution         Record each task's       Missing or unconfirmable handle
                                      resource handles         cannot be represented as
                                      (e.g. cgroup, PostgreSQL confirmed release.
                                      backend, downstream      
                                      call, lock).             

  Resource ledger   Execution         Track charge per handle  Keep charge when release is
                                      and bottleneck resource. unconfirmed.

  Release ladder    Execution         Cancel, terminate/kill,  Mark QUARANTINED when any
                                      verify handles           required handle remains
                                      individually, release    unconfirmed.
                                      only confirmed charges.  

  Reverse auditor   Execution         Enumerate runtime        Flag unowned resources as
                                      cgroups and PostgreSQL   orphans; fail the relevant safety
                                      activity independently   assertion.
                                      of the ledger.           

  Fence and outbox  Effects           Persist effects with     Reject stale claims; never reset
                                      per-scope fence version  a live CLAIMED row to PENDING.
                                      and safe claim protocol. 

  Effect senders    Effects           Send provider effects    Use provider idempotency where
                                      under their declared     available; measure non-idempotent
                                      effect contract.         duplicate window.

  Evaluation        Evaluation        Drive workloads,         Block confirmatory run if
  harness                             attackers, baselines,    planted-attacker or budget checks
                                      seeds, metrics, and      fail.
                                      equal-budget checks.     
  ----------------------------------------------------------------------------------------------

## 4. Intake and Source Scoping

### 4.1 Hierarchy

The proposed hierarchy is:

`source group (IPv4 /24 or IPv6 /48) → ASN + region → global`

Every level has a fixed cap. Do not divide a global budget by a live
source count that an attacker can inflate.

Trust only attributes actually verified by the gateway. A device
identifier is not trusted simply because it is present. The attested
lane may be enabled only if hardware-backed attestation is verified
server-side; otherwise treat the ID as untrusted and use the fallback
policy documented in the business requirements.

### 4.2 Source tiers and penalties

-   New sources start in probation and are confined to the sacrificial
    pool.
-   Device-level abuse affects that device's quota when attribution is
    reliable.
-   ASN/region congestion activates an aggregate cap, **not** an
    automatic punitive tier for every device in that ASN.
-   Penalties require evidence, are temporary, and use cooldown/decay
    and a registered promotion window.
-   Unattested users behind NAT receive bounded
    anonymous/session/request budgets and an anonymous admission share.
-   Emergency-class traffic receives a small protected per-group floor,
    subject to measured capacity and registered policy.

### 4.3 Elastic lanes

Within an ASN quota, attested and unattested traffic each receive a
minimum guarantee and may borrow unused capacity from a shared elastic
pool. Borrowing must never exceed the aggregate ASN cap.

Illustrative only: of 100 units, 20 may be protected for the attested
lane, 20 for the unattested lane, and 60 shared. These are not
implementation defaults. Minimums must be based on measured legitimate
traffic and resource costs, and must be enforced at the actual
bottleneck, not only at intake.

## 5. Execution Pools, Leases, and Resource Accounting

### 5.1 Pools

-   **Emergency floor:** protected capacity; must not be charged for
    other tiers' quarantine.
-   **Normal pool:** work promoted from probation after meeting
    registered criteria.
-   **Sacrificial pool:** new/probation sources and untrusted or
    expensive work; process-per-task isolation is mandatory.

Quarantined cells remain charged to the pool where they ran. At `qmax`,
probation admission freezes; the emergency and normal pools are not
automatically stopped. Operator alerting and `qmax` values are
placeholders to measure and register.

### 5.2 Lease lifecycle

``` mermaid
stateDiagram-v2
    [*] --> ACTIVE: admitted with bounded resources
    ACTIVE --> EXPIRED: lease expires / deadline policy fires
    EXPIRED --> RELEASED: all required handles confirmed gone
    EXPIRED --> QUARANTINED: one or more handles unconfirmed
    QUARANTINED --> RELEASED: later verification confirms all handles gone
    QUARANTINED --> QUARANTINED: operator action or resource still present
    RELEASED --> [*]
```

A lease expiry marks work as potentially zombie; it does not release
capacity. Each lease has an epoch/fence version and a manifest of owned
handles. New lease issuance must commit the fence bump before the new
lease is usable.

The in-memory task lifecycle also records a worker-reported error as
`FAILED`. This is an outcome state, not a lease release: the lease remains
charged until every required handle is independently confirmed, then the task
can move to `RELEASED`; otherwise it remains quarantined.

### 5.3 Resource manifest and charges

Expected handle types in the report: - `cgroup` --- task execution
cell/process group. - `pg_backend` --- dedicated PostgreSQL backend
associated with task ID and epoch. - `downstream` --- downstream call or
reserved downstream budget. - `lock` --- lock handle; confirmation
semantics remain an open item.

Charges are made per bottleneck resource, not just per task. Example:
cell slot, database connection, and downstream budget can each have
separate charges. A task becomes RELEASED only when all confirmable
handles have been verified gone and all required release conditions are
satisfied.

### 5.4 Release ladder

1.  **Cancel:** send SIGTERM to the task; use `pg_cancel_backend` only
    for dedicated connections and after matching PID, backend start
    time, and task/epoch application name.
2.  **Kill:** use `cgroup.kill`, then `pg_terminate_backend` with the
    same identity checks.
3.  **Verify:** check each handle independently.
4.  **Release:** free only the charge corresponding to the handle
    confirmed gone.
5.  **Quarantine:** if any required handle cannot be confirmed, keep its
    charge and mark the task QUARANTINED.

Shared pooled database connections can make task-to-backend attribution
unconfirmable. Such resources must remain explicitly unconfirmed/debt;
do not claim verified release. A backend that survives termination
remains charged and quarantined.

## 6. Suggested Logical Data Model

These names follow the supplied design report; exact SQL types, indexes,
migrations, and retention policy remain implementation decisions.

  -----------------------------------------------------------------------
  Record                              Purpose / key fields
  ----------------------------------- -----------------------------------
  `report`                            `report_id`, `source_key`,
                                      `idem_key`, `client_nonce`,
                                      `payload_hash`, `receipt`,
                                      `created_at`

  `lease`                             `lease_id`, `task_id`, `scope`,
                                      `epoch`, `fence_version`, `class`,
                                      `source_key`, `source_tier`,
                                      `exploration`, `state`

  `manifest`                          `lease_id`, `handle_kind`,
                                      `handle_id`, `backend_start`,
                                      `charged_resource`, `state`,
                                      `confirmed_at`

  `charge`                            `resource`, `pool`, `lease_id`,
                                      `handle_id`, `amount`,
                                      `released_at`

  `source_tier`                       `source_key`, `level`, `tier`,
                                      `parent_key`, `since`

  `quarantine_budget`                 `pool`, `source_key`, `used`, `cap`

  `fence`                             `scope`, monotonically increasing
                                      `version`

  `outbox`                            `effect_id`, `scope`,
                                      `fence_version`, `payload`,
                                      `payload_hash`, `status`,
                                      `claimed_by`, `claimed_until`
  -----------------------------------------------------------------------

Suggested lease states: `ACTIVE`, `EXPIRED`, `QUARANTINED`, `RELEASED`.
Suggested outbox statuses include `PENDING`, `CLAIMED`, and terminal
provider-specific acknowledgement/failure states. Define allowed
transitions centrally and test them.

## 7. Durable Intake, Idempotency, and Receipts

1.  Compute an idempotency scope from the trusted source key when
    available, otherwise the source-group scope, plus the client
    nonce/key as defined in the report.
2.  Store a payload hash with the key.
3.  If the key and payload hash match an existing report, return the
    stored receipt.
4.  If the key exists with a different payload hash, reject and log a
    conflict; do not rewrite the original receipt or silently report
    success.
5.  Append the report to the durable log and fsync before issuing a
    receipt.
6.  On fsync error, stop issuing receipts.
7.  Keep intake records separate from effects-pending state so
    downstream provider failure does not close the intake path.
8.  Route by key hash to a home gateway where applicable; reconcile
    cross-gateway duplicates with a unique index at drain/merge time.

Residual limitation: unattested clients sharing the same source-group
scope can collide. The conflict path must be visible and logged, not
silently treated as successful duplication.

## 8. Fenced Outbox and Side Effects

### 8.1 Fence protocol

-   Maintain a monotonically increasing fence version per scope.
-   A replacement lease commits the fence bump in its own transaction
    before issuing the new lease.
-   Claim and bump operations use the same scope-derived advisory lock:
    claim uses a shared transaction lock; bump uses an exclusive
    transaction lock.
-   A claim succeeds only if the row's fence version is current enough
    and its state is `PENDING`, or its previous claim has expired.
-   Never reset a `CLAIMED` row to `PENDING` while a sender might still
    be executing.
-   `claimed_until` must exceed measured maximum send time; this is a
    measured assumption, not a magic constant.
-   The sender rechecks the fence under the shared advisory lock
    immediately before the provider call.

There remains a residual race between the final fence check and the
external call. For non-idempotent providers, measure and report this
window rather than claiming zero duplicates.

### 8.2 Effect contracts

  -----------------------------------------------------------------------
  Contract                Handling                Guarantee / limit
  ----------------------- ----------------------- -----------------------
  Provider-keyed          Send stable `effect_id` Duplicate suppression
                          as provider idempotency depends on provider
                          key.                    semantics and
                                                  key-retention window.

  Naturally idempotent    Repeated execution has  Verify the effect is
                          no additional effect.   genuinely idempotent.

  Non-idempotent          Recheck fence           Residual duplicate
                          immediately before      window is measured, not
                          call; reconcile through claimed to be zero.
                          provider status         
                          interface if available. 
  -----------------------------------------------------------------------

After the registered `Teffect_max`, escalate pending effects to a human
dispatch queue and count each escalation. The timeout must be measured
and frozen before confirmatory evaluation.

## 9. Reverse Audit

The reverse auditor begins from the runtime/database rather than
trusting the ledger: - Enumerate PostgreSQL `pg_stat_activity` rows
relevant to the testbed. - Enumerate cgroups under the dedicated testbed
root. - Match each discovered resource to a manifest/ledger owner. -
Flag any resource with no owner as an orphan. - Record orphan count and
fail the relevant safety assertion if an orphan is present.

The simulator's audit is simulated and must be labelled as such. The
real testbed must run the actual cgroup and PostgreSQL enumeration. Keep
the auditor scoped to an isolated testbed root; do not scan or terminate
unrelated host workloads.

## 10. Evaluation Architecture

### Environments

-   **Discrete simulator:** scalable model of overload,
    cancellation-ignoring handlers, source rotation, shared-network
    traffic, and attacker behavior. All simulated resource/audit claims
    must be labelled simulated.
-   **Real testbed:** Linux cgroups for execution cells and PostgreSQL
    for database, outbox, and fence; real reverse audit over the testbed
    cgroups and `pg_stat_activity`.

### Baselines

-   **B1:** plain over-provisioning with timeouts, cancellation, fair
    queuing, and extra cells.
-   **B2:** B1 plus per-source quarantine budgets and fair queuing.
-   **ZAC-fixed:** fixed-time cancellation/release policy plus
    completion-backed handle accounting and quarantine.
-   **ZAC-adaptive:** stop-time estimator; exploratory until validated.

Every arm must have the same total physical budget on each resource. CI
must reject runs outside the registered tolerance.

### Workloads and attacker search

-   W4 is the reference workload for the confirmatory hypothesis.
-   W7 is a database-bound negative/control case where extra cells may
    add no benefit.
-   Attacker parameters include zombie rate, hang-duration distribution,
    switch time, source-group count, ASN count, controlled
    attested-device count, and request mix, with the dimensionality
    bounded as registered.
-   Search each arm's attacker within the registered evaluation budget;
    use repeated search starts.
-   Validate the search against a planted attacker and compare the
    selected attacker on held-out seeds.
-   Freeze pilot/confirmatory seed split before confirmatory runs.

### Metrics

Primary: legitimate deadline success `S_L`.

Secondary: false rejection, resource occupancy and unused capacity,
orphan count, collateral kills, capacity-seconds per legitimate success,
fence-bump latency, duplicate-send window, quarantine occupancy, and
simulator-to-testbed discrepancy.

## 11. Safety Invariants

  -----------------------------------------------------------------------------
  ID                      Invariant               Required test
  ----------------------- ----------------------- -----------------------------
  I1                      No stale fence version  Randomized interleavings on
                          can claim an effect.    per-report scopes.

  I2                      No RELEASED task has a  Randomized
                          live tracked handle.    cancellation/release tests.

  I3                      No orphan handle is     Plant a backend/cgroup that
                          left unreported on the  bypasses the manifest.
                          testbed.                

  I4                      No double grant of a    **Open item:** write the
                          lease.                  concurrency/fault-injection
                                                  test before confirmatory
                                                  runs.

  I5                      A receipt implies a     Restart/power-loss cycles.
                          durable report.         

  I6                      An observed idempotency Attacker registers key first;
                          key cannot be hijacked  victim conflict is visible.
                          silently.               

  I7                      Emergency floor is      Induce quarantine from
                          never charged for other increasing attacker-group
                          tiers' quarantine.      counts.

  I8                      Every evaluation arm    CI budget check within
                          has equal total budget  registered tolerance.
                          per resource.           
  -----------------------------------------------------------------------------

The lock-handle confirmation rule is also open. Do not declare all
invariants complete until these gaps have a testable policy.

## 12. Failure Modes and Explicit Limits

-   **Attacker fills sacrificial pool:** probation admission freezes at
    `qmax`; first-time unattested reporters may be crowded out until
    operator action. This is bounded damage, not a solved problem.
-   **Legitimate and malicious anonymous requests are
    indistinguishable:** aggregate caps bound consumption, but perfect
    per-request classification is impossible with the available signals.
-   **Many ASNs used by attacker:** per-ASN limits raise attack cost but
    do not stop distributed attacks.
-   **Database-bound workload:** extra execution cells may add no
    capacity; report a tie if measured.
-   **Pooled database backend:** handle ownership may be unconfirmable;
    keep charge as debt/quarantined.
-   **Downstream call:** report currently treats it as unconfirmable and
    charged as debt unless a provider-specific confirmation mechanism is
    implemented.
-   **Lock handle:** release confirmation is undefined and must be
    resolved before claiming complete per-handle accounting.
-   **Node replacement:** manual/operator-driven; no availability
    guarantee depends on unmeasured replacement time.
-   **Attestation:** if hardware-backed attestation cannot be verified,
    disable the attested-lane trust assumption.
-   **External effects:** exactly-once semantics depend on provider
    contracts; non-idempotent residual window must be measured.
-   **Search and simulation:** attacker search may miss a true worst
    case; simulator results may not transfer to the real testbed.

## 13. Implementation Order

1.  Measure stop-time distributions, fence bump/claim behavior, provider
    send time, and operator/node replacement time.
2.  Define unresolved resource semantics: downstream-call debt, lock
    confirmation, pooled backend handling, and lease double-grant
    prevention.
3.  Implement schema, state transitions, ledger, and unit/property
    tests.
4.  Implement reverse audit early and prove it detects a planted orphan.
5.  Implement intake quota/tier/idempotency/durable-receipt path.
6.  Implement pool isolation, lease service, release ladder, and
    quarantine budgets.
7.  Implement fenced outbox and mock effect providers for all three
    contracts.
8.  Implement B1 and B2 before tuning ZAC; enforce equal budgets in CI.
9.  Build simulator and real testbed; compare them on shared scenarios.
10. Run attacker-search validation and pilot runs.
11. Freeze pre-registration, code/config revision, and seed split.
12. Run held-out confirmatory evaluation; publish win/tie/loss and
    limitations.

## 14. Architecture Decision Log / Open Decisions

  -------------------------------------------------------------------------
  Decision                Current position          Status
  ----------------------- ------------------------- -----------------------
  Adaptive vs fixed       ZAC-fixed is              Provisional
  policy                  confirmatory;             
                          ZAC-adaptive exploratory. 

  Device trust            Hardware-backed           Must validate
                          server-verified           
                          attestation only.         

  Quarantine release      Per-handle verification;  Core invariant
                          unconfirmed charges       
                          remain held.              

  Downstream handle       Keep as debt unless       Open implementation
                          provider confirms         detail
                          completion/termination.   

  Lock handle             No confirmation rule      Blocking open item
                          specified yet.            

  Double lease grant      Must be prevented by      Test not yet written
                          admission/lease service.  

  Node replacement        Operator runbook, not an  Measure
                          automatic guarantee.      

  Numerical thresholds    All values are            Not frozen
                          placeholders until        
                          measured and              
                          pre-registered.           

  Novelty claim           No novelty claim accepted Unverified
                          until wider prior-art     
                          review.                   
  -------------------------------------------------------------------------

## 15. Deployment Boundary

This design is for a controlled research prototype. Do not connect it to
live emergency intake or dispatch systems until there is independent
security review, formal protocol analysis, operational failover design,
provider-contract validation, accessibility/operator testing, and a
separately approved safety case. The present report explicitly leaves
production readiness and high availability out of scope.
