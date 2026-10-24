// Package quarantine implements bounded isolation for tasks that cannot
// be confirmed terminated.
//
// Architecture §5.4: if any required handle cannot be confirmed gone,
// keep its charge and mark the task QUARANTINED.
// BR-F09: emergency floor must not be charged for other tiers' quarantine.
//
// Quarantine is bounded: at qmax, new quarantine requests are rejected
// and the system reports capacity loss rather than hiding it.
package quarantine

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// Entry represents a quarantined task.
type Entry struct {
	TaskID                 string    `json:"task_id"`
	LeaseID                string    `json:"lease_id"`
	SourceID               string    `json:"source_id"`
	Pool                   string    `json:"pool"` // Which pool this was charged to
	QuarantinedAt          time.Time `json:"quarantined_at"`
	Reason                 string    `json:"reason"`
	HandleManifestComplete bool      `json:"handle_manifest_complete"`
	HandleIDs              []string  `json:"unconfirmed_handles"`
	ConfirmedHandleIDs     []string  `json:"confirmed_handles"`
}

// HandleReleaseVerifier authenticates evidence from the authoritative owner
// of a resource. Implementations must bind the evidence to both IDs and return
// nil only when that specific handle is confirmed released.
type HandleReleaseVerifier interface {
	VerifyHandleReleased(taskID, handleID string, evidence []byte) error
}

// Pool manages bounded quarantine slots.
type Pool struct {
	mu               sync.Mutex
	entries          map[string]Entry // taskID → entry
	reservations     map[string]string
	capacity         int // qmax
	emergencyReserve int
	verifier         HandleReleaseVerifier

	// Per-pool quarantine tracking (BR-F09).
	perPool                   map[string]int // pool name → quarantine count
	nonEmergencyCount         int
	nonEmergencyReservedCount int

	// Metrics
	totalAdded    int64
	totalReleased int64
	totalRejected int64 // Rejected because qmax reached
}

// NewPool creates a bounded quarantine pool.
func NewPool(capacity int) *Pool {
	if capacity < 1 {
		capacity = 1
	}
	reserve := 1
	if reserve > capacity {
		reserve = capacity
	}
	return newPool(capacity, reserve)
}

// NewPoolWithEmergencyReserve creates a quarantine pool with a minimum
// emergency allowance. Non-emergency entries cannot consume the reserve;
// emergency entries may also use otherwise-free shared capacity.
func NewPoolWithEmergencyReserve(capacity, emergencyReserve int) (*Pool, error) {
	return NewPoolWithVerifier(capacity, emergencyReserve, nil)
}

// NewPoolWithVerifier creates a bounded quarantine pool and configures the
// trusted verifier required to confirm resource release. A nil verifier is
// permitted for tracking but causes all release confirmations to fail closed.
func NewPoolWithVerifier(capacity, emergencyReserve int, verifier HandleReleaseVerifier) (*Pool, error) {
	if capacity < 1 {
		return nil, fmt.Errorf("quarantine capacity must be at least 1")
	}
	if emergencyReserve < 0 || emergencyReserve > capacity {
		return nil, fmt.Errorf("emergency reserve must be between 0 and capacity")
	}
	p := newPool(capacity, emergencyReserve)
	p.verifier = verifier
	return p, nil
}

func newPool(capacity, emergencyReserve int) *Pool {
	return &Pool{
		entries:          make(map[string]Entry),
		reservations:     make(map[string]string),
		capacity:         capacity,
		emergencyReserve: emergencyReserve,
		perPool:          make(map[string]int),
	}
}

// Add quarantines a task. Returns error if quarantine is full.
func (p *Pool) Add(entry Entry) error {
	if entry.TaskID == "" {
		return fmt.Errorf("quarantine task ID must not be empty")
	}
	entry = cloneEntry(entry)
	if err := validateEntryHandles(entry); err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if existing, exists := p.entries[entry.TaskID]; exists {
		if entriesEqual(existing, entry) {
			p.releaseReservationLocked(entry.TaskID)
			return nil
		}
		return fmt.Errorf("task %s already exists in quarantine with conflicting entry", entry.TaskID)
	}

	reservedPool, reserved := p.reservations[entry.TaskID]
	if reserved && reservedPool != entry.Pool {
		return fmt.Errorf("task %s reserved quarantine capacity for pool %s, not %s", entry.TaskID, reservedPool, entry.Pool)
	}
	if !reserved && !p.canAddLocked(entry.Pool) {
		p.totalRejected++
		return fmt.Errorf("quarantine capacity unavailable for pool %s (%d/%d total, %d/%d non-emergency): cannot quarantine task %s",
			entry.Pool, p.occupiedLocked(), p.capacity, p.nonEmergencyCount+p.nonEmergencyReservedCount,
			p.capacity-p.emergencyReserve, entry.TaskID)
	}

	if reserved {
		p.releaseReservationLocked(entry.TaskID)
	}
	entry.QuarantinedAt = time.Now().UTC()
	p.entries[entry.TaskID] = entry
	p.perPool[entry.Pool]++
	if entry.Pool != "emergency" {
		p.nonEmergencyCount++
	}
	p.totalAdded++

	return nil
}

