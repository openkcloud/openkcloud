"""Pod 하나가 어느 노드, VM, 물리서버에 올라가 있는지 추적하는 라우터

Prometheus 라벨을 단계별로 이어 붙여 Pod → 워크로드 → 가속기 → K8s 노드 → VM → 물리서버를 조회.
서버 전력은 /clusters/mgmt/nodes/{server}/power 로 따로 조회.
  - Pod 찾기       : kube_pod_info{pod} 의 cluster, namespace, node, created_by_*
  - 워크로드       : ReplicaSet/Job 이면 kube_replicaset_owner / kube_job_owner 로 한 단계 위
  - 노드 → VM      : kube_node_info.system_uuid = openstack_nova_server_status.uuid (없으면 이름 일치)
  - VM → 물리서버  : openstack_nova_server_status.hypervisor_hostname
주의: kube_node_info, kube_replicaset_owner 의 pod 라벨은 수집기(kube-state-metrics) 자신의 이름이라
Pod 이름으로 거르는 쿼리는 kube_pod_info 에서만 쓴다.
"""
from typing import Optional
from urllib.parse import urlencode

from fastapi import APIRouter, HTTPException, Query, Request

from app.schemas.resource_map import PodCandidate, PodTraceResponse, TraceHop
from app.services.cluster_discovery import DEFAULT_CLUSTER_NAME, cluster_discovery, cluster_label
from app.services.prometheus import esc, prometheus_client

router = APIRouter()

V2 = "/api/v2"

# 주인 종류 → (주인의 주인을 알려 주는 메트릭, 그 메트릭에서 주인 이름이 담긴 라벨)
_OWNER_METRICS = {
    "ReplicaSet": ("kube_replicaset_owner", "replicaset"),
    "Job": ("kube_job_owner", "job_name"),
}


def _sel(cluster: str, **labels: str) -> str:
    """cluster 라벨 + 추가 라벨 셀렉터. mgmt는 cluster="" (라벨 없음과 일치)."""
    parts = [f'cluster="{esc(cluster_label(cluster))}"']
    parts += [f'{k}="{esc(v)}"' for k, v in labels.items()]
    return ",".join(parts)


def _metric(results: list[dict]) -> dict:
    return results[0].get("metric", {}) if results else {}


async def _workload_hop(cluster: str, namespace: str, kind: Optional[str], name: Optional[str]) -> TraceHop:
    if not kind or not name:
        return TraceHop(layer="workload", status="none", cluster=cluster, via="kube_pod_info.created_by 없음(단독 Pod)")
    via = f"kube_pod_info.created_by({kind}/{name})"
    owner = _OWNER_METRICS.get(kind)
    if owner:
        metric, label = owner
        m = _metric(await prometheus_client.instant(f"{metric}{{{_sel(cluster, namespace=namespace, **{label: name})}}}"))
        if m.get("owner_kind") and m.get("owner_name") not in (None, "", "<none>"):
            kind, name, via = m["owner_kind"], m["owner_name"], f"{via} → {metric}"
    return TraceHop(layer="workload", status="ok", cluster=cluster, name=f"{kind}/{name}", via=via)


async def _accelerator_hops(cluster: str, namespace: str, pod: str) -> list[TraceHop]:
    results = await prometheus_client.instant(
        f"DCGM_FI_DEV_GPU_UTIL{{{_sel(cluster, exported_namespace=namespace, exported_pod=pod)}}}"
    )
    if results:
        return [
            TraceHop(
                layer="accelerator", status="ok", name=m.get("modelName"), id=m.get("UUID") or m.get("gpu"),
                via="DCGM_FI_DEV_GPU_UTIL{exported_namespace, exported_pod}",
            )
            for m in (r.get("metric", {}) for r in results)
        ]
    info = await cluster_discovery.get_cluster(cluster)
    if info and info.vendor in ("furiosa", "rebellions"):
        # ponytail: NPU exporter가 Pod 라벨을 주지 않아 카드 매핑 불가. exporter가 라벨을 주면 여기서 조회
        return [TraceHop(layer="accelerator", status="unavailable", via=f"{info.vendor} exporter에 Pod 라벨 없음")]
    return [TraceHop(layer="accelerator", status="none", via="가속기 할당 없음")]


