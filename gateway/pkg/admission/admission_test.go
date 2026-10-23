package admission

import (
	"fmt"
	"testing"
	"time"

	"github.com/rescue-gateway/gateway/pkg/source"
)

type verifier struct{}

func (verifier) VerifyHandleReleased(taskID, handleID string, evidence []byte) error {
	if len(evidence) == 0 {
		return fmt.Errorf("missing evidence")
	}
	if string(evidence) != taskID+":"+handleID {
		return fmt.Errorf("evidence mismatch")
	}
	return nil
}

func TestAdmitCreatesLeaseAndTracksManifest(t *testing.T) {
	reg := source.NewRegistry(map[string]int{"group": 10, "asn": 10, "region": 10, "global": 10})
	svc := New(reg, verifier{})
	rec, err := svc.Admit("task-1", "src-1", "normal", "scope-1", time.Now().Add(time.Minute), []string{"db-1", "cache-1"})
	if err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	if rec.LeaseID == "" {
		t.Fatal("admission created without a lease identity")
	}
	if len(rec.Manifest) != 2 {
		t.Fatalf("expected 2 handles, got %d", len(rec.Manifest))
	}
	if reg.Get("src-1") == nil {
		t.Fatal("source was not registered")
	}
	if reg.Get("src-1").Usage["global"] != 1 {
		t.Fatalf("source charge should be held after admission, got %d", reg.Get("src-1").Usage["global"])
	}
}

func TestConfirmReleaseSettlesOnlyAfterAllHandlesAreConfirmed(t *testing.T) {
	reg := source.NewRegistry(map[string]int{"group": 10, "asn": 10, "region": 10, "global": 10})
	svc := New(reg, verifier{})
	rec, err := svc.Admit("task-2", "src-2", "sacrificial", "scope-2", time.Now().Add(time.Minute), []string{"db-2", "cache-2"})
	if err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	if err := svc.ConfirmRelease(rec.TaskID, "db-2", []byte("task-2:db-2")); err != nil {
		t.Fatalf("confirm first handle: %v", err)
	}
	if rec.State != "QUARANTINED" {
		t.Fatalf("partial confirmation should keep the record quarantined, got %s", rec.State)
	}
	if reg.Get("src-2").Usage["global"] != 1 {
		t.Fatal("source charge should remain held while manifests are unconfirmed")
	}
	if err := svc.ConfirmRelease(rec.TaskID, "cache-2", []byte("task-2:cache-2")); err != nil {
		t.Fatalf("confirm final handle: %v", err)
	}
	if rec.State != "SETTLED" {
		t.Fatalf("full confirmation should settle the lease, got %s", rec.State)
	}
	if reg.Get("src-2").Usage["global"] != 0 {
		t.Fatal("source charge was not released after final handle confirmation")
	}
}

func TestConfirmReleaseRejectsUnauthenticatedEvidence(t *testing.T) {
	reg := source.NewRegistry(map[string]int{"group": 10, "asn": 10, "region": 10, "global": 10})
	svc := New(reg, verifier{})
	rec, err := svc.Admit("task-3", "src-3", "normal", "scope-3", time.Now().Add(time.Minute), []string{"db-3"})
	if err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	if err := svc.ConfirmRelease(rec.TaskID, "db-3", []byte("bad-evidence")); err == nil {
		t.Fatal("expected verifier rejection for bad evidence")
	}
	if rec.State != "ADMITTED" {
		t.Fatalf("invalid evidence must not settle the record, got %s", rec.State)
	}
}
