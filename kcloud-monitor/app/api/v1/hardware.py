"""
Hardware API - Physical hardware monitoring endpoints (IPMI-based).

This module provides endpoints for:
- IPMI sensor monitoring (power, temperature, fans, voltage)
- BMC (Baseboard Management Controller) data
- Physical server hardware health
"""

from fastapi import APIRouter, Depends, HTTPException, Query, Path
from typing import Optional
from datetime import datetime
import logging

# Authentication handled at router level in main.py
from app.models.hardware.ipmi import (
    IPMISensorListResponse,
    IPMIPowerResponse,
    IPMITemperatureResponse,
    IPMIFanResponse,
    IPMISummaryResponse,
    IPMISensorType,
    IPMISensorStatus,
    IPMIVoltageResponse
)
from app.services import cache_service
from app import crud

router = APIRouter()
logger = logging.getLogger(__name__)

# ============================================================================
# IPMI Sensor Endpoints
# ============================================================================

@router.get("/hardware/ipmi/sensors",
           response_model=IPMISensorListResponse,
           summary="Get all IPMI sensors",
           description="Get data from all IPMI sensors across all nodes.")
async def get_ipmi_sensors(
    node: Optional[str] = Query(None, description="Filter by node hostname"),
    sensor_type: Optional[str] = Query(None, description="Filter by sensor type (temperature/power/fan/voltage)")
):
    """
    Get data from all IPMI sensors.

    **Query Parameters:**
    - `node`: Filter by node hostname
    - `sensor_type`: Filter by sensor type (temperature, power, fan, voltage)

    **Returns:** List of all IPMI sensor readings with thresholds and status.

    **Note:** IPMI Exporter must be configured and running on target nodes.
    See spec/PROMETHEUS_SETUP.md for configuration details.
    """
    # Build cache key
    cache_key = f"ipmi_sensors:{node or 'all'}:{sensor_type or 'all'}"

    # Try to get from cache (30 second TTL)
    cached_result = await cache_service.get(cache_key)
    if cached_result:
        logger.debug(f"Cache hit for {cache_key}")
        return cached_result

    try:
        # Get sensors from Prometheus
        sensors_data = await crud.get_ipmi_all_sensors(
            node_filter=node,
            sensor_type_filter=sensor_type
        )

        # Calculate summary statistics
        critical_sensors = sum(1 for s in sensors_data if s['status'] == IPMISensorStatus.CRITICAL.value)
        warning_sensors = sum(1 for s in sensors_data if s['status'] == IPMISensorStatus.WARNING.value)

        # Build response
        response = IPMISensorListResponse(
            timestamp=datetime.utcnow(),
            node_filter=node,
            sensor_type_filter=IPMISensorType(sensor_type) if sensor_type else None,
            total_sensors=len(sensors_data),
            sensors=sensors_data,
            critical_sensors=critical_sensors,
            warning_sensors=warning_sensors
        )

        # Cache result
        await cache_service.set(cache_key, response, ttl=30)
        logger.info(f"Retrieved {len(sensors_data)} IPMI sensors")

        return response

    except Exception as e:
        logger.error(f"Failed to get IPMI sensors: {e}")
        raise HTTPException(status_code=500, detail=f"Failed to retrieve IPMI sensors: {str(e)}")


@router.get("/hardware/ipmi/sensors/{node_name}",
           response_model=IPMISensorListResponse,
           summary="Get IPMI sensors for node",
           description="Get all IPMI sensor data for a specific node.")
async def get_node_ipmi_sensors(
    node_name: str = Path(..., description="Node hostname")
):
    """
    Get all IPMI sensor data for a specific node.

    **Path Parameters:**
    - `node_name`: Node hostname

    **Returns:** All IPMI sensors for the specified node.
    """
    # Reuse the main endpoint with node filter
    return await get_ipmi_sensors(node=node_name, sensor_type=None)


@router.get("/hardware/ipmi/power",
           response_model=IPMIPowerResponse,
           summary="Get IPMI power sensors",
           description="Get power consumption data from IPMI sensors.")
