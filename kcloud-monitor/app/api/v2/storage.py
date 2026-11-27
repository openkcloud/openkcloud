"""Ceph 분산 스토리지 조회 라우터

여러 서버의 디스크를 묶어 하나의 저장소로 쓰는 Ceph를 조회.
노드 한 대에 붙은 로컬 디스크는 노드 하위의 storage 경로에서 조회.

Ceph는 관리 클러스터에만 있고 메트릭에 cluster 라벨이 붙지 않아, 경로에 클러스터를 받지 않는다.
메트릭이 수집되지 않으면 status="partial" 과 NO_DATA 경고를 반환한다.
"""
from datetime import datetime, timezone
from typing import Optional

from fastapi import APIRouter, Depends, HTTPException

from app.api.v2.deps import PaginationParams, TimeseriesParams
from app.schemas.storage import (
    CephCapacityData,
    CephCapacityResponse,
    CephCapacitySeries,
    CephCapacityTimeseriesResponse,
    CephDeviceClassCapacity,
    CephHealthCheck,
    CephHealthData,
    CephHealthResponse,
    CephOSDDetailResponse,
    CephOSDItem,
    CephOSDListResponse,
    CephOSDSummaryData,
    CephPGData,
    CephPGResponse,
    CephPoolDetail,
    CephPoolDetailResponse,
    CephPoolItem,
    CephPoolListResponse,
    CephSummaryData,
    CephSummaryResponse,
)
from app.services import ceph

router = APIRouter()


# ---------------------------------------------------------------------------
# 공통 계산
# ---------------------------------------------------------------------------

def _percent(used: Optional[float], total: Optional[float]) -> Optional[float]:
    """사용률(%). 전체 용량이 없거나 0이면 None."""
    if used is None or not total:
        return None
    return round(used / total * 100, 2)


def _osd_number(osd_id: str) -> tuple:
    """osd.10 이 osd.2 뒤에 오도록 숫자로 정렬하기 위한 키."""
    suffix = osd_id.split(".")[-1]
    return (0, int(suffix)) if suffix.isdigit() else (1, 0)


def _osd_key(osd_id: str) -> str:
    """경로로 받은 7 과 osd.7 을 같은 값으로 취급."""
    return osd_id if osd_id.startswith("osd.") else f"osd.{osd_id}"


def _sort_value(item, field: str):
    """정렬 키. 값이 없는 항목은 뒤로 보낸다."""
    value = getattr(item, field, None)
    return (value is None, value if isinstance(value, (int, float)) else str(value or ""))


def _page(items: list, params: PaginationParams, search_fields: tuple[str, ...]):
    """검색·정렬·페이지 적용. (현재 페이지, 검색 후 전체 개수) 반환."""
    if params.search:
        keyword = params.search.lower()
        items = [
            i for i in items
            if any(keyword in str(getattr(i, f, "") or "").lower() for f in search_fields)
        ]
    if params.sort_by:
        items = sorted(items, key=lambda i: _sort_value(i, params.sort_by),
                       reverse=params.sort_order == "desc")
    return items[params.offset:params.offset + params.limit], len(items)


def _osd_items(snap: ceph.CephSnapshot) -> list[CephOSDItem]:
    """OSD 값 메트릭과 metadata 라벨을 ceph_daemon 기준으로 이어 붙인다."""
    up = snap.by_label("ceph_osd_up", "ceph_daemon")
    joined = snap.by_label("ceph_osd_in", "ceph_daemon")
    total = snap.by_label("ceph_osd_stat_bytes", "ceph_daemon")
    used = snap.by_label("ceph_osd_stat_bytes_used", "ceph_daemon")
    apply_ms = snap.by_label("ceph_osd_apply_latency_ms", "ceph_daemon")
    commit_ms = snap.by_label("ceph_osd_commit_latency_ms", "ceph_daemon")
    meta = snap.labels_by("ceph_osd_metadata", "ceph_daemon")

    items = []
    for osd_id in sorted(set(up) | set(joined) | set(total) | set(meta), key=_osd_number):
        labels = meta.get(osd_id, {})
        items.append(CephOSDItem(
            osd_id=osd_id,
            up=bool(up[osd_id]) if osd_id in up else None,
            in_cluster=bool(joined[osd_id]) if osd_id in joined else None,
            hostname=labels.get("hostname"),
            device_class=labels.get("device_class"),
            device=labels.get("devices") or labels.get("device"),
            total_bytes=total.get(osd_id),
            used_bytes=used.get(osd_id),
            usage_percent=_percent(used.get(osd_id), total.get(osd_id)),
            apply_latency_ms=apply_ms.get(osd_id),
            commit_latency_ms=commit_ms.get(osd_id),
        ))
    return items


