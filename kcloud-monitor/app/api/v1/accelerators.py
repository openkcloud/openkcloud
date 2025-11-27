"""
Accelerators API - GPU and NPU monitoring endpoints.

This module provides endpoints for:
- GPU monitoring (DCGM-based)
- NPU monitoring (Furiosa, Rebellions)
- Unified accelerator summary
"""

from fastapi import APIRouter, Depends, HTTPException, Query, Path
from typing import Optional
from datetime import datetime

# Authentication is handled at router level in main.py
from app.models.accelerators.gpu import (
    GPUListResponse, GPUDetailResponse, GPUMetricsResponse, GPUPowerResponse,
    GPUTemperatureResponse, GPUSummaryResponse,
    GPUInfo, GPUMetrics, GPUPowerData, GPUTemperature, GPUSummary,
    GPUVendor, GPUStatus, DataSource
)
from app.models.common.queries import AcceleratorQueryParams, PowerQueryParams
from app.services import cache_service
from app import crud

router = APIRouter()

# ============================================================================
# GPU Endpoints
# ============================================================================

@router.get("/accelerators/gpus",
           response_model=GPUListResponse,
           summary="List all GPUs",
           description="Get a list of all GPUs with optional filtering by cluster, node, status, and vendor.")
async def list_gpus(
    cluster: Optional[str] = Query(None, description="Filter by cluster name"),
    node: Optional[str] = Query(None, description="Filter by node hostname"),
    status: Optional[str] = Query(None, description="Filter by GPU status (active/idle/error)"),
    vendor: Optional[str] = Query(None, description="Filter by vendor (nvidia)"),
    include_metrics: bool = Query(False, description="Include real-time metrics")
):
    """
    Get list of all GPUs in the cluster.

    **Query Parameters:**
    - `cluster`: Filter by cluster name (multi-cluster support)
    - `node`: Filter by node hostname
    - `status`: Filter by GPU status
    - `vendor`: Filter by GPU vendor
    - `include_metrics`: Include real-time performance metrics

    **Returns:** List of GPU information with optional metrics.
    """
    cache_key = f"gpus_list_{cluster or 'all'}_{node or 'all'}_{status or 'all'}_{vendor or 'all'}_{include_metrics}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    try:
        # Try DCGM first
        gpu_data = await crud.get_dcgm_gpu_info(node)
        data_source = DataSource.DCGM

        # Fallback to Kepler if DCGM is not available
        if not gpu_data:
            gpu_data = await crud.get_kepler_gpu_info(node)
            data_source = DataSource.KEPLER

        if not gpu_data:
            # No data from either source - return empty response
            response = GPUListResponse(
                timestamp=datetime.utcnow(),
                cluster=cluster or "default",
                total_gpus=0,
                gpus=[],
                summary=None
            )
            await cache_service.set(cache_key, response, ttl=30)
            return response

        # Convert to response models
        gpus = []

        for gpu in gpu_data:
            gpus.append(GPUInfo(
                gpu_id=gpu.get('gpu_id', 'unknown'),
                uuid=gpu.get('uuid'),
                device_index=gpu.get('device_index'),
                model_name=gpu.get('model_name', 'unknown'),
                vendor=GPUVendor.NVIDIA,
                architecture=gpu.get('architecture'),
                driver_version=gpu.get('driver_version'),
                hostname=gpu.get('hostname', 'unknown'),
                instance=gpu.get('instance'),
                pci_bus_id=gpu.get('pci_bus_id'),
                memory_total_mb=gpu.get('memory_total_mb'),
                compute_capability=gpu.get('compute_capability'),
                # VM-specific fields
                is_vm_gpu=gpu.get('is_vm_gpu'),
                hypervisor_host=gpu.get('hypervisor_host'),
                vm_type=gpu.get('vm_type'),
                physical_node=gpu.get('physical_node'),
                gpu_allocation=gpu.get('gpu_allocation'),
                # Power monitoring
                power_source=gpu.get('power_source'),
                status=GPUStatus.ACTIVE,
                data_source=data_source
            ))

        # Generate summary if requested
        summary = None
        if include_metrics:
            # Try DCGM metrics first, fallback to Kepler
            metrics_data = await crud.get_dcgm_gpu_metrics(node)
            if not metrics_data and data_source == DataSource.KEPLER:
                metrics_data = await crud.get_kepler_gpu_metrics(node)
            
            if metrics_data:
                total_gpus = len(metrics_data)
                active_gpus = sum(1 for m in metrics_data if crud._safe_float(m.get('gpu_utilization_percent', 0)) > 0)
                idle_gpus = total_gpus - active_gpus

                utilizations = [crud._safe_float(m.get('gpu_utilization_percent')) for m in metrics_data if crud._safe_float(m.get('gpu_utilization_percent')) is not None]
                temperatures = [crud._safe_float(m.get('gpu_temperature_celsius')) for m in metrics_data if crud._safe_float(m.get('gpu_temperature_celsius')) is not None]
                powers = [crud._safe_float(m.get('power_usage_watts')) for m in metrics_data if crud._safe_float(m.get('power_usage_watts')) is not None]
                memory_used = [crud._safe_int(m.get('memory_used_mb')) for m in metrics_data if crud._safe_int(m.get('memory_used_mb')) is not None]
                memory_free = [crud._safe_int(m.get('memory_free_mb')) for m in metrics_data if crud._safe_int(m.get('memory_free_mb')) is not None]

                summary = GPUSummary(
                    total_gpus=total_gpus,
                    active_gpus=active_gpus,
                    idle_gpus=idle_gpus,
                    error_gpus=0,
                    avg_gpu_utilization_percent=sum(utilizations) / len(utilizations) if utilizations else 0,
                    max_gpu_utilization_percent=max(utilizations) if utilizations else 0,
                    avg_temperature_celsius=sum(temperatures) / len(temperatures) if temperatures else 0,
                    max_temperature_celsius=max(temperatures) if temperatures else 0,
                    total_power_watts=sum(powers) if powers else 0,
                    avg_power_watts=sum(powers) / len(powers) if powers else 0,
                    max_power_watts=max(powers) if powers else 0,
                    total_memory_used_mb=sum(memory_used) if memory_used else 0,
                    total_memory_available_mb=sum(memory_used) + sum(memory_free) if (memory_used and memory_free) else 0
                )

        response = GPUListResponse(
            timestamp=datetime.utcnow(),
            cluster=cluster or "default",
            total_gpus=len(gpus),
            gpus=gpus,
            summary=summary
        )

        # Cache for 1 hour (static GPU info) or 30 seconds (with metrics)
        ttl = 30 if include_metrics else 3600
        await cache_service.set(cache_key, response, ttl=ttl)
        return response

    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to fetch GPU list: {str(e)}")


@router.get("/accelerators/gpus/summary",
           response_model=GPUSummaryResponse,
           summary="Get GPU summary",
           description="Get summary statistics for all GPUs.")
