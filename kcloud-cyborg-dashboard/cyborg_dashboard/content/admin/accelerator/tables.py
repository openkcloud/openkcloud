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

from django.urls import reverse
from django.utils.html import escape
from django.utils.safestring import mark_safe
from django.utils.translation import gettext_lazy as _

from horizon import tables


def get_device_instance_link(device):
    """Return links to instance detail, labelled with the instance name."""
    instances = device.get('attached_instances', [])
    if not instances:
        return "-"
    links = []
    for inst in instances:
        iuid = inst.get('instance_uuid')
        if not iuid:
            continue
        url = reverse('horizon:admin:instances:detail', args=[iuid])
        text = escape(inst.get('instance_name') or iuid)
        links.append('<a href="%s">%s</a>' % (escape(url), text))
    return mark_safe(', '.join(links)) if links else "-"


def render_resource_classes(device):
    """Placement resource classes backing the device, e.g. 'PGPU'."""
    return ', '.join(device.get('resource_classes') or []) or '-'


def render_traits(device):
    """Custom Placement traits, e.g. 'CUSTOM_NVIDIA_1E78'.

    These are the values a device profile references, so surfacing them here
    lets an admin read a device's resource class and trait straight off this
    panel when writing a profile.
    """
    return ', '.join(device.get('device_traits') or []) or '-'


class DevicesTable(tables.DataTable):
    uuid = tables.Column("uuid", verbose_name=_("UUID"))
    type = tables.Column("type", verbose_name=_("Type"))
    model = tables.Column("model", verbose_name=_("Model"))
    hostname = tables.Column("hostname", verbose_name=_("Hostname"))
    resource_class = tables.Column(
        render_resource_classes, verbose_name=_("Resource Class"))
    traits = tables.WrappingColumn(
        render_traits, verbose_name=_("Traits"))
    attached_instances = tables.WrappingColumn(
        get_device_instance_link,
        verbose_name=_("Attached Instance(s)"))
    usage = tables.Column("usage_display", verbose_name=_("Usage"))

    def get_object_id(self, device):
        # Fall back to the internal id so devices without a uuid do not all
        # collapse onto the same row id.
        object_id = device.get('uuid') or device.get('id')
        return str(object_id) if object_id is not None else ''

    class Meta(object):
        name = "accelerator_devices"
        verbose_name = _("Accelerator Devices")
