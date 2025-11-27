"""
Container Data Models - Container-level monitoring.

This module defines Pydantic models for container information and metrics.
Data source: Kepler for container power data, Kubernetes API for container metadata.
All field names follow unit-explicit naming convention.
"""

from typing import List, Optional, Dict
from datetime import datetime
from pydantic import BaseModel, Field
from enum import Enum


class ContainerStatus(str, Enum):
    """Container operational status."""
    RUNNING = "running"
    WAITING = "waiting"
    TERMINATED = "terminated"
    UNKNOWN = "unknown"


class ContainerRestartPolicy(str, Enum):
    """Container restart policy."""
    ALWAYS = "Always"
    ON_FAILURE = "OnFailure"
    NEVER = "Never"


# ============================================================================
# Core Container Models
# ============================================================================

class ContainerInfo(BaseModel):
    """
    Detailed container information.

    Data source: Kubernetes API for container metadata.
    """
    # Identifiers
    container_id: str = Field(..., description="Container ID")
    container_name: str = Field(..., description="Container name")
    pod_name: str = Field(..., description="Pod name")
    namespace: str = Field(..., description="Kubernetes namespace")
    cluster: str = Field("default", description="Cluster name")

    # Container metadata
    image: str = Field(..., description="Container image")
    image_id: Optional[str] = Field(None, description="Container image ID")
    node_name: Optional[str] = Field(None, description="Node where container is running")
    status: ContainerStatus = Field(ContainerStatus.RUNNING, description="Container status")

    # Resource allocation
    cpu_request: Optional[str] = Field(None, description="CPU request (e.g., '100m', '1')")
    cpu_limit: Optional[str] = Field(None, description="CPU limit (e.g., '1', '2')")
    memory_request_mb: Optional[int] = Field(None, ge=0, description="Memory request in megabytes")
    memory_limit_mb: Optional[int] = Field(None, ge=0, description="Memory limit in megabytes")

    # Container configuration
    restart_policy: Optional[ContainerRestartPolicy] = Field(None, description="Restart policy")
    restart_count: int = Field(0, ge=0, description="Container restart count")

    # Timestamps
    created_at: Optional[datetime] = Field(None, description="Container creation timestamp")
    started_at: Optional[datetime] = Field(None, description="Container start timestamp")
    finished_at: Optional[datetime] = Field(None, description="Container finish timestamp (if terminated)")
    current_power_watts: Optional[float] = Field(None, ge=0, description="Current power draw in watts")


class ContainerMetrics(BaseModel):
    """
    Real-time container resource metrics.

    Data source: Kubernetes metrics API, Kepler for power data.
    All numeric fields follow unit-explicit naming.
    """
    container_id: str = Field(..., description="Container ID")
    container_name: str = Field(..., description="Container name")
    timestamp: datetime = Field(..., description="Metric collection timestamp")

    # CPU metrics (millicores and percent)
    cpu_usage_millicores: Optional[int] = Field(None, ge=0, description="CPU usage in millicores")
    cpu_utilization_percent: Optional[float] = Field(None, ge=0, le=100, description="CPU utilization percentage")

    # Memory metrics (Megabytes and percent)
    memory_used_mb: Optional[int] = Field(None, ge=0, description="Used memory in megabytes")
    memory_working_set_mb: Optional[int] = Field(None, ge=0, description="Memory working set in megabytes")
    memory_rss_mb: Optional[int] = Field(None, ge=0, description="Resident Set Size in megabytes")
    memory_cache_mb: Optional[int] = Field(None, ge=0, description="Cache memory in megabytes")
    memory_utilization_percent: Optional[float] = Field(None, ge=0, le=100, description="Memory utilization percentage")

    # Filesystem metrics (Megabytes)
    fs_reads_mb: Optional[int] = Field(None, ge=0, description="Filesystem reads in megabytes")
    fs_writes_mb: Optional[int] = Field(None, ge=0, description="Filesystem writes in megabytes")
    fs_used_mb: Optional[int] = Field(None, ge=0, description="Filesystem usage in megabytes")

    # Network metrics (Megabits per second)
    network_rx_mbps: Optional[float] = Field(None, ge=0, description="Network receive rate in Mbps")
    network_tx_mbps: Optional[float] = Field(None, ge=0, description="Network transmit rate in Mbps")

    # Power metrics (Watts) - from Kepler
    power_watts: Optional[float] = Field(None, ge=0, description="Container power consumption in watts")
    cpu_power_watts: Optional[float] = Field(None, ge=0, description="CPU power consumption in watts")
    dram_power_watts: Optional[float] = Field(None, ge=0, description="DRAM power consumption in watts")


# ============================================================================
# API Response Models
# ============================================================================

class ContainerListResponse(BaseModel):
    """Response model for container list endpoint (GET /api/v1/infrastructure/containers)."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp")
    cluster: str = Field("default", description="Cluster name")
    pod_filter: Optional[str] = Field(None, description="Pod filter applied")
    namespace_filter: Optional[str] = Field(None, description="Namespace filter applied")
    total_containers: int = Field(..., ge=0, description="Total number of containers")
    containers: List[ContainerInfo] = Field(..., description="List of container information")


class ContainerDetailResponse(BaseModel):
    """Response model for container detail endpoint (GET /api/v1/infrastructure/containers/{container_id})."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp")
    container: ContainerInfo = Field(..., description="Detailed container information")
    metrics: Optional[ContainerMetrics] = Field(None, description="Current container metrics (if requested)")


class ContainerMetricsResponse(BaseModel):
    """Response model for container metrics endpoint (GET /api/v1/infrastructure/containers/{container_id}/metrics)."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp")
    container_id: str = Field(..., description="Container identifier")
    metrics: ContainerMetrics = Field(..., description="Container resource metrics")
