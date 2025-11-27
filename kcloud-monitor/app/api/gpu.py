from fastapi import APIRouter, Depends, HTTPException, Query
from typing import Optional, List
from datetime import datetime

# Authentication handled at router level in main.py
from app.models.responses import (
    DCGMGPUInfoResponse, DCGMGPUMetricsResponse, DCGMGPUInfo, DCGMGPUMetrics, DCGMGPUSummary,
    DCGMGPUTemperatureResponse, DCGMGPUTemperature
)
from app.services import cache_service
from app import crud

router = APIRouter()

@router.get("/gpu/info",
           response_model=DCGMGPUInfoResponse)
async def get_gpu_info(node: Optional[str] = Query(None, description="Filter by node hostname")):
    """Get basic GPU information from DCGM."""
    cache_key = f"dcgm_gpu_info_{node or 'all'}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    try:
        gpu_data = await crud.get_dcgm_gpu_info(node)

        if not gpu_data:
            raise HTTPException(status_code=404, detail="No GPUs found")

        # Convert to response models
        gpus = []
        node_name = "unknown"

        for gpu in gpu_data:
            node_name = gpu.get('hostname', node_name)
            gpus.append(DCGMGPUInfo(
                gpu_id=gpu.get('gpu_id', 'unknown'),
                uuid=gpu.get('uuid', 'unknown'),
                model_name=gpu.get('model_name', 'unknown'),
                driver_version=gpu.get('driver_version', 'unknown'),
                hostname=gpu.get('hostname', 'unknown'),
                pci_bus_id=gpu.get('pci_bus_id', 'unknown'),
                device_index=gpu.get('device_index', 0)
            ))

        response = DCGMGPUInfoResponse(
            timestamp=datetime.utcnow(),
            node=node_name,
            total_gpus=len(gpus),
            gpus=gpus
        )

        # Cache for 1 hour (static GPU info)
        await cache_service.set(cache_key, response, ttl=3600)
        return response

    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to fetch GPU info: {str(e)}")


@router.get("/gpu/metrics",
           response_model=DCGMGPUMetricsResponse)
