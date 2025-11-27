from fastapi import APIRouter, Depends

from app.config import Settings
from app.deps import get_settings
from app.models.responses import HealthResponse, PrometheusStatus, CacheStatus, SystemInfo
from app.services import prometheus_client, cache_service
from app.services.prometheus import PROMETHEUS_QUERIES

router = APIRouter()


@router.get("/health", response_model=HealthResponse)
async def health_check(settings: Settings = Depends(get_settings)):
    """Provides the health status of the API and its dependencies."""

    # Check Prometheus status
    prometheus_status = prometheus_client.check_health()

    # Check Cache status
    cache_size = await cache_service.size()

    return HealthResponse(
        status="healthy",
        version="1.0.0",  # This could be dynamic in a real app
        prometheus=PrometheusStatus(
            status=prometheus_status,
            url=settings.PROMETHEUS_URL
        ),
        cache=CacheStatus(
            status="active",
            entries=cache_size
        )
    )


@router.get("/info", response_model=SystemInfo)
async def get_system_info():
    """Returns system information, including available instances and metrics."""
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
