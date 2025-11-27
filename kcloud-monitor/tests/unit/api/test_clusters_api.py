"""
Tests for Clusters API (/api/v1/clusters)
"""
import pytest
from unittest.mock import patch


class TestClustersEndpoints:
    """Test clusters endpoints"""

    def test_list_clusters_requires_auth(self, client):
        """Test that listing clusters requires authentication"""
        response = client.get("/api/v1/clusters")
        assert response.status_code == 403

    def test_list_clusters_with_auth(self, client, auth_headers):
        """Test listing clusters with authentication"""
        with patch('app.services.cluster_registry.list_clusters') as mock_clusters:
            mock_clusters.return_value = [
                {"name": "cluster-1", "prometheus_url": "http://prom1:9090"},
                {"name": "cluster-2", "prometheus_url": "http://prom2:9090"}
            ]

            response = client.get("/api/v1/clusters", headers=auth_headers)
            assert response.status_code == 200
            data = response.json()
            assert "clusters" in data

    def test_get_cluster_detail(self, client, auth_headers):
        """Test getting cluster detail"""
        with patch('app.services.cluster_registry.get_cluster') as mock_cluster:
            mock_cluster.return_value = {
                "name": "cluster-1",
                "prometheus_url": "http://prom1:9090"
            }

            response = client.get("/api/v1/clusters/cluster-1", headers=auth_headers)
            assert response.status_code == 200
            data = response.json()
            assert data["name"] == "cluster-1"

    def test_get_cluster_summary(self, client, auth_headers):
        """Test getting cluster summary"""
        with patch('app.services.cluster_registry.get_cluster_summary') as mock_summary:
            mock_summary.return_value = {
                "cluster_name": "cluster-1",
                "total_nodes": 5,
                "total_gpus": 10,
                "health_status": "healthy"
            }

            response = client.get("/api/v1/clusters/cluster-1/summary", headers=auth_headers)
            assert response.status_code == 200
            data = response.json()
            assert data["cluster_name"] == "cluster-1"
