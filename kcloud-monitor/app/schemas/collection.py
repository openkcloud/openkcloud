"""
수집 관리 API v2 Pydantic 스키마.

공통 응답 정책: status(success | partial | error) / observed_at / warnings.
경고 코드: NO_DATA(수집 대상 없음), UPSTREAM_ERROR(Prometheus 수집 대상 목록 조회 실패),
METRIC_NOT_FOUND(빼려는 메트릭이 그 수집 대상에 없음).
"""
from datetime import datetime
from typing import Any, Optional

from pydantic import BaseModel, Field

from app.schemas._common import DESC_OBSERVED_AT, DESC_STATUS, DESC_TOTAL, DESC_WARNINGS, _now


class CollectionTarget(BaseModel):
    """수집 대상 묶음 하나. 같은 설정으로 수집되는 대상들의 모음"""

    id: str = Field(
        ...,
        description=(
            "수집 대상 ID. pull 은 Prometheus 수집 설정 이름 "
            "(예: serviceMonitor/monitoring/kepler/0), push 는 push/<cluster>/<job>"
        ),
    )
    job: str = Field(..., description="메트릭의 job 라벨 값")
    cluster: Optional[str] = Field(None, description="메트릭의 cluster 라벨 값. 라벨이 없으면 null")
    source: Optional[str] = Field(None, description="메트릭의 source 라벨 값. 라벨이 없으면 null")
    mode: Optional[str] = Field(
        None,
        description=(
            "수집 방식 (pull | push). pull: Prometheus 가 직접 가져옴, "
            "push: 원격 수집기(Alloy)가 보내 줌. 판별 실패 시 null"
        ),
    )
    targets: int = Field(0, description="이 묶음에 속한 수집 지점 개수")
    targets_up: int = Field(0, description="그중 수집에 성공하고 있는 개수")
    interval_seconds: Optional[float] = Field(None, description="수집 주기(초). 확인 불가 시 null")
    interval_estimated: bool = Field(
        False, description="주기가 최근 10분 수집 횟수로 추정한 값인지 여부. push 는 항상 true"
    )
    timeout_seconds: Optional[float] = Field(None, description="수집 제한 시간(초). pull 만 제공")
    max_scrape_duration_seconds: Optional[float] = Field(
        None, description="최근 수집 1회에 걸린 시간 중 최댓값(초). pull 만 제공"
    )
    samples_per_scrape: Optional[float] = Field(
        None, description="수집 1회에 저장되는 값 개수 합계. 미수집 시 null"
    )
    samples_per_second: Optional[float] = Field(
        None, description="초당 저장되는 값 개수 (samples_per_scrape / interval_seconds). 계산 불가 시 null"
    )
    share_percent: Optional[float] = Field(
        None, description="전체 초당 저장량에서 이 묶음이 차지하는 비율(%). 계산 불가 시 null"
    )
    adjustable: bool = Field(False, description="API 로 수집 주기와 필터를 바꿀 수 있는지 여부")
    lock_reason: Optional[str] = Field(
        None,
        description=(
            "바꿀 수 없는 이유 (push | not_managed). push: 원격 수집기 설정 파일로만 변경 가능, "
            "not_managed: 서버의 허용 목록에 없음. 바꿀 수 있으면 null"
        ),
    )


class CollectionSummary(BaseModel):
    """수집 대상 전체 합계. 필터와 무관하게 전체 기준"""

    target_groups: int = Field(0, description="수집 대상 묶음 개수")
    pull_groups: int = Field(0, description="pull 방식 묶음 개수")
    push_groups: int = Field(0, description="push 방식 묶음 개수")
    adjustable_groups: int = Field(0, description="API 로 바꿀 수 있는 묶음 개수")
    samples_per_second: Optional[float] = Field(None, description="전체 초당 저장되는 값 개수. 계산 불가 시 null")


