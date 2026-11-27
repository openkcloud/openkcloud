"""노드(서버 한 대) 조회 라우터

운영체제 수준 지표는 node_exporter, 전력과 하드웨어 센서는 IPMI 기반.
노드 전력은 BMC 실측(벽면 전력) 하나로 통일했다. 노드 8대가 같은 기준이라 서로 비교할 수 있다.
CPU/메모리만 재는 Kepler는 측정 범위가 CPU 세대에 따라 갈려(psys/package) 노드 간 비교가
불가능하므로 노드 경로에서 쓰지 않고, 클러스터·시스템 요약의 CPU 계층에서만 제공한다.
hardware 하위 경로는 IPMI 센서를 걷어오지 않는 노드에서 IPMI_NOT_AVAILABLE 경고와
함께 빈 데이터 반환.
"""
from datetime import datetime, timedelta, timezone
from typing import Optional

from fastapi import APIRouter, Depends, Query, Request

from app.api.v2.clusters import _card_id, _collect_cards, _phys_node_set, _require_cluster
from app.api.v2.deps import PaginationParams, TimeseriesParams
from app.schemas.nodes import (
    CpuCoreUsage,
    CpuModeBreakdown,
    DiskIoRate,
    FilesystemUsage,
    HardwarePowerResponse,
    HardwareSensor,
    HardwareSensorsResponse,
    HardwareTemperatureResponse,
    HardwareTemperatureSensor,
    NetworkInterfaceStats,
    NodeCpuData,
    NodeCpuResponse,
    NodeDetailData,
    NodeDetailResponse,
    NodeListResponse,
    NodeMemoryData,
    NodeMemoryResponse,
    NodeMetricsData,
    NodeMetricsResponse,
    NodeNetworkData,
    NodeNetworkResponse,
    NodeOsInfo,
    NodePowerData,
    NodePowerResponse,
    NodePowerTimeseriesPoint,
    NodePowerTimeseriesResponse,
    NodeStorageData,
    NodeStorageResponse,
    NodeSummaryItem,
    NodesSummaryData,
)
from app.services.cluster_discovery import ClusterInfo, cluster_label
from app.services.prometheus import esc, first_finite, prometheus_client, value_map

router = APIRouter()

# 실디스크 파일시스템만 남기는 PromQL 라벨 조건. tmpfs·ramfs·overlay 같은 가상 파일시스템을
# 용량 집계에 넣으면 사용률이 왜곡되고, `/run/user/*`처럼 로그인 수에 따라 개수가 변한다.
REAL_FS = 'fstype=~"ext4|xfs|btrfs"'


def _cl(cluster: str) -> str:
    """cluster path param → escaped PromQL label value."""
    return esc(cluster_label(cluster))


async def _accelerator_count_for_host(host: str) -> int:
    """해당 호스트(node)에 연결된 가속기(카드) 수. cluster 라벨 없이 hostname/Hostname으로
    매칭한다 — compute6처럼 cluster 라벨이 누락된 물리 L40S도 이 방식이면 잡힌다."""
    h = esc(host)
    queries = [
        f'DCGM_FI_DEV_GPU_UTIL{{Hostname="{h}"}}',
        f'furiosa_npu_core_utilization{{hostname="{h}"}}',
        f'{{__name__="RBLN_DEVICE_STATUS:UTILIZATION",hostname="{h}"}}',
    ]
    ids: set[str] = set()
    for q in queries:
        for item in await prometheus_client.instant(q):
            ids.add(_card_id(item.get("metric", {})))
    return len(ids)


async def _service_cluster_hosts(
    info: ClusterInfo, phys_nodes: set[str]
) -> list[tuple[str, bool, int, Optional[float], Optional[float]]]:
    """서비스 클러스터(가속기 VM/K8s)의 노드(호스트) 목록 — 가속기 메트릭 hostname 기반.

    kube_node_info가 없는 l40s/rebellions/k8s-furiosa-rngd 등에서 노드 목록의 유일한 단서다.
    호스트 발견에 쓴 사용률 응답을 그대로 평균 내어 호스트 사용률도 함께 돌려준다 (추가 쿼리 없음).
    Returns: (host, is_physical, accelerator_count, power_watts, utilization_percent) 목록.
    """
    cards_by_key, power_by_id = await _collect_cards(info)
    host_cards: dict[str, set[str]] = {}
    host_utils: dict[str, list[float]] = {}
    for (host, cid), e in cards_by_key.items():
        host_cards.setdefault(host, set()).add(cid)
        host_utils.setdefault(host, []).extend(e["utils"])

    rows: list[tuple[str, bool, int, Optional[float], Optional[float]]] = []
    for host, cards in host_cards.items():
        powers = [power_by_id[c] for c in cards if c in power_by_id]
        utils = host_utils.get(host, [])
        rows.append((
            host, host in phys_nodes, len(cards),
            sum(powers) if powers else None,
            round(sum(utils) / len(utils), 1) if utils else None,
        ))
    return rows


