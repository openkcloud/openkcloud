from typing import List, Optional, Dict
from datetime import datetime
from pydantic import BaseModel, Field
from .common import BaseResponse

# GPU Metrics Models
class GPUInfo(BaseModel):
    """Detailed information for a single GPU."""
    gpu_id: str = Field(..., description="GPU identifier (e.g., GPU-0)")
    instance: str = Field(..., description="The node where the GPU is located")
    power_draw_watts: float = Field(..., ge=0, description="Current power draw in watts")
    utilization_percent: float = Field(..., ge=0, le=100, description="GPU utilization percentage")
    temperature_celsius: float = Field(..., description="GPU temperature in Celsius")
    memory_used_mb: int = Field(..., ge=0, description="Used GPU memory in megabytes")
    memory_total_mb: int = Field(..., ge=0, description="Total GPU memory in megabytes")

    # Enhanced DCGM data (optional)
    dcgm_uuid: Optional[str] = Field(None, description="DCGM GPU UUID")
    dcgm_model_name: Optional[str] = Field(None, description="DCGM GPU model name")
    dcgm_driver_version: Optional[str] = Field(None, description="DCGM driver version")
    dcgm_power_watts: Optional[float] = Field(None, ge=0, description="DCGM power usage in watts")
    dcgm_temperature_celsius: Optional[float] = Field(None, description="DCGM GPU temperature")
    dcgm_memory_used_mb: Optional[int] = Field(None, ge=0, description="DCGM memory used in MB")
    dcgm_utilization_percent: Optional[float] = Field(None, ge=0, le=100, description="DCGM GPU utilization")
    data_source: str = Field("kepler", description="Primary data source: kepler, dcgm, or hybrid")

class GPUSummary(BaseModel):
    """Aggregated summary of GPU metrics."""
    total_power_watts: float = Field(..., ge=0)
    avg_power_watts: float = Field(..., ge=0)
    max_power_watts: float = Field(..., ge=0)
    avg_utilization_percent: float = Field(..., ge=0, le=100)

class WorkloadPower(BaseModel):
    """Power data for workloads, based on Kepler metrics."""
    cluster_power_watts: Optional[float] = Field(None, ge=0)
    pod_power_watts: Optional[float] = Field(None, ge=0)
    namespace: Optional[str] = None
    workload: Optional[str] = None
    sample_count: Optional[int] = Field(None, ge=0)

class GPUPowerResponse(BaseResponse):
    """Response model for the main GPU power API."""
    period: str
    total_gpus: int = Field(..., ge=0)
    gpus: List[GPUInfo]
    summary: GPUSummary
    workload_power: Optional[WorkloadPower] = None

class GPUInstanceResponse(BaseResponse):
    """Response model for a specific GPU node."""
    instance: str
    gpus: List[GPUInfo]
    workload_power: Optional[WorkloadPower] = None

# Time Series Models
class TimeSeriesPoint(BaseModel):
    """A single point in a time series."""
    timestamp: datetime
    value: float = Field(..., ge=0)

class TimeSeriesMetrics(BaseModel):
    """A collection of time series metrics from Prometheus."""
    gpu_total_power: List[TimeSeriesPoint]
    kepler_cluster_power: Optional[List[TimeSeriesPoint]] = None
    kepler_pod_power: Optional[List[TimeSeriesPoint]] = None

class TimeSeriesResponse(BaseResponse):
    """Response model for time series queries."""
    period: Optional[str] = None
    step: str
    start_time: datetime
    end_time: datetime
    total_samples: int
    metrics: TimeSeriesMetrics

# System Info and Health Check Models
class PrometheusStatus(BaseModel):
    status: str
    url: str

class CacheStatus(BaseModel):
    status: str
    entries: int

class HealthResponse(BaseResponse):
    status: str
    version: str = "1.0.0"
    prometheus: PrometheusStatus
    cache: CacheStatus

class SystemInfo(BaseResponse):
    available_instances: List[str]
    total_gpus: int
    prometheus_metrics: Dict[str, List[str]]
    data_retention: str

