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

"""Minimal Placement client for reading a device's resource class and traits.

openstack_dashboard.api.placement builds request URLs as
``url_for('placement') + path``. When the Placement endpoint is registered
in the catalog with a trailing slash (as it is under openstack-helm), the
leading slash on ``path`` produces ``//resource_providers`` and Placement
answers an authenticated request to that doubled path with a 404. This
module joins the URL the same defensive way cyborg.py does
(``rstrip('/') + '/' + lstrip('/')``) so the resource class and trait
columns work regardless of the trailing slash.
"""

from django.conf import settings
from keystoneauth1 import adapter
from keystoneauth1 import identity
from keystoneauth1 import session

from openstack_dashboard.api import base

from horizon.utils.memoized import memoized

# Traits were added to the Placement API at microversion 1.6.
API_VERSION = "placement 1.6"


class Adapter(adapter.LegacyJsonAdapter):
    def __init__(self, *args, **kwargs):
        self.api_version = kwargs.pop('api_version', None)
        super().__init__(*args, **kwargs)

    def request(self, url, method, **kwargs):
        kwargs.setdefault('headers', kwargs.get('headers', {}))
        if self.api_version is not None:
            kwargs['headers']['OpenStack-API-Version'] = self.api_version
        return super().request(url, method, **kwargs)


@memoized
def make_adapter(request):
    auth = identity.Token(
        auth_url=base.url_for(request, 'identity'),
        token=request.user.token.id,
        project_id=request.user.project_id,
        project_name=request.user.project_name,
        project_domain_id=request.user.domain_id,
    )
    verify = True
    if settings.OPENSTACK_SSL_NO_VERIFY:
        verify = False
    elif settings.OPENSTACK_SSL_CACERT:
        verify = settings.OPENSTACK_SSL_CACERT
    return Adapter(
        session.Session(auth=auth, verify=verify),
        api_version=API_VERSION,
    )


def _url(request, path):
    base_uri = base.url_for(request, 'placement')
    return base_uri.rstrip('/') + '/' + path.lstrip('/')


def _get_json(request, path):
    client = make_adapter(request)
    _response, body = client.get(_url(request, path))
    return body if isinstance(body, dict) else {}


def resource_provider_inventories(request, rp_uuid):
    """Return the resource classes a provider offers, e.g. {'PGPU': {...}}."""
    return _get_json(
        request,
        'resource_providers/%s/inventories' % rp_uuid).get('inventories', {})


def resource_provider_traits(request, rp_uuid):
    """Return the trait names on a provider, e.g. ['CUSTOM_NVIDIA_1E78']."""
    return _get_json(
        request,
        'resource_providers/%s/traits' % rp_uuid).get('traits', [])