class CollectionTargetListResponse(BaseModel):
    """수집 대상 목록 응답"""

    status: str = Field(..., description=DESC_STATUS)
    data: list[CollectionTarget] = Field([], description="수집 대상 묶음 목록. 초당 저장량이 많은 순")
    total: int = Field(0, description=DESC_TOTAL)
    summary: CollectionSummary = Field(default_factory=CollectionSummary, description="전체 합계")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class CollectionMetric(BaseModel):
    """메트릭 이름 하나의 시계열 개수"""

    metric: str = Field(..., description="메트릭 이름")
    series: int = Field(0, description="이 이름으로 저장 중인 시계열 개수")
    share_percent: Optional[float] = Field(
        None, description="같은 job 전체 시계열에서 차지하는 비율(%). 계산 불가 시 null"
    )
    protected: bool = Field(False, description="이 API 가 조회에 쓰는 메트릭이라 수집에서 뺄 수 없는지 여부")


class CollectionMetricListResponse(BaseModel):
    """job 하나의 메트릭별 시계열 개수 응답"""

    status: str = Field(..., description=DESC_STATUS)
    job: str = Field(..., description="조회한 job 라벨 값")
    cluster: Optional[str] = Field(None, description="조회한 cluster 라벨 값. 지정하지 않았으면 null")
    data: list[CollectionMetric] = Field([], description="메트릭 목록. 시계열이 많은 순")
    total_series: Optional[int] = Field(None, description="이 job 의 전체 시계열 개수. 조회 실패 시 null")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


# ---------------------------------------------------------------------------
# 수집 정책
# ---------------------------------------------------------------------------

class CollectionPolicyCreate(BaseModel):
    """수집 정책 생성 요청"""

    name: str = Field(..., description="정책 이름")
    description: str = Field("", description="정책 설명")
    target_id: str = Field(..., description="수집 대상 ID. 수집 대상 목록의 id 값")
    interval_seconds: Optional[int] = Field(None, description="바꿀 수집 주기(초, 5 ~ 60). 주기를 안 바꾸면 생략")
    drop_metrics: list[str] = Field([], description="수집에서 뺄 메트릭 이름 목록. 이름을 정확히 적어야 하고 패턴은 불가")


class CollectionPolicyPatch(BaseModel):
    """수집 정책 수정 요청. 보낸 항목만 변경"""

    name: Optional[str] = Field(None, description="정책 이름")
    description: Optional[str] = Field(None, description="정책 설명")
    target_id: Optional[str] = Field(None, description="수집 대상 ID")
    interval_seconds: Optional[int] = Field(None, description="바꿀 수집 주기(초, 5 ~ 60). null 을 보내면 주기 변경 취소")
    drop_metrics: Optional[list[str]] = Field(None, description="수집에서 뺄 메트릭 이름 목록. 통째로 교체")


class CollectionPolicy(BaseModel):
    """저장된 수집 정책 1건"""

    id: str = Field(..., description="정책 ID")
    name: str = Field(..., description="정책 이름")
    description: str = Field("", description="정책 설명")
    target_id: str = Field(..., description="수집 대상 ID")
    interval_seconds: Optional[int] = Field(None, description="바꿀 수집 주기(초). 주기를 안 바꾸는 정책이면 null")
    drop_metrics: list[str] = Field([], description="수집에서 뺄 메트릭 이름 목록")
    state: str = Field(
        "draft",
        description=(
            "정책 상태 (draft | pending | applied). draft: 저장만 된 상태, "
            "pending: 적용했고 확정 대기 중, applied: 확정 완료"
        ),
    )
    applied_at: Optional[datetime] = Field(None, description="적용 시각 (ISO 8601, UTC). draft 면 null")
    confirm_deadline: Optional[datetime] = Field(
        None, description="이 시각까지 확정하지 않으면 자동으로 되돌림 (ISO 8601, UTC). pending 일 때만 값 있음"
    )
    created_at: datetime = Field(..., description="생성 시각 (ISO 8601, UTC)")
    updated_at: datetime = Field(..., description="마지막 수정 시각 (ISO 8601, UTC)")


