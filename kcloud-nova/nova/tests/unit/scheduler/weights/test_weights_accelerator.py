# Copyright 2024 OpenStack Foundation
# All Rights Reserved.
#
#    Licensed under the Apache License, Version 2.0 (the "License"); you may
#    not use this file except in compliance with the License. You may obtain
#    a copy of the License at
#
#         http://www.apache.org/licenses/LICENSE-2.0
#
#    Unless required by applicable law or agreed to in writing, software
#    distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
#    WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
#    License for the specific language governing permissions and limitations
#    under the License.
"""Tests for Scheduler Accelerator weights."""

from unittest import mock

from nova import objects
from nova.scheduler import weights
from nova.scheduler.weights import accelerator
from nova import test
from nova.tests.unit.scheduler import fakes


# ----------------------------- Fake helpers --------------------------------

class FakeProviderData:
    """Mimics the object returned by ProviderTree.data(uuid)."""

    def __init__(self, inventory=None, traits=None):
        self.inventory = inventory or {}
        self.traits = traits or set()


class FakeProviderTree:
    """Minimal ProviderTree mock for testing."""

    def __init__(self, root_uuid, providers):
        """
        :param root_uuid: UUID of the root provider
        :param providers: dict {uuid: FakeProviderData}
        """
        self._root_uuid = root_uuid
        self._providers = {root_uuid: FakeProviderData()}
        self._providers.update(providers)

    def get_provider_uuids_in_tree(self, root_uuid):
        if root_uuid != self._root_uuid:
            raise ValueError("Root %s not found" % root_uuid)
        return list(self._providers.keys())

    def data(self, rp_uuid):
        if rp_uuid not in self._providers:
            raise ValueError("RP %s not found" % rp_uuid)
        return self._providers[rp_uuid]


class FakeRequestGroup:
    """Mimics a RequestGroup with resources and required_traits."""

    def __init__(self, resources, required_traits=None):
        self.resources = resources
        self.required_traits = required_traits or set()


class FakeRequestSpec:
    """Mimics a RequestSpec with requested_resources."""

    def __init__(self, groups=None):
        self.requested_resources = groups or []


class FakeResponse:
    """Mimics an HTTP response object."""

    def __init__(self, status_code=200, json_data=None):
        self.status_code = status_code
        self._json_data = json_data or {}

    def json(self):
        return self._json_data


def _make_usage_response(usages):
    """Create a FakeResponse for /resource_providers/{uuid}/usages."""
    return FakeResponse(200, {"usages": usages})


# ----------------------------- Test cases ----------------------------------

