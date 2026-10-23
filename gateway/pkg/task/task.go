// Package task implements the task lifecycle state machine for RESCUE.
//
// Task states follow the architecture doc §5.2:
//
//	QUEUED → ACTIVE → COMPLETED | EXPIRED → RELEASED | QUARANTINED
//
// A task is the unit of work created when a request is admitted.
// Each task has a unique ID, a lease, a resource manifest (set of handles),
// and tracked state transitions with timestamps.
package task

import (
	"fmt"
	"sync"
	"time"
)

// State represents a task lifecycle state.
type State int

const (
	StateQueued      State = iota // Waiting in admission queue
	StateActive                   // Executing on a worker
	StateCompleted                // Work finished successfully within deadline
	StateExpired                  // Lease expired / deadline fired
	StateCancelling               // Cancel requested, awaiting confirmation
	StateQuarantined              // One or more handles unconfirmed after expiry
	StateReleased                 // All handles confirmed gone, capacity freed
)

func (s State) String() string {
	switch s {
	case StateQueued:
		return "QUEUED"
	case StateActive:
		return "ACTIVE"
	case StateCompleted:
		return "COMPLETED"
	case StateExpired:
		return "EXPIRED"
	case StateCancelling:
		return "CANCELLING"
	case StateQuarantined:
		return "QUARANTINED"
	case StateReleased:
		return "RELEASED"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", int(s))
	}
}

// HandleKind identifies the type of resource handle a task owns.
type HandleKind int

const (
	HandleWorkerSlot       HandleKind = iota // Goroutine/worker pool slot
	HandleQueueSlot                          // Bounded queue slot
	HandleExternalResource                   // Independently confirmed runtime/backend resource
)

func (h HandleKind) String() string {
	switch h {
	case HandleWorkerSlot:
		return "worker_slot"
	case HandleQueueSlot:
		return "queue_slot"
	case HandleExternalResource:
		return "external_resource"
	default:
		return fmt.Sprintf("unknown(%d)", int(h))
	}
}

// HandleState tracks whether a specific resource handle has been confirmed released.
type HandleState int

const (
	HandleActive    HandleState = iota // Handle is live/occupied
	HandleReleasing                    // Release requested, not yet confirmed
	HandleConfirmed                    // Independently confirmed gone
)

func (h HandleState) String() string {
	switch h {
	case HandleActive:
		return "ACTIVE"
	case HandleReleasing:
		return "RELEASING"
	case HandleConfirmed:
		return "CONFIRMED"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", int(h))
	}
}

// ResourceHandle represents a single resource owned by a task.
// Per architecture doc §5.3: charges are per handle, not per task.
type ResourceHandle struct {
	Kind        HandleKind  `json:"kind"`
	HandleID    string      `json:"handle_id"`
	State       HandleState `json:"state"`
	AcquiredAt  time.Time   `json:"acquired_at"`
	ReleasedAt  time.Time   `json:"released_at,omitempty"`
	ConfirmedAt time.Time   `json:"confirmed_at,omitempty"`
}

// Transition records a state change with timestamp for auditability.
type Transition struct {
	From      State     `json:"from"`
	To        State     `json:"to"`
	Timestamp time.Time `json:"timestamp"`
	Reason    string    `json:"reason"`
}

// Task represents a unit of work in the RESCUE system.
type Task struct {
	mu sync.RWMutex

	// Identity
	ID       string `json:"task_id"`
	LeaseID  string `json:"lease_id"`
	SourceID string `json:"source_id"` // Client identity for fairness
	Pool     string `json:"pool"`      // emergency | normal | sacrificial

	// Classification
	IsLegitimate bool   `json:"is_legitimate"` // Ground truth (known only in experiments)
	IsEmergency  bool   `json:"is_emergency"`  // Claimed emergency priority
	RequestClass string `json:"request_class"` // For workload characterization

	// Lifecycle
	State       State     `json:"state"`
	CreatedAt   time.Time `json:"created_at"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	Deadline    time.Time `json:"deadline"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
	ExpiredAt   time.Time `json:"expired_at,omitempty"`

	// Work characteristics
	WorkDuration     time.Duration `json:"work_duration"`      // Configured backend work time
	SurvivesTimeout  bool          `json:"survives_timeout"`   // Task continues after client timeout
	CancelResistant  bool          `json:"cancel_resistant"`   // Task ignores cancellation signal
	CancelResponseMs int           `json:"cancel_response_ms"` // Time to respond to cancel (0=immediate)

	// Resource manifest (architecture §5.3)
	Handles []ResourceHandle `json:"handles"`

	// Audit trail
	Transitions []Transition `json:"transitions"`

	// Fence version at time of lease creation
	FenceVersion uint64 `json:"fence_version"`
}

