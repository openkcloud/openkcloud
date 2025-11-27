"""
NPU Data Models - AI accelerator monitoring (Furiosa AI, Rebellions).

This module defines Pydantic models for NPU information, metrics, and responses.
All field names follow unit-explicit naming convention.
"""

from typing import List, Optional
from datetime import datetime
from pydantic import BaseModel, Field
from enum import Enum


class NPUVendor(str, Enum):
    """NPU vendor enumeration."""
    FURIOSA = "furiosa"
    REBELLIONS = "rebellions"
    GRAPHCORE = "graphcore"
    INTEL_HABANA = "intel_habana"


class NPUStatus(str, Enum):
    """NPU operational status."""
    ACTIVE = "active"
    IDLE = "idle"
    ERROR = "error"
    OFFLINE = "offline"


class NPUCoreState(str, Enum):
    """NPU core operational state (Furiosa-specific)."""
    IDLE = "idle"
    RUNNING = "running"
    ERROR = "error"


# ============================================================================
# Core NPU Models
# ============================================================================

class NPUInfo(BaseModel):
    """
    Detailed NPU hardware information.

    Data source: NPU-specific Prometheus exporters (Furiosa AI, Rebellions).
    """
    model_config = {"protected_namespaces": ()}

    # Identifiers
    npu_id: str = Field(..., description="NPU identifier (e.g., npu0, furiosa0)")
    uuid: Optional[str] = Field(None, description="NPU UUID (vendor-specific)")
    device_index: Optional[int] = Field(None, ge=0, description="NPU device index")

    # Hardware information
    model_name: str = Field(..., description="NPU model name (e.g., Warboy, ATOM)")
    vendor: NPUVendor = Field(..., description="NPU vendor")
    architecture: Optional[str] = Field(None, description="NPU architecture")
    firmware_version: Optional[str] = Field(None, description="NPU firmware version")
    driver_version: Optional[str] = Field(None, description="NPU driver version")

    # Location
    hostname: str = Field(..., description="Node hostname where NPU is located")
    instance: Optional[str] = Field(None, description="Instance identifier (IP:port)")
    pci_bus_id: Optional[str] = Field(None, description="PCIe bus ID")

    # Capacity
    memory_total_mb: Optional[int] = Field(None, ge=0, description="Total NPU memory in megabytes")
    cores_total: Optional[int] = Field(None, ge=0, description="Total number of NPU cores (Furiosa)")
    pe_count: Optional[int] = Field(None, ge=0, description="Processing element count (Furiosa)")

    # Status
    status: NPUStatus = Field(NPUStatus.ACTIVE, description="NPU operational status")


class NPUMetrics(BaseModel):
    """
    Real-time NPU performance metrics.

    Data source: NPU-specific exporters (Furiosa AI, Rebellions).
    All numeric fields follow unit-explicit naming.
    """
    npu_id: str = Field(..., description="NPU identifier")
    timestamp: datetime = Field(..., description="Metric collection timestamp")

    # Utilization metrics (percentage)
    npu_utilization_percent: Optional[float] = Field(None, ge=0, le=100, description="NPU utilization percentage")
    memory_utilization_percent: Optional[float] = Field(None, ge=0, le=100, description="Memory utilization percentage")

    # Core metrics (Furiosa-specific)
    active_cores: Optional[int] = Field(None, ge=0, description="Number of active cores")
    idle_cores: Optional[int] = Field(None, ge=0, description="Number of idle cores")

    # Temperature metrics (Celsius)
    npu_temperature_celsius: Optional[float] = Field(None, description="NPU core temperature in Celsius")
    board_temperature_celsius: Optional[float] = Field(None, description="NPU board temperature in Celsius")
    temperature_limit_celsius: Optional[float] = Field(None, description="NPU temperature limit")

    # Power metrics (Watts)
    power_usage_watts: Optional[float] = Field(None, ge=0, description="Current power usage in watts")
    power_limit_watts: Optional[float] = Field(None, ge=0, description="NPU power limit in watts")
    total_energy_joules: Optional[float] = Field(None, ge=0, description="Total energy consumption in joules")

    # Memory metrics (Megabytes)
    memory_used_mb: Optional[int] = Field(None, ge=0, description="Used NPU memory in megabytes")
    memory_free_mb: Optional[int] = Field(None, ge=0, description="Free NPU memory in megabytes")

    # Performance metrics
    throughput_fps: Optional[float] = Field(None, ge=0, description="Inference throughput in frames per second")
    latency_ms: Optional[float] = Field(None, ge=0, description="Inference latency in milliseconds")

    # Error metrics
    error_count: Optional[int] = Field(None, ge=0, description="Total error count")
    timeout_count: Optional[int] = Field(None, ge=0, description="Timeout error count")


class NPUCoreStatus(BaseModel):
    """
    NPU core-level status (Furiosa AI specific).

    Provides detailed information about individual NPU cores.
    """
    npu_id: str = Field(..., description="NPU identifier")
    core_id: int = Field(..., ge=0, description="Core identifier")
    timestamp: datetime = Field(..., description="Status collection timestamp")

    # Core state
    state: NPUCoreState = Field(..., description="Core operational state")
    utilization_percent: Optional[float] = Field(None, ge=0, le=100, description="Core utilization percentage")

    # Core temperature (Celsius)
    temperature_celsius: Optional[float] = Field(None, description="Core temperature in Celsius")

    # Process information
    process_name: Optional[str] = Field(None, description="Process running on this core")
    process_id: Optional[int] = Field(None, description="Process ID")


