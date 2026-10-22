package quarantine

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type testReleaseVerifier struct{}

func (testReleaseVerifier) VerifyHandleReleased(taskID, handleID string, evidence []byte) error {
	if string(evidence) != "released:"+taskID+":"+handleID {
		return fmt.Errorf("invalid release evidence")
	}
	return nil
}

func proof(taskID, handleID string) []byte {
	return []byte("released:" + taskID + ":" + handleID)
}

func entry(taskID, pool string, handles ...string) Entry {
	return Entry{
		TaskID:                 taskID,
		LeaseID:                "lease-" + taskID,
		SourceID:               "source-" + taskID,
		Pool:                   pool,
		QuarantinedAt:          time.Unix(100, 0),
		Reason:                 "unconfirmed release",
		HandleManifestComplete: true,
		HandleIDs:              handles,
	}
}

func TestEmergencyReserveCannotBeConsumedByOtherPools(t *testing.T) {
	p, err := NewPoolWithEmergencyReserve(3, 1)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	if err := p.Add(entry("sacrificial-1", "sacrificial", "h1")); err != nil {
		t.Fatalf("add first sacrificial task: %v", err)
	}
	if err := p.Add(entry("normal-1", "normal", "h2")); err != nil {
		t.Fatalf("add normal task: %v", err)
	}
	if p.CanAdd("normal") {
		t.Fatal("non-emergency pool can consume emergency-reserved capacity")
	}
	if !p.CanAdd("emergency") {
		t.Fatal("emergency pool cannot use its reserved capacity")
	}
	if err := p.Add(entry("emergency-1", "emergency", "h3")); err != nil {
		t.Fatalf("add emergency task into reserved capacity: %v", err)
	}

	if p.PoolCount("emergency") != 1 || p.Count() != 3 {
		t.Fatalf("unexpected counts: emergency=%d total=%d", p.PoolCount("emergency"), p.Count())
	}
	if p.CanAdd("emergency") {
		t.Fatal("emergency pool admitted beyond total quarantine capacity")
	}
}

