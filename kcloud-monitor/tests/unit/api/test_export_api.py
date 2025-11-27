"""
Tests for Export API (/api/v1/export)
"""
import pytest
from unittest.mock import patch


class TestExportEndpoints:
    """Test data export endpoints"""

    def test_export_power_requires_auth(self, client):
        """Test that export requires authentication"""
        response = client.get("/api/v1/export/power")
        assert response.status_code == 403

    def test_export_power_json(self, client, auth_headers):
        """Test exporting power data as JSON"""
        with patch('app.crud.get_unified_power_data') as mock_power:
            mock_power.return_value = {
                "total_power": 2500.0,
                "breakdown": []
            }

            response = client.get(
                "/api/v1/export/power",
                headers=auth_headers,
                params={"format": "json"}
            )
            assert response.status_code == 200
            assert "application/json" in response.headers.get("content-type", "")

    def test_export_power_csv(self, client, auth_headers):
        """Test exporting power data as CSV"""
        with patch('app.crud.get_unified_power_data') as mock_power:
            mock_power.return_value = {
                "total_power": 2500.0,
                "breakdown": [
                    {"gpu_id": "GPU-0", "power": 250.0},
                    {"gpu_id": "GPU-1", "power": 230.0}
                ]
            }

            response = client.get(
                "/api/v1/export/power",
                headers=auth_headers,
                params={"format": "csv"}
            )
            assert response.status_code == 200
            assert "text/csv" in response.headers.get("content-type", "")

    def test_export_metrics(self, client, auth_headers):
        """Test exporting metrics data"""
        with patch('app.crud.get_all_metrics') as mock_metrics:
            mock_metrics.return_value = {
                "metrics": []
            }

            response = client.get("/api/v1/export/metrics", headers=auth_headers)
            assert response.status_code == 200

    def test_export_report(self, client, auth_headers):
        """Test exporting daily report"""
        with patch('app.crud.generate_daily_report') as mock_report:
            mock_report.return_value = {
                "report_date": "2025-01-01",
                "summary": {},
                "details": []
            }

            response = client.get("/api/v1/export/reports/daily", headers=auth_headers)
            assert response.status_code == 200
