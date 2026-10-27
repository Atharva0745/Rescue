package audit

import (
	"fmt"
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
	ResourceID      string
	Kind            string
	OwnerID         string
	ExpectedOwnerID string
	Reason          string
	ObservedAt      time.Time
}

type ownerRecord struct {
	ownerID  string
	released bool
}

// ReverseAuditor compares observed runtime resources against the manifest ledger.
type ReverseAuditor struct {
	mu          sync.RWMutex
	knownOwners map[string]ownerRecord
}

func NewReverseAuditor() *ReverseAuditor {
	return &ReverseAuditor{knownOwners: make(map[string]ownerRecord)}
}

func (a *ReverseAuditor) RegisterOwner(resourceID, ownerID string) error {
	return a.RegisterOwners([]string{resourceID}, ownerID)
}

// RegisterOwners reserves a set of handles for one owner without allowing a
// live handle to be silently reassigned.
func (a *ReverseAuditor) RegisterOwners(resourceIDs []string, ownerID string) error {
	if ownerID == "" {
		return fmt.Errorf("resource owner ID must not be empty")
	}
	seen := make(map[string]struct{}, len(resourceIDs))
	for _, resourceID := range resourceIDs {
		if resourceID == "" {
			return fmt.Errorf("resource ID must not be empty")
		}
		if _, exists := seen[resourceID]; exists {
			return fmt.Errorf("duplicate resource ID %s", resourceID)
		}
		seen[resourceID] = struct{}{}
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	for _, resourceID := range resourceIDs {
		record, exists := a.knownOwners[resourceID]
		if exists && !record.released && record.ownerID != ownerID {
			return fmt.Errorf("resource %s is still owned by %s", resourceID, record.ownerID)
		}
	}
	for _, resourceID := range resourceIDs {
		a.knownOwners[resourceID] = ownerRecord{ownerID: ownerID}
	}
	return nil
}

// MarkReleased records the ledger's release transition for a resource.
func (a *ReverseAuditor) MarkReleased(resourceID, ownerID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	record, exists := a.knownOwners[resourceID]
	if !exists {
		return fmt.Errorf("resource %s is not registered in the owner ledger", resourceID)
	}
	if record.ownerID != ownerID {
		return fmt.Errorf("resource %s belongs to %s, not %s", resourceID, record.ownerID, ownerID)
	}
	record.released = true
	a.knownOwners[resourceID] = record
	return nil
}

func (a *ReverseAuditor) Audit(resources []Resource) []Orphan {
	out := make([]Orphan, 0)
	a.mu.RLock()
	defer a.mu.RUnlock()
	observedAt := time.Now().UTC()
	for _, r := range resources {
		record, known := a.knownOwners[r.ID]
		reason := ""
		switch {
		case !known:
			reason = "resource is absent from the owner ledger"
		case r.OwnerID == "":
			reason = "observed resource has no owner"
		case record.ownerID != r.OwnerID:
			reason = "observed owner does not match the owner ledger"
		case record.released:
			reason = "resource is still observed after ledger release"
		}
		if reason != "" {
			out = append(out, Orphan{
				ResourceID:      r.ID,
				Kind:            r.Kind,
				OwnerID:         r.OwnerID,
				ExpectedOwnerID: record.ownerID,
				Reason:          reason,
				ObservedAt:      observedAt,
			})
		}
	}
	return out
}