class CollectionPolicyResponse(BaseModel):
    """수집 정책 단건 응답"""

    status: str = Field("success", description=DESC_STATUS)
    data: CollectionPolicy = Field(..., description="수집 정책 1건")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class CollectionPolicyListResponse(BaseModel):
    """수집 정책 목록 응답"""

    status: str = Field("success", description=DESC_STATUS)
    data: list[CollectionPolicy] = Field([], description="수집 정책 목록. 생성 시각 오래된 순")
    total: int = Field(0, description="정책 개수")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class CollectionLoad(BaseModel):
    """수집 대상 하나의 주기와 저장량"""

    interval_seconds: Optional[float] = Field(None, description="수집 주기(초). 확인 불가 시 null")
    samples_per_scrape: Optional[float] = Field(None, description="수집 1회에 저장되는 값 개수. 미수집 시 null")
    samples_per_second: Optional[float] = Field(None, description="초당 저장되는 값 개수. 계산 불가 시 null")


class CollectionDroppedMetric(BaseModel):
    """정책이 빼려는 메트릭 하나"""

    metric: str = Field(..., description="메트릭 이름")
    series: int = Field(0, description="이 수집 대상에서 지금 저장 중인 시계열 개수. 0 이면 대상에 없는 메트릭")


class CollectionPreview(BaseModel):
    """정책을 적용했을 때의 예상 결과"""

    policy_id: str = Field(..., description="정책 ID")
    target_id: str = Field(..., description="수집 대상 ID")
    applicable: bool = Field(False, description="이 대상에 정책을 실제로 적용할 수 있는지 여부")
    lock_reason: Optional[str] = Field(
        None, description="적용할 수 없는 이유 (not_managed: 서버의 허용 목록에 없음). 적용 가능하면 null"
    )
    current: CollectionLoad = Field(..., description="지금 주기와 저장량")
    expected: CollectionLoad = Field(..., description="적용 후 예상 주기와 저장량")
    dropped: list[CollectionDroppedMetric] = Field([], description="빼려는 메트릭별 시계열 개수")
    saved_samples_per_second: Optional[float] = Field(
        None, description="줄어드는 초당 저장량. 주기를 줄이면 음수(저장량 증가). 계산 불가 시 null"
    )
    saved_target_percent: Optional[float] = Field(None, description="이 수집 대상 저장량 대비 줄어드는 비율(%)")
    saved_total_percent: Optional[float] = Field(None, description="전체 저장량 대비 줄어드는 비율(%)")


class CollectionPreviewResponse(BaseModel):
    """수집 정책 미리보기 응답"""

    status: str = Field(..., description=DESC_STATUS)
    data: Optional[CollectionPreview] = Field(None, description="예상 결과. 대상이 지금 수집 중이 아니면 null")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class CollectionHistoryItem(BaseModel):
    """수집 설정 변경 이력 1건"""

    id: int = Field(..., description="이력 번호")
    ts: datetime = Field(..., description="기록 시각 (ISO 8601, UTC)")
    policy_id: str = Field(..., description="정책 ID")
    target_id: str = Field(..., description="수집 대상 ID")
    action: str = Field(
        ...,
        description=(
            "한 일 (apply | confirm | rollback | auto_rollback). "
            "auto_rollback: 서버가 자동으로 되돌림"
        ),
    )
    reason: str = Field(
        "",
        description=(
            "자동으로 되돌린 이유 (confirm_timeout: 제한 시간 안에 확정하지 않음, "
            "health_check_failed: 적용 후 수집 성공 개수 감소, config_gone: 수집 설정이 사라짐). 그 외에는 빈 문자열"
        ),
    )
    actor: str = Field("", description="요청한 사용자. 서버가 자동으로 한 일이면 system")
    before: Optional[dict[str, Any]] = Field(
        None, description="바꾸기 전 수집 주기(interval)와 필터 규칙(metric_relabelings). confirm 이면 null"
    )
    after: Optional[dict[str, Any]] = Field(
        None, description="바꾼 후 수집 주기(interval)와 필터 규칙(metric_relabelings). confirm 이면 null"
    )


class CollectionHistoryResponse(BaseModel):
    """수집 설정 변경 이력 응답"""

    status: str = Field("success", description=DESC_STATUS)
    data: list[CollectionHistoryItem] = Field([], description="이력 목록. 기록 시각 최신순")
    total: int = Field(0, description="반환한 이력 개수")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)
