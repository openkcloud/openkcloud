"""
Example script demonstrating energy prediction usage

This script shows how to use the energy prediction API programmatically.
"""

import sys
import os

# Add src to path for imports
sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..'))

from datetime import datetime, timedelta
from src.predictor import (
    EnergyPredictor,
    WorkloadPredictor,
    CalibrationConfig,
    HistoricalData,
)


def example_static_workload_prediction():
    """Example: Predict energy for static workload"""
    print("Example 1: Static Workload Prediction")
    print("-" * 50)

    # Create historical data (static pattern at 0.8 cores)
    timestamps = [
        datetime.utcnow() - timedelta(minutes=i)
        for i in range(90, 0, -1)
    ]
    values = [0.8] * 90

    historical_data = HistoricalData(
        timestamps=timestamps,
        values=values,
        metric_name="cpu_cores"
    )

    # Initialize predictor
    predictor = EnergyPredictor()

    # Predict
    prediction = predictor.predict_container_energy(
        container_name="nginx-server",
        pod_name="nginx-deploy-abc123",
        namespace="production",
        historical_workload=historical_data,
        container_cpu_request=1.0,
        node_current_util=45.0,
        node_idle_util=5.0,
        containers_on_node=[],
        prediction_horizon_minutes=30
    )

    print(f"Container: {prediction.container_name}")
    print(f"Predicted Power: {prediction.predicted_power_watts:.2f} watts")
    print(f"Prediction Horizon: {prediction.prediction_horizon_minutes} minutes")
    print()


def example_calibration():
    """Example: Calibrate models with measurement data"""
    print("Example 2: Model Calibration")
    print("-" * 50)

    # Simulated measurement data
    container_node_measurements = [
        (0.5, 15.2),
        (1.0, 28.5),
        (1.5, 42.1),
        (2.0, 55.8),
    ]

    node_power_measurements = [
        (0, 54.0),
        (25, 68.5),
        (50, 85.2),
        (75, 102.8),
        (100, 120.1),
    ]

    from src.predictor import CalibrationTool

    calibrator = CalibrationTool()

    # Calibrate container-to-node mapping
    c2n_slope, c2n_intercept = calibrator.calibrate_container_to_node(
        container_node_measurements
    )

    # Calibrate node-util-to-power mapping
    u2p_slope, u2p_intercept = calibrator.calibrate_node_util_to_power(
        node_power_measurements
    )

    print(f"Container-to-Node: {c2n_slope:.3f}x + {c2n_intercept:.3f}")
    print(f"Util-to-Power: {u2p_slope:.3f}x + {u2p_intercept:.3f}")
    print()


def example_workload_predictor():
    """Example: Workload prediction only"""
    print("Example 3: Workload Prediction Only")
    print("-" * 50)

    # Periodic workload pattern
    timestamps = [
        datetime.utcnow() - timedelta(minutes=i)
        for i in range(60, 0, -1)
    ]
    # Alternating pattern
    values = [0.9 if (i // 10) % 2 == 0 else 0.3 for i in range(60)]

    historical_data = HistoricalData(
        timestamps=timestamps,
        values=values,
        metric_name="cpu_cores"
    )

    predictor = WorkloadPredictor(auto_select=True)

    prediction = predictor.predict(
        historical_data=historical_data,
        horizon_minutes=30,
        container_name="periodic-job",
        pod_name="periodic-job-12345",
        namespace="batch"
    )

    print(f"Predicted CPU: {prediction.predicted_cpu_cores:.3f} cores")
    if prediction.confidence_interval:
        lower, upper = prediction.confidence_interval
        print(f"Confidence Interval: [{lower:.3f}, {upper:.3f}]")
    print()


if __name__ == "__main__":
    print("Energy Prediction Examples")
    print("=" * 50)
    print()

    example_static_workload_prediction()
    example_calibration()
    example_workload_predictor()

    print("All examples completed successfully!")
