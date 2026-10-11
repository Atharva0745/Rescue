# RESCUE-4 --- Business Requirements (BR)

**Document status:** Draft based on Technical Specification v1.5\
**Validation status:** Requirements and experiments are specified but
not yet executed.\
**Purpose:** Define the problem, intended outcomes, scope, success
criteria, and constraints for RESCUE-4.

## 1. Problem Statement

RESCUE-4 investigates whether lease-based admission control combined
with owner-confirmed release, executor quarantine, and database fencing
can contain orphaned work more effectively than admission controls that
release capacity at lease expiry.

The central concern is work that continues executing after its lease
expires. Such work can consume executor capacity and may attempt stale
writes. The proposal measures this as orphaned-work exposure rather than
assuming that improved admission control alone provides the desired
protection.

The contribution is explicitly conditional: if the measured stop-failure
rate is too low, or if the shared semaphore already provides the
observed benefit, the containment claim may not be supported.

## 2. Objectives

1.  Measure orphaned-work exposure (OE), including executor time spent
    running beyond lease expiry and stale-write commits, reported
    separately as well as in the combined metric.
2.  Compare RESCUE-4 against alternative admission policies under
    matched workload traces and fault draws.
3.  Determine whether owner confirmation, quarantine, and database
    fencing provide measurable benefit beyond a shared stage semaphore
    and execution budgets.
4.  Test whether legitimate requests retain acceptable success rates and
    latency behavior.
5.  Make safety and performance claims reproducible through
    pre-registration, seeded workloads, adversarial tests, and explicit
    validation gates.
6.  Report negative or inconclusive results without changing the
    pre-registered decision rules.

## 3. Stakeholders and Users

The specification does not identify named business stakeholders. The
relevant technical roles are:

-   **System designer:** defines policies, assumptions, thresholds, and
    experiment configuration.
-   **Implementer:** builds the admission, release, quarantine, fencing,
    and measurement components.
-   **Independent reviewer:** reviews proofs, baseline fidelity, and
    implementation diffs.
-   **Experiment operator:** runs calibration, simulation, and testbed
    experiments.
-   **Evaluator:** assesses whether the registered hypotheses are
    supported.

## 4. Scope

### In scope

-   Three admission policies: A′ (adaptive limiter), D (DRACO-style
    datastore-aware admission policy), and B (RESCUE-4 reservations).
-   Three enforcement/release levels:
    -   **NS:** no shared stage semaphore or cgroup budgets; release at
        expiry.
    -   **TR:** shared stage semaphore and cgroup budgets; release at
        expiry.
    -   **OC:** shared stage semaphore and cgroup budgets;
        owner-confirmed release, quarantine, and database fencing.
-   Workloads W1 (bottleneck shift), W2 (slow completion,
    acknowledgement withholding, or hung executors), and W3 (W1 + W2 +
    gateway crash).
-   Stop-failure calibration, fault-rate sweep, and zero-fault overhead
    guardrail.
-   Simulation checker, transaction-level checker tests,
    scheduling-bound tests, and later database testbed validation.
-   Comparison with a faithful DRACO implementation only if its
    baseline-fidelity protocol passes.

### Out of scope / explicitly limited

-   Database failover behavior under assumption A4.
-   Multi-gateway behavior beyond the stated single-gateway assumption
    for the current bounds.
-   Byzantine writers that forge audit values.
-   Claims that empirical service-rate envelopes constitute formal
    guarantees.
-   Claims that the system or its tests have already passed; v1.5 states
    that nothing has been run.

## 5. Functional Requirements

  -----------------------------------------------------------------------
  ID                                  Requirement
  ----------------------------------- -----------------------------------
  BR-F01                              The system shall support the A′, D,
                                      and B admission policies.

  BR-F02                              Each admission policy shall support
                                      NS, TR, and OC levels with only the
                                      specified factors differing.

  BR-F03                              The OC level shall require owner
                                      confirmation before capacity
                                      release and shall include
                                      quarantine and database fencing.

  BR-F04                              The system shall track lease epochs
                                      and incarnations and associate
                                      fenced writes with the
                                      corresponding version.

  BR-F05                              The checker shall process committed
                                      transactions in commit-record
                                      order, validate each transaction
                                      against pre-transaction state, then
                                      apply fence bumps.

  BR-F06                              The checker shall flag stale
                                      commits, future versions, unknown
                                      gateways, non-monotone bumps, mixed
                                      transactions, and unaudited or
                                      orphan audit records as defined in
                                      v1.5.

  BR-F07                              The system shall record executor
                                      occupancy, stop outcomes, useful
                                      completion, legitimate-request
                                      success, and database audit
                                      outcomes.

  BR-F08                              Calibration shall be independent of
                                      admission arm outcomes and shall be
                                      frozen before confirmatory seed
                                      lists and traces are generated.

  BR-F09                              Each run shall record its
                                      configuration, seeds, fault rates,
                                      software/database versions, and
                                      relevant system measurements in a
                                      manifest.

  BR-F10                              Results shall distinguish
                                      simulation-only evidence from
                                      PostgreSQL testbed evidence.
  -----------------------------------------------------------------------

