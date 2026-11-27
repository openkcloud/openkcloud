"""여러 클러스터를 한꺼번에 묶어 보는 라우터

클러스터 구분 없이 전체를 합친 현황, 전력, 메트릭 조회와 실시간 스트리밍(SSE).
스트리밍은 15초마다 heartbeat 전송, Last-Event-ID 헤더로 재개 가능.
"""
import asyncio
import json
import logging
from datetime import datetime, timezone
from typing import Optional

from fastapi import APIRouter, Depends, HTTPException, Query, Request
from fastapi.responses import StreamingResponse

from app.api.v2.accelerators import count_throttle, throttle_series, throttle_starts
from app.api.v2.deps import TimeseriesParams, parse_period
from app.schemas.accelerators import (
    ThrottlingSeriesItem,
    ThrottlingSummary,
    ThrottlingTimelinePoint,
    ThrottlingTimeseriesResponse,
)
from app.schemas.monitoring import (
    METRIC_ALLOWLIST,
    AcceleratorEfficiency,
    AcceleratorUtilizationData,
    AcceleratorUtilizationResponse,
    AcceleratorUtilizationTimeseriesResponse,
    ClusterCounts,
    MetricSample,
    MetricSeries,
    MetricsQueryResponse,
    NodeCounts,
    NodeCountSeriesItem,
    NodesTimeseriesResponse,
    OverviewData,
    OverviewResponse,
    PowerBreakdownItem,
    PowerBreakdownResponse,
    PowerEfficiencyData,
    PowerEfficiencyResponse,
    PowerEfficiencySeriesItem,
    PowerEfficiencyTimeseriesResponse,
    PowerSummaryData,
    PowerSummaryResponse,
    PowerTimeseriesLayer,
    PowerTimeseriesResponse,
    TemperatureSeriesItem,
    TemperatureTimeseriesResponse,
    HostUtilization,
    HostUtilizationSeriesItem,
    TimeseriesResponse,
    UtilizationSeriesItem,
    VendorUtilization,
)
from app.services.cluster_discovery import cluster_discovery
from app.services.power import (
    accelerator_utilization,
    accelerator_utilization_timeseries,
    power_breakdown,
    power_efficiency,
    power_efficiency_timeseries,
    power_summary,
    power_timeseries,
)
from app.services.prometheus import first_finite, first_value, prometheus_client, ts_iso

logger = logging.getLogger(__name__)

router = APIRouter()

# 노드 집계. Kubernetes 노드는 kube_node_info, 쿠버네티스 없는 단독 VM 은 node_uname_info 로 센다.
# 같은 클러스터가 둘 다 있으면(서비스 k8s 클러스터) kube_node_info 만 써서 중복을 막는다.
# kube_node_status_condition 은 노드마다 true/false/unknown 시계열이 늘 있어 == 1 로 걸러야 Ready 수가 된다.
# 단독 VM 은 push 경로라 꺼지면 5분 뒤 전체 수에서도 빠짐(비정상이 아니라 총수 감소로 보임).
_VM_NODES = 'count(node_uname_info{cluster!=""} unless on(cluster) kube_node_info)'
NODE_TOTAL_QUERY = f"count(kube_node_info) + {_VM_NODES}"
NODE_HEALTHY_QUERY = f'count(kube_node_status_condition{{condition="Ready",status="true"}} == 1) + {_VM_NODES}'
NODE_PHYSICAL_QUERY = 'count(kube_node_info{cluster=""})'


def time_range(params: TimeseriesParams) -> tuple[str, str, str]:
    """시계열 조회 구간 (start, end, step). 시작과 끝을 같은 now 기준으로 계산."""
    now = datetime.now(timezone.utc)
    return params.start_iso(now), params.end_iso(now), params.step


def _check_metric(metric: Optional[str]) -> None:
    """metric 누락 또는 허용 목록에 없는 이름이면 400."""
    if metric is None:
        raise HTTPException(
            status_code=400,
            detail="metric 파라미터 필수. 허용 목록: " + str(list(METRIC_ALLOWLIST.keys())),
        )
    if metric not in METRIC_ALLOWLIST:
        raise HTTPException(
            status_code=400,
            detail=f"허용되지 않은 메트릭: {metric}. 허용 목록: {list(METRIC_ALLOWLIST.keys())}",
        )


