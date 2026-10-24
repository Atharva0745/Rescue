package admission

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rescue-gateway/gateway/pkg/lease"
	"github.com/rescue-gateway/gateway/pkg/source"
)

// HandleReleaseVerifier authenticates evidence that a resource has been
// independently released before the lease can be settled.
type HandleReleaseVerifier interface {
	VerifyHandleReleased(taskID, handleID string, evidence []byte) error
}

// ManifestEntry records one resource handle that must be confirmed gone before
// the source charge can be released.
type ManifestEntry struct {
	HandleID string
	Kind     string
}

// Record is the admission ledger entry for a task.
type Record struct {
	TaskID     string
	SourceID   string
	Pool       string
	Scope      string
	Deadline   time.Time
	LeaseID    string
	Manifest   []ManifestEntry
	Confirmed  map[string]bool
	State      string
	Lease      *lease.Lease
	ChargeHeld bool
}

// Service is the explicit admission and lease component described by the RESCUE
// architecture. It enforces source quotas before admission and only settles a
// lease after every handle in the manifest has been independently confirmed.
type Service struct {
	mu       sync.Mutex
	records  map[string]*Record
	Source   *source.Registry
	Leases   *lease.Service
	Verifier HandleReleaseVerifier
}

func New(reg *source.Registry, verifier HandleReleaseVerifier) *Service {
	if reg == nil {
		reg = source.NewRegistry(nil)
	}
	return &Service{
		Source:   reg,
		Leases:   lease.NewService(),
		Verifier: verifier,
		records:  make(map[string]*Record),
	}
}

// Admit creates a lease and records the manifest. New sources default to the
// sacrificial pool and their charge remains held until the lease is settled.
func (s *Service) Admit(taskID, sourceID, pool, scope string, deadline time.Time, handles []string) (*Record, error) {
	if strings.TrimSpace(taskID) == "" {
		return nil, fmt.Errorf("task ID is required")
	}
	if strings.TrimSpace(sourceID) == "" {
		return nil, fmt.Errorf("source ID is required")
	}
	if scope == "" {
		scope = "default-scope"
	}
	if pool == "" {
		pool = "sacrificial"
	}
	if len(handles) == 0 {
		return nil, fmt.Errorf("task %s resource manifest must contain at least one handle", taskID)
	}
	manifest := make([]ManifestEntry, 0, len(handles))
	seen := make(map[string]struct{}, len(handles))
	for _, handleID := range handles {
		if strings.TrimSpace(handleID) == "" {
			return nil, fmt.Errorf("task %s contains an empty handle ID", taskID)
		}
		if _, exists := seen[handleID]; exists {
			return nil, fmt.Errorf("task %s has duplicate resource handle %s", taskID, handleID)
		}
		seen[handleID] = struct{}{}
		manifest = append(manifest, ManifestEntry{HandleID: handleID, Kind: "external_resource"})
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.records[taskID]; exists {
		return nil, fmt.Errorf("task %s already exists in the admission ledger", taskID)
	}
	if s.Source.Get(sourceID) == nil {
		s.Source.RegisterSource(sourceID, sourceID, "unknown-asn", "unknown-region")
	}
	if s.Source.IsProbation(sourceID) {
		pool = "sacrificial"
	}
	if err := s.Source.CheckAdmission(sourceID, 1); err != nil {
		return nil, err
	}
	if err := s.Source.Reserve(sourceID, 1); err != nil {
		return nil, err
	}
	leaseRecord, err := s.Leases.Begin(taskID, sourceID, scope, pool, handles, deadline)
	if err != nil {
		s.Source.Release(sourceID, 1)
		return nil, fmt.Errorf("lease creation failed for task %s: %w", taskID, err)
	}

	rec := &Record{
		TaskID:     taskID,
		SourceID:   sourceID,
		Pool:       pool,
		Scope:      scope,
		Deadline:   deadline,
		LeaseID:    leaseRecord.ID,
		Manifest:   append([]ManifestEntry(nil), manifest...),
		Confirmed:  make(map[string]bool, len(manifest)),
		State:      "ADMITTED",
		Lease:      leaseRecord,
		ChargeHeld: true,
	}
	s.records[taskID] = cloneRecord(rec)
	return cloneRecord(rec), nil
}

// ConfirmRelease verifies the evidence and settles the admission only when all
// required handles are independently confirmed released.
func (s *Service) ConfirmRelease(taskID, handleID string, evidence []byte) error {
	s.mu.Lock()
	rec, ok := s.records[taskID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("task %s is not tracked by the admission service", taskID)
	}
	if rec.State == "SETTLED" {
		s.mu.Unlock()
		return nil
	}
	if !rec.Contains(handleID) {
		s.mu.Unlock()
		return fmt.Errorf("handle %s is not in task %s manifest", handleID, taskID)
	}
	alreadyConfirmed := rec.Confirmed[handleID]
	verifier := s.Verifier
	s.mu.Unlock()

	if !alreadyConfirmed {
		if verifier == nil {
			return fmt.Errorf("no trusted handle-release verifier is configured")
		}
		if err := verifier.VerifyHandleReleased(taskID, handleID, append([]byte(nil), evidence...)); err != nil {
			return err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok = s.records[taskID]
	if !ok {
		return fmt.Errorf("task %s is not tracked by the admission service", taskID)
	}
	if rec.State == "SETTLED" {
		return nil
	}
	if !rec.Confirmed[handleID] {
		if err := s.Leases.ConfirmHandle(rec.LeaseID, handleID); err != nil {
			return err
		}
		rec.Confirmed[handleID] = true
	}
	allConfirmed := true
	for _, entry := range rec.Manifest {
		if !rec.Confirmed[entry.HandleID] {
			allConfirmed = false
			break
		}
	}
	if allConfirmed {
		if err := s.Leases.Release(rec.LeaseID); err != nil {
			return err
		}
		rec.State = "SETTLED"
		rec.ChargeHeld = false
		s.Source.Release(rec.SourceID, 1)
		return nil
	}
	rec.State = "QUARANTINED"
	rec.ChargeHeld = true
	return nil
}

// Snapshot returns a safe copy of the current admissions.
func (s *Service) Snapshot() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Record, 0, len(s.records))
	for _, r := range s.records {
		out = append(out, *cloneRecord(r))
	}
	return out
}

func cloneRecord(r *Record) *Record {
	copyRecord := *r
	copyRecord.Manifest = append([]ManifestEntry(nil), r.Manifest...)
	copyRecord.Confirmed = make(map[string]bool, len(r.Confirmed))
	for handleID, confirmed := range r.Confirmed {
		copyRecord.Confirmed[handleID] = confirmed
	}
	if r.Lease != nil {
		leaseCopy := *r.Lease
		leaseCopy.Handles = append([]string(nil), r.Lease.Handles...)
		leaseCopy.Confirmed = make(map[string]bool, len(r.Lease.Confirmed))
		for handleID, confirmed := range r.Lease.Confirmed {
			leaseCopy.Confirmed[handleID] = confirmed
		}
		copyRecord.Lease = &leaseCopy
	}
	return &copyRecord
}

func (r *Record) Contains(handleID string) bool {
	for _, entry := range r.Manifest {
		if entry.HandleID == handleID {
			return true
		}
	}
	return false
}
