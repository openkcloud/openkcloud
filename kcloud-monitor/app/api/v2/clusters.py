"""클러스터 조회 라우터

모든 자원 탐색의 시작점. 클러스터 목록은 Prometheus의 cluster 라벨로 자동 발견.
"""
from typing import Optional

from fastapi import APIRouter, HTTPException, Query, Request

from app.schemas.clusters import (
    AcceleratorResources,
    ClusterDetailData,
    ClusterDetailResponse,
    ClusterListItem,
    ClusterListResponse,
    ClusterPagination,
    ClusterPower,
    ClusterPowerCard,
    ClusterPowerData,
    ClusterPowerResponse,
    ClusterResources,
    ClusterResourceData,
    ClusterResourceResponse,
    ClusterTopologyData,
    ClusterTopologyResponse,
    CpuResources,
    MemoryResources,
    NodeResources,
    PowerBreakdown,
    TopologyAccelerator,
    TopologyNode,
)
from app.services.cluster_discovery import ClusterInfo, cluster_discovery
from app.services.prometheus import first_finite, first_value, prometheus_client, sample_value

router = APIRouter()


async def _require_cluster(cluster: str) -> ClusterInfo:
    info = await cluster_discovery.get_cluster(cluster)
    if info is None:
        raise HTTPException(status_code=404, detail=f"알 수 없는 클러스터: {cluster}")
    return info


async def _node_counts(label_value: str) -> tuple[int, int, int, int, list[str]]:
    """노드(kube_node_info 기준)·타겟(up 기준) up/total 카운트.

    노드 수는 쿠버네티스 공식 노드(kube_node_info)를 기준으로 세고,
    타겟 수는 기존과 동일하게 Prometheus 스크랩 타겟(up)을 기준으로 센다.
    가속기 클러스터(rebellions/l40s/k8s-furiosa-rngd)는 kube_node_info가 없어
    node_total=0이 되는 것이 의도된 동작이다.

    Returns: (node_up, node_total, target_up, target_total, sorted_nodes)
    """
    node_info_results = await prometheus_client.instant(f'kube_node_info{{cluster="{label_value}"}}')
    node_names = {n for it in node_info_results if (n := it.get("metric", {}).get("node"))}

    ready_results = await prometheus_client.instant(
        f'kube_node_status_condition{{cluster="{label_value}",condition="Ready",status="true"}}'
    )
    target_results = await prometheus_client.instant(f'up{{cluster="{label_value}"}}')
    node_up = sum(1 for it in ready_results if sample_value(it) == 1.0)
    target_up = sum(1 for it in target_results if sample_value(it) == 1.0)

    return node_up, len(node_names), target_up, len(target_results), sorted(node_names)


async def _scalar(expr: Optional[str]) -> Optional[float]:
    """단일 스칼라 PromQL 결과(집계식 그대로). 없으면 None."""
    if not expr:
        return None
    return first_value(await prometheus_client.instant(expr))


async def _avg(query: Optional[str]) -> Optional[float]:
    return await _scalar(f"avg({query})") if query else None


async def _sum(query: Optional[str]) -> Optional[float]:
    return await _scalar(f"sum({query})") if query else None


async def _accelerator_count(query: Optional[str]) -> int:
    """가속기(카드) 개수. 전력 메트릭은 벤더 공통으로 카드 1장당 1시계열이라
    노드 수(up 타겟)가 아니라 이 카운트가 실제 카드 수다.
    (util은 Furiosa가 코어 단위로 쪼개져 과대집계되므로 쓰지 않는다.)"""
    if not query:
        return 0
    count = first_finite(await prometheus_client.instant(f"count({query})"))
    return int(count) if count is not None else 0


async def _targets(info: ClusterInfo) -> list[ClusterInfo]:
    """집계 대상 클러스터. 관리 클러스터는 하위 서비스 클러스터 전체, 그 외는 자기 자신."""
    if info.type != "management":
        return [info]
    clusters = await cluster_discovery.get_clusters()
    return [i for i in clusters.values() if i.type == "service"]


async def _distinct_instances(query: Optional[str]) -> int:
    """메트릭 시계열의 서로 다른 호스트(instance) 수. K8s 노드가 없는 가속기 VM용."""
    if not query:
        return 0
    results = await prometheus_client.instant(query)
    hosts: set[str] = set()
    for item in results:
        metric = item.get("metric", {})
        h = metric.get("instance") or metric.get("Hostname") or metric.get("hostname")
        if h:
            hosts.add(h)
    return len(hosts)


