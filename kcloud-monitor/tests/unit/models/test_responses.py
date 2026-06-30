"""Regression tests for the common response envelope (design_contracts §6)."""

from app.models.common.responses import (
    BaseResponse,
    SuccessResponse,
    ErrorResponse,
    ErrorDetail,
    ErrorCode,
)
from app.models.accelerators.gpu import GPUListResponse


def test_base_response_meta_defaults():
    r = SuccessResponse(message="ok")
    assert r.status == "success"
    assert r.observed_at is None
    assert r.is_stale is False
    assert r.warnings == []
    assert r.partial_sources == []
    assert r.request_id is None
    assert r.timestamp is not None


def test_base_response_meta_fields_present():
    fields = set(BaseResponse.model_fields.keys())
    assert {"timestamp", "observed_at", "is_stale", "warnings", "partial_sources", "request_id"} <= fields


def test_error_response_schema():
    e = ErrorResponse(
        error=ErrorDetail(code=ErrorCode.PROMETHEUS_QUERY_FAILED, message="boom", retryable=True)
    )
    assert e.status == "error"
    assert e.error.code == "PROMETHEUS_QUERY_FAILED"
    assert e.error.retryable is True
    assert e.request_id is None and e.observed_at is None
    # envelope inherited from BaseResponse
    assert e.warnings == [] and e.is_stale is False


def test_error_detail_retryable_default():
    d = ErrorDetail(code="VALIDATION_ERROR", message="bad")
    assert d.retryable is False


def test_error_code_members():
    codes = {c.value for c in ErrorCode}
    assert {
        "VALIDATION_ERROR",
        "NOT_FOUND",
        "UNAUTHORIZED",
        "PROMETHEUS_QUERY_FAILED",
        "PROMETHEUS_UNAVAILABLE",
        "UPSTREAM_TIMEOUT",
        "INTERNAL_ERROR",
    } <= codes


def test_gpu_list_response_inherits_envelope():
    r = GPUListResponse(total_gpus=0, gpus=[])
    assert r.status == "success"
    assert r.warnings == [] and r.partial_sources == [] and r.is_stale is False
    assert hasattr(r, "observed_at") and hasattr(r, "request_id")
    dumped = r.model_dump()
    for key in ("status", "warnings", "partial_sources", "is_stale", "observed_at", "request_id", "total_gpus", "gpus"):
        assert key in dumped


def test_gpu_list_response_partial():
    r = GPUListResponse(
        total_gpus=1,
        gpus=[],
        status="partial",
        partial_sources=["dcgm"],
        warnings=["DCGM_UNAVAILABLE"],
    )
    assert r.status == "partial"
    assert r.partial_sources == ["dcgm"]
    assert "DCGM_UNAVAILABLE" in r.warnings
