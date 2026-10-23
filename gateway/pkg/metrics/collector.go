// Package metrics collects and exports experiment measurements.
//
// All metrics are defined mathematically in the experiment protocol.
// This package collects raw per-request records and computes aggregates.
// Metrics are exported as JSONL for analysis scripts.
package metrics

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"sync"
	"time"
)

// RequestRecord captures the full lifecycle of a single request.
// This is the raw data unit exported for analysis.
type RequestRecord struct {
	// Identity
	RequestID  string `json:"request_id"`
	TaskID     string `json:"task_id,omitempty"`
	SourceID   string `json:"source_id"`
	Controller string `json:"controller"` // "baseline" or "rescue"

	// Classification (ground truth for experiments)
	IsLegitimate   bool   `json:"is_legitimate"`
	IsEmergency    bool   `json:"is_emergency"`
	IsAttacker     bool   `json:"is_attacker"`
	IsFirstContact bool   `json:"is_first_contact"`
	RequestClass   string `json:"request_class"`

	// Timing
	ArrivedAt    time.Time     `json:"arrived_at"`
	Deadline     time.Time     `json:"deadline"`
	AdmittedAt   time.Time     `json:"admitted_at,omitempty"`
	StartedAt    time.Time     `json:"started_at,omitempty"`
	CompletedAt  time.Time     `json:"completed_at,omitempty"`
	QueueWait    time.Duration `json:"queue_wait_ns"`
	TotalLatency time.Duration `json:"total_latency_ns"`
	WorkDuration time.Duration `json:"work_duration_ns"`

	// Outcome
	Outcome        string `json:"outcome"` // admitted, rejected, completed, deadline_missed, timed_out, quarantined
	WasAdmitted    bool   `json:"was_admitted"`
	MetDeadline    bool   `json:"met_deadline"`
	WasRejected    bool   `json:"was_rejected"`
	RejectReason   string `json:"reject_reason,omitempty"`
	WasQuarantined bool   `json:"was_quarantined"`

	// Resource accounting
	HandleCount               int  `json:"handle_count"`
	ConfirmedHandleCount      int  `json:"confirmed_handle_count"`
	SurvivedTimeout           bool `json:"survived_timeout"`             // Task continued after client timeout
	CapacityReleasedOnTimeout bool `json:"capacity_released_on_timeout"` // Baseline behavior
}

// MeasurementWindow is the shared wall-clock interval used to normalize
// goodput across experiment runs.
type MeasurementWindow struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// Snapshot captures system state at a point in time.
type Snapshot struct {
	Timestamp       time.Time `json:"timestamp"`
	ActiveWorkers   int       `json:"active_workers"`
	TotalWorkers    int       `json:"total_workers"`
	QueueLength     int       `json:"queue_length"`
	QueueCapacity   int       `json:"queue_capacity"`
	QuarantineCount int       `json:"quarantine_count"`
	QuarantineMax   int       `json:"quarantine_max"`
	RecordedFree    int       `json:"recorded_free"`    // Controller's view of free capacity
	ActualFree      int       `json:"actual_free"`      // Ground truth free capacity
	AccountingError int       `json:"accounting_error"` // recorded_free - actual_free
}

// Collector accumulates request records and periodic snapshots.
type Collector struct {
	mu         sync.Mutex
	records    []RequestRecord
	snapshots  []Snapshot
	controller string
	runID      string
	seed       int64
	startTime  time.Time
	window     MeasurementWindow
	hasWindow  bool
	requestIDs map[string]RequestRecord
}

// NewCollector creates a new metrics collector for an experiment run.
func NewCollector(controller string, runID string, seed int64) *Collector {
	return &Collector{
		controller: controller,
		runID:      runID,
		seed:       seed,
		records:    make([]RequestRecord, 0, 10000),
		snapshots:  make([]Snapshot, 0, 1000),
		startTime:  time.Now(),
		requestIDs: make(map[string]RequestRecord),
	}
}

