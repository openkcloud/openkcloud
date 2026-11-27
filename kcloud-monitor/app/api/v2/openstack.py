"""OpenStack 자원 조회 라우터

관리 클러스터 전용. 물리 서버(하이퍼바이저), 그 위에 올라간 VM, 프로젝트 단위 사용량 조회.

접속 정보가 없으면 NOT_CONFIGURED, OpenStack 호출이 실패하면 UPSTREAM_ERROR 경고와 함께
HTTP 200 + status="partial" 반환.
VM 이름은 가속기 메트릭의 hostname 라벨과 같은 값이라 두 데이터를 잇는 키로 쓰임.
"""
from typing import Optional

from fastapi import APIRouter, Depends, Request

from app.api.v2._stub import stub
from app.api.v2.deps import WorkloadFilterParams
from app.schemas.openstack import (
    HypervisorDetailData,
    HypervisorDetailResponse,
    HypervisorItem,
    HypervisorListResponse,
    ProjectDetailResponse,
    ProjectItem,
    ProjectListResponse,
    VMAccelerator,
    VMDetailResponse,
    VMItem,
    VMListResponse,
    VMMetricsData,
    VMMetricsResponse,
    VMPowerData,
    VMPowerResponse,
    VMSummaryData,
)
from app.services.openstack import OpenStackError, openstack_client, parse_accelerator_alias
from app.services.vm_metrics import vm_attributed_power, vm_usage

router = APIRouter()


def _error_code(exc: OpenStackError) -> str:
    """OpenStackError 메시지를 경고 코드로 변환. 알 수 없는 값은 UPSTREAM_ERROR."""
    return str(exc) if str(exc) in ("NOT_CONFIGURED", "UPSTREAM_ERROR") else "UPSTREAM_ERROR"


async def _collect_vms() -> tuple[list[VMItem], list[str]]:
    """Nova servers + flavors → VMItem 목록. (items, warnings) 반환."""
    if not openstack_client.nova_configured:
        return [], ["NOT_CONFIGURED"]
    try:
        flavor_specs = await openstack_client.flavors()
        servers = await openstack_client.servers()
    except OpenStackError as exc:
        code = _error_code(exc)
        return [], [code]

    # Cyborg에서 실제로 바인딩된 장치를 우선 쓰고, 없거나 실패하면 flavor 설정으로 판별한다.
    # 실패를 warnings로 올리지 않는 이유: 호출부들이 warnings가 있으면 VM 목록 전체를 비우기 때문
    # (Cyborg 장애가 VM 조회 장애로 번지면 안 됨). 실패 내용은 _cyborg_get이 로그로 남긴다.
    bound: dict[str, tuple[str, int]] = {}
    if openstack_client.cyborg_configured:
        try:
            bound = await openstack_client.bound_accelerators()
        except OpenStackError:
            pass

    items: list[VMItem] = []
    for s in servers:
        flavor_name = (s.get("flavor") or {}).get("original_name") or (s.get("flavor") or {}).get("id")
        spec = flavor_specs.get(flavor_name) or {} if flavor_name else {}
        acc = None
        parsed = bound.get(s.get("id", "")) or parse_accelerator_alias(spec.get("extra_specs") or {})
        if parsed:
            acc = VMAccelerator(alias=parsed[0], count=parsed[1])
        items.append(
            VMItem(
                vm_id=s.get("id", ""),
                name=s.get("name", ""),
                host=s.get("OS-EXT-SRV-ATTR:host"),
                status=s.get("status"),
                flavor=flavor_name,
                vcpus=spec.get("vcpus"),
                ram_mb=spec.get("ram"),
                project_id=s.get("tenant_id"),
                accelerator=acc,
            )
        )
    return items, []