// Snapshot is an immutable-by-convention view that does not copy Task's lock.
type Snapshot struct {
	ID               string           `json:"task_id"`
	LeaseID          string           `json:"lease_id"`
	SourceID         string           `json:"source_id"`
	Pool             string           `json:"pool"`
	IsLegitimate     bool             `json:"is_legitimate"`
	IsEmergency      bool             `json:"is_emergency"`
	RequestClass     string           `json:"request_class"`
	State            State            `json:"state"`
	CreatedAt        time.Time        `json:"created_at"`
	StartedAt        time.Time        `json:"started_at,omitempty"`
	Deadline         time.Time        `json:"deadline"`
	CompletedAt      time.Time        `json:"completed_at,omitempty"`
	ExpiredAt        time.Time        `json:"expired_at,omitempty"`
	WorkDuration     time.Duration    `json:"work_duration"`
	SurvivesTimeout  bool             `json:"survives_timeout"`
	CancelResistant  bool             `json:"cancel_resistant"`
	CancelResponseMs int              `json:"cancel_response_ms"`
	Handles          []ResourceHandle `json:"handles"`
	Transitions      []Transition     `json:"transitions"`
	FenceVersion     uint64           `json:"fence_version"`
}

// New creates a new task in QUEUED state.
func New(id, leaseID, sourceID string, deadline time.Time, workDuration time.Duration) *Task {
	now := time.Now()
	t := &Task{
		ID:           id,
		LeaseID:      leaseID,
		SourceID:     sourceID,
		State:        StateQueued,
		CreatedAt:    now,
		Deadline:     deadline,
		WorkDuration: workDuration,
		Handles:      make([]ResourceHandle, 0, 2),
		Transitions: []Transition{{
			From:      StateQueued,
			To:        StateQueued,
			Timestamp: now,
			Reason:    "created",
		}},
	}
	return t
}

// validTransitions defines allowed state transitions.
// Architecture doc §5.2 lease lifecycle + additional operational states.
var validTransitions = map[State][]State{
	StateQueued:      {StateActive, StateExpired, StateReleased}, // Can expire or be rejected before dispatch
	StateActive:      {StateCompleted, StateExpired, StateCancelling},
	StateCompleted:   {StateReleased, StateQuarantined}, // Unconfirmed resources remain charged
	StateExpired:     {StateReleased, StateQuarantined}, // Handles confirmed or not
	StateCancelling:  {StateCompleted, StateExpired, StateQuarantined},
	StateQuarantined: {StateReleased, StateQuarantined}, // May re-enter on partial confirmation
	// StateReleased is terminal
}

// TransitionTo attempts a state transition. Returns error if invalid.
// Thread-safe: holds write lock.
func (t *Task) TransitionTo(newState State, reason string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.transitionToLocked(newState, reason)
}

func (t *Task) transitionToLocked(newState State, reason string) error {
	allowed, ok := validTransitions[t.State]
	if !ok {
		return fmt.Errorf("task %s: no transitions from terminal state %s", t.ID, t.State)
	}

	valid := false
	for _, s := range allowed {
		if s == newState {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("task %s: invalid transition %s → %s (reason: %s)",
			t.ID, t.State, newState, reason)
	}

	now := time.Now()
	t.Transitions = append(t.Transitions, Transition{
		From:      t.State,
		To:        newState,
		Timestamp: now,
		Reason:    reason,
	})

	t.State = newState

	// Update timestamps based on transition
	switch newState {
	case StateActive:
		t.StartedAt = now
	case StateCompleted:
		t.CompletedAt = now
	case StateExpired:
		t.ExpiredAt = now
	}

	return nil
}

// AddHandle registers a resource handle in the task's manifest.
func (t *Task) AddHandle(kind HandleKind, handleID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.Handles = append(t.Handles, ResourceHandle{
		Kind:       kind,
		HandleID:   handleID,
		State:      HandleActive,
		AcquiredAt: time.Now(),
	})
}

// ConfigureLease records the lease identity before the task is dispatched.
func (t *Task) ConfigureLease(leaseID, pool string, fenceVersion uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.LeaseID = leaseID
	t.Pool = pool
	t.FenceVersion = fenceVersion
}

func (t *Task) LeaseIdentity() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.LeaseID
}

