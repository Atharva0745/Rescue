"""
RESCUE-4 Admission Policies & Enforcement Ladder
Implements:
  1. Admission Policies:
     - AdaptiveLimiter (A'): Global congestion-based limiter (AIMD / gradient).
     - DracoAdmission (D): Datastore-aware selective-dropping policy ("DRACO-inspired").
     - ReservationAdmission (B): RESCUE-4 class reservation ledger.
  2. Enforcement Levels:
     - NS: No semaphore, capacity released at lease expiry.
     - TR: Shared stage semaphore, capacity released at lease expiry.
     - OC: Shared semaphore, owner-confirmed release, quarantine, database fencing.
"""

from typing import Dict, List, Optional, Any, Tuple
from enum import Enum
import math


class EnforcementLevel(Enum):
    NS = "NS"  # No semaphore, expiry release
    TR = "TR"  # Semaphore + cgroup budgets, expiry release
    OC = "OC"  # Semaphore + cgroup, owner confirmation, quarantine, fencing


class Request:
    def __init__(self, req_id: int, req_class: str, datastore_partition: int = 1, wall_clock_cap: float = 0.200, arrival_time: float = 0.0):
        self.req_id = req_id
        self.req_class = req_class
        self.datastore_partition = datastore_partition
        self.wall_clock_cap = wall_clock_cap
        self.arrival_time = arrival_time
        self.admitted: bool = False
        self.drop_reason: Optional[str] = None


class BaseAdmissionPolicy:
    def __init__(self, name: str):
        self.name = name

    def should_admit(self, req: Request, current_time: float) -> Tuple[bool, Optional[str]]:
        raise NotImplementedError


