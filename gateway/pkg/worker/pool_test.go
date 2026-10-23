package worker

import (
	"context"
	"strings"
	"testing"
	"time"
)

func receiveCompletion(t *testing.T, p *Pool) CompletionEvent {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if pending := p.PendingCompletions(); len(pending) > 0 {
			return pending[0]
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for worker completion")
	return CompletionEvent{}
}

func acknowledgeCompletion(t *testing.T, p *Pool, event CompletionEvent) {
	t.Helper()
	if err := p.AcknowledgeCompletion(event.TaskID, event.HandleID); err != nil {
		t.Fatalf("acknowledge completion: %v", err)
	}
}

func TestSubmitEnforcesCapacityAndReportsCompletion(t *testing.T) {
	p := NewPool("test", 1, 1)
	started := make(chan struct{})
	finish := make(chan struct{})

	handle, err := p.SubmitWithHandle("task-1", "worker-1", context.Background(), func(context.Context) error {
		close(started)
		<-finish
		return nil
	})
	if err != nil {
		t.Fatalf("submit first task: %v", err)
	}
	<-started

	if _, err := p.Submit("task-2", func(context.Context) error { return nil }); err == nil {
		t.Fatal("expected submission to fail while capacity is exhausted")
	}
	if got := p.ActiveCount(); got != 1 {
		t.Fatalf("active count = %d, want 1", got)
	}
	if got := p.FreeSlots(); got != 0 {
		t.Fatalf("free slots = %d, want 0", got)
	}

	close(finish)
	event := receiveCompletion(t, p)
	if event.TaskID != "task-1" || event.HandleID != handle {
		t.Fatalf("completion identity = (%q, %q), want (task-1, %q)", event.TaskID, event.HandleID, handle)
	}
	if event.Err != nil {
		t.Fatalf("unexpected task error: %v", event.Err)
	}
	if p.ActiveCount() != 1 || p.IsTaskActive("task-1") {
		t.Fatal("completion delivery freed capacity before acknowledgement")
	}
	acknowledgeCompletion(t, p, event)
	if p.ActiveCount() != 0 {
		t.Fatal("acknowledging completion did not free worker capacity")
	}
	p.Close()
}

func TestCancellationDoesNotReleaseSlotUntilHandlerReturns(t *testing.T) {
	p := NewPool("test", 1, 1)
	started := make(chan struct{})
	cancelObserved := make(chan struct{})
	finish := make(chan struct{})

	if _, err := p.Submit("task-1", func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(cancelObserved)
		<-finish
		return nil
	}); err != nil {
		t.Fatalf("submit task: %v", err)
	}
	<-started

	if err := p.Cancel("task-1"); err != nil {
		t.Fatalf("cancel task: %v", err)
	}
	<-cancelObserved
	if got := p.ActiveCount(); got != 1 {
		t.Fatalf("cancellation released slot before handler returned: active=%d", got)
	}
	if err := p.Cancel("missing"); err == nil {
		t.Fatal("expected cancellation of unknown task to fail")
	}

	close(finish)
	event := receiveCompletion(t, p)
	if !event.Cancelled {
		t.Fatal("completion did not report cancellation")
	}
	if got := p.ActiveCount(); got != 1 {
		t.Fatalf("completion delivery freed capacity before acknowledgement: active=%d", got)
	}
	acknowledgeCompletion(t, p, event)
	if got := p.ActiveCount(); got != 0 {
		t.Fatalf("active count after acknowledgement = %d, want 0", got)
	}
	p.Close()
}

func TestParentDeadlineIsReportedAfterHandlerStops(t *testing.T) {
	p := NewPool("test", 1, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := make(chan struct{})
	finish := make(chan struct{})

	if _, err := p.SubmitWithHandle("task-1", "worker-1", ctx, func(context.Context) error {
		close(started)
		<-finish
		return nil
	}); err != nil {
		t.Fatalf("submit task: %v", err)
	}
	<-started
	<-ctx.Done()

	if got := p.ActiveCount(); got != 1 {
		t.Fatalf("deadline released a still-running handler: active=%d", got)
	}
	close(finish)
	event := receiveCompletion(t, p)
	if !event.TimedOut {
		t.Fatal("completion did not report deadline expiration")
	}
	acknowledgeCompletion(t, p, event)
	p.Close()
}

func TestPanickingHandlerStillReleasesSlotAndReportsError(t *testing.T) {
	p := NewPool("test", 1, 1)
	if _, err := p.Submit("task-1", func(context.Context) error {
		panic("test panic")
	}); err != nil {
		t.Fatalf("submit task: %v", err)
	}

	event := receiveCompletion(t, p)
	if event.Err == nil || !strings.Contains(event.Err.Error(), "test panic") {
		t.Fatalf("panic error = %v, want reported panic", event.Err)
	}
	if got := p.ActiveCount(); got != 1 {
		t.Fatalf("completion delivery freed capacity before acknowledgement: active=%d", got)
	}
	acknowledgeCompletion(t, p, event)
	if got := p.ActiveCount(); got != 0 {
		t.Fatalf("active count after acknowledgement = %d, want 0", got)
	}
	p.Close()
}

func TestCloseCancelsActiveTasksAndRejectsNewSubmissions(t *testing.T) {
	p := NewPool("test", 1, 1)
	started := make(chan struct{})
	cancelObserved := make(chan struct{})

	if _, err := p.Submit("task-1", func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(cancelObserved)
		return nil
	}); err != nil {
		t.Fatalf("submit task: %v", err)
	}
	<-started
	p.Close()
	<-cancelObserved

	event := receiveCompletion(t, p)
	if !event.Cancelled {
		t.Fatal("closed pool completion did not report cancellation")
	}
	acknowledgeCompletion(t, p, event)
	if _, err := p.Submit("task-2", func(context.Context) error { return nil }); err == nil {
		t.Fatal("closed pool accepted a new task")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := p.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown after completion acknowledgement: %v", err)
	}
}

