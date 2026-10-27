# RESCUE gateway prototype

This Go module includes a locally executable controller path for exercising
admission, bounded queueing, leases, worker completion, resource confirmation,
and quarantine. It is a research prototype; it is not connected to emergency
services and is not production infrastructure.

## Run the local demonstration

From this directory:

```powershell
go run ./cmd/rescue-demo
```

The command admits one new source into the sacrificial pool, dispatches the
work to a bounded worker pool, waits for the worker's completion confirmation,
and prints the task lifecycle snapshot as JSON.

## Measure the Go controller with a generated workload

To collect observations from the Go controller (not from the Python simulator),
run this from the `gateway` directory:

```powershell
go run ./cmd/rescue-bench `
  -duration=60s `
  -rate=100 `
  -workers=8 `
  -work=60ms `
  -deadline=500ms `
  -seed=20261020
```

The runner sends a seeded, Poisson-paced workload through `EnqueueWork`,
`DispatchNext`, real worker goroutines, and completion reconciliation. It
assigns an 80/20 legitimate/attacker label mix for analysis; those labels do
not alter controller policy. Worker work is a timer-backed stub, not a
database, HTTP service, or real emergency-report operation.

By default, output is placed in a new timestamped directory under
`../results/`. An explicit directory can be selected with `-out`. The runner
fails rather than overwrite an existing output directory. Each run contains:

- `requests.jsonl`: one observed outcome/timing record per offered request.
- `summary.json`: overall legitimate deadline success (`S_L`), legitimate
  first-contact deadline success (`S_F`), in-window goodput, latency
  percentiles, and counts.
- `run_manifest.json`: seed, fixed measurement window, workload settings,
  Go/OS/CPU provenance, Git revision and dirty-worktree status, and record/code
  hashes.
- `snapshots.jsonl`: 50 ms samples of sacrificial/protected worker occupancy,
  total and per-pool queue depth, blocked-front dispatch count, quarantine
  occupancy, and recorded versus instrumented-running capacity.

The printed `S_L` is deadline success for legitimate requests arriving in the
measurement window; a completion may occur after that window if it still meets
the request's own deadline. `in-window goodput` counts successful completions
whose completion timestamps fall inside the fixed measurement window. These
metrics intentionally have different time boundaries and should not be
interpreted as interchangeable.
`S_F` uses only harness-labeled legitimate requests marked as first-contact;
the generated legitimate/attacker labels remain independent of source tier.

To compare first-contact behavior with and without deliberate sacrificial
worker exhaustion, use the same seed and workload settings for both runs:

```powershell
go run ./cmd/rescue-bench `
  -duration=60s -rate=100 -workers=8 `
  -work=60ms -deadline=500ms -seed=20261021 `
  -first-contact `
  -out=../results/first-contact-open-seed-20261021

go run ./cmd/rescue-bench `
  -duration=60s -rate=100 -workers=8 `
  -work=60ms -deadline=500ms -seed=20261021 `
  -first-contact -saturate-sacrificial `
  -out=../results/first-contact-saturated-seed-20261021
```

Each pool has `workers` worker slots. In the saturated run, `workers` held
tasks occupy every sacrificial slot for the arrival window. First-contact
requests use distinct new source IDs, so the controller routes them to the
sacrificial pool; the separate normal pool is recorded as protected capacity.
The saturation tasks are reported in the manifest, not counted as offered
workload requests.

To test cross-pool head-of-line behavior separately, `-mixed-pools` alternates
new first-contact sources routed to sacrificial with an established source
routed to normal. Compare open and saturated runs with the same settings:

```powershell
go run ./cmd/rescue-bench `
  -duration=60s -rate=100 -workers=8 `
  -work=60ms -deadline=500ms -seed=20261021 `
  -mixed-pools `
  -out=../results/mixed-pools-open-seed-20261021

go run ./cmd/rescue-bench `
  -duration=60s -rate=100 -workers=8 `
  -work=60ms -deadline=500ms -seed=20261021 `
  -mixed-pools -saturate-sacrificial `
  -out=../results/mixed-pools-saturated-seed-20261021
```

