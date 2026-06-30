"""
Common Response Models - Standardized API response structures.

This module defines common response models used across all API endpoints.
"""

from typing import Optional, Any, List, TypeVar, Generic
from datetime import datetime
from enum import Enum
from pydantic import BaseModel, Field


# ============================================================================
# Base Response Models
# ============================================================================

class BaseResponse(BaseModel):
    """
    Base response model for all API responses.

    Provides common metadata shared by every response (design_contracts §6):
    timestamp, observed_at, is_stale, warnings[], partial_sources[].
    """
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response generation time (UTC)")
    observed_at: Optional[datetime] = Field(
        None,
        description="Time the underlying data was observed (UTC); None if not time-based",
    )
    is_stale: bool = Field(
        False,
        description="True if the underlying data exceeds the staleness threshold (design_contracts §6)",
    )
    warnings: List[str] = Field(
        default_factory=list,
        description="Non-fatal warning codes (e.g. STALE_DATA, FACILITY_DATA_EXTERNAL)",
    )
    partial_sources: List[str] = Field(
        default_factory=list,
        description="Auxiliary sources that failed when status is 'partial' (design_contracts §6)",
    )
    request_id: Optional[str] = Field(
        None,
        description="Correlation id for this request; populated by middleware (design_contracts §6)",
    )


class SuccessResponse(BaseResponse):
    """
    Generic success response for operations without specific data structure.

    Used for simple confirmation responses (e.g., health checks, acknowledgments).
    """
    status: str = Field("success", description="Operation status")
    message: str = Field(..., description="Success message")
    data: Optional[Any] = Field(None, description="Optional response data")


# ============================================================================
# Error Response Models
# ============================================================================

class ErrorCode(str, Enum):
    """Standard error codes (design_contracts §6)."""
    VALIDATION_ERROR = "VALIDATION_ERROR"
    NOT_FOUND = "NOT_FOUND"
    UNAUTHORIZED = "UNAUTHORIZED"
    PROMETHEUS_QUERY_FAILED = "PROMETHEUS_QUERY_FAILED"
    PROMETHEUS_UNAVAILABLE = "PROMETHEUS_UNAVAILABLE"
    UPSTREAM_TIMEOUT = "UPSTREAM_TIMEOUT"
    INTERNAL_ERROR = "INTERNAL_ERROR"


class ErrorDetail(BaseModel):
    """
    Detailed error information.

    Provides structured error data with code, message, retryable flag, and optional details.
    """
    code: str = Field(..., description="Error code (see ErrorCode, e.g. PROMETHEUS_QUERY_FAILED)")
    message: str = Field(..., description="Human-readable error message")
    retryable: bool = Field(False, description="Whether the client may retry the request (design_contracts §6)")
    details: Optional[str] = Field(None, description="Additional error details or context")
    field: Optional[str] = Field(None, description="Field name (for validation errors)")


class ErrorResponse(BaseResponse):
    """
    Standard error response for all API errors.

    Provides consistent error structure across all endpoints.
    """
    status: str = Field("error", description="Operation status")
    error: ErrorDetail = Field(..., description="Error details")


# ============================================================================
# Pagination Models
# ============================================================================

class PaginationMetadata(BaseModel):
    """
    Pagination metadata for list responses.

    Provides information about page size, current page, and total items.
    """
    page: int = Field(..., ge=1, description="Current page number (1-indexed)")
    page_size: int = Field(..., ge=1, le=1000, description="Number of items per page")
    total_items: int = Field(..., ge=0, description="Total number of items across all pages")
    total_pages: int = Field(..., ge=0, description="Total number of pages")
    has_next: bool = Field(..., description="Whether there is a next page")
    has_previous: bool = Field(..., description="Whether there is a previous page")


# Generic type for paginated data
T = TypeVar('T')


class PaginatedResponse(BaseResponse, Generic[T]):
    """
    Generic paginated response wrapper.

    Wraps list responses with pagination metadata.

    Example:
        ```python
        class GPUListPaginated(PaginatedResponse[List[GPUInfo]]):
            pass
        ```
    """
    data: List[T] = Field(..., description="List of items for current page")
    pagination: PaginationMetadata = Field(..., description="Pagination metadata")


# ============================================================================
# Common Data Response Models
# ============================================================================

class HealthStatus(BaseModel):
    """
    Health status for a single component.

    Used in health check responses.
    """
    component: str = Field(..., description="Component name (e.g., prometheus, cache, database)")
    status: str = Field(..., description="Component status (healthy, degraded, unhealthy)")
    message: Optional[str] = Field(None, description="Status message or error details")
    response_time_ms: Optional[float] = Field(None, ge=0, description="Component response time in milliseconds")


class HealthCheckResponse(BaseResponse):
    """
    Health check response with component statuses.

    Provides overall system health and individual component statuses.
    """
    status: str = Field(..., description="Overall system status (healthy, degraded, unhealthy)")
    version: str = Field(..., description="API version")
    components: List[HealthStatus] = Field(..., description="Component health statuses")
