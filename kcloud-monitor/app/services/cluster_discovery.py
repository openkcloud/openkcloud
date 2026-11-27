"""
KCloud Monitor v2 — 클러스터 자동 발견.

Prometheus에서 cluster 라벨 값을 조회하고,
벤더별 탐지 메트릭(DCGM_*, furiosa_*, RBLN_*)을 프로브하여
클러스터 유형(GPU/NPU/일반)을 자동 분류한다.
"""
import asyncio
import logging
import time
from dataclasses import dataclass
from typing import Optional

from app.config import npu_util
from app.services.openstack import OpenStackError, openstack_client
from app.services.prometheus import prometheus_client

logger = logging.getLogger(__name__)

DEFAULT_CLUSTER_NAME = "mgmt"

VENDOR_PROFILES: dict[str, dict[str, Optional[str]]] = {
    "nvidia": {
        "accelerator_type": "GPU",
        "detect_metric": "DCGM_FI_DEV_GPU_UTIL",
        "utilization_query": 'DCGM_FI_DEV_GPU_UTIL{{cluster="{cluster}"}}',
        "temperature_query": 'DCGM_FI_DEV_GPU_TEMP{{cluster="{cluster}"}}',
        "power_query": 'DCGM_FI_DEV_POWER_USAGE{{cluster="{cluster}"}}',
    },
    "furiosa": {
        "accelerator_type": "NPU",
        # 공식 furiosa-metrics-exporter (2026-10-15 복귀). 사용률은 코어별(8개)이라 카드 단위로 평균
        "detect_metric": "furiosa_npu_core_utilization",
        "utilization_query": npu_util('avg by (cluster,device,uuid,instance,hostname) (furiosa_npu_core_utilization{{cluster="{cluster}"}})', "furiosa"), # 단위: %
        "temperature_query": 'furiosa_npu_hw_temperature{{label="peak",cluster="{cluster}"}}', # 단위: °C
        "power_query": 'furiosa_npu_hw_power{{cluster="{cluster}"}}', # 단위: W
    },
    "rebellions": {
        "accelerator_type": "NPU",
        "detect_metric": "RBLN_DEVICE_STATUS:UTILIZATION", # 단위: %
        "utilization_query": npu_util('RBLN_DEVICE_STATUS:UTILIZATION{{cluster="{cluster}"}}', "rebellions"),
        # colon(:) 메트릭은 집계함수(_avg 등)로 감싸일 때 파서가 원형을 거부할 수 있어
        # __name__ 매처 형식으로 지정한다 (power.py 참고).
        # temp·power ×1000: exporter가 실측의 1/1000로 표출 (rbln-stat 대조 확정, 2026-08-24)
        "temperature_query": '({{__name__="RBLN_DEVICE_STATUS:TEMPERATURE",cluster="{cluster}"}}) * 1000', # 단위: °C (보정 후)
        "power_query": '(RBLN_DEVICE_STATUS:CARD_POWER{{cluster="{cluster}"}}) * 1000', # 단위: W (보정 후)
    },
}


@dataclass
class ClusterInfo:
    name: str
    label_value: str
    vendor: Optional[str] = None
    accelerator_type: Optional[str] = None
    utilization_query: Optional[str] = None
    temperature_query: Optional[str] = None
    power_query: Optional[str] = None
    # 관리/서비스 클러스터 구분
    type: str = "service"  # "management" | "service"
    parent_cluster: Optional[str] = None  # 서비스 클러스터의 부모 관리 클러스터명(mgmt). 관리 클러스터는 None
    has_openstack: bool = False  # 관리 클러스터만 True (OpenStack Keystone/Nova 연동 대상)
    openstack_project: Optional[str] = None  # 서비스 클러스터의 OpenStack 프로젝트명(과금 단위). Magnum 조회로 채움
    service_clusters: Optional[list[str]] = None  # 관리 클러스터가 거느린 서비스 클러스터명 목록 (서비스 클러스터는 None)
    # 서비스 클러스터가 "진짜 K8s 클러스터"(Magnum)인지 "단독 가속기 VM"인지 구분.
    # 판별: Magnum 명단에 있거나 이름이 "k8s-"로 시작 → True. l40s/rebellions 같은 단독 VM은 False.
    is_kubernetes: bool = False


def cluster_label(cluster: str) -> str:
    """API cluster name → PromQL cluster= 라벨 값."""
    return "" if cluster == DEFAULT_CLUSTER_NAME else cluster


