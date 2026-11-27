"""클러스터 안에서 돌아가는 워크로드 조회 라우터

Pod, 컨테이너, 네임스페이스를 클러스터를 지정해서 조회.
클러스터를 고르지 않고 전체를 보려면 workloads_global.py 의 경로 사용.
"""
import re
from typing import Optional

from fastapi import APIRouter, Depends, Request

from app.api.v2.deps import WorkloadFilterParams, with_links
from app.schemas.workloads import (
    ContainerDetailResponse,
    ContainerItem,
    ContainerListResponse,
    ContainerMetricsData,
    ContainerMetricsResponse,
    NamespaceItem,
    NamespaceListResponse,
    NamespaceDetailData,
    NamespaceDetailResponse,
    PodAcceleratorItem,
    PodAcceleratorResponse,
    PodDetailData,
    PodDetailResponse,
    PodItem,
    PodListResponse,
    PodPowerData,
    PodPowerResponse,
    PodSummaryData,
    PodSummaryResponse,
)
from app.services.cluster_discovery import cluster_discovery, cluster_label
from app.services.prometheus import esc, first_value, prometheus_client, ts_iso, value_map

router = APIRouter()


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _cl(cluster: str) -> str:
    return esc(cluster_label(cluster))


def _derive_service_name(
    labels: dict[str, str],
    created_by_name: Optional[str],
    pod_name: str,
) -> Optional[str]:
    svc = labels.get("label_app_kubernetes_io_name")
    if svc:
        return svc
    svc = labels.get("label_k8s_app")
    if svc:
        return svc
    if created_by_name:
        return re.sub(r"-[a-f0-9]{7,10}$", "", created_by_name)
    stripped = re.sub(r"(-[a-z0-9]{5,10}){1,2}$", "", pod_name)
    return stripped if stripped != pod_name else None


# ---------------------------------------------------------------------------
# Internal fetch functions (also used by workloads_global.py)
# ---------------------------------------------------------------------------


async def fetch_pods(
    cluster: str, params: WorkloadFilterParams,
) -> tuple[list[PodItem], int, list[str]]:
    c = _cl(cluster)

    info_results = await prometheus_client.instant(f'kube_pod_info{{cluster="{c}"}}')
    phase_results = await prometheus_client.instant(
        f'kube_pod_status_phase{{cluster="{c}"}} == 1'
    )
    created_results = await prometheus_client.instant(f'kube_pod_created{{cluster="{c}"}}')
    label_results = await prometheus_client.instant(f'kube_pod_labels{{cluster="{c}"}}')
    restart_results = await prometheus_client.instant(
        f'sum by (namespace, pod) (kube_pod_container_status_restarts_total{{cluster="{c}"}})'
    )
    cnt_results = await prometheus_client.instant(
        f'count by (namespace, pod) (kube_pod_container_info{{cluster="{c}"}})'
    )
    cpu_results = await prometheus_client.instant(
        f'sum by (namespace, pod) (rate(container_cpu_usage_seconds_total{{cluster="{c}",container!=""}}[5m]))'
    )
    mem_results = await prometheus_client.instant(
        f'sum by (namespace, pod) (container_memory_working_set_bytes{{cluster="{c}",container!=""}})'
    )

    pod_map: dict[tuple[str, str], dict] = {}
    for item in info_results:
        m = item.get("metric", {})
        key = (m.get("namespace", ""), m.get("pod", ""))
        pod_map[key] = {
            "node": m.get("node"),
            "host_ip": m.get("host_ip"),
            "pod_ip": m.get("pod_ip"),
            "created_by_kind": m.get("created_by_kind"),
            "created_by_name": m.get("created_by_name"),
        }

    phase_map: dict[tuple[str, str], str] = {}
    for item in phase_results:
        m = item.get("metric", {})
        phase_map[(m.get("namespace", ""), m.get("pod", ""))] = m.get("phase", "Unknown")

    labels_map: dict[tuple[str, str], dict[str, str]] = {}
    for item in label_results:
        m = item.get("metric", {})
        labels_map[(m.get("namespace", ""), m.get("pod", ""))] = m

    created = value_map(created_results, "namespace", "pod")
    restarts = value_map(restart_results, "namespace", "pod")
    counts = value_map(cnt_results, "namespace", "pod")
    cpus = value_map(cpu_results, "namespace", "pod")
    mems = value_map(mem_results, "namespace", "pod")

    items: list[PodItem] = []
    for key, info in pod_map.items():
        ns, pod_name = key
        labels = labels_map.get(key, {})
        svc = _derive_service_name(labels, info.get("created_by_name"), pod_name)
        cnt_val = counts.get(key)
        rst_val = restarts.get(key)
        items.append(
            PodItem(
                namespace=ns,
                pod=pod_name,
                cluster=cluster,
                node=info.get("node"),
                phase=phase_map.get(key),
                pod_ip=info.get("pod_ip"),
                host_ip=info.get("host_ip"),
                created_at=ts_iso(created.get(key)),
                workload_type=info.get("created_by_kind"),
                workload_name=info.get("created_by_name"),
                service_name=svc,
                container_count=int(cnt_val) if cnt_val is not None else None,
                restart_count=int(rst_val) if rst_val is not None else None,
                cpu_usage=cpus.get(key),
                memory_usage_bytes=mems.get(key),
            )
        )

    if params.namespace:
        items = [i for i in items if i.namespace == params.namespace]
    if params.status:
        sl = params.status.lower()
        items = [i for i in items if i.phase and i.phase.lower() == sl]
    if params.workload_type:
        wl = params.workload_type.lower()
        items = [i for i in items if i.workload_type and i.workload_type.lower() == wl]
    if params.service_name:
        sn = params.service_name.lower()
        items = [i for i in items if i.service_name and sn in i.service_name.lower()]
    if params.search:
        q = params.search.lower()
        items = [i for i in items if q in i.pod.lower() or q in i.namespace.lower()]

    if params.sort_by:
        reverse = params.sort_order == "desc"
        items.sort(key=lambda x: getattr(x, params.sort_by, "") or "", reverse=reverse)

    total = len(items)
    items = items[params.offset : params.offset + params.limit]
    warnings: list[str] = [] if pod_map else ["NO_DATA"]
    return items, total, warnings