async def get_gpus_summary(
    cluster: Optional[str] = Query(None, description="Filter by cluster name"),
    node: Optional[str] = Query(None, description="Filter by node hostname")
):
    """
    Get summary statistics for all GPUs.

    **Query Parameters:**
    - `cluster`: Filter by cluster name
    - `node`: Filter by node hostname

    **Returns:** GPU summary including counts, average metrics, and totals.
    """
    cache_key = f"gpus_summary_{cluster or 'all'}_{node or 'all'}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    try:
        metrics_data = await crud.get_dcgm_gpu_metrics(node)

        if not metrics_data:
            raise HTTPException(status_code=404, detail="No GPU metrics found")

        # Calculate summary statistics
        total_gpus = len(metrics_data)
        active_gpus = 0
        idle_gpus = 0
        utilizations = []
        temperatures = []
        total_power = 0
        total_memory_used = 0
        total_memory_available = 0
        warning_temp_count = 0
        critical_temp_count = 0

        for metric in metrics_data:
            gpu_util = crud._safe_float(metric.get('gpu_utilization_percent'))
            if gpu_util and gpu_util > 0:
                active_gpus += 1
            else:
                idle_gpus += 1
            if gpu_util is not None:
                utilizations.append(gpu_util)

            temp = crud._safe_float(metric.get('gpu_temperature_celsius'))
            if temp is not None:
                temperatures.append(temp)
                if temp >= 85:
                    critical_temp_count += 1
                elif temp >= 75:
                    warning_temp_count += 1

            power = crud._safe_float(metric.get('power_usage_watts'))
            if power is not None:
                total_power += power

            mem_used = crud._safe_int(metric.get('memory_used_mb'))
            if mem_used is not None:
                total_memory_used += mem_used

            mem_free = crud._safe_int(metric.get('memory_free_mb'))
            if mem_free is not None:
                total_memory_available += mem_free

        # Calculate averages
        avg_utilization = sum(utilizations) / len(utilizations) if utilizations else 0
        max_utilization = max(utilizations) if utilizations else 0
        avg_temperature = sum(temperatures) / len(temperatures) if temperatures else 0
        max_temperature = max(temperatures) if temperatures else 0
        avg_power = total_power / total_gpus if total_gpus > 0 else 0
        max_power = max([crud._safe_float(m.get('power_usage_watts')) for m in metrics_data if crud._safe_float(m.get('power_usage_watts'))]) if metrics_data else 0

        summary = GPUSummary(
            total_gpus=total_gpus,
            active_gpus=active_gpus,
            idle_gpus=idle_gpus,
            error_gpus=0,
            avg_gpu_utilization_percent=avg_utilization,
            max_gpu_utilization_percent=max_utilization,
            avg_memory_utilization_percent=None,  # Calculate from used/total if needed
            avg_temperature_celsius=avg_temperature,
            max_temperature_celsius=max_temperature,
            warning_temperature_count=warning_temp_count,
            critical_temperature_count=critical_temp_count,
            total_power_watts=total_power,
            avg_power_watts=avg_power,
            max_power_watts=max_power,
            total_memory_used_mb=total_memory_used,
            total_memory_available_mb=total_memory_available + total_memory_used,
            avg_memory_used_percent=None  # Calculate if needed
        )

        response = GPUSummaryResponse(
            timestamp=datetime.utcnow(),
            cluster=cluster or "default",
            summary=summary
        )

        # Cache for 30 seconds
        await cache_service.set(cache_key, response, ttl=30)
        return response

    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to generate GPU summary: {str(e)}")


@router.get("/accelerators/gpus/{gpu_id}",
           response_model=GPUDetailResponse,
           response_model_exclude_none=True,
           summary="Get GPU details",
           description="Get detailed information for a specific GPU by ID or UUID.")
async def get_gpu_detail(
    gpu_id: str = Path(..., description="GPU identifier (device ID like 'nvidia0' or UUID)"),
    include_metrics: bool = Query(False, description="Include current GPU metrics")
):
    """
    Get detailed information for a specific GPU.

    **Path Parameters:**
    - `gpu_id`: GPU device ID (e.g., 'nvidia0') or UUID

    **Query Parameters:**
    - `include_metrics`: Include current GPU performance metrics

    **Returns:** Detailed GPU information including model, driver, architecture, etc.
    """
    cache_key = f"gpu_detail_{gpu_id}_{include_metrics}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    try:
        gpu_data = await crud.get_dcgm_gpu_info(node=None)

        if not gpu_data:
            raise HTTPException(status_code=404, detail=f"GPU '{gpu_id}' not found")

        # Find GPU by ID or UUID
        gpu_info = None
        for gpu in gpu_data:
            if gpu.get('gpu_id') == gpu_id or gpu.get('uuid') == gpu_id:
                gpu_info = gpu
                break

        if not gpu_info:
            raise HTTPException(status_code=404, detail=f"GPU '{gpu_id}' not found")

        gpu_model = GPUInfo(
            gpu_id=gpu_info.get('gpu_id', 'unknown'),
            uuid=gpu_info.get('uuid'),
            device_index=gpu_info.get('device_index'),
            model_name=gpu_info.get('model_name', 'unknown'),
            vendor=GPUVendor.NVIDIA,
            architecture=gpu_info.get('architecture'),
            driver_version=gpu_info.get('driver_version'),
            hostname=gpu_info.get('hostname', 'unknown'),
            instance=gpu_info.get('instance'),
            pci_bus_id=gpu_info.get('pci_bus_id'),
            memory_total_mb=gpu_info.get('memory_total_mb'),
            compute_capability=gpu_info.get('compute_capability'),
            # VM-specific fields
            is_vm_gpu=gpu_info.get('is_vm_gpu'),
            hypervisor_host=gpu_info.get('hypervisor_host'),
            vm_type=gpu_info.get('vm_type'),
            physical_node=gpu_info.get('physical_node'),
            gpu_allocation=gpu_info.get('gpu_allocation'),
            # Power monitoring
            power_source=gpu_info.get('power_source'),
            status=GPUStatus.ACTIVE,
            data_source=DataSource.DCGM
        )

        # Get current metrics if requested
        metrics_model = None
        if include_metrics:
            metrics_data = await crud.get_dcgm_gpu_metrics(node=gpu_info.get('hostname'), gpu_id=gpu_info.get('gpu_id'))
            if metrics_data and len(metrics_data) > 0:
                metric = metrics_data[0]
                metrics_model = GPUMetrics(
                    gpu_id=metric.get('gpu_id', 'unknown'),
                    timestamp=metric.get('timestamp', datetime.utcnow()),
                    gpu_utilization_percent=crud._safe_float(metric.get('gpu_utilization_percent')),
                    memory_utilization_percent=None,  # Calculate from used/total if needed
                    decoder_utilization_percent=crud._safe_float(metric.get('decoder_utilization_percent')),
                    encoder_utilization_percent=crud._safe_float(metric.get('encoder_utilization_percent')),
                    memory_copy_utilization_percent=crud._safe_float(metric.get('memory_copy_utilization_percent')),
                    gpu_temperature_celsius=crud._safe_float(metric.get('gpu_temperature_celsius')),
                    memory_temperature_celsius=crud._safe_float(metric.get('memory_temperature_celsius')),
                    temperature_limit_celsius=None,
                    power_usage_watts=crud._safe_float(metric.get('power_usage_watts')),
                    power_limit_watts=None,
                    total_energy_joules=crud._safe_float(metric.get('total_energy_joules')),
                    memory_used_mb=crud._safe_int(metric.get('memory_used_mb')),
                    memory_free_mb=crud._safe_int(metric.get('memory_free_mb')),
                    memory_reserved_mb=crud._safe_int(metric.get('memory_reserved_mb')),
                    sm_clock_mhz=crud._safe_int(metric.get('sm_clock_mhz')),
                    memory_clock_mhz=crud._safe_int(metric.get('memory_clock_mhz')),
                    xid_errors=crud._safe_int(metric.get('xid_errors')),
                    pcie_replay_counter=crud._safe_int(metric.get('pcie_replay_counter')),
                    correctable_remapped_rows=crud._safe_int(metric.get('correctable_remapped_rows')),
                    uncorrectable_remapped_rows=crud._safe_int(metric.get('uncorrectable_remapped_rows'))
                )

        response = GPUDetailResponse(
            timestamp=datetime.utcnow(),
            gpu=gpu_model,
            metrics=metrics_model
        )

        # Cache for 1 hour (static info) or 30 seconds (with metrics)
        ttl = 30 if include_metrics else 3600
        await cache_service.set(cache_key, response, ttl=ttl)
        return response

    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to fetch GPU details: {str(e)}")