async def _node_hop(cluster: str, node: Optional[str]) -> tuple[TraceHop, Optional[str]]:
    """K8s 노드 단계와 그 노드의 system_uuid."""
    if not node:
        return TraceHop(layer="k8s_node", status="unavailable", cluster=cluster, via="kube_pod_info.node 없음(미배치 Pod)"), None
    m = _metric(await prometheus_client.instant(f"kube_node_info{{{_sel(cluster, node=node)}}}"))
    ready = await prometheus_client.instant(
        f'kube_node_status_condition{{{_sel(cluster, node=node)},condition="Ready",status="true"}} == 1'
    )
    hop = TraceHop(
        layer="k8s_node", status="ok" if m else "unavailable", cluster=cluster, name=node, id=m.get("system_uuid"),
        state=("Ready" if ready else "NotReady") if m else None,
        via="kube_pod_info.node", href=f"{V2}/clusters/{cluster}/nodes/{node}",
    )
    return hop, m.get("system_uuid")


def _match_vm(servers: list[dict], node: Optional[str], system_uuid: Optional[str]) -> tuple[Optional[dict], str]:
    """nova VM 중 노드와 같은 기계 찾기: uuid 우선, 없으면 이름."""
    uuid = (system_uuid or "").lower()
    for m in servers:
        if uuid and (m.get("uuid") or "").lower() == uuid:
            return m, "kube_node_info.system_uuid = openstack_nova_server_status.uuid"
    for m in servers:
        if node and m.get("name") == node:
            return m, "K8s 노드 이름 = openstack_nova_server_status.name"
    return None, ""


async def trace_pod(pod: str, cluster: Optional[str] = None, namespace: Optional[str] = None) -> PodTraceResponse:
    sel = [f'pod="{esc(pod)}"']
    if cluster:
        sel.append(f'cluster="{esc(cluster_label(cluster))}"')
    if namespace:
        sel.append(f'namespace="{esc(namespace)}"')
    found = await prometheus_client.instant(f"kube_pod_info{{{','.join(sel)}}}")
    by_place = {
        (r["metric"].get("cluster") or DEFAULT_CLUSTER_NAME, r["metric"].get("namespace", "")): r["metric"]
        for r in found
    }
    if not by_place:
        # instant()는 오류 시에도 [] 를 주므로, Prometheus 장애를 "Pod 없음"으로 오판하지 않게 확인
        if not await prometheus_client.instant("vector(1)"):
            raise HTTPException(status_code=503, detail="Prometheus 조회 실패")
        raise HTTPException(status_code=404, detail=f"Pod를 찾을 수 없음: {pod}")
    if len(by_place) > 1:
        return PodTraceResponse(
            status="ambiguous", pod=pod, warnings=["MULTIPLE_PODS_MATCHED"],
            candidates=[
                PodCandidate(cluster=c, namespace=ns, href=f"{V2}/resource-map/pods/{pod}?{urlencode({'cluster': c, 'namespace': ns})}")
                for c, ns in sorted(by_place)
            ],
        )

    (cluster, namespace), m = next(iter(by_place.items()))
    node = m.get("node")
    phase = _metric(await prometheus_client.instant(
        f"kube_pod_status_phase{{{_sel(cluster, namespace=namespace, pod=pod)}}} == 1"
    )).get("phase")

    path = [TraceHop(
        layer="pod", status="ok", cluster=cluster, name=pod, id=m.get("uid"), state=phase,
        via="kube_pod_info{pod}", href=f"{V2}/workloads/pods/{cluster}/{namespace}/{pod}",
    )]
    path.append(await _workload_hop(cluster, namespace, m.get("created_by_kind"), m.get("created_by_name")))
    path += await _accelerator_hops(cluster, namespace, pod)
    path += await trace_node(cluster, node)

    warnings = [f"{h.layer.upper()}_NOT_AVAILABLE" for h in path if h.status == "unavailable"]
    return PodTraceResponse(
        status="partial" if warnings else "success",
        pod=pod, cluster=cluster, namespace=namespace, path=path, warnings=warnings,
    )


