"""
Common Response Models - Standardized API response structures.

This module defines common response models used across all API endpoints.
"""

from typing import Optional, Any, List, TypeVar, Generic
from datetime import datetime
from pydantic import BaseModel, Field


# ============================================================================
# Base Response Models
# ============================================================================

class BaseResponse(BaseModel):
    """
    Base response model for all API responses.

    Provides a common timestamp field.
    """
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Response timestamp (UTC)")


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

class ErrorDetail(BaseModel):
    """
    Detailed error information.

    Provides structured error data with code, message, and optional details.
    """
    code: str = Field(..., description="Error code (e.g., PROMETHEUS_ERROR, VALIDATION_ERROR)")
    message: str = Field(..., description="Human-readable error message")
    details: Optional[str] = Field(None, description="Additional error details or context")
    field: Optional[str] = Field(None, description="Field name (for validation errors)")


class ErrorResponse(BaseResponse):
    """
    Standard error response for all API errors.

    Provides consistent error structure across all endpoints.
    """
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
