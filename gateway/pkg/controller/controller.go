package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rescue-gateway/gateway/pkg/audit"
	"github.com/rescue-gateway/gateway/pkg/effect"
	"github.com/rescue-gateway/gateway/pkg/intake"
	"github.com/rescue-gateway/gateway/pkg/lease"
	"github.com/rescue-gateway/gateway/pkg/metrics"
	"github.com/rescue-gateway/gateway/pkg/pool"
	"github.com/rescue-gateway/gateway/pkg/quarantine"
	"github.com/rescue-gateway/gateway/pkg/queue"
	"github.com/rescue-gateway/gateway/pkg/source"
	"github.com/rescue-gateway/gateway/pkg/task"
	"github.com/rescue-gateway/gateway/pkg/worker"
)

// ErrNoWorkerSlots indicates that none of the queued tasks' pools can run work.
var ErrNoWorkerSlots = errors.New("no free worker slots")

// Controller is a compact orchestration layer that wires together the main
// architecture components: intake, admission, queueing, execution, and quarantine.
type Controller struct {
	mu            sync.Mutex
	dispatchMu    sync.Mutex
	IntakeLog     *intake.IntakeLog
	Source        *source.Registry
	LeaseService  *lease.Service
	Queue         *queue.Queue
	Quarantine    *quarantine.Pool
	Metrics       *metrics.Collector
	Outbox        *effect.Outbox
	Auditor       *audit.ReverseAuditor
	Pools         map[string]*worker.Pool
	tasks         map[string]*task.Task
	pending       map[string]workRequest
	taskPools     map[string]string
	workerHandles map[string]string
	released      map[string]bool
	nextHandleID  uint64
	blockedFront  uint64
}

type workRequest struct {
	pool            string
	scope           string
	deadline        time.Time
	requiredHandles []string
	work            worker.TaskFunc
}

// WorkRequest describes work admitted through the executable controller path.
// RequiredHandles represent resources acquired for the task that need separate
// confirmation before the task's accounting charge can be released.
type WorkRequest struct {
	TaskID                 string
	SourceID               string
	SourceGroup            string
	ASN                    string
	Region                 string
	Scope                  string
	Pool                   string
	Deadline               time.Time
	RequiredHandles        []string
	HandleManifestComplete bool
	Work                   worker.TaskFunc
}

func NewController(capacity int, maxPerSource int, qmax int) *Controller {
	return newController(capacity, maxPerSource, qmax, quarantine.NewPool(qmax))
}

// NewControllerWithEmergencyQuarantineReserve configures the minimum emergency
// quarantine allowance. Handle confirmations fail closed because no verifier
// is configured by this constructor.
func NewControllerWithEmergencyQuarantineReserve(capacity, maxPerSource, qmax, emergencyReserve int) (*Controller, error) {
	return NewControllerWithHandleReleaseVerifier(capacity, maxPerSource, qmax, emergencyReserve, nil)
}

// NewControllerWithHandleReleaseVerifier configures the trusted verifier used
// to authenticate evidence before a resource handle can be confirmed.
func NewControllerWithHandleReleaseVerifier(capacity, maxPerSource, qmax, emergencyReserve int, verifier quarantine.HandleReleaseVerifier) (*Controller, error) {
	qpool, err := quarantine.NewPoolWithVerifier(qmax, emergencyReserve, verifier)
	if err != nil {
		return nil, err
	}
	return newController(capacity, maxPerSource, qmax, qpool), nil
}

func newController(capacity, maxPerSource, qmax int, qpool *quarantine.Pool) *Controller {
	reg := source.NewRegistry(nil)
	policy := pool.NewPolicy(capacity, 1)
	policy.RegisterPool("sacrificial", capacity/2)
	policy.RegisterPool("normal", capacity/2)
	policy.RegisterPool("emergency", 1)
	reg.SetPoolPolicy(policy)
	return &Controller{
		IntakeLog:     intake.NewIntakeLog(true),
		Source:        reg,
		LeaseService:  lease.NewService(),
		Queue:         queue.NewQueue(capacity, maxPerSource),
		Quarantine:    qpool,
		Metrics:       metrics.NewCollector("rescue", fmt.Sprintf("run-%d", time.Now().UnixNano()), time.Now().UnixNano()),
		Outbox:        effect.NewOutbox(),
		Auditor:       audit.NewReverseAuditor(),
		Pools:         make(map[string]*worker.Pool),
		tasks:         make(map[string]*task.Task),
		pending:       make(map[string]workRequest),
		taskPools:     make(map[string]string),
		workerHandles: make(map[string]string),
		released:      make(map[string]bool),
	}
}

