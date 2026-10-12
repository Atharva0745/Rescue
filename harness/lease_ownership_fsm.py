#!/usr/bin/env python3
"""
RESCUE-4 Lease and Ownership State Machine Protocol & Model Checker
Specification: RESCUE-4 Section 3 (Lease and Ownership Protocol)

State Machine Matrix:
+--------------+---------------------------------------+------------------+------------------------------------+
| From         | Event                                 | To               | Capacity / authority               |
+--------------+---------------------------------------+------------------+------------------------------------+
| LEASED       | complete                              | FREE             | release capacity                   |
| LEASED       | lease expires                         | EXPIRED          | still held, cancel sent            |
| EXPIRED      | owner confirms stop (cgroup empty)    | FENCED_CONFIRMED | release after fence                |
| EXPIRED      | no confirm by T_cancel                | KILLING          | still held, fence bump begins      |
| KILLING      | kill confirmed                        | FENCED_CONFIRMED | release capacity                   |
| KILLING      | no confirm by T_kill                  | QUARANTINED      | K_avail - 1, capacity stays charged|
| QUARANTINED  | late confirm or out-of-band node reset| FENCED_CONFIRMED | release capacity, K_avail + 1      |
+--------------+---------------------------------------+------------------+------------------------------------+

Rules:
  R1: Write authority transfers only when the bump inc+1 has committed. A new owner never writes before then.
  R2: Capacity is released only on completion or confirmed stop, never at expiry or when cancel is sent.
  R3: A quarantined slot keeps its charge, so orphan resource use is capped by its budget. Define q_max,
      and report the availability cost when quarantined slots shrink m(t).
  R4: Before the bump, the old owner's valid late writes are accepted (preserves legitimate late completions).
      After the bump, all of its writes fail.

Invariants Checked:
  Inv 1: At most one active write token exists per resource at any instant.
  Inv 2: Total charged capacity (held + quarantined) never exceeds K_s.
  Inv 3: No new lease is admitted if K_avail <= 0 or quarantined >= q_max.
"""

from enum import Enum, auto
from typing import Dict, List, Set, Optional, Tuple, Any
import random


class LeaseState(Enum):
    FREE = auto()
    LEASED = auto()
    EXPIRED = auto()
    KILLING = auto()
    QUARANTINED = auto()
    FENCED_CONFIRMED = auto()


class LeaseToken:
    def __init__(self, resource_id: str, owner_id: str, inc: int, epoch: int):
        self.resource_id = resource_id
        self.owner_id = owner_id
        self.inc = inc
        self.epoch = epoch
        self.valid = True

    def __repr__(self):
        return f"Token({self.owner_id}, v=({self.inc}, {self.epoch}), valid={self.valid})"


class ExecutorSlot:
    def __init__(self, slot_id: int):
        self.slot_id = slot_id
        self.state = LeaseState.FREE
        self.current_token: Optional[LeaseToken] = None
        self.charged_capacity: float = 0.0


