# RESCUE-4 --- Experiment Evaluation Process

**Source:** RESCUE-4 Technical Specification v1.5\
**Status:** Evaluation criteria and methods; this document is not an execution
ledger. Current completion status and evidence are maintained only in
[`phase.md`](phase.md).\
**Purpose:** Define how RESCUE-4 experiments are evaluated, how evidence
is accepted, and how conclusions are selected without changing the
registered criteria after results are observed.

## 1. Evaluation Principles

1.  **Pre-register before confirmatory runs.** Freeze hypotheses,
    thresholds, seed policy, calibration outcome, sample-size procedure,
    test method, and fallback rules before generating confirmatory seed
    lists and traces.
2.  **Use paired comparisons.** A seed defines the arrival trace,
    request classes, demand draws, and fault draws. Arms in a contrast
    use the same trace and draws; arm execution order is randomized.
3.  **Separate evidence types.** Mathematical review, simulation
    evidence, and PostgreSQL testbed evidence are not interchangeable.
4.  **Keep all outcomes.** Record failed runs, exclusions, configuration
    deviations, and inconclusive results. Do not silently remove
    unfavorable runs.
5.  **Follow the registered decision rules.** Do not change thresholds
    or add confirmatory hypotheses after observing results. Additions
    are exploratory.
6.  **Report conditional claims honestly.** The containment claim is
    conditional on the measured stop-failure rate and tested
    configuration; it is not unconditional superiority.

## 2. Evaluation Workflow

``` text
Review assumptions and proofs
            |
            v
Run G0-C1 checker tests and G0-C5 bound tests
            |
            v
Review baseline fidelity and NS/TR/OC arm differences
            |
            v
Measure stop-failure rates and freeze p_main
            |
            v
Freeze and hash the pre-registration
            |
            v
Run paired confirmatory simulation
            |
            v
Apply the registered statistical decision rules
            |
            v
Validate database-dependent claims in PostgreSQL
            |
            v
Report supported, unsupported, or inconclusive conclusions
```

A stage must not be treated as passed merely because its implementation
plan exists. Each gate requires recorded evidence.

## 3. Stage A --- Pre-Experiment Verification

### A1. Review the scheduling bounds

Independently review T1 and T2, including: - Definitions of `held_live`,
`R`, `m`, and `β_max`. - Whether the arriving job is included in the
held-cap total used by T1. - Work-conserving dispatch, FIFO order,
identical executors, and dispatch-overhead assumptions. - The
Graham-style drain bound and the tightness example. - Limitations for
adaptive LIFO and executor-loss cases.

Run the specified bound tests: - **T-D1:** at least 10\^6 exact-rational
dispatcher instances; no T1/T2 violations in valid cases. - **T-D2:**
tight instances for `m ∈ {2, 4, 8, 16}` meet T2 with equality. -
**T-D3:** non-work-conserving, LIFO, and ignored-executor-loss mutants
produce counterexamples. - **T-D4:** real-stage measurements remain
within the bounds under the declared assumptions. This test cannot be
passed by simulation alone.

**Evaluation outcome:** `PASS`,
`FAIL—correct specification/implementation`, or `NOT RUN`.

### A2. Validate the transaction checker

The checker must be compared with a naive reference oracle on at least
10\^5 generated histories. Histories include fence bumps, valid and
stale writes, mixed transactions, bypass writes, and checker restarts
during replay.

Required checks: - Zero disagreements with the naive oracle in the
registered differential test set. - All planted stale commits are
detected. - No planted legitimate late completions are flagged. -
Mutants M1--M4 are detected. - Edge cases for multiple gateways,
multiple bumps, mixed transactions, slot lag, and replay are handled
according to the specification.

The simulation checker and PostgreSQL logical-decoding checker are
evaluated separately. Passing the synthetic-history tests does not
establish correctness of the database integration.

**Evaluation outcome:** `PASS`, `FAIL—fix checker`, or `NOT RUN`.

### A3. Verify the arm ladder and baseline

-   Review implementation diffs for A′, D, and B at NS, TR, and OC.
-   Confirm that NS/TR/OC differ only in the factors listed in the
    specification.
