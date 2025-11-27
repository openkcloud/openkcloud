"""
Common Accelerator Models - Shared models for GPU and NPU accelerators.

This module defines common models and enumerations used across all accelerator types.
"""

from typing import Optional
from datetime import datetime
from pydantic import BaseModel, Field
from enum import Enum


class AcceleratorType(str, Enum):
    """Type of AI accelerator."""
    GPU = "gpu"
    NPU = "npu"
    TPU = "tpu"
    FPGA = "fpga"
    ASIC = "asic"
    OTHER = "other"


class AcceleratorStatus(str, Enum):
    """Operational status of accelerator."""
    ACTIVE = "active"
    IDLE = "idle"
    ERROR = "error"
    OFFLINE = "offline"
    MAINTENANCE = "maintenance"


class AcceleratorSummary(BaseModel):
    """
    Unified summary statistics for all accelerators (GPU + NPU + others).

    Provides cluster-wide accelerator statistics across all types.
    """
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Summary timestamp")
    cluster: str = Field("default", description="Cluster name")

    # Total counts by type
    total_accelerators: int = Field(..., ge=0, description="Total number of all accelerators")
    total_gpus: int = Field(0, ge=0, description="Total number of GPUs")
    total_npus: int = Field(0, ge=0, description="Total number of NPUs")
    total_other: int = Field(0, ge=0, description="Total number of other accelerators")

    # Status breakdown
    active_accelerators: int = Field(..., ge=0, description="Accelerators with >0% utilization")
    idle_accelerators: int = Field(..., ge=0, description="Accelerators with 0% utilization")
    error_accelerators: int = Field(..., ge=0, description="Accelerators in error state")

    # Utilization statistics (percent)
    avg_utilization_percent: float = Field(..., ge=0, le=100, description="Average utilization across all accelerators")
    max_utilization_percent: float = Field(..., ge=0, le=100, description="Maximum utilization of any single accelerator")

    # Temperature statistics (Celsius)
    avg_temperature_celsius: float = Field(..., description="Average temperature across all accelerators")
    max_temperature_celsius: float = Field(..., description="Maximum temperature of any single accelerator")
    warning_temperature_count: int = Field(0, ge=0, description="Accelerators in warning temperature range")
    critical_temperature_count: int = Field(0, ge=0, description="Accelerators in critical temperature range")

    # Power statistics (Watts)
    total_power_watts: float = Field(..., ge=0, description="Total power consumption across all accelerators")
    avg_power_watts: float = Field(..., ge=0, description="Average power per accelerator")
    max_power_watts: float = Field(..., ge=0, description="Maximum power of any single accelerator")

    # Power breakdown by type (Watts)
    gpu_power_watts: float = Field(0, ge=0, description="Total GPU power consumption")
    npu_power_watts: float = Field(0, ge=0, description="Total NPU power consumption")
    other_power_watts: float = Field(0, ge=0, description="Total other accelerator power consumption")

    # Memory statistics (Megabytes)
    total_memory_used_mb: int = Field(..., ge=0, description="Total memory used across all accelerators")
    total_memory_available_mb: int = Field(..., ge=0, description="Total memory available across all accelerators")
    avg_memory_used_percent: Optional[float] = Field(None, ge=0, le=100, description="Average memory usage percentage")
