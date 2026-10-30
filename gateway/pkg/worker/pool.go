// Package worker implements a bounded worker pool for the RESCUE gateway.
//
// The worker pool is the authoritative source of task completion in the prototype.
// It provides independent confirmation that a task has terminated and its worker
// slot has been released — this is the "handle verification" for HandleWorkerSlot.
//
// Key invariants:
//   - Running and unreconciled work never exceed configured capacity
//   - Each worker slot is a tracked resource handle
//   - A handler completion remains charged until the consumer acknowledges reconciliation
package worker

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// TaskFunc is the function executed by a worker. It receives a context
// that may be cancelled (for cooperative cancellation) and returns an error.
type TaskFunc func(ctx context.Context) error

// CompletionEvent is recorded when a task finishes execution.
// This is the independent confirmation of resource release.
type CompletionEvent struct {
	CompletionID string
	TaskID       string
	HandleID     string // Worker slot handle ID
	StartedAt    time.Time
	Duration     time.Duration
	Err          error // Non-nil if task failed
	Cancelled    bool  // True if context was cancelled
	TimedOut     bool  // True if exceeded deadline
}

// Pool bounds running work and retains each worker-slot charge until its
// completion has been acknowledged by the consumer.
type Pool struct {
	mu                  sync.Mutex
	capacity            int
	activeCount         int32                 // running tasks plus completions awaiting acknowledgement
	activeTasks         map[string]activeTask // taskID → currently executing task
	pendingCompletions  map[string]CompletionEvent
	handleOwners        map[string]string
	usedHandles         map[string]struct{}
	acknowledgedHandles map[string]string
	closed              int32
	poolName            string // "emergency" | "normal" | "sacrificial"
}

type activeTask struct {
	taskID   string
	handleID string
	cancel   context.CancelFunc
	started  time.Time
}

// NewPool creates a worker pool. completionBuf remains for source compatibility;
// completions are polled from PendingCompletions, not delivered on a second
// channel.
func NewPool(poolName string, capacity int, completionBuf int) *Pool {
	if capacity < 1 {
		panic("worker pool capacity must be at least 1")
	}
	_ = completionBuf
	return &Pool{
		capacity:            capacity,
		activeTasks:         make(map[string]activeTask),
		pendingCompletions:  make(map[string]CompletionEvent),
		handleOwners:        make(map[string]string),
		usedHandles:         make(map[string]struct{}),
		acknowledgedHandles: make(map[string]string),
		poolName:            poolName,
	}
}

// Submit attempts to start a task on a free worker slot.
// Returns the handle ID on success, or an error if no capacity.
// The task function receives a cancellable context.
//
// The caller is responsible for calling this only when capacity is available
// (after an admission check). This function enforces the hard invariant that
// active tasks never exceed capacity.
func (p *Pool) Submit(taskID string, fn TaskFunc) (handleID string, err error) {
	handleID = fmt.Sprintf("%s-worker-%s-%d", p.poolName, taskID, time.Now().UnixNano())
	return p.SubmitWithHandle(taskID, handleID, context.Background(), fn)
}

