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

from horizon import exceptions
from horizon import forms
from horizon import messages

from cyborg_dashboard.api import cyborg as cyborg_api
from cyborg_dashboard.content.admin.accelerator.device_profiles import (
    validation)


class CreateDeviceProfileForm(forms.SelfHandlingForm):
    name = forms.RegexField(
        label=_("Name"),
        max_length=255,
        regex=validation.NAME_RE,
        error_messages={'invalid': _(
            "Name may only contain letters, numbers, hyphens and "
            "underscores.")})
    description = forms.CharField(
        label=_("Description"),
        required=False,
        max_length=255)
    groups = forms.CharField(
        label=_("Groups"),
        widget=forms.Textarea(attrs={'rows': 6}),
        help_text=_(
            "One 'key=value' per line; leave a blank line to start another "
            "group. Keys must start with 'resources:', 'trait:' or 'accel:'. "
            "Example:\n"
            "resources:CUSTOM_AICHIP=1\n"
            "trait:CUSTOM_FURIOSA_0001=required\n"
            "\n"
            "The CLI/API JSON form is also accepted, so a value from "
            "'openstack accelerator device profile show' pastes in as-is:\n"
            '[{"resources:CUSTOM_AICHIP": 1, '
            '"trait:CUSTOM_FURIOSA_0001": "required"}]'))

    def clean_groups(self):
        # Full group validation here so the error attaches to the groups field
        # instead of coming back as an HTTP 400 from Cyborg.
        data = self.cleaned_data['groups']
        try:
            self._groups = validation.parse_groups_auto(data)
            validation.validate_groups(self._groups)
        except validation.ValidationError as exc:
            raise forms.ValidationError(str(exc))
        return data

    def clean(self):
        cleaned = super().clean()
        # Assemble the profile once the fields are individually clean.
        if cleaned.get('name') and getattr(self, '_groups', None):
            self._profile = {'name': cleaned['name'], 'groups': self._groups}
            if cleaned.get('description'):
                self._profile['description'] = cleaned['description']
        return cleaned

    def handle(self, request, data):
        try:
            cyborg_api.device_profile_create(request, self._profile)
            messages.success(
                request,
                _('Created device profile "%s".') % data['name'])
            return True
        except Exception:
            exceptions.handle(request,
                              _('Unable to create device profile.'))
            return False
