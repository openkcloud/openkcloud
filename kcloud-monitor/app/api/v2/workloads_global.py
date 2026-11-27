"""클러스터를 고르지 않고 워크로드 전체를 보는 라우터

어느 클러스터에 있는지 몰라도 Pod와 서비스를 바로 찾을 수 있는 경로.
모든 응답에 `_links.canonical` 로 클러스터를 포함한 정식 경로를 함께 안내.
"""
import asyncio
from types import SimpleNamespace
from typing import Optional

from fastapi import APIRouter, Depends, Query, Request

from app.api.v2.deps import WorkloadFilterParams, with_links
from app.api.v2.workloads import (
    fetch_pod_accelerators,
    fetch_pod_containers,
    fetch_pod_detail,
    fetch_pod_power,
    fetch_pods,
    fetch_pods_summary,
)
from app.schemas.workloads import (
    ContainerListResponse,
    PodAcceleratorResponse,
    PodDetailResponse,
    PodListResponse,
    PodPowerResponse,
    PodSummaryData,
    PodSummaryResponse,
    ServiceDetailData,
    ServiceDetailResponse,
    ServiceItem,
    ServiceListResponse,
    ServicePowerData,
    ServicePowerResponse,
    ServiceSummaryData,
)
from app.services.cluster_discovery import cluster_discovery

router = APIRouter()


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _pod_links(cluster: str, namespace: str, pod: str, suffix: str = "") -> dict:
    return {
        "self": f"/api/v2/workloads/pods/{cluster}/{namespace}/{pod}{suffix}",
        "canonical": f"/api/v2/clusters/{cluster}/workloads/pods/{namespace}/{pod}{suffix}",
    }


def _service_links(cluster: str, namespace: str, service_name: str, suffix: str = "") -> dict:
    return {
        "self": f"/api/v2/workloads/services/{cluster}/{namespace}/{service_name}{suffix}",
        "canonical": (
            f"/api/v2/clusters/{cluster}/workloads/pods"
            f"?namespace={namespace}&service_name={service_name}"
        ),
    }


def _all_params(params: Optional[WorkloadFilterParams] = None, **overrides) -> SimpleNamespace:
    """fetch_pods 에 넘길 페이지 없는 파라미터. 필터와 정렬은 params 를 따르고 overrides 가 우선."""
    filters = dict(
        sort_by=None, sort_order="asc", search=None, namespace=None,
        service_name=None, workload_type=None, status=None,
    )
    if params:
        filters = {k: getattr(params, k) for k in filters}
    filters.update(overrides)
    return SimpleNamespace(limit=100000, offset=0, **filters)


async def _resolve_clusters(cluster_filter: Optional[str]) -> list[str]:
    clusters = await cluster_discovery.get_clusters()
    if cluster_filter and cluster_filter in clusters:
        return [cluster_filter]
    return list(clusters.keys())


async def _all_pods(cluster_filter: Optional[str], params: WorkloadFilterParams) -> list:
    """대상 클러스터 Pod 를 페이지 없이 모아 한 목록으로."""
    names = await _resolve_clusters(cluster_filter)
    inner = _all_params(params)
    results = await asyncio.gather(*(fetch_pods(n, inner) for n in names))
    return [p for pods, _, _ in results for p in pods]


# ---------------------------------------------------------------------------
# Pods (전역)
# ---------------------------------------------------------------------------


@router.get("/workloads/pods", summary="전체 클러스터 Pod 목록", response_model=PodListResponse)
async def list_pods_global(
    request: Request,
    params: WorkloadFilterParams = Depends(),
    cluster: Optional[str] = Query(None, description="클러스터 이름 필터 (예: mgmt, 없으면 전체)"),
):
    """모든 클러스터의 Pod 를 한 목록으로 조회

    입력 예시

    * `GET /api/v2/workloads/pods`
    * `GET /api/v2/workloads/pods?cluster=mgmt&namespace=openstack&status=Running&limit=20`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `mgmt` (선택, 없거나 없는 이름이면 전체 클러스터)
    * `limit`: 한 번에 받을 개수 `1` ~ `1000` (기본 `100`)
    * `offset`: 건너뛸 개수, 페이지 이동용 (기본 `0`)
    * `sort_by`: 정렬 기준이 될 응답 항목 이름, 예 `pod` (선택, 클러스터 안에서만 적용)
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

    * `NO_DATA`: Pod 가 하나도 없음
    """
    all_pods = await _all_pods(cluster, params)
    total = len(all_pods)
    page = all_pods[params.offset : params.offset + params.limit]
    warnings: list[str] = [] if all_pods else ["NO_DATA"]
    return PodListResponse(
        status="success" if all_pods else "partial",
        pods=page, total=total, warnings=warnings,
    )