// SetMeasurementWindow configures the fixed interval used by goodput metrics.
// The window is immutable after its first successful configuration so runs
// cannot silently change denominators during collection.
func (c *Collector) SetMeasurementWindow(window MeasurementWindow) error {
	if window.Start.IsZero() || window.End.IsZero() || !window.End.After(window.Start) {
		return fmt.Errorf("measurement window must have a start before its end")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hasWindow {
		if c.window == window {
			return nil
		}
		return fmt.Errorf("measurement window is already configured")
	}
	c.window = window
	c.hasWindow = true
	return nil
}

// Record adds a validated request record. Exact retries are idempotent;
// conflicting records with the same ID are rejected.
func (c *Collector) Record(r RequestRecord) error {
	if err := validateRecord(r); err != nil {
		return err
	}
	r.Controller = c.controller
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, exists := c.requestIDs[r.RequestID]; exists {
		if existing == r {
			return nil
		}
		return fmt.Errorf("request %s already has a conflicting record", r.RequestID)
	}

	c.records = append(c.records, r)
	c.requestIDs[r.RequestID] = r
	return nil
}

func validateRecord(r RequestRecord) error {
	if r.RequestID == "" {
		return fmt.Errorf("request ID must not be empty")
	}
	if r.ArrivedAt.IsZero() || r.Deadline.IsZero() || r.Deadline.Before(r.ArrivedAt) {
		return fmt.Errorf("request %s must have a valid arrival and deadline", r.RequestID)
	}
	if r.QueueWait < 0 || r.TotalLatency < 0 || r.WorkDuration < 0 {
		return fmt.Errorf("request %s has a negative duration", r.RequestID)
	}
	if r.HandleCount < 0 || r.ConfirmedHandleCount < 0 || r.ConfirmedHandleCount > r.HandleCount {
		return fmt.Errorf("request %s has invalid handle counts", r.RequestID)
	}
	if r.WasAdmitted && (r.AdmittedAt.IsZero() || r.AdmittedAt.Before(r.ArrivedAt)) {
		return fmt.Errorf("admitted request %s must have a valid admission time", r.RequestID)
	}
	if !r.WasAdmitted && !r.AdmittedAt.IsZero() {
		return fmt.Errorf("non-admitted request %s cannot have an admission time", r.RequestID)
	}
	if !r.StartedAt.IsZero() && (!r.WasAdmitted || r.StartedAt.Before(r.AdmittedAt)) {
		return fmt.Errorf("request %s has an invalid start time", r.RequestID)
	}
	if !r.CompletedAt.IsZero() && (!r.WasAdmitted || r.StartedAt.IsZero() || r.CompletedAt.Before(r.StartedAt)) {
		return fmt.Errorf("request %s has an invalid completion time", r.RequestID)
	}
	if !r.CompletedAt.IsZero() && r.TotalLatency != r.CompletedAt.Sub(r.ArrivedAt) {
		return fmt.Errorf("request %s total latency does not match its timestamps", r.RequestID)
	}
	if r.WasRejected != (r.Outcome == "rejected") {
		return fmt.Errorf("request %s rejection fields conflict with outcome %q", r.RequestID, r.Outcome)
	}
	if r.WasQuarantined != (r.Outcome == "quarantined") {
		return fmt.Errorf("request %s quarantine fields conflict with outcome %q", r.RequestID, r.Outcome)
	}

	switch r.Outcome {
	case "rejected":
		if r.WasAdmitted || !r.WasRejected || !r.CompletedAt.IsZero() || r.MetDeadline {
			return fmt.Errorf("request %s has inconsistent rejected outcome fields", r.RequestID)
		}
	case "admitted":
		if !r.WasAdmitted || !r.CompletedAt.IsZero() || r.MetDeadline {
			return fmt.Errorf("request %s has inconsistent admitted outcome fields", r.RequestID)
		}
	case "completed":
		if !r.WasAdmitted || r.CompletedAt.IsZero() || r.WasRejected || r.WasQuarantined {
			return fmt.Errorf("request %s has inconsistent completed outcome fields", r.RequestID)
		}
		if r.CompletedAt.After(r.Deadline) || !r.MetDeadline {
			return fmt.Errorf("completed request %s did not complete within its deadline", r.RequestID)
		}
	case "deadline_missed":
		if !r.WasAdmitted || r.MetDeadline || (!r.CompletedAt.IsZero() && !r.CompletedAt.After(r.Deadline)) {
			return fmt.Errorf("request %s has inconsistent deadline-missed outcome fields", r.RequestID)
		}
	case "timed_out":
		if !r.WasAdmitted || r.MetDeadline {
			return fmt.Errorf("request %s has inconsistent timed-out outcome fields", r.RequestID)
		}
	case "quarantined":
		if !r.WasAdmitted || r.MetDeadline {
			return fmt.Errorf("request %s has inconsistent quarantined outcome fields", r.RequestID)
		}
	default:
		return fmt.Errorf("request %s has unknown outcome %q", r.RequestID, r.Outcome)
	}
	return nil
}

func isSuccessful(r RequestRecord) bool {
	return r.Outcome == "completed" &&
		r.WasAdmitted &&
		!r.CompletedAt.IsZero() &&
		!r.CompletedAt.After(r.Deadline)
}

func offeredInWindow(r RequestRecord, window MeasurementWindow, hasWindow bool) bool {
	return !hasWindow || (!r.ArrivedAt.Before(window.Start) && r.ArrivedAt.Before(window.End))
}

// AddSnapshot records a point-in-time system state.
func (c *Collector) AddSnapshot(s Snapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snapshots = append(c.snapshots, s)
}

// --- Aggregate metric computations ---
// Each metric is defined in docs/metrics.md

// LegitimateDeadlineSuccessRate computes S_L over legitimate requests that
// arrived in the configured measurement window (or all records if unset).
func (c *Collector) LegitimateDeadlineSuccessRate() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	var offered, succeeded int
	for _, r := range c.records {
		if r.IsLegitimate && offeredInWindow(r, c.window, c.hasWindow) {
			offered++
			if isSuccessful(r) {
				succeeded++
			}
		}
	}
	if offered == 0 {
		return 0
	}
	return float64(succeeded) / float64(offered)
}