class AcceleratorWeigherTestCase(test.NoDBTestCase):
    """Tests for AcceleratorWeigher."""

    def setUp(self):
        super(AcceleratorWeigherTestCase, self).setUp()
        self.weight_handler = weights.HostWeightHandler()
        self.weighers = [accelerator.AcceleratorWeigher()]
        self.accel_weigher = accelerator.AcceleratorWeigher()

        # Patch _get_client to return a mock
        self.mock_client = mock.MagicMock()
        patcher = mock.patch(
            'nova.scheduler.weights.accelerator._get_client',
            return_value=self.mock_client)
        self.addCleanup(patcher.stop)
        patcher.start()

        # Patch context.get_context
        ctx_patcher = mock.patch(
            'nova.scheduler.weights.accelerator.context.get_context',
            return_value=mock.MagicMock())
        self.addCleanup(ctx_patcher.stop)
        ctx_patcher.start()

    def _make_host(self, host, node, uuid, aggregates=None):
        """Create a FakeHostState with uuid."""
        host_state = fakes.FakeHostState(host, node, {'uuid': uuid})
        if aggregates:
            host_state.aggregates = aggregates
        return host_state

    def _setup_provider_tree(self, root_uuid, providers, usages_dict=None):
        """Set up mock client to return a FakeProviderTree.

        :param root_uuid: Root RP UUID
        :param providers: dict {child_uuid: FakeProviderData}
        :param usages_dict: dict {child_uuid: {rc: usage}}
        """
        ptree = FakeProviderTree(root_uuid, providers)
        self.mock_client.get_provider_tree_and_ensure_root.return_value = ptree

        usages_dict = usages_dict or {}

        def _mock_get(url):
            # Match /resource_providers/{uuid}/usages
            for rp_uuid, usages in usages_dict.items():
                if url == f"/resource_providers/{rp_uuid}/usages":
                    return _make_usage_response(usages)
            return _make_usage_response({})

        self.mock_client.get.side_effect = _mock_get
        return ptree

    def _make_spec(self, groups):
        """Create a FakeRequestSpec from a list of (resources, traits) tuples.

        :param groups: list of (resources_dict, traits_set) tuples
        """
        request_groups = [
            FakeRequestGroup(res, traits) for res, traits in groups
        ]
        return FakeRequestSpec(request_groups)

    # ---------- Basic scoring tests ----------

    def test_no_accel_groups_returns_zero(self):
        """When request has no accelerator groups, score should be 0."""
        host = self._make_host('host1', 'node1', 'uuid-root-1')
        spec = FakeRequestSpec()  # no requested_resources

        score = self.accel_weigher._weigh_object(host, spec)
        self.assertEqual(0.0, score)

    def test_no_uuid_no_root_rp_returns_zero(self):
        """When host has no UUID and HTTP lookup fails, score should be 0."""
        host = fakes.FakeHostState('host1', 'node1', {})
        # _lookup_root_rp_uuid will be called; make it return empty
        self.mock_client.get.return_value = FakeResponse(
            200, {"resource_providers": []})

        spec = self._make_spec([
            ({"PGPU": 1}, set()),
        ])
        score = self.accel_weigher._weigh_object(host, spec)
        self.assertEqual(0.0, score)

    def test_sum_fit_single_group(self):
        """Sum-fit with a single group: score = slack."""
        self.flags(policy='sum-fit', group='accelerator_weigher')
        root_uuid = 'root-1'

        # Child RP with 4 PGPU total, 0 reserved, 1 used → 3 free
        self._setup_provider_tree(
            root_uuid,
            {'child-1': FakeProviderData(
                inventory={'PGPU': {'total': 4, 'reserved': 0}},
                traits=set())},
            usages_dict={'child-1': {'PGPU': 1}})

        host = self._make_host('host1', 'node1', root_uuid)
        spec = self._make_spec([
            ({"PGPU": 2}, set()),  # request 2 PGPU
        ])

        score = self.accel_weigher._weigh_object(host, spec)
        # free=3, required=2, slack=1, score=sum([1])=1.0
        self.assertEqual(1.0, score)

    def test_sum_fit_multiple_groups(self):
        """Sum-fit with multiple groups: score = sum of slacks."""
        self.flags(policy='sum-fit', group='accelerator_weigher')
        root_uuid = 'root-1'

        self._setup_provider_tree(
            root_uuid,
            {
                'child-1': FakeProviderData(
                    inventory={'PGPU': {'total': 6, 'reserved': 0}},
                    traits=set()),
                'child-2': FakeProviderData(
                    inventory={'FPGA': {'total': 4, 'reserved': 0}},
                    traits=set()),
            },
            usages_dict={
                'child-1': {'PGPU': 2},  # 4 free
                'child-2': {'FPGA': 1},  # 3 free
            })

        host = self._make_host('host1', 'node1', root_uuid)
        spec = self._make_spec([
            ({"PGPU": 1}, set()),  # slack = 4 - 1 = 3
            ({"FPGA": 2}, set()),  # slack = 3 - 2 = 1
        ])

        score = self.accel_weigher._weigh_object(host, spec)
        # score = sum([3, 1]) = 4.0
        self.assertEqual(4.0, score)

    def test_sum_fit_negative_slack_included(self):
        """Sum-fit includes negative slacks (not filtered out)."""
        self.flags(policy='sum-fit', group='accelerator_weigher')
        root_uuid = 'root-1'

        self._setup_provider_tree(
            root_uuid,
            {'child-1': FakeProviderData(
                inventory={
                    'PGPU': {'total': 5, 'reserved': 0},
                    'FPGA': {'total': 1, 'reserved': 0},
                },
                traits=set())},
            usages_dict={'child-1': {'PGPU': 0, 'FPGA': 0}})

        host = self._make_host('host1', 'node1', root_uuid)
        spec = self._make_spec([
            ({"PGPU": 2}, set()),  # slack = 5 - 2 = 3
            ({"FPGA": 3}, set()),  # slack = 1 - 3 = -2
        ])

        score = self.accel_weigher._weigh_object(host, spec)
        # score = sum([3, -2]) = 1.0 (negative slack IS included)
        self.assertEqual(1.0, score)

    def test_product_fit_single_group(self):
        """Product-fit with single group: score = (slack + EPS)."""
        self.flags(policy='product-fit', group='accelerator_weigher')
        root_uuid = 'root-1'

        self._setup_provider_tree(
            root_uuid,
            {'child-1': FakeProviderData(
                inventory={'PGPU': {'total': 5, 'reserved': 0}},
                traits=set())},
            usages_dict={'child-1': {'PGPU': 1}})

        host = self._make_host('host1', 'node1', root_uuid)
        spec = self._make_spec([
            ({"PGPU": 2}, set()),  # slack = 4 - 2 = 2
        ])

        score = self.accel_weigher._weigh_object(host, spec)
        # score = (2 + EPS) ≈ 2.000001
        self.assertAlmostEqual(2.0, score, places=4)

    def test_product_fit_multiple_groups(self):
        """Product-fit with multiple groups: score = product of (slack+EPS)."""
        self.flags(policy='product-fit', group='accelerator_weigher')
        root_uuid = 'root-1'

        self._setup_provider_tree(
            root_uuid,
            {
                'child-1': FakeProviderData(
                    inventory={'PGPU': {'total': 5, 'reserved': 0}},
                    traits=set()),
                'child-2': FakeProviderData(
                    inventory={'FPGA': {'total': 6, 'reserved': 0}},
                    traits=set()),
            },
            usages_dict={
                'child-1': {'PGPU': 2},  # 3 free
                'child-2': {'FPGA': 1},  # 5 free
            })

        host = self._make_host('host1', 'node1', root_uuid)
        spec = self._make_spec([
            ({"PGPU": 1}, set()),  # slack = 3 - 1 = 2
            ({"FPGA": 2}, set()),  # slack = 5 - 2 = 3
        ])

        score = self.accel_weigher._weigh_object(host, spec)
        # score ≈ (2 + EPS) * (3 + EPS) ≈ 6.0
        self.assertAlmostEqual(6.0, score, places=4)

    # ---------- Unmet group tests ----------

    def test_unmet_group_non_matching_rc_returns_zero(self):
        """When request has no RC matching rc_pattern, returns 0 (no groups)."""
        self.flags(policy='sum-fit', group='accelerator_weigher')
        root_uuid = 'root-1'

        self._setup_provider_tree(
            root_uuid,
            {'child-1': FakeProviderData(
                inventory={'PGPU': {'total': 4, 'reserved': 0}},
                traits=set())},
            usages_dict={'child-1': {'PGPU': 0}})

        host = self._make_host('host1', 'node1', root_uuid)
        spec = self._make_spec([
            # RC doesn't match default rc_pattern → group skipped → 0
            ({"CUSTOM_UNKNOWN_ACCEL": 1}, set()),
        ])

        score = self.accel_weigher._weigh_object(host, spec)
        self.assertEqual(0.0, score)

    def test_group_slack_empty_resources_returns_unmet_floor(self):
        """_group_slack returns UNMET_FLOOR when accel_resources is empty."""
        ptree = FakeProviderTree('root-1', {})
        stats = {"errors": []}
        result = accelerator._group_slack(
            ptree, 'root-1', {}, set(), {}, stats)
        self.assertEqual(accelerator.UNMET_FLOOR, result)

    def test_unmet_floor_clamped_by_minval(self):
        """UNMET_FLOOR is clamped to minval=0 during normalization."""
        self.flags(policy='sum-fit', group='accelerator_weigher')
        root_uuid_1 = 'root-1'
        root_uuid_2 = 'root-2'

        # host1: has PGPU capacity → positive score
        ptree1 = FakeProviderTree(
            root_uuid_1,
            {'child-1': FakeProviderData(
                inventory={'PGPU': {'total': 4, 'reserved': 0}},
                traits=set())})
        # host2: no matching RC → UNMET_FLOOR
        ptree2 = FakeProviderTree(root_uuid_2, {})

        def _mock_get_tree(ctx, root_uuid):
            if root_uuid == root_uuid_1:
                return ptree1
            return ptree2

        self.mock_client.get_provider_tree_and_ensure_root.side_effect = \
            _mock_get_tree
        self.mock_client.get.side_effect = lambda url: _make_usage_response(
            {'PGPU': 0})

        host1 = self._make_host('host1', 'node1', root_uuid_1)
        host2 = self._make_host('host2', 'node2', root_uuid_2)

        spec = self._make_spec([
            ({"PGPU": 2}, set()),
        ])

        weigher = accelerator.AcceleratorWeigher()
        # weigh_objects applies minval clamping
        weighed = self.weight_handler.get_weighed_objects(
            [weigher], [host1, host2], spec)

        # host1 should win with positive weight, host2 should get 0
        self.assertEqual('host1', weighed[0].obj.host)
        self.assertGreaterEqual(weighed[-1].weight, 0.0)

    # ---------- Traits filtering tests ----------

    def test_traits_filter_matching(self):
        """RPs with matching traits contribute to free count."""
        self.flags(policy='sum-fit', group='accelerator_weigher')
        root_uuid = 'root-1'

        self._setup_provider_tree(
            root_uuid,
            {
                'child-1': FakeProviderData(
                    inventory={'PGPU': {'total': 4, 'reserved': 0}},
                    traits={'CUSTOM_GPU_NVIDIA'}),
                'child-2': FakeProviderData(
                    inventory={'PGPU': {'total': 2, 'reserved': 0}},
                    traits={'CUSTOM_GPU_AMD'}),  # different trait
            },
            usages_dict={
                'child-1': {'PGPU': 0},
                'child-2': {'PGPU': 0},
            })

        host = self._make_host('host1', 'node1', root_uuid)
        spec = self._make_spec([
            ({"PGPU": 1}, {"CUSTOM_GPU_NVIDIA"}),
        ])

        score = self.accel_weigher._weigh_object(host, spec)
        # Only child-1 matches traits: free=4, required=1, slack=3
        self.assertEqual(3.0, score)

    def test_traits_filter_no_match(self):
        """When no RP matches required traits, group slack → UNMET_FLOOR."""
        self.flags(policy='sum-fit', group='accelerator_weigher')
        root_uuid = 'root-1'

        self._setup_provider_tree(
            root_uuid,
            {'child-1': FakeProviderData(
                inventory={'PGPU': {'total': 4, 'reserved': 0}},
                traits={'CUSTOM_GPU_AMD'})},
            usages_dict={'child-1': {'PGPU': 0}})

        host = self._make_host('host1', 'node1', root_uuid)
        spec = self._make_spec([
            ({"PGPU": 1}, {"CUSTOM_GPU_NVIDIA"}),  # no match
        ])

        score = self.accel_weigher._weigh_object(host, spec)
        # No matching RP → free=0, required=1, slack=-1
        # slack is -1 (not UNMET_FLOOR because the group had RCs, just no
        # matching providers)
        self.assertEqual(-1.0, score)

    # ---------- Reserved and usage accounting ----------

    def test_reserved_subtracted(self):
        """Reserved inventory is subtracted from total."""
        self.flags(policy='sum-fit', group='accelerator_weigher')
        root_uuid = 'root-1'

        self._setup_provider_tree(
            root_uuid,
            {'child-1': FakeProviderData(
                inventory={'PGPU': {'total': 10, 'reserved': 3}},
                traits=set())},
            usages_dict={'child-1': {'PGPU': 2}})

        host = self._make_host('host1', 'node1', root_uuid)
        spec = self._make_spec([
            ({"PGPU": 1}, set()),
        ])

        score = self.accel_weigher._weigh_object(host, spec)
        # free = 10 - 3 - 2 = 5, slack = 5 - 1 = 4
        self.assertEqual(4.0, score)

    def test_fully_used_returns_negative_slack(self):
        """When all capacity is used, slack is negative."""
        self.flags(policy='sum-fit', group='accelerator_weigher')
        root_uuid = 'root-1'

        self._setup_provider_tree(
            root_uuid,
            {'child-1': FakeProviderData(
                inventory={'PGPU': {'total': 2, 'reserved': 0}},
                traits=set())},
            usages_dict={'child-1': {'PGPU': 2}})

        host = self._make_host('host1', 'node1', root_uuid)
        spec = self._make_spec([
            ({"PGPU": 1}, set()),
        ])

        score = self.accel_weigher._weigh_object(host, spec)
        # free = max(2 - 0 - 2, 0) = 0, slack = 0 - 1 = -1
        self.assertEqual(-1.0, score)

    # ---------- Multiple children aggregated ----------

    def test_multiple_children_summed(self):
        """Free units across multiple children are summed."""
        self.flags(policy='sum-fit', group='accelerator_weigher')
        root_uuid = 'root-1'

        self._setup_provider_tree(
            root_uuid,
            {
                'child-1': FakeProviderData(
                    inventory={'PGPU': {'total': 3, 'reserved': 0}},
                    traits=set()),
                'child-2': FakeProviderData(
                    inventory={'PGPU': {'total': 5, 'reserved': 1}},
                    traits=set()),
            },
            usages_dict={
                'child-1': {'PGPU': 1},  # 2 free
                'child-2': {'PGPU': 2},  # 2 free (5-1-2)
            })

        host = self._make_host('host1', 'node1', root_uuid)
        spec = self._make_spec([
            ({"PGPU": 1}, set()),
        ])

        score = self.accel_weigher._weigh_object(host, spec)
        # total_free = 2 + 2 = 4, slack = 4 - 1 = 3
        self.assertEqual(3.0, score)

    # ---------- Multiplier tests ----------

    def test_weight_multiplier_default(self):
        """Default multiplier is 1.0."""
        host = self._make_host('host1', 'node1', 'uuid-1')
        self.assertEqual(1.0, self.accel_weigher.weight_multiplier(host))

    def test_weight_multiplier_config(self):
        """Multiplier reads from config."""
        self.flags(accelerator_weight_multiplier=2.5,
                   group='accelerator_weigher')
        host = self._make_host('host1', 'node1', 'uuid-1')
        self.assertEqual(2.5, self.accel_weigher.weight_multiplier(host))

    def test_weight_multiplier_aggregate_override(self):
        """Aggregate metadata overrides config multiplier."""
        self.flags(accelerator_weight_multiplier=1.0,
                   group='accelerator_weigher')
        aggs = [
            objects.Aggregate(
                id=1,
                name='gpu-agg',
                hosts=['host1'],
                metadata={'accelerator_weight_multiplier': '3.0'},
            )]
        host = self._make_host('host1', 'node1', 'uuid-1', aggregates=aggs)
        self.assertEqual(3.0, self.accel_weigher.weight_multiplier(host))

    def test_weight_multiplier_aggregate_min_wins(self):
        """When host is in multiple aggregates, minimum value wins."""
        self.flags(accelerator_weight_multiplier=1.0,
                   group='accelerator_weigher')
        aggs = [
            objects.Aggregate(
                id=1,
                name='agg1',
                hosts=['host1'],
                metadata={'accelerator_weight_multiplier': '5.0'},
            ),
            objects.Aggregate(
                id=2,
                name='agg2',
                hosts=['host1'],
                metadata={'accelerator_weight_multiplier': '2.0'},
            )]
        host = self._make_host('host1', 'node1', 'uuid-1', aggregates=aggs)
        self.assertEqual(2.0, self.accel_weigher.weight_multiplier(host))

    # ---------- Host UUID optimization ----------

    def test_host_uuid_skips_http_lookup(self):
        """When host_state.uuid is available, no HTTP lookup is made."""
        self.flags(policy='sum-fit', group='accelerator_weigher')
        root_uuid = 'root-uuid-direct'

        self._setup_provider_tree(
            root_uuid,
            {'child-1': FakeProviderData(
                inventory={'PGPU': {'total': 2, 'reserved': 0}},
                traits=set())},
            usages_dict={'child-1': {'PGPU': 0}})

        host = self._make_host('host1', 'node1', root_uuid)
        spec = self._make_spec([({"PGPU": 1}, set())])

        self.accel_weigher._weigh_object(host, spec)

        # get_provider_tree_and_ensure_root should be called with root_uuid
        self.mock_client.get_provider_tree_and_ensure_root.assert_called_once()
        # No call to GET /resource_providers?name=... since uuid was used
        for call in self.mock_client.get.call_args_list:
            url = call[0][0]
            self.assertNotIn('/resource_providers?name=', url)

    def test_fallback_to_http_lookup_when_no_uuid(self):
        """When host_state.uuid is None, falls back to HTTP lookup."""
        self.flags(policy='sum-fit', group='accelerator_weigher')
        root_uuid = 'root-from-lookup'

        self._setup_provider_tree(
            root_uuid,
            {'child-1': FakeProviderData(
                inventory={'PGPU': {'total': 2, 'reserved': 0}},
                traits=set())},
            usages_dict={'child-1': {'PGPU': 0}})

        # Override get side_effect to handle both RP lookup and usages
        original_get = self.mock_client.get.side_effect

        def _mock_get(url):
            if '/resource_providers?name=' in url:
                return FakeResponse(200, {
                    "resource_providers": [{"uuid": root_uuid}]
                })
            return original_get(url)

        self.mock_client.get.side_effect = _mock_get

        # Host with no uuid
        host = fakes.FakeHostState('host1', 'node1', {})
        spec = self._make_spec([({"PGPU": 1}, set())])

        score = self.accel_weigher._weigh_object(host, spec)
        # Should still work via HTTP lookup
        self.assertEqual(1.0, score)  # free=2, required=1, slack=1

    # ---------- Cache tests ----------

    def test_pass_cache_cleared_on_weigh_objects(self):
        """Cache is cleared at the start of each weigh_objects call."""
        weigher = accelerator.AcceleratorWeigher()
        weigher._pass_cache = {'stale-root': ('ptree', 'usages')}

        # weigh_objects should clear cache (even with empty list)
        weigher.weigh_objects([], FakeRequestSpec())
        self.assertEqual({}, weigher._pass_cache)

    # ---------- RC pattern matching ----------

    def test_non_accel_rc_ignored(self):
        """Non-accelerator RCs (e.g., VCPU, MEMORY_MB) are ignored."""
        self.flags(policy='sum-fit', group='accelerator_weigher')
        root_uuid = 'root-1'

        self._setup_provider_tree(
            root_uuid,
            {'child-1': FakeProviderData(
                inventory={'PGPU': {'total': 4, 'reserved': 0}},
                traits=set())},
            usages_dict={'child-1': {'PGPU': 0}})

        host = self._make_host('host1', 'node1', root_uuid)
        # Mix of accel and non-accel RCs
        spec = self._make_spec([
            ({"VCPU": 4, "MEMORY_MB": 8192, "PGPU": 1}, set()),
        ])

        score = self.accel_weigher._weigh_object(host, spec)
        # Only PGPU is matched: free=4, required=1, slack=3
        self.assertEqual(3.0, score)

    def test_custom_rc_pattern(self):
        """Custom rc_pattern config matches different RCs."""
        self.flags(rc_pattern=r'^CUSTOM_MYACCEL$',
                   policy='sum-fit', group='accelerator_weigher')
        # Clear cached regex
        accelerator._rc_regex_cache = None
        accelerator._rc_regex_pattern = None

        root_uuid = 'root-1'
        self._setup_provider_tree(
            root_uuid,
            {'child-1': FakeProviderData(
                inventory={'CUSTOM_MYACCEL': {'total': 10, 'reserved': 0}},
                traits=set())},
            usages_dict={'child-1': {'CUSTOM_MYACCEL': 3}})

        host = self._make_host('host1', 'node1', root_uuid)
        spec = self._make_spec([
            ({"CUSTOM_MYACCEL": 2}, set()),
        ])

        score = self.accel_weigher._weigh_object(host, spec)
        # free=7, required=2, slack=5
        self.assertEqual(5.0, score)

    # ---------- Edge cases ----------

    def test_provider_tree_failure_returns_zero(self):
        """When get_provider_tree_and_ensure_root fails, score is 0."""
        self.mock_client.get_provider_tree_and_ensure_root.side_effect = \
            Exception("placement unavailable")

        host = self._make_host('host1', 'node1', 'root-1')
        spec = self._make_spec([({"PGPU": 1}, set())])

        score = self.accel_weigher._weigh_object(host, spec)
        self.assertEqual(0.0, score)

    def test_empty_group_resources_returns_unmet(self):
        """A request group with only non-accel RCs → group is skipped."""
        self.flags(policy='sum-fit', group='accelerator_weigher')
        root_uuid = 'root-1'

        self._setup_provider_tree(root_uuid, {})

        host = self._make_host('host1', 'node1', root_uuid)
        # Only non-accel RCs
        spec = self._make_spec([
            ({"VCPU": 2, "MEMORY_MB": 4096}, set()),
        ])

        score = self.accel_weigher._weigh_object(host, spec)
        # No accel groups extracted → returns 0
        self.assertEqual(0.0, score)

    # ---------- Integration: multiple hosts weighed ----------

    def test_multiple_hosts_ranked_by_capacity(self):
        """Hosts with more free accelerator capacity score higher."""
        self.flags(policy='sum-fit', group='accelerator_weigher')

        root_1 = 'root-host1'
        root_2 = 'root-host2'
        root_3 = 'root-host3'

        trees = {
            root_1: FakeProviderTree(root_1, {
                'c1': FakeProviderData(
                    inventory={'PGPU': {'total': 8, 'reserved': 0}},
                    traits=set())}),
            root_2: FakeProviderTree(root_2, {
                'c2': FakeProviderData(
                    inventory={'PGPU': {'total': 4, 'reserved': 0}},
                    traits=set())}),
            root_3: FakeProviderTree(root_3, {
                'c3': FakeProviderData(
                    inventory={'PGPU': {'total': 2, 'reserved': 0}},
                    traits=set())}),
        }
        usages = {
            'c1': {'PGPU': 0},  # 8 free
            'c2': {'PGPU': 0},  # 4 free
            'c3': {'PGPU': 0},  # 2 free
        }

        def _mock_get_tree(ctx, root_uuid):
            return trees[root_uuid]

        self.mock_client.get_provider_tree_and_ensure_root.side_effect = \
            _mock_get_tree

        def _mock_get(url):
            for rp_uuid, usage in usages.items():
                if rp_uuid in url:
                    return _make_usage_response(usage)
            return _make_usage_response({})

        self.mock_client.get.side_effect = _mock_get

        hosts = [
            self._make_host('host1', 'node1', root_1),
            self._make_host('host2', 'node2', root_2),
            self._make_host('host3', 'node3', root_3),
        ]

        spec = self._make_spec([({"PGPU": 1}, set())])

        weigher = accelerator.AcceleratorWeigher()
        weighed = self.weight_handler.get_weighed_objects(
            [weigher], hosts, spec)

        # host1 (slack=7) should rank first, host3 (slack=1) last
        self.assertEqual('host1', weighed[0].obj.host)
        self.assertEqual('host3', weighed[-1].obj.host)

    def test_minval_zero_prevents_negative_normalization(self):
        """minval=0 ensures negative scores normalize to 0, not below."""
        # This tests the BaseWeigher.minval=0 setting
        weigher = accelerator.AcceleratorWeigher()
        self.assertEqual(0, weigher.minval)

    # ---------- Flavor-driven dynamic policy ----------

    def test_flavor_policy_override(self):
        """Flavor extra_spec overrides global config policy."""
        # Global config = sum-fit
        self.flags(policy='sum-fit', group='accelerator_weigher')
        root_uuid = 'root-1'

        self._setup_provider_tree(
            root_uuid,
            {
                'child-1': FakeProviderData(
                    inventory={'PGPU': {'total': 6, 'reserved': 0}},
                    traits=set()),
                'child-2': FakeProviderData(
                    inventory={'FPGA': {'total': 3, 'reserved': 0}},
                    traits=set()),
            },
            usages_dict={
                'child-1': {'PGPU': 0},
                'child-2': {'FPGA': 1},
            })

        host = self._make_host('host1', 'node1', root_uuid)

        # Create spec with flavor extra_spec overriding to product-fit
        spec = self._make_spec([
            ({"PGPU": 2}, set()),  # slack = 6 - 2 = 4
            ({"FPGA": 1}, set()),  # slack = 2 - 1 = 1
        ])
        # Attach flavor with extra_specs
        spec.flavor = type('FakeFlavor', (), {
            'extra_specs': {'accelerator_weigher:policy': 'product-fit'}
        })()

        score = self.accel_weigher._weigh_object(host, spec)
        # With product-fit (from flavor override): (4+eps)*(1+eps) ~= 4
        # With sum-fit (global config): 4 + 1 = 5
        self.assertAlmostEqual(4.0, score, places=4)

    def test_flavor_invalid_policy_falls_back_to_config(self):
        """Invalid flavor policy value falls back to global config."""
        self.flags(policy='sum-fit', group='accelerator_weigher')
        root_uuid = 'root-1'

        self._setup_provider_tree(
            root_uuid,
            {'child-1': FakeProviderData(
                inventory={'PGPU': {'total': 4, 'reserved': 0}},
                traits=set())},
            usages_dict={'child-1': {'PGPU': 0}})

        host = self._make_host('host1', 'node1', root_uuid)
        spec = self._make_spec([({"PGPU": 1}, set())])
        spec.flavor = type('FakeFlavor', (), {
            'extra_specs': {'accelerator_weigher:policy': 'invalid-policy'}
        })()

        score = self.accel_weigher._weigh_object(host, spec)
        # Falls back to sum-fit: slack = 4 - 1 = 3
        self.assertEqual(3.0, score)

    def test_flavor_no_extra_specs_uses_config(self):
        """When flavor has no extra_specs, use global config."""
        self.flags(policy='product-fit', group='accelerator_weigher')
        root_uuid = 'root-1'

        self._setup_provider_tree(
            root_uuid,
            {'child-1': FakeProviderData(
                inventory={'PGPU': {'total': 5, 'reserved': 0}},
                traits=set())},
            usages_dict={'child-1': {'PGPU': 1}})

        host = self._make_host('host1', 'node1', root_uuid)
        spec = self._make_spec([({"PGPU": 2}, set())])
        # No flavor attribute at all
        score = self.accel_weigher._weigh_object(host, spec)
        # product-fit: (2 + EPS) ≈ 2.0
        self.assertAlmostEqual(2.0, score, places=4)