def _vm_summary(vms: list[VMItem]) -> VMSummaryData:
    """VM 목록을 상태별·프로젝트별로 세어 집계값으로 정리."""
    by_status: dict[str, int] = {}
    by_project: dict[str, int] = {}
    accelerator_vm_count = 0
    for v in vms:
        if v.status:
            by_status[v.status] = by_status.get(v.status, 0) + 1
        if v.project_id:
            by_project[v.project_id] = by_project.get(v.project_id, 0) + 1
        if v.accelerator is not None:
            accelerator_vm_count += 1
    return VMSummaryData(
        total=len(vms),
        by_status=by_status,
        accelerator_vm_count=accelerator_vm_count,
        by_project=by_project,
    )


def _project_item(project_id: str, name: str, project_vms: list[VMItem]) -> ProjectItem:
    """프로젝트 하나의 VM 개수와 flavor 기준 자원 합계를 담은 항목."""
    return ProjectItem(
        project_id=project_id,
        name=name,
        vm_count=len(project_vms),
        accelerator_vm_count=sum(1 for v in project_vms if v.accelerator is not None),
        total_vcpus=sum(v.vcpus or 0 for v in project_vms),
        total_ram_mb=sum(v.ram_mb or 0 for v in project_vms),
    )


@router.get("/openstack/hypervisors", summary="하이퍼바이저 목록",
            response_model=HypervisorListResponse)
async def list_openstack_hypervisors(request: Request,params: WorkloadFilterParams = Depends()):
    """VM 을 실행하는 물리 서버(하이퍼바이저) 목록 조회

    입력 예시

    * `GET /api/v2/openstack/hypervisors`

    응답

    * 항목마다 `hostname`, `state` (`up` | `down`), `status` (`enabled` | `disabled`), `vm_count` (배치된 VM 개수)

    경고

    * `NOT_CONFIGURED`: OpenStack 접속 정보 미설정
    * `UPSTREAM_ERROR`: OpenStack 호출 실패
    * `NO_DATA`: 하이퍼바이저가 없음

    참고

    * `limit`, `offset`, `search` 같은 목록 옵션은 적용되지 않음
    """
    if not openstack_client.nova_configured:
        return HypervisorListResponse(status="partial", data=[], warnings=["NOT_CONFIGURED"])
    try:
        hvs = await openstack_client.hypervisors()
        servers = await openstack_client.servers()
    except OpenStackError as exc:
        code = _error_code(exc)
        return HypervisorListResponse(status="partial", data=[], warnings=[code])

    vm_by_host: dict[str, int] = {}
    for s in servers:
        h = s.get("OS-EXT-SRV-ATTR:host")
        if h:
            vm_by_host[h] = vm_by_host.get(h, 0) + 1

    items = [
        HypervisorItem(
            hostname=h.get("hypervisor_hostname", ""),
            state=h.get("state"),
            status=h.get("status"),
            vm_count=vm_by_host.get(h.get("hypervisor_hostname", ""), 0),
        )
        for h in hvs
    ]
    warnings = [] if items else ["NO_DATA"]
    return HypervisorListResponse(status="success" if items else "partial", data=items, warnings=warnings)


@router.get("/openstack/hypervisors/{host}/vms", summary="하이퍼바이저별 VM 목록",
            response_model=VMListResponse)
async def list_hypervisor_vms(request: Request,host: str):
    """물리 서버 한 대에 배치된 VM 목록 조회

    입력 예시

    * `GET /api/v2/openstack/hypervisors/compute1/vms`

    입력 옵션

    * `host`: 물리 서버 이름, 예 `compute1` (경로, 필수)

    응답

    * 항목마다 VM ID, 이름, 상태 (예 `ACTIVE`, `SHUTOFF`, `ERROR`), 배치된 물리 서버 이름
    * flavor 이름, vCPU 수, 메모리(MB), 소속 프로젝트 ID
    * `accelerator`: 연결된 가속기 종류(`alias`)와 카드 수 (없으면 `null`)
    * `summary`: 이 서버의 VM 수(`total`), 상태별 개수(`by_status`), 가속기 VM 수(`accelerator_vm_count`), 프로젝트별 개수(`by_project`)

    경고

    * `NOT_CONFIGURED`: OpenStack 접속 정보 미설정
    * `UPSTREAM_ERROR`: OpenStack 호출 실패
    * `NO_DATA`: 배치된 VM 이 없음
    """
    vms, warnings = await _collect_vms()
    filtered = [v for v in vms if v.host == host]
    if warnings:
        return VMListResponse(status="partial", data=[], warnings=warnings)
    return VMListResponse(status="success" if filtered else "partial",
                          data=filtered, summary=_vm_summary(filtered),
                          warnings=[] if filtered else ["NO_DATA"])


