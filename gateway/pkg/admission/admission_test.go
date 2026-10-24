package admission

import (
	"fmt"
	"sync"
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
	if state := svc.Snapshot()[0].State; state != "QUARANTINED" {
		t.Fatalf("partial confirmation should keep the record quarantined, got %s", state)
	}
	if reg.Get("src-2").Usage["global"] != 1 {
		t.Fatal("source charge should remain held while manifests are unconfirmed")
	}
	if err := svc.ConfirmRelease(rec.TaskID, "cache-2", []byte("task-2:cache-2")); err != nil {
		t.Fatalf("confirm final handle: %v", err)
	}
	if state := svc.Snapshot()[0].State; state != "SETTLED" {
		t.Fatalf("full confirmation should settle the lease, got %s", state)
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
	if state := svc.Snapshot()[0].State; state != "ADMITTED" {
		t.Fatalf("invalid evidence must not settle the record, got %s", state)
	}
}

func TestNilVerifierFailsClosed(t *testing.T) {
	reg := source.NewRegistry(map[string]int{"group": 10, "asn": 10, "region": 10, "global": 10})
	svc := New(reg, nil)
	rec, err := svc.Admit("task-4", "src-4", "normal", "scope-4", time.Now().Add(time.Minute), []string{"db-4"})
	if err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	if err := svc.ConfirmRelease(rec.TaskID, "db-4", []byte("task-4:db-4")); err == nil {
		t.Fatal("nil verifier accepted release evidence")
	}
	if state := svc.Snapshot()[0].State; state != "ADMITTED" || reg.Get("src-4").Usage["global"] != 1 {
		t.Fatal("failed verification changed the admission or released its charge")
	}
}

func TestAdmissionCopiesReturnedRecordsAndSerializesConfirmations(t *testing.T) {
	reg := source.NewRegistry(map[string]int{"group": 10, "asn": 10, "region": 10, "global": 10})
	svc := New(reg, verifier{})
	rec, err := svc.Admit("task-5", "src-5", "normal", "scope-5", time.Now().Add(time.Minute), []string{"db-5", "cache-5"})
	if err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	rec.State = "SETTLED"
	rec.Manifest[0].HandleID = "caller-mutated"
	rec.Lease.Handles[0] = "caller-mutated"

	var wg sync.WaitGroup
	for _, handleID := range []string{"db-5", "cache-5"} {
		wg.Add(1)
		go func(handleID string) {
			defer wg.Done()
			if err := svc.ConfirmRelease("task-5", handleID, []byte("task-5:"+handleID)); err != nil {
				t.Errorf("confirm %s: %v", handleID, err)
			}
		}(handleID)
	}
	wg.Wait()
	snapshot := svc.Snapshot()
	if len(snapshot) != 1 || snapshot[0].State != "SETTLED" {
		t.Fatalf("concurrent confirmations did not settle the admission: %+v", snapshot)
	}
	if reg.Get("src-5").Usage["global"] != 0 {
		t.Fatal("concurrent confirmation failed to release the source charge")
	}
}
