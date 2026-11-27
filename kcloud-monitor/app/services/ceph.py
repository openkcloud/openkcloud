"""
KCloud Monitor v2 — Ceph 분산 스토리지 메트릭 조회.

데이터소스: Prometheus. Ceph mgr의 prometheus 모듈(:9283)이 ceph_* 를 노출하고,
`serviceMonitor/monitoring/rook-ceph-mgr-external` 잡이 30초 주기로 수집한다.
Ceph 본체는 K8s 밖의 외부 클러스터라 kube API로는 조회할 수 없고 이 메트릭이 유일한 창구다.

설계 주의
  - **mgr이 2대(active, standby) 모두 스크레이프 대상이다.** Ceph는 active mgr만
    실제 값을 내고 standby는 빈 값이나 0을 낸다. 같은 시리즈가 인스턴스만 다르게 두 번
    들어오므로 파싱 단계에서 합치지 않으면 값이 0으로 덮인다(_merge).
  - **ceph_health_status는 0=OK, 1=WARN, 2=ERR로 값이 클수록 나쁘다.** 사용률 같은
    다른 지표와 방향이 반대라, 그대로 크기 비교하면 판정이 정확히 뒤집힌다.
  - 라우트마다 쿼리를 따로 날리지 않는다. snapshot() 이 정규식 셀렉터 하나로 필요한
    시리즈를 통째로 받고, 각 라우트는 그 결과를 슬라이스만 한다.
  - Prometheus 호출 실패는 prometheus_client 가 빈 리스트로 흡수한다. 여기서는 예외를
    올리지 않고 빈 스냅샷을 돌려주며, 호출부가 NO_DATA 로 응답한다.
"""
import asyncio
import logging
from typing import Optional

from app.services.prometheus import prometheus_client

logger = logging.getLogger(__name__)

# 스크레이프 출처를 가리키는 라벨. 두 mgr이 같은 값을 각자 올리므로 동일 대상인지
# 판별하기 전에 떼어낸다. __name__ 은 메트릭 이름이라 함께 제외한다.
_SCRAPE_LABELS = frozenset({
    "__name__", "instance", "pod", "job", "service", "endpoint",
    "namespace", "container", "node", "prometheus", "prometheus_replica",
})

# 스토리지 라우트 9개가 쓰는 메트릭 전부. 한 번의 instant 쿼리로 같이 받는다.
SNAPSHOT_METRICS = (
    # 전체 상태·용량
    "ceph_health_status",
    "ceph_health_detail",
    "ceph_cluster_total_bytes",
    "ceph_cluster_total_used_bytes",
    # OSD (디스크 단위)
    "ceph_osd_up",
    "ceph_osd_in",
    "ceph_osd_metadata",
    "ceph_osd_stat_bytes",
    "ceph_osd_stat_bytes_used",
    "ceph_osd_apply_latency_ms",
    "ceph_osd_commit_latency_ms",
    # 풀 (용도별 저장 공간 묶음)
    "ceph_pool_metadata",
    "ceph_pool_stored",
    "ceph_pool_max_avail",
    "ceph_pool_objects",
    "ceph_pool_rd",
    "ceph_pool_wr",
    # PG (데이터 배치 단위)
    "ceph_pg_total",
    "ceph_pg_active",
    "ceph_pg_clean",
    "ceph_pg_degraded",
)

SNAPSHOT_QUERY = '{__name__=~"%s"}' % "|".join(SNAPSHOT_METRICS)

CAPACITY_TOTAL = "ceph_cluster_total_bytes"
CAPACITY_USED = "ceph_cluster_total_used_bytes"

# ceph_pool_rd / _wr 는 누적 횟수라 그대로 쓰면 초당 값이 아니다. rate로 환산해서 따로 받는다.
# max로 묶는 이유는 두 mgr이 올린 같은 값을 sum이 두 배로 만들기 때문이다.
POOL_READ_RATE = "max by (pool_id) (rate(ceph_pool_rd[5m]))"
POOL_WRITE_RATE = "max by (pool_id) (rate(ceph_pool_wr[5m]))"

# 값이 클수록 나쁘다. 사용률처럼 다루면 안 된다.
HEALTH_CODES = {0: "HEALTH_OK", 1: "HEALTH_WARN", 2: "HEALTH_ERR"}


def health_code(value: Optional[float]) -> Optional[str]:
    """ceph_health_status 숫자를 상태 문자열로. 값이 없으면 None."""
    if value is None:
        return None
    return HEALTH_CODES.get(int(value), "HEALTH_UNKNOWN")


def _identity(labels: dict) -> tuple:
    """스크레이프 출처를 뺀 나머지 라벨쌍. 두 mgr이 올린 게 같은 대상인지 판별하는 키."""
    return tuple(sorted((k, v) for k, v in labels.items() if k not in _SCRAPE_LABELS))


def _merge(bucket: dict, key: tuple, labels: dict, value: float) -> None:
    """같은 대상의 중복 시리즈를 하나로. standby mgr의 빈 값(0)에 덮이지 않게 큰 쪽을 남긴다."""
    # ponytail: active/standby를 값 크기로 가른다. mgr별 구분이 필요해지면
    # ceph_mgr_status 로 active 인스턴스를 골라 셀렉터에 붙이는 쪽으로 올린다.
    prev = bucket.get(key)
    if prev is None or value > prev[1]:
        bucket[key] = (labels, value)