// LegitimeFalseRejectionRate computes false rejections over legitimate requests
// in the configured measurement window (or all records if unset).
func (c *Collector) LegitimeFalseRejectionRate() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	var offered, rejected int
	for _, r := range c.records {
		if r.IsLegitimate && offeredInWindow(r, c.window, c.hasWindow) {
			offered++
			if r.WasRejected {
				rejected++
			}
		}
	}
	if offered == 0 {
		return 0
	}
	return float64(rejected) / float64(offered)
}

// AttackerAdmissionRate computes attacker admissions over attacker requests
// in the configured measurement window (or all records if unset).
func (c *Collector) AttackerAdmissionRate() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	var offered, admitted int
	for _, r := range c.records {
		if r.IsAttacker && offeredInWindow(r, c.window, c.hasWindow) {
			offered++
			if r.WasAdmitted {
				admitted++
			}
		}
	}
	if offered == 0 {
		return 0
	}
	return float64(admitted) / float64(offered)
}

// Goodput computes successful completions per second over the configured
// measurement window. It returns zero until a window is configured.
func (c *Collector) Goodput() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return goodput(c.records, c.window, c.hasWindow, false)
}

// LegitimateGoodput computes legitimate successful requests per second.
func (c *Collector) LegitimateGoodput() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return goodput(c.records, c.window, c.hasWindow, true)
}

func goodput(records []RequestRecord, window MeasurementWindow, hasWindow, legitimateOnly bool) float64 {
	if !hasWindow {
		return 0
	}
	succeeded := 0
	for _, r := range records {
		if !isSuccessful(r) || (legitimateOnly && !r.IsLegitimate) {
			continue
		}
		if !r.CompletedAt.Before(window.Start) && r.CompletedAt.Before(window.End) {
			succeeded++
		}
	}
	return float64(succeeded) / window.End.Sub(window.Start).Seconds()
}

// LatencyPercentiles computes p50, p95, p99 latency for successful legitimate
// requests only. Failed and unfinished requests are reported by outcome counts
// in Summary; their censored durations are not mixed into this distribution.
func (c *Collector) LatencyPercentiles() (p50, p95, p99 time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return latencyPercentiles(c.records, c.window, c.hasWindow)
}

// ResourceAccountingError computes the average difference between
// recorded free capacity and actual free capacity across snapshots.
// Positive = controller overestimates free capacity (unsafe).
// Negative = controller underestimates free capacity (conservative).
func (c *Collector) ResourceAccountingError() (avgError float64, maxUnsafe int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.snapshots) == 0 {
		return 0, 0
	}

	var totalErr int
	for _, s := range c.snapshots {
		err := s.RecordedFree - s.ActualFree
		totalErr += err
		if err > maxUnsafe {
			maxUnsafe = err
		}
	}
	return float64(totalErr) / float64(len(c.snapshots)), maxUnsafe
}

