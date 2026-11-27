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

import json
import unittest

from cyborg_dashboard.tests import base  # noqa: F401  (django.setup)

from cyborg_dashboard.content.admin.accelerator.device_profiles import tables


class RenderGroupsTest(unittest.TestCase):

    def test_renders_groups_as_json(self):
        # Matches the API payload and `openstack accelerator device profile
        # list`, so a value round-trips between the CLI and the dashboard.
        groups = [{'resources:CUSTOM_AICHIP': '1',
                   'trait:CUSTOM_FURIOSA_0001': 'required'}]
        rendered = tables.render_groups({'groups': groups})
        self.assertEqual(groups, json.loads(rendered))

    def test_output_is_valid_json(self):
        rendered = tables.render_groups({'groups': [{'resources:FPGA': '1'}]})
        # Would raise if not valid JSON (e.g. Python single-quote repr).
        json.loads(rendered)
        self.assertIn('"resources:FPGA"', rendered)

    def test_preserves_key_order(self):
        group = {'resources:FPGA': '1', 'trait:CUSTOM_A': 'required'}
        rendered = tables.render_groups({'groups': [group]})
        self.assertLess(rendered.index('resources:FPGA'),
                        rendered.index('trait:CUSTOM_A'))

    def test_multiple_groups(self):
        groups = [{'resources:FPGA': '1'}, {'resources:GPU': '2'}]
        self.assertEqual(groups,
                         json.loads(tables.render_groups({'groups': groups})))

    def test_missing_groups_renders_empty_list(self):
        self.assertEqual([], json.loads(tables.render_groups({})))


if __name__ == '__main__':
    unittest.main()
