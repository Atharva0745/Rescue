# RESCUE

RESCUE is a **research prototype** exploring completion-backed admission
control, resource quarantine, and fencing for overloaded gateway workloads. It
is not connected to emergency services and is not production infrastructure.

## Current evidence and status

The authoritative phase-by-phase status, test commands, evidence limits, and
next workflow steps are in [`docs/phase.md`](docs/phase.md). In brief:

- The Python harness includes scheduling-bound checks, admission-policy
  models, a transaction checker, and a simulation experiment.
- The recorded 60-seed simulation did **not** support the registered joint
  claim. Its conclusions apply only to that simplified model.
- The database validator is a generated-stream/in-memory mock. No live
  PostgreSQL or logical-decoding test has been run.
- The Go gateway is an in-memory prototype. It does not persist state or
  inspect/terminate real cgroups, database backends, or provider effects.
- Superseded historical results remain in `config/` for provenance; do not use
  them as current evidence.

## Repository map

| Path | Purpose |
| --- | --- |
| [`harness/`](harness/) | Python models, research simulations, and checks |
| [`gateway/`](gateway/) | Go controller prototype, demo, benchmark, and package tests |
| [`config/`](config/) | Frozen configurations and historical/repaired result artifacts |
| [`results/`](results/) | Recorded Go timer-workload benchmark runs |
| [`docs/phase.md`](docs/phase.md) | **Authoritative workflow and completion status** |
| [`docs/project_guide.md`](docs/project_guide.md) | Plain-language explanation of the components and flow diagrams |
| [`docs/evaluation.md`](docs/evaluation.md) | Evaluation method and acceptance criteria |
| [`docs/new_br.md`](docs/new_br.md) | Target business requirements, not implementation status |
| [`docs/new_arhitecture.md`](docs/new_arhitecture.md) | Proposed architecture, not a claim of implementation |

## Install prerequisites

The validation suite uses Python's standard library and Go modules; there are
no Python packages or Go dependencies to install. Install Python 3.11 or 3.12
and Go 1.27.2 or newer, then open a new PowerShell window and confirm both are
on `PATH`:

```powershell
python --version
go version
```

On Windows, one option is:

```powershell
winget install --id Python.Python.3.12 --exact
winget install --id GoLang.Go --exact
```

The Go race detector on Windows additionally needs cgo and a C compiler. If
you want to run that check locally, install MSYS2, open its UCRT64 terminal,
and install GCC:

```sh
pacman -S --needed mingw-w64-ucrt-x86_64-gcc
```

Then set `CC` to the installed `gcc.exe` path and `CGO_ENABLED=1` in the
PowerShell session before validation:

```powershell
$env:CGO_ENABLED = "1"
$env:CC = "C:\msys64\ucrt64\bin\gcc.exe"
$env:Path = "C:\msys64\ucrt64\bin;$env:Path"
```

Adjust those paths if MSYS2 is installed elsewhere. The validation script
adds the compiler directory to the subprocess `PATH` when it locates GCC.
This optional compiler is not needed for regular Go tests or for the Linux CI
runner.

## Run local validation

From the repository root, run the complete validation script:

```powershell
python scripts\validate.py
```

It checks the supported Python and Go versions, runs each Python and Go check,
continues after individual test failures, and returns a failing exit code if
any check fails. It does not install software or change machine policy. It
finds Go on `PATH` or in the standard Windows install location. If you prefer,
the individual commands are listed in [`docs/phase.md`](docs/phase.md).

The scheduling command runs one million simulated instances and analytical
tightness/mutant checks. Its synthetic assumption-monitor exercise is not a
Linux cgroup test and does not satisfy live T-D4 infrastructure evidence.

The same checks are configured in [`.github/workflows/ci.yml`](.github/workflows/ci.yml).
If Windows Application Control blocks a generated Go test executable, do not
weaken or bypass that policy; use the CI run on its Linux runner to verify the
complete regular Go suite. A passing CI run validates software behavior in
those tests only; it does not complete the live testbed phases or establish
production guarantees.

## Local demo and benchmark

From `gateway`:

```powershell
go run ./cmd/rescue-demo
```

The benchmark instructions and interpretation limits are in
[`gateway/README.md`](gateway/README.md). Its workers perform timer-backed
stub work, not real emergency-report processing.

## Research integrity

Do not edit frozen registrations or overwrite historical results to make a
new run appear registered. Any changed simulator, workload, hypothesis, or
threshold requires a new explicitly versioned exploratory/registered
artifact and updated provenance. Never describe mock or simulation outcomes
as live PostgreSQL, cgroup, or deployed-system evidence.
