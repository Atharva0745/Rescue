package audit

import "testing"

func TestReverseAuditorFindsOrphans(t *testing.T) {
	a := NewReverseAuditor()
	a.RegisterOwner("res-1", "lease-1")
	resources := []Resource{{ID: "res-1", Kind: "cgroup", OwnerID: "lease-1"}, {ID: "res-2", Kind: "pg_backend", OwnerID: ""}}
	orphans := a.Audit(resources)
	if len(orphans) != 1 {
		t.Fatalf("expected 1 orphan, got %d", len(orphans))
	}
	if orphans[0].ResourceID != "res-2" {
		t.Fatalf("orphan mismatch: %s", orphans[0].ResourceID)
	}
}

func TestReverseAuditorFindsUnknownAndMismatchedOwners(t *testing.T) {
	a := NewReverseAuditor()
	a.RegisterOwner("known", "lease-1")
	resources := []Resource{
		{ID: "known", Kind: "cgroup", OwnerID: "lease-1"},
		{ID: "unknown", Kind: "pg_backend", OwnerID: "unregistered-lease"},
		{ID: "known", Kind: "socket", OwnerID: "wrong-lease"},
	}
	orphans := a.Audit(resources)
	if len(orphans) != 2 {
		t.Fatalf("expected unknown and mismatched resources to be reported, got %d: %+v", len(orphans), orphans)
	}
	if orphans[0].ObservedAt.IsZero() || orphans[1].ObservedAt.IsZero() {
		t.Fatal("orphan observations must include timestamps")
	}
}