// Summary produces a JSON-serializable summary of all metrics.
type Summary struct {
	RunID             string            `json:"run_id"`
	Controller        string            `json:"controller"`
	Seed              int64             `json:"seed"`
	Duration          float64           `json:"duration_seconds"`
	MeasurementWindow MeasurementWindow `json:"measurement_window"`

	// Counts
	TotalRequests                   int `json:"total_requests"`
	LegitimateOffered               int `json:"legitimate_offered"`
	LegitimateAdmitted              int `json:"legitimate_admitted"`
	LegitimateSucceeded             int `json:"legitimate_succeeded"`
	FirstContactOffered             int `json:"first_contact_offered"`
	LegitimateFirstContactOffered   int `json:"legitimate_first_contact_offered"`
	LegitimateFirstContactSucceeded int `json:"legitimate_first_contact_succeeded"`
	LegitimateRejected              int `json:"legitimate_rejected"`
	LegitimateTimedOut              int `json:"legitimate_timed_out"`
	LegitimateDeadlineMissed        int `json:"legitimate_deadline_missed"`
	TimedOutRequests                int `json:"timed_out_requests"`
	DeadlineMissedRequests          int `json:"deadline_missed_requests"`
	AttackerOffered                 int `json:"attacker_offered"`
	AttackerAdmitted                int `json:"attacker_admitted"`

	// Primary metric
	LegitimateDeadlineSuccessRate     float64 `json:"S_L"`
	LegitimateFirstContactSuccessRate float64 `json:"S_F"`

	// Secondary metrics
	FalseRejectionRate  float64 `json:"false_rejection_rate"`
	AttackerAdmRate     float64 `json:"attacker_admission_rate"`
	Goodput             float64 `json:"goodput_rps"`
	LegitimateGoodput   float64 `json:"legitimate_goodput_rps"`
	P50Latency          float64 `json:"p50_latency_ms"`
	P95Latency          float64 `json:"p95_latency_ms"`
	P99Latency          float64 `json:"p99_latency_ms"`
	AvgAccountingError  float64 `json:"avg_accounting_error"`
	MaxUnsafeError      int     `json:"max_unsafe_error"`
	AccountingSnapshots int     `json:"accounting_snapshots"`
	QuarantineCount     int     `json:"quarantine_count"`
}

