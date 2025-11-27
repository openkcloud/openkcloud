"""
Monitoring API - Cross-domain monitoring endpoints.

This module provides endpoints for:
- Unified power monitoring (all resources)
- Timeseries data (power, metrics, temperature)
- Real-time streaming (WebSocket/SSE)
- Power efficiency metrics (PUE)
"""

from fastapi import APIRouter, Depends, HTTPException, Query
from typing import Optional
from datetime import datetime
import logging

# Authentication handled at router level in main.py
from app.models.queries import TimeSeriesQueryParams, ClusterTotalQueryParams
from app.models.responses import TimeSeriesResponse, ClusterTotalPowerResponse, ClusterPowerTimeSeriesResponse
from app.services import cache_service
from app import crud

router = APIRouter()
logger = logging.getLogger(__name__)

# ============================================================================
# Unified Power Monitoring
# ============================================================================

@router.get("/monitoring/power",
           summary="Get unified power consumption",
           description="Get total power consumption across all resource types.")
async def get_unified_power(
    cluster: Optional[str] = Query(None, description="Filter by cluster name"),
    resource_types: Optional[str] = Query(None, description="Comma-separated resource types (gpus,npus,nodes,pods,vms)")
):
    """
    Get unified power consumption across all resource types.

    **Query Parameters:**
    - `cluster`: Filter by cluster name
    - `resource_types`: Filter by resource types (comma-separated)

    **Returns:** Total power consumption with breakdown by resource type.

    **Example Response:**
    ```json
    {
      "timestamp": "2024-01-01T12:00:00Z",
      "data": {
        "total_power_watts": 8520.5,
        "breakdown": {
          "accelerators": {
            "gpus": 3850.2,
            "npus": 980.5
          },
          "infrastructure": {
            "nodes": 2890.8,
            "vms": 799.0
          },
          "hardware": {
            "ipmi_measured": 8500.0
          }
        }
      }
    }
    ```
    """
    # Build cache key
    cache_key = f"unified_power:{cluster or 'all'}:{resource_types or 'all'}"
    cached_result = await cache_service.get(cache_key)
    if cached_result:
        logger.debug(f"Cache hit for {cache_key}")
        return cached_result

    try:
        # Parse resource types
        resource_types_list = None
        if resource_types:
            resource_types_list = [rt.strip() for rt in resource_types.split(',')]

        # Get unified power data
        result = await crud.get_unified_power(cluster, resource_types_list)

        # Cache for 30 seconds
        await cache_service.set(cache_key, result, ttl=30)
        logger.info(f"Retrieved unified power data: {result['data']['total_power_watts']}W")

        return result

    except Exception as e:
        logger.error(f"Failed to get unified power: {e}")
        raise HTTPException(status_code=500, detail=f"Failed to fetch unified power: {str(e)}")


@router.get("/monitoring/power/accelerators",
           summary="Get accelerator power only",
           description="Get power consumption from accelerators (GPUs + NPUs).")
async def get_accelerator_power(
    cluster: Optional[str] = Query(None, description="Filter by cluster name")
):
    """
    Get power consumption from accelerators only.

    **Query Parameters:**
    - `cluster`: Filter by cluster name

    **Returns:** Accelerator power consumption (GPUs + NPUs).
    """
    cache_key = f"accelerator_power:{cluster or 'all'}"
    cached_result = await cache_service.get(cache_key)
    if cached_result:
        logger.debug(f"Cache hit for {cache_key}")
        return cached_result

    try:
        result = await crud.get_accelerator_power(cluster)
        await cache_service.set(cache_key, result, ttl=30)
        logger.info(f"Retrieved accelerator power data: {result['data']['total_power_watts']}W")
        return result
    except Exception as e:
        logger.error(f"Failed to get accelerator power: {e}")
        raise HTTPException(status_code=500, detail=f"Failed to fetch accelerator power: {str(e)}")


@router.get("/monitoring/power/infrastructure",
           summary="Get infrastructure power only",
           description="Get power consumption from infrastructure (Nodes + Pods + VMs).")