async def _resolve_instance(cluster: str, node: str) -> str:
    """노드 param이 k8s 노드 이름이면 node-exporter instance(IP:9100)로 변환.
    이미 instance 형태거나 해석 불가면 원본을 그대로 반환(하위호환)."""
    c = _cl(cluster)
    r = await prometheus_client.instant(
        f'node_uname_info{{cluster="{c}",nodename="{esc(node)}"}}'
    )
    if r:
        inst = r[0].get("metric", {}).get("instance")
        if inst:
            return inst
    return node


async def _node_label(cluster: str, node: str) -> tuple[str, str]:
    """404 확인 후 노드를 instance로 풀어 (instance, node-exporter PromQL 라벨 조건) 반환."""
    await _require_cluster(cluster)
    instance = await _resolve_instance(cluster, node)
    return instance, f'cluster="{_cl(cluster)}",instance="{esc(instance)}"'


async def _ipmi_node_power(node: str, *window: str) -> tuple[list[dict], str]:
    """물리 노드 서버 총전력 — BMC 실측. 결과와 산출 경로를 함께 반환.
    window(start, end, step)를 주면 같은 기준의 시계열.

    노드 전력은 IPMI를 기준으로 삼는다. BMC가 전원공급장치를 직접 읽으므로 CPU·메모리뿐
    아니라 가속기·팬·디스크·PSU 손실까지 포함한 벽면 전력이고, 노드 8대가 같은 기준이라
    서로 비교할 수 있다. Kepler는 CPU 세대에 따라 psys/package로 측정 범위가 갈려
    노드 간 비교가 불가능하다(CPU/RAM 계층은 클러스터·시스템 요약에서 별도 제공).

    DCMI가 기본이며, mlt만 BMC가 DCMI 명령을 지원하지 않는다(2026-08-26 확정).
    그 노드는 PSU 입력 전력 합으로 대체한다. 둘 다 벽면 전력이지만 산출 경로가 달라
    (compute5 실측 DCMI 417W vs PSU 입력 합 400W) source로 구분해 알린다.
    ipmi 메트릭에는 cluster 라벨이 없어 물리 `node` 라벨로 식별한다.
    """
    async def query(q: str) -> list[dict]:
        if window:
            return await prometheus_client.range_query(q, *window)
        return await prometheus_client.instant(q)

    n = esc(node)
    dcmi = await query(f'ipmi_dcmi_power_consumption_watts{{node="{n}"}}')
    if dcmi:
        return dcmi, "ipmi-dcmi"
    psu = await query(f'sum(ipmi_power_watts{{node="{n}",name=~".*(Power In|Input Power)"}})')
    return psu, "ipmi-psu-input"


SORTABLE_FIELDS = ("power_watts", "utilization_percent")


def _sort_nodes(nodes: list[NodeSummaryItem], sort_by: Optional[str], sort_order: str) -> None:
    """sort_by가 power_watts, utilization_percent면 그 값으로, 그 외는 이름순. 값이 없는 노드는 항상 맨 뒤."""
    if sort_by not in SORTABLE_FIELDS:
        nodes.sort(key=lambda n: n.nodename or "")
        return
    sign = -1 if sort_order == "desc" else 1
    nodes.sort(key=lambda n: (getattr(n, sort_by) is None, sign * (getattr(n, sort_by) or 0.0)))


def _host_items(info: ClusterInfo, cluster: str, host_rows) -> list[NodeSummaryItem]:
    """서비스 클러스터 호스트 행을 노드 항목으로. kube_node_info가 없어 가속기 메트릭 hostname을 노드로 삼는다."""
    return [
        NodeSummaryItem(
            nodename=host,
            internal_ip=None,
            role=None,
            cluster=cluster,
            vendor=info.vendor,
            up=True,  # 가속기 메트릭이 보고되는 시점 = 살아있는 호스트
            os=None,
            kubelet_version=None,
            node_type="physical" if is_phys else "virtual",
            accelerator_count=accel_count,
            power_watts=power,
            utilization_percent=util,
        )
        for host, is_phys, accel_count, power, util in host_rows
    ]


# ---------------------------------------------------------------------------
# Nodes
# ---------------------------------------------------------------------------


