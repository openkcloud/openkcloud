"""
Nodes API v2 Pydantic 스키마.

공통 응답 정책(docs/API_GUIDE.md §공통-응답-정책):
  - status: "success" | "partial" | "error"
  - observed_at: ISO 8601 수집 시각
  - warnings[]: NO_DATA, NO_POWER_DATA, IPMI_NOT_AVAILABLE 등 (정상 시 빈 목록)
"""
from typing import Optional

from pydantic import BaseModel, Field

from app.schemas._common import DESC_OBSERVED_AT, DESC_STATUS, DESC_TOTAL, DESC_WARNINGS, _now


_DESC_NODE_TYPE = "노드 종류 (physical | virtual). physical: 물리 서버, virtual: VM"
_DESC_POWER_SOURCE = (
    "전력 산출 경로 (ipmi-dcmi | ipmi-psu-input). ipmi-dcmi: BMC 의 DCMI 측정값, "
    "ipmi-psu-input: 전원 장치 입력 전력 합"
)


class NodeSummaryItem(BaseModel):
    """노드 목록의 노드 한 대 요약."""

    nodename: str = Field(
        ...,
        description="노드 이름. 관리 클러스터는 Kubernetes 노드 이름, 가속기 클러스터는 가속기를 보고하는 "
        "호스트 이름. 노드 하위 경로의 식별자로 사용",
    )
    internal_ip: Optional[str] = Field(None, description="노드 내부 IP. 관리 클러스터만 값 있음, 그 외 null")
    role: Optional[str] = Field(
        None, description="노드 역할 (worker | control-plane). 관리 클러스터만 값 있음, 그 외 null"
    )
    cluster: str = Field(..., description="소속 클러스터 이름")
    vendor: Optional[str] = Field(
        None, description="가속기 벤더 (nvidia | furiosa | rebellions). 가속기 클러스터만 값 있음, 그 외 null"
    )
    up: bool = Field(
        ...,
        description="동작 여부. 관리 클러스터는 Kubernetes Ready 상태, 가속기 클러스터는 가속기 메트릭 보고 여부",
    )
    os: Optional[str] = Field(None, description="OS 이미지 이름. 관리 클러스터만 값 있음, 그 외 null")
    kubelet_version: Optional[str] = Field(
        None, description="kubelet 버전. 관리 클러스터만 값 있음, 그 외 null"
    )
    node_type: Optional[str] = Field(None, description=_DESC_NODE_TYPE)
    accelerator_count: Optional[int] = Field(
        None, description="장착된 가속기(카드) 수. 가속기 클러스터만 값 있음, 그 외 null"
    )
    power_watts: Optional[float] = Field(
        None,
        description="전력(W). 관리 클러스터는 서버 전원 장치(IPMI) 측정값, 가속기 클러스터는 가속기 전력 합계. "
        "미수집 시 null",
    )
    utilization_percent: Optional[float] = Field(
        None, description="장착된 가속기의 평균 사용률(%). 가속기 클러스터만 값 있음, 그 외 null"
    )


class NodesSummaryData(BaseModel):
    """클러스터 전체 노드 집계."""

    ready_count: int = Field(..., description="동작 중인 노드 수 (up 이 true 인 노드)")
    total_count: int = Field(..., description="전체 노드 수. 필터와 페이지 적용 전 기준")
    memory_total_bytes: Optional[float] = Field(None, description="클러스터 전체 메모리 합계(bytes). 미수집 시 null")
    memory_used_bytes: Optional[float] = Field(None, description="클러스터 전체 사용 메모리(bytes). 미수집 시 null")
    memory_usage_percent: Optional[float] = Field(None, description="클러스터 전체 메모리 사용률(%). 미수집 시 null")


class NodeListResponse(BaseModel):
    """노드 목록 응답."""

    status: str = Field(..., description=DESC_STATUS)
    nodes: list[NodeSummaryItem] = Field(..., description="노드 목록. limit, offset 으로 자른 페이지")
    total: int = Field(..., description=DESC_TOTAL)
    summary: Optional[NodesSummaryData] = Field(
        None, description="클러스터 전체 노드 집계. 동작 중 노드 수, 전체 노드 수, 메모리 합계와 사용률"
    )
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class NodeOsInfo(BaseModel):
    """노드 운영체제 정보."""

    sysname: Optional[str] = Field(None, description="커널 종류 (예: Linux)")
    release: Optional[str] = Field(None, description="커널 릴리스")
    version: Optional[str] = Field(None, description="커널 빌드 버전")
    machine: Optional[str] = Field(None, description="CPU 아키텍처 (예: x86_64)")
    nodename: Optional[str] = Field(None, description="호스트명")