async def fetch_pods_summary(cluster: str) -> tuple[PodSummaryData, list[str]]:
    c = _cl(cluster)

    phase_results = await prometheus_client.instant(
        f'count by (phase) (kube_pod_status_phase{{cluster="{c}"}} == 1)'
    )
    ns_results = await prometheus_client.instant(
        f'count by (namespace) (kube_pod_info{{cluster="{c}"}})'
    )

    phase_counts: dict[str, int] = {}
    for item in phase_results:
        phase = item.get("metric", {}).get("phase", "Unknown")
        val = first_value([item])
        if val is not None:
            phase_counts[phase] = int(val)

    ns_dist = {ns: int(val) for ns, val in value_map(ns_results, "namespace").items()}

    total = sum(phase_counts.values())
    data = PodSummaryData(
        total_count=total,
        running_count=phase_counts.get("Running", 0),
        pending_count=phase_counts.get("Pending", 0),
        succeeded_count=phase_counts.get("Succeeded", 0),
        failed_count=phase_counts.get("Failed", 0),
        unknown_count=phase_counts.get("Unknown", 0),
        namespace_distribution=ns_dist,
    )
    warnings: list[str] = [] if total > 0 else ["NO_DATA"]
    return data, warnings


async def fetch_pod_detail(
    cluster: str, namespace: str, pod: str,
) -> tuple[Optional[PodDetailData], list[str]]:
    c = _cl(cluster)
    label = f'cluster="{c}",namespace="{esc(namespace)}",pod="{esc(pod)}"'

    info_results = await prometheus_client.instant(f"kube_pod_info{{{label}}}")
    if not info_results:
        return None, ["NO_DATA"]

    m = info_results[0].get("metric", {})

    phase_results = await prometheus_client.instant(
        f"kube_pod_status_phase{{{label}}} == 1"
    )
    created_results = await prometheus_client.instant(f"kube_pod_created{{{label}}}")
    label_results = await prometheus_client.instant(f"kube_pod_labels{{{label}}}")
    cpu_results = await prometheus_client.instant(
        f'sum(rate(container_cpu_usage_seconds_total{{{label},container!=""}}[5m]))'
    )
    mem_results = await prometheus_client.instant(
        f'sum(container_memory_working_set_bytes{{{label},container!=""}})'
    )
    cpu_req = await prometheus_client.instant(
        f'sum(kube_pod_container_resource_requests{{{label},resource="cpu"}})'
    )
    cpu_lim = await prometheus_client.instant(
        f'sum(kube_pod_container_resource_limits{{{label},resource="cpu"}})'
    )
    mem_req = await prometheus_client.instant(
        f'sum(kube_pod_container_resource_requests{{{label},resource="memory"}})'
    )
    mem_lim = await prometheus_client.instant(
        f'sum(kube_pod_container_resource_limits{{{label},resource="memory"}})'
    )
    restart_results = await prometheus_client.instant(
        f"sum(kube_pod_container_status_restarts_total{{{label}}})"
    )
    cnt_results = await prometheus_client.instant(
        f"count(kube_pod_container_info{{{label}}})"
    )

    phase = None
    if phase_results:
        phase = phase_results[0].get("metric", {}).get("phase")

    labels = label_results[0].get("metric", {}) if label_results else {}
    svc = _derive_service_name(labels, m.get("created_by_name"), pod)

    cnt_val = first_value(cnt_results)
    rst_val = first_value(restart_results)

    data = PodDetailData(
        namespace=namespace,
        pod=pod,
        cluster=cluster,
        uid=m.get("uid"),
        node=m.get("node"),
        phase=phase,
        pod_ip=m.get("pod_ip"),
        host_ip=m.get("host_ip"),
        created_at=ts_iso(first_value(created_results)),
        workload_type=m.get("created_by_kind"),
        workload_name=m.get("created_by_name"),
        service_name=svc,
        container_count=int(cnt_val) if cnt_val is not None else None,
        restart_count=int(rst_val) if rst_val is not None else None,
        cpu_usage=first_value(cpu_results),
        memory_usage_bytes=first_value(mem_results),
        cpu_requests=first_value(cpu_req),
        cpu_limits=first_value(cpu_lim),
        memory_requests_bytes=first_value(mem_req),
        memory_limits_bytes=first_value(mem_lim),
    )
    return data, []


