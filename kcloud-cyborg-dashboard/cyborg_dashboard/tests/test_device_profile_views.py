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

from cyborg_dashboard.tests import base  # noqa: F401  (django.setup)

from cyborg_dashboard.content.admin.accelerator.device_profiles import views


class CreateViewTest(unittest.TestCase):

    # The panel's URL namespace is only registered once the enabled file wires
    # the panel into the admin dashboard, which the unit test settings do not
    # do, so reverse() can't resolve here. Assert the attributes are set
    # instead: the bug was submit_url left at its ModalFormView default of
    # None, which _modal_form.html renders as the form action, making the
    # modal post to '.../None' and 404.
    def test_submit_url_is_set(self):
        self.assertIsNotNone(views.CreateView.submit_url)

    def test_success_url_is_set(self):
        self.assertIsNotNone(views.CreateView.success_url)


if __name__ == '__main__':
    unittest.main()
