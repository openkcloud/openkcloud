"""
Node Data Models - Kubernetes and physical node monitoring.

This module defines Pydantic models for node information, metrics, and responses.
Data source: Kepler for power data, Kubernetes API for node information.
All field names follow unit-explicit naming convention.
"""

from typing import List, Optional, Dict
from datetime import datetime
from pydantic import BaseModel, Field
from enum import Enum


class NodeStatus(str, Enum):
    """Node operational status."""
    READY = "ready"
    NOT_READY = "not_ready"
    UNKNOWN = "unknown"
    MAINTENANCE = "maintenance"


class NodeRole(str, Enum):
    """Node role in cluster."""
    MASTER = "master"
    WORKER = "worker"
    GPU_WORKER = "gpu_worker"
    NPU_WORKER = "npu_worker"


# ============================================================================
# Core Node Models
# ============================================================================

class NodeInfo(BaseModel):
    """
    Detailed node information.

    Data source: Kubernetes API for node metadata, Kepler for power data.
    """
    # Identifiers
    node_name: str = Field(..., description="Node name")
    instance: str = Field(..., description="Instance identifier (IP:port)")
    cluster: str = Field("default", description="Cluster name")

    # Node metadata
    hostname: str = Field(..., description="Node hostname")
    role: NodeRole = Field(NodeRole.WORKER, description="Node role in cluster")
    status: NodeStatus = Field(NodeStatus.READY, description="Node operational status")

    # Hardware information
    cpu_architecture: Optional[str] = Field(None, description="CPU architecture (e.g., amd64, arm64)")
    kernel_version: Optional[str] = Field(None, description="Kernel version")
    os_image: Optional[str] = Field(None, description="Operating system image")
    container_runtime: Optional[str] = Field(None, description="Container runtime (e.g., containerd, docker)")

    # Capacity information
    cpu_cores: Optional[float] = Field(None, ge=0, description="Total CPU cores")
    memory_total_mb: Optional[int] = Field(None, ge=0, description="Total memory in megabytes")
    gpu_count: int = Field(0, ge=0, description="Number of GPUs on this node")
    npu_count: int = Field(0, ge=0, description="Number of NPUs on this node")

    # Allocatable resources
    allocatable_cpu_cores: Optional[float] = Field(None, ge=0, description="Allocatable CPU cores")
    allocatable_memory_mb: Optional[int] = Field(None, ge=0, description="Allocatable memory in megabytes")
    allocatable_gpu_count: Optional[int] = Field(None, ge=0, description="Allocatable GPU count")

    # Power information
    current_power_watts: Optional[float] = Field(None, ge=0, description="Current power consumption in watts")
    power_source: Optional[str] = Field(None, description="Power measurement source (e.g., rapl, acpi)")

    # Labels and annotations
    labels: Optional[Dict[str, str]] = Field(None, description="Node labels")
    annotations: Optional[Dict[str, str]] = Field(None, description="Node annotations")


class NodePowerSample(BaseModel):
    """Time series sample for node power data."""
    timestamp: datetime = Field(..., description="Sample timestamp")
    power_watts: float = Field(..., ge=0, description="Power consumption in watts")


class NodePowerCurrent(BaseModel):
    """Current node power breakdown."""
    total_power_watts: float = Field(..., ge=0, description="Total node power consumption in watts")
    cpu_power_watts: Optional[float] = Field(None, ge=0, description="CPU power consumption in watts")
    dram_power_watts: Optional[float] = Field(None, ge=0, description="DRAM power consumption in watts")
    gpu_power_watts: Optional[float] = Field(None, ge=0, description="GPU power consumption in watts")
    other_power_watts: Optional[float] = Field(None, ge=0, description="Other component power consumption in watts")


class NodePowerStatistics(BaseModel):
    """Aggregate statistics for node power consumption over a period."""
    avg_power_watts: Optional[float] = Field(None, ge=0, description="Average power over the selected period")
    max_power_watts: Optional[float] = Field(None, ge=0, description="Maximum power over the selected period")
    min_power_watts: Optional[float] = Field(None, ge=0, description="Minimum power over the selected period")
    total_energy_kwh: Optional[float] = Field(None, ge=0, description="Total energy consumption in kilowatt-hours")


class NodePowerData(BaseModel):
    """
    Node power consumption data from Kepler.

    Provides power breakdown, statistics, and optional timeseries.
    """
    node_name: str = Field(..., description="Node name")
    period: Optional[str] = Field(None, description="Requested time period (1h/1d/1w/1m)")
    start_time: datetime = Field(..., description="Start time for the aggregated window")
    end_time: datetime = Field(..., description="End time for the aggregated window")
    current: NodePowerCurrent = Field(..., description="Current power measurements")
    statistics: NodePowerStatistics = Field(..., description="Aggregated power statistics")
    timeseries: Optional[List[NodePowerSample]] = Field(None, description="Power time series data")