// RegisterPool adds a named execution pool.
func (c *Controller) RegisterPool(name string, capacity int) error {
	if name == "" {
		return fmt.Errorf("pool name must not be empty")
	}
	if capacity < 1 {
		return fmt.Errorf("pool %s capacity must be at least 1", name)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.Pools[name]; exists {
		return fmt.Errorf("pool %s is already registered", name)
	}
	c.Pools[name] = worker.NewPool(name, capacity, capacity*2)
	return nil
}

// EnqueueWork admits work against source capacity and places it in the bounded
// queue. New sources are confined to the sacrificial pool regardless of the
// requested pool; callers must register that pool before dispatch.
func (c *Controller) EnqueueWork(req WorkRequest) error {
	if req.TaskID == "" || req.SourceID == "" {
		return fmt.Errorf("task ID and source ID are required")
	}
	if req.Work == nil {
		return fmt.Errorf("task %s has no worker function", req.TaskID)
	}
	if !req.HandleManifestComplete {
		return fmt.Errorf("task %s handle manifest must be declared complete before admission", req.TaskID)
	}
	if !req.Deadline.IsZero() && !time.Now().Before(req.Deadline) {
		return fmt.Errorf("task %s deadline has already passed", req.TaskID)
	}
	if req.Scope == "" {
		req.Scope = req.TaskID
	}

	sourceState := c.Source.Get(req.SourceID)
	if sourceState == nil {
		group := req.SourceGroup
		if group == "" {
			group = req.SourceID
		}
		asn := req.ASN
		if asn == "" {
			asn = "unknown-asn"
		}
		region := req.Region
		if region == "" {
			region = "unknown-region"
		}
		c.Source.RegisterSource(req.SourceID, group, asn, region)
		sourceState = c.Source.Get(req.SourceID)
	}
	if c.Source.IsProbation(req.SourceID) {
		req.Pool = "sacrificial"
		if !c.Quarantine.CanAdd(req.Pool) {
			return fmt.Errorf("probationary admission paused: no quarantine capacity for pool %s", req.Pool)
		}
	} else {
		req.Pool = c.Source.ChoosePool(req.SourceID, req.Pool)
	}
	if req.Pool == "" {
		return fmt.Errorf("task %s has no execution pool", req.TaskID)
	}
	seenHandles := make(map[string]struct{}, len(req.RequiredHandles))
	for _, handle := range req.RequiredHandles {
		if handle == "" {
			return fmt.Errorf("task %s has an empty resource handle", req.TaskID)
		}
		if _, duplicate := seenHandles[handle]; duplicate {
			return fmt.Errorf("task %s has duplicate resource handle %s", req.TaskID, handle)
		}
		seenHandles[handle] = struct{}{}
	}

	c.mu.Lock()
	if _, exists := c.tasks[req.TaskID]; exists {
		c.mu.Unlock()
		return fmt.Errorf("task %s already exists", req.TaskID)
	}
	if c.Pools[req.Pool] == nil {
		c.mu.Unlock()
		return fmt.Errorf("execution pool %s is not registered", req.Pool)
	}
	c.mu.Unlock()

	if err := c.Source.Reserve(req.SourceID, 1); err != nil {
		return err
	}

	t := task.New(req.TaskID, "", req.SourceID, req.Deadline, 0)
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.tasks[req.TaskID]; exists {
		c.Source.Release(req.SourceID, 1)
		return fmt.Errorf("task %s already exists", req.TaskID)
	}
	if err := c.Queue.Enqueue(queue.Item{TaskID: req.TaskID, SourceID: req.SourceID, Deadline: req.Deadline}); err != nil {
		c.Source.Release(req.SourceID, 1)
		return err
	}
	c.tasks[req.TaskID] = t
	c.pending[req.TaskID] = workRequest{
		pool:            req.Pool,
		scope:           req.Scope,
		deadline:        req.Deadline,
		requiredHandles: append([]string(nil), req.RequiredHandles...),
		work:            req.Work,
	}
	c.taskPools[req.TaskID] = req.Pool
	return nil
}

// DispatchNext dequeues the next admitted task, creates its fenced lease before
// worker execution, and submits it to the selected bounded pool.
func (c *Controller) DispatchNext() (string, error) {
	c.dispatchMu.Lock()
	defer c.dispatchMu.Unlock()

	if err := c.sweepExpiredQueuedLocked(time.Now()); err != nil {
		return "", err
	}
	items := c.Queue.Snapshot()
	if len(items) == 0 {
		return "", fmt.Errorf("no queued work")
	}

	type dispatchCandidate struct {
		item queue.Item
		req  workRequest
		task *task.Task
		pool *worker.Pool
	}
	candidates := make([]dispatchCandidate, 0, len(items))
	c.mu.Lock()
	for _, item := range items {
		req, exists := c.pending[item.TaskID]
		if !exists {
			c.mu.Unlock()
			return "", fmt.Errorf("queued task %s has no controller request", item.TaskID)
		}
		t := c.tasks[item.TaskID]
		if t == nil {
			c.mu.Unlock()
			return "", fmt.Errorf("queued task %s has no controller task", item.TaskID)
		}
		pool := c.Pools[req.pool]
		if pool == nil {
			c.mu.Unlock()
			return "", fmt.Errorf("execution pool %s is not registered", req.pool)
		}
		candidates = append(candidates, dispatchCandidate{item: item, req: req, task: t, pool: pool})
	}
	c.mu.Unlock()

	freePools := make(map[string]bool)
	for _, candidate := range candidates {
		if _, checked := freePools[candidate.req.pool]; !checked {
			freePools[candidate.req.pool] = candidate.pool.FreeSlots() > 0
		}
	}
	hasFreePool := false
	for _, free := range freePools {
		if free {
			hasFreePool = true
			break
		}
	}
	if !hasFreePool {
		return "", fmt.Errorf("%w for queued execution pools", ErrNoWorkerSlots)
	}

	selected := -1
	for index, candidate := range candidates {
		if freePools[candidate.req.pool] {
			selected = index
			break
		}
	}
	if selected < 0 {
		return "", fmt.Errorf("%w for queued work", ErrNoWorkerSlots)
	}
	if selected > 0 && !freePools[candidates[0].req.pool] {
		atomic.AddUint64(&c.blockedFront, 1)
	}
	candidate := candidates[selected]
	item, req, t, pool := candidate.item, candidate.req, candidate.task, candidate.pool
	removed, ok := c.Queue.RemoveForDispatch(item.TaskID)
	if !ok || removed.TaskID != item.TaskID {
		return "", fmt.Errorf("queued task %s was removed before dispatch", item.TaskID)
	}
	c.mu.Lock()
	current, exists := c.pending[item.TaskID]
	if !exists || current.pool != req.pool || c.tasks[item.TaskID] != t {
		c.mu.Unlock()
		return "", fmt.Errorf("queued task %s changed before dispatch", item.TaskID)
	}
	delete(c.pending, item.TaskID)
	c.mu.Unlock()

	workerHandle := fmt.Sprintf("%s-worker-%d", item.TaskID, atomic.AddUint64(&c.nextHandleID, 1))
	handles := append([]string(nil), req.requiredHandles...)
	collision := true
	for collision {
		collision = false
		for _, existing := range handles {
			if existing == workerHandle {
				workerHandle = fmt.Sprintf("%s-worker-%d", item.TaskID, atomic.AddUint64(&c.nextHandleID, 1))
				collision = true
				break
			}
		}
	}
	handles = append(handles, workerHandle)
	seen := make(map[string]struct{}, len(handles))
	for _, handle := range handles {
		if handle == "" {
			return "", fmt.Errorf("task %s has an empty resource handle", item.TaskID)
		}
		if _, duplicate := seen[handle]; duplicate {
			return "", fmt.Errorf("task %s has duplicate resource handle %s", item.TaskID, handle)
		}
		seen[handle] = struct{}{}
	}

	l := c.LeaseService.Begin(item.TaskID, t.SourceID, req.scope, req.pool, handles, req.deadline)
	t.ConfigureLease(l.ID, req.pool, l.Fence.Inc)
	for _, handle := range req.requiredHandles {
		t.AddHandle(task.HandleExternalResource, handle)
	}
	t.AddHandle(task.HandleWorkerSlot, workerHandle)
	if err := t.TransitionTo(task.StateActive, "worker dispatch"); err != nil {
		return "", err
	}

	ctx := context.Background()
	cancel := func() {}
	if !req.deadline.IsZero() {
		ctx, cancel = context.WithDeadline(ctx, req.deadline)
	}
	c.mu.Lock()
	c.workerHandles[item.TaskID] = workerHandle
	c.mu.Unlock()
	work := func(workCtx context.Context) error {
		defer cancel()
		return req.work(workCtx)
	}
	if _, err := pool.SubmitWithHandle(item.TaskID, workerHandle, ctx, work); err != nil {
		cancel()
		c.mu.Lock()
		delete(c.workerHandles, item.TaskID)
		c.mu.Unlock()
		_ = c.LeaseService.ConfirmHandle(l.ID, workerHandle)
		_ = t.ConfirmHandle(workerHandle)
		submitErr := err
		if transitionErr := t.TransitionTo(task.StateExpired, "worker submission failed"); transitionErr != nil {
			return "", fmt.Errorf("submit worker: %v; transition task: %w", submitErr, transitionErr)
		}
		if err := c.finishOrQuarantine(t, l.ID, "worker submission failed", req.pool); err != nil {
			return "", err
		}
		return "", submitErr
	}
	return item.TaskID, nil
}

// BlockedFrontDispatches reports how often dispatch skipped a blocked queue
// head to serve a later task from a pool with available capacity.
func (c *Controller) BlockedFrontDispatches() uint64 {
	return atomic.LoadUint64(&c.blockedFront)
}

// QueueDepthsByPool returns queued task counts grouped by their selected pool.
func (c *Controller) QueueDepthsByPool() map[string]int {
	items := c.Queue.Snapshot()
	depths := make(map[string]int)
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, item := range items {
		req, ok := c.pending[item.TaskID]
		poolName := "unassigned"
		if ok {
			poolName = req.pool
		}
		depths[poolName]++
	}
	return depths
}

// SweepExpiredQueued removes queued requests whose deadlines elapsed, marks
// their tasks expired and released, and returns their source charges.
func (c *Controller) SweepExpiredQueued() error {
	c.dispatchMu.Lock()
	defer c.dispatchMu.Unlock()
	return c.sweepExpiredQueuedLocked(time.Now())
}

func (c *Controller) sweepExpiredQueuedLocked(now time.Time) error {
	expired := c.Queue.RemoveExpired(now)
	var firstErr error
	for _, item := range expired {
		c.mu.Lock()
		t := c.tasks[item.TaskID]
		_, hasRequest := c.pending[item.TaskID]
		delete(c.pending, item.TaskID)
		c.mu.Unlock()
		if !hasRequest {
			continue
		}
		if t == nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("expired queued task %s has no controller task", item.TaskID)
			}
			continue
		}
		if err := t.TransitionTo(task.StateExpired, "task deadline elapsed before dispatch"); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("expire queued task %s: %w", item.TaskID, err)
			}
			continue
		}
		if err := t.TransitionTo(task.StateReleased, "queued task expired and had no acquired worker handles"); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("release expired queued task %s: %w", item.TaskID, err)
			}
			continue
		}
		c.Source.Release(t.SourceID, 1)
	}
	return firstErr
}

