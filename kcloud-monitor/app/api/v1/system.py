"""
System API - System information and health monitoring endpoints.

This module provides endpoints for:
- Health checks
- System information
- API capabilities
- Version information
- API metrics (Prometheus format)
"""

from fastapi import APIRouter, Depends
from fastapi.responses import Response
from datetime import datetime
import sys

from app.config import Settings
from app.deps import get_settings
from app.models.responses import HealthResponse, PrometheusStatus, CacheStatus, SystemInfo
from app.services import prometheus_client, cache_service
from app.services.prometheus import PROMETHEUS_QUERIES
from app.middleware import get_metrics_text, get_metrics_content_type

router = APIRouter()

# API version (should be read from package metadata in production)
API_VERSION = "0.1.0"
API_BUILD_DATE = "2025-01-23"

# ============================================================================
# Health Check
# ============================================================================

@router.get("/system/health", response_model=HealthResponse)
async def health_check(settings: Settings = Depends(get_settings)):
    """
    Provides the health status of the API and its dependencies.

    **No authentication required.**

    **Returns:** Health status including Prometheus connection, cache status, and uptime.
    """
    # Check Prometheus status
    prometheus_status = prometheus_client.check_health()

    # Check Cache status
    cache_size = await cache_service.size()

    return HealthResponse(
        status="healthy",
        version=API_VERSION,
        prometheus=PrometheusStatus(
            status=prometheus_status,
            url=settings.PROMETHEUS_URL
        ),
        cache=CacheStatus(
            status="active",
            entries=cache_size
        )
    )


# ============================================================================
# System Information
# ============================================================================

@router.get("/system/info", response_model=SystemInfo)
async def get_system_info():
    """
    Returns system information, including available instances and metrics.

    **No authentication required.**

    **Returns:** System information including API version, supported features, and cluster data.
    """
    instances = prometheus_client.get_label_values("instance")

    # A simple way to estimate total GPUs is to query a per-GPU metric and count the results.
    try:
        gpu_power_query = prometheus_client.build_query("gpu_power")
        results = prometheus_client.query(gpu_power_query)
        total_gpus = len(results.get("data", {}).get("result", []))
    except Exception:
        total_gpus = 0

    return SystemInfo(
        available_instances=instances,
        total_gpus=total_gpus,
        prometheus_metrics={
            "gpu": [q for k, q in PROMETHEUS_QUERIES.items() if k.startswith("gpu_")],
            "workload": [q for k, q in PROMETHEUS_QUERIES.items() if k.startswith("kepler_")]
        },
        data_retention="Based on external Prometheus retention"
    )


@router.get("/system/version")
async def get_version():
    """
    Get API version information.

    **No authentication required.**

    **Returns:** API version, build date, Python version, and key dependencies.

    **Example Response:**
    ```json
    {
      "api_version": "0.1.0",
      "build_date": "2025-01-23",
      "git_commit": "unknown",
      "python_version": "3.12.1",
      "dependencies": {
        "fastapi": "0.119.1",
        "pydantic": "2.12.3",
        "requests": "2.32.5",
        "prometheus_client": "0.23.1"
      }
    }
    ```
    """
    # Get Python version dynamically
    python_version = f"{sys.version_info.major}.{sys.version_info.minor}.{sys.version_info.micro}"

    return {
        "api_version": API_VERSION,
        "build_date": API_BUILD_DATE,
        "git_commit": "unknown",  # TODO: Add from CI/CD environment variables
        "python_version": python_version,
        "dependencies": {
            "fastapi": "0.119.1",
            "pydantic": "2.12.3",
            "requests": "2.32.5",
            "httpx": "0.28.1",
            "prometheus_client": "0.23.1",
            "pyarrow": "21.0.0+",
            "openpyxl": "3.1.5+",
            "reportlab": "4.4.4+"
        }
    }