async def get_ipmi_power(
    node: Optional[str] = Query(None, description="Filter by node hostname")
):
    """
    Get power consumption data from IPMI sensors.

    **Query Parameters:**
    - `node`: Filter by node hostname

    **Returns:** IPMI power sensor readings including PSU data.

    **Note:** Provides detailed power breakdown per node including PSU1/PSU2, CPU, and memory power.
    """
    # Build cache key
    cache_key = f"ipmi_power:{node or 'all'}"

    # Try to get from cache (30 second TTL)
    cached_result = await cache_service.get(cache_key)
    if cached_result:
        logger.debug(f"Cache hit for {cache_key}")
        return cached_result

    try:
        # Get power data from Prometheus
        power_data_list = await crud.get_ipmi_power(node_filter=node)

        if not power_data_list:
            logger.warning(f"No IPMI power data found for node filter: {node}")

        # Calculate summary statistics
        total_power = sum(p['total_power_watts'] for p in power_data_list)
        avg_power = total_power / len(power_data_list) if power_data_list else 0.0
        max_power = max((p['total_power_watts'] for p in power_data_list), default=0.0)

        # Build response
        response = IPMIPowerResponse(
            timestamp=datetime.utcnow(),
            node_filter=node,
            total_nodes=len(power_data_list),
            power_data=power_data_list,
            total_power_watts=total_power,
            avg_power_watts=avg_power,
            max_power_watts=max_power
        )

        # Cache result
        await cache_service.set(cache_key, response, ttl=30)
        logger.info(f"Retrieved IPMI power data for {len(power_data_list)} nodes")

        return response

    except Exception as e:
        logger.error(f"Failed to get IPMI power data: {e}")
        raise HTTPException(status_code=500, detail=f"Failed to retrieve IPMI power data: {str(e)}")


@router.get("/hardware/ipmi/temperature",
           response_model=IPMITemperatureResponse,
           summary="Get IPMI temperature sensors",
           description="Get temperature data from IPMI sensors.")
async def get_ipmi_temperature(
    node: Optional[str] = Query(None, description="Filter by node hostname")
):
    """
    Get temperature data from IPMI sensors.

    **Query Parameters:**
    - `node`: Filter by node hostname

    **Returns:** IPMI temperature sensor readings with thresholds.

    **Note:** Includes CPU, inlet, exhaust, motherboard, memory, and PCIe temperatures.
    """
    # Build cache key
    cache_key = f"ipmi_temperature:{node or 'all'}"

    # Try to get from cache (30 second TTL)
    cached_result = await cache_service.get(cache_key)
    if cached_result:
        logger.debug(f"Cache hit for {cache_key}")
        return cached_result

    try:
        # Get temperature data from Prometheus
        temp_data_list = await crud.get_ipmi_temperature(node_filter=node)

        if not temp_data_list:
            logger.warning(f"No IPMI temperature data found for node filter: {node}")

        # Calculate summary statistics
        all_highest = [t['highest_temperature_celsius'] for t in temp_data_list if t.get('highest_temperature_celsius')]
        highest_temp = max(all_highest) if all_highest else None

        # Calculate average of all highest temps
        avg_temp = sum(all_highest) / len(all_highest) if all_highest else None

        critical_node_count = sum(1 for t in temp_data_list if t['overall_temperature_status'] == IPMISensorStatus.CRITICAL.value)
        warning_node_count = sum(1 for t in temp_data_list if t['overall_temperature_status'] == IPMISensorStatus.WARNING.value)

        # Build response
        response = IPMITemperatureResponse(
            timestamp=datetime.utcnow(),
            node_filter=node,
            total_nodes=len(temp_data_list),
            temperature_data=temp_data_list,
            highest_temperature_celsius=highest_temp,
            avg_temperature_celsius=avg_temp,
            critical_node_count=critical_node_count,
            warning_node_count=warning_node_count
        )

        # Cache result
        await cache_service.set(cache_key, response, ttl=30)
        logger.info(f"Retrieved IPMI temperature data for {len(temp_data_list)} nodes")

        return response

    except Exception as e:
        logger.error(f"Failed to get IPMI temperature data: {e}")
        raise HTTPException(status_code=500, detail=f"Failed to retrieve IPMI temperature data: {str(e)}")


@router.get("/hardware/ipmi/fans",
           response_model=IPMIFanResponse,
           summary="Get IPMI fan sensors",
           description="Get fan speed data from IPMI sensors.")