async def get_infrastructure_power(
    cluster: Optional[str] = Query(None, description="Filter by cluster name")
):
    """
    Get power consumption from infrastructure only.

    **Query Parameters:**
    - `cluster`: Filter by cluster name

    **Returns:** Infrastructure power consumption (Nodes + Pods + VMs).
    """
    cache_key = f"infrastructure_power:{cluster or 'all'}"
    cached_result = await cache_service.get(cache_key)
    if cached_result:
        logger.debug(f"Cache hit for {cache_key}")
        return cached_result

    try:
        result = await crud.get_infrastructure_power(cluster)
        await cache_service.set(cache_key, result, ttl=30)
        logger.info(f"Retrieved infrastructure power data: {result['data']['total_power_watts']}W")
        return result
    except Exception as e:
        logger.error(f"Failed to get infrastructure power: {e}")
        raise HTTPException(status_code=500, detail=f"Failed to fetch infrastructure power: {str(e)}")


@router.get("/monitoring/power/breakdown",
           summary="Get power breakdown",
           description="Get detailed power breakdown by various dimensions.")
async def get_power_breakdown(
    breakdown_by: str = Query("cluster", description="Breakdown dimension (cluster/node/namespace/vendor/resource_type)"),
    cluster: Optional[str] = Query(None, description="Filter by cluster name")
):
    """
    Get detailed power breakdown analysis.

    **Query Parameters:**
    - `breakdown_by`: Breakdown dimension (cluster, node, namespace, vendor, resource_type)
    - `cluster`: Filter by cluster name

    **Returns:** Power breakdown by specified dimension.

    **Example Response (breakdown_by=namespace):**
    ```json
    {
      "timestamp": "2024-01-01T12:00:00Z",
      "breakdown_by": "namespace",
      "data": {
        "breakdowns": [
          {
            "namespace": "ml-workloads",
            "power_watts": 2450.5,
            "pods": 35,
            "percentage": 28.8
          }
        ],
        "total_power_watts": 8520.5
      }
    }
    ```
    """
    cache_key = f"power_breakdown:{breakdown_by}:{cluster or 'all'}"
    cached_result = await cache_service.get(cache_key)
    if cached_result:
        logger.debug(f"Cache hit for {cache_key}")
        return cached_result

    try:
        result = await crud.get_power_breakdown(breakdown_by, cluster)
        await cache_service.set(cache_key, result, ttl=30)
        logger.info(f"Retrieved power breakdown by {breakdown_by}: {len(result['data']['breakdowns'])} entries")
        return result
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e))
    except Exception as e:
        logger.error(f"Failed to get power breakdown: {e}")
        raise HTTPException(status_code=500, detail=f"Failed to fetch power breakdown: {str(e)}")


@router.get("/monitoring/power/efficiency",
           summary="Get power efficiency metrics",
           description="Get power efficiency metrics including PUE.")
async def get_power_efficiency(
    cluster: Optional[str] = Query(None, description="Filter by cluster name")
):
    """
    Get power efficiency metrics.

    **Query Parameters:**
    - `cluster`: Filter by cluster name

    **Returns:** Power efficiency metrics including PUE (Power Usage Effectiveness).

    **Example Response:**
    ```json
    {
      "timestamp": "2024-01-01T12:00:00Z",
      "data": {
        "pue": 1.42,
        "it_power_watts": 8520.5,
        "total_facility_power_watts": 12099.1,
        "cooling_power_watts": 3578.6,
        "efficiency_metrics": {
          "compute_per_watt": 125.3,
          "gpu_efficiency_percent": 82.5
        }
      }
    }
    ```
    """
    cache_key = f"power_efficiency:{cluster or 'all'}"
    cached_result = await cache_service.get(cache_key)
    if cached_result:
        logger.debug(f"Cache hit for {cache_key}")
        return cached_result

    try:
        result = await crud.get_power_efficiency(cluster)
        await cache_service.set(cache_key, result, ttl=60)  # Cache for 1 minute
        logger.info(f"Retrieved power efficiency metrics: PUE={result['data']['pue']}")
        return result
    except Exception as e:
        logger.error(f"Failed to get power efficiency: {e}")
        raise HTTPException(status_code=500, detail=f"Failed to fetch power efficiency: {str(e)}")


