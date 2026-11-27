"""복구 정보 응답 스키마."""
from typing import Optional

from pydantic import BaseModel, Field

from app.schemas._common import DESC_OBSERVED_AT, DESC_STATUS, DESC_WARNINGS, _now
from app.schemas.faults import AffectedPod, FaultTarget
from app.schemas.recovery_policies import PolicyStep
from app.schemas.resource_map import TraceHop


class RecommendationSummary(BaseModel):
    """장애 하나에 대한 복구 정보 요약."""

    recommendation_id: str = Field(..., description="복구 정보 ID. 장애 하나에 하나")
    fault_id: str = Field(..., description="대상 장애 ID. `/faults/{fault_id}` 로 증거 조회")
    fault_type: str = Field(..., description="장애 유형")
    fault_status: str = Field(..., description="장애 상태 (active | resolved | unknown)")
    severity: str = Field(..., description="장애 심각도 (critical | warning | info)")
    priority: str = Field(..., description="처리 우선순위 (urgent | normal | info). 심각도 critical 이 urgent")
    summary: str = Field("", description="장애 한 줄 설명")
    target: FaultTarget = Field(..., description="장애 대상")
    detected_at: Optional[str] = Field(None, description="장애 발생 시각. 모르면 null")
    policy_id: str = Field(..., description="적용한 후속 조치 정책 ID")
    policy_name: str = Field(..., description="적용한 후속 조치 정책 이름")
    mode: str = Field(..., description="항상 recommendation-only. 조치는 관리자가 직접 실행")
    review_required: bool = Field(..., description="조치 전에 관리자 검토가 필요한지")
    step_count: int = Field(..., description="조치 절차 단계 수")


class AffectedScope(BaseModel):
    """장애 영향 범위."""

    status: str = Field(..., description="ok | unknown. unknown 이면 범위를 정하지 못한 것이고 아래 수치는 null (0 과 구분)")
    scope: Optional[str] = Field(None, description="범위를 정한 기준 (pod | accelerator | node)")
    accelerators: list[str] = Field(default_factory=list, description="영향 가속기 ID")
    pod_count: Optional[int] = Field(None, description="영향 Pod 수 (실행 중, 대기 중)")
    pods: Optional[list[AffectedPod]] = Field(None, description="영향 Pod, 최대 100개")
    workloads: Optional[list[str]] = Field(None, description="영향 워크로드 (종류/이름)")
    path: list[TraceHop] = Field(default_factory=list, description="대상에서 물리서버까지의 단계")


class Verification(BaseModel):
    """조치 뒤 복구 확인 방법."""

    post_action_window: str = Field(..., description="조치 뒤 지켜볼 시간")
    check: str = Field(..., description="무엇을 확인하면 복구로 보는지")
    reference_queries: list[str] = Field(default_factory=list, description="확인에 쓸 API 경로")


class Recommendation(RecommendationSummary):
    """복구 정보 상세."""

    probable_cause: str = Field(..., description="추정 원인. `/faults/{fault_id}` 의 analysis 와 같음")
    contributing_factors: list[str] = Field(default_factory=list, description="기여 요인")
    confidence: str = Field(..., description="추정 확신 정도 (high | medium | low)")
    recommended_actions: list[PolicyStep] = Field(..., description="조치 절차. 참고 조회식의 자리가 장애 대상 값으로 채워져 있고, 채울 값이 없는 조회식은 빠짐")
    affected_scope: AffectedScope = Field(..., description="영향 범위")
    verification: Verification = Field(..., description="조치 뒤 확인 방법")
    generated_at: str = Field(..., description="생성 시각 (ISO 8601, UTC). 저장하지 않고 조회할 때마다 만든다")


class RecommendationListResponse(BaseModel):
    status: str = Field("success", description=DESC_STATUS)
    data: list[RecommendationSummary] = Field(..., description="복구 정보 요약 목록")
    total: int = Field(..., description="반환한 개수")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field(default_factory=list, description=DESC_WARNINGS)


class RecommendationResponse(BaseModel):
    status: str = Field("success", description=DESC_STATUS)
    data: Recommendation = Field(..., description="복구 정보 1건")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field(default_factory=list, description=DESC_WARNINGS)
