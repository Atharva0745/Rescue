package effect

import (
	"fmt"
	"sync"
	"time"
)

// Status values for a fenced outbox item.
type Status string

const (
	StatusPending Status = "PENDING"
	StatusClaimed Status = "CLAIMED"
	StatusSent    Status = "SENT"
	StatusFailed  Status = "FAILED"
)

// Effect is a provider effect with fence and claim state.
type Effect struct {
	ID           string
	Scope        string
	Fence        uint64
	Payload      string
	Status       Status
	ClaimedBy    string
	ClaimedUntil time.Time
	ClaimedAt    time.Time
	SentAt       time.Time
}

// Outbox stores effects attached to a scope and enforces fence-version semantics.
type Outbox struct {
	mu      sync.Mutex
	effects map[string]*Effect
	fences  map[string]uint64
}

func NewOutbox() *Outbox {
	return &Outbox{
		effects: make(map[string]*Effect),
		fences:  make(map[string]uint64),
	}
}

// NextFence returns the current fence version for a scope.
func (o *Outbox) NextFence(scope string) uint64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	v := o.fences[scope]
	if v == 0 {
		v = 1
	}
	o.fences[scope] = v
	return v
}

// BumpFence advances a scope's fence version and is used before a replacement lease is live.
func (o *Outbox) BumpFence(scope string) uint64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	v := o.fences[scope] + 1
	o.fences[scope] = v
	for _, e := range o.effects {
		if e.Scope == scope && e.Status == StatusClaimed {
			e.Status = StatusFailed
			e.Fence = v
		}
		if e.Scope == scope {
			e.Fence = v
		}
	}
	return v
}

// Add registers a new effect in the outbox in PENDING state.
func (o *Outbox) Add(id, scope, payload string) *Effect {
	o.mu.Lock()
	defer o.mu.Unlock()
	fence := o.fences[scope]
	if fence == 0 {
		fence = 1
		o.fences[scope] = fence
	}
	e := &Effect{ID: id, Scope: scope, Fence: fence, Payload: payload, Status: StatusPending}
	o.effects[id] = e
	return e
}

// Claim reserves an effect for a sender; a claim is valid only while pending or previous claim expired.
func (o *Outbox) Claim(id, claimant string, ttl time.Duration) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	e, ok := o.effects[id]
	if !ok {
		return fmt.Errorf("effect %s not found", id)
	}
	if e.Status == StatusClaimed && e.ClaimedUntil.After(time.Now()) {
		return fmt.Errorf("effect %s already claimed", id)
	}
	e.Status = StatusClaimed
	e.ClaimedBy = claimant
	e.ClaimedAt = time.Now()
	e.ClaimedUntil = time.Now().Add(ttl)
	return nil
}

// CanSend verifies the claim is still current and the fence version has not changed.
func (o *Outbox) CanSend(id string, fence uint64) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	e, ok := o.effects[id]
	if !ok || e.Status != StatusClaimed {
		return false
	}
	if e.Fence != fence {
		return false
	}
	return time.Now().Before(e.ClaimedUntil)
}

// MarkSent marks the effect as completed and final.
func (o *Outbox) MarkSent(id string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	e, ok := o.effects[id]
	if !ok {
		return
	}
	e.Status = StatusSent
	e.SentAt = time.Now()
}

// MarkFailed marks a claim as failed.
func (o *Outbox) MarkFailed(id string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	e, ok := o.effects[id]
	if !ok {
		return
	}
	e.Status = StatusFailed
}

// Snapshot enumerates all known effects.
func (o *Outbox) Snapshot() []Effect {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]Effect, 0, len(o.effects))
	for _, e := range o.effects {
		out = append(out, *e)
	}
	return out
}