async def fetch_pod_containers(
    cluster: str, namespace: str, pod: str,
) -> tuple[list[ContainerItem], list[str]]:
    c = _cl(cluster)
    label = f'cluster="{c}",namespace="{esc(namespace)}",pod="{esc(pod)}"'

    info_results = await prometheus_client.instant(f"kube_pod_container_info{{{label}}}")
    if not info_results:
        return [], ["NO_DATA"]

    running_results = await prometheus_client.instant(
        f"kube_pod_container_status_running{{{label}}}"
    )
    waiting_results = await prometheus_client.instant(
        f"kube_pod_container_status_waiting{{{label}}}"
    )
    terminated_results = await prometheus_client.instant(
        f"kube_pod_container_status_terminated{{{label}}}"
    )
    restart_results = await prometheus_client.instant(
        f"kube_pod_container_status_restarts_total{{{label}}}"
    )
    cpu_results = await prometheus_client.instant(
        f'rate(container_cpu_usage_seconds_total{{{label},container!=""}}[5m])'
    )
    mem_results = await prometheus_client.instant(
        f'container_memory_working_set_bytes{{{label},container!=""}}'
    )
    req_results = await prometheus_client.instant(
        f"kube_pod_container_resource_requests{{{label}}}"
    )
    lim_results = await prometheus_client.instant(
        f"kube_pod_container_resource_limits{{{label}}}"
    )

    running_m = value_map(running_results, "container")
    waiting_m = value_map(waiting_results, "container")
    terminated_m = value_map(terminated_results, "container")
    restart_m = value_map(restart_results, "container")
    cpu_m = value_map(cpu_results, "container")
    mem_m = value_map(mem_results, "container")
    req_map = value_map(req_results, "container", "resource")
    lim_map = value_map(lim_results, "container", "resource")

    def _status(cn: str) -> Optional[str]:
        if running_m.get(cn) == 1.0:
            return "running"
        if waiting_m.get(cn) == 1.0:
            return "waiting"
        if terminated_m.get(cn) == 1.0:
            return "terminated"
        return None

    containers: list[ContainerItem] = []
    for item in info_results:
        mi = item.get("metric", {})
        cn = mi.get("container", "")
        if not cn:
            continue
        rst = restart_m.get(cn)
        containers.append(
            ContainerItem(
                namespace=namespace,
                pod=pod,
                container=cn,
                cluster=cluster,
                container_id=mi.get("container_id"),
                image=mi.get("image"),
                status=_status(cn),
                restart_count=int(rst) if rst is not None else None,
                cpu_usage=cpu_m.get(cn),
                memory_usage_bytes=mem_m.get(cn),
                cpu_requests=req_map.get((cn, "cpu")),
                cpu_limits=lim_map.get((cn, "cpu")),
                memory_requests_bytes=req_map.get((cn, "memory")),
                memory_limits_bytes=lim_map.get((cn, "memory")),
            )
        )
    return containers, []


async def fetch_container_metrics(
    cluster: str, namespace: str, pod: str, container_name: str,
) -> tuple[ContainerMetricsData, list[str]]:
    c = _cl(cluster)
    label = (
        f'cluster="{c}",namespace="{esc(namespace)}",'
        f'pod="{esc(pod)}",container="{esc(container_name)}"'
    )
    cpu_results = await prometheus_client.instant(
        f"rate(container_cpu_usage_seconds_total{{{label}}}[5m])"
    )
    mem_results = await prometheus_client.instant(
        f"container_memory_working_set_bytes{{{label}}}"
    )
    has_data = bool(cpu_results or mem_results)
    data = ContainerMetricsData(
        cpu_usage=first_value(cpu_results),
        memory_usage_bytes=first_value(mem_results),
    )
    return data, [] if has_data else ["NO_DATA"]


async def fetch_pod_power(
    cluster: str, namespace: str, pod: str,
) -> tuple[Optional[PodPowerData], list[str]]:
    c = _cl(cluster)
    label = f'cluster="{c}",namespace="{esc(namespace)}",pod="{esc(pod)}"'

    results = await prometheus_client.instant(
        f"sum(rate(kepler_container_joules_total{{{label}}}[5m]))"
    )
    if not results:
        label_alt = (
            f'cluster="{c}",pod_namespace="{esc(namespace)}",'
            f'pod_name="{esc(pod)}"'
        )
        results = await prometheus_client.instant(
            f"sum(rate(kepler_container_joules_total{{{label_alt}}}[5m]))"
        )

    if not results:
        return None, ["NO_POWER_DATA"]

    return PodPowerData(watts=first_value(results), source="kepler"), []


async def fetch_pod_accelerators(
    cluster: str, namespace: str, pod: str,
) -> tuple[list[PodAcceleratorItem], list[str]]:
    info = await cluster_discovery.get_cluster(cluster)
    if not info or info.vendor != "nvidia":
        return [], ["POD_ACCELERATOR_DATA_NOT_AVAILABLE"]

    c = _cl(cluster)
    results = await prometheus_client.instant(
        f'DCGM_FI_DEV_GPU_UTIL{{cluster="{c}",exported_pod="{esc(pod)}"}}'
    )
    if not results:
        return [], ["POD_ACCELERATOR_DATA_NOT_AVAILABLE"]

    items: list[PodAcceleratorItem] = []
    for item in results:
        mi = item.get("metric", {})
        items.append(
            PodAcceleratorItem(
                acc_id=mi.get("gpu") or mi.get("UUID"),
                vendor="nvidia",
                model_name=mi.get("modelName"),
            )
        )
    return items, []


