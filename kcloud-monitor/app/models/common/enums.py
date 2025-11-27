"""
Common Enumerations - Shared enums across all domains.

This module defines common enumerations used throughout the API.
"""

from enum import Enum


# ============================================================================
# Resource Type Enumerations
# ============================================================================

class ResourceType(str, Enum):
    """Type of monitored resource."""
    GPU = "gpu"
    NPU = "npu"
    NODE = "node"
    POD = "pod"
    CONTAINER = "container"
    VM = "vm"
    CLUSTER = "cluster"


class AcceleratorType(str, Enum):
    """Type of AI accelerator."""
    GPU = "gpu"
    NPU = "npu"
    TPU = "tpu"
    FPGA = "fpga"
    ASIC = "asic"


# ============================================================================
# Sensor and Hardware Enumerations
# ============================================================================

class SensorType(str, Enum):
    """Type of hardware sensor."""
    TEMPERATURE = "temperature"
    POWER = "power"
    FAN = "fan"
    VOLTAGE = "voltage"
    CURRENT = "current"
    HUMIDITY = "humidity"
    OTHER = "other"


class TemperatureUnit(str, Enum):
    """Temperature measurement unit."""
    CELSIUS = "celsius"
    FAHRENHEIT = "fahrenheit"
    KELVIN = "kelvin"


class PowerUnit(str, Enum):
    """Power measurement unit."""
    WATTS = "watts"
    KILOWATTS = "kilowatts"
    MILLIWATTS = "milliwatts"


# ============================================================================
# Time Range Enumerations
# ============================================================================

class TimeRange(str, Enum):
    """Predefined time range for queries."""
    ONE_HOUR = "1h"
    SIX_HOURS = "6h"
    TWELVE_HOURS = "12h"
    ONE_DAY = "1d"
    THREE_DAYS = "3d"
    ONE_WEEK = "1w"
    TWO_WEEKS = "2w"
    ONE_MONTH = "1m"
    THREE_MONTHS = "3m"
    SIX_MONTHS = "6m"
    ONE_YEAR = "1y"


class TimeUnit(str, Enum):
    """Time unit for step and interval parameters."""
    SECOND = "s"
    MINUTE = "m"
    HOUR = "h"
    DAY = "d"


# ============================================================================
# Aggregation and Analysis Enumerations
# ============================================================================

class AggregationMethod(str, Enum):
    """Data aggregation method for time series queries."""
    AVG = "avg"
    MIN = "min"
    MAX = "max"
    SUM = "sum"
    COUNT = "count"
    MEDIAN = "median"
    PERCENTILE_95 = "p95"
    PERCENTILE_99 = "p99"


class BreakdownDimension(str, Enum):
    """Dimension for data breakdown and grouping."""
    CLUSTER = "cluster"
    NODE = "node"
    NAMESPACE = "namespace"
    VENDOR = "vendor"
    RESOURCE_TYPE = "resource_type"
    POD = "pod"
    WORKLOAD = "workload"


# ============================================================================
# Export Format Enumerations
# ============================================================================

class ExportFormat(str, Enum):
    """Data export format."""
    JSON = "json"
    CSV = "csv"
    PARQUET = "parquet"
    EXCEL = "excel"
    PDF = "pdf"
    HTML = "html"


class ReportTemplate(str, Enum):
    """Report template type."""
    DAILY = "daily"
    WEEKLY = "weekly"
    MONTHLY = "monthly"
    CUSTOM = "custom"


# ============================================================================
# Streaming Protocol Enumerations
# ============================================================================

class StreamProtocol(str, Enum):
    """Real-time streaming protocol."""
    WEBSOCKET = "websocket"
    SSE = "sse"


class StreamEventType(str, Enum):
    """Type of streaming event."""
    DATA_UPDATE = "data_update"
    ALERT = "alert"
    STATUS_CHANGE = "status_change"
    HEARTBEAT = "heartbeat"
    ERROR = "error"


# ============================================================================
# Status Enumerations
# ============================================================================

class OperationalStatus(str, Enum):
    """General operational status for resources."""
    ACTIVE = "active"
    IDLE = "idle"
    DEGRADED = "degraded"
    ERROR = "error"
    OFFLINE = "offline"
    MAINTENANCE = "maintenance"
    UNKNOWN = "unknown"


class HealthStatus(str, Enum):
    """Health status for components and systems."""
    HEALTHY = "healthy"
    DEGRADED = "degraded"
    UNHEALTHY = "unhealthy"
    UNKNOWN = "unknown"


class AlertSeverity(str, Enum):
    """Alert severity level."""
    INFO = "info"
    WARNING = "warning"
    ERROR = "error"
    CRITICAL = "critical"


# ============================================================================
# API and System Enumerations
# ============================================================================

class APIVersion(str, Enum):
    """API version."""
    V1 = "v1"
    V2 = "v2"


class ErrorCode(str, Enum):
    """Standard error codes."""
    # Client errors (4xx)
    VALIDATION_ERROR = "VALIDATION_ERROR"
    AUTHENTICATION_ERROR = "AUTHENTICATION_ERROR"
    AUTHORIZATION_ERROR = "AUTHORIZATION_ERROR"
    NOT_FOUND = "NOT_FOUND"
    INVALID_PARAMETER = "INVALID_PARAMETER"

    # Server errors (5xx)
    INTERNAL_ERROR = "INTERNAL_ERROR"
    PROMETHEUS_ERROR = "PROMETHEUS_ERROR"
    DATABASE_ERROR = "DATABASE_ERROR"
    CACHE_ERROR = "CACHE_ERROR"
    EXTERNAL_SERVICE_ERROR = "EXTERNAL_SERVICE_ERROR"
    TIMEOUT_ERROR = "TIMEOUT_ERROR"

    # Data source errors
    DCGM_ERROR = "DCGM_ERROR"
    KEPLER_ERROR = "KEPLER_ERROR"
    IPMI_ERROR = "IPMI_ERROR"
    OPENSTACK_ERROR = "OPENSTACK_ERROR"


# ============================================================================
# Vendor Enumerations
# ============================================================================

class GPUVendor(str, Enum):
    """GPU vendor."""
    NVIDIA = "nvidia"
    AMD = "amd"
    INTEL = "intel"


class NPUVendor(str, Enum):
    """NPU vendor."""
    FURIOSA = "furiosa"
    REBELLIONS = "rebellions"
    GRAPHCORE = "graphcore"
    INTEL_HABANA = "intel_habana"