def _status_of(n_up: int, n_total: int, t_up: int, t_total: int) -> str:
    """healthy | warning | critical. K8s는 노드 Ready 비율, 그 외는 up 타겟 비율."""
    total = n_total if n_total > 0 else t_total
    up = n_up if n_total > 0 else t_up
    if total == 0:
        return "warning"  # 데이터 없음
    if up == total:
        return "healthy"
    if up == 0:
        return "critical"
    return "warning"


def _description_of(info: ClusterInfo) -> str:
    """사람이 읽는 설명 자동 생성.

    서비스 클러스터라도 진짜 K8s(Magnum)인지 단독 가속기 VM인지 구분해 표기한다
    (l40s/rebellions를 "서비스 클러스터"라 부르면 오해되므로 "단독 VM"으로).
    """
    if info.type == "management":
        return "관리 클러스터"
    accel = info.accelerator_type or "가속기"
    if info.is_kubernetes:
        return f"{accel} 쿠버네티스 클러스터"
    return f"{accel} VM"


async def _collect_raw(clusters: dict[str, ClusterInfo]) -> dict[str, dict]:
    """클러스터별 원시 메트릭(Prometheus) 수집. 목록·상세 공용."""
    raw: dict[str, dict] = {}
    for name, info in clusters.items():
        n_up, n_total, t_up, t_total, nodes = await _node_counts(info.label_value)
        gpu = await _accelerator_count(info.power_query)
        power = await _sum(info.power_query)
        node_count = n_total if n_total > 0 else await _distinct_instances(info.power_query)
        raw[name] = {
            "n_up": n_up, "n_total": n_total, "t_up": t_up, "t_total": t_total,
            "gpu": gpu, "power": power, "node_count": node_count, "nodes": nodes,
        }
    return raw


def _aggregate_metrics(
    name: str, info: ClusterInfo, raw: dict[str, dict], service_names: list[str]
) -> tuple[int, int, Optional[float], str]:
    """(node_count, accelerator_count, total_power, status). 관리 클러스터는 하위 합계."""
    r = raw[name]
    if info.type == "management":
        acc = sum(raw[s]["gpu"] for s in service_names)
        powers = [raw[s]["power"] for s in service_names if raw[s]["power"] is not None]
        power = sum(powers) if powers else None
    else:
        acc = r["gpu"]
        power = r["power"]
    return r["node_count"], acc, power, _status_of(r["n_up"], r["n_total"], r["t_up"], r["t_total"])


@router.get(
    "/clusters",
    summary="클러스터 목록",
    response_model=ClusterListResponse,
    response_model_exclude_none=True,  # 타입에 안 맞는 필드(parent_cluster/service_clusters 등)는 생략
)
async def list_clusters(
    request: Request,
    cluster_type: Optional[str] = Query(
        None, alias="type", description="클러스터 구분 (management | service)"
    ),
    project: Optional[str] = Query(
        None, description="OpenStack 프로젝트 이름 (그 프로젝트의 클러스터만)"
    ),
    limit: int = Query(100, ge=1, le=1000, description="한 번에 받을 개수 (1 ~ 1000, 기본 100)"),
    offset: int = Query(0, ge=0, description="건너뛸 개수 (기본 0)"),
):
    """운영 중인 클러스터 전체 목록 조회

    입력 예시

    * `GET /api/v2/clusters`
    * `GET /api/v2/clusters?type=service&limit=20&offset=0`

    입력 옵션

    * `type`: 클러스터 구분 `management` | `service` (선택)
    * `project`: OpenStack 프로젝트 이름 (선택, 그 프로젝트의 클러스터만)
    * `limit`: 한 번에 받을 개수 `1` ~ `1000` (기본 `100`)
    * `offset`: 건너뛸 개수 (기본 `0`)

    응답

    * 이름, 구분(`management` | `service`), 상태(`healthy` | `warning` | `critical`)
    * 부모 관리 클러스터, 소속 OpenStack 프로젝트, 하위 서비스 클러스터 목록, OpenStack 연동 여부
    * 노드 수, 가속기 수, 가속기 전력 합계(W)
    * `pagination`: 필터 적용 후 전체 개수, 페이지 크기, 건너뛴 개수, 다음 페이지 존재 여부
    * 관리 클러스터 먼저, 그다음 이름순으로 정렬

    경고

    * `NO_DATA`: 조건에 맞는 클러스터 없음

    참고

    * 관리 클러스터의 가속기 수와 전력은 하위 서비스 클러스터 합계
    * 해당하지 않는 항목은 키 자체가 빠짐
    """
    warnings: list[str] = []
    clusters = await cluster_discovery.get_clusters()

    # 1) 클러스터별 원시 메트릭 수집 (Prometheus) — 목록·상세 공용
    raw = await _collect_raw(clusters)
    service_names = [n for n, i in clusters.items() if i.type == "service"]

    # 2) 응답 아이템 구성 (관리 클러스터는 하위 합계로 집계)
    items: list[ClusterListItem] = []
    for name, info in clusters.items():
        if cluster_type and info.type != cluster_type:
            continue
        if project and info.openstack_project != project:
            continue
        node_count, acc, power, cstatus = _aggregate_metrics(name, info, raw, service_names)
        items.append(
            ClusterListItem(
                name=name,
                type=info.type,
                parent_cluster=info.parent_cluster,
                openstack_project=info.openstack_project,
                # 관리 클러스터만 True, 서비스는 None → exclude_none으로 생략
                has_openstack=True if info.has_openstack else None,
                service_clusters=info.service_clusters,
                node_count=node_count,
                accelerator_count=acc,
                total_power_watts=power,
                status=cstatus,
            )
        )

    # 3) 정렬(관리 클러스터 우선, 그다음 이름) + 페이지네이션
    items.sort(key=lambda it: (it.type != "management", it.name))
    total = len(items)
    page = items[offset: offset + limit]
    paging = ClusterPagination(
        total=total,
        limit=limit,
        offset=offset,
        has_next=offset + limit < total,
    )

    if total == 0:
        warnings.append("NO_DATA")
    status = "partial" if warnings else "success"
    return ClusterListResponse(status=status, data=page, pagination=paging, warnings=warnings)


