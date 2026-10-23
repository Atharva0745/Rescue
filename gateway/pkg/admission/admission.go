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
	if verifier == nil {
		verifier = noopVerifier{}
	}
	return &Service{
		Source:   reg,
		Leases:   lease.NewService(),
		Verifier: verifier,
		records:  make(map[string]*Record),
	}
}

type noopVerifier struct{}

func (noopVerifier) VerifyHandleReleased(taskID, handleID string, evidence []byte) error {
	if len(evidence) == 0 {
		return fmt.Errorf("no release evidence provided for %s/%s", taskID, handleID)
	}
	return nil
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

	manifest := make([]ManifestEntry, 0, len(handles))
	seen := make(map[string]struct{}, len(handles))
	for _, handleID := range handles {
		if strings.TrimSpace(handleID) == "" {
			s.Source.Release(sourceID, 1)
			return nil, fmt.Errorf("task %s contains an empty handle ID", taskID)
		}
		if _, exists := seen[handleID]; exists {
			s.Source.Release(sourceID, 1)
			return nil, fmt.Errorf("task %s has duplicate resource handle %s", taskID, handleID)
		}
		seen[handleID] = struct{}{}
		manifest = append(manifest, ManifestEntry{HandleID: handleID, Kind: "external_resource"})
	}

	lease := s.Leases.Begin(taskID, sourceID, scope, pool, handles, deadline)
	if lease == nil {
		s.Source.Release(sourceID, 1)
		return nil, fmt.Errorf("lease creation failed for task %s", taskID)
	}

	rec := &Record{
		TaskID:     taskID,
		SourceID:   sourceID,
		Pool:       pool,
		Scope:      scope,
		Deadline:   deadline,
		LeaseID:    lease.ID,
		Manifest:   append([]ManifestEntry(nil), manifest...),
		Confirmed:  make(map[string]bool, len(manifest)),
		State:      "ADMITTED",
		Lease:      lease,
		ChargeHeld: true,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.records[taskID]; exists {
		s.Source.Release(sourceID, 1)
		return nil, fmt.Errorf("task %s already exists in the admission ledger", taskID)
	}
	s.records[taskID] = rec
	return rec, nil
}

// ConfirmRelease verifies the evidence and settles the admission only when all
// required handles are independently confirmed released.
func (s *Service) ConfirmRelease(taskID, handleID string, evidence []byte) error {
	s.mu.Lock()
	rec, ok := s.records[taskID]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("task %s is not tracked by the admission service", taskID)
	}
	if rec.State == "SETTLED" {
		return nil
	}

	if err := s.Verifier.VerifyHandleReleased(taskID, handleID, evidence); err != nil {
		return err
	}
	if !rec.Contains(handleID) {
		return fmt.Errorf("handle %s is not in task %s manifest", handleID, taskID)
	}
	if rec.Confirmed[handleID] {
		return nil
	}
	if err := s.Leases.ConfirmHandle(rec.LeaseID, handleID); err != nil {
		return err
	}
	rec.Confirmed[handleID] = true
	allConfirmed := true
	for _, entry := range rec.Manifest {
		if !rec.Confirmed[entry.HandleID] {
			allConfirmed = false
			break
		}
	}
	if allConfirmed {
		rec.State = "SETTLED"
		rec.ChargeHeld = false
		if err := s.Leases.Release(rec.LeaseID); err != nil {
			return err
		}
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
		copyManifest := append([]ManifestEntry(nil), r.Manifest...)
		confirmed := make(map[string]bool, len(r.Confirmed))
		for k, v := range r.Confirmed {
			confirmed[k] = v
		}
		out = append(out, Record{
			TaskID:     r.TaskID,
			SourceID:   r.SourceID,
			Pool:       r.Pool,
			Scope:      r.Scope,
			Deadline:   r.Deadline,
			LeaseID:    r.LeaseID,
			Manifest:   copyManifest,
			Confirmed:  confirmed,
			State:      r.State,
			Lease:      r.Lease,
			ChargeHeld: r.ChargeHeld,
		})
	}
	return out
}

func (r *Record) Contains(handleID string) bool {
	for _, entry := range r.Manifest {
		if entry.HandleID == handleID {
			return true
		}
	}
	return false
}
