"""
Unit tests for Cluster Registry Service

Tests the ClusterRegistry implementation including:
- Single and multi-cluster initialization
- Cluster registration and retrieval
- Health checks
- Prometheus client management
"""

import pytest
import json
from unittest.mock import Mock, patch, MagicMock
from datetime import datetime

from app.services.cluster_registry import ClusterRegistry, ClusterInfo
from app.services.prometheus import PrometheusClient


@pytest.fixture
def mock_settings_single_cluster():
    """Mock settings for single cluster mode"""
    with patch('app.services.cluster_registry.settings') as mock_settings:
        mock_settings.PROMETHEUS_URL = "http://prometheus:9090"
        mock_settings.PROMETHEUS_CLUSTERS = None
        mock_settings.DEFAULT_CLUSTER = "default"
        mock_settings.PROMETHEUS_TIMEOUT = 30
        mock_settings.PROMETHEUS_USERNAME = None
        mock_settings.PROMETHEUS_PASSWORD = None
        mock_settings.PROMETHEUS_CA_BUNDLE = None
        yield mock_settings


@pytest.fixture
def mock_settings_multi_cluster():
    """Mock settings for multi-cluster mode"""
    clusters_config = [
        {
            "name": "cluster1",
            "url": "http://prom1:9090",
            "region": "us-east-1",
            "description": "US East cluster"
        },
        {
            "name": "cluster2",
            "url": "http://prom2:9090",
            "region": "eu-west-1",
            "description": "EU West cluster"
        }
    ]

    with patch('app.services.cluster_registry.settings') as mock_settings:
        mock_settings.PROMETHEUS_URL = "http://prometheus:9090"
        mock_settings.PROMETHEUS_CLUSTERS = json.dumps(clusters_config)
        mock_settings.DEFAULT_CLUSTER = "default"
        mock_settings.PROMETHEUS_TIMEOUT = 30
        mock_settings.PROMETHEUS_USERNAME = None
        mock_settings.PROMETHEUS_PASSWORD = None
        mock_settings.PROMETHEUS_CA_BUNDLE = None
        yield mock_settings


@pytest.fixture
def mock_prometheus_client():
    """Mock PrometheusClient"""
    mock_client = Mock(spec=PrometheusClient)
    mock_client.check_health.return_value = "connected"
    return mock_client


class TestClusterInfo:
    """Test ClusterInfo dataclass"""

    def test_cluster_info_creation(self):
        """Test creating ClusterInfo"""
        cluster = ClusterInfo(
            name="test-cluster",
            url="http://test:9090",
            region="us-east-1",
            description="Test cluster"
        )

        assert cluster.name == "test-cluster"
        assert cluster.url == "http://test:9090"
        assert cluster.region == "us-east-1"
        assert cluster.description == "Test cluster"
        assert cluster.prometheus_client is None
        assert cluster.last_health_check is None
        assert cluster.health_status == "unknown"


class TestClusterRegistrySingleMode:
    """Test ClusterRegistry in single-cluster mode"""

    @patch('app.services.cluster_registry.PrometheusClient')
    @patch('app.services.cluster_registry.prometheus_client')
    def test_init_single_cluster(self, mock_prom_client, mock_prom_class, mock_settings_single_cluster):
        """Test initialization in single-cluster mode"""
        registry = ClusterRegistry()

        assert len(registry._clusters) == 1
        assert "default" in registry._clusters
        assert registry._default_cluster_name == "default"
        assert not registry.is_multi_cluster()

    @patch('app.services.cluster_registry.PrometheusClient')
    @patch('app.services.cluster_registry.prometheus_client')
    def test_get_cluster_single_mode(self, mock_prom_client, mock_prom_class, mock_settings_single_cluster):
        """Test getting cluster in single-cluster mode"""
        registry = ClusterRegistry()

        # Get default cluster
        cluster = registry.get_cluster()
        assert cluster is not None
        assert cluster.name == "default"

        # Get by name
        cluster = registry.get_cluster("default")
        assert cluster is not None
        assert cluster.name == "default"

        # Get nonexistent cluster
        cluster = registry.get_cluster("nonexistent")
        assert cluster is None

    @patch('app.services.cluster_registry.PrometheusClient')
    @patch('app.services.cluster_registry.prometheus_client')
    def test_get_cluster_names_single_mode(self, mock_prom_client, mock_prom_class, mock_settings_single_cluster):
        """Test getting cluster names in single-cluster mode"""
        registry = ClusterRegistry()

        names = registry.get_cluster_names()
        assert names == ["default"]


