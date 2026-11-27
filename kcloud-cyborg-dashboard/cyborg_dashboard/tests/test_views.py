#    Licensed under the Apache License, Version 2.0 (the "License"); you may
#    not use this file except in compliance with the License. You may obtain
#    a copy of the License at
#
#         http://www.apache.org/licenses/LICENSE-2.0
#
#    Unless required by applicable law or agreed to in writing, software
#    distributed under the License is distributed on an "AS IS" BASIS,
#    WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#    See the License for the specific language governing permissions and
#    limitations under the License.

import inspect
import unittest
from unittest import mock

from cyborg_dashboard.tests import base  # noqa: F401  (django.setup)

from cyborg_dashboard.content.admin.accelerator import usage
from cyborg_dashboard.content.admin.accelerator import views


class FakeServer(object):
    def __init__(self, id_, name):
        self.id = id_
        self.name = name


NOVA_LIST = 'openstack_dashboard.api.nova.server_list'
PLACEMENT_INV = ('cyborg_dashboard.api.placement'
                 '.resource_provider_inventories')
PLACEMENT_TRAITS = ('cyborg_dashboard.api.placement'
                    '.resource_provider_traits')


def _no_placement():
    # Context managers that make Placement return nothing, for tests that
    # only care about the instance/usage decoration.
    return (mock.patch(PLACEMENT_INV, return_value={}),
            mock.patch(PLACEMENT_TRAITS, return_value=[]))


class ResolveInstanceNamesTest(unittest.TestCase):

    def test_calls_nova_with_a_supported_signature(self):
        """Regression: all_tenants is a search_opt, not a keyword argument.

        Passing it directly raised TypeError on every request, which the
        broad except swallowed, so every instance rendered as a raw uuid.
        """
        from openstack_dashboard.api import nova

        captured = {}

        def fake_server_list(request, search_opts=None, detailed=True):
            captured['search_opts'] = search_opts
            captured['detailed'] = detailed
            return ([], False)

        with mock.patch(NOVA_LIST, side_effect=fake_server_list):
            views._resolve_instance_names(None, {'i-1'})

        self.assertEqual({'all_tenants': True}, captured['search_opts'])
        self.assertFalse(captured['detailed'])
        # Binding proves the call matches the real signature, not just a mock.
        inspect.signature(nova.server_list).bind(
            None, search_opts={'all_tenants': True}, detailed=False)

    def test_instance_without_name_falls_back_to_uuid(self):
        # Instances in a down cell come back without a display name.
        server = mock.Mock(spec=['id'])
        server.id = 'i-1'
        with mock.patch(NOVA_LIST, return_value=([server], False)):
            self.assertEqual({'i-1': 'i-1'},
                             views._resolve_instance_names(None, {'i-1'}))

    def test_maps_uuid_to_name(self):
        with mock.patch(NOVA_LIST,
                        return_value=([FakeServer('i-1', 'gpu-vm')], False)):
            self.assertEqual({'i-1': 'gpu-vm'},
                             views._resolve_instance_names(None, {'i-1'}))

    def test_unknown_uuid_keeps_uuid(self):
        with mock.patch(NOVA_LIST, return_value=([], False)):
            self.assertEqual({'i-9': 'i-9'},
                             views._resolve_instance_names(None, {'i-9'}))

    def test_nova_failure_degrades_to_uuids(self):
        with mock.patch(NOVA_LIST, side_effect=RuntimeError('nova down')):
            self.assertEqual({'i-1': 'i-1'},
                             views._resolve_instance_names(None, {'i-1'}))

    def test_no_uuids_skips_nova_entirely(self):
        with mock.patch(NOVA_LIST) as server_list:
            self.assertEqual({}, views._resolve_instance_names(None, set()))
        server_list.assert_not_called()


