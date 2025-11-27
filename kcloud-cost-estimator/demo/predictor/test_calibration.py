"""Unit tests for calibration tool"""

import pytest
import numpy as np

from src.predictor.calibration import CalibrationTool
from src.predictor.models import CalibrationConfig


class TestCalibrationTool:
    """Test cases for CalibrationTool class"""

    @pytest.fixture
    def calibrator(self):
        """Create calibration tool instance"""
        return CalibrationTool()

    @pytest.fixture
    def container_node_measurements(self):
        """Sample container to node measurements"""
        return [
            (0.5, 15.2),
            (1.0, 28.5),
            (1.5, 42.1),
            (2.0, 55.8),
            (2.5, 69.3),
        ]

    @pytest.fixture
    def node_power_measurements(self):
        """Sample node power measurements"""
        return [
            (0, 54.0),
            (25, 68.5),
            (50, 85.2),
            (75, 102.8),
            (100, 120.1),
        ]

    def test_calibrator_initialization(self, calibrator):
        """Test calibrator initializes correctly"""
        assert calibrator is not None

    def test_calibrate_container_to_node(
        self,
        calibrator,
        container_node_measurements
    ):
        """Test container-to-node calibration"""
        slope, intercept = calibrator.calibrate_container_to_node(
            container_node_measurements
        )

        assert isinstance(slope, float)
        assert isinstance(intercept, float)
        # Should be positive slope
        assert slope > 0
        # Intercept should be small positive
        assert intercept >= 0

    def test_calibrate_node_util_to_power(
        self,
        calibrator,
        node_power_measurements
    ):
        """Test node-utilization-to-power calibration"""
        slope, intercept = calibrator.calibrate_node_util_to_power(
            node_power_measurements
        )

        assert isinstance(slope, float)
        assert isinstance(intercept, float)
        # Should be positive slope
        assert slope > 0
        # Intercept should be around idle power
        assert intercept > 50

    def test_calibrate_from_prometheus_data(
        self,
        calibrator,
        container_node_measurements,
        node_power_measurements
    ):
        """Test calibration from Prometheus format data"""
        container_data = [
            {
                "container_cpu_cores": cores,
                "node_cpu_util_percent": util
            }
            for cores, util in container_node_measurements
        ]

        power_data = [
            {
                "node_cpu_util_percent": util,
                "node_power_watts": power
            }
            for util, power in node_power_measurements
        ]

        config = calibrator.calibrate_from_prometheus_data(
            container_node_data=container_data,
            node_power_data=power_data
        )

        assert isinstance(config, CalibrationConfig)
        assert config.container_to_node_slope > 0
        assert config.node_util_to_power_slope > 0
        assert config.node_idle_power_watts > 0
        assert config.node_max_power_watts > config.node_idle_power_watts

    def test_insufficient_measurements_error(self, calibrator):
        """Test error with insufficient measurements"""
        insufficient_data = [(0.5, 15.0)]

        with pytest.raises(ValueError, match="at least 2 measurements"):
            calibrator.calibrate_container_to_node(insufficient_data)

    def test_validate_calibration(
        self,
        calibrator,
        container_node_measurements,
        node_power_measurements
    ):
        """Test calibration validation"""
        container_data = [
            {"container_cpu_cores": c, "node_cpu_util_percent": u}
            for c, u in container_node_measurements
        ]
        power_data = [
            {"node_cpu_util_percent": u, "node_power_watts": p}
            for u, p in node_power_measurements
        ]

        config = calibrator.calibrate_from_prometheus_data(
            container_data, power_data
        )

        # Validate using same data (should have low error)
        test_measurements = [
            {
                "container_cpu_cores": 1.0,
                "actual_node_util": 28.5,
                "actual_power": 68.5,
            }
        ]

        metrics = calibrator.validate_calibration(config, test_measurements)

        assert "utilization_mae" in metrics
        assert "power_mae" in metrics
        assert metrics["utilization_mae"] >= 0
        assert metrics["power_mae"] >= 0

    def test_linear_fit_with_perfect_data(self, calibrator):
        """Test linear fit with perfect linear relationship"""
        # y = 2x + 3
        X = np.array([[1], [2], [3], [4], [5]])
        y = np.array([5, 7, 9, 11, 13])

        slope, intercept = calibrator._fit_linear_model(X, y)

        assert abs(slope - 2.0) < 0.01
        assert abs(intercept - 3.0) < 0.01

    def test_linear_fit_without_sklearn(self, calibrator):
        """Test fallback linear fit when sklearn unavailable"""
        calibrator.sklearn_available = False

        X = np.array([[1], [2], [3], [4], [5]])
        y = np.array([5, 7, 9, 11, 13])

        slope, intercept = calibrator._fit_linear_model(X, y)

        # Should still work with manual calculation
        assert abs(slope - 2.0) < 0.1
        assert abs(intercept - 3.0) < 0.1
