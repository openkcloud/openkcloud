"""장애 정보 분석 응답 스키마."""
from typing import Any, Optional

from pydantic import BaseModel, Field

from app.schemas._common import DESC_OBSERVED_AT, DESC_STATUS, DESC_WARNINGS, _now
from app.schemas.resource_map import TraceHop


class FaultTarget(BaseModel):
    """장애 대상. 탐지 근거가 알려 주는 항목만 채워진다."""

    cluster: Optional[str] = Field(None, description="클러스터 이름")
    node: Optional[str] = Field(None, description="노드 이름")
    namespace: Optional[str] = Field(None, description="네임스페이스 (워크로드 장애)")
    pod: Optional[str] = Field(None, description="Pod 이름 (워크로드 장애)")
    container: Optional[str] = Field(None, description="컨테이너 이름 (워크로드 장애)")
    accelerator_id: Optional[str] = Field(None, description="가속기 ID (NVIDIA 는 UUID, Furiosa 는 device)")
    vendor: Optional[str] = Field(None, description="가속기 벤더 (nvidia | furiosa | rebellions)")
    model: Optional[str] = Field(None, description="가속기 모델 또는 LLM 모델 이름")
    vm: Optional[str] = Field(None, description="VM 이름 (VM 장애)")
    vm_id: Optional[str] = Field(None, description="VM UUID (VM 장애)")
    physical_server: Optional[str] = Field(None, description="VM 이 올라간 물리서버 (VM 장애)")


class SourceAlert(BaseModel):
    """장애의 근거가 된 알람."""

    fingerprint: str = Field(..., description="알람 식별자. `/alerts/active/{fingerprint}/ack` 에 쓰는 값")
    policy_id: str = Field(..., description="알람 정책 ID")
    policy_name: Optional[str] = Field(None, description="알람 정책 이름")


class Fault(BaseModel):
    """장애 하나."""

    fault_id: str = Field(..., description="장애 ID. 같은 장애는 조회할 때마다 같은 값")
    fault_type: str = Field(..., description="장애 유형 (accelerator_error | npu_fault | workload_oom | workload_crash_loop | node_not_ready | vm_down | hardware_fault)")
    severity: str = Field(..., description="심각도 (critical | warning | info). 알람과 같은 구분. 고정 신호는 critical, warning 만")
    status: str = Field(..., description="상태 (active | resolved | unknown). unknown: 알람 정책이 지워져 해제 시각을 모름")
    source: str = Field(..., description="탐지 경로 (alert: 알람 정책 | signal: 고정 신호)")
    trigger: Optional[str] = Field(None, description="탐지 근거. alert 면 알람 지표 키(예: log_oom), signal 이면 신호 키(xid | oom_killed | restart_loop | npu_dead | npu_unhealthy | node_not_ready | vm_error)")
    detected_at: Optional[str] = Field(None, description="발생 시각 (ISO 8601, UTC). 고정 신호는 OOM 종료만 시각을 알고 나머지는 null")
    resolved_at: Optional[str] = Field(None, description="해제 시각. 진행 중이면 null")
    target: FaultTarget = Field(..., description="장애 대상")
    summary: str = Field("", description="한 줄 설명")
    value: Optional[float] = Field(None, description="탐지 시점 값 (알람 지표 값, 재시작 횟수 등). 없으면 null")
    xid_code: Optional[int] = Field(None, description="NVIDIA XID 코드 (xid 신호만)")
    source_alerts: list[SourceAlert] = Field(default_factory=list, description="근거 알람. 고정 신호면 빈 목록")


class MetricSeries(BaseModel):
    """증거 구간의 메트릭 시계열 하나."""

    metric: str = Field(..., description="지표 이름 (예: temperature_celsius, memory_working_set_bytes, node_ready)")
    labels: dict[str, str] = Field(default_factory=dict, description="시계열을 구분하는 라벨")
    min: float = Field(..., description="구간 최솟값")
    max: float = Field(..., description="구간 최댓값")
    last: float = Field(..., description="구간 마지막 값")
    points: list[list[Any]] = Field(default_factory=list, description="[시각(ISO 8601), 값] 목록, 1분 간격")


