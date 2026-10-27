package audit

import "testing"

func TestReverseAuditorFindsOrphans(t *testing.T) {
	a := NewReverseAuditor()
	a.RegisterOwner("res-1", "lease-1")
	resources := []Resource{
		{ID: "res-1", Kind: "cgroup", OwnerID: "lease-1"},
		{ID: "res-2", Kind: "pg_backend"},
		{ID: "res-3", Kind: "cgroup", OwnerID: "unknown-lease"},
		{ID: "res-1", Kind: "cgroup", OwnerID: "wrong-lease"},
	}
	orphans := a.Audit(resources)
	if len(orphans) != 3 {
		t.Fatalf("expected 3 orphan or mismatched observations, got %d", len(orphans))
	}
	if orphans[0].ResourceID != "res-2" {
		t.Fatalf("empty-owner orphan mismatch: %s", orphans[0].ResourceID)
	}
	if orphans[0].ExpectedOwnerID != "" || orphans[0].OwnerID != "" {
		t.Fatalf("unexpected owner data for unknown resource: %+v", orphans[0])
	}
	if orphans[1].ResourceID != "res-3" || orphans[2].ExpectedOwnerID != "lease-1" {
		t.Fatalf("unknown owner or owner mismatch was not reported: %+v", orphans)
	}
}

func TestReverseAuditorDetectsResourceAfterLedgerRelease(t *testing.T) {
	a := NewReverseAuditor()
	if err := a.RegisterOwner("res-1", "lease-1"); err != nil {
		t.Fatalf("register owner: %v", err)
	}
	if err := a.MarkReleased("res-1", "lease-1"); err != nil {
		t.Fatalf("mark resource released: %v", err)
	}
	orphans := a.Audit([]Resource{{ID: "res-1", Kind: "cgroup", OwnerID: "lease-1"}})
	if len(orphans) != 1 || orphans[0].Reason != "resource is still observed after ledger release" {
		t.Fatalf("live resource after ledger release was not detected: %+v", orphans)
	}
}

func TestReverseAuditorCannotReassignLiveResource(t *testing.T) {
	a := NewReverseAuditor()
	if err := a.RegisterOwner("res-1", "lease-1"); err != nil {
		t.Fatalf("register owner: %v", err)
	}
	if err := a.RegisterOwner("res-1", "lease-2"); err == nil {
		t.Fatal("live resource was reassigned to a different owner")
	}
}

func TestRegisterOwnersIsAtomicAndAllowsConfirmedReuse(t *testing.T) {
	a := NewReverseAuditor()
	if err := a.RegisterOwner("res-1", "lease-1"); err != nil {
		t.Fatalf("register first owner: %v", err)
	}
	if err := a.RegisterOwners([]string{"res-2", "res-1"}, "lease-2"); err == nil {
		t.Fatal("registration should reject a resource held by another live owner")
	}
	if findings := a.Audit([]Resource{{ID: "res-2", Kind: "cgroup", OwnerID: "lease-2"}}); len(findings) != 1 {
		t.Fatal("failed multi-resource reservation partially modified the owner ledger")
	}
	if err := a.MarkReleased("res-1", "lease-1"); err != nil {
		t.Fatalf("mark first owner released: %v", err)
	}
	if err := a.RegisterOwners([]string{"res-1", "res-2"}, "lease-2"); err != nil {
		t.Fatalf("reuse confirmed resources: %v", err)
	}
}