# ============================================================================
# Timeseries Data
# ============================================================================

@router.get("/monitoring/timeseries/power",
           summary="Get power timeseries",
           description="Get power consumption timeseries data.")
async def get_power_timeseries(
    params: ClusterTotalQueryParams = Depends()
):
    """
    Get power consumption timeseries data.

    **Query Parameters:**
    - `period`: Time period (1h/1d/1w/1m)
    - `step`: Sampling interval (1m/5m/15m/1h)
    - `resource_type`: Resource type filter (gpus/npus/nodes/pods/vms)
    - `breakdown_by`: Breakdown dimension
    - `cluster`: Cluster filter

    **Returns:** Power timeseries data with optional breakdown.
    """
    # Reuse existing cluster power timeseries endpoint
    cache_key = f"monitoring_power_timeseries_{params.cluster or 'default'}_{params.period}_{params.step}_{params.breakdown_by}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    try:
        data = await crud.get_cluster_power_timeseries(params)
        await cache_service.set(cache_key, data, ttl=300)  # Cache for 5 minutes
        return data
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to fetch power timeseries: {str(e)}")


@router.get("/monitoring/timeseries/metrics",
           summary="Get metrics timeseries",
           description="Get performance metrics timeseries (utilization, temperature, etc.).")
async def get_metrics_timeseries(
    metric_name: str = Query(..., description="Metric name (utilization/temperature/memory_usage)"),
    resource_type: Optional[str] = Query(None, description="Resource type (gpus/npus/nodes)"),
    period: str = Query("1h", description="Time period (1h/1d/1w/1m)"),
    step: Optional[str] = Query("5m", description="Sampling interval")
):
    """
    Get performance metrics timeseries data.

    **Query Parameters:**
    - `metric_name`: Metric name (utilization, temperature, memory_usage) [Required]
    - `resource_type`: Resource type filter
    - `period`: Time period
    - `step`: Sampling interval

    **Returns:** Metrics timeseries data.
    """
    cache_key = f"metrics_timeseries:{metric_name}:{resource_type or 'all'}:{period}:{step}"
    cached_result = await cache_service.get(cache_key)
    if cached_result:
        logger.debug(f"Cache hit for {cache_key}")
        return cached_result

    try:
        result = await crud.get_metrics_timeseries(metric_name, resource_type, period, step)
        await cache_service.set(cache_key, result, ttl=300)  # Cache for 5 minutes
        logger.info(f"Retrieved metrics timeseries: {metric_name} for {resource_type or 'all'}")
        return result
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e))
    except Exception as e:
        logger.error(f"Failed to get metrics timeseries: {e}")
        raise HTTPException(status_code=500, detail=f"Failed to fetch metrics timeseries: {str(e)}")


@router.get("/monitoring/timeseries/temperature",
           summary="Get temperature timeseries",
           description="Get temperature timeseries data across all monitored resources.")
async def get_temperature_timeseries(
    resource_type: Optional[str] = Query(None, description="Resource type (gpus/npus/nodes)"),
    period: str = Query("1h", description="Time period (1h/1d/1w/1m)"),
    step: Optional[str] = Query("5m", description="Sampling interval")
):
    """
    Get temperature timeseries data.

    **Query Parameters:**
    - `resource_type`: Resource type filter
    - `period`: Time period
    - `step`: Sampling interval

    **Returns:** Temperature timeseries data.
    """
    cache_key = f"temperature_timeseries:{resource_type or 'all'}:{period}:{step}"
    cached_result = await cache_service.get(cache_key)
    if cached_result:
        logger.debug(f"Cache hit for {cache_key}")
        return cached_result

    try:
        result = await crud.get_temperature_timeseries(resource_type, period, step)
        await cache_service.set(cache_key, result, ttl=300)  # Cache for 5 minutes
        logger.info(f"Retrieved temperature timeseries for {resource_type or 'all'}")
        return result
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e))
    except Exception as e:
        logger.error(f"Failed to get temperature timeseries: {e}")
        raise HTTPException(status_code=500, detail=f"Failed to fetch temperature timeseries: {str(e)}")


