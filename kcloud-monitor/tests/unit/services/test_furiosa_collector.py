"""Tests for the Furiosa NPU collector and crud NPU functions (mock-based).

The Furiosa Metrics Exporter is not installed in the test env, so these use a
mock Prometheus client; metric names follow the official exporter docs.
"""

import asyncio
from types import SimpleNamespace
from unittest.mock import patch

import pytest

import app.crud as crud
from app.services.collectors.furiosa import (
    FuriosaNPUCollector,
    METRIC_ALIVE,
    METRIC_POWER,
    NODE_LABEL,
)
from app.services.prometheus import PrometheusException
from app.utils.prometheus_validation import PromQLValidationError


def _result(series):
    return {"data": {"result": series}}


def _run(coro):
    return asyncio.run(coro)


# --------------------------------------------------------------------------
# Collector
# --------------------------------------------------------------------------

def test_build_query_sanitized():
    assert FuriosaNPUCollector.build_query(METRIC_ALIVE) == "furiosa_npu_alive"
    q = FuriosaNPUCollector.build_query(METRIC_POWER, node="node-1", label="rms")
    assert q == f'furiosa_npu_hw_power{{{NODE_LABEL}="node-1",label="rms"}}'


def test_build_query_blocks_injection():
    with pytest.raises(PromQLValidationError):
        FuriosaNPUCollector.build_query(METRIC_POWER, node='x"} evil')


def test_query_parse_and_failure():
    ok = FuriosaNPUCollector(SimpleNamespace(query=lambda q: _result([{"metric": {}, "value": [0, "1"]}])))
    assert ok.alive()[0]["value"][1] == "1"

    def boom(q):
        raise PrometheusException("down")

    assert FuriosaNPUCollector(SimpleNamespace(query=boom)).alive() == []


# --------------------------------------------------------------------------
# crud NPU functions (shared fake exporter)
# --------------------------------------------------------------------------

def _fake_exporter(q):
    if q.startswith("furiosa_npu_alive"):
        return _result([{"metric": {"serial": "RNG123", "pci_bdf": "0000:01:00.0", "uuid": "u-1", "kubernetes_node": "n1"}, "value": [0, "1"]}])
    if q.startswith("furiosa_npu_hw_power"):
        return _result([{"metric": {"serial": "RNG123"}, "value": [0, "150"]}])
    if q.startswith("furiosa_npu_hw_temperature") and 'label="peak"' in q:
        return _result([{"metric": {"serial": "RNG123", "label": "peak"}, "value": [0, "55"]}])
    if q.startswith("furiosa_npu_hw_temperature") and 'label="ambient"' in q:
        return _result([{"metric": {"serial": "RNG123", "label": "ambient"}, "value": [0, "30"]}])
    if q.startswith("furiosa_npu_core_utilization"):
        return _result([
            {"metric": {"serial": "RNG123", "core": "0"}, "value": [0, "0"]},
            {"metric": {"serial": "RNG123", "core": "1"}, "value": [0, "70"]},
        ])
    return _result([])


def test_get_npu_info_identity_and_vendor_filter():
    with patch.object(crud, "prometheus_client", SimpleNamespace(query=_fake_exporter)):
        npus = _run(crud.get_npu_info(node="n1"))
        assert len(npus) == 1 and npus[0]["npu_id"] == "RNG123"  # serial preferred (G-1)
        assert npus[0]["alive"] is True
        assert _run(crud.get_npu_info(vendor="rebellions")) == []


def test_get_npu_metrics_aggregates():
    with patch.object(crud, "prometheus_client", SimpleNamespace(query=_fake_exporter)):
        m = _run(crud.get_npu_metrics(node="n1"))[0]
    assert m["npu_utilization_percent"] == 35.0  # (0 + 70) / 2
    assert m["active_cores"] == 1 and m["idle_cores"] == 1
    assert m["npu_temperature_celsius"] == 55.0
    assert m["board_temperature_celsius"] == 30.0
    assert m["power_usage_watts"] == 150.0


def test_get_npu_core_status_per_core():
    with patch.object(crud, "prometheus_client", SimpleNamespace(query=_fake_exporter)):
        cores = _run(crud.get_npu_core_status(node="n1"))
    assert [c["state"] for c in cores] == ["idle", "running"]
    assert all(c["temperature_celsius"] is None for c in cores)  # per-PE temp not exposed


def test_get_npu_summary_aggregates():
    with patch.object(crud, "prometheus_client", SimpleNamespace(query=_fake_exporter)):
        s = _run(crud.get_npu_summary(node="n1"))
    assert s["total_npus"] == 1 and s["furiosa_count"] == 1
    assert s["total_power_watts"] == 150.0
    assert s["max_npu_utilization_percent"] == 35.0


def test_get_npu_metrics_hwmon_fallback():
    def fake(q):
        if q.startswith("furiosa_npu_alive"):
            return _result([{"metric": {"serial": "RNG9", "device": "0", "kubernetes_node": "n2"}, "value": [0, "1"]}])
        if q.startswith("furiosa_npu_"):  # power/temp/util absent -> exporter incomplete
            return _result([])
        if q.startswith("node_hwmon_temp_celsius") and 'sensor="temp1"' in q:
            return _result([{"metric": {"chip": "rngd0", "kubernetes_node": "n2"}, "value": [0, "61"]}])
        if q.startswith("node_hwmon_temp_celsius") and 'sensor="temp12"' in q:
            return _result([{"metric": {"chip": "rngd0", "kubernetes_node": "n2"}, "value": [0, "33"]}])
        if q.startswith("node_hwmon_power_average_watt"):
            return _result([{"metric": {"chip": "rngd0", "kubernetes_node": "n2"}, "value": [0, "142"]}])
        return _result([])

    with patch.object(crud, "prometheus_client", SimpleNamespace(query=fake)):
        m = _run(crud.get_npu_metrics(node="n2"))[0]
    assert m["npu_temperature_celsius"] == 61.0
    assert m["board_temperature_celsius"] == 33.0
    assert m["power_usage_watts"] == 142.0
