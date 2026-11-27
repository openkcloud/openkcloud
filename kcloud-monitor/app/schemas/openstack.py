"""
OpenStack API v2 Pydantic 스키마.

공통 응답 정책: status(success|partial|error) / observed_at / warnings.
경고 코드: NOT_CONFIGURED(크리덴셜 미설정), UPSTREAM_ERROR(OpenStack 호출 실패), NO_DATA.
"""
from typing import Optional

from pydantic import BaseModel, Field

from app.schemas._common import DESC_OBSERVED_AT, DESC_STATUS, DESC_WARNINGS, _now


_D_HV_STATE = "물리 서버 연결 상태 (up | down)"
_D_HV_STATUS = "물리 서버 운영 상태 (enabled | disabled). disabled 면 새 VM 배치 안 함"
_D_VM_ID = "VM ID(UUID)"
_D_VM_HOST = "VM이 배치된 물리 서버 이름. 미배치 시 null"


class HypervisorItem(BaseModel):
    """물리 서버(하이퍼바이저) 목록 항목."""

    hostname: str = Field(..., description="물리 서버 호스트명 (예: compute5)")
    state: Optional[str] = Field(None, description=_D_HV_STATE)
    status: Optional[str] = Field(None, description=_D_HV_STATUS)
    vm_count: int = Field(0, description="이 서버에 배치된 VM 수")


class VMAccelerator(BaseModel):
    """VM에 연결된 가속기."""

    alias: str = Field(
        ...,
        description="가속기 종류 (예: L40S, furiosa-rngd, rebellions). VM 사양(flavor)의 PCI 장치 설정 또는 가속기 장치 프로필 이름으로 판별",
    )
    count: int = Field(..., description="VM에 연결된 카드 수")


class VMItem(BaseModel):
    """OpenStack VM 항목."""

    vm_id: str = Field(..., description=_D_VM_ID)
    name: str = Field(..., description="VM 이름. 가속기 메트릭의 호스트 이름과 같은 값")
    host: Optional[str] = Field(None, description=_D_VM_HOST)
    status: Optional[str] = Field(None, description="VM 상태 (예: ACTIVE, SHUTOFF, ERROR)")
    flavor: Optional[str] = Field(None, description="VM 사양(flavor) 이름")
    vcpus: Optional[int] = Field(None, description="flavor에 정의된 vCPU 수. flavor 미확인 시 null")
    ram_mb: Optional[int] = Field(None, description="flavor에 정의된 메모리(MB). flavor 미확인 시 null")
    project_id: Optional[str] = Field(None, description="소속 프로젝트 ID")
    accelerator: Optional[VMAccelerator] = Field(
        None, description="VM에 직접 연결(passthrough)된 가속기. 없으면 null"
    )


class VMSummaryData(BaseModel):
    """VM 개수 집계."""

    total: int = Field(0, description="전체 VM 수")
    by_status: dict[str, int] = Field(
        {}, description="VM 상태별 개수 (키: 상태(예: ACTIVE, SHUTOFF), 값: VM 수)"
    )
    accelerator_vm_count: int = Field(0, description="가속기가 연결된 VM 수")
    by_project: dict[str, int] = Field(
        {}, description="프로젝트별 VM 수 (키: 프로젝트 ID, 값: VM 수)"
    )


class HypervisorListResponse(BaseModel):
    """물리 서버(하이퍼바이저) 목록 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: list[HypervisorItem] = Field([], description="물리 서버 목록")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class VMListResponse(BaseModel):
    """VM 목록 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: list[VMItem] = Field([], description="VM 목록")
    summary: VMSummaryData = Field(default_factory=VMSummaryData,
                                   description="VM 집계. 검색과 페이지에 영향받지 않는 기준")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class VMDetailResponse(BaseModel):
    """VM 상세 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: Optional[VMItem] = Field(None, description="VM 상세. 대상이 없으면 null")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class ProjectItem(BaseModel):
    """OpenStack 프로젝트 항목."""

    project_id: str = Field(..., description="프로젝트 ID")
    name: str = Field(..., description="프로젝트 이름")
    vm_count: int = Field(0, description="소속 VM 수")
    accelerator_vm_count: int = Field(0, description="가속기가 연결된 소속 VM 수")
    total_vcpus: int = Field(0, description="소속 VM의 flavor 기준 vCPU 합계")
    total_ram_mb: int = Field(0, description="소속 VM의 flavor 기준 메모리 합계(MB)")


class ProjectListResponse(BaseModel):
    """프로젝트 목록 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: list[ProjectItem] = Field([], description="프로젝트 목록")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class ProjectDetailResponse(BaseModel):
    """프로젝트 상세 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: Optional[ProjectItem] = Field(None, description="프로젝트 상세. 대상이 없으면 null")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class HypervisorDetailData(BaseModel):
    """물리 서버 상세. 서버 자원 정보와 그 위에 올라간 VM 목록."""

    hostname: str = Field(..., description="물리 서버 호스트명")
    state: Optional[str] = Field(None, description=_D_HV_STATE)
    status: Optional[str] = Field(None, description=_D_HV_STATUS)
    vcpus: Optional[int] = Field(None, description="전체 vCPU 수")
    vcpus_used: Optional[int] = Field(None, description="VM에 배정된 vCPU 수")
    memory_mb: Optional[int] = Field(None, description="전체 메모리(MB)")
    memory_mb_used: Optional[int] = Field(None, description="VM에 배정된 메모리(MB)")
    local_gb: Optional[int] = Field(None, description="전체 로컬 디스크(GB)")
    local_gb_used: Optional[int] = Field(None, description="사용 중인 로컬 디스크(GB)")
    running_vms: Optional[int] = Field(None, description="실행 중인 VM 수")
    hypervisor_type: Optional[str] = Field(None, description="가상화 종류 (예: QEMU)")
    hypervisor_version: Optional[int] = Field(None, description="가상화 소프트웨어 버전 번호")
    host_ip: Optional[str] = Field(None, description="물리 서버 관리 IP")
    vms: list[VMItem] = Field([], description="이 서버에 배치된 VM 목록")


class HypervisorDetailResponse(BaseModel):
    """물리 서버(하이퍼바이저) 상세 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: Optional[HypervisorDetailData] = Field(
        None, description="물리 서버 상세. 대상이 없으면 null"
    )
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