@router.get("/monitoring/overview", summary="전체 인프라 현황(KPI)", response_model=OverviewResponse)
async def get_overview(request: Request):
    """전체 인프라 현황을 한 화면용 요약값으로 조회

    입력 예시

    * `GET /api/v2/monitoring/overview`

    응답

    * `clusters`: 전체, 관리, 서비스 클러스터 수
    * `nodes`: 전체, 물리(관리 클러스터), 가상(서비스 클러스터), 정상, 비정상 노드 수
    * `accelerator_count`: 가속기 클러스터의 메트릭 수집 대상 수
    * `healthy_count`: 그중 응답 중인(`up`) 수집 대상 수
    * `avg_temperature`: 클러스터별(`l40s` | `furiosa` | `rebellions`) 평균 가속기 온도(°C), 미수집 시 `null`

    경고

    * `REBELLIONS_SCALE_CORRECTED_X1000`: 리벨리온 온도를 1000배 보정한 값

    참고

    * `accelerator_count` 는 카드 수와 다를 수 있음
    * 쿠버네티스 없는 단독 VM 은 보고가 끊기면 5분 뒤 `nodes` 전체 수에서 빠짐
    """
    warnings: list[str] = []

    # 1. up 쿼리로 클러스터별 인스턴스 수 집계
    up_results = await prometheus_client.instant(
        'up{cluster=~"l40s|rebellions|furiosa.*"}'
    )

    # 클러스터별 up/total 집계 (가속기 target 기준 — accelerator_count/healthy_count 근거)
    cluster_map: dict[str, dict] = {}
    for item in up_results:
        cluster = item.get("metric", {}).get("cluster", "unknown")
        val = float(item.get("value", [0, "0"])[1])
        if cluster not in cluster_map:
            cluster_map[cluster] = {"up": 0, "total": 0}
        cluster_map[cluster]["total"] += 1
        if val == 1.0:
            cluster_map[cluster]["up"] += 1

    healthy_count = sum(counts["up"] for counts in cluster_map.values())
    accelerator_count = sum(counts["total"] for counts in cluster_map.values())

    # 2. 클러스터 discovery 기반 관리/서비스 구분 집계
    clusters_info = await cluster_discovery.get_clusters()
    cluster_counts = ClusterCounts(
        total=len(clusters_info),
        management=sum(1 for c in clusters_info.values() if c.type == "management"),
        service=sum(1 for c in clusters_info.values() if c.type == "service"),
    )

    # count() 값은 정수라 NaN/Inf 가 없음. 미수집이면 0
    total, healthy, physical = (
        int(first_finite(r) or 0)
        for r in await asyncio.gather(*(prometheus_client.instant(q) for q in (
            NODE_TOTAL_QUERY, NODE_HEALTHY_QUERY, NODE_PHYSICAL_QUERY,
        )))
    )
    node_counts = NodeCounts(
        total=total,
        physical=physical,
        virtual=total - physical,
        healthy=healthy,
        unhealthy=max(0, total - healthy),
    )

    # 3. L40S 평균 온도
    l40s_avg_temp = first_value(await prometheus_client.instant(
        'avg(DCGM_FI_DEV_GPU_TEMP{cluster="l40s"})'
    ))

    # 4. Furiosa 평균 온도
    furiosa_avg_temp = first_value(await prometheus_client.instant(
        'avg(furiosa_npu_hw_temperature{label="peak",cluster=~"furiosa.*"})'
    ))

    # 5. Rebellions 평균 온도 — exporter가 실측의 1/1000로 표출하므로 ×1000 보정
    #    (rbln-stat 카드 실측 39°C·18.4W와 대조 확정, 2026-08-24)
    rebellions_avg_temp = first_value(await prometheus_client.instant(
        'avg({__name__="RBLN_DEVICE_STATUS:TEMPERATURE",cluster="rebellions"}) * 1000'
    ))
    warnings.append("REBELLIONS_SCALE_CORRECTED_X1000")

    # 6. Prometheus 결과 없으면 status="partial"
    status = "partial" if not up_results else "success"

    data = OverviewData(
        clusters=cluster_counts,
        nodes=node_counts,
        accelerator_count=accelerator_count,
        healthy_count=healthy_count,
        # 키는 클러스터 라벨값으로 통일(시스템 전체 구분 키가 cluster 라벨) — 벤더명 혼용 제거
        avg_temperature={
            "l40s": l40s_avg_temp,
            "furiosa": furiosa_avg_temp,  # 퓨리오사 클러스터 전체 평균(furiosa, furiosa-1348 ...)
            "rebellions": rebellions_avg_temp,  # ×1000 보정값 (REBELLIONS_SCALE_CORRECTED_X1000)
        },
    )

    return OverviewResponse(status=status, data=data, warnings=warnings)


@router.get("/monitoring/nodes/timeseries", summary="노드 수 시계열", response_model=NodesTimeseriesResponse)
async def get_nodes_timeseries(request: Request, params: TimeseriesParams = Depends()):
    """전체 노드 수와 정상 노드 수의 변화 추이 조회

    입력 예시

    * `GET /api/v2/monitoring/nodes/timeseries`
    * `GET /api/v2/monitoring/nodes/timeseries?period=24h&step=1h`

    입력 옵션

    * `period`: 조회 기간, 예 `30m`, `1h`, `7d` (기본 `1h`, 최대 10일)
    * `start`: 시작 시각, ISO 8601 (선택, 없으면 지금에서 `period` 만큼 이전)
    * `end`: 종료 시각, ISO 8601 (선택, 없으면 지금)
    * `step`: 데이터 점 간격, 예 `1m`, `5m`, `1h` (기본 `5m`)

    응답

    * `series`: 노드 수 종류(`metric`: `total` | `healthy`)별 (시각, 값) 쌍 목록(`values`)
    * 시각은 ISO 8601 UTC

    경고

    * `NO_DATA`: 결과 없음

    참고

    * 집계 기준은 `GET /api/v2/monitoring/overview` 의 `nodes.total`, `nodes.healthy` 와 같음
    * `aggregation` 은 쓰지 않음, 각 시각의 순간값
    """
    start, end, step = time_range(params)
    total, healthy = await asyncio.gather(
        prometheus_client.range_query(NODE_TOTAL_QUERY, start, end, step),
        prometheus_client.range_query(NODE_HEALTHY_QUERY, start, end, step),
    )
    series = [
        NodeCountSeriesItem(metric=name, values=[(ts_iso(t), v) for t, v in results[0].get("values", [])])
        for name, results in (("total", total), ("healthy", healthy))
        if results
    ]
    if not series:
        return NodesTimeseriesResponse(status="partial", series=[], warnings=["NO_DATA"])
    return NodesTimeseriesResponse(status="success", series=series)


