"""
Accelerators Domain Models - GPU and NPU data models.

This module contains data models for AI accelerators including:
- GPU models (NVIDIA GPUs via DCGM)
- NPU models (Furiosa AI, Rebellions)
- Common accelerator models
"""

from .gpu import (
    GPUInfo,
    GPUMetrics,
    GPUPowerData,
    GPUTemperature,
    GPUSummary,
    GPUListResponse,
    GPUDetailResponse,
    GPUMetricsResponse,
    GPUPowerResponse,
    GPUTemperatureResponse,
    GPUSummaryResponse
)

from .npu import (
    NPUInfo,
    NPUMetrics,
    NPUCoreStatus,
    NPUPowerData,
    NPUSummary,
    NPUListResponse,
    NPUDetailResponse,
    NPUMetricsResponse,
    NPUSummaryResponse
)

from .common import (
    AcceleratorSummary,
    AcceleratorType,
    AcceleratorStatus
)

__all__ = [
    # GPU models
    "GPUInfo",
    "GPUMetrics",
    "GPUPowerData",
    "GPUTemperature",
    "GPUSummary",
    "GPUListResponse",
    "GPUDetailResponse",
    "GPUMetricsResponse",
    "GPUPowerResponse",
    "GPUTemperatureResponse",
    "GPUSummaryResponse",

    # NPU models
    "NPUInfo",
    "NPUMetrics",
    "NPUCoreStatus",
    "NPUPowerData",
    "NPUSummary",
    "NPUListResponse",
    "NPUDetailResponse",
    "NPUMetricsResponse",
    "NPUSummaryResponse",

    # Common accelerator models
    "AcceleratorSummary",
    "AcceleratorType",
    "AcceleratorStatus"
]
