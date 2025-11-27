"""
Unit tests for CSV Exporter

Tests CSV export functionality including:
- Power data export
- Metrics export
- Timeseries export
- CSV formatting
"""

import pytest
import csv
import io
from datetime import datetime

from app.services.exporters.csv_exporter import (
    export_to_csv,
    export_power_to_csv,
    export_metrics_to_csv,
    export_timeseries_to_csv
)


class TestCSVExporter:
    """Test CSV export functions"""

    def test_export_to_csv_basic(self):
        """Test basic CSV export"""
        data = [
            {"name": "item1", "value": 100},
            {"name": "item2", "value": 200}
        ]

        csv_content = export_to_csv(data)

        # Parse CSV
        reader = csv.DictReader(io.StringIO(csv_content))
        rows = list(reader)

        assert len(rows) == 2
        assert rows[0]["name"] == "item1"
        assert rows[0]["value"] == "100"
        assert rows[1]["name"] == "item2"
        assert rows[1]["value"] == "200"

    def test_export_to_csv_empty(self):
        """Test CSV export with empty data"""
        csv_content = export_to_csv([])

        # Should return minimal CSV with no data rows
        assert "," not in csv_content or csv_content.count("\n") <= 1

    def test_export_power_to_csv(self):
        """Test power data export to CSV"""
        power_data = {
            "timestamp": "2024-01-01T00:00:00Z",
            "total_power_watts": 500.0,
            "accelerators": {
                "gpus": [
                    {
                        "gpu_id": "GPU-0",
                        "node": "node1",
                        "power_watts": 250.0,
                        "utilization_percent": 75.5
                    }
                ]
            }
        }

        csv_content = export_power_to_csv(power_data)

        assert "GPU-0" in csv_content
        assert "node1" in csv_content
        assert "250" in csv_content or "250.0" in csv_content

    def test_export_metrics_to_csv(self):
        """Test metrics export to CSV"""
        metrics_data = {
            "timestamp": "2024-01-01T00:00:00Z",
            "metrics": [
                {
                    "resource_id": "gpu-0",
                    "utilization_percent": 80.0,
                    "temperature_celsius": 65.0
                },
                {
                    "resource_id": "gpu-1",
                    "utilization_percent": 60.0,
                    "temperature_celsius": 55.0
                }
            ]
        }

        csv_content = export_metrics_to_csv(metrics_data)

        assert "resource_id" in csv_content
        assert "utilization_percent" in csv_content
        assert "gpu-0" in csv_content
        assert "gpu-1" in csv_content

    def test_export_timeseries_to_csv(self):
        """Test timeseries export to CSV"""
        timeseries_data = {
            "metric_name": "power_watts",
            "data": [
                {
                    "timestamp": "2024-01-01T00:00:00Z",
                    "value": 100.0
                },
                {
                    "timestamp": "2024-01-01T00:01:00Z",
                    "value": 110.0
                }
            ]
        }

        csv_content = export_timeseries_to_csv(timeseries_data)

        assert "timestamp" in csv_content
        assert "2024-01-01T00:00:00Z" in csv_content
        assert "100" in csv_content or "100.0" in csv_content

    def test_export_to_csv_unicode(self):
        """Test CSV export with unicode characters"""
        data = [
            {"name": "测试", "value": 100},
            {"name": "テスト", "value": 200}
        ]

        csv_content = export_to_csv(data)

        # Should handle unicode properly
        assert "测试" in csv_content
        assert "テスト" in csv_content

    def test_export_to_csv_special_characters(self):
        """Test CSV export with special characters"""
        data = [
            {"name": "item,with,commas", "value": 100},
            {"name": 'item"with"quotes', "value": 200}
        ]

        csv_content = export_to_csv(data)

        # CSV should properly escape special characters
        reader = csv.DictReader(io.StringIO(csv_content))
        rows = list(reader)

        assert len(rows) == 2
        assert "commas" in rows[0]["name"]
        assert "quotes" in rows[1]["name"]

    def test_export_power_to_csv_no_gpus(self):
        """Test power export when no GPUs present"""
        power_data = {
            "timestamp": "2024-01-01T00:00:00Z",
            "total_power_watts": 0.0,
            "accelerators": {
                "gpus": []
            }
        }

        csv_content = export_power_to_csv(power_data)

        # Should handle empty GPU list gracefully
        assert csv_content is not None
        assert len(csv_content) > 0