@router.get("/accelerators/gpus/{gpu_id}/metrics",
           response_model=GPUMetricsResponse,
           response_model_exclude_none=True,
           summary="Get GPU metrics",
           description="Get real-time performance metrics for a specific GPU.")
async def get_gpu_metrics(
    gpu_id: str = Path(..., description="GPU identifier (device ID or UUID)"),
    node: Optional[str] = Query(None, description="Filter by node hostname")
):
    """
    Get real-time performance metrics for a specific GPU.

    **Path Parameters:**
    - `gpu_id`: GPU device ID (e.g., 'nvidia0') or UUID

    **Returns:** Real-time GPU metrics including utilization, power, temperature, memory, etc.
    """
    cache_key = f"gpu_metrics_{gpu_id}_{node or 'all'}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    try:
        metrics_data = await crud.get_dcgm_gpu_metrics(node, gpu_id)

        if not metrics_data or len(metrics_data) == 0:
            raise HTTPException(status_code=404, detail=f"No metrics found for GPU '{gpu_id}'")

        metric = metrics_data[0]

        # Use safe conversion helpers
        gpu_metric = GPUMetrics(
            gpu_id=metric.get('gpu_id', 'unknown'),
            timestamp=metric.get('timestamp', datetime.utcnow()),

            # Performance metrics
            gpu_utilization_percent=crud._safe_float(metric.get('gpu_utilization_percent')),
            memory_utilization_percent=None,  # Calculate from used/total if needed
            decoder_utilization_percent=crud._safe_float(metric.get('decoder_utilization_percent')),
            encoder_utilization_percent=crud._safe_float(metric.get('encoder_utilization_percent')),
            memory_copy_utilization_percent=crud._safe_float(metric.get('memory_copy_utilization_percent')),

            # Temperature metrics
            gpu_temperature_celsius=crud._safe_float(metric.get('gpu_temperature_celsius')),
            memory_temperature_celsius=crud._safe_float(metric.get('memory_temperature_celsius')),
            temperature_limit_celsius=None,

            # Power metrics
            power_usage_watts=crud._safe_float(metric.get('power_usage_watts')),
            power_limit_watts=None,
            total_energy_joules=crud._safe_float(metric.get('total_energy_joules')),

            # Memory metrics
            memory_used_mb=crud._safe_int(metric.get('memory_used_mb')),
            memory_free_mb=crud._safe_int(metric.get('memory_free_mb')),
            memory_reserved_mb=crud._safe_int(metric.get('memory_reserved_mb')),

            # Clock metrics
            sm_clock_mhz=crud._safe_int(metric.get('sm_clock_mhz')),
            memory_clock_mhz=crud._safe_int(metric.get('memory_clock_mhz')),

            # Error metrics
            xid_errors=crud._safe_int(metric.get('xid_errors')),
            pcie_replay_counter=crud._safe_int(metric.get('pcie_replay_counter')),
            correctable_remapped_rows=crud._safe_int(metric.get('correctable_remapped_rows')),
            uncorrectable_remapped_rows=crud._safe_int(metric.get('uncorrectable_remapped_rows'))
        )

        response = GPUMetricsResponse(
            timestamp=datetime.utcnow(),
            gpu_id=metric.get('gpu_id', 'unknown'),
            metrics=gpu_metric
        )

        # Cache for 30 seconds (real-time metrics)
        await cache_service.set(cache_key, response, ttl=30)
        return response

    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to fetch GPU metrics: {str(e)}")


@router.get("/accelerators/gpus/{gpu_id}/power",
           response_model=GPUPowerResponse,
           summary="Get GPU power data",
           description="Get power consumption data for a specific GPU.")
async def get_gpu_power(
    gpu_id: str = Path(..., description="GPU identifier (device ID or UUID)"),
    node: Optional[str] = Query(None, description="Filter by node hostname"),
    period: Optional[str] = Query("5m", description="Time period for statistics (e.g., 5m, 1h, 24h)")
):
    """
    Get power consumption data for a specific GPU.

    **Path Parameters:**
    - `gpu_id`: GPU device ID (e.g., 'nvidia0') or UUID

    **Query Parameters:**
    - `node`: Filter by node hostname
    - `period`: Time period for statistics (default: 5m)

    **Returns:** GPU power data with current value and statistics over the specified period.
    """
    cache_key = f"gpu_power_{gpu_id}_{node or 'all'}_{period}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    try:
        # Get current power reading
        metrics_data = await crud.get_dcgm_gpu_metrics(node, gpu_id)

        if not metrics_data or len(metrics_data) == 0:
            raise HTTPException(status_code=404, detail=f"No power data found for GPU '{gpu_id}'")

        metric = metrics_data[0]
        current_power = crud._safe_float(metric.get('power_usage_watts')) or 0
        
        # Get power statistics over the period
        power_stats = await crud.get_gpu_power_stats(gpu_id, period)

        power_data = GPUPowerData(
            gpu_id=metric.get('gpu_id', 'unknown'),
            timestamp=metric.get('timestamp', datetime.utcnow()),
            power_watts=current_power,
            avg_power_watts=power_stats.get('avg_power'),
            max_power_watts=power_stats.get('max_power'),
            min_power_watts=power_stats.get('min_power'),
            total_energy_joules=crud._safe_float(metric.get('total_energy_joules'))
        )

        response = GPUPowerResponse(
            timestamp=datetime.utcnow(),
            gpu_id=metric.get('gpu_id', 'unknown'),
            period=period,
            power_data=power_data
        )

        # Cache for 30 seconds
        await cache_service.set(cache_key, response, ttl=30)
        return response

    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to fetch GPU power data: {str(e)}")


@router.get("/accelerators/gpus/{gpu_id}/temperature",
           response_model=GPUTemperatureResponse,
           summary="Get GPU temperature",
           description="Get temperature monitoring data for a specific GPU.")