@router.get("/monitoring/power/summary", summary="전력 요약", response_model=PowerSummaryResponse)
async def get_power_summary(request: Request):
    """인프라 전체 전력을 측정 구분별로 나눈 합계 조회

    입력 예시

    * `GET /api/v2/monitoring/power/summary`

    응답

    * `server_total_watts`: 서버 총 전력(W), IPMI 실측
    * `cpu_total_watts`: CPU 전력(W), Kepler 실측
    * `accelerator_total_watts`: 가속기 전력 합계(W)
    * `accelerator_by_vendor`: 벤더별(`nvidia` | `furiosa` | `rebellions`) 가속기 전력 합계(W)
    * `other_watts`: 서버 총 전력에서 CPU와 가속기를 뺀 나머지 전력(W)

    참고

    * 미수집 항목은 `null`
    """
    r = await power_summary()
    return PowerSummaryResponse(
        status=r["status"],
        data=PowerSummaryData(**r["data"]),
        warnings=r["warnings"],
    )


@router.get("/monitoring/power/breakdown", summary="기준별 전력 분포", response_model=PowerBreakdownResponse)
async def get_power_breakdown(
    request: Request,
    dimension: str = Query("vendor", description="나누는 기준 (vendor | cluster | node | accelerator, 기본 vendor)"),
    sort_order: str = Query("desc", pattern="^(asc|desc)$", description="정렬 방향 (desc | asc, 기본 desc)"),
):
    """선택한 기준(벤더, 클러스터, 노드, 카드)별 전력 조회

    입력 예시

    * `GET /api/v2/monitoring/power/breakdown`
    * `GET /api/v2/monitoring/power/breakdown?dimension=node&sort_order=asc`

    입력 옵션

    * `dimension`: 나누는 기준 `vendor` | `cluster` | `node` | `accelerator` (기본 `vendor`)
    * `sort_order`: 정렬 방향 `desc` | `asc` (기본 `desc`)

    응답

    * `dimension`: 적용된 기준
    * `items`: 기준값 이름(`key`), 전력(W), 측정 구분(`layer`: `server` | `cpu` | `accelerator`)
    * 전력 큰 순으로 정렬 (`sort_order=asc` 면 작은 순)
    * 전력 값이 없는 항목은 맨 뒤

    경고

    * `UNKNOWN_DIMENSION`: 알 수 없는 기준, 빈 목록 반환

    참고

    * `node` 기준은 노드마다 서버 전력(`server`)과 CPU 전력(`cpu`) 항목이 따로 나옴
    """
    r = await power_breakdown(dimension)
    sign = -1 if sort_order == "desc" else 1
    items = sorted(r["data"], key=lambda i: (i["watts"] is None, sign * (i["watts"] or 0.0)))
    return PowerBreakdownResponse(
        status=r["status"],
        dimension=dimension,
        items=[PowerBreakdownItem(**i) for i in items],
        warnings=r["warnings"],
    )


@router.get("/monitoring/power/timeseries", summary="전체 전력 시계열", response_model=PowerTimeseriesResponse)
async def get_power_timeseries(request: Request, params: TimeseriesParams = Depends()):
    """인프라 전체 전력의 측정 구분별 변화 추이 조회

    입력 예시

    * `GET /api/v2/monitoring/power/timeseries`
    * `GET /api/v2/monitoring/power/timeseries?period=6h&step=10m`

    입력 옵션

    * `period`: 조회 기간, 예 `30m`, `1h`, `7d` (기본 `1h`, 최대 10일)
    * `start`: 시작 시각, ISO 8601 (선택, 없으면 지금에서 `period` 만큼 이전)
    * `end`: 종료 시각, ISO 8601 (선택, 없으면 지금)
    * `step`: 데이터 점 간격, 예 `1m`, `5m`, `1h` (기본 `5m`)

    응답

    * `layers`: 측정 구분(`server` | `cpu` | `accelerator`)별 (시각, 전력 W) 쌍 목록
    * 시각은 ISO 8601 UTC

    참고

    * 누적 그래프에 바로 쓰는 형태
    """
    start, end, step = time_range(params)

    r = await power_timeseries(start, end, step)
    return PowerTimeseriesResponse(
        status=r["status"],
        layers=[PowerTimeseriesLayer(**l) for l in r["data"]],
        warnings=r["warnings"],
    )


@router.get("/monitoring/power/efficiency", summary="가속기 전력 효율", response_model=PowerEfficiencyResponse)
async def get_power_efficiency(request: Request):
    """가속기의 전력 대비 사용률 효율 조회

    입력 예시

    * `GET /api/v2/monitoring/power/efficiency`

    응답

    * `pue_estimate`: 서버 전체 전력 ÷ (CPU 전력 + 가속기 전력)
    * `avg_efficiency_pct_per_watt`: 벤더별 1W당 사용률의 평균(%/W)
    * `accelerators`: 벤더별 전력 합계(W), 평균 사용률(%), 규격 최대 전력(W)
    * 카드 1장 평균의 규격 대비 비중(%), 1W당 사용률(%/W)

    참고

    * `pue_estimate` 는 냉방 전력을 뺀 근사치
    * 모든 값은 조회 시점 최신값 (수집 주기 5초 ~ 1분)
    """
    r = await power_efficiency()
    data = r["data"]
    return PowerEfficiencyResponse(
        status=r["status"],
        data=PowerEfficiencyData(
            pue_estimate=data.get("pue_estimate"),
            avg_efficiency_pct_per_watt=data.get("avg_efficiency_pct_per_watt"),
            accelerators=[AcceleratorEfficiency(**a) for a in data.get("accelerators", [])],
        ),
        warnings=r["warnings"],
    )