# ============================================================================
# Real-time Streaming (WebSocket/SSE)
# ============================================================================

# NOTE: WebSocket endpoints are defined in main.py due to different connection handling requirements

from starlette.responses import StreamingResponse
from app.services.stream import power_events_generator

@router.get("/monitoring/events/power",
           summary="Power events SSE stream",
           description="Server-Sent Events stream for power-related events.")
async def power_events_stream(
    cluster: Optional[str] = Query(None, description="Filter by cluster name"),
    resource_type: Optional[str] = Query(None, description="Filter by resource type"),
    threshold_watts: Optional[float] = Query(None, description="Power threshold for alerts (watts)")
):
    """
    Get power events via Server-Sent Events (SSE) stream.

    **Query Parameters:**
    - `cluster`: Filter by cluster name
    - `resource_type`: Filter by resource type
    - `threshold_watts`: Power threshold for event generation

    **Returns:** SSE stream of power events (threshold exceeded, power spikes, etc.)

    **Connection Example:**
    ```javascript
    const eventSource = new EventSource('/api/v1/monitoring/events/power?threshold_watts=5000');
    eventSource.addEventListener('threshold_exceeded', (event) => {
      const data = JSON.parse(event.data);
      console.log('Power threshold exceeded:', data);
    });
    eventSource.addEventListener('power_spike', (event) => {
      const data = JSON.parse(event.data);
      console.log('Power spike detected:', data);
    });
    ```

    **Event Types:**
    - `threshold_exceeded`: Power consumption exceeds specified threshold
    - `power_spike`: Significant power change detected (>10%)
    - `error`: Error occurred during monitoring
    """
    logger.info(f"SSE connection established: power events (cluster={cluster}, threshold={threshold_watts}W)")

    return StreamingResponse(
        power_events_generator(cluster, resource_type, threshold_watts),
        media_type="text/event-stream",
        headers={
            "Cache-Control": "no-cache",
            "Connection": "keep-alive",
            "X-Accel-Buffering": "no"  # Disable nginx buffering
        }
    )


@router.get("/monitoring/stream/info",
           summary="Get streaming info",
           description="Get information about available WebSocket/SSE streams.")
async def get_streaming_info():
    """
    Get information about available real-time streams.

    **Returns:** Available WebSocket and SSE endpoints with connection details.
    """
    from app.services.stream import connection_manager

    return {
        "websocket_endpoints": {
            "power": {
                "url": "ws://{host}/api/v1/monitoring/stream/power",
                "description": "Real-time power consumption stream",
                "query_parameters": {
                    "cluster": "Optional cluster name filter",
                    "resource_type": "Optional resource type filter (accelerators, infrastructure)",
                    "interval": "Update interval in seconds (1-60, default: 5)"
                },
                "update_interval_seconds": 5,
                "active_connections": connection_manager.get_connection_count('power'),
                "status": "available"
            },
            "metrics": {
                "url": "ws://{host}/api/v1/monitoring/stream/metrics",
                "description": "Real-time performance metrics stream",
                "query_parameters": {
                    "metric_name": "Metric name (utilization, temperature, memory_usage)",
                    "resource_type": "Optional resource type filter (gpus, npus, nodes)",
                    "interval": "Update interval in seconds (1-60, default: 5)"
                },
                "update_interval_seconds": 5,
                "active_connections": connection_manager.get_connection_count('metrics'),
                "status": "available"
            }
        },
        "sse_endpoints": {
            "power_events": {
                "url": "/api/v1/monitoring/events/power",
                "description": "Power-related event stream (SSE)",
                "query_parameters": {
                    "cluster": "Optional cluster name filter",
                    "resource_type": "Optional resource type filter",
                    "threshold_watts": "Optional power threshold for alerts"
                },
                "event_types": [
                    "threshold_exceeded",
                    "power_spike",
                    "error"
                ],
                "status": "available"
            }
        },
        "usage_example": {
            "websocket": "const ws = new WebSocket('ws://localhost:8000/api/v1/monitoring/stream/power?interval=5');",
            "sse": "const eventSource = new EventSource('/api/v1/monitoring/events/power?threshold_watts=5000');"
        }
    }
