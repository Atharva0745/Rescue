package audit

import (
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
	knownOwners map[string]string
}

func NewReverseAuditor() *ReverseAuditor {
	return &ReverseAuditor{knownOwners: make(map[string]string)}
}

func (a *ReverseAuditor) RegisterOwner(resourceID, ownerID string) {
	a.knownOwners[resourceID] = ownerID
}

func (a *ReverseAuditor) Audit(resources []Resource) []Orphan {
	out := make([]Orphan, 0)
	for _, r := range resources {
		if r.OwnerID == "" {
			out = append(out, Orphan{ResourceID: r.ID, Kind: r.Kind, ObservedAt: time.Now()})
			continue
		}
		if owner, ok := a.knownOwners[r.ID]; ok && owner != r.OwnerID {
			out = append(out, Orphan{ResourceID: r.ID, Kind: r.Kind, ObservedAt: time.Now()})
		}
	}
	return out
}
