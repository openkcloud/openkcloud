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

"""Pure correlation logic for accelerator device usage.

This module deliberately has no Django, Horizon or OpenStack imports so the
join between devices, deployables and ARQs can be unit tested standalone.
Presentation (translation, links) belongs in views/tables.
"""

# Usage states. Callers map these to translated labels.
USAGE_IN_USE = 'in_use'
USAGE_AVAILABLE = 'available'
USAGE_UNKNOWN = 'unknown'


def _device_keys(device):
    """Return the identifiers a deployable may reference this device by.

    Cyborg's deployable carries ``device_id``. Depending on the API version
    that value matches either the device's internal ``id`` or its ``uuid``,
    so both are accepted rather than guessing one.
    """
    keys = []
    for field in ('id', 'uuid'):
        value = device.get(field)
        if value is not None:
            keys.append(value)
    return keys


def _rp_to_device_key(deployables):
    """Map resource provider uuid -> deployable's device_id."""
    mapping = {}
    for dep in (deployables or []):
        device_id = dep.get('device_id')
        rp_uuid = dep.get('rp_uuid')
        if device_id is not None and rp_uuid:
            mapping[rp_uuid] = device_id
    return mapping


def _device_key_to_instances(arqs, rp_to_device_key):
    """Map device key -> instance uuids, in ARQ order.

    May contain repeats: one instance legitimately holds several ARQs
    against the same physical device (one per deployable/accelerator).
    ``correlate`` collapses them.
    """
    mapping = {}
    for arq in (arqs or []):
        rp_uuid = arq.get('device_rp_uuid')
        instance_uuid = arq.get('instance_uuid')
        if not rp_uuid or not instance_uuid:
            continue
        device_key = rp_to_device_key.get(rp_uuid)
        if device_key is None:
            continue
        mapping.setdefault(device_key, []).append(instance_uuid)
    return mapping


def correlate(devices, deployables, arqs, usage_known=True):
    """Attach instance uuids and a usage state to each device.

    :param devices: device dicts from the Cyborg API
    :param deployables: deployable dicts, used to bridge rp_uuid -> device
    :param arqs: accelerator request dicts holding the instance binding
    :param usage_known: False when the deployable or ARQ lookup failed, in
        which case usage is reported as UNKNOWN instead of being silently
        downgraded to AVAILABLE.
    :returns: the device list, mutated in place, always a list
    """
    rp_to_device_key = _rp_to_device_key(deployables)
    key_to_instances = _device_key_to_instances(arqs, rp_to_device_key)

    result = []
    for device in (devices or []):
        instances = []
        for key in _device_keys(device):
            for instance_uuid in key_to_instances.get(key, []):
                if instance_uuid not in instances:
                    instances.append(instance_uuid)
        device['attached_instances'] = instances
        if not usage_known:
            device['usage'] = USAGE_UNKNOWN
        elif instances:
            device['usage'] = USAGE_IN_USE
        else:
            device['usage'] = USAGE_AVAILABLE
        result.append(device)
    return result


def collect_instance_uuids(devices):
    """Every instance uuid referenced by the correlated devices."""
    uuids = set()
    for device in (devices or []):
        uuids.update(device.get('attached_instances') or [])
    return uuids


def rp_uuids_for_device(device, deployables):
    """Resource provider uuids backing a device, via its deployables.

    A device may have more than one deployable (one per accelerator), each
    with its own resource provider; return them de-duplicated in order.
    """
    keys = set(_device_keys(device))
    uuids = []
    for dep in (deployables or []):
        rp_uuid = dep.get('rp_uuid')
        if rp_uuid and dep.get('device_id') in keys and rp_uuid not in uuids:
            uuids.append(rp_uuid)
    return uuids


def collect_rp_uuids(devices, deployables):
    """Every resource provider uuid backing the given devices."""
    uuids = set()
    for device in (devices or []):
        uuids.update(rp_uuids_for_device(device, deployables))
    return uuids


def describe_placement(rp_uuids, rp_inventory, rp_traits, custom_only=True):
    """Aggregate resource classes and traits across a device's providers.

    :param rp_uuids: the device's resource provider uuids
    :param rp_inventory: {rp_uuid: [resource class, ...]}
    :param rp_traits: {rp_uuid: [trait, ...]}
    :param custom_only: keep only CUSTOM_ traits (the device-specific ones
        used in device profiles); standard COMPUTE_/HW_ traits are noise here
    :returns: (sorted resource classes, sorted traits)
    """
    classes = set()
    traits = set()
    for rp_uuid in rp_uuids:
        classes.update(rp_inventory.get(rp_uuid) or [])
        for trait in rp_traits.get(rp_uuid) or []:
            if not custom_only or trait.startswith('CUSTOM_'):
                traits.add(trait)
    return sorted(classes), sorted(traits)