Mixed-pool output includes per-pool queue depths and a cumulative count of
dispatches that bypassed a blocked front task to serve another pool. This tests
scheduling behavior; it does not grant first-contact requests access to the
normal pool or implement a reserved first-contact lane.

Run multiple seeds into distinct `-out` directories to check repeatability.
These are measurements of this Go controller prototype under the specified
local timer workload. They are not the registered paired baseline comparison,
do not include PostgreSQL or downstream-resource measurements, and cannot by
themselves support the confirmatory claims in `docs/evaluation.md`.

## Controller workflow

Call `EnqueueWork` with a `WorkRequest`, then call `DispatchNext`. Use
`WaitTask` to drive worker completion reconciliation. New sources are
automatically registered as probationary and forced into the sacrificial pool.
Every `WorkRequest` must declare `HandleManifestComplete: true`. This means
`RequiredHandles` is the complete resource manifest, including when it is
intentionally empty; work with an unknown/incomplete manifest is rejected.
For each non-worker resource in `RequiredHandles`, the caller supplies evidence
to `ConfirmHandle`. The controller accepts it only if the configured
`quarantine.HandleReleaseVerifier` authenticates that the authoritative
resource owner released that exact task/handle pair. Constructors without a
verifier fail closed for handle confirmation. The included demo has no external
handles and therefore does not need a verifier.

Quarantine reserves one slot by default as a **minimum guaranteed allowance**
for emergency-pool entries, not as a separate emergency quota. Emergency
entries can use any otherwise-free shared capacity, but total occupancy cannot
exceed `qmax`. Use `NewControllerWithHandleReleaseVerifier` to configure both
the reserve and trusted verifier; normal and sacrificial entries cannot consume
the reserved slots. Quarantine timestamps are assigned by the pool when an
entry is added.

Quarantine records and verifier integration are in-memory prototype components;
there is no crash recovery or built-in OS/database proof verifier. A production
deployment must provide a trusted authoritative verifier and durable quarantine
storage/reconciliation.

Cancellation and deadline expiry do not release a worker slot. The slot is
confirmed only when the handler returns and the worker emits a completion
event. The slot remains charged until the controller successfully reconciles
and acknowledges that event. Completion events have stable `CompletionID`
values and are delivered only through `PendingCompletions`; there is no
competing event channel. Polling returns the same unacknowledged event until it
is acknowledged, so reconciliation should be idempotent by `CompletionID`.
A handler that ignores its context therefore keeps its worker slot and source
charge held until it actually stops.

The bounded FIFO queue is the admission wait: requests may wait there while
workers are busy, and a full queue rejects immediately. A successful
`EnqueueWork` acknowledges queue admission, not task completion. The configured
queue capacity and per-source limit are authoritative; no unbounded wait is
performed.

## Controller process-cell execution

For command-backed work, create a delegated cgroup-v2 manager and construct the
controller with `NewControllerWithExecutionCells`. Set `WorkRequest.Command`
instead of `Work`; the command then runs in its own verified process cell, and
its worker-slot charge is reconciled only after the cgroup is empty. On a
termination or verification failure, the controller keeps the task charged in
quarantine. An operator can retry the termination ladder with
`StopProcessCell(taskID, ctx)`.

The controller's `NewController` constructor and `WorkRequest.Work` closure path
remain for local tests and the timer-based benchmark. They do not provide
process isolation and must not be used to claim cancellation-resistant work is
contained. Command-backed execution requires Linux cgroup v2; it is not
available on native Windows. WSL2 is suitable for development integration
tests, while confirmatory measurements require a controlled Linux testbed.

Consumers using `worker.Pool` directly must call `AcknowledgeCompletion` only
after reconciling the event. `WaitDrain` and `Shutdown` include unacknowledged
completion events and will not report a drained pool until they are acknowledged.
There is no completion dispatcher goroutine to block or outlive shutdown.

## Architecture work implemented in this prototype