@router.get("/system/capabilities")
async def get_capabilities():
    """
    Get supported features and capabilities.

    **No authentication required.**

    **Returns:** List of supported features, data sources, and API endpoints with implementation status.

    **Example Response:**
    ```json
    {
      "timestamp": "2025-01-23T12:00:00Z",
      "api_version": "0.1.0",
      "supported_features": {
        "accelerators": ["gpu", "npu"],
        "infrastructure": ["nodes", "pods", "containers"],
        "hardware": ["ipmi"],
        "streaming": ["websocket", "sse"],
        "export_formats": ["json", "csv", "parquet", "excel", "pdf"]
      }
    }
    ```
    """
    return {
        "timestamp": datetime.utcnow(),
        "api_version": API_VERSION,
        "supported_features": {
            "accelerators": ["gpu", "npu"],  # Phase 3: Both GPU and NPU implemented (NPU as placeholder)
            "infrastructure": ["nodes", "pods", "containers"],  # Phase 4: Implemented (VMs placeholder)
            "hardware": ["ipmi"],  # Phase 5: IPMI implemented
            "streaming": ["websocket", "sse"],  # Phase 7: WebSocket and SSE implemented
            "export_formats": ["json", "csv", "parquet", "excel", "pdf"]  # Phase 8: All formats implemented
        },
        "data_sources": {
            "prometheus": {
                "enabled": True,
                "version": "2.45.0+",
                "retention_days": 15,
                "status": "connected"
            },
            "dcgm": {
                "enabled": True,
                "version": "3.1.8",
                "vendor": "NVIDIA",
                "status": "connected"
            },
            "kepler": {
                "enabled": True,
                "version": "0.5.0+",
                "metrics": ["node_power", "pod_power", "container_power"],
                "status": "connected"
            },
            "ipmi": {
                "enabled": True,
                "version": "exporter",
                "status": "not_configured",
                "note": "IPMI Exporter configuration required"
            },
            "npu_exporters": {
                "furiosa": {
                    "enabled": False,
                    "status": "placeholder"
                },
                "rebellions": {
                    "enabled": False,
                    "status": "placeholder"
                }
            },
            "openstack": {
                "enabled": False,
                "status": "not_implemented",
                "phase": "Phase 4.4 (Future)"
            }
        },
        "api_domains": {
            "accelerators": {
                "endpoints": [
                    "/accelerators/gpus",
                    "/accelerators/gpus/{gpu_id}",
                    "/accelerators/gpus/{gpu_id}/metrics",
                    "/accelerators/gpus/{gpu_id}/power",
                    "/accelerators/gpus/{gpu_id}/temperature",
                    "/accelerators/gpus/summary",
                    "/accelerators/npus",
                    "/accelerators/npus/{npu_id}",
                    "/accelerators/npus/{npu_id}/metrics",
                    "/accelerators/npus/{npu_id}/cores",
                    "/accelerators/npus/summary",
                    "/accelerators/all",
                    "/accelerators/summary"
                ],
                "status": "implemented",
                "note": "GPU fully implemented with DCGM, NPU as placeholder awaiting hardware"
            },
            "infrastructure": {
                "endpoints": [
                    "/infrastructure/nodes",
                    "/infrastructure/nodes/{node_name}",
                    "/infrastructure/nodes/{node_name}/power",
                    "/infrastructure/nodes/{node_name}/metrics",
                    "/infrastructure/nodes/summary",
                    "/infrastructure/pods",
                    "/infrastructure/pods/{namespace}/{pod_name}",
                    "/infrastructure/pods/{namespace}/{pod_name}/power",
                    "/infrastructure/pods/summary",
                    "/infrastructure/containers",
                    "/infrastructure/containers/{container_id}",
                    "/infrastructure/containers/{container_id}/metrics"
                ],
                "status": "implemented",
                "note": "Nodes, Pods, Containers implemented; VMs (OpenStack) planned for future"
            },
            "hardware": {
                "endpoints": [
                    "/hardware/ipmi/sensors",
                    "/hardware/ipmi/sensors/{node_name}",
                    "/hardware/ipmi/power",
                    "/hardware/ipmi/temperature",
                    "/hardware/ipmi/fans",
                    "/hardware/ipmi/voltage",
                    "/hardware/ipmi/summary"
                ],
                "status": "implemented",
                "note": "IPMI API complete, requires IPMI Exporter configuration"
            },
            "clusters": {
                "endpoints": [
                    "/clusters",
                    "/clusters/{cluster_name}",
                    "/clusters/{cluster_name}/summary",
                    "/clusters/{cluster_name}/topology",
                    "/clusters/{cluster_name}/accelerators",
                    "/clusters/{cluster_name}/nodes",
                    "/clusters/{cluster_name}/pods",
                    "/clusters/{cluster_name}/power"
                ],
                "status": "implemented",
                "note": "Multi-cluster framework implemented, supports PROMETHEUS_CLUSTERS env"
            },
            "monitoring": {
                "endpoints": [
                    "/monitoring/power",
                    "/monitoring/power/accelerators",
                    "/monitoring/power/infrastructure",
                    "/monitoring/power/breakdown",
                    "/monitoring/power/efficiency",
                    "/monitoring/timeseries/power",
                    "/monitoring/timeseries/metrics",
                    "/monitoring/timeseries/temperature",
                    "/monitoring/stream/power (WebSocket)",
                    "/monitoring/stream/metrics (WebSocket)",
                    "/monitoring/events/power (SSE)",
                    "/monitoring/stream/info"
                ],
                "status": "implemented",
                "note": "Cross-domain monitoring with real-time streaming"
            },
            "export": {
                "endpoints": [
                    "/export/power",
                    "/export/metrics",
                    "/export/report"
                ],
                "formats": ["json", "csv", "parquet", "excel", "pdf"],
                "status": "implemented",
                "note": "All export formats implemented with optional dependencies"
            },
            "system": {
                "endpoints": [
                    "/system/health",
                    "/system/info",
                    "/system/version",
                    "/system/capabilities",
                    "/system/metrics",
                    "/system/status"
                ],
                "status": "implemented",
                "note": "Complete with Prometheus metrics exposure"
            }
        },
        "multi_cluster": {
            "enabled": False,  # Set to True when PROMETHEUS_CLUSTERS is configured
            "clusters": [],
            "note": "Configure PROMETHEUS_CLUSTERS environment variable for multi-cluster support"
        }
    }


