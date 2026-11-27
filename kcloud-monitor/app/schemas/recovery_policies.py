"""장애 후속 조치 정책 요청, 응답 스키마."""
from typing import Literal, Optional

from pydantic import BaseModel, Field

from app.schemas._common import DESC_OBSERVED_AT, DESC_STATUS, DESC_WARNINGS, _now

FaultType = Literal["accelerator_error", "npu_fault", "workload_oom", "workload_crash_loop", "node_not_ready",
                    "vm_down", "hardware_fault"]


class PolicyStepBody(BaseModel):
    """조치 절차의 단계 하나. 순서는 목록 순서."""

    name: str = Field(..., min_length=1, max_length=100, description="단계 이름")
    action_type: Literal["manual_review", "manual_action", "record_feedback"] = Field(
        ..., description="단계 종류 (manual_review: 확인 | manual_action: 관리자가 직접 실행하는 조치 | record_feedback: 결과 기록)")
    guidance: str = Field("", max_length=1000, description="무엇을 확인하거나 실행할지 적은 안내문")
    reference_queries: list[str] = Field(
        default_factory=list, max_length=10,
        description="확인에 쓸 API 경로나 조회식. `{fault_id}`, `{cluster}`, `{node}`, `{namespace}`, `{pod}`, `{container}`, "
                    "`{accelerator_id}`, `{vm_id}` 자리는 복구 정보를 만들 때 장애 대상 값으로 채워진다")
    risk_level: Optional[Literal["low", "medium", "high"]] = Field(
        None, description="조치 위험도 (low | medium | high). manual_action 은 필수")


class PolicyStep(PolicyStepBody):
    step: int = Field(..., description="단계 번호 (1부터)")


class RecoveryPolicyBody(BaseModel):
    """후속 조치 정책 생성, 수정 본문."""

    name: str = Field(..., min_length=1, max_length=100, description="정책 이름")
    fault_type: FaultType = Field(..., description="적용할 장애 유형")
    vendor_scope: list[Literal["nvidia", "furiosa", "rebellions"]] = Field(
        default_factory=list, description="적용할 가속기 벤더. 비우면 전체")
    error_codes: list[int] = Field(default_factory=list, max_length=50,
                                   description="적용할 XID 코드. 비우면 전체. 지정한 정책이 지정하지 않은 정책보다 우선")
    enabled: bool = Field(True, description="사용 여부. 꺼진 정책은 복구 정보에 쓰이지 않음")
    mode: Literal["recommendation-only"] = Field(
        "recommendation-only", description="동작 방식. 권고 전용만 허용 (자동 실행 없음)")
    description: str = Field("", max_length=500, description="정책 설명")
    review_required: bool = Field(True, description="조치 전에 관리자 검토가 필요한지")
    steps: list[PolicyStepBody] = Field(..., min_length=1, max_length=20, description="조치 절차, 순서대로")


class RecoveryPolicy(RecoveryPolicyBody):
    policy_id: str = Field(..., description="정책 ID")
    steps: list[PolicyStep] = Field(..., description="조치 절차, 순서대로")
    created_at: Optional[str] = Field(None, description="생성 시각. 저장소 없이 초기 정책을 쓸 때는 null")
    updated_at: Optional[str] = Field(None, description="수정 시각")


class RecoveryPolicyListResponse(BaseModel):
    status: str = Field("success", description=DESC_STATUS)
    data: list[RecoveryPolicy] = Field(..., description="정책 목록")
    total: int = Field(..., description="반환한 정책 개수")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field(default_factory=list, description=DESC_WARNINGS)


class RecoveryPolicyResponse(BaseModel):
    status: str = Field("success", description=DESC_STATUS)
    data: RecoveryPolicy = Field(..., description="정책 1건")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field(default_factory=list, description=DESC_WARNINGS)
