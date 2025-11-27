"""
Pod Data Models - Kubernetes pod monitoring.

This module defines Pydantic models for pod information, metrics, and responses.
Data source: Kepler for pod power data, Kubernetes API for pod metadata.
All field names follow unit-explicit naming convention.
"""

from typing import List, Optional, Dict
from datetime import datetime
from pydantic import BaseModel, Field
from enum import Enum


class PodStatus(str, Enum):
    """Pod operational status."""
    RUNNING = "running"
    PENDING = "pending"
    SUCCEEDED = "succeeded"
    FAILED = "failed"
    UNKNOWN = "unknown"


class PodPhase(str, Enum):
    """Pod lifecycle phase."""
    PENDING = "Pending"
    RUNNING = "Running"
    SUCCEEDED = "Succeeded"
    FAILED = "Failed"
    UNKNOWN = "Unknown"


# ============================================================================
# Core Pod Models
# ============================================================================

class PodInfo(BaseModel):
    """
    Detailed pod information.

    Data source: Kubernetes API for pod metadata, Kepler for power data.
    """
    # Identifiers
    pod_name: str = Field(..., description="Pod name")
    namespace: str = Field(..., description="Kubernetes namespace")
    uid: Optional[str] = Field(None, description="Pod UID")
    cluster: str = Field("default", description="Cluster name")

    # Pod metadata
    node_name: Optional[str] = Field(None, description="Node where pod is running")
    status: PodStatus = Field(PodStatus.RUNNING, description="Pod status")
    phase: PodPhase = Field(PodPhase.RUNNING, description="Pod lifecycle phase")

    # Container information
    container_count: int = Field(0, ge=0, description="Number of containers in this pod")
    container_names: Optional[List[str]] = Field(None, description="List of container names")

    # Resource allocation
    cpu_request: Optional[str] = Field(None, description="CPU request (e.g., '100m', '1')")
    cpu_limit: Optional[str] = Field(None, description="CPU limit (e.g., '1', '2')")
    memory_request_mb: Optional[int] = Field(None, ge=0, description="Memory request in megabytes")
    memory_limit_mb: Optional[int] = Field(None, ge=0, description="Memory limit in megabytes")

    # GPU/NPU allocation
    gpu_count: int = Field(0, ge=0, description="Number of GPUs allocated to this pod")
    npu_count: int = Field(0, ge=0, description="Number of NPUs allocated to this pod")

    # Labels and annotations
    labels: Optional[Dict[str, str]] = Field(None, description="Pod labels")
    annotations: Optional[Dict[str, str]] = Field(None, description="Pod annotations")

    # Workload information
    workload_type: Optional[str] = Field(None, description="Workload type (Deployment, StatefulSet, etc.)")
    workload_name: Optional[str] = Field(None, description="Owner workload name")

    # Timestamps
    created_at: Optional[datetime] = Field(None, description="Pod creation timestamp")
    started_at: Optional[datetime] = Field(None, description="Pod start timestamp")

    # Observability
    current_power_watts: Optional[float] = Field(None, ge=0, description="Current pod power draw in watts")
    cpu_usage_millicores: Optional[int] = Field(None, ge=0, description="Approximate CPU usage in millicores")
    memory_used_mb: Optional[int] = Field(None, ge=0, description="Approximate memory usage in megabytes")


class PodContainerDetail(BaseModel):
    """Container-level detail embedded in pod responses."""
    name: str = Field(..., description="Container name")
    image: Optional[str] = Field(None, description="Container image")
    status: Optional[str] = Field(None, description="Container status (running/waiting/terminated)")
    cpu_request: Optional[str] = Field(None, description="CPU request (e.g., '500m')")
    cpu_limit: Optional[str] = Field(None, description="CPU limit (e.g., '1')")
    memory_request_mb: Optional[int] = Field(None, ge=0, description="Memory request in megabytes")
    memory_limit_mb: Optional[int] = Field(None, ge=0, description="Memory limit in megabytes")
    gpu_request: Optional[int] = Field(None, ge=0, description="GPU request count")
    restarts: Optional[int] = Field(None, ge=0, description="Container restart count")


class PodPowerSample(BaseModel):
    """Time series sample for pod power data."""
    timestamp: datetime = Field(..., description="Sample timestamp")
    power_watts: float = Field(..., ge=0, description="Power consumption in watts")


class PodPowerCurrent(BaseModel):
    """Current pod power breakdown."""
    total_power_watts: float = Field(..., ge=0, description="Total pod power consumption in watts")
    cpu_power_watts: Optional[float] = Field(None, ge=0, description="CPU power consumption in watts")
    dram_power_watts: Optional[float] = Field(None, ge=0, description="DRAM power consumption in watts")
    gpu_power_watts: Optional[float] = Field(None, ge=0, description="GPU power consumption in watts")
    container_power_watts: Optional[Dict[str, float]] = Field(None, description="Power by container (watts)")


class PodPowerStatistics(BaseModel):
    """Aggregate statistics for pod power consumption over a period."""
    avg_power_watts: Optional[float] = Field(None, ge=0, description="Average power over the selected period")
    max_power_watts: Optional[float] = Field(None, ge=0, description="Maximum power over the selected period")
    min_power_watts: Optional[float] = Field(None, ge=0, description="Minimum power over the selected period")
    total_energy_kwh: Optional[float] = Field(None, ge=0, description="Total energy consumption in kilowatt-hours")
    runtime_hours: Optional[float] = Field(None, ge=0, description="Active runtime covered by the period in hours")


