package controller

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rescue-gateway/gateway/pkg/audit"
	"github.com/rescue-gateway/gateway/pkg/source"
	"github.com/rescue-gateway/gateway/pkg/task"
	"github.com/rescue-gateway/gateway/pkg/worker/cell"
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

func TestRegisterPoolEnforcesAggregateWorkerBudget(t *testing.T) {
	c := NewController(3, 2, 3)
	if err := c.RegisterPool("normal", 2); err != nil {
		t.Fatalf("register first pool: %v", err)
	}
	if err := c.RegisterPool("sacrificial", 2); err == nil {
		t.Fatal("registered worker capacity exceeded the aggregate budget")
	}
	if got := len(c.Pools); got != 1 {
		t.Fatalf("failed pool registration changed controller state: pool count=%d", got)
	}
}

func TestEnqueueRequiresOneConfiguredExecutionBackend(t *testing.T) {
	c := newTestController(t)
	base := WorkRequest{
		TaskID:                 "execution-backend",
		SourceID:               "execution-backend-source",
		HandleManifestComplete: true,
	}
	if err := c.EnqueueWork(base); err == nil {
		t.Fatal("request without an execution backend was accepted")
	}
	base.Work = func(context.Context) error { return nil }
	base.Command = &cell.CommandSpec{Path: "/bin/true"}
	if err := c.EnqueueWork(base); err == nil {
		t.Fatal("request with two execution backends was accepted")
	}
	base.Work = nil
	if err := c.EnqueueWork(base); err == nil {
		t.Fatal("command request was accepted without a process-cell manager")
	}
}

func TestDispatchAdvancesEffectFenceBeforeWorkStarts(t *testing.T) {
	c := newTestController(t)
	effect := c.RegisterEffect("fenced-effect", "fenced-scope", "payload")
	if err := c.Outbox.Claim(effect.ID, "old-sender", time.Minute); err != nil {
		t.Fatalf("claim effect before replacement lease: %v", err)
	}
	if !c.Outbox.CanSend(effect.ID, effect.Fence) {
		t.Fatal("initial effect claim should be valid")
	}

	started := make(chan struct{})
	if err := c.EnqueueWork(WorkRequest{
		TaskID:                 "replacement-task",
		SourceID:               "replacement-source",
		Scope:                  "fenced-scope",
		HandleManifestComplete: true,
		Work: func(context.Context) error {
			close(started)
			return nil
		},
	}); err != nil {
		t.Fatalf("enqueue replacement work: %v", err)
	}
	if _, err := c.DispatchNext(); err != nil {
		t.Fatalf("dispatch replacement work: %v", err)
	}
	<-started
	if c.Outbox.CanSend(effect.ID, effect.Fence) {
		t.Fatal("replacement lease did not invalidate the old effect fence")
	}
	if err := c.Outbox.MarkSent(effect.ID, "old-sender", effect.Fence); err == nil {
		t.Fatal("stale effect write was accepted after replacement lease")
	}
}

func TestDispatchFailsClosedWhenLeaseFenceWouldRegress(t *testing.T) {
	c := newTestController(t)
	for range 3 {
		c.Outbox.BumpFence("diverged-scope")
	}
	var started atomic.Bool
	if err := c.EnqueueWork(WorkRequest{
		TaskID:                 "diverged-fence-task",
		SourceID:               "diverged-fence-source",
		Scope:                  "diverged-scope",
		HandleManifestComplete: true,
		Work: func(context.Context) error {
			started.Store(true)
			return nil
		},
	}); err != nil {
		t.Fatalf("enqueue work: %v", err)
	}
	if _, err := c.DispatchNext(); err == nil {
		t.Fatal("dispatch should fail when the lease fence would regress the effect fence")
	}
	snapshot, ok := c.TaskSnapshot("diverged-fence-task")
	if !ok || snapshot.State != task.StateReleased {
		t.Fatalf("unstarted task state = %v, want RELEASED", snapshot.State)
	}
	if started.Load() {
		t.Fatal("work started after effect fence synchronization failed")
	}
	if got := c.Source.Get("diverged-fence-source").Usage["global"]; got != 0 {
		t.Fatalf("source charge leaked after pre-start rejection: %d", got)
	}
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
	for _, handle := range snapshot.Handles {
		if handle.Kind != task.HandleWorkerSlot {
			continue
		}
		orphans := c.RecordRuntimeAudit([]audit.Resource{{
			ID:      handle.HandleID,
			Kind:    "worker_slot",
			OwnerID: snapshot.ID,
		}})
		if len(orphans) != 1 || orphans[0].Reason != "resource is still observed after ledger release" {
			t.Fatalf("auditor did not detect a resource still live after ledger release: %+v", orphans)
		}
		break
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
	c, err := NewControllerWithHandleReleaseVerifier(32, 32, 8, 1, testHandleReleaseVerifier{})
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