// ComputeSummary calculates the full metrics summary.
func (c *Collector) ComputeSummary() Summary {
	c.mu.Lock()
	records := append([]RequestRecord(nil), c.records...)
	snapshots := append([]Snapshot(nil), c.snapshots...)
	window := c.window
	hasWindow := c.hasWindow
	duration := time.Since(c.startTime).Seconds()
	c.mu.Unlock()

	s := Summary{
		RunID:      c.runID,
		Controller: c.controller,
		Seed:       c.seed,
		Duration:   duration,
	}
	if hasWindow {
		s.MeasurementWindow = window
	}

	var legitimateRejected int
	var attackerAdmitted int
	var attackerOffered int
	var legitimateOffered int
	var legitimateFirstContactOffered int
	var legitimateFirstContactSucceeded int
	var succeeded, legitimateSucceeded int
	var accountingTotal int
	for _, r := range records {
		s.TotalRequests++
		if isSuccessful(r) && hasWindow &&
			!r.CompletedAt.Before(window.Start) && r.CompletedAt.Before(window.End) {
			succeeded++
		}
		if !offeredInWindow(r, window, hasWindow) {
			if r.WasQuarantined {
				s.QuarantineCount++
			}
			continue
		}
		if r.IsFirstContact {
			s.FirstContactOffered++
			if r.IsLegitimate {
				s.LegitimateFirstContactOffered++
				legitimateFirstContactOffered++
				if isSuccessful(r) {
					s.LegitimateFirstContactSucceeded++
					legitimateFirstContactSucceeded++
				}
			}
		}
		if r.IsLegitimate {
			s.LegitimateOffered++
			legitimateOffered++
			if r.WasAdmitted {
				s.LegitimateAdmitted++
			}
			if isSuccessful(r) {
				s.LegitimateSucceeded++
				legitimateSucceeded++
			}
			if r.WasRejected {
				s.LegitimateRejected++
				legitimateRejected++
			}
		}
		if r.Outcome == "timed_out" {
			s.TimedOutRequests++
			if r.IsLegitimate {
				s.LegitimateTimedOut++
			}
		}
		if r.Outcome == "deadline_missed" {
			s.DeadlineMissedRequests++
			if r.IsLegitimate {
				s.LegitimateDeadlineMissed++
			}
		}
		if r.IsAttacker {
			s.AttackerOffered++
			attackerOffered++
			if r.WasAdmitted {
				s.AttackerAdmitted++
				attackerAdmitted++
			}
		}
		if r.WasQuarantined {
			s.QuarantineCount++
		}
	}
	if legitimateOffered > 0 {
		s.LegitimateDeadlineSuccessRate = float64(legitimateSucceeded) / float64(legitimateOffered)
		s.FalseRejectionRate = float64(legitimateRejected) / float64(legitimateOffered)
	}
	if legitimateFirstContactOffered > 0 {
		s.LegitimateFirstContactSuccessRate = float64(legitimateFirstContactSucceeded) / float64(legitimateFirstContactOffered)
	}
	if attackerOffered > 0 {
		s.AttackerAdmRate = float64(attackerAdmitted) / float64(attackerOffered)
	}
	if hasWindow {
		windowSeconds := window.End.Sub(window.Start).Seconds()
		s.Goodput = float64(succeeded) / windowSeconds
		legitimateInWindow := 0
		for _, r := range records {
			if r.IsLegitimate && isSuccessful(r) &&
				!r.CompletedAt.Before(window.Start) && r.CompletedAt.Before(window.End) {
				legitimateInWindow++
			}
		}
		s.LegitimateGoodput = float64(legitimateInWindow) / windowSeconds
	}

	p50, p95, p99 := latencyPercentiles(records, window, hasWindow)
	s.P50Latency = float64(p50.Microseconds()) / 1000.0
	s.P95Latency = float64(p95.Microseconds()) / 1000.0
	s.P99Latency = float64(p99.Microseconds()) / 1000.0

	if len(snapshots) > 0 {
		s.AccountingSnapshots = len(snapshots)
		var maxUnsafe int
		for _, snapshot := range snapshots {
			err := snapshot.RecordedFree - snapshot.ActualFree
			accountingTotal += err
			if err > maxUnsafe {
				maxUnsafe = err
			}
		}
		s.AvgAccountingError = float64(accountingTotal) / float64(len(snapshots))
		s.MaxUnsafeError = maxUnsafe
	}

	return s
}

func latencyPercentiles(records []RequestRecord, window MeasurementWindow, hasWindow bool) (p50, p95, p99 time.Duration) {
	var latencies []time.Duration
	for _, r := range records {
		if r.IsLegitimate && isSuccessful(r) && offeredInWindow(r, window, hasWindow) {
			latencies = append(latencies, r.CompletedAt.Sub(r.ArrivedAt))
		}
	}
	if len(latencies) == 0 {
		return 0, 0, 0
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	percentile := func(p float64) time.Duration {
		idx := int(math.Ceil(p*float64(len(latencies)))) - 1
		if idx < 0 {
			idx = 0
		}
		if idx >= len(latencies) {
			idx = len(latencies) - 1
		}
		return latencies[idx]
	}
	return percentile(0.50), percentile(0.95), percentile(0.99)
}

// ExportJSONL writes all raw request records to a JSONL file.
func (c *Collector) ExportJSONL(path string) error {
	c.mu.Lock()
	records := make([]RequestRecord, len(c.records))
	copy(records, c.records)
	c.mu.Unlock()

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	for _, r := range records {
		if err := enc.Encode(r); err != nil {
			return fmt.Errorf("encode record: %w", err)
		}
	}
	return nil
}

// ExportSummaryJSON writes the metrics summary to a JSON file.
func (c *Collector) ExportSummaryJSON(path string) error {
	summary := c.ComputeSummary()
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal summary: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// RecordCount returns total records collected.
func (c *Collector) RecordCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.records)
}
