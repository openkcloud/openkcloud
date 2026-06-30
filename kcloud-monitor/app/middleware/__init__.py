"""
Middleware package for FastAPI application.

Contains:
- RequestIDMiddleware: Per-request correlation id (X-Request-ID)
- MetricsMiddleware: Request tracking and Prometheus metrics
"""

from app.middleware.request_id import RequestIDMiddleware, REQUEST_ID_HEADER
from app.middleware.rate_limit import RateLimitMiddleware
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
    "RequestIDMiddleware",
    "REQUEST_ID_HEADER",
    "RateLimitMiddleware",
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
