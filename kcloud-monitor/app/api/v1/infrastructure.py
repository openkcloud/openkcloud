"""
Infrastructure API - Nodes, Pods, Containers, and VMs monitoring endpoints.

This module provides endpoints for:
- Node-level monitoring (Kepler-based)
- Pod-level power consumption
- Container-level metrics
- VM monitoring (OpenStack)
"""

from fastapi import APIRouter, Depends, HTTPException, Query, Path
from typing import Optional

# Authentication handled at router level in main.py
from app.models.infrastructure.nodes import (
    NodeInfo,
    NodeListResponse,
    NodeDetailResponse,
    NodePowerData,
    NodePowerResponse,
    NodeMetrics,
    NodeMetricsResponse,
    NodeSummaryResponse
)
from app.models.infrastructure.pods import (
    PodListResponse,
    PodDetailResponse,
    PodPowerResponse,
    PodSummaryResponse,
    PodPowerData
)
from app.models.infrastructure.containers import (
    ContainerListResponse,
    ContainerDetailResponse,
    ContainerMetricsResponse,
    ContainerMetrics
)
from app.models.queries import PodQueryParams, ContainerQueryParams
from app.services import cache_service
from app import crud

router = APIRouter()

# ============================================================================
# Nodes Endpoints
# ============================================================================

@router.get("/infrastructure/nodes",
           response_model=NodeListResponse,
           summary="List all nodes",
           description="Get list of all Kubernetes nodes with resource information.")
async def list_nodes(
    cluster: Optional[str] = Query(None, description="Filter by cluster name"),
    role: Optional[str] = Query(None, description="Filter by node role (master/worker/gpu/npu)"),
    status: Optional[str] = Query(None, description="Filter by node status (ready/notready)")
):
    """
    Get list of all Kubernetes nodes.

    **Query Parameters:**
    - `cluster`: Filter by cluster name
    - `role`: Filter by node role
    - `status`: Filter by node status

    **Status:** Phase 4.1 - Implemented ✅

    **Returns:** List of nodes with capacity, allocatable resources, and power data.
    """
    cache_key = f"nodes_list_{cluster or 'all'}_{role or 'all'}_{status or 'all'}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    try:
        nodes_data = await crud.get_node_list(cluster=cluster, role=role, status=status)

        # Convert to NodeInfo models
        nodes = []
        for node in nodes_data:
            nodes.append(NodeInfo(**node))

        response = NodeListResponse(
            cluster=cluster or "default",
            total_nodes=len(nodes),
            nodes=nodes
        )

        await cache_service.set(cache_key, response, ttl=60)  # Cache for 60 seconds
        return response

    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to fetch node list: {str(e)}")


@router.get("/infrastructure/nodes/summary",
           response_model=NodeSummaryResponse,
           summary="Get nodes summary",
           description="Get summary statistics for all nodes.")
async def get_nodes_summary(
    cluster: Optional[str] = Query(None, description="Filter by cluster name")
):
    """
    Get summary statistics for all nodes.

    **Query Parameters:**
    - `cluster`: Filter by cluster name

    **Status:** Phase 4.1 - Implemented ✅

    **Returns:** Nodes summary including counts, total capacity, and power.
    """
    cache_key = f"nodes_summary_{cluster or 'all'}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    try:
        summary_data = await crud.get_nodes_summary(cluster=cluster)
        response = NodeSummaryResponse(**summary_data)

        await cache_service.set(cache_key, response, ttl=30)  # Cache for 30 seconds
        return response

    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to fetch nodes summary: {str(e)}")


@router.get("/infrastructure/nodes/{node_name}",
           response_model=NodeDetailResponse,
           summary="Get node details",
           description="Get detailed information for a specific node.")
async def get_node_detail(
    node_name: str = Path(..., description="Node hostname"),
    include_metrics: bool = Query(False, description="Include current metrics"),
    include_power: bool = Query(False, description="Include current power data")
):
    """
    Get detailed information for a specific node.

    **Path Parameters:**
    - `node_name`: Node hostname

    **Query Parameters:**
    - `include_metrics`: Include current metrics
    - `include_power`: Include current power data

    **Status:** Phase 4.1 - Implemented ✅

    **Returns:** Detailed node information including labels, capacity, and status.
    """
    cache_key = f"node_detail_{node_name}_{include_metrics}_{include_power}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    try:
        node_data = await crud.get_node_detail(node_name)
        node = NodeInfo(**node_data)

        # Optionally include metrics
        metrics = None
        if include_metrics:
            metrics_data = await crud.get_node_metrics(node_name)
            metrics = NodeMetrics(**metrics_data)

        # Optionally include power
        power = None
        if include_power:
            power_data = await crud.get_node_power(node_name, period="1h")
            power = NodePowerData(**power_data)

        response = NodeDetailResponse(
            node=node,
            metrics=metrics,
            power=power
        )

        ttl = 30 if (include_metrics or include_power) else 300  # 30s for dynamic data, 5min for static
        await cache_service.set(cache_key, response, ttl=ttl)
        return response

    except ValueError as e:
        raise HTTPException(status_code=404, detail=str(e))
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to fetch node details: {str(e)}")


