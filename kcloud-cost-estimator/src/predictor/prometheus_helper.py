"""
Prometheus query helper for fetching historical data

This module provides utilities to query Kepler metrics from Prometheus
and convert them into format suitable for energy prediction.
"""

import logging
from typing import List, Optional
from datetime import datetime, timedelta
import requests

from .models import HistoricalData

logger = logging.getLogger(__name__)


class PrometheusHelper:
    """
    Helper class for querying Prometheus metrics

    Provides methods to fetch historical container CPU usage
    and node-level metrics from Prometheus/Kepler.
    """

    def __init__(self, prometheus_url: str, verify_ssl: bool = False):
        """
        Initialize Prometheus helper

        Args:
            prometheus_url: Prometheus server URL
            verify_ssl: Whether to verify SSL certificates
        """
        self.prometheus_url = prometheus_url.rstrip("/")
        self.verify_ssl = verify_ssl
        self.session = requests.Session()

    def fetch_container_cpu_history(
        self,
        container_name: str,
        pod_name: str,
        namespace: str,
        duration_minutes: int = 90,
        step: str = "1m"
    ) -> HistoricalData:
        """
        Fetch historical CPU usage for a container

        Args:
            container_name: Container name
            pod_name: Pod name
            namespace: Kubernetes namespace
            duration_minutes: How far back to query
            step: Query step interval

        Returns:
            HistoricalData: Historical CPU usage in cores
        """
        end_time = datetime.utcnow()
        start_time = end_time - timedelta(minutes=duration_minutes)

        # Query rate of kepler_container_joules_total
        # This gives us Joules/second = Watts
        query = f'''
            rate(
                kepler_container_joules_total{{
                    container_name="{container_name}",
                    pod_name="{pod_name}",
                    container_namespace="{namespace}"
                }}[1m]
            )
        '''

        result = self._query_range(
            query=query,
            start_time=start_time,
            end_time=end_time,
            step=step
        )

        if not result or len(result) == 0:
            logger.warning(f"No data found for {namespace}/{pod_name}/{container_name}")
            return HistoricalData(
                timestamps=[],
                values=[],
                metric_name="cpu_cores"
            )

        # Extract data
        series = result[0]
        timestamps = []
        cpu_cores = []

        for timestamp, value_str in series.get("values", []):
            timestamps.append(datetime.fromtimestamp(float(timestamp)))
            # Convert Watts to approximate CPU cores
            # Rough estimate: 1 core ~ 10-15 watts
            watts = float(value_str)
            cores = watts / 12.0  # Configurable conversion factor
            cpu_cores.append(cores)

        return HistoricalData(
            timestamps=timestamps,
            values=cpu_cores,
            metric_name="cpu_cores"
        )

    def fetch_node_cpu_utilization(
        self,
        node_name: str,
        duration_minutes: int = 10
    ) -> float:
        """
        Fetch current node CPU utilization

        Args:
            node_name: Kubernetes node name
            duration_minutes: Averaging window

        Returns:
            Current CPU utilization percentage
        """
        query = f'''
            100 * (
                1 - avg(
                    rate(
                        node_cpu_seconds_total{{
                            mode="idle",
                            instance="{node_name}"
                        }}[{duration_minutes}m]
                    )
                )
            )
        '''

        result = self._query(query)

        if result and len(result) > 0:
            return float(result[0]["value"][1])

        return 0.0

    def fetch_node_power(
        self,
        node_name: str
    ) -> float:
        """
        Fetch current node power consumption

        Args:
            node_name: Kubernetes node name

        Returns:
            Current power consumption in watts
        """
        query = f'''
            rate(
                kepler_node_platform_joules_total{{
                    instance="{node_name}"
                }}[1m]
            )
        '''

        result = self._query(query)

        if result and len(result) > 0:
            return float(result[0]["value"][1])

        return 0.0

    def _query(self, query: str) -> List:
        """Execute instant Prometheus query"""
        url = f"{self.prometheus_url}/api/v1/query"
        params = {"query": query}

        try:
            response = self.session.get(
                url,
                params=params,
                verify=self.verify_ssl,
                timeout=30
            )
            response.raise_for_status()

            data = response.json()
            if data.get("status") == "success":
                return data.get("data", {}).get("result", [])
            else:
                logger.error(f"Prometheus query failed: {data}")
                return []

        except Exception as e:
            logger.error(f"Failed to query Prometheus: {e}")
            return []

    def _query_range(
        self,
        query: str,
        start_time: datetime,
        end_time: datetime,
        step: str
    ) -> List:
        """Execute range Prometheus query"""
        url = f"{self.prometheus_url}/api/v1/query_range"
        params = {
            "query": query,
            "start": start_time.timestamp(),
            "end": end_time.timestamp(),
            "step": step
        }

        try:
            response = self.session.get(
                url,
                params=params,
                verify=self.verify_ssl,
                timeout=60
            )
            response.raise_for_status()

            data = response.json()
            if data.get("status") == "success":
                return data.get("data", {}).get("result", [])
            else:
                logger.error(f"Prometheus range query failed: {data}")
                return []

        except Exception as e:
            logger.error(f"Failed to query Prometheus range: {e}")
            return []
