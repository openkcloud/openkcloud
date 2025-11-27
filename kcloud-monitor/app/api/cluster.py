from fastapi import APIRouter, Depends, HTTPException, Path, Query
from typing import Optional

# Authentication handled at router level in main.py
from app.models.responses import (
    ClusterInfoResponse, PodPowerResponse, PodDetailResponse,
    ClusterTotalPowerResponse, ClusterPowerTimeSeriesResponse
)
from app.models.queries import PodQueryParams, ClusterTotalQueryParams
from app.services import cache_service
from app import crud

router = APIRouter()

@router.get("/cluster/info",
            response_model=ClusterInfoResponse)
async def get_cluster_info():
    """Get cluster metadata including nodes, pods, and namespaces."""
    cache_key = "cluster_info"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    data = await crud.get_cluster_info()
    await cache_service.set(cache_key, data, ttl=60)  # Cache for 60 seconds
    return data

@router.get("/power/pods",
            response_model=PodPowerResponse)
async def get_pod_power(params: PodQueryParams = Depends()):
    """Get power consumption data for all pods."""
    cache_key = f"pod_power_{params.namespace or 'all'}_{params.cluster or 'default'}_{params.min_power}_{params.max_power}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    data = await crud.get_pod_power_data(params)
    await cache_service.set(cache_key, data, ttl=30)  # Cache for 30 seconds
    return data

@router.get("/power/pods/{namespace}/{pod_name}",
            response_model=PodDetailResponse)
async def get_pod_power_detail(
    namespace: str = Path(..., description="Pod namespace"),
    pod_name: str = Path(..., description="Pod name")
):
    """Get detailed power information for a specific pod."""
    cache_key = f"pod_detail_{namespace}_{pod_name}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    data = await crud.get_pod_detail(namespace, pod_name)
    await cache_service.set(cache_key, data, ttl=30)  # Cache for 30 seconds
    return data

@router.get("/power/cluster/total",
            response_model=ClusterTotalPowerResponse)
async def get_cluster_total_power(params: ClusterTotalQueryParams = Depends()):
    """Get total cluster power consumption with optional breakdown."""
    cache_key = f"cluster_total_{params.cluster or 'default'}_{params.breakdown_by}_{params.include_efficiency}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    data = await crud.get_cluster_total_power(params)
    await cache_service.set(cache_key, data, ttl=60)  # Cache for 60 seconds
    return data

@router.get("/power/cluster/timeseries",
            response_model=ClusterPowerTimeSeriesResponse)
async def get_cluster_power_timeseries(params: ClusterTotalQueryParams = Depends()):
    """Get cluster power consumption over time with optional breakdown."""

    # Build cache key based on time parameters
    time_key = ""
    if params.start and params.end:
        time_key = f"{params.start.isoformat()}_{params.end.isoformat()}"
    else:
        period_value = params.period.value if params.period else "1h"
        time_key = period_value

    cache_key = f"cluster_timeseries_{params.cluster or 'default'}_{time_key}_{params.step}_{params.breakdown_by}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    data = await crud.get_cluster_power_timeseries(params)
    await cache_service.set(cache_key, data, ttl=300)  # Cache for 5 minutes
    return data