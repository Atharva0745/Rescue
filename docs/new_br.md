# RESCUE --- Business Requirements (BR)

**Version:** 1.0\
**Status:** Requirements baseline for implementation planning\
**Source basis:** *RESCUE: Completion-Backed Admission Control for
Emergency Communication Gateways under Adversarial Overload* and the
accompanying deep-research report.\
**Important:** This is a research prototype specification, not a
production emergency-service deployment plan. Numeric limits and pass
thresholds remain placeholders until measured and pre-registered.

> This is the target requirements baseline, not an implementation-status
> report. Current completion evidence and remaining phases are maintained in
> [`phase.md`](phase.md).

## 1. Executive Summary

RESCUE investigates whether an emergency-report gateway can preserve
more legitimate reports through their deadlines during application-layer
overload and adversarial traffic by combining bounded admission,
per-resource accounting, and verified resource release.

The central rule is **do not return capacity merely because a client
timed out or cancellation was requested**. A resource charge is released
only after that specific resource is confirmed gone. If release cannot
be confirmed, the task is quarantined and the charge remains held.

The project also evaluates hierarchical source limits, protected
execution pools, elastic capacity sharing between attested and
unattested traffic, source-specific penalties, durable receipts,
idempotency, fenced side effects, and reverse auditing for resources
absent from the ledger.

## 2. Business Problem

During an emergency or disaster, legitimate reports may arrive in bursts
while malicious requests consume workers, database connections,
downstream capacity, or other constrained resources. Request-count
limits may misrepresent actual cost. Attackers may rotate IP addresses
or sessions, while legitimate users may share a public address through
carrier-grade NAT. Backend work may also continue after the client
disconnects, causing a controller to overestimate free capacity if it
treats a timeout as proof of termination.

RESCUE will test whether a completion-backed resource-accounting design
improves **legitimate deadline success** over strong, equal-budget
baselines. It does not claim to identify every legitimate anonymous
request, nor to guarantee success when genuine demand exceeds
sustainable capacity.

## 3. Goals

1.  Improve the fraction of legitimate reports completed within their
    registered deadlines under the defined adversarial workload.
2.  Bound admitted and executing work at source-group, ASN-and-region,
    and global scopes.
3.  Keep resource charges held until each resource is independently
    confirmed released.
4.  Contain untrusted or first-seen sources in a sacrificial/probation
    pool without consuming the protected emergency floor.
5.  Reduce collateral rejection caused by shared carrier networks and
    blanket tier inheritance.
6.  Preserve durable report receipts and safe duplicate handling.
7.  Prevent stale lease holders from claiming or sending effects under
    an obsolete fence version, subject to documented provider
    limitations.
8.  Detect orphaned resources by auditing the runtime and database
    independently of the ledger.
9.  Compare RESCUE against baselines with equal physical resource
    budgets and report wins, ties, and losses honestly.

## 4. Non-Goals

-   Production deployment to a live emergency-service network.
-   Solving physical network saturation, routing/DNS failures,
    compromised infrastructure, or third-party provider outages.
-   Perfectly distinguishing legitimate and malicious requests when
    their observable attributes are indistinguishable.
-   Guaranteeing completion when legitimate demand alone exceeds
    capacity.
-   Assuming that device attestation proves the truth or urgency of a
    report.
-   Claiming exactly-once external effects for providers that do not
    support idempotency or equivalent reconciliation.
-   Automatic node replacement or a high-availability control plane in
    the initial research prototype.
-   Treating simulated audit results as real testbed measurements.

## 5. Stakeholders and Users

  -----------------------------------------------------------------------
  Stakeholder                         Need
  ----------------------------------- -----------------------------------
  Legitimate reporter/client          Receive a truthful receipt for a
                                      durably stored report; avoid silent
                                      duplicate/conflict outcomes.

  Emergency intake operator           Preserve intake and protected
                                      capacity during overload; see
                                      quarantine and escalation states.

  System operator                     Inspect source tiers, resource
                                      charges, quarantine budgets, orphan
                                      findings, and recovery actions.

  Research evaluator                  Reproduce workloads, baselines,
                                      attacker search, seed splits, and
                                      metrics.

  Downstream dispatch/SMS provider    Receive effects under explicit
                                      idempotency/fencing contracts.
  -----------------------------------------------------------------------

## 6. Scope and Assumptions

### In scope

-   Application-level intake and admission control.
-   Hierarchical quotas and source tiers.
-   Attested/unattested elastic lanes with minimum guarantees and an
    aggregate hard cap.
-   Execution leases, resource manifests, a per-resource ledger,
    cancellation/termination, verification, and quarantine.
-   Durable intake logging, receipts, scoped idempotency, and
    payload-hash conflict detection.
-   Fenced outbox and effect senders.
-   Reverse audit of PostgreSQL activity and Linux cgroups in the real
    testbed.
-   Simulator, Linux/PostgreSQL testbed, attacker search, baselines, and
    statistical evaluation.

