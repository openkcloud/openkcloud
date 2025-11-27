"""
Unit tests for Prometheus Client

Tests the PrometheusClient implementation including:
- Query and range query methods
- Error handling and exceptions
- Authentication
- Health check
- Query building
"""

import pytest
import responses
from datetime import datetime, timedelta
from requests.exceptions import Timeout, HTTPError, RequestException

from app.services.prometheus import PrometheusClient, PrometheusException
from app.config import Settings


@pytest.fixture
def settings():
    """Create test settings"""
    return Settings(
        PROMETHEUS_URL="http://test-prometheus:9090",
        PROMETHEUS_TIMEOUT=30,
        PROMETHEUS_USERNAME="",
        PROMETHEUS_PASSWORD=""
    )


@pytest.fixture
def settings_with_auth():
    """Create test settings with authentication"""
    return Settings(
        PROMETHEUS_URL="http://test-prometheus:9090/",  # Test URL stripping
        PROMETHEUS_TIMEOUT=30,
        PROMETHEUS_USERNAME="testuser",
        PROMETHEUS_PASSWORD="testpass"
    )


@pytest.fixture
def prometheus_client(settings):
    """Create PrometheusClient instance"""
    return PrometheusClient(settings)


@pytest.fixture
def prometheus_client_with_auth(settings_with_auth):
    """Create PrometheusClient instance with auth"""
    return PrometheusClient(settings_with_auth)


class TestPrometheusClientInitialization:
    """Test PrometheusClient initialization"""

    def test_client_init_basic(self, prometheus_client):
        """Test basic client initialization"""
        assert prometheus_client.base_url == "http://test-prometheus:9090"
        assert prometheus_client.timeout == 30
        assert prometheus_client.auth is None
        assert prometheus_client.verify is True

    def test_client_init_with_auth(self, prometheus_client_with_auth):
        """Test client initialization with authentication"""
        assert prometheus_client_with_auth.base_url == "http://test-prometheus:9090"
        assert prometheus_client_with_auth.auth == ("testuser", "testpass")

    def test_client_init_strips_trailing_slash(self, settings_with_auth):
        """Test that trailing slash is stripped from URL"""
        client = PrometheusClient(settings_with_auth)
        assert client.base_url == "http://test-prometheus:9090"
        assert not client.base_url.endswith("/")


class TestPrometheusQuery:
    """Test Prometheus query methods"""

    @responses.activate
    def test_query_success(self, prometheus_client):
        """Test successful instant query"""
        query = "up"
        expected_response = {
            "status": "success",
            "data": {
                "resultType": "vector",
                "result": [
                    {
                        "metric": {"__name__": "up", "job": "prometheus"},
                        "value": [1609459200, "1"]
                    }
                ]
            }
        }

        responses.add(
            responses.GET,
            "http://test-prometheus:9090/api/v1/query",
            json=expected_response,
            status=200
        )

        result = prometheus_client.query(query)
        assert result == expected_response
        assert result["status"] == "success"
        assert len(responses.calls) == 1
        assert responses.calls[0].request.params["query"] == query

    @responses.activate
    def test_query_with_params(self, prometheus_client):
        """Test query with parameters"""
        query = 'rate(kepler_node_platform_joules_total[5m])'
        expected_response = {"status": "success", "data": {"result": []}}

        responses.add(
            responses.GET,
            "http://test-prometheus:9090/api/v1/query",
            json=expected_response,
            status=200
        )

        result = prometheus_client.query(query)
        assert result["status"] == "success"

    @responses.activate
    def test_query_timeout(self, prometheus_client):
        """Test query timeout handling"""
        query = "up"

        responses.add(
            responses.GET,
            "http://test-prometheus:9090/api/v1/query",
            body=Timeout("Connection timed out")
        )

        with pytest.raises(PrometheusException) as exc_info:
            prometheus_client.query(query)

        assert "timed out" in str(exc_info.value).lower()

    @responses.activate
    def test_query_http_error(self, prometheus_client):
        """Test query HTTP error handling"""
        query = "up"

        responses.add(
            responses.GET,
            "http://test-prometheus:9090/api/v1/query",
            json={"error": "bad query"},
            status=400
        )

        with pytest.raises(PrometheusException) as exc_info:
            prometheus_client.query(query)

        assert "HTTP error" in str(exc_info.value)

    @responses.activate
    def test_query_connection_error(self, prometheus_client):
        """Test query connection error handling"""
        query = "up"

        responses.add(
            responses.GET,
            "http://test-prometheus:9090/api/v1/query",
            body=RequestException("Connection refused")
        )

        with pytest.raises(PrometheusException) as exc_info:
            prometheus_client.query(query)

        assert "error occurred" in str(exc_info.value).lower()


class TestPrometheusQueryRange:
    """Test Prometheus range query methods"""

    @responses.activate
    def test_query_range_success(self, prometheus_client):
        """Test successful range query"""
        query = "up"
        start = datetime(2024, 1, 1, 0, 0, 0)
        end = datetime(2024, 1, 1, 1, 0, 0)
        step = "15s"

        expected_response = {
            "status": "success",
            "data": {
                "resultType": "matrix",
                "result": [
                    {
                        "metric": {"__name__": "up"},
                        "values": [
                            [1704067200, "1"],
                            [1704067215, "1"]
                        ]
                    }
                ]
            }
        }

        responses.add(
            responses.GET,
            "http://test-prometheus:9090/api/v1/query_range",
            json=expected_response,
            status=200
        )

        result = prometheus_client.query_range(query, start, end, step)
        assert result == expected_response
        assert result["status"] == "success"

        # Verify request parameters
        request = responses.calls[0].request
        assert request.params["query"] == query
        assert request.params["start"] == "2024-01-01T00:00:00Z"
        assert request.params["end"] == "2024-01-01T01:00:00Z"
        assert request.params["step"] == step

    @responses.activate
    def test_query_range_error(self, prometheus_client):
        """Test range query error handling"""
        query = "up"
        start = datetime(2024, 1, 1, 0, 0, 0)
        end = datetime(2024, 1, 1, 1, 0, 0)
        step = "15s"

        responses.add(
            responses.GET,
            "http://test-prometheus:9090/api/v1/query_range",
            json={"error": "invalid time range"},
            status=422
        )

        with pytest.raises(PrometheusException) as exc_info:
            prometheus_client.query_range(query, start, end, step)

        assert "HTTP error" in str(exc_info.value)