def _parse_instant(results: list[dict]) -> dict[str, list[tuple[dict, float]]]:
    """instant 응답을 메트릭 이름별 [(라벨, 값)] 으로 정리하고 mgr 중복을 합친다."""
    merged: dict[str, dict[tuple, tuple[dict, float]]] = {}
    for item in results:
        labels = dict(item.get("metric") or {})
        name = labels.get("__name__")
        if not name:
            continue
        try:
            value = float(item["value"][1])
        except (KeyError, IndexError, TypeError, ValueError):
            continue
        clean = {k: v for k, v in labels.items() if k != "__name__"}
        _merge(merged.setdefault(name, {}), _identity(labels), clean, value)
    return {name: list(b.values()) for name, b in merged.items()}


def _range_points(results: list[dict]) -> dict[float, float]:
    """range 응답을 timestamp → 값으로. 여기서도 mgr 중복은 큰 값으로 합친다."""
    points: dict[float, float] = {}
    for item in results:
        for ts, raw in item.get("values") or []:
            try:
                at, value = float(ts), float(raw)
            except (TypeError, ValueError):
                continue
            if value > points.get(at, float("-inf")):
                points[at] = value
    return points


class CephSnapshot:
    """한 시점의 ceph_* 시리즈 묶음. 라우트는 쿼리를 직접 짜지 않고 이 객체만 슬라이스한다."""

    def __init__(self, series: dict[str, list[tuple[dict, float]]]):
        self._series = series

    @property
    def empty(self) -> bool:
        """ceph 시리즈가 하나도 없음. 호출부는 partial + NO_DATA 로 응답한다."""
        return not self._series

    def series(self, metric: str) -> list[tuple[dict, float]]:
        """해당 메트릭의 [(라벨, 값)] 목록. 없으면 빈 목록."""
        return self._series.get(metric, [])

    def scalar(self, metric: str) -> Optional[float]:
        """클러스터 단위 단일 값(health, 전체 용량 등). 없으면 None."""
        found = self.series(metric)
        return found[0][1] if found else None

    def by_label(self, metric: str, label: str) -> dict[str, float]:
        """라벨 값 → 메트릭 값. OSD는 ceph_daemon, 풀은 pool_id 를 키로 쓴다.

        풀 이름과 OSD의 호스트명은 값 메트릭에 없고 metadata 메트릭에만 있으므로
        labels_by() 로 따로 받아 같은 키로 이어 붙인다.
        """
        return {labels[label]: value for labels, value in self.series(metric) if label in labels}

    def labels_by(self, metric: str, label: str) -> dict[str, dict]:
        """라벨 값 → 그 시리즈의 라벨 전체. metadata 메트릭에서 부가 정보를 꺼낼 때."""
        return {labels[label]: labels for labels, _ in self.series(metric) if label in labels}


async def snapshot() -> CephSnapshot:
    """ceph_* 시리즈를 한 번의 쿼리로 조회. 수집이 없거나 실패하면 빈 스냅샷."""
    results = await prometheus_client.instant(SNAPSHOT_QUERY)
    if not results:
        logger.warning("Ceph 메트릭 조회 결과가 비었습니다 — query=%s", SNAPSHOT_QUERY)
    return CephSnapshot(_parse_instant(results))


def _by_pool(results: list[dict]) -> dict[str, float]:
    """rate 쿼리 응답을 pool_id → 값으로. 집계 결과라 __name__ 이 없다."""
    points: dict[str, float] = {}
    for item in results:
        pool_id = (item.get("metric") or {}).get("pool_id")
        if pool_id is None:
            continue
        try:
            points[pool_id] = float(item["value"][1])
        except (KeyError, IndexError, TypeError, ValueError):
            continue
    return points


async def pool_iops() -> dict[str, dict[str, float]]:
    """풀별 초당 읽기·쓰기 횟수. {"read": {pool_id: 값}, "write": {...}} 형태."""
    read_raw, write_raw = await asyncio.gather(
        prometheus_client.instant(POOL_READ_RATE),
        prometheus_client.instant(POOL_WRITE_RATE),
    )
    return {"read": _by_pool(read_raw), "write": _by_pool(write_raw)}


async def capacity_range(
    start: str, end: str, step: str
) -> list[tuple[float, float, Optional[float]]]:
    """용량 시계열. (timestamp, 사용 bytes, 사용률 %) 목록을 시각 순으로 반환.

    사용률은 같은 시각의 전체 용량이 있을 때만 계산하고, 없으면 None 으로 둔다.
    """
    used_raw, total_raw = await asyncio.gather(
        prometheus_client.range_query(CAPACITY_USED, start, end, step),
        prometheus_client.range_query(CAPACITY_TOTAL, start, end, step),
    )
    used, total = _range_points(used_raw), _range_points(total_raw)
    points = []
    for at in sorted(used):
        capacity = total.get(at)
        ratio = round(used[at] / capacity * 100, 2) if capacity else None
        points.append((at, used[at], ratio))
    return points
