"""
Pydantic models for API request/response validation
"""

from typing import List, Optional
from pydantic import BaseModel, Field, validator


class ContainerInfo(BaseModel):
    """Container information for power distribution"""
    cpu_request: float = Field(gt=0, description="CPU request in cores")
    cpu_util: float = Field(ge=0, le=1, description="CPU utilization ratio (0-1)")


class EnergyPredictionRequest(BaseModel):
    """Request model for energy prediction endpoint"""

    container_name: str = Field(..., min_length=1)
    pod_name: str = Field(..., min_length=1)
    namespace: str = Field(..., min_length=1)
    historical_cpu_cores: List[float] = Field(..., min_items=3)
    container_cpu_request: float = Field(gt=0)
    node_current_util: float = Field(ge=0, le=100)
    node_idle_util: float = Field(ge=0, le=100)
    containers_on_node: List[ContainerInfo] = Field(default_factory=list)
    prediction_horizon_minutes: int = Field(default=30, gt=0, le=240)

    @validator("historical_cpu_cores")
    def validate_cpu_cores(cls, v):
        if any(x < 0 for x in v):
            raise ValueError("CPU cores cannot be negative")
        return v

    @validator("node_idle_util")
    def validate_idle_less_than_current(cls, v, values):
        if "node_current_util" in values and v > values["node_current_util"]:
            raise ValueError("Idle utilization cannot exceed current utilization")
        return v


class CalibrationDataPoint(BaseModel):
    """Single calibration measurement"""
    container_cpu_cores: Optional[float] = None
    node_cpu_util_percent: Optional[float] = Field(None, ge=0, le=100)
    node_power_watts: Optional[float] = Field(None, gt=0)


class CalibrationRequest(BaseModel):
    """Request model for calibration endpoint"""

    container_node_data: List[CalibrationDataPoint] = Field(..., min_items=2)
    node_power_data: List[CalibrationDataPoint] = Field(..., min_items=2)

    @validator("container_node_data")
    def validate_container_data(cls, v):
        for item in v:
            if item.container_cpu_cores is None or item.node_cpu_util_percent is None:
                raise ValueError(
                    "container_node_data must have container_cpu_cores "
                    "and node_cpu_util_percent"
                )
        return v

    @validator("node_power_data")
    def validate_power_data(cls, v):
        for item in v:
            if item.node_cpu_util_percent is None or item.node_power_watts is None:
                raise ValueError(
                    "node_power_data must have node_cpu_util_percent "
                    "and node_power_watts"
                )
        return v
