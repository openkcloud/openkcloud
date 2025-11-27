"""
Streaming Service - WebSocket and SSE (Server-Sent Events) support.

This module provides:
- WebSocket connection management
- SSE event streaming
- Subscription management and filtering
- Periodic data push
"""

import asyncio
import json
import logging
from datetime import datetime
from typing import Dict, List, Optional, Set, Any
from fastapi import WebSocket, WebSocketDisconnect
from starlette.responses import StreamingResponse
import time

from app import crud

logger = logging.getLogger(__name__)


class ConnectionManager:
    """
    Manages active WebSocket connections and subscriptions.
    """

    def __init__(self):
        """Initialize connection manager."""
        self.active_connections: Dict[str, Set[WebSocket]] = {
            'power': set(),
            'metrics': set()
        }
        self.connection_filters: Dict[WebSocket, Dict[str, Any]] = {}

    async def connect(self, websocket: WebSocket, stream_type: str, filters: Optional[Dict[str, Any]] = None):
        """
        Accept a new WebSocket connection.

        Args:
            websocket: WebSocket connection
            stream_type: Type of stream (power, metrics)
            filters: Optional filters for data (cluster, resource_type, etc.)
        """
        await websocket.accept()
        if stream_type not in self.active_connections:
            self.active_connections[stream_type] = set()

        self.active_connections[stream_type].add(websocket)
        if filters:
            self.connection_filters[websocket] = filters

        logger.info(f"New WebSocket connection: {stream_type} (filters: {filters})")

    def disconnect(self, websocket: WebSocket, stream_type: str):
        """
        Remove a WebSocket connection.

        Args:
            websocket: WebSocket connection
            stream_type: Type of stream
        """
        if stream_type in self.active_connections:
            self.active_connections[stream_type].discard(websocket)
        if websocket in self.connection_filters:
            del self.connection_filters[websocket]

        logger.info(f"WebSocket disconnected: {stream_type}")

    async def send_to_connection(self, websocket: WebSocket, message: Dict[str, Any]):
        """
        Send message to a specific WebSocket connection.

        Args:
            websocket: WebSocket connection
            message: Message data
        """
        try:
            await websocket.send_json(message)
        except Exception as e:
            logger.error(f"Failed to send message to WebSocket: {e}")

    async def broadcast(self, stream_type: str, message: Dict[str, Any]):
        """
        Broadcast message to all connections of a specific stream type.

        Args:
            stream_type: Type of stream
            message: Message data
        """
        if stream_type not in self.active_connections:
            return

        disconnected = set()

        for connection in self.active_connections[stream_type]:
            try:
                # Apply filters if any
                if connection in self.connection_filters:
                    filters = self.connection_filters[connection]
                    if not self._apply_filters(message, filters):
                        continue

                await connection.send_json(message)
            except WebSocketDisconnect:
                disconnected.add(connection)
            except Exception as e:
                logger.error(f"Error broadcasting to WebSocket: {e}")
                disconnected.add(connection)

        # Clean up disconnected connections
        for connection in disconnected:
            self.disconnect(connection, stream_type)

    def _apply_filters(self, message: Dict[str, Any], filters: Dict[str, Any]) -> bool:
        """
        Check if message matches connection filters.

        Args:
            message: Message data
            filters: Connection filters

        Returns:
            True if message matches filters
        """
        # Check cluster filter
        if 'cluster' in filters and filters['cluster']:
            if message.get('cluster') != filters['cluster']:
                return False

        # Check resource_type filter
        if 'resource_type' in filters and filters['resource_type']:
            if message.get('resource_type') != filters['resource_type']:
                return False

        return True

    def get_connection_count(self, stream_type: str) -> int:
        """
        Get number of active connections for a stream type.

        Args:
            stream_type: Type of stream

        Returns:
            Number of active connections
        """
        return len(self.active_connections.get(stream_type, set()))


# Global connection manager instance
connection_manager = ConnectionManager()


# ============================================================================
# WebSocket Stream Handlers
# ============================================================================

async def power_stream_handler(
    websocket: WebSocket,
    cluster: Optional[str] = None,
    resource_type: Optional[str] = None,
    interval: int = 5
):
    """
    Handle power data WebSocket stream.

    Args:
        websocket: WebSocket connection
        cluster: Cluster filter
        resource_type: Resource type filter
        interval: Update interval in seconds
    """
    filters = {'cluster': cluster, 'resource_type': resource_type}
    await connection_manager.connect(websocket, 'power', filters)

    try:
        while True:
            # Get current power data
            try:
                if resource_type == 'accelerators':
                    data = await crud.get_accelerator_power(cluster)
                elif resource_type == 'infrastructure':
                    data = await crud.get_infrastructure_power(cluster)
                else:
                    data = await crud.get_unified_power(cluster)

                # Prepare message
                message = {
                    'type': 'power_update',
                    'timestamp': data['timestamp'].isoformat(),
                    'cluster': cluster,
                    'resource_type': resource_type,
                    'data': data['data']
                }

                await connection_manager.send_to_connection(websocket, message)

            except Exception as e:
                logger.error(f"Error fetching power data for WebSocket: {e}")
                error_message = {
                    'type': 'error',
                    'timestamp': datetime.utcnow().isoformat(),
                    'error': str(e)
                }
                await connection_manager.send_to_connection(websocket, error_message)

            # Wait for next interval
            await asyncio.sleep(interval)

    except WebSocketDisconnect:
        logger.info("WebSocket disconnected: power stream")
    finally:
        connection_manager.disconnect(websocket, 'power')


