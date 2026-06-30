"""
Rate limiting middleware (Phase 11.2) - stdlib only, no extra dependency.

Fixed-window per-client limiting. No-op unless ``RATE_LIMIT_ENABLED`` is set.
Adds ``X-RateLimit-*`` headers and returns 429 (design_contracts §6 error shape)
when the per-minute limit is exceeded.

ponytail: in-memory per-process counter and client key from ``request.client``
(not X-Forwarded-For). For multi-worker/K8s accuracy and proxy-correct client
IPs, move the counter to Redis and trust a configured proxy header.
"""

import time
from typing import Callable, Dict, List

from fastapi import Request
from starlette.middleware.base import BaseHTTPMiddleware
from starlette.responses import JSONResponse

from app.config import settings

WINDOW_SECONDS = 60


class RateLimitMiddleware(BaseHTTPMiddleware):
    """Fixed-window per-client rate limiter."""

    def __init__(self, app, limit_per_minute: int = 120):
        super().__init__(app)
        self.limit = limit_per_minute
        self._hits: Dict[str, List[float]] = {}  # client key -> [window_start, count]

    def _client_key(self, request: Request) -> str:
        return request.client.host if request.client else "unknown"

    async def dispatch(self, request: Request, call_next: Callable):
        if not settings.RATE_LIMIT_ENABLED:
            return await call_next(request)

        now = time.time()
        key = self._client_key(request)
        window_start, count = self._hits.get(key, (now, 0.0))
        if now - window_start >= WINDOW_SECONDS:
            window_start, count = now, 0.0
        count += 1
        self._hits[key] = [window_start, count]

        reset = int(window_start + WINDOW_SECONDS)
        remaining = max(0, self.limit - int(count))

        if count > self.limit:
            response = JSONResponse(
                status_code=429,
                content={
                    "status": "error",
                    "error": {"code": "RATE_LIMITED", "message": "Too many requests", "retryable": True},
                },
            )
            response.headers["Retry-After"] = str(max(1, reset - int(now)))
        else:
            response = await call_next(request)

        response.headers["X-RateLimit-Limit"] = str(self.limit)
        response.headers["X-RateLimit-Remaining"] = str(remaining)
        response.headers["X-RateLimit-Reset"] = str(reset)
        return response