class NPUPowerData(BaseModel):
    """
    NPU power consumption data over time.

    Supports both instant readings and time series data.
    """
    npu_id: str = Field(..., description="NPU identifier")
    timestamp: datetime = Field(..., description="Power measurement timestamp")

    # Instant power (Watts)
    power_watts: float = Field(..., ge=0, description="Current power draw in watts")

    # Statistics (Watts) - for time series queries
    avg_power_watts: Optional[float] = Field(None, ge=0, description="Average power over period")
    max_power_watts: Optional[float] = Field(None, ge=0, description="Maximum power over period")
    min_power_watts: Optional[float] = Field(None, ge=0, description="Minimum power over period")

    # Energy (Joules)
    total_energy_joules: Optional[float] = Field(None, ge=0, description="Total energy consumption")


class NPUSummary(BaseModel):
    """
    Aggregated summary statistics for all NPUs.

    Provides cluster-wide or filtered NPU statistics.
    """
    total_npus: int = Field(..., ge=0, description="Total number of NPUs")
    active_npus: int = Field(..., ge=0, description="NPUs with >0% utilization")
    idle_npus: int = Field(..., ge=0, description="NPUs with 0% utilization")
    error_npus: int = Field(..., ge=0, description="NPUs in error state")

    # Vendor breakdown
    furiosa_count: int = Field(0, ge=0, description="Number of Furiosa NPUs")
    rebellions_count: int = Field(0, ge=0, description="Number of Rebellions NPUs")

    # Utilization statistics (percent)
    avg_npu_utilization_percent: float = Field(..., ge=0, le=100, description="Average NPU utilization")
    max_npu_utilization_percent: float = Field(..., ge=0, le=100, description="Maximum NPU utilization")
    avg_memory_utilization_percent: Optional[float] = Field(None, ge=0, le=100, description="Average memory utilization")

    # Temperature statistics (Celsius)
    avg_temperature_celsius: float = Field(..., description="Average NPU temperature")
    max_temperature_celsius: float = Field(..., description="Maximum NPU temperature")
    warning_temperature_count: int = Field(0, ge=0, description="NPUs in warning temperature range")

    # Power statistics (Watts)
    total_power_watts: float = Field(..., ge=0, description="Total power consumption across all NPUs")
    avg_power_watts: float = Field(..., ge=0, description="Average power per NPU")
    max_power_watts: float = Field(..., ge=0, description="Maximum power of any single NPU")

    # Memory statistics (Megabytes)
    total_memory_used_mb: int = Field(..., ge=0, description="Total memory used across all NPUs")
    total_memory_available_mb: int = Field(..., ge=0, description="Total memory available across all NPUs")
    avg_memory_used_percent: Optional[float] = Field(None, ge=0, le=100, description="Average memory usage percentage")

    # Performance statistics
    avg_throughput_fps: Optional[float] = Field(None, ge=0, description="Average throughput in frames per second")
    avg_latency_ms: Optional[float] = Field(None, ge=0, description="Average latency in milliseconds")


# ============================================================================
# API Response Models
# ============================================================================

class NPUListResponse(BaseModel):
    """Response model for NPU list endpoint (GET /api/v1/accelerators/npus)."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp")
    cluster: str = Field("default", description="Cluster name")
    total_npus: int = Field(..., ge=0, description="Total number of NPUs")
    npus: List[NPUInfo] = Field(..., description="List of NPU information")
    summary: Optional[NPUSummary] = Field(None, description="NPU summary statistics (if requested)")


class NPUDetailResponse(BaseModel):
    """Response model for NPU detail endpoint (GET /api/v1/accelerators/npus/{npu_id})."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp")
    npu: NPUInfo = Field(..., description="Detailed NPU information")
    metrics: Optional[NPUMetrics] = Field(None, description="Current NPU metrics (if requested)")
    cores: Optional[List[NPUCoreStatus]] = Field(None, description="Core status (Furiosa only)")


class NPUMetricsResponse(BaseModel):
    """Response model for NPU metrics endpoint (GET /api/v1/accelerators/npus/{npu_id}/metrics)."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp")
    npu_id: str = Field(..., description="NPU identifier")
    metrics: NPUMetrics = Field(..., description="NPU performance metrics")


class NPUCoreStatusResponse(BaseModel):
    """Response model for NPU core status endpoint (GET /api/v1/accelerators/npus/{npu_id}/cores)."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp")
    npu_id: str = Field(..., description="NPU identifier")
    cores: List[NPUCoreStatus] = Field(..., description="Core status information")


class NPUSummaryResponse(BaseModel):
    """Response model for NPU summary endpoint (GET /api/v1/accelerators/npus/summary)."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp")
    cluster: str = Field("default", description="Cluster name")
    summary: NPUSummary = Field(..., description="NPU summary statistics")