@router.get("/openstack/vms", summary="VM 목록", response_model=VMListResponse)
async def list_openstack_vms(request: Request,params: WorkloadFilterParams = Depends()):
    """전체 VM 목록 조회

    입력 예시

    * `GET /api/v2/openstack/vms`
    * `GET /api/v2/openstack/vms?search=gpu&limit=20`

    입력 옵션

    * `search`: VM 이름에 들어 있는 글자 (선택, 대소문자 무시)
    * `limit`: 한 번에 받을 개수 `1` ~ `1000` (기본 `100`)
    * `offset`: 건너뛸 개수 (기본 `0`)

    응답

    * 항목마다 VM ID, 이름, 상태 (예 `ACTIVE`, `SHUTOFF`, `ERROR`), 배치된 물리 서버 이름
    * flavor 이름, vCPU 수, 메모리(MB), 소속 프로젝트 ID
    * `accelerator`: 연결된 가속기 종류(`alias`)와 카드 수 (없으면 `null`)
    * `summary`: 전체 VM 수(`total`), 상태별 개수(`by_status`), 가속기 VM 수(`accelerator_vm_count`), 프로젝트별 개수(`by_project`)

    경고

    * `NOT_CONFIGURED`: OpenStack 접속 정보 미설정
    * `UPSTREAM_ERROR`: OpenStack 호출 실패

    참고

    * `summary` 는 `search`, `limit`, `offset` 에 영향받지 않음
    """
    vms, warnings = await _collect_vms()
    summary = _vm_summary(vms)
    if params.search:
        q = params.search.lower()
        vms = [v for v in vms if q in v.name.lower()]
    page = vms[params.offset : params.offset + params.limit]
    status = "partial" if warnings else "success"
    return VMListResponse(status=status, data=page, summary=summary, warnings=warnings)


@router.get("/openstack/vms/{vm_id}", summary="VM 상세", response_model=VMDetailResponse)
async def get_openstack_vm(request: Request,vm_id: str):
    """VM 한 대의 상세 조회

    입력 예시

    * `GET /api/v2/openstack/vms/gpu-vm-01`

    입력 옵션

    * `vm_id`: VM ID 또는 VM 이름, 예 `gpu-vm-01` (경로, 필수)

    응답

    * 항목마다 VM ID, 이름, 상태 (예 `ACTIVE`, `SHUTOFF`, `ERROR`), 배치된 물리 서버 이름
    * flavor 이름, vCPU 수, 메모리(MB), 소속 프로젝트 ID
    * `accelerator`: 연결된 가속기 종류(`alias`)와 카드 수 (없으면 `null`)

    경고

    * `NOT_CONFIGURED`: OpenStack 접속 정보 미설정
    * `UPSTREAM_ERROR`: OpenStack 호출 실패
    * `NOT_FOUND`: 없는 VM, `data` 는 `null`
    """
    match, warnings = await _resolve_vm(vm_id)
    if match is None:
        return VMDetailResponse(status="partial", data=None, warnings=warnings)
    return VMDetailResponse(status="success", data=match, warnings=[])


# ── 실구현: Keystone 프로젝트 / 하이퍼바이저 상세 ────────────────────────────

@router.get("/openstack/projects", summary="프로젝트 목록",
            response_model=ProjectListResponse)
