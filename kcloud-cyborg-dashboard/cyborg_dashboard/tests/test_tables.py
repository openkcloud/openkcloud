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

from cyborg_dashboard.tests import base  # noqa: F401  (django.setup)

from cyborg_dashboard.content.admin.accelerator import tables


class DeviceInstanceLinkTest(unittest.TestCase):

    def test_instance_name_is_escaped(self):
        html = tables.get_device_instance_link({'attached_instances': [
            {'instance_uuid': 'uuid-1',
             'instance_name': '<script>alert(1)</script>'},
        ]})
        self.assertNotIn('<script>', html)
        self.assertIn('&lt;script&gt;', html)

    def test_links_to_instance_detail(self):
        html = tables.get_device_instance_link({'attached_instances': [
            {'instance_uuid': 'uuid-1', 'instance_name': 'gpu-vm'},
        ]})
        self.assertIn('uuid-1', html)
        self.assertIn('>gpu-vm</a>', html)

    def test_no_instances_renders_dash(self):
        self.assertEqual('-', tables.get_device_instance_link({}))
        self.assertEqual(
            '-', tables.get_device_instance_link({'attached_instances': []}))

    def test_entry_without_uuid_is_skipped(self):
        html = tables.get_device_instance_link({'attached_instances': [
            {'instance_name': 'orphan'},
        ]})
        self.assertEqual('-', html)

    def test_falls_back_to_uuid_when_name_missing(self):
        html = tables.get_device_instance_link({'attached_instances': [
            {'instance_uuid': 'uuid-1', 'instance_name': None},
        ]})
        self.assertIn('>uuid-1</a>', html)


class DevicesTableTest(unittest.TestCase):

    def test_attached_instances_column_uses_callable_transform(self):
        column = tables.DevicesTable.base_columns['attached_instances']
        self.assertTrue(callable(column.transform))

    def test_object_id_prefers_uuid(self):
        get_id = tables.DevicesTable.get_object_id
        self.assertEqual('u1', get_id(None, {'uuid': 'u1', 'id': 7}))

    def test_object_id_falls_back_to_id(self):
        # Without the fallback every uuid-less device collapsed onto ''.
        get_id = tables.DevicesTable.get_object_id
        self.assertEqual('7', get_id(None, {'id': 7}))

    def test_object_id_empty_when_unidentifiable(self):
        get_id = tables.DevicesTable.get_object_id
        self.assertEqual('', get_id(None, {}))


if __name__ == '__main__':
    unittest.main()