@router.get("/infrastructure/nodes/{node_name}/power",
           response_model=NodePowerResponse,
           summary="Get node power data",
           description="Get power consumption data for a specific node (Kepler-based).")
async def get_node_power_endpoint(
    node_name: str = Path(..., description="Node hostname"),
    period: Optional[str] = Query("1h", description="Time period (1h/1d/1w)")
):
    """
    Get power consumption data for a specific node.

    **Path Parameters:**
    - `node_name`: Node hostname

    **Query Parameters:**
    - `period`: Time period for historical data

    **Status:** Phase 4.1 - Implemented ✅

    **Returns:** Node power data with breakdown (CPU, DRAM, GPU) and timeseries.
    """
    cache_key = f"node_power_{node_name}_{period}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    try:
        power_data = await crud.get_node_power(node_name, period=period)
        power = NodePowerData(**power_data)

        response = NodePowerResponse(
            node_name=node_name,
            period=period,
            power_data=power
        )

        await cache_service.set(cache_key, response, ttl=30)  # Cache for 30 seconds
        return response

    except ValueError as e:
        raise HTTPException(status_code=404, detail=str(e))
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to fetch node power data: {str(e)}")


@router.get("/infrastructure/nodes/{node_name}/metrics",
           response_model=NodeMetricsResponse,
           summary="Get node metrics",
           description="Get resource usage metrics for a specific node.")
async def get_node_metrics_endpoint(
    node_name: str = Path(..., description="Node hostname")
):
    """
    Get resource usage metrics for a specific node.

    **Path Parameters:**
    - `node_name`: Node hostname

    **Status:** Phase 4.1 - Implemented ✅

    **Returns:** Node CPU, memory, disk, network metrics.
    """
    cache_key = f"node_metrics_{node_name}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    try:
        metrics_data = await crud.get_node_metrics(node_name)
        metrics = NodeMetrics(**metrics_data)

        response = NodeMetricsResponse(
            node_name=node_name,
            metrics=metrics
        )

        await cache_service.set(cache_key, response, ttl=30)  # Cache for 30 seconds
        return response

    except ValueError as e:
        raise HTTPException(status_code=404, detail=str(e))
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to fetch node metrics: {str(e)}")


# ============================================================================
# Pods Endpoints
# ============================================================================

@router.get("/infrastructure/pods",
           response_model=PodListResponse,
           summary="List all pods",
           description="Get list of all pods with power consumption and resource information.")
async def list_pods(
    params: PodQueryParams = Depends(),
    include_metrics: bool = Query(False, description="Include resource metrics for each pod"),
    include_power: bool = Query(True, description="Include power data for each pod")
):
    """
    Get list of all pods with power consumption data.

    **Query Parameters:**
    - `cluster`: Filter by cluster name
    - `namespace`: Filter by namespace
    - `node`: Filter by node hostname
    - `label_selector`: Filter by Kubernetes labels (e.g., app=nginx,env=prod)
    - `min_power`: Minimum power consumption filter (watts)
    - `max_power`: Maximum power consumption filter (watts)

    **Returns:** List of pods with power consumption and resource usage.
    """
    cache_key = (
        f"pods_list_{params.cluster or 'default'}_{params.namespace or 'all'}_{params.node or 'all'}_"
        f"{params.label_selector or 'all'}_{params.min_power}_{params.max_power}_{include_metrics}_{include_power}"
    )
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    try:
        data = await crud.get_pod_list(params, include_metrics=include_metrics, include_power=include_power)
        response = PodListResponse(**data)
        await cache_service.set(cache_key, response, ttl=30)
        return response
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to fetch pod list: {str(e)}")


@router.get("/infrastructure/pods/summary",
           response_model=PodSummaryResponse,
           summary="Get pods summary",
           description="Get summary statistics for all pods.")
async def get_pods_summary(
    cluster: Optional[str] = Query(None, description="Filter by cluster name"),
    namespace: Optional[str] = Query(None, description="Filter by namespace")
):
    """
    Get summary statistics for all pods.

    **Query Parameters:**
    - `cluster`: Filter by cluster name
    - `namespace`: Filter by namespace

    **Returns:** Pods summary including counts, total power, and resource usage.
    """
    cache_key = f"pods_summary_{cluster or 'default'}_{namespace or 'all'}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    try:
        summary = await crud.get_pod_summary(cluster=cluster, namespace=namespace)
        response = PodSummaryResponse(**summary)
        await cache_service.set(cache_key, response, ttl=30)
        return response
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to fetch pods summary: {str(e)}")