# Cluster and Pod Models
class NodeInfo(BaseModel):
    """Information about a cluster node."""
    node_name: str
    instance: str = Field(..., description="Instance identifier (IP:port)")
    internal_ip: Optional[str] = Field(None, description="Internal IP address")
    role: Optional[str] = Field(None, description="Node role (master/worker)")
    cpu_architecture: Optional[str] = None
    power_source: Optional[str] = None
    os_image: Optional[str] = Field(None, description="Operating system image")
    kernel_version: Optional[str] = Field(None, description="Kernel version")
    kubelet_version: Optional[str] = Field(None, description="Kubelet version")
    container_runtime: Optional[str] = Field(None, description="Container runtime version")
    status: str = "active"
    has_kepler: bool = Field(False, description="Whether Kepler is installed on this node")

class PodInfo(BaseModel):
    """Information about a pod."""
    pod_name: str
    namespace: str
    container_namespace: str
    power_watts: float = Field(..., ge=0)
    containers: Optional[List[str]] = None

class ClusterInfo(BaseModel):
    """Cluster metadata and information."""
    cluster_name: str = "default"
    nodes: List[NodeInfo]
    total_nodes: int
    active_pods: int
    namespaces: List[str]

class ClusterInfoResponse(BaseResponse):
    """Response for cluster information endpoint."""
    cluster: ClusterInfo

class PodPowerResponse(BaseResponse):
    """Response for pod-level power monitoring."""
    cluster_name: str = "default"
    namespace_filter: Optional[str] = None
    total_pods: int
    pods: List[PodInfo]
    total_power_watts: float = Field(..., ge=0)
    namespaces_summary: Dict[str, float]  # namespace -> total power

class PodDetailResponse(BaseResponse):
    """Response for individual pod power details."""
    pod_name: str
    namespace: str
    power_watts: float = Field(..., ge=0)
    containers: List[Dict[str, float]]  # container_name -> power_watts

# Cluster Total Power Models
class PowerBreakdown(BaseModel):
    """Power consumption breakdown by category."""
    category: str
    power_watts: float = Field(..., ge=0)
    percentage: float = Field(..., ge=0, le=100)

class EfficiencyMetrics(BaseModel):
    """Cluster efficiency metrics."""
    power_per_pod: float = Field(..., ge=0)
    power_per_namespace: float = Field(..., ge=0)
    total_workloads: int = Field(..., ge=0)
    active_namespaces: int = Field(..., ge=0)

class ClusterTotalPowerResponse(BaseResponse):
    """Response for cluster total power consumption."""
    cluster_name: str = "default"
    total_power_watts: float = Field(..., ge=0)
    measurement_time: datetime
    breakdown: Optional[List[PowerBreakdown]] = None
    efficiency: Optional[EfficiencyMetrics] = None
    node_count: int = Field(..., ge=0)
    pod_count: int = Field(..., ge=0)

class ClusterPowerTimeSeriesResponse(BaseResponse):
    """Response for cluster power consumption over time."""
    cluster_name: str = "default"
    period: Optional[str] = None
    step: str
    start_time: datetime
    end_time: datetime
    total_samples: int
    total_power_timeseries: List[TimeSeriesPoint]
    breakdown_timeseries: Optional[Dict[str, List[TimeSeriesPoint]]] = None
    efficiency_timeseries: Optional[List[TimeSeriesPoint]] = None

# Error Model
class ErrorDetail(BaseModel):
    code: str
    message: str
    details: Optional[str] = None

class ErrorResponse(BaseResponse):
    error: ErrorDetail

# DCGM GPU Models
class DCGMGPUInfo(BaseModel):
    """Detailed DCGM GPU information for a single GPU."""
    model_config = {"protected_namespaces": ()}

    gpu_id: str = Field(..., description="GPU identifier (e.g., nvidia0)")
    uuid: str = Field(..., description="GPU UUID")
    model_name: str = Field(..., description="GPU model name (e.g., NVIDIA A30)")
    driver_version: str = Field(..., description="NVIDIA driver version")
    hostname: str = Field(..., description="Node hostname")
    pci_bus_id: str = Field(..., description="PCIe bus ID")
    device_index: int = Field(..., ge=0, description="GPU device index")