class TestClusterRegistryMultiMode:
    """Test ClusterRegistry in multi-cluster mode"""

    @patch('app.services.cluster_registry.PrometheusClient')
    def test_init_multi_cluster(self, mock_prom_class, mock_settings_multi_cluster):
        """Test initialization in multi-cluster mode"""
        mock_prom_class.return_value = Mock(spec=PrometheusClient)

        registry = ClusterRegistry()

        assert len(registry._clusters) == 2
        assert "cluster1" in registry._clusters
        assert "cluster2" in registry._clusters
        assert registry.is_multi_cluster()

    @patch('app.services.cluster_registry.PrometheusClient')
    def test_list_clusters(self, mock_prom_class, mock_settings_multi_cluster):
        """Test listing all clusters"""
        mock_prom_class.return_value = Mock(spec=PrometheusClient)

        registry = ClusterRegistry()
        clusters = registry.list_clusters()

        assert len(clusters) == 2
        assert all(isinstance(c, ClusterInfo) for c in clusters)

        cluster_names = [c.name for c in clusters]
        assert "cluster1" in cluster_names
        assert "cluster2" in cluster_names

    @patch('app.services.cluster_registry.PrometheusClient')
    def test_get_cluster_multi_mode(self, mock_prom_class, mock_settings_multi_cluster):
        """Test getting specific cluster in multi-cluster mode"""
        mock_prom_class.return_value = Mock(spec=PrometheusClient)

        registry = ClusterRegistry()

        # Get specific cluster
        cluster1 = registry.get_cluster("cluster1")
        assert cluster1 is not None
        assert cluster1.name == "cluster1"
        assert cluster1.url == "http://prom1:9090"
        assert cluster1.region == "us-east-1"

        cluster2 = registry.get_cluster("cluster2")
        assert cluster2 is not None
        assert cluster2.name == "cluster2"

        # Get default cluster (should be first one)
        default_cluster = registry.get_cluster()
        assert default_cluster is not None

    @patch('app.services.cluster_registry.PrometheusClient')
    def test_get_prometheus_client(self, mock_prom_class, mock_settings_multi_cluster):
        """Test getting Prometheus client for cluster"""
        mock_client = Mock(spec=PrometheusClient)
        mock_prom_class.return_value = mock_client

        registry = ClusterRegistry()

        # Get client for specific cluster
        client = registry.get_prometheus_client("cluster1")
        assert client is not None

        # Get client for nonexistent cluster
        client = registry.get_prometheus_client("nonexistent")
        assert client is None


class TestClusterHealth:
    """Test cluster health checking"""

    @patch('app.services.cluster_registry.PrometheusClient')
    @patch('app.services.cluster_registry.prometheus_client')
    @pytest.mark.asyncio
    async def test_check_cluster_health_success(self, mock_default_client, mock_prom_class, mock_settings_single_cluster):
        """Test successful health check"""
        mock_client = Mock(spec=PrometheusClient)
        mock_client.check_health.return_value = "connected"
        mock_default_client.check_health.return_value = "connected"

        registry = ClusterRegistry()
        registry._clusters["default"].prometheus_client = mock_client

        name, status = await registry.check_cluster_health("default")

        assert name == "default"
        assert status == "connected"

        cluster = registry.get_cluster("default")
        assert cluster.health_status == "connected"
        assert cluster.last_health_check is not None

    @patch('app.services.cluster_registry.PrometheusClient')
    @patch('app.services.cluster_registry.prometheus_client')
    @pytest.mark.asyncio
    async def test_check_cluster_health_failure(self, mock_default_client, mock_prom_class, mock_settings_single_cluster):
        """Test health check failure"""
        mock_client = Mock(spec=PrometheusClient)
        mock_client.check_health.side_effect = Exception("Connection failed")

        registry = ClusterRegistry()
        registry._clusters["default"].prometheus_client = mock_client

        name, status = await registry.check_cluster_health("default")

        assert name == "default"
        assert status == "error"

        cluster = registry.get_cluster("default")
        assert cluster.health_status == "error"

    @patch('app.services.cluster_registry.PrometheusClient')
    @patch('app.services.cluster_registry.prometheus_client')
    @pytest.mark.asyncio
    async def test_check_nonexistent_cluster(self, mock_default_client, mock_prom_class, mock_settings_single_cluster):
        """Test health check for nonexistent cluster"""
        registry = ClusterRegistry()

        name, status = await registry.check_cluster_health("nonexistent")

        assert name == "nonexistent"
        assert status == "not_found"

    @patch('app.services.cluster_registry.PrometheusClient')
    @pytest.mark.asyncio
    async def test_check_all_clusters_health(self, mock_prom_class, mock_settings_multi_cluster):
        """Test checking health of all clusters"""
        mock_client1 = Mock(spec=PrometheusClient)
        mock_client1.check_health.return_value = "connected"

        mock_client2 = Mock(spec=PrometheusClient)
        mock_client2.check_health.return_value = "disconnected"

        mock_prom_class.side_effect = [mock_client1, mock_client2]

        registry = ClusterRegistry()

        health_status = await registry.check_all_clusters_health()

        assert len(health_status) == 2
        assert "cluster1" in health_status
        assert "cluster2" in health_status