class NodeDetailData(BaseModel):
    """노드 상세."""

    instance: str = Field(
        ..., description="노드 식별자. 메트릭 수집 대상 주소(예: 10.0.0.1:9100), 변환할 수 없으면 요청한 노드 이름"
    )
    cluster: str = Field(..., description="소속 클러스터 이름")
    up: bool = Field(..., description="메트릭 보고 여부")
    os: Optional[NodeOsInfo] = Field(
        None, description="운영체제 정보. 커널 종류, 릴리스, 버전, 아키텍처, 호스트명. 미수집 시 null"
    )
    boot_time: Optional[str] = Field(None, description="마지막 부팅 시각 (ISO 8601, UTC). 미수집 시 null")
    cpu_cores: Optional[int] = Field(None, description="CPU 코어 수. 미수집 시 null")
    memory_total_bytes: Optional[float] = Field(None, description="전체 메모리(bytes). 미수집 시 null")
    node_type: Optional[str] = Field(None, description=_DESC_NODE_TYPE)
    ready: Optional[bool] = Field(
        None, description="Kubernetes Ready 상태. 관리 클러스터만 값 있음, 그 외 null"
    )
    accelerator_count: int = Field(0, description="장착된 가속기(카드) 수")


class NodeDetailResponse(BaseModel):
    """노드 상세 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: Optional[NodeDetailData] = Field(None, description="노드 상세. 대상이 없으면 null")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class NodeMetricsData(BaseModel):
    """노드 주요 사용량."""

    cpu_usage_percent: Optional[float] = Field(None, description="CPU 사용률(%). 최근 5분 평균, 미수집 시 null")
    memory_usage_percent: Optional[float] = Field(None, description="메모리 사용률(%). 미수집 시 null")
    memory_total_bytes: Optional[float] = Field(None, description="전체 메모리(bytes). 미수집 시 null")
    memory_used_bytes: Optional[float] = Field(None, description="사용 메모리(bytes). 미수집 시 null")
    disk_usage_percent: Optional[float] = Field(
        None, description="실디스크 사용률(%). 용량 가중 합계, tmpfs 같은 가상 파일시스템 제외. 미수집 시 null"
    )
    network_receive_bytes_per_sec: Optional[float] = Field(
        None, description="네트워크 수신 처리량(bytes/s). 최근 5분 평균, 미수집 시 null"
    )
    network_transmit_bytes_per_sec: Optional[float] = Field(
        None, description="네트워크 송신 처리량(bytes/s). 최근 5분 평균, 미수집 시 null"
    )


class NodeMetricsResponse(BaseModel):
    """노드 종합 메트릭 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: NodeMetricsData = Field(..., description="노드 주요 사용량")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class CpuCoreUsage(BaseModel):
    """코어별 CPU 사용률."""

    cpu: str = Field(..., description="코어 번호")
    usage_percent: float = Field(..., description="코어 사용률(%). 최근 5분 평균")


class CpuModeBreakdown(BaseModel):
    """처리 종류별 CPU 시간 비율."""

    mode: str = Field(..., description="처리 종류 (예: user, system, iowait, idle)")
    rate: float = Field(
        ..., description="초당 사용한 CPU 시간 비율(단위 없음). 전체 코어 합산, 최근 5분 평균"
    )


class NodeCpuData(BaseModel):
    """노드 CPU 사용 상세."""

    usage_percent: Optional[float] = Field(None, description="전체 CPU 사용률(%). 최근 5분 평균, 미수집 시 null")
    load1: Optional[float] = Field(None, description="1분 평균 부하(단위 없음). 미수집 시 null")
    load5: Optional[float] = Field(None, description="5분 평균 부하(단위 없음). 미수집 시 null")
    load15: Optional[float] = Field(None, description="15분 평균 부하(단위 없음). 미수집 시 null")
    per_core: list[CpuCoreUsage] = Field([], description="코어별 사용률 목록")
    per_mode: list[CpuModeBreakdown] = Field([], description="처리 종류별 CPU 시간 비율 목록")


class NodeCpuResponse(BaseModel):
    """노드 CPU 상세 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: NodeCpuData = Field(..., description="노드 CPU 사용 상세")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class NodeMemoryData(BaseModel):
    """노드 메모리 사용 상세."""

    total_bytes: Optional[float] = Field(None, description="전체 메모리(bytes). 미수집 시 null")
    available_bytes: Optional[float] = Field(None, description="가용 메모리(bytes). 미수집 시 null")
    used_bytes: Optional[float] = Field(None, description="사용 메모리(bytes). 전체 - 가용, 판정 불가 시 null")
    used_percent: Optional[float] = Field(None, description="메모리 사용률(%). 판정 불가 시 null")
    cached_bytes: Optional[float] = Field(None, description="캐시 메모리(bytes). 미수집 시 null")
    buffers_bytes: Optional[float] = Field(None, description="버퍼 메모리(bytes). 미수집 시 null")
    swap_total_bytes: Optional[float] = Field(None, description="전체 스왑(bytes). 미수집 시 null")
    swap_free_bytes: Optional[float] = Field(None, description="가용 스왑(bytes). 미수집 시 null")
    swap_used_bytes: Optional[float] = Field(None, description="사용 스왑(bytes). 판정 불가 시 null")


class NodeMemoryResponse(BaseModel):
    """노드 메모리 상세 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: NodeMemoryData = Field(..., description="노드 메모리 사용 상세")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class FilesystemUsage(BaseModel):
    """마운트 위치별 파일시스템 사용량."""

    mountpoint: str = Field(..., description="마운트 위치 (예: /, /var)")
    fstype: Optional[str] = Field(None, description="파일시스템 종류 (예: ext4, xfs, tmpfs)")
    size_bytes: Optional[float] = Field(None, description="전체 용량(bytes). 미수집 시 null")
    avail_bytes: Optional[float] = Field(None, description="남은 용량(bytes). 미수집 시 null")
    used_percent: Optional[float] = Field(None, description="사용률(%). 판정 불가 시 null")