### Assumptions to validate

-   Hardware-backed attestation can be verified server-side before the
    attested lane is enabled. A self-generated installation ID is not
    sufficient.
-   The testbed can isolate the sacrificial pool and identify the
    process/cgroup and database backend associated with each task.
-   The selected downstream providers' idempotency and reconciliation
    semantics are documented.
-   Sustainable capacity, stop-time distributions, send-time bounds, and
    node-replacement latency will be measured rather than guessed.

## 7. Functional Requirements

  -----------------------------------------------------------------------
  ID                      Requirement             Acceptance evidence
  ----------------------- ----------------------- -----------------------
  BR-F01                  Every incoming report   Tests cover
                          must pass hierarchical  source-group,
                          quota and tier checks   ASN-region, and global
                          before execution        caps.
                          admission.              

  BR-F02                  New or unproven sources Integration test
                          must begin in probation confirms pool placement
                          and execute only in the and bounded starter
                          sacrificial pool.       share.

  BR-F03                  Admission must account  Ledger assertions match
                          for a report's required allocated cells, DB
                          resource handles and    connections, and
                          charge each bottleneck  tracked downstream
                          resource.               budgets.

  BR-F04                  A client timeout or     Cancellation-ignoring
                          cancellation request    handler test keeps
                          alone must never        charge held.
                          release a resource      
                          charge.                 

  BR-F05                  Release must verify     Per-handle tests cover
                          each handle             confirmed and
                          independently; any      unconfirmed resources.
                          unconfirmed handle      
                          keeps the task          
                          quarantined and         
                          charged.                

  BR-F06                  A reverse auditor must  Plant an unregistered
                          enumerate               backend/cgroup and
                          runtime/database        confirm an orphan
                          resources and flag      finding.
                          handles with no ledger  
                          owner.                  

  BR-F07                  Source-specific         One attacking device
                          penalties must not      test verifies unrelated
                          automatically downgrade devices do not inherit
                          all users in an ASN.    its punitive tier.
                          ASN congestion may      
                          activate an aggregate   
                          cap.                    

  BR-F08                  Attested and unattested Tests cover idle-lane
                          lanes must have         borrowing and
                          protected minimum       both-lanes-overloaded
                          allocations and may     behavior.
                          borrow unused capacity  
                          without exceeding the   
                          ASN aggregate cap.      

  BR-F09                  The emergency floor     Induce quarantine
                          must not be charged for across attacker-group
                          other tiers'            counts and inspect pool
                          quarantine.             accounting.

  BR-F10                  A report receipt may be Restart/power-loss
                          issued only after the   simulation verifies
                          durable intake record   every receipted report
                          has been fsynced. On    is present.
                          fsync failure, stop     
                          issuing receipts.       

  BR-F11                  A repeated idempotency  Replay and key-hijack
                          key with the same       tests.
                          payload hash must       
                          return the stored       
                          receipt; a conflicting  
                          payload must be         
                          rejected and logged.    

  BR-F12                  Lease changes must      Randomized
                          fence stale senders;    interleavings and
                          claims and fence bumps  crash-point tests.
                          must use the specified  
                          shared/exclusive        
                          locking protocol.       

  BR-F13                  Effect handling must    Contract-specific
                          declare whether each    sender tests and
                          effect is               duplicate-window
                          provider-keyed,         measurement.
                          naturally idempotent,   
                          or non-idempotent.      

  BR-F14                  The evaluation harness  CI budget check passes
                          must run B1, B2, and    within the
                          ZAC-fixed with equal    pre-registered
                          total physical budgets  tolerance (currently a
                          on each resource.       placeholder of 1%).

  BR-F15                  The evaluation harness  Reproducible seed
                          must retain             manifest, search logs,
                          pilot/confirmatory seed and planted-attacker
                          separation and test     report.
                          whether the attacker    
                          search finds a planted  
                          attacker.               

  BR-F16                  ZAC-adaptive must       Reports clearly
                          remain exploratory      distinguish
                          until its estimator and confirmatory and
                          comparison protocol are exploratory results.
                          separately validated.   
  -----------------------------------------------------------------------

## 8. Quality and Research Requirements

-   **Safety:** no task is marked RELEASED while a tracked, confirmable
    handle remains live.
-   **Boundedness:** source and global budgets cannot grow merely
    because an attacker creates more identifiers.
-   **Durability:** a successful receipt means the report is durable in
    the intake log.
-   **Auditability:** state transitions, quota decisions, fence
    versions, charges, quarantine, conflicts, and escalations are
    logged.
-   **Reproducibility:** configuration, workload parameters, seeds, code
    revision, and budget checks are recorded for every run.
-   **Fairness measurement:** report legitimate deadline success and
    false rejection separately; do not infer legitimacy solely from
    IP/ASN.
-   **Honest uncertainty:** unconfirmable downstream calls and locks
    remain explicit open items/debt, not falsely reported as released.
