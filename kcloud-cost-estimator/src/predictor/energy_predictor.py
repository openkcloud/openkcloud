"""
Energy prediction framework for containers

This module implements the complete four-step energy prediction framework
adapted from VM environments to Kubernetes containers.
"""

import logging
from typing import List, Dict, Optional
import numpy as np

from .models import (
    WorkloadPrediction,
    EnergyPrediction,
    CalibrationConfig,
    HistoricalData,
)
from .workload_predictor import WorkloadPredictor

logger = logging.getLogger(__name__)


class EnergyPredictor:
    """
    Four-step energy prediction framework for containers

    Steps:
    1. Predict container CPU workload using ARIMA
    2. Map container workload to node CPU utilization
    3. Predict node power consumption from CPU utilization
    4. Distribute node power back to containers

    Adapted from Alzamil & Djemame (2017) for container environments.

    Key differences from original paper:
    - Uses millicores/cores instead of vCPUs
    - Leverages Kepler direct measurements for calibration
    - Supports dynamic container scaling
    """

    def __init__(self, config: Optional[CalibrationConfig] = None):
        """
        Initialize energy predictor

        Args:
            config: Calibration parameters for linear models.
                   Uses defaults from Dell X3430 if not provided.
        """
        self.config = config or CalibrationConfig()
        self.workload_predictor = WorkloadPredictor(auto_select=True)

    def predict_container_energy(
        self,
        container_name: str,
        pod_name: str,
        namespace: str,
        historical_workload: HistoricalData,
        container_cpu_request: float,
        node_current_util: float,
        node_idle_util: float,
        containers_on_node: List[Dict],
        prediction_horizon_minutes: int = 30,
    ) -> EnergyPrediction:
        """
        Predict future energy consumption for a container

        Args:
            container_name: Target container name
            pod_name: Target pod name
            namespace: Kubernetes namespace
            historical_workload: Historical CPU usage time series (in cores)
            container_cpu_request: Container CPU request (in cores)
            node_current_util: Current node CPU utilization (0-100%)
            node_idle_util: Node idle CPU utilization (0-100%)
            containers_on_node: List of containers on same node
                               Each dict: {'cpu_request': float, 'cpu_util': float}
            prediction_horizon_minutes: Prediction time horizon

        Returns:
            EnergyPrediction: Predicted power consumption in watts
        """
        # Step 1: Predict container CPU workload
        workload_pred = self.workload_predictor.predict(
            historical_workload,
            horizon_minutes=prediction_horizon_minutes,
            container_name=container_name,
            pod_name=pod_name,
            namespace=namespace,
        )

        logger.info(
            f"Step 1: Predicted workload for {namespace}/{pod_name}/{container_name}: "
            f"{workload_pred.predicted_cpu_cores:.3f} cores"
        )

        # Step 2: Map container workload to node utilization
        predicted_node_util = self._predict_node_utilization(
            container_predicted_cores=workload_pred.predicted_cpu_cores,
            container_cpu_request=container_cpu_request,
            node_current_util=node_current_util,
            node_idle_util=node_idle_util,
        )

        logger.info(
            f"Step 2: Predicted node utilization: {predicted_node_util:.2f}%"
        )

        # Step 3: Predict node power consumption
        predicted_node_power = self._predict_node_power(predicted_node_util)

        logger.info(
            f"Step 3: Predicted node power: {predicted_node_power:.2f} watts"
        )

        # Step 4: Distribute power to container
        predicted_container_power = self._distribute_power_to_container(
            node_predicted_power=predicted_node_power,
            container_cpu_request=container_cpu_request,
            container_predicted_util=workload_pred.predicted_cpu_cores / container_cpu_request if container_cpu_request > 0 else 0,
            containers_on_node=containers_on_node,
        )

        logger.info(
            f"Step 4: Predicted container power: {predicted_container_power:.2f} watts"
        )

        return EnergyPrediction(
            container_name=container_name,
            pod_name=pod_name,
            namespace=namespace,
            predicted_power_watts=predicted_container_power,
            prediction_timestamp=workload_pred.prediction_timestamp,
            prediction_horizon_minutes=prediction_horizon_minutes,
            confidence_interval=workload_pred.confidence_interval,
        )

    def _predict_node_utilization(
        self,
        container_predicted_cores: float,
        container_cpu_request: float,
        node_current_util: float,
        node_idle_util: float,
    ) -> float:
        """
        Step 2: Map container CPU cores to node CPU utilization

        Based on Equation 2 from the paper, adapted for containers.
        Original equation used vCPUs (integers), we use cores (floats).

        Args:
            container_predicted_cores: Predicted container CPU usage (cores)
            container_cpu_request: Container CPU request (cores)
            node_current_util: Current node CPU utilization (%)
            node_idle_util: Node idle CPU utilization (%)

        Returns:
            Predicted node CPU utilization (%)
        """
        # Calculate utilization ratio
        if container_cpu_request > 0:
            util_ratio = container_predicted_cores / container_cpu_request
        else:
            util_ratio = 0

        # Linear mapping: cores -> node utilization increment
        # Equation: node_util_increment = slope * cores + intercept
        node_util_increment = (
            self.config.container_to_node_slope * container_predicted_cores +
            self.config.container_to_node_intercept
        )

        # Add to current utilization (subtract idle as it's already in equation)
        predicted_node_util = (
            node_util_increment +
            (node_current_util - node_idle_util)
        )

        # Clamp to valid range [0, 100]
        predicted_node_util = np.clip(predicted_node_util, 0, 100)

        return float(predicted_node_util)

    def _predict_node_power(self, node_predicted_util: float) -> float:
        """
        Step 3: Predict node power from CPU utilization

        Based on Equation 3 from the paper.
        Linear relationship: power = slope * utilization + intercept

        Args:
            node_predicted_util: Predicted node CPU utilization (%)

        Returns:
            Predicted node power consumption (watts)
        """
        predicted_power = (
            self.config.node_util_to_power_slope * node_predicted_util +
            self.config.node_util_to_power_intercept
        )

        # Clamp to valid range [idle, max]
        predicted_power = np.clip(
            predicted_power,
            self.config.node_idle_power_watts,
            self.config.node_max_power_watts,
        )

        return float(predicted_power)

    def _distribute_power_to_container(
        self,
        node_predicted_power: float,
        container_cpu_request: float,
        container_predicted_util: float,
        containers_on_node: List[Dict],
    ) -> float:
        """
        Step 4: Distribute node power to target container

        Based on Equation 1 from the paper, adapted for containers.
        Uses CPU requests (millicores -> cores) instead of vCPUs.

        Power distribution:
        - Idle power: split by CPU request ratio
        - Active power: split by (CPU request × utilization) ratio

        Args:
            node_predicted_power: Total predicted node power (watts)
            container_cpu_request: Target container CPU request (cores)
            container_predicted_util: Target container utilization (0-1)
            containers_on_node: List of all containers on node
                               Each dict must have 'cpu_request' and 'cpu_util'

        Returns:
            Predicted container power consumption (watts)
        """
        node_idle_power = self.config.node_idle_power_watts

        # Add target container to the list
        all_containers = containers_on_node + [{
            'cpu_request': container_cpu_request,
            'cpu_util': container_predicted_util,
        }]

        # Calculate total CPU requests
        total_cpu_requests = sum(c['cpu_request'] for c in all_containers)

        if total_cpu_requests == 0:
            logger.warning("Total CPU requests is zero, returning idle power")
            return node_idle_power

        # Distribute idle power by CPU request ratio
        container_idle_power = node_idle_power * (
            container_cpu_request / total_cpu_requests
        )

        # Calculate total weighted utilization
        total_weighted_util = sum(
            c['cpu_request'] * c['cpu_util']
            for c in all_containers
        )

        if total_weighted_util == 0:
            logger.warning("Total weighted utilization is zero")
            return container_idle_power

        # Distribute active power by weighted utilization
        node_active_power = node_predicted_power - node_idle_power
        container_weighted_util = container_cpu_request * container_predicted_util

        container_active_power = node_active_power * (
            container_weighted_util / total_weighted_util
        )

        # Total container power
        container_power = container_idle_power + container_active_power

        return float(container_power)

    def update_calibration(self, config: CalibrationConfig):
        """
        Update calibration parameters

        Args:
            config: New calibration configuration
        """
        self.config = config
        logger.info(
            f"Updated calibration: "
            f"C2N slope={config.container_to_node_slope:.3f}, "
            f"U2P slope={config.node_util_to_power_slope:.3f}"
        )
