"""
Tests for Monitoring API (/api/v1/monitoring)
"""
import pytest
from unittest.mock import patch


class TestPowerEndpoints:
    """Test power monitoring endpoints"""

    def test_get_unified_power_requires_auth(self, client):
        """Test that getting unified power requires authentication"""
        response = client.get("/api/v1/monitoring/power/unified")
        assert response.status_code == 403

    def test_get_unified_power_with_auth(self, client, auth_headers):
        """Test getting unified power data"""
        with patch('app.crud.get_unified_power_data') as mock_power:
            mock_power.return_value = {
                "total_power": 2500.0,
                "gpu_power": 1800.0,
                "node_power": 700.0,
                "breakdown": []
            }

            response = client.get("/api/v1/monitoring/power/unified", headers=auth_headers)
            assert response.status_code == 200
            data = response.json()
            assert "total_power" in data

    def test_get_power_efficiency(self, client, auth_headers):
        """Test getting power efficiency metrics"""
        with patch('app.crud.get_power_efficiency') as mock_efficiency:
            mock_efficiency.return_value = {
                "pue": 1.2,
                "gpu_utilization": 75.0,
                "power_per_gpu": 250.0
            }

            response = client.get("/api/v1/monitoring/power/efficiency", headers=auth_headers)
            assert response.status_code == 200
            data = response.json()
            assert "pue" in data


class TestTimeseriesEndpoints:
    """Test timeseries monitoring endpoints"""

    def test_get_power_timeseries(self, client, auth_headers):
        """Test getting power timeseries data"""
        with patch('app.crud.get_power_timeseries') as mock_timeseries:
            mock_timeseries.return_value = {
                "data": [],
                "start": 1640000000,
                "end": 1640003600,
                "step": 60
            }

            response = client.get("/api/v1/monitoring/timeseries/power", headers=auth_headers)
            assert response.status_code == 200
            data = response.json()
            assert "data" in data

    def test_get_metrics_timeseries(self, client, auth_headers):
        """Test getting metrics timeseries data"""
        with patch('app.crud.get_metrics_timeseries') as mock_timeseries:
            mock_timeseries.return_value = {
                "data": [],
                "metrics": ["utilization", "temperature"]
            }

            response = client.get("/api/v1/monitoring/timeseries/metrics", headers=auth_headers)
            assert response.status_code == 200
            data = response.json()
            assert "data" in data
