package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rescue-gateway/gateway/pkg/controller"
	"github.com/rescue-gateway/gateway/pkg/metrics"
	"github.com/rescue-gateway/gateway/pkg/source"
	"github.com/rescue-gateway/gateway/pkg/task"
)

type options struct {
	duration            time.Duration
	rate                float64
	workers             int
	work                time.Duration
	deadline            time.Duration
	seed                int64
	outDir              string
	firstContact        bool
	mixedPools          bool
	saturateSacrificial bool
	zombieFraction      float64
	zombieHangFactor    float64
	zombieAllTraffic    bool
	baselineMode        bool
}

type requestObservation struct {
	id           string
	sourceID     string
	arrivedAt    time.Time
	deadline     time.Time
	workDuration time.Duration
	legitimate   bool
	firstContact bool
	admitted     bool
	rejectReason string

	mu        sync.Mutex
	completed time.Time
	workErr   error
}

type runManifest struct {
	RunType               string                    `json:"run_type"`
	Interpretation        string                    `json:"interpretation"`
	Seed                  int64                     `json:"seed"`
	ArrivalRatePerSecond  float64                   `json:"arrival_rate_per_second"`
	MeasurementWindow     metrics.MeasurementWindow `json:"measurement_window"`
	Workers               int                       `json:"workers"`
	WorkersPerPool        int                       `json:"workers_per_pool"`
	FirstContactSources   bool                      `json:"first_contact_sources"`
	MixedPoolWorkload     bool                      `json:"mixed_pool_workload"`
	SacrificialSaturated  bool                      `json:"sacrificial_pool_saturated_during_measurement"`
	SaturationTasks       int                       `json:"saturation_tasks"`
	ZombieFraction        float64                   `json:"zombie_fraction"`
	ZombieHangFactor      float64                   `json:"zombie_hang_factor"`
	BaselineMode          bool                      `json:"baseline_timed_release_mode"`
	ProtectedCapacity     int                       `json:"protected_worker_capacity"`
	WorkDuration          time.Duration             `json:"work_duration_ns"`
	Deadline              time.Duration             `json:"deadline_ns"`
	RequestsOffered       int                       `json:"requests_offered"`
	RequestsAdmitted      int                       `json:"requests_admitted"`
	RequestsRejected      int                       `json:"requests_rejected"`
	HandlerReturns        int                       `json:"handler_returns"`
	SuccessfulCompletions int                       `json:"successful_completions"`
	GoVersion             string                    `json:"go_version"`
	OS                    string                    `json:"os"`
	Architecture          string                    `json:"architecture"`
	CPUCount              int                       `json:"cpu_count"`
	GitRevision           string                    `json:"git_revision"`
	GitWorktreeDirty      bool                      `json:"git_worktree_dirty"`
	CodeSHA256            string                    `json:"code_sha256"`
	RecordsSHA256         string                    `json:"records_sha256"`
	Summary               metrics.Summary           `json:"summary"`
}