@router.get(
    "/monitoring/power/efficiency/timeseries",
    summary="가속기 전력 효율 시계열",
    response_model=PowerEfficiencyTimeseriesResponse,
)
async def get_power_efficiency_timeseries(request: Request, params: TimeseriesParams = Depends()):
    """가속기 전력 효율의 변화 추이 조회

    입력 예시

    * `GET /api/v2/monitoring/power/efficiency/timeseries`
    * `GET /api/v2/monitoring/power/efficiency/timeseries?period=24h&step=30m`

    입력 옵션

    * `period`: 조회 기간, 예 `30m`, `1h`, `7d` (기본 `1h`, 최대 10일)
    * `start`: 시작 시각, ISO 8601 (선택, 없으면 지금에서 `period` 만큼 이전)
    * `end`: 종료 시각, ISO 8601 (선택, 없으면 지금)
    * `step`: 데이터 점 간격, 예 `1m`, `5m`, `1h` (기본 `5m`)

    응답

    * `series`: 벤더별 (시각, 1W당 사용률 %/W) 쌍 목록
    * 시각은 ISO 8601 UTC
    * `vendor=all` 항목은 값이 있는 벤더들의 효율 평균

    참고

    * 산식은 벤더 평균 사용률(%) ÷ 카드 1장 평균 전력(W)
    * 사용률이 0이면 효율도 0
    * 서버 전체 전력은 포함하지 않음
    """
    start, end, step = time_range(params)

    r = await power_efficiency_timeseries(start, end, step)
    return PowerEfficiencyTimeseriesResponse(
        status=r["status"],
        series=[PowerEfficiencySeriesItem(**item) for item in r["data"]],
        warnings=r["warnings"],
    )


@router.get(
    "/monitoring/accelerators/utilization",
    summary="가속기 사용률",
    response_model=AcceleratorUtilizationResponse,
)
async def get_accelerator_utilization(
    request: Request,
    by: Optional[str] = Query(None, pattern="^node$", description="node 지정 시 호스트별 목록(hosts)도 받음"),
    limit: Optional[int] = Query(None, ge=1, description="hosts 최대 개수 (없으면 전체)"),
    sort_order: str = Query("desc", pattern="^(asc|desc)$", description="hosts 정렬 방향 (desc | asc, 기본 desc)"),
):
    """전체 가속기의 현재 사용률 조회

    입력 예시

    * `GET /api/v2/monitoring/accelerators/utilization`
    * `GET /api/v2/monitoring/accelerators/utilization?by=node&limit=5&sort_order=desc`

    입력 옵션

    * `by`: `node` 지정 시 호스트별 목록(`hosts`)도 반환 (선택)
    * `limit`: `hosts` 최대 개수 `1` 이상 (선택, 없으면 전체)
    * `sort_order`: `hosts` 정렬 방향 `desc` | `asc` (기본 `desc`)

    응답

    * `avg_utilization_pct`: 전체 카드 사용률 평균(%)
    * `card_count`: 값이 수집된 카드 수
    * `vendors`: 벤더별 카드 수, 평균 사용률(%)
    * `hosts`: 호스트별 벤더, 카드 수, 평균 사용률(%) (`by=node` 일 때만)
    * `hosts` 는 `sort_order` 순으로 정렬

    참고

    * 평균은 벤더 구분 없이 카드 1장씩 같은 비중
    * 모든 값은 조회 시점 최신값 (수집 주기 5초 ~ 1분)
    """
    r = await accelerator_utilization(by=by, limit=limit, sort_order=sort_order)
    data = r["data"]
    return AcceleratorUtilizationResponse(
        status=r["status"],
        data=AcceleratorUtilizationData(
            avg_utilization_pct=data.get("avg_utilization_pct"),
            card_count=data.get("card_count", 0),
            vendors=[VendorUtilization(**v) for v in data.get("vendors", [])],
            hosts=[HostUtilization(**h) for h in data.get("hosts", [])],
        ),
        warnings=r["warnings"],
    )