@router.get("/workloads/pods/summary", summary="전체 클러스터 Pod 집계", response_model=PodSummaryResponse)
async def get_pods_summary_global(request: Request):
    """모든 클러스터의 Pod 상태별 개수 합계 조회

    입력 예시

    * `GET /api/v2/workloads/pods/summary`

    응답

    * `total_count`: 전체 Pod 개수
    * `running_count`, `pending_count`: `Running`, `Pending` 상태 개수
    * `succeeded_count`, `failed_count`, `unknown_count`: 정상 완료, 실패, 상태 불명 개수
    * `namespace_distribution`: 네임스페이스별 Pod 개수

    경고

    * `NO_DATA`: Pod 가 하나도 없음
    """
    names = await _resolve_clusters(None)
    results = await asyncio.gather(*(fetch_pods_summary(n) for n in names))

    merged = PodSummaryData()
    for data, _ in results:
        merged.total_count += data.total_count
        merged.running_count += data.running_count
        merged.pending_count += data.pending_count
        merged.succeeded_count += data.succeeded_count
        merged.failed_count += data.failed_count
        merged.unknown_count += data.unknown_count
        for ns, cnt in data.namespace_distribution.items():
            merged.namespace_distribution[ns] = (
                merged.namespace_distribution.get(ns, 0) + cnt
            )

    warnings: list[str] = [] if merged.total_count > 0 else ["NO_DATA"]
    return PodSummaryResponse(
        status="success" if merged.total_count else "partial",
        data=merged, warnings=warnings,
    )


@router.get("/workloads/pods/{cluster}/{namespace}/{pod}", summary="Pod 상세(클러스터 지정)")
async def get_pod_global(request: Request, cluster: str, namespace: str, pod: str):
    """클러스터 이름을 경로에 넣어 Pod 한 개의 상세 조회

    입력 예시

    * `GET /api/v2/workloads/pods/mgmt/openstack/neutron-server-0`

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
    * `_links.canonical`: 클러스터를 포함한 정식 경로

    경고

    * `NO_DATA`: Pod 가 없음 (`data` 는 `null`)
    """
    data, warnings = await fetch_pod_detail(cluster, namespace, pod)
    status = "success" if data else "partial"
    resp = PodDetailResponse(status=status, data=data, warnings=warnings)
    return with_links(resp, _pod_links(cluster, namespace, pod))


@router.get(
    "/workloads/pods/{cluster}/{namespace}/{pod}/power", summary="Pod 추정 전력(클러스터 지정)",
)
async def get_pod_power_global(
    request: Request, cluster: str, namespace: str, pod: str,
):
    """클러스터 이름을 경로에 넣어 Pod 한 개의 추정 전력 조회

    입력 예시

    * `GET /api/v2/workloads/pods/mgmt/openstack/neutron-server-0/power`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `mgmt` (경로, 필수)
    * `namespace`: 네임스페이스 이름, 예 `openstack` (경로, 필수)
    * `pod`: Pod 이름, 예 `neutron-server-0` (경로, 필수)

    응답

    * `watts`: Pod 추정 전력 (W, 최근 5분 평균)
    * `source`: 산출 근거 (`kepler`)
    * `_links.self`: 이번 요청 경로
    * `_links.canonical`: 클러스터를 포함한 정식 경로

    경고

    * `NO_POWER_DATA`: 전력값이 없음 (`data` 는 `null`)

    참고

    * Kepler 가 컨테이너별로 추정한 전력의 합
    """
    data, warnings = await fetch_pod_power(cluster, namespace, pod)
    status = "success" if data else "partial"
    resp = PodPowerResponse(status=status, data=data, warnings=warnings)
    return with_links(resp, _pod_links(cluster, namespace, pod, "/power"))