func TestEmergencyEntriesMayUseSharedCapacity(t *testing.T) {
	p, err := NewPoolWithEmergencyReserve(3, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []Entry{
		entry("emergency-reserve", "emergency", "h1"),
		entry("normal-shared", "normal", "h2"),
		entry("emergency-shared", "emergency", "h3"),
	} {
		if err := p.Add(item); err != nil {
			t.Fatalf("add %s: %v", item.TaskID, err)
		}
	}
	if p.Count() != 3 || p.CanAdd("emergency") || p.CanAdd("normal") {
		t.Fatalf("unexpected capacity state: count=%d emergency=%v normal=%v",
			p.Count(), p.CanAdd("emergency"), p.CanAdd("normal"))
	}
}

func TestReleaseRequiresEveryHandleConfirmed(t *testing.T) {
	p, err := NewPoolWithVerifier(2, 1, testReleaseVerifier{})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Add(entry("task-1", "normal", "handle-a", "handle-b")); err != nil {
		t.Fatalf("add task: %v", err)
	}
	if err := p.Release("task-1"); err == nil {
		t.Fatal("release removed task with unconfirmed handles")
	}
	if !p.Contains("task-1") {
		t.Fatal("failed release removed quarantine entry")
	}
	if err := p.ConfirmHandle("task-1", "handle-a", proof("task-1", "handle-a")); err != nil {
		t.Fatalf("confirm first handle: %v", err)
	}
	if err := p.ConfirmHandle("task-1", "handle-a", proof("task-1", "handle-a")); err != nil {
		t.Fatalf("repeat confirmation should be idempotent: %v", err)
	}
	if err := p.Release("task-1"); err == nil {
		t.Fatal("release removed task while one handle remained unconfirmed")
	}
	if err := p.ConfirmHandle("task-1", "not-in-manifest", proof("task-1", "not-in-manifest")); err == nil {
		t.Fatal("confirmation accepted a handle absent from the quarantine manifest")
	}
	if err := p.ConfirmHandle("task-1", "handle-b", proof("task-1", "handle-b")); err != nil {
		t.Fatalf("confirm second handle: %v", err)
	}
	if err := p.Release("task-1"); err != nil {
		t.Fatalf("release after all confirmations: %v", err)
	}
}

func TestReleaseRequiresCompleteManifestEvenWhenEmpty(t *testing.T) {
	p, err := NewPoolWithEmergencyReserve(2, 0)
	if err != nil {
		t.Fatal(err)
	}
	incomplete := entry("unknown-manifest", "normal")
	incomplete.HandleManifestComplete = false
	if err := p.Add(incomplete); err != nil {
		t.Fatalf("add incomplete manifest: %v", err)
	}
	if err := p.Release("unknown-manifest"); err == nil {
		t.Fatal("released task with an unknown handle manifest")
	}

	complete := entry("no-handles", "normal")
	if len(complete.HandleIDs) != 0 || !complete.HandleManifestComplete {
		t.Fatal("test entry should represent a complete, empty manifest")
	}
	if err := p.Add(complete); err != nil {
		t.Fatalf("add complete empty manifest: %v", err)
	}
	if err := p.Release("no-handles"); err != nil {
		t.Fatalf("release task with a complete empty manifest: %v", err)
	}
}

func TestConfirmationRequiresTrustedVerifierAndValidEvidence(t *testing.T) {
	unverified := NewPool(2)
	if err := unverified.Add(entry("task-1", "normal", "handle-a")); err != nil {
		t.Fatal(err)
	}
	if err := unverified.ConfirmHandle("task-1", "handle-a", proof("task-1", "handle-a")); err == nil {
		t.Fatal("confirmation succeeded without a configured verifier")
	}
	if len(unverified.Entries()[0].HandleIDs) != 1 {
		t.Fatal("failed confirmation changed quarantine state")
	}

	verified, err := NewPoolWithVerifier(2, 1, testReleaseVerifier{})
	if err != nil {
		t.Fatal(err)
	}
	if err := verified.Add(entry("task-2", "normal", "handle-b")); err != nil {
		t.Fatal(err)
	}
	if err := verified.ConfirmHandle("task-2", "handle-b", []byte("forged")); err == nil {
		t.Fatal("invalid release evidence was accepted")
	}
	if len(verified.Entries()[0].HandleIDs) != 1 {
		t.Fatal("invalid evidence changed quarantine state")
	}
}

func TestAddAndEntriesDeepCopyHandleIDs(t *testing.T) {
	p := NewPool(2)
	handles := []string{"handle-a"}
	input := entry("task-1", "normal", handles...)
	input.HandleIDs = handles
	if err := p.Add(input); err != nil {
		t.Fatalf("add task: %v", err)
	}

	handles[0] = "mutated-input"
	snapshot := p.Entries()
	if len(snapshot) != 1 || snapshot[0].HandleIDs[0] != "handle-a" {
		t.Fatalf("stored entry changed through Add input alias: %#v", snapshot)
	}
	if snapshot[0].QuarantinedAt.Equal(input.QuarantinedAt) {
		t.Fatal("pool retained caller-supplied quarantine timestamp")
	}
	snapshot[0].HandleIDs[0] = "mutated-snapshot"
	if got := p.Entries()[0].HandleIDs[0]; got != "handle-a" {
		t.Fatalf("stored entry changed through Entries snapshot: %q", got)
	}
}

func TestConcurrentAddConfirmAndRelease(t *testing.T) {
	const count = 24
	p, err := NewPoolWithVerifier(count+1, 1, testReleaseVerifier{})
	if err != nil {
		t.Fatal(err)
	}

	var adders sync.WaitGroup
	for i := 0; i < count; i++ {
		taskID := fmt.Sprintf("task-%02d", i)
		adders.Add(2)
		for duplicate := 0; duplicate < 2; duplicate++ {
			go func() {
				defer adders.Done()
				if err := p.Add(entry(taskID, "normal", "handle-"+taskID)); err != nil {
					t.Errorf("concurrent add %s: %v", taskID, err)
				}
			}()
		}
	}
	adders.Wait()
	if p.Count() != count {
		t.Fatalf("quarantine count = %d, want %d", p.Count(), count)
	}

	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := 0; i < count; i++ {
		taskID := fmt.Sprintf("task-%02d", i)
		handleID := "handle-" + taskID
		workers.Add(2)
		go func() {
			defer workers.Done()
			<-start
			if err := p.ConfirmHandle(taskID, handleID, proof(taskID, handleID)); err != nil {
				t.Errorf("concurrent confirm %s: %v", taskID, err)
			}
		}()
		go func() {
			defer workers.Done()
			<-start
			for attempt := 0; attempt < 1000; attempt++ {
				err := p.Release(taskID)
				if err == nil {
					return
				}
				if !strings.Contains(err.Error(), "unconfirmed handles") {
					t.Errorf("concurrent release %s: %v", taskID, err)
					return
				}
				time.Sleep(time.Millisecond)
			}
			t.Errorf("concurrent release timed out for %s", taskID)
		}()
	}
	close(start)
	workers.Wait()
	if p.Count() != 0 {
		t.Fatalf("quarantine count after concurrent release = %d, want 0", p.Count())
	}
}

func TestDuplicateAddRequiresIdenticalEntry(t *testing.T) {
	p := NewPool(2)
	original := entry("task-1", "normal", "handle-a")
	if err := p.Add(original); err != nil {
		t.Fatalf("add original: %v", err)
	}
	if err := p.Add(original); err != nil {
		t.Fatalf("identical add should be idempotent: %v", err)
	}
	if p.Count() != 1 || p.Stats().TotalAdded != 1 {
		t.Fatal("identical duplicate changed quarantine occupancy or metrics")
	}

	conflict := original
	conflict.Pool = "emergency"
	if err := p.Add(conflict); err == nil {
		t.Fatal("conflicting duplicate task ID was accepted")
	}
	conflict = original
	conflict.HandleIDs = []string{"different-handle"}
	if err := p.Add(conflict); err == nil {
		t.Fatal("duplicate with conflicting handle manifest was accepted")
	}
	if p.PoolCount("normal") != 1 || p.PoolCount("emergency") != 0 {
		t.Fatal("conflicting duplicate mutated pool accounting")
	}
}

func TestEmergencyReserveConfigurationValidation(t *testing.T) {
	for _, config := range [][2]int{{0, 0}, {2, -1}, {2, 3}} {
		if _, err := NewPoolWithEmergencyReserve(config[0], config[1]); err == nil {
			t.Fatalf("expected invalid capacity/reserve %v to fail", config)
		}
	}
}