def _osd_summary(items: list[CephOSDItem]) -> CephOSDSummaryData:
    """OSD 상태 집계. up/down 과 in/out 은 서로 다른 축이라 따로 센다."""
    return CephOSDSummaryData(
        total=len(items),
        up=sum(1 for i in items if i.up),
        in_cluster=sum(1 for i in items if i.in_cluster),
        down=sum(1 for i in items if i.up is False),
        out=sum(1 for i in items if i.in_cluster is False),
    )


async def _pool_items(snap: ceph.CephSnapshot) -> list[CephPoolItem]:
    """풀 값 메트릭과 metadata 라벨을 pool_id 기준으로 이어 붙인다."""
    stored = snap.by_label("ceph_pool_stored", "pool_id")
    avail = snap.by_label("ceph_pool_max_avail", "pool_id")
    objects = snap.by_label("ceph_pool_objects", "pool_id")
    meta = snap.labels_by("ceph_pool_metadata", "pool_id")
    iops = await ceph.pool_iops()

    items = []
    for pool_id in sorted(set(stored) | set(meta)):
        labels = meta.get(pool_id, {})
        items.append(CephPoolItem(
            name=labels.get("name") or pool_id,
            pool_id=pool_id,
            stored_bytes=stored.get(pool_id),
            available_bytes=avail.get(pool_id),
            objects=objects.get(pool_id),
            read_ops_per_sec=iops["read"].get(pool_id),
            write_ops_per_sec=iops["write"].get(pool_id),
        ))
    return items


# ---------------------------------------------------------------------------
# 라우트
# ---------------------------------------------------------------------------

@router.get("/storage/ceph/summary", summary="Ceph 요약", response_model=CephSummaryResponse)
async def get_ceph_summary():
    """Ceph 스토리지 전체 상태 요약 조회

    입력 예시

    * `GET /api/v2/storage/ceph/summary`

    응답

    * `health`: 전체 상태 (`HEALTH_OK` | `HEALTH_WARN` | `HEALTH_ERR`)
    * `osd_total`, `osd_up`, `osd_in`: OSD 전체, 동작 중, 데이터 배치 대상 개수
    * `total_bytes`, `used_bytes`, `usage_percent`: 전체 용량, 사용 용량 (bytes), 사용률 (%)
    * `pool_count`: 풀 개수

    경고

    * `NO_DATA`: Ceph 메트릭이 수집되지 않음, `data` 는 `null`
    """
    snap = await ceph.snapshot()
    if snap.empty:
        return CephSummaryResponse(status="partial", warnings=["NO_DATA"])

    osds = _osd_items(snap)
    total = snap.scalar(ceph.CAPACITY_TOTAL)
    used = snap.scalar(ceph.CAPACITY_USED)
    counts = _osd_summary(osds)
    return CephSummaryResponse(
        status="success",
        data=CephSummaryData(
            health=ceph.health_code(snap.scalar("ceph_health_status")),
            osd_total=counts.total,
            osd_up=counts.up,
            osd_in=counts.in_cluster,
            total_bytes=total,
            used_bytes=used,
            usage_percent=_percent(used, total),
            pool_count=len(snap.series("ceph_pool_metadata")) or len(
                snap.by_label("ceph_pool_stored", "pool_id")
            ),
        ),
    )