-   Read the full DRACO paper and create a traceability table mapping
    its algorithmic elements to code.
-   Reproduce its qualitative selective-dropping behavior on a
    hot-datastore-node workload.
-   Obtain independent review of the reimplementation.

If DRACO fidelity cannot be established, label the arm
**DRACO-inspired** and do not describe the result as a DRACO comparison.

**Evaluation outcome:** `PASS—faithful baseline`,
`PASS—relabeled DRACO-inspired`, or `NOT RUN`.

## 4. Stage B --- Fault-Rate Calibration

### B1. Measure cancellation failures

Measure the probability that cooperative cancellation fails to stop an
executor within `T_cancel`, under attack-load conditions and
independently of admission-arm outcomes.

-   Minimum: `N_cal = 1,000` cancellation attempts per stage.
-   Extend in blocks up to `N_max = 10,000` when the interval is too
    wide.
-   Compute a two-sided 95% Clopper--Pearson interval `[L, U]`.
-   Record per-class rates and separately calibrate
    kill-failure/UNCONFIRMED paths as specified.

Zero observed failures must not be reported as proof that the true
failure rate is zero.

### B2. Select and freeze the operating point

Apply the v1.5 rule: - **Case A:** if `U < 1%`, run the 1% cell as a
stress cell, not as the measured operating condition. State that the
headline claim is conditional and not testable at the measured rate. -
**Case B:** otherwise, select `p_main` using the registered grid rule
and interval. - **Case C:** if `L > 20%`, use the specified 20% and 40%
cells and flag the stop mechanism as unreliable.

Resolve and document every edge case deterministically, including
intervals that overlap the grid but contain no grid point.

Before confirmatory seed lists and traces are generated, commit/hash the
calibration result, interval, sample size, case, and selected `p_main`.

## 5. Stage C --- Pre-Registration

Freeze and commit: - The six primary hypotheses: P-1a, P-1b, P-2, P-3,
P-4a, P-4b. - Per-seed `S_L` and `OE_norm` definitions and measurement
windows. - `δ_C` and `δ_OE`, calculated by the specified pilot rules. -
The paired seed unit, arm randomization, seed blocks, and exclusion
policy. - The one-sided paired sign-flip permutation test and its
assumptions. - The pre-registered fallback if the assumptions are not
sufficiently supported. - Sample-size and joint-power procedures,
maximum sample size, and inconclusive-result rule. - Intersection-union
decision rule for the joint claim and Holm adjustment for individual
claims.

The numerical thresholds remain provisional until frozen by the
pre-registration. Pilot data may inform the registered threshold rules;
confirmatory outcomes must not be used to retune them.

## 6. Stage D --- Confirmatory Experiment Execution

### D1. Required runs

  --------------------------------------------------------------------------
  Test              Workload / cell   Main comparison   Purpose
  ----------------- ----------------- ----------------- --------------------
  P-1a              W3 at `p_main`    B-OC vs D-TR      Test OE improvement
                                                        over the
                                                        strengthened D
                                                        baseline

  P-1b              W3 at `p_main`    B-OC vs A′-TR     Test OE improvement
                                                        over the
                                                        strengthened A′
                                                        baseline

  P-2               W3 at `p_main`    B-OC vs D-TR      Test
                                                        legitimate-request
                                                        success

  P-3               W1, α = 0, no     B-OC vs D-OC      Test the no-attack
                    attack                              non-inferiority
                                                        guardrail

  P-4a              W3 at `p_main`    B-OC vs B-TR      Test incremental OE
                                                        benefit from OC

  P-4b              W3 at `p_main`    B-OC vs B-TR      Test incremental
                                                        legitimate-request
                                                        success
  --------------------------------------------------------------------------

Use the same seed-defined trace and fault draws within each paired
contrast. Randomize arm order. Store per-seed results, not only
aggregate averages.

### D2. Secondary runs

Run and report the registered fault sweep `p_fail ∈ {0, 1%, 5%, 20%}`,
plus any additional cell required by the above-grid calibration rule.
Report the crossover point where OC begins to outperform TR. Include the
mandatory zero-fault overhead check.

Secondary analyses do not replace or redefine the primary hypotheses.

### D3. Required records

