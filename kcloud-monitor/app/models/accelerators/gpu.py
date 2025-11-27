"""
GPU Data Models - NVIDIA GPU monitoring via DCGM.

This module defines Pydantic models for GPU information, metrics, and responses.
All field names follow unit-explicit naming convention.
"""

from typing import List, Optional
from datetime import datetime
from pydantic import BaseModel, Field
from enum import Enum


class GPUVendor(str, Enum):
    """GPU vendor enumeration."""
    NVIDIA = "nvidia"
    AMD = "amd"
    INTEL = "intel"


class GPUStatus(str, Enum):
    """GPU operational status."""
    ACTIVE = "active"
    IDLE = "idle"
    ERROR = "error"
    OFFLINE = "offline"


class TemperatureStatus(str, Enum):
    """GPU temperature status levels."""
    NORMAL = "normal"
    WARNING = "warning"
    CRITICAL = "critical"


class DataSource(str, Enum):
    """Data source for GPU metrics."""
    KEPLER = "kepler"
    DCGM = "dcgm"
    HYBRID = "hybrid"
    PARTIAL_DCGM = "partial-dcgm"


# ============================================================================
# Core GPU Models
# ============================================================================

class GPUInfo(BaseModel):
    """
    Detailed GPU hardware information.

    Data source: DCGM (GPU-level detail) or Kepler (node-level aggregation).
    """
    model_config = {"protected_namespaces": ()}

    # Identifiers
    gpu_id: str = Field(..., description="GPU identifier (e.g., nvidia0, GPU-0)")
    uuid: Optional[str] = Field(None, description="GPU UUID (DCGM only)")
    device_index: Optional[int] = Field(None, ge=0, description="GPU device index (DCGM only)")

    # Hardware information
    model_name: str = Field(..., description="GPU model name (e.g., NVIDIA A30)")
    vendor: GPUVendor = Field(GPUVendor.NVIDIA, description="GPU vendor")
    architecture: Optional[str] = Field(None, description="GPU architecture (e.g., Ampere)")
    driver_version: Optional[str] = Field(None, description="NVIDIA driver version")

    # Location
    hostname: str = Field(..., description="Node hostname where GPU is located")
    instance: Optional[str] = Field(None, description="Instance identifier (IP:port)")
    pci_bus_id: Optional[str] = Field(None, description="PCIe bus ID")

    # VM-specific fields (for GPU passthrough)
    is_vm_gpu: Optional[bool] = Field(None, description="Whether this GPU is in a VM (passthrough)")
    hypervisor_host: Optional[str] = Field(None, description="Physical host running the VM (if VM GPU)")
    vm_type: Optional[str] = Field(None, description="VM type (e.g., kvm-qemu)")
    physical_node: Optional[str] = Field(None, description="Physical node location (hypervisor or direct host)")
    gpu_allocation: Optional[str] = Field(None, description="GPU allocation type (kubernetes | passthrough)")
    
    # Power monitoring source
    power_source: Optional[str] = Field(None, description="Power monitoring source (kepler | dcgm)")

    # Capacity
    memory_total_mb: Optional[int] = Field(None, ge=0, description="Total GPU memory in megabytes")
    compute_capability: Optional[str] = Field(None, description="CUDA compute capability")

    # Status
    status: GPUStatus = Field(GPUStatus.ACTIVE, description="GPU operational status")
    data_source: DataSource = Field(DataSource.KEPLER, description="Primary data source")