// SubmitWithHandle starts a task using a caller-assigned handle ID and parent
// context. Assigning the ID before submission lets the controller commit its
// lease manifest and fence before execution becomes visible.
func (p *Pool) SubmitWithHandle(taskID, handleID string, parent context.Context, fn TaskFunc) (string, error) {
	if taskID == "" {
		return "", fmt.Errorf("task ID must not be empty")
	}
	if handleID == "" {
		return "", fmt.Errorf("handle ID must not be empty")
	}
	if parent == nil {
		return "", fmt.Errorf("parent context must not be nil")
	}
	if fn == nil {
		return "", fmt.Errorf("task function must not be nil")
	}
	if atomic.LoadInt32(&p.closed) != 0 {
		return "", fmt.Errorf("pool %s is closed", p.poolName)
	}

	p.mu.Lock()
	if atomic.LoadInt32(&p.closed) != 0 {
		p.mu.Unlock()
		return "", fmt.Errorf("pool %s is closed", p.poolName)
	}
	if _, exists := p.activeTasks[taskID]; exists {
		p.mu.Unlock()
		return "", fmt.Errorf("pool %s: task %s is already active", p.poolName, taskID)
	}
	for _, event := range p.pendingCompletions {
		if event.TaskID == taskID {
			p.mu.Unlock()
			return "", fmt.Errorf("pool %s: task %s has an unacknowledged completion", p.poolName, taskID)
		}
	}
	if owner, exists := p.handleOwners[handleID]; exists {
		p.mu.Unlock()
		return "", fmt.Errorf("pool %s: handle %s is already assigned to task %s", p.poolName, handleID, owner)
	}
	if _, used := p.usedHandles[handleID]; used {
		p.mu.Unlock()
		return "", fmt.Errorf("pool %s: handle %s was already used", p.poolName, handleID)
	}

	// Hard invariant: active tasks never exceed capacity
	currentActive := int(atomic.LoadInt32(&p.activeCount))
	if currentActive >= p.capacity {
		p.mu.Unlock()
		return "", fmt.Errorf("pool %s: capacity exhausted (%d/%d)",
			p.poolName, currentActive, p.capacity)
	}

	ctx, cancel := context.WithCancel(parent)

	p.activeTasks[taskID] = activeTask{
		taskID:   taskID,
		handleID: handleID,
		cancel:   cancel,
		started:  time.Now(),
	}
	p.handleOwners[handleID] = taskID
	p.usedHandles[handleID] = struct{}{}

	atomic.AddInt32(&p.activeCount, 1)
	p.mu.Unlock()

	// Run the task in a new goroutine
	go p.runTask(taskID, handleID, ctx, fn)

	return handleID, nil
}

// runTask executes the task function and records a completion event. The slot
// remains charged until the consumer acknowledges reconciliation.
func (p *Pool) runTask(taskID, handleID string, ctx context.Context, fn TaskFunc) {
	started := time.Now()

	// Execute the task and still report slot release if the handler panics.
	taskErr := runTaskFunc(ctx, fn)

	duration := time.Since(started)
	cancelled := ctx.Err() == context.Canceled
	timedOut := ctx.Err() == context.DeadlineExceeded

	// The handler is finished, but its slot remains charged until its completion
	// has been reconciled and acknowledged by the consumer.
	p.mu.Lock()
	at := p.activeTasks[taskID]
	delete(p.activeTasks, taskID)
	p.pendingCompletions[handleID] = CompletionEvent{
		CompletionID: completionID(p.poolName, handleID),
		TaskID:       taskID,
		HandleID:     handleID,
		StartedAt:    started,
		Duration:     duration,
		Err:          taskErr,
		Cancelled:    cancelled,
		TimedOut:     timedOut,
	}
	p.mu.Unlock()
	if at.cancel != nil {
		at.cancel()
	}
}

// ForceRelease simulates baseline timed release by releasing the capacity slot
// immediately without waiting for the task to finish running.
func (p *Pool) ForceRelease(handleID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	taskID, exists := p.handleOwners[handleID]
	if !exists {
		return fmt.Errorf("pool %s: handle %s not found for force release", p.poolName, handleID)
	}
	delete(p.activeTasks, taskID)
	delete(p.handleOwners, handleID)
	p.acknowledgedHandles[handleID] = taskID
	atomic.AddInt32(&p.activeCount, -1)
	return nil
}

func completionID(poolName, handleID string) string {
	return fmt.Sprintf("%d:%s:%d:%s", len(poolName), poolName, len(handleID), handleID)
}

