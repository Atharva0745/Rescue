package effect

import (
	"fmt"
	"strings"
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
	ClaimToken   uint64
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
		if e.Scope == scope && (e.Status == StatusClaimed || e.Status == StatusPending) {
			e.Status = StatusFailed
			clearClaim(e)
		}
	}
	return v
}

// Add registers a new effect in the outbox in PENDING state.
func (o *Outbox) Add(id, scope, payload string) (*Effect, error) {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(scope) == "" {
		return nil, fmt.Errorf("effect ID and scope are required")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if existing, ok := o.effects[id]; ok {
		if existing.Scope != scope || existing.Payload != payload {
			return nil, fmt.Errorf("effect ID %s is already registered with different content", id)
		}
		return cloneEffect(existing), nil
	}
	fence := o.fences[scope]
	if fence == 0 {
		fence = 1
		o.fences[scope] = fence
	}
	e := &Effect{ID: id, Scope: scope, Fence: fence, Payload: payload, Status: StatusPending}
	o.effects[id] = e
	return cloneEffect(e), nil
}

// Claim reserves an effect for a sender; a claim is valid only while pending or previous claim expired.
func (o *Outbox) Claim(id, claimant string, ttl time.Duration) (uint64, error) {
	if strings.TrimSpace(claimant) == "" {
		return 0, fmt.Errorf("effect claimant must not be empty")
	}
	if ttl <= 0 {
		return 0, fmt.Errorf("effect claim TTL must be positive")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	e, ok := o.effects[id]
	if !ok {
		return 0, fmt.Errorf("effect %s not found", id)
	}
	if e.Status == StatusClaimed && time.Now().Before(e.ClaimedUntil) {
		return 0, fmt.Errorf("effect %s already claimed", id)
	}
	if e.Status != StatusPending && e.Status != StatusClaimed {
		return 0, fmt.Errorf("effect %s cannot be claimed from state %s", id, e.Status)
	}
	if o.fences[e.Scope] != e.Fence {
		e.Status = StatusFailed
		clearClaim(e)
		return 0, fmt.Errorf("effect %s has a stale fence", id)
	}
	if e.ClaimToken == ^uint64(0) {
		return 0, fmt.Errorf("effect %s claim token exhausted", id)
	}
	now := time.Now()
	e.ClaimToken++
	e.Status = StatusClaimed
	e.ClaimedBy = claimant
	e.ClaimedAt = now
	e.ClaimedUntil = now.Add(ttl)
	return e.ClaimToken, nil
}

// CanSend verifies the claim is still current and the fence version has not changed.
func (o *Outbox) CanSend(id, claimant string, claimToken, fence uint64) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	e, ok := o.effects[id]
	return ok && validClaim(e, claimant, claimToken, fence, o.fences[e.Scope], time.Now())
}

// MarkSent marks the effect as completed and final.
func (o *Outbox) MarkSent(id, claimant string, claimToken, fence uint64) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	e, ok := o.effects[id]
	if !ok {
		return fmt.Errorf("effect %s not found", id)
	}
	if !validClaim(e, claimant, claimToken, fence, o.fences[e.Scope], time.Now()) {
		return fmt.Errorf("effect %s claim is stale or not owned by %s", id, claimant)
	}
	e.Status = StatusSent
	e.SentAt = time.Now()
	clearClaim(e)
	return nil
}

// MarkFailed marks a claim as failed.
func (o *Outbox) MarkFailed(id, claimant string, claimToken, fence uint64) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	e, ok := o.effects[id]
	if !ok {
		return fmt.Errorf("effect %s not found", id)
	}
	if !validClaim(e, claimant, claimToken, fence, o.fences[e.Scope], time.Now()) {
		return fmt.Errorf("effect %s claim is stale or not owned by %s", id, claimant)
	}
	e.Status = StatusFailed
	clearClaim(e)
	return nil
}

// Retry returns a failed effect to PENDING only while its original fence is current.
func (o *Outbox) Retry(id string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	e, ok := o.effects[id]
	if !ok {
		return fmt.Errorf("effect %s not found", id)
	}
	if e.Status != StatusFailed {
		return fmt.Errorf("effect %s cannot be retried from state %s", id, e.Status)
	}
	if o.fences[e.Scope] != e.Fence {
		return fmt.Errorf("effect %s has a stale fence", id)
	}
	e.Status = StatusPending
	clearClaim(e)
	return nil
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

func validClaim(e *Effect, claimant string, claimToken, fence, currentFence uint64, now time.Time) bool {
	return e.Status == StatusClaimed &&
		e.ClaimedBy == claimant &&
		e.ClaimToken == claimToken &&
		e.Fence == fence &&
		e.Fence == currentFence &&
		now.Before(e.ClaimedUntil)
}

func clearClaim(e *Effect) {
	e.ClaimedBy = ""
	e.ClaimedUntil = time.Time{}
	e.ClaimedAt = time.Time{}
}

func cloneEffect(e *Effect) *Effect {
	copyEffect := *e
	return &copyEffect
}