@router.get(
    "/monitoring/accelerators/utilization/timeseries",
    summary="가속기 사용률 시계열",
    response_model=AcceleratorUtilizationTimeseriesResponse,
)
async def get_accelerator_utilization_timeseries(
    request: Request,
    by: Optional[str] = Query(None, pattern="^node$", description="node 지정 시 호스트별 시계열(hosts)도 받음"),
    params: TimeseriesParams = Depends(),
):
    """전체 가속기 사용률의 변화 추이 조회

    입력 예시

    * `GET /api/v2/monitoring/accelerators/utilization/timeseries`
    * `GET /api/v2/monitoring/accelerators/utilization/timeseries?by=node&period=6h`

    입력 옵션

    * `by`: `node` 지정 시 호스트별 시계열(`hosts`)도 반환 (선택)
    * `period`: 조회 기간, 예 `30m`, `1h`, `7d` (기본 `1h`, 최대 10일)
    * `start`: 시작 시각, ISO 8601 (선택, 없으면 지금에서 `period` 만큼 이전)
    * `end`: 종료 시각, ISO 8601 (선택, 없으면 지금)
    * `step`: 데이터 점 간격, 예 `1m`, `5m`, `1h` (기본 `5m`)

    응답

    * `series`: 벤더별 (시각, 평균 사용률 %) 쌍 목록
    * `vendor=all` 항목은 카드 1장씩 같은 비중으로 계산한 평균
    * `hosts`: 호스트별 벤더와 (시각, 평균 사용률 %) 쌍 목록 (`by=node` 일 때만)
    * 시각은 ISO 8601 UTC

    참고

    * 값은 각 `step` 시점의 순간값 평균
    * `step` 사이의 짧은 변화는 반영되지 않음
    """
    r = await accelerator_utilization_timeseries(*time_range(params), by=by)
    return AcceleratorUtilizationTimeseriesResponse(
        status=r["status"],
        series=[UtilizationSeriesItem(**s) for s in r["data"]["series"]],
        hosts=[HostUtilizationSeriesItem(**h) for h in r["data"]["hosts"]],
        warnings=r["warnings"],
    )


@router.get("/monitoring/metrics/query", summary="메트릭 현재값", response_model=MetricsQueryResponse)
async def query_metrics(
    request: Request,
    metric: Optional[str] = Query(None, description="조회할 메트릭 이름 (허용 목록에 있는 값만)"),
):
    """허용된 메트릭 한 개의 현재 값 조회

    입력 예시

    * `GET /api/v2/monitoring/metrics/query?metric=gpu_temperature`

    입력 옵션

    * `metric`: 조회할 메트릭 이름, 허용 목록에 있는 값만 (필수)

    응답

    * `metric`: 조회한 메트릭 이름
    * `results`: 시리즈별 메트릭 라벨(`metric`)과 현재값(`value`)
    * `value` 는 (Unix 시각(초), 값 문자열) 쌍

    경고

    * `NO_DATA`: 결과 없음

    오류

    * 400: `metric` 누락 또는 허용 목록에 없는 이름
    """
    _check_metric(metric)

    results = await prometheus_client.instant(METRIC_ALLOWLIST[metric])

    warnings: list[str] = []
    if not results:
        warnings.append("NO_DATA")
        return MetricsQueryResponse(
            status="partial",
            metric=metric,
            results=[],
            warnings=warnings,
        )

    samples = [
        MetricSample(
            metric=item.get("metric", {}),
            value=(float(item["value"][0]), str(item["value"][1])),
        )
        for item in results
        if "value" in item
    ]

    return MetricsQueryResponse(
        status="success",
        metric=metric,
        results=samples,
        warnings=warnings,
    )


HOST_LABELS = ("Hostname", "hostname")  # by 값 중 호스트 묶음으로 처리할 것. 응답 라벨은 항상 hostname


def _grouped_query(query: str, by: Optional[str], aggregation: str, step: str) -> str:
    """by 라벨이 있으면 라벨별로 묶고 step 구간을 aggregation으로 요약하는 PromQL로 감싼다.

    range_query는 step 시점의 순간값만 돌려주므로, 히트맵처럼 구간 대표값이 필요하면
    `<agg>_over_time(...[step:])` 서브쿼리로 구간 전체를 요약해야 스파이크가 빠지지 않는다.
    """
    if not by:
        return query
    if by in HOST_LABELS:
        # 벤더마다 호스트 라벨 이름이 달라(NVIDIA Hostname, Rebellions hostname, Furiosa ETRI exporter node) 하나로 합친다.
        # shortcut: 한 시리즈에 두 라벨이 함께 있으면 값이 이어 붙음, 그런 exporter 가 생기면 우선순위 선택으로 교체
        query = f'label_join({query}, "hostname", "", "Hostname", "node", "hostname")'
        by = "hostname"
    return f"{aggregation} by ({by}) ({aggregation}_over_time(({query})[{step}:]))"