class GPUMetrics(BaseModel):
    """
    Real-time GPU performance metrics.

    Data source: DCGM for individual GPU metrics.
    All numeric fields follow unit-explicit naming (e.g., power_watts).
    """
    model_config = {"json_schema_extra": {"exclude_none": True}}
    
    gpu_id: str = Field(..., description="GPU identifier")
    timestamp: datetime = Field(..., description="Metric collection timestamp")

    # Utilization metrics (percentage)
    gpu_utilization_percent: Optional[float] = Field(None, ge=0, le=100, description="GPU utilization percentage")
    memory_utilization_percent: Optional[float] = Field(None, ge=0, le=100, description="Memory utilization percentage")
    decoder_utilization_percent: Optional[float] = Field(None, ge=0, le=100, description="Decoder utilization percentage")
    encoder_utilization_percent: Optional[float] = Field(None, ge=0, le=100, description="Encoder utilization percentage")
    memory_copy_utilization_percent: Optional[float] = Field(None, ge=0, le=100, description="Memory copy utilization percentage")

    # Temperature metrics (Celsius)
    gpu_temperature_celsius: Optional[float] = Field(None, description="GPU core temperature in Celsius")
    memory_temperature_celsius: Optional[float] = Field(None, description="GPU memory temperature in Celsius")
    temperature_limit_celsius: Optional[float] = Field(None, description="GPU temperature limit")

    # Power metrics (Watts)
    power_usage_watts: Optional[float] = Field(None, ge=0, description="Current power usage in watts")
    power_limit_watts: Optional[float] = Field(None, ge=0, description="GPU power limit in watts")
    total_energy_joules: Optional[float] = Field(None, ge=0, description="Total energy consumption in joules")

    # Memory metrics (Megabytes)
    memory_used_mb: Optional[int] = Field(None, ge=0, description="Used GPU memory in megabytes")
    memory_free_mb: Optional[int] = Field(None, ge=0, description="Free GPU memory in megabytes")
    memory_reserved_mb: Optional[int] = Field(None, ge=0, description="Reserved GPU memory in megabytes")

    # Clock metrics (MHz)
    sm_clock_mhz: Optional[int] = Field(None, ge=0, description="SM (Streaming Multiprocessor) clock speed in MHz")
    memory_clock_mhz: Optional[int] = Field(None, ge=0, description="Memory clock speed in MHz")

    # Error metrics
    xid_errors: Optional[int] = Field(None, ge=0, description="XID error count")
    pcie_replay_counter: Optional[int] = Field(None, ge=0, description="PCIe replay counter")
    correctable_remapped_rows: Optional[int] = Field(None, ge=0, description="Correctable remapped memory rows")
    uncorrectable_remapped_rows: Optional[int] = Field(None, ge=0, description="Uncorrectable remapped memory rows")


class GPUPowerData(BaseModel):
    """
    GPU power consumption data over time.

    Supports both instant readings and time series data.
    """
    gpu_id: str = Field(..., description="GPU identifier")
    timestamp: datetime = Field(..., description="Power measurement timestamp")

    # Instant power (Watts)
    power_watts: float = Field(..., ge=0, description="Current power draw in watts")

    # Statistics (Watts) - for time series queries
    avg_power_watts: Optional[float] = Field(None, ge=0, description="Average power over period")
    max_power_watts: Optional[float] = Field(None, ge=0, description="Maximum power over period")
    min_power_watts: Optional[float] = Field(None, ge=0, description="Minimum power over period")

    # Energy (Joules)
    total_energy_joules: Optional[float] = Field(None, ge=0, description="Total energy consumption")


class GPUTemperature(BaseModel):
    """
    GPU temperature monitoring with status levels.

    Includes temperature thresholds and alert status.
    """
    gpu_id: str = Field(..., description="GPU identifier")
    hostname: str = Field(..., description="Node hostname")
    timestamp: datetime = Field(..., description="Temperature measurement timestamp")

    # Temperature readings (Celsius)
    gpu_temperature_celsius: Optional[float] = Field(None, description="GPU core temperature")
    memory_temperature_celsius: Optional[float] = Field(None, description="GPU memory temperature")

    # Thresholds and status
    temperature_limit_celsius: Optional[float] = Field(None, description="GPU temperature limit")
    slowdown_threshold_celsius: Optional[float] = Field(None, description="Temperature threshold for slowdown")
    shutdown_threshold_celsius: Optional[float] = Field(None, description="Temperature threshold for shutdown")
    temperature_status: TemperatureStatus = Field(TemperatureStatus.NORMAL, description="Temperature status level")


