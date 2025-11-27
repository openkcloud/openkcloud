"""
CSV Exporter - Export data to CSV format.

This module provides CSV export functionality for monitoring data.
"""

import csv
import io
from typing import List, Dict, Any, Optional
from datetime import datetime


def export_to_csv(
    data: List[Dict[str, Any]],
    columns: Optional[List[str]] = None,
    include_header: bool = True
) -> str:
    """
    Export data to CSV format.

    Args:
        data: List of dictionaries to export
        columns: Column names (if None, uses keys from first row)
        include_header: Whether to include header row

    Returns:
        CSV string
    """
    if not data:
        return ""

    # Determine columns
    if columns is None:
        columns = list(data[0].keys())

    # Create CSV in memory
    output = io.StringIO()
    writer = csv.DictWriter(output, fieldnames=columns, extrasaction='ignore')

    if include_header:
        writer.writeheader()

    writer.writerows(data)

    return output.getvalue()


def export_power_to_csv(power_data: Dict[str, Any]) -> str:
    """
    Export power monitoring data to CSV format.

    Args:
        power_data: Power data dictionary

    Returns:
        CSV string
    """
    rows = []

    # Add summary row
    if 'summary' in power_data:
        summary = power_data['summary']
        rows.append({
            'type': 'summary',
            'timestamp': power_data.get('timestamp', datetime.utcnow().isoformat()),
            'total_power_watts': summary.get('total_power_watts', 0),
            'resource_count': summary.get('resource_count', 0),
            'avg_power_watts': summary.get('avg_power_watts', 0),
            'max_power_watts': summary.get('max_power_watts', 0),
            'min_power_watts': summary.get('min_power_watts', 0)
        })

    # Add breakdown data
    if 'breakdown' in power_data:
        for item in power_data['breakdown']:
            rows.append({
                'type': 'breakdown',
                'timestamp': power_data.get('timestamp', datetime.utcnow().isoformat()),
                'name': item.get('name', ''),
                'power_watts': item.get('power_watts', 0),
                'percentage': item.get('percentage', 0),
                'resource_count': item.get('resource_count', 0),
                'resource_type': item.get('resource_type', '')
            })

    # Add accelerators data
    if 'accelerators' in power_data:
        acc = power_data['accelerators']
        rows.append({
            'type': 'accelerators',
            'timestamp': power_data.get('timestamp', datetime.utcnow().isoformat()),
            'total_power_watts': acc.get('total_power_watts', 0),
            'gpu_count': acc.get('gpu_count', 0),
            'npu_count': acc.get('npu_count', 0)
        })

    # Add infrastructure data
    if 'infrastructure' in power_data:
        infra = power_data['infrastructure']
        rows.append({
            'type': 'infrastructure',
            'timestamp': power_data.get('timestamp', datetime.utcnow().isoformat()),
            'total_power_watts': infra.get('total_power_watts', 0),
            'node_count': infra.get('node_count', 0),
            'pod_count': infra.get('pod_count', 0)
        })

    return export_to_csv(rows)


def export_metrics_to_csv(metrics_data: Dict[str, Any]) -> str:
    """
    Export performance metrics data to CSV format.

    Args:
        metrics_data: Metrics data dictionary

    Returns:
        CSV string
    """
    rows = []

    # Handle different metric formats
    if 'metrics' in metrics_data:
        for metric in metrics_data['metrics']:
            rows.append({
                'timestamp': metrics_data.get('timestamp', datetime.utcnow().isoformat()),
                'resource_id': metric.get('resource_id', ''),
                'resource_type': metric.get('resource_type', ''),
                'metric_name': metric.get('metric_name', ''),
                'value': metric.get('value', 0),
                'unit': metric.get('unit', ''),
                'status': metric.get('status', '')
            })

    # Handle timeseries format
    if 'timeseries' in metrics_data:
        for series in metrics_data['timeseries']:
            resource_id = series.get('resource_id', '')
            for point in series.get('datapoints', []):
                rows.append({
                    'timestamp': point[0],
                    'resource_id': resource_id,
                    'value': point[1],
                    'metric_name': metrics_data.get('metric_name', ''),
                    'resource_type': metrics_data.get('resource_type', '')
                })

    return export_to_csv(rows)


def export_timeseries_to_csv(
    timeseries_data: Dict[str, Any],
    flatten: bool = True
) -> str:
    """
    Export timeseries data to CSV format.

    Args:
        timeseries_data: Timeseries data dictionary
        flatten: If True, flatten all series into single table

    Returns:
        CSV string
    """
    rows = []

    if 'timeseries' in timeseries_data:
        for series in timeseries_data['timeseries']:
            resource_id = series.get('resource_id', series.get('name', ''))
            metric_name = series.get('metric_name', timeseries_data.get('metric_name', ''))

            for datapoint in series.get('datapoints', []):
                timestamp, value = datapoint
                rows.append({
                    'timestamp': timestamp,
                    'resource_id': resource_id,
                    'metric_name': metric_name,
                    'value': value,
                    'resource_type': timeseries_data.get('resource_type', ''),
                    'period': timeseries_data.get('period', ''),
                    'step': timeseries_data.get('step', '')
                })

    return export_to_csv(rows)