# ============================================================================
# API Metrics (Prometheus Format)
# ============================================================================

@router.get("/system/metrics")
async def get_api_metrics():
    """
    Get API server metrics in Prometheus text format.

    **No authentication required.**

    **Returns:** Prometheus-formatted metrics for API server monitoring.

    **Metrics include:**
    - `api_requests_total`: Total number of API requests by method, endpoint, and status code
    - `api_request_duration_seconds`: Request duration histogram by method and endpoint
    - `api_errors_total`: Total number of errors by method, endpoint, and error type
    - `cache_hits_total`: Cache hit counter by cache type
    - `cache_misses_total`: Cache miss counter by cache type
    - `websocket_connections`: Current number of active WebSocket connections by stream type
    - `prometheus_query_duration_seconds`: Prometheus query duration histogram
    - `prometheus_query_errors_total`: Prometheus query error counter

    **Example:**
    ```
    # HELP api_requests_total Total number of API requests
    # TYPE api_requests_total counter
    api_requests_total{method="GET",endpoint="/api/v1/accelerators/gpus",status_code="200"} 1523

    # HELP api_request_duration_seconds API request duration in seconds
    # TYPE api_request_duration_seconds histogram
    api_request_duration_seconds_bucket{method="GET",endpoint="/api/v1/accelerators/gpus",le="0.1"} 1200
    ```
    """
    metrics_text = get_metrics_text()
    content_type = get_metrics_content_type()

    return Response(content=metrics_text, media_type=content_type)


# ============================================================================
# System Status
# ============================================================================

@router.get("/system/status")
async def get_system_status(settings: Settings = Depends(get_settings)):
    """
    Get comprehensive system status.

    **No authentication required.**

    **Returns:** Comprehensive system status including all components.
    """
    # Reuse health check data
    health = await health_check(settings)

    return {
        "timestamp": datetime.utcnow(),
        "api_version": API_VERSION,
        "status": health.status,
        "components": {
            "api_server": {
                "status": "healthy",
                "version": API_VERSION
            },
            "prometheus": {
                "status": health.prometheus.status,
                "url": health.prometheus.url
            },
            "cache": {
                "status": health.cache.status,
                "entries": health.cache.entries
            },
            "data_sources": {
                "dcgm": "connected",
                "kepler": "connected",
                "ipmi": "not_configured",
                "openstack": "not_configured"
            }
        }
    }