class DCGMGPUMetrics(BaseModel):
    """Real-time DCGM GPU performance metrics."""
    gpu_id: str = Field(..., description="GPU identifier")
    timestamp: datetime = Field(..., description="Metric collection timestamp")

    # Performance metrics
    gpu_utilization_percent: Optional[float] = Field(None, ge=0, le=100, description="GPU utilization percentage")
    decoder_utilization_percent: Optional[float] = Field(None, ge=0, le=100, description="Decoder utilization percentage")
    encoder_utilization_percent: Optional[float] = Field(None, ge=0, le=100, description="Encoder utilization percentage")
    memory_copy_utilization_percent: Optional[float] = Field(None, ge=0, le=100, description="Memory copy utilization percentage")

    # Temperature metrics
    gpu_temperature_celsius: Optional[float] = Field(None, description="GPU core temperature in Celsius")
    memory_temperature_celsius: Optional[float] = Field(None, description="GPU memory temperature in Celsius")

    # Power metrics
    power_usage_watts: Optional[float] = Field(None, ge=0, description="Current power usage in watts")
    total_energy_joules: Optional[float] = Field(None, ge=0, description="Total energy consumption in joules")

    # Memory metrics
    memory_used_mb: Optional[int] = Field(None, ge=0, description="Used GPU memory in megabytes")
    memory_free_mb: Optional[int] = Field(None, ge=0, description="Free GPU memory in megabytes")
    memory_reserved_mb: Optional[int] = Field(None, ge=0, description="Reserved GPU memory in megabytes")

    # Clock metrics
    sm_clock_mhz: Optional[int] = Field(None, ge=0, description="SM clock speed in MHz")
    memory_clock_mhz: Optional[int] = Field(None, ge=0, description="Memory clock speed in MHz")

    # Error metrics
    xid_errors: Optional[int] = Field(None, ge=0, description="XID error count")
    pcie_replay_counter: Optional[int] = Field(None, ge=0, description="PCIe replay counter")
    correctable_remapped_rows: Optional[int] = Field(None, ge=0, description="Correctable remapped memory rows")
    uncorrectable_remapped_rows: Optional[int] = Field(None, ge=0, description="Uncorrectable remapped memory rows")

class DCGMGPUInfoResponse(BaseResponse):
    """Response for GPU information API."""
    node: str = Field(..., description="Node hostname")
    total_gpus: int = Field(..., ge=0, description="Total number of GPUs")
    gpus: List[DCGMGPUInfo] = Field(..., description="List of GPU information")

class DCGMGPUMetricsResponse(BaseResponse):
    """Response for GPU metrics API."""
    node: str = Field(..., description="Node hostname")
    total_gpus: int = Field(..., ge=0, description="Total number of GPUs")
    metrics: List[DCGMGPUMetrics] = Field(..., description="List of GPU metrics")

class DCGMGPUSummary(BaseModel):
    """Summary statistics for all GPUs."""
    total_gpus: int = Field(..., ge=0)
    active_gpus: int = Field(..., ge=0, description="GPUs with >0% utilization")
    avg_gpu_utilization_percent: float = Field(..., ge=0, le=100)
    max_gpu_utilization_percent: float = Field(..., ge=0, le=100)
    avg_temperature_celsius: float = Field(..., description="Average GPU temperature")
    max_temperature_celsius: float = Field(..., description="Maximum GPU temperature")
    total_power_watts: float = Field(..., ge=0, description="Total power consumption")
    total_memory_used_mb: int = Field(..., ge=0, description="Total memory used across all GPUs")
    total_memory_available_mb: int = Field(..., ge=0, description="Total memory available across all GPUs")

class DCGMGPUTemperature(BaseModel):
    """GPU temperature information for a single GPU."""
    gpu_id: str = Field(..., description="GPU identifier")
    hostname: str = Field(..., description="Node hostname")
    timestamp: datetime = Field(..., description="Temperature measurement timestamp")
    gpu_temperature_celsius: Optional[float] = Field(None, description="GPU core temperature in Celsius")
    memory_temperature_celsius: Optional[float] = Field(None, description="GPU memory temperature in Celsius")
    temperature_limit_celsius: Optional[float] = Field(None, description="GPU temperature limit")
    temperature_status: str = Field("normal", description="Temperature status: normal, warning, critical")

class DCGMGPUTemperatureResponse(BaseResponse):
    """Response for GPU temperature monitoring API."""
    node: str = Field(..., description="Node hostname")
    total_gpus: int = Field(..., ge=0, description="Total number of GPUs")
    temperatures: List[DCGMGPUTemperature] = Field(..., description="List of GPU temperatures")
    temperature_summary: Optional[Dict[str, float]] = Field(None, description="Temperature summary statistics")
