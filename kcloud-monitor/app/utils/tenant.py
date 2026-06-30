"""
Tenant visibility masking (design_contracts §9).

Tenants must not see physical/passthrough internals (PCI BDF, IOMMU group,
evidence/confidence, server/BMC/VM identity). ``mask_sensitive`` recursively
strips these keys from a response object for tenant-scoped requests.
"""

from typing import Any

# Keys hidden from tenants (design_contracts §9).
SENSITIVE_FIELDS = frozenset({
    "pci_bdf",
    "host_pci_bdf",
    "guest_pci_bdf",
    "iommu_group_id",
    "resource_evidence",
    "confidence",
    "server_id",
    "bmc",
    "vm_uuid",
    "hypervisor_hostname",
    "hypervisor_host",
})


def mask_sensitive(obj: Any, is_tenant: bool = True) -> Any:
    """Recursively remove tenant-forbidden keys (§9). No-op when is_tenant is False.

    Returns a new structure; the input is not mutated.
    """
    if not is_tenant:
        return obj
    if isinstance(obj, dict):
        return {k: mask_sensitive(v, is_tenant) for k, v in obj.items() if k not in SENSITIVE_FIELDS}
    if isinstance(obj, list):
        return [mask_sensitive(v, is_tenant) for v in obj]
    return obj
