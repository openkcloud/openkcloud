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

from django.urls import reverse_lazy
from django.utils.translation import gettext_lazy as _

from horizon import exceptions
from horizon import forms
from horizon import tables

from cyborg_dashboard.api import cyborg as cyborg_api
from cyborg_dashboard.content.admin.accelerator.device_profiles import (
    forms as device_profile_forms)
from cyborg_dashboard.content.admin.accelerator.device_profiles import (
    tables as device_profile_tables)


class IndexView(tables.DataTableView):
    table_class = device_profile_tables.DeviceProfilesTable
    template_name = 'admin/device_profiles/index.html'
    page_title = _("Device Profiles")

    def get_data(self):
        try:
            return cyborg_api.device_profile_list(self.request)
        except Exception:
            exceptions.handle(
                self.request,
                _('Unable to retrieve device profiles.'))
            return []


class CreateView(forms.ModalFormView):
    form_class = device_profile_forms.CreateDeviceProfileForm
    template_name = 'admin/device_profiles/create.html'
    # submit_url is required: _modal_form.html renders it as the form action,
    # so without it the modal posts to '.../None' and 404s.
    submit_url = reverse_lazy('horizon:admin:device_profiles:create')
    success_url = reverse_lazy('horizon:admin:device_profiles:index')
    page_title = _("Create Device Profile")
    submit_label = _("Create")