async def fetch_cluster_containers(
    cluster: str, params: WorkloadFilterParams,
) -> tuple[list[ContainerItem], int, list[str]]:
    c = _cl(cluster)

    info_results = await prometheus_client.instant(
        f'kube_pod_container_info{{cluster="{c}"}}'
    )
    if not info_results:
        return [], 0, ["NO_DATA"]

    running_results = await prometheus_client.instant(
        f'kube_pod_container_status_running{{cluster="{c}"}}'
    )
    restart_results = await prometheus_client.instant(
        f'kube_pod_container_status_restarts_total{{cluster="{c}"}}'
    )
    cpu_results = await prometheus_client.instant(
        f'rate(container_cpu_usage_seconds_total{{cluster="{c}",container!=""}}[5m])'
    )
    mem_results = await prometheus_client.instant(
        f'container_memory_working_set_bytes{{cluster="{c}",container!=""}}'
    )

    keys = ("namespace", "pod", "container")
    running_m = value_map(running_results, *keys)
    restart_m = value_map(restart_results, *keys)
    cpu_m = value_map(cpu_results, *keys)
    mem_m = value_map(mem_results, *keys)

    items: list[ContainerItem] = []
    for item in info_results:
        mi = item.get("metric", {})
        ns = mi.get("namespace", "")
        pd = mi.get("pod", "")
        cn = mi.get("container", "")
        if not cn:
            continue
        key = (ns, pd, cn)
        rst = restart_m.get(key)
        items.append(
            ContainerItem(
                namespace=ns,
                pod=pd,
                container=cn,
                cluster=cluster,
                container_id=mi.get("container_id"),
                image=mi.get("image"),
                status="running" if running_m.get(key) == 1.0 else None,
                restart_count=int(rst) if rst is not None else None,
                cpu_usage=cpu_m.get(key),
                memory_usage_bytes=mem_m.get(key),
            )
        )

    if params.namespace:
        items = [i for i in items if i.namespace == params.namespace]
    if params.search:
        q = params.search.lower()
        items = [i for i in items if q in i.container.lower() or q in i.pod.lower()]

    total = len(items)
    items = items[params.offset : params.offset + params.limit]
    return items, total, []


async def fetch_container_by_id(
    cluster: str, container_id: str,
) -> tuple[Optional[ContainerItem], list[str]]:
    c = _cl(cluster)
    info_results = await prometheus_client.instant(
        f'kube_pod_container_info{{cluster="{c}"}}'
    )

    query_id = container_id.split("://", 1)[-1]

    for item in info_results:
        mi = item.get("metric", {})
        raw_id = mi.get("container_id", "")
        stored_id = raw_id.split("://", 1)[-1]
        if stored_id and stored_id.startswith(query_id):
            ns = mi.get("namespace", "")
            pd = mi.get("pod", "")
            cn = mi.get("container", "")
            containers, _ = await fetch_pod_containers(cluster, ns, pd)
            for ci in containers:
                if ci.container == cn:
                    return ci, []
            return (
                ContainerItem(
                    namespace=ns, pod=pd, container=cn, cluster=cluster,
                    container_id=raw_id, image=mi.get("image"),
                ),
                [],
            )

    return None, ["NO_DATA"]


async def fetch_namespaces(
    cluster: str, params: WorkloadFilterParams,
) -> tuple[list[NamespaceItem], int, list[str]]:
    c = _cl(cluster)

    created_results = await prometheus_client.instant(
        f'kube_namespace_created{{cluster="{c}"}}'
    )
    pod_cnt_results = await prometheus_client.instant(
        f'count by (namespace) (kube_pod_info{{cluster="{c}"}})'
    )

    if not created_results:
        created_results = await prometheus_client.instant(
            f'kube_namespace_labels{{cluster="{c}"}}'
        )

    created_map: dict[str, Optional[str]] = {}
    ns_names: set[str] = set()
    for item in created_results:
        ns = item.get("metric", {}).get("namespace", "")
        if ns:
            ns_names.add(ns)
            val = first_value([item])
            created_map[ns] = ts_iso(val) if val and val > 0 else None

    pod_cnt_map: dict[str, int] = {}
    for item in pod_cnt_results:
        ns = item.get("metric", {}).get("namespace", "")
        val = first_value([item])
        if ns and val is not None:
            pod_cnt_map[ns] = int(val)
            ns_names.add(ns)

    sorted_ns = sorted(ns_names)
    if params.search:
        sorted_ns = [ns for ns in sorted_ns if params.search.lower() in ns.lower()]

    total = len(sorted_ns)
    page = sorted_ns[params.offset : params.offset + params.limit]

    items = [
        NamespaceItem(
            namespace=ns,
            cluster=cluster,
            pod_count=pod_cnt_map.get(ns),
            created_at=created_map.get(ns),
        )
        for ns in page
    ]
    warnings: list[str] = [] if ns_names else ["NO_DATA"]
    return items, total, warnings


async def fetch_namespace_summary(
    cluster: str, namespace: str,
) -> tuple[NamespaceDetailData, list[str]]:
    c = _cl(cluster)
    label = f'cluster="{c}",namespace="{esc(namespace)}"'

    pod_cnt = await prometheus_client.instant(f"count(kube_pod_info{{{label}}})")
    container_cnt = await prometheus_client.instant(
        f"count(kube_pod_container_info{{{label}}})"
    )
    cpu_results = await prometheus_client.instant(
        f'sum(rate(container_cpu_usage_seconds_total{{{label},container!=""}}[5m]))'
    )
    mem_results = await prometheus_client.instant(
        f'sum(container_memory_working_set_bytes{{{label},container!=""}})'
    )
    cpu_req = await prometheus_client.instant(
        f'sum(kube_pod_container_resource_requests{{{label},resource="cpu"}})'
    )
    mem_req = await prometheus_client.instant(
        f'sum(kube_pod_container_resource_requests{{{label},resource="memory"}})'
    )

    pc = first_value(pod_cnt)
    cc = first_value(container_cnt)

    data = NamespaceDetailData(
        namespace=namespace,
        cluster=cluster,
        pod_count=int(pc) if pc is not None else 0,
        container_count=int(cc) if cc is not None else 0,
        cpu_usage=first_value(cpu_results),
        memory_usage_bytes=first_value(mem_results),
        cpu_requests=first_value(cpu_req),
        memory_requests_bytes=first_value(mem_req),
    )
    warnings: list[str] = [] if pc else ["NO_DATA"]
    return data, warnings


