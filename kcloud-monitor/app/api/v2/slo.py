"""LLM 서빙 SLO(TTFT·TPOT) 라우터

vLLM 이 내보내는 히스토그램을 Prometheus 에서 읽어 분위수, 목표 달성률, 남은 오류 예산을 계산.
서빙 하나는 (cluster, model_name) 라벨 쌍으로 구분.
"""
import asyncio
import math
from collections import defaultdict
from datetime import datetime, timezone
from typing import Optional

from fastapi import APIRouter, Depends, Query, Request

from app.api.v2.deps import TimeseriesParams, parse_period
from app.schemas.slo import (
    LatencySlo,
    SloLoad,
    SloPoint,
    SloService,
    SloServiceListResponse,
    SloServiceResponse,
    SloThroughput,
    SloTimeseriesResponse,
)
from app.services.prometheus import esc, prometheus_client

router = APIRouter()

TTFT = "vllm:time_to_first_token_seconds"
TPOT = "vllm:request_time_per_output_token_seconds"  # 요청별 평균. 토큰 간격 단위는 inter_token_latency_seconds
_BY = "cluster, model_name"

_WINDOW = Query("1h", pattern=r"^\d+[smhdw]$", description="계산 기간 (예: 5m, 1h, 1d, 최대 10일, 기본 1h)")
_TTFT_TARGET = Query(500.0, gt=0, description="TTFT 목표값(ms) (기본 500)")
_TPOT_TARGET = Query(50.0, gt=0, description="TPOT 목표값(ms) (기본 50)")
_OBJECTIVE = Query(99.0, gt=0, lt=100, description="달성 목표(%) (기본 99)")


def _selector(cluster: Optional[str] = None, model: Optional[str] = None) -> str:
    parts = []
    if cluster:
        parts.append(f'cluster="{esc(cluster)}"')
    if model:
        parts.append(f'model_name="{esc(model)}"')
    return "{" + ",".join(parts) + "}"


def _key(metric: dict) -> tuple[str, str]:
    return metric.get("cluster", ""), metric.get("model_name", "")


def _num(value) -> Optional[float]:
    f = float(value)
    return None if math.isnan(f) or math.isinf(f) else f


def _ms(value: Optional[float]) -> Optional[float]:
    return None if value is None else round(value * 1000, 2)


def _attainment(buckets: list[tuple[float, float]], target_s: float) -> Optional[float]:
    """누적 히스토그램 구간에서 target_s 이하로 끝난 요청 비율(%). 구간 사이는 직선 보간."""
    pts = sorted(buckets)
    total = pts[-1][1] if pts else 0.0
    if total <= 0:
        return None
    prev_le, prev_c = 0.0, 0.0
    for le, c in pts:
        if target_s <= le:
            # 마지막 유한 구간보다 큰 목표: 넘친 요청의 실제 시간을 몰라 마지막 유한 구간까지만 셈
            done = prev_c if math.isinf(le) else prev_c + (c - prev_c) * (target_s - prev_le) / (le - prev_le)
            return round(done / total * 100, 2)
        prev_le, prev_c = le, c
    return 100.0


def _budget(attainment: Optional[float], objective: float) -> Optional[float]:
    if attainment is None:
        return None
    return round(100 - (100 - attainment) / (100 - objective) * 100, 2)


