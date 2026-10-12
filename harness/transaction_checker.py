"""
RESCUE-4 Transaction-Level Checker & Reference Oracle
Implements commit-order validate-before-apply logical decoding validation,
incarnation/epoch fence tracking, and reference oracle.
"""

from typing import Dict, List, Tuple, Set, Optional, Any
from dataclasses import dataclass, field


@dataclass(frozen=True, order=True)
class FenceVersion:
    inc: int
    epoch: int

    def __str__(self):
        return f"({self.inc}, {self.epoch})"


@dataclass
class TransactionRecord:
    tx_id: int
    lsn: int
    gateway_id: str
    fence_bumps: List[Dict[str, Any]] = field(default_factory=list)
    audit_records: List[Dict[str, Any]] = field(default_factory=list)
    data_mutations: List[Dict[str, Any]] = field(default_factory=list)
    # Metadata for test assertions
    is_planted_stale: bool = False
    is_legitimate_late: bool = False


class TransactionChecker:
    """
    Validate-before-apply transaction checker that consumes committed transactions
    in commit-record order.
    """
    def __init__(self, known_gateways: Optional[Set[str]] = None, mutant_mode: Optional[str] = None):
        self.known_gateways = set(known_gateways) if known_gateways else set()
        self.fence_state: Dict[str, FenceVersion] = {gw: FenceVersion(1, 0) for gw in self.known_gateways}
        self.last_seq: Dict[str, int] = {gw: 0 for gw in self.known_gateways}
        self.last_checkpoint_lsn: int = 0
        self.processed_lsns: Set[int] = set()
        self.mutant_mode = mutant_mode  # None, 'M1_STALE_BLIND', 'M2_LATE_FALSE_POS', 'M3_MIXED_BLIND', 'M4_AUDIT_BLIND'

    def register_gateway(self, gw: str, initial_version: FenceVersion = FenceVersion(1, 0)):
        self.known_gateways.add(gw)
        self.fence_state[gw] = initial_version
        self.last_seq[gw] = 0

    def create_checkpoint(self) -> Dict[str, Any]:
        """Creates a crash-consistent checkpoint snapshot of checker state and LSN."""
        return {
            "fence_state": {gw: (ver.inc, ver.epoch) for gw, ver in self.fence_state.items()},
            "last_seq": dict(self.last_seq),
            "last_checkpoint_lsn": self.last_checkpoint_lsn,
            "processed_lsns": set(self.processed_lsns)
        }

    def restore_from_checkpoint(self, chk: Dict[str, Any]):
        """Restores checker state following a simulated process crash / restart."""
        self.fence_state = {gw: FenceVersion(inc, ep) for gw, (inc, ep) in chk["fence_state"].items()}
        self.last_seq = dict(chk["last_seq"])
        self.last_checkpoint_lsn = chk["last_checkpoint_lsn"]
        self.processed_lsns = set(chk["processed_lsns"])

    def process_transaction(self, tx: TransactionRecord) -> Dict[str, Any]:
        """
        Processes a single committed transaction using validate-before-apply semantics.
        Returns a dictionary of validation results and flags.
        """
        flags: Set[str] = set()
        is_valid = True

        # 1. Replay deduplication check
        if tx.lsn <= self.last_checkpoint_lsn or tx.lsn in self.processed_lsns:
            return {
                "tx_id": tx.tx_id,
                "lsn": tx.lsn,
                "is_replay": True,
                "is_valid": True,
                "flags": {"FLAG_REPLAY_DEDUPLICATED"}
            }

        # 2. Snapshot current pre-transaction fence state
        pre_fence_state = {gw: ver for gw, ver in self.fence_state.items()}

        # 3. Check for mixed transactions (bump + data/audit in same tx)
        has_bumps = len(tx.fence_bumps) > 0
        has_writes = len(tx.audit_records) > 0 or len(tx.data_mutations) > 0
        if has_bumps and has_writes:
            if self.mutant_mode != "M3_MIXED_BLIND":
                flags.add("FLAG_MIXED_TRANSACTION")
                is_valid = False

        # 4. Audit-to-Data correlation check (1:1 correspondence)
        if self.mutant_mode != "M4_AUDIT_BLIND":
            audit_write_ids = {a["write_id"] for a in tx.audit_records}
            data_write_ids = {d["write_id"] for d in tx.data_mutations}

            # Data without audit
            unaudited = data_write_ids - audit_write_ids
            if unaudited:
                flags.add("FLAG_UNAUDITED_MUTATION")
                is_valid = False

            # Audit without data
            orphan_audits = audit_write_ids - data_write_ids
            if orphan_audits:
                flags.add("FLAG_ORPHAN_AUDIT")
                is_valid = False

        # 5. Validate write audit versions against pre-transaction state
        for audit in tx.audit_records:
            gw = audit["gw"]
            audit_ver = FenceVersion(audit["inc"], audit["epoch"])

            if gw not in self.known_gateways:
                flags.add("FLAG_UNKNOWN_GATEWAY")
                is_valid = False
                continue

            current_gw_ver = pre_fence_state[gw]

            # Mutant M1: fails to detect stale writes
            if self.mutant_mode == "M1_STALE_BLIND":
                pass
            else:
                if audit_ver < current_gw_ver:
                    flags.add("FLAG_STALE_WRITE")
                    is_valid = False

            # Future versions
            if audit_ver > current_gw_ver:
                flags.add("FLAG_FUTURE_VERSION")
                is_valid = False

            # Mutant M2: falsely flags legitimate late completions
            if self.mutant_mode == "M2_LATE_FALSE_POS" and tx.is_legitimate_late:
                flags.add("FLAG_STALE_WRITE")
                is_valid = False

        # 6. Validate and apply fence bumps
        for bump in tx.fence_bumps:
            gw = bump["gw"]
            seq = bump["seq"]
            new_ver = FenceVersion(bump["inc_new"], bump["epoch_new"])

            if gw not in self.known_gateways:
                self.known_gateways.add(gw)
                self.fence_state[gw] = FenceVersion(0, 0)
                self.last_seq[gw] = 0

            cur_ver = self.fence_state[gw]
            cur_seq = self.last_seq[gw]

            if new_ver <= cur_ver:
                flags.add("FLAG_NON_MONOTONE_BUMP")
                is_valid = False
            elif seq <= cur_seq:
                flags.add("FLAG_NON_MONOTONE_SEQUENCE")
                is_valid = False
            else:
                # Apply fence bump
                self.fence_state[gw] = new_ver
                self.last_seq[gw] = seq

        # 7. Advance LSN checkpoint
        self.processed_lsns.add(tx.lsn)
        self.last_checkpoint_lsn = tx.lsn

        return {
            "tx_id": tx.tx_id,
            "lsn": tx.lsn,
            "is_replay": False,
            "is_valid": is_valid,
            "flags": flags
        }