class ClusterDiscovery:
    def __init__(self, cache_ttl: float = 300.0) -> None:
        self._cache: dict[str, ClusterInfo] = {}
        self._cache_ts: float = 0.0
        self._cache_ttl = cache_ttl
        self._lock: Optional[asyncio.Lock] = None

    def _get_lock(self) -> asyncio.Lock:
        if self._lock is None:
            self._lock = asyncio.Lock()
        return self._lock

    def _is_stale(self) -> bool:
        return time.monotonic() - self._cache_ts > self._cache_ttl

    async def get_clusters(self) -> dict[str, ClusterInfo]:
        if not self._is_stale() and self._cache:
            return self._cache
        lock = self._get_lock()
        async with lock:
            if not self._is_stale() and self._cache:
                return self._cache
            self._cache = await self._discover()
            self._cache_ts = time.monotonic()
            return self._cache

    async def get_cluster(self, name: str) -> Optional[ClusterInfo]:
        clusters = await self.get_clusters()
        return clusters.get(name)

    async def _discover(self) -> dict[str, ClusterInfo]:
        results = await prometheus_client.instant("count by (cluster) (up)")

        clusters: dict[str, ClusterInfo] = {}
        for item in results:
            label_val = item.get("metric", {}).get("cluster", "")
            api_name = label_val if label_val else DEFAULT_CLUSTER_NAME
            is_management = api_name == DEFAULT_CLUSTER_NAME
            clusters[api_name] = ClusterInfo(
                name=api_name,
                label_value=label_val,
                type="management" if is_management else "service",
                parent_cluster=None if is_management else DEFAULT_CLUSTER_NAME,
                has_openstack=is_management,
                openstack_project=None,
            )

        prev = self._cache
        for info in clusters.values():
            if info.name == DEFAULT_CLUSTER_NAME:
                continue
            await self._detect_vendor(info)
            # 가속기 exporter 가 잠깐 멈추면 탐지 메트릭이 비어 벤더가 사라지고, 캐시(5분) 동안 알람 평가에서 빠져
            # 수집 중단 알람이 해제되지 않았다 (2026-10-22). 클러스터가 살아 있는 동안은 직전 벤더를 이어 쓴다
            old = prev.get(info.name)
            if not info.vendor and old and old.vendor:
                for k in ("vendor", "accelerator_type", "utilization_query", "temperature_query", "power_query"):
                    setattr(info, k, getattr(old, k))
            # 기본 휴리스틱: 이름이 "k8s-"로 시작하면 진짜 K8s 클러스터로 간주(터널 없이도 동작).
            # Magnum 조회가 되면 아래 _enrich에서 권위 있게 덮어쓴다.
            info.is_kubernetes = info.name.startswith("k8s-")

        # 관리 클러스터(mgmt)에 자식 서비스 클러스터 목록 채우기 (Prometheus 발견 결과만으로 계산)
        service_names = sorted(
            info.name for info in clusters.values() if info.type == "service"
        )
        for info in clusters.values():
            if info.type == "management":
                info.service_clusters = service_names

        # 서비스 클러스터 ↔ OpenStack 프로젝트 매핑 보강 (Magnum). 실패해도 발견은 유지.
        await self._enrich_openstack_projects(clusters)

        logger.info(
            "Cluster discovery: %d clusters — %s",
            len(clusters),
            ", ".join(sorted(clusters.keys())),
        )
        return clusters

    async def _detect_vendor(self, info: ClusterInfo) -> None:
        for vendor_name, profile in VENDOR_PROFILES.items():
            detect = profile["detect_metric"]
            probe = await prometheus_client.instant(
                f'count({detect}{{cluster="{info.label_value}"}})'
            )
            if probe:
                info.vendor = vendor_name
                info.accelerator_type = profile["accelerator_type"]
                tpl = {"cluster": info.label_value}
                info.utilization_query = profile["utilization_query"].format(**tpl)
                info.temperature_query = profile["temperature_query"].format(**tpl)
                info.power_query = profile["power_query"].format(**tpl)
                break

    async def _enrich_openstack_projects(self, clusters: dict[str, ClusterInfo]) -> None:
        """Magnum으로 서비스 클러스터명 → OpenStack 프로젝트명을 채운다.

        Magnum 미설정/호출 실패 시 조용히 건너뛴다(openstack_project는 None 유지).
        Magnum 클러스터명 == Prometheus cluster 라벨 이라는 전제로 조인한다.
        """
        # 1차: Magnum (진짜 K8s 서비스 클러스터)
        if openstack_client.magnum_configured:
            try:
                project_map = await openstack_client.cluster_project_map()
                for info in clusters.values():
                    if info.type != "service":
                        continue
                    mapping = project_map.get(info.name)
                    if mapping:
                        info.openstack_project = mapping.get("project_name")
                        info.is_kubernetes = True  # Magnum 명단에 있으면 진짜 K8s 클러스터
            except OpenStackError as exc:
                logger.info("Magnum 프로젝트 매핑 생략: %s", exc)

        # 2차: Nova 폴백 (Magnum이 모르는 단독 가속기 VM — 클러스터명 ↔ alias 매칭)
        remaining = [
            info for info in clusters.values()
            if info.type == "service" and not info.openstack_project
        ]
        if remaining and openstack_client.nova_configured:
            try:
                alias_project = await openstack_client.accelerator_vm_projects()
            except OpenStackError as exc:
                logger.info("Nova 프로젝트 폴백 생략: %s", exc)
                alias_project = {}
            for info in remaining:
                key = info.name.lower()
                proj = alias_project.get(key)
                if not proj:  # 부분 일치 (예: cluster "l40s" ↔ alias "l40s")
                    for alias, pname in alias_project.items():
                        if alias in key or key in alias:
                            proj = pname
                            break
                if proj:
                    info.openstack_project = proj


cluster_discovery = ClusterDiscovery()
