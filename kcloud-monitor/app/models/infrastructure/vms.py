"""
VM Data Models - OpenStack virtual machine monitoring.

This module defines Pydantic models for VM information, metrics, and responses.
Data source: OpenStack API for VM metadata, Telegraf/libvirt for VM metrics.
All field names follow unit-explicit naming convention.
"""

from typing import List, Optional, Dict
from datetime import datetime
from pydantic import BaseModel, Field
from enum import Enum


class VMStatus(str, Enum):
    """VM operational status."""
    ACTIVE = "active"
    BUILD = "build"
    SHUTOFF = "shutoff"
    SUSPENDED = "suspended"
    ERROR = "error"
    UNKNOWN = "unknown"


class VMPowerState(str, Enum):
    """VM power state."""
    RUNNING = "running"
    PAUSED = "paused"
    SHUTDOWN = "shutdown"
    CRASHED = "crashed"
    SUSPENDED = "suspended"


# ============================================================================
# Core VM Models
# ============================================================================

class VMInfo(BaseModel):
    """
    Detailed VM information.

    Data source: OpenStack API for VM metadata.
    """
    # Identifiers
    vm_id: str = Field(..., description="VM UUID")
    vm_name: str = Field(..., description="VM name")
    cluster: str = Field("default", description="Cluster name")

    # OpenStack metadata
    project_id: str = Field(..., description="OpenStack project (tenant) ID")
    project_name: Optional[str] = Field(None, description="OpenStack project name")
    user_id: Optional[str] = Field(None, description="VM owner user ID")

    # VM status
    status: VMStatus = Field(VMStatus.ACTIVE, description="VM status")
    power_state: VMPowerState = Field(VMPowerState.RUNNING, description="VM power state")
    task_state: Optional[str] = Field(None, description="Current task state")

    # Hypervisor information
    hypervisor_hostname: Optional[str] = Field(None, description="Hypervisor hostname")
    availability_zone: Optional[str] = Field(None, description="Availability zone")

    # Flavor (VM size)
    flavor_id: str = Field(..., description="Flavor ID")
    flavor_name: Optional[str] = Field(None, description="Flavor name")
    vcpus: int = Field(..., ge=0, description="Number of virtual CPUs")
    memory_mb: int = Field(..., ge=0, description="Allocated memory in megabytes")
    disk_gb: int = Field(..., ge=0, description="Root disk size in gigabytes")
    ephemeral_disk_gb: int = Field(0, ge=0, description="Ephemeral disk size in gigabytes")

    # Image information
    image_id: Optional[str] = Field(None, description="Image ID")
    image_name: Optional[str] = Field(None, description="Image name")

    # Network information
    ip_addresses: Optional[List[str]] = Field(None, description="List of IP addresses")
    networks: Optional[Dict[str, List[str]]] = Field(None, description="Networks mapping (network_name -> [IPs])")

    # Metadata
    metadata: Optional[Dict[str, str]] = Field(None, description="VM metadata key-value pairs")

    # Timestamps
    created_at: Optional[datetime] = Field(None, description="VM creation timestamp")
    updated_at: Optional[datetime] = Field(None, description="VM last update timestamp")
    launched_at: Optional[datetime] = Field(None, description="VM launch timestamp")


class VMPowerData(BaseModel):
    """
    VM power consumption data.

    Note: VM power is often estimated based on resource usage.
    Data source: Telegraf/libvirt metrics or estimation algorithm.
    """
    vm_id: str = Field(..., description="VM UUID")
    vm_name: str = Field(..., description="VM name")
    timestamp: datetime = Field(..., description="Power measurement timestamp")

    # Total power (Watts) - estimated
    total_power_watts: float = Field(..., ge=0, description="Total VM power consumption in watts (estimated)")

    # Component breakdown (Watts) - estimated
    cpu_power_watts: Optional[float] = Field(None, ge=0, description="CPU power consumption in watts")
    memory_power_watts: Optional[float] = Field(None, ge=0, description="Memory power consumption in watts")

    # Statistics (Watts) - for time series queries
    avg_power_watts: Optional[float] = Field(None, ge=0, description="Average power over period")
    max_power_watts: Optional[float] = Field(None, ge=0, description="Maximum power over period")
    min_power_watts: Optional[float] = Field(None, ge=0, description="Minimum power over period")

    # Energy (Joules)
    total_energy_joules: Optional[float] = Field(None, ge=0, description="Total energy consumption")

    # Estimation method
    estimation_method: Optional[str] = Field(None, description="Power estimation method (cpu_util, model_based, etc.)")