@router.get(
    "/clusters/{cluster}",
    summary="클러스터 상세",
    response_model=ClusterDetailResponse,
    response_model_exclude_none=True,  # 타입에 안 맞는 필드는 생략(목록과 동일 정책)
)
async def get_cluster(request: Request, cluster: str):
    """클러스터 한 개의 상세 조회

    입력 예시

    * `GET /api/v2/clusters/l40s`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `l40s`, `mgmt` (경로, 필수)

    응답

    * 이름, 구분(`management` | `service`), 설명, 상태(`healthy` | `warning` | `critical`)
    * 부모 관리 클러스터, 소속 OpenStack 프로젝트, 하위 서비스 클러스터 목록, OpenStack 연동 여부
    * 노드 수, 가속기 수, 가속기 전력 합계(W)
    * 실행 형태(`kubernetes` | `vm`), 가속기 벤더, 가속기 종류(`GPU` | `NPU`)
    * 노드 이름 목록, 평균 가속기 사용률(%), 평균 가속기 온도(°C)

    경고

    * `TEMPERATURE_UNAVAILABLE`: 온도 메트릭이 없는 클러스터
    * `NO_DATA`: 노드와 가속기 값이 모두 없음

    오류

    * 404: 없는 클러스터

    참고

    * 해당하지 않는 항목은 키 자체가 빠짐
    """
    info = await _require_cluster(cluster)
    warnings: list[str] = []

    clusters = await cluster_discovery.get_clusters()
    raw = await _collect_raw(clusters)
    service_names = [n for n, i in clusters.items() if i.type == "service"]
    node_count, acc, power, cstatus = _aggregate_metrics(cluster, info, raw, service_names)

    avg_util = await _avg(info.utilization_query)
    avg_temp = await _avg(info.temperature_query)

    if info.temperature_query is None:
        warnings.append("TEMPERATURE_UNAVAILABLE")
    if node_count == 0 and acc == 0:
        warnings.append("NO_DATA")

    status = "partial" if warnings else "success"
    data = ClusterDetailData(
        name=cluster,
        type=info.type,
        description=_description_of(info),
        parent_cluster=info.parent_cluster,
        openstack_project=info.openstack_project,
        has_openstack=True if info.has_openstack else None,
        service_clusters=info.service_clusters,
        node_count=node_count,
        accelerator_count=acc,
        total_power_watts=power,
        status=cstatus,
        # 서비스 클러스터만 kubernetes/vm 구분 노출, 관리 클러스터는 None(생략)
        runtime=("kubernetes" if info.is_kubernetes else "vm") if info.type == "service" else None,
        vendor=info.vendor,
        accelerator_type=info.accelerator_type,
        nodes=raw[cluster]["nodes"],
        avg_utilization=avg_util,
        avg_temperature=avg_temp,
    )
    return ClusterDetailResponse(status=status, data=data, warnings=warnings)