@router.get("/storage/ceph/health", summary="Ceph 상태 점검 상세", response_model=CephHealthResponse)
async def get_ceph_health():
    """Ceph 상태의 원인 점검 항목 조회

    입력 예시

    * `GET /api/v2/storage/ceph/health`

    응답

    * `health`: 전체 상태 (`HEALTH_OK` | `HEALTH_WARN` | `HEALTH_ERR`)
    * `checks`: 경고나 오류를 낸 점검 항목, 항목마다 `code`, `severity`

    경고

    * `NO_DATA`: Ceph 메트릭이 수집되지 않음, `data` 는 `null`
    """
    snap = await ceph.snapshot()
    if snap.empty:
        return CephHealthResponse(status="partial", warnings=["NO_DATA"])

    checks = [
        CephHealthCheck(code=labels.get("name", ""), severity=labels.get("severity"))
        for labels, value in snap.series("ceph_health_detail")
        if value > 0 and labels.get("name")
    ]
    return CephHealthResponse(
        status="success",
        data=CephHealthData(
            health=ceph.health_code(snap.scalar("ceph_health_status")),
            checks=checks,
        ),
    )


@router.get("/storage/ceph/capacity", summary="Ceph 용량", response_model=CephCapacityResponse)
async def get_ceph_capacity():
    """Ceph 스토리지 용량 조회

    입력 예시

    * `GET /api/v2/storage/ceph/capacity`

    응답

    * `total_bytes`, `used_bytes`, `available_bytes`: 전체, 사용, 남은 용량 (bytes)
    * `usage_percent`: 사용률 (%)
    * `by_device_class`: 디스크 종류(`ssd` | `hdd` | `nvme`)별 전체와 사용 용량 (bytes), 사용률 (%), OSD 개수

    경고

    * `NO_DATA`: Ceph 메트릭이 수집되지 않음, `data` 는 `null`

    참고

    * 복제본을 포함한 실제 디스크 소모 기준
    """
    snap = await ceph.snapshot()
    if snap.empty:
        return CephCapacityResponse(status="partial", warnings=["NO_DATA"])

    total = snap.scalar(ceph.CAPACITY_TOTAL)
    used = snap.scalar(ceph.CAPACITY_USED)

    by_class: dict[str, dict] = {}
    for osd in _osd_items(snap):
        if not osd.device_class:
            continue
        bucket = by_class.setdefault(
            osd.device_class, {"total": 0.0, "used": 0.0, "count": 0}
        )
        bucket["total"] += osd.total_bytes or 0.0
        bucket["used"] += osd.used_bytes or 0.0
        bucket["count"] += 1

    return CephCapacityResponse(
        status="success",
        data=CephCapacityData(
            total_bytes=total,
            used_bytes=used,
            available_bytes=(total - used) if total is not None and used is not None else None,
            usage_percent=_percent(used, total),
            by_device_class=[
                CephDeviceClassCapacity(
                    device_class=name,
                    total_bytes=v["total"],
                    used_bytes=v["used"],
                    usage_percent=_percent(v["used"], v["total"]),
                    osd_count=v["count"],
                )
                for name, v in sorted(by_class.items())
            ],
        ),
    )


@router.get("/storage/ceph/capacity/timeseries", summary="Ceph 용량 시계열",
            response_model=CephCapacityTimeseriesResponse)
async def get_ceph_capacity_timeseries(params: TimeseriesParams = Depends()):
    """Ceph 사용 용량과 사용률의 변화 추이 조회

    입력 예시

    * `GET /api/v2/storage/ceph/capacity/timeseries`
    * `GET /api/v2/storage/ceph/capacity/timeseries?period=7d&step=1h`

    입력 옵션

    * `period`: 조회 기간, 예 `30m`, `1h`, `7d` (기본 `1h`)
    * `start`: 시작 시각, ISO 8601 형식 (선택, 기본 현재 시각에서 `period` 만큼 이전)
    * `end`: 종료 시각, ISO 8601 형식 (선택, 기본 현재 시각)
    * `step`: 데이터 점 간격, 예 `1m`, `5m`, `1h` (기본 `5m`)

    응답

    * `series`: 시리즈 목록, 항목마다 `name` (`used_bytes` | `usage_percent`)과 (시각, 값) 쌍 목록
    * 시각은 ISO 8601 UTC, 값은 숫자 문자열
    * `used_bytes` 는 bytes 단위, `usage_percent` 는 % 단위

    경고

    * `NO_DATA`: Ceph 메트릭이 수집되지 않음, `series` 는 빈 목록
    """
    now = datetime.now(timezone.utc)
    points = await ceph.capacity_range(params.start_iso(now), params.end_iso(now), params.step)
    if not points:
        return CephCapacityTimeseriesResponse(status="partial", warnings=["NO_DATA"])

    def _iso(at: float) -> str:
        return datetime.fromtimestamp(at, tz=timezone.utc).isoformat()

    return CephCapacityTimeseriesResponse(
        status="success",
        series=[
            CephCapacitySeries(
                name="used_bytes",
                values=[(_iso(at), str(used)) for at, used, _ in points],
            ),
            CephCapacitySeries(
                name="usage_percent",
                values=[(_iso(at), str(pct)) for at, _, pct in points if pct is not None],
            ),
        ],
    )


