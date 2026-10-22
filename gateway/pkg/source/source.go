package source

import (
	"fmt"
	"sync"

	"github.com/rescue-gateway/gateway/pkg/pool"
)

// Scope describes the hierarchical budget attribution used by the RESCUE
// architecture. This is intentionally coarse-grained and compatible with the
// research report's source-group → ASN/region → global caps.
type Scope struct {
	Group  string
	ASN    string
	Region string
}

// Tier is the assigned source quality tier.
type Tier string

const (
	TierProbation Tier = "probation"
	TierNormal    Tier = "normal"
	TierEmergency Tier = "emergency"
)

// SourceState tracks source-scoped admissions and associated quotas.
type SourceState struct {
	ID        string
	Scope     Scope
	Tier      Tier
	Pool      string
	Usage     map[string]int
	Caps      map[string]int
	Probation bool
}

// Registry stores quota and tier state for source IDs.
type Registry struct {
	mu          sync.RWMutex
	sources     map[string]*SourceState
	defaultCaps map[string]int
	usage       map[string]map[string]int
	poolPolicy  *pool.Policy
}

func NewRegistry(defaultCaps map[string]int) *Registry {
	caps := map[string]int{"group": 8, "asn": 16, "region": 32, "global": 64}
	for k, v := range defaultCaps {
		caps[k] = v
	}
	registry := &Registry{
		sources:     make(map[string]*SourceState),
		defaultCaps: caps,
		usage: map[string]map[string]int{
			"group":  make(map[string]int),
			"asn":    make(map[string]int),
			"region": make(map[string]int),
			"global": {"all": 0},
		},
		poolPolicy: pool.NewPolicy(64, 8),
	}
	registry.poolPolicy.RegisterPool("sacrificial", 16)
	registry.poolPolicy.RegisterPool("normal", 32)
	registry.poolPolicy.RegisterPool("emergency", 16)
	return registry
}

// RegisterSource adds a source and initializes probation if it is new.
func (r *Registry) RegisterSource(sourceID, group, asn, region string) *SourceState {
	r.mu.Lock()
	defer r.mu.Unlock()

	if s, ok := r.sources[sourceID]; ok {
		return cloneState(s)
	}
	state := &SourceState{
		ID:        sourceID,
		Scope:     Scope{Group: group, ASN: asn, Region: region},
		Tier:      TierProbation,
		Pool:      "sacrificial",
		Probation: true,
		Usage:     map[string]int{"global": 0, "region": 0, "asn": 0, "group": 0},
		Caps:      cloneCaps(r.defaultCaps),
	}
	r.sources[sourceID] = state
	return cloneState(state)
}

func (r *Registry) Get(sourceID string) *SourceState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, ok := r.sources[sourceID]
	if !ok {
		return nil
	}
	return cloneState(state)
}

// CheckAdmission verifies that the source can consume the requested amount at
// each budget scope without exceeding the registered caps.
func (r *Registry) CheckAdmission(sourceID string, requested int) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	state, ok := r.sources[sourceID]
	if !ok {
		return fmt.Errorf("source %s not registered", sourceID)
	}
	if requested < 0 {
		return fmt.Errorf("negative request for %s", sourceID)
	}

	for _, scope := range []string{"group", "asn", "region", "global"} {
		key := sourceScopeKey(state, scope)
		cap := state.Caps[scope]
		if cap <= 0 {
			cap = r.defaultCaps[scope]
		}
		used := r.usage[scope][key]
		if used+requested > cap {
			return fmt.Errorf("source %s exceeds %s cap: %d+%d > %d", sourceID, scope, used, requested, cap)
		}
	}
	return nil
}

// Reserve consumes capacity for a request and applies the same charge at each
// source-scoped budget level.
func (r *Registry) Reserve(sourceID string, requested int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ok := r.sources[sourceID]
	if !ok {
		return fmt.Errorf("source %s not registered", sourceID)
	}
	if requested < 0 {
		return fmt.Errorf("negative request for %s", sourceID)
	}
	for _, scope := range []string{"group", "asn", "region", "global"} {
		key := sourceScopeKey(state, scope)
		cap := state.Caps[scope]
		if cap <= 0 {
			cap = r.defaultCaps[scope]
		}
		used := r.usage[scope][key]
		if used+requested > cap {
			return fmt.Errorf("source %s exceeds %s cap: %d+%d > %d", sourceID, scope, used, requested, cap)
		}
	}
	for _, scope := range []string{"group", "asn", "region", "global"} {
		key := sourceScopeKey(state, scope)
		r.usage[scope][key] += requested
		state.Usage[scope] += requested
	}
	return nil
}

// Release returns capacity after a completed or failed task.
func (r *Registry) Release(sourceID string, released int) {
	if released <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ok := r.sources[sourceID]
	if !ok {
		return
	}
	for _, scope := range []string{"group", "asn", "region", "global"} {
		amount := released
		if amount > state.Usage[scope] {
			amount = state.Usage[scope]
		}
		key := sourceScopeKey(state, scope)
		if amount > r.usage[scope][key] {
			amount = r.usage[scope][key]
		}
		state.Usage[scope] -= amount
		r.usage[scope][key] -= amount
	}
}

func sourceScopeKey(state *SourceState, scope string) string {
	switch scope {
	case "group":
		return state.Scope.Group
	case "asn":
		return state.Scope.ASN + "\x00" + state.Scope.Region
	case "region":
		return state.Scope.Region
	default:
		return "all"
	}
}

func cloneCaps(caps map[string]int) map[string]int {
	clone := make(map[string]int, len(caps))
	for key, value := range caps {
		clone[key] = value
	}
	return clone
}

func cloneState(state *SourceState) *SourceState {
	copyState := *state
	copyState.Usage = cloneCaps(state.Usage)
	copyState.Caps = cloneCaps(state.Caps)
	return &copyState
}

// Promote moves a source out of probation after passing the registered criteria.
func (r *Registry) Promote(sourceID string, tier Tier, pool string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ok := r.sources[sourceID]
	if !ok {
		return
	}
	state.Tier = tier
	if pool == "" {
		if tier == TierEmergency {
			pool = "emergency"
		} else {
			pool = "normal"
		}
	}
	state.Pool = pool
	state.Probation = false
}

// SetPoolPolicy installs the execution-pool policy used to decide which pool a
// non-probation source may use and to enforce the aggregate pool caps.
func (r *Registry) SetPoolPolicy(policy *pool.Policy) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.poolPolicy = policy
}

// ChoosePool decides the execution pool for a source using the configured
// policy. Probationary sources are pinned to the sacrificial pool.
func (r *Registry) ChoosePool(sourceID string, preferred string) string {
	r.mu.RLock()
	state, ok := r.sources[sourceID]
	r.mu.RUnlock()
	if !ok {
		return preferred
	}
	if r.poolPolicy != nil {
		return r.poolPolicy.ChoosePool(string(state.Tier), preferred)
	}
	if state.Probation {
		return "sacrificial"
	}
	if preferred != "" {
		return preferred
	}
	return state.Pool
}

// IsProbation reports whether a source is still confined to the sacrificial pool.
func (r *Registry) IsProbation(sourceID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, ok := r.sources[sourceID]
	if !ok {
		return true
	}
	return state.Probation
}