def _rnd(x: Optional[float]) -> Optional[float]:
    return round(x, 1) if x is not None else None


async def _acc_summary(info: ClusterInfo) -> tuple[Optional[AcceleratorResources], Optional[float]]:
    """가속기 요약 (AcceleratorResources, gpu_watts). 관리 클러스터는 하위 서비스 합산.

    active/idle은 벤더별 산출 기준이 지저분해 현재 생략(None). total/avg_util/power만 채운다.
    """
    if info.type == "management":
        total = 0
        power_sum = 0.0
        power_any = False
        utils: list[float] = []
        for s in await _targets(info):
            total += await _accelerator_count(s.power_query)
            p = await _sum(s.power_query)
            if p is not None:
                power_sum += p
                power_any = True
            u = await _avg(s.utilization_query)
            if u is not None:
                utils.append(u)
        if total == 0 and not power_any:
            return None, None
        power = _rnd(power_sum) if power_any else None
        avg_util = _rnd(sum(utils) / len(utils)) if utils else None
        return AcceleratorResources(total=total, avg_utilization_percent=avg_util, total_power_watts=power), power

    total = await _accelerator_count(info.power_query)
    power = await _sum(info.power_query)
    avg_util = await _avg(info.utilization_query)
    if total == 0 and power is None:
        return None, None
    return (
        AcceleratorResources(total=total, avg_utilization_percent=_rnd(avg_util), total_power_watts=_rnd(power)),
        power,
    )


@router.get(
    "/clusters/{cluster}/resource",
    summary="클러스터 자원 현황",
    response_model=ClusterResourceResponse,
)
async def get_cluster_resource(request: Request, cluster: str):
    """클러스터 자원을 종류별로 합산한 현황 조회

    입력 예시

    * `GET /api/v2/clusters/mgmt/resource`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `l40s`, `mgmt` (경로, 필수)

    응답

    * `resources.nodes`: 전체, `Ready`, `NotReady` 노드 수
    * `resources.cpu`: 전체 코어 수, 사용 코어 수, 사용률(%)
    * `resources.memory`: 전체, 사용 메모리(GB), 사용률(%)
    * `resources.accelerators`: 카드 수, 평균 사용률(%), 전력 합계(W), 가속기가 없으면 `null`
    * `resources.storage`: 전체, 사용 용량(TB), 사용률(%), 미수집 시 `null`
    * `power`: 서버 총전력(W, IPMI 기준), `breakdown`(`cpu_watts`, `gpu_watts`, `other_watts`, 단위 W)

    경고

    * `NO_DATA`: 노드, 가속기, 메모리 값이 모두 없음

    오류

    * 404: 없는 클러스터

    참고

    * 값이 없는 항목도 키는 유지하고 `null` 반환
    * 가속기 클러스터는 가속기를 보고하는 호스트 수를 노드 수로 사용
    * 관리 클러스터의 가속기 값은 하위 서비스 클러스터 합계
    """
    info = await _require_cluster(cluster)
    warnings: list[str] = []
    L = info.label_value
    sel = f'{{cluster="{L}"}}'

    # ── nodes ──
    n_total = await _scalar(f"count(kube_node_info{sel})")
    n_ready = await _scalar(
        f'sum(kube_node_status_condition{{cluster="{L}",condition="Ready",status="true"}})'
    )
    if n_total:
        t = int(n_total)
        r = int(n_ready or 0)
    else:
        # 서비스 클러스터는 kube_node_info가 없음. 클러스터 목록, 노드 목록과 같은 기준으로
        # 가속기를 보고하는 호스트를 노드로 셈. 보고 중 = 살아 있음이라 ready = total
        t = r = await _distinct_instances(info.power_query)
    nodes = NodeResources(total=t, ready=r, not_ready=max(0, t - r))

    # ── cpu ──
    # machine_cpu_cores(cAdvisor)는 쿠버네티스 노드에만 있어 없으면 node_exporter 코어 수로 대신함
    cpu_total = await _scalar(f"sum(machine_cpu_cores{sel})") or await _scalar(
        f'count(node_cpu_seconds_total{{cluster="{L}",mode="idle"}})'
    )
    cpu_used = await _scalar(f'sum(rate(node_cpu_seconds_total{{cluster="{L}",mode!="idle"}}[5m]))')
    util = _rnd(cpu_used / cpu_total * 100) if cpu_total and cpu_used is not None else None
    cpu = CpuResources(total_cores=_rnd(cpu_total), used_cores=_rnd(cpu_used), utilization_percent=util)

    # ── memory ──
    mem_total = await _scalar(f"sum(node_memory_MemTotal_bytes{sel})")
    mem_avail = await _scalar(f"sum(node_memory_MemAvailable_bytes{sel})")
    used = (mem_total - mem_avail) if mem_total and mem_avail is not None else None
    memory = MemoryResources(
        total_gb=_rnd(mem_total / 1e9) if mem_total else None,
        used_gb=_rnd(used / 1e9) if used is not None else None,
        utilization_percent=_rnd(used / mem_total * 100) if used is not None else None,
    )

    # ── accelerators ──
    accelerators, gpu_watts = await _acc_summary(info)

    # ── power (ipmi 총량 / kepler cpu / 가속기 gpu / 나머지) ──
    p_total = await _scalar(f"sum(ipmi_dcmi_power_consumption_watts{sel})")
    p_cpu = await _scalar(f"sum(kepler_node_cpu_watts{sel})")
    breakdown = None
    if p_cpu is not None or gpu_watts is not None or p_total is not None:
        other = None
        if p_total is not None:
            other = _rnd(max(0.0, p_total - (p_cpu or 0.0) - (gpu_watts or 0.0)))
        breakdown = PowerBreakdown(cpu_watts=_rnd(p_cpu), gpu_watts=_rnd(gpu_watts), other_watts=other)
    power = ClusterPower(total_watts=_rnd(p_total), breakdown=breakdown)

    resources = ClusterResources(nodes=nodes, cpu=cpu, memory=memory, accelerators=accelerators, storage=None)

    if t == 0 and accelerators is None and memory.total_gb is None:
        warnings.append("NO_DATA")
    status = "partial" if warnings else "success"
    data = ClusterResourceData(cluster=cluster, type=info.type, resources=resources, power=power)
    return ClusterResourceResponse(status=status, data=data, warnings=warnings)


