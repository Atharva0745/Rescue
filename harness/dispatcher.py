"""
RESCUE-4 Phase 2: Exact-Rational Dispatcher Simulator
Implements work-conserving FIFO dispatch, Graham-style drain tracking,
and mutants (NWC, LIFO, and Ignored Executor Loss).
Uses exact-rational arithmetic (fractions.Fraction) to prevent IEEE-754 precision artifacts.
"""

from fractions import Fraction
from typing import List, Dict, Any, Tuple, Optional
import heapq


class Job:
    def __init__(self, job_id: int, duration: Fraction, cap: Optional[Fraction] = None, arrival_time: Fraction = Fraction(0)):
        self.job_id = job_id
        self.duration = duration
        self.cap = cap if cap is not None else duration
        self.arrival_time = arrival_time
        self.start_time: Optional[Fraction] = None
        self.completion_time: Optional[Fraction] = None
        self.held_live_at_admission: Optional[Fraction] = None

    def __repr__(self):
        return f"Job(id={self.job_id}, dur={self.duration}, cap={self.cap}, arr={self.arrival_time}, start={self.start_time}, comp={self.completion_time})"


class DispatcherSimulation:
    def __init__(self, m: int, mode: str = "NOMINAL", nwc_pause: Fraction = Fraction(1)):
        """
        m: Count of executors
        mode: 'NOMINAL', 'MUTANT_NWC', 'MUTANT_LIFO', 'MUTANT_IGN_LOSS'
        nwc_pause: Artificial idle duration injected in MUTANT_NWC
        """
        assert m >= 1, "Executor count must be at least 1"
        self.m = m
        self.nominal_m = m
        self.mode = mode
        self.nwc_pause = nwc_pause
        self.quarantined_executors = 0

    def quarantine_executors(self, count: int):
        """Simulate executor loss / quarantine."""
        assert count <= self.m, "Cannot quarantine more executors than exist"
        self.quarantined_executors += count
        self.m -= count

    def run_batch(self, jobs: List[Job]) -> Dict[str, Any]:
        """
        Run a batch of jobs all available at arrival_time = 0.
        Returns execution schedule, T1 evaluations, and T2 makespan evaluation.
        """
        m_active = self.m
        assert m_active >= 1, "At least 1 active executor is required to dispatch"
        
        # Calculate held_live ledger (sum of all caps)
        total_held_live = sum(j.cap for j in jobs)
        beta_max = max(j.cap for j in jobs) if jobs else Fraction(0)
        
        # In a batch arriving at 0, each job's post-admission held_live is total_held_live
        # strictly accounting for the queue order:
        # For job at index i (0-based in FIFO), the work ahead is sum(jobs[k].cap for k < i).
        # When job i is admitted into the running queue, post-admission held_live for job i is:
        # sum(jobs[k].cap for k <= i).
        current_cumulative_cap = Fraction(0)
        for j in jobs:
            current_cumulative_cap += j.cap
            j.held_live_at_admission = current_cumulative_cap

        # Priority queue for executors: (available_time, executor_id)
        # We start with m_active executors ready at time 0
        executor_heap: List[Tuple[Fraction, int]] = [(Fraction(0), i) for i in range(m_active)]
        heapq.heapify(executor_heap)

        scheduled_jobs: List[Job] = []

        if self.mode == "MUTANT_LIFO":
            # Mutant LIFO: jobs processed in reverse queue order
            job_queue = list(reversed(jobs))
        else:
            # Nominal or other mutants: FIFO queue
            job_queue = list(jobs)

        for j in job_queue:
            avail_time, exec_id = heapq.heappop(executor_heap)
            
            # Start time is when the executor is available (since arrival is 0)
            start_t = avail_time

            # Mutant NWC: deliberately pause executor even though work is available
            if self.mode == "MUTANT_NWC":
                start_t += self.nwc_pause

            comp_t = start_t + j.duration
            j.start_time = start_t
            j.completion_time = comp_t
            scheduled_jobs.append(j)

            # Put executor back into heap
            heapq.heappush(executor_heap, (comp_t, exec_id))

        makespan = max((j.completion_time for j in scheduled_jobs), default=Fraction(0))

        # Evaluate Bounds
        # Effective m used in bounds:
        # For MUTANT_IGN_LOSS, the buggy bound calculation uses nominal_m instead of active m
        bound_m = self.nominal_m if self.mode == "MUTANT_IGN_LOSS" else m_active

        # T1 Evaluation per job:
        # S_j <= (held_live_at_admission - beta_j) / m
        t1_results = []
        for j in jobs:
            t1_bound = (j.held_live_at_admission - j.cap) / bound_m
            t1_slack = t1_bound - j.start_time
            t1_valid = t1_slack >= 0
            t1_results.append({
                "job_id": j.job_id,
                "start_time": j.start_time,
                "bound": t1_bound,
                "slack": t1_slack,
                "valid": t1_valid
            })

        # T2 Evaluation:
        # C_max <= total_held_live / m + (1 - 1/m) * beta_max
        t2_bound = (total_held_live / bound_m) + (Fraction(1) - Fraction(1, bound_m)) * beta_max
        t2_slack = t2_bound - makespan
        t2_valid = t2_slack >= 0
        t2_is_tight = (t2_slack == Fraction(0))

        return {
            "mode": self.mode,
            "m_active": m_active,
            "bound_m": bound_m,
            "total_held_live": total_held_live,
            "beta_max": beta_max,
            "makespan": makespan,
            "t1_all_valid": all(r["valid"] for r in t1_results),
            "t1_results": t1_results,
            "t2_bound": t2_bound,
            "t2_slack": t2_slack,
            "t2_valid": t2_valid,
            "t2_is_tight": t2_is_tight,
            "scheduled_jobs": scheduled_jobs
        }


def build_tight_instance(m: int) -> List[Job]:
    """
    Constructs the canonical analytical tightness instance I(m):
    m*(m - 1) unit jobs (duration 1) followed by 1 job of duration m.
    Total jobs = m*(m - 1) + 1.
    Total work = m^2.
    beta_max = m.
    Makespan under FIFO = 2m - 1.
    T2 bound = 2m - 1 (Slack = 0).
    """
    jobs = []
    job_id = 0
    # m*(m - 1) unit jobs
    for _ in range(m * (m - 1)):
        jobs.append(Job(job_id=job_id, duration=Fraction(1), cap=Fraction(1)))
        job_id += 1
    # 1 long job of duration m
    jobs.append(Job(job_id=job_id, duration=Fraction(m), cap=Fraction(m)))
    return jobs