async def get_gpu_metrics(
    node: Optional[str] = Query(None, description="Filter by node hostname"),
    gpu_id: Optional[str] = Query(None, description="Filter by GPU device ID (e.g., nvidia0)")
):
    """Get real-time GPU performance metrics from DCGM."""
    cache_key = f"dcgm_gpu_metrics_{node or 'all'}_{gpu_id or 'all'}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    try:
        metrics_data = await crud.get_dcgm_gpu_metrics(node, gpu_id)

        if not metrics_data:
            raise HTTPException(status_code=404, detail="No GPU metrics found")

        # Convert to response models
        metrics = []
        node_name = "unknown"

        for metric in metrics_data:
            node_name = metric.get('hostname', node_name)

            # Use safe conversion helpers
            gpu_metric = DCGMGPUMetrics(
                gpu_id=metric.get('gpu_id', 'unknown'),
                timestamp=metric.get('timestamp', datetime.utcnow()),

                # Performance metrics
                gpu_utilization_percent=crud._safe_float(metric.get('gpu_utilization_percent')),
                decoder_utilization_percent=crud._safe_float(metric.get('decoder_utilization_percent')),
                encoder_utilization_percent=crud._safe_float(metric.get('encoder_utilization_percent')),
                memory_copy_utilization_percent=crud._safe_float(metric.get('memory_copy_utilization_percent')),

                # Temperature metrics
                gpu_temperature_celsius=crud._safe_float(metric.get('gpu_temperature_celsius')),
                memory_temperature_celsius=crud._safe_float(metric.get('memory_temperature_celsius')),

                # Power metrics
                power_usage_watts=crud._safe_float(metric.get('power_usage_watts')),
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
            metrics.append(gpu_metric)

        response = DCGMGPUMetricsResponse(
            timestamp=datetime.utcnow(),
            node=node_name,
            total_gpus=len(metrics),
            metrics=metrics
        )

        # Cache for 30 seconds (real-time metrics)
        await cache_service.set(cache_key, response, ttl=30)
        return response

    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to fetch GPU metrics: {str(e)}")


@router.get("/gpu/summary")
async def get_gpu_summary(node: Optional[str] = Query(None, description="Filter by node hostname")):
    """Get GPU summary statistics."""
    cache_key = f"dcgm_gpu_summary_{node or 'all'}"
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
        utilizations = []
        temperatures = []
        total_power = 0
        total_memory_used = 0
        total_memory_available = 0

        for metric in metrics_data:
            gpu_util = crud._safe_float(metric.get('gpu_utilization_percent'))
            if gpu_util and gpu_util > 0:
                active_gpus += 1
            if gpu_util is not None:
                utilizations.append(gpu_util)

            temp = crud._safe_float(metric.get('gpu_temperature_celsius'))
            if temp is not None:
                temperatures.append(temp)

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

        summary = DCGMGPUSummary(
            total_gpus=total_gpus,
            active_gpus=active_gpus,
            avg_gpu_utilization_percent=avg_utilization,
            max_gpu_utilization_percent=max_utilization,
            avg_temperature_celsius=avg_temperature,
            max_temperature_celsius=max_temperature,
            total_power_watts=total_power,
            total_memory_used_mb=total_memory_used,
            total_memory_available_mb=total_memory_available + total_memory_used
        )

        response = {
            "timestamp": datetime.utcnow(),
            "summary": summary
        }

        # Cache for 30 seconds
        await cache_service.set(cache_key, response, ttl=30)
        return response

    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to generate GPU summary: {str(e)}")


@router.get("/gpu/temperature",
           response_model=DCGMGPUTemperatureResponse)
async def get_gpu_temperature(
    node: Optional[str] = Query(None, description="Filter by node hostname"),
    gpu_id: Optional[str] = Query(None, description="Filter by GPU device ID (e.g., nvidia0)")
):
    """Get GPU temperature monitoring data from DCGM."""
    cache_key = f"dcgm_gpu_temperature_{node or 'all'}_{gpu_id or 'all'}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    try:
        temp_data = await crud.get_dcgm_gpu_temperatures(node, gpu_id)

        if not temp_data:
            raise HTTPException(status_code=404, detail="No GPU temperature data found")

        # Convert to response models
        temperatures = []
        node_name = "unknown"
        temp_values = []

        for temp in temp_data:
            node_name = temp.get('hostname', node_name)

            gpu_temp = DCGMGPUTemperature(
                gpu_id=temp.get('gpu_id', 'unknown'),
                hostname=temp.get('hostname', 'unknown'),
                timestamp=temp.get('timestamp', datetime.utcnow()),
                gpu_temperature_celsius=crud._safe_float(temp.get('gpu_temperature_celsius')),
                memory_temperature_celsius=crud._safe_float(temp.get('memory_temperature_celsius')),
                temperature_limit_celsius=crud._safe_float(temp.get('temperature_limit_celsius')),
                temperature_status=temp.get('temperature_status', 'normal')
            )
            temperatures.append(gpu_temp)

            # Collect temperature values for summary
            if gpu_temp.gpu_temperature_celsius is not None:
                temp_values.append(gpu_temp.gpu_temperature_celsius)
            if gpu_temp.memory_temperature_celsius is not None:
                temp_values.append(gpu_temp.memory_temperature_celsius)

        # Generate temperature summary
        temperature_summary = None
        if temp_values:
            temperature_summary = {
                "avg_temperature_celsius": sum(temp_values) / len(temp_values),
                "max_temperature_celsius": max(temp_values),
                "min_temperature_celsius": min(temp_values),
                "critical_count": sum(1 for t in temperatures if t.temperature_status == "critical"),
                "warning_count": sum(1 for t in temperatures if t.temperature_status == "warning"),
                "normal_count": sum(1 for t in temperatures if t.temperature_status == "normal")
            }

        response = DCGMGPUTemperatureResponse(
            timestamp=datetime.utcnow(),
            node=node_name,
            total_gpus=len(temperatures),
            temperatures=temperatures,
            temperature_summary=temperature_summary
        )

        # Cache for 30 seconds (real-time temperature data)
        await cache_service.set(cache_key, response, ttl=30)
        return response

    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to fetch GPU temperature data: {str(e)}")