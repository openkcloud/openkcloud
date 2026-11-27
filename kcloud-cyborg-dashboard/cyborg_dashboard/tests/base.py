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

"""Django bootstrap for the tests that touch Horizon.

``tox.ini`` points DJANGO_SETTINGS_MODULE at Horizon's own test settings;
importing this module first makes the app registry usable. Tests that only
exercise pure logic (see ``test_usage``) must not import this.
"""

import os

import django

os.environ.setdefault('DJANGO_SETTINGS_MODULE',
                      'openstack_dashboard.test.settings')
django.setup()
