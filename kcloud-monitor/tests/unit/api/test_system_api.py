"""
Tests for System API (/api/v1/system)
"""
import pytest
from unittest.mock import patch, Mock


class TestHealthCheck:
    """Test /api/v1/system/health endpoint"""

    def test_health_check_success(self, client):
        """Test health check returns healthy status"""
        with patch('app.services.prometheus_client.check_health') as mock_health:
            mock_health.return_value = "connected"

            response = client.get("/api/v1/system/health")
            assert response.status_code == 200
            data = response.json()
            assert data["status"] == "healthy"
            assert "version" in data
            assert "prometheus" in data
            assert "cache" in data

    def test_health_check_no_auth_required(self, client):
        """Test health check does not require authentication"""
        response = client.get("/api/v1/system/health")
        assert response.status_code == 200  # Not 401 or 403


class TestSystemInfo:
    """Test /api/v1/system/info endpoint"""

    def test_system_info_success(self, client):
        """Test getting system information"""
        with patch('app.services.prometheus_client.get_label_values') as mock_labels, \
             patch('app.services.prometheus_client.build_query') as mock_query, \
             patch('app.services.prometheus_client.query') as mock_prometheus:

            mock_labels.return_value = ["instance-1", "instance-2"]
            mock_query.return_value = "dcgm_gpu_power"
            mock_prometheus.return_value = {"data": {"result": [{"metric": {}}]}}

            response = client.get("/api/v1/system/info")
            assert response.status_code == 200
            data = response.json()
            assert "version" in data
            assert "instances" in data

    def test_system_info_no_auth_required(self, client):
        """Test system info does not require authentication"""
        with patch('app.services.prometheus_client.get_label_values') as mock_labels:
            mock_labels.return_value = []
            response = client.get("/api/v1/system/info")
            assert response.status_code == 200


class TestMetrics:
    """Test /api/v1/system/metrics endpoint"""

    def test_metrics_endpoint(self, client):
        """Test Prometheus metrics endpoint"""
        response = client.get("/api/v1/system/metrics")
        assert response.status_code == 200
        # Prometheus metrics should be in text format
        assert "text/plain" in response.headers.get("content-type", "")

    def test_metrics_no_auth_required(self, client):
        """Test metrics endpoint does not require authentication"""
        response = client.get("/api/v1/system/metrics")
        assert response.status_code == 200