@router.get("/clusters/{cluster}/nodes", summary="노드 목록", response_model=NodeListResponse)
async def list_nodes(
    request: Request,
    cluster: str,
    params: PaginationParams = Depends(),
    node_type: Optional[str] = Query(None, description="노드 구분 필터 (physical | virtual)"),
):
    """클러스터에 속한 노드 목록 조회

    입력 예시

    * `GET /api/v2/clusters/mgmt/nodes`
    * `GET /api/v2/clusters/l40s/nodes?node_type=physical&sort_by=power_watts&sort_order=desc&limit=20`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `mgmt`, `l40s` (경로, 필수)
    * `limit`: 한 번에 받을 최대 개수 `1` ~ `1000` (기본 `100`)
    * `offset`: 건너뛸 개수, 페이지 이동용 (기본 `0`)
    * `sort_by`: `power_watts` | `utilization_percent` (선택, 없으면 이름순)
    * `sort_order`: `asc` | `desc` (기본 `asc`)
    * `search`: 노드 이름 또는 내부 IP 부분 일치 검색어, 대소문자 무시 (선택)
    * `node_type`: 노드 구분 필터 `physical` | `virtual` (선택)

    응답

    * 노드 이름, 소속 클러스터, 동작 여부(`up`), 종류(`physical` | `virtual`)
    * 내부 IP, 역할(`worker` | `control-plane`), OS 이미지, kubelet 버전, 관리 클러스터만
    * 가속기 벤더, 가속기 카드 수, 가속기 평균 사용률(%), 가속기 클러스터만
    * 전력(W), 관리 클러스터는 서버 전원 장치(IPMI) 측정값, 가속기 클러스터는 가속기 전력 합계
    * `total`: 필터 적용 후 전체 노드 수
    * `summary`: 동작 중 노드 수, 전체 노드 수, 메모리 합계와 사용량(bytes), 메모리 사용률(%)
    * `sort_by` 가 없거나 지원하지 않는 값이면 이름순으로 정렬, 값이 없는 노드는 맨 뒤

    경고

    * `NO_DATA`: 조건에 맞는 노드가 없음

    오류

    * 404: 없는 클러스터

    참고

    * 관리 클러스터는 Kubernetes 등록 노드 기준
    * 가속기 클러스터는 가속기를 보고하는 호스트 기준
    * `summary` 는 필터와 페이지와 무관하게 전체 노드 기준
    """
    info = await _require_cluster(cluster)
    phys_nodes = await _phys_node_set()
    c = _cl(cluster)

    # summary 블록의 메모리 합계. 클러스터 전체 sum 이라 노드 필터·페이지와 무관하다.
    total_mem = first_finite(
        await prometheus_client.instant(f'sum(node_memory_MemTotal_bytes{{cluster="{c}"}})')
    )
    avail_mem = first_finite(
        await prometheus_client.instant(f'sum(node_memory_MemAvailable_bytes{{cluster="{c}"}})')
    )

    nodes: list[NodeSummaryItem]
    if info.type == "management":
        info_results = await prometheus_client.instant(f'kube_node_info{{cluster="{c}"}}')
        role_results = await prometheus_client.instant(f'kube_node_role{{cluster="{c}"}}')
        ready_results = await prometheus_client.instant(
            f'kube_node_status_condition{{cluster="{c}",condition="Ready",status="true"}}'
        )
        # ipmi 메트릭에는 cluster 라벨이 없어 전체 조회 후 node 라벨로 매칭한다.
        # 단건 조회(`/nodes/{node}/power`)와 같은 기준을 쓴다. DCMI가 기본이고, DCMI를
        # 지원하지 않는 노드(mlt)는 PSU 입력 전력 합으로 채운다.
        power_results = await prometheus_client.instant("ipmi_dcmi_power_consumption_watts")
        psu_results = await prometheus_client.instant(
            'sum by (node) (ipmi_power_watts{name=~".*(Power In|Input Power)"})'
        )

        role_map: dict[str, str] = {}
        for item in role_results:
            metric = item.get("metric", {})
            n = metric.get("node")
            role = metric.get("role")
            if n and role:
                role_map[n] = role

        ready_map: dict[str, bool] = {}
        for item in ready_results:
            metric = item.get("metric", {})
            n = metric.get("node")
            if not n:
                continue
            val = first_finite([item]) or 0.0
            ready_map[n] = ready_map.get(n, False) or val == 1.0

        power_map = value_map(psu_results + power_results, "node", finite=True)  # DCMI가 나중에 덮어써 우선한다

        # kube_node_info 1시계열 = k8s 노드 1대. (node, internal_ip, os, kubelet_version)로 수집.
        nodes = []
        for item in info_results:
            metric = item.get("metric", {})
            n = metric.get("node")
            if not n:
                continue
            nodes.append(
                NodeSummaryItem(
                    nodename=n,
                    internal_ip=metric.get("internal_ip"),
                    role=role_map.get(n),
                    cluster=cluster,
                    up=ready_map.get(n, False),
                    os=metric.get("os_image") or metric.get("kernel_version"),
                    kubelet_version=metric.get("kubelet_version"),
                    node_type="physical" if n in phys_nodes else "virtual",
                    power_watts=power_map.get(n),
                )
            )
    else:
        nodes = _host_items(info, cluster, await _service_cluster_hosts(info, phys_nodes))

    # summary는 필터·페이지와 무관한 전체 노드 기준 — 필터 적용 전에 센다.
    mem_used = (total_mem - avail_mem) if total_mem is not None and avail_mem is not None else None
    summary = NodesSummaryData(
        ready_count=sum(1 for n in nodes if n.up),
        total_count=len(nodes),
        memory_total_bytes=total_mem,
        memory_used_bytes=mem_used,
        memory_usage_percent=(mem_used / total_mem * 100) if mem_used is not None and total_mem else None,
    )

    if node_type:
        nodes = [n for n in nodes if n.node_type == node_type]

    if params.search:
        q = params.search.lower()
        nodes = [n for n in nodes if q in (n.nodename or "").lower() or q in (n.internal_ip or "").lower()]

    _sort_nodes(nodes, params.sort_by, params.sort_order)

    total = len(nodes)
    page = nodes[params.offset : params.offset + params.limit]

    warnings: list[str] = [] if total else ["NO_DATA"]

    return NodeListResponse(
        status="success" if total else "partial",
        nodes=page,
        total=total,
        summary=summary,
        warnings=warnings,
    )