# ---------------------------------------------------------------------------
# Route handlers — Pods
# ---------------------------------------------------------------------------


@router.get("/clusters/{cluster}/workloads/pods", summary="Pod 목록", response_model=PodListResponse)
async def list_pods(
    request: Request, cluster: str, params: WorkloadFilterParams = Depends(),
):
    """클러스터의 Pod 목록 조회

    입력 예시

    * `GET /api/v2/clusters/mgmt/workloads/pods`
    * `GET /api/v2/clusters/mgmt/workloads/pods?namespace=openstack&status=Running&limit=20`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `mgmt` (경로, 필수)
    * `limit`: 한 번에 받을 개수 `1` ~ `1000` (기본 `100`)
    * `offset`: 건너뛸 개수, 페이지 이동용 (기본 `0`)
    * `sort_by`: 정렬 기준이 될 응답 항목 이름, 예 `pod` (선택)
    * `sort_order`: `asc` | `desc` (기본 `asc`)
    * `namespace`: 네임스페이스 이름, 예 `openstack` (선택)
    * `status`: Pod 상태, 예 `Running` (선택, 대소문자 무시)
    * `workload_type`: 상위 리소스 종류, 예 `StatefulSet` (선택, 대소문자 무시)
    * `service_name`: 서비스 이름 일부, 예 `neutron` (선택)
    * `search`: Pod 이름 또는 네임스페이스에 들어 있는 글자 (선택, 대소문자 무시)

    응답

    * 네임스페이스(`namespace`), Pod 이름(`pod`), 소속 클러스터(`cluster`), 노드 이름(`node`)
    * 상태 `phase` (`Running` | `Pending` | `Succeeded` | `Failed` | `Unknown`)
    * `pod_ip`, `host_ip`, 생성 시각 `created_at`
    * 상위 리소스 종류와 이름 (`workload_type`, `workload_name`), 서비스 이름 `service_name`
    * 컨테이너 개수 `container_count`, 재시작 횟수 `restart_count`
    * `cpu_usage`: CPU 사용량 (코어, 최근 5분 평균)
    * `memory_usage_bytes`: 메모리 사용량 (bytes)
    * `total`: 필터를 적용한 뒤 전체 Pod 개수

    경고

    * `NO_DATA`: Pod 정보가 수집되지 않음
    """
    pods, total, warnings = await fetch_pods(cluster, params)
    status = "success" if not warnings else "partial"
    return PodListResponse(status=status, pods=pods, total=total, warnings=warnings)


@router.get(
    "/clusters/{cluster}/workloads/pods/summary",
    summary="Pod 집계 요약",
    response_model=PodSummaryResponse,
)
async def get_pods_summary(request: Request, cluster: str):
    """클러스터의 Pod 상태별 개수 조회

    입력 예시

    * `GET /api/v2/clusters/mgmt/workloads/pods/summary`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `mgmt` (경로, 필수)

    응답

    * `total_count`: 전체 Pod 개수
    * `running_count`, `pending_count`: `Running`, `Pending` 상태 개수
    * `succeeded_count`, `failed_count`, `unknown_count`: 정상 완료, 실패, 상태 불명 개수
    * `namespace_distribution`: 네임스페이스별 Pod 개수

    경고

    * `NO_DATA`: Pod 정보가 수집되지 않음
    """
    data, warnings = await fetch_pods_summary(cluster)
    status = "success" if not warnings else "partial"
    return PodSummaryResponse(status=status, data=data, warnings=warnings)


@router.get(
    "/clusters/{cluster}/workloads/pods/{namespace}/{pod}",
    summary="Pod 상세",
    response_model=PodDetailResponse,
)
async def get_pod(request: Request, cluster: str, namespace: str, pod: str):
    """Pod 한 개의 상세 조회

    입력 예시

    * `GET /api/v2/clusters/mgmt/workloads/pods/openstack/neutron-server-0`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `mgmt` (경로, 필수)
    * `namespace`: 네임스페이스 이름, 예 `openstack` (경로, 필수)
    * `pod`: Pod 이름, 예 `neutron-server-0` (경로, 필수)

    응답

    * 네임스페이스(`namespace`), Pod 이름(`pod`), 소속 클러스터(`cluster`), 고유 ID `uid`, 노드 이름(`node`)
    * 상태 `phase` (`Running` | `Pending` | `Succeeded` | `Failed` | `Unknown`)
    * `pod_ip`, `host_ip`, 생성 시각 `created_at`
    * 상위 리소스 종류와 이름 (`workload_type`, `workload_name`), 서비스 이름 `service_name`
    * 컨테이너 개수 `container_count`, 재시작 횟수 `restart_count`
    * `cpu_usage`, `cpu_requests`, `cpu_limits`: CPU 사용량, 요청량, 상한값 (코어)
    * `memory_usage_bytes`, `memory_requests_bytes`, `memory_limits_bytes`: 메모리 사용량, 요청량, 상한값 (bytes)

    경고

    * `NO_DATA`: Pod 가 없음 (`data` 는 `null`)
    """
    data, warnings = await fetch_pod_detail(cluster, namespace, pod)
    status = "success" if data else "partial"
    return PodDetailResponse(status=status, data=data, warnings=warnings)