async def _collect(sel: str, window: str) -> dict[str, list[dict]]:
    """서빙별 SLO 계산에 필요한 PromQL 을 한꺼번에 실행."""
    queries = {
        "nodes": f"group by ({_BY}, instance) (vllm:num_requests_running{sel})",
        "rps": f"sum by ({_BY}) (rate(vllm:request_success_total{sel}[{window}]))",
        "tps": f"sum by ({_BY}) (rate(vllm:generation_tokens_total{sel}[{window}]))",
        "running": f"sum by ({_BY}) (vllm:num_requests_running{sel})",
        "waiting": f"sum by ({_BY}) (vllm:num_requests_waiting{sel})",
        "kv": f"max by ({_BY}) (vllm:kv_cache_usage_perc{sel}) * 100",
        "preemptions": f"sum by ({_BY}) (increase(vllm:num_preemptions_total{sel}[{window}]))",
    }
    for name, metric in (("ttft", TTFT), ("tpot", TPOT)):
        rate = f"sum by (le, {_BY}) (rate({metric}_bucket{sel}[{window}]))"
        for q in (50, 95, 99):
            queries[f"{name}_p{q}"] = f"histogram_quantile(0.{q}, {rate})"
        queries[f"{name}_buckets"] = f"sum by (le, {_BY}) (increase({metric}_bucket{sel}[{window}]))"

    names = list(queries)
    results = await asyncio.gather(*(prometheus_client.instant(queries[n]) for n in names))
    return dict(zip(names, results))


def _build(raw: dict[str, list[dict]], window: str, ttft_target: float, tpot_target: float,
           objective: float) -> list[SloService]:
    nodes: dict[tuple, set] = defaultdict(set)
    for r in raw["nodes"]:
        nodes[_key(r["metric"])].add(r["metric"].get("instance", ""))

    scalar: dict[str, dict[tuple, Optional[float]]] = {}
    for name, rows in raw.items():
        if name != "nodes" and not name.endswith("_buckets"):
            scalar[name] = {_key(r["metric"]): _num(r["value"][1]) for r in rows}

    buckets: dict[str, dict[tuple, list]] = {"ttft": defaultdict(list), "tpot": defaultdict(list)}
    for name in buckets:
        for r in raw[f"{name}_buckets"]:
            buckets[name][_key(r["metric"])].append((float(r["metric"]["le"]), float(r["value"][1])))

    def latency(name: str, key: tuple, target_ms: float) -> LatencySlo:
        att = _attainment(buckets[name].get(key, []), target_ms / 1000)
        return LatencySlo(
            p50_ms=_ms(scalar[f"{name}_p50"].get(key)),
            p95_ms=_ms(scalar[f"{name}_p95"].get(key)),
            p99_ms=_ms(scalar[f"{name}_p99"].get(key)),
            target_ms=target_ms,
            attainment_percent=att,
            error_budget_remaining_percent=_budget(att, objective),
        )

    items = []
    for key in sorted(nodes):
        ttft, tpot = latency("ttft", key, ttft_target), latency("tpot", key, tpot_target)
        atts = [a for a in (ttft.attainment_percent, tpot.attainment_percent) if a is not None]
        status = "no_traffic" if not atts else "violated" if min(atts) < objective else "met"
        kv, rps, tps = scalar["kv"].get(key), scalar["rps"].get(key), scalar["tps"].get(key)
        items.append(SloService(
            cluster=key[0],
            model=key[1],
            nodes=sorted(nodes[key]),
            window=window,
            objective_percent=objective,
            slo_status=status,
            ttft=ttft,
            tpot=tpot,
            throughput=SloThroughput(
                requests_per_sec=None if rps is None else round(rps, 3),
                tokens_per_sec=None if tps is None else round(tps, 2),
            ),
            load=SloLoad(
                running=scalar["running"].get(key),
                waiting=scalar["waiting"].get(key),
                kv_cache_usage_percent=None if kv is None else round(kv, 2),
                preemptions=scalar["preemptions"].get(key),
            ),
        ))
    return items


def _window(window: str) -> str:
    """PromQL 범위 문자열. 보관 기간 10일 상한을 적용하려고 초 단위로 변환."""
    return f"{int(parse_period(window).total_seconds())}s"


