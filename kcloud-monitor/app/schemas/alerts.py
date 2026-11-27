"""
알람 API v2 Pydantic 스키마.

정책(policy)은 "지표가 선을 얼마 동안 넘으면 어느 채널로 알린다" 한 문장을 저장한 것.
kind=resource 는 가속기 메트릭(Prometheus), kind=log 는 로그 단어 건수(Loki),
kind=slo 는 vLLM 서빙 지연과 대기 요청 수(Prometheus),
kind=composite 는 조건 2개 이상을 같은 클러스터에서 모두 만족할 때.
"""
from datetime import datetime
from typing import Annotated, Any, Literal, Optional

from pydantic import BaseModel, BeforeValidator, Field

from app.schemas._common import DESC_OBSERVED_AT, DESC_STATUS, DESC_WARNINGS, _now


Severity = Literal["info", "warning", "critical"]
Op = Literal[">=", ">", "<=", "<", "==", "!="]
ChannelType = Literal["email", "slack", "webhook"]
_lower = BeforeValidator(lambda v: v.lower() if isinstance(v, str) else v)


class AlertTarget(BaseModel):
    """알람 정책의 감시 대상 범위, 모두 비우면 가속기 전체, cluster 만 지정하면 그 클러스터 전체"""

    cluster: Optional[str] = Field(None, description="클러스터 이름 (예: l40s, furiosa, rebellions), 미지정 시 전체")
    node: Optional[str] = Field(None, description="노드 이름, resource 정책만 사용, 미지정 시 전체")
    acc_id: Optional[str] = Field(None, description="가속기 ID, resource 정책만 사용, 미지정 시 전체")
    model: Optional[str] = Field(None, description="vLLM 모델 이름 (예: qwen2.5-3b), slo 정책만 사용, 미지정 시 전체")
    unit: Optional[str] = Field(None, description="systemd 유닛 이름 (예: docker.service), log 조건만 사용, 미지정 시 전체")
    container: Optional[str] = Field(None, description="컨테이너 이름 (예: vllm-slo), log 조건만 사용, 미지정 시 전체")


class Condition(BaseModel):
    """정책의 조건 1개, 지표가 기준을 넘는지 보는 단위"""

    metric: str = Field(..., description="지표 키, GET /alerts/metrics 의 key 중 하나")
    op: Op = Field(">=", description="비교 연산자 (>= | > | <= | < | == | !=)")
    threshold: float = Field(..., description="임계값, 단위는 지표 목록의 unit")
    window_min: int = Field(5, ge=1, le=1440, description="log, slo 지표만 사용, 건수를 세거나 p95 를 계산하는 기간(분)")


class PolicyCreate(BaseModel):
    """알람 정책 생성 요청 (화면 입력 형식)"""

    name: str = Field(..., description="정책 이름")
    description: str = Field("", description="정책 설명")
    metric: Optional[str] = Field(None, description="지표 키, GET /alerts/metrics 의 key 중 하나, 조건이 1개일 때 쓰는 짧은 형식 (conditions 를 주면 무시)")
    op: Op = Field(">=", description="비교 연산자 (>= | > | <= | < | == | !=), 조건이 1개일 때")
    threshold: Optional[float] = Field(None, description="임계값, 단위는 지표 목록의 unit, 조건이 1개일 때")
    duration: str = Field("0m", description="이 시간 동안 조건이 계속되면 발생 (예: 5m), 0m 이면 즉시")
    window_min: int = Field(5, ge=1, le=1440, description="log, slo 지표만 사용, 건수를 세거나 p95 를 계산하는 기간(분), 조건이 1개일 때")
    conditions: list[Condition] = Field(
        default_factory=list,
        description="조건 목록, 2개 이상이면 같은 클러스터에서 모두 만족할 때 발생(그리고), 주면 metric, op, threshold, window_min 은 무시",
    )
    selector: str = Field(
        "",
        description="감시 대상 (예: cluster=l40s,node=gpu-node-1), 비우면 전체, log 는 cluster, unit, container, slo 는 cluster, model, 조건 2개 이상이면 cluster, unit, container 만 사용",
    )
    severity: Annotated[Severity, _lower] = Field("warning", description="심각도 (info | warning | critical), 대소문자 무시")
    channels: list[ChannelType] = Field(
        default_factory=list,
        description="알림 받을 채널 종류 (email | slack | webhook), 비우면 알림 없음",
    )
    enabled: bool = Field(True, description="정책 사용 여부")