class ReferenceOracle:
    """
    Independent reference oracle that evaluates committed transactions against
    the latest prior valid fence bump.
    """
    def __init__(self, known_gateways: Optional[Set[str]] = None):
        self.history: List[TransactionRecord] = []
        self.known_gateways = set(known_gateways) if known_gateways else set()
        self.latest_prior_bumps: Dict[str, Tuple[FenceVersion, int]] = {
            gw: (FenceVersion(1, 0), 0) for gw in self.known_gateways
        }
        self.processed_lsns: Set[int] = set()

    def register_gateway(self, gw: str):
        self.known_gateways.add(gw)
        if gw not in self.latest_prior_bumps:
            self.latest_prior_bumps[gw] = (FenceVersion(1, 0), 0)

    def evaluate_transaction(self, tx: TransactionRecord) -> Dict[str, Any]:
        flags: Set[str] = set()
        is_valid = True

        # Check replay
        if tx.lsn in self.processed_lsns:
            return {
                "tx_id": tx.tx_id,
                "lsn": tx.lsn,
                "is_replay": True,
                "is_valid": True,
                "flags": {"FLAG_REPLAY_DEDUPLICATED"}
            }

        # Check mixed
        if len(tx.fence_bumps) > 0 and (len(tx.audit_records) > 0 or len(tx.data_mutations) > 0):
            flags.add("FLAG_MIXED_TRANSACTION")
            is_valid = False

        # Check 1:1 audit:data correspondence
        audit_write_ids = {a["write_id"] for a in tx.audit_records}
        data_write_ids = {d["write_id"] for d in tx.data_mutations}
        if data_write_ids - audit_write_ids:
            flags.add("FLAG_UNAUDITED_MUTATION")
            is_valid = False
        if audit_write_ids - data_write_ids:
            flags.add("FLAG_ORPHAN_AUDIT")
            is_valid = False

        # Snapshot prior fence before this transaction's bumps
        prior_fence = {gw: ver for gw, (ver, _) in self.latest_prior_bumps.items()}
        prior_seq = {gw: seq for gw, (_, seq) in self.latest_prior_bumps.items()}

        # Check writes against prior_fence
        for audit in tx.audit_records:
            gw = audit["gw"]
            audit_ver = FenceVersion(audit["inc"], audit["epoch"])
            if gw not in self.known_gateways:
                flags.add("FLAG_UNKNOWN_GATEWAY")
                is_valid = False
                continue

            active_ver = prior_fence.get(gw, FenceVersion(1, 0))
            if audit_ver < active_ver:
                flags.add("FLAG_STALE_WRITE")
                is_valid = False
            elif audit_ver > active_ver:
                flags.add("FLAG_FUTURE_VERSION")
                is_valid = False

        # Check bumps monotonicity
        for b in tx.fence_bumps:
            gw = b["gw"]
            new_ver = FenceVersion(b["inc_new"], b["epoch_new"])
            seq = b["seq"]
            cur_ver = prior_fence.get(gw, FenceVersion(1, 0))
            cur_seq = prior_seq.get(gw, 0)
            if new_ver <= cur_ver:
                flags.add("FLAG_NON_MONOTONE_BUMP")
                is_valid = False
            elif seq <= cur_seq:
                flags.add("FLAG_NON_MONOTONE_SEQUENCE")
                is_valid = False
            else:
                self.latest_prior_bumps[gw] = (new_ver, seq)

        self.processed_lsns.add(tx.lsn)
        self.history.append(tx)

        return {
            "tx_id": tx.tx_id,
            "lsn": tx.lsn,
            "is_replay": False,
            "is_valid": is_valid,
            "flags": flags
        }
