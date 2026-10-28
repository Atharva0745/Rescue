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

func TestPolicyEnforcesPoolCapacityAndEmergencyFloor(t *testing.T) {
	p := NewPolicy(8, 2)
	p.RegisterPool("sacrificial", 8)
	p.RegisterPool("normal", 8)
	p.RegisterPool("emergency", 2)

	if err := p.Reserve("normal", 6); err != nil {
		t.Fatalf("reserve up to non-emergency limit: %v", err)
	}
	if p.CanUse("sacrificial", 1) {
		t.Fatal("non-emergency pool consumed the reserved emergency floor")
	}
	if err := p.Reserve("emergency", 2); err != nil {
		t.Fatalf("emergency pool should be able to use its reserved floor: %v", err)
	}
	if p.CanUse("emergency", 1) {
		t.Fatal("aggregate pool capacity was exceeded")
	}
}

func TestPolicyEnforcesRegisteredPoolCapacity(t *testing.T) {
	p := NewPolicy(8, 0)
	p.RegisterPool("sacrificial", 2)
	if err := p.Reserve("sacrificial", 2); err != nil {
		t.Fatalf("reserve pool capacity: %v", err)
	}
	if err := p.Reserve("sacrificial", 1); err == nil {
		t.Fatal("reservation exceeded the registered pool capacity")
	}
	if p.CanUse("unregistered", 1) {
		t.Fatal("unregistered pool should not be usable")
	}
}