@router.get("/infrastructure/pods/{namespace}/{pod_name}",
           response_model=PodDetailResponse,
           summary="Get pod details",
           description="Get detailed information and power data for a specific pod.")
async def get_pod_detail(
    namespace: str = Path(..., description="Pod namespace"),
    pod_name: str = Path(..., description="Pod name"),
    include_metrics: bool = Query(True, description="Include resource metrics"),
    include_power: bool = Query(True, description="Include power data")
):
    """
    Get detailed information for a specific pod.

    **Path Parameters:**
    - `namespace`: Pod namespace
    - `pod_name`: Pod name

    **Query Parameters:**
    - `include_metrics`: Include resource metrics
    - `include_power`: Include power data

    **Returns:** Detailed pod information including containers, resource usage, and power consumption.
    """
    cache_key = f"pod_detail_{namespace}_{pod_name}_{include_metrics}_{include_power}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    try:
        data = await crud.get_pod_detail_extended(
            namespace,
            pod_name,
            include_metrics=include_metrics,
            include_power=include_power
        )
        response = PodDetailResponse(**data)
        await cache_service.set(cache_key, response, ttl=30)
        return response
    except ValueError as e:
        raise HTTPException(status_code=404, detail=str(e))
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to fetch pod details: {str(e)}")


@router.get("/infrastructure/pods/{namespace}/{pod_name}/power",
           response_model=PodPowerResponse,
           summary="Get pod power data",
           description="Get power consumption data for a specific pod with timeseries support.")
async def get_pod_power(
    namespace: str = Path(..., description="Pod namespace"),
    pod_name: str = Path(..., description="Pod name"),
    period: Optional[str] = Query("1h", description="Time period (1h/1d/1w)")
):
    """
    Get power consumption data for a specific pod.

    **Path Parameters:**
    - `namespace`: Pod namespace
    - `pod_name`: Pod name

    **Query Parameters:**
    - `period`: Time period for historical data

    **Returns:** Pod power data with breakdown and timeseries.
    """
    cache_key = f"pod_power_{namespace}_{pod_name}_{period}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    try:
        power_data = await crud.get_pod_power(namespace, pod_name, period=period)
        response = PodPowerResponse(
            pod_name=pod_name,
            namespace=namespace,
            period=period,
            power_data=PodPowerData(**power_data)
        )
        await cache_service.set(cache_key, response, ttl=30)
        return response
    except ValueError as e:
        raise HTTPException(status_code=404, detail=str(e))
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to fetch pod power data: {str(e)}")



# ============================================================================
# Containers Endpoints
# ============================================================================

@router.get("/infrastructure/containers",
           response_model=ContainerListResponse,
           summary="List all containers",
           description="Get list of all containers with resource metrics (Kepler-based).")
async def list_containers(
    params: ContainerQueryParams = Depends(),
    include_metrics: bool = Query(False, description="Include resource metrics for each container")
):
    """
    Get list of all containers with resource metrics.

    **Query Parameters:**
    - `cluster`: Filter by cluster name
    - `pod`: Filter by pod name
    - `namespace`: Filter by namespace
    - `node`: Filter by node hostname

    **Returns:** List of containers with CPU, memory, and power metrics.
    """
    cache_key = (
        f"containers_list_{params.cluster or 'default'}_{params.namespace or 'all'}_{params.pod or 'all'}_"
        f"{params.node or 'all'}_{params.include_terminated}_{params.min_power}_{params.max_power}_{include_metrics}"
    )
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    try:
        data = await crud.get_container_list(params, include_metrics=include_metrics)
        response = ContainerListResponse(**data)
        await cache_service.set(cache_key, response, ttl=30)
        return response
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to fetch container list: {str(e)}")


@router.get("/infrastructure/containers/{container_id}",
           response_model=ContainerDetailResponse,
           summary="Get container details",
           description="Get detailed information for a specific container.")
async def get_container_detail(
    container_id: str = Path(..., description="Container ID"),
    include_metrics: bool = Query(True, description="Include resource metrics for the container")
):
    """
    Get detailed information for a specific container.

    **Path Parameters:**
    - `container_id`: Container ID

    **Returns:** Detailed container information including metrics and parent pod.
    """
    cache_key = f"container_detail_{container_id}_{include_metrics}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    try:
        data = await crud.get_container_detail(container_id, include_metrics=include_metrics)
        response = ContainerDetailResponse(**data)
        await cache_service.set(cache_key, response, ttl=30)
        return response
    except ValueError as e:
        raise HTTPException(status_code=404, detail=str(e))
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to fetch container detail: {str(e)}")


