"""
워크로드 API v2 Pydantic 스키마.

Pod / Container / Namespace / Service 응답 모델.
"""
from typing import Optional

from pydantic import BaseModel, Field

from app.schemas._common import DESC_OBSERVED_AT, DESC_STATUS, DESC_TOTAL, DESC_WARNINGS, _now


# 같은 뜻의 필드가 여러 모델에 반복되어 문구를 한 곳에 둠
_D_NAMESPACE = "Kubernetes 네임스페이스 이름"
_D_POD = "Pod 이름"
_D_CLUSTER = "소속 클러스터 이름"
_D_NODE = "Pod가 실행 중인 노드 이름. 노드 배치 전이면 null"
_D_PHASE = "Pod 상태 (Running | Pending | Succeeded | Failed | Unknown). 미수집 시 null"
_D_POD_IP = "Pod IP. 할당 전이면 null"
_D_HOST_IP = "Pod가 실행 중인 노드의 IP"
_D_CREATED_AT = "Pod 생성 시각 (ISO 8601, UTC). 미수집 시 null"
_D_WORKLOAD_TYPE = (
    "Pod를 만든 상위 리소스 종류 (예: ReplicaSet, StatefulSet, DaemonSet, Job). "
    "상위 리소스가 없으면 null"
)
_D_WORKLOAD_NAME = "Pod를 만든 상위 리소스 이름. 상위 리소스가 없으면 null"
_D_SERVICE_NAME = (
    "서비스(애플리케이션) 이름. Pod 라벨 또는 상위 리소스 이름에서 추정, 추정 불가 시 null"
)
_D_CONTAINER_COUNT = "컨테이너 개수. 미수집 시 null"
_D_RESTART_COUNT = "컨테이너 재시작 횟수 합계. 미수집 시 null"
_D_CPU_USAGE = "CPU 사용량(코어). 최근 5분 평균, 미수집 시 null"
_D_MEM_USAGE = "메모리 사용량(bytes). 실사용(working set) 기준, 미수집 시 null"
_D_CPU_REQ = "CPU 요청량 합계(코어). 미설정 시 null"
_D_CPU_LIM = "CPU 상한값 합계(코어). 미설정 시 null"
_D_MEM_REQ = "메모리 요청량 합계(bytes). 미설정 시 null"
_D_MEM_LIM = "메모리 상한값 합계(bytes). 미설정 시 null"
_D_SOURCE = "산출 근거 (kepler). Kepler 의 컨테이너 전력 추정값"


# ---------------------------------------------------------------------------
# Pod
# ---------------------------------------------------------------------------


class PodItem(BaseModel):
    """Pod 목록 항목."""

    namespace: str = Field(..., description=_D_NAMESPACE)
    pod: str = Field(..., description=_D_POD)
    cluster: str = Field(..., description=_D_CLUSTER)
    node: Optional[str] = Field(None, description=_D_NODE)
    phase: Optional[str] = Field(None, description=_D_PHASE)
    pod_ip: Optional[str] = Field(None, description=_D_POD_IP)
    host_ip: Optional[str] = Field(None, description=_D_HOST_IP)
    created_at: Optional[str] = Field(None, description=_D_CREATED_AT)
    workload_type: Optional[str] = Field(None, description=_D_WORKLOAD_TYPE)
    workload_name: Optional[str] = Field(None, description=_D_WORKLOAD_NAME)
    service_name: Optional[str] = Field(None, description=_D_SERVICE_NAME)
    container_count: Optional[int] = Field(None, description=_D_CONTAINER_COUNT)
    restart_count: Optional[int] = Field(None, description=_D_RESTART_COUNT)
    cpu_usage: Optional[float] = Field(None, description=_D_CPU_USAGE)
    memory_usage_bytes: Optional[float] = Field(None, description=_D_MEM_USAGE)