async def get_gpu_temperature(
    gpu_id: str = Path(..., description="GPU identifier (device ID or UUID)"),
    node: Optional[str] = Query(None, description="Filter by node hostname")
):
    """
    Get temperature monitoring data for a specific GPU.

    **Path Parameters:**
    - `gpu_id`: GPU device ID (e.g., 'nvidia0') or UUID

    **Returns:** GPU temperature data with thresholds and status.
    """
    cache_key = f"gpu_temperature_{gpu_id}_{node or 'all'}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    try:
        temp_data = await crud.get_dcgm_gpu_temperatures(node, gpu_id)

        if not temp_data or len(temp_data) == 0:
            raise HTTPException(status_code=404, detail=f"No temperature data found for GPU '{gpu_id}'")

        temp_info = temp_data[0]

        # Map temperature status from string to enum
        from app.models.accelerators.gpu import TemperatureStatus
        status_str = temp_info.get('temperature_status', 'normal')
        temp_status = TemperatureStatus.NORMAL
        if status_str == 'critical':
            temp_status = TemperatureStatus.CRITICAL
        elif status_str == 'warning':
            temp_status = TemperatureStatus.WARNING

        gpu_temp = GPUTemperature(
            gpu_id=temp_info.get('gpu_id', 'unknown'),
            hostname=temp_info.get('hostname', 'unknown'),
            timestamp=temp_info.get('timestamp', datetime.utcnow()),
            gpu_temperature_celsius=crud._safe_float(temp_info.get('gpu_temperature_celsius')),
            memory_temperature_celsius=crud._safe_float(temp_info.get('memory_temperature_celsius')),
            temperature_limit_celsius=crud._safe_float(temp_info.get('temperature_limit_celsius')),
            slowdown_threshold_celsius=75.0,  # Standard NVIDIA threshold
            shutdown_threshold_celsius=90.0,  # Standard NVIDIA threshold
            temperature_status=temp_status
        )

        response = GPUTemperatureResponse(
            timestamp=datetime.utcnow(),
            gpu_id=temp_info.get('gpu_id', 'unknown'),
            temperature=gpu_temp
        )

        # Cache for 30 seconds
        await cache_service.set(cache_key, response, ttl=30)
        return response

    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to fetch GPU temperature: {str(e)}")




# ============================================================================
# NPU Endpoints (Phase 3.2 - Placeholder implementation)
# ============================================================================

@router.get("/accelerators/npus",
           summary="List all NPUs",
           description="Get a list of all NPUs (Furiosa, Rebellions). NOTE: Placeholder until NPU exporters are configured.")
async def list_npus(
    cluster: Optional[str] = Query(None, description="Filter by cluster name"),
    node: Optional[str] = Query(None, description="Filter by node hostname"),
    vendor: Optional[str] = Query(None, description="Filter by vendor (furiosa/rebellions)"),
    include_metrics: bool = Query(False, description="Include real-time metrics")
):
    """
    Get list of all NPUs in the cluster.

    **NOTE:** This endpoint returns placeholder data until NPU Prometheus exporters are configured.
    Once configured, it will return:
    - NPU hardware information (model, vendor, firmware)
    - Optional real-time metrics (utilization, power, temperature)
    - Summary statistics

    **Query Parameters:**
    - `cluster`: Filter by cluster name
    - `node`: Filter by node hostname
    - `vendor`: Filter by NPU vendor (furiosa, rebellions)
    - `include_metrics`: Include real-time performance metrics

    **Returns:** List of NPU information with optional metrics.
    """
    from app.models.accelerators.npu import (
        NPUListResponse, NPUInfo, NPUSummary, NPUVendor, NPUStatus
    )

    cache_key = f"npus_list_{cluster or 'all'}_{node or 'all'}_{vendor or 'all'}_{include_metrics}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    try:
        # Placeholder: Get NPU info (will return empty list until exporters configured)
        npu_data = await crud.get_npu_info(node, vendor)

        # Return empty response with informative message if no NPUs configured
        if not npu_data:
            # Return empty but valid response structure
            response = NPUListResponse(
                timestamp=datetime.utcnow(),
                cluster=cluster or "default",
                total_npus=0,
                npus=[],
                summary=None
            )

            # Cache for 30 seconds
            await cache_service.set(cache_key, response, ttl=30)
            return response

        # Convert to response models (when data is available)
        npus = []
        for npu in npu_data:
            vendor_enum = NPUVendor.FURIOSA if npu.get('vendor') == 'furiosa' else NPUVendor.REBELLIONS
            npus.append(NPUInfo(
                npu_id=npu.get('npu_id', 'unknown'),
                uuid=npu.get('uuid'),
                device_index=npu.get('device_index'),
                model_name=npu.get('model_name', 'unknown'),
                vendor=vendor_enum,
                firmware_version=npu.get('firmware_version'),
                driver_version=npu.get('driver_version'),
                hostname=npu.get('hostname', 'unknown'),
                pci_bus_id=npu.get('pci_bus_id'),
                memory_total_mb=npu.get('memory_total_mb'),
                cores_total=npu.get('cores_total'),
                pe_count=npu.get('pe_count'),
                status=NPUStatus.ACTIVE
            ))

        # Generate summary if requested and data available
        summary = None
        if include_metrics and npu_data:
            metrics_data = await crud.get_npu_metrics(node, None, vendor)
            if metrics_data:
                total_npus = len(metrics_data)
                active_npus = sum(1 for m in metrics_data if crud._safe_float(m.get('npu_utilization_percent', 0)) > 0)
                idle_npus = total_npus - active_npus

                utilizations = [crud._safe_float(m.get('npu_utilization_percent')) for m in metrics_data if crud._safe_float(m.get('npu_utilization_percent')) is not None]
                temperatures = [crud._safe_float(m.get('npu_temperature_celsius')) for m in metrics_data if crud._safe_float(m.get('npu_temperature_celsius')) is not None]
                powers = [crud._safe_float(m.get('power_usage_watts')) for m in metrics_data if crud._safe_float(m.get('power_usage_watts')) is not None]
                memory_used = [crud._safe_int(m.get('memory_used_mb')) for m in metrics_data if crud._safe_int(m.get('memory_used_mb')) is not None]
                memory_free = [crud._safe_int(m.get('memory_free_mb')) for m in metrics_data if crud._safe_int(m.get('memory_free_mb')) is not None]

                summary = NPUSummary(
                    total_npus=total_npus,
                    active_npus=active_npus,
                    idle_npus=idle_npus,
                    error_npus=0,
                    furiosa_count=sum(1 for n in npus if n.vendor == NPUVendor.FURIOSA),
                    rebellions_count=sum(1 for n in npus if n.vendor == NPUVendor.REBELLIONS),
                    avg_npu_utilization_percent=sum(utilizations) / len(utilizations) if utilizations else 0,
                    max_npu_utilization_percent=max(utilizations) if utilizations else 0,
                    avg_memory_utilization_percent=None,
                    avg_temperature_celsius=sum(temperatures) / len(temperatures) if temperatures else 0,
                    max_temperature_celsius=max(temperatures) if temperatures else 0,
                    warning_temperature_count=0,
                    total_power_watts=sum(powers) if powers else 0,
                    avg_power_watts=sum(powers) / len(powers) if powers else 0,
                    max_power_watts=max(powers) if powers else 0,
                    total_memory_used_mb=sum(memory_used) if memory_used else 0,
                    total_memory_available_mb=sum(memory_used) + sum(memory_free) if (memory_used and memory_free) else 0,
                    avg_memory_used_percent=None,
                    avg_throughput_fps=None,
                    avg_latency_ms=None
                )

        response = NPUListResponse(
            timestamp=datetime.utcnow(),
            cluster=cluster or "default",
            total_npus=len(npus),
            npus=npus,
            summary=summary
        )

        # Cache for 1 hour (static NPU info) or 30 seconds (with metrics)
        ttl = 30 if include_metrics else 3600
        await cache_service.set(cache_key, response, ttl=ttl)
        return response

    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to fetch NPU list: {str(e)}")


