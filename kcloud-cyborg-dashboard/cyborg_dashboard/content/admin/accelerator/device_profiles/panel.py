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

from django.utils.translation import gettext_lazy as _

import horizon

from openstack_dashboard import api


class DeviceProfiles(horizon.Panel):
    name = _("Device Profiles")
    slug = 'device_profiles'
    permissions = ('openstack.roles.admin',)
    # Listing gates the panel; create/delete are gated per-action in tables.py
    # against cyborg:device_profile:{create,delete}. Inert until the operator
    # registers a Cyborg policy file under the 'accelerator' key.
    policy_rules = (("accelerator", "cyborg:device_profile:get_all"),)

    def allowed(self, context):
        request = context['request']
        if not api.base.is_service_enabled(request, 'accelerator'):
            return False
        return super().allowed(context)