@router.get("/monitoring/metrics/timeseries", summary="메트릭 시계열", response_model=TimeseriesResponse)
async def get_metrics_timeseries(
    request: Request,
    metric: Optional[str] = Query(None, description="조회할 메트릭 이름 (허용 목록에 있는 값만)"),
    by: Optional[str] = Query(
        None,
        pattern="^(Hostname|hostname|cluster)$",
        description="묶는 기준 (hostname | cluster). hostname: 가속기가 꽂힌 컴퓨터별로 묶음. "
        "cluster: 클러스터별로 묶음. 없으면 묶지 않은 순간값",
    ),
    params: TimeseriesParams = Depends(),
):
    """허용된 메트릭 한 개의 변화 추이 조회

    입력 예시

    * `GET /api/v2/monitoring/metrics/timeseries?metric=gpu_temperature`
    * `GET /api/v2/monitoring/metrics/timeseries?metric=gpu_utilization&by=hostname&period=6h&aggregation=max`
    * `GET /api/v2/monitoring/metrics/timeseries?metric=furiosa_utilization&by=hostname&period=24h&step=1h`

    입력 옵션

    * `metric`: 조회할 메트릭 이름, 허용 목록에 있는 값만 (필수)
    * `by`: 묶는 기준 `hostname` | `cluster` (선택)
    * `hostname`: 가속기가 꽂힌 컴퓨터별로 묶음
    * `cluster`: 클러스터별로 묶음
    * `period`: 조회 기간, 예 `30m`, `1h`, `7d` (기본 `1h`, 최대 10일)
    * `start`: 시작 시각, ISO 8601 (선택, 없으면 지금에서 `period` 만큼 이전)
    * `end`: 종료 시각, ISO 8601 (선택, 없으면 지금)
    * `step`: 데이터 점 간격, 예 `1m`, `5m`, `1h` (기본 `5m`)
    * `aggregation`: 구간 집계 방식 `avg` | `min` | `max` | `sum` (기본 `avg`)

    응답

    * `metric`: 조회한 메트릭 이름
    * `series`: 메트릭 라벨(`metric`)별 (시각, 값) 쌍 목록(`values`)
    * 시각은 ISO 8601 UTC

    경고

    * `NO_DATA`: 결과 없음

    오류

    * 400: `metric` 누락 또는 허용 목록에 없는 이름

    참고

    * `by` 지정 시 그 라벨 값이 같은 시리즈를 묶고 각 `step` 구간을 `aggregation` 방식으로 요약
    * `by` 가 없으면 시리즈별 순간값
    * `by=hostname` 은 벤더마다 다른 호스트 라벨(`Hostname`, `hostname`, `node`)을 합쳐 묶고, 응답 라벨은 `hostname`
    """
    _check_metric(metric)

    start, end, step = time_range(params)

    results = await prometheus_client.range_query(
        _grouped_query(METRIC_ALLOWLIST[metric], by, params.aggregation, step), start, end, step
    )

    warnings: list[str] = []
    if not results:
        warnings.append("NO_DATA")
        return TimeseriesResponse(
            status="partial",
            metric=metric,
            series=[],
            warnings=warnings,
        )

    series = [
        MetricSeries(
            metric=item.get("metric", {}),
            values=[(ts_iso(v[0]), str(v[1])) for v in item.get("values", [])],
        )
        for item in results
    ]

    return TimeseriesResponse(
        status="success",
        metric=metric,
        series=series,
        warnings=warnings,
    )


@router.get(
    "/monitoring/temperature/timeseries",
    summary="가속기 온도 시계열",
    response_model=TemperatureTimeseriesResponse,
)
async def get_temperature_timeseries(request: Request, params: TimeseriesParams = Depends()):
    """전체 가속기 온도의 변화 추이 조회

    입력 예시

    * `GET /api/v2/monitoring/temperature/timeseries`
    * `GET /api/v2/monitoring/temperature/timeseries?period=6h&step=10m`

    입력 옵션

    * `period`: 조회 기간, 예 `30m`, `1h`, `7d` (기본 `1h`, 최대 10일)
    * `start`: 시작 시각, ISO 8601 (선택, 없으면 지금에서 `period` 만큼 이전)
    * `end`: 종료 시각, ISO 8601 (선택, 없으면 지금)
    * `step`: 데이터 점 간격, 예 `1m`, `5m`, `1h` (기본 `5m`)

    응답

    * `series`: 장치별 벤더(`nvidia` | `furiosa` | `rebellions`), 클러스터, 메트릭 라벨
    * 장치별 (시각, 온도 °C) 쌍 목록
    * 시각은 ISO 8601 UTC

    경고

    * `REBELLIONS_SCALE_CORRECTED_X1000`: 리벨리온 온도를 1000배 보정한 값
    * `NO_DATA_NVIDIA`: NVIDIA 온도 미수집
    * `NO_DATA_FURIOSA`: 퓨리오사 온도 미수집
    * `NO_DATA_REBELLIONS`: 리벨리온 온도 미수집
    """
    start, end, step = time_range(params)

    warnings: list[str] = []
    series: list[TemperatureSeriesItem] = []

    # (벤더, 클러스터, 쿼리). 클러스터가 None 이면 퓨리오사처럼 라벨값을 씀
    for vendor, cluster, query in (
        ("nvidia", "l40s", 'DCGM_FI_DEV_GPU_TEMP{cluster="l40s"}'),
        ("furiosa", None, 'furiosa_npu_hw_temperature{label="peak",cluster=~"furiosa.*"}'),
        # Rebellions NPU 온도 — exporter가 실측의 1/1000로 표출하므로 ×1000 보정
        # (rbln-stat 대조 확정, 2026-08-24)
        ("rebellions", "rebellions", '({__name__="RBLN_DEVICE_STATUS:TEMPERATURE",cluster="rebellions"}) * 1000'),
    ):
        results = await prometheus_client.range_query(query, start, end, step)
        if not results:
            warnings.append(f"NO_DATA_{vendor.upper()}")
        for item in results:
            series.append(
                TemperatureSeriesItem(
                    vendor=vendor,
                    cluster=cluster or item.get("metric", {}).get("cluster", "furiosa"),
                    metric_labels=item.get("metric", {}),
                    values=[(ts_iso(v[0]), str(v[1])) for v in item.get("values", [])],
                )
            )
    warnings.append("REBELLIONS_SCALE_CORRECTED_X1000")

    status = "success" if series else "partial"

    return TemperatureTimeseriesResponse(
        status=status,
        series=series,
        warnings=warnings,
    )