class PolicyPatch(BaseModel):
    """알람 정책 수정 요청, 보낸 항목만 변경"""

    name: Optional[str] = Field(None, description="정책 이름")
    description: Optional[str] = Field(None, description="정책 설명")
    metric: Optional[str] = Field(None, description="첫 번째 조건의 지표 키")
    op: Optional[Op] = Field(None, description="첫 번째 조건의 비교 연산자 (>= | > | <= | < | == | !=)")
    threshold: Optional[float] = Field(None, description="첫 번째 조건의 임계값, 단위는 지표 목록의 unit")
    duration: Optional[str] = Field(None, description="지속 시간 (예: 5m)")
    window_min: Optional[int] = Field(None, ge=1, le=1440, description="첫 번째 조건의 집계 기간(분), log, slo 지표만 사용")
    conditions: Optional[list[Condition]] = Field(None, description="조건 목록을 통째로 교체, 1개면 단일 정책, 2개 이상이면 조합 정책")
    selector: Optional[str] = Field(None, description="감시 대상 (예: cluster=l40s), 빈 문자열이면 전체")
    severity: Optional[Annotated[Severity, _lower]] = Field(None, description="심각도 (info | warning | critical)")
    channels: Optional[list[ChannelType]] = Field(None, description="알림 받을 채널 종류 (email | slack | webhook)")
    enabled: Optional[bool] = Field(None, description="정책 활성 여부")


class PolicyChannel(BaseModel):
    """정책에 연결된 채널 1개 (화면 표시용)"""

    id: str = Field(..., description="채널 ID")
    name: str = Field(..., description="채널 이름")
    enabled: bool = Field(..., description="채널 활성 여부, false 면 이 채널로는 발송 안 함")


class Policy(BaseModel):
    """저장된 알람 정책"""

    id: str = Field(..., description="정책 ID")
    name: str = Field(..., description="정책 이름")
    description: str = Field(..., description="정책 설명")
    kind: Literal["resource", "log", "slo", "composite"] = Field(..., description="정책 종류 (resource | log | slo | composite), resource: 가속기 메트릭 기준, log: 로그 건수 기준, slo: vLLM 서빙 지연 기준, composite: 조건 2개 이상을 모두 만족")
    conditions: list[Condition] = Field(..., description="조건 목록, 1개면 단일 정책, 2개 이상이면 같은 클러스터에서 모두 만족할 때 발생")
    metric: str = Field(..., description="첫 번째 조건의 지표 키 (conditions[0] 과 같음, 옛 화면 호환용)")
    op: Op = Field(..., description="첫 번째 조건의 비교 연산자")
    threshold: float = Field(..., description="첫 번째 조건의 임계값")
    window_min: int = Field(..., description="첫 번째 조건의 집계 창(분)")
    for_min: int = Field(..., description="지속 시간(분)")
    severity: Severity = Field(..., description="심각도 (info | warning | critical)")
    target: AlertTarget = Field(..., description="감시 대상 범위 (cluster, node, acc_id, model, unit, container)")
    selector: str = Field("", description="감시 대상을 입력 형식으로 적은 것 (예: cluster=l40s), 빈 값이면 전체, 화면 표시용")
    duration: str = Field("0m", description="지속 시간을 입력 형식으로 적은 것 (예: 5m), 화면 표시용")
    channel_ids: list[str] = Field(..., description="알림을 보낼 채널 ID 목록, 비어 있으면 알림 발송 없음")
    channels: dict[ChannelType, list[PolicyChannel]] = Field(
        default_factory=dict,
        description="연결된 채널을 종류(email | slack | webhook)별로 묶은 것, 연결된 채널이 없는 종류와 삭제된 채널은 빠짐",
    )
    enabled: bool = Field(..., description="정책 활성 여부")
    created_at: datetime = Field(..., description="생성 시각 (ISO 8601, UTC)")
    updated_at: datetime = Field(..., description="마지막 수정 시각 (ISO 8601, UTC)")


class MetricItem(BaseModel):
    """알람 정책에 고를 수 있는 지표 1개"""

    key: str = Field(..., description="지표 키, 정책 생성 시 metric 에 그대로 넣음")
    kind: Literal["resource", "log", "slo"] = Field(..., description="정책 종류 (resource | log | slo), resource: 가속기 메트릭, log: 로그 건수, slo: vLLM 서빙 지연")
    label: str = Field(..., description="화면 표시용 이름")
    unit: str = Field(..., description="임계값 단위 (%, °C, W, s, ms, 건)")


_CHANNEL_CONFIG_DESC = (
    "email: to(수신 주소 목록), "
    "webhook: url(받을 주소), format(generic | slack), headers(선택)"
)
_CHANNEL_CONFIG_EXAMPLES = [
    {"to": ["ops@example.com"]},
    {"url": "https://hooks.slack.com/services/T000/B000/XXXX", "format": "slack"},
    {"url": "https://portal.example.com/alerts", "headers": {"X-Token": "..."}},
]


class ChannelCreate(BaseModel):
    """알림 채널 생성 요청"""

    model_config = {"json_schema_extra": {"examples": [
        {"name": "운영팀 메일", "type": "email", "config": {"to": ["ops@example.com"]}},
    ]}}

    name: str = Field(..., description="채널 이름")
    type: Literal["webhook", "email"] = Field(..., description="채널 종류 (email | webhook), email: 메일 발송, webhook: 지정한 주소로 HTTP POST 전송 (Slack, 포털 등)")
    config: dict[str, Any] = Field(default_factory=dict, description=_CHANNEL_CONFIG_DESC, examples=_CHANNEL_CONFIG_EXAMPLES)
    notify_resolved: bool = Field(True, description="해제(resolved) 알림 발송 여부")
    enabled: bool = Field(True, description="채널 활성 여부, false 면 알림 발송 안 함")


