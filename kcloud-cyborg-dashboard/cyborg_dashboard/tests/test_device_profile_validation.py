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

from cyborg_dashboard.content.admin.accelerator.device_profiles import (
    validation)


class ParseGroupsTest(unittest.TestCase):

    def test_single_group(self):
        text = "resources:FPGA=1\ntrait:CUSTOM_FPGA_INTEL=required"
        self.assertEqual(
            [{'resources:FPGA': '1', 'trait:CUSTOM_FPGA_INTEL': 'required'}],
            validation.parse_groups(text))

    def test_blank_line_separates_groups(self):
        text = "resources:FPGA=1\n\nresources:GPU=1"
        self.assertEqual(
            [{'resources:FPGA': '1'}, {'resources:GPU': '1'}],
            validation.parse_groups(text))

    def test_whitespace_is_stripped(self):
        self.assertEqual(
            [{'resources:FPGA': '1'}],
            validation.parse_groups("  resources:FPGA = 1  "))

    def test_trailing_and_leading_blanks_do_not_make_empty_groups(self):
        self.assertEqual(
            [{'resources:FPGA': '1'}],
            validation.parse_groups("\n\nresources:FPGA=1\n\n"))

    def test_value_may_contain_equals(self):
        self.assertEqual(
            [{'trait:CUSTOM_A': 'required'}],
            validation.parse_groups("trait:CUSTOM_A=required"))

    def test_empty_text_is_no_groups(self):
        self.assertEqual([], validation.parse_groups(""))

    def test_line_without_equals_is_rejected(self):
        with self.assertRaises(validation.ValidationError):
            validation.parse_groups("resources:FPGA")


class ParseGroupsJsonTest(unittest.TestCase):

    def test_parses_a_json_list(self):
        text = ('[{"resources:CUSTOM_AICHIP": 1, '
                '"trait:CUSTOM_FURIOSA_0001": "required"}]')
        self.assertEqual(
            [{'resources:CUSTOM_AICHIP': '1',
              'trait:CUSTOM_FURIOSA_0001': 'required'}],
            validation.parse_groups_json(text))

    def test_wraps_a_bare_object_in_a_list(self):
        self.assertEqual(
            [{'resources:FPGA': '1'}],
            validation.parse_groups_json('{"resources:FPGA": 1}'))

    def test_numbers_are_normalised_to_strings(self):
        out = validation.parse_groups_json('[{"resources:FPGA": 2}]')
        self.assertEqual('2', out[0]['resources:FPGA'])

    def test_invalid_json_raises(self):
        with self.assertRaises(validation.ValidationError):
            validation.parse_groups_json('[{not json}]')

    def test_non_object_elements_rejected(self):
        with self.assertRaises(validation.ValidationError):
            validation.parse_groups_json('["resources:FPGA=1"]')


class ParseGroupsAutoTest(unittest.TestCase):

    def test_json_when_it_starts_with_bracket(self):
        self.assertEqual(
            [{'resources:FPGA': '1'}],
            validation.parse_groups_auto('[{"resources:FPGA": 1}]'))

    def test_json_when_it_starts_with_brace(self):
        self.assertEqual(
            [{'resources:FPGA': '1'}],
            validation.parse_groups_auto('{"resources:FPGA": 1}'))

    def test_key_value_otherwise(self):
        self.assertEqual(
            [{'resources:FPGA': '1'}],
            validation.parse_groups_auto('resources:FPGA=1'))

    def test_leading_whitespace_before_json_still_detected(self):
        self.assertEqual(
            [{'resources:FPGA': '1'}],
            validation.parse_groups_auto('  [{"resources:FPGA": 1}]  '))

    def test_both_forms_reach_the_same_validation(self):
        # A JSON group with a bad resource class is caught by validate_groups
        # exactly as the key=value form would be.
        groups = validation.parse_groups_auto('[{"resources:WIDGET": 1}]')
        with self.assertRaises(validation.ValidationError):
            validation.validate_groups(groups)


class ValidateTest(unittest.TestCase):

    def _valid_group(self):
        return {'resources:FPGA': '1'}

    def test_accepts_a_minimal_profile(self):
        validation.validate('fpga-dp', [self._valid_group()])

    def test_name_required(self):
        with self.assertRaises(validation.ValidationError):
            validation.validate('', [self._valid_group()])

    def test_name_must_match_pattern(self):
        with self.assertRaises(validation.ValidationError):
            validation.validate('bad name!', [self._valid_group()])

    def test_at_least_one_group(self):
        with self.assertRaises(validation.ValidationError):
            validation.validate('dp', [])

    def test_empty_group_rejected(self):
        with self.assertRaises(validation.ValidationError):
            validation.validate('dp', [{}])

    def test_bad_group_key_prefix(self):
        with self.assertRaises(validation.ValidationError):
            validation.validate('dp', [{'cpu:1': '1'}])

    def test_trait_name_must_be_custom(self):
        with self.assertRaises(validation.ValidationError):
            validation.validate('dp', [{'trait:HW_GPU': 'required'}])

    def test_trait_value_must_be_required_or_forbidden(self):
        with self.assertRaises(validation.ValidationError):
            validation.validate('dp', [{'trait:CUSTOM_X': 'maybe'}])

    def test_trait_forbidden_is_accepted(self):
        validation.validate('dp', [{'trait:CUSTOM_X': 'forbidden'}])

    def test_known_resource_class_accepted(self):
        for rc in validation.SUPPORT_RESOURCES:
            validation.validate('dp', [{'resources:%s' % rc: '1'}])

    def test_custom_resource_class_accepted(self):
        validation.validate('dp', [{'resources:CUSTOM_THING': '2'}])

    def test_unknown_resource_class_rejected(self):
        with self.assertRaises(validation.ValidationError):
            validation.validate('dp', [{'resources:WIDGET': '1'}])

    def test_resource_amount_must_be_integer(self):
        with self.assertRaises(validation.ValidationError):
            validation.validate('dp', [{'resources:FPGA': 'two'}])

    def test_accel_key_accepted_as_is(self):
        validation.validate('dp', [{'accel:something': 'whatever'}])


class BuildProfileTest(unittest.TestCase):

    def test_builds_dict_without_description(self):
        profile = validation.build_profile('dp', "resources:FPGA=1")
        self.assertEqual(
            {'name': 'dp', 'groups': [{'resources:FPGA': '1'}]}, profile)

    def test_includes_description_when_given(self):
        profile = validation.build_profile(
            'dp', "resources:FPGA=1", description="an fpga")
        self.assertEqual('an fpga', profile['description'])

    def test_blank_description_is_omitted(self):
        profile = validation.build_profile(
            'dp', "resources:FPGA=1", description="")
        self.assertNotIn('description', profile)

    def test_invalid_input_raises_before_building(self):
        with self.assertRaises(validation.ValidationError):
            validation.build_profile('dp', "resources:WIDGET=1")

    def test_accepts_json_groups(self):
        profile = validation.build_profile(
            'dp', '[{"resources:FPGA": 1, "trait:CUSTOM_A": "required"}]')
        self.assertEqual(
            [{'resources:FPGA': '1', 'trait:CUSTOM_A': 'required'}],
            profile['groups'])


if __name__ == '__main__':
    unittest.main()
