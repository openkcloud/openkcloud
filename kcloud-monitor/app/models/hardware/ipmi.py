"""
IPMI Data Models - Physical hardware sensor monitoring.

This module defines Pydantic models for IPMI sensor data.
Data source: IPMI Exporter Prometheus metrics.
All field names follow unit-explicit naming convention.
"""

from typing import List, Optional, Dict
from datetime import datetime
from pydantic import BaseModel, Field
from enum import Enum


class IPMISensorType(str, Enum):
    """IPMI sensor type enumeration."""
    TEMPERATURE = "temperature"
    POWER = "power"
    FAN = "fan"
    VOLTAGE = "voltage"
    CURRENT = "current"
    OTHER = "other"


class IPMISensorStatus(str, Enum):
    """IPMI sensor status levels."""
    NORMAL = "normal"
    WARNING = "warning"
    CRITICAL = "critical"
    UNKNOWN = "unknown"


# ============================================================================
# Core IPMI Models
# ============================================================================

class IPMISensorData(BaseModel):
    """
    Generic IPMI sensor data.

    Data source: IPMI Exporter Prometheus metrics.
    """
    # Identifiers
    sensor_id: str = Field(..., description="Sensor identifier")
    sensor_name: str = Field(..., description="Sensor name (e.g., CPU1_Temp, FAN1)")
    sensor_type: IPMISensorType = Field(..., description="Sensor type")
    node_name: str = Field(..., description="Node where sensor is located")

    # Sensor value
    value: float = Field(..., description="Sensor value in appropriate unit")
    unit: str = Field(..., description="Measurement unit (celsius, watts, rpm, volts)")

    # Status and thresholds
    status: IPMISensorStatus = Field(IPMISensorStatus.NORMAL, description="Sensor status")
    lower_critical: Optional[float] = Field(None, description="Lower critical threshold")
    lower_warning: Optional[float] = Field(None, description="Lower warning threshold")
    upper_warning: Optional[float] = Field(None, description="Upper warning threshold")
    upper_critical: Optional[float] = Field(None, description="Upper critical threshold")

    # Metadata
    timestamp: datetime = Field(..., description="Sensor reading timestamp")
    entity_id: Optional[str] = Field(None, description="IPMI entity ID")


class IPMIPowerData(BaseModel):
    """
    IPMI power sensor data.

    Provides detailed power consumption from IPMI sensors.
    """
    node_name: str = Field(..., description="Node name")
    timestamp: datetime = Field(..., description="Power measurement timestamp")

    # Total power (Watts)
    total_power_watts: float = Field(..., ge=0, description="Total system power in watts")

    # Individual power sensors (Watts)
    psu1_power_watts: Optional[float] = Field(None, ge=0, description="PSU1 power output in watts")
    psu2_power_watts: Optional[float] = Field(None, ge=0, description="PSU2 power output in watts")
    cpu_power_watts: Optional[float] = Field(None, ge=0, description="CPU power consumption in watts")
    memory_power_watts: Optional[float] = Field(None, ge=0, description="Memory power consumption in watts")

    # Power status
    power_status: IPMISensorStatus = Field(IPMISensorStatus.NORMAL, description="Overall power status")
    power_limit_watts: Optional[float] = Field(None, ge=0, description="System power limit in watts")

    # PSU status
    psu1_status: Optional[IPMISensorStatus] = Field(None, description="PSU1 status")
    psu2_status: Optional[IPMISensorStatus] = Field(None, description="PSU2 status")


