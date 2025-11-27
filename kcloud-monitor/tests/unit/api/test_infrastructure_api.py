"""
Tests for Infrastructure API (/api/v1/infrastructure)
"""
import pytest
from unittest.mock import patch


class TestNodesEndpoints:
    """Test nodes endpoints"""

    def test_list_nodes_requires_auth(self, client):
        """Test that listing nodes requires authentication"""
        response = client.get("/api/v1/infrastructure/nodes")
        assert response.status_code == 403

    def test_list_nodes_with_auth(self, client, auth_headers, sample_node_data):
        """Test listing nodes with authentication"""
        with patch('app.crud.get_nodes_list') as mock_nodes:
            mock_nodes.return_value = {
                "nodes": sample_node_data,
                "total_count": len(sample_node_data)
            }

            response = client.get("/api/v1/infrastructure/nodes", headers=auth_headers)
            assert response.status_code == 200
            data = response.json()
            assert "nodes" in data
            assert len(data["nodes"]) == 2

    def test_get_node_detail(self, client, auth_headers, sample_node_data):
        """Test getting node detail"""
        with patch('app.crud.get_node_detail') as mock_node:
            mock_node.return_value = sample_node_data[0]

            response = client.get("/api/v1/infrastructure/nodes/node-1", headers=auth_headers)
            assert response.status_code == 200
            data = response.json()
            assert data["node_name"] == "node-1"
            assert "gpu_count" in data

    def test_get_nodes_summary(self, client, auth_headers):
        """Test getting nodes summary"""
        with patch('app.crud.get_nodes_summary') as mock_summary:
            mock_summary.return_value = {
                "total_nodes": 2,
                "ready_nodes": 2,
                "total_gpus": 4,
                "total_power": 930.5
            }

            response = client.get("/api/v1/infrastructure/nodes/summary", headers=auth_headers)
            assert response.status_code == 200
            data = response.json()
            assert data["total_nodes"] == 2


class TestPodsEndpoints:
    """Test pods endpoints"""

    def test_list_pods(self, client, auth_headers):
        """Test listing pods"""
        with patch('app.crud.get_pods_list') as mock_pods:
            mock_pods.return_value = {
                "pods": [],
                "total_count": 0
            }

            response = client.get("/api/v1/infrastructure/pods", headers=auth_headers)
            assert response.status_code == 200
            data = response.json()
            assert "pods" in data


class TestContainersEndpoints:
    """Test containers endpoints"""

    def test_list_containers(self, client, auth_headers):
        """Test listing containers"""
        with patch('app.crud.get_containers_list') as mock_containers:
            mock_containers.return_value = {
                "containers": [],
                "total_count": 0
            }

            response = client.get("/api/v1/infrastructure/containers", headers=auth_headers)
            assert response.status_code == 200
            data = response.json()
            assert "containers" in data
