"""
Tests for Hardware API (/api/v1/hardware)
"""
import pytest
from unittest.mock import patch


class TestHardwareEndpoints:
    """Test hardware (IPMI) endpoints"""

    def test_list_hardware_requires_auth(self, client):
        """Test that listing hardware requires authentication"""
        response = client.get("/api/v1/hardware/servers")
        assert response.status_code == 403

    def test_list_servers_with_auth(self, client, auth_headers):
        """Test listing servers with authentication"""
        with patch('app.crud.get_servers_list') as mock_servers:
            mock_servers.return_value = {
                "servers": [
                    {
                        "server_id": "server-1",
                        "hostname": "node-1",
                        "ipmi_address": "192.168.1.100",
                        "power_usage": 350.0,
                        "temperature": 45.0,
                        "status": "online"
                    }
                ],
                "total_count": 1
            }

            response = client.get("/api/v1/hardware/servers", headers=auth_headers)
            assert response.status_code == 200
            data = response.json()
            assert "servers" in data

    def test_get_server_detail(self, client, auth_headers):
        """Test getting server detail"""
        with patch('app.crud.get_server_detail') as mock_server:
            mock_server.return_value = {
                "server_id": "server-1",
                "hostname": "node-1",
                "power_usage": 350.0,
                "status": "online"
            }

            response = client.get("/api/v1/hardware/servers/server-1", headers=auth_headers)
            assert response.status_code == 200
            data = response.json()
            assert data["server_id"] == "server-1"
