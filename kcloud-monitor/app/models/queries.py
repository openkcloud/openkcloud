from typing import Optional
from datetime import datetime
from pydantic import BaseModel, Field
from .common.enums import TimeRange, ExportFormat
from enum import Enum

# Legacy TimePeriod enum for backward compatibility
class TimePeriod(str, Enum):
    """Legacy time period enum (use TimeRange in new code)."""
    HOUR = "1h"
    DAY = "1d"
    WEEK = "1w"
    MONTH = "1m"

# Legacy alias
DataFormat = ExportFormat

class GPUQueryParams(BaseModel):
    """Query parameters for GPU power endpoints."""
    period: TimePeriod = Field(default=TimePeriod.HOUR, description="Time period to query.")
    start_time: Optional[datetime] = Field(None, description="Custom start time for the query period.")
    end_time: Optional[datetime] = Field(None, description="Custom end time for the query period.")
    instance: Optional[str] = Field(None, description="Filter by a specific GPU node instance.")
    cluster: Optional[str] = Field(None, description="Filter by cluster name (defaults to 'default').")
    node: Optional[str] = Field(None, description="Filter by node name.")

class TimeSeriesQueryParams(BaseModel):
    """Query parameters for time-series endpoints."""
    period: Optional[TimePeriod] = Field(None, description="Predefined time period to query (overridden by start/end).")
    step: str = Field(default="5m", pattern=r"^(1m|5m|15m|30m|1h|6h|12h|1d)$", description="Time interval for sampling data.")
    samples: Optional[int] = Field(None, ge=1, le=10000, description="Number of samples to return (overrides period calculation).")
    start: Optional[datetime] = Field(None, description="Custom start time (ISO format).")
    end: Optional[datetime] = Field(None, description="Custom end time (ISO format).")
    instance: Optional[str] = Field(None, description="Filter by a specific GPU node instance.")
    cluster: Optional[str] = Field(None, description="Filter by cluster name.")
    node: Optional[str] = Field(None, description="Filter by node name.")

class ExportQueryParams(BaseModel):
    """Query parameters for data export endpoints."""
    period: TimePeriod = Field(description="Time period to query.")
    format: DataFormat = Field(default=DataFormat.JSON, description="The format for the exported data.")
    step: str = Field(default="1m", description="Time interval for sampling data.")
    instance: Optional[str] = Field(None, description="Filter by a specific GPU node instance.")

class PodQueryParams(BaseModel):
    """Query parameters for pod power endpoints."""
    cluster: Optional[str] = Field(None, description="Filter by cluster name (defaults to 'default').")
    namespace: Optional[str] = Field(None, description="Filter by namespace.")
    node: Optional[str] = Field(None, description="Filter by node name.")
    label_selector: Optional[str] = Field(
        None,
        description="Label selector in k8s format (e.g., app=nginx,env=prod)."
    )
    period: TimePeriod = Field(default=TimePeriod.HOUR, description="Time period to query.")
    min_power: Optional[float] = Field(None, ge=0, description="Minimum power threshold in watts.")
    max_power: Optional[float] = Field(None, ge=0, description="Maximum power threshold in watts.")


class ContainerQueryParams(BaseModel):
    """Query parameters for container endpoints."""
    cluster: Optional[str] = Field(None, description="Filter by cluster name (defaults to 'default').")
    namespace: Optional[str] = Field(None, description="Filter by namespace.")
    pod: Optional[str] = Field(None, description="Filter by pod name.")
    node: Optional[str] = Field(None, description="Filter by node name.")
    include_terminated: bool = Field(False, description="Include terminated containers.")
    min_power: Optional[float] = Field(None, ge=0, description="Minimum power threshold in watts.")
    max_power: Optional[float] = Field(None, ge=0, description="Maximum power threshold in watts.")

class ClusterTotalQueryParams(BaseModel):
    """Query parameters for cluster total power endpoints."""
    period: Optional[TimePeriod] = Field(None, description="Predefined time period to query.")
    step: str = Field(default="5m", pattern=r"^(\d+[smhd]|1m|5m|15m|30m|1h|6h|12h|1d)$", description="Time interval for sampling (e.g., 60s, 1m, 5m, 1h).")
    start: Optional[datetime] = Field(None, description="Custom start time (ISO format).")
    end: Optional[datetime] = Field(None, description="Custom end time (ISO format).")
    breakdown_by: Optional[str] = Field(None, pattern=r"^(node|namespace|workload_type)$", description="Break down power by category.")
    include_efficiency: bool = Field(default=False, description="Include efficiency metrics.")
    cluster: Optional[str] = Field(None, description="Filter by cluster name.")