@router.get(
    "/workloads/pods/{cluster}/{namespace}/{pod}/containers",
    summary="Pod 컨테이너 목록(클러스터 지정)",
)
async def list_pod_containers_global(
    request: Request, cluster: str, namespace: str, pod: str,
):
    """클러스터 이름을 경로에 넣어 Pod 한 개 안의 컨테이너 목록 조회

    입력 예시

    * `GET /api/v2/workloads/pods/mgmt/openstack/neutron-server-0/containers`

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
    * `_links.self`: 이번 요청 경로
    * `_links.canonical`: 클러스터를 포함한 정식 경로

    경고

    * `NO_DATA`: Pod 정보가 없음 (빈 목록)
    """
    containers, warnings = await fetch_pod_containers(cluster, namespace, pod)
    status = "success" if not warnings else "partial"
    resp = ContainerListResponse(
        status=status, containers=containers, total=len(containers), warnings=warnings,
    )
    return with_links(resp, _pod_links(cluster, namespace, pod, "/containers"))


@router.get(
    "/workloads/pods/{cluster}/{namespace}/{pod}/accelerators",
    summary="Pod 할당 가속기(클러스터 지정)",
)
async def get_pod_accelerators_global(
    request: Request, cluster: str, namespace: str, pod: str,
):
    """클러스터 이름을 경로에 넣어 Pod 한 개에 배정된 가속기 조회

    입력 예시

    * `GET /api/v2/workloads/pods/furiosa/kube-system/neutron-server-0/accelerators`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `mgmt` (경로, 필수)
    * `namespace`: 네임스페이스 이름, 예 `openstack` (경로, 필수)
    * `pod`: Pod 이름, 예 `neutron-server-0` (경로, 필수)

    응답

    * 가속기 ID(`acc_id`), 벤더(`vendor`), 모델명(`model_name`)
    * `_links.self`: 이번 요청 경로
    * `_links.canonical`: 클러스터를 포함한 정식 경로

    경고

    * `POD_ACCELERATOR_DATA_NOT_AVAILABLE`: 배정된 가속기가 없거나 조회를 지원하지 않는 클러스터 (빈 목록)

    참고

    * NVIDIA 클러스터만 값이 나옴
    """
    items, warnings = await fetch_pod_accelerators(cluster, namespace, pod)
    status = "success" if not warnings else "partial"
    resp = PodAcceleratorResponse(status=status, data=items, warnings=warnings)
    return with_links(resp, _pod_links(cluster, namespace, pod, "/accelerators"))


# ---------------------------------------------------------------------------
# Services (전역)
# ---------------------------------------------------------------------------


def _group_by_service(pods: list) -> dict[tuple[str, str, str], list]:
    groups: dict[tuple[str, str, str], list] = {}
    for p in pods:
        svc = p.service_name or p.pod
        groups.setdefault((p.cluster, p.namespace, svc), []).append(p)
    return groups


def _build_service_item(key: tuple[str, str, str], pods: list) -> ServiceItem:
    cluster, namespace, svc = key
    running = sum(1 for p in pods if p.phase == "Running")
    cpu_vals = [p.cpu_usage for p in pods if p.cpu_usage is not None]
    mem_vals = [p.memory_usage_bytes for p in pods if p.memory_usage_bytes is not None]
    return ServiceItem(
        service_name=svc, namespace=namespace, cluster=cluster,
        pod_count=len(pods), running_pod_count=running,
        cpu_usage=sum(cpu_vals) if cpu_vals else None,
        memory_usage_bytes=sum(mem_vals) if mem_vals else None,
    )