@router.get(
    "/clusters/{cluster}/workloads/pods/{namespace}/{pod}/power",
    summary="Pod 추정 전력",
    response_model=PodPowerResponse,
)
async def get_pod_power(request: Request, cluster: str, namespace: str, pod: str):
    """Pod 한 개의 추정 전력 조회

    입력 예시

    * `GET /api/v2/clusters/mgmt/workloads/pods/openstack/neutron-server-0/power`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `mgmt` (경로, 필수)
    * `namespace`: 네임스페이스 이름, 예 `openstack` (경로, 필수)
    * `pod`: Pod 이름, 예 `neutron-server-0` (경로, 필수)

    응답

    * `watts`: Pod 추정 전력 (W, 최근 5분 평균)
    * `source`: 산출 근거 (`kepler`)

    경고

    * `NO_POWER_DATA`: 전력값이 없음 (`data` 는 `null`)

    참고

    * Kepler 가 컨테이너별로 추정한 전력의 합
    """
    data, warnings = await fetch_pod_power(cluster, namespace, pod)
    status = "success" if data else "partial"
    return PodPowerResponse(status=status, data=data, warnings=warnings)


@router.get(
    "/clusters/{cluster}/workloads/pods/{namespace}/{pod}/containers",
    summary="Pod 컨테이너 목록",
    response_model=ContainerListResponse,
)
async def list_pod_containers(
    request: Request, cluster: str, namespace: str, pod: str,
):
    """Pod 한 개 안의 컨테이너 목록 조회

    입력 예시

    * `GET /api/v2/clusters/mgmt/workloads/pods/openstack/neutron-server-0/containers`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `mgmt` (경로, 필수)
    * `namespace`: 네임스페이스 이름, 예 `openstack` (경로, 필수)
    * `pod`: Pod 이름, 예 `neutron-server-0` (경로, 필수)

    응답

    * 컨테이너 이름(`container`), 컨테이너 ID(`container_id`), 이미지(`image`)
    * 네임스페이스(`namespace`), Pod 이름(`pod`), 소속 클러스터(`cluster`)
    * 상태 `status` (`running` | `waiting` | `terminated`), 재시작 횟수 `restart_count`
    * `cpu_usage`, `cpu_requests`, `cpu_limits`: CPU 사용량, 요청량, 상한값 (코어)
    * `memory_usage_bytes`, `memory_requests_bytes`, `memory_limits_bytes`: 메모리 사용량, 요청량, 상한값 (bytes)
    * `total`: 컨테이너 개수

    경고

    * `NO_DATA`: Pod 정보가 없음 (빈 목록)
    """
    containers, warnings = await fetch_pod_containers(cluster, namespace, pod)
    status = "success" if not warnings else "partial"
    return ContainerListResponse(
        status=status, containers=containers, total=len(containers), warnings=warnings,
    )


@router.get(
    "/clusters/{cluster}/workloads/pods/{namespace}/{pod}/containers/{container_name}/metrics",
    summary="컨테이너 메트릭",
    response_model=ContainerMetricsResponse,
)
async def get_container_metrics(
    request: Request, cluster: str, namespace: str, pod: str, container_name: str,
):
    """컨테이너 한 개의 현재 자원 사용량 조회

    입력 예시

    * `GET /api/v2/clusters/mgmt/workloads/pods/openstack/neutron-server-0/containers/neutron-server/metrics`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `mgmt` (경로, 필수)
    * `namespace`: 네임스페이스 이름, 예 `openstack` (경로, 필수)
    * `pod`: Pod 이름, 예 `neutron-server-0` (경로, 필수)
    * `container_name`: 컨테이너 이름, 예 `neutron-server` (경로, 필수)

    응답

    * `cpu_usage`: CPU 사용량 (코어, 최근 5분 평균)
    * `memory_usage_bytes`: 메모리 사용량 (bytes, 실사용 기준)

    경고

    * `NO_DATA`: 두 값 모두 없음
    """
    data, warnings = await fetch_container_metrics(cluster, namespace, pod, container_name)
    status = "success" if not warnings else "partial"
    return ContainerMetricsResponse(status=status, data=data, warnings=warnings)


@router.get(
    "/clusters/{cluster}/workloads/pods/{namespace}/{pod}/accelerators",
    summary="Pod 할당 가속기",
    response_model=PodAcceleratorResponse,
)
async def get_pod_accelerators(
    request: Request, cluster: str, namespace: str, pod: str,
):
    """Pod 한 개에 배정된 가속기 조회

    입력 예시

    * `GET /api/v2/clusters/furiosa/workloads/pods/kube-system/neutron-server-0/accelerators`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `mgmt` (경로, 필수)
    * `namespace`: 네임스페이스 이름, 예 `openstack` (경로, 필수)
    * `pod`: Pod 이름, 예 `neutron-server-0` (경로, 필수)

    응답

    * 가속기 ID(`acc_id`), 벤더(`vendor`), 모델명(`model_name`)

    경고

    * `POD_ACCELERATOR_DATA_NOT_AVAILABLE`: 배정된 가속기가 없거나 조회를 지원하지 않는 클러스터 (빈 목록)

    참고

    * NVIDIA 클러스터만 값이 나옴
    """
    items, warnings = await fetch_pod_accelerators(cluster, namespace, pod)
    status = "success" if not warnings else "partial"
    return PodAcceleratorResponse(status=status, data=items, warnings=warnings)