class NodeMetrics(BaseModel):
    """
    Real-time node resource metrics.

    Data source: Kubernetes metrics API or node-exporter.
    All numeric fields follow unit-explicit naming.
    """
    node_name: str = Field(..., description="Node name")
    timestamp: datetime = Field(..., description="Metric collection timestamp")

    # CPU metrics (percent)
    cpu_utilization_percent: Optional[float] = Field(None, ge=0, le=100, description="CPU utilization percentage")
    cpu_load_1min: Optional[float] = Field(None, ge=0, description="1-minute CPU load average")
    cpu_load_5min: Optional[float] = Field(None, ge=0, description="5-minute CPU load average")
    cpu_load_15min: Optional[float] = Field(None, ge=0, description="15-minute CPU load average")

    # Memory metrics (Megabytes and percent)
    memory_total_mb: Optional[int] = Field(None, ge=0, description="Total memory in megabytes")
    memory_used_mb: Optional[int] = Field(None, ge=0, description="Used memory in megabytes")
    memory_available_mb: Optional[int] = Field(None, ge=0, description="Available memory in megabytes")
    memory_utilization_percent: Optional[float] = Field(None, ge=0, le=100, description="Memory utilization percentage")

    # Disk metrics (Megabytes and percent)
    disk_total_mb: Optional[int] = Field(None, ge=0, description="Total disk capacity in megabytes")
    disk_used_mb: Optional[int] = Field(None, ge=0, description="Used disk space in megabytes")
    disk_available_mb: Optional[int] = Field(None, ge=0, description="Available disk space in megabytes")
    disk_utilization_percent: Optional[float] = Field(None, ge=0, le=100, description="Disk utilization percentage")

    # Network metrics (Megabits per second)
    network_rx_mbps: Optional[float] = Field(None, ge=0, description="Network receive rate in Mbps")
    network_tx_mbps: Optional[float] = Field(None, ge=0, description="Network transmit rate in Mbps")

    # Pod/Container counts
    pod_count: int = Field(0, ge=0, description="Number of pods running on this node")
    container_count: int = Field(0, ge=0, description="Number of containers running on this node")


# ============================================================================
# API Response Models
# ============================================================================

class NodeListResponse(BaseModel):
    """Response model for node list endpoint (GET /api/v1/infrastructure/nodes)."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp")
    cluster: str = Field("default", description="Cluster name")
    total_nodes: int = Field(..., ge=0, description="Total number of nodes")
    nodes: List[NodeInfo] = Field(..., description="List of node information")


class NodeDetailResponse(BaseModel):
    """Response model for node detail endpoint (GET /api/v1/infrastructure/nodes/{node_name})."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp")
    node: NodeInfo = Field(..., description="Detailed node information")
    metrics: Optional[NodeMetrics] = Field(None, description="Current node metrics (if requested)")
    power: Optional[NodePowerData] = Field(None, description="Current node power data (if requested)")


class NodePowerResponse(BaseModel):
    """Response model for node power endpoint (GET /api/v1/infrastructure/nodes/{node_name}/power)."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp")
    node_name: str = Field(..., description="Node name")
    period: Optional[str] = Field(None, description="Time period (if time series)")
    power_data: NodePowerData = Field(..., description="Node power consumption data")


class NodeMetricsResponse(BaseModel):
    """Response model for node metrics endpoint (GET /api/v1/infrastructure/nodes/{node_name}/metrics)."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp")
    node_name: str = Field(..., description="Node name")
    metrics: NodeMetrics = Field(..., description="Node resource metrics")


class NodeSummary(BaseModel):
    """Aggregated summary information for nodes."""
    total_nodes: int = Field(..., ge=0, description="Total number of nodes")
    ready_nodes: int = Field(..., ge=0, description="Number of Ready nodes")
    not_ready_nodes: int = Field(..., ge=0, description="Number of NotReady nodes")
    unknown_nodes: int = Field(..., ge=0, description="Number of nodes with unknown status")
    total_gpus: int = Field(..., ge=0, description="Total GPU count across nodes")
    total_npus: int = Field(..., ge=0, description="Total NPU count across nodes")
    total_power_watts: float = Field(..., ge=0, description="Cluster-wide node power consumption in watts")
    avg_power_per_node_watts: float = Field(..., ge=0, description="Average power draw per node")
    nodes_by_role: Dict[str, int] = Field(..., description="Node counts by role")


class NodeSummaryResponse(BaseModel):
    """Response model for node summary endpoint (GET /api/v1/infrastructure/nodes/summary)."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp")
    cluster: str = Field("default", description="Cluster name")
    summary: NodeSummary = Field(..., description="Aggregated node summary statistics")
    top_nodes_by_power: Optional[List[NodeInfo]] = Field(
        None,
        description="Top nodes sorted by current power consumption"
    )