async def list_openstack_projects(request: Request, params: WorkloadFilterParams = Depends()):
    """OpenStack 프로젝트 목록 조회

    입력 예시

    * `GET /api/v2/openstack/projects`
    * `GET /api/v2/openstack/projects?search=admin`

    입력 옵션

    * `search`: 프로젝트 이름에 들어 있는 글자 (선택, 대소문자 무시)

    응답

    * 항목마다 `project_id`, `name`
    * `vm_count`: 소속 VM 개수
    * `accelerator_vm_count`: 가속기가 연결된 VM 개수
    * `total_vcpus`, `total_ram_mb`: 소속 VM 의 flavor 기준 vCPU 합계, 메모리 합계(MB)

    경고

    * `NOT_CONFIGURED`: OpenStack 접속 정보 미설정
    * `UPSTREAM_ERROR`: OpenStack 호출 실패

    참고

    * `limit`, `offset` 은 적용되지 않음
    """
    if not openstack_client.configured:
        return ProjectListResponse(status="partial", data=[], warnings=["NOT_CONFIGURED"])
    try:
        projects = await openstack_client.keystone_projects()
    except OpenStackError as exc:
        code = _error_code(exc)
        return ProjectListResponse(status="partial", data=[], warnings=[code])

    vms, vm_warnings = await _collect_vms()
    vms_by_project: dict[str, list[VMItem]] = {}
    for v in vms:
        if v.project_id:
            vms_by_project.setdefault(v.project_id, []).append(v)

    items = [
        _project_item(pid, name, vms_by_project.get(pid, []))
        for pid, name in projects.items()
    ]
    if params.search:
        q = params.search.lower()
        items = [i for i in items if q in i.name.lower()]

    status = "partial" if vm_warnings else "success"
    return ProjectListResponse(status=status, data=items, warnings=vm_warnings)


@router.get("/openstack/projects/{project_id}", summary="프로젝트 상세",
            response_model=ProjectDetailResponse)
async def get_openstack_project(request: Request, project_id: str):
    """OpenStack 프로젝트 한 개의 상세 조회

    입력 예시

    * `GET /api/v2/openstack/projects/0a1b2c3d4e5f67890a1b2c3d4e5f6789`

    입력 옵션

    * `project_id`: 프로젝트 ID (경로, 필수)

    응답

    * `project_id`, `name`: 프로젝트 ID, 이름
    * `vm_count`: 소속 VM 개수
    * `accelerator_vm_count`: 가속기가 연결된 VM 개수
    * `total_vcpus`, `total_ram_mb`: 소속 VM 의 flavor 기준 vCPU 합계, 메모리 합계(MB)

    경고

    * `NOT_CONFIGURED`: OpenStack 접속 정보 미설정
    * `UPSTREAM_ERROR`: OpenStack 호출 실패
    * `NOT_FOUND`: 없는 프로젝트, `data` 는 `null`
    """
    if not openstack_client.configured:
        return ProjectDetailResponse(status="partial", data=None, warnings=["NOT_CONFIGURED"])
    try:
        projects = await openstack_client.keystone_projects()
    except OpenStackError as exc:
        code = _error_code(exc)
        return ProjectDetailResponse(status="partial", data=None, warnings=[code])

    name = projects.get(project_id)
    if name is None:
        return ProjectDetailResponse(status="partial", data=None, warnings=["NOT_FOUND"])

    vms, vm_warnings = await _collect_vms()
    project_vms = [v for v in vms if v.project_id == project_id]
    data = _project_item(project_id, name, project_vms)
    status = "partial" if vm_warnings else "success"
    return ProjectDetailResponse(status=status, data=data, warnings=vm_warnings)


@router.get("/openstack/hypervisors/{host}", summary="하이퍼바이저 상세",
            response_model=HypervisorDetailResponse)
