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

"""Pure parsing and validation for device profile input.

No Django or Horizon imports, so it can be unit tested standalone. It mirrors
Cyborg's server-side ``_validate_post_request`` (api/controllers/v2/
device_profiles.py) so the form can reject bad input before a round trip
rather than surfacing a raw HTTP 400.

Keep the rules in sync with the Cyborg release this plugin targets.
"""

import json
import re

NAME_RE = re.compile(r'^[a-zA-Z0-9-_]+$')
GROUP_KEY_RE = re.compile(r'^(resources:|trait:|accel:)')

TRAIT_VALUES = ('required', 'forbidden')

# From cyborg.common.constants.SUPPORT_RESOURCES. A resource class must be one
# of these or a CUSTOM_ class.
SUPPORT_RESOURCES = (
    'FPGA', 'GPU', 'VGPU', 'PGPU',
    'CUSTOM_QAT', 'CUSTOM_NIC', 'CUSTOM_SSD', 'CUSTOM_AICHIP',
)


class ValidationError(Exception):
    """Raised with a human-readable message when input is invalid."""


def parse_groups(text):
    """Parse the groups textarea into a list of group dicts.

    One ``key=value`` per line; a blank line starts a new group. So::

        resources:FPGA=1
        trait:CUSTOM_FPGA_INTEL=required

        resources:GPU=1

    parses to two groups. Whitespace around keys and values is stripped.
    Raises ValidationError on a line without '='.
    """
    groups = []
    current = {}
    for lineno, raw in enumerate(text.splitlines(), 1):
        line = raw.strip()
        if not line:
            if current:
                groups.append(current)
                current = {}
            continue
        if '=' not in line:
            raise ValidationError(
                "Line %d is not 'key=value': %r" % (lineno, raw))
        key, value = line.split('=', 1)
        current[key.strip()] = value.strip()
    if current:
        groups.append(current)
    return groups


def parse_groups_json(text):
    """Parse groups from the CLI's JSON form: a list of group objects.

    Matches ``openstack accelerator device profile`` so a value copied from
    the CLI or the API pastes in unchanged. A bare object is wrapped in a
    list. Values may be numbers in JSON (``"resources:FPGA": 1``); the
    key=value form and stored profiles use strings, so they are normalised.
    """
    try:
        data = json.loads(text)
    except ValueError as exc:
        raise ValidationError("Groups is not valid JSON: %s" % exc)
    if isinstance(data, dict):
        data = [data]
    if not isinstance(data, list) or not all(
            isinstance(group, dict) for group in data):
        raise ValidationError(
            'JSON groups must be a list of objects, e.g. '
            '[{"resources:FPGA": 1}].')
    return [{str(k): str(v) for k, v in group.items()} for group in data]


def parse_groups_auto(text):
    """Parse groups from either the key=value textarea or the JSON form.

    Text that starts with ``[`` or ``{`` is treated as JSON; anything else
    is the key=value form.
    """
    stripped = (text or '').strip()
    if stripped.startswith('[') or stripped.startswith('{'):
        return parse_groups_json(stripped)
    return parse_groups(text)


def _validate_group(group):
    for key, value in group.items():
        if not GROUP_KEY_RE.match(key):
            raise ValidationError(
                "Group key %r must start with 'resources:', 'trait:' or "
                "'accel:'." % key)
        if key.startswith('trait:'):
            trait = key[len('trait:'):].strip()
            if not trait.startswith('CUSTOM_'):
                raise ValidationError(
                    "Trait name %r must start with 'CUSTOM_'." % trait)
            if value not in TRAIT_VALUES:
                raise ValidationError(
                    "Trait value %r must be one of %s."
                    % (value, ', '.join(TRAIT_VALUES)))
        elif key.startswith('resources:'):
            rc = key[len('resources:'):].strip()
            if rc not in SUPPORT_RESOURCES and not rc.startswith('CUSTOM_'):
                raise ValidationError(
                    "Unsupported resource class %r. Use one of %s or a "
                    "CUSTOM_ class." % (rc, ', '.join(SUPPORT_RESOURCES)))
            try:
                int(value)
            except (TypeError, ValueError):
                raise ValidationError(
                    "Resource amount for %r must be an integer, got %r."
                    % (rc, value))
        # accel: keys are accepted as-is; Cyborg does not constrain them here.


def validate_name(name):
    """Validate the device profile name. Raises ValidationError if invalid."""
    if not name:
        raise ValidationError("A device profile name is required.")
    if not NAME_RE.match(name):
        raise ValidationError(
            "Device profile name %r must match %s." % (name, NAME_RE.pattern))


def validate_groups(groups):
    """Validate the parsed groups. Raises ValidationError if invalid.

    :param groups: list of group dicts (from :func:`parse_groups`)
    """
    if not groups:
        raise ValidationError("At least one group is required.")
    for group in groups:
        if not group:
            raise ValidationError("A group must have at least one key.")
        _validate_group(group)


def validate(name, groups):
    """Validate a parsed device profile. Raises ValidationError if invalid."""
    validate_name(name)
    validate_groups(groups)


def build_profile(name, groups_text, description=None):
    """Parse and validate input into a device profile dict ready for the API.

    Raises ValidationError on any problem. The returned dict is the single
    profile object (the API layer wraps it in a list).
    """
    groups = parse_groups_auto(groups_text or '')
    validate(name, groups)
    profile = {'name': name, 'groups': groups}
    if description:
        profile['description'] = description
    return profile