class ChannelPatch(BaseModel):
    """알림 채널 수정 요청, 보낸 항목만 변경, 채널 종류(type)는 변경 불가"""

    name: Optional[str] = Field(None, description="채널 이름")
    config: Optional[dict[str, Any]] = Field(
        None, description=_CHANNEL_CONFIG_DESC + ", 보내면 통째로 교체",
        examples=_CHANNEL_CONFIG_EXAMPLES,
    )
    notify_resolved: Optional[bool] = Field(None, description="해제(resolved) 알림 발송 여부")
    enabled: Optional[bool] = Field(None, description="채널 활성 여부")


class Channel(ChannelCreate):
    """저장된 알림 채널"""

    id: str = Field(..., description="채널 ID")
    created_at: datetime = Field(..., description="생성 시각 (ISO 8601, UTC)")


class ActiveAlert(BaseModel):
    """정책 조건에 걸린 알람 1건"""

    fingerprint: str = Field(..., description="알람 식별값, 정책 ID 와 대상으로 만든 값이며 같은 값은 한 건으로 묶임")
    policy_id: str = Field(..., description="알람을 만든 정책 ID")
    state: Literal["pending", "firing", "resolved"] = Field(..., description="알람 상태 (pending | firing | resolved), pending: 지속 시간 채우는 중, firing: 발생 중, resolved: 해제")
    severity: Severity = Field(..., description="심각도 (info | warning | critical)")
    target: dict[str, Any] = Field(..., description="알람 대상 (cluster, node, acc_id, model 중 해당 키)")
    value: Optional[float] = Field(None, description="마지막으로 평가한 지표 값, 값이 없으면 null")
    message: str = Field(..., description="알람 메시지")
    started_at: datetime = Field(..., description="조건이 처음 충족된 시각 (ISO 8601, UTC)")
    fired_at: Optional[datetime] = Field(None, description="발생(firing) 시각 (ISO 8601, UTC), pending 상태면 null")
    resolved_at: Optional[datetime] = Field(None, description="해제 시각 (ISO 8601, UTC), 해제 전이면 null")
    last_notified_at: Optional[datetime] = Field(None, description="마지막 알림 발송 시각 (ISO 8601, UTC), 발송 전이면 null")
    acked_at: Optional[datetime] = Field(None, description="사용자 확인(ack) 시각 (ISO 8601, UTC), 확인 후 해제까지 재알림 없음, 미확인 시 null")
    duration_seconds: int = Field(..., description="조건을 넘은 채로 지속된 시간(초), 해제된 알람은 시작 시각부터 해제 시각까지")
    for_seconds: int = Field(..., description="정책의 지속 시간 기준(초)")


class AlertEvent(BaseModel):
    """알람 이력 1건"""

    id: int = Field(..., description="이벤트 ID")
    ts: datetime = Field(..., description="기록 시각 (ISO 8601, UTC)")
    policy_id: str = Field(..., description="정책 ID")
    fingerprint: str = Field(..., description="알람 식별값")
    kind: Literal["fire", "resolve", "notified", "notify_failed"] = Field(
        ..., description="이벤트 종류 (fire | resolve | notified | notify_failed), fire: 발생, resolve: 해제, notified: 알림 발송, notify_failed: 알림 발송 실패"
    )
    severity: Severity = Field(..., description="심각도 (info | warning | critical)")
    target: dict[str, Any] = Field(..., description="알람 대상 (cluster, node, acc_id, model 중 해당 키)")
    value: Optional[float] = Field(None, description="기록 시점의 지표 값, 값이 없으면 null")
    message: str = Field(..., description="이벤트 메시지")


class PreviewItem(BaseModel):
    """알람 정책 미리 평가 결과 1건"""

    target: dict[str, Any] = Field(..., description="평가 대상 (cluster, node, acc_id, model 중 해당 키)")
    value: float = Field(..., description="현재 값, resource, slo 정책은 지표 값, log 정책은 집계 창 동안의 건수, 조합 정책은 첫 번째 조건의 값")
    cond: bool = Field(..., description="현재 값 기준 조건 충족 여부, 조합 정책은 모든 조건 충족 여부")
    values: Optional[list[dict[str, Any]]] = Field(None, description="조합 정책의 조건별 지표 키, 현재 값, 충족 여부 목록, 단일 정책은 null")


class ListResponse(BaseModel):
    """알람 목록 응답"""

    status: str = Field("success", description=DESC_STATUS)
    data: list[Any] = Field(..., description="항목 목록, 정책, 채널, 활성 알람, 이력, 미리 평가 결과 중 하나")
    total: int = Field(..., description="반환한 항목 개수")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field(default_factory=list, description=DESC_WARNINGS)


class ItemResponse(BaseModel):
    """알람 단건 응답"""

    status: str = Field("success", description=DESC_STATUS)
    data: Any = Field(..., description="항목 1건, 정책, 채널, 활성 알람 또는 시험 발송 결과")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field(default_factory=list, description=DESC_WARNINGS)
