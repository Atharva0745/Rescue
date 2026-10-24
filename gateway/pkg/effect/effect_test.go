package effect

import (
	"testing"
	"time"
)

func addEffect(t *testing.T, outbox *Outbox, id string) *Effect {
	t.Helper()
	effect, err := outbox.Add(id, "scope-a", "payload")
	if err != nil {
		t.Fatalf("add effect: %v", err)
	}
	return effect
}

func TestOutboxClaimAndFence(t *testing.T) {
	ob := NewOutbox()
	e := addEffect(t, ob, "effect-1")
	if e.Status != StatusPending {
		t.Fatalf("expected pending, got %s", e.Status)
	}
	token, err := ob.Claim("effect-1", "sender-1", time.Second)
	if err != nil {
		t.Fatalf("claim failed: %v", err)
	}
	if !ob.CanSend("effect-1", "sender-1", token, 1) {
		t.Fatal("expected current claim to be sendable")
	}
	ob.BumpFence("scope-a")
	if ob.CanSend("effect-1", "sender-1", token, 1) {
		t.Fatal("send should fail after fence bump")
	}
	if err := ob.MarkSent("effect-1", "sender-1", token, 1); err == nil {
		t.Fatal("stale claim completed after the fence changed")
	}
}

func TestOutboxDoesNotResendSentOrFailedEffects(t *testing.T) {
	ob := NewOutbox()
	addEffect(t, ob, "effect-1")
	token, err := ob.Claim("effect-1", "sender-1", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := ob.MarkSent("effect-1", "sender-1", token, 1); err != nil {
		t.Fatalf("mark sent: %v", err)
	}
	if _, err := ob.Claim("effect-1", "sender-2", time.Second); err == nil {
		t.Fatal("sent effect was claimable again")
	}

	addEffect(t, ob, "effect-2")
	token, err = ob.Claim("effect-2", "sender-1", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := ob.MarkFailed("effect-2", "sender-1", token, 1); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	if _, err := ob.Claim("effect-2", "sender-2", time.Second); err == nil {
		t.Fatal("failed effect was claimable without explicit retry")
	}
	if err := ob.Retry("effect-2"); err != nil {
		t.Fatalf("retry failed effect: %v", err)
	}
}

func TestOutboxRejectsDuplicateIDsAndStaleClaimTokens(t *testing.T) {
	ob := NewOutbox()
	e := addEffect(t, ob, "effect-1")
	e.Payload = "caller mutation"
	if got := ob.Snapshot()[0].Payload; got != "payload" {
		t.Fatalf("returned effect mutated outbox state: %q", got)
	}
	if _, err := ob.Add("effect-1", "scope-a", "different"); err == nil {
		t.Fatal("conflicting effect ID was accepted")
	}

	firstToken, err := ob.Claim("effect-1", "sender-1", 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	secondToken, err := ob.Claim("effect-1", "sender-1", time.Second)
	if err != nil {
		t.Fatalf("expired claim could not be reclaimed: %v", err)
	}
	if firstToken == secondToken {
		t.Fatal("claim token did not change on reclamation")
	}
	if ob.CanSend("effect-1", "sender-1", firstToken, 1) {
		t.Fatal("expired claim token remained valid after reclamation")
	}
	if err := ob.MarkSent("effect-1", "sender-1", firstToken, 1); err == nil {
		t.Fatal("stale claim token marked the effect sent")
	}
	if !ob.CanSend("effect-1", "sender-1", secondToken, 1) {
		t.Fatal("new claim token is not valid")
	}
}