@router.get("/storage/ceph/osds", summary="OSD 목록", response_model=CephOSDListResponse)
async def list_ceph_osds(params: PaginationParams = Depends()):
    """Ceph 디스크 단위(OSD) 목록 조회

    입력 예시

    * `GET /api/v2/storage/ceph/osds`
    * `GET /api/v2/storage/ceph/osds?search=compute1&sort_by=usage_percent&sort_order=desc`

    입력 옵션

    * `search`: OSD 이름이나 노드 이름에 들어 있는 글자 (선택, 대소문자 무시)
    * `sort_by`: 정렬 기준, 응답 항목의 필드 이름, 예 `usage_percent` (선택)
    * `sort_order`: `asc` | `desc` (기본 `asc`)
    * `limit`: 한 번에 받을 개수 `1` ~ `1000` (기본 `100`)
    * `offset`: 건너뛸 개수 (기본 `0`)

    응답

    * 항목마다 `osd_id`, 동작 여부(`up`), 데이터 배치 대상 여부(`in_cluster`)
    * 노드 이름(`hostname`), 디스크 종류(`ssd` | `hdd` | `nvme`), 장치 이름
    * 전체 용량, 사용 용량 (bytes), 사용률 (%), 쓰기 반영 지연 (ms), 쓰기 확정 지연 (ms)
    * `total`: 검색을 적용한 뒤의 OSD 개수
    * `summary`: 전체, `up`, `in_cluster`, `down`, `out` 개수

    경고

    * `NO_DATA`: Ceph 메트릭이 수집되지 않음

    참고

    * `summary` 는 `search`, `limit`, `offset` 에 영향받지 않음
    """
    snap = await ceph.snapshot()
    if snap.empty:
        return CephOSDListResponse(status="partial", warnings=["NO_DATA"])

    items = _osd_items(snap)
    page, total = _page(items, params, ("osd_id", "hostname"))
    return CephOSDListResponse(
        status="success", data=page, total=total, summary=_osd_summary(items)
    )


@router.get("/storage/ceph/osds/{osd_id}", summary="OSD 상세",
            response_model=CephOSDDetailResponse)
async def get_ceph_osd(osd_id: str):
    """OSD 한 개의 상세 조회

    입력 예시

    * `GET /api/v2/storage/ceph/osds/osd.7`

    입력 옵션

    * `osd_id`: OSD 이름, 예 `osd.7` 또는 `7` (경로, 필수)

    응답

    * `osd_id`, 동작 여부(`up`), 데이터 배치 대상 여부(`in_cluster`)
    * 노드 이름(`hostname`), 디스크 종류(`ssd` | `hdd` | `nvme`), 장치 이름
    * 전체 용량, 사용 용량 (bytes), 사용률 (%), 쓰기 반영 지연 (ms), 쓰기 확정 지연 (ms)

    경고

    * `NO_DATA`: Ceph 메트릭이 수집되지 않음, `data` 는 `null`

    오류

    * 404: 없는 OSD
    """
    snap = await ceph.snapshot()
    if snap.empty:
        return CephOSDDetailResponse(status="partial", warnings=["NO_DATA"])

    wanted = _osd_key(osd_id)
    for item in _osd_items(snap):
        if item.osd_id == wanted:
            return CephOSDDetailResponse(status="success", data=item)
    raise HTTPException(status_code=404, detail=f"알 수 없는 OSD: {osd_id}")


