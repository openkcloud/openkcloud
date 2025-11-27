"""
Data models for energy prediction framework
"""

from typing import List, Optional
from datetime import datetime
from pydantic import BaseModel, Field


class WorkloadPrediction(BaseModel):
    """Predicted workload for a container"""

    container_name: str
    pod_name: str
    namespace: str
    predicted_cpu_cores: float = Field(..., description="Predicted CPU usage in cores")
    prediction_timestamp: datetime
    confidence_interval: Optional[tuple] = None
    accuracy_metrics: Optional[dict] = None


class EnergyPrediction(BaseModel):
    """Predicted energy consumption for a container"""

    container_name: str
    pod_name: str
    namespace: str
    predicted_power_watts: float = Field(..., description="Predicted power consumption in watts")
    prediction_timestamp: datetime
    prediction_horizon_minutes: int
    confidence_interval: Optional[tuple] = None


class CalibrationConfig(BaseModel):
    """Calibration parameters for the prediction models"""

    # Container to Node workload mapping (Step 2)
    container_to_node_slope: float = Field(
        default=23.993,
        description="Linear slope for container cores to node utilization"
    )
    container_to_node_intercept: float = Field(
        default=4.5347,
        description="Linear intercept for container cores to node utilization"
    )

    # Node workload to power mapping (Step 3)
    node_util_to_power_slope: float = Field(
        default=0.7254,
        description="Linear slope for node utilization to power (watts)"
    )
    node_util_to_power_intercept: float = Field(
        default=53.88,
        description="Linear intercept for node utilization to power (watts)"
    )

    # Node characteristics
    node_idle_power_watts: float = Field(
        default=53.88,
        description="Node idle power consumption in watts"
    )
    node_max_power_watts: float = Field(
        default=126.34,
        description="Node maximum power consumption in watts"
    )

    class Config:
        validate_assignment = True


class HistoricalData(BaseModel):
    """Historical time series data for prediction"""

    timestamps: List[datetime]
    values: List[float]
    metric_name: str

    def __len__(self):
        return len(self.values)
