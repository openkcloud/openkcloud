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

from cyborg_dashboard.content.admin.accelerator import panel


IS_ENABLED = 'openstack_dashboard.api.base.is_service_enabled'


class AcceleratorPanelTest(unittest.TestCase):

    def test_admin_role_still_required(self):
        self.assertIn('openstack.roles.admin', panel.Accelerator.permissions)

    def test_policy_rule_targets_the_api_the_panel_calls(self):
        # The panel's landing view lists devices; gate on that rule.
        self.assertEqual((("accelerator", "cyborg:device:get_all"),),
                         panel.Accelerator.policy_rules)

    def test_hidden_when_accelerator_service_absent(self):
        with mock.patch(IS_ENABLED, return_value=False):
            allowed = panel.Accelerator().allowed({'request': mock.Mock()})
        self.assertFalse(allowed)

    def test_service_present_defers_to_base_check(self):
        with mock.patch(IS_ENABLED, return_value=True), \
                mock.patch('horizon.Panel._can_access',
                           return_value=True) as can_access:
            allowed = panel.Accelerator().allowed({'request': mock.Mock()})
        self.assertTrue(allowed)
        can_access.assert_called_once()

    def test_service_present_but_policy_denies_hides_panel(self):
        with mock.patch(IS_ENABLED, return_value=True), \
                mock.patch('horizon.Panel._can_access', return_value=False):
            allowed = panel.Accelerator().allowed({'request': mock.Mock()})
        self.assertFalse(allowed)


if __name__ == '__main__':
    unittest.main()