class AdaptiveLimiter(BaseAdmissionPolicy):
    """
    Policy A': Adaptive Limiter
    Monitors global system latency. Dynamically adjusts an aggregate concurrency limit
    using AIMD (Additive Increase, Multiplicative Decrease).
    Unaware of individual datastore partitions.
    """
    def __init__(self, min_limit: int = 2, max_limit: int = 32, target_latency_ms: float = 50.0):
        super().__init__("A_PRIME")
        self.min_limit = min_limit
        self.max_limit = max_limit
        self.target_latency_ms = target_latency_ms
        self.current_limit = float(max_limit // 2)
        self.current_in_flight = 0

    def record_completion(self, latency_ms: float):
        if latency_ms <= self.target_latency_ms:
            # Additive increase
            self.current_limit = min(float(self.max_limit), self.current_limit + 0.5)
        else:
            # Multiplicative decrease
            self.current_limit = max(float(self.min_limit), self.current_limit * 0.8)

    def should_admit(self, req: Request, current_time: float) -> Tuple[bool, Optional[str]]:
        if self.current_in_flight >= int(self.current_limit):
            return False, "ADAPTIVE_LIMITER_SATURATED"
        self.current_in_flight += 1
        return True, None

    def release_in_flight(self):
        self.current_in_flight = max(0, self.current_in_flight - 1)


class DracoAdmission(BaseAdmissionPolicy):
    """
    Policy D: DRACO-style Datastore-Aware Admission Policy ("DRACO-inspired")
    Tracks latency per datastore partition.
    When partition p becomes a bottleneck (hot datastore node), selectively sheds
    traffic targeting partition p, while preserving admission for cold partitions.
    """
    def __init__(self, num_partitions: int = 4, hot_threshold_ms: float = 100.0, base_partition_limit: int = 8):
        super().__init__("DRACO_INSPIRED")
        self.num_partitions = num_partitions
        self.hot_threshold_ms = hot_threshold_ms
        self.base_partition_limit = base_partition_limit
        # Latency and in-flight tracking per partition
        self.partition_latency: Dict[int, float] = {p: 20.0 for p in range(1, num_partitions + 1)}
        self.partition_in_flight: Dict[int, int] = {p: 0 for p in range(1, num_partitions + 1)}

    def update_datastore_latency(self, partition_id: int, latency_ms: float):
        self.partition_latency[partition_id] = latency_ms

    def should_admit(self, req: Request, current_time: float) -> Tuple[bool, Optional[str]]:
        p = req.datastore_partition
        observed_latency = self.partition_latency.get(p, 20.0)

        # Selective dropping for hot datastore partition
        if observed_latency > self.hot_threshold_ms:
            # Shed hot node traffic
            return False, f"DRACO_HOT_DATASTORE_SHED_PARTITION_{p}"

        # Standard partition capacity limit
        if self.partition_in_flight[p] >= self.base_partition_limit:
            return False, f"DRACO_PARTITION_{p}_SATURATED"

        self.partition_in_flight[p] += 1
        return True, None

    def release_in_flight(self, partition_id: int):
        self.partition_in_flight[partition_id] = max(0, self.partition_in_flight.get(partition_id, 0) - 1)


class ReservationAdmission(BaseAdmissionPolicy):
    """
    Policy B: RESCUE-4 Class-Based Reservation Ledger
    Tracks capacity reservation weights w[c][s] = beta_s[c] against available capacity
    C_max = K_avail * tau_s.
    """
    def __init__(self, K_nominal: int = 16, tau_s: float = 2.0, class_weights: Optional[Dict[str, float]] = None):
        super().__init__("RESCUE_B")
        self.K_nominal = K_nominal
        self.K_avail = K_nominal
        self.tau_s = tau_s
        self.class_weights = class_weights or {"C_INTERACTIVE": 0.255, "C_BATCH": 1.110}
        self.held_live = 0.0

    def set_available_executors(self, K_avail: int):
        self.K_avail = K_avail

    def should_admit(self, req: Request, current_time: float) -> Tuple[bool, Optional[str]]:
        weight = self.class_weights.get(req.req_class, 0.255)
        max_capacity = self.K_avail * self.tau_s

        if self.held_live + weight > max_capacity:
            return False, "RESERVATION_CAPACITY_EXCEEDED"

        self.held_live += weight
        return True, None

    def release_capacity(self, req_class: str):
        weight = self.class_weights.get(req_class, 0.255)
        self.held_live = max(0.0, self.held_live - weight)


class ExecutionArm:
    """
    Combines an AdmissionPolicy (A', D, B) with an EnforcementLevel (NS, TR, OC).
    Maintains exact factor isolation:
    - NS: no semaphore, expiry release
    - TR: semaphore, expiry release
    - OC: semaphore, owner confirmation, quarantine, fencing
    """
    def __init__(self, policy: BaseAdmissionPolicy, level: EnforcementLevel, K_executors: int = 8):
        self.policy = policy
        self.level = level
        self.K_nominal = K_executors
        self.K_avail = K_executors

        # Explicit factor configuration (traceable diff properties)
        self.has_shared_semaphore: bool = (level in (EnforcementLevel.TR, EnforcementLevel.OC))
        self.has_cgroup_budgets: bool = (level in (EnforcementLevel.TR, EnforcementLevel.OC))
        self.owner_confirmed_release: bool = (level == EnforcementLevel.OC)
        self.quarantine_enabled: bool = (level == EnforcementLevel.OC)
        self.database_fencing_enabled: bool = (level == EnforcementLevel.OC)

        # Runtime metrics
        self.quarantined_executors = 0
        self.orphaned_time_past_expiry = 0.0
        self.admitted_count = 0
        self.dropped_count = 0
        self.stale_writes_committed = 0

    def get_factor_manifest(self) -> Dict[str, Any]:
        """Returns the factor dictionary for diff audit."""
        return {
            "policy": self.policy.name,
            "level": self.level.value,
            "has_shared_semaphore": self.has_shared_semaphore,
            "has_cgroup_budgets": self.has_cgroup_budgets,
            "owner_confirmed_release": self.owner_confirmed_release,
            "quarantine_enabled": self.quarantine_enabled,
            "database_fencing_enabled": self.database_fencing_enabled
        }