-   **Failure transparency:** ties, negative results, search failures,
    and testbed/simulator differences must be reported.

## 9. Primary Metrics and Hypothesis

### Primary metric

(S_L): fraction of legitimate reports that complete within their
required deadline.

### Confirmatory comparison

ZAC-fixed versus B2, where B2 already includes fair queuing and
per-source quarantine budgets under the same resource budget. B1 is a
secondary baseline.

The report's current H1 is that, on the registered reference workload W4
and against a best-responding attacker found within the bounded search,
ZAC-fixed improves worst-case legitimate deadline success over B2 by at
least a frozen margin. The current document contains placeholder
margins; these must be measured, justified, and pre-registered before
confirmatory runs.

### Secondary metrics

-   False-rejection rate for legitimate reports.
-   Per-resource occupancy and unused capacity.
-   Orphan resources found by reverse audit.
-   Collateral kills of legitimate work.
-   Capacity-seconds per legitimate success.
-   Fence-bump latency under sustained claims.
-   Duplicate-send window for non-idempotent effects.
-   Quarantine occupancy and time to operator intervention.
-   Intake durability and recovery outcomes.

## 10. Required Test Scenarios

1.  One attacking attested device cannot exceed its device
    quota/concurrency budget.
2.  One attacking unattested device cannot exhaust the entire ASN
    budget.
3.  Unattested traffic can borrow unused attested-lane capacity without
    exceeding the aggregate cap.
4.  An ASN congestion alarm does not automatically punish unrelated
    legitimate devices.
5.  Both lanes overloaded: aggregate capacity remains bounded and
    scheduling continues.
6.  Backend work survives client timeout: capacity remains charged until
    verified release.
7.  A backend bypasses the manifest: reverse audit finds the orphan.
8.  Quarantine caused by multiple attacker groups does not consume
    emergency-floor capacity.
9.  Sender crashes before claim, after claim, and after send but before
    acknowledgement.
10. An attacker registers an observed idempotency key first; the victim
    is not silently told the report was accepted as a duplicate.
11. Power-loss/restart cycles preserve every receipted report.
12. Database-bound workload W7 may show no improvement; report a tie if
    that is what measurements show.

## 11. Constraints and Open Risks

-   Numeric thresholds, lane minimums, qmax, deadlines, and effect
    timeouts are not final until measured.
-   Device attestation remains an unconfirmed deployment assumption.
-   Downstream calls and lock handles currently lack complete
    release-confirmation rules.
-   Pooled database connections may make task-to-backend attribution
    unconfirmable; such charges must remain debt/quarantined.
-   The sacrificial pool can be filled by an attacker, temporarily
    crowding out first-time unattested reporters.
-   Intake-log capacity does not guarantee worker or database capacity.
-   A bounded attacker search can miss the true worst case;
    planted-attacker validation is mandatory.
-   Simulator findings may not transfer to the real testbed.
-   Novelty claims in the research report are provisional and require a
    broader literature review.

## 12. Delivery Plan and Gates

  -----------------------------------------------------------------------
  Weeks                   Deliverable             Gate
  ----------------------- ----------------------- -----------------------
  1--2                    Measure stop-time       Measurements recorded;
                          distributions,          unresolved resource
                          replacement time, and   types documented.
                          per-report fence        
                          behavior.               

  3--4                    Schema, ledger,         Go/no-go gate at end of
                          reverse-audit stub, and week 4.
                          harness skeleton.       

  5--7                    Linux cgroup/PostgreSQL Release tests pass;
                          testbed, B1/B2,         budget check passes.
                          equal-budget            
                          enforcement.            

  8--9                    Attacker search and     Planted attacker found
                          pilot variance          within registered
                          estimates.              tolerance.

  10                      Freeze pre-registration Configuration/hash
                          and seed split.         committed before
                                                  confirmatory runs.

  11--12                  Held-out confirmatory   Report interval and
                          ZAC-fixed vs B2         win/tie/loss.
                          evaluation.             

  13--14                  Final analysis,         Negative and
                          limitations, and        database-bound results
                          reproducibility         included.
                          package.                
  -----------------------------------------------------------------------

If the week-4 gate fails, reduce attacker-search dimensionality first,
then remove the CMA-ES-versus-random-search comparison, then move the
adaptive estimator and worker isolation to optional work, as capacity
permits.

## 13. Definition of Done

The research prototype is ready for evaluation---not
production---when: - Required components compile and start from
documented instructions. - Functional requirements have automated
tests. - All safety invariants are exercised by tests or explicitly
marked unresolved. - Reverse audit detects a deliberately planted
orphan. - Every baseline passes the equal-budget check. - Workload,
attacker search, seeds, and analysis are reproducible. -
Pre-registration is frozen before confirmatory runs. - Results include
limitations, uncertainty, ties/losses, and simulator-to-testbed
differences. - Open items are clearly documented; no unmeasured value is
presented as an established guarantee.