// ReconcileCompletions consumes authoritative worker completion events. A
// completed worker confirms only its own slot; other handles stay charged.
func (c *Controller) ReconcileCompletions() error {
	if err := c.expireOverdueTasks(); err != nil {
		return err
	}

	c.mu.Lock()
	pools := make([]*worker.Pool, 0, len(c.Pools))
	for _, p := range c.Pools {
		pools = append(pools, p)
	}
	c.mu.Unlock()

	var firstErr error
	for _, p := range pools {
		for _, event := range p.PendingCompletions() {
			if err := c.reconcileCompletion(event); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			if err := p.AcknowledgeCompletion(event.TaskID, event.HandleID); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (c *Controller) expireOverdueTasks() error {
	c.mu.Lock()
	tasks := make([]*task.Task, 0, len(c.tasks))
	for _, t := range c.tasks {
		tasks = append(tasks, t)
	}
	c.mu.Unlock()

	now := time.Now()
	for _, t := range tasks {
		snapshot := t.Snapshot()
		if snapshot.State == task.StateActive && !snapshot.Deadline.IsZero() && !now.Before(snapshot.Deadline) {
			if err := t.TransitionTo(task.StateExpired, "task deadline elapsed; worker release still unconfirmed"); err != nil && t.GetState() == task.StateActive {
				return err
			}
		}
	}
	return nil
}

// WaitTask drives completion reconciliation until execution is released or
// quarantined, or the caller's context ends. A quarantined snapshot means
// external handles still need independent confirmation.
func (c *Controller) WaitTask(ctx context.Context, taskID string) (task.Snapshot, error) {
	if ctx == nil {
		return task.Snapshot{}, fmt.Errorf("wait context must not be nil")
	}
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := c.ReconcileCompletions(); err != nil {
			return task.Snapshot{}, err
		}
		snapshot, ok := c.TaskSnapshot(taskID)
		if !ok {
			return task.Snapshot{}, fmt.Errorf("task %s not found", taskID)
		}
		if snapshot.State == task.StateReleased || snapshot.State == task.StateQuarantined {
			return snapshot, nil
		}
		select {
		case <-ctx.Done():
			return task.Snapshot{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *Controller) reconcileCompletion(event worker.CompletionEvent) error {
	c.mu.Lock()
	t := c.tasks[event.TaskID]
	workerHandle := c.workerHandles[event.TaskID]
	poolName := c.taskPools[event.TaskID]
	c.mu.Unlock()
	if t == nil || workerHandle == "" {
		return fmt.Errorf("completion for unknown or undispatched task %s", event.TaskID)
	}
	if event.HandleID != workerHandle {
		return fmt.Errorf("completion for task %s reported unexpected worker handle %s", event.TaskID, event.HandleID)
	}

	if err := c.LeaseService.ConfirmHandle(t.LeaseIdentity(), workerHandle); err != nil {
		return err
	}
	if err := t.ConfirmHandle(workerHandle); err != nil {
		return err
	}
	state := t.GetState()
	if state == task.StateActive || state == task.StateCancelling {
		next := task.StateCompleted
		reason := "worker completion confirmed"
		if event.TimedOut || state == task.StateCancelling {
			next = task.StateExpired
			reason = "worker stopped after deadline or cancellation"
		}
		if err := t.TransitionTo(next, reason); err != nil {
			return err
		}
	}

	if t.AllHandlesConfirmed() {
		if err := c.releaseTask(t); err != nil {
			return err
		}
	} else {
		reason := "worker stopped but required resource handles remain unconfirmed"
		if event.Err != nil {
			reason = fmt.Sprintf("%s: %v", reason, event.Err)
		}
		if err := c.finishOrQuarantine(t, t.LeaseIdentity(), reason, poolName); err != nil {
			return err
		}
	}
	c.mu.Lock()
	delete(c.workerHandles, event.TaskID)
	c.mu.Unlock()
	return nil
}

// ConfirmHandle authenticates release evidence before recording a manifest
// handle as confirmed. Worker handles remain completion-event-only.
func (c *Controller) ConfirmHandle(taskID, handleID string, evidence []byte) error {
	c.mu.Lock()
	t := c.tasks[taskID]
	c.mu.Unlock()
	if t == nil {
		return fmt.Errorf("task %s not found", taskID)
	}
	kind, exists := t.HandleKindFor(handleID)
	if !exists {
		return fmt.Errorf("handle %s is not in task %s manifest", handleID, taskID)
	}
	if kind == task.HandleWorkerSlot {
		return fmt.Errorf("worker handle %s can only be confirmed by its completion event", handleID)
	}
	if c.Quarantine.Contains(taskID) {
		if err := c.Quarantine.ConfirmHandle(taskID, handleID, evidence); err != nil {
			return err
		}
	} else if err := c.Quarantine.VerifyHandleRelease(taskID, handleID, evidence); err != nil {
		return err
	}
	if err := c.LeaseService.ConfirmHandle(t.LeaseIdentity(), handleID); err != nil {
		return err
	}
	if err := t.ConfirmHandle(handleID); err != nil {
		return err
	}
	if !t.AllHandlesConfirmed() {
		return nil
	}
	return c.releaseTask(t)
}

// CancelTask requests cancellation without releasing any charge. The worker
// slot remains held until a completion event confirms that the handler stopped.
func (c *Controller) CancelTask(taskID string) error {
	c.dispatchMu.Lock()
	defer c.dispatchMu.Unlock()

	c.mu.Lock()
	t := c.tasks[taskID]
	req, queued := c.pending[taskID]
	pool := c.Pools[req.pool]
	if queued && c.Queue.Remove(taskID) {
		delete(c.pending, taskID)
		c.mu.Unlock()
		if err := t.TransitionTo(task.StateReleased, "cancelled before dispatch"); err != nil {
			return err
		}
		c.Source.Release(t.SourceID, 1)
		return nil
	}
	c.mu.Unlock()
	if t == nil {
		return fmt.Errorf("task %s not found", taskID)
	}
	if pool == nil {
		c.mu.Lock()
		pool = c.Pools[c.taskPools[taskID]]
		c.mu.Unlock()
	}
	if pool == nil {
		return fmt.Errorf("execution pool for task %s is unavailable", taskID)
	}
	if err := t.TransitionTo(task.StateCancelling, "cancellation requested"); err != nil {
		return err
	}
	return pool.Cancel(taskID)
}

func (c *Controller) finishOrQuarantine(t *task.Task, leaseID, reason string, poolNames ...string) error {
	if t.AllHandlesConfirmed() {
		return c.releaseTask(t)
	}
	poolName := t.PoolName()
	if poolName == "" && len(poolNames) > 0 {
		poolName = poolNames[0]
	}
	unconfirmed := make([]string, 0)
	confirmed := make([]string, 0)
	for _, handle := range t.Snapshot().Handles {
		if handle.State != task.HandleConfirmed {
			unconfirmed = append(unconfirmed, handle.HandleID)
		} else {
			confirmed = append(confirmed, handle.HandleID)
		}
	}
	state := t.GetState()
	if state != task.StateQuarantined {
		if err := t.TransitionTo(task.StateQuarantined, reason); err != nil {
			return err
		}
	}
	if !c.Quarantine.Contains(t.ID) {
		if err := c.Quarantine.Add(quarantine.Entry{
			TaskID:                 t.ID,
			LeaseID:                leaseID,
			SourceID:               t.SourceID,
			Pool:                   poolName,
			Reason:                 reason,
			HandleManifestComplete: true,
			HandleIDs:              unconfirmed,
			ConfirmedHandleIDs:     confirmed,
		}); err != nil {
			return fmt.Errorf("task %s remains QUARANTINED and charged, but quarantine tracking failed: %w", t.ID, err)
		}
	}
	return nil
}

func (c *Controller) releaseTask(t *task.Task) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.released[t.ID] {
		return nil
	}
	if !t.AllHandlesConfirmed() {
		return fmt.Errorf("task %s has unconfirmed handles", t.ID)
	}
	if err := c.LeaseService.Release(t.LeaseIdentity()); err != nil {
		return err
	}
	if c.Quarantine.Contains(t.ID) {
		if err := c.Quarantine.Release(t.ID); err != nil {
			return err
		}
	}
	state := t.GetState()
	if state != task.StateReleased {
		if err := t.TransitionTo(task.StateReleased, "all resource handles confirmed released"); err != nil {
			return err
		}
	}
	c.released[t.ID] = true
	c.Source.Release(t.SourceID, 1)
	return nil
}

// TaskSnapshot returns a detached view of a task's lifecycle and manifest.
func (c *Controller) TaskSnapshot(taskID string) (task.Snapshot, bool) {
	c.mu.Lock()
	t, ok := c.tasks[taskID]
	c.mu.Unlock()
	if !ok {
		return task.Snapshot{}, false
	}
	return t.Snapshot(), true
}

// AcceptReport performs intake validation, source registration, and receipt issuance.
func (c *Controller) AcceptReport(sourceID, nonce string, payload []byte) (intake.Receipt, error) {
	if strings.TrimSpace(sourceID) == "" {
		sourceID = "anonymous"
	}
	if existing, ok := c.IntakeLog.ReceiptForKey(intake.IdempotencyScope(sourceID, nonce)); ok {
		expectedHash := intake.PayloadHash(payload)
		if expectedHash == existing.PayloadHash {
			return existing, nil
		}
		return intake.Receipt{}, fmt.Errorf("%w: key %s has conflicting payloads", intake.ErrConflict, intake.IdempotencyScope(sourceID, nonce))
	}
	if err := c.Source.CheckAdmission(sourceID, 1); err != nil {
		c.Source.RegisterSource(sourceID, sourceID, "unknown-asn", "unknown-region")
		if err := c.Source.CheckAdmission(sourceID, 1); err != nil {
			return intake.Receipt{}, err
		}
	}
	if err := c.Source.Reserve(sourceID, 1); err != nil {
		return intake.Receipt{}, err
	}

	r := intake.Report{
		ID:          fmt.Sprintf("report-%d", time.Now().UnixNano()),
		SourceKey:   sourceID,
		Nonce:       nonce,
		PayloadHash: intake.PayloadHash(payload),
		Payload:     append([]byte(nil), payload...),
		CreatedAt:   time.Now(),
	}
	receipt, err := c.IntakeLog.Accept(r)
	if err != nil {
		c.Source.Release(sourceID, 1)
		return intake.Receipt{}, err
	}
	return receipt, nil
}

// EnqueueTask records a task in the bounded queue and applies source accounting.
func (c *Controller) EnqueueTask(taskID, sourceID string) error {
	if err := c.Source.CheckAdmission(sourceID, 1); err != nil {
		return err
	}
	return c.Queue.Enqueue(queue.Item{TaskID: taskID, SourceID: sourceID, Deadline: time.Now().Add(5 * time.Second)})
}

// LeaseTask creates a lease and marks the source as holding capacity.
func (c *Controller) LeaseTask(taskID, sourceID, pool string, handles []string) *lease.Lease {
	c.Source.Reserve(sourceID, 1)
	return c.LeaseService.Begin(taskID, sourceID, "default-scope", pool, handles, time.Now().Add(30*time.Second))
}

// QuarantineTask adds a task to the quarantine pool. Callers must explicitly
// indicate whether the supplied handle set is complete; incomplete manifests
// remain quarantined and cannot be released.
func (c *Controller) QuarantineTask(taskID, leaseID, sourceID, pool, reason string, unconfirmed []string, manifestComplete bool) error {
	return c.Quarantine.Add(quarantine.Entry{
		TaskID:                 taskID,
		LeaseID:                leaseID,
		SourceID:               sourceID,
		Pool:                   pool,
		Reason:                 reason,
		HandleManifestComplete: manifestComplete,
		HandleIDs:              unconfirmed,
	})
}

// RecordRuntimeAudit registers observed runtime resources and reports any orphaned resources.
func (c *Controller) RecordRuntimeAudit(resources []audit.Resource) []audit.Orphan {
	return c.Auditor.Audit(resources)
}

// RegisterEffect adds a fenced effect to the outbox.
func (c *Controller) RegisterEffect(id, scope, payload string) *effect.Effect {
	return c.Outbox.Add(id, scope, payload)
}
