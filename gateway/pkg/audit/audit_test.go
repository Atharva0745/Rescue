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