@router.get("/accelerators/npus/{npu_id}",
           summary="Get NPU details",
           description="Get detailed information for a specific NPU by ID or UUID. NOTE: Placeholder until NPU exporters are configured.")
async def get_npu_detail(
    npu_id: str = Path(..., description="NPU identifier (device ID like 'npu0' or UUID)"),
    include_metrics: bool = Query(False, description="Include current NPU metrics"),
    include_cores: bool = Query(False, description="Include core status (Furiosa only)")
):
    """
    Get detailed information for a specific NPU.

    **NOTE:** Returns empty/404 until NPU exporters are configured.

    **Path Parameters:**
    - `npu_id`: NPU device ID (e.g., 'npu0', 'furiosa0') or UUID

    **Query Parameters:**
    - `include_metrics`: Include current NPU performance metrics
    - `include_cores`: Include core-level status (Furiosa multi-core NPUs only)

    **Returns:** Detailed NPU information including model, firmware, architecture, etc.
    """
    from app.models.accelerators.npu import (
        NPUDetailResponse, NPUInfo, NPUMetrics, NPUCoreStatus, NPUVendor, NPUStatus, NPUCoreState
    )

    cache_key = f"npu_detail_{npu_id}_{include_metrics}_{include_cores}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    try:
        npu_data = await crud.get_npu_info(node=None, vendor=None)

        if not npu_data:
            raise HTTPException(status_code=404, detail=f"NPU '{npu_id}' not found (NPU exporters not configured)")

        # Find NPU by ID or UUID
        npu_info = None
        for npu in npu_data:
            if npu.get('npu_id') == npu_id or npu.get('uuid') == npu_id:
                npu_info = npu
                break

        if not npu_info:
            raise HTTPException(status_code=404, detail=f"NPU '{npu_id}' not found")

        vendor_enum = NPUVendor.FURIOSA if npu_info.get('vendor') == 'furiosa' else NPUVendor.REBELLIONS
        npu_model = NPUInfo(
            npu_id=npu_info.get('npu_id', 'unknown'),
            uuid=npu_info.get('uuid'),
            device_index=npu_info.get('device_index'),
            model_name=npu_info.get('model_name', 'unknown'),
            vendor=vendor_enum,
            firmware_version=npu_info.get('firmware_version'),
            driver_version=npu_info.get('driver_version'),
            hostname=npu_info.get('hostname', 'unknown'),
            pci_bus_id=npu_info.get('pci_bus_id'),
            memory_total_mb=npu_info.get('memory_total_mb'),
            cores_total=npu_info.get('cores_total'),
            pe_count=npu_info.get('pe_count'),
            status=NPUStatus.ACTIVE
        )

        # Get current metrics if requested
        metrics_model = None
        if include_metrics:
            metrics_data = await crud.get_npu_metrics(node=npu_info.get('hostname'), npu_id=npu_info.get('npu_id'), vendor=npu_info.get('vendor'))
            if metrics_data and len(metrics_data) > 0:
                metric = metrics_data[0]
                metrics_model = NPUMetrics(
                    npu_id=metric.get('npu_id', 'unknown'),
                    timestamp=metric.get('timestamp', datetime.utcnow()),
                    npu_utilization_percent=crud._safe_float(metric.get('npu_utilization_percent')),
                    memory_utilization_percent=crud._safe_float(metric.get('memory_utilization_percent')),
                    active_cores=crud._safe_int(metric.get('active_cores')),
                    idle_cores=crud._safe_int(metric.get('idle_cores')),
                    npu_temperature_celsius=crud._safe_float(metric.get('npu_temperature_celsius')),
                    board_temperature_celsius=crud._safe_float(metric.get('board_temperature_celsius')),
                    temperature_limit_celsius=crud._safe_float(metric.get('temperature_limit_celsius')),
                    power_usage_watts=crud._safe_float(metric.get('power_usage_watts')),
                    power_limit_watts=crud._safe_float(metric.get('power_limit_watts')),
                    total_energy_joules=crud._safe_float(metric.get('total_energy_joules')),
                    memory_used_mb=crud._safe_int(metric.get('memory_used_mb')),
                    memory_free_mb=crud._safe_int(metric.get('memory_free_mb')),
                    throughput_fps=crud._safe_float(metric.get('throughput_fps')),
                    latency_ms=crud._safe_float(metric.get('latency_ms')),
                    error_count=crud._safe_int(metric.get('error_count')),
                    timeout_count=crud._safe_int(metric.get('timeout_count'))
                )

        # Get core status if requested (Furiosa only)
        cores_model = None
        if include_cores and vendor_enum == NPUVendor.FURIOSA:
            core_data = await crud.get_npu_core_status(node=npu_info.get('hostname'), npu_id=npu_info.get('npu_id'))
            if core_data:
                cores_model = []
                for core in core_data:
                    core_state = NPUCoreState.IDLE
                    if core.get('state') == 'running':
                        core_state = NPUCoreState.RUNNING
                    elif core.get('state') == 'error':
                        core_state = NPUCoreState.ERROR

                    cores_model.append(NPUCoreStatus(
                        npu_id=core.get('npu_id', 'unknown'),
                        core_id=core.get('core_id', 0),
                        timestamp=core.get('timestamp', datetime.utcnow()),
                        state=core_state,
                        utilization_percent=crud._safe_float(core.get('utilization_percent')),
                        temperature_celsius=crud._safe_float(core.get('temperature_celsius')),
                        process_name=core.get('process_name'),
                        process_id=core.get('process_id')
                    ))

        response = NPUDetailResponse(
            timestamp=datetime.utcnow(),
            npu=npu_model,
            metrics=metrics_model,
            cores=cores_model
        )

        # Cache for 1 hour (static info) or 30 seconds (with metrics)
        ttl = 30 if include_metrics else 3600
        await cache_service.set(cache_key, response, ttl=ttl)
        return response

    except HTTPException:
        raise
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to fetch NPU details: {str(e)}")


@router.get("/accelerators/npus/{npu_id}/metrics",
           summary="Get NPU metrics",
           description="Get real-time performance metrics for a specific NPU. NOTE: Placeholder until NPU exporters are configured.")
