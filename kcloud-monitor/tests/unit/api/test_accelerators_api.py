"""
Tests for Accelerators API (/api/v1/accelerators)
"""
import pytest
from unittest.mock import patch


class TestGPUEndpoints:
    """Test GPU-related endpoints"""

    def test_list_gpus_requires_auth(self, client):
        """Test that listing GPUs requires authentication"""
        response = client.get("/api/v1/accelerators/gpus")
        assert response.status_code == 403  # JWT returns 403 when no token

    def test_list_gpus_with_auth(self, client, auth_headers, sample_gpu_data):
        """Test listing GPUs with valid authentication"""
        with patch('app.crud.get_gpu_list') as mock_get_gpus:
            mock_get_gpus.return_value = {
                "gpus": sample_gpu_data,
                "total_count": len(sample_gpu_data)
            }

            response = client.get("/api/v1/accelerators/gpus", headers=auth_headers)
            assert response.status_code == 200
            data = response.json()
            assert "gpus" in data
            assert len(data["gpus"]) == 2

    def test_get_gpu_detail(self, client, auth_headers, sample_gpu_data):
        """Test getting GPU detail"""
        with patch('app.crud.get_gpu_detail') as mock_get_gpu:
            mock_get_gpu.return_value = sample_gpu_data[0]

            response = client.get("/api/v1/accelerators/gpus/GPU-0", headers=auth_headers)
            assert response.status_code == 200
            data = response.json()
            assert data["gpu_id"] == "GPU-0"
            assert data["model"] == "NVIDIA A100"

    def test_get_gpu_metrics(self, client, auth_headers):
        """Test getting GPU metrics"""
        with patch('app.crud.get_gpu_metrics') as mock_metrics:
            mock_metrics.return_value = {
                "gpu_id": "GPU-0",
                "utilization": 85.0,
                "memory_used": 32000,
                "memory_total": 40960,
                "temperature": 65.0
            }

            response = client.get("/api/v1/accelerators/gpus/GPU-0/metrics", headers=auth_headers)
            assert response.status_code == 200
            data = response.json()
            assert data["gpu_id"] == "GPU-0"
            assert "utilization" in data


class TestAcceleratorsSummary:
    """Test accelerators summary endpoints"""

    def test_get_summary(self, client, auth_headers):
        """Test getting accelerators summary"""
        with patch('app.crud.get_accelerators_summary') as mock_summary:
            mock_summary.return_value = {
                "total_gpus": 4,
                "active_gpus": 3,
                "total_power": 900.0,
                "average_utilization": 75.0
            }

            response = client.get("/api/v1/accelerators/summary", headers=auth_headers)
            assert response.status_code == 200
            data = response.json()
            assert data["total_gpus"] == 4
            assert "average_utilization" in data
