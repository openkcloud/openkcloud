"""
Request ID middleware - per-request correlation id (design_contracts §6).

Reuses a valid inbound ``X-Request-ID`` (for trace propagation) or generates a
new one, exposes it on ``request.state.request_id`` for handlers/response models,
and echoes it back on the response header.
"""

import re
import uuid
from typing import Callable

from fastapi import Request, Response
from starlette.middleware.base import BaseHTTPMiddleware

from app.logging_config import request_id_var

REQUEST_ID_HEADER = "X-Request-ID"

# Inbound header is a trust boundary: only reflect safe ids, else generate a new one.
_SAFE_REQUEST_ID = re.compile(r"^[A-Za-z0-9._-]{1,128}$")


class RequestIDMiddleware(BaseHTTPMiddleware):
    """Attach a correlation id to every request and response."""

    async def dispatch(self, request: Request, call_next: Callable) -> Response:
        incoming = request.headers.get(REQUEST_ID_HEADER, "")
        request_id = incoming if _SAFE_REQUEST_ID.match(incoming) else uuid.uuid4().hex
        request.state.request_id = request_id
        # Expose to logging for the duration of the request (design_contracts §6).
        token = request_id_var.set(request_id)
        try:
            response = await call_next(request)
            response.headers[REQUEST_ID_HEADER] = request_id
            return response
        finally:
            request_id_var.reset(token)