@router.get("/slo/services", summary="LLM 서빙 SLO 목록", response_model=SloServiceListResponse)
async def list_slo_services(
    request: Request,
    cluster: Optional[str] = Query(None, description="클러스터 이름 (없으면 전체)"),
    window: str = _WINDOW,
    ttft_target_ms: float = _TTFT_TARGET,
    tpot_target_ms: float = _TPOT_TARGET,
    objective_percent: float = _OBJECTIVE,
):
    """LLM 서빙별 응답 속도 약속(SLO) 지킴 현황 목록

    입력 예시

    * `GET /api/v2/slo/services`
    * `GET /api/v2/slo/services?cluster=l40s&window=5m`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `l40s` (선택, 없으면 전체)
    * `window`: 계산 기간, 예 `5m`, `1h`, `1d` (기본 `1h`, 최대 10일)
    * `ttft_target_ms`: 첫 글자까지 걸린 시간(TTFT)의 목표값, ms (기본 `500`)
    * `tpot_target_ms`: 글자 하나당 걸린 시간(TPOT)의 목표값, ms (기본 `50`)
    * `objective_percent`: 목표값 안에 끝나야 하는 요청 비율, % (기본 `99`)

    응답

    * `items`: 서빙별 클러스터, 모델, 노드 목록, SLO 판정(`met` | `violated` | `no_traffic`)
    * `ttft`, `tpot`: p50, p95, p99(ms), 목표값, 달성률(%), 남은 오류 예산(%)
    * `throughput`: 초당 요청 수, 초당 생성 토큰 수
    * `load`: 처리 중 요청 수, 대기 요청 수, KV 캐시 사용률(%), 밀어낸 횟수
    * 클러스터, 모델 이름순으로 정렬

    경고

    * `NO_DATA`: 수집 중인 vLLM 서빙 없음

    참고

    * 데이터 출처는 vLLM 메트릭 `vllm:time_to_first_token_seconds`, `vllm:request_time_per_output_token_seconds`
    * 달성률은 히스토그램 구간 사이를 직선으로 보간한 근사치
    """
    raw = await _collect(_selector(cluster), _window(window))
    items = _build(raw, window, ttft_target_ms, tpot_target_ms, objective_percent)
    if not items:
        return SloServiceListResponse(status="partial", warnings=["NO_DATA"])
    return SloServiceListResponse(status="success", items=items, total=len(items))


@router.get("/slo/services/{cluster}/{model}", summary="LLM 서빙 SLO 상세", response_model=SloServiceResponse)
async def get_slo_service(
    request: Request,
    cluster: str,
    model: str,
    window: str = _WINDOW,
    ttft_target_ms: float = _TTFT_TARGET,
    tpot_target_ms: float = _TPOT_TARGET,
    objective_percent: float = _OBJECTIVE,
):
    """LLM 서빙 하나의 응답 속도 약속(SLO) 지킴 현황 조회

    입력 예시

    * `GET /api/v2/slo/services/l40s/qwen2.5-3b`
    * `GET /api/v2/slo/services/l40s/qwen2.5-3b?window=1d&ttft_target_ms=300`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `l40s` (필수, 경로)
    * `model`: vLLM 실행 시 지정한 모델 이름, 예 `qwen2.5-3b` (필수, 경로)
    * `window`: 계산 기간, 예 `5m`, `1h`, `1d` (기본 `1h`, 최대 10일)
    * `ttft_target_ms`: 첫 글자까지 걸린 시간(TTFT)의 목표값, ms (기본 `500`)
    * `tpot_target_ms`: 글자 하나당 걸린 시간(TPOT)의 목표값, ms (기본 `50`)
    * `objective_percent`: 목표값 안에 끝나야 하는 요청 비율, % (기본 `99`)

    응답

    * `data`: 클러스터, 모델, 노드 목록, SLO 판정(`met` | `violated` | `no_traffic`)
    * `ttft`, `tpot`: p50, p95, p99(ms), 목표값, 달성률(%), 남은 오류 예산(%)
    * `throughput`: 초당 요청 수, 초당 생성 토큰 수
    * `load`: 처리 중 요청 수, 대기 요청 수, KV 캐시 사용률(%), 밀어낸 횟수

    경고

    * `SLO_SERVICE_NOT_FOUND`: 해당 클러스터와 모델의 vLLM 메트릭 없음, `data` 는 `null`
    """
    raw = await _collect(_selector(cluster, model), _window(window))
    items = _build(raw, window, ttft_target_ms, tpot_target_ms, objective_percent)
    if not items:
        return SloServiceResponse(status="partial", warnings=["SLO_SERVICE_NOT_FOUND"])
    return SloServiceResponse(status="success", data=items[0])


