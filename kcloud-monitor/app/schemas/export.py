"""내보내기 응답 스키마.

CSV/JSON 내보내기 응답 모델과 허용 메트릭 목록.
CSV 응답은 `fastapi.responses.Response`로 파일 스트림을 직접 반환하므로
FastAPI response_model을 적용하지 않는다(json 포맷만 아래 모델을 사용).

허용 메트릭 목록(EXPORT_METRIC_ALLOWLIST)은 app.schemas.monitoring.METRIC_ALLOWLIST와
별도로 둔다 — export API는 원본 Prometheus 메트릭 이름을 그대로 파라미터로 받으므로
(예: DCGM_FI_DEV_POWER_USAGE), 별칭이 아닌 실제 메트릭명을 키로 사용해 임의 PromQL
주입을 차단한다.
"""
from typing import Any, Optional

from pydantic import BaseModel, Field

from app.schemas._common import DESC_OBSERVED_AT, DESC_STATUS, DESC_WARNINGS, _now

# ---------------------------------------------------------------------------
# 허용 메트릭 목록 (EXPORT_METRIC_ALLOWLIST) — 키/값 모두 원본 메트릭명 또는 안전한 PromQL
# ---------------------------------------------------------------------------
EXPORT_METRIC_ALLOWLIST: dict[str, str] = {
    # NVIDIA (L40S)
    "DCGM_FI_DEV_POWER_USAGE": "DCGM_FI_DEV_POWER_USAGE",
    "DCGM_FI_DEV_GPU_TEMP": "DCGM_FI_DEV_GPU_TEMP",
    "DCGM_FI_DEV_GPU_UTIL": "DCGM_FI_DEV_GPU_UTIL",
    "DCGM_FI_PROF_GR_ENGINE_ACTIVE": "DCGM_FI_PROF_GR_ENGINE_ACTIVE",
    "DCGM_FI_DEV_FB_USED": "DCGM_FI_DEV_FB_USED",
    "DCGM_FI_DEV_FB_FREE": "DCGM_FI_DEV_FB_FREE",
    "DCGM_FI_DEV_XID_ERRORS": "DCGM_FI_DEV_XID_ERRORS",
    # Furiosa
    "furiosa_npu_hw_power": "furiosa_npu_hw_power",
    "furiosa_npu_hw_temperature": "furiosa_npu_hw_temperature",
    "furiosa_npu_core_utilization": "furiosa_npu_core_utilization",
    "furiosa_npu_dram_usage": "furiosa_npu_dram_usage",
    "furiosa_npu_alive": "furiosa_npu_alive",
    # Rebellions — 콜론 포함 메트릭명은 PromQL 파서가 원형을 거부하므로 __name__ 매칭 사용
    "RBLN_DEVICE_STATUS:CARD_POWER": '{__name__="RBLN_DEVICE_STATUS:CARD_POWER"}',
    "RBLN_DEVICE_STATUS:UTILIZATION": '{__name__="RBLN_DEVICE_STATUS:UTILIZATION"}',
    "RBLN_DEVICE_STATUS:DRAM_USED": '{__name__="RBLN_DEVICE_STATUS:DRAM_USED"}',
    "RBLN_DEVICE_STATUS:HEALTH": '{__name__="RBLN_DEVICE_STATUS:HEALTH"}',
    # 전력 3계층 공통
    "ipmi_dcmi_power_consumption_watts": "ipmi_dcmi_power_consumption_watts",
    "kepler_node_cpu_watts": "kepler_node_cpu_watts",
}


# ---------------------------------------------------------------------------
# /export/power
# ---------------------------------------------------------------------------

class PowerExportRow(BaseModel):
    """전력 내보내기 행 하나."""

    timestamp: str = Field(..., description="측정 시각 (ISO 8601, UTC)")
    node: str = Field(..., description="노드 이름. 확인 불가 시 unknown")
    layer: str = Field(
        ...,
        description="측정 구분 (server | cpu | accelerator:nvidia | accelerator:furiosa | accelerator:rebellions). "
        "server: IPMI 서버 총전력, cpu: Kepler CPU 전력, accelerator: 가속기 전력",
    )
    watts: Optional[float] = Field(..., description="전력(W). 값을 읽을 수 없으면 null")


class PowerExportData(BaseModel):
    """전력 내보내기 데이터."""

    rows: list[PowerExportRow] = Field(..., description="전력 측정 행 목록")


class PowerExportResponse(BaseModel):
    """전력 내보내기 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: Optional[PowerExportData] = Field(..., description="전력 내보내기 데이터")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


# ---------------------------------------------------------------------------
# /export/metrics
# ---------------------------------------------------------------------------

class MetricExportRow(BaseModel):
    """메트릭 내보내기 행 하나."""

    timestamp: str = Field(..., description="측정 시각 (ISO 8601, UTC)")
    labels: dict[str, str] = Field(..., description="원본 메트릭 라벨 (키: 라벨 이름, 값: 라벨 값)")
    value: Optional[float] = Field(..., description="메트릭 값. 원본 메트릭 단위 그대로, 값을 읽을 수 없으면 null")


class MetricExportData(BaseModel):
    """메트릭 내보내기 데이터."""

    metric: str = Field(..., description="내보낸 메트릭 이름")
    rows: list[MetricExportRow] = Field(..., description="메트릭 측정 행 목록")


class MetricExportResponse(BaseModel):
    """메트릭 내보내기 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: Optional[MetricExportData] = Field(..., description="메트릭 내보내기 데이터")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


# ---------------------------------------------------------------------------
# /export/report
# ---------------------------------------------------------------------------

class ReportExportResponse(BaseModel):
    """리포트 생성 응답. 생성 불가 시 NOT_CONFIGURED 경고 반환."""

    status: str = Field(..., description=DESC_STATUS)
    data: Optional[Any] = Field(None, description="리포트 데이터. 생성 불가 시 null")
    report_type: str = Field(..., description="리포트 주기 (daily | weekly | monthly)")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)
