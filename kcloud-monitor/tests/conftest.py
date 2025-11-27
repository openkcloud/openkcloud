"""
Pytest configuration and fixtures for all tests
"""
import pytest
from datetime import timedelta
from fastapi.testclient import TestClient
from unittest.mock import Mock

from app.main import app
from app.auth import create_access_token
from app.config import Settings
from app.deps import get_settings


# Test settings
@pytest.fixture
def test_settings():
    """Override settings for testing"""
    return Settings(
        PROMETHEUS_URL="http://test-prometheus:9090",
        API_AUTH_USERNAME="testuser",
        API_AUTH_PASSWORD="testpass",
        JWT_SECRET_KEY="test-secret-key-for-testing-only",
        JWT_ALGORITHM="HS256",
        JWT_ACCESS_TOKEN_EXPIRE_MINUTES=30
    )


@pytest.fixture
def client(test_settings):
    """Create test client with overridden settings"""
    app.dependency_overrides[get_settings] = lambda: test_settings
    client = TestClient(app)
    yield client
    app.dependency_overrides.clear()


@pytest.fixture
def auth_token(test_settings):
    """Generate valid JWT token for testing"""
    token = create_access_token(
        data={"sub": test_settings.API_AUTH_USERNAME},
        expires_delta=timedelta(minutes=30),
        settings=test_settings
    )
    return token


@pytest.fixture
def auth_headers(auth_token):
    """Generate authorization headers with valid JWT token"""
    return {"Authorization": f"Bearer {auth_token}"}


@pytest.fixture
def mock_prometheus_response():
    """Mock Prometheus query response"""
    return {
        "status": "success",
        "data": {
            "resultType": "vector",
            "result": []
        }
    }


@pytest.fixture
def sample_gpu_data():
    """Sample GPU data for testing"""
    return [
        {
            "gpu_id": "GPU-0",
            "node": "node-1",
            "gpu_index": 0,
            "model": "NVIDIA A100",
            "power_usage": 250.5,
            "temperature": 65.0,
            "utilization": 85.0,
            "memory_used": 32000,
            "memory_total": 40960
        },
        {
            "gpu_id": "GPU-1",
            "node": "node-1",
            "gpu_index": 1,
            "model": "NVIDIA A100",
            "power_usage": 230.0,
            "temperature": 62.0,
            "utilization": 75.0,
            "memory_used": 28000,
            "memory_total": 40960
        }
    ]


@pytest.fixture
def sample_node_data():
    """Sample node data for testing"""
    return [
        {
            "node_name": "node-1",
            "ip": "192.168.1.10",
            "status": "Ready",
            "gpu_count": 2,
            "total_power": 480.5
        },
        {
            "node_name": "node-2",
            "ip": "192.168.1.11",
            "status": "Ready",
            "gpu_count": 2,
            "total_power": 450.0
        }
    ]