class TestPrometheusLabelValues:
    """Test Prometheus label values retrieval"""

    @responses.activate
    def test_get_label_values_success(self, prometheus_client):
        """Test successful label values retrieval"""
        label = "instance"
        expected_response = {
            "status": "success",
            "data": ["localhost:9090", "localhost:9091"]
        }

        responses.add(
            responses.GET,
            f"http://test-prometheus:9090/api/v1/label/{label}/values",
            json=expected_response,
            status=200
        )

        result = prometheus_client.get_label_values(label)
        assert result == ["localhost:9090", "localhost:9091"]

    @responses.activate
    def test_get_label_values_empty(self, prometheus_client):
        """Test label values with empty result"""
        label = "nonexistent"
        expected_response = {
            "status": "success",
            "data": []
        }

        responses.add(
            responses.GET,
            f"http://test-prometheus:9090/api/v1/label/{label}/values",
            json=expected_response,
            status=200
        )

        result = prometheus_client.get_label_values(label)
        assert result == []

    @responses.activate
    def test_get_label_values_error(self, prometheus_client):
        """Test label values error handling"""
        label = "test"

        responses.add(
            responses.GET,
            f"http://test-prometheus:9090/api/v1/label/{label}/values",
            json={"error": "server error"},
            status=500
        )

        result = prometheus_client.get_label_values(label)
        assert result == []  # Should return empty list on error


class TestPrometheusHealth:
    """Test Prometheus health check"""

    @responses.activate
    def test_health_check_success(self, prometheus_client):
        """Test successful health check"""
        responses.add(
            responses.GET,
            "http://test-prometheus:9090/-/healthy",
            status=200
        )

        result = prometheus_client.check_health()
        assert result == "connected"

    @responses.activate
    def test_health_check_fallback_query(self, prometheus_client):
        """Test health check fallback to query"""
        # Health endpoint fails
        responses.add(
            responses.GET,
            "http://test-prometheus:9090/-/healthy",
            status=404
        )

        # But query succeeds
        responses.add(
            responses.GET,
            "http://test-prometheus:9090/api/v1/query",
            json={"status": "success", "data": {"result": []}},
            status=200
        )

        result = prometheus_client.check_health()
        assert result == "connected"

    @responses.activate
    def test_health_check_disconnected(self, prometheus_client):
        """Test health check when disconnected"""
        # Both health and query fail
        responses.add(
            responses.GET,
            "http://test-prometheus:9090/-/healthy",
            body=RequestException("Connection refused")
        )

        responses.add(
            responses.GET,
            "http://test-prometheus:9090/api/v1/query",
            body=RequestException("Connection refused")
        )

        result = prometheus_client.check_health()
        assert result == "disconnected"


class TestPrometheusQueryBuilder:
    """Test Prometheus query building"""

    def test_build_query_basic(self, prometheus_client):
        """Test building a basic query"""
        query = prometheus_client.build_query("gpu_power")
        assert query == "rate(kepler_node_platform_joules_total[5m])"

    def test_build_query_with_instance_rate(self, prometheus_client):
        """Test building query with instance filter for rate query"""
        query = prometheus_client.build_query("gpu_power", instance="medgew01")
        assert 'exported_instance="medgew01"' in query
        assert "rate(" in query

    def test_build_query_with_instance_simple(self, prometheus_client):
        """Test building query with instance filter for simple metric"""
        query = prometheus_client.build_query("gpu_utilization", instance="medgew01")
        assert 'exported_instance="medgew01"' in query

    def test_build_query_invalid_metric(self, prometheus_client):
        """Test building query with invalid metric"""
        with pytest.raises(ValueError) as exc_info:
            prometheus_client.build_query("nonexistent_metric")

        assert "not found" in str(exc_info.value)


class TestPrometheusAuthentication:
    """Test Prometheus client with authentication"""

    @responses.activate
    def test_query_with_auth(self, prometheus_client_with_auth):
        """Test query with authentication"""
        query = "up"

        responses.add(
            responses.GET,
            "http://test-prometheus:9090/api/v1/query",
            json={"status": "success", "data": {"result": []}},
            status=200
        )

        result = prometheus_client_with_auth.query(query)
        assert result["status"] == "success"

        # Verify auth was sent
        request = responses.calls[0].request
        assert request.headers.get("Authorization") is not None

    @responses.activate
    def test_query_auth_failure(self, prometheus_client_with_auth):
        """Test query with authentication failure"""
        query = "up"

        responses.add(
            responses.GET,
            "http://test-prometheus:9090/api/v1/query",
            json={"error": "unauthorized"},
            status=401
        )

        with pytest.raises(PrometheusException) as exc_info:
            prometheus_client_with_auth.query(query)

        assert "HTTP error" in str(exc_info.value)
