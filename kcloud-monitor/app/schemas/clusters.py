"""
클러스터 API v2 Pydantic 스키마.
"""
from typing import Optional

from pydantic import BaseModel, Field

from app.schemas._common import (
    DESC_HAS_NEXT,
    DESC_LIMIT,
    DESC_OBSERVED_AT,
    DESC_OFFSET,
    DESC_STATUS,
    DESC_TOTAL,
    DESC_WARNINGS,
    _now,
)


_DESC_TYPE = (
    "클러스터 구분 (management | service). management: 물리 서버와 OpenStack 을 관리하는 클러스터, "
    "service: VM 위에서 동작하는 가속기 클러스터"
)
_DESC_PARENT = "부모 관리 클러스터 이름. 관리 클러스터 응답에는 없음"
_DESC_PROJECT = "소속 OpenStack 프로젝트 이름(과금 단위). 값이 없으면 응답에 없음"
_DESC_HAS_OPENSTACK = "OpenStack 연동 여부. 관리 클러스터만 true, 서비스 클러스터 응답에는 없음"
_DESC_SERVICE_CLUSTERS = "하위 서비스 클러스터 이름 목록. 서비스 클러스터 응답에는 없음"
_DESC_NODE_COUNT = "노드 수. Kubernetes 클러스터는 등록 노드 수, 가속기 VM 은 가속기를 보고하는 호스트 수"
_DESC_ACC_COUNT = "가속기(카드) 수. 관리 클러스터는 하위 서비스 클러스터 전체 합계"
_DESC_TOTAL_POWER = "가속기 전력 합계(W). 관리 클러스터는 하위 서비스 클러스터 전체 합계, 미수집 시 null"
_DESC_CLUSTER_STATUS = (
    "클러스터 상태 (healthy | warning | critical). healthy: 노드 전부 정상, "
    "warning: 일부 비정상 또는 데이터 없음, critical: 전부 비정상"
)
_DESC_CARD_ID = "가속기 ID. NVIDIA, Rebellions 는 카드 UUID, Furiosa 는 장치 이름(예: npu0)"


class ClusterListItem(BaseModel):
    """클러스터 목록의 클러스터 하나."""

    name: str = Field(..., description="클러스터 이름")
    type: str = Field(..., description=_DESC_TYPE)
    # description은 목록에서 제외. 상세 조회에서만 제공
    parent_cluster: Optional[str] = Field(None, description=_DESC_PARENT)
    openstack_project: Optional[str] = Field(None, description=_DESC_PROJECT)
    has_openstack: Optional[bool] = Field(None, description=_DESC_HAS_OPENSTACK)
    service_clusters: Optional[list[str]] = Field(None, description=_DESC_SERVICE_CLUSTERS)
    node_count: int = Field(0, description=_DESC_NODE_COUNT)
    accelerator_count: int = Field(0, description=_DESC_ACC_COUNT)
    total_power_watts: Optional[float] = Field(None, description=_DESC_TOTAL_POWER)
    status: str = Field("healthy", description=_DESC_CLUSTER_STATUS)


class ClusterPagination(BaseModel):
    """목록 페이지 정보."""

    total: int = Field(..., description=DESC_TOTAL)
    limit: int = Field(..., description=DESC_LIMIT)
    offset: int = Field(..., description=DESC_OFFSET)
    has_next: bool = Field(..., description=DESC_HAS_NEXT)