// ReserveSlot guarantees quarantine capacity for a task before it is admitted.
// The reservation is transferred to Add or released when the task completes.
func (p *Pool) ReserveSlot(taskID, pool string) error {
	if taskID == "" || pool == "" {
		return fmt.Errorf("quarantine reservation task ID and pool are required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if entry, exists := p.entries[taskID]; exists {
		if entry.Pool == pool {
			return nil
		}
		return fmt.Errorf("task %s is already quarantined in pool %s", taskID, entry.Pool)
	}
	if reservedPool, exists := p.reservations[taskID]; exists {
		if reservedPool == pool {
			return nil
		}
		return fmt.Errorf("task %s already reserved quarantine capacity for pool %s", taskID, reservedPool)
	}
	if !p.canAddLocked(pool) {
		p.totalRejected++
		return fmt.Errorf("quarantine capacity unavailable for pool %s (%d/%d total, %d/%d non-emergency)",
			pool, p.occupiedLocked(), p.capacity, p.nonEmergencyCount+p.nonEmergencyReservedCount,
			p.capacity-p.emergencyReserve)
	}
	p.reservations[taskID] = pool
	if pool != "emergency" {
		p.nonEmergencyReservedCount++
	}
	return nil
}

// ReleaseReservation relinquishes quarantine capacity held for a task that did
// not need quarantine. It is safe to call after a reservation was transferred.
func (p *Pool) ReleaseReservation(taskID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.releaseReservationLocked(taskID)
}

func (p *Pool) releaseReservationLocked(taskID string) {
	pool, exists := p.reservations[taskID]
	if !exists {
		return
	}
	delete(p.reservations, taskID)
	if pool != "emergency" {
		p.nonEmergencyReservedCount--
	}
}

// VerifyHandleRelease authenticates evidence without changing quarantine
// state. It is used when a handle is confirmed before its task is quarantined.
func (p *Pool) VerifyHandleRelease(taskID, handleID string, evidence []byte) error {
	if handleID == "" {
		return fmt.Errorf("handle ID must not be empty")
	}
	if len(evidence) == 0 {
		return fmt.Errorf("release evidence must not be empty")
	}
	if p.verifier == nil {
		return fmt.Errorf("no trusted handle-release verifier is configured")
	}
	if err := p.verifier.VerifyHandleReleased(taskID, handleID, append([]byte(nil), evidence...)); err != nil {
		return fmt.Errorf("verify release of handle %s for task %s: %w", handleID, taskID, err)
	}
	return nil
}

// ConfirmHandle records a release only after the configured verifier accepts
// evidence from the authoritative resource owner.
func (p *Pool) ConfirmHandle(taskID, handleID string, evidence []byte) error {
	if handleID == "" {
		return fmt.Errorf("handle ID must not be empty")
	}
	p.mu.Lock()
	entry, ok := p.entries[taskID]
	if !ok {
		p.mu.Unlock()
		return fmt.Errorf("task %s not in quarantine", taskID)
	}
	for _, id := range entry.HandleIDs {
		if id == handleID {
			if !entry.HandleManifestComplete {
				p.mu.Unlock()
				return fmt.Errorf("task %s has an incomplete handle manifest", taskID)
			}
			p.mu.Unlock()
			if err := p.VerifyHandleRelease(taskID, handleID, evidence); err != nil {
				return err
			}
			p.mu.Lock()
			defer p.mu.Unlock()
			entry, ok = p.entries[taskID]
			if !ok {
				return fmt.Errorf("task %s not in quarantine", taskID)
			}
			for current, currentID := range entry.HandleIDs {
				if currentID != handleID {
					continue
				}
				entry.HandleIDs = append(entry.HandleIDs[:current], entry.HandleIDs[current+1:]...)
				entry.ConfirmedHandleIDs = append(entry.ConfirmedHandleIDs, handleID)
				p.entries[taskID] = entry
				return nil
			}
			for _, confirmedID := range entry.ConfirmedHandleIDs {
				if confirmedID == handleID {
					return nil
				}
			}
			return fmt.Errorf("handle %s is not unconfirmed for quarantined task %s", handleID, taskID)
		}
	}
	for _, id := range entry.ConfirmedHandleIDs {
		if id == handleID {
			p.mu.Unlock()
			return nil
		}
	}
	p.mu.Unlock()
	return fmt.Errorf("handle %s is not unconfirmed for quarantined task %s", handleID, taskID)
}

// Release removes a task only after its complete handle manifest has been
// confirmed and every listed handle is released.
func (p *Pool) Release(taskID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	entry, ok := p.entries[taskID]
	if !ok {
		return fmt.Errorf("task %s not in quarantine", taskID)
	}
	if !entry.HandleManifestComplete {
		return fmt.Errorf("task %s has an incomplete handle manifest", taskID)
	}
	if len(entry.HandleIDs) != 0 {
		return fmt.Errorf("task %s still has %d unconfirmed handles", taskID, len(entry.HandleIDs))
	}

	p.perPool[entry.Pool]--
	if p.perPool[entry.Pool] <= 0 {
		delete(p.perPool, entry.Pool)
	}
	delete(p.entries, taskID)
	if entry.Pool != "emergency" {
		p.nonEmergencyCount--
	}
	p.totalReleased++
	return nil
}

// Contains checks if a task is quarantined.
func (p *Pool) Contains(taskID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.entries[taskID]
	return ok
}

// Count returns current quarantine occupancy.
func (p *Pool) Count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.entries)
}

