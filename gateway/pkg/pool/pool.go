package pool

import (
	"fmt"
	"sync"
)

// Policy models the execution-pool division described by the RESCUE
// architecture: protected emergency work has a minimum reserved floor while
// untrusted/probationary work is confined to the sacrificial pool.
type Policy struct {
	mu             sync.Mutex
	capacity       int
	emergencyFloor int
	pools          map[string]int
	usage          map[string]int
}

// NewPolicy creates a pool policy with an aggregate cap and a minimum reserve
// for emergency traffic. The aggregate cap is enforced across all pools.
func NewPolicy(totalCapacity, emergencyFloor int) *Policy {
	if totalCapacity < 1 {
		totalCapacity = 1
	}
	if emergencyFloor < 0 {
		emergencyFloor = 0
	}
	if emergencyFloor > totalCapacity {
		emergencyFloor = totalCapacity
	}
	return &Policy{
		capacity:       totalCapacity,
		emergencyFloor: emergencyFloor,
		pools:          make(map[string]int),
		usage:          make(map[string]int),
	}
}

// RegisterPool adds a named pool and its configured capacity.
func (p *Policy) RegisterPool(name string, capacity int) {
	if name == "" {
		return
	}
	if capacity < 0 {
		capacity = 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pools[name] = capacity
}

// Capacity returns the configured capacity for a pool.
func (p *Policy) Capacity(name string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pools[name]
}

// TotalCapacity returns the aggregate capacity across all pools.
func (p *Policy) TotalCapacity() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.capacity
}

// EmergencyFloor returns the minimum protected floor reserved for emergency work.
func (p *Policy) EmergencyFloor() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.emergencyFloor
}

// CanUse reports whether a pool has capacity left under the aggregate cap.
func (p *Policy) CanUse(poolName string, requested int) bool {
	if requested < 0 {
		return false
	}
	if requested == 0 {
		return true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.canUseLocked(poolName, requested)
}

// Reserve records that a pool has accepted a task.
func (p *Policy) Reserve(poolName string, amount int) error {
	if amount < 0 {
		return fmt.Errorf("pool reservation amount must not be negative")
	}
	if amount == 0 {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.canUseLocked(poolName, amount) {
		return fmt.Errorf("pool %s capacity or emergency reserve exceeded: requested=%d total=%d", poolName, amount, p.capacity)
	}
	p.usage[poolName] += amount
	return nil
}

func (p *Policy) canUseLocked(poolName string, requested int) bool {
	if poolName == "" {
		return false
	}
	poolCapacity, registered := p.pools[poolName]
	if !registered || p.usage[poolName]+requested > poolCapacity {
		return false
	}

	used := 0
	nonEmergencyUsed := 0
	for name, value := range p.usage {
		used += value
		if name != "emergency" {
			nonEmergencyUsed += value
		}
	}
	if used+requested > p.capacity {
		return false
	}
	if poolName != "emergency" && nonEmergencyUsed+requested > p.capacity-p.emergencyFloor {
		return false
	}
	return true
}

// Release removes a reserved unit from the aggregate pool tracker.
func (p *Policy) Release(poolName string, amount int) {
	if amount <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.usage[poolName] <= 0 {
		delete(p.usage, poolName)
		return
	}
	if p.usage[poolName] <= amount {
		delete(p.usage, poolName)
		return
	}
	p.usage[poolName] -= amount
}

// IsReservedPath reports whether a pool is treated as protected emergency lane.
func (p *Policy) IsReservedPath(poolName string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return poolName == "emergency" && p.emergencyFloor > 0
}

// ChoosePool resolves a candidate pool assignment for a source.
func (p *Policy) ChoosePool(sourceTier, preferred string) string {
	if sourceTier == "probation" || preferred == "sacrificial" {
		return "sacrificial"
	}
	if sourceTier == "emergency" || preferred == "emergency" {
		return "emergency"
	}
	if preferred == "normal" {
		return "normal"
	}
	return "normal"
}