async def get_ipmi_fans(
    node: Optional[str] = Query(None, description="Filter by node hostname")
):
    """
    Get fan speed data from IPMI sensors.

    **Query Parameters:**
    - `node`: Filter by node hostname

    **Returns:** IPMI fan sensor readings (RPM).

    **Note:** Supports up to 6 fans per node, includes failure detection.
    """
    # Build cache key
    cache_key = f"ipmi_fans:{node or 'all'}"

    # Try to get from cache (30 second TTL)
    cached_result = await cache_service.get(cache_key)
    if cached_result:
        logger.debug(f"Cache hit for {cache_key}")
        return cached_result

    try:
        # Get fan data from Prometheus
        fan_data_list = await crud.get_ipmi_fans(node_filter=node)

        if not fan_data_list:
            logger.warning(f"No IPMI fan data found for node filter: {node}")

        # Calculate summary statistics
        # Estimate total fans (count non-null fan speeds)
        total_fans = 0
        for fan_data in fan_data_list:
            for i in range(1, 7):
                if fan_data.get(f'fan{i}_speed_rpm') is not None:
                    total_fans += 1

        failed_fans = sum(f['fan_failure_count'] for f in fan_data_list)

        # Calculate average fan speed
        all_avg_speeds = [f['avg_fan_speed_rpm'] for f in fan_data_list if f.get('avg_fan_speed_rpm')]
        avg_fan_speed = sum(all_avg_speeds) / len(all_avg_speeds) if all_avg_speeds else None

        # Build response
        response = IPMIFanResponse(
            timestamp=datetime.utcnow(),
            node_filter=node,
            total_nodes=len(fan_data_list),
            fan_data=fan_data_list,
            total_fans=total_fans,
            failed_fans=failed_fans,
            avg_fan_speed_rpm=avg_fan_speed
        )

        # Cache result
        await cache_service.set(cache_key, response, ttl=30)
        logger.info(f"Retrieved IPMI fan data for {len(fan_data_list)} nodes")

        return response

    except Exception as e:
        logger.error(f"Failed to get IPMI fan data: {e}")
        raise HTTPException(status_code=500, detail=f"Failed to retrieve IPMI fan data: {str(e)}")


@router.get("/hardware/ipmi/voltage",
           response_model=IPMIVoltageResponse,
           summary="Get IPMI voltage sensors",
           description="Get voltage data from IPMI sensors.")
async def get_ipmi_voltage(
    node: Optional[str] = Query(None, description="Filter by node hostname")
):
    """
    Get voltage data from IPMI sensors.

    **Query Parameters:**
    - `node`: Filter by node hostname

    **Returns:** IPMI voltage sensor readings with thresholds.

    **Note:** Monitors 12V, 5V, 3.3V power rails, plus CPU and DIMM voltages.

    **Status:** Phase 5 - Implemented (API only, IPMI Exporter setup required)
    """
    # Build cache key
    cache_key = f"ipmi_voltage:{node or 'all'}"

    # Try to get from cache (30 second TTL)
    cached_result = await cache_service.get(cache_key)
    if cached_result:
        logger.debug(f"Cache hit for {cache_key}")
        return cached_result

    try:
        # Get voltage data from Prometheus
        voltage_data_list = await crud.get_ipmi_voltage(node_filter=node)

        if not voltage_data_list:
            logger.warning(f"No IPMI voltage data found for node filter: {node}")
            response = IPMIVoltageResponse(
                timestamp=datetime.utcnow(),
                node_filter=node,
                total_nodes=0,
                voltage_data=[],
                message="IPMI voltage data not available. Ensure IPMI Exporter is configured and running."
            )
            await cache_service.set(cache_key, response, ttl=30)
            return response

        response = IPMIVoltageResponse(
            timestamp=datetime.utcnow(),
            node_filter=node,
            total_nodes=len(voltage_data_list),
            voltage_data=voltage_data_list
        )
        await cache_service.set(cache_key, response, ttl=30)
        logger.info(f"Retrieved IPMI voltage data for {len(voltage_data_list)} nodes")

        return response

    except Exception as e:
        logger.error(f"Failed to get IPMI voltage data: {e}")
        raise HTTPException(status_code=500, detail=f"Failed to retrieve IPMI voltage data: {str(e)}")


@router.get("/hardware/ipmi/summary",
           response_model=IPMISummaryResponse,
           summary="Get IPMI summary",
           description="Get summary statistics for all IPMI sensors.")
async def get_ipmi_summary(
    node: Optional[str] = Query(None, description="Filter by node hostname")
):
    """
    Get summary statistics for all IPMI sensors.

    **Query Parameters:**
    - `node`: Filter by node hostname

    **Returns:** IPMI summary including:
    - Total power consumption across all nodes
    - Temperature statistics (highest, average, critical/warning counts)
    - Fan status (total fans, failures, average speed)
    - Overall health status (healthy/warning/critical nodes)

    **Note:** Aggregates data from power, temperature, and fan sensors.
    """
    # Build cache key
    cache_key = f"ipmi_summary:{node or 'all'}"

    # Try to get from cache (30 second TTL)
    cached_result = await cache_service.get(cache_key)
    if cached_result:
        logger.debug(f"Cache hit for {cache_key}")
        return cached_result

    try:
        # Get summary from helper function
        summary_data = await crud.get_ipmi_summary(node_filter=node)

        # Build response
        response = IPMISummaryResponse(**summary_data)

        # Cache result
        await cache_service.set(cache_key, response, ttl=30)
        logger.info(f"Retrieved IPMI summary for {summary_data['total_nodes']} nodes")

        return response

    except Exception as e:
        logger.error(f"Failed to get IPMI summary: {e}")
        raise HTTPException(status_code=500, detail=f"Failed to retrieve IPMI summary: {str(e)}")
