"""
Common Models Package - Shared models across all domains.

This module contains common models used across all API domains including:
- Response models (success, error, pagination)
- Query parameter models
- Enumerations and constants
"""

from .responses import (
    BaseResponse,
    SuccessResponse,
    ErrorResponse,
    ErrorDetail,
    PaginationMetadata,
    PaginatedResponse
)

from .queries import (
    CommonQueryParams,
    TimeSeriesQueryParams,
    PaginationParams,
    FilterParams
)

from .enums import (
    ResourceType,
    SensorType,
    StreamProtocol,
    ExportFormat,
    TimeRange,
    AggregationMethod
)

__all__ = [
    # Response models
    "BaseResponse",
    "SuccessResponse",
    "ErrorResponse",
    "ErrorDetail",
    "PaginationMetadata",
    "PaginatedResponse",

    # Query parameter models
    "CommonQueryParams",
    "TimeSeriesQueryParams",
    "PaginationParams",
    "FilterParams",

    # Enumerations
    "ResourceType",
    "SensorType",
    "StreamProtocol",
    "ExportFormat",
    "TimeRange",
    "AggregationMethod"
]