class VMMetrics(BaseModel):
    """
    Real-time VM resource metrics.

    Data source: libvirt or OpenStack metrics.
    All numeric fields follow unit-explicit naming.
    """
    vm_id: str = Field(..., description="VM UUID")
    vm_name: str = Field(..., description="VM name")
    timestamp: datetime = Field(..., description="Metric collection timestamp")

    # CPU metrics (percent)
    cpu_utilization_percent: Optional[float] = Field(None, ge=0, le=100, description="CPU utilization percentage")
    cpu_time_seconds: Optional[float] = Field(None, ge=0, description="Cumulative CPU time in seconds")

    # Memory metrics (Megabytes and percent)
    memory_used_mb: Optional[int] = Field(None, ge=0, description="Used memory in megabytes")
    memory_available_mb: Optional[int] = Field(None, ge=0, description="Available memory in megabytes")
    memory_utilization_percent: Optional[float] = Field(None, ge=0, le=100, description="Memory utilization percentage")
    memory_rss_mb: Optional[int] = Field(None, ge=0, description="Resident Set Size in megabytes")

    # Disk metrics (Megabytes and IOPS)
    disk_read_mb: Optional[int] = Field(None, ge=0, description="Cumulative disk reads in megabytes")
    disk_write_mb: Optional[int] = Field(None, ge=0, description="Cumulative disk writes in megabytes")
    disk_read_iops: Optional[float] = Field(None, ge=0, description="Disk read IOPS")
    disk_write_iops: Optional[float] = Field(None, ge=0, description="Disk write IOPS")
    disk_used_gb: Optional[int] = Field(None, ge=0, description="Disk space used in gigabytes")

    # Network metrics (Megabits per second and packets)
    network_rx_mbps: Optional[float] = Field(None, ge=0, description="Network receive rate in Mbps")
    network_tx_mbps: Optional[float] = Field(None, ge=0, description="Network transmit rate in Mbps")
    network_rx_packets: Optional[int] = Field(None, ge=0, description="Cumulative received packets")
    network_tx_packets: Optional[int] = Field(None, ge=0, description="Cumulative transmitted packets")


# ============================================================================
# API Response Models
# ============================================================================

class VMListResponse(BaseModel):
    """Response model for VM list endpoint (GET /api/v1/infrastructure/vms)."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp")
    cluster: str = Field("default", description="Cluster name")
    project_filter: Optional[str] = Field(None, description="Project filter applied")
    status_filter: Optional[str] = Field(None, description="Status filter applied")
    total_vms: int = Field(..., ge=0, description="Total number of VMs")
    vms: List[VMInfo] = Field(..., description="List of VM information")

    # Summary statistics
    total_vcpus: Optional[int] = Field(None, ge=0, description="Total vCPUs allocated")
    total_memory_mb: Optional[int] = Field(None, ge=0, description="Total memory allocated in MB")
    total_disk_gb: Optional[int] = Field(None, ge=0, description="Total disk allocated in GB")


class VMDetailResponse(BaseModel):
    """Response model for VM detail endpoint (GET /api/v1/infrastructure/vms/{vm_id})."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp")
    vm: VMInfo = Field(..., description="Detailed VM information")
    metrics: Optional[VMMetrics] = Field(None, description="Current VM metrics (if requested)")
    power: Optional[VMPowerData] = Field(None, description="Current VM power data (if requested)")


class VMPowerResponse(BaseModel):
    """Response model for VM power endpoint (GET /api/v1/infrastructure/vms/{vm_id}/power)."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp")
    vm_id: str = Field(..., description="VM UUID")
    vm_name: str = Field(..., description="VM name")
    period: Optional[str] = Field(None, description="Time period (if time series)")
    power_data: VMPowerData = Field(..., description="VM power consumption data")