class IPMITemperatureData(BaseModel):
    """
    IPMI temperature sensor data.

    Provides temperature readings from various system components.
    """
    node_name: str = Field(..., description="Node name")
    timestamp: datetime = Field(..., description="Temperature measurement timestamp")

    # CPU temperatures (Celsius)
    cpu1_temperature_celsius: Optional[float] = Field(None, description="CPU1 temperature in Celsius")
    cpu2_temperature_celsius: Optional[float] = Field(None, description="CPU2 temperature in Celsius")

    # System temperatures (Celsius)
    inlet_temperature_celsius: Optional[float] = Field(None, description="Inlet temperature in Celsius")
    exhaust_temperature_celsius: Optional[float] = Field(None, description="Exhaust temperature in Celsius")
    motherboard_temperature_celsius: Optional[float] = Field(None, description="Motherboard temperature in Celsius")

    # Memory temperatures (Celsius)
    memory_temperature_celsius: Optional[float] = Field(None, description="Memory temperature in Celsius")

    # PCIe temperatures (Celsius)
    pcie_temperature_celsius: Optional[float] = Field(None, description="PCIe area temperature in Celsius")

    # Temperature status
    overall_temperature_status: IPMISensorStatus = Field(IPMISensorStatus.NORMAL, description="Overall temperature status")
    highest_temperature_celsius: Optional[float] = Field(None, description="Highest temperature reading")
    critical_temperature_count: int = Field(0, ge=0, description="Number of sensors in critical state")
    warning_temperature_count: int = Field(0, ge=0, description="Number of sensors in warning state")


class IPMIFanData(BaseModel):
    """
    IPMI fan sensor data.

    Provides fan speed and status information.
    """
    node_name: str = Field(..., description="Node name")
    timestamp: datetime = Field(..., description="Fan reading timestamp")

    # Fan speeds (RPM - Revolutions Per Minute)
    fan1_speed_rpm: Optional[int] = Field(None, ge=0, description="FAN1 speed in RPM")
    fan2_speed_rpm: Optional[int] = Field(None, ge=0, description="FAN2 speed in RPM")
    fan3_speed_rpm: Optional[int] = Field(None, ge=0, description="FAN3 speed in RPM")
    fan4_speed_rpm: Optional[int] = Field(None, ge=0, description="FAN4 speed in RPM")
    fan5_speed_rpm: Optional[int] = Field(None, ge=0, description="FAN5 speed in RPM")
    fan6_speed_rpm: Optional[int] = Field(None, ge=0, description="FAN6 speed in RPM")

    # Fan status
    overall_fan_status: IPMISensorStatus = Field(IPMISensorStatus.NORMAL, description="Overall fan system status")
    fan_failure_count: int = Field(0, ge=0, description="Number of failed fans")
    avg_fan_speed_rpm: Optional[float] = Field(None, ge=0, description="Average fan speed in RPM")


class IPMIVoltageData(BaseModel):
    """
    IPMI voltage sensor data.

    Provides voltage readings from power rails.
    """
    node_name: str = Field(..., description="Node name")
    timestamp: datetime = Field(..., description="Voltage reading timestamp")

    # Voltage readings (Volts)
    voltage_12v: Optional[float] = Field(None, ge=0, description="12V rail voltage")
    voltage_5v: Optional[float] = Field(None, ge=0, description="5V rail voltage")
    voltage_3_3v: Optional[float] = Field(None, ge=0, description="3.3V rail voltage")
    voltage_cpu: Optional[float] = Field(None, ge=0, description="CPU voltage")
    voltage_dimm: Optional[float] = Field(None, ge=0, description="DIMM voltage")

    # Voltage status
    overall_voltage_status: IPMISensorStatus = Field(IPMISensorStatus.NORMAL, description="Overall voltage status")
    voltage_warning_count: int = Field(0, ge=0, description="Number of voltage sensors in warning state")


# ============================================================================
# API Response Models
# ============================================================================