@router.get("/clusters/{cluster}/nodes/{node}", summary="노드 상세", response_model=NodeDetailResponse)
async def get_node(request: Request, cluster: str, node: str):
    """노드 한 대의 상세 조회

    입력 예시

    * `GET /api/v2/clusters/l40s/nodes/gpu-node-1`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `l40s` (경로, 필수)
    * `node`: 노드 이름, 예 `gpu-node-1` (경로, 필수)

    응답

    * `instance`, `cluster`, `up`: 노드 식별자, 소속 클러스터, 메트릭 보고 여부
    * `os`: 커널 종류, 릴리스, 버전, 아키텍처, 호스트명
    * `boot_time`: 마지막 부팅 시각, ISO 8601 UTC
    * `cpu_cores`, `memory_total_bytes`: CPU 코어 수, 전체 메모리(bytes)
    * `node_type`: 노드 종류 `physical` | `virtual`
    * `ready`: Kubernetes Ready 상태, 관리 클러스터만
    * `accelerator_count`: 장착된 가속기 카드 수

    경고

    * `NO_DATA`: 노드 데이터가 없음 (`data` 는 `null`)

    오류

    * 404: 없는 클러스터
    """
    instance, label = await _node_label(cluster, node)

    up_results = await prometheus_client.instant(f"up{{{label}}}")
    uname_results = await prometheus_client.instant(f"node_uname_info{{{label}}}")
    accel_count = await _accelerator_count_for_host(node)

    if not up_results and not uname_results and accel_count == 0:
        return NodeDetailResponse(status="partial", data=None, warnings=["NO_DATA"])

    boot_results = await prometheus_client.instant(f"node_boot_time_seconds{{{label}}}")
    mem_results = await prometheus_client.instant(f"node_memory_MemTotal_bytes{{{label}}}")

    # machine_cpu_cores(cAdvisor)가 있으면 물리 노드
    phys_results = await prometheus_client.instant(f'machine_cpu_cores{{node="{esc(node)}"}}')
    cpu_val = first_finite(phys_results)
    if cpu_val is None:
        cpu_val = first_finite(
            await prometheus_client.instant(f'count(node_cpu_seconds_total{{{label},mode="idle"}})')
        )

    ready_results = await prometheus_client.instant(
        f'kube_node_status_condition{{condition="Ready",status="true",node="{esc(node)}"}}'
    )
    ready = any(first_finite([item]) == 1.0 for item in ready_results) if ready_results else None

    up = any(first_finite([item]) == 1.0 for item in up_results)

    os_info = None
    if uname_results:
        metric = uname_results[0].get("metric", {})
        os_info = NodeOsInfo(
            sysname=metric.get("sysname"),
            release=metric.get("release"),
            version=metric.get("version"),
            machine=metric.get("machine"),
            nodename=metric.get("nodename"),
        )

    boot_val = first_finite(boot_results)
    boot_time = (
        datetime.fromtimestamp(boot_val, tz=timezone.utc).isoformat() if boot_val is not None else None
    )

    data = NodeDetailData(
        instance=instance,
        cluster=cluster,
        up=up,
        os=os_info,
        boot_time=boot_time,
        cpu_cores=int(cpu_val) if cpu_val is not None else None,
        memory_total_bytes=first_finite(mem_results),
        node_type="physical" if phys_results else "virtual",
        ready=ready,
        accelerator_count=accel_count,
    )
    return NodeDetailResponse(status="success", data=data, warnings=[])