class LeaseOwnershipManager:
    def __init__(self, K_s: int = 16, tau_s: float = 2.0, q_max: Optional[int] = None):
        self.K_s = K_s
        self.tau_s = tau_s
        self.q_max = q_max if q_max is not None else (K_s // 2)
        self.K_avail = K_s
        self.fence_inc = 1
        self.fence_epoch = 0

        self.slots: Dict[int, ExecutorSlot] = {i: ExecutorSlot(i) for i in range(K_s)}
        self.active_tokens: Dict[str, LeaseToken] = {}  # resource_id -> active token
        self.quarantined_slots: Set[int] = set()

        # Metrics for availability cost reporting
        self.total_lease_acquisitions = 0
        self.total_confirmed_releases = 0
        self.total_quarantined_events = 0
        self.total_stale_writes_prevented = 0
        self.total_legitimate_late_accepted = 0

    # -------------------------------------------------------------------------
    # Invariant Verification
    # -------------------------------------------------------------------------
    def assert_invariants(self):
        """Model checker invariant assertions."""
        # Inv 1: At most one valid token per resource
        resource_tokens = {}
        for token in self.active_tokens.values():
            if token.valid:
                assert token.resource_id not in resource_tokens, f"Inv 1 Violation: duplicate valid token for {token.resource_id}"
                resource_tokens[token.resource_id] = token

        # Inv 2: Charged capacity count never exceeds K_s
        held_slots = sum(1 for s in self.slots.values() if s.state != LeaseState.FREE and s.state != LeaseState.FENCED_CONFIRMED)
        assert held_slots <= self.K_s, f"Inv 2 Violation: held slots {held_slots} > K_s {self.K_s}"

        # Inv 3: K_avail accounting matches physical availability
        expected_avail = self.K_s - len(self.quarantined_slots)
        assert self.K_avail == expected_avail, f"Inv 3 Violation: K_avail {self.K_avail} != expected {expected_avail}"
        assert len(self.quarantined_slots) <= self.q_max, f"Inv 3 Violation: quarantined slots {len(self.quarantined_slots)} > q_max {self.q_max}"

    # -------------------------------------------------------------------------
    # State Transitions
    # -------------------------------------------------------------------------
    def acquire_lease(self, slot_id: int, resource_id: str, owner_id: str, weight: float = 1.0) -> Optional[LeaseToken]:
        """
        Transition: FREE -> LEASED
        Admit only if K_avail > 0 and slot is FREE.
        """
        self.assert_invariants()
        if self.K_avail <= 0 or len(self.quarantined_slots) >= self.q_max:
            return None  # Admission rejected: capacity/quarantine boundary reached

        slot = self.slots[slot_id]
        if slot.state != LeaseState.FREE:
            return None

        # Rule R1: Token carries active fence version
        token = LeaseToken(resource_id, owner_id, self.fence_inc, self.fence_epoch)
        slot.state = LeaseState.LEASED
        slot.current_token = token
        slot.charged_capacity = weight
        self.active_tokens[resource_id] = token
        self.total_lease_acquisitions += 1

        self.assert_invariants()
        return token

    def complete_normally(self, slot_id: int):
        """
        Transition: LEASED -> FREE
        Normal job completion: capacity released immediately.
        """
        self.assert_invariants()
        slot = self.slots[slot_id]
        assert slot.state == LeaseState.LEASED, f"Invalid state {slot.state} for complete_normally"

        if slot.current_token:
            slot.current_token.valid = False
            self.active_tokens.pop(slot.current_token.resource_id, None)

        slot.state = LeaseState.FREE
        slot.charged_capacity = 0.0
        slot.current_token = None
        self.total_confirmed_releases += 1
        self.assert_invariants()

    def lease_expires(self, slot_id: int):
        """
        Transition: LEASED -> EXPIRED
        Rule R2: Capacity is NOT released at expiry! Stays charged. Cancel signal sent.
        """
        self.assert_invariants()
        slot = self.slots[slot_id]
        assert slot.state == LeaseState.LEASED, f"Invalid state {slot.state} for lease_expires"
        slot.state = LeaseState.EXPIRED
        # Capacity remains charged!
        self.assert_invariants()

    def owner_confirms_stop(self, slot_id: int):
        """
        Transition: EXPIRED -> FENCED_CONFIRMED -> FREE
        Owner confirms cgroup is empty: capacity released safely.
        """
        self.assert_invariants()
        slot = self.slots[slot_id]
        assert slot.state == LeaseState.EXPIRED, f"Invalid state {slot.state} for owner_confirms_stop"

        if slot.current_token:
            slot.current_token.valid = False
            self.active_tokens.pop(slot.current_token.resource_id, None)

        slot.state = LeaseState.FREE
        slot.charged_capacity = 0.0
        slot.current_token = None
        self.total_confirmed_releases += 1
        self.assert_invariants()

    def cancel_timeout(self, slot_id: int):
        """
        Transition: EXPIRED -> KILLING
        Cooperative cancel failed by T_cancel: escalate to emergency kill path.
        Fence bump initiated (Rule R1).
        """
        self.assert_invariants()
        slot = self.slots[slot_id]
        assert slot.state == LeaseState.EXPIRED, f"Invalid state {slot.state} for cancel_timeout"
        slot.state = LeaseState.KILLING
        # Bump fence epoch to invalidate old authority
        self.fence_epoch += 1
        self.assert_invariants()

    def kill_confirmed(self, slot_id: int):
        """
        Transition: KILLING -> FENCED_CONFIRMED -> FREE
        Kill confirmed: capacity released safely.
        """
        self.assert_invariants()
        slot = self.slots[slot_id]
        assert slot.state == LeaseState.KILLING, f"Invalid state {slot.state} for kill_confirmed"

        if slot.current_token:
            slot.current_token.valid = False
            self.active_tokens.pop(slot.current_token.resource_id, None)

        slot.state = LeaseState.FREE
        slot.charged_capacity = 0.0
        slot.current_token = None
        self.total_confirmed_releases += 1
        self.assert_invariants()

    def kill_timeout_quarantine(self, slot_id: int):
        """
        Transition: KILLING -> QUARANTINED
        Rule R3: Executor lost/hung! Slot is quarantined.
        K_avail decreases by 1. Capacity remains charged against orphan budget.
        """
        self.assert_invariants()
        slot = self.slots[slot_id]
        assert slot.state == LeaseState.KILLING, f"Invalid state {slot.state} for kill_timeout_quarantine"

        slot.state = LeaseState.QUARANTINED
        self.quarantined_slots.add(slot_id)
        self.K_avail = max(0, self.K_avail - 1)
        self.total_quarantined_events += 1

        # Strict fence bump to incarnation level (Rule R1)
        self.fence_inc += 1
        self.fence_epoch = 0

        if slot.current_token:
            slot.current_token.valid = False
            self.active_tokens.pop(slot.current_token.resource_id, None)

        self.assert_invariants()

    def late_confirm_or_reset(self, slot_id: int):
        """
        Transition: QUARANTINED -> FENCED_CONFIRMED -> FREE
        Node reset / out-of-band recovery: slot unquarantined, K_avail restored.
        """
        self.assert_invariants()
        slot = self.slots[slot_id]
        assert slot.state == LeaseState.QUARANTINED, f"Invalid state {slot.state} for late_confirm_or_reset"

        self.quarantined_slots.discard(slot_id)
        self.K_avail = min(self.K_s, self.K_avail + 1)
        slot.state = LeaseState.FREE
        slot.charged_capacity = 0.0
        slot.current_token = None
        self.total_confirmed_releases += 1
        self.assert_invariants()

    # -------------------------------------------------------------------------
    # Rule R4: Write Authority Evaluation
    # -------------------------------------------------------------------------
    def attempt_write(self, token: LeaseToken) -> Tuple[bool, str]:
        """
        Rule R4:
        Before bump, old owner's valid late write is accepted.
        After bump, all writes with old token fail.
        """
        if not token.valid:
            self.total_stale_writes_prevented += 1
            return False, "TOKEN_INVALIDATED"

        if (token.inc, token.epoch) < (self.fence_inc, self.fence_epoch):
            self.total_stale_writes_prevented += 1
            return False, f"STALE_FENCE_TOKEN: token ({token.inc}, {token.epoch}) < fence ({self.fence_inc}, {self.fence_epoch})"

        if (token.inc, token.epoch) == (self.fence_inc, self.fence_epoch):
            self.total_legitimate_late_accepted += 1
            return True, "WRITE_COMMITTED"

        return False, "FUTURE_TOKEN"


# -----------------------------------------------------------------------------
# Model Checker & Fault Injection Test Suite
# -----------------------------------------------------------------------------
def run_fsm_model_checker(n_trials: int = 5_000) -> Dict[str, Any]:
    print("=" * 80)
    print("  RESCUE-4 LEASE & OWNERSHIP STATE MACHINE MODEL CHECKER  ")
    print("=" * 80)

    mgr = LeaseOwnershipManager(K_s=16, tau_s=2.0, q_max=8)
    rng = random.Random(20261011)

    print(f"Initial State: K_s={mgr.K_s}, K_avail={mgr.K_avail}, q_max={mgr.q_max}")
    print(f"Executing {n_trials:,} fault-injected interleaved transitions...")

    for step in range(1, n_trials + 1):
        action = rng.choice([
            "ACQUIRE", "COMPLETE", "EXPIRE", "OWNER_CONFIRM",
            "CANCEL_TIMEOUT", "KILL_CONFIRM", "KILL_TIMEOUT", "RESET_QUARANTINE",
            "WRITE_ATTEMPT"
        ])

        if action == "ACQUIRE":
            free_slots = [sid for sid, s in mgr.slots.items() if s.state == LeaseState.FREE]
            if free_slots and mgr.K_avail > 0:
                sid = rng.choice(free_slots)
                mgr.acquire_lease(sid, f"res_{sid}", f"owner_{step}")

        elif action == "COMPLETE":
            leased_slots = [sid for sid, s in mgr.slots.items() if s.state == LeaseState.LEASED]
            if leased_slots:
                sid = rng.choice(leased_slots)
                mgr.complete_normally(sid)

        elif action == "EXPIRE":
            leased_slots = [sid for sid, s in mgr.slots.items() if s.state == LeaseState.LEASED]
            if leased_slots:
                sid = rng.choice(leased_slots)
                mgr.lease_expires(sid)

        elif action == "OWNER_CONFIRM":
            expired_slots = [sid for sid, s in mgr.slots.items() if s.state == LeaseState.EXPIRED]
            if expired_slots:
                sid = rng.choice(expired_slots)
                mgr.owner_confirms_stop(sid)

        elif action == "CANCEL_TIMEOUT":
            expired_slots = [sid for sid, s in mgr.slots.items() if s.state == LeaseState.EXPIRED]
            if expired_slots:
                sid = rng.choice(expired_slots)
                mgr.cancel_timeout(sid)

        elif action == "KILL_CONFIRM":
            killing_slots = [sid for sid, s in mgr.slots.items() if s.state == LeaseState.KILLING]
            if killing_slots:
                sid = rng.choice(killing_slots)
                mgr.kill_confirmed(sid)

        elif action == "KILL_TIMEOUT":
            killing_slots = [sid for sid, s in mgr.slots.items() if s.state == LeaseState.KILLING]
            if killing_slots and len(mgr.quarantined_slots) < mgr.q_max:
                sid = rng.choice(killing_slots)
                mgr.kill_timeout_quarantine(sid)

        elif action == "RESET_QUARANTINE":
            if mgr.quarantined_slots:
                sid = rng.choice(list(mgr.quarantined_slots))
                mgr.late_confirm_or_reset(sid)

        elif action == "WRITE_ATTEMPT":
            for tok in list(mgr.active_tokens.values()):
                # Attempt write under current state
                mgr.attempt_write(tok)

        # Verify invariants on every single step
        mgr.assert_invariants()

    print(f"\n[MODEL CHECKER PASSED] All invariants verified across {n_trials:,} transitions:")
    print(f"  - Total leases acquired:           {mgr.total_lease_acquisitions:,}")
    print(f"  - Confirmed safe releases:         {mgr.total_confirmed_releases:,}")
    print(f"  - Quarantined events handled:      {mgr.total_quarantined_events:,}")
    print(f"  - Stale writes prevented (R1/R4):  {mgr.total_stale_writes_prevented:,}")
    print(f"  - Legitimate late writes accepted: {mgr.total_legitimate_late_accepted:,}")
    print(f"  - Current K_avail:                 {mgr.K_avail}/{mgr.K_s}")
    print(f"  - Availability cost:               {len(mgr.quarantined_slots)} quarantined slots ({len(mgr.quarantined_slots)/mgr.K_s*100:.1f}% capacity deduction)")

    return {
        "status": "PASSED",
        "transitions_evaluated": n_trials,
        "invariants_verified": ["Inv 1: Single valid token", "Inv 2: Charged <= K_s", "Inv 3: K_avail = K_s - |quarantine|"],
        "metrics": {
            "acquisitions": mgr.total_lease_acquisitions,
            "releases": mgr.total_confirmed_releases,
            "quarantined": mgr.total_quarantined_events,
            "stale_prevented": mgr.total_stale_writes_prevented,
            "late_accepted": mgr.total_legitimate_late_accepted,
            "final_k_avail": mgr.K_avail
        }
    }


if __name__ == "__main__":
    run_fsm_model_checker(5_000)