class IPMISensorListResponse(BaseModel):
    """Response model for IPMI sensor list endpoint (GET /api/v1/hardware/ipmi/sensors)."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp")
    node_filter: Optional[str] = Field(None, description="Node filter applied")
    sensor_type_filter: Optional[IPMISensorType] = Field(None, description="Sensor type filter applied")
    total_sensors: int = Field(..., ge=0, description="Total number of sensors")
    sensors: List[IPMISensorData] = Field(..., description="List of sensor data")

    # Summary statistics
    critical_sensors: int = Field(0, ge=0, description="Number of sensors in critical state")
    warning_sensors: int = Field(0, ge=0, description="Number of sensors in warning state")


class IPMIPowerResponse(BaseModel):
    """Response model for IPMI power endpoint (GET /api/v1/hardware/ipmi/power)."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp")
    node_filter: Optional[str] = Field(None, description="Node filter applied")
    total_nodes: int = Field(..., ge=0, description="Total number of nodes")
    power_data: List[IPMIPowerData] = Field(..., description="List of node power data")

    # Summary statistics
    total_power_watts: float = Field(..., ge=0, description="Total power across all nodes")
    avg_power_watts: float = Field(..., ge=0, description="Average power per node")
    max_power_watts: float = Field(..., ge=0, description="Maximum power of any single node")


class IPMITemperatureResponse(BaseModel):
    """Response model for IPMI temperature endpoint (GET /api/v1/hardware/ipmi/temperature)."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp")
    node_filter: Optional[str] = Field(None, description="Node filter applied")
    total_nodes: int = Field(..., ge=0, description="Total number of nodes")
    temperature_data: List[IPMITemperatureData] = Field(..., description="List of node temperature data")

    # Summary statistics
    highest_temperature_celsius: Optional[float] = Field(None, description="Highest temperature across all nodes")
    avg_temperature_celsius: Optional[float] = Field(None, description="Average temperature across all nodes")
    critical_node_count: int = Field(0, ge=0, description="Number of nodes with critical temperatures")
    warning_node_count: int = Field(0, ge=0, description="Number of nodes with warning temperatures")


class IPMIFanResponse(BaseModel):
    """Response model for IPMI fan endpoint (GET /api/v1/hardware/ipmi/fans)."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp")
    node_filter: Optional[str] = Field(None, description="Node filter applied")
    total_nodes: int = Field(..., ge=0, description="Total number of nodes")
    fan_data: List[IPMIFanData] = Field(..., description="List of node fan data")

    # Summary statistics
    total_fans: int = Field(..., ge=0, description="Total number of fans across all nodes")
    failed_fans: int = Field(0, ge=0, description="Total number of failed fans")
    avg_fan_speed_rpm: Optional[float] = Field(None, ge=0, description="Average fan speed in RPM")


class IPMIVoltageResponse(BaseModel):
    """Response model for IPMI voltage endpoint (GET /api/v1/hardware/ipmi/voltage)."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp")
    node_filter: Optional[str] = Field(None, description="Node filter applied")
    total_nodes: int = Field(..., ge=0, description="Total number of nodes")
    voltage_data: List[IPMIVoltageData] = Field(..., description="List of node voltage data")
    message: Optional[str] = Field(None, description="Informational message when voltage data is unavailable")


class IPMISummaryResponse(BaseModel):
    """Response model for IPMI summary endpoint (GET /api/v1/hardware/ipmi/summary)."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp")
    total_nodes: int = Field(..., ge=0, description="Total number of monitored nodes")

    # Power summary
    total_power_watts: float = Field(..., ge=0, description="Total power across all nodes")
    avg_power_watts: float = Field(..., ge=0, description="Average power per node")

    # Temperature summary
    highest_temperature_celsius: Optional[float] = Field(None, description="Highest temperature")
    avg_temperature_celsius: Optional[float] = Field(None, description="Average temperature")
    critical_temperature_count: int = Field(0, ge=0, description="Sensors in critical state")
    warning_temperature_count: int = Field(0, ge=0, description="Sensors in warning state")

    # Fan summary
    total_fans: int = Field(..., ge=0, description="Total number of fans")
    failed_fans: int = Field(0, ge=0, description="Failed fans")
    avg_fan_speed_rpm: Optional[float] = Field(None, ge=0, description="Average fan speed")

    # Overall health
    critical_nodes: int = Field(0, ge=0, description="Nodes with critical issues")
    warning_nodes: int = Field(0, ge=0, description="Nodes with warnings")
    healthy_nodes: int = Field(..., ge=0, description="Healthy nodes")