@router.get(
    "/clusters/{cluster}/nodes/{node}/metrics", summary="노드 종합 메트릭", response_model=NodeMetricsResponse
)
async def get_node_metrics(request: Request, cluster: str, node: str):
    """노드 한 대의 주요 사용량 조회

    입력 예시

    * `GET /api/v2/clusters/l40s/nodes/gpu-node-1/metrics`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `l40s` (경로, 필수)
    * `node`: 노드 이름, 예 `gpu-node-1` (경로, 필수)

    응답

    * `cpu_usage_percent`: CPU 사용률(%)
    * `memory_usage_percent`, `memory_total_bytes`, `memory_used_bytes`: 메모리 사용률(%), 전체와 사용 메모리(bytes)
    * `disk_usage_percent`: 실디스크 사용률(%), 용량 가중 합계이며 `tmpfs` 같은 가상 파일시스템 제외
    * `network_receive_bytes_per_sec`, `network_transmit_bytes_per_sec`: 수신, 송신 처리량(bytes/s)

    경고

    * `NO_DATA`: 노드 값이 수집되지 않음

    오류

    * 404: 없는 클러스터

    참고

    * CPU 와 네트워크 값은 최근 5분 평균
    """
    _, label = await _node_label(cluster, node)

    cpu_results = await prometheus_client.instant(
        f'100 - (avg(rate(node_cpu_seconds_total{{{label},mode="idle"}}[5m])) * 100)'
    )
    mem_total_results = await prometheus_client.instant(f"node_memory_MemTotal_bytes{{{label}}}")
    mem_avail_results = await prometheus_client.instant(f"node_memory_MemAvailable_bytes{{{label}}}")
    # 용량 가중으로 계산한다. 파일시스템별 사용률을 avg()로 평균내면 1GB tmpfs가 500GB 디스크와
    # 같은 비중을 가져 실제 사용률이 희석된다(실측 27.9% → 9.2%). 또 `/run/user/*` tmpfs는
    # 로그인 수에 따라 늘어나 값이 흔들린다. REAL_FS로 실디스크만 남기면 크기 0인 ramfs의
    # 0/0 = NaN도 함께 걸러진다(NaN이 하나라도 섞이면 집계 전체가 NaN이 되어 null로 떨어짐).
    disk_label = f"{label},{REAL_FS}"
    disk_results = await prometheus_client.instant(
        f"(1 - sum(node_filesystem_avail_bytes{{{disk_label}}})"
        f" / sum(node_filesystem_size_bytes{{{disk_label}}})) * 100"
    )
    net_rx_results = await prometheus_client.instant(f"sum(rate(node_network_receive_bytes_total{{{label}}}[5m]))")
    net_tx_results = await prometheus_client.instant(f"sum(rate(node_network_transmit_bytes_total{{{label}}}[5m]))")

    mem_total = first_finite(mem_total_results)
    mem_avail = first_finite(mem_avail_results)
    mem_used = (mem_total - mem_avail) if mem_total is not None and mem_avail is not None else None
    mem_pct = (mem_used / mem_total * 100) if mem_used is not None and mem_total else None

    has_data = bool(cpu_results or mem_total_results or disk_results or net_rx_results)
    warnings: list[str] = [] if has_data else ["NO_DATA"]

    data = NodeMetricsData(
        cpu_usage_percent=first_finite(cpu_results),
        memory_usage_percent=mem_pct,
        memory_total_bytes=mem_total,
        memory_used_bytes=mem_used,
        disk_usage_percent=first_finite(disk_results),
        network_receive_bytes_per_sec=first_finite(net_rx_results),
        network_transmit_bytes_per_sec=first_finite(net_tx_results),
    )
    return NodeMetricsResponse(status="success" if has_data else "partial", data=data, warnings=warnings)


@router.get("/clusters/{cluster}/nodes/{node}/cpu", summary="노드 CPU 상세", response_model=NodeCpuResponse)
async def get_node_cpu(request: Request, cluster: str, node: str):
    """노드 한 대의 CPU 사용 상세 조회

    입력 예시

    * `GET /api/v2/clusters/l40s/nodes/gpu-node-1/cpu`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `l40s` (경로, 필수)
    * `node`: 노드 이름, 예 `gpu-node-1` (경로, 필수)

    응답

    * `usage_percent`: 전체 CPU 사용률(%)
    * `load1`, `load5`, `load15`: 1분, 5분, 15분 평균 부하
    * `per_core`: 코어 번호별 사용률(%)
    * `per_mode`: 처리 종류별 CPU 시간 비율, 예 `user`, `system`, `iowait`

    경고

    * `NO_DATA`: CPU 값이 수집되지 않음

    오류

    * 404: 없는 클러스터

    참고

    * 사용률과 시간 비율은 최근 5분 평균
    """
    _, label = await _node_label(cluster, node)

    usage_results = await prometheus_client.instant(
        f'100 - (avg(rate(node_cpu_seconds_total{{{label},mode="idle"}}[5m])) * 100)'
    )
    per_core_results = await prometheus_client.instant(
        f'100 - (avg by (cpu) (rate(node_cpu_seconds_total{{{label},mode="idle"}}[5m])) * 100)'
    )
    per_mode_results = await prometheus_client.instant(f"sum by (mode) (rate(node_cpu_seconds_total{{{label}}}[5m]))")
    load1_results = await prometheus_client.instant(f"node_load1{{{label}}}")
    load5_results = await prometheus_client.instant(f"node_load5{{{label}}}")
    load15_results = await prometheus_client.instant(f"node_load15{{{label}}}")

    per_core = [
        CpuCoreUsage(cpu=item.get("metric", {}).get("cpu", "?"), usage_percent=first_finite([item]) or 0.0)
        for item in per_core_results
    ]
    per_mode = [
        CpuModeBreakdown(mode=item.get("metric", {}).get("mode", "?"), rate=first_finite([item]) or 0.0)
        for item in per_mode_results
    ]

    has_data = bool(usage_results or per_core_results)
    warnings: list[str] = [] if has_data else ["NO_DATA"]

    data = NodeCpuData(
        usage_percent=first_finite(usage_results),
        load1=first_finite(load1_results),
        load5=first_finite(load5_results),
        load15=first_finite(load15_results),
        per_core=per_core,
        per_mode=per_mode,
    )
    return NodeCpuResponse(status="success" if has_data else "partial", data=data, warnings=warnings)


