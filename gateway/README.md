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

Consumers using `worker.Pool` directly must call `AcknowledgeCompletion` only
after reconciling the event. `WaitDrain` and `Shutdown` include unacknowledged
completion events and will not report a drained pool until they are acknowledged.
There is no completion dispatcher goroutine to block or outlive shutdown.

## Prototype boundaries

- Intake receipts, source state, leases, outbox entries, and task records are
  in-memory and do not survive process restart.
- Worker completion events are also in-memory. The acknowledgement protocol
  prevents capacity from being returned before reconciliation during a live
  process, but it does not provide crash recovery or durable completion records.
- Resource handles are supplied by the caller; this module does not create or
  inspect Linux cgroups, PostgreSQL backends, locks, or provider calls.
- Reverse audit currently compares supplied observations; it does not enumerate
  operating-system or database resources.
- The effect outbox is not wired to a real provider or a persistent shared
  fence store.
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