class PodListResponse(BaseModel):
    """Pod 목록 응답."""

    status: str = Field(..., description=DESC_STATUS)
    pods: list[PodItem] = Field([], description="Pod 목록")
    total: int = Field(0, description=DESC_TOTAL)
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class PodSummaryData(BaseModel):
    """Pod 상태별 개수 집계."""

    total_count: int = Field(0, description="전체 Pod 개수")
    running_count: int = Field(0, description="Running 상태 Pod 개수")
    pending_count: int = Field(0, description="Pending 상태 Pod 개수")
    succeeded_count: int = Field(0, description="Succeeded(정상 완료) 상태 Pod 개수")
    failed_count: int = Field(0, description="Failed 상태 Pod 개수")
    unknown_count: int = Field(0, description="Unknown(상태 불명) Pod 개수")
    namespace_distribution: dict[str, int] = Field(
        {}, description="네임스페이스별 Pod 개수 (키: 네임스페이스 이름, 값: Pod 수)"
    )


class PodSummaryResponse(BaseModel):
    """Pod 집계 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: PodSummaryData = Field(..., description="Pod 집계 결과")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class PodDetailData(BaseModel):
    """Pod 상세."""

    namespace: str = Field(..., description=_D_NAMESPACE)
    pod: str = Field(..., description=_D_POD)
    cluster: str = Field(..., description=_D_CLUSTER)
    uid: Optional[str] = Field(None, description="Pod 고유 ID(UID)")
    node: Optional[str] = Field(None, description=_D_NODE)
    phase: Optional[str] = Field(None, description=_D_PHASE)
    pod_ip: Optional[str] = Field(None, description=_D_POD_IP)
    host_ip: Optional[str] = Field(None, description=_D_HOST_IP)
    created_at: Optional[str] = Field(None, description=_D_CREATED_AT)
    workload_type: Optional[str] = Field(None, description=_D_WORKLOAD_TYPE)
    workload_name: Optional[str] = Field(None, description=_D_WORKLOAD_NAME)
    service_name: Optional[str] = Field(None, description=_D_SERVICE_NAME)
    container_count: Optional[int] = Field(None, description=_D_CONTAINER_COUNT)
    restart_count: Optional[int] = Field(None, description=_D_RESTART_COUNT)
    cpu_usage: Optional[float] = Field(None, description=_D_CPU_USAGE)
    memory_usage_bytes: Optional[float] = Field(None, description=_D_MEM_USAGE)
    cpu_requests: Optional[float] = Field(None, description=_D_CPU_REQ)
    cpu_limits: Optional[float] = Field(None, description=_D_CPU_LIM)
    memory_requests_bytes: Optional[float] = Field(None, description=_D_MEM_REQ)
    memory_limits_bytes: Optional[float] = Field(None, description=_D_MEM_LIM)


class PodDetailResponse(BaseModel):
    """Pod 상세 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: Optional[PodDetailData] = Field(None, description="Pod 상세. 대상이 없으면 null")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class PodPowerData(BaseModel):
    """Pod 추정 전력."""

    watts: Optional[float] = Field(
        None, description="Pod 추정 전력(W). 소속 컨테이너 추정 전력 합계, 최근 5분 평균"
    )
    source: Optional[str] = Field(None, description=_D_SOURCE)