@router.get(
    "/slo/services/{cluster}/{model}/timeseries",
    summary="LLM 서빙 SLO 시계열",
    response_model=SloTimeseriesResponse,
)
async def get_slo_timeseries(
    request: Request,
    cluster: str,
    model: str,
    ttft_target_ms: float = _TTFT_TARGET,
    tpot_target_ms: float = _TPOT_TARGET,
    params: TimeseriesParams = Depends(),
):
    """LLM 서빙 하나의 TTFT p95, TPOT p95 변화 추이와 목표 초과 구간 조회

    입력 예시

    * `GET /api/v2/slo/services/l40s/qwen2.5-3b/timeseries`
    * `GET /api/v2/slo/services/l40s/qwen2.5-3b/timeseries?period=6h&step=10m`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `l40s` (필수, 경로)
    * `model`: vLLM 실행 시 지정한 모델 이름, 예 `qwen2.5-3b` (필수, 경로)
    * `ttft_target_ms`: TTFT 목표값, ms (기본 `500`)
    * `tpot_target_ms`: TPOT 목표값, ms (기본 `50`)
    * `period`: 조회 기간, 예 `30m`, `1h`, `7d` (기본 `1h`, 최대 10일)
    * `start`: 시작 시각, ISO 8601 (선택, 없으면 지금에서 `period` 만큼 이전)
    * `end`: 종료 시각, ISO 8601 (선택, 없으면 지금)
    * `step`: 데이터 점 간격, 예 `1m`, `5m`, `1h` (기본 `5m`)

    응답

    * `points`: 시각, TTFT p95(ms), TPOT p95(ms), 목표 초과 여부
    * `violated_points`: 목표를 넘은 점 개수
    * 시각순으로 정렬, 시각은 ISO 8601 UTC

    경고

    * `NO_DATA`: 기간 안에 요청 없음

    참고

    * 점마다 직전 `step` 동안 들어온 요청으로 p95 계산 (`step` 이 1분보다 짧으면 1분)
    * `aggregation` 옵션은 쓰지 않음
    """
    now = datetime.now(timezone.utc)
    start, end, step = params.start_iso(now), params.end_iso(now), params.step
    rw = f"{max(int(parse_period(step).total_seconds()), 60)}s"
    sel = _selector(cluster, model)

    def p95(metric: str) -> str:
        return f"histogram_quantile(0.95, sum by (le) (rate({metric}_bucket{sel}[{rw}])))"

    ttft_rows, tpot_rows = await asyncio.gather(
        prometheus_client.range_query(p95(TTFT), start, end, step),
        prometheus_client.range_query(p95(TPOT), start, end, step),
    )
    merged: dict[float, dict] = defaultdict(dict)
    for field, rows in (("ttft", ttft_rows), ("tpot", tpot_rows)):
        for row in rows:
            for ts, v in row.get("values", []):
                merged[float(ts)][field] = _ms(_num(v))

    points = []
    for ts in sorted(merged):
        ttft, tpot = merged[ts].get("ttft"), merged[ts].get("tpot")
        points.append(SloPoint(
            time=datetime.fromtimestamp(ts, tz=timezone.utc).isoformat(),
            ttft_p95_ms=ttft,
            tpot_p95_ms=tpot,
            violated=(ttft is not None and ttft > ttft_target_ms) or (tpot is not None and tpot > tpot_target_ms),
        ))

    has_data = any(p.ttft_p95_ms is not None or p.tpot_p95_ms is not None for p in points)
    return SloTimeseriesResponse(
        status="success" if has_data else "partial",
        cluster=cluster,
        model=model,
        ttft_target_ms=ttft_target_ms,
        tpot_target_ms=tpot_target_ms,
        points=points,
        violated_points=sum(p.violated for p in points),
        warnings=[] if has_data else ["NO_DATA"],
    )
