"""
Calibration utilities for energy prediction models

This module provides tools to calibrate the linear regression parameters
used in the energy prediction framework based on actual measurements.
"""

import logging
from typing import List, Tuple, Dict
import numpy as np

from .models import CalibrationConfig

logger = logging.getLogger(__name__)


class CalibrationTool:
    """
    Tool for calibrating energy prediction parameters

    The calibration process measures actual relationships between:
    1. Container CPU cores -> Node CPU utilization
    2. Node CPU utilization -> Node power consumption

    These measurements are then fitted using linear regression to obtain
    calibration parameters for the prediction models.
    """

    def __init__(self):
        """Initialize calibration tool"""
        self._check_dependencies()

    def _check_dependencies(self):
        """Check if required libraries are available"""
        try:
            from sklearn.linear_model import LinearRegression
            self.sklearn_available = True
        except ImportError:
            logger.warning(
                "scikit-learn not installed. "
                "Install with: pip install scikit-learn"
            )
            self.sklearn_available = False

    def calibrate_container_to_node(
        self,
        measurements: List[Tuple[float, float]]
    ) -> Tuple[float, float]:
        """
        Calibrate container CPU cores to node utilization mapping

        Args:
            measurements: List of (container_cores, node_utilization_percent) tuples
                         Example: [(0.5, 15.2), (1.0, 28.4), (2.0, 52.1)]

        Returns:
            Tuple of (slope, intercept) for linear model

        Example:
            >>> calibrator = CalibrationTool()
            >>> data = [(0.5, 15), (1.0, 30), (1.5, 45), (2.0, 60)]
            >>> slope, intercept = calibrator.calibrate_container_to_node(data)
            >>> print(f"Equation: node_util = {slope:.3f} * cores + {intercept:.3f}")
        """
        if len(measurements) < 2:
            raise ValueError("Need at least 2 measurements for calibration")

        X = np.array([m[0] for m in measurements]).reshape(-1, 1)
        y = np.array([m[1] for m in measurements])

        slope, intercept = self._fit_linear_model(X, y)

        logger.info(
            f"Container-to-Node calibration: "
            f"slope={slope:.4f}, intercept={intercept:.4f}"
        )

        return slope, intercept

    def calibrate_node_util_to_power(
        self,
        measurements: List[Tuple[float, float]]
    ) -> Tuple[float, float]:
        """
        Calibrate node utilization to power consumption mapping

        Args:
            measurements: List of (node_utilization_percent, power_watts) tuples
                         Example: [(10, 60), (50, 90), (100, 120)]

        Returns:
            Tuple of (slope, intercept) for linear model

        Example:
            >>> calibrator = CalibrationTool()
            >>> data = [(0, 54), (25, 70), (50, 87), (75, 105), (100, 122)]
            >>> slope, intercept = calibrator.calibrate_node_util_to_power(data)
            >>> print(f"Equation: power = {slope:.3f} * util + {intercept:.3f}")
        """
        if len(measurements) < 2:
            raise ValueError("Need at least 2 measurements for calibration")

        X = np.array([m[0] for m in measurements]).reshape(-1, 1)
        y = np.array([m[1] for m in measurements])

        slope, intercept = self._fit_linear_model(X, y)

        logger.info(
            f"Node-Util-to-Power calibration: "
            f"slope={slope:.4f}, intercept={intercept:.4f}"
        )

        return slope, intercept

    def calibrate_from_prometheus_data(
        self,
        container_node_data: List[Dict],
        node_power_data: List[Dict]
    ) -> CalibrationConfig:
        """
        Calibrate using Prometheus/Kepler historical data

        Args:
            container_node_data: List of measurements with keys:
                                'container_cpu_cores', 'node_cpu_util_percent'
            node_power_data: List of measurements with keys:
                            'node_cpu_util_percent', 'node_power_watts'

        Returns:
            CalibrationConfig: Calibrated configuration

        Example:
            >>> container_data = [
            ...     {'container_cpu_cores': 0.5, 'node_cpu_util_percent': 15},
            ...     {'container_cpu_cores': 1.0, 'node_cpu_util_percent': 30},
            ... ]
            >>> power_data = [
            ...     {'node_cpu_util_percent': 10, 'node_power_watts': 60},
            ...     {'node_cpu_util_percent': 50, 'node_power_watts': 90},
            ... ]
            >>> config = calibrator.calibrate_from_prometheus_data(
            ...     container_data, power_data
            ... )
        """
        # Calibrate container-to-node
        c2n_measurements = [
            (d['container_cpu_cores'], d['node_cpu_util_percent'])
            for d in container_node_data
        ]
        c2n_slope, c2n_intercept = self.calibrate_container_to_node(c2n_measurements)

        # Calibrate node-util-to-power
        u2p_measurements = [
            (d['node_cpu_util_percent'], d['node_power_watts'])
            for d in node_power_data
        ]
        u2p_slope, u2p_intercept = self.calibrate_node_util_to_power(u2p_measurements)

        # Determine idle and max power
        power_values = [d['node_power_watts'] for d in node_power_data]
        idle_power = min(power_values)
        max_power = max(power_values)

        config = CalibrationConfig(
            container_to_node_slope=c2n_slope,
            container_to_node_intercept=c2n_intercept,
            node_util_to_power_slope=u2p_slope,
            node_util_to_power_intercept=u2p_intercept,
            node_idle_power_watts=idle_power,
            node_max_power_watts=max_power,
        )

        logger.info(f"Calibration complete: {config}")

        return config

    def _fit_linear_model(
        self,
        X: np.ndarray,
        y: np.ndarray
    ) -> Tuple[float, float]:
        """
        Fit linear regression model

        Args:
            X: Input features (n_samples, 1)
            y: Target values (n_samples,)

        Returns:
            Tuple of (slope, intercept)
        """
        if self.sklearn_available:
            from sklearn.linear_model import LinearRegression

            model = LinearRegression()
            model.fit(X, y)

            slope = float(model.coef_[0])
            intercept = float(model.intercept_)

            # Calculate R² score
            r2_score = model.score(X, y)
            logger.info(f"Linear fit R² = {r2_score:.4f}")

        else:
            # Fallback: manual linear regression
            X_flat = X.flatten()
            slope = float(np.cov(X_flat, y)[0, 1] / np.var(X_flat))
            intercept = float(np.mean(y) - slope * np.mean(X_flat))

        return slope, intercept

    def validate_calibration(
        self,
        config: CalibrationConfig,
        test_measurements: List[Dict]
    ) -> Dict[str, float]:
        """
        Validate calibration using test data

        Args:
            config: Calibrated configuration
            test_measurements: Test data with keys:
                             'container_cpu_cores', 'actual_node_util', 'actual_power'

        Returns:
            Dictionary with validation metrics (MAE, RMSE, MAPE)
        """
        errors_util = []
        errors_power = []

        for measurement in test_measurements:
            cores = measurement['container_cpu_cores']
            actual_util = measurement['actual_node_util']
            actual_power = measurement['actual_power']

            # Predict using calibration
            predicted_util = (
                config.container_to_node_slope * cores +
                config.container_to_node_intercept
            )
            predicted_power = (
                config.node_util_to_power_slope * actual_util +
                config.node_util_to_power_intercept
            )

            errors_util.append(abs(predicted_util - actual_util))
            errors_power.append(abs(predicted_power - actual_power))

        metrics = {
            'utilization_mae': float(np.mean(errors_util)),
            'utilization_rmse': float(np.sqrt(np.mean(np.array(errors_util) ** 2))),
            'power_mae': float(np.mean(errors_power)),
            'power_rmse': float(np.sqrt(np.mean(np.array(errors_power) ** 2))),
        }

        logger.info(f"Validation metrics: {metrics}")

        return metrics