The Go controller remains the single RESCUE reservation-policy path; the
historical A′/D and B1/B2/ZAC policies remain in the Python simulator and are
not gateway arms. Registered worker-pool capacities are now checked against the
controller's aggregate worker budget. The standalone pool policy also enforces
per-pool caps and protects the emergency floor from non-emergency reservations.

Fenced effect commits must use `effect.Outbox.MarkSent` with the claimant and
fence version. It validates the active claim and current scope fence atomically
and counts commits rejected for stale fences. `CanSend` is only a preflight
check; it is not a substitute for the commit-time validation. Controller
dispatch advances the effect-sink fence to the newly issued lease fence before
starting work and fails closed if those counters have diverged.

The in-gateway reverse auditor now reports observations whose resource ID is
unknown to its owner ledger, whose observed owner is empty, or whose owner
does not match, including resources still observed after the ledger records
their release. The controller registers handles at dispatch and records their
release in that ledger. It still audits only observations passed to it; it is
not the independent operating-system or database enumerator required by the
evaluation harness.

## Linux cgroup-v2 process-cell tests

`pkg/worker/cell` provides a Linux-only process execution-cell backend. It
creates cgroups only under a caller-supplied delegated cgroup-v2 directory,
places the child in its cgroup before user code executes, applies cooperative
SIGINT and SIGTERM grace periods, uses `cgroup.kill` as the final escalation,
and confirms both `cgroup.events` (`populated 0`) and an empty `cgroup.procs`
before reporting release. If atomic `CLONE_INTO_CGROUP` placement is denied,
the backend uses a ptrace exec-stop to move the process before resuming it.
Lifecycle events are available from `Cell.Events()`.

On WSL2, run tests from a systemd user scope with delegation. First verify
`/sys/fs/cgroup/user.slice/user-$(id -u).slice/user@$(id -u).service` exists
and is writable, then run:

```bash
cd /mnt/c/Users/hp/OneDrive/Desktop/Rescue-main/Rescue-main/gateway
systemd-run --user --scope --property=Delegate=yes \
  env RESCUE_CGROUP_ROOT="/sys/fs/cgroup/user.slice/user-$(id -u).slice/user@$(id -u).service" \
  go test ./pkg/worker/cell -v
```

The execution-cell package currently accepts an executable `CommandSpec`; it
does not convert the controller's existing Go closures into child processes,
and is not yet selected by `Controller`. The goroutine backend remains the
controller's execution path. The process cell currently verifies containment
and termination but does not configure CPU or memory limits. Those are explicit
integration and calibration tasks, not properties established by these tests.

## Prototype boundaries

- Intake receipts, source state, leases, outbox entries, and task records are
  in-memory and do not survive process restart.
- Worker completion events are also in-memory. The acknowledgement protocol
  prevents capacity from being returned before reconciliation during a live
  process, but it does not provide crash recovery or durable completion records.
- The controller's resource handles are supplied by the caller. The separate
  process-cell package creates and inspects Linux cgroups; neither package
  creates or inspects PostgreSQL backends, locks, or provider calls.
- Reverse audit compares supplied observations; it does not enumerate
  operating-system or database resources and is not independent of the
  controller.
- The effect outbox is not wired to a real provider or a persistent shared
  fence store.
- The default controller constructor still uses goroutines for its
  closure-backed test/benchmark path. Command-backed controller execution is
  integrated with the Linux process-cell package and retains the worker-slot
  charge until cgroup emptiness is verified.
- Process cells currently enforce containment and termination but do not
  configure CPU or memory limits. Process-backed workload packaging and
  resource-limit calibration remain required.
- The simulator, B1/B2 baselines, attacker search, equal-budget enforcement,
  seed split, and confirmatory statistical analysis remain unimplemented.
- Numeric limits are placeholders, and this prototype must not be connected to
  live emergency intake or dispatch systems.

## Validation

Run the package tests with:

```powershell
go test ./...
```

The integration tests exercise probationary pool placement, shared source-scope
caps, worker-completion release, quarantine for unconfirmed handles, and a
deadline-expired handler that continues running. On Windows systems with
application-control policy enabled, the generated Go test executable may be
blocked from running; compiling focused test binaries with `go test -c` still
checks that the tests and packages build.