@router.get("/infrastructure/containers/{container_id}/metrics",
           response_model=ContainerMetricsResponse,
           summary="Get container metrics",
           description="Get resource usage metrics for a specific container.")
async def get_container_metrics(
    container_id: str = Path(..., description="Container ID")
):
    """
    Get resource usage metrics for a specific container.

    **Path Parameters:**
    - `container_id`: Container ID

    **Returns:** Container CPU, memory, network, and power metrics.
    """
    cache_key = f"container_metrics_{container_id}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    try:
        metrics = await crud.get_container_metrics(container_id)
        response = ContainerMetricsResponse(
            container_id=container_id,
            metrics=ContainerMetrics(**metrics)
        )
        await cache_service.set(cache_key, response, ttl=30)
        return response
    except ValueError as e:
        raise HTTPException(status_code=404, detail=str(e))
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to fetch container metrics: {str(e)}")


# ============================================================================
# VMs Endpoints (OpenStack)
# ============================================================================

@router.get("/infrastructure/vms",
           summary="List all VMs",
           description="Get list of all virtual machines (OpenStack-based).")
async def list_vms(
    cluster: Optional[str] = Query(None, description="Filter by cluster name"),
    project: Optional[str] = Query(None, description="Filter by OpenStack project"),
    status: Optional[str] = Query(None, description="Filter by VM status (active/stopped/error)"),
    hypervisor: Optional[str] = Query(None, description="Filter by hypervisor hostname")
):
    """
    Get list of all virtual machines from OpenStack.

    **Query Parameters:**
    - `cluster`: Filter by cluster name
    - `project`: Filter by OpenStack project
    - `status`: Filter by VM status
    - `hypervisor`: Filter by hypervisor

    **Status:** Phase 4.4 - To be implemented

    **Returns:** List of VMs with resource allocation and power data.
    """
    raise HTTPException(status_code=501, detail="VM listing not yet implemented (Phase 4.4 - OpenStack)")


@router.get("/infrastructure/vms/summary",
           summary="Get VMs summary",
           description="Get summary statistics for all virtual machines.")
async def get_vms_summary(
    cluster: Optional[str] = Query(None, description="Filter by cluster name"),
    project: Optional[str] = Query(None, description="Filter by OpenStack project")
):
    """
    Get summary statistics for all virtual machines.

    **Query Parameters:**
    - `cluster`: Filter by cluster name
    - `project`: Filter by OpenStack project

    **Status:** Phase 4.4 - To be implemented

    **Returns:** VMs summary including counts, total resources, and power.
    """
    raise HTTPException(status_code=501, detail="VMs summary not yet implemented (Phase 4.4 - OpenStack)")


@router.get("/infrastructure/vms/{vm_id}",
           summary="Get VM details",
           description="Get detailed information for a specific virtual machine.")
async def get_vm_detail(
    vm_id: str = Path(..., description="VM UUID")
):
    """
    Get detailed information for a specific virtual machine.

    **Path Parameters:**
    - `vm_id`: VM UUID

    **Status:** Phase 4.4 - To be implemented

    **Returns:** Detailed VM information including flavor, project, and resource usage.
    """
    raise HTTPException(status_code=501, detail="VM details not yet implemented (Phase 4.4 - OpenStack)")


@router.get("/infrastructure/vms/{vm_id}/power",
           summary="Get VM power data",
           description="Get power consumption data for a specific virtual machine.")
async def get_vm_power(
    vm_id: str = Path(..., description="VM UUID"),
    period: Optional[str] = Query("1h", description="Time period (1h/1d/1w)")
):
    """
    Get power consumption data for a specific virtual machine.

    **Path Parameters:**
    - `vm_id`: VM UUID

    **Query Parameters:**
    - `period`: Time period for historical data

    **Status:** Phase 4.4 - To be implemented

    **Returns:** VM power data with timeseries (estimated or measured via libvirt).
    """
    raise HTTPException(status_code=501, detail="VM power data not yet implemented (Phase 4.4 - OpenStack)")


@router.get("/infrastructure/vms/{vm_id}/metrics",
           summary="Get VM metrics",
           description="Get resource usage metrics for a specific virtual machine.")
async def get_vm_metrics(
    vm_id: str = Path(..., description="VM UUID")
):
    """
    Get resource usage metrics for a specific virtual machine.

    **Path Parameters:**
    - `vm_id`: VM UUID

    **Status:** Phase 4.4 - To be implemented

    **Returns:** VM CPU, memory, disk, and network metrics.
    """
    raise HTTPException(status_code=501, detail="VM metrics not yet implemented (Phase 4.4 - OpenStack)")