// PendingCompletions is the sole completion delivery mechanism. Events remain
// available until acknowledged, so repeated polling may return an event again.
// Consumers should make reconciliation idempotent using CompletionID.
func (p *Pool) PendingCompletions() []CompletionEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	pending := make([]CompletionEvent, 0, len(p.pendingCompletions))
	for _, event := range p.pendingCompletions {
		pending = append(pending, event)
	}
	sort.Slice(pending, func(i, j int) bool {
		return pending[i].CompletionID < pending[j].CompletionID
	})
	return pending
}

// AcknowledgeCompletion frees a worker slot only after the consumer has
// successfully reconciled the completion. Acknowledgement is idempotent.
func (p *Pool) AcknowledgeCompletion(taskID, handleID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	event, exists := p.pendingCompletions[handleID]
	if !exists {
		if p.acknowledgedHandles[handleID] == taskID {
			return nil
		}
		return fmt.Errorf("pool %s: completion for handle %s is not pending", p.poolName, handleID)
	}
	if event.TaskID != taskID {
		return fmt.Errorf("pool %s: completion handle %s belongs to task %s, not %s", p.poolName, handleID, event.TaskID, taskID)
	}
	// ForceRelease may have acknowledged the handle while its task was still running.
	alreadyReleased := p.acknowledgedHandles[handleID] == taskID
	delete(p.pendingCompletions, handleID)
	delete(p.handleOwners, handleID)
	p.acknowledgedHandles[handleID] = taskID
	if alreadyReleased {
		return nil
	}
	atomic.AddInt32(&p.activeCount, -1)
	return nil
}

func runTaskFunc(ctx context.Context, fn TaskFunc) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("task handler panicked: %v", recovered)
		}
	}()
	return fn(ctx)
}

// Cancel sends a cancellation signal to a running task.
// This does NOT release the worker slot or confirm handle release.
// The task must cooperatively check ctx.Done() to actually stop.
func (p *Pool) Cancel(taskID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	at, ok := p.activeTasks[taskID]
	if !ok {
		return fmt.Errorf("pool %s: task %s not found (may have already completed)", p.poolName, taskID)
	}

	at.cancel()
	return nil
}

// ActiveCount returns running tasks plus completed tasks awaiting acknowledgement.
// Both count against pool capacity so unprocessed completions cannot create
// fictitious free slots.
func (p *Pool) ActiveCount() int {
	return int(atomic.LoadInt32(&p.activeCount))
}

// Capacity returns the total pool capacity.
func (p *Pool) Capacity() int {
	return p.capacity
}

// FreeSlots returns capacity not occupied by running or unreconciled work.
func (p *Pool) FreeSlots() int {
	return p.capacity - int(atomic.LoadInt32(&p.activeCount))
}

// Name returns the pool name.
func (p *Pool) Name() string {
	return p.poolName
}

// IsTaskActive returns whether a specific task is currently running.
func (p *Pool) IsTaskActive(taskID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.activeTasks[taskID]
	return ok
}

// Close gracefully shuts down the pool by cancelling all active tasks.
func (p *Pool) Close() {
	atomic.StoreInt32(&p.closed, 1)

	p.mu.Lock()
	for _, at := range p.activeTasks {
		at.cancel()
	}
	p.mu.Unlock()
}

// Shutdown cancels active handlers and waits until they have completed and
// their completion events have been reconciled and acknowledged.
func (p *Pool) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("pool %s: shutdown context must not be nil", p.poolName)
	}
	p.Close()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if p.ActiveCount() == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("pool %s: shutdown incomplete with %d tasks or unacknowledged completions: %w", p.poolName, p.ActiveCount(), ctx.Err())
		case <-ticker.C:
		}
	}
}

// WaitDrain blocks until all running tasks finish and every completion is
// acknowledged, or timeout.
func (p *Pool) WaitDrain(timeout time.Duration) error {
	deadline := time.After(timeout)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		if p.ActiveCount() == 0 {
			return nil
		}
		select {
		case <-deadline:
			return fmt.Errorf("pool %s: drain timeout with %d tasks still active",
				p.poolName, p.ActiveCount())
		case <-ticker.C:
			continue
		}
	}
}
