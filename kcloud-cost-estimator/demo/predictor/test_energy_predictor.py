"""Unit tests for energy predictor"""

import pytest
from datetime import datetime, timedelta

from src.predictor.energy_predictor import EnergyPredictor
from src.predictor.models import (
    HistoricalData,
    EnergyPrediction,
    CalibrationConfig,
)


class TestEnergyPredictor:
    """Test cases for EnergyPredictor class"""

    @pytest.fixture
    def default_config(self):
        """Create default calibration config"""
        return CalibrationConfig()

    @pytest.fixture
    def predictor(self, default_config):
        """Create energy predictor instance"""
        return EnergyPredictor(config=default_config)

    @pytest.fixture
    def historical_data(self):
        """Create sample historical data"""
        timestamps = [
            datetime.utcnow() - timedelta(minutes=i)
            for i in range(90, 0, -1)
        ]
        values = [0.8] * 90
        return HistoricalData(
            timestamps=timestamps,
            values=values,
            metric_name="cpu_cores"
        )

    def test_predictor_initialization(self, predictor, default_config):
        """Test predictor initializes with config"""
        assert predictor is not None
        assert predictor.config == default_config
        assert predictor.workload_predictor is not None

    def test_predict_node_utilization(self, predictor):
        """Test Step 2: container to node utilization mapping"""
        predicted_util = predictor._predict_node_utilization(
            container_predicted_cores=1.0,
            container_cpu_request=1.0,
            node_current_util=50.0,
            node_idle_util=5.0
        )

        assert isinstance(predicted_util, float)
        assert 0 <= predicted_util <= 100

    def test_predict_node_power(self, predictor):
        """Test Step 3: node utilization to power mapping"""
        predicted_power = predictor._predict_node_power(
            node_predicted_util=50.0
        )

        assert isinstance(predicted_power, float)
        assert predicted_power >= predictor.config.node_idle_power_watts
        assert predicted_power <= predictor.config.node_max_power_watts

    def test_distribute_power_to_container(self, predictor):
        """Test Step 4: power distribution"""
        containers_on_node = [
            {"cpu_request": 0.5, "cpu_util": 0.6},
            {"cpu_request": 1.0, "cpu_util": 0.8},
        ]

        predicted_power = predictor._distribute_power_to_container(
            node_predicted_power=100.0,
            container_cpu_request=1.0,
            container_predicted_util=0.75,
            containers_on_node=containers_on_node
        )

        assert isinstance(predicted_power, float)
        assert predicted_power > 0
        assert predicted_power < 100.0  # Should be less than total

    def test_full_prediction_pipeline(self, predictor, historical_data):
        """Test complete prediction workflow"""
        prediction = predictor.predict_container_energy(
            container_name="test-container",
            pod_name="test-pod",
            namespace="default",
            historical_workload=historical_data,
            container_cpu_request=1.0,
            node_current_util=45.0,
            node_idle_util=5.0,
            containers_on_node=[],
            prediction_horizon_minutes=30
        )

        assert isinstance(prediction, EnergyPrediction)
        assert prediction.container_name == "test-container"
        assert prediction.pod_name == "test-pod"
        assert prediction.namespace == "default"
        assert prediction.predicted_power_watts > 0
        assert prediction.prediction_horizon_minutes == 30

    def test_update_calibration(self, predictor):
        """Test calibration update"""
        new_config = CalibrationConfig(
            container_to_node_slope=20.0,
            node_util_to_power_slope=0.8
        )

        predictor.update_calibration(new_config)

        assert predictor.config == new_config
        assert predictor.config.container_to_node_slope == 20.0

    def test_zero_cpu_request_handling(self, predictor):
        """Test handling of zero CPU request"""
        predicted_util = predictor._predict_node_utilization(
            container_predicted_cores=0.5,
            container_cpu_request=0.0,  # Zero request
            node_current_util=50.0,
            node_idle_util=5.0
        )

        assert isinstance(predicted_util, float)
        assert predicted_util >= 0

    def test_power_distribution_with_empty_containers(self, predictor):
        """Test power distribution when only target container exists"""
        predicted_power = predictor._distribute_power_to_container(
            node_predicted_power=100.0,
            container_cpu_request=2.0,
            container_predicted_util=0.5,
            containers_on_node=[]  # Only target container
        )

        assert isinstance(predicted_power, float)
        # Should get all the power
        assert predicted_power > 0