async def get_npu_metrics(
    npu_id: str = Path(..., description="NPU identifier (device ID or UUID)"),
    node: Optional[str] = Query(None, description="Filter by node hostname")
):
    """
    Get real-time performance metrics for a specific NPU.

    **NOTE:** Returns 404 until NPU exporters are configured.

    **Path Parameters:**
    - `npu_id`: NPU device ID (e.g., 'npu0', 'furiosa0') or UUID

    **Returns:** Real-time NPU metrics including utilization, power, temperature, memory, etc.
    """
    from app.models.accelerators.npu import NPUMetricsResponse, NPUMetrics

    cache_key = f"npu_metrics_{npu_id}_{node or 'all'}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    try:
        metrics_data = await crud.get_npu_metrics(node, npu_id, None)

        if not metrics_data or len(metrics_data) == 0:
            raise HTTPException(status_code=404, detail=f"No metrics found for NPU '{npu_id}' (NPU exporters not configured)")

        metric = metrics_data[0]

        npu_metric = NPUMetrics(
            npu_id=metric.get('npu_id', 'unknown'),
            timestamp=metric.get('timestamp', datetime.utcnow()),
            npu_utilization_percent=crud._safe_float(metric.get('npu_utilization_percent')),
            memory_utilization_percent=crud._safe_float(metric.get('memory_utilization_percent')),
            active_cores=crud._safe_int(metric.get('active_cores')),
            idle_cores=crud._safe_int(metric.get('idle_cores')),
            npu_temperature_celsius=crud._safe_float(metric.get('npu_temperature_celsius')),
            board_temperature_celsius=crud._safe_float(metric.get('board_temperature_celsius')),
            temperature_limit_celsius=crud._safe_float(metric.get('temperature_limit_celsius')),
            power_usage_watts=crud._safe_float(metric.get('power_usage_watts')),
            power_limit_watts=crud._safe_float(metric.get('power_limit_watts')),
            total_energy_joules=crud._safe_float(metric.get('total_energy_joules')),
            memory_used_mb=crud._safe_int(metric.get('memory_used_mb')),
            memory_free_mb=crud._safe_int(metric.get('memory_free_mb')),
            throughput_fps=crud._safe_float(metric.get('throughput_fps')),
            latency_ms=crud._safe_float(metric.get('latency_ms')),
            error_count=crud._safe_int(metric.get('error_count')),
            timeout_count=crud._safe_int(metric.get('timeout_count'))
        )

        response = NPUMetricsResponse(
            timestamp=datetime.utcnow(),
            npu_id=metric.get('npu_id', 'unknown'),
            metrics=npu_metric
        )

        # Cache for 30 seconds (real-time metrics)
        await cache_service.set(cache_key, response, ttl=30)
        return response

    except HTTPException:
        raise
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to fetch NPU metrics: {str(e)}")


@router.get("/accelerators/npus/{npu_id}/cores",
           summary="Get NPU core status",
           description="Get core-level status for a specific NPU (Furiosa only). NOTE: Placeholder until NPU exporters are configured.")
async def get_npu_cores(
    npu_id: str = Path(..., description="NPU identifier (device ID or UUID)"),
    node: Optional[str] = Query(None, description="Filter by node hostname")
):
    """
    Get core-level status for a specific NPU.

    **NOTE:** This endpoint is specific to Furiosa AI multi-core NPUs.
    Returns 404 until NPU exporters are configured.

    **Path Parameters:**
    - `npu_id`: NPU device ID (e.g., 'furiosa0') or UUID

    **Returns:** Core status for each NPU core including state, utilization, temperature, and process info.
    """
    from app.models.accelerators.npu import NPUCoreStatusResponse, NPUCoreStatus, NPUCoreState

    cache_key = f"npu_cores_{npu_id}_{node or 'all'}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    try:
        core_data = await crud.get_npu_core_status(node, npu_id)

        if not core_data or len(core_data) == 0:
            raise HTTPException(status_code=404, detail=f"No core status found for NPU '{npu_id}' (NPU exporters not configured or not a Furiosa NPU)")

        cores = []
        for core in core_data:
            core_state = NPUCoreState.IDLE
            if core.get('state') == 'running':
                core_state = NPUCoreState.RUNNING
            elif core.get('state') == 'error':
                core_state = NPUCoreState.ERROR

            cores.append(NPUCoreStatus(
                npu_id=core.get('npu_id', 'unknown'),
                core_id=core.get('core_id', 0),
                timestamp=core.get('timestamp', datetime.utcnow()),
                state=core_state,
                utilization_percent=crud._safe_float(core.get('utilization_percent')),
                temperature_celsius=crud._safe_float(core.get('temperature_celsius')),
                process_name=core.get('process_name'),
                process_id=core.get('process_id')
            ))

        response = NPUCoreStatusResponse(
            timestamp=datetime.utcnow(),
            npu_id=npu_id,
            cores=cores
        )

        # Cache for 30 seconds
        await cache_service.set(cache_key, response, ttl=30)
        return response

    except HTTPException:
        raise
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to fetch NPU core status: {str(e)}")


@router.get("/accelerators/npus/summary",
           summary="Get NPU summary",
           description="Get summary statistics for all NPUs. NOTE: Placeholder until NPU exporters are configured.")