class ClusterListResponse(BaseModel):
    """클러스터 목록 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: list[ClusterListItem] = Field(..., description="클러스터 목록. 관리 클러스터 먼저, 그다음 이름순")
    pagination: Optional[ClusterPagination] = Field(
        None, description="페이지 정보. total(필터 적용 후 전체 개수), limit, offset, has_next"
    )
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class ClusterDetailData(BaseModel):
    """클러스터 상세."""

    name: str = Field(..., description="클러스터 이름")
    type: str = Field(..., description=_DESC_TYPE)
    description: Optional[str] = Field(
        None, description="클러스터 설명 (예: 관리 클러스터, GPU VM, NPU 쿠버네티스 클러스터)"
    )
    parent_cluster: Optional[str] = Field(None, description=_DESC_PARENT)
    openstack_project: Optional[str] = Field(None, description=_DESC_PROJECT)
    has_openstack: Optional[bool] = Field(None, description=_DESC_HAS_OPENSTACK)
    service_clusters: Optional[list[str]] = Field(None, description=_DESC_SERVICE_CLUSTERS)
    node_count: int = Field(0, description=_DESC_NODE_COUNT)
    accelerator_count: int = Field(0, description=_DESC_ACC_COUNT)
    total_power_watts: Optional[float] = Field(None, description=_DESC_TOTAL_POWER)
    status: str = Field("healthy", description=_DESC_CLUSTER_STATUS)
    # ── 상세 전용 ──────────────────────────────────────────────
    runtime: Optional[str] = Field(
        None,
        description="실행 형태 (kubernetes | vm). kubernetes: Kubernetes 클러스터, vm: 단독 가속기 VM. "
        "관리 클러스터 응답에는 없음",
    )
    vendor: Optional[str] = Field(
        None, description="가속기 벤더 (nvidia | furiosa | rebellions). 가속기가 없으면 응답에 없음"
    )
    accelerator_type: Optional[str] = Field(
        None, description="가속기 종류 (GPU | NPU). 가속기가 없으면 응답에 없음"
    )
    nodes: list[str] = Field(default_factory=list, description="노드 이름 목록")
    avg_utilization: Optional[float] = Field(
        None, description="평균 가속기 사용률(%). 미수집 시 응답에 없음"
    )
    avg_temperature: Optional[float] = Field(
        None, description="평균 가속기 온도(°C). 미수집 시 응답에 없음"
    )


class ClusterDetailResponse(BaseModel):
    """클러스터 상세 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: ClusterDetailData = Field(..., description="클러스터 상세")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class NodeResources(BaseModel):
    """노드 수 요약."""

    total: int = Field(0, description="전체 노드 수")
    ready: int = Field(0, description="Ready 노드 수")
    not_ready: int = Field(0, description="NotReady 노드 수")


class CpuResources(BaseModel):
    """CPU 자원 요약."""

    total_cores: Optional[float] = Field(None, description="전체 CPU 코어 수(코어). 미수집 시 null")
    used_cores: Optional[float] = Field(None, description="사용 중 CPU 코어 수(코어). 최근 5분 평균, 미수집 시 null")
    utilization_percent: Optional[float] = Field(None, description="CPU 사용률(%). 판정 불가 시 null")


class MemoryResources(BaseModel):
    """메모리 자원 요약."""

    total_gb: Optional[float] = Field(None, description="전체 메모리(GB). 1GB = 10^9 bytes, 미수집 시 null")
    used_gb: Optional[float] = Field(None, description="사용 메모리(GB). 1GB = 10^9 bytes, 미수집 시 null")
    utilization_percent: Optional[float] = Field(None, description="메모리 사용률(%). 판정 불가 시 null")


class AcceleratorResources(BaseModel):
    """가속기 자원 요약."""

    total: int = Field(0, description="가속기(카드) 수")
    active: Optional[int] = Field(None, description="사용 중 카드 수. 미산출 시 null")
    idle: Optional[int] = Field(None, description="유휴 카드 수. 미산출 시 null")
    avg_utilization_percent: Optional[float] = Field(None, description="평균 사용률(%). 미수집 시 null")
    total_power_watts: Optional[float] = Field(None, description="전력 합계(W). 미수집 시 null")


class StorageResources(BaseModel):
    """스토리지 자원 요약."""

    total_tb: Optional[float] = Field(None, description="전체 용량(TB). 미수집 시 null")
    used_tb: Optional[float] = Field(None, description="사용 용량(TB). 미수집 시 null")
    utilization_percent: Optional[float] = Field(None, description="사용률(%). 미수집 시 null")


class ClusterResources(BaseModel):
    """클러스터 자원 종류별 요약. 데이터 없는 항목은 null."""

    nodes: Optional[NodeResources] = Field(None, description="노드 수 요약. 전체, Ready, NotReady 노드 수")
    cpu: Optional[CpuResources] = Field(None, description="CPU 요약. 전체 코어 수, 사용 코어 수, 사용률(%)")
    memory: Optional[MemoryResources] = Field(None, description="메모리 요약. 전체, 사용 메모리(GB), 사용률(%)")
    accelerators: Optional[AcceleratorResources] = Field(
        None, description="가속기 요약. 카드 수, 평균 사용률(%), 전력 합계(W). 가속기가 없으면 null"
    )
    storage: Optional[StorageResources] = Field(
        None, description="스토리지 요약. 전체, 사용 용량(TB), 사용률(%). 미수집 시 null"
    )


