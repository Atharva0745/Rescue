package audit

import (
	"sync"
	"time"
)

// Resource is a runtime resource discovered by the reverse auditor.
type Resource struct {
	ID      string
	Kind    string
	OwnerID string
}

// Orphan is a runtime resource with no manifest owner.
type Orphan struct {
	ResourceID string
	Kind       string
	ObservedAt time.Time
}

// ReverseAuditor compares observed runtime resources against the manifest ledger.
type ReverseAuditor struct {
	mu          sync.RWMutex
	knownOwners map[string]string
}

func NewReverseAuditor() *ReverseAuditor {
	return &ReverseAuditor{knownOwners: make(map[string]string)}
}

func (a *ReverseAuditor) RegisterOwner(resourceID, ownerID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.knownOwners[resourceID] = ownerID
}

func (a *ReverseAuditor) Audit(resources []Resource) []Orphan {
	a.mu.RLock()
	defer a.mu.RUnlock()

	out := make([]Orphan, 0)
	now := time.Now()
	for _, r := range resources {
		owner, known := a.knownOwners[r.ID]
		if r.ID == "" || r.OwnerID == "" || !known || owner != r.OwnerID {
			out = append(out, Orphan{ResourceID: r.ID, Kind: r.Kind, ObservedAt: now})
		}
	}
	return out
}