class PodPowerResponse(BaseModel):
    """Pod 전력 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: Optional[PodPowerData] = Field(
        None, description="Pod 추정 전력. 전력값이 없으면 null"
    )
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


# ---------------------------------------------------------------------------
# Container
# ---------------------------------------------------------------------------


class ContainerItem(BaseModel):
    """컨테이너 항목."""

    namespace: str = Field(..., description=_D_NAMESPACE)
    pod: str = Field(..., description="컨테이너가 속한 Pod 이름")
    container: str = Field(..., description="컨테이너 이름")
    cluster: str = Field(..., description=_D_CLUSTER)
    container_id: Optional[str] = Field(
        None, description="컨테이너 ID. 런타임 접두어 포함 (예: containerd://3f2a9c)"
    )
    image: Optional[str] = Field(None, description="컨테이너 이미지")
    status: Optional[str] = Field(
        None, description="컨테이너 상태 (running | waiting | terminated). 판별 불가 시 null"
    )
    restart_count: Optional[int] = Field(None, description="재시작 횟수. 미수집 시 null")
    cpu_usage: Optional[float] = Field(None, description=_D_CPU_USAGE)
    memory_usage_bytes: Optional[float] = Field(None, description=_D_MEM_USAGE)
    cpu_requests: Optional[float] = Field(None, description="CPU 요청량(코어). 미설정 시 null")
    cpu_limits: Optional[float] = Field(None, description="CPU 상한값(코어). 미설정 시 null")
    memory_requests_bytes: Optional[float] = Field(
        None, description="메모리 요청량(bytes). 미설정 시 null"
    )
    memory_limits_bytes: Optional[float] = Field(
        None, description="메모리 상한값(bytes). 미설정 시 null"
    )


class ContainerListResponse(BaseModel):
    """컨테이너 목록 응답."""

    status: str = Field(..., description=DESC_STATUS)
    containers: list[ContainerItem] = Field([], description="컨테이너 목록")
    total: int = Field(0, description=DESC_TOTAL)
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class ContainerMetricsData(BaseModel):
    """컨테이너 자원 사용량."""

    cpu_usage: Optional[float] = Field(None, description=_D_CPU_USAGE)
    memory_usage_bytes: Optional[float] = Field(None, description=_D_MEM_USAGE)


class ContainerMetricsResponse(BaseModel):
    """컨테이너 메트릭 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: ContainerMetricsData = Field(..., description="컨테이너 자원 사용량")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class ContainerDetailResponse(BaseModel):
    """컨테이너 상세 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: Optional[ContainerItem] = Field(None, description="컨테이너 상세. 대상이 없으면 null")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


# ---------------------------------------------------------------------------
# Pod Accelerator
# ---------------------------------------------------------------------------


class PodAcceleratorItem(BaseModel):
    """Pod에 배정된 가속기 항목."""

    acc_id: Optional[str] = Field(None, description="가속기 ID (GPU 번호 또는 UUID)")
    vendor: Optional[str] = Field(None, description="가속기 벤더 (예: nvidia)")
    model_name: Optional[str] = Field(None, description="가속기 모델명 (예: NVIDIA L40S)")


class PodAcceleratorResponse(BaseModel):
    """Pod 가속기 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: list[PodAcceleratorItem] = Field([], description="Pod에 배정된 가속기 목록")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


# ---------------------------------------------------------------------------
# Namespace
# ---------------------------------------------------------------------------


class NamespaceItem(BaseModel):
    """네임스페이스 목록 항목."""

    namespace: str = Field(..., description=_D_NAMESPACE)
    cluster: str = Field(..., description=_D_CLUSTER)
    pod_count: Optional[int] = Field(None, description="Pod 개수. Pod가 없으면 null")
    created_at: Optional[str] = Field(
        None, description="네임스페이스 생성 시각 (ISO 8601, UTC). 미수집 시 null"
    )


class NamespaceListResponse(BaseModel):
    """네임스페이스 목록 응답."""

    status: str = Field(..., description=DESC_STATUS)
    namespaces: list[NamespaceItem] = Field([], description="네임스페이스 목록. 이름순")
    total: int = Field(0, description=DESC_TOTAL)
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class NamespaceDetailData(BaseModel):
    """네임스페이스 자원 사용 상세."""

    namespace: str = Field(..., description=_D_NAMESPACE)
    cluster: str = Field(..., description=_D_CLUSTER)
    pod_count: int = Field(0, description="Pod 개수")
    container_count: int = Field(0, description="컨테이너 개수")
    cpu_usage: Optional[float] = Field(
        None, description="CPU 사용량 합계(코어). 최근 5분 평균, 미수집 시 null"
    )
    memory_usage_bytes: Optional[float] = Field(
        None, description="메모리 사용량 합계(bytes). 실사용(working set) 기준, 미수집 시 null"
    )
    cpu_requests: Optional[float] = Field(None, description=_D_CPU_REQ)
    memory_requests_bytes: Optional[float] = Field(None, description=_D_MEM_REQ)


