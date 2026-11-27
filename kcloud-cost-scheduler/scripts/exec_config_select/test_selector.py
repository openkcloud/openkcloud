"""Selector cases: normal / SLO fail / lookup miss / tie / none eligible / device pinned."""

from __future__ import annotations

import copy
import unittest

try:
    from .fixtures import FIXTURE_PROFILES
    from .selector import BundleProfile, WorkBundleSpec, select_execution_config
except ImportError:
    from fixtures import FIXTURE_PROFILES
    from selector import BundleProfile, WorkBundleSpec, select_execution_config


def _spec(**kw):
    base = dict(work_id="selector-test")
    base.update(kw)
    return WorkBundleSpec(**base)


class TestSelectExecutionConfig(unittest.TestCase):
    def test_normal_selects_lower_wh(self):
        d = select_execution_config(_spec(), FIXTURE_PROFILES)
        self.assertEqual(d.decision, "selected")
        self.assertEqual(d.selected_config_id, "B_rngd_gpu")
        elig = [c for c in d.candidates if c.eligible]
        self.assertEqual(len(elig), 2)

    def test_slo_violation_excludes(self):
        profiles = {
            "A_gpu_serial": FIXTURE_PROFILES["A_gpu_serial"],
            "B_rngd_gpu": BundleProfile(
                config_id="B_rngd_gpu",
                predicted_server_wh=50.0,
                sla_pass=False,
                confidence="MEASURED",
                profile_id="bad-slo",
            ),
        }
        d = select_execution_config(_spec(), profiles)
        self.assertEqual(d.selected_config_id, "A_gpu_serial")
        b = next(c for c in d.candidates if c.config_id == "B_rngd_gpu")
        self.assertFalse(b.eligible)
        self.assertEqual(b.exclude_reason, "sla_fail")

    def test_lookup_failure_hold_if_all_missing(self):
        d = select_execution_config(_spec(), {"A_gpu_serial": None, "B_rngd_gpu": None})
        self.assertEqual(d.decision, "hold")
        self.assertEqual(d.hold_reason, "no_eligible_candidate")
        for c in d.candidates:
            self.assertEqual(c.exclude_reason, "profile_not_found")

    def test_tie_breaks_lexicographic(self):
        profiles = {
            "A_gpu_serial": BundleProfile(
                config_id="A_gpu_serial",
                predicted_server_wh=100.0,
                sla_pass=True,
                confidence="MEASURED",
                profile_id="tie-a",
            ),
            "B_rngd_gpu": BundleProfile(
                config_id="B_rngd_gpu",
                predicted_server_wh=100.0,
                sla_pass=True,
                confidence="MEASURED",
                profile_id="tie-b",
            ),
        }
        d = select_execution_config(_spec(), profiles)
        self.assertEqual(d.selected_config_id, "A_gpu_serial")

    def test_no_eligible_when_wrong_token_cell(self):
        # inject wrong-scope profile only for A; B missing
        profiles = {
            "A_gpu_serial": BundleProfile(
                config_id="A_gpu_serial",
                predicted_server_wh=10.0,
                sla_pass=True,
                confidence="MEASURED",
                energy_scope="device_net_j",
                profile_id="wrong-scope",
            ),
            "B_rngd_gpu": None,
        }
        d = select_execution_config(_spec(), profiles)
        self.assertEqual(d.decision, "hold")
        a = next(c for c in d.candidates if c.config_id == "A_gpu_serial")
        self.assertEqual(a.exclude_reason, "energy_scope_mismatch")

    def test_device_pinned_rejected(self):
        d = select_execution_config(
            _spec(device="nvidia.com/gpu"),
            FIXTURE_PROFILES,
        )
        self.assertEqual(d.decision, "hold")
        for c in d.candidates:
            self.assertEqual(c.exclude_reason, "input_device_fixed")

    def test_non_measured_excluded(self):
        profiles = copy.copy(dict(FIXTURE_PROFILES))
        profiles["B_rngd_gpu"] = BundleProfile(
            config_id="B_rngd_gpu",
            predicted_server_wh=1.0,
            sla_pass=True,
            confidence="ESTIMATED",
            profile_id="est",
        )
        d = select_execution_config(_spec(), profiles)
        self.assertEqual(d.selected_config_id, "A_gpu_serial")
        b = next(c for c in d.candidates if c.config_id == "B_rngd_gpu")
        self.assertEqual(b.exclude_reason, "confidence_not_measured")


if __name__ == "__main__":
    unittest.main()