For every run, record: - Code revision and configuration hash. - Seed,
workload, arm, arm order, fault draws, and calibration cell. -
PostgreSQL and logical-decoding versions/configuration where
applicable. - OS, CPU, memory, service-rate, executor, and database
measurements relevant to the run. - Run completion status, errors,
deviations, exclusions, and reasons.

## 7. Metrics to Evaluate

### 7.1 Orphaned-work exposure

Report both components separately: - Executor-seconds spent running
beyond lease expiry. - Stale-write commits weighted by the registered
`c_w`.

Also report the registered aggregate `OE` and normalized metric:

`OE_norm = OE / (K_s · T_window)`, summed over stages.

Do not let a favorable aggregate conceal a regression in one component.

### 7.2 Legitimate-request success

For each seed, `S_L` is the fraction of harness-labeled legitimate
requests that arrive within the measurement window and receive their
full response by `arrival + D_c`. A success requires a `completed`
outcome, admission, a recorded completion timestamp no later than the
request deadline, and consistent deadline fields. Rejections, timeouts,
deadline misses, and unfinished requests count as failures. The same
pre-registered arrival window must be configured for every arm in a
paired comparison.

Report per-arm per-seed values, paired differences, and the registered
aggregate statistics.

For the generated Go mixed-pool diagnostic, also report first-contact
legitimate success (`S_F`) separately: successful, harness-labeled
legitimate first-contact requests divided by legitimate first-contact
requests offered in the measurement window. Use the same completion and
deadline predicate as `S_L`. This diagnostic is not a substitute for the
registered baseline comparison, and generated legitimacy labels do not
establish real-world user identity.

### 7.3 Supporting measures

If reporting goodput, use the same fixed wall-clock measurement interval
for every arm. Goodput is the number of successful completions whose
completion timestamps fall within that interval, divided by the full
interval duration; do not derive the denominator from observed arrivals
or successful requests. Report successful legitimate-request latency
percentiles separately from timeout and deadline-miss counts. Failed or
unfinished request durations are censored and excluded from the
successful-latency distribution. In the Go benchmark, `S_L` is evaluated
for requests arriving within the measurement window and against each
request's own deadline; a qualifying completion can occur after the
measurement window. In-window goodput instead requires the completion
timestamp to fall inside the measurement window. Label these distinct
boundaries when reporting both measures.

Report executor occupancy, cancellation and kill outcomes,
quarantined/UNCONFIRMED executors, service-rate degradation, deadline
misses, and overhead. Distinguish occupancy guarantees from conditional
useful-completion behavior.

## 8. Statistical Evaluation

### 8.1 Primary hypotheses

  ID     Registered alternative
  ------ -----------------------------------------
  P-1a   `OE_norm(D-TR) − OE_norm(B-OC) > δ_OE`
  P-1b   `OE_norm(A′-TR) − OE_norm(B-OC) > δ_OE`
  P-2    `S_L(B-OC) − S_L(D-TR) ≥ δ_C`
  P-3    `S_L(B-OC) − S_L(D-OC) ≥ −0.03`
  P-4a   `OE_norm(B-TR) − OE_norm(B-OC) > δ_OE`
  P-4b   `S_L(B-OC) − S_L(B-TR) ≥ δ_C`

### 8.2 Tests and uncertainty

Use the registered one-sided paired sign-flip permutation test, with the
threshold incorporated into the null as specified in the
pre-registration. The v1.5 plan specifies 100,000 flips.

-   Check the sign-flip assumptions using pilot differences.
-   Use only the fallback method specified before confirmatory runs if
    the assumptions are not adequately supported.
-   Report BCa bootstrap confidence intervals descriptively as planned.
-   If a confidence interval and the registered test disagree, apply the
    pre-registered decision rule rather than selecting whichever result
    favors the claim.
-   Report effect estimates, uncertainty, sample size, and per-seed
    distributions, not only p-values.

### 8.3 Sample size and power

Use the pre-registered sample-size procedure based on the largest paired
standard deviation among the six pilot contrasts, with at least 20 seeds
and a maximum of 60 as specified.