@router.get(
    "/monitoring/throttling/timeseries",
    summary="가속기 쓰로틀링 시계열",
    response_model=ThrottlingTimeseriesResponse,
)
async def get_throttling_timeseries(
    request: Request,
    cluster: Optional[str] = Query(None, description="클러스터 이름 (없으면 전체)"),
    node: Optional[str] = Query(None, description="노드 이름 (없으면 전체)"),
    acc_id: Optional[str] = Query(None, description="가속기 ID (없으면 전체)"),
    detail: bool = Query(False, description="true 면 카드별 시계열(series)도 받음 (기본 false, summary 만)"),
    bucket: Optional[str] = Query(
        None, pattern="^[1-9][0-9]*[smhd]$", description="timeline 을 묶을 구간 (예: 1h, step 이상, 없으면 step 단위)"
    ),
    params: TimeseriesParams = Depends(),
):
    """가속기가 스스로 속도를 낮춘(쓰로틀링) 시점과 횟수 조회

    입력 예시

    * `GET /api/v2/monitoring/throttling/timeseries`
    * `GET /api/v2/monitoring/throttling/timeseries?cluster=l40s&detail=true&period=6h`
    * `GET /api/v2/monitoring/throttling/timeseries?period=24h&step=5m&bucket=1h`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `l40s` (선택, 없으면 전체)
    * `node`: 노드 이름, 예 `gpu-node-1` (선택, 없으면 전체)
    * `acc_id`: 가속기 ID (선택, 없으면 전체)
    * `detail`: `true` 면 카드별 시계열(`series`)도 반환 (기본 `false`, `summary` 만)
    * `period`: 조회 기간, 예 `30m`, `1h`, `7d` (기본 `1h`, 최대 10일)
    * `start`: 시작 시각, ISO 8601 (선택, 없으면 지금에서 `period` 만큼 이전)
    * `end`: 종료 시각, ISO 8601 (선택, 없으면 지금)
    * `step`: 데이터 점 간격, 예 `1m`, `5m`, `1h` (기본 `5m`)
    * `bucket`: `timeline` 을 묶을 구간, 예 `1h`, `step` 이상 (선택, 없으면 `step` 단위)

    응답

    * `summary`: 대상 카드 수, 쓰로틀링 발생 카드 수, 시작 횟수 합계, 지속 시간 합계(분), 판정 간격(초)
    * `timeline`: 시각별 쓰로틀링 중인 카드 수, 시작 횟수, 지속 시간(분), `detail` 과 무관하게 포함
    * `series`: 카드별 벤더, 클러스터, 노드, 가속기 ID, (시각, `1` | `0`) 목록 (`detail=true` 일 때만)
    * `series` 에는 카드별 시작 횟수, 지속 시간(분)도 포함

    경고

    * `UNKNOWN_CLUSTER`: 없는 클러스터 이름, `summary` 는 `null`
    * `NO_DATA`: 대상 카드 없음, `summary` 는 `null`

    오류

    * 400: `bucket` 이 `step` 보다 짧음

    참고

    * 판정 기준: 2분 평균 사용률 90% 이상, 최근 10분 최고 온도 기준 이상, 전력이 15분 최대의 85% 미만
    * NVIDIA 는 클럭 제한 사유가 수집되면 그 값으로, Furiosa 는 장치가 보고하는 제한 사유로 직접 판정
    * 값이 빠진 시각은 점이 없음
    * `step` 이 길면 짧은 쓰로틀링을 놓칠 수 있음, 긴 구간 그래프는 `step` 을 짧게 두고 `bucket` 으로 묶는 편이 정확
    * `timeline` 의 `events` 합은 `summary.throttle_events` 와 같음
    * `period=24h&step=5m&bucket=1h` 이면 `timeline` 25점 (시작과 끝 시각 모두 포함)
    """
    start, end, step = time_range(params)
    step_seconds = parse_period(step).total_seconds()
    bucket_seconds = parse_period(bucket).total_seconds() if bucket else step_seconds
    if bucket_seconds < step_seconds:
        raise HTTPException(status_code=400, detail=f"bucket({bucket}) 은 step({step}) 이상이어야 함")

    series: list[ThrottlingSeriesItem] = []
    clusters = await cluster_discovery.get_clusters()
    if cluster and cluster not in clusters:
        return ThrottlingTimeseriesResponse(status="partial", warnings=["UNKNOWN_CLUSTER"])
    for name, info in clusters.items():
        if not info.vendor or (cluster and name != cluster):
            continue
        for s in await throttle_series(info.vendor, name, start, end, step, node=node, acc_id=acc_id):
            events, minutes = count_throttle(s["values"], step_seconds)
            series.append(ThrottlingSeriesItem(
                vendor=info.vendor, cluster=name, throttle_events=events, throttled_minutes=minutes, **s
            ))

    if not series:
        return ThrottlingTimeseriesResponse(status="partial", warnings=["NO_DATA"])
    summary = ThrottlingSummary(
        cards_total=len(series),
        cards_throttled=sum(1 for s in series if s.throttle_events),
        throttle_events=sum(s.throttle_events for s in series),
        throttled_minutes=sum(s.throttled_minutes for s in series),
        step_seconds=step_seconds,
    )
    return ThrottlingTimeseriesResponse(
        status="success",
        series=series if detail else [],
        summary=summary,
        timeline=_throttle_timeline(series, start, end, step_seconds, bucket_seconds),
    )


