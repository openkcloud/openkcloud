"""
LLM 서빙 SLO(TTFT·TPOT) API v2 Pydantic 스키마.

vLLM 이 내보내는 히스토그램을 Prometheus 에서 읽어 분위수, 목표 달성률, 남은 오류 예산을 계산한다.
"""
from typing import Optional

from pydantic import BaseModel, Field

from app.schemas._common import DESC_OBSERVED_AT, DESC_STATUS, DESC_TOTAL, DESC_WARNINGS, _now


class LatencySlo(BaseModel):
    """지연 지표 하나(TTFT 또는 TPOT)의 분위수와 목표 달성 현황."""

    p50_ms: Optional[float] = Field(None, description="중앙값(ms). 기간 안에 요청이 없으면 null")
    p95_ms: Optional[float] = Field(None, description="95% 요청이 이 값 이하(ms). 요청이 없으면 null")
    p99_ms: Optional[float] = Field(None, description="99% 요청이 이 값 이하(ms). 요청이 없으면 null")
    target_ms: float = Field(..., description="목표값(ms). 이 시간 안에 끝난 요청을 목표 달성으로 셈")
    attainment_percent: Optional[float] = Field(
        None, description="목표 달성 요청 비율(%). 히스토그램 구간 사이는 직선으로 보간, 요청이 없으면 null"
    )
    error_budget_remaining_percent: Optional[float] = Field(
        None,
        description="남은 오류 예산(%). 100 이면 미달 요청 없음, 0 이면 허용치를 다 씀, 음수면 허용치 초과. 요청이 없으면 null",
    )


class SloThroughput(BaseModel):
    """기간 평균 처리량."""

    requests_per_sec: Optional[float] = Field(None, description="초당 완료 요청 수. 미수집 시 null")
    tokens_per_sec: Optional[float] = Field(None, description="초당 생성 토큰 수. 미수집 시 null")


class SloLoad(BaseModel):
    """느려졌을 때 원인을 좁히는 서빙 엔진 상태."""

    running: Optional[float] = Field(None, description="지금 처리 중인 요청 수. 미수집 시 null")
    waiting: Optional[float] = Field(None, description="지금 대기 줄에 선 요청 수. 늘면 TTFT 증가. 미수집 시 null")
    kv_cache_usage_percent: Optional[float] = Field(
        None, description="KV 캐시(대화 기억 공간) 사용률(%). 100 에 가까우면 대기와 밀어냄 발생. 미수집 시 null"
    )
    preemptions: Optional[float] = Field(
        None, description="기간 안에 메모리 부족으로 처리 중 요청을 밀어낸 횟수. 미수집 시 null"
    )


class SloService(BaseModel):
    """LLM 서빙 하나(클러스터 + 모델)의 SLO 현황."""

    cluster: str = Field(..., description="클러스터 이름 (예: l40s)")
    model: str = Field(..., description="서빙 중인 모델 이름. vLLM 실행 시 지정한 이름")
    nodes: list[str] = Field(default_factory=list, description="이 모델을 서빙하는 노드 이름 목록")
    window: str = Field(..., description="계산 기간 (예: 5m, 1h, 1d)")
    objective_percent: float = Field(..., description="달성 목표(%). 전체 요청 중 목표값 안에 끝나야 하는 비율")
    slo_status: str = Field(
        ...,
        description="SLO 판정 (met | violated | no_traffic). violated: TTFT 나 TPOT 달성률이 달성 목표 미만",
    )
    ttft: LatencySlo = Field(..., description="첫 글자가 나오기까지 걸린 시간(TTFT)")
    tpot: LatencySlo = Field(..., description="첫 글자 이후 글자 하나당 걸린 시간(TPOT). 요청별 평균값의 분포")
    throughput: SloThroughput = Field(..., description="기간 평균 처리량")
    load: SloLoad = Field(..., description="서빙 엔진 상태")


class SloServiceListResponse(BaseModel):
    """SLO 서비스 목록 응답."""

    status: str = Field(..., description=DESC_STATUS)
    items: list[SloService] = Field(default_factory=list, description="서빙별 SLO 현황 목록")
    total: int = Field(0, description=DESC_TOTAL)
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class SloServiceResponse(BaseModel):
    """SLO 서비스 상세 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: Optional[SloService] = Field(None, description="SLO 현황. 서빙을 찾지 못하면 null")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class SloPoint(BaseModel):
    """시계열 한 점."""

    time: str = Field(..., description="시각 (ISO 8601, UTC)")
    ttft_p95_ms: Optional[float] = Field(None, description="구간 TTFT p95(ms). 구간에 요청이 없으면 null")
    tpot_p95_ms: Optional[float] = Field(None, description="구간 TPOT p95(ms). 구간에 요청이 없으면 null")
    violated: bool = Field(..., description="TTFT p95 나 TPOT p95 가 목표값을 넘었는지 여부")


class SloTimeseriesResponse(BaseModel):
    """SLO 시계열 응답."""

    status: str = Field(..., description=DESC_STATUS)
    cluster: str = Field(..., description="클러스터 이름")
    model: str = Field(..., description="모델 이름")
    ttft_target_ms: float = Field(..., description="TTFT 목표값(ms)")
    tpot_target_ms: float = Field(..., description="TPOT 목표값(ms)")
    points: list[SloPoint] = Field(default_factory=list, description="시각순 (TTFT p95, TPOT p95, 위반 여부) 목록")
    violated_points: int = Field(0, description="목표를 넘은 점 개수")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)