# ---------------------------------------------------------------------------
# Route handlers — Containers (클러스터 전역)
# ---------------------------------------------------------------------------


@router.get(
    "/clusters/{cluster}/workloads/containers",
    summary="컨테이너 목록",
    response_model=ContainerListResponse,
)
async def list_containers(
    request: Request, cluster: str, params: WorkloadFilterParams = Depends(),
):
    """클러스터 전체 Pod 의 컨테이너 목록 조회

    입력 예시

    * `GET /api/v2/clusters/mgmt/workloads/containers`
    * `GET /api/v2/clusters/mgmt/workloads/containers?namespace=openstack&search=neutron&limit=20`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `mgmt` (경로, 필수)
    * `limit`: 한 번에 받을 개수 `1` ~ `1000` (기본 `100`)
    * `offset`: 건너뛸 개수, 페이지 이동용 (기본 `0`)
    * `namespace`: 네임스페이스 이름, 예 `openstack` (선택)
    * `search`: 컨테이너 이름 또는 Pod 이름에 들어 있는 글자 (선택, 대소문자 무시)

    응답

    * 컨테이너 이름(`container`), 컨테이너 ID(`container_id`), 이미지(`image`)
    * 네임스페이스(`namespace`), Pod 이름(`pod`), 소속 클러스터(`cluster`)
    * 상태 `status` (실행 중이면 `running`, 그 외 `null`), 재시작 횟수 `restart_count`
    * `cpu_usage`: CPU 사용량 (코어)
    * `memory_usage_bytes`: 메모리 사용량 (bytes)
    * `cpu_requests`, `cpu_limits`, `memory_requests_bytes`, `memory_limits_bytes`: 이 목록에서는 값이 없음 (`null`)
    * `total`: 필터를 적용한 뒤 전체 컨테이너 개수

    경고

    * `NO_DATA`: 컨테이너 정보가 수집되지 않음
    """
    containers, total, warnings = await fetch_cluster_containers(cluster, params)
    status = "success" if not warnings else "partial"
    return ContainerListResponse(
        status=status, containers=containers, total=total, warnings=warnings,
    )


@router.get(
    "/clusters/{cluster}/workloads/containers/{container_id}",
    summary="컨테이너 상세",
    response_model=ContainerDetailResponse,
)
async def get_container(request: Request, cluster: str, container_id: str):
    """컨테이너 ID 로 컨테이너 한 개의 상세 조회

    입력 예시

    * `GET /api/v2/clusters/mgmt/workloads/containers/3f2a9c1d`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `mgmt` (경로, 필수)
    * `container_id`: 컨테이너 ID, 앞부분만 넣어도 되고 `containerd://` 같은 접두어는 빼도 됨 (경로, 필수)

    응답

    * 컨테이너 이름(`container`), 컨테이너 ID(`container_id`), 이미지(`image`)
    * 네임스페이스(`namespace`), Pod 이름(`pod`), 소속 클러스터(`cluster`)
    * 상태 `status` (`running` | `waiting` | `terminated`), 재시작 횟수 `restart_count`
    * `cpu_usage`, `cpu_requests`, `cpu_limits`: CPU 사용량, 요청량, 상한값 (코어)
    * `memory_usage_bytes`, `memory_requests_bytes`, `memory_limits_bytes`: 메모리 사용량, 요청량, 상한값 (bytes)

    경고

    * `NO_DATA`: 없는 컨테이너 (`data` 는 `null`)
    """
    data, warnings = await fetch_container_by_id(cluster, container_id)
    status = "success" if data else "partial"
    return ContainerDetailResponse(status=status, data=data, warnings=warnings)


# ---------------------------------------------------------------------------
# Route handlers — Namespaces
# ---------------------------------------------------------------------------


@router.get(
    "/clusters/{cluster}/namespaces",
    summary="네임스페이스 목록",
    response_model=NamespaceListResponse,
)
async def list_namespaces(
    request: Request, cluster: str, params: WorkloadFilterParams = Depends(),
):
    """클러스터의 네임스페이스 목록 조회

    입력 예시

    * `GET /api/v2/clusters/mgmt/namespaces`
    * `GET /api/v2/clusters/mgmt/namespaces?search=kube&limit=20`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `mgmt` (경로, 필수)
    * `limit`: 한 번에 받을 개수 `1` ~ `1000` (기본 `100`)
    * `offset`: 건너뛸 개수, 페이지 이동용 (기본 `0`)
    * `search`: 네임스페이스 이름에 들어 있는 글자 (선택, 대소문자 무시)

    응답

    * 네임스페이스 이름(`namespace`), 소속 클러스터(`cluster`)
    * `pod_count`: Pod 개수
    * `created_at`: 생성 시각
    * `total`: 검색을 적용한 뒤 전체 네임스페이스 개수
    * 이름순으로 정렬

    경고

    * `NO_DATA`: 네임스페이스 정보가 수집되지 않음
    """
    items, total, warnings = await fetch_namespaces(cluster, params)
    status = "success" if not warnings else "partial"
    return NamespaceListResponse(
        status=status, namespaces=items, total=total, warnings=warnings,
    )


