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
