# Copyright 2025 Electronics and Telecommunications Research Institute.
#
# This software was supported by the Institute of Information & Communications
# Technology Planning & Evaluation(IITP) grant funded by the Korea government
# (MSIT) (No.RS-2025-02263869, Development of AI Semiconductor Cloud Platform
# Establishment and Optimization Technology)
#
# Licensed under the Apache License, Version 2.0 (the "License"); you may
# not use this file except in compliance with the License. You may obtain
# a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
# WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
# License for the specific language governing permissions and limitations
# under the License.

import json
from unittest import mock

from cyborg.accelerator.drivers.aichip.rebellions.driver import (
    RebellionsAICHIPDriver
)
from cyborg.tests import base

rebellions_pci_res = (
    '0000:00:0c.0 Processing accelerators [1200]: '
    'Rebellions NPU [1eff:0000] (rev 01)\n'
    '0000:00:0d.0 Processing accelerators [1200]: '
    'Rebellions NPU [1eff:0000] (rev 01)\n',)


class TestRebellionsAICHIPDriver(base.TestCase):
    """Test Rebellions AICHIP driver."""

    @mock.patch('cyborg.accelerator.drivers.aichip.utils.lspci_privileged',
                return_value=rebellions_pci_res)
    def test_discover(self, mock_pci):
        self.set_defaults(host='host-192-168-32-195', debug=True)

        rebellions_driver = RebellionsAICHIPDriver()
        npu_list = rebellions_driver.discover()

        self.assertEqual(2, len(npu_list))
        for rebellions in npu_list:
            self.assertEqual('AICHIP', rebellions.type)
            self.assertEqual('PCI', rebellions.controlpath_id.cpid_type)
            self.assertEqual(
                json.loads('{"controller": "Processing accelerators", '
                           '"product_id": "0000"}'),
                json.loads(rebellions.std_board_info))
            self.assertEqual('1eff', rebellions.vendor)

        self.assertEqual(
            {"bus": "00", "device": "0c", "domain": "0000", "function": "0"},
            json.loads(npu_list[0].controlpath_id.cpid_info))
        self.assertEqual(
            {"bus": "00", "device": "0d", "domain": "0000", "function": "0"},
            json.loads(npu_list[1].controlpath_id.cpid_info))

        self.assertEqual('host-192-168-32-195_0000:00:0c.0',
                         npu_list[0].deployable_list[0].name)
        self.assertEqual('host-192-168-32-195_0000:00:0d.0',
                         npu_list[1].deployable_list[0].name)