async def metrics_stream_handler(
    websocket: WebSocket,
    metric_name: str = 'utilization',
    resource_type: Optional[str] = None,
    interval: int = 5
):
    """
    Handle metrics data WebSocket stream.

    Args:
        websocket: WebSocket connection
        metric_name: Metric name
        resource_type: Resource type filter
        interval: Update interval in seconds
    """
    filters = {'metric_name': metric_name, 'resource_type': resource_type}
    await connection_manager.connect(websocket, 'metrics', filters)

    try:
        while True:
            # Get current metrics
            try:
                # Get instant query for metrics
                if metric_name == 'utilization' and resource_type == 'gpus':
                    summary = await crud.get_gpu_summary()
                    metrics_data = {
                        'avg_utilization_percent': summary.get('avg_utilization_percent', 0.0),
                        'total_count': summary.get('total_gpus', 0)
                    }
                elif metric_name == 'temperature' and resource_type == 'gpus':
                    summary = await crud.get_gpu_summary()
                    metrics_data = {
                        'avg_temperature_celsius': summary.get('avg_temperature_celsius', 0.0),
                        'max_temperature_celsius': summary.get('max_temperature_celsius', 0.0)
                    }
                else:
                    metrics_data = {'status': 'not_implemented'}

                # Prepare message
                message = {
                    'type': 'metrics_update',
                    'timestamp': datetime.utcnow().isoformat(),
                    'metric_name': metric_name,
                    'resource_type': resource_type,
                    'data': metrics_data
                }

                await connection_manager.send_to_connection(websocket, message)

            except Exception as e:
                logger.error(f"Error fetching metrics for WebSocket: {e}")
                error_message = {
                    'type': 'error',
                    'timestamp': datetime.utcnow().isoformat(),
                    'error': str(e)
                }
                await connection_manager.send_to_connection(websocket, error_message)

            # Wait for next interval
            await asyncio.sleep(interval)

    except WebSocketDisconnect:
        logger.info("WebSocket disconnected: metrics stream")
    finally:
        connection_manager.disconnect(websocket, 'metrics')


# ============================================================================
# SSE (Server-Sent Events) Handlers
# ============================================================================

async def power_events_generator(
    cluster: Optional[str] = None,
    resource_type: Optional[str] = None,
    threshold_watts: Optional[float] = None
):
    """
    Generate power events for SSE stream.

    Args:
        cluster: Cluster filter
        resource_type: Resource type filter
        threshold_watts: Power threshold for event generation

    Yields:
        SSE formatted events
    """
    logger.info(f"Starting SSE power events stream (cluster={cluster}, threshold={threshold_watts}W)")

    previous_power = 0.0

    while True:
        try:
            # Get current power data
            if resource_type == 'accelerators':
                data = await crud.get_accelerator_power(cluster)
            elif resource_type == 'infrastructure':
                data = await crud.get_infrastructure_power(cluster)
            else:
                data = await crud.get_unified_power(cluster)

            current_power = data['data']['total_power_watts']

            # Check for threshold events
            event_data = {
                'timestamp': data['timestamp'].isoformat(),
                'cluster': cluster,
                'resource_type': resource_type,
                'power_watts': current_power
            }

            # Generate event if threshold exceeded
            if threshold_watts and current_power > threshold_watts:
                event_data['event_type'] = 'threshold_exceeded'
                event_data['threshold_watts'] = threshold_watts
                yield f"event: threshold_exceeded\ndata: {json.dumps(event_data)}\n\n"

            # Generate event for significant power change (>10%)
            if previous_power > 0:
                change_percent = abs((current_power - previous_power) / previous_power * 100)
                if change_percent > 10:
                    event_data['event_type'] = 'power_spike'
                    event_data['change_percent'] = round(change_percent, 2)
                    event_data['previous_power_watts'] = previous_power
                    yield f"event: power_spike\ndata: {json.dumps(event_data)}\n\n"

            previous_power = current_power

            # Periodic update (every 30 seconds)
            await asyncio.sleep(30)

        except Exception as e:
            logger.error(f"Error generating power events: {e}")
            error_event = {
                'event_type': 'error',
                'timestamp': datetime.utcnow().isoformat(),
                'error': str(e)
            }
            yield f"event: error\ndata: {json.dumps(error_event)}\n\n"
            await asyncio.sleep(30)