class PowerBreakdown(BaseModel):
    """전력 구성 내역. 메모리 전력은 기타 전력에 포함."""

    cpu_watts: Optional[float] = Field(None, description="CPU 전력(W). Kepler 기준, 미수집 시 null")
    gpu_watts: Optional[float] = Field(None, description="가속기 전력 합계(W). 미수집 시 null")
    other_watts: Optional[float] = Field(
        None, description="기타 전력(W). 서버 총전력 - CPU - 가속기, 0 미만이면 0. 서버 총전력 미수집 시 null"
    )


class ClusterPower(BaseModel):
    """클러스터 전력 요약."""

    total_watts: Optional[float] = Field(None, description="서버 총전력 합계(W). IPMI 기준, 미수집 시 null")
    breakdown: Optional[PowerBreakdown] = Field(
        None, description="전력 구성 내역. CPU, 가속기, 기타 전력(W). 모두 미수집 시 null"
    )


class ClusterResourceData(BaseModel):
    """클러스터 자원 현황."""

    cluster: str = Field(..., description="클러스터 이름")
    type: str = Field(..., description=_DESC_TYPE)
    resources: ClusterResources = Field(..., description="자원 종류별 요약. 노드, CPU, 메모리, 가속기, 스토리지")
    power: ClusterPower = Field(..., description="전력 요약. 서버 총전력(W)과 구성 내역")


class ClusterResourceResponse(BaseModel):
    """클러스터 자원 현황 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: ClusterResourceData = Field(..., description="클러스터 자원 현황")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class TopologyAccelerator(BaseModel):
    """노드에 장착된 가속기 한 장."""

    id: str = Field(..., description=_DESC_CARD_ID)
    model: Optional[str] = Field(None, description="모델명 (예: NVIDIA L40S, Furiosa rngd). 확인 불가 시 응답에 없음")
    utilization_percent: Optional[float] = Field(None, description="사용률(%). 미수집 시 응답에 없음")
    power_watts: Optional[float] = Field(None, description="전력(W). 미수집 시 응답에 없음")


class TopologyNode(BaseModel):
    """클러스터 구성도의 노드 하나."""

    name: str = Field(..., description="가속기를 보고하는 호스트 이름")
    node_type: str = Field(..., description="노드 종류 (physical | virtual). physical: 물리 서버, virtual: VM")
    cpu_cores: Optional[int] = Field(None, description="CPU 코어 수. 물리 노드만 값 있음, 그 외 응답에 없음")
    memory_gb: Optional[float] = Field(None, description="메모리(GB). 미수집 시 응답에 없음")
    accelerators: list[TopologyAccelerator] = Field(default_factory=list, description="장착된 가속기 목록")


class ClusterTopologyData(BaseModel):
    """클러스터 토폴로지."""

    cluster: str = Field(..., description="클러스터 이름")
    nodes: list[TopologyNode] = Field(..., description="호스트 목록. 이름순")


class ClusterTopologyResponse(BaseModel):
    """클러스터 토폴로지 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: ClusterTopologyData = Field(..., description="클러스터 토폴로지")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class ClusterPowerCard(BaseModel):
    """가속기 카드 한 장의 전력."""

    id: str = Field(..., description=_DESC_CARD_ID)
    hostname: str = Field(..., description="카드가 장착된 호스트 이름")
    watts: Optional[float] = Field(None, description="카드 전력(W). 미수집 시 null")


class ClusterPowerData(BaseModel):
    """클러스터 가속기 전력 합계."""

    cluster: str = Field(..., description="클러스터 이름")
    total_power_watts: Optional[float] = Field(None, description="가속기 전력 합계(W). 미수집 시 null")
    accelerator_count: int = Field(..., description="전력을 보고한 가속기(카드) 수")
    cards: list[ClusterPowerCard] = Field(
        default_factory=list,
        description="카드별 전력 목록. 관리 클러스터는 하위 서비스 클러스터의 카드 전체",
    )


class ClusterPowerResponse(BaseModel):
    """클러스터 가속기 전력 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: ClusterPowerData = Field(..., description="클러스터 가속기 전력 합계")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)