class PodPowerData(BaseModel):
    """
    Pod power consumption data from Kepler.

    Provides power breakdown by container and resource type.
    """
    pod_name: str = Field(..., description="Pod name")
    namespace: str = Field(..., description="Kubernetes namespace")
    period: Optional[str] = Field(None, description="Requested time period (1h/1d/1w/1m)")
    start_time: datetime = Field(..., description="Time window start")
    end_time: datetime = Field(..., description="Time window end")
    current: PodPowerCurrent = Field(..., description="Current pod power data")
    statistics: PodPowerStatistics = Field(..., description="Aggregated statistics for the period")
    timeseries: Optional[List[PodPowerSample]] = Field(None, description="Power time series for the period")


class PodMetrics(BaseModel):
    """
    Real-time pod resource metrics.

    Data source: Kubernetes metrics API.
    All numeric fields follow unit-explicit naming.
    """
    pod_name: str = Field(..., description="Pod name")
    namespace: str = Field(..., description="Kubernetes namespace")
    timestamp: datetime = Field(..., description="Metric collection timestamp")

    # CPU metrics (millicores and percent)
    cpu_usage_millicores: Optional[int] = Field(None, ge=0, description="CPU usage in millicores")
    cpu_utilization_percent: Optional[float] = Field(None, ge=0, le=100, description="CPU utilization percentage")

    # Memory metrics (Megabytes and percent)
    memory_used_mb: Optional[int] = Field(None, ge=0, description="Used memory in megabytes")
    memory_working_set_mb: Optional[int] = Field(None, ge=0, description="Memory working set in megabytes")
    memory_utilization_percent: Optional[float] = Field(None, ge=0, le=100, description="Memory utilization percentage")

    # Network metrics (Megabits per second)
    network_rx_mbps: Optional[float] = Field(None, ge=0, description="Network receive rate in Mbps")
    network_tx_mbps: Optional[float] = Field(None, ge=0, description="Network transmit rate in Mbps")

    # Filesystem metrics (Megabytes)
    fs_used_mb: Optional[int] = Field(None, ge=0, description="Filesystem usage in megabytes")

    # Container metrics
    container_count: int = Field(0, ge=0, description="Number of containers")
    ready_containers: int = Field(0, ge=0, description="Number of ready containers")
    restarts: int = Field(0, ge=0, description="Total container restart count")


# ============================================================================
# API Response Models
# ============================================================================

class PodListResponse(BaseModel):
    """Response model for pod list endpoint (GET /api/v1/infrastructure/pods)."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp")
    cluster: str = Field("default", description="Cluster name")
    namespace_filter: Optional[str] = Field(None, description="Namespace filter applied")
    node_filter: Optional[str] = Field(None, description="Node filter applied")
    total_pods: int = Field(..., ge=0, description="Total number of pods")
    pods: List[PodInfo] = Field(..., description="List of pod information")

    # Summary statistics
    total_power_watts: Optional[float] = Field(None, ge=0, description="Total power across all pods")
    namespaces_summary: Optional[Dict[str, int]] = Field(None, description="Pod count by namespace")
    pods_by_status: Optional[Dict[str, int]] = Field(None, description="Pod counts by status")


class PodDetailResponse(BaseModel):
    """Response model for pod detail endpoint (GET /api/v1/infrastructure/pods/{namespace}/{pod_name})."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp")
    pod: PodInfo = Field(..., description="Detailed pod information")
    metrics: Optional[PodMetrics] = Field(None, description="Current pod metrics (if requested)")
    power: Optional[PodPowerData] = Field(None, description="Current pod power data (if requested)")
    containers: Optional[List[PodContainerDetail]] = Field(None, description="Container details")


class PodPowerResponse(BaseModel):
    """Response model for pod power endpoint (GET /api/v1/infrastructure/pods/{namespace}/{pod_name}/power)."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp")
    pod_name: str = Field(..., description="Pod name")
    namespace: str = Field(..., description="Kubernetes namespace")
    period: Optional[str] = Field(None, description="Time period (if time series)")
    power_data: PodPowerData = Field(..., description="Pod power consumption data")


class PodSummary(BaseModel):
    """Aggregated summary information for pods."""
    total_pods: int = Field(..., ge=0, description="Total number of pods")
    namespaces: int = Field(..., ge=0, description="Number of namespaces with pods")
    running_pods: int = Field(..., ge=0, description="Number of running pods")
    pending_pods: int = Field(..., ge=0, description="Number of pending pods")
    failed_pods: int = Field(..., ge=0, description="Number of failed pods")
    total_power_watts: Optional[float] = Field(None, ge=0, description="Total power consumption across pods")
    avg_power_per_pod_watts: Optional[float] = Field(None, ge=0, description="Average power per pod in watts")
    top_pods_by_power: Optional[List[PodInfo]] = Field(None, description="Top pods ordered by power consumption")


class PodSummaryResponse(BaseModel):
    """Response model for pod summary endpoint (GET /api/v1/infrastructure/pods/summary)."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp")
    cluster: str = Field("default", description="Cluster name")
    namespace_filter: Optional[str] = Field(None, description="Namespace filter applied")
    summary: PodSummary = Field(..., description="Aggregated pod summary statistics")