@router.get("/clusters/{cluster}/nodes/{node}/memory", summary="노드 메모리 상세", response_model=NodeMemoryResponse)
async def get_node_memory(request: Request, cluster: str, node: str):
    """노드 한 대의 메모리 사용 상세 조회

    입력 예시

    * `GET /api/v2/clusters/l40s/nodes/gpu-node-1/memory`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `l40s` (경로, 필수)
    * `node`: 노드 이름, 예 `gpu-node-1` (경로, 필수)

    응답

    * `total_bytes`, `available_bytes`, `used_bytes`: 전체, 가용, 사용 메모리(bytes)
    * `used_percent`: 메모리 사용률(%)
    * `cached_bytes`, `buffers_bytes`: 캐시, 버퍼 메모리(bytes)
    * `swap_total_bytes`, `swap_free_bytes`, `swap_used_bytes`: 스왑 전체, 가용, 사용량(bytes)

    경고

    * `NO_DATA`: 메모리 값이 수집되지 않음

    오류

    * 404: 없는 클러스터
    """
    _, label = await _node_label(cluster, node)

    total_results = await prometheus_client.instant(f"node_memory_MemTotal_bytes{{{label}}}")
    avail_results = await prometheus_client.instant(f"node_memory_MemAvailable_bytes{{{label}}}")
    cached_results = await prometheus_client.instant(f"node_memory_Cached_bytes{{{label}}}")
    buffers_results = await prometheus_client.instant(f"node_memory_Buffers_bytes{{{label}}}")
    swap_total_results = await prometheus_client.instant(f"node_memory_SwapTotal_bytes{{{label}}}")
    swap_free_results = await prometheus_client.instant(f"node_memory_SwapFree_bytes{{{label}}}")

    total = first_finite(total_results)
    avail = first_finite(avail_results)
    used = (total - avail) if total is not None and avail is not None else None
    used_pct = (used / total * 100) if used is not None and total else None
    swap_total = first_finite(swap_total_results)
    swap_free = first_finite(swap_free_results)
    swap_used = (swap_total - swap_free) if swap_total is not None and swap_free is not None else None

    warnings: list[str] = [] if total_results else ["NO_DATA"]

    data = NodeMemoryData(
        total_bytes=total,
        available_bytes=avail,
        used_bytes=used,
        used_percent=used_pct,
        cached_bytes=first_finite(cached_results),
        buffers_bytes=first_finite(buffers_results),
        swap_total_bytes=swap_total,
        swap_free_bytes=swap_free,
        swap_used_bytes=swap_used,
    )
    return NodeMemoryResponse(status="success" if total_results else "partial", data=data, warnings=warnings)


@router.get(
    "/clusters/{cluster}/nodes/{node}/storage", summary="노드 로컬 디스크 상세", response_model=NodeStorageResponse
)
async def get_node_storage(request: Request, cluster: str, node: str):
    """노드 한 대의 로컬 디스크 사용 상세 조회

    입력 예시

    * `GET /api/v2/clusters/l40s/nodes/gpu-node-1/storage`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `l40s` (경로, 필수)
    * `node`: 노드 이름, 예 `gpu-node-1` (경로, 필수)

    응답

    * `filesystems`: 마운트 위치, 파일시스템 종류, 전체 용량과 남은 용량(bytes), 사용률(%)
    * `disks`: 장치 이름, 읽기와 쓰기 처리량(bytes/s)

    경고

    * `NO_DATA`: 디스크 값이 수집되지 않음

    오류

    * 404: 없는 클러스터

    참고

    * Ceph 분산 스토리지 용량은 포함하지 않음
    """
    _, label = await _node_label(cluster, node)

    size_results = await prometheus_client.instant(f"node_filesystem_size_bytes{{{label}}}")
    avail_results = await prometheus_client.instant(f"node_filesystem_avail_bytes{{{label}}}")
    read_results = await prometheus_client.instant(f"rate(node_disk_read_bytes_total{{{label}}}[5m])")
    write_results = await prometheus_client.instant(f"rate(node_disk_written_bytes_total{{{label}}}[5m])")

    avail_map = value_map(avail_results, "mountpoint", finite=True)
    filesystems = []
    for item in size_results:
        metric = item.get("metric", {})
        mp = metric.get("mountpoint", "?")
        size = first_finite([item])
        avail = avail_map.get(mp)
        used_pct = (1 - avail / size) * 100 if size and avail is not None else None
        filesystems.append(
            FilesystemUsage(mountpoint=mp, fstype=metric.get("fstype"), size_bytes=size, avail_bytes=avail, used_percent=used_pct)
        )

    write_map = value_map(write_results, "device", finite=True)
    disks = [
        DiskIoRate(
            device=item.get("metric", {}).get("device", "?"),
            read_bytes_per_sec=first_finite([item]),
            write_bytes_per_sec=write_map.get(item.get("metric", {}).get("device")),
        )
        for item in read_results
    ]

    warnings: list[str] = [] if size_results else ["NO_DATA"]

    data = NodeStorageData(filesystems=filesystems, disks=disks)
    return NodeStorageResponse(status="success" if size_results else "partial", data=data, warnings=warnings)