func TestShutdownTimesOutUntilPendingCompletionIsAcknowledged(t *testing.T) {
	p := NewPool("test", 1, 1)
	if _, err := p.Submit("task-1", func(context.Context) error { return nil }); err != nil {
		t.Fatalf("submit task: %v", err)
	}

	event := receiveCompletion(t, p)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := p.Shutdown(ctx); err == nil {
		t.Fatal("shutdown succeeded with an unacknowledged completion")
	}
	if p.ActiveCount() != 1 {
		t.Fatal("shutdown timeout released capacity with a pending completion")
	}

	acknowledgeCompletion(t, p, event)
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	if err := p.Shutdown(ctx2); err != nil {
		t.Fatalf("shutdown after acknowledgement: %v", err)
	}
}

func TestWaitDrainReportsTimeoutThenSucceeds(t *testing.T) {
	p := NewPool("test", 1, 1)
	started := make(chan struct{})
	finish := make(chan struct{})
	if _, err := p.Submit("task-1", func(context.Context) error {
		close(started)
		<-finish
		return nil
	}); err != nil {
		t.Fatalf("submit task: %v", err)
	}
	<-started

	if err := p.WaitDrain(20 * time.Millisecond); err == nil {
		t.Fatal("expected drain timeout while task remained active")
	}
	close(finish)
	event := receiveCompletion(t, p)
	acknowledgeCompletion(t, p, event)
	if err := p.WaitDrain(time.Second); err != nil {
		t.Fatalf("wait for drained pool: %v", err)
	}
	p.Close()
}

func TestSubmitRejectsInvalidArgumentsAndDuplicateActiveTask(t *testing.T) {
	p := NewPool("test", 1, 1)
	if _, err := p.Submit("", func(context.Context) error { return nil }); err == nil {
		t.Fatal("expected empty task ID to fail")
	}
	if _, err := p.Submit("task-1", nil); err == nil {
		t.Fatal("expected nil task function to fail")
	}
	if _, err := p.SubmitWithHandle("task-1", "", context.Background(), func(context.Context) error { return nil }); err == nil {
		t.Fatal("expected empty handle ID to fail")
	}
	if _, err := p.SubmitWithHandle("task-1", "worker-1", nil, func(context.Context) error { return nil }); err == nil {
		t.Fatal("expected nil parent context to fail")
	}

	started := make(chan struct{})
	finish := make(chan struct{})
	if _, err := p.Submit("task-1", func(context.Context) error {
		close(started)
		<-finish
		return nil
	}); err != nil {
		t.Fatalf("submit task: %v", err)
	}
	<-started
	if _, err := p.Submit("task-1", func(context.Context) error { return nil }); err == nil {
		t.Fatal("expected duplicate active task ID to fail")
	}
	close(finish)
	event := receiveCompletion(t, p)
	acknowledgeCompletion(t, p, event)
	p.Close()
}

func TestUnacknowledgedCompletionsKeepCapacityCharged(t *testing.T) {
	p := NewPool("test", 2, 1)
	for _, taskID := range []string{"task-1", "task-2"} {
		if _, err := p.Submit(taskID, func(context.Context) error { return nil }); err != nil {
			t.Fatalf("submit %s: %v", taskID, err)
		}
	}

	deadline := time.Now().Add(time.Second)
	for (p.IsTaskActive("task-1") || p.IsTaskActive("task-2")) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if p.ActiveCount() != 2 {
		t.Fatalf("completed but unacknowledged tasks stopped being charged: active=%d", p.ActiveCount())
	}
	if p.FreeSlots() != 0 {
		t.Fatalf("unacknowledged completions advertised free slots: free=%d", p.FreeSlots())
	}

	pending := p.PendingCompletions()
	if len(pending) != 2 {
		t.Fatalf("pending completions = %d, want 2", len(pending))
	}
	if again := p.PendingCompletions(); len(again) != len(pending) || again[0].CompletionID != pending[0].CompletionID {
		t.Fatal("polling pending completions did not return the same stable IDs")
	}
	first := pending[0]
	if err := p.AcknowledgeCompletion(first.TaskID, first.HandleID); err != nil {
		t.Fatalf("acknowledge first event: %v", err)
	}
	second := pending[1]
	if err := p.AcknowledgeCompletion(second.TaskID, second.HandleID); err != nil {
		t.Fatalf("acknowledge second event: %v", err)
	}
	if p.ActiveCount() != 0 {
		t.Fatalf("active count after both acknowledgements = %d, want 0", p.ActiveCount())
	}
	p.Close()
}

func TestActiveAndPendingHandleIDsMustBeUnique(t *testing.T) {
	p := NewPool("test", 2, 2)
	started := make(chan struct{})
	finish := make(chan struct{})
	if _, err := p.SubmitWithHandle("task-1", "duplicate-handle", context.Background(), func(context.Context) error {
		close(started)
		<-finish
		return nil
	}); err != nil {
		t.Fatalf("submit first task: %v", err)
	}
	<-started
	if _, err := p.SubmitWithHandle("task-2", "duplicate-handle", context.Background(), func(context.Context) error {
		return nil
	}); err == nil {
		t.Fatal("expected duplicate active handle ID to be rejected")
	}

	close(finish)
	event := receiveCompletion(t, p)
	if _, err := p.SubmitWithHandle("task-2", event.HandleID, context.Background(), func(context.Context) error {
		return nil
	}); err == nil {
		t.Fatal("expected handle reuse before acknowledgement to be rejected")
	}
	acknowledgeCompletion(t, p, event)
	p.Close()
}
