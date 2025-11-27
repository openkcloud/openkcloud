"""
Clusters API - Multi-cluster management endpoints.

This module provides endpoints for:
- Cluster information and metadata
- Cluster-specific resource queries
- Multi-cluster topology
"""

from fastapi import APIRouter, Depends, HTTPException, Query, Path
from typing import Optional
from datetime import datetime
import logging

# Authentication handled at router level in main.py
from app.models.responses import ClusterInfoResponse
from app.models.queries import ClusterTotalQueryParams
from app.services import cache_service
from app.services.cluster_registry import cluster_registry
from app import crud

router = APIRouter()
logger = logging.getLogger(__name__)

# ============================================================================
# Cluster Information
# ============================================================================

@router.get("/clusters",
           summary="List all clusters",
           description="Get list of all monitored clusters.")
async def list_clusters():
    """
    Get list of all monitored clusters.

    **Returns:** List of clusters with region, URL, and health status.

    **Multi-cluster mode:** Enabled via PROMETHEUS_CLUSTERS environment variable.
    **Single-cluster mode:** Returns default cluster from PROMETHEUS_URL.

    **Example Response:**
    ```json
    {
      "timestamp": "2024-01-01T12:00:00Z",
      "total_clusters": 2,
      "multi_cluster_enabled": true,
      "default_cluster": "cluster1",
      "clusters": [
        {
          "name": "cluster1",
          "url": "http://prometheus1:9090",
          "region": "us-east-1",
          "description": "Production US East",
          "health_status": "connected",
          "last_health_check": "2024-01-01T12:00:00Z"
        }
      ]
    }
    ```
    """
    cache_key = "clusters_list"
    cached_result = await cache_service.get(cache_key)
    if cached_result:
        logger.debug("Cache hit for clusters list")
        return cached_result

    try:
        # Get cluster summary from registry
        summary = cluster_registry.get_cluster_summary()
        summary['timestamp'] = datetime.utcnow()

        # Cache result for 60 seconds
        await cache_service.set(cache_key, summary, ttl=60)
        logger.info(f"Retrieved {summary['total_clusters']} clusters")

        return summary

    except Exception as e:
        logger.error(f"Failed to list clusters: {e}")
        raise HTTPException(status_code=500, detail=f"Failed to list clusters: {str(e)}")


@router.get("/clusters/{cluster_name}",
           response_model=ClusterInfoResponse,
           summary="Get cluster details",
           description="Get detailed information for a specific cluster.")
async def get_cluster_detail(
    cluster_name: str = Path(..., description="Cluster name")
):
    """
    Get detailed information for a specific cluster.

    **Path Parameters:**
    - `cluster_name`: Cluster name

    **Returns:** Detailed cluster information including resources, capacity, and Prometheus URL.
    """
    # For now, return single cluster info (default cluster)
    cache_key = f"cluster_detail_{cluster_name}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    try:
        data = await crud.get_cluster_info()
        await cache_service.set(cache_key, data, ttl=60)  # Cache for 60 seconds
        return data
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to fetch cluster details: {str(e)}")


@router.get("/clusters/{cluster_name}/summary",
           summary="Get cluster summary",
           description="Get summary statistics for a specific cluster.")
async def get_cluster_summary(
    cluster_name: str = Path(..., description="Cluster name")
):
    """
    Get summary statistics for a specific cluster.

    **Path Parameters:**
    - `cluster_name`: Cluster name

    **Returns:** Cluster summary including resource counts, power, and utilization.

    **Note:** This endpoint aggregates data from nodes, pods, and accelerators in the cluster.
    """
    cache_key = f"cluster_summary:{cluster_name}"
    cached_result = await cache_service.get(cache_key)
    if cached_result:
        logger.debug(f"Cache hit for {cache_key}")
        return cached_result

    try:
        # Verify cluster exists
        cluster = cluster_registry.get_cluster(cluster_name)
        if not cluster:
            raise HTTPException(status_code=404, detail=f"Cluster '{cluster_name}' not found")

        # Check cluster health
        cluster_name_result, health_status = await cluster_registry.check_cluster_health(cluster_name)

        # Get cluster info (uses cluster-specific Prometheus client if multi-cluster)
        cluster_info_response = await crud.get_cluster_info()

        # Build summary
        summary = {
            "timestamp": datetime.utcnow(),
            "cluster_name": cluster_name,
            "health_status": health_status,
            "prometheus_url": cluster.url,
            "region": cluster.region,
            "description": cluster.description,
            "resources": cluster_info_response.dict() if cluster_info_response else None
        }

        # Cache for 60 seconds
        await cache_service.set(cache_key, summary, ttl=60)
        logger.info(f"Retrieved summary for cluster: {cluster_name}")

        return summary

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Failed to get cluster summary for {cluster_name}: {e}")
        raise HTTPException(status_code=500, detail=f"Failed to get cluster summary: {str(e)}")


