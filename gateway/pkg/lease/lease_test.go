package lease

import (
	"testing"
	"time"
)

func TestUnknownHandleCannotSatisfyReleaseManifest(t *testing.T) {
	s := NewService()
	l, err := s.Begin("task-1", "source-1", "scope-1", "sacrificial", []string{"worker-1"}, time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}

	if err := s.ConfirmHandle(l.ID, "unowned-handle"); err == nil {
		t.Fatal("expected confirmation of a handle outside the manifest to fail")
	}
	if s.AllHandlesConfirmed(l.ID) {
		t.Fatal("unknown handle confirmation satisfied the lease manifest")
	}
	if err := s.Release(l.ID); err == nil {
		t.Fatal("lease released while its manifest handle was unconfirmed")
	}
	if err := s.ConfirmHandle(l.ID, "worker-1"); err != nil {
		t.Fatalf("confirm manifest handle: %v", err)
	}
	if err := s.Release(l.ID); err != nil {
		t.Fatalf("release after confirmation: %v", err)
	}
}

func TestBeginValidatesManifestAndReturnsDetachedLease(t *testing.T) {
	s := NewService()
	for _, handles := range [][]string{nil, {""}, {"same", "same"}} {
		if _, err := s.Begin("task-invalid", "source", "scope", "normal", handles, time.Time{}); err == nil {
			t.Fatalf("invalid handle manifest was accepted: %#v", handles)
		}
	}
	l, err := s.Begin("task-valid", "source", "scope", "normal", []string{"resource"}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	l.Handles[0] = "caller-mutated"
	l.Confirmed["resource"] = true
	if s.AllHandlesConfirmed(l.ID) {
		t.Fatal("caller mutation altered the internal lease")
	}
	if err := s.ConfirmHandle(l.ID, "resource"); err != nil {
		t.Fatal(err)
	}
	if err := s.Release(l.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmHandle(l.ID, "resource"); err == nil {
		t.Fatal("released lease accepted a later handle mutation")
	}
}
