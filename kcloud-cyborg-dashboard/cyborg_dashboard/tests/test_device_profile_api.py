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

import unittest
from unittest import mock

from cyborg_dashboard.tests import base  # noqa: F401  (django.setup)

from cyborg_dashboard.api import cyborg


ADAPTER = 'cyborg_dashboard.api.cyborg.make_adapter'
URLFOR = 'openstack_dashboard.api.base.url_for'


class DeviceProfileApiTest(unittest.TestCase):

    def setUp(self):
        self.client = mock.Mock()
        self.url_patch = mock.patch(
            URLFOR, return_value='http://accel.example/v2')
        self.adapter_patch = mock.patch(ADAPTER, return_value=self.client)
        self.url_patch.start()
        self.adapter_patch.start()
        self.addCleanup(self.url_patch.stop)
        self.addCleanup(self.adapter_patch.stop)

    def test_create_wraps_profile_in_a_single_element_list(self):
        # Cyborg's POST body is a list and it rejects anything but one entry.
        self.client.post.return_value = (mock.Mock(), {'uuid': 'dp-1'})
        profile = {'name': 'dp', 'groups': [{'resources:FPGA': '1'}]}

        cyborg.device_profile_create(mock.Mock(), profile)

        _args, kwargs = self.client.post.call_args
        self.assertEqual([profile], kwargs['json'])

    def test_create_targets_the_device_profiles_collection(self):
        self.client.post.return_value = (mock.Mock(), {})
        cyborg.device_profile_create(mock.Mock(), {'name': 'dp'})
        url = self.client.post.call_args[0][0]
        self.assertEqual('http://accel.example/v2/device_profiles', url)

    def test_delete_targets_the_named_resource(self):
        cyborg.device_profile_delete(mock.Mock(), 'dp-1')
        url = self.client.delete.call_args[0][0]
        self.assertEqual(
            'http://accel.example/v2/device_profiles/dp-1', url)

    def test_get_unwraps_single_element_list(self):
        self.client.get.return_value = (
            mock.Mock(), {'device_profiles': [{'name': 'dp'}]})
        self.assertEqual(
            {'name': 'dp'}, cyborg.device_profile_get(mock.Mock(), 'dp'))

    def test_get_returns_none_when_empty(self):
        self.client.get.return_value = (
            mock.Mock(), {'device_profiles': []})
        self.assertIsNone(cyborg.device_profile_get(mock.Mock(), 'nope'))


if __name__ == '__main__':
    unittest.main()
