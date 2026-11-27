"""
로그 API v2 Pydantic 스키마 (OPT.003).

공통 응답 정책(docs/API_GUIDE.md):
  - observed_at, warnings[] 동일
  - 로그 응답은 data[] + pagination 구조
"""
from typing import Any, Optional

from pydantic import BaseModel, Field

from app.schemas._common import DESC_OBSERVED_AT, DESC_STATUS, DESC_WARNINGS, _now


# ---------------------------------------------------------------------------
# 로그 엔트리
# ---------------------------------------------------------------------------

class LogEntry(BaseModel):
    """로그 한 줄"""

    timestamp: str = Field(..., description="로그 발생 시각 (ISO 8601, UTC)")
    log_level: str = Field("info", description="로그 레벨 (error | warning | info | debug)")
    message: str = Field(..., description="로그 원문 메시지")
    labels: dict[str, str] = Field(default_factory=dict, description="로그에 붙은 Loki 라벨 (키: 라벨 이름, 값: 라벨 값)")
    detected_fields: dict[str, Any] = Field(
        default_factory=dict, description="원문에서 자동 추출한 장애 정보. 해당 없으면 빈 객체. GPU 오류: xid_code, gpu_pci_bdf, severity. 메모리 부족: error_type(OOM | CUDA OOM), 커널 OOM 은 pid, process, CUDA OOM 은 requested_gb, allocated_gb(GB). severity(critical | warning)"
    )
    trace_id: str = Field("", description="분산 추적(OpenTelemetry) trace ID. 추적 정보가 없으면 빈 문자열")
    span_id: str = Field("", description="분산 추적(OpenTelemetry) span ID. 추적 정보가 없으면 빈 문자열")


class LogPagination(BaseModel):
    """로그 페이지 정보. 로그는 시각 기준으로 넘기므로 offset 은 항상 0, 다음 페이지는 next_cursor 사용"""

    total: int = Field(..., description="이번 페이지 건수. 이번 응답에 담긴 로그 수이며 전체 건수가 아님")
    limit: int = Field(..., description="페이지 크기")
    offset: int = Field(0, description="항상 0. 다음 페이지는 next_cursor 사용")
    has_next: bool = Field(False, description="다음 페이지 존재 여부")
    next_cursor: Optional[str] = Field(
        None, description="다음 페이지 요청 시 cursor 파라미터로 넘길 값. 마지막 페이지면 null",
    )


# ---------------------------------------------------------------------------
# 응답 모델
# ---------------------------------------------------------------------------

class LogSearchResponse(BaseModel):
    """로그 검색 응답"""

    status: str = Field("success", description=DESC_STATUS)
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    data: list[LogEntry] = Field([], description="로그 목록. direction 순서(backward: 최신 먼저)")
    pagination: LogPagination = Field(..., description="페이지 정보. 다음 페이지는 next_cursor 사용")
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class PodLogResponse(BaseModel):
    """Pod 로그 응답"""

    status: str = Field("success", description=DESC_STATUS)
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    cluster: str = Field(..., description="클러스터 이름")
    namespace: str = Field(..., description="Kubernetes 네임스페이스")
    pod: str = Field(..., description="Pod 이름")
    data: list[LogEntry] = Field([], description="로그 목록. direction 순서(backward: 최신 먼저)")
    pagination: LogPagination = Field(..., description="페이지 정보. 다음 페이지는 next_cursor 사용")
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class AcceleratorLogResponse(BaseModel):
    """가속기 드라이버 로그 응답"""

    status: str = Field("success", description=DESC_STATUS)
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    cluster: str = Field(..., description="클러스터 이름")
    accelerator_id: str = Field(..., description="가속기 ID")
    node: Optional[str] = Field(None, description="카드가 장착된 노드 이름. 카드를 못 찾으면 null")
    data: list[LogEntry] = Field([], description="로그 목록. direction 순서(backward: 최신 먼저)")
    pagination: LogPagination = Field(..., description="페이지 정보. 다음 페이지는 next_cursor 사용")
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class NodeLogResponse(BaseModel):
    """노드 시스템 로그 응답"""

    status: str = Field("success", description=DESC_STATUS)
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    cluster: str = Field(..., description="클러스터 이름")
    node: str = Field(..., description="노드 이름")
    data: list[LogEntry] = Field([], description="로그 목록. direction 순서(backward: 최신 먼저)")
    pagination: LogPagination = Field(..., description="페이지 정보. 다음 페이지는 next_cursor 사용")
    warnings: list[str] = Field([], description=DESC_WARNINGS)


# ---------------------------------------------------------------------------
# 라벨 / 볼륨
# ---------------------------------------------------------------------------

class LabelListResponse(BaseModel):
    """로그 라벨 이름 목록 응답"""

    status: str = Field("success", description=DESC_STATUS)
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    data: list[str] = Field([], description="라벨 이름 목록 (예: namespace, job, pod)")
    warnings: list[str] = Field(default_factory=list, description=DESC_WARNINGS + ". LOKI_UNAVAILABLE: Loki 조회 실패로 data 가 빈 경우")


class LabelValuesResponse(BaseModel):
    """로그 라벨 값 목록 응답"""

    status: str = Field("success", description=DESC_STATUS)
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    label: str = Field(..., description="조회한 라벨 이름")
    data: list[str] = Field([], description="라벨 값 목록")
    warnings: list[str] = Field(default_factory=list, description=DESC_WARNINGS + ". LOKI_UNAVAILABLE: Loki 조회 실패로 data 가 빈 경우")


class VolumeEntry(BaseModel):
    """라벨 조합 하나의 로그 양"""

    labels: dict[str, str] = Field(default_factory=dict, description="라벨 조합 (키: 라벨 이름, 값: 라벨 값)")
    volume: str = Field("0", description="로그 크기(bytes). 숫자 문자열")


class VolumeResponse(BaseModel):
    """로그 볼륨 통계 응답"""

    status: str = Field("success", description=DESC_STATUS)
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    data: list[VolumeEntry] = Field([], description="라벨 조합별 로그 양 목록")
    warnings: list[str] = Field(default_factory=list, description=DESC_WARNINGS + ". LOKI_UNAVAILABLE: Loki 조회 실패로 data 가 빈 경우")