@router.get("/workloads/services", summary="전체 클러스터 서비스 목록", response_model=ServiceListResponse)
async def list_services_global(
    request: Request,
    params: WorkloadFilterParams = Depends(),
    cluster: Optional[str] = Query(None, description="클러스터 이름 필터 (예: mgmt, 없으면 전체)"),
):
    """모든 클러스터의 서비스를 한 목록으로 조회

    입력 예시

    * `GET /api/v2/workloads/services`
    * `GET /api/v2/workloads/services?cluster=mgmt&namespace=openstack&limit=20`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `mgmt` (선택, 없거나 없는 이름이면 전체 클러스터)
    * `limit`: 한 번에 받을 개수 `1` ~ `1000` (기본 `100`)
    * `offset`: 건너뛸 개수, 페이지 이동용 (기본 `0`)
    * `namespace`: 네임스페이스 이름, 예 `openstack` (선택)
    * `status`: Pod 상태, 예 `Running` (선택, 대소문자 무시)
    * `workload_type`: 상위 리소스 종류, 예 `StatefulSet` (선택, 대소문자 무시)
    * `service_name`: 서비스 이름 일부, 예 `neutron` (선택)
    * `search`: 서비스 이름에 들어 있는 글자 (선택, 대소문자 무시)

    응답

    * 서비스 이름(`service_name`), 네임스페이스(`namespace`), 소속 클러스터(`cluster`)
    * `pod_count`, `running_pod_count`: 소속 Pod 개수, `Running` 상태 개수
    * `cpu_usage`: 소속 Pod CPU 사용량 합계 (코어)
    * `memory_usage_bytes`: 소속 Pod 메모리 사용량 합계 (bytes)
    * `total`: 서비스 이름 검색을 적용한 뒤 서비스 개수
    * `summary`: 전체 서비스 수 `total_services`, 클러스터별 서비스 수 `cluster_distribution` (페이지와 검색 적용 전 기준)

    경고

    * `NO_DATA`: 서비스가 없음

    참고

    * 클러스터, 네임스페이스, 서비스 이름이 같은 Pod 를 하나로 묶음
    * 서비스 이름을 알 수 없는 Pod 는 Pod 이름을 서비스 이름으로 사용
    * `namespace`, `status`, `workload_type`, `service_name` 은 묶기 전 Pod 에 적용
    """
    groups = _group_by_service(await _all_pods(cluster, params))
    items = [_build_service_item(k, v) for k, v in groups.items()]

    cluster_dist: dict[str, int] = {}
    for item in items:
        cluster_dist[item.cluster] = cluster_dist.get(item.cluster, 0) + 1
    summary = ServiceSummaryData(total_services=len(items), cluster_distribution=cluster_dist)

    if params.search:
        q = params.search.lower()
        items = [i for i in items if q in i.service_name.lower()]

    total = len(items)
    page = items[params.offset : params.offset + params.limit]
    warnings: list[str] = [] if items else ["NO_DATA"]
    return ServiceListResponse(
        status="success" if items else "partial",
        services=page, total=total, summary=summary, warnings=warnings,
    )


@router.get(
    "/workloads/services/{cluster}/{namespace}/{service_name}",
    summary="서비스 상세",
)
async def get_service_global(
    request: Request, cluster: str, namespace: str, service_name: str,
):
    """서비스 한 개의 자원 사용 상세 조회

    입력 예시

    * `GET /api/v2/workloads/services/mgmt/openstack/neutron-server`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `mgmt` (경로, 필수)
    * `namespace`: 네임스페이스 이름, 예 `openstack` (경로, 필수)
    * `service_name`: 서비스 이름, 예 `neutron-server` (경로, 필수)

    응답

    * 서비스 이름(`service_name`), 네임스페이스(`namespace`), 소속 클러스터(`cluster`)
    * `pod_count`, `running_pod_count`: 소속 Pod 개수, `Running` 상태 개수
    * `cpu_usage`: 소속 Pod CPU 사용량 합계 (코어)
    * `memory_usage_bytes`: 소속 Pod 메모리 사용량 합계 (bytes)
    * `cpu_requests`, `memory_requests_bytes`: 이 경로에서는 값이 없음 (`null`)
    * `_links.self`: 이번 요청 경로
    * `_links.canonical`: 이 서비스의 Pod 만 거르는 클러스터 Pod 목록 경로

    경고

    * `NO_DATA`: 소속 Pod 가 없음 (`data` 는 `null`)

    참고

    * 서비스 이름이 일부라도 맞는 Pod 를 소속 Pod 로 판단
    """
    inner = _all_params(namespace=namespace, service_name=service_name)
    pods, _, _ = await fetch_pods(cluster, inner)

    links = _service_links(cluster, namespace, service_name)

    if not pods:
        return with_links(ServiceDetailResponse(status="partial", data=None, warnings=["NO_DATA"]), links)

    item = _build_service_item((cluster, namespace, service_name), pods)
    data = ServiceDetailData(**item.model_dump())
    return with_links(ServiceDetailResponse(status="success", data=data, warnings=[]), links)


