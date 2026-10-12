import unittest

from confirmatory_simulator import (
    ArmSimulator,
    SimulatedRequest,
    generate_workload_trace,
)


class ConfirmatorySimulatorMechanicsTest(unittest.TestCase):
    @staticmethod
    def make_request(
        req_id,
        arrival_time=0.0,
        req_class="C_INTERACTIVE",
        is_attack=False,
        fault_draw=1.0,
        policy_draw=0.5,
        demand=0.01,
    ):
        return SimulatedRequest(
            req_id=req_id,
            req_class=req_class,
            arrival_time=arrival_time,
            is_attack=is_attack,
            demands={"S1": demand, "S2": demand, "S3": demand},
            deadline=0.5 if req_class == "C_INTERACTIVE" else 3.0,
            fault_draw=fault_draw,
            policy_draw=policy_draw,
        )

    def test_success_rate_is_derived_from_observed_completion(self):
        requests = [
            self.make_request(1, demand=0.01),
            self.make_request(2, arrival_time=1.0, demand=0.3),
        ]
        result = ArmSimulator("B-TR", 0.0, 1).run_workload(
            requests, (0.0, 2.0)
        )

        self.assertEqual(result["total_legitimate"], 2)
        self.assertEqual(result["successful_legitimate"], 1)
        self.assertEqual(result["S_L"], 0.5)
        self.assertEqual(result["OE"], 0.0)

    def test_fault_draws_are_matched_across_arm_runs(self):
        first = generate_workload_trace("W3", 123, 2.0, 0.25, fault_seed=456)
        second = generate_workload_trace("W3", 123, 2.0, 0.25, fault_seed=456)

        self.assertEqual(
            [request.fault_draw for request in first],
            [request.fault_draw for request in second],
        )
        self.assertEqual(
            [(r.arrival_time, r.is_attack, r.demands) for r in first],
            [(r.arrival_time, r.is_attack, r.demands) for r in second],
        )

    def test_reservation_policy_rejects_when_live_weight_exceeds_capacity(self):
        requests = [self.make_request(i) for i in range(1, 160)]
        result = ArmSimulator("B-TR", 0.0, 1).run_workload(
            requests, (0.0, 1.0)
        )

        self.assertGreater(result["rejected_requests"], 0)
        self.assertLess(result["admitted_requests"], len(requests))

    def test_owner_confirmation_quarantines_capacity_temporarily(self):
        requests = [
            self.make_request(
                i,
                is_attack=True,
                fault_draw=0.0,
                policy_draw=0.5,
                demand=0.1,
            )
            for i in range(1, 40)
        ]
        result = ArmSimulator("B-OC", 1.0, 1).run_workload(
            requests, (0.0, 1.0)
        )
        tr_result = ArmSimulator("B-TR", 1.0, 1).run_workload(
            requests, (0.0, 1.0)
        )

        self.assertEqual(result["peak_quarantined_executors"], 15)
        self.assertGreater(
            result["rejected_requests"], tr_result["rejected_requests"]
        )
        self.assertEqual(result["stale_writes_committed"], 0)

    def test_fencing_blocks_stale_write_after_gateway_restart(self):
        requests = [
            self.make_request(
                1,
                arrival_time=36.0,
                is_attack=True,
                fault_draw=0.0,
                demand=0.1,
            )
        ]
        timed_release = ArmSimulator(
            "B-TR", 1.0, 1, gateway_crash_time=35.0
        ).run_workload(requests, (36.0, 37.0))
        owner_confirmed = ArmSimulator(
            "B-OC", 1.0, 1, gateway_crash_time=35.0
        ).run_workload(requests, (36.0, 37.0))

        self.assertEqual(timed_release["stale_writes_committed"], 1)
        self.assertEqual(owner_confirmed["stale_writes_committed"], 0)


if __name__ == "__main__":
    unittest.main()
