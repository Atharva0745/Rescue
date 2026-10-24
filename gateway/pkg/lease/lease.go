package lease

import (
	"fmt"
	"sort"
	"strings"
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
	nextID uint64
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
func (s *Service) Begin(taskID, sourceID, scope, pool string, handles []string, expiresAt time.Time) (*Lease, error) {
	if strings.TrimSpace(taskID) == "" || strings.TrimSpace(sourceID) == "" || strings.TrimSpace(scope) == "" {
		return nil, fmt.Errorf("task ID, source ID, and lease scope are required")
	}
	seen := make(map[string]struct{}, len(handles))
	for _, handle := range handles {
		if strings.TrimSpace(handle) == "" {
			return nil, fmt.Errorf("lease handle ID must not be empty")
		}
		if _, exists := seen[handle]; exists {
			return nil, fmt.Errorf("duplicate lease handle %s", handle)
		}
		seen[handle] = struct{}{}
	}
	if len(handles) == 0 {
		return nil, fmt.Errorf("lease manifest must contain at least one handle")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	v := s.ensureFence(scope)
	v.Inc++
	v.Epoch = 0
	s.fences[scope] = v
	s.nextID++

	lease := &Lease{
		ID:        fmt.Sprintf("lease-%d", s.nextID),
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
	return cloneLease(lease), nil
}

// ConfirmHandle records independent confirmation that a handle has been released.
func (s *Service) ConfirmHandle(leaseID, handleID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	l, ok := s.leases[leaseID]
	if !ok {
		return fmt.Errorf("lease %s not found", leaseID)
	}
	if l.State != "ACTIVE" {
		return fmt.Errorf("lease %s is not active", leaseID)
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
	if l.State == "RELEASED" {
		return nil
	}
	if !s.allHandlesConfirmedLocked(l) {
		return fmt.Errorf("lease %s still has unconfirmed handles", leaseID)
	}
	l.State = "RELEASED"
	return nil
}

func (s *Service) allHandlesConfirmedLocked(l *Lease) bool {
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
		leases = append(leases, *cloneLease(l))
	}
	sort.Slice(leases, func(i, j int) bool {
		if leases[i].CreatedAt.Equal(leases[j].CreatedAt) {
			return leases[i].ID < leases[j].ID
		}
		return leases[i].CreatedAt.Before(leases[j].CreatedAt)
	})
	return leases
}

func cloneLease(l *Lease) *Lease {
	copyLease := *l
	copyLease.Handles = append([]string(nil), l.Handles...)
	copyLease.Confirmed = make(map[string]bool, len(l.Confirmed))
	for handle, confirmed := range l.Confirmed {
		copyLease.Confirmed[handle] = confirmed
	}
	return &copyLease
}