## 6. Non-Functional Requirements

-   **Safety:** stale-write detection and fencing behavior must be
    tested with planted violations, legitimate late completions,
    mutants, and differential histories.
-   **Reproducibility:** paired arms use the same seed-defined arrival
    trace, request classes, demand draws, and fault draws; arm order is
    randomized.
-   **Observability:** OS and database counters are used for OE
    measurement, and the OE components are reported separately.
-   **Bounded occupancy:** executor occupancy is constrained by
    wall-clock caps and enforced stop/kill handling under the stated
    assumptions.
-   **Performance:** useful completion is conditional on empirically
    measured service-rate envelopes; interference and deadline misses
    must be reported.
-   **Research integrity:** thresholds, hypotheses, seed blocks,
    calibration decisions, and analysis procedures are frozen before
    confirmatory runs.

## 7. Workloads

-   **W1 --- Bottleneck shift:** evaluates behavior when the bottleneck
    changes; also supports the no-attack overhead guardrail.
-   **W2 --- Slow completion / cancellation difficulty:** exercises
    delayed completion, withheld acknowledgements, and hung executors.
-   **W3 --- Combined failure workload:** combines W1 and W2 with a
    gateway crash.

Exact workload timing and scenario details should remain aligned with
the main technical specification and the eventual pre-registration.

## 8. Success Criteria and Decision Rules

The primary hypothesis family is P-1a, P-1b, P-2, P-3, P-4a, and P-4b,
as defined in v1.5.

-   The joint novelty claim is supported only if all six hypotheses
    reject under the registered one-sided decision rule.
-   P-1a and P-1b assess OE improvement against D-TR and A′-TR.
-   P-2 assesses legitimate-request success against D-TR.
-   P-3 is the no-attack W1 non-inferiority guardrail against D-OC.
-   P-4a and P-4b test whether OC adds value beyond TR within the B
    policy.
-   If the P-4 tests fail while baseline comparisons pass, the novelty
    claim based on OC fails.
-   If power is inadequate at the registered maximum sample size, report
    the result as inconclusive.
-   If calibration Case A applies, the headline claim must be explicitly
    conditional and is not testable at the measured stop-failure rate.

The exact values of δ_C, δ_OE, p_main, sample size, and analysis
settings are provisional until frozen in the pre-registration.

## 9. Mandatory Gates

  -----------------------------------------------------------------------
  Gate                                Required evidence
  ----------------------------------- -----------------------------------
  G0-C1                               Checker passes planted-violation,
                                      legitimate-late-completion,
                                      differential, edge-case, replay,
                                      and mutant tests.

  G0-C2                               D baseline passes the faithfulness
                                      protocol or is relabeled
                                      "DRACO-inspired"; no DRACO
                                      comparison is claimed if
                                      reproduction fails.

  G0-C3                               Diff review confirms NS/TR/OC
                                      differ only in specified factors.

  G0-C4                               Stop-failure calibration is
                                      measured and p_main is frozen.

  G0-C5                               T1--T2 derivations are
                                      independently reviewed and T-D1 to
                                      T-D4 pass.

  G0-C6                               Closed hypothesis family,
                                      thresholds, seed blocks, and
                                      analysis procedure are committed
                                      before confirmatory runs.
  -----------------------------------------------------------------------

## 10. Risks and Open Questions

-   T1 and T2 are hand-derived and require independent review.
-   No checker, simulation, calibration, or experiment has yet been run.
-   DRACO full text has not yet been read; baseline fidelity is
    unconfirmed.
-   Useful-completion guarantees remain empirical.
-   Low measured stop-failure rates may not support the containment
    claim at the real operating point.
-   Failover, broader multi-gateway scope, and Byzantine writers are
    outside the stated scope.
-   The sign-flip test's assumptions and pre-registered fallback need
    review.

## 11. Definition of Done

The project is ready for a supported research conclusion when the
mandatory gates pass, the pre-registered experiments are run, results
are reported with uncertainty and all OE components separated, and
conclusions follow the registered decision rules. Passing simulation
gates alone does not validate PostgreSQL fencing or baseline fidelity.