class DecorateTest(unittest.TestCase):

    def test_builds_display_dicts_and_label(self):
        deployables = [{'device_id': 1, 'rp_uuid': 'rp-a'}]
        devices = usage.correlate(
            [{'id': 1, 'uuid': 'dev-a'}], deployables,
            [{'device_rp_uuid': 'rp-a', 'instance_uuid': 'i-1'}])
        inv, traits = _no_placement()
        with mock.patch(NOVA_LIST,
                        return_value=([FakeServer('i-1', 'gpu-vm')], False)), \
                inv, traits:
            result = views._decorate(None, devices, deployables)
        self.assertEqual(
            [{'instance_uuid': 'i-1', 'instance_name': 'gpu-vm'}],
            result[0]['attached_instances'])
        self.assertEqual('In Use', str(result[0]['usage_display']))

    def test_unknown_usage_is_labelled_unknown(self):
        devices = usage.correlate([{'id': 1, 'uuid': 'dev-a'}], [], [],
                                  usage_known=False)
        inv, traits = _no_placement()
        with mock.patch(NOVA_LIST, return_value=([], False)), inv, traits:
            result = views._decorate(None, devices, [])
        self.assertEqual('Unknown', str(result[0]['usage_display']))

    def test_available_device_has_no_instances(self):
        devices = usage.correlate([{'id': 1, 'uuid': 'dev-a'}], [], [])
        inv, traits = _no_placement()
        with mock.patch(NOVA_LIST, return_value=([], False)), inv, traits:
            result = views._decorate(None, devices, [])
        self.assertEqual([], result[0]['attached_instances'])
        self.assertEqual('Available', str(result[0]['usage_display']))

    def test_attaches_resource_class_and_traits_from_placement(self):
        deployables = [{'device_id': 1, 'rp_uuid': 'rp-a'}]
        devices = usage.correlate([{'id': 1, 'uuid': 'dev-a'}],
                                  deployables, [])
        with mock.patch(NOVA_LIST, return_value=([], False)), \
                mock.patch(PLACEMENT_INV, return_value={'PGPU': {}}), \
                mock.patch(PLACEMENT_TRAITS,
                           return_value=['CUSTOM_NVIDIA_1E78',
                                         'HW_GPU_API_VULKAN']):
            result = views._decorate(None, devices, deployables)
        self.assertEqual(['PGPU'], result[0]['resource_classes'])
        # HW_ trait filtered out; only the device-specific custom trait shown.
        self.assertEqual(['CUSTOM_NVIDIA_1E78'], result[0]['device_traits'])

    def test_placement_failure_degrades_to_empty(self):
        deployables = [{'device_id': 1, 'rp_uuid': 'rp-a'}]
        devices = usage.correlate([{'id': 1, 'uuid': 'dev-a'}],
                                  deployables, [])
        with mock.patch(NOVA_LIST, return_value=([], False)), \
                mock.patch(PLACEMENT_INV,
                           side_effect=RuntimeError('placement down')), \
                mock.patch(PLACEMENT_TRAITS,
                           side_effect=RuntimeError('placement down')):
            result = views._decorate(None, devices, deployables)
        self.assertEqual([], result[0]['resource_classes'])
        self.assertEqual([], result[0]['device_traits'])


class GetDataTest(unittest.TestCase):
    """The regression that mattered.

    A failed deployable/ARQ lookup must never render as "Available".
    """

    def _view(self):
        view = views.IndexView()
        view.request = mock.Mock()
        return view

    def _patch_api(self, **kwargs):
        target = 'cyborg_dashboard.content.admin.accelerator.views.cyborg_api'
        return mock.patch(target, **kwargs)

    def test_failed_deployable_lookup_reports_unknown(self):
        api = mock.Mock()
        api.device_list.return_value = [{'id': 1, 'uuid': 'dev-a'}]
        api.deployable_list.side_effect = RuntimeError('cyborg down')
        api.accelerator_request_list.return_value = []
        with self._patch_api(new=api), \
                mock.patch(NOVA_LIST, return_value=([], False)), \
                mock.patch('horizon.exceptions.handle'):
            data = self._view().get_data()
        self.assertEqual('Unknown', str(data[0]['usage_display']))

    def test_failed_arq_lookup_reports_unknown(self):
        api = mock.Mock()
        api.device_list.return_value = [{'id': 1, 'uuid': 'dev-a'}]
        api.deployable_list.return_value = []
        api.accelerator_request_list.side_effect = RuntimeError('boom')
        with self._patch_api(new=api), \
                mock.patch(NOVA_LIST, return_value=([], False)), \
                mock.patch('horizon.exceptions.handle'):
            data = self._view().get_data()
        self.assertEqual('Unknown', str(data[0]['usage_display']))

    def test_all_lookups_succeed_reports_available(self):
        api = mock.Mock()
        api.device_list.return_value = [{'id': 1, 'uuid': 'dev-a'}]
        api.deployable_list.return_value = []
        api.accelerator_request_list.return_value = []
        with self._patch_api(new=api), \
                mock.patch(NOVA_LIST, return_value=([], False)):
            data = self._view().get_data()
        self.assertEqual('Available', str(data[0]['usage_display']))

    def test_device_lookup_failure_returns_empty(self):
        api = mock.Mock()
        api.device_list.side_effect = RuntimeError('cyborg down')
        with self._patch_api(new=api), \
                mock.patch('horizon.exceptions.handle'):
            self.assertEqual([], self._view().get_data())


if __name__ == '__main__':
    unittest.main()
