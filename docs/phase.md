# RESCUE workflow and status

**Authority:** This is the single source of truth for current phase status and
the order of remaining work. Requirements describe the target; architecture
documents describe a proposal; neither implies implementation. Historical
reports and generated artifacts are provenance, not status.

**Project boundary:** RESCUE is a research prototype. It is not a production
emergency-service system. No live PostgreSQL, Linux cgroup, or downstream
provider integration has been demonstrated.

## Status at a glance

| Area | Status | Evidence and interpretation |
| --- | --- | --- |
| Scope, assumptions, and configuration (Phases 0–1) | Recorded complete; not independently re-reviewed in this pass | See [`context.md`](context.md) and the versioned configuration. |
| Scheduling bounds (Phase 2) | **Simulation checks verified locally** | One million generated instances: zero T1/T2 violations; tightness and three mutants checked. The 50-trial assumption-monitor routine is synthetic and is not real T-D4 evidence. |
| Transaction checker (Phase 3) | **Verified locally** | 100,000 histories: zero disagreements with the reference oracle; planted stale writes and mutants detected. This validates the Python model only. |
| Baseline and arm ladder (Phase 4) | **Mechanism checks only** | The policy is DRACO-inspired, not a verified reproduction of the original algorithm. Historical hard-coded sensitivity values are invalid; no comparative performance conclusion is established. |
| Calibration and registration (Phases 5–6) | Historical/simulation-only artifacts; old registration superseded | The fault calibrator uses seeded synthetic trials, not measured cancellation attempts. The repaired registration is based on a simulator pilot and is limited to simulation. |
| Confirmatory simulation (Phase 7) | **Completed, simulation-only; joint claim not supported** | The repaired artifact records 60 seed blocks and P-1a `p = 1.0`. Do not interpret this as a deployed-system result. |
| Database validation (Phase 8) | **Not started against a live database** | Both database reports are mock-only. There is no real PostgreSQL server, WAL stream, replication slot, or crash/restart test in this evidence. |
| Technical reporting (Phase 9) | **Simulation-only summary is reportable; final system claim is blocked** | Any report must preserve the unsupported result and state that Phase 8 is not established. Historical reports are superseded. |
| Go gateway | **In-memory prototype; local software checks pass** | The latest full validation ran package tests, race tests, and `go vet` successfully. No durable recovery or real resource integration. |

## Workflow rules

1. Keep the evaluation method in [`evaluation.md`](evaluation.md), the target
   requirements in [`new_br.md`](new_br.md), and the proposed design in
   [`new_arhitecture.md`](new_arhitecture.md). Report execution status only
   here.
2. Label evidence by environment: analytical, simulated, mock, or live
   integration. Passing one category cannot satisfy another category’s gate.
3. A phase is complete only when its exit evidence exists and is reproducible.
   A written plan, generated mock stream, or successful unit test is not proof
   of a live-system property.
4. Preserve superseded artifacts. Do not silently replace them or cite them
   without their status and limitation.
5. Do not rerun the repaired registration as if it were unchanged after
   modifying the simulator or registration inputs. Create a new version and
   record its source hashes and status.

## Setup and validation

The Python checks use only the standard library; the Go module has no external
dependencies. Use Python 3.11 or 3.12 and Go 1.27.2 or newer. The repository
root README documents Windows installation options and the optional C compiler
needed to run the Go race detector locally on Windows.

The recommended local entry point from the repository root is:

```powershell
python scripts\validate.py
```

It checks tool versions, runs every check below, and reports failures without
stopping at the first failed test. It uses only Python's standard library,
does not install dependencies, and does not change machine policy.

To run checks individually from the repository root:

```powershell
python -m compileall -q harness scripts
python -m unittest discover -s harness -p "*_test.py"
python harness\checker_differential_test.py
python harness\scheduling_bound_test.py
python harness\baseline_ladder_test.py
```

Run the Go checks from `gateway`:

```powershell
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
```

These commands are configured in [`.github/workflows/ci.yml`](../.github/workflows/ci.yml).
The latest full local validation on 2026-10-24 used Python 3.12.10 and Go
1.27.3:

- Python compile check passed; unit discovery ran 7 tests successfully.
- Checker differential suite: 100,000 histories, zero disagreements; planted
  stale writes and mutants detected.
- Scheduling suite: 1,000,000 simulated cases, zero T1/T2 violations; tight
  cases and three mutants checked.
- Baseline suite: policy/arm behavior checks only; no reproduction or
  performance claim.
- Go vet and Go formatting checks passed.
- Go package tests: `go test -count=1 ./...` passed for all packages.
- Go race tests: `go test -race -count=1 ./...` passed for all packages after
  configuring MSYS2 UCRT64 GCC and adding its `bin` directory to the test
  subprocess `PATH`.
- Go static analysis: `go vet ./...` passed.
- Go formatting check passed for all 28 Go files; `git diff --check` passed.

Python 3.11 still requires its CI matrix run. The local checks validate the
prototype and simulated paths only; they do not establish live database,
cgroup, or deployment behavior.

CI passing does not mean Phases 7–9 have passed and does not establish live
database, cgroup, or deployment behavior.

## Remaining work, in dependency order

### 1. Keep the research workflow reproducible

- Require the Python and Go CI jobs to pass on the supported versions.
- For every experiment, retain the registration, source/configuration hashes,
  seed list, raw per-seed output, and report as separate immutable artifacts.
- Re-run statistical analysis only from the registered artifact; do not
  promote exploratory results to confirmatory status.

### 2. Define and build the real testbed (Phase 8)

This is the next infrastructure gate and is **not implemented in this
repository**. Before claiming it complete:

- Select and provision an isolated Linux/PostgreSQL environment matching a
  newly recorded version/configuration.
- Implement database schema and transactional fencing against that database;
  exercise the actual logical-decoding output and replication-slot lifecycle.
- Persist checker checkpoints and prove crash/restart and replay behavior.
- Integrate isolated task processes and resource handles; independently verify
  release and audit runtime resources against the ledger.
- Record setup instructions, test output, versions, failures, and limitations.

Do not use the Python mock validator or the Go in-memory components as
substitutes for these checks.

### 3. Publish a bounded final report (Phase 9)

After Phase 8 evidence exists, update the report with separate findings for
the model, software tests, and live integration. Until then, the only valid
experiment conclusion is that the registered joint claim was not supported
in the repaired simulation model; no deployed-system conclusion is available.

### 4. Production deployment remains out of scope

The Go prototype has no durable state/recovery, real intake API, resource
enumeration/termination, or provider integration. A production project would
need a separate threat model, operational design, safety review, privacy
review, provider contracts, deployment and incident procedures, and
independent validation. Do not treat completion of this research workflow as
production readiness.

## Artifact interpretation

- [`confirmatory_results_repaired_confirmatory.json`](../config/confirmatory_results_repaired_confirmatory.json):
  repaired 60-seed simulation result; joint claim unsupported.
- [`confirmatory_pilot_repaired.json`](../config/confirmatory_pilot_repaired.json):
  exploratory simulator pilot; not confirmatory evidence.
- [`database_testbed_report_mock.json`](../config/database_testbed_report_mock.json):
  in-memory mock only.
- [`database_testbed_report.json`](../config/database_testbed_report.json):
  explicitly invalidated historical mock report.
- [`technical_report_manifest.json`](../config/technical_report_manifest.json):
  superseded report manifest; do not cite its positive claim.