@router.get(
    "/clusters/{cluster}/nodes/{node}/network", summary="노드 네트워크 상세", response_model=NodeNetworkResponse
)
async def get_node_network(request: Request, cluster: str, node: str):
    """노드 한 대의 네트워크 사용 상세 조회

    입력 예시

    * `GET /api/v2/clusters/l40s/nodes/gpu-node-1/network`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `l40s` (경로, 필수)
    * `node`: 노드 이름, 예 `gpu-node-1` (경로, 필수)

    응답

    * `interfaces`: 장치 이름, 수신과 송신 처리량(bytes/s), 수신과 송신 에러율(회/s)

    경고

    * `NO_DATA`: 네트워크 값이 수집되지 않음

    오류

    * 404: 없는 클러스터

    참고

    * 모든 값은 최근 5분 평균
    """
    _, label = await _node_label(cluster, node)

    rx_results = await prometheus_client.instant(f"rate(node_network_receive_bytes_total{{{label}}}[5m])")
    tx_results = await prometheus_client.instant(f"rate(node_network_transmit_bytes_total{{{label}}}[5m])")
    rx_err_results = await prometheus_client.instant(f"rate(node_network_receive_errs_total{{{label}}}[5m])")
    tx_err_results = await prometheus_client.instant(f"rate(node_network_transmit_errs_total{{{label}}}[5m])")

    tx_map = value_map(tx_results, "device", finite=True)
    rx_err_map = value_map(rx_err_results, "device", finite=True)
    tx_err_map = value_map(tx_err_results, "device", finite=True)

    interfaces = [
        NetworkInterfaceStats(
            device=item.get("metric", {}).get("device", "?"),
            receive_bytes_per_sec=first_finite([item]),
            transmit_bytes_per_sec=tx_map.get(item.get("metric", {}).get("device")),
            receive_errors_per_sec=rx_err_map.get(item.get("metric", {}).get("device")),
            transmit_errors_per_sec=tx_err_map.get(item.get("metric", {}).get("device")),
        )
        for item in rx_results
    ]

    warnings: list[str] = [] if rx_results else ["NO_DATA"]

    data = NodeNetworkData(interfaces=interfaces)
    return NodeNetworkResponse(status="success" if rx_results else "partial", data=data, warnings=warnings)


# ---------------------------------------------------------------------------
# Power
# ---------------------------------------------------------------------------


@router.get(
    "/clusters/{cluster}/nodes/{node}/power", summary="노드 서버 전력 현재값", response_model=NodePowerResponse
)
async def get_node_power(request: Request, cluster: str, node: str):
    """노드 한 대의 현재 서버 총전력 조회

    입력 예시

    * `GET /api/v2/clusters/l40s/nodes/gpu-node-1/power`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `l40s` (경로, 필수)
    * `node`: 노드 이름, 예 `gpu-node-1` (경로, 필수)

    응답

    * `watts`: 서버 총전력(W)
    * `source`: 산출 경로 `ipmi-dcmi` | `ipmi-psu-input`
    * `ipmi-dcmi`: BMC 의 DCMI 측정값
    * `ipmi-psu-input`: 전원 장치 입력 전력 합

    경고

    * `NO_POWER_DATA`: 전력 값이 없음, BMC 없는 가상 노드 등 (`data` 는 `null`)

    오류

    * 404: 없는 클러스터

    참고

    * BMC 가 측정한 벽면 전력
    * CPU, 메모리, 가속기, 팬, 디스크, 전원 장치 손실 포함
    """
    await _require_cluster(cluster)
    results, source = await _ipmi_node_power(node)

    if not results:
        return NodePowerResponse(status="partial", data=None, warnings=["NO_POWER_DATA"])

    data = NodePowerData(watts=first_finite(results), source=source)
    return NodePowerResponse(status="success", data=data, warnings=[])


@router.get(
    "/clusters/{cluster}/nodes/{node}/power/timeseries",
    summary="노드 서버 전력 시계열",
    response_model=NodePowerTimeseriesResponse,
)
async def get_node_power_timeseries(
    request: Request, cluster: str, node: str, params: TimeseriesParams = Depends()
):
    """노드 한 대의 서버 총전력 변화 추이 조회

    입력 예시

    * `GET /api/v2/clusters/l40s/nodes/gpu-node-1/power/timeseries`
    * `GET /api/v2/clusters/l40s/nodes/gpu-node-1/power/timeseries?period=6h&step=1m`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `l40s` (경로, 필수)
    * `node`: 노드 이름, 예 `gpu-node-1` (경로, 필수)
    * `period`: 조회 기간, 예 `30m`, `1h`, `7d` (기본 `1h`, 최대 `10d`)
    * `start`: 시작 시각, ISO 8601 (선택, 없으면 현재에서 `period` 만큼 이전)
    * `end`: 종료 시각, ISO 8601 (선택, 기본 현재)
    * `step`: 데이터 점 간격, 예 `1m`, `5m`, `1h` (기본 `5m`)

    응답

    * `series`: 측정 시각(`timestamp`, ISO 8601 UTC)과 서버 총전력(`watts`, W) 목록
    * `source`: 산출 경로 `ipmi-dcmi` | `ipmi-psu-input`

    경고

    * `NO_POWER_DATA`: 해당 기간에 전력 값이 없음

    오류

    * 404: 없는 클러스터

    참고

    * IPMI 기준 벽면 전력
    """
    await _require_cluster(cluster)
    now = datetime.now(timezone.utc)
    start = params.start or (now - timedelta(hours=1)).isoformat()
    end = params.end or now.isoformat()
    step = params.step

    results, source = await _ipmi_node_power(node, start, end, step)

    if not results:
        return NodePowerTimeseriesResponse(status="partial", series=[], warnings=["NO_POWER_DATA"])

    series = [
        NodePowerTimeseriesPoint(
            timestamp=datetime.fromtimestamp(float(v[0]), tz=timezone.utc).isoformat(),
            watts=float(v[1]),
        )
        for v in results[0].get("values", [])
    ]
    return NodePowerTimeseriesResponse(status="success", series=series, source=source, warnings=[])


