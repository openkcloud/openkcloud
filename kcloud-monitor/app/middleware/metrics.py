"""
API Metrics Middleware - Request and response tracking for Prometheus.

This module provides middleware for collecting API server metrics:
- Request count per endpoint
- Request duration histogram
- Error rates
- Cache hit rates
"""

import time
from typing import Callable
from fastapi import Request, Response
from starlette.middleware.base import BaseHTTPMiddleware
from prometheus_client import Counter, Histogram, Gauge, generate_latest, CONTENT_TYPE_LATEST, CollectorRegistry


# ============================================================================
# Prometheus Metrics Registry
# ============================================================================

# Create a separate registry for API metrics (separate from default registry)
metrics_registry = CollectorRegistry()

# Request counter - Total API requests by method, endpoint, status
api_requests_total = Counter(
    'api_requests_total',
    'Total number of API requests',
    ['method', 'endpoint', 'status_code'],
    registry=metrics_registry
)

# Request duration histogram - Request processing time
api_request_duration_seconds = Histogram(
    'api_request_duration_seconds',
    'API request duration in seconds',
    ['method', 'endpoint'],
    buckets=(0.01, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0),
    registry=metrics_registry
)

# Error counter - Total errors by endpoint and error type
api_errors_total = Counter(
    'api_errors_total',
    'Total number of API errors',
    ['method', 'endpoint', 'error_type'],
    registry=metrics_registry
)

# Cache metrics - Cache hits and misses
cache_hits_total = Counter(
    'cache_hits_total',
    'Total number of cache hits',
    ['cache_type'],
    registry=metrics_registry
)

cache_misses_total = Counter(
    'cache_misses_total',
    'Total number of cache misses',
    ['cache_type'],
    registry=metrics_registry
)

# WebSocket connections gauge
websocket_connections = Gauge(
    'websocket_connections',
    'Current number of active WebSocket connections',
    ['stream_type'],
    registry=metrics_registry
)

# Prometheus query metrics
prometheus_query_duration_seconds = Histogram(
    'prometheus_query_duration_seconds',
    'Prometheus query duration in seconds',
    ['query_type'],
    buckets=(0.1, 0.25, 0.5, 1.0, 2.0, 5.0, 10.0, 30.0),
    registry=metrics_registry
)

prometheus_query_errors_total = Counter(
    'prometheus_query_errors_total',
    'Total number of Prometheus query errors',
    ['query_type', 'error_type'],
    registry=metrics_registry
)


# ============================================================================
# Metrics Middleware
# ============================================================================

class MetricsMiddleware(BaseHTTPMiddleware):
    """
    Middleware for collecting API request metrics.

    Tracks:
    - Request count
    - Request duration
    - Error rates
    - HTTP status codes
    """

    async def dispatch(self, request: Request, call_next: Callable) -> Response:
        """Process request and collect metrics."""
        # Skip metrics collection for /system/metrics endpoint itself
        if request.url.path == "/api/v2/system/metrics":
            return await call_next(request)

        start_time = time.time()
        method = request.method

        try:
            response = await call_next(request)
        except Exception as exc:
            duration = time.time() - start_time
            endpoint = self._endpoint_label(request)
            api_errors_total.labels(
                method=method,
                endpoint=endpoint,
                error_type=exc.__class__.__name__
            ).inc()
            api_request_duration_seconds.labels(
                method=method,
                endpoint=endpoint
            ).observe(duration)
            raise

        endpoint = self._endpoint_label(request)
        status_code = response.status_code

        api_requests_total.labels(
            method=method,
            endpoint=endpoint,
            status_code=status_code
        ).inc()

        duration = time.time() - start_time
        api_request_duration_seconds.labels(
            method=method,
            endpoint=endpoint
        ).observe(duration)

        if status_code >= 400:
            error_type = "client_error" if status_code < 500 else "server_error"
            api_errors_total.labels(
                method=method,
                endpoint=endpoint,
                error_type=error_type
            ).inc()

        return response

    def _endpoint_label(self, request: Request) -> str:
        """Endpoint label for metrics = matched route template (set in scope
        during routing), keeping label cardinality bounded. Unmatched requests
        (404 on arbitrary paths) collapse into a single label."""
        route = request.scope.get("route")
        return getattr(route, "path_format", None) or "unmatched"


# ============================================================================
# Metrics Export Functions
# ============================================================================

def get_metrics_text() -> bytes:
    """
    Generate Prometheus text format metrics.

    Returns:
        bytes: Prometheus-formatted metrics
    """
    return generate_latest(metrics_registry)


def get_metrics_content_type() -> str:
    """
    Get the content type for Prometheus metrics.

    Returns:
        str: Content-Type header value
    """
    return CONTENT_TYPE_LATEST


# ============================================================================
# Helper Functions for Manual Metric Recording
# ============================================================================

def record_cache_hit(cache_type: str = "default"):
    """Record a cache hit."""
    cache_hits_total.labels(cache_type=cache_type).inc()


def record_cache_miss(cache_type: str = "default"):
    """Record a cache miss."""
    cache_misses_total.labels(cache_type=cache_type).inc()


def record_websocket_connect(stream_type: str):
    """Record a WebSocket connection."""
    websocket_connections.labels(stream_type=stream_type).inc()


def record_websocket_disconnect(stream_type: str):
    """Record a WebSocket disconnection."""
    websocket_connections.labels(stream_type=stream_type).dec()


def record_prometheus_query(query_type: str, duration: float):
    """Record Prometheus query duration."""
    prometheus_query_duration_seconds.labels(query_type=query_type).observe(duration)


def record_prometheus_error(query_type: str, error_type: str):
    """Record Prometheus query error."""
    prometheus_query_errors_total.labels(query_type=query_type, error_type=error_type).inc()
