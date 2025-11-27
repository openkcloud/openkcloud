"""
Middleware package for FastAPI application.

Contains:
- MetricsMiddleware: Request tracking and Prometheus metrics
"""

from app.middleware.metrics import (
    MetricsMiddleware,
    get_metrics_text,
    get_metrics_content_type,
    record_cache_hit,
    record_cache_miss,
    record_websocket_connect,
    record_websocket_disconnect,
    record_prometheus_query,
    record_prometheus_error
)

__all__ = [
    "MetricsMiddleware",
    "get_metrics_text",
    "get_metrics_content_type",
    "record_cache_hit",
    "record_cache_miss",
    "record_websocket_connect",
    "record_websocket_disconnect",
    "record_prometheus_query",
    "record_prometheus_error"
]