def _throttle_timeline(
    series: list[ThrottlingSeriesItem], start: str, end: str, step_seconds: float, bucket_seconds: float
) -> list[ThrottlingTimelinePoint]:
    """카드별 0/1 시계열을 시각별로 합산한다. 점이 없는 시각도 0으로 채워 그래프가 끊기지 않게 한다."""
    t0 = datetime.fromisoformat(start).timestamp()
    n = int((datetime.fromisoformat(end).timestamp() - t0) // step_seconds) + 1
    cards, events = [0] * n, [0] * n

    def index(t: float) -> Optional[int]:
        k = round((t - t0) / step_seconds)
        return k if 0 <= k < n else None

    for s in series:
        for iso, v in s.values:
            if v == "1" and (k := index(datetime.fromisoformat(iso).timestamp())) is not None:
                cards[k] += 1
        for t in throttle_starts(s.values, step_seconds):
            if (k := index(t)) is not None:
                events[k] += 1

    buckets: dict[int, list[int]] = {}
    for k in range(n):
        buckets.setdefault(int(k * step_seconds // bucket_seconds), []).append(k)
    return [
        ThrottlingTimelinePoint(
            time=ts_iso(t0 + b * bucket_seconds),
            throttled_cards=max(cards[k] for k in ks),
            events=sum(events[k] for k in ks),
            throttled_minutes=sum(cards[k] for k in ks) * step_seconds / 60,
        )
        for b, ks in buckets.items()
    ]


def _now_iso() -> str:
    return datetime.now(timezone.utc).isoformat()


def _sse_poll(request: Request, event: str, poll, *log_args) -> StreamingResponse:
    """heartbeat 다음 poll() 결과를 event 이름으로 보내기를 15초마다 반복하는 SSE. 조회 실패는 error 이벤트 후 계속."""
    async def generate():
        event_id = 0
        while True:
            if await request.is_disconnected():
                break

            event_id += 1
            yield f"id: {event_id}\nevent: heartbeat\ndata: {json.dumps({'observed_at': _now_iso()})}\n\n"

            try:
                payload = await poll()
                event_id += 1
                yield f"id: {event_id}\nevent: {event}\ndata: {json.dumps(payload, ensure_ascii=False)}\n\n"
            except Exception:
                logger.exception(*log_args)
                event_id += 1
                yield f"id: {event_id}\nevent: error\ndata: {json.dumps({'error': 'poll_failed', 'observed_at': _now_iso()})}\n\n"

            await asyncio.sleep(15)

    return StreamingResponse(generate(), media_type="text/event-stream")


@router.get("/monitoring/stream/power", summary="실시간 전력 스트림(SSE)")
async def stream_power(request: Request):
    """전력 요약값을 주기적으로 보내는 SSE 스트림

    입력 예시

    * `GET /api/v2/monitoring/stream/power`

    응답

    * `power` 이벤트: `status`, `warnings`, `observed_at`, `data`
    * `data` 에는 `server_total_watts`, `cpu_total_watts`, `accelerator_total_watts`, `accelerator_by_vendor`, `other_watts` (단위 W)
    * `heartbeat` 이벤트: 15초마다 `observed_at` 전송
    * `error` 이벤트: 조회 실패 시 `error="poll_failed"`, `observed_at` 전송 후 스트림 유지

    참고

    * 15초 간격으로 전송
    * 각 이벤트에 연결 이후 1부터 증가하는 `id` 포함
    """
    async def poll() -> dict:
        r = await power_summary()
        return {"status": r["status"], "data": r["data"], "warnings": r["warnings"], "observed_at": _now_iso()}

    return _sse_poll(request, "power", poll, "SSE power poll error")


@router.get("/monitoring/stream/metrics", summary="실시간 메트릭 스트림(SSE)")
async def stream_metrics(
    request: Request,
    metric: Optional[str] = Query(None, description="조회할 메트릭 이름 (허용 목록에 있는 값만)"),
):
    """허용된 메트릭 값을 주기적으로 보내는 SSE 스트림

    입력 예시

    * `GET /api/v2/monitoring/stream/metrics?metric=gpu_temperature`

    입력 옵션

    * `metric`: 조회할 메트릭 이름, 허용 목록에 있는 값만 (필수)

    응답

    * `metric` 이벤트: `status`, `metric`(메트릭 이름), `observed_at`, `results`
    * `results` 는 시리즈별 라벨(`metric`)과 `value`(Unix 시각(초), 값 문자열)
    * `heartbeat` 이벤트: 15초마다 `observed_at` 전송
    * `error` 이벤트: 조회 실패 시 `error="poll_failed"`, `observed_at` 전송 후 스트림 유지

    오류

    * 400: `metric` 누락 또는 허용 목록에 없는 이름

    참고

    * 15초 간격으로 전송
    """
    if metric is None or metric not in METRIC_ALLOWLIST:
        available = list(METRIC_ALLOWLIST.keys())
        raise HTTPException(
            status_code=400,
            detail=f"metric 파라미터 필수(허용 목록). 사용 가능: {available}",
        )

    async def poll() -> dict:
        results = await prometheus_client.instant(METRIC_ALLOWLIST[metric])
        samples = [
            {
                "metric": item.get("metric", {}),
                "value": [item["value"][0], item["value"][1]],
            }
            for item in results
            if "value" in item
        ]
        return {
            "status": "success" if samples else "partial",
            "metric": metric,
            "results": samples,
            "observed_at": _now_iso(),
        }

    return _sse_poll(request, "metric", poll, "SSE metric poll error for %s", metric)