class GPUSummary(BaseModel):
    """
    Aggregated summary statistics for all GPUs.

    Provides cluster-wide or filtered GPU statistics.
    """
    total_gpus: int = Field(..., ge=0, description="Total number of GPUs")
    active_gpus: int = Field(..., ge=0, description="GPUs with >0% utilization")
    idle_gpus: int = Field(..., ge=0, description="GPUs with 0% utilization")
    error_gpus: int = Field(..., ge=0, description="GPUs in error state")

    # Utilization statistics (percent)
    avg_gpu_utilization_percent: float = Field(..., ge=0, le=100, description="Average GPU utilization")
    max_gpu_utilization_percent: float = Field(..., ge=0, le=100, description="Maximum GPU utilization")
    avg_memory_utilization_percent: Optional[float] = Field(None, ge=0, le=100, description="Average memory utilization")

    # Temperature statistics (Celsius)
    avg_temperature_celsius: float = Field(..., description="Average GPU temperature")
    max_temperature_celsius: float = Field(..., description="Maximum GPU temperature")
    warning_temperature_count: int = Field(0, ge=0, description="GPUs in warning temperature range")
    critical_temperature_count: int = Field(0, ge=0, description="GPUs in critical temperature range")

    # Power statistics (Watts)
    total_power_watts: float = Field(..., ge=0, description="Total power consumption across all GPUs")
    avg_power_watts: float = Field(..., ge=0, description="Average power per GPU")
    max_power_watts: float = Field(..., ge=0, description="Maximum power of any single GPU")

    # Memory statistics (Megabytes)
    total_memory_used_mb: int = Field(..., ge=0, description="Total memory used across all GPUs")
    total_memory_available_mb: int = Field(..., ge=0, description="Total memory available across all GPUs")
    avg_memory_used_percent: Optional[float] = Field(None, ge=0, le=100, description="Average memory usage percentage")


# ============================================================================
# API Response Models
# ============================================================================

class GPUListResponse(BaseModel):
    """Response model for GPU list endpoint (GET /api/v1/accelerators/gpus)."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp")
    cluster: str = Field("default", description="Cluster name")
    total_gpus: int = Field(..., ge=0, description="Total number of GPUs")
    gpus: List[GPUInfo] = Field(..., description="List of GPU information")
    summary: Optional[GPUSummary] = Field(None, description="GPU summary statistics (if requested)")


class GPUDetailResponse(BaseModel):
    """Response model for GPU detail endpoint (GET /api/v1/accelerators/gpus/{gpu_id})."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp")
    gpu: GPUInfo = Field(..., description="Detailed GPU information")
    metrics: Optional[GPUMetrics] = Field(None, description="Current GPU metrics (if requested)")


class GPUMetricsResponse(BaseModel):
    """Response model for GPU metrics endpoint (GET /api/v1/accelerators/gpus/{gpu_id}/metrics)."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp")
    gpu_id: str = Field(..., description="GPU identifier")
    metrics: GPUMetrics = Field(..., description="GPU performance metrics")


class GPUPowerResponse(BaseModel):
    """Response model for GPU power endpoint (GET /api/v1/accelerators/gpus/{gpu_id}/power)."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp")
    gpu_id: str = Field(..., description="GPU identifier")
    period: Optional[str] = Field(None, description="Time period (if time series)")
    power_data: GPUPowerData = Field(..., description="GPU power consumption data")


class GPUTemperatureResponse(BaseModel):
    """Response model for GPU temperature endpoint (GET /api/v1/accelerators/gpus/{gpu_id}/temperature)."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp")
    gpu_id: str = Field(..., description="GPU identifier")
    temperature: GPUTemperature = Field(..., description="GPU temperature data")


class GPUSummaryResponse(BaseModel):
    """Response model for GPU summary endpoint (GET /api/v1/accelerators/gpus/summary)."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp")
    cluster: str = Field("default", description="Cluster name")
    summary: GPUSummary = Field(..., description="GPU summary statistics")