class LogSnapshot(BaseModel):
    """증거 구간의 로그 한 줄."""

    timestamp: str = Field(..., description="로그 시각 (ISO 8601)")
    message: str = Field(..., description="로그 원문 (최대 500자)")
    labels: dict[str, str] = Field(default_factory=dict, description="노드, Pod, 컨테이너, unit 라벨")
    detected_fields: dict[str, Any] = Field(default_factory=dict, description="원문에서 뽑은 필드 (xid_code, gpu_pci_bdf, error_type, pid, process 등)")


class AffectedPod(BaseModel):
    namespace: Optional[str] = Field(None, description="네임스페이스")
    pod: Optional[str] = Field(None, description="Pod 이름")
    workload: Optional[str] = Field(None, description="Pod 를 만든 워크로드 (종류/이름)")
    via: Optional[str] = Field(None, description="이 Pod 를 영향 범위로 본 근거")


class RelatedResources(BaseModel):
    """장애가 영향을 주는 자원."""

    scope: Optional[str] = Field(
        None, description="영향 범위를 정한 기준 (pod | accelerator | node | vm). vm 은 K8s 노드가 아닌 VM 이라 Pod 를 셀 수 없음 "
                          "(affected_pod_count null). 정하지 못했으면 null")
    path: list[TraceHop] = Field(default_factory=list, description="대상에서 물리서버까지의 단계. `/resource-map/pods/{pod}` 의 path 와 같은 형식")
    affected_pod_count: Optional[int] = Field(None, description="영향을 받는 Pod 수 (실행 중, 대기 중). 범위를 정하지 못했으면 null (0건과 구분)")
    affected_pods: Optional[list[AffectedPod]] = Field(None, description="영향을 받는 Pod, 최대 100개. 범위를 정하지 못했으면 null")


class CollectionStatus(BaseModel):
    """증거 종류별 수집 결과."""

    metrics: str = Field(..., description="ok | empty | skipped(대상을 좁힐 수 없음)")
    logs: str = Field(..., description="ok | empty | skipped | not_configured(LOKI_URL 없음) | unavailable")
    resource_map: str = Field(..., description="ok | unknown(대상이 없거나 좁힐 수 없음) | unavailable")


class Evidence(BaseModel):
    """장애 증거 스냅샷."""

    window: dict[str, str] = Field(..., description="증거 구간 (start, end). 발생 30분 전부터 10분 뒤, 발생 시각을 모르면 최근 30분")
    metrics_snapshot: list[MetricSeries] = Field(default_factory=list, description="구간 메트릭")
    logs_snapshot: list[LogSnapshot] = Field(default_factory=list, description="구간 로그, 최신 순 최대 20줄")
    related_resources: RelatedResources = Field(..., description="영향 자원")
    collection_status: CollectionStatus = Field(..., description="증거 종류별 수집 결과")


class Analysis(BaseModel):
    """증거 기반 원인 분석. 규칙으로 낸 추정이다."""

    probable_cause: str = Field(..., description="추정 원인")
    contributing_factors: list[str] = Field(default_factory=list, description="기여 요인 (과열, 메모리 한도 근접 등)")
    confidence: str = Field(..., description="확신 정도 (high: 원인을 직접 가리키는 증거 있음 | medium: 탐지 신호뿐 | low: 증거 수집이 빠져 확인 못 함)")
    evidence_basis: list[str] = Field(
        default_factory=list, description="판단에 쓴 증거 종류 (alert | signal | logs | metrics). alert, signal 은 장애를 잡은 탐지 경로")
    unverified: list[str] = Field(default_factory=list, description="수집하지 못해 확인하지 못한 증거 종류")


class FaultDetail(Fault):
    evidence: Evidence = Field(..., description="증거 스냅샷")
    analysis: Analysis = Field(..., description="원인 분석")


class FaultListResponse(BaseModel):
    status: str = Field("success", description=DESC_STATUS)
    data: list[Fault] = Field(..., description="장애 목록")
    total: int = Field(..., description="반환한 장애 개수")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field(default_factory=list, description=DESC_WARNINGS)


class FaultDetailResponse(BaseModel):
    status: str = Field("success", description=DESC_STATUS)
    data: FaultDetail = Field(..., description="장애 상세")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field(default_factory=list, description=DESC_WARNINGS)
