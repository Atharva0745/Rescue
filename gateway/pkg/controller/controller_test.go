package controller

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/rescue-gateway/gateway/pkg/source"
	"github.com/rescue-gateway/gateway/pkg/task"
)

type testHandleReleaseVerifier struct{}

func (testHandleReleaseVerifier) VerifyHandleReleased(taskID, handleID string, evidence []byte) error {
	if string(evidence) != taskID+":"+handleID {
		return fmt.Errorf("evidence does not match task and handle")
	}
	return nil
}

func releaseEvidence(taskID, handleID string) []byte {
	return []byte(taskID + ":" + handleID)
}

func newTestController(t *testing.T) *Controller {
	t.Helper()
	c, err := NewControllerWithHandleReleaseVerifier(8, 4, 4, 1, testHandleReleaseVerifier{})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RegisterPool("sacrificial", 2); err != nil {
		t.Fatal(err)
	}
	if err := c.RegisterPool("normal", 2); err != nil {
		t.Fatal(err)
	}
	return c
}

func waitForState(t *testing.T, c *Controller, taskID string, want task.State) task.Snapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := c.ReconcileCompletions(); err != nil {
			t.Fatalf("reconcile completions: %v", err)
		}
		snapshot, ok := c.TaskSnapshot(taskID)
		if ok && snapshot.State == want {
			return snapshot
		}
		time.Sleep(5 * time.Millisecond)
	}
	snapshot, _ := c.TaskSnapshot(taskID)
	t.Fatalf("task %s did not reach %s; got %s", taskID, want, snapshot.State)
	return task.Snapshot{}
}

func TestWorkLifecycleReleasesAfterWorkerConfirmation(t *testing.T) {
	c := newTestController(t)
	err := c.EnqueueWork(WorkRequest{
		TaskID:                 "task-1",
		SourceID:               "new-source",
		Pool:                   "normal",
		HandleManifestComplete: true,
		Work: func(context.Context) error {
			return nil
		},
	})
	if err != nil {
		t.Fatalf("enqueue work: %v", err)
	}
	if _, err := c.DispatchNext(); err != nil {
		t.Fatalf("dispatch work: %v", err)
	}

	snapshot := waitForState(t, c, "task-1", task.StateReleased)
	if snapshot.LeaseID == "" {
		t.Fatal("released task has no lease identity")
	}
	if snapshot.Pool != "sacrificial" {
		t.Fatalf("probation source dispatched to %s pool, want sacrificial", snapshot.Pool)
	}
	if c.Source.Get("new-source").Usage["global"] != 0 {
		t.Fatal("source charge was not released after worker confirmation")
	}
	if got := c.Quarantine.Stats().ReservedCount; got != 0 {
		t.Fatalf("successful completion leaked a quarantine reservation: %d", got)
	}
}

