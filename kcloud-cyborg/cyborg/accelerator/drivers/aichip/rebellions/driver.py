# Copyright 2025 Electronics and Telecommunications Research Institute.
#
# This software was supported by the Institute of Information & Communications
# Technology Planning & Evaluation(IITP) grant funded by the Korea government
# (MSIT) (No.RS-2025-02263869, Development of AI Semiconductor Cloud Platform
# Establishment and Optimization Technology)
#
# Modifications Copyright (C) 2020 ZTE Corporation
# Copyright 2018 Beijing Lenovo Software Ltd.
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


"""
Cyborg Rebellions AICHIP driver implementation.
"""

from cyborg.accelerator.drivers.aichip.base import AICHIPDriver
from cyborg.accelerator.drivers.aichip.rebellions import sysinfo


class RebellionsAICHIPDriver(AICHIPDriver):
    """Class for Rebellions AICHIP drivers.
    Vendor should implement their specific drivers in this class.
    """

    VENDOR = "rebellions"
    VENDOR_ID = "1eff"

    def discover(self):
        return sysinfo.discover(self.VENDOR_ID)
