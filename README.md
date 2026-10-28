# RESCUE-4: Lease-Based Admission Control with Owner-Confirmed Release and Database Fencing

[![Python Version](https://img.shields.io/badge/python-3.11%20%7C%203.12-blue.svg)](https://www.python.org/)
[![Database Validation](https://img.shields.io/badge/Database%20Validation-Mock%20Only-lightgrey.svg)](config/database_testbed_report_mock.json)
[![Pre-Registration](https://img.shields.io/badge/Pre--Registration-Repaired%20v2-blue.svg)](config/preregistration_repaired.json)
[![Hypotheses Status](https://img.shields.io/badge/IUT%20Claim-Not%20Supported%20in%20Simulation-orange.svg)](config/confirmatory_results_repaired_confirmatory.json)

> **Evidence status:** The earlier `confirmatory_results.json`, preregistration,
> and Phase 9 report contain results from a simulator with arm-specific
> calibrated outcomes and synthetic pilot thresholds. They are historical and
> must not be treated as confirmatory evidence. The repaired simulation-only
> preregistration and 60-seed run are in `config/preregistration_repaired.json`
> and `config/confirmatory_results_repaired_confirmatory.json`. The registered
> joint claim was **not supported in the simulation** (P-1a p=1.0); this is not
> evidence about a deployed system. The database validator is an in-memory
> mock, not a live PostgreSQL integration test.

---

## 1. Overview

**RESCUE-4** is an architectural framework designed to eliminate orphaned-work exposure (OE) and prevent stale-write commits in staged, multi-tenant distributed transaction systems. In distributed architectures where executors operate under time-limited leases, standard systems release execution capacity immediately upon lease expiry (timed release, `TR`). When executors stall, hang, or experience silent partitions, timed release leads to severe capacity over-allocation, trailing CPU waste, and inconsistent database mutations ("stale writes").

RESCUE-4 addresses this vulnerability through three tightly integrated mechanisms:
1. **Owner-Confirmed Capacity Release (OC):** Executor capacity is only released after the lease owner explicitly confirms process cessation (cgroup termination), never upon lease timer expiration.
2. **Executor Quarantine & Capacity Charging:** Unresponsive executors that fail to confirm termination are transitioned into a `QUARANTINED` state where their resource capacity remains charged aFor RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the dFor RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.ocumentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.For RESCUE-4, I would investigate two things separately:

1. Does the code actually work correctly? Look for bugs, fake validation, hidden assumptions, and incorrect logic.
2. Does the project prove what it claims? A program can run successfully and still produce misleading research results.

Based on the RESCUE-4 documents you shared, I can identify several red flags and major weaknesses. However, I don't currently have the actual Python source files to inspect line by line, so I'll distinguish confirmed documentation problems from things that need code verification.

# 1. The biggest red flags I see

1\. The project documents contradict each other

High priority

One execution-plan document says Phase 9, analysis and reporting, is pending in its status summary, but its phase table marks Phase 9 completed. That is an inconsistency in the project's recorded state.&#x20;

Pasted markdown (2)(1).md



What to check: Does the report actually exist? Does it match the latest code, experiment results, and configuration? Are completion claims backed by timestamps and test logs?

2\. The architecture audit gives itself “FULL COMPLIANCE” everywhere

High priority

The document says the implementation satisfies all listed architectural requirements. But an audit table is not proof by itself.&#x20;

Pasted markdown(4).md



What to check: Can you reproduce each result from a clean checkout? Do the tests fail when you deliberately introduce a known bug? Are raw logs available, or only a report claiming success?

3\. The DRACO comparison may not be a fair comparison

The project labels policy D as “DRACO-inspired” if fidelity cannot be reproduced. That qualification matters. A simplified imitation may behave differently from the actual method it is being compared against.&#x20;

Pasted markdown(3).md



What to check: Compare the implementation with the original algorithm's paper and specification. Verify admission rules, thresholds, feedback signals, and tuning. Don't claim superiority over original DRACO if only an approximation was tested.

4\. The safety result depends on assumptions

RESCUE-4 assumes a particular system architecture and threat model. Its stated limitations include single-gateway scheduling bounds and non-Byzantine writers.

What to check: What happens if the gateway restarts, two owners disagree, a task cannot be stopped, or a worker keeps retrying a database write? A result under the tested assumptions is not proof of safety in every real deployment.

# 2. What to look for in the actual code

Think of code review as inspecting a restaurant's kitchen: don't just read the manager's report saying everything is clean. Inspect the food preparation, safety checks, and records yourself.

These are the files I would review first, in this order.

1\. `harness/dispatcher.py`

Highest priority: resource accounting

This simulates which tasks are running and how much capacity is available.

Look for:

- Capacity being released as soon as a lease expires, even when work is still running.
- A task being counted twice, or not counted at all.
- Quarantined workers accidentally becoming available again.
- Incorrect event ordering when completion and lease expiry happen at the same time.
- Time calculations that accidentally allow negative durations or impossible schedules.
- State not being restored correctly after a simulated crash.

Analogy: A restaurant has four chefs. One chef's booking expires, but the chef is still cooking. The system must not count that chef as free and assign the same capacity to another order.

2\. `harness/transaction_checker.py`

Highest priority: stale-write protection

This is intended to check whether database writes are valid.

Look for:

- A write being accepted without checking the current fencing token.
- The token check and data update happening in separate transactions.
- Old tokens being accepted after a restart or ownership change.
- Audit records and actual data updates becoming inconsistent.
- Duplicate database events being processed twice.
- A checker that validates only the final state and misses an invalid intermediate write.

Analogy: A former employee's access card should stop working after their permission is revoked. Checking the card only when they enter the building isn't enough if they can still use an old authorization to modify the records later.

3\. `harness/admission_policy.py`

High priority: fairness of the comparison

This implements the A′, D and B policies and the NS, TR and OC enforcement levels.

Look for:

- Policies that accidentally share logic they are supposed to differ on.
- One policy receiving extra information or privileges unavailable to others.
- Thresholds tuned specifically to make RESCUE-4 win.
- The OC arm receiving several protections at once, making it difficult to determine which protection caused the improvement.
- Configuration options that are declared but ignored.

Key question: Are you comparing the algorithms fairly, or giving your preferred algorithm an unfair advantage?

4\. `harness/confirmatory_simulator.py`

High priority: experimental validity

This runs the comparisons that support the project's conclusions.

Look for:

- Random seeds not being shared properly between paired comparisons.
- Failed runs silently excluded from the results.
- Results overwritten when the experiment is rerun.
- Hypotheses or thresholds changed after seeing results.
- Metrics calculated differently for different policies.
- The same underlying bug affecting every experiment arm.

Analogy: If you test two cars, both must face comparable road conditions. Giving one car dry roads and the other wet roads does not establish which car is better.

5\. `harness/scheduling_bound_test.py` and `harness/checker_differential_test.py`

Look for:

- Tests that exercise only normal cases instead of boundary conditions.
- Expected answers generated by the same logic being tested.
- Mutation tests that fail to detect deliberate bugs.
- Randomized tests that cannot be reproduced.
- Assertions that are disabled, never reached, or too weak to catch errors.

Passing one million tests is impressive only if those tests exercise meaningful failure cases and the expected answers are trustworthy.

# 3. Suspicious code patterns to search for

These are warning signs, not proof of a bug. Some uses are legitimate; what matters is the surrounding logic.

| Search for                                        | Why it deserves inspection                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------ |
| `except Exception: pass`                          | Errors may be silently ignored.                                          |
| `assert` used as the only production safety check | Python can disable assertions with optimization flags.                   |
| `random.seed(...)` or hard-coded seeds            | May cause repeated or unrepresentative trials if used incorrectly.       |
| `TODO`, `FIXME`, `NotImplementedError`            | May indicate unfinished paths.                                           |
| `return True` in validators                       | Could bypass real validation if reached too early.                       |
| `if test_mode`, `mock`, `fake`, `stub`            | Test behavior may differ from actual behavior.                           |
| `lease_expiry` or `timeout`                       | Check whether expiry is confused with proof that work stopped.           |
| `inc`, `epoch`, `fence`, `token`                  | Check that every protected write validates the current token atomically. |
| `except`, retry loops, `continue`                 | Check whether failures are swallowed or retries are unbounded.           |
| `overwrite`, `append`, output-file writes         | Check whether prior results or failed-run records are lost.              |

On Windows, you can search a local project folder from PowerShell:

```
Get-ChildItem .\harness -Recurse -Filter *.py |
  Select-String -Pattern 'except Exception|TODO|FIXME|NotImplementedError|lease_expiry|epoch|fence|token|test_mode'
```

This finds places worth reading; it does not automatically establish that they are flawed.

# 4. The major scientific and design flaws to investigate

Beyond individual bugs, these are the biggest questions that could weaken the entire project.

1

The implementation may not match the claims

If the documentation says “PostgreSQL fencing was validated,” verify that the test exercises the real database transaction path and not merely a Python model of it.

2

The tests may share the same mistake

Two implementations agreeing is weak evidence if both copied the same incorrect rule. The reference oracle should be independently implemented, and mutation tests should prove it catches known mistakes.

3

The experiment may be too narrow

Twenty-five paired seeds can be useful, but the adequacy of that sample depends on variability, effect size and the statistical test. More seeds alone do not fix unrealistic workloads.

4

The chosen metric may hide trade-offs

Lower orphaned-work exposure is not enough by itself. Check useful completion, latency, rejected work, resource usage, and behavior when owner confirmation never arrives. A system that prevents bad writes by refusing nearly all work is not necessarily a good system.

5

The result may only hold under the tested fault model

If workers can stall but cannot maliciously bypass authorization, the result does not establish safety against malicious workers. If failover is out of scope, don't generalize the single-gateway result to a distributed multi-gateway deployment.

6

Too many protections may be bundled together

OC combines owner confirmation, quarantine, resource controls and database fencing. The nine-arm matrix helps separate some effects, but inspect whether the experiment can identify which component contributes to each improvement.

# 5. What I would do first

My review order would be:

1. Reproduce the reported results from the actual source code and a clean environment.
2. Audit the database fencing transaction — an accepted stale write is a core safety failure.
3. Audit lease expiry and capacity accounting — an accounting bug could invalidate the main result.
4. Inspect the experiment harness for unfair comparisons, hidden exclusions and altered configurations.
5. Challenge the tests with deliberate faults: skip token validation, release capacity early, duplicate an event, or let a worker continue after its lease expires. Confirm that the relevant tests fail.
6. Reconcile the documentation with the actual artifacts, test logs and Git commit used to produce them.

## My current verdict

The documentation raises at least one concrete consistency issue and several important questions about verification and experimental validity. It does not, by itself, prove that the implementation is defective or dishonest.

To identify genuinely suspicious code and confirm the major flaws, upload the RESCUE-4 project ZIP (or at least the `harness` folder plus `config` and test files). I can then review the real source, point to specific files and lines, explain each problem in simple language, and classify it as a confirmed bug, a likely bug, a design weakness, or something that merely needs more evidence.gainst the gateway's budget ($K_{\text{avail}} = K_s - |quarantined_s|$), bounding orphan interference.
3. **PostgreSQL Preventative Database Fencing:** Write authority requires in-transaction monotonic token verification (`SELECT ... FOR SHARE`) executed via `SECURITY DEFINER` fenced procedures, serialized against conflict bumps (`UPDATE fence`) and backed by trigger backstops to ensure 0 stale writes are committed.

---

## 2. Formal Lease & Ownership State Machine (§3)

The executor lifecycle is governed by a 7-state finite state machine with strict capacity and authority invariants:

| From | Event | To | Capacity / Authority Action |
| :--- | :--- | :--- | :--- |
| `LEASED` | complete | `FREE` | Normal release to pool |
| `LEASED` | lease expires | `EXPIRED` | Capacity held; cancellation signal sent |
| `EXPIRED` | owner confirms stop | `FENCED_CONFIRMED` | Release after fence verification |
| `EXPIRED` | no confirm by $T_{\text{cancel}}$ | `KILLING` | Capacity held; fence bump initiated |
| `KILLING` | kill confirmed | `FENCED_CONFIRMED` | Capacity released |
| `KILLING` | no confirm by $T_{\text{kill}}$ | `QUARANTINED` | $K_{\text{avail}} \leftarrow K_{\text{avail}} - 1$; capacity stays charged |
| `QUARANTINED` | late confirm / node reset | `FENCED_CONFIRMED` | Capacity released; $K_{\text{avail}} \leftarrow K_{\text{avail}} + 1$ |

### Core Protocol Rules:
- **R1:** Write authority transfers only when the bump `inc + 1` has committed. A new owner never writes before then.
- **R2:** Capacity is released only on completion or confirmed stop, never at expiry or when cancel is sent.
- **R3:** Quarantined slots keep their charge, bounding orphan resource usage by $q_{\max}$.
- **R4:** Before the bump, valid late writes from the old owner are accepted; after the bump, all writes fail.

---

## 3. 3×3 Arm Evaluation Matrix

RESCUE-4 evaluates the factorial combination of 3 admission policies against 3 enforcement levels:

| Admission Policy | NS (No Semaphore) | TR (Timed Release) | OC (Owner-Confirmed) |
| :--- | :---: | :---: | :---: |
| **A′ (Adaptive Limiter)** | A′-NS | A′-TR (Baseline) | A′-OC |
| **D (DRACO-inspired)** | D-NS | D-TR (Baseline) | D-OC (Guardrail) |
| **B (RESCUE-4 Reservation)** | B-NS | B-TR (Ablation) | **B-OC (RESCUE-4)** |

---

## 4. Confirmatory Hypotheses & Results

All 6 primary hypotheses are evaluated under an Intersection-Union Test (IUT) at $p_{\text{main}} = 0.05$ across 25 paired seed blocks. Monotonicity is preserved using a running-max Holm adjustment, and exact sign-flip p-values ($2^{25}$ evaluations) are calculated via meet-in-the-middle permutation tests.

| Hypothesis | Comparison | Metric | Threshold ($\delta$) | Observed Mean Diff (SD) | Exact Raw $p$ | Holm Adjusted $p$ | Decision |
| :--- | :---: | :---: | :---: | :---: | :---: | :---: | :---: |
| **P-1a** | D-TR vs B-OC | $\text{OE}_{\text{norm}}$ | $> 0.0328$ | $0.0686$ ($0.0059$) | $2.98 \times 10^{-8}$ | $1.79 \times 10^{-7}$ | **REJECT $H_0$** |
| **P-1b** | A′-TR vs B-OC | $\text{OE}_{\text{norm}}$ | $> 0.0328$ | $0.1047$ ($0.0058$) | $2.98 \times 10^{-8}$ | $1.79 \times 10^{-7}$ | **REJECT $H_0$** |
| **P-2** | B-OC vs D-TR | $S_L$ | $\ge 0.0138$ | $0.0342$ ($0.0039$) | $2.98 \times 10^{-8}$ | $1.79 \times 10^{-7}$ | **REJECT $H_0$** |
| **P-3** | B-OC vs D-OC | $S_L$ | $\ge -0.0300$ | $0.0000$ ($0.0028$) | $2.98 \times 10^{-8}$ | $1.79 \times 10^{-7}$ | **REJECT $H_0$** |
| **P-4a** | B-TR vs B-OC | $\text{OE}_{\text{norm}}$ | $> 0.0328$ | $0.0656$ ($0.0055$) | $2.98 \times 10^{-8}$ | $1.79 \times 10^{-7}$ | **REJECT $H_0$** |
| **P-4b** | B-OC vs B-TR | $S_L$ | $\ge 0.0138$ | $0.0275$ ($0.0039$) | $2.98 \times 10^{-8}$ | $1.79 \times 10^{-7}$ | **REJECT $H_0$** |

**Historical result: invalidated.** Those values came from the pre-repair
simulator and synthetic pilot thresholds. They do not establish the registered
claims. See `config/confirmatory_results_repaired_confirmatory.json` for the
new simulation-only test; its joint claim was not supported.

---

## 5. Reproduction & Test Commands

Run the test suites in PowerShell / Terminal:

```bash
# 1. Independent Statistical Audit (Exact Sign-Flip & Scipy Agreement)
py harness/audit_confirmatory.py

# 2. Scheduling Bounds Suite (T-D1, T-D2, T-D3, & Staged T-D4 with A1–A9 Monitors)
py harness/scheduling_bound_test.py

# 3. Baseline Fidelity & Graded Shedding Curve Suite (G0-C2, G0-C3)
py harness/baseline_ladder_test.py

# 4. Lease & Ownership FSM Model Checker (Rules R1–R4 Invariant Audit)
py harness/lease_ownership_fsm.py

# 5. Mock parser and fencing-model checks (not a live PostgreSQL test)
py harness/database_testbed_validator.py

# 6. Generate the repaired simulation-only report
py harness/technical_report_generator.py
```

---

## 6. Exit Gate Compliance Matrix

| Gate / Requirement | Status | Evidence Artifact | Notes |
| :--- | :---: | :--- | :--- |
| **G0-C1: Checker Differential** | **PASSED** | `harness/checker_differential_test.py` | 100k tx; 0 disagreements vs ReferenceOracle; M1--M4 caught |
| **G0-C2: Baseline Fidelity** | **PASSED** | `harness/baseline_ladder_test.py` | Graded curve (20–500ms); relabeled "D (DRACO-inspired)" |
| **G0-C3: Arm-Ladder Diff** | **PASSED** | `harness/baseline_ladder_test.py` | Exact factor diffs across 9 arms; 0 unintended leakage |
| **G0-C4: Fault Calibration** | **PASSED** | `config/calibration_report.json` | 4,500 trials; CASE_B; $p_{\text{main}} = 0.05$ frozen |
| **G0-C5: Scheduling Bounds** | **PASSED** | `harness/scheduling_bound_test.py` | Passed on T-D1–D3 (simulation); staged T-D4 verified under A1–A9 |
| **G0-C6: Pre-Registration** | **REPAIRED REGISTRATION** | `config/preregistration_repaired.json` | 60 independent seeds; hash `089cee59cb6dd9780ec0a2c5b6b5d9c2b6f650341eb5ce568ef01b35df66d131` |
| **C1-Equivalent Testbed** | **NOT RUN** | `config/database_testbed_report_mock.json` | In-memory mock only; no PostgreSQL integration test |
| **IUT Novelty Claim** | **NOT SUPPORTED IN SIMULATION** | `config/confirmatory_results_repaired_confirmatory.json` | P-1a did not reject; this does not establish a real-system result |

---

## 7. Scope & Threats to Validity

1. **DRACO Naming Boundary:** Baseline D is relabeled strictly as "D (DRACO-inspired)" because full author code is unreleased. Production eBPF hooks and Paxos consensus were omitted in favor of gateway-level selective dropping. Claims state that RESCUE-4 outperforms a DRACO-inspired policy.
2. **Deterministic Scheduling Bounds Scope:** Bounds $T_1$ and $T_2$ hold strictly under assumptions A1–A9. Unmodeled executor loss and adaptive LIFO scheduling are explicitly out of scope.
3. **Database Safety Separation:** Database write safety is enforced preventatively by PostgreSQL 16.3 role isolation, in-transaction `SELECT ... FOR SHARE` verification, and bump conflict serialization (`fenced_write()`). Logical decoding operates as a detective control. Fencing claims in the discrete-event simulator are designated as "SIMULATION-ONLY".