func TestDispatchKeepsWorkContextAliveUntilHandlerReturns(t *testing.T) {
	c := newTestController(t)
	const workDuration = 40 * time.Millisecond
	started := time.Now()
	if err := c.EnqueueWork(WorkRequest{
		TaskID:                 "context-lifetime",
		SourceID:               "context-source",
		Pool:                   "normal",
		Deadline:               time.Now().Add(time.Second),
		HandleManifestComplete: true,
		Work: func(ctx context.Context) error {
			timer := time.NewTimer(workDuration)
			defer timer.Stop()
			select {
			case <-timer.C:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}); err != nil {
		t.Fatalf("enqueue work: %v", err)
	}
	if _, err := c.DispatchNext(); err != nil {
		t.Fatalf("dispatch work: %v", err)
	}
	snapshot := waitForState(t, c, "context-lifetime", task.StateReleased)
	if snapshot.CompletedAt.Sub(started) < workDuration {
		t.Fatalf("work context was canceled before the handler completed: elapsed=%s", snapshot.CompletedAt.Sub(started))
	}
}

func TestSuccessfulCompletionIsNotMarkedCancelled(t *testing.T) {
	c := newTestController(t)
	if err := c.EnqueueWork(WorkRequest{
		TaskID:                 "natural-completion",
		SourceID:               "natural-completion-source",
		Deadline:               time.Now().Add(time.Second),
		HandleManifestComplete: true,
		Work:                   func(context.Context) error { return nil },
	}); err != nil {
		t.Fatalf("enqueue work: %v", err)
	}
	if _, err := c.DispatchNext(); err != nil {
		t.Fatalf("dispatch work: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		events := c.Pools["sacrificial"].PendingCompletions()
		if len(events) > 0 {
			if events[0].Cancelled || events[0].TimedOut {
				t.Fatalf("normal completion was marked cancelled or timed out: %+v", events[0])
			}
			if err := c.ReconcileCompletions(); err != nil {
				t.Fatalf("reconcile completion: %v", err)
			}
			if snapshot, ok := c.TaskSnapshot("natural-completion"); !ok || snapshot.State != task.StateReleased {
				t.Fatalf("task did not release after normal completion: %+v", snapshot)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("worker completion was not recorded")
}

func TestWorkerErrorsAreNotReportedAsSuccessfulCompletion(t *testing.T) {
	c := newTestController(t)
	if err := c.EnqueueWork(WorkRequest{
		TaskID:                 "failed-work",
		SourceID:               "failed-work-source",
		HandleManifestComplete: true,
		Work: func(context.Context) error {
			return fmt.Errorf("backend failure")
		},
	}); err != nil {
		t.Fatalf("enqueue work: %v", err)
	}
	if _, err := c.DispatchNext(); err != nil {
		t.Fatalf("dispatch work: %v", err)
	}
	snapshot := waitForState(t, c, "failed-work", task.StateReleased)
	foundFailure := false
	for _, transition := range snapshot.Transitions {
		if transition.To == task.StateFailed {
			foundFailure = true
		}
	}
	if !foundFailure {
		t.Fatalf("worker error was recorded as successful work: %+v", snapshot.Transitions)
	}
}

func TestUnconfirmedHandleQuarantinesAndKeepsCharge(t *testing.T) {
	c := newTestController(t)
	if err := c.EnqueueWork(WorkRequest{
		TaskID:                 "task-2",
		SourceID:               "source-2",
		Pool:                   "normal",
		RequiredHandles:        []string{"db-backend-2"},
		HandleManifestComplete: true,
		Work: func(context.Context) error {
			return nil
		},
	}); err != nil {
		t.Fatalf("enqueue work: %v", err)
	}
	if _, err := c.DispatchNext(); err != nil {
		t.Fatalf("dispatch work: %v", err)
	}

	snapshot := waitForState(t, c, "task-2", task.StateQuarantined)
	if c.Source.Get("source-2").Usage["global"] != 1 {
		t.Fatal("source charge released while a required handle remained unconfirmed")
	}
	if !c.Quarantine.Contains(snapshot.ID) {
		t.Fatal("task with an unconfirmed handle was not quarantined")
	}

	if err := c.ConfirmHandle(snapshot.ID, "db-backend-2", []byte("forged")); err == nil {
		t.Fatal("controller accepted unauthenticated release evidence")
	}
	if final, _ := c.TaskSnapshot(snapshot.ID); final.State != task.StateQuarantined {
		t.Fatal("invalid release evidence changed the task state")
	}
	if err := c.ConfirmHandle(snapshot.ID, "db-backend-2", releaseEvidence(snapshot.ID, "db-backend-2")); err != nil {
		t.Fatalf("confirm external handle: %v", err)
	}
	final, ok := c.TaskSnapshot(snapshot.ID)
	if !ok || final.State != task.StateReleased {
		t.Fatalf("task state after confirmation = %v, want RELEASED", final.State)
	}
	if c.Quarantine.Contains(snapshot.ID) {
		t.Fatal("released task remains in quarantine")
	}
	if c.Source.Get("source-2").Usage["global"] != 0 {
		t.Fatal("source charge was not released after all handles were confirmed")
	}
}

func TestWorkAdmissionRequiresCompleteHandleManifest(t *testing.T) {
	c := newTestController(t)
	err := c.EnqueueWork(WorkRequest{
		TaskID:   "unknown-manifest",
		SourceID: "source-manifest",
		Pool:     "normal",
		Work: func(context.Context) error {
			return nil
		},
	})
	if err == nil {
		t.Fatal("work admitted without an authoritative complete handle manifest")
	}
	if c.Queue.Len() != 0 || c.Source.Get("source-manifest") != nil {
		t.Fatal("rejected work changed admission state")
	}
}

func TestDeadlineDoesNotReleaseCancellationResistantWork(t *testing.T) {
	c := newTestController(t)
	started := make(chan struct{})
	finish := make(chan struct{})
	if err := c.EnqueueWork(WorkRequest{
		TaskID:                 "task-3",
		SourceID:               "source-3",
		Pool:                   "normal",
		Deadline:               time.Now().Add(40 * time.Millisecond),
		HandleManifestComplete: true,
		Work: func(context.Context) error {
			close(started)
			<-finish
			return nil
		},
	}); err != nil {
		t.Fatalf("enqueue work: %v", err)
	}
	if _, err := c.DispatchNext(); err != nil {
		t.Fatalf("dispatch work: %v", err)
	}
	<-started
	time.Sleep(70 * time.Millisecond)
	if err := c.ReconcileCompletions(); err != nil {
		t.Fatalf("reconcile expired task: %v", err)
	}
	expired, ok := c.TaskSnapshot("task-3")
	if !ok || expired.State != task.StateExpired {
		t.Fatalf("task state while handler remained active = %v, want EXPIRED", expired.State)
	}

	if got := c.Pools["sacrificial"].ActiveCount(); got != 1 {
		t.Fatalf("worker slot released before resistant work stopped: active=%d", got)
	}
	if got := c.Source.Get("source-3").Usage["global"]; got != 1 {
		t.Fatalf("source charge released on timeout alone: usage=%d", got)
	}

	close(finish)
	snapshot := waitForState(t, c, "task-3", task.StateReleased)
	if snapshot.State != task.StateReleased {
		t.Fatalf("task state = %s, want RELEASED", snapshot.State)
	}
	if c.Source.Get("source-3").Usage["global"] != 0 {
		t.Fatal("source charge was not released after worker termination")
	}
}

func TestFullWorkerPoolLeavesNextTaskQueued(t *testing.T) {
	c, err := NewControllerWithHandleReleaseVerifier(8, 4, 4, 1, testHandleReleaseVerifier{})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RegisterPool("sacrificial", 1); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	finish := make(chan struct{})
	if err := c.EnqueueWork(WorkRequest{
		TaskID:                 "task-4a",
		SourceID:               "source-4a",
		HandleManifestComplete: true,
		Work: func(context.Context) error {
			close(started)
			<-finish
			return nil
		},
	}); err != nil {
		t.Fatalf("enqueue first work: %v", err)
	}
	if err := c.EnqueueWork(WorkRequest{
		TaskID:                 "task-4b",
		SourceID:               "source-4b",
		HandleManifestComplete: true,
		Work: func(context.Context) error {
			return nil
		},
	}); err != nil {
		t.Fatalf("enqueue second work: %v", err)
	}
	if _, err := c.DispatchNext(); err != nil {
		t.Fatalf("dispatch first work: %v", err)
	}
	<-started

	if _, err := c.DispatchNext(); err == nil {
		t.Fatal("expected dispatch to reject while the worker pool is full")
	}
	if got := c.Queue.Len(); got != 1 {
		t.Fatalf("second task was removed from queue when pool was full: length=%d", got)
	}

	close(finish)
	waitForState(t, c, "task-4a", task.StateReleased)
	if _, err := c.DispatchNext(); err != nil {
		t.Fatalf("dispatch queued work after capacity returned: %v", err)
	}
	waitForState(t, c, "task-4b", task.StateReleased)
}

func TestDispatchSkipsBlockedPoolWithoutReorderingOtherPool(t *testing.T) {
	c, err := NewControllerWithHandleReleaseVerifier(16, 8, 8, 1, testHandleReleaseVerifier{})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RegisterPool("sacrificial", 1); err != nil {
		t.Fatal(err)
	}
	if err := c.RegisterPool("normal", 2); err != nil {
		t.Fatal(err)
	}

	blockerStarted := make(chan struct{})
	releaseBlocker := make(chan struct{})
	if err := c.EnqueueWork(WorkRequest{
		TaskID:                 "sacrificial-blocker",
		SourceID:               "probation-blocker",
		HandleManifestComplete: true,
		Work: func(context.Context) error {
			close(blockerStarted)
			<-releaseBlocker
			return nil
		},
	}); err != nil {
		t.Fatalf("enqueue sacrificial blocker: %v", err)
	}
	if id, err := c.DispatchNext(); err != nil || id != "sacrificial-blocker" {
		t.Fatalf("dispatch sacrificial blocker: id=%q err=%v", id, err)
	}
	<-blockerStarted

	if err := c.EnqueueWork(WorkRequest{
		TaskID:                 "sacrificial-waiting",
		SourceID:               "probation-waiting",
		HandleManifestComplete: true,
		Work:                   func(context.Context) error { return nil },
	}); err != nil {
		t.Fatalf("enqueue blocked sacrificial task: %v", err)
	}

	c.Source.RegisterSource("established-source", "established-group", "established-asn", "established-region")
	c.Source.Promote("established-source", source.TierNormal, "normal")
	for _, taskID := range []string{"normal-first", "normal-second"} {
		if err := c.EnqueueWork(WorkRequest{
			TaskID:                 taskID,
			SourceID:               "established-source",
			HandleManifestComplete: true,
			Work:                   func(context.Context) error { return nil },
		}); err != nil {
			t.Fatalf("enqueue established task %s: %v", taskID, err)
		}
	}

	for _, want := range []string{"normal-first", "normal-second"} {
		got, err := c.DispatchNext()
		if err != nil {
			t.Fatalf("dispatch %s past blocked sacrificial task: %v", want, err)
		}
		if got != want {
			t.Fatalf("dispatched %s, want %s", got, want)
		}
	}
	if got := c.BlockedFrontDispatches(); got != 2 {
		t.Fatalf("blocked-front dispatch count = %d, want 2", got)
	}
	if got := c.Queue.Len(); got != 1 {
		t.Fatalf("blocked sacrificial task was removed or reordered: queued=%d", got)
	}
	if depths := c.QueueDepthsByPool(); depths["sacrificial"] != 1 || depths["normal"] != 0 {
		t.Fatalf("per-pool queue depths = %v, want sacrificial=1 normal=0", depths)
	}
	if front, ok := c.Queue.Peek(); !ok || front.TaskID != "sacrificial-waiting" {
		t.Fatalf("queue front = %+v, ok=%t; want sacrificial-waiting", front, ok)
	}

	close(releaseBlocker)
	waitForState(t, c, "sacrificial-blocker", task.StateReleased)
	if _, err := c.DispatchNext(); err != nil {
		t.Fatalf("dispatch sacrificial task after capacity returns: %v", err)
	}
	waitForState(t, c, "sacrificial-waiting", task.StateReleased)
	waitForState(t, c, "normal-first", task.StateReleased)
	waitForState(t, c, "normal-second", task.StateReleased)
}

func TestSweepExpiredQueuedReleasesSourceChargeOnce(t *testing.T) {
	c := newTestController(t)
	deadline := time.Now().Add(30 * time.Millisecond)
	if err := c.EnqueueWork(WorkRequest{
		TaskID:                 "queued-expiry",
		SourceID:               "queued-expiry-source",
		Deadline:               deadline,
		HandleManifestComplete: true,
		Work:                   func(context.Context) error { return nil },
	}); err != nil {
		t.Fatalf("enqueue expiring task: %v", err)
	}
	time.Sleep(time.Until(deadline) + time.Millisecond)

	if err := c.SweepExpiredQueued(); err != nil {
		t.Fatalf("sweep expired queue: %v", err)
	}
	snapshot, ok := c.TaskSnapshot("queued-expiry")
	if !ok || snapshot.State != task.StateReleased {
		t.Fatalf("expired queued task state = %v, want RELEASED", snapshot.State)
	}
	if got := c.Source.Get("queued-expiry-source").Usage["global"]; got != 0 {
		t.Fatalf("source charge after expiry = %d, want 0", got)
	}
	if got := c.Queue.Len(); got != 0 {
		t.Fatalf("queue length after expiry = %d, want 0", got)
	}
	c.mu.Lock()
	_, pending := c.pending["queued-expiry"]
	c.mu.Unlock()
	if pending {
		t.Fatal("expired request remained in controller pending map")
	}

	if err := c.SweepExpiredQueued(); err != nil {
		t.Fatalf("repeat expiry sweep: %v", err)
	}
	if got := c.Source.Get("queued-expiry-source").Usage["global"]; got != 0 {
		t.Fatalf("repeat sweep changed source charge to %d", got)
	}
	stats := c.Queue.Stats()
	if stats.TotalEnqueued != stats.TotalDequeued+stats.TotalCancelled+stats.TotalExpired+int64(stats.Length) {
		t.Fatalf("queue accounting identity failed after expiry: %+v", stats)
	}
	if stats.TotalExpired != 1 || stats.Length != 0 {
		t.Fatalf("unexpected expiry counters: %+v", stats)
	}
}

func TestConcurrentDispatchersNeverSubmitPastPoolCapacity(t *testing.T) {
	c, err := NewControllerWithHandleReleaseVerifier(32, 32, 16, 1, testHandleReleaseVerifier{})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RegisterPool("sacrificial", 1); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 8)
	finish := make(chan struct{})
	for i := 0; i < 8; i++ {
		taskID := fmt.Sprintf("concurrent-%d", i)
		sourceID := fmt.Sprintf("concurrent-source-%d", i)
		if err := c.EnqueueWork(WorkRequest{
			TaskID:                 taskID,
			SourceID:               sourceID,
			HandleManifestComplete: true,
			Work: func(context.Context) error {
				started <- struct{}{}
				<-finish
				return nil
			},
		}); err != nil {
			t.Fatalf("enqueue %s: %v", taskID, err)
		}
	}

	successes := make(chan string, 8)
	var wait sync.WaitGroup
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if taskID, dispatchErr := c.DispatchNext(); dispatchErr == nil {
				successes <- taskID
			}
		}()
	}
	wait.Wait()
	if len(successes) != 1 {
		t.Fatalf("concurrent successful dispatches = %d, want 1", len(successes))
	}
	dispatchedTask := <-successes
	<-started
	if got := c.Pools["sacrificial"].ActiveCount(); got != 1 {
		t.Fatalf("active worker count = %d, capacity is 1", got)
	}
	if got := c.Queue.Len(); got != 7 {
		t.Fatalf("queued work after concurrent dispatch = %d, want 7", got)
	}

	close(finish)
	waitForState(t, c, dispatchedTask, task.StateReleased)
}

func TestWorkerSlotCannotBeExternallyConfirmed(t *testing.T) {
	c := newTestController(t)
	started := make(chan struct{})
	finish := make(chan struct{})
	if err := c.EnqueueWork(WorkRequest{
		TaskID:                 "task-5",
		SourceID:               "source-5",
		HandleManifestComplete: true,
		Work: func(context.Context) error {
			close(started)
			<-finish
			return nil
		},
	}); err != nil {
		t.Fatalf("enqueue work: %v", err)
	}
	if _, err := c.DispatchNext(); err != nil {
		t.Fatalf("dispatch work: %v", err)
	}
	<-started
	snapshot, ok := c.TaskSnapshot("task-5")
	if !ok {
		t.Fatal("task snapshot not found")
	}

	if err := c.ConfirmHandle("task-5", snapshot.Handles[0].HandleID, releaseEvidence("task-5", snapshot.Handles[0].HandleID)); err == nil {
		t.Fatal("caller was able to confirm a live worker slot")
	}
	if got := c.Pools["sacrificial"].ActiveCount(); got != 1 {
		t.Fatalf("worker slot released without completion: active=%d", got)
	}
	if got := c.Source.Get("source-5").Usage["global"]; got != 1 {
		t.Fatalf("source charge released before worker completion: usage=%d", got)
	}

	close(finish)
	waitForState(t, c, "task-5", task.StateReleased)
}

func TestFullQuarantinePausesProbationAndRetainsExistingCharges(t *testing.T) {
	c, err := NewControllerWithHandleReleaseVerifier(8, 4, 2, 1, testHandleReleaseVerifier{})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RegisterPool("sacrificial", 1); err != nil {
		t.Fatal(err)
	}
	if err := c.RegisterPool("normal", 1); err != nil {
		t.Fatal(err)
	}

	if err := c.EnqueueWork(WorkRequest{
		TaskID:                 "task-6a",
		SourceID:               "probation-source",
		RequiredHandles:        []string{"db-6a"},
		HandleManifestComplete: true,
		Work: func(context.Context) error {
			return nil
		},
	}); err != nil {
		t.Fatalf("enqueue probation work: %v", err)
	}
	if _, err := c.DispatchNext(); err != nil {
		t.Fatalf("dispatch probation work: %v", err)
	}
	waitForState(t, c, "task-6a", task.StateQuarantined)
	if stats := c.Quarantine.Stats(); stats.Count != 1 || stats.ReservedCount != 0 {
		t.Fatalf("probation reservation did not transfer to quarantine: %+v", stats)
	}

	if err := c.EnqueueWork(WorkRequest{
		TaskID:                 "task-6b",
		SourceID:               "another-probation-source",
		HandleManifestComplete: true,
		Work: func(context.Context) error {
			return nil
		},
	}); err == nil {
		t.Fatal("probationary admission continued while quarantine was full")
	}

	c.Source.RegisterSource("promoted-source", "group-6c", "asn-6c", "region-6c")
	c.Source.Promote("promoted-source", source.TierNormal, "normal")
	if err := c.EnqueueWork(WorkRequest{
		TaskID:                 "task-6c",
		SourceID:               "promoted-source",
		RequiredHandles:        []string{"db-6c"},
		HandleManifestComplete: true,
		Work: func(context.Context) error {
			return nil
		},
	}); err != nil {
		t.Fatalf("enqueue normal-pool work: %v", err)
	}
	if _, err := c.DispatchNext(); err != nil {
		t.Fatalf("dispatch normal-pool work: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := c.WaitTask(ctx, "task-6c"); err == nil {
		t.Fatal("expected explicit error when the quarantine tracker is full")
	}
	snapshot, ok := c.TaskSnapshot("task-6c")
	if !ok || snapshot.State != task.StateQuarantined {
		t.Fatalf("task state under full quarantine = %v, want QUARANTINED", snapshot.State)
	}
	if c.Source.Get("promoted-source").Usage["global"] != 1 {
		t.Fatal("charge for untracked quarantine was released")
	}

	if err := c.ConfirmHandle("task-6a", "db-6a", releaseEvidence("task-6a", "db-6a")); err != nil {
		t.Fatalf("confirm first external handle: %v", err)
	}
	if err := c.ReconcileCompletions(); err != nil {
		t.Fatalf("retry completion after quarantine capacity returned: %v", err)
	}
	if got := c.Pools["normal"].ActiveCount(); got != 0 {
		t.Fatalf("worker capacity remained charged after successful retry: active=%d", got)
	}
	if err := c.ConfirmHandle("task-6c", "db-6c", releaseEvidence("task-6c", "db-6c")); err != nil {
		t.Fatalf("confirm second external handle: %v", err)
	}
}

func TestControllerPolicyProtectsEmergencyCapacity(t *testing.T) {
	c, err := NewControllerWithHandleReleaseVerifier(4, 8, 8, 1, testHandleReleaseVerifier{})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RegisterPool("normal", 4); err != nil {
		t.Fatal(err)
	}
	if err := c.RegisterPool("emergency", 4); err != nil {
		t.Fatal(err)
	}
	c.Source.RegisterSource("normal-source", "normal-group", "normal-asn", "normal-region")
	c.Source.Promote("normal-source", source.TierNormal, "normal")

	finish := make(chan struct{})
	started := make(chan string, 4)
	for i := 0; i < 4; i++ {
		taskID := fmt.Sprintf("normal-%d", i)
		if err := c.EnqueueWork(WorkRequest{
			TaskID:                 taskID,
			SourceID:               "normal-source",
			HandleManifestComplete: true,
			Work: func(context.Context) error {
				started <- taskID
				<-finish
				return nil
			},
		}); err != nil {
			t.Fatalf("enqueue %s: %v", taskID, err)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := c.DispatchNext(); err != nil {
			t.Fatalf("dispatch normal work %d: %v", i, err)
		}
	}
	for i := 0; i < 3; i++ {
		<-started
	}
	if _, err := c.DispatchNext(); !errors.Is(err, ErrNoWorkerSlots) {
		t.Fatalf("normal work crossed the emergency floor: %v", err)
	}
	if got := c.Queue.Len(); got != 1 {
		t.Fatalf("blocked normal task was removed from queue: length=%d", got)
	}

	c.Source.RegisterSource("emergency-source", "emergency-group", "emergency-asn", "emergency-region")
	c.Source.Promote("emergency-source", source.TierEmergency, "emergency")
	if err := c.EnqueueWork(WorkRequest{
		TaskID:                 "emergency-1",
		SourceID:               "emergency-source",
		HandleManifestComplete: true,
		Work: func(context.Context) error {
			started <- "emergency-1"
			<-finish
			return nil
		},
	}); err != nil {
		t.Fatalf("enqueue emergency work: %v", err)
	}
	if id, err := c.DispatchNext(); err != nil || id != "emergency-1" {
		t.Fatalf("dispatch protected emergency work: id=%q err=%v", id, err)
	}
	if got := <-started; got != "emergency-1" {
		t.Fatalf("started task = %s, want emergency-1", got)
	}

	close(finish)
	for _, taskID := range []string{"normal-0", "normal-1", "normal-2", "emergency-1"} {
		waitForState(t, c, taskID, task.StateReleased)
	}
	if _, err := c.DispatchNext(); err != nil {
		t.Fatalf("normal work did not regain capacity after release: %v", err)
	}
	waitForState(t, c, "normal-3", task.StateReleased)
}

func TestAcceptReportIdempotencyDoesNotLeakQuota(t *testing.T) {
	c := newTestController(t)
	first, err := c.AcceptReport("report-source", "", []byte("payload"))
	if err != nil {
		t.Fatalf("accept report: %v", err)
	}
	second, err := c.AcceptReport("report-source", "  ", []byte("payload"))
	if err != nil {
		t.Fatalf("accept duplicate report: %v", err)
	}
	if first.ReportID != second.ReportID {
		t.Fatalf("blank nonce replay returned different receipts: %s != %s", first.ReportID, second.ReportID)
	}
	if got := c.Source.Get("report-source").Usage["global"]; got != 0 {
		t.Fatalf("receipt acceptance leaked source quota: usage=%d", got)
	}
	if _, err := c.AcceptReport("report-source", "", []byte("different")); err == nil {
		t.Fatal("conflicting duplicate report was accepted")
	}
}

func TestConcurrentDuplicateReportsShareReceiptAndDoNotLeakQuota(t *testing.T) {
	c := newTestController(t)
	const requests = 20
	receipts := make(chan string, requests)
	errs := make(chan error, requests)
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			receipt, err := c.AcceptReport("concurrent-report-source", "same-nonce", []byte("same-payload"))
			if err != nil {
				errs <- err
				return
			}
			receipts <- receipt.ReportID
		}()
	}
	wg.Wait()
	close(receipts)
	close(errs)
	for err := range errs {
		t.Errorf("accept concurrent duplicate: %v", err)
	}
	var reportID string
	for got := range receipts {
		if reportID == "" {
			reportID = got
		}
		if got != reportID {
			t.Errorf("duplicate returned receipt %s; expected %s", got, reportID)
		}
	}
	if got := c.IntakeLog.Count(); got != 1 {
		t.Fatalf("receipt count = %d, want 1", got)
	}
	if got := c.Source.Get("concurrent-report-source").Usage["global"]; got != 0 {
		t.Fatalf("concurrent receipt acceptance leaked source quota: usage=%d", got)
	}
}

func TestStandaloneLeaseReportsReservationAndReleaseErrors(t *testing.T) {
	c := newTestController(t)
	if _, err := c.LeaseTask("unknown-source-task", "unknown-source", "normal", []string{"resource"}); err == nil {
		t.Fatal("standalone lease accepted an unregistered source")
	}
	c.Source.RegisterSource("standalone-source", "standalone-group", "standalone-asn", "standalone-region")
	leaseRecord, err := c.LeaseTask("standalone-task", "standalone-source", "normal", []string{"resource"})
	if err != nil {
		t.Fatalf("create standalone lease: %v", err)
	}
	if err := c.ReleaseLease(leaseRecord.ID); err == nil {
		t.Fatal("standalone lease released with an unconfirmed handle")
	}
	if err := c.LeaseService.ConfirmHandle(leaseRecord.ID, "resource"); err != nil {
		t.Fatal(err)
	}
	if err := c.ReleaseLease(leaseRecord.ID); err != nil {
		t.Fatalf("release confirmed standalone lease: %v", err)
	}
	if got := c.Source.Get("standalone-source").Usage["global"]; got != 0 {
		t.Fatalf("standalone source charge remained after release: %d", got)
	}
}

func TestIncompleteEnqueueTaskAPIFailsExplicitly(t *testing.T) {
	c := newTestController(t)
	if err := c.EnqueueTask("task", "source"); err == nil {
		t.Fatal("incomplete queue-only API reported success")
	}
	if c.Queue.Len() != 0 {
		t.Fatal("rejected legacy enqueue changed queue state")
	}
}

func TestErrorReturningConstructorRejectsInvalidExecutionCapacity(t *testing.T) {
	if _, err := NewControllerWithHandleReleaseVerifier(0, 1, 1, 0, testHandleReleaseVerifier{}); err == nil {
		t.Fatal("constructor accepted zero execution capacity")
	}
}

func TestProbationaryAdmissionsReserveQuarantineCapacity(t *testing.T) {
	c, err := NewControllerWithHandleReleaseVerifier(8, 4, 2, 1, testHandleReleaseVerifier{})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RegisterPool("sacrificial", 2); err != nil {
		t.Fatal(err)
	}
	request := func(taskID string) WorkRequest {
		return WorkRequest{
			TaskID:                 taskID,
			SourceID:               taskID + "-source",
			HandleManifestComplete: true,
			Work:                   func(context.Context) error { return nil },
		}
	}

	if err := c.EnqueueWork(request("reserved-1")); err != nil {
		t.Fatalf("first probationary admission: %v", err)
	}
	if got := c.Quarantine.Stats().ReservedCount; got != 1 {
		t.Fatalf("quarantine reservations = %d, want 1", got)
	}
	if err := c.EnqueueWork(request("reserved-2")); err == nil {
		t.Fatal("second probationary admission exceeded reserved quarantine capacity")
	}
	if err := c.CancelTask("reserved-1"); err != nil {
		t.Fatalf("cancel first queued task: %v", err)
	}
	if got := c.Quarantine.Stats().ReservedCount; got != 0 {
		t.Fatalf("cancellation leaked quarantine reservation: %d", got)
	}
	if err := c.EnqueueWork(request("reserved-2")); err != nil {
		t.Fatalf("admission after reservation release: %v", err)
	}
	if err := c.CancelTask("reserved-2"); err != nil {
		t.Fatalf("cancel second queued task: %v", err)
	}
}