func (t *Task) PoolName() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.Pool
}

// ConfirmHandle marks a specific handle as confirmed released.
// Returns error if handle not found or already confirmed.
// This is the core of completion-backed accounting: only this function
// can transition a handle to confirmed state.
func (t *Task) ConfirmHandle(handleID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	for i := range t.Handles {
		if t.Handles[i].HandleID == handleID {
			if t.Handles[i].State == HandleConfirmed {
				// Idempotent: already confirmed. This is safe.
				return nil
			}

			t.Handles[i].State = HandleConfirmed
			t.Handles[i].ConfirmedAt = time.Now()
			return nil
		}
	}
	return fmt.Errorf("task %s: handle %s not found in manifest", t.ID, handleID)
}

// HandleKindFor reports the manifest kind for a handle without exposing the
// mutable handle slice.
func (t *Task) HandleKindFor(handleID string) (HandleKind, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, handle := range t.Handles {
		if handle.HandleID == handleID {
			return handle.Kind, true
		}
	}
	return 0, false
}

// AllHandlesConfirmed returns true if every handle in the manifest
// has been independently confirmed released.
func (t *Task) AllHandlesConfirmed() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	for _, h := range t.Handles {
		if h.State != HandleConfirmed {
			return false
		}
	}
	return true
}

// HasUnconfirmedHandles returns true if any handle is not yet confirmed.
func (t *Task) HasUnconfirmedHandles() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	for _, h := range t.Handles {
		if h.State != HandleConfirmed {
			return true
		}
	}
	return false
}

// GetState returns the current state (thread-safe).
func (t *Task) GetState() State {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.State
}

// IsTerminal returns true if the task is in a terminal state.
func (t *Task) IsTerminal() bool {
	s := t.GetState()
	return s == StateReleased
}

// IsDeadlineExceeded checks whether the task's deadline has passed.
func (t *Task) IsDeadlineExceeded() bool {
	return time.Now().After(t.Deadline)
}

// LifetimeDuration returns the total time from creation to now or completion.
func (t *Task) LifetimeDuration() time.Duration {
	t.mu.RLock()
	defer t.mu.RUnlock()

	end := time.Now()
	if !t.CompletedAt.IsZero() {
		end = t.CompletedAt
	}
	return end.Sub(t.CreatedAt)
}

// QueueWaitDuration returns the time spent in the queue.
func (t *Task) QueueWaitDuration() time.Duration {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.StartedAt.IsZero() {
		return time.Since(t.CreatedAt)
	}
	return t.StartedAt.Sub(t.CreatedAt)
}

// Snapshot returns a read-only copy of the task for metrics/logging.
func (t *Task) Snapshot() Snapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return Snapshot{
		ID:               t.ID,
		LeaseID:          t.LeaseID,
		SourceID:         t.SourceID,
		Pool:             t.Pool,
		IsLegitimate:     t.IsLegitimate,
		IsEmergency:      t.IsEmergency,
		RequestClass:     t.RequestClass,
		State:            t.State,
		CreatedAt:        t.CreatedAt,
		StartedAt:        t.StartedAt,
		Deadline:         t.Deadline,
		CompletedAt:      t.CompletedAt,
		ExpiredAt:        t.ExpiredAt,
		WorkDuration:     t.WorkDuration,
		SurvivesTimeout:  t.SurvivesTimeout,
		CancelResistant:  t.CancelResistant,
		CancelResponseMs: t.CancelResponseMs,
		Handles:          append([]ResourceHandle(nil), t.Handles...),
		Transitions:      append([]Transition(nil), t.Transitions...),
		FenceVersion:     t.FenceVersion,
	}
}
