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

from django.utils.translation import gettext_lazy as _
from django.utils.translation import ngettext_lazy

from horizon import tables

from cyborg_dashboard.api import cyborg as cyborg_api


def render_groups(device_profile):
    """Render groups as JSON, matching the API and the openstack CLI.

    ``openstack accelerator device profile list`` shows the raw group list,
    so the panel does the same instead of a bespoke 'key=value | ...' form,
    keeping a value copy/paste-able between the CLI and the dashboard.
    """
    groups = device_profile.get('groups') or []
    return json.dumps(groups, ensure_ascii=False)


class CreateDeviceProfile(tables.LinkAction):
    name = "create"
    verbose_name = _("Create Device Profile")
    url = "horizon:admin:device_profiles:create"
    classes = ("ajax-modal",)
    icon = "plus"
    policy_rules = (("accelerator", "cyborg:device_profile:create"),)


class DeleteDeviceProfile(tables.DeleteAction):
    policy_rules = (("accelerator", "cyborg:device_profile:delete"),)

    @staticmethod
    def action_present(count):
        return ngettext_lazy(
            u"Delete Device Profile",
            u"Delete Device Profiles",
            count)

    @staticmethod
    def action_past(count):
        return ngettext_lazy(
            u"Deleted Device Profile",
            u"Deleted Device Profiles",
            count)

    def delete(self, request, obj_id):
        # obj_id is the uuid (see get_object_id); Cyborg accepts uuid or name.
        cyborg_api.device_profile_delete(request, obj_id)


class DeviceProfilesTable(tables.DataTable):
    name = tables.Column("name", verbose_name=_("Name"))
    uuid = tables.Column("uuid", verbose_name=_("UUID"))
    description = tables.Column("description", verbose_name=_("Description"))
    groups = tables.Column(render_groups, verbose_name=_("Groups"))

    def get_object_id(self, device_profile):
        return device_profile.get('uuid') or device_profile.get('name', '')

    class Meta(object):
        name = "device_profiles"
        verbose_name = _("Device Profiles")
        table_actions = (CreateDeviceProfile, DeleteDeviceProfile)
        row_actions = (DeleteDeviceProfile,)
