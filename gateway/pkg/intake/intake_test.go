package intake

import (
	"testing"

	"github.com/rescue-gateway/gateway/pkg/source"
)

func TestAcceptRejectsConflictingPayloadForSameIdempotencyKey(t *testing.T) {
	log := NewIntakeLog(true)
	report := Report{ID: "r-1", SourceKey: "alice", Nonce: "nonce-1", Payload: []byte("hello")}
	if _, err := log.Accept(report); err != nil {
		t.Fatalf("first accept failed: %v", err)
	}
	if _, err := log.Accept(Report{ID: "r-2", SourceKey: "alice", Nonce: "nonce-1", Payload: []byte("world")}); err == nil {
		t.Fatal("expected conflict on same idempotency key with different payload")
	}
}

func TestAcceptReturnsStoredReceiptForRepeatRequest(t *testing.T) {
	log := NewIntakeLog(true)
	report := Report{ID: "r-1", SourceKey: "alice", Nonce: "nonce-2", Payload: []byte("hello")}
	first, err := log.Accept(report)
	if err != nil {
		t.Fatalf("first accept failed: %v", err)
	}
	second, err := log.Accept(Report{ID: "r-2", SourceKey: "alice", Nonce: "nonce-2", Payload: []byte("hello")})
	if err != nil {
		t.Fatalf("repeat accept failed: %v", err)
	}
	if first.ReportID != second.ReportID {
		t.Fatalf("expected same receipt for duplicate request; got %s and %s", first.ReportID, second.ReportID)
	}
}

func TestPolicyValidatesQuotasAndRegistersProbationarySource(t *testing.T) {
	reg := source.NewRegistry(map[string]int{"group": 1, "asn": 2, "region": 2, "global": 2})
	pol := NewPolicy(reg, true)
	if _, err := pol.ValidateRequest("device-a", "nonce", "group-a", "asn-a", "region-a", []byte("payload")); err != nil {
		t.Fatalf("initial validation failed: %v", err)
	}
	if !reg.IsProbation("device-a") {
		t.Fatal("new source should remain in probation until promoted")
	}
	if err := reg.Reserve("device-a", 1); err != nil {
		t.Fatalf("reserve should succeed for a newly registered source: %v", err)
	}
}