async def get_npus_summary(
    cluster: Optional[str] = Query(None, description="Filter by cluster name"),
    node: Optional[str] = Query(None, description="Filter by node hostname")
):
    """
    Get summary statistics for all NPUs.

    **NOTE:** Returns empty summary until NPU exporters are configured.

    **Query Parameters:**
    - `cluster`: Filter by cluster name
    - `node`: Filter by node hostname

    **Returns:** NPU summary including counts, average metrics, vendor breakdown, and totals.
    """
    from app.models.accelerators.npu import NPUSummaryResponse, NPUSummary, NPUVendor

    cache_key = f"npus_summary_{cluster or 'all'}_{node or 'all'}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    try:
        metrics_data = await crud.get_npu_metrics(node, None, None)

        # Return empty summary if no NPUs configured
        if not metrics_data:
            summary = NPUSummary(
                total_npus=0,
                active_npus=0,
                idle_npus=0,
                error_npus=0,
                furiosa_count=0,
                rebellions_count=0,
                avg_npu_utilization_percent=0,
                max_npu_utilization_percent=0,
                avg_memory_utilization_percent=None,
                avg_temperature_celsius=0,
                max_temperature_celsius=0,
                warning_temperature_count=0,
                total_power_watts=0,
                avg_power_watts=0,
                max_power_watts=0,
                total_memory_used_mb=0,
                total_memory_available_mb=0,
                avg_memory_used_percent=None,
                avg_throughput_fps=None,
                avg_latency_ms=None
            )

            response = NPUSummaryResponse(
                timestamp=datetime.utcnow(),
                cluster=cluster or "default",
                summary=summary
            )

            await cache_service.set(cache_key, response, ttl=30)
            return response

        # Calculate summary statistics
        total_npus = len(metrics_data)
        active_npus = 0
        idle_npus = 0
        utilizations = []
        temperatures = []
        total_power = 0
        total_memory_used = 0
        total_memory_available = 0
        warning_temp_count = 0

        for metric in metrics_data:
            npu_util = crud._safe_float(metric.get('npu_utilization_percent'))
            if npu_util and npu_util > 0:
                active_npus += 1
            else:
                idle_npus += 1
            if npu_util is not None:
                utilizations.append(npu_util)

            temp = crud._safe_float(metric.get('npu_temperature_celsius'))
            if temp is not None:
                temperatures.append(temp)
                if temp >= 75:  # Warning temperature for NPUs
                    warning_temp_count += 1

            power = crud._safe_float(metric.get('power_usage_watts'))
            if power is not None:
                total_power += power

            mem_used = crud._safe_int(metric.get('memory_used_mb'))
            if mem_used is not None:
                total_memory_used += mem_used

            mem_free = crud._safe_int(metric.get('memory_free_mb'))
            if mem_free is not None:
                total_memory_available += mem_free

        # Calculate averages
        avg_utilization = sum(utilizations) / len(utilizations) if utilizations else 0
        max_utilization = max(utilizations) if utilizations else 0
        avg_temperature = sum(temperatures) / len(temperatures) if temperatures else 0
        max_temperature = max(temperatures) if temperatures else 0
        avg_power = total_power / total_npus if total_npus > 0 else 0
        max_power = max([crud._safe_float(m.get('power_usage_watts')) for m in metrics_data if crud._safe_float(m.get('power_usage_watts'))]) if metrics_data else 0

        # Get vendor counts
        npu_info_data = await crud.get_npu_info(node, None)
        furiosa_count = sum(1 for n in npu_info_data if n.get('vendor') == 'furiosa')
        rebellions_count = sum(1 for n in npu_info_data if n.get('vendor') == 'rebellions')

        summary = NPUSummary(
            total_npus=total_npus,
            active_npus=active_npus,
            idle_npus=idle_npus,
            error_npus=0,
            furiosa_count=furiosa_count,
            rebellions_count=rebellions_count,
            avg_npu_utilization_percent=avg_utilization,
            max_npu_utilization_percent=max_utilization,
            avg_memory_utilization_percent=None,
            avg_temperature_celsius=avg_temperature,
            max_temperature_celsius=max_temperature,
            warning_temperature_count=warning_temp_count,
            total_power_watts=total_power,
            avg_power_watts=avg_power,
            max_power_watts=max_power,
            total_memory_used_mb=total_memory_used,
            total_memory_available_mb=total_memory_available + total_memory_used,
            avg_memory_used_percent=None,
            avg_throughput_fps=None,
            avg_latency_ms=None
        )

        response = NPUSummaryResponse(
            timestamp=datetime.utcnow(),
            cluster=cluster or "default",
            summary=summary
        )

        # Cache for 30 seconds
        await cache_service.set(cache_key, response, ttl=30)
        return response

    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to generate NPU summary: {str(e)}")


# ============================================================================
# Unified Accelerator Endpoints (Phase 3.3)
# ============================================================================

@router.get("/accelerators/all",
           summary="List all accelerators",
           description="Get unified list of all accelerators (GPUs + NPUs) with comprehensive statistics.")
async def list_all_accelerators(
    cluster: Optional[str] = Query(None, description="Filter by cluster name"),
    node: Optional[str] = Query(None, description="Filter by node hostname"),
    include_metrics: bool = Query(False, description="Include real-time metrics for each accelerator")
):
    """
    Get unified list of all accelerators (GPUs + NPUs).

    **Query Parameters:**
    - `cluster`: Filter by cluster name
    - `node`: Filter by node hostname
    - `include_metrics`: Include real-time performance metrics

    **Returns:** Combined list of all accelerators with type classification and optional metrics.

    **Response Structure:**
    - `summary`: Total counts by type (GPUs, NPUs) and power consumption
    - `accelerators`: Detailed list with type, device information, and optional metrics
    """
    from app.models.accelerators.common import AcceleratorType

    cache_key = f"accelerators_all_{cluster or 'all'}_{node or 'all'}_{include_metrics}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    try:
        # Collect GPU data
        gpu_data = await crud.get_dcgm_gpu_info(node)
        gpu_metrics = await crud.get_dcgm_gpu_metrics(node) if include_metrics else []

        # Collect NPU data (placeholder - will return empty until NPU exporters configured)
        npu_data = await crud.get_npu_info(node, None)
        npu_metrics = await crud.get_npu_metrics(node, None, None) if include_metrics else []

        # Build GPU accelerator list
        gpus = []
        total_gpu_power = 0
        for gpu in (gpu_data or []):
            gpu_id = gpu.get('gpu_id', 'unknown')
            gpu_entry = {
                "type": AcceleratorType.GPU.value,
                "id": gpu_id,
                "uuid": gpu.get('uuid'),
                "model": gpu.get('model_name', 'unknown'),
                "vendor": "nvidia",
                "hostname": gpu.get('hostname', 'unknown'),
                "pci_bus_id": gpu.get('pci_bus_id')
            }

            # Add metrics if requested
            if include_metrics:
                gpu_metric = next((m for m in gpu_metrics if m.get('gpu_id') == gpu_id), None)
                if gpu_metric:
                    power = crud._safe_float(gpu_metric.get('power_usage_watts')) or 0
                    total_gpu_power += power
                    gpu_entry["metrics"] = {
                        "power_watts": power,
                        "utilization_percent": crud._safe_float(gpu_metric.get('gpu_utilization_percent')),
                        "temperature_celsius": crud._safe_float(gpu_metric.get('gpu_temperature_celsius')),
                        "memory_used_mb": crud._safe_int(gpu_metric.get('memory_used_mb'))
                    }

            gpus.append(gpu_entry)

        # Build NPU accelerator list
        npus = []
        total_npu_power = 0
        for npu in (npu_data or []):
            npu_id = npu.get('npu_id', 'unknown')
            npu_entry = {
                "type": AcceleratorType.NPU.value,
                "id": npu_id,
                "uuid": npu.get('uuid'),
                "model": npu.get('model_name', 'unknown'),
                "vendor": npu.get('vendor', 'unknown'),
                "hostname": npu.get('hostname', 'unknown'),
                "pci_bus_id": npu.get('pci_bus_id')
            }

            # Add metrics if requested and available
            if include_metrics:
                npu_metric = next((m for m in npu_metrics if m.get('npu_id') == npu_id), None)
                if npu_metric:
                    power = crud._safe_float(npu_metric.get('power_usage_watts')) or 0
                    total_npu_power += power
                    npu_entry["metrics"] = {
                        "power_watts": power,
                        "utilization_percent": crud._safe_float(npu_metric.get('npu_utilization_percent')),
                        "temperature_celsius": crud._safe_float(npu_metric.get('npu_temperature_celsius')),
                        "memory_used_mb": crud._safe_int(npu_metric.get('memory_used_mb'))
                    }

            npus.append(npu_entry)

        # Combine all accelerators
        all_accelerators = gpus + npus

        # Build response
        response = {
            "timestamp": datetime.utcnow(),
            "cluster": cluster or "default",
            "node": node,
            "summary": {
                "total_accelerators": len(all_accelerators),
                "total_gpus": len(gpus),
                "total_npus": len(npus),
                "total_power_watts": total_gpu_power + total_npu_power if include_metrics else None,
                "gpu_power_watts": total_gpu_power if include_metrics else None,
                "npu_power_watts": total_npu_power if include_metrics else None
            },
            "accelerators": all_accelerators,
            "metadata": {
                "gpu_data_source": "dcgm" if gpu_data else None,
                "npu_data_source": "prometheus" if npu_data else None,
                "metrics_included": include_metrics
            }
        }

        # Cache for 1 hour (static info) or 30 seconds (with metrics)
        ttl = 30 if include_metrics else 3600
        await cache_service.set(cache_key, response, ttl=ttl)
        return response

    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to fetch accelerators: {str(e)}")