type benchmarkSnapshot struct {
	Timestamp              time.Time      `json:"timestamp"`
	Offered                int64          `json:"offered"`
	Admitted               int64          `json:"admitted"`
	Rejected               int64          `json:"rejected"`
	SacrificialActive      int            `json:"sacrificial_active"`
	SacrificialFree        int            `json:"sacrificial_free"`
	ProtectedActive        int            `json:"protected_active"`
	ProtectedFree          int            `json:"protected_free"`
	ActualRunningHandlers  int64          `json:"actual_running_handlers"`
	QueueLength            int            `json:"queue_length"`
	QueueCapacity          int            `json:"queue_capacity"`
	QueueDepthsByPool      map[string]int `json:"queue_depths_by_pool"`
	BlockedFrontDispatches uint64         `json:"blocked_front_dispatches"`
	QuarantineCount        int            `json:"quarantine_count"`
	RecordedFreeWorkers    int            `json:"recorded_free_workers"`
	ActualFreeWorkers      int64          `json:"actual_free_workers"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	opts := options{}
	flag.DurationVar(&opts.duration, "duration", 10*time.Second, "fixed request-arrival measurement window")
	flag.Float64Var(&opts.rate, "rate", 100, "mean offered arrivals per second (Poisson process)")
	flag.IntVar(&opts.workers, "workers", 8, "number of real Go worker goroutines")
	flag.DurationVar(&opts.work, "work", 60*time.Millisecond, "work duration before small seeded jitter")
	flag.DurationVar(&opts.deadline, "deadline", 500*time.Millisecond, "per-request deadline")
	flag.Int64Var(&opts.seed, "seed", 20261020, "seed for request-class, arrival, and work-duration draws")
	flag.StringVar(&opts.outDir, "out", "", "output directory (default: ../results/go-gateway-<UTC timestamp>)")
	flag.BoolVar(&opts.firstContact, "first-contact", false, "give each offered request a new source, routing it to sacrificial")
	flag.BoolVar(&opts.mixedPools, "mixed-pools", false, "alternate first-contact sacrificial requests with established normal-pool requests")
	flag.BoolVar(&opts.saturateSacrificial, "saturate-sacrificial", false, "occupy every sacrificial worker for the full measurement window")
	flag.Float64Var(&opts.zombieFraction, "zombie-fraction", 0, "fraction of attacker requests that resist cancellation and hang (0.0 to 1.0)")
	flag.Float64Var(&opts.zombieHangFactor, "zombie-hang-factor", 3.0, "multiplier of deadline that zombie tasks continue running past deadline")
	flag.BoolVar(&opts.zombieAllTraffic, "zombie-all-traffic", false, "apply zombie cancellation-resistance to legitimate tasks as well")
	flag.BoolVar(&opts.baselineMode, "baseline-timed-release", false, "run in Timed Release baseline mode (immediately releases capacity on timeout)")
	flag.Parse()

	if opts.duration <= 0 || opts.rate <= 0 || opts.workers <= 0 || opts.work <= 0 || opts.deadline <= 0 {
		return fmt.Errorf("duration, rate, workers, work, and deadline must all be positive")
	}
	if opts.firstContact && opts.mixedPools {
		return fmt.Errorf("-first-contact and -mixed-pools are mutually exclusive")
	}
	if opts.outDir == "" {
		opts.outDir = filepath.Join("..", "results", "go-gateway-"+time.Now().UTC().Format("20060102T150405.000000000Z"))
	}

	const sourceCapacity = 100000
	gateway := controller.NewController(sourceCapacity, sourceCapacity, opts.workers*4)
	if opts.baselineMode {
		gateway.SetTimedReleaseMode(true)
	}
	gateway.Source = source.NewRegistry(map[string]int{
		"group":  sourceCapacity,
		"asn":    sourceCapacity,
		"region": sourceCapacity,
		"global": sourceCapacity,
	})
	if err := gateway.RegisterPool("normal", opts.workers); err != nil {
		return fmt.Errorf("register normal worker pool: %w", err)
	}
	if err := gateway.RegisterPool("sacrificial", opts.workers); err != nil {
		return fmt.Errorf("register sacrificial worker pool: %w", err)
	}
	gateway.Source.RegisterSource("benchmark-source", "benchmark-group", "benchmark-asn", "benchmark-region")
	gateway.Source.Promote("benchmark-source", source.TierNormal, "normal")

	var saturationRelease chan struct{}
	var releaseSaturationOnce sync.Once
	releaseSaturation := func() {
		if saturationRelease != nil {
			releaseSaturationOnce.Do(func() { close(saturationRelease) })
		}
	}
	defer releaseSaturation()
	saturationStarted := make(chan struct{}, opts.workers)
	saturationTasks := 0
	var actualRunning atomic.Int64
	if opts.saturateSacrificial {
		saturationRelease = make(chan struct{})
		for i := 0; i < opts.workers; i++ {
			taskID := fmt.Sprintf("saturation-%04d", i)
			sourceID := fmt.Sprintf("saturation-source-%04d", i)
			if err := gateway.EnqueueWork(controller.WorkRequest{
				TaskID: taskID, SourceID: sourceID, Pool: "sacrificial",
				HandleManifestComplete: true,
				Work: func(context.Context) error {
					actualRunning.Add(1)
					defer actualRunning.Add(-1)
					saturationStarted <- struct{}{}
					<-saturationRelease
					return nil
				},
			}); err != nil {
				return fmt.Errorf("enqueue sacrificial saturation task %s: %w", taskID, err)
			}
			if _, err := gateway.DispatchNext(); err != nil {
				return fmt.Errorf("dispatch sacrificial saturation task %s: %w", taskID, err)
			}
			saturationTasks++
		}
		for i := 0; i < saturationTasks; i++ {
			<-saturationStarted
		}
	}

	collector := metrics.NewCollector("rescue-go-controller", "go-bench-"+time.Now().UTC().Format("20060102T150405Z"), opts.seed)
	windowStart := time.Now()
	window := metrics.MeasurementWindow{Start: windowStart, End: windowStart.Add(opts.duration)}
	if err := collector.SetMeasurementWindow(window); err != nil {
		return fmt.Errorf("configure measurement window: %w", err)
	}

	rng := rand.New(rand.NewSource(opts.seed))
	requests := make([]*requestObservation, 0, int(opts.rate*opts.duration.Seconds()*1.2)+1)
	stopDispatch := make(chan struct{})
	dispatchDone := make(chan struct{})
	defer func() {
		close(stopDispatch)
		<-dispatchDone
	}()
	dispatchErrors := make(chan error, 1)
	go dispatchLoop(gateway, stopDispatch, dispatchDone, dispatchErrors)
	var offeredCount, admittedCount, rejectedCount atomic.Int64
	var snapshotMu sync.Mutex
	snapshots := make([]benchmarkSnapshot, 0, int(opts.duration/time.Second)*20+1)
	stopSnapshots := make(chan struct{})
	snapshotDone := make(chan struct{})
	var stopSnapshotsOnce sync.Once
	stopSnapshotCollection := func() {
		stopSnapshotsOnce.Do(func() { close(stopSnapshots) })
		<-snapshotDone
	}
	snapshotErrors := make(chan error, 1)
	go collectSnapshots(gateway, &actualRunning, &offeredCount, &admittedCount, &rejectedCount,
		stopSnapshots, snapshotDone, &snapshotMu, &snapshots, collector, snapshotErrors)
	defer stopSnapshotCollection()

	fmt.Printf("Running real Go controller workload: duration=%s rate=%.1f/s workers/pool=%d work=%s deadline=%s seed=%d first-contact=%t mixed-pools=%t sacrificial-saturated=%t\n",
		opts.duration, opts.rate, opts.workers, opts.work, opts.deadline, opts.seed, opts.firstContact, opts.mixedPools, opts.saturateSacrificial)
	fmt.Printf("Measurement window: %s to %s\n", window.Start.Format(time.RFC3339Nano), window.End.Format(time.RFC3339Nano))

	nextArrival := window.Start
	sequence := 0
	for nextArrival.Before(window.End) {
		wait := time.Until(nextArrival)
		if wait > 0 {
			time.Sleep(wait)
		}
		if !time.Now().Before(window.End) {
			break
		}
		sequence++
		isLegitimate := rng.Float64() < 0.8
		workDuration := opts.work + time.Duration(rng.Int63n(int64(opts.work/2)+1))
		arrivedAt := time.Now()
		requestDeadline := arrivedAt.Add(opts.deadline)
		sourceID := "benchmark-source"
		isFirstContact := opts.firstContact || (opts.mixedPools && sequence%2 == 1)
		if isFirstContact {
			sourceID = fmt.Sprintf("first-contact-%08d", sequence)
		}
		isZombie := false
		if opts.zombieFraction > 0 {
			if opts.zombieAllTraffic || !isLegitimate {
				isZombie = rng.Float64() < opts.zombieFraction
			}
		}
		if isZombie {
			// A cancellation-resistant zombie task attempts to run longer than the deadline
			deadlineDuration := requestDeadline.Sub(arrivedAt)
			workDuration = deadlineDuration + time.Duration(float64(deadlineDuration)*opts.zombieHangFactor)
		}
		observation := &requestObservation{
			id:           fmt.Sprintf("request-%08d", sequence),
			sourceID:     sourceID,
			arrivedAt:    arrivedAt,
			deadline:     requestDeadline,
			workDuration: workDuration,
			legitimate:   isLegitimate,
			firstContact: isFirstContact,
		}
		requests = append(requests, observation)
		offeredCount.Add(1)
		if err := gateway.EnqueueWork(controller.WorkRequest{
			TaskID:                 observation.id,
			SourceID:               observation.sourceID,
			SourceGroup:            "benchmark-group",
			ASN:                    "benchmark-asn",
			Region:                 "benchmark-region",
			Pool:                   "normal",
			Deadline:               observation.deadline,
			HandleManifestComplete: true,
			Work: func(ctx context.Context) error {
				actualRunning.Add(1)
				defer actualRunning.Add(-1)
				timer := time.NewTimer(workDuration)
				defer timer.Stop()
				var workErr error
				select {
				case <-timer.C:
				case <-ctx.Done():
					if isZombie {
						hangMultiplier := opts.zombieHangFactor
						if hangMultiplier <= 0 {
							hangMultiplier = 2.0
						}
						deadlineDuration := observation.deadline.Sub(observation.arrivedAt)
						hangDuration := time.Duration(float64(deadlineDuration) * hangMultiplier)
						time.Sleep(hangDuration)
						workErr = ctx.Err()
					} else {
						workErr = ctx.Err()
					}
				}
				observation.mu.Lock()
				observation.completed = time.Now()
				observation.workErr = workErr
				observation.mu.Unlock()
				return workErr
			},
		}); err != nil {
			observation.rejectReason = err.Error()
			rejectedCount.Add(1)
		} else {
			observation.admitted = true
			admittedCount.Add(1)
		}
		nextArrival = nextArrival.Add(time.Duration(rng.ExpFloat64() / opts.rate * float64(time.Second)))
	}

	releaseSaturation()
	drainDeadline := time.Now().Add(opts.deadline + 5*time.Second)
	for time.Now().Before(drainDeadline) {
		select {
		case err := <-dispatchErrors:
			return err
		case err := <-snapshotErrors:
			return err
		default:
		}
		if allTerminal(gateway, requests) {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !allTerminal(gateway, requests) {
		return fmt.Errorf("workload did not drain before %s", drainDeadline.Format(time.RFC3339))
	}
	stopSnapshotCollection()
	select {
	case err := <-snapshotErrors:
		return err
	default:
	}

	admitted, rejected, handlerReturns, successfulCompletions := 0, 0, 0, 0
	for _, observation := range requests {
		record, handlerReturned, err := makeRecord(gateway, observation, opts.baselineMode)
		if err != nil {
			return err
		}
		if err := collector.Record(record); err != nil {
			return fmt.Errorf("record request %s: %w", observation.id, err)
		}
		if observation.admitted {
			admitted++
		} else {
			rejected++
		}
		if handlerReturned {
			handlerReturns++
		}
		if record.Outcome == "completed" {
			successfulCompletions++
		}
	}

	if err := os.MkdirAll(filepath.Dir(opts.outDir), 0755); err != nil {
		return fmt.Errorf("create output parent directory: %w", err)
	}
	if err := os.Mkdir(opts.outDir, 0755); err != nil {
		return fmt.Errorf("create output directory %s: %w", opts.outDir, err)
	}
	recordsPath := filepath.Join(opts.outDir, "requests.jsonl")
	if err := collector.ExportJSONL(recordsPath); err != nil {
		return err
	}
	snapshotPath := filepath.Join(opts.outDir, "snapshots.jsonl")
	if err := exportSnapshots(snapshotPath, &snapshotMu, snapshots); err != nil {
		return err
	}
	summary := collector.ComputeSummary()
	summaryPath := filepath.Join(opts.outDir, "summary.json")
	summaryBytes, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal summary: %w", err)
	}
	if err := os.WriteFile(summaryPath, summaryBytes, 0644); err != nil {
		return fmt.Errorf("write summary: %w", err)
	}
	runType := "observed_go_controller_benchmark"
	interpretation := "One measured RESCUE Go-controller arm only; not a baseline comparison, simulator result, or PostgreSQL testbed result."
	if opts.baselineMode {
		runType = "observed_baseline_timed_release_benchmark"
		interpretation = "Measured Timed-Release (TR) baseline arm; releases capacity upon timeout without waiting for handle confirmation."
	}
	manifest := runManifest{
		RunType:               runType,
		Interpretation:        interpretation,
		Seed:                  opts.seed,
		ArrivalRatePerSecond:  opts.rate,
		MeasurementWindow:     window,
		Workers:               opts.workers,
		WorkersPerPool:        opts.workers,
		FirstContactSources:   opts.firstContact,
		MixedPoolWorkload:     opts.mixedPools,
		SacrificialSaturated:  opts.saturateSacrificial,
		SaturationTasks:       saturationTasks,
		ZombieFraction:        opts.zombieFraction,
		ZombieHangFactor:      opts.zombieHangFactor,
		BaselineMode:          opts.baselineMode,
		ProtectedCapacity:     gateway.Pools["normal"].Capacity(),
		WorkDuration:          opts.work,
		Deadline:              opts.deadline,
		RequestsOffered:       len(requests),
		RequestsAdmitted:      admitted,
		RequestsRejected:      rejected,
		HandlerReturns:        handlerReturns,
		SuccessfulCompletions: successfulCompletions,
		GoVersion:             runtime.Version(),
		OS:                    runtime.GOOS,
		Architecture:          runtime.GOARCH,
		CPUCount:              runtime.NumCPU(),
		Summary:               summary,
	}
	manifest.RecordsSHA256, err = fileSHA256(recordsPath)
	if err != nil {
		return err
	}
	manifest.GitRevision, err = gitRevision()
	if err != nil {
		return err
	}
	manifest.GitWorktreeDirty, err = gitWorktreeDirty()
	if err != nil {
		return err
	}
	manifest.CodeSHA256, err = codeSHA256()
	if err != nil {
		return err
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal run manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(opts.outDir, "run_manifest.json"), manifestBytes, 0644); err != nil {
		return fmt.Errorf("write run manifest: %w", err)
	}

	fmt.Printf("\nRun complete: offered=%d admitted=%d rejected=%d handler-returns=%d successes=%d\n",
		len(requests), admitted, rejected, handlerReturns, successfulCompletions)
	if summary.LegitimateFirstContactOffered > 0 {
		fmt.Printf("Legitimate first-contact deadline success (S_F): %.4f (%d/%d); ",
			summary.LegitimateFirstContactSuccessRate, summary.LegitimateFirstContactSucceeded, summary.LegitimateFirstContactOffered)
	} else {
		fmt.Print("Legitimate first-contact deadline success (S_F): n/a (no first-contact requests); ")
	}
	fmt.Printf("overall legitimate deadline success (S_L): %.4f; in-window goodput: %.3f completions/s\n",
		summary.LegitimateDeadlineSuccessRate, summary.Goodput)
	fmt.Printf("Data files: %s\n", opts.outDir)
	return nil
}

func dispatchLoop(gateway *controller.Controller, stop <-chan struct{}, done chan<- struct{}, errors chan<- error) {
	defer close(done)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	expiryTicker := time.NewTicker(10 * time.Millisecond)
	defer expiryTicker.Stop()
	for {
		select {
		case <-stop:
			if err := gateway.SweepExpiredQueued(); err != nil {
				select {
				case errors <- fmt.Errorf("sweep expired benchmark work: %w", err):
				default:
				}
			}
			reconcileAndDispatch(gateway, errors)
			dispatchQueued(gateway, errors)
			return
		case <-ticker.C:
			reconcileAndDispatch(gateway, errors)
		case <-expiryTicker.C:
			if err := gateway.SweepExpiredQueued(); err != nil {
				select {
				case errors <- fmt.Errorf("sweep expired benchmark work: %w", err):
				default:
				}
			}
		}
	}
}

func collectSnapshots(gateway *controller.Controller, actualRunning *atomic.Int64,
	offered, admitted, rejected *atomic.Int64, stop <-chan struct{}, done chan<- struct{},
	mu *sync.Mutex, snapshots *[]benchmarkSnapshot, collector *metrics.Collector, errorCh chan<- error) {
	defer close(done)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case now := <-ticker.C:
			sacrificial := gateway.Pools["sacrificial"]
			protected := gateway.Pools["normal"]
			for poolName, pool := range gateway.Pools {
				free := pool.FreeSlots()
				if free > pool.Capacity() {
					errorCh <- fmt.Errorf("worker pool %s reports %d free slots above capacity %d",
						poolName, free, pool.Capacity())
					return
				}
			}
			active := sacrificial.ActiveCount() + protected.ActiveCount()
			total := sacrificial.Capacity() + protected.Capacity()
			recordedFree := sacrificial.FreeSlots() + protected.FreeSlots()
			running := actualRunning.Load()
			snapshot := benchmarkSnapshot{
				Timestamp:              now,
				Offered:                offered.Load(),
				Admitted:               admitted.Load(),
				Rejected:               rejected.Load(),
				SacrificialActive:      sacrificial.ActiveCount(),
				SacrificialFree:        sacrificial.FreeSlots(),
				ProtectedActive:        protected.ActiveCount(),
				ProtectedFree:          protected.FreeSlots(),
				ActualRunningHandlers:  running,
				QueueLength:            gateway.Queue.Len(),
				QueueCapacity:          gateway.Queue.Capacity(),
				QueueDepthsByPool:      gateway.QueueDepthsByPool(),
				BlockedFrontDispatches: gateway.BlockedFrontDispatches(),
				QuarantineCount:        gateway.Quarantine.Count(),
				RecordedFreeWorkers:    recordedFree,
				ActualFreeWorkers:      int64(total) - running,
			}
			mu.Lock()
			*snapshots = append(*snapshots, snapshot)
			mu.Unlock()
			collector.AddSnapshot(metrics.Snapshot{
				Timestamp:       now,
				ActiveWorkers:   active,
				TotalWorkers:    total,
				QueueLength:     snapshot.QueueLength,
				QueueCapacity:   snapshot.QueueCapacity,
				QuarantineCount: snapshot.QuarantineCount,
				QuarantineMax:   gateway.Quarantine.Capacity(),
				RecordedFree:    recordedFree,
				ActualFree:      int(snapshot.ActualFreeWorkers),
			})
		}
	}
}

func exportSnapshots(path string, mu *sync.Mutex, snapshots []benchmarkSnapshot) error {
	mu.Lock()
	copied := append([]benchmarkSnapshot(nil), snapshots...)
	mu.Unlock()
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create snapshot file %s: %w", path, err)
	}
	encoder := json.NewEncoder(file)
	for _, snapshot := range copied {
		if err := encoder.Encode(snapshot); err != nil {
			_ = file.Close()
			return fmt.Errorf("encode benchmark snapshot: %w", err)
		}
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close snapshot file %s: %w", path, err)
	}
	return nil
}

func reconcileAndDispatch(gateway *controller.Controller, errs chan<- error) {
	if err := gateway.ReconcileCompletions(); err != nil {
		select {
		case errs <- fmt.Errorf("reconcile work completions: %w", err):
		default:
		}
		return
	}
	dispatchQueued(gateway, errs)
}

func dispatchQueued(gateway *controller.Controller, errs chan<- error) {
	for gateway.Queue.Len() > 0 {
		_, err := gateway.DispatchNext()
		if err != nil {
			if errors.Is(err, controller.ErrNoWorkerSlots) || errors.Is(err, controller.ErrNoQueuedWork) {
				return
			}
			select {
			case errs <- fmt.Errorf("dispatch queued benchmark work: %w", err):
			default:
			}
			return
		}
	}
}

func allTerminal(gateway *controller.Controller, requests []*requestObservation) bool {
	for _, request := range requests {
		if !request.admitted {
			continue
		}
		snapshot, ok := gateway.TaskSnapshot(request.id)
		if !ok || (snapshot.State != task.StateReleased && snapshot.State != task.StateQuarantined) {
			return false
		}
	}
	return true
}

func makeRecord(gateway *controller.Controller, request *requestObservation, baselineMode bool) (metrics.RequestRecord, bool, error) {
	record := metrics.RequestRecord{
		RequestID:                 request.id,
		TaskID:                    request.id,
		SourceID:                  request.sourceID,
		IsLegitimate:              request.legitimate,
		IsFirstContact:            request.firstContact,
		IsAttacker:                !request.legitimate,
		RequestClass:              map[bool]string{true: "legitimate", false: "attacker"}[request.legitimate],
		ArrivedAt:                 request.arrivedAt,
		Deadline:                  request.deadline,
		WorkDuration:              request.workDuration,
		CapacityReleasedOnTimeout: baselineMode,
	}
	if !request.admitted {
		record.Outcome = "rejected"
		record.WasRejected = true
		record.RejectReason = request.rejectReason
		return record, false, nil
	}

	snapshot, ok := gateway.TaskSnapshot(request.id)
	if !ok {
		return metrics.RequestRecord{}, false, fmt.Errorf("task snapshot %s missing after admission", request.id)
	}
	record.WasAdmitted = true
	record.AdmittedAt = snapshot.CreatedAt
	record.StartedAt = snapshot.StartedAt
	record.WasQuarantined = snapshot.State == task.StateQuarantined
	record.HandleCount = len(snapshot.Handles)
	for _, handle := range snapshot.Handles {
		if handle.State == task.HandleConfirmed {
			record.ConfirmedHandleCount++
		}
	}
	if !record.StartedAt.IsZero() && !record.StartedAt.Before(record.AdmittedAt) {
		record.QueueWait = record.StartedAt.Sub(record.AdmittedAt)
	}

	request.mu.Lock()
	completedAt, workErr := request.completed, request.workErr
	request.mu.Unlock()
	if completedAt.IsZero() {
		record.Outcome = "deadline_missed"
		record.MetDeadline = false
		return record, false, nil
	}
	record.CompletedAt = completedAt
	record.TotalLatency = completedAt.Sub(request.arrivedAt)
	if record.TotalLatency < 0 {
		return metrics.RequestRecord{}, false, fmt.Errorf("negative observed latency for %s", request.id)
	}
	if completedAt.After(request.deadline) {
		record.Outcome = "deadline_missed"
		record.MetDeadline = false
		record.SurvivedTimeout = workErr == nil
		return record, true, nil
	}
	if workErr != nil {
		record.Outcome = "timed_out"
		record.MetDeadline = false
		return record, true, nil
	}
	record.Outcome = "completed"
	record.MetDeadline = true
	return record, true, nil
}

func gitRevision() (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("read Git revision: %w", err)
	}
	return string(output[:len(output)-1]), nil
}

func gitWorktreeDirty() (bool, error) {
	cmd := exec.Command("git", "status", "--porcelain")
	output, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("read Git worktree status: %w", err)
	}
	return len(output) != 0, nil
}

func fileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read request records for SHA-256: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func codeSHA256() (string, error) {
	files := []string{
		"cmd/rescue-bench/main.go",
		"pkg/controller/controller.go",
		"pkg/worker/pool.go",
		"pkg/metrics/collector.go",
	}
	hash := sha256.New()
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read source file %s for code hash: %w", path, err)
		}
		if _, err := fmt.Fprintf(hash, "%s\x00", path); err != nil {
			return "", fmt.Errorf("hash source path %s: %w", path, err)
		}
		if _, err := hash.Write(data); err != nil {
			return "", fmt.Errorf("hash source file %s: %w", path, err)
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
