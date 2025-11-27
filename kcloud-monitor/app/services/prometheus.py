import requests
from datetime import datetime
from typing import Optional, Dict, Any, Tuple, Union

from app.config import Settings
from app.utils.prometheus_validation import (
    sanitize_label_value,
    sanitize_metric_name,
    build_label_matcher,
    build_label_filter,
    validate_step,
    PromQLValidationError
)

# Updated to use available Kepler metrics instead of nvidia-smi
PROMETHEUS_QUERIES = {
    "gpu_power": "rate(kepler_node_platform_joules_total[5m])",  # Convert joules/s to watts
    "gpu_utilization": "kepler_node_gpu_utilization",  # If available, fallback to 0
    "gpu_temperature": "kepler_node_gpu_temperature",  # If available, fallback to 0
    "gpu_memory_used": "kepler_node_gpu_memory_used_bytes",  # If available, fallback to 0
    "gpu_memory_total": "kepler_node_gpu_memory_total_bytes",  # If available, fallback to 0
    "kepler_node_power": "rate(kepler_node_platform_joules_total[5m])",
    "kepler_pod_power": "rate(kepler_pod_package_joules_total[5m])",
}

class PrometheusException(Exception):
    """Custom exception for Prometheus client errors."""
    pass

class PrometheusClient:
    """A client for querying a Prometheus server."""

    def __init__(self, settings: Settings):
        self.base_url = settings.PROMETHEUS_URL.rstrip('/')
        self.timeout = settings.PROMETHEUS_TIMEOUT
        
        self.auth: Optional[Tuple[str, str]] = None
        if settings.PROMETHEUS_USERNAME and settings.PROMETHEUS_PASSWORD:
            self.auth = (settings.PROMETHEUS_USERNAME, settings.PROMETHEUS_PASSWORD)
            
        self.verify: Union[str, bool] = True
        if settings.PROMETHEUS_CA_BUNDLE:
            self.verify = settings.PROMETHEUS_CA_BUNDLE

    def _request(self, method: str, url: str, params: Optional[Dict[str, Any]] = None) -> Dict[str, Any]:
        try:
            response = requests.request(
                method,
                url,
                params=params,
                auth=self.auth,
                timeout=self.timeout,
                verify=self.verify
            )
            response.raise_for_status() # Raises HTTPError for bad responses (4xx or 5xx)
            return response.json()
        except requests.exceptions.Timeout as e:
            raise PrometheusException(f"Request to Prometheus timed out: {e}") from e
        except requests.exceptions.HTTPError as e:
            raise PrometheusException(f"HTTP error occurred: {e} - {e.response.text}") from e
        except requests.exceptions.RequestException as e:
            raise PrometheusException(f"An error occurred while querying Prometheus: {e}") from e

    def query(self, query: str) -> Dict[str, Any]:
        """Performs an instant query."""
        url = f"{self.base_url}/api/v1/query"
        return self._request("get", url, params={"query": query})

    def query_range(self, query: str, start: datetime, end: datetime, step: str) -> Dict[str, Any]:
        """Performs a range query."""
        url = f"{self.base_url}/api/v1/query_range"
        params = {
            "query": query,
            "start": start.isoformat() + "Z",
            "end": end.isoformat() + "Z",
            "step": step
        }
        return self._request("get", url, params=params)

    def get_label_values(self, label_name: str) -> list[str]:
        """Gets all values for a given label from Prometheus."""
        url = f"{self.base_url}/api/v1/label/{label_name}/values"
        try:
            response_json = self._request("get", url)
            if response_json.get("status") == "success":
                return response_json.get("data", [])
            return []
        except PrometheusException:
            return []

    def check_health(self) -> str:
        """Checks the health of the Prometheus server."""
        # Try the health endpoint first, if it fails try a simple query
        url = f"{self.base_url}/-/healthy"
        try:
            self._request("get", url)
            return "connected"
        except PrometheusException:
            # Fallback: try a simple query to see if Prometheus is responsive
            try:
                self.query("up")
                return "connected"
            except PrometheusException:
                return "disconnected"

    def build_query(self, metric_name: str, instance: Optional[str] = None) -> str:
        """
        Builds a secure PromQL query with an optional instance filter.

        This method prevents PromQL injection by validating all user inputs
        before constructing the query string.

        Args:
            metric_name: The metric name (must be in PROMETHEUS_QUERIES)
            instance: Optional instance/node filter (will be sanitized)

        Returns:
            A safe PromQL query string

        Raises:
            ValueError: If metric_name is not found
            PromQLValidationError: If instance contains invalid characters
        """
        query = PROMETHEUS_QUERIES.get(metric_name)
        if not query:
            raise ValueError(f"Metric '{metric_name}' not found in PROMETHEUS_QUERIES mapping.")

        if instance:
            # Sanitize the instance value to prevent PromQL injection
            try:
                safe_instance = sanitize_label_value(instance)
            except PromQLValidationError as e:
                raise ValueError(f"Invalid instance value: {e}") from e

            # Build a safe label matcher
            label_filter = build_label_matcher("exported_instance", safe_instance)

            # For Kepler metrics, we need to filter by exported_instance inside the query
            # Handle both simple metrics and complex expressions like rate()
            if "rate(" in query:
                # Insert the filter inside the rate() function
                # Example: rate(kepler_node_platform_joules_total[5m])
                #       -> rate(kepler_node_platform_joules_total{exported_instance="node-01"}[5m])
                query = query.replace("kepler_node_platform_joules_total",
                                    f'kepler_node_platform_joules_total{{{label_filter}}}')
            else:
                # Simple metric, just append the filter
                # Example: kepler_node_gpu_utilization
                #       -> kepler_node_gpu_utilization{exported_instance="node-01"}
                query += f'{{{label_filter}}}'

        return query