async def get_openstack_hypervisor(request: Request,host: str):
    """물리 서버(하이퍼바이저) 한 대의 상세 조회

    입력 예시

    * `GET /api/v2/openstack/hypervisors/compute1`

    입력 옵션

    * `host`: 물리 서버 이름, 예 `compute1` (경로, 필수)

    응답

    * `hostname`, `state` (`up` | `down`), `status` (`enabled` | `disabled`)
    * `vcpus`, `vcpus_used`: 전체 vCPU 수, VM 에 배정된 vCPU 수
    * `memory_mb`, `memory_mb_used`: 전체 메모리, VM 에 배정된 메모리 (MB)
    * `local_gb`, `local_gb_used`: 전체 로컬 디스크, 사용 중인 로컬 디스크 (GB)
    * `running_vms`, `hypervisor_type`, `hypervisor_version`, `host_ip`: 실행 중 VM 수, 가상화 종류, 버전 번호, 관리 IP
    * `vms`: 배치된 VM 목록, 항목마다 ID, 이름, 상태, flavor, vCPU 수, 메모리(MB), 프로젝트 ID, `accelerator`

    경고

    * `NOT_CONFIGURED`: OpenStack 접속 정보 미설정
    * `UPSTREAM_ERROR`: OpenStack 호출 실패
    * `NOT_FOUND`: 없는 서버, `data` 는 `null`
    """
    if not openstack_client.nova_configured:
        return HypervisorDetailResponse(status="partial", data=None, warnings=["NOT_CONFIGURED"])
    try:
        hvs = await openstack_client.hypervisors()
    except OpenStackError as exc:
        code = _error_code(exc)
        return HypervisorDetailResponse(status="partial", data=None, warnings=[code])

    match = next((h for h in hvs if h.get("hypervisor_hostname") == host), None)
    if match is None:
        return HypervisorDetailResponse(status="partial", data=None, warnings=["NOT_FOUND"])

    vms, vm_warnings = await _collect_vms()
    placed = [v for v in vms if v.host == host]
    data = HypervisorDetailData(
        hostname=match.get("hypervisor_hostname", ""),
        state=match.get("state"),
        status=match.get("status"),
        vcpus=match.get("vcpus"),
        vcpus_used=match.get("vcpus_used"),
        memory_mb=match.get("memory_mb"),
        memory_mb_used=match.get("memory_mb_used"),
        local_gb=match.get("local_gb"),
        local_gb_used=match.get("local_gb_used"),
        running_vms=match.get("running_vms"),
        hypervisor_type=match.get("hypervisor_type"),
        hypervisor_version=match.get("hypervisor_version"),
        host_ip=match.get("host_ip"),
        vms=placed,
    )
    status = "partial" if vm_warnings else "success"
    return HypervisorDetailResponse(status=status, data=data, warnings=vm_warnings)


# ── 실구현: VM 사용량·전력 귀속 (libvirt exporter) ──────────────────────────

async def _resolve_vm(vm_id: str) -> tuple[Optional[VMItem], list[str]]:
    """vm_id(uuid 또는 name)를 nova에서 실 VMItem으로 해소. (match, warnings) 반환.

    libvirt/IPMI 쿼리에 넣는 uuid·host는 반드시 여기서 해소된 nova 출처 값을 쓴다
    (경로 파라미터 원문을 PromQL에 직접 넣지 않기 위함 — 인젝션 방지).
    """
    vms, warnings = await _collect_vms()
    if warnings:
        return None, warnings
    match = next((v for v in vms if v.vm_id == vm_id or v.name == vm_id), None)
    if match is None:
        return None, ["NOT_FOUND"]
    return match, []


@router.get("/openstack/vms/{vm_id}/metrics", summary="VM 사용량 메트릭",
            response_model=VMMetricsResponse)
