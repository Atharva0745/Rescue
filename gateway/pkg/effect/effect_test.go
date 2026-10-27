package effect

import (
	"testing"
	"time"
)

func TestOutboxClaimAndFence(t *testing.T) {
	ob := NewOutbox()
	if e := ob.Add("effect-1", "scope-a", "payload"); e.Status != StatusPending {
		t.Fatalf("expected pending, got %s", e.Status)
	}
	if err := ob.Claim("effect-1", "sender-1", time.Second); err != nil {
		t.Fatalf("claim failed: %v", err)
	}
	if !ob.CanSend("effect-1", 1) {
		t.Fatal("expected effect to be sendable")
	}
	ob.BumpFence("scope-a")
	if ob.CanSend("effect-1", 1) {
		t.Fatal("send should fail after fence bump")
	}
	if err := ob.MarkSent("effect-1", "sender-1", 1); err == nil {
		t.Fatal("stale effect commit should be rejected")
	}
	if got := ob.StaleWritesRejected(); got != 1 {
		t.Fatalf("stale write rejection count = %d, want 1", got)
	}
}

func TestOutboxCommitsOnlyWithCurrentActiveClaim(t *testing.T) {
	ob := NewOutbox()
	ob.Add("effect-current", "scope-current", "payload")
	if err := ob.Claim("effect-current", "sender-current", time.Second); err != nil {
		t.Fatalf("claim effect: %v", err)
	}
	if err := ob.MarkSent("effect-current", "other-sender", 1); err == nil {
		t.Fatal("commit by a different claimant should be rejected")
	}
	if err := ob.MarkSent("effect-current", "sender-current", 1); err != nil {
		t.Fatalf("commit with current claim and fence: %v", err)
	}
}

func TestOutboxAdvanceFenceInvalidatesClaimsAndRejectsRegression(t *testing.T) {
	ob := NewOutbox()
	ob.Add("effect-advance", "scope-advance", "payload")
	if err := ob.Claim("effect-advance", "sender", time.Second); err != nil {
		t.Fatalf("claim effect: %v", err)
	}
	if err := ob.AdvanceFence("scope-advance", 2); err != nil {
		t.Fatalf("advance fence: %v", err)
	}
	if ob.CanSend("effect-advance", 1) {
		t.Fatal("claim remained valid after fence advanced")
	}
	if err := ob.AdvanceFence("scope-advance", 1); err == nil {
		t.Fatal("fence regression should be rejected")
	}
}
