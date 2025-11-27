# Energy Prediction API Examples

This document provides practical examples for using the energy prediction API endpoints.

## Table of Contents

- [Predict Container Energy](#predict-container-energy)
- [Calibrate Models](#calibrate-models)
- [Get Calibration Config](#get-calibration-config)

---

## Predict Container Energy

Predict future power consumption for a container based on historical CPU usage.

### Endpoint

```
POST /predict/energy
```

### Request Example

```bash
curl -X POST http://localhost:8001/predict/energy \
  -H "Content-Type: application/json" \
  -d '{
    "container_name": "ml-training-worker",
    "pod_name": "ml-training-job-12345",
    "namespace": "ml-workloads",
    "historical_cpu_cores": [0.8, 0.82, 0.85, 0.83, 0.81, 0.84],
    "container_cpu_request": 1.0,
    "node_current_util": 45.5,
    "node_idle_util": 5.0,
    "containers_on_node": [
      {"cpu_request": 0.5, "cpu_util": 0.6},
      {"cpu_request": 2.0, "cpu_util": 0.4}
    ],
    "prediction_horizon_minutes": 30
  }'
```

### Response Example

```json
{
  "prediction": {
    "container_name": "ml-training-worker",
    "pod_name": "ml-training-job-12345",
    "namespace": "ml-workloads",
    "predicted_power_watts": 25.34,
    "prediction_timestamp": "2025-01-26T10:30:00.123456",
    "prediction_horizon_minutes": 30,
    "confidence_interval": [23.1, 27.5]
  },
  "status": "success"
}
```

### Python Client Example

```python
import requests
from datetime import datetime, timedelta

def predict_container_energy(
    container_name: str,
    pod_name: str,
    namespace: str,
    historical_cpu_cores: list,
    container_cpu_request: float,
    node_current_util: float = 50.0,
    node_idle_util: float = 5.0,
    prediction_horizon_minutes: int = 30
):
    url = "http://localhost:8001/predict/energy"

    payload = {
        "container_name": container_name,
        "pod_name": pod_name,
        "namespace": namespace,
        "historical_cpu_cores": historical_cpu_cores,
        "container_cpu_request": container_cpu_request,
        "node_current_util": node_current_util,
        "node_idle_util": node_idle_util,
        "containers_on_node": [],
        "prediction_horizon_minutes": prediction_horizon_minutes
    }

    response = requests.post(url, json=payload)
    response.raise_for_status()

    return response.json()

# Usage
result = predict_container_energy(
    container_name="nginx-server",
    pod_name="nginx-deployment-abc123",
    namespace="default",
    historical_cpu_cores=[0.5, 0.52, 0.48, 0.51, 0.49, 0.50],
    container_cpu_request=1.0
)

print(f"Predicted power: {result['prediction']['predicted_power_watts']:.2f} watts")
```

---

## Calibrate Models

Calibrate the prediction models using actual measurement data from your environment.

### Endpoint

```
POST /calibrate
```

### Request Example

```bash
curl -X POST http://localhost:8001/calibrate \
  -H "Content-Type: application/json" \
  -d '{
    "container_node_data": [
      {"container_cpu_cores": 0.5, "node_cpu_util_percent": 15.2},
      {"container_cpu_cores": 1.0, "node_cpu_util_percent": 28.5},
      {"container_cpu_cores": 1.5, "node_cpu_util_percent": 42.1},
      {"container_cpu_cores": 2.0, "node_cpu_util_percent": 55.8}
    ],
    "node_power_data": [
      {"node_cpu_util_percent": 10, "node_power_watts": 58},
      {"node_cpu_util_percent": 25, "node_power_watts": 68},
      {"node_cpu_util_percent": 50, "node_power_watts": 85},
      {"node_cpu_util_percent": 75, "node_power_watts": 103},
      {"node_cpu_util_percent": 100, "node_power_watts": 120}
    ]
  }'
```

### Response Example

```json
{
  "calibration": {
    "container_to_node_slope": 22.15,
    "container_to_node_intercept": 4.83,
    "node_util_to_power_slope": 0.68,
    "node_util_to_power_intercept": 52.1,
    "node_idle_power_watts": 58.0,
    "node_max_power_watts": 120.0
  },
  "status": "calibration_successful"
}
```

### Python Client Example

```python
def calibrate_from_measurements(container_node_data, node_power_data):
    url = "http://localhost:8001/calibrate"

    payload = {
        "container_node_data": container_node_data,
        "node_power_data": node_power_data
    }

    response = requests.post(url, json=payload)
    response.raise_for_status()

    return response.json()

# Example: Calibrate using measurement data
container_measurements = [
    {"container_cpu_cores": i * 0.5, "node_cpu_util_percent": 10 + i * 15}
    for i in range(1, 5)
]

power_measurements = [
    {"node_cpu_util_percent": util, "node_power_watts": 55 + util * 0.65}
    for util in [0, 25, 50, 75, 100]
]

calibration_result = calibrate_from_measurements(
    container_measurements,
    power_measurements
)

print("Calibration complete:")
print(f"  C2N slope: {calibration_result['calibration']['container_to_node_slope']:.3f}")
print(f"  U2P slope: {calibration_result['calibration']['node_util_to_power_slope']:.3f}")
```

---

## Get Calibration Config

View current calibration parameters being used by the predictor.

### Endpoint

```
GET /calibration/config
```

### Request Example

```bash
curl -X GET http://localhost:8001/calibration/config
```

### Response Example

```json
{
  "calibration": {
    "container_to_node_slope": 23.993,
    "container_to_node_intercept": 4.5347,
    "node_util_to_power_slope": 0.7254,
    "node_util_to_power_intercept": 53.88,
    "node_idle_power_watts": 53.88,
    "node_max_power_watts": 126.34
  },
  "note": "Using paper defaults from Dell X3430 unless calibrated"
}
```

### Python Client Example

```python
def get_calibration_config():
    url = "http://localhost:8001/calibration/config"
    response = requests.get(url)
    response.raise_for_status()
    return response.json()

# Usage
config = get_calibration_config()
print("Current calibration parameters:")
for key, value in config['calibration'].items():
    print(f"  {key}: {value}")
```

---

## Integration with Prometheus

Example of fetching historical data from Prometheus and using it for prediction:

```python
from prometheus_api_client import PrometheusConnect
from datetime import datetime, timedelta

def fetch_container_cpu_history(
    prometheus_url: str,
    container_name: str,
    pod_name: str,
    namespace: str,
    duration_minutes: int = 90
):
    prom = PrometheusConnect(url=prometheus_url, disable_ssl=True)

    # Query: rate of CPU usage over 1 minute
    query = f'''
        rate(
            kepler_container_joules_total{{
                container_name="{container_name}",
                pod_name="{pod_name}",
                container_namespace="{namespace}"
            }}[1m]
        )
    '''

    end_time = datetime.now()
    start_time = end_time - timedelta(minutes=duration_minutes)

    result = prom.custom_query_range(
        query=query,
        start_time=start_time,
        end_time=end_time,
        step='1m'
    )

    # Extract CPU cores from Joules/sec (Watts)
    # Assuming 1 core ~ 10 watts
    cpu_cores = [float(v[1]) / 10.0 for v in result[0]['values']]

    return cpu_cores

# Example usage
historical_cpu = fetch_container_cpu_history(
    prometheus_url="http://prometheus:9090",
    container_name="app-server",
    pod_name="app-deployment-xyz",
    namespace="production",
    duration_minutes=90
)

# Use for prediction
prediction = predict_container_energy(
    container_name="app-server",
    pod_name="app-deployment-xyz",
    namespace="production",
    historical_cpu_cores=historical_cpu,
    container_cpu_request=2.0
)
```

---

## Notes

- Historical data should have at least 3 data points for ARIMA prediction
- More historical data (90+ minutes) improves prediction accuracy
- Calibration should be performed with measurements from your specific hardware
- Prediction accuracy is best for static and periodic workload patterns
- For unpredictable workloads, confidence intervals will be wider