Estimate joint power by simulation from pilot differences. If joint
power is below 0.8 at `N_max = 60`, label a non-rejection
**inconclusive**, not evidence of no effect.

The sample-size formula and its assumptions must be checked before the
pre-registration is frozen.

## 9. Decision Rules

### Joint novelty claim

The joint claim is supported only if all six hypotheses reject under the
registered one-sided decision rule.

### Individual claims

Report individual claims using the registered Holm adjustment across the
six hypotheses. Do not infer that OC is novel merely because B-OC beats
a baseline; P-4a and P-4b specifically test whether OC adds value beyond
TR.

### Interpretation

  -----------------------------------------------------------------------
  Outcome                             Required interpretation
  ----------------------------------- -----------------------------------
  All six hypotheses reject           Containment benefit supported at
                                      `p_main` for the tested stack and
                                      configuration

  P-1 and P-2 reject, but P-4a or     The improvement may be attributable
  P-4b does not                       to the semaphore/budgets; the
                                      OC-based novelty claim fails under
                                      the registered rule

  P-4a/P-4b support OC, but P-1 or    OC may help within B, but
  P-2 does not                        superiority over the required
                                      baselines is not established

  P-3 fails                           No-attack overhead/non-inferiority
                                      guardrail fails

  Calibration Case A applies          Report the claim as conditional; do
                                      not represent the stress cell as
                                      the measured operating condition

  Power is below the registered       Inconclusive
  requirement                         

  A required gate fails               Do not make claims that depend on
                                      that gate
  -----------------------------------------------------------------------

## 10. Database Testbed Evaluation

Database-fencing claims require a separate PostgreSQL testbed
evaluation: - Pin the server version, output plugin, and decoding
options. - Verify commit-order and in-progress streaming settings for
the pinned version. - Run planted stale commits and legitimate late
completions through the real decoder. - Test checker restart/replay,
checkpoint crash consistency, slot lag, multiple gateways, multiple
bumps, mixed transactions, and all relevant data-table mutations. -
Confirm that audit/data correspondence covers every supported mutation
path.

Until the database checker passes its C1-equivalent, results that depend
on PostgreSQL fencing must be labeled **simulation-only**.

## 11. Final Evaluation Report

The final report should include:

1.  Executive conclusion: supported, unsupported, or inconclusive.
2.  Exact specification, code revision, configuration, and
    pre-registration hashes.
3.  Gate results G0-C1 through G0-C6, with evidence and any deviations.
4.  Baseline fidelity decision and all deviations from DRACO.
5.  Calibration sample sizes, failure rates, intervals, selected
    `p_main`, and case.
6.  Per-seed and aggregate `OE`, both OE components, `OE_norm`, and
    `S_L`.
7.  All six primary hypothesis results, effect estimates, uncertainty,
    adjusted individual claims, and power.
8.  Fault-sweep and zero-fault results, including crossover analysis.
9.  Simulation versus PostgreSQL testbed evidence, clearly separated.
10. Limitations, failed checks, unrun tests, inconclusive results, and
    follow-up work.

## 12. Evaluation Status Template

Use this table in each run report:

  ------------------------------------------------------------------------
  Item               Status            Evidence /        Notes
                                       artifact          
  ------------------ ----------------- ----------------- -----------------
  T1/T2 independent  Not run           ---               ---
  review                                                 

  T-D1 to T-D3       Not run           ---               ---

  T-D4 real-stage    Not run           ---               Requires testbed
  test                                                   

  Checker            Not run           ---               ---
  differential test                                      

  Planted V/L and    Not run           ---               Simulation and DB
  mutants                                                paths tracked
                                                         separately

  Baseline fidelity  Not run           ---               DRACO full text
                                                         still required

  Arm-ladder diff    Not run           ---               ---
  review                                                 

  Fault calibration  Not run           ---               ---

  Pre-registration   Not run           ---               ---
  frozen                                                 

  Confirmatory       Not run           ---               ---
  simulation                                             

  PostgreSQL testbed Not run           ---               ---
  checker                                                

  Final hypothesis   Not run           ---               ---
  decisions                                              
  ------------------------------------------------------------------------

**Rule:** Never replace "Not run" with "Pass" until the evidence exists
and is linked from the report.
