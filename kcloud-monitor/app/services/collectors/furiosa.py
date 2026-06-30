"""
Furiosa NPU Collector - RNGD metrics from the Furiosa Metrics Exporter.

Official exporter metrics (4): ``furiosa_npu_alive``, ``furiosa_npu_hw_temperature``
(label = peak|ambient), ``furiosa_npu_hw_power`` (label = rms, watts, chip total),
``furiosa_npu_core_utilization`` (per-core %). Memory / throttle / clock / pcie /
error are NOT provided by the exporter and come from ``node_hwmon_*`` and
libsmi/sysfs aux collectors (handled separately).

ponytail: the exporter is not yet installed in the test env, so metric names,
labels, and units follow the official docs and must be re-verified after install
(docs/temp/02-decisions/furiosa_npu_findings.md §2·§4).
"""

from typing import Any, Dict, List, Optional
import logging

from app.services.prometheus import PrometheusClient, PrometheusException
from app.utils.prometheus_validation import build_label_filter

logger = logging.getLogger(__name__)

# Official Furiosa Metrics Exporter metric names (4종).
METRIC_ALIVE = "furiosa_npu_alive"
METRIC_TEMPERATURE = "furiosa_npu_hw_temperature"          # label: peak | ambient
METRIC_POWER = "furiosa_npu_hw_power"                      # label: rms (watts, chip total)
METRIC_CORE_UTILIZATION = "furiosa_npu_core_utilization"   # per-core %

# ponytail: confirm the node label exposed by the installed exporter (DaemonSet).
NODE_LABEL = "kubernetes_node"

# Aux hwmon fallback via node_exporter (used when the exporter is absent/incomplete).
# ponytail: confirm metric/label/sensor names per node_exporter version + hwmon mapping.
HWMON_TEMP_METRIC = "node_hwmon_temp_celsius"
HWMON_POWER_METRIC = "node_hwmon_power_average_watt"
HWMON_CHIP_PREFIX = "rngd"
HWMON_SENSOR_PEAK = "temp1"      # PEAK (core)
HWMON_SENSOR_AMBIENT = "temp12"  # AMBIENT (board)


class FuriosaNPUCollector:
    """Builds and runs Furiosa Metrics Exporter (`furiosa_npu_*`) queries."""

    def __init__(self, prometheus_client: PrometheusClient):
        self.prom = prometheus_client

    @staticmethod
    def build_query(metric: str, node: Optional[str] = None, **labels: Optional[str]) -> str:
        """Build a sanitized PromQL query for a ``furiosa_npu_*`` metric.

        Label values are sanitized via ``build_label_filter`` (PromQL injection
        defense); invalid values raise ``PromQLValidationError``.
        """
        filters: Dict[str, str] = {}
        if node:
            filters[NODE_LABEL] = node
        for name, value in labels.items():
            if value is not None:
                filters[name] = value
        return f"{metric}{build_label_filter(filters)}"

    def _query(self, query: str) -> List[Dict[str, Any]]:
        """Run a query, returning the Prometheus result list (empty on failure)."""
        try:
            result = self.prom.query(query)
            return result.get("data", {}).get("result", [])
        except PrometheusException as e:
            logger.warning(f"Furiosa NPU query failed [{query}]: {e}")
            return []

    def alive(self, node: Optional[str] = None) -> List[Dict[str, Any]]:
        """Liveness per NPU (1=alive)."""
        return self._query(self.build_query(METRIC_ALIVE, node))

    def temperature(self, node: Optional[str] = None, label: Optional[str] = None) -> List[Dict[str, Any]]:
        """Hardware temperature; label 'peak' (core) or 'ambient' (board)."""
        return self._query(self.build_query(METRIC_TEMPERATURE, node, label=label))

    def power(self, node: Optional[str] = None) -> List[Dict[str, Any]]:
        """Chip-total power (RMS, watts). No per-PE power is exposed."""
        return self._query(self.build_query(METRIC_POWER, node, label="rms"))

    def core_utilization(self, node: Optional[str] = None) -> List[Dict[str, Any]]:
        """Per-core utilization (%)."""
        return self._query(self.build_query(METRIC_CORE_UTILIZATION, node))

    def _hwmon(self, metric: str, node: Optional[str], sensor: Optional[str] = None) -> List[Dict[str, Any]]:
        filters: Dict[str, str] = {}
        if node:
            filters[NODE_LABEL] = node
        if sensor:
            filters["sensor"] = sensor
        series = self._query(f"{metric}{build_label_filter(filters)}")
        # Keep only Furiosa hwmon chips (rngd0/rngd1/...).
        return [s for s in series if str(s.get("metric", {}).get("chip", "")).startswith(HWMON_CHIP_PREFIX)]

    def hwmon_temperature(self, node: Optional[str] = None, sensor: Optional[str] = None) -> List[Dict[str, Any]]:
        """node_exporter hwmon temperature fallback for Furiosa chips."""
        return self._hwmon(HWMON_TEMP_METRIC, node, sensor)

    def hwmon_power(self, node: Optional[str] = None) -> List[Dict[str, Any]]:
        """node_exporter hwmon power fallback for Furiosa chips."""
        return self._hwmon(HWMON_POWER_METRIC, node)
