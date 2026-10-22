package lease

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// FenceVersion is the monotonically increasing version used when replacing a
// lease or effect scope. It follows the architecture's per-scope fence model.
type FenceVersion struct {
	Inc   uint64
	Epoch uint64
}

// Lease is a bounded execution lease with a manifest of handles that must be
// independently confirmed cleared before the task is released.
type Lease struct {
	ID        string
	TaskID    string
	SourceID  string
	Scope     string
	Pool      string
	State     string
	Epoch     uint64
	Fence     FenceVersion
	Handles   []string
	CreatedAt time.Time
	ExpiresAt time.Time
	Confirmed map[string]bool
}

// Service tracks lease epochs and per-scope fence versions.
type Service struct {
	mu     sync.Mutex
	leases map[string]*Lease
	fences map[string]FenceVersion
}

func NewService() *Service {
	return &Service{
		leases: make(map[string]*Lease),
		fences: make(map[string]FenceVersion),
	}
}

func (s *Service) ensureFence(scope string) FenceVersion {
	if v, ok := s.fences[scope]; ok {
		return v
	}
	v := FenceVersion{Inc: 1, Epoch: 0}
	s.fences[scope] = v
	return v
}

// Begin creates a new lease in the ACTIVE state and advances the scope fence in
// the same transaction before the lease is usable.
func (s *Service) Begin(taskID, sourceID, scope, pool string, handles []string, expiresAt time.Time) *Lease {
	s.mu.Lock()
	defer s.mu.Unlock()

	v := s.ensureFence(scope)
	v.Inc++
	v.Epoch = 0
	s.fences[scope] = v

	lease := &Lease{
		ID:        fmt.Sprintf("lease-%s-%d", scope, time.Now().UnixNano()),
		TaskID:    taskID,
		SourceID:  sourceID,
		Scope:     scope,
		Pool:      pool,
		State:     "ACTIVE",
		Epoch:     v.Inc,
		Fence:     v,
		Handles:   append([]string(nil), handles...),
		CreatedAt: time.Now(),
		ExpiresAt: expiresAt,
		Confirmed: make(map[string]bool),
	}
	s.leases[lease.ID] = lease
	return lease
}

// ConfirmHandle records independent confirmation that a handle has been released.
func (s *Service) ConfirmHandle(leaseID, handleID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	l, ok := s.leases[leaseID]
	if !ok {
		return fmt.Errorf("lease %s not found", leaseID)
	}
	for _, h := range l.Handles {
		if h == handleID {
			l.Confirmed[handleID] = true
			return nil
		}
	}
	return fmt.Errorf("handle %s not in lease %s manifest", handleID, leaseID)
}

// AllHandlesConfirmed returns true only if every handle in the lease was independently confirmed.
func (s *Service) AllHandlesConfirmed(leaseID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.leases[leaseID]
	if !ok {
		return false
	}
	for _, h := range l.Handles {
		if !l.Confirmed[h] {
			return false
		}
	}
	return true
}

// CurrentFence returns the latest fence value for a scope.
func (s *Service) CurrentFence(scope string) FenceVersion {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ensureFence(scope)
}

// Release marks a lease released when all handles have been confirmed.
func (s *Service) Release(leaseID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.leases[leaseID]
	if !ok {
		return fmt.Errorf("lease %s not found", leaseID)
	}
	if !s.AllHandlesConfirmedLocked(l) {
		return fmt.Errorf("lease %s still has unconfirmed handles", leaseID)
	}
	l.State = "RELEASED"
	return nil
}

func (s *Service) AllHandlesConfirmedLocked(l *Lease) bool {
	for _, h := range l.Handles {
		if !l.Confirmed[h] {
			return false
		}
	}
	return true
}

// Snapshot returns a sorted list of known leases for observability.
func (s *Service) Snapshot() []Lease {
	s.mu.Lock()
	defer s.mu.Unlock()
	leases := make([]Lease, 0, len(s.leases))
	for _, l := range s.leases {
		copyHandles := append([]string(nil), l.Handles...)
		confirmed := make(map[string]bool, len(l.Confirmed))
		for k, v := range l.Confirmed {
			confirmed[k] = v
		}
		leases = append(leases, Lease{
			ID:        l.ID,
			TaskID:    l.TaskID,
			SourceID:  l.SourceID,
			Scope:     l.Scope,
			Pool:      l.Pool,
			State:     l.State,
			Epoch:     l.Epoch,
			Fence:     l.Fence,
			Handles:   copyHandles,
			CreatedAt: l.CreatedAt,
			ExpiresAt: l.ExpiresAt,
			Confirmed: confirmed,
		})
	}
	sort.Slice(leases, func(i, j int) bool { return leases[i].CreatedAt.Before(leases[j].CreatedAt) })
	return leases
}
