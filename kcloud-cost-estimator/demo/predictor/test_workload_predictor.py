"""Unit tests for workload predictor"""

import pytest
import numpy as np
from datetime import datetime, timedelta

from src.predictor.workload_predictor import WorkloadPredictor
from src.predictor.models import HistoricalData, WorkloadPrediction


class TestWorkloadPredictor:
    """Test cases for WorkloadPredictor class"""

    @pytest.fixture
    def predictor(self):
        """Create predictor instance"""
        return WorkloadPredictor(auto_select=True)

    @pytest.fixture
    def static_workload_data(self):
        """Create static workload pattern test data"""
        timestamps = [
            datetime.utcnow() - timedelta(minutes=i)
            for i in range(90, 0, -1)
        ]
        values = [0.8] * 90  # Static 0.8 cores
        return HistoricalData(
            timestamps=timestamps,
            values=values,
            metric_name="cpu_cores"
        )

    @pytest.fixture
    def periodic_workload_data(self):
        """Create periodic workload pattern test data"""
        timestamps = [
            datetime.utcnow() - timedelta(minutes=i)
            for i in range(90, 0, -1)
        ]
        # Periodic pattern: alternating between 0.3 and 0.9
        values = [0.9 if (i // 15) % 2 == 0 else 0.3 for i in range(90)]
        return HistoricalData(
            timestamps=timestamps,
            values=values,
            metric_name="cpu_cores"
        )

    def test_predictor_initialization(self, predictor):
        """Test predictor initializes correctly"""
        assert predictor is not None
        assert predictor.auto_select is True
        assert predictor.model is None

    def test_predict_with_static_workload(self, predictor, static_workload_data):
        """Test prediction with static workload pattern"""
        prediction = predictor.predict(
            historical_data=static_workload_data,
            horizon_minutes=30,
            container_name="test-container",
            pod_name="test-pod",
            namespace="test-ns"
        )

        assert isinstance(prediction, WorkloadPrediction)
        assert prediction.container_name == "test-container"
        assert prediction.pod_name == "test-pod"
        assert prediction.namespace == "test-ns"
        assert 0.7 <= prediction.predicted_cpu_cores <= 0.9
        assert prediction.prediction_timestamp is not None

    def test_predict_with_periodic_workload(self, predictor, periodic_workload_data):
        """Test prediction with periodic workload pattern"""
        prediction = predictor.predict(
            historical_data=periodic_workload_data,
            horizon_minutes=30,
            container_name="periodic-container",
            pod_name="periodic-pod",
            namespace="test-ns"
        )

        assert isinstance(prediction, WorkloadPrediction)
        assert 0.2 <= prediction.predicted_cpu_cores <= 1.0

    def test_predict_with_insufficient_data(self, predictor):
        """Test prediction fails with insufficient data"""
        insufficient_data = HistoricalData(
            timestamps=[datetime.utcnow(), datetime.utcnow() - timedelta(minutes=1)],
            values=[0.5, 0.6],
            metric_name="cpu_cores"
        )

        with pytest.raises(ValueError, match="Insufficient historical data"):
            predictor.predict(
                historical_data=insufficient_data,
                horizon_minutes=30
            )

    def test_predict_with_minimum_data(self, predictor):
        """Test prediction works with minimum required data points"""
        minimum_data = HistoricalData(
            timestamps=[
                datetime.utcnow() - timedelta(minutes=i)
                for i in range(3)
            ],
            values=[0.5, 0.52, 0.51],
            metric_name="cpu_cores"
        )

        prediction = predictor.predict(
            historical_data=minimum_data,
            horizon_minutes=30,
            container_name="min-test",
            pod_name="min-pod",
            namespace="default"
        )

        assert isinstance(prediction, WorkloadPrediction)
        assert prediction.predicted_cpu_cores > 0

    def test_simple_prediction_fallback(self):
        """Test fallback to simple prediction when statsmodels unavailable"""
        predictor = WorkloadPredictor(auto_select=False)
        predictor.statsmodels = None  # Simulate missing dependency

        data = HistoricalData(
            timestamps=[
                datetime.utcnow() - timedelta(minutes=i)
                for i in range(5)
            ],
            values=[0.5, 0.52, 0.48, 0.51, 0.49],
            metric_name="cpu_cores"
        )

        prediction = predictor.predict(
            historical_data=data,
            horizon_minutes=30,
            container_name="fallback-test",
            pod_name="fallback-pod",
            namespace="default"
        )

        assert isinstance(prediction, WorkloadPrediction)
        # Should be close to moving average
        expected = np.mean([0.48, 0.51, 0.49])
        assert abs(prediction.predicted_cpu_cores - expected) < 0.1
        assert "simple_moving_average" in prediction.accuracy_metrics.get("method", "")

    def test_confidence_intervals(self, predictor, static_workload_data):
        """Test that confidence intervals are provided"""
        prediction = predictor.predict(
            historical_data=static_workload_data,
            horizon_minutes=30
        )

        if prediction.confidence_interval is not None:
            lower, upper = prediction.confidence_interval
            assert lower < prediction.predicted_cpu_cores < upper
            assert lower >= 0
            assert upper >= 0

    def test_accuracy_metrics(self, predictor, static_workload_data):
        """Test that accuracy metrics are calculated"""
        prediction = predictor.predict(
            historical_data=static_workload_data,
            horizon_minutes=30
        )

        if prediction.accuracy_metrics:
            metrics = prediction.accuracy_metrics
            # Should contain at least one metric
            assert len(metrics) > 0

    def test_prediction_with_varying_horizon(self, predictor, static_workload_data):
        """Test prediction with different time horizons"""
        horizons = [15, 30, 60, 120]

        for horizon in horizons:
            prediction = predictor.predict(
                historical_data=static_workload_data,
                horizon_minutes=horizon
            )

            assert isinstance(prediction, WorkloadPrediction)
            assert prediction.predicted_cpu_cores > 0
