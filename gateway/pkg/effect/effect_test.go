package effect

import (
	"testing"
	"time"
)

func TestOutboxClaimAndFence(t *testing.T) {
	ob := NewOutbox()
	e := ob.Add("effect-1", "scope-a", "payload")
	if e.Status != StatusPending {
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
}