@router.get("/accelerators/summary",
           summary="Get accelerator summary",
           description="Get comprehensive summary statistics for all accelerators (GPUs + NPUs).")
async def get_accelerators_summary(
    cluster: Optional[str] = Query(None, description="Filter by cluster name"),
    node: Optional[str] = Query(None, description="Filter by node hostname")
):
    """
    Get comprehensive summary statistics for all accelerators.

    **Query Parameters:**
    - `cluster`: Filter by cluster name
    - `node`: Filter by node hostname

    **Returns:** Unified summary including:
    - Total counts by type (GPU, NPU)
    - Status breakdown (active, idle, error)
    - Power statistics (total, average, maximum, breakdown by type)
    - Utilization statistics (average, maximum)
    - Temperature statistics (average, maximum, warning/critical counts)
    - Memory statistics (total used, available)

    **Use Case:** Dashboard overview and cluster-wide accelerator monitoring.
    """
    from app.models.accelerators.common import AcceleratorSummary

    cache_key = f"accelerators_summary_{cluster or 'all'}_{node or 'all'}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    try:
        # Collect GPU metrics
        gpu_metrics = await crud.get_dcgm_gpu_metrics(node)

        # Collect NPU metrics (placeholder - will return empty until NPU exporters configured)
        npu_metrics = await crud.get_npu_metrics(node, None, None)

        # Initialize counters
        total_gpus = len(gpu_metrics) if gpu_metrics else 0
        total_npus = len(npu_metrics) if npu_metrics else 0
        total_accelerators = total_gpus + total_npus

        # GPU statistics
        gpu_active = 0
        gpu_idle = 0
        gpu_utilizations = []
        gpu_temperatures = []
        gpu_powers = []
        gpu_memory_used = 0
        gpu_memory_available = 0
        gpu_warning_temp = 0
        gpu_critical_temp = 0

        for metric in (gpu_metrics or []):
            util = crud._safe_float(metric.get('gpu_utilization_percent'))
            if util and util > 0:
                gpu_active += 1
            else:
                gpu_idle += 1
            if util is not None:
                gpu_utilizations.append(util)

            temp = crud._safe_float(metric.get('gpu_temperature_celsius'))
            if temp is not None:
                gpu_temperatures.append(temp)
                if temp >= 85:
                    gpu_critical_temp += 1
                elif temp >= 75:
                    gpu_warning_temp += 1

            power = crud._safe_float(metric.get('power_usage_watts'))
            if power is not None:
                gpu_powers.append(power)

            mem_used = crud._safe_int(metric.get('memory_used_mb'))
            if mem_used is not None:
                gpu_memory_used += mem_used

            mem_free = crud._safe_int(metric.get('memory_free_mb'))
            if mem_free is not None:
                gpu_memory_available += mem_free

        # NPU statistics
        npu_active = 0
        npu_idle = 0
        npu_utilizations = []
        npu_temperatures = []
        npu_powers = []
        npu_memory_used = 0
        npu_memory_available = 0
        npu_warning_temp = 0

        for metric in (npu_metrics or []):
            util = crud._safe_float(metric.get('npu_utilization_percent'))
            if util and util > 0:
                npu_active += 1
            else:
                npu_idle += 1
            if util is not None:
                npu_utilizations.append(util)

            temp = crud._safe_float(metric.get('npu_temperature_celsius'))
            if temp is not None:
                npu_temperatures.append(temp)
                if temp >= 75:  # NPU warning threshold
                    npu_warning_temp += 1

            power = crud._safe_float(metric.get('power_usage_watts'))
            if power is not None:
                npu_powers.append(power)

            mem_used = crud._safe_int(metric.get('memory_used_mb'))
            if mem_used is not None:
                npu_memory_used += mem_used

            mem_free = crud._safe_int(metric.get('memory_free_mb'))
            if mem_free is not None:
                npu_memory_available += mem_free

        # Combined statistics
        all_utilizations = gpu_utilizations + npu_utilizations
        all_temperatures = gpu_temperatures + npu_temperatures
        all_powers = gpu_powers + npu_powers

        # Calculate totals and averages
        total_gpu_power = sum(gpu_powers) if gpu_powers else 0
        total_npu_power = sum(npu_powers) if npu_powers else 0
        total_power = total_gpu_power + total_npu_power

        avg_utilization = sum(all_utilizations) / len(all_utilizations) if all_utilizations else 0
        max_utilization = max(all_utilizations) if all_utilizations else 0

        avg_temperature = sum(all_temperatures) / len(all_temperatures) if all_temperatures else 0
        max_temperature = max(all_temperatures) if all_temperatures else 0

        avg_power = total_power / total_accelerators if total_accelerators > 0 else 0
        max_power = max(all_powers) if all_powers else 0

        total_memory_used = gpu_memory_used + npu_memory_used
        total_memory_available = gpu_memory_available + npu_memory_available + total_memory_used
        avg_memory_percent = (total_memory_used / total_memory_available * 100) if total_memory_available > 0 else None

        # Build summary using AcceleratorSummary model
        summary = AcceleratorSummary(
            timestamp=datetime.utcnow(),
            cluster=cluster or "default",
            total_accelerators=total_accelerators,
            total_gpus=total_gpus,
            total_npus=total_npus,
            total_other=0,
            active_accelerators=gpu_active + npu_active,
            idle_accelerators=gpu_idle + npu_idle,
            error_accelerators=0,
            avg_utilization_percent=avg_utilization,
            max_utilization_percent=max_utilization,
            avg_temperature_celsius=avg_temperature,
            max_temperature_celsius=max_temperature,
            warning_temperature_count=gpu_warning_temp + npu_warning_temp,
            critical_temperature_count=gpu_critical_temp,
            total_power_watts=total_power,
            avg_power_watts=avg_power,
            max_power_watts=max_power,
            gpu_power_watts=total_gpu_power,
            npu_power_watts=total_npu_power,
            other_power_watts=0,
            total_memory_used_mb=total_memory_used,
            total_memory_available_mb=total_memory_available,
            avg_memory_used_percent=avg_memory_percent
        )

        response = {
            "timestamp": datetime.utcnow(),
            "cluster": cluster or "default",
            "node": node,
            "summary": summary.model_dump(),
            "breakdown": {
                "gpus": {
                    "count": total_gpus,
                    "active": gpu_active,
                    "idle": gpu_idle,
                    "power_watts": total_gpu_power,
                    "avg_utilization_percent": sum(gpu_utilizations) / len(gpu_utilizations) if gpu_utilizations else 0
                },
                "npus": {
                    "count": total_npus,
                    "active": npu_active,
                    "idle": npu_idle,
                    "power_watts": total_npu_power,
                    "avg_utilization_percent": sum(npu_utilizations) / len(npu_utilizations) if npu_utilizations else 0
                }
            }
        }

        # Cache for 30 seconds
        await cache_service.set(cache_key, response, ttl=30)
        return response

    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to generate accelerators summary: {str(e)}")
