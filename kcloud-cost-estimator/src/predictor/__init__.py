"""
Energy Prediction Module

This module implements energy prediction for cloud workload patterns
based on ARIMA time series forecasting and linear regression models.

Reference:
Alzamil, I., & Djemame, K. (2017). Energy Prediction for Cloud Workload Patterns.
GECON 2016: Economics of Grids, Clouds, Systems, and Services, pp. 160-174.
"""

__version__ = "1.0.0"

from .workload_predictor import WorkloadPredictor
from .energy_predictor import EnergyPredictor
from .calibration import CalibrationTool
from .prometheus_helper import PrometheusHelper
from .models import (
    WorkloadPrediction,
    EnergyPrediction,
    CalibrationConfig,
    HistoricalData,
)

__all__ = [
    "WorkloadPredictor",
    "EnergyPredictor",
    "CalibrationTool",
    "PrometheusHelper",
    "WorkloadPrediction",
    "EnergyPrediction",
    "CalibrationConfig",
    "HistoricalData",
]
