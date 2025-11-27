"""
Common Query Parameter Models - Reusable query parameters for API endpoints.

This module defines common query parameter models used across multiple endpoints.
"""

from typing import Optional, List
from datetime import datetime
from pydantic import BaseModel, Field, field_validator
from .enums import TimeRange, AggregationMethod, ExportFormat


# ============================================================================
# Base Query Parameters
# ============================================================================

class CommonQueryParams(BaseModel):
    """
    Common query parameters used across many endpoints.

    Provides standard filtering by cluster and metadata.
    """
    cluster: Optional[str] = Field(None, description="Filter by cluster name")
    labels: Optional[str] = Field(None, description="Label selector (e.g., 'app=web,tier=frontend')")


class PaginationParams(BaseModel):
    """
    Pagination query parameters.

    Provides page-based pagination controls.
    """
    page: int = Field(1, ge=1, description="Page number (1-indexed)")
    page_size: int = Field(50, ge=1, le=1000, description="Number of items per page")


class FilterParams(BaseModel):
    """
    Generic filtering parameters.

    Provides common filtering options for list endpoints.
    """
    status: Optional[str] = Field(None, description="Filter by status")
    node: Optional[str] = Field(None, description="Filter by node name")
    namespace: Optional[str] = Field(None, description="Filter by namespace (Kubernetes)")


# ============================================================================
# Time Series Query Parameters
# ============================================================================

class TimeSeriesQueryParams(BaseModel):
    """
    Time series query parameters for metrics over time.

    Supports both predefined time ranges and custom start/end times.
    """
    # Time range options
    period: Optional[TimeRange] = Field(None, description="Predefined time period (1h, 1d, 1w, 1m)")
    start_time: Optional[datetime] = Field(None, description="Custom start time (ISO 8601)")
    end_time: Optional[datetime] = Field(None, description="Custom end time (ISO 8601)")

    # Resolution
    step: Optional[str] = Field("1m", description="Query resolution step (e.g., '1m', '5m', '1h')")

    # Aggregation
    aggregation: Optional[AggregationMethod] = Field(
        AggregationMethod.AVG,
        description="Aggregation method for data points"
    )

    @field_validator('step')
    @classmethod
    def validate_step(cls, v):
        """Validate step format (e.g., '1m', '5m', '1h')."""
        if v is None:
            return v
        valid_units = ['s', 'm', 'h', 'd']
        if len(v) < 2 or v[-1] not in valid_units or not v[:-1].isdigit():
            raise ValueError(f"Invalid step format '{v}'. Expected format: <number><unit> (e.g., '1m', '5m', '1h')")
        return v

    @field_validator('end_time')
    @classmethod
    def validate_end_after_start(cls, v, info):
        """Ensure end_time is after start_time."""
        if v is not None and 'start_time' in info.data and info.data['start_time'] is not None:
            if v <= info.data['start_time']:
                raise ValueError("end_time must be after start_time")
        return v


# ============================================================================
# Export Query Parameters
# ============================================================================

class ExportQueryParams(TimeSeriesQueryParams):
    """
    Export query parameters for data export endpoints.

    Extends time series parameters with format and filtering options.
    """
    format: ExportFormat = Field(ExportFormat.JSON, description="Export format")
    include_metadata: bool = Field(True, description="Include metadata in export")
    resource_type: Optional[str] = Field(None, description="Filter by resource type (gpus, npus, nodes, pods)")
    breakdown_by: Optional[str] = Field(None, description="Breakdown dimension (cluster, node, namespace)")


# ============================================================================
# Power Query Parameters
# ============================================================================

class PowerQueryParams(TimeSeriesQueryParams, FilterParams):
    """
    Power monitoring query parameters.

    Combines time series and filtering parameters for power endpoints.
    """
    include_breakdown: bool = Field(False, description="Include power breakdown by component")
    min_power_watts: Optional[float] = Field(None, ge=0, description="Minimum power threshold in watts")
    max_power_watts: Optional[float] = Field(None, ge=0, description="Maximum power threshold in watts")