@router.get("/storage/ceph/pools", summary="풀 목록", response_model=CephPoolListResponse)
async def list_ceph_pools(params: PaginationParams = Depends()):
    """Ceph 저장 공간 묶음(풀) 목록 조회

    입력 예시

    * `GET /api/v2/storage/ceph/pools`
    * `GET /api/v2/storage/ceph/pools?search=volumes&sort_by=stored_bytes&sort_order=desc`

    입력 옵션

    * `search`: 풀 이름이나 풀 번호에 들어 있는 글자 (선택, 대소문자 무시)
    * `sort_by`: 정렬 기준, 응답 항목의 필드 이름, 예 `stored_bytes` (선택)
    * `sort_order`: `asc` | `desc` (기본 `asc`)
    * `limit`: 한 번에 받을 개수 `1` ~ `1000` (기본 `100`)
    * `offset`: 건너뛸 개수 (기본 `0`)

    응답

    * 항목마다 풀 이름(`name`), 풀 번호(`pool_id`)
    * 저장된 논리 데이터량 (bytes), 추가로 쓸 수 있는 용량 (bytes), 오브젝트 개수
    * 초당 읽기 횟수 (회/s), 초당 쓰기 횟수 (회/s)
    * `total`: 검색을 적용한 뒤의 풀 개수

    경고

    * `NO_DATA`: Ceph 메트릭이 수집되지 않음
    """
    snap = await ceph.snapshot()
    if snap.empty:
        return CephPoolListResponse(status="partial", warnings=["NO_DATA"])

    page, total = _page(await _pool_items(snap), params, ("name", "pool_id"))
    return CephPoolListResponse(status="success", data=page, total=total)


@router.get("/storage/ceph/pools/{pool}", summary="풀 상세",
            response_model=CephPoolDetailResponse)
async def get_ceph_pool(pool: str):
    """Ceph 풀 한 개의 상세 조회

    입력 예시

    * `GET /api/v2/storage/ceph/pools/volumes`

    입력 옵션

    * `pool`: 풀 이름 또는 풀 번호, 예 `volumes` 또는 `3` (경로, 필수)

    응답

    * 풀 이름(`name`), 풀 번호(`pool_id`)
    * 저장된 논리 데이터량 (bytes), 추가로 쓸 수 있는 용량 (bytes), 오브젝트 개수
    * 초당 읽기 횟수 (회/s), 초당 쓰기 횟수 (회/s)
    * `replication_type`: 데이터 보호 방식 (`replicated` | `erasure`)
    * `replication_size`: 복제본 개수

    경고

    * `NO_DATA`: Ceph 메트릭이 수집되지 않음, `data` 는 `null`

    오류

    * 404: 없는 풀
    """
    snap = await ceph.snapshot()
    if snap.empty:
        return CephPoolDetailResponse(status="partial", warnings=["NO_DATA"])

    meta = snap.labels_by("ceph_pool_metadata", "pool_id")
    for item in await _pool_items(snap):
        if pool not in (item.name, item.pool_id):
            continue
        labels = meta.get(item.pool_id or "", {})
        size = labels.get("size")
        return CephPoolDetailResponse(
            status="success",
            data=CephPoolDetail(
                **item.model_dump(),
                replication_type=labels.get("type"),
                replication_size=int(size) if size and size.isdigit() else None,
            ),
        )
    raise HTTPException(status_code=404, detail=f"알 수 없는 풀: {pool}")


@router.get("/storage/ceph/pgs", summary="PG 상태 요약", response_model=CephPGResponse)
async def get_ceph_pgs():
    """Ceph 데이터 배치 단위(Placement Group) 상태 집계 조회

    입력 예시

    * `GET /api/v2/storage/ceph/pgs`

    응답

    * `total`: 전체 PG 개수
    * `active`: 읽기와 쓰기가 가능한 개수
    * `clean`: 복제본까지 맞춰진 개수
    * `degraded`: 복제본이 부족한 개수

    경고

    * `NO_DATA`: Ceph 메트릭이 수집되지 않음, `data` 는 `null`
    """
    snap = await ceph.snapshot()
    if snap.empty:
        return CephPGResponse(status="partial", warnings=["NO_DATA"])

    return CephPGResponse(
        status="success",
        data=CephPGData(
            total=snap.scalar("ceph_pg_total"),
            active=snap.scalar("ceph_pg_active"),
            clean=snap.scalar("ceph_pg_clean"),
            degraded=snap.scalar("ceph_pg_degraded"),
        ),
    )