# ── VM 사용량 메트릭 (libvirt exporter) ─────────────────────────────────────

class VMCpuMetrics(BaseModel):
    """VM CPU 사용량."""

    cores_used: Optional[float] = Field(
        None, description="사용 중 vCPU 양(코어). 최근 5분 평균, 미수집 시 null"
    )
    cpu_time_ns_total: Optional[float] = Field(
        None, description="VM 시작 이후 누적 CPU 시간(ns). 미수집 시 null"
    )


class VMMemoryMetrics(BaseModel):
    """VM 메모리 사용량."""

    rss_bytes: Optional[float] = Field(
        None, description="물리 서버에서 이 VM이 실제로 점유한 메모리(bytes). 미수집 시 null"
    )
    current_bytes: Optional[float] = Field(
        None, description="VM에 현재 할당된 메모리(bytes). 미수집 시 null"
    )
    available_bytes: Optional[float] = Field(
        None, description="VM 안에서 인식하는 전체 메모리(bytes). 미수집 시 null"
    )
    unused_bytes: Optional[float] = Field(
        None, description="VM 안에서 쓰지 않는 메모리(bytes). 미수집 시 null"
    )
    usable_bytes: Optional[float] = Field(
        None, description="VM 안에서 바로 쓸 수 있는 메모리(bytes). 미수집 시 null"
    )
    used_bytes: Optional[float] = Field(
        None, description="VM 안에서 사용 중인 메모리(bytes). 할당 메모리 - 미사용 메모리, 계산 불가 시 null"
    )


class VMDiskMetrics(BaseModel):
    """VM 디스크 누적 입출력. 모든 디스크 합계."""

    read_bytes_total: Optional[float] = Field(
        None, description="VM 시작 이후 누적 읽기량(bytes). 미수집 시 null"
    )
    write_bytes_total: Optional[float] = Field(
        None, description="VM 시작 이후 누적 쓰기량(bytes). 미수집 시 null"
    )


class VMNetworkMetrics(BaseModel):
    """VM 네트워크 누적 트래픽. 모든 네트워크 인터페이스 합계."""

    rx_bytes_total: Optional[float] = Field(
        None, description="VM 시작 이후 누적 수신량(bytes). 미수집 시 null"
    )
    tx_bytes_total: Optional[float] = Field(
        None, description="VM 시작 이후 누적 송신량(bytes). 미수집 시 null"
    )


class VMMetricsData(BaseModel):
    """VM 자원 사용량 종합."""

    vm_id: str = Field(..., description=_D_VM_ID)
    name: Optional[str] = Field(None, description="VM 이름")
    host: Optional[str] = Field(None, description=_D_VM_HOST)
    state: Optional[str] = Field(
        None,
        description="가상화 계층에서 본 VM 상태 (nostate | running | blocked | paused | shutdown | shutoff | crashed | pmsuspended). 미수집 시 null",
    )
    cpu: VMCpuMetrics = Field(..., description="CPU 사용량. 사용 중 vCPU 양(코어), 누적 CPU 시간(ns)")
    memory: VMMemoryMetrics = Field(..., description="메모리 사용량. 실제 점유, 할당, 미사용, 사용 메모리(bytes)")
    disk: VMDiskMetrics = Field(..., description="디스크 누적 입출력. 읽기량, 쓰기량(bytes)")
    network: VMNetworkMetrics = Field(..., description="네트워크 누적 트래픽. 수신량, 송신량(bytes)")


class VMMetricsResponse(BaseModel):
    """VM 사용량 메트릭 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: Optional[VMMetricsData] = Field(None, description="VM 자원 사용량. 대상이 없으면 null")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


# ── VM 전력 귀속 (P6) ───────────────────────────────────────────────────────

class VMPowerData(BaseModel):
    """VM 추정 전력. 물리 서버 총 전력을 CPU 점유 비율로 나눈 추정치."""

    vm_id: str = Field(..., description=_D_VM_ID)
    name: Optional[str] = Field(None, description="VM 이름")
    host: Optional[str] = Field(None, description=_D_VM_HOST)
    attributed_watts: Optional[float] = Field(
        None, description="이 VM에 배분된 추정 전력(W). 서버 총 전력 x CPU 점유 비율, 계산 불가 시 null"
    )
    server_total_watts: Optional[float] = Field(
        None, description="물리 서버 총 전력(W). 서버 전원 장치(IPMI) 측정값, 미수집 시 null"
    )
    cpu_share_pct: Optional[float] = Field(
        None, description="같은 서버의 VM 전체 CPU 사용량 중 이 VM의 비율(%). 계산 불가 시 null"
    )
    method: str = Field(
        "cpu_proportional", description="배분 방식 (cpu_proportional). CPU 사용량 비율로 배분"
    )


class VMPowerResponse(BaseModel):
    """VM 추정 전력 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: Optional[VMPowerData] = Field(None, description="VM 추정 전력. 대상이 없으면 null")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)