@router.get("/clusters/{cluster_name}/topology",
           summary="Get cluster topology",
           description="Get cluster topology showing node-pod relationships from Kubernetes metrics.")
async def get_cluster_topology(
    cluster_name: str = Path(..., description="Cluster name"),
    include_pods: bool = Query(True, description="Include pod information"),
    namespace: Optional[str] = Query(None, description="Filter pods by namespace")
):
    """
    Get cluster topology visualization data from Kubernetes metrics.

    **Path Parameters:**
    - `cluster_name`: Cluster name

    **Query Parameters:**
    - `include_pods`: Include pod information (default: true)
    - `namespace`: Filter pods by namespace (optional)

    **Returns:** Cluster topology data with nodes, pods, and their relationships.

    **Data Sources:**
    - Nodes: `kube_node_info` (all nodes) + `kepler_node_info` (power info)
    - Pods: `kube_pod_info` (pod-to-node mapping)
    - Power: `kepler_node_*` and `kepler_container_*` metrics
    """
    cache_key = f"cluster_topology:{cluster_name}:{include_pods}:{namespace or 'all'}"
    cached_result = await cache_service.get(cache_key)
    if cached_result:
        logger.debug(f"Cache hit for {cache_key}")
        return cached_result

    try:
        # Verify cluster exists
        cluster = cluster_registry.get_cluster(cluster_name)
        if not cluster:
            raise HTTPException(status_code=404, detail=f"Cluster '{cluster_name}' not found")

        # Get topology data from crud helper
        topology_data = await crud.get_cluster_topology(
            cluster_name, 
            include_pods=include_pods,
            namespace=namespace
        )

        result = {
            "timestamp": datetime.utcnow(),
            "cluster_name": cluster_name,
            "topology": topology_data
        }

        # Cache for 1 minute (topology changes relatively frequently)
        await cache_service.set(cache_key, result, ttl=60)
        logger.info(f"Retrieved topology for cluster: {cluster_name} - {topology_data.get('summary', {})}")

        return result

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Failed to get cluster topology for {cluster_name}: {e}")
        raise HTTPException(status_code=500, detail=f"Failed to get cluster topology: {str(e)}")


# ============================================================================
# Cluster-Specific Resources
# Note: accelerators, nodes, pods endpoints removed to avoid duplication
# with /api/v1/accelerators and /api/v1/infrastructure endpoints.
# Use those endpoints with cluster parameter for filtering.
# ============================================================================


@router.get("/clusters/{cluster_name}/power",
           summary="Get cluster total power",
           description="Get total power consumption for a specific cluster.")
async def get_cluster_power(
    cluster_name: str = Path(..., description="Cluster name"),
    breakdown_by: Optional[str] = Query(None, description="Break down power by category (node, namespace, workload_type)"),
    include_efficiency: bool = Query(False, description="Include efficiency metrics")
):
    """
    Get total power consumption for a specific cluster.

    **Path Parameters:**
    - `cluster_name`: Cluster name

    **Query Parameters:**
    - `breakdown_by`: Break down power by category (node, namespace, workload_type)
    - `include_efficiency`: Include efficiency metrics (default: false)

    **Returns:** Cluster power consumption with breakdown (GPUs, NPUs, nodes).

    **Note:** Aggregates power data from all resources in the cluster.
    """
    # Verify cluster exists
    cluster = cluster_registry.get_cluster(cluster_name)
    if not cluster:
        raise HTTPException(status_code=404, detail=f"Cluster '{cluster_name}' not found")

    cache_key = f"cluster_power:{cluster_name}:{breakdown_by}:{include_efficiency}"
    cached_result = await cache_service.get(cache_key)
    if cached_result:
        return cached_result

    try:
        # Create params with cluster filter
        params = ClusterTotalQueryParams(
            cluster=cluster_name,
            breakdown_by=breakdown_by,
            include_efficiency=include_efficiency
        )

        # Get total cluster power
        total_power_response = await crud.get_cluster_total_power(params)

        # Convert response to dict
        result = total_power_response.dict()
        result["cluster_name"] = cluster_name

        await cache_service.set(cache_key, result, ttl=30)
        return result

    except Exception as e:
        logger.error(f"Failed to get cluster power for {cluster_name}: {e}")
        raise HTTPException(status_code=500, detail=f"Failed to get cluster power: {str(e)}")