class TestClusterSummary:
    """Test cluster summary generation"""

    @patch('app.services.cluster_registry.PrometheusClient')
    def test_get_cluster_summary_multi(self, mock_prom_class, mock_settings_multi_cluster):
        """Test getting cluster summary in multi-cluster mode"""
        mock_prom_class.return_value = Mock(spec=PrometheusClient)

        registry = ClusterRegistry()

        summary = registry.get_cluster_summary()

        assert summary["total_clusters"] == 2
        assert summary["multi_cluster_enabled"] is True
        assert len(summary["clusters"]) == 2

        # Check cluster details
        cluster_names = [c["name"] for c in summary["clusters"]]
        assert "cluster1" in cluster_names
        assert "cluster2" in cluster_names

    @patch('app.services.cluster_registry.PrometheusClient')
    @patch('app.services.cluster_registry.prometheus_client')
    def test_get_cluster_summary_single(self, mock_default_client, mock_prom_class, mock_settings_single_cluster):
        """Test getting cluster summary in single-cluster mode"""
        registry = ClusterRegistry()

        summary = registry.get_cluster_summary()

        assert summary["total_clusters"] == 1
        assert summary["multi_cluster_enabled"] is False
        assert len(summary["clusters"]) == 1


class TestClusterRegistryHelpers:
    """Test helper methods"""

    @patch('app.services.cluster_registry.PrometheusClient')
    @patch('app.services.cluster_registry.prometheus_client')
    def test_get_default_cluster_name(self, mock_default_client, mock_prom_class, mock_settings_single_cluster):
        """Test getting default cluster name"""
        registry = ClusterRegistry()

        default_name = registry.get_default_cluster_name()
        assert default_name == "default"

    @patch('app.services.cluster_registry.PrometheusClient')
    @patch('app.services.cluster_registry.prometheus_client')
    def test_is_multi_cluster_false(self, mock_default_client, mock_prom_class, mock_settings_single_cluster):
        """Test is_multi_cluster returns False for single cluster"""
        registry = ClusterRegistry()

        assert not registry.is_multi_cluster()

    @patch('app.services.cluster_registry.PrometheusClient')
    def test_is_multi_cluster_true(self, mock_prom_class, mock_settings_multi_cluster):
        """Test is_multi_cluster returns True for multiple clusters"""
        mock_prom_class.return_value = Mock(spec=PrometheusClient)

        registry = ClusterRegistry()

        assert registry.is_multi_cluster()


class TestClusterRegistryErrors:
    """Test error handling"""

    @patch('app.services.cluster_registry.PrometheusClient')
    @patch('app.services.cluster_registry.prometheus_client')
    def test_invalid_json_fallback(self, mock_default_client, mock_prom_class):
        """Test fallback to single cluster on invalid JSON"""
        with patch('app.services.cluster_registry.settings') as mock_settings:
            mock_settings.PROMETHEUS_URL = "http://prometheus:9090"
            mock_settings.PROMETHEUS_CLUSTERS = "invalid json {"
            mock_settings.DEFAULT_CLUSTER = "default"
            mock_settings.PROMETHEUS_TIMEOUT = 30
            mock_settings.PROMETHEUS_USERNAME = None
            mock_settings.PROMETHEUS_PASSWORD = None
            mock_settings.PROMETHEUS_CA_BUNDLE = None

            registry = ClusterRegistry()

            # Should fall back to single cluster mode
            assert len(registry._clusters) == 1
            assert "default" in registry._clusters

    @patch('app.services.cluster_registry.PrometheusClient')
    def test_skip_invalid_cluster_config(self, mock_prom_class):
        """Test skipping invalid cluster configurations"""
        invalid_clusters = [
            {"name": "cluster1", "url": "http://prom1:9090"},  # Valid
            {"name": "cluster2"},  # Missing URL
            {"url": "http://prom3:9090"},  # Missing name
            {"name": "cluster3", "url": "http://prom3:9090"}  # Valid
        ]

        with patch('app.services.cluster_registry.settings') as mock_settings:
            mock_settings.PROMETHEUS_URL = "http://prometheus:9090"
            mock_settings.PROMETHEUS_CLUSTERS = json.dumps(invalid_clusters)
            mock_settings.DEFAULT_CLUSTER = "default"
            mock_settings.PROMETHEUS_TIMEOUT = 30
            mock_settings.PROMETHEUS_USERNAME = None
            mock_settings.PROMETHEUS_PASSWORD = None
            mock_settings.PROMETHEUS_CA_BUNDLE = None

            mock_prom_class.return_value = Mock(spec=PrometheusClient)

            registry = ClusterRegistry()

            # Should only register valid clusters
            assert len(registry._clusters) == 2
            assert "cluster1" in registry._clusters
            assert "cluster3" in registry._clusters
            assert "cluster2" not in registry._clusters