async def trace_node(cluster: str, node: Optional[str]) -> list[TraceHop]:
    """K8s 노드에서 VM, 물리서버까지의 단계. Pod 추적의 뒷부분이고 장애 영향 범위 조회에도 쓴다."""
    node_hop, system_uuid = await _node_hop(cluster, node)
    path = [node_hop]

    servers = [r.get("metric", {}) for r in await prometheus_client.instant("openstack_nova_server_status")]
    vm, via = _match_vm(servers, node, system_uuid)
    host: Optional[str] = None
    if vm:
        host = vm.get("hypervisor_hostname")
        path.append(TraceHop(
            layer="vm", status="ok", name=vm.get("name"), id=vm.get("uuid"), state=vm.get("status"),
            project=vm.get("tenant_id"), via=via, href=f"{V2}/openstack/vms/{vm.get('uuid')}",
        ))
        path.append(TraceHop(
            layer="physical_server", status="ok" if host else "unavailable", name=host,
            via="openstack_nova_server_status.hypervisor_hostname",
            href=f"{V2}/openstack/hypervisors/{host}" if host else None,
        ))
    elif cluster == DEFAULT_CLUSTER_NAME and node:
        # 관리 클러스터 노드는 물리서버에 직접 설치되어 노드 이름 = 서버 이름
        host = node
        path.append(TraceHop(layer="vm", status="skipped", via="nova VM 목록에 없음: 물리서버에 직접 설치된 노드"))
        path.append(TraceHop(
            layer="physical_server", status="ok", name=host, via="관리 클러스터 노드 이름 = 서버 이름",
            href=f"{V2}/clusters/{DEFAULT_CLUSTER_NAME}/nodes/{host}",
        ))
    else:
        path.append(TraceHop(layer="vm", status="unavailable", via="노드와 일치하는 nova VM 없음"))
        path.append(TraceHop(layer="physical_server", status="unavailable"))
    return path


@router.get("/resource-map/pods/{pod}", summary="Pod 자원 추적", response_model=PodTraceResponse)
async def get_pod_trace(
    request: Request,
    pod: str,
    cluster: Optional[str] = Query(None, description="클러스터 이름, 예 `mgmt` (같은 이름 Pod 가 여러 클러스터에 있을 때 지정)"),
    namespace: Optional[str] = Query(None, description="네임스페이스, 예 `openstack` (같은 이름 Pod 가 여러 네임스페이스에 있을 때 지정)"),
):
    """Pod 하나가 올라간 자원을 물리 서버까지 순서대로 조회

    입력 예시

    * `GET /api/v2/resource-map/pods/nova-api-0`
    * `GET /api/v2/resource-map/pods/nova-api-0?cluster=mgmt&namespace=openstack`

    입력 옵션

    * `pod`: Pod 이름, 예 `nova-api-0` (경로, 필수)
    * `cluster`: 클러스터 이름, 예 `mgmt` (선택, 같은 이름 Pod 가 여러 클러스터에 있을 때 지정)
    * `namespace`: 네임스페이스, 예 `openstack` (선택, 같은 이름 Pod 가 여러 네임스페이스에 있을 때 지정)

    응답

    * `pod`, `cluster`, `namespace`: 요청한 Pod 이름, 확인된 클러스터, 네임스페이스
    * `path`: 단계 목록, 순서는 `pod` | `workload` | `accelerator` | `k8s_node` | `vm` | `physical_server`
    * 단계마다 이름, ID, 상태, 프로젝트, 소속 클러스터
    * 단계마다 `status` (`ok` | `none` | `skipped` | `unavailable`), 연결 근거(`via`), 상세 API 경로(`href`)
    * `candidates`: 같은 이름 Pod 가 여러 개일 때 후보 목록, 항목마다 클러스터, 네임스페이스, 다시 추적할 API 경로

    상태

    * `ambiguous`: 같은 이름 Pod 가 여러 개, `candidates` 로 다시 요청
    * `partial`: 확인하지 못한 단계가 있음
    * `success`: 모든 단계 확인

    경고

    * `MULTIPLE_PODS_MATCHED`: 같은 이름 Pod 가 여러 개
    * `<단계>_NOT_AVAILABLE`: 확인하지 못한 단계, 예 `VM_NOT_AVAILABLE`

    참고

    * `accelerator` 단계는 NVIDIA 카드만 확인 가능

    오류

    * 404: 없는 Pod
    * 503: Prometheus 조회 실패
    """
    return await trace_pod(pod, cluster, namespace)