class NamespaceDetailResponse(BaseModel):
    """네임스페이스 상세 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: NamespaceDetailData = Field(..., description="네임스페이스 자원 사용 상세")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


# ---------------------------------------------------------------------------
# Service (전역 엔드포인트용)
# ---------------------------------------------------------------------------


class ServiceItem(BaseModel):
    """서비스 목록 항목. 같은 서비스 이름의 Pod를 하나로 묶은 단위."""

    service_name: str = Field(
        ..., description="서비스 이름. 서비스 이름을 추정할 수 없는 Pod는 Pod 이름"
    )
    namespace: str = Field(..., description=_D_NAMESPACE)
    cluster: str = Field(..., description=_D_CLUSTER)
    pod_count: int = Field(0, description="소속 Pod 개수")
    running_pod_count: int = Field(0, description="소속 Pod 중 Running 상태 개수")
    cpu_usage: Optional[float] = Field(
        None, description="소속 Pod CPU 사용량 합계(코어). 미수집 시 null"
    )
    memory_usage_bytes: Optional[float] = Field(
        None, description="소속 Pod 메모리 사용량 합계(bytes). 미수집 시 null"
    )


class ServiceSummaryData(BaseModel):
    """서비스 개수 집계."""

    total_services: int = Field(0, description="전체 서비스 개수")
    cluster_distribution: dict[str, int] = Field(
        {}, description="클러스터별 서비스 개수 (키: 클러스터 이름, 값: 서비스 수)"
    )


class ServiceListResponse(BaseModel):
    """서비스 목록 응답."""

    status: str = Field(..., description=DESC_STATUS)
    services: list[ServiceItem] = Field([], description="서비스 목록")
    total: int = Field(0, description=DESC_TOTAL)
    summary: ServiceSummaryData = Field(
        default_factory=ServiceSummaryData,
        description="서비스 집계. 페이지와 서비스 이름 검색 적용 전 기준",
    )
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class ServiceDetailData(BaseModel):
    """서비스 자원 사용 상세."""

    service_name: str = Field(..., description="서비스 이름")
    namespace: str = Field(..., description=_D_NAMESPACE)
    cluster: str = Field(..., description=_D_CLUSTER)
    pod_count: int = Field(0, description="소속 Pod 개수")
    running_pod_count: int = Field(0, description="소속 Pod 중 Running 상태 개수")
    cpu_usage: Optional[float] = Field(
        None, description="소속 Pod CPU 사용량 합계(코어). 미수집 시 null"
    )
    memory_usage_bytes: Optional[float] = Field(
        None, description="소속 Pod 메모리 사용량 합계(bytes). 미수집 시 null"
    )
    cpu_requests: Optional[float] = Field(
        None, description="소속 Pod CPU 요청량 합계(코어). 값 미제공 시 null"
    )
    memory_requests_bytes: Optional[float] = Field(
        None, description="소속 Pod 메모리 요청량 합계(bytes). 값 미제공 시 null"
    )


class ServiceDetailResponse(BaseModel):
    """서비스 상세 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: Optional[ServiceDetailData] = Field(
        None, description="서비스 상세. 소속 Pod가 없으면 null"
    )
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class ServicePowerData(BaseModel):
    """서비스 추정 전력."""

    watts: Optional[float] = Field(
        None, description="서비스 추정 전력(W). 소속 Pod 추정 전력 합계"
    )
    source: Optional[str] = Field(None, description=_D_SOURCE)


class ServicePowerResponse(BaseModel):
    """서비스 전력 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: Optional[ServicePowerData] = Field(
        None, description="서비스 추정 전력. 어느 Pod도 전력값이 없으면 null"
    )
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)