# ---------------------------------------------------------------------------
# Hardware (IPMI — 물리 노드 전용)
# ---------------------------------------------------------------------------


@router.get(
    "/clusters/{cluster}/nodes/{node}/hardware/sensors",
    summary="하드웨어 센서 전체(IPMI)",
    response_model=HardwareSensorsResponse,
)
async def get_hardware_sensors(request: Request, cluster: str, node: str):
    """서버 본체 하드웨어 센서 값 전체 조회

    입력 예시

    * `GET /api/v2/clusters/l40s/nodes/gpu-node-1/hardware/sensors`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `l40s` (경로, 필수)
    * `node`: 노드 이름, 예 `gpu-node-1` (경로, 필수)

    응답

    * `sensors`: 센서 이름, 값, 단위, 온도와 팬 회전수와 전압과 전력 센서 포함

    경고

    * `IPMI_NOT_AVAILABLE`: IPMI 센서를 수집하지 않는 노드 (`sensors` 는 빈 목록)

    오류

    * 404: 없는 클러스터

    참고

    * 판독 불가 센서는 제외
    """
    await _require_cluster(cluster)
    results = await prometheus_client.instant(f'{{__name__=~"ipmi_.*",node="{esc(node)}"}}')

    if not results:
        return HardwareSensorsResponse(status="partial", sensors=[], warnings=["IPMI_NOT_AVAILABLE"])

    sensors = []
    for item in results:
        value = first_finite([item])
        if value is None:  # NaN 센서(미적용/판독불가) 제외 — JSON nan 방지
            continue
        metric = item.get("metric", {})
        sensors.append(
            HardwareSensor(
                name=metric.get("name") or metric.get("__name__", "?"),
                value=value,
                unit=metric.get("unit"),
            )
        )
    return HardwareSensorsResponse(status="success", sensors=sensors, warnings=[])


@router.get(
    "/clusters/{cluster}/nodes/{node}/hardware/power",
    summary="서버 본체 전력 실측(IPMI)",
    response_model=HardwarePowerResponse,
)
async def get_hardware_power(request: Request, cluster: str, node: str):
    """서버 한 대가 전원에서 끌어가는 전체 전력 조회

    입력 예시

    * `GET /api/v2/clusters/l40s/nodes/gpu-node-1/hardware/power`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `l40s` (경로, 필수)
    * `node`: 노드 이름, 예 `gpu-node-1` (경로, 필수)

    응답

    * `watts`: 서버 총전력(W)

    경고

    * `IPMI_NOT_AVAILABLE`: IPMI 센서를 수집하지 않는 노드 (`watts` 는 `null`)

    오류

    * 404: 없는 클러스터

    참고

    * BMC 의 DCMI 측정값
    * CPU, 가속기, 팬, 메모리 포함
    """
    await _require_cluster(cluster)
    results = await prometheus_client.instant(
        f'ipmi_dcmi_power_consumption_watts{{node="{esc(node)}"}}'
    )

    if not results:
        return HardwarePowerResponse(status="partial", watts=None, warnings=["IPMI_NOT_AVAILABLE"])

    return HardwarePowerResponse(status="success", watts=first_finite(results), warnings=[])


@router.get(
    "/clusters/{cluster}/nodes/{node}/hardware/temperature",
    summary="하드웨어 온도(IPMI)",
    response_model=HardwareTemperatureResponse,
)
async def get_hardware_temperature(request: Request, cluster: str, node: str):
    """서버 본체 온도 센서 값 조회

    입력 예시

    * `GET /api/v2/clusters/l40s/nodes/gpu-node-1/hardware/temperature`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `l40s` (경로, 필수)
    * `node`: 노드 이름, 예 `gpu-node-1` (경로, 필수)

    응답

    * `sensors`: 센서 이름, 온도(°C), CPU 흡기, 배기, 메인보드 등 위치별

    경고

    * `IPMI_NOT_AVAILABLE`: IPMI 센서를 수집하지 않는 노드 (`sensors` 는 빈 목록)

    오류

    * 404: 없는 클러스터
    """
    await _require_cluster(cluster)
    results = await prometheus_client.instant(f'ipmi_temperature_celsius{{node="{esc(node)}"}}')

    if not results:
        return HardwareTemperatureResponse(status="partial", sensors=[], warnings=["IPMI_NOT_AVAILABLE"])

    sensors = [
        HardwareTemperatureSensor(name=item.get("metric", {}).get("name", "?"), celsius=first_finite([item]) or 0.0)
        for item in results
    ]
    return HardwareTemperatureResponse(status="success", sensors=sensors, warnings=[])
