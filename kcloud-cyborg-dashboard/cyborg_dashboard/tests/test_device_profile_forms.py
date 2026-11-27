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

from cyborg_dashboard.content.admin.accelerator.device_profiles import forms


CREATE = ('cyborg_dashboard.content.admin.accelerator.device_profiles'
          '.forms.cyborg_api.device_profile_create')


def _form(name='dp', groups='resources:FPGA=1', description=''):
    data = {'name': name, 'groups': groups, 'description': description}
    return forms.CreateDeviceProfileForm(mock.Mock(), data=data)


class CreateDeviceProfileFormTest(unittest.TestCase):

    def test_valid_input_builds_profile_and_calls_create(self):
        form = _form(groups='resources:FPGA=1\ntrait:CUSTOM_A=required')
        self.assertTrue(form.is_valid(), form.errors)
        with mock.patch(CREATE) as create, \
                mock.patch('horizon.messages.success'):
            self.assertTrue(form.handle(mock.Mock(), form.cleaned_data))
        profile = create.call_args[0][1]
        self.assertEqual('dp', profile['name'])
        self.assertEqual(
            [{'resources:FPGA': '1', 'trait:CUSTOM_A': 'required'}],
            profile['groups'])

    def test_json_input_is_accepted(self):
        form = _form(groups='[{"resources:FPGA": 1, '
                            '"trait:CUSTOM_A": "required"}]')
        self.assertTrue(form.is_valid(), form.errors)
        with mock.patch(CREATE) as create, \
                mock.patch('horizon.messages.success'):
            self.assertTrue(form.handle(mock.Mock(), form.cleaned_data))
        profile = create.call_args[0][1]
        self.assertEqual(
            [{'resources:FPGA': '1', 'trait:CUSTOM_A': 'required'}],
            profile['groups'])

    def test_invalid_json_is_a_field_error(self):
        form = _form(groups='[{bad json}]')
        self.assertFalse(form.is_valid())
        self.assertIn('groups', form.errors)

    def test_bad_group_is_a_field_error_not_a_round_trip(self):
        form = _form(groups='resources:WIDGET=1')
        self.assertFalse(form.is_valid())
        self.assertIn('groups', form.errors)

    def test_bad_name_rejected(self):
        self.assertFalse(_form(name='bad name!').is_valid())

    def test_line_without_equals_rejected(self):
        form = _form(groups='resources:FPGA')
        self.assertFalse(form.is_valid())
        self.assertIn('groups', form.errors)

    def test_description_is_optional(self):
        self.assertTrue(_form(description='').is_valid())

    def test_handle_reports_failure_on_api_error(self):
        form = _form()
        self.assertTrue(form.is_valid(), form.errors)
        with mock.patch(CREATE, side_effect=RuntimeError('boom')), \
                mock.patch('horizon.exceptions.handle'):
            self.assertFalse(form.handle(mock.Mock(), form.cleaned_data))


if __name__ == '__main__':
    unittest.main()