# ============================================================================
# Accelerator Query Parameters
# ============================================================================

class AcceleratorQueryParams(CommonQueryParams, FilterParams, PaginationParams):
    """
    Accelerator (GPU/NPU) query parameters.

    Combines common, filtering, and pagination parameters.
    """
    vendor: Optional[str] = Field(None, description="Filter by vendor (nvidia, amd, furiosa, rebellions)")
    include_metrics: bool = Field(False, description="Include current metrics in response")
    min_utilization_percent: Optional[float] = Field(None, ge=0, le=100, description="Minimum utilization threshold")


# ============================================================================
# Infrastructure Query Parameters
# ============================================================================

class NodeQueryParams(CommonQueryParams, FilterParams, PaginationParams):
    """
    Node query parameters.

    Provides node-specific filtering options.
    """
    role: Optional[str] = Field(None, description="Filter by node role (master, worker, gpu_worker)")
    include_metrics: bool = Field(False, description="Include current metrics in response")
    include_power: bool = Field(False, description="Include power data in response")


class PodQueryParams(CommonQueryParams, FilterParams, PaginationParams):
    """
    Pod query parameters.

    Provides pod-specific filtering options.
    """
    label_selector: Optional[str] = Field(None, description="Kubernetes label selector")
    min_power_watts: Optional[float] = Field(None, ge=0, description="Minimum power threshold in watts")
    include_containers: bool = Field(False, description="Include container details in response")


class ContainerQueryParams(CommonQueryParams, FilterParams, PaginationParams):
    """
    Container query parameters.

    Provides container-specific filtering options.
    """
    pod: Optional[str] = Field(None, description="Filter by pod name")
    include_metrics: bool = Field(False, description="Include current metrics in response")


class VMQueryParams(CommonQueryParams, FilterParams, PaginationParams):
    """
    VM (OpenStack) query parameters.

    Provides VM-specific filtering options.
    """
    project: Optional[str] = Field(None, description="Filter by OpenStack project (tenant) ID")
    hypervisor: Optional[str] = Field(None, description="Filter by hypervisor hostname")
    include_metrics: bool = Field(False, description="Include current metrics in response")
    include_power: bool = Field(False, description="Include power data in response")


# ============================================================================
# Hardware Query Parameters
# ============================================================================

class IPMIQueryParams(CommonQueryParams, FilterParams):
    """
    IPMI sensor query parameters.

    Provides IPMI-specific filtering options.
    """
    sensor_type: Optional[str] = Field(None, description="Filter by sensor type (temperature, power, fan, voltage)")
    status: Optional[str] = Field(None, description="Filter by sensor status (normal, warning, critical)")


# ============================================================================
# Monitoring Query Parameters
# ============================================================================

class MonitoringQueryParams(PowerQueryParams):
    """
    Monitoring domain query parameters.

    Extends power parameters for cross-domain monitoring.
    PowerQueryParams already includes TimeSeriesQueryParams and FilterParams.
    """
    resource_types: Optional[List[str]] = Field(None, description="Filter by resource types (gpus, npus, nodes, pods)")
    breakdown_by: Optional[str] = Field(None, description="Breakdown dimension (cluster, node, namespace, vendor)")


# ============================================================================
# Stream Query Parameters
# ============================================================================

class StreamQueryParams(BaseModel):
    """
    Real-time streaming query parameters.

    Provides controls for WebSocket/SSE streaming endpoints.
    """
    interval_seconds: int = Field(5, ge=1, le=60, description="Update interval in seconds")
    resource_type: Optional[str] = Field(None, description="Filter by resource type")
    node: Optional[str] = Field(None, description="Filter by node name")
    namespace: Optional[str] = Field(None, description="Filter by namespace")
