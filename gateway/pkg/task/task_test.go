package task

import (
	"strings"
	"testing"
	"time"
)

func TestNewTask(t *testing.T) {
	deadline := time.Now().Add(500 * time.Millisecond)
	task := New("task-1", "lease-1", "source-1", deadline, 100*time.Millisecond)

	if task.State != StateQueued {
		t.Errorf("new task should be QUEUED, got %s", task.State)
	}
	if task.ID != "task-1" {
		t.Errorf("task ID mismatch: got %s", task.ID)
	}
	if len(task.Transitions) != 1 {
		t.Errorf("expected 1 initial transition, got %d", len(task.Transitions))
	}
}

func TestValidTransitions(t *testing.T) {
	tests := []struct {
		name    string
		from    State
		to      State
		wantErr bool
	}{
		{"queued to active", StateQueued, StateActive, false},
		{"queued to released (rejected)", StateQueued, StateReleased, false},
		{"active to completed", StateActive, StateCompleted, false},
		{"active to expired", StateActive, StateExpired, false},
		{"active to cancelling", StateActive, StateCancelling, false},
		{"completed to released", StateCompleted, StateReleased, false},
		{"expired to released", StateExpired, StateReleased, false},
		{"expired to quarantined", StateExpired, StateQuarantined, false},
		{"quarantined to released", StateQuarantined, StateReleased, false},
		// Invalid transitions
		{"queued to completed", StateQueued, StateCompleted, true},
		{"queued to expired", StateQueued, StateExpired, false},
		{"active to released directly", StateActive, StateReleased, true},
		{"completed to active", StateCompleted, StateActive, true},
		{"released to anything", StateReleased, StateActive, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := &Task{
				ID:    "test",
				State: tt.from,
			}
			err := task.TransitionTo(tt.to, "test")
			if (err != nil) != tt.wantErr {
				t.Errorf("TransitionTo(%s → %s) error = %v, wantErr %v",
					tt.from, tt.to, err, tt.wantErr)
			}
			if err == nil && task.State != tt.to {
				t.Errorf("after transition, state = %s, want %s", task.State, tt.to)
			}
		})
	}
}

func TestReleasedIsTerminal(t *testing.T) {
	task := &Task{ID: "test", State: StateReleased}
	err := task.TransitionTo(StateActive, "should fail")
	if err == nil {
		t.Error("released state should be terminal")
	}
}

func TestHandleLifecycle(t *testing.T) {
	task := New("t-1", "l-1", "s-1", time.Now().Add(time.Second), 100*time.Millisecond)

	// Add handles
	task.AddHandle(HandleWorkerSlot, "worker-0")
	task.AddHandle(HandleQueueSlot, "queue-0")

	// Initially not all confirmed
	if task.AllHandlesConfirmed() {
		t.Error("handles should not be confirmed initially")
	}
	if !task.HasUnconfirmedHandles() {
		t.Error("should have unconfirmed handles")
	}

	// Confirm one handle
	if err := task.ConfirmHandle("worker-0"); err != nil {
		t.Fatalf("ConfirmHandle failed: %v", err)
	}
	if task.AllHandlesConfirmed() {
		t.Error("should not be all confirmed yet")
	}

	// Confirm second handle
	if err := task.ConfirmHandle("queue-0"); err != nil {
		t.Fatalf("ConfirmHandle failed: %v", err)
	}
	if !task.AllHandlesConfirmed() {
		t.Error("all handles should be confirmed now")
	}

	// Idempotent re-confirmation should succeed
	if err := task.ConfirmHandle("worker-0"); err != nil {
		t.Fatalf("idempotent ConfirmHandle failed: %v", err)
	}
}

func TestConfirmNonexistentHandle(t *testing.T) {
	task := New("t-1", "l-1", "s-1", time.Now().Add(time.Second), 100*time.Millisecond)
	err := task.ConfirmHandle("nonexistent")
	if err == nil {
		t.Error("confirming nonexistent handle should fail")
	}
}

func TestAuditTrail(t *testing.T) {
	task := New("t-1", "l-1", "s-1", time.Now().Add(time.Second), 100*time.Millisecond)

	task.TransitionTo(StateActive, "dispatched")
	task.TransitionTo(StateCompleted, "work done")
	task.TransitionTo(StateReleased, "handles confirmed")

	if len(task.Transitions) != 4 { // initial + 3 transitions
		t.Errorf("expected 4 transitions, got %d", len(task.Transitions))
	}

	// Verify audit trail records the sequence
	expected := []State{StateQueued, StateActive, StateCompleted, StateReleased}
	for i, tr := range task.Transitions {
		if tr.To != expected[i] {
			t.Errorf("transition %d: got %s, want %s", i, tr.To, expected[i])
		}
	}
}

// Invariant: a client timeout does not imply backend termination (BR-F04).
func TestTimeoutDoesNotImplyTermination(t *testing.T) {
	task := New("t-1", "l-1", "s-1", time.Now().Add(time.Second), 100*time.Millisecond)
	task.AddHandle(HandleWorkerSlot, "worker-0")

	// Activate and expire (client timeout)
	task.TransitionTo(StateActive, "dispatched")
	task.TransitionTo(StateExpired, "deadline exceeded")

	// After expiry, handle is still active — NOT confirmed
	if task.AllHandlesConfirmed() {
		t.Error("expiry must not confirm handles (BR-F04)")
	}

	// Task cannot be released without confirmed handles
	if task.AllHandlesConfirmed() {
		t.Error("task with unconfirmed handles must not claim release")
	}
}

// Invariant: a cancellation request does not imply resource release (BR-F04).
func TestCancellationDoesNotImplyRelease(t *testing.T) {
	task := New("t-1", "l-1", "s-1", time.Now().Add(time.Second), 100*time.Millisecond)
	task.AddHandle(HandleWorkerSlot, "worker-0")

	task.TransitionTo(StateActive, "dispatched")
	task.TransitionTo(StateCancelling, "cancellation requested")

	// Cancelling does not confirm handles
	if task.AllHandlesConfirmed() {
		t.Error("cancellation must not confirm handles (BR-F04)")
	}
}

func TestStateStrings(t *testing.T) {
	states := []State{StateQueued, StateActive, StateCompleted, StateExpired,
		StateCancelling, StateQuarantined, StateReleased}
	for _, s := range states {
		str := s.String()
		if strings.Contains(str, "UNKNOWN") {
			t.Errorf("state %d has no string representation", int(s))
		}
	}
}

func TestSnapshotIsolation(t *testing.T) {
	task := New("t-1", "l-1", "s-1", time.Now().Add(time.Second), 100*time.Millisecond)
	task.AddHandle(HandleWorkerSlot, "worker-0")

	snap := task.Snapshot()

	// Modify original
	task.TransitionTo(StateActive, "dispatched")
	task.ConfirmHandle("worker-0")

	// Snapshot should be unaffected
	if snap.State != StateQueued {
		t.Error("snapshot state should be independent of original")
	}
	if snap.Handles[0].State != HandleActive {
		t.Error("snapshot handle state should be independent")
	}
}