def _card_id(metric: dict) -> str:
    """가속기 카드 식별자 (벤더 공통). UUID > uuid > gpu > device."""
    return (
        metric.get("UUID") or metric.get("uuid") or metric.get("gpu")
        or metric.get("device") or "unknown"
    )


def _card_host(metric: dict) -> str:
    """카드가 보고된 호스트 (벤더별 라벨 차이 흡수)."""
    return metric.get("Hostname") or metric.get("hostname") or metric.get("instance") or "unknown"


def _card_model(metric: dict) -> Optional[str]:
    """모델명 (벤더별 라벨 차이 흡수)."""
    if metric.get("modelName"):
        return metric["modelName"]
    if metric.get("arch"):
        return f"Furiosa {metric['arch']}"
    if metric.get("card"):
        return f"Rebellions {metric['card']}"
    return None


async def _phys_node_set() -> set[str]:
    """machine_cpu_cores의 node 라벨 집합. 물리 노드 판별 기준."""
    results = await prometheus_client.instant("machine_cpu_cores")
    return {n for it in results if (n := it.get("metric", {}).get("node"))}


async def _collect_cards(info: ClusterInfo) -> tuple[dict[tuple[str, str], dict], dict[str, float]]:
    """한 클러스터의 가속기를 카드별로 수집. 토폴로지와 노드 목록 공용.

    Returns: ({(host, id): {"model", "utils"(코어 값 목록)}}, 카드 id별 전력 합)
    """
    if not info.utilization_query:
        return {}, {}
    util_res = await prometheus_client.instant(info.utilization_query)
    power_res = await prometheus_client.instant(info.power_query) if info.power_query else []

    power_by_id: dict[str, float] = {}
    for it in power_res:
        cid = _card_id(it.get("metric", {}))
        try:
            power_by_id[cid] = power_by_id.get(cid, 0.0) + float(it["value"][1])
        except (KeyError, IndexError, ValueError):
            continue

    # util: core 단위 시계열을 카드(=host,id)로 묶음
    cards: dict[tuple[str, str], dict] = {}
    for it in util_res:
        m = it.get("metric", {})
        e = cards.setdefault((_card_host(m), _card_id(m)), {"model": _card_model(m), "utils": []})
        try:
            e["utils"].append(float(it["value"][1]))
        except (KeyError, IndexError, ValueError):
            pass
    return cards, power_by_id


