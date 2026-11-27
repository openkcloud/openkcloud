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

from cyborg_dashboard.api import placement


ADAPTER = 'cyborg_dashboard.api.placement.make_adapter'
URLFOR = 'openstack_dashboard.api.base.url_for'
RP = '7f3edca8'


class PlacementUrlTest(unittest.TestCase):

    def test_url_does_not_double_slash_a_trailing_slash_endpoint(self):
        # The whole point: a catalog endpoint with a trailing slash must not
        # yield '//resource_providers', which Placement 404s.
        with mock.patch(URLFOR,
                        return_value='http://placement.local:8778/'):
            url = placement._url(mock.Mock(),
                                 'resource_providers/%s/traits' % RP)
        self.assertEqual(
            'http://placement.local:8778/resource_providers/%s/traits' % RP,
            url)
        self.assertNotIn('//resource_providers', url)

    def test_url_handles_no_trailing_slash_too(self):
        with mock.patch(URLFOR, return_value='http://placement.local:8778'):
            url = placement._url(mock.Mock(), 'resource_providers')
        self.assertEqual('http://placement.local:8778/resource_providers',
                         url)


class PlacementReadTest(unittest.TestCase):

    def setUp(self):
        self.client = mock.Mock()
        p1 = mock.patch(ADAPTER, return_value=self.client)
        p2 = mock.patch(URLFOR, return_value='http://placement.local:8778/')
        p1.start()
        p2.start()
        self.addCleanup(p1.stop)
        self.addCleanup(p2.stop)

    def test_inventories_returns_the_inventories_mapping(self):
        self.client.get.return_value = (
            mock.Mock(), {'inventories': {'PGPU': {'total': 1}}})
        self.assertEqual(
            {'PGPU': {'total': 1}},
            placement.resource_provider_inventories(mock.Mock(), RP))

    def test_traits_returns_the_trait_list(self):
        self.client.get.return_value = (
            mock.Mock(), {'traits': ['CUSTOM_NVIDIA_1E78']})
        self.assertEqual(
            ['CUSTOM_NVIDIA_1E78'],
            placement.resource_provider_traits(mock.Mock(), RP))

    def test_missing_keys_default_empty(self):
        self.client.get.return_value = (mock.Mock(), {})
        self.assertEqual(
            {}, placement.resource_provider_inventories(mock.Mock(), RP))
        self.assertEqual(
            [], placement.resource_provider_traits(mock.Mock(), RP))


if __name__ == '__main__':
    unittest.main()