@router.get(
    "/workloads/services/{cluster}/{namespace}/{service_name}/pods",
    summary="서비스 소속 Pod 목록",
)
async def list_service_pods_global(
    request: Request, cluster: str, namespace: str, service_name: str,
):
    """서비스 한 개에 속한 Pod 목록 조회

    입력 예시

    * `GET /api/v2/workloads/services/mgmt/openstack/neutron-server/pods`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `mgmt` (경로, 필수)
    * `namespace`: 네임스페이스 이름, 예 `openstack` (경로, 필수)
    * `service_name`: 서비스 이름, 예 `neutron-server` (경로, 필수)

    응답

    * 네임스페이스(`namespace`), Pod 이름(`pod`), 소속 클러스터(`cluster`), 노드 이름(`node`)
    * 상태 `phase` (`Running` | `Pending` | `Succeeded` | `Failed` | `Unknown`)
    * `pod_ip`, `host_ip`, 생성 시각 `created_at`
    * 상위 리소스 종류와 이름 (`workload_type`, `workload_name`), 서비스 이름 `service_name`
    * 컨테이너 개수 `container_count`, 재시작 횟수 `restart_count`
    * `cpu_usage`: CPU 사용량 (코어, 최근 5분 평균)
    * `memory_usage_bytes`: 메모리 사용량 (bytes)
    * `total`: 이 서비스의 Pod 개수
    * `_links.self`: 이번 요청 경로
    * `_links.canonical`: 이 서비스의 Pod 만 거르는 클러스터 Pod 목록 경로

    경고

    * `NO_DATA`: Pod 정보가 수집되지 않음

    참고

    * 서비스 이름이 일부라도 맞는 Pod 를 소속 Pod 로 판단
    """
    inner = _all_params(namespace=namespace, service_name=service_name)
    pods, total, warnings = await fetch_pods(cluster, inner)
    status = "success" if pods else "partial"
    resp = PodListResponse(status=status, pods=pods, total=total, warnings=warnings)
    return with_links(resp, _service_links(cluster, namespace, service_name, "/pods"))


@router.get(
    "/workloads/services/{cluster}/{namespace}/{service_name}/power",
    summary="서비스 추정 전력 합계",
)
async def get_service_power_global(
    request: Request, cluster: str, namespace: str, service_name: str,
):
    """서비스 한 개의 추정 전력 합계 조회

    입력 예시

    * `GET /api/v2/workloads/services/mgmt/openstack/neutron-server/power`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `mgmt` (경로, 필수)
    * `namespace`: 네임스페이스 이름, 예 `openstack` (경로, 필수)
    * `service_name`: 서비스 이름, 예 `neutron-server` (경로, 필수)

    응답

    * `watts`: 소속 Pod 추정 전력 합계 (W)
    * `source`: 산출 근거 (`kepler`)
    * `_links.self`: 이번 요청 경로
    * `_links.canonical`: 이 서비스의 Pod 만 거르는 클러스터 Pod 목록 경로

    경고

    * `NO_POWER_DATA`: 어느 Pod 도 전력값이 없음 (`data` 는 `null`)

    참고

    * Kepler 가 컨테이너별로 추정한 전력을 소속 Pod 전체에 대해 더한 값
    """
    inner = _all_params(namespace=namespace, service_name=service_name)
    pods, _, _ = await fetch_pods(cluster, inner)
    links = _service_links(cluster, namespace, service_name, "/power")

    # 소속 Pod 가 없으면 반복 없이 has_power=False 로 끝나 NO_POWER_DATA
    total_watts = 0.0
    has_power = False
    for p in pods:
        power_data, _ = await fetch_pod_power(cluster, p.namespace, p.pod)
        if power_data and power_data.watts is not None:
            total_watts += power_data.watts
            has_power = True

    if not has_power:
        resp = ServicePowerResponse(status="partial", data=None, warnings=["NO_POWER_DATA"])
    else:
        resp = ServicePowerResponse(
            status="success",
            data=ServicePowerData(watts=total_watts, source="kepler"),
            warnings=[],
        )
    return with_links(resp, links)