@router.get(
    "/clusters/{cluster}/topology",
    summary="클러스터 토폴로지",
    response_model=ClusterTopologyResponse,
    response_model_exclude_none=True,
)
async def get_cluster_topology(request: Request, cluster: str):
    """클러스터의 호스트와 호스트별 장착 가속기 구성 조회

    입력 예시

    * `GET /api/v2/clusters/furiosa/topology`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `furiosa`, `mgmt` (경로, 필수)

    응답

    * `cluster`: 클러스터 이름
    * `nodes`: 호스트 이름, 종류(`physical` | `virtual`), CPU 코어 수(물리 노드만)
    * `nodes[].accelerators`: 가속기 ID, 모델명, 사용률(%), 전력(W)
    * 가속기를 보고하는 호스트 기준, 이름순으로 정렬

    경고

    * `NO_DATA`: 호스트 없음

    오류

    * 404: 없는 클러스터

    참고

    * 관리 클러스터는 하위 서비스 클러스터의 가속기까지 호스트별로 합쳐 반환
    * 값이 없는 항목은 키 자체가 빠짐
    """
    info = await _require_cluster(cluster)
    warnings: list[str] = []

    # host → id → 카드정보. core 단위 사용률은 카드별 평균
    node_map: dict[str, dict[str, dict]] = {}
    for tinfo in await _targets(info):
        cards, power_by_id = await _collect_cards(tinfo)
        for (host, cid), e in cards.items():
            node_map.setdefault(host, {})[cid] = {
                "id": cid,
                "model": e["model"],
                "utilization_percent": _rnd(sum(e["utils"]) / len(e["utils"])) if e["utils"] else None,
                "power_watts": _rnd(power_by_id.get(cid)),
            }

    phys_nodes = await _phys_node_set()

    nodes: list[TopologyNode] = []
    for host, cards in sorted(node_map.items()):
        is_phys = host in phys_nodes
        cpu_cores = None
        if is_phys:
            c = await _scalar(f'max(machine_cpu_cores{{node="{host}"}})')
            cpu_cores = int(c) if c is not None else None
        accels = [TopologyAccelerator(**card) for card in cards.values()]
        nodes.append(
            TopologyNode(
                name=host,
                node_type="physical" if is_phys else "virtual",
                cpu_cores=cpu_cores,
                accelerators=accels,
            )
        )

    if not nodes:
        warnings.append("NO_DATA")
    status = "partial" if warnings else "success"
    data = ClusterTopologyData(cluster=cluster, nodes=nodes)
    return ClusterTopologyResponse(status=status, data=data, warnings=warnings)


@router.get("/clusters/{cluster}/power", summary="클러스터 가속기 전력 합계", response_model=ClusterPowerResponse)
async def get_cluster_power(request: Request, cluster: str):
    """클러스터 전체 가속기의 전력 합계 조회

    입력 예시

    * `GET /api/v2/clusters/rebellions/power`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `rebellions`, `mgmt` (경로, 필수)

    응답

    * `cluster`: 클러스터 이름
    * `total_power_watts`: 가속기 전력 합계(W)
    * `accelerator_count`: 전력을 보고한 카드 수
    * `cards`: 카드별 ID, 장착 호스트 이름, 전력(W)

    경고

    * `NO_DATA`: 전력 값 없음

    오류

    * 404: 없는 클러스터

    참고

    * 관리 클러스터는 하위 서비스 클러스터의 카드까지 합산
    """
    info = await _require_cluster(cluster)
    warnings: list[str] = []

    cards: list[ClusterPowerCard] = []
    for tinfo in await _targets(info):
        if not tinfo.power_query:
            continue
        for item in await prometheus_client.instant(tinfo.power_query):
            metric = item.get("metric", {})
            try:
                watts = float(item["value"][1])
            except (KeyError, IndexError, ValueError, TypeError):
                watts = None
            cards.append(
                ClusterPowerCard(id=_card_id(metric), hostname=_card_host(metric), watts=_rnd(watts))
            )

    total_power = _rnd(sum(c.watts for c in cards if c.watts is not None)) if cards else None
    acc_count = len(cards)

    if total_power is None:
        warnings.append("NO_DATA")

    status = "partial" if warnings else "success"
    data = ClusterPowerData(
        cluster=cluster, total_power_watts=total_power, accelerator_count=acc_count, cards=cards
    )
    return ClusterPowerResponse(status=status, data=data, warnings=warnings)
