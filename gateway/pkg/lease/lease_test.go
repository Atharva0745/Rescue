package lease

import (
	"testing"
	"time"
)

func TestUnknownHandleCannotSatisfyReleaseManifest(t *testing.T) {
	s := NewService()
	l := s.Begin("task-1", "source-1", "scope-1", "sacrificial", []string{"worker-1"}, time.Now().Add(time.Second))

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