async def get_openstack_vm_metrics(request: Request,vm_id: str):
    """VM 한 대의 실제 자원 사용량 조회

    입력 예시

    * `GET /api/v2/openstack/vms/gpu-vm-01/metrics`

    입력 옵션

    * `vm_id`: VM ID 또는 VM 이름, 예 `gpu-vm-01` (경로, 필수)

    응답

    * `vm_id`, `name`, `host`: VM ID, 이름, 배치된 물리 서버
    * `state`: 가상화 계층에서 본 VM 상태 (예 `running`, `paused`, `shutoff`)
    * `cpu`: 사용 중인 vCPU 양 (코어, 최근 5분 평균), 누적 CPU 시간 (ns)
    * `memory`: 실제 점유, 할당, 인식, 미사용, 즉시 사용 가능, 사용 중 메모리 (bytes)
    * `disk`: VM 시작 이후 누적 읽기량, 쓰기량 (bytes)
    * `network`: VM 시작 이후 누적 수신량, 송신량 (bytes)

    경고

    * `NO_DATA_LIBVIRT`: 가상화 계층 메트릭이 없음
    * `NOT_FOUND`: 없는 VM
    * `NOT_CONFIGURED`, `UPSTREAM_ERROR`: OpenStack 접속 정보 미설정, 호출 실패
    """
    match, warnings = await _resolve_vm(vm_id)
    if match is None:
        return VMMetricsResponse(status="partial", data=None, warnings=warnings)

    r = await vm_usage(match.vm_id)
    data = None
    if r["data"] is not None:
        data = VMMetricsData(vm_id=match.vm_id, name=match.name, host=match.host, **r["data"])
    return VMMetricsResponse(status=r["status"], data=data, warnings=r["warnings"])


@router.get("/openstack/vms/{vm_id}/power", summary="VM 추정 전력",
            response_model=VMPowerResponse)
async def get_openstack_vm_power(request: Request,vm_id: str):
    """VM 한 대에 배분된 추정 전력 조회

    입력 예시

    * `GET /api/v2/openstack/vms/gpu-vm-01/power`

    입력 옵션

    * `vm_id`: VM ID 또는 VM 이름, 예 `gpu-vm-01` (경로, 필수)

    응답

    * `vm_id`, `name`, `host`: VM ID, 이름, 배치된 물리 서버
    * `attributed_watts`: 이 VM 에 배분된 추정 전력 (W)
    * `server_total_watts`: 물리 서버 총 전력 (W), 서버 전원 장치(IPMI) 측정값
    * `cpu_share_pct`: 같은 서버 VM 전체 CPU 사용량 중 이 VM 의 비율 (%)
    * `method`: 배분 방식 (`cpu_proportional`)

    경고

    * `POWER_ATTRIBUTION_CPU_PROPORTIONAL`: 서버 총 전력에 CPU 사용량 비율을 곱한 추정치, 항상 붙음
    * `NO_DATA_SERVER_POWER`: 서버 전력이 없음
    * `NO_DATA_LIBVIRT`: CPU 사용량이 없음
    * `NODE_CPU_TOTAL_ZERO`: 같은 서버 VM 전체 CPU 사용량이 0
    * `NO_HOST`: 배치된 서버를 모름
    * `NOT_FOUND`: 없는 VM
    """
    match, warnings = await _resolve_vm(vm_id)
    if match is None:
        return VMPowerResponse(status="partial", data=None, warnings=warnings)

    r = await vm_attributed_power(match.vm_id, match.host)
    data = None
    if r["data"] is not None:
        data = VMPowerData(vm_id=match.vm_id, name=match.name, host=match.host, **r["data"])
    return VMPowerResponse(status=r["status"], data=data, warnings=r["warnings"])


# ── 미구현(libvirt hostdev 필요) — stub 유지 ─────────────────────────────

@router.get("/openstack/vms/{vm_id}/gpu-passthrough", summary="VM GPU passthrough 확인")
async def get_openstack_vm_gpu_passthrough(request: Request,vm_id: str):
    """VM 한 대가 물리 가속기를 직접 넘겨받았는지 조회

    입력 예시

    * `GET /api/v2/openstack/vms/gpu-vm-01/gpu-passthrough`

    입력 옵션

    * `vm_id`: VM ID 또는 VM 이름, 예 `gpu-vm-01` (경로, 필수)

    응답

    * `status`: 항상 `not_implemented`
    * `data`: 항상 `null`
    * `api`, `path_template`, `description`, `data_sources`: 요청 경로, 경로 형식, 기능 설명, 사용할 데이터 출처

    경고

    * `NOT_IMPLEMENTED`: 데이터를 아직 수집하지 않음
    """
    return stub(request, "VM GPU passthrough 확인(libvirt hostdev)", sources=("libvirt(hostdev)",))
