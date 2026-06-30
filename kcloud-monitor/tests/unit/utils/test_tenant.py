"""Tests for tenant visibility masking (design_contracts §9)."""

from app.utils.tenant import mask_sensitive, SENSITIVE_FIELDS


def test_masks_top_level_sensitive_keys():
    data = {"gpu_id": "nvidia0", "pci_bdf": "0000:01:00.0", "confidence": 0.9, "temp": 50}
    assert mask_sensitive(data) == {"gpu_id": "nvidia0", "temp": 50}


def test_masks_nested_and_lists():
    data = {"gpus": [{"gpu_id": "g0", "iommu_group_id": "12", "host_pci_bdf": "x"}], "server_id": "s1"}
    assert mask_sensitive(data) == {"gpus": [{"gpu_id": "g0"}]}


def test_non_tenant_passthrough():
    data = {"pci_bdf": "x", "a": 1}
    assert mask_sensitive(data, is_tenant=False) == data


def test_does_not_mutate_input():
    data = {"pci_bdf": "x", "a": 1}
    mask_sensitive(data)
    assert "pci_bdf" in data  # original unchanged


def test_sensitive_fields_cover_contract():
    for key in ("pci_bdf", "iommu_group_id", "resource_evidence", "confidence", "vm_uuid"):
        assert key in SENSITIVE_FIELDS