class DiskIoRate(BaseModel):
    """디스크 장치별 읽기, 쓰기 처리량."""

    device: str = Field(..., description="디스크 장치 이름 (예: sda, nvme0n1)")
    read_bytes_per_sec: Optional[float] = Field(
        None, description="읽기 처리량(bytes/s). 최근 5분 평균, 미수집 시 null"
    )
    write_bytes_per_sec: Optional[float] = Field(
        None, description="쓰기 처리량(bytes/s). 최근 5분 평균, 미수집 시 null"
    )


class NodeStorageData(BaseModel):
    """노드 로컬 디스크 사용 상세."""

    filesystems: list[FilesystemUsage] = Field([], description="마운트 위치별 파일시스템 사용량 목록")
    disks: list[DiskIoRate] = Field([], description="디스크 장치별 처리량 목록")


class NodeStorageResponse(BaseModel):
    """노드 로컬 디스크 상세 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: NodeStorageData = Field(..., description="노드 로컬 디스크 사용 상세")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class NetworkInterfaceStats(BaseModel):
    """네트워크 장치별 처리량과 에러율."""

    device: str = Field(..., description="네트워크 장치 이름 (예: eth0)")
    receive_bytes_per_sec: Optional[float] = Field(
        None, description="수신 처리량(bytes/s). 최근 5분 평균, 미수집 시 null"
    )
    transmit_bytes_per_sec: Optional[float] = Field(
        None, description="송신 처리량(bytes/s). 최근 5분 평균, 미수집 시 null"
    )
    receive_errors_per_sec: Optional[float] = Field(
        None, description="수신 에러율(회/s). 최근 5분 평균, 미수집 시 null"
    )
    transmit_errors_per_sec: Optional[float] = Field(
        None, description="송신 에러율(회/s). 최근 5분 평균, 미수집 시 null"
    )


class NodeNetworkData(BaseModel):
    """노드 네트워크 사용 상세."""

    interfaces: list[NetworkInterfaceStats] = Field([], description="네트워크 장치별 처리량과 에러율 목록")


class NodeNetworkResponse(BaseModel):
    """노드 네트워크 상세 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: NodeNetworkData = Field(..., description="노드 네트워크 사용 상세")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class NodePowerData(BaseModel):
    """노드 서버 총전력 현재값."""

    watts: Optional[float] = Field(None, description="서버 총전력(W). IPMI 기준 벽면 전력, 미수집 시 null")
    source: Optional[str] = Field(None, description=_DESC_POWER_SOURCE)


class NodePowerResponse(BaseModel):
    """노드 서버 전력 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: Optional[NodePowerData] = Field(None, description="서버 총전력 현재값. 전력 데이터가 없으면 null")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class NodePowerTimeseriesPoint(BaseModel):
    """전력 시계열의 점 하나."""

    timestamp: str = Field(..., description="측정 시각 (ISO 8601, UTC)")
    watts: float = Field(..., description="서버 총전력(W)")


class NodePowerTimeseriesResponse(BaseModel):
    """노드 서버 전력 시계열 응답."""

    status: str = Field(..., description=DESC_STATUS)
    series: list[NodePowerTimeseriesPoint] = Field([], description="시각별 서버 총전력 목록")
    source: Optional[str] = Field(None, description=_DESC_POWER_SOURCE + ". 전력 데이터가 없으면 null")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class HardwareSensor(BaseModel):
    """IPMI 센서 값 하나."""

    name: str = Field(..., description="센서 이름")
    value: float = Field(..., description="센서 값. 단위는 unit 참고")
    unit: Optional[str] = Field(None, description="센서 값 단위. 원본 메트릭에 단위 정보가 없으면 null")


class HardwareSensorsResponse(BaseModel):
    """하드웨어 센서 전체 응답."""

    status: str = Field(..., description=DESC_STATUS)
    sensors: list[HardwareSensor] = Field([], description="센서 목록. 판독 불가 센서는 제외")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class HardwarePowerResponse(BaseModel):
    """서버 본체 전력 응답."""

    status: str = Field(..., description=DESC_STATUS)
    watts: Optional[float] = Field(None, description="서버 총전력(W). BMC 의 DCMI 측정값, 미수집 시 null")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class HardwareTemperatureSensor(BaseModel):
    """IPMI 온도 센서 값 하나."""

    name: str = Field(..., description="온도 센서 이름")
    celsius: float = Field(..., description="온도(°C). 판독 불가 시 0")


class HardwareTemperatureResponse(BaseModel):
    """하드웨어 온도 응답."""

    status: str = Field(..., description=DESC_STATUS)
    sensors: list[HardwareTemperatureSensor] = Field([], description="위치별 온도 센서 목록")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)