@router.get(
    "/clusters/{cluster}/namespaces/{namespace}",
    summary="네임스페이스 상세",
    response_model=NamespaceDetailResponse,
)
async def get_namespace(request: Request, cluster: str, namespace: str):
    """네임스페이스 한 개의 자원 사용 상세 조회

    입력 예시

    * `GET /api/v2/clusters/mgmt/namespaces/openstack`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `mgmt` (경로, 필수)
    * `namespace`: 네임스페이스 이름, 예 `openstack` (경로, 필수)

    응답

    * 네임스페이스 이름(`namespace`), 소속 클러스터(`cluster`)
    * `pod_count`, `container_count`: Pod 개수, 컨테이너 개수
    * `cpu_usage`, `cpu_requests`: CPU 사용량 합계, 요청량 합계 (코어)
    * `memory_usage_bytes`, `memory_requests_bytes`: 메모리 사용량 합계, 요청량 합계 (bytes)

    경고

    * `NO_DATA`: 네임스페이스에 Pod 가 없음
    """
    data, warnings = await fetch_namespace_summary(cluster, namespace)
    status = "success" if not warnings else "partial"
    return NamespaceDetailResponse(status=status, data=data, warnings=warnings)


# ---------------------------------------------------------------------------
# workloads 없이 바로 접근하는 짧은 경로
# ---------------------------------------------------------------------------


@router.get(
    "/clusters/{cluster}/pods/{namespace}/{pod}",
    summary="Pod 상세(짧은 경로)",
)
async def get_pod_alias(request: Request, cluster: str, namespace: str, pod: str):
    """`workloads` 를 뺀 짧은 경로로 Pod 한 개의 상세 조회

    입력 예시

    * `GET /api/v2/clusters/mgmt/pods/openstack/neutron-server-0`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `mgmt` (경로, 필수)
    * `namespace`: 네임스페이스 이름, 예 `openstack` (경로, 필수)
    * `pod`: Pod 이름, 예 `neutron-server-0` (경로, 필수)

    응답

    * 네임스페이스(`namespace`), Pod 이름(`pod`), 소속 클러스터(`cluster`), 고유 ID `uid`, 노드 이름(`node`)
    * 상태 `phase` (`Running` | `Pending` | `Succeeded` | `Failed` | `Unknown`)
    * `pod_ip`, `host_ip`, 생성 시각 `created_at`
    * 상위 리소스 종류와 이름 (`workload_type`, `workload_name`), 서비스 이름 `service_name`
    * 컨테이너 개수 `container_count`, 재시작 횟수 `restart_count`
    * `cpu_usage`, `cpu_requests`, `cpu_limits`: CPU 사용량, 요청량, 상한값 (코어)
    * `memory_usage_bytes`, `memory_requests_bytes`, `memory_limits_bytes`: 메모리 사용량, 요청량, 상한값 (bytes)
    * `_links.self`: 이번 요청 경로
    * `_links.canonical`: 클러스터와 `workloads` 를 포함한 정식 경로

    경고

    * `NO_DATA`: Pod 가 없음 (`data` 는 `null`)
    """
    data, warnings = await fetch_pod_detail(cluster, namespace, pod)
    status = "success" if data else "partial"
    resp = PodDetailResponse(status=status, data=data, warnings=warnings)
    return with_links(resp, {
        "self": f"/api/v2/clusters/{cluster}/pods/{namespace}/{pod}",
        "canonical": f"/api/v2/clusters/{cluster}/workloads/pods/{namespace}/{pod}",
    })


@router.get(
    "/clusters/{cluster}/containers/{container_id}",
    summary="컨테이너 상세(짧은 경로)",
)
async def get_container_alias(request: Request, cluster: str, container_id: str):
    """`workloads` 를 뺀 짧은 경로로 컨테이너 한 개의 상세 조회

    입력 예시

    * `GET /api/v2/clusters/mgmt/containers/3f2a9c1d`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `mgmt` (경로, 필수)
    * `container_id`: 컨테이너 ID, 앞부분만 넣어도 되고 `containerd://` 같은 접두어는 빼도 됨 (경로, 필수)

    응답

    * 컨테이너 이름(`container`), 컨테이너 ID(`container_id`), 이미지(`image`)
    * 네임스페이스(`namespace`), Pod 이름(`pod`), 소속 클러스터(`cluster`)
    * 상태 `status` (`running` | `waiting` | `terminated`), 재시작 횟수 `restart_count`
    * `cpu_usage`, `cpu_requests`, `cpu_limits`: CPU 사용량, 요청량, 상한값 (코어)
    * `memory_usage_bytes`, `memory_requests_bytes`, `memory_limits_bytes`: 메모리 사용량, 요청량, 상한값 (bytes)
    * `_links.self`: 이번 요청 경로
    * `_links.canonical`: 클러스터와 `workloads` 를 포함한 정식 경로

    경고

    * `NO_DATA`: 없는 컨테이너 (`data` 는 `null`)
    """
    data, warnings = await fetch_container_by_id(cluster, container_id)
    status = "success" if data else "partial"
    resp = ContainerDetailResponse(status=status, data=data, warnings=warnings)
    return with_links(resp, {
        "self": f"/api/v2/clusters/{cluster}/containers/{container_id}",
        "canonical": f"/api/v2/clusters/{cluster}/workloads/containers/{container_id}",
    })