// Capacity returns the quarantine capacity (qmax).
func (p *Pool) Capacity() int {
	return p.capacity
}

// IsFull returns whether quarantine is at capacity.
func (p *Pool) IsFull() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.occupiedLocked() >= p.capacity
}

// CanAdd reports whether an entry from the given execution pool currently has
// quarantine capacity, including the emergency-only reserve rule.
func (p *Pool) CanAdd(pool string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.canAddLocked(pool)
}

func (p *Pool) canAddLocked(pool string) bool {
	if p.occupiedLocked() >= p.capacity {
		return false
	}
	if pool == "emergency" {
		return true
	}
	return p.nonEmergencyCount+p.nonEmergencyReservedCount < p.capacity-p.emergencyReserve
}

func (p *Pool) occupiedLocked() int {
	return len(p.entries) + len(p.reservations)
}

// PoolCount returns quarantine usage for a specific execution pool.
// Used to verify BR-F09: emergency floor not charged for other tiers' quarantine.
func (p *Pool) PoolCount(pool string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.perPool[pool]
}

// Entries returns a snapshot of all quarantined entries.
func (p *Pool) Entries() []Entry {
	p.mu.Lock()
	defer p.mu.Unlock()

	result := make([]Entry, 0, len(p.entries))
	for _, e := range p.entries {
		result = append(result, cloneEntry(e))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].TaskID < result[j].TaskID })
	return result
}

func cloneEntry(entry Entry) Entry {
	entry.HandleIDs = append([]string(nil), entry.HandleIDs...)
	entry.ConfirmedHandleIDs = append([]string(nil), entry.ConfirmedHandleIDs...)
	return entry
}

func validateEntryHandles(entry Entry) error {
	seen := make(map[string]struct{}, len(entry.HandleIDs)+len(entry.ConfirmedHandleIDs))
	for _, handles := range [][]string{entry.HandleIDs, entry.ConfirmedHandleIDs} {
		for _, handleID := range handles {
			if handleID == "" {
				return fmt.Errorf("quarantine handle ID must not be empty")
			}
			if _, exists := seen[handleID]; exists {
				return fmt.Errorf("duplicate quarantine handle ID %s", handleID)
			}
			seen[handleID] = struct{}{}
		}
	}
	return nil
}

func entriesEqual(a, b Entry) bool {
	if a.TaskID != b.TaskID || a.LeaseID != b.LeaseID ||
		a.SourceID != b.SourceID || a.Pool != b.Pool ||
		a.Reason != b.Reason || a.HandleManifestComplete != b.HandleManifestComplete ||
		len(a.HandleIDs) != len(b.HandleIDs) ||
		len(a.ConfirmedHandleIDs) != len(b.ConfirmedHandleIDs) {
		return false
	}
	for i := range a.HandleIDs {
		if a.HandleIDs[i] != b.HandleIDs[i] {
			return false
		}
	}
	for i := range a.ConfirmedHandleIDs {
		if a.ConfirmedHandleIDs[i] != b.ConfirmedHandleIDs[i] {
			return false
		}
	}
	return true
}

// Stats returns quarantine statistics.
type Stats struct {
	Count                     int            `json:"count"`
	ReservedCount             int            `json:"reserved_count"`
	Capacity                  int            `json:"capacity"`
	EmergencyReserve          int            `json:"emergency_reserve"`
	NonEmergencyCount         int            `json:"non_emergency_count"`
	NonEmergencyReservedCount int            `json:"non_emergency_reserved_count"`
	TotalAdded                int64          `json:"total_added"`
	TotalReleased             int64          `json:"total_released"`
	TotalRejected             int64          `json:"total_rejected"`
	PerPool                   map[string]int `json:"per_pool"`
}

func (p *Pool) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()

	perPool := make(map[string]int)
	for k, v := range p.perPool {
		perPool[k] = v
	}

	return Stats{
		Count:                     len(p.entries),
		ReservedCount:             len(p.reservations),
		Capacity:                  p.capacity,
		EmergencyReserve:          p.emergencyReserve,
		NonEmergencyCount:         p.nonEmergencyCount,
		NonEmergencyReservedCount: p.nonEmergencyReservedCount,
		TotalAdded:                p.totalAdded,
		TotalReleased:             p.totalReleased,
		TotalRejected:             p.totalRejected,
		PerPool:                   perPool,
	}
}
