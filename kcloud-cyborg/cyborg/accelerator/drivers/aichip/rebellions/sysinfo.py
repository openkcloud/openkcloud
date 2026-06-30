# Copyright 2025 Electronics and Telecommunications Research Institute.
#
# This software was supported by the Institute of Information & Communications
# Technology Planning & Evaluation(IITP) grant funded by the Korea government
# (MSIT) (No.RS-2025-02263869, Development of AI Semiconductor Cloud Platform
# Establishment and Optimization Technology)
#
# Modifications Copyright (C) 2021 ZTE Corporation
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

from oslo_log import log as logging
from oslo_serialization import jsonutils


from cyborg.accelerator.common import utils
from cyborg.accelerator.drivers.aichip import utils as aichip_utils
from cyborg.common import constants
from cyborg.conf import CONF
from cyborg.objects.driver_objects import driver_attach_handle
from cyborg.objects.driver_objects import driver_attribute
from cyborg.objects.driver_objects import driver_controlpath_id
from cyborg.objects.driver_objects import driver_deployable
from cyborg.objects.driver_objects import driver_device

LOG = logging.getLogger(__name__)


def _get_traits(vendor_id, product_id):
    """Generate traits for AICHIPs.
    : param vendor_id: vendor_id of AICHIP, eg."1eff"
    : param product_id: product_id of AICHIP, eg."0000".
    Example AICHIP traits:
    {traits:["OWNER_CYBORG", "CUSTOM_REBELLION_0000"]}
    """
    traits = ["OWNER_CYBORG"]
    # AICHIP trait
    aichip_trait = "_".join(
        (
            "CUSTOM",
            aichip_utils.VENDOR_MAPS.get(vendor_id, "").upper(),
            product_id.upper(),
        )
    )
    traits.append(aichip_trait)
    return {"traits": traits}


def _generate_attribute_list(aichip):
    attr_list = []
    index = 0
    for k, v in aichip.items():
        if k == "rc":
            driver_attr = driver_attribute.DriverAttribute()
            driver_attr.key, driver_attr.value = k, v
            attr_list.append(driver_attr)
        if k == "traits":
            values = aichip.get(k, [])
            for val in values:
                driver_attr = driver_attribute.DriverAttribute(
                    key="trait" + str(index), value=val
                )
                index = index + 1
                attr_list.append(driver_attr)
    return attr_list


def _generate_attach_handle(aichip, num=None):
    driver_ah = driver_attach_handle.DriverAttachHandle()
    driver_ah.in_use = False
    if aichip["rc"] == "CUSTOM_AICHIP":
        driver_ah.attach_type = constants.AH_TYPE_PCI
        driver_ah.attach_info = utils.pci_str_to_json(aichip["devices"])
    return driver_ah


def _generate_dep_list(aichip):
    driver_dep = driver_deployable.DriverDeployable()
    driver_dep.attribute_list = _generate_attribute_list(aichip)
    driver_dep.attach_handle_list = []
    # NOTE(wangzhh): The name of deployable should be unique, its format is
    # under disscussion, may looks like
    # <ComputeNodeName>_<NumaNodeName>_<CyborgName>_<NumInHost>
    # NOTE(yumeng) Since Wallaby release, the deplpyable_name is named as
    # <Compute_hostname>_<Device_address>
    driver_dep.name = aichip.get("hostname", "") + "_" + aichip["devices"]
    driver_dep.driver_name = aichip_utils.VENDOR_MAPS.get(
        aichip["vendor_id"], ""
    ).upper()
    # if it is AICHIP, num_accelerators = 1
    if aichip["rc"] == constants.RESOURCES["AICHIP"]:
        driver_dep.num_accelerators = 1
        driver_dep.attach_handle_list = [_generate_attach_handle(aichip)]
    return [driver_dep]


def _generate_controlpath_id(aichip):
    driver_cpid = driver_controlpath_id.DriverControlPathID()
    driver_cpid.cpid_type = "PCI"
    driver_cpid.cpid_info = utils.pci_str_to_json(aichip["devices"])
    return driver_cpid


def _generate_driver_device(aichip):
    driver_device_obj = driver_device.DriverDevice()
    driver_device_obj.vendor = aichip["vendor_id"]
    driver_device_obj.model = aichip.get("model", "miss model info")
    std_board_info = {
        "product_id": aichip.get("product_id"),
        "controller": aichip.get("controller"),
    }
    vendor_board_info = {
        "vendor_info": aichip.get("vendor_info", "aichip_vb_info")
    }
    driver_device_obj.std_board_info = jsonutils.dumps(std_board_info)
    driver_device_obj.vendor_board_info = jsonutils.dumps(vendor_board_info)
    driver_device_obj.type = constants.DEVICE_AICHIP
    driver_device_obj.stub = aichip.get("stub", False)
    driver_device_obj.controlpath_id = _generate_controlpath_id(aichip)
    driver_device_obj.deployable_list = _generate_dep_list(aichip)
    return driver_device_obj


def _discover_aichips(vendor_id):
    """param: vendor_id=VENDOR_ID means only discover Rebellions AICHIP
       on the host
    """
    # discover aichip devices by "lspci"
    aichip_list = []
    aichips = aichip_utils.get_pci_devices(
        aichip_utils.AICHIP_FLAGS, vendor_id
    )
    # report trait,rc and generate driver object
    for aichip in aichips:
        m = aichip_utils.AICHIP_INFO_PATTERN.match(aichip)
        if m:
            aichip_dict = m.groupdict()
            # get hostname for deployable_name usage
            aichip_dict["hostname"] = CONF.host
            aichip_dict["rc"] = constants.RESOURCES["AICHIP"]
            traits = _get_traits(
                aichip_dict["vendor_id"], aichip_dict["product_id"]
            )
            aichip_dict.update(traits)
            aichip_list.append(_generate_driver_device(aichip_dict))
    return aichip_list


def discover(vendor_id):
    devs = _discover_aichips(vendor_id)
    return devs
