"""
Parquet Exporter - Export data to Apache Parquet format.

This module provides Parquet export functionality for efficient columnar storage.
Requires: pyarrow
"""

import io
from typing import List, Dict, Any, Optional
from datetime import datetime

try:
    import pyarrow as pa
    import pyarrow.parquet as pq
    PARQUET_AVAILABLE = True
except ImportError:
    PARQUET_AVAILABLE = False


def export_to_parquet(data: List[Dict[str, Any]]) -> bytes:
    """
    Export data to Parquet format.

    Args:
        data: List of dictionaries to export

    Returns:
        Parquet file bytes

    Raises:
        ImportError: If pyarrow is not installed
    """
    if not PARQUET_AVAILABLE:
        raise ImportError("pyarrow is not installed. Install with: pip install pyarrow")

    if not data:
        # Return empty parquet file
        schema = pa.schema([])
        table = pa.Table.from_pydict({}, schema=schema)
    else:
        # Convert to PyArrow table
        table = pa.Table.from_pylist(data)

    # Write to bytes buffer
    buffer = io.BytesIO()
    pq.write_table(table, buffer, compression='snappy')

    return buffer.getvalue()


def export_power_to_parquet(power_data: Dict[str, Any]) -> bytes:
    """
    Export power monitoring data to Parquet format.

    Args:
        power_data: Power data dictionary

    Returns:
        Parquet file bytes
    """
    rows = []
    timestamp = power_data.get('timestamp', datetime.utcnow().isoformat())

    # Add summary data
    if 'summary' in power_data:
        summary = power_data['summary']
        rows.append({
            'type': 'summary',
            'timestamp': timestamp,
            'total_power_watts': float(summary.get('total_power_watts', 0)),
            'resource_count': int(summary.get('resource_count', 0)),
            'avg_power_watts': float(summary.get('avg_power_watts', 0)),
            'max_power_watts': float(summary.get('max_power_watts', 0)),
            'min_power_watts': float(summary.get('min_power_watts', 0)),
            'name': 'total',
            'percentage': 100.0,
            'resource_type': 'all'
        })

    # Add breakdown data
    if 'breakdown' in power_data:
        for item in power_data['breakdown']:
            rows.append({
                'type': 'breakdown',
                'timestamp': timestamp,
                'name': str(item.get('name', '')),
                'power_watts': float(item.get('power_watts', 0)),
                'percentage': float(item.get('percentage', 0)),
                'resource_count': int(item.get('resource_count', 0)),
                'resource_type': str(item.get('resource_type', '')),
                'total_power_watts': float(item.get('power_watts', 0)),
                'avg_power_watts': 0.0,
                'max_power_watts': 0.0,
                'min_power_watts': 0.0
            })

    # Add accelerators data
    if 'accelerators' in power_data:
        acc = power_data['accelerators']
        rows.append({
            'type': 'accelerators',
            'timestamp': timestamp,
            'total_power_watts': float(acc.get('total_power_watts', 0)),
            'resource_count': int(acc.get('gpu_count', 0)) + int(acc.get('npu_count', 0)),
            'name': 'accelerators',
            'percentage': 0.0,
            'resource_type': 'accelerators',
            'avg_power_watts': 0.0,
            'max_power_watts': 0.0,
            'min_power_watts': 0.0
        })

    # Add infrastructure data
    if 'infrastructure' in power_data:
        infra = power_data['infrastructure']
        rows.append({
            'type': 'infrastructure',
            'timestamp': timestamp,
            'total_power_watts': float(infra.get('total_power_watts', 0)),
            'resource_count': int(infra.get('node_count', 0)) + int(infra.get('pod_count', 0)),
            'name': 'infrastructure',
            'percentage': 0.0,
            'resource_type': 'infrastructure',
            'avg_power_watts': 0.0,
            'max_power_watts': 0.0,
            'min_power_watts': 0.0
        })

    return export_to_parquet(rows)


def export_metrics_to_parquet(metrics_data: Dict[str, Any]) -> bytes:
    """
    Export performance metrics data to Parquet format.

    Args:
        metrics_data: Metrics data dictionary

    Returns:
        Parquet file bytes
    """
    rows = []
    timestamp = metrics_data.get('timestamp', datetime.utcnow().isoformat())

    # Handle different metric formats
    if 'metrics' in metrics_data:
        for metric in metrics_data['metrics']:
            rows.append({
                'timestamp': timestamp,
                'resource_id': str(metric.get('resource_id', '')),
                'resource_type': str(metric.get('resource_type', '')),
                'metric_name': str(metric.get('metric_name', '')),
                'value': float(metric.get('value', 0)),
                'unit': str(metric.get('unit', '')),
                'status': str(metric.get('status', ''))
            })

    return export_to_parquet(rows)


def export_timeseries_to_parquet(timeseries_data: Dict[str, Any]) -> bytes:
    """
    Export timeseries data to Parquet format.

    Args:
        timeseries_data: Timeseries data dictionary

    Returns:
        Parquet file bytes
    """
    rows = []

    if 'timeseries' in timeseries_data:
        metric_name = timeseries_data.get('metric_name', '')
        resource_type = timeseries_data.get('resource_type', '')
        period = timeseries_data.get('period', '')
        step = timeseries_data.get('step', '')

        for series in timeseries_data['timeseries']:
            resource_id = series.get('resource_id', series.get('name', ''))

            for datapoint in series.get('datapoints', []):
                timestamp, value = datapoint
                rows.append({
                    'timestamp': str(timestamp),
                    'resource_id': str(resource_id),
                    'metric_name': str(metric_name),
                    'value': float(value),
                    'resource_type': str(resource_type),
                    'period': str(period),
                    'step': str(step)
                })

    return export_to_parquet(rows)
