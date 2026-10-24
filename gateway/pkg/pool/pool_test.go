package pool

import "testing"

func TestPolicyKeepsEmergencyFloorReserved(t *testing.T) {
	p := NewPolicy(10, 3)
	if !p.IsReservedPath("emergency") {
		t.Fatal("emergency should be treated as a reserved path")
	}
	if got := p.ChoosePool("emergency", "normal"); got != "emergency" {
		t.Fatalf("emergency-tier source should prefer emergency lane, got %s", got)
	}
	if got := p.ChoosePool("probation", "normal"); got != "sacrificial" {
		t.Fatalf("probationary source should be pinned to sacrificial lane, got %s", got)
	}
}

func TestPolicyRejectsAggregateCapOverflow(t *testing.T) {
	p := NewPolicy(4, 1)
	p.RegisterPool("sacrificial", 2)
	p.RegisterPool("normal", 2)
	if err := p.Reserve("sacrificial", 2); err != nil {
		t.Fatalf("first reservation should succeed: %v", err)
	}
	if err := p.Reserve("normal", 3); err == nil {
		t.Fatal("aggregate pool cap should reject over-allocation")
	}
}

func TestPolicyPreservesEmergencyFloor(t *testing.T) {
	p := NewPolicy(10, 3)
	p.RegisterPool("sacrificial", 10)
	p.RegisterPool("normal", 10)
	p.RegisterPool("emergency", 10)

	if err := p.Reserve("normal", 7); err != nil {
		t.Fatalf("normal work up to the unreserved budget should succeed: %v", err)
	}
	if p.CanUse("sacrificial", 1) {
		t.Fatal("non-emergency work should not consume the emergency floor")
	}
	if err := p.Reserve("sacrificial", 1); err == nil {
		t.Fatal("reservation crossed the protected emergency floor")
	}
	if err := p.Reserve("emergency", 3); err != nil {
		t.Fatalf("emergency work should be able to consume the protected floor: %v", err)
	}
	if p.CanUse("emergency", 1) {
		t.Fatal("emergency work exceeded aggregate capacity")
	}
}

func TestPolicyEnforcesRegisteredPoolCapacity(t *testing.T) {
	p := NewPolicy(10, 0)
	p.RegisterPool("normal", 1)
	if err := p.Reserve("normal", 1); err != nil {
		t.Fatalf("reservation within the pool cap failed: %v", err)
	}
	if p.CanUse("normal", 1) {
		t.Fatal("pool capacity was exceeded")
	}
	if p.CanUse("unregistered", 1) {
		t.Fatal("unregistered pool was allowed")
	}
}
