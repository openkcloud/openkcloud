"""
KCloud Monitor v2 — 전력(Power) 도메인 서비스.

전력 3계층은 합산이 아니라 포함관계다: 서버총전력(IPMI) ⊇ CPU/RAM(Kepler) + 가속기 + 기타.
  - 서버총전력: `ipmi_dcmi_power_consumption_watts` — 라벨 `node`(cluster 없음).
  - CPU/RAM:    `kepler_node_cpu_watts` — 라벨 `node_name`(cluster 없음), `zone` 라벨.
                zone은 psys ⊇ package ⊇ core 포함관계라 단순 합산하면 중복 계산된다.
                psys가 있으면 그 값만 쓰고, 없으면 package(+dram)을 합산한다.
  - 가속기(W):  벤더별 ACCEL_POWER_QUERIES 참고.

rebellions 콜론 메트릭 주의: PromQL 파서가 `RBLN_DEVICE_STATUS:CARD_POWER{...}` 원형을
거부하므로 반드시 `{__name__="RBLN_DEVICE_STATUS:CARD_POWER",cluster="rebellions"}` 형식을
사용한다.

각 서비스 함수는 `{"status": str, "data": <dict|list>, "warnings": list[str]}` 형태로 반환한다.
"""
import asyncio
from typing import Optional

from app.config import npu_util
from app.services.cluster_discovery import DEFAULT_CLUSTER_NAME
from app.services.prometheus import first_finite, parse_finite, prometheus_client, ts_iso

IPMI_POWER_METRIC = "ipmi_dcmi_power_consumption_watts"

ACCEL_POWER_QUERIES = {
    # cluster=~"l40s|": L40S 2장 — compute5 카드(VM push, cluster="l40s") + compute6 카드
    # (호스트 직접 수집, cluster 라벨 없음). 빈 매치 정규식이 무라벨 시계열을 포함시킨다.
    "nvidia": 'DCGM_FI_DEV_POWER_USAGE{cluster=~"l40s|"}',
    # cluster=~"furiosa.*": 퓨리오사 클러스터는 재생성될 때마다 늘어난다(furiosa, furiosa-1348 ...).
    # 라벨을 박아두면 클러스터 재배포마다 조회가 깨지므로 접두사로 잡는다.
    "furiosa": 'furiosa_npu_hw_power{cluster=~"furiosa.*"}',
    # ×1000: exporter가 실측의 1/1000로 표출 (rbln-stat 카드 실측 18.4W와 대조 확정, 2026-08-24)
    "rebellions": '({__name__="RBLN_DEVICE_STATUS:CARD_POWER",cluster="rebellions"}) * 1000',
}

ACCEL_UTIL_QUERIES = {  # 효율·사용률용, %. 카드 1장당 시계열 1개
    # 공통 지표 표(가속기 목록 API)와 같은 GPU_UTIL. compute6 카드(cluster 라벨 없음)도 포함
    "nvidia": 'DCGM_FI_DEV_GPU_UTIL{cluster=~"l40s|"}',
    # 코어(8개)별 시계열이라 카드 단위로 평균
    "furiosa": npu_util('avg by (cluster,device,uuid,instance,hostname) (furiosa_npu_core_utilization{cluster=~"furiosa.*"})', "furiosa"),
    "rebellions": npu_util('{__name__="RBLN_DEVICE_STATUS:UTILIZATION",cluster="rebellions"}', "rebellions"),
}

# per-card 공식 스펙: L40S 350W / Furiosa RNGD 180W / Rebellions ATOM+ RBLN-CA22 85W
# (CA22는 rbln-stat 실측 모델. CA12는 TDP 60~130W 가변인 다른 모델이라 해당 없음)
KNOWN_TDP_WATTS = {"nvidia": 350.0, "furiosa": 180.0, "rebellions": 85.0}


# ---------------------------------------------------------------------------
# 내부 헬퍼
# ---------------------------------------------------------------------------

def _sum_values(results: list[dict]) -> Optional[float]:
    """instant 쿼리 결과 각 항목의 value를 합산. 유효 값이 하나도 없으면 None."""
    if not results:
        return None
    total = 0.0
    found = False
    for item in results:
        try:
            raw = item["value"][1]
        except (KeyError, IndexError, TypeError):
            continue
        val = parse_finite(raw)
        if val is None:
            continue
        total += val
        found = True
    return total if found else None


def _range_values(results: list[dict]) -> list[tuple[str, str]]:
    """range 쿼리 결과 첫 시리즈의 values를 (ISO 8601 UTC 시각, raw_str) 목록으로 변환.

    다른 시계열 API(온도·가속기 전력 등)와 같은 형식으로 맞춘다.
    """
    if not results:
        return []
    values = results[0].get("values", [])
    out: list[tuple[str, str]] = []
    for v in values:
        try:
            ts = ts_iso(v[0])
            raw = str(v[1])
        except (IndexError, TypeError, ValueError, OverflowError, OSError):
            continue
        out.append((ts, raw))
    return out


def _accel_sum_expr() -> str:
    """세 벤더 가속기 전력 합산 PromQL 표현. 벤더별 시리즈가 없어도(or vector(0)) 전체 합이 유지된다."""
    parts = [f"(sum({query}) or vector(0))" for query in ACCEL_POWER_QUERIES.values()]
    return " + ".join(parts)


def _kepler_by_node(results: list[dict]) -> dict[str, float]:
    """`kepler_node_cpu_watts` instant 조회 결과 → node_name별 CPU 전력.
    zone=psys 있으면 그 값, 없으면 package(+dram) 합.

    zone은 psys ⊇ package ⊇ core 포함관계이므로 단순 합산은 중복 계산된다.
    """
    zones_by_node: dict[str, dict[str, float]] = {}
    for item in results:
        metric = item.get("metric", {})
        node = metric.get("node_name")
        zone = metric.get("zone")
        if not node or not zone:
            continue
        val = first_finite([item])
        if val is None:
            continue
        zones_by_node.setdefault(node, {})[zone] = val
    return {
        node: zones["psys"] if "psys" in zones else zones.get("package", 0.0) + zones.get("dram", 0.0)
        for node, zones in zones_by_node.items()
    }


def _kepler_total_watts(results: list[dict]) -> Optional[float]:
    """_kepler_by_node 의 전 노드 합산. 노드가 없으면 None."""
    by_node = _kepler_by_node(results)
    if not by_node:
        return None
    # sum() 은 3.12부터 보정 합산이라 결과가 달라질 수 있어 앞에서부터 더함
    total = 0.0
    for watts in by_node.values():
        total += watts
    return total


def _status_from_warnings(warnings: list[str]) -> str:
    """NO_DATA* 경고가 하나라도 있으면 partial, 아니면 success."""
    return "partial" if any(w.startswith("NO_DATA") for w in warnings) else "success"


# ---------------------------------------------------------------------------
# 서비스 함수
# ---------------------------------------------------------------------------

async def power_summary() -> dict:
    """시스템 전체 전력 요약 — 서버총전력(IPMI)/CPU(Kepler)/가속기 벤더별/기타(server-known)."""
    warnings: list[str] = []

    ipmi_results = await prometheus_client.instant(IPMI_POWER_METRIC)
    server_total_watts = _sum_values(ipmi_results)
    if server_total_watts is None:
        warnings.append("NO_DATA_SERVER_POWER")

    kepler_results = await prometheus_client.instant("kepler_node_cpu_watts")
    cpu_total_watts = _kepler_total_watts(kepler_results)
    if cpu_total_watts is None:
        warnings.append("NO_DATA_CPU_POWER")

    accelerator_by_vendor: dict[str, Optional[float]] = {}
    any_accel = False
    for vendor, query in ACCEL_POWER_QUERIES.items():
        results = await prometheus_client.instant(query)
        watts = _sum_values(results)
        accelerator_by_vendor[vendor] = watts
        if watts is not None:
            any_accel = True
    if not any_accel:
        warnings.append("NO_DATA_ACCELERATOR")

    accelerator_total_watts: Optional[float] = None
    if any_accel:
        accelerator_total_watts = sum(v for v in accelerator_by_vendor.values() if v is not None)

    other_watts: Optional[float] = None
    if server_total_watts is not None:
        known = (cpu_total_watts or 0.0) + (accelerator_total_watts or 0.0)
        other_watts = server_total_watts - known
        if other_watts < 0:
            other_watts = 0.0
            warnings.append("OTHER_WATTS_NEGATIVE_CLAMPED")

    data = {
        "server_total_watts": server_total_watts,
        "cpu_total_watts": cpu_total_watts,
        "accelerator_total_watts": accelerator_total_watts,
        "accelerator_by_vendor": accelerator_by_vendor,
        "other_watts": other_watts,
    }

    return {"status": _status_from_warnings(warnings), "data": data, "warnings": warnings}


async def power_breakdown(dimension: str) -> dict:
    """전력 분해 — dimension별(vendor/cluster/node/accelerator) 차원 기여도 목록."""
    warnings: list[str] = []
    items: list[dict] = []

    if dimension == "vendor":
        for vendor, query in ACCEL_POWER_QUERIES.items():
            results = await prometheus_client.instant(query)
            watts = _sum_values(results)
            if watts is None:
                warnings.append(f"NO_DATA_{vendor.upper()}")
            items.append({"key": vendor, "watts": watts, "layer": "accelerator"})

    elif dimension == "cluster":
        cluster_totals: dict[str, float] = {}
        for vendor, query in ACCEL_POWER_QUERIES.items():
            results = await prometheus_client.instant(query)
            for item in results:
                # cluster 라벨이 없는 카드는 관리 클러스터 장비(예: compute6 L40S)
                cluster = item.get("metric", {}).get("cluster") or DEFAULT_CLUSTER_NAME
                val = first_finite([item])
                if val is None:
                    continue
                cluster_totals[cluster] = cluster_totals.get(cluster, 0.0) + val
        if not cluster_totals:
            warnings.append("NO_DATA")
        for cluster, watts in cluster_totals.items():
            items.append({"key": cluster, "watts": watts, "layer": "accelerator"})

    elif dimension == "node":
        ipmi_results = await prometheus_client.instant(IPMI_POWER_METRIC)
        if not ipmi_results:
            warnings.append("NO_DATA_SERVER_POWER")
        for item in ipmi_results:
            node = item.get("metric", {}).get("node", "unknown")
            val = first_finite([item])
            items.append({"key": node, "watts": val, "layer": "server"})

        by_node = _kepler_by_node(await prometheus_client.instant("kepler_node_cpu_watts"))
        if not by_node:
            warnings.append("NO_DATA_CPU_POWER")
        for node, watts in by_node.items():
            items.append({"key": node, "watts": watts, "layer": "cpu"})

    elif dimension == "accelerator":
        any_data = False
        for vendor, query in ACCEL_POWER_QUERIES.items():
            results = await prometheus_client.instant(query)
            for item in results:
                metric = item.get("metric", {})
                gpu = metric.get("gpu") or metric.get("device") or metric.get("name") or ""
                cluster = metric.get("cluster") or DEFAULT_CLUSTER_NAME
                key = "/".join(p for p in (_card_host(metric), gpu, cluster) if p)
                val = first_finite([item])
                if val is None:
                    continue
                any_data = True
                items.append({"key": key, "watts": val, "layer": "accelerator"})
        if not any_data:
            warnings.append("NO_DATA")

    else:
        warnings.append("UNKNOWN_DIMENSION")
        return {"status": "partial", "data": [], "warnings": warnings}

    return {"status": _status_from_warnings(warnings), "data": items, "warnings": warnings}


async def power_timeseries(start: str, end: str, step: str) -> dict:
    """시스템 전력 시계열 — server/cpu/accelerator 계층별 sum(...) range 쿼리."""
    warnings: list[str] = []
    layers: list[dict] = []

    server_results = await prometheus_client.range_query(f"sum({IPMI_POWER_METRIC})", start, end, step)
    server_values = _range_values(server_results)
    if not server_values:
        warnings.append("NO_DATA_SERVER_POWER")
    layers.append({"layer": "server", "values": server_values})

    # 단순화(허용): zone="psys" 노드만 반영 — package-only 노드(마스터 등)는 시계열 합계에서
    # 누락될 수 있다(zone 포함관계 특성상 psys/package를 동일 sum()으로 안전하게 합칠 수 없음).
    cpu_results = await prometheus_client.range_query(
        'sum(kepler_node_cpu_watts{zone="psys"})', start, end, step
    )
    cpu_values = _range_values(cpu_results)
    if not cpu_values:
        warnings.append("NO_DATA_CPU_POWER")
    layers.append({"layer": "cpu", "values": cpu_values})

    accel_results = await prometheus_client.range_query(_accel_sum_expr(), start, end, step)
    accel_values = _range_values(accel_results)
    if not accel_values:
        warnings.append("NO_DATA_ACCELERATOR")
    layers.append({"layer": "accelerator", "values": accel_values})

    return {"status": _status_from_warnings(warnings), "data": layers, "warnings": warnings}


async def power_efficiency() -> dict:
    """전력 효율 — PUE 추정(냉각 미측정 근사) + 가속기별 사용률 대비 전력/TDP 비율."""
    warnings: list[str] = ["PUE_COOLING_UNMEASURED_ESTIMATE"]

    ipmi_results = await prometheus_client.instant(IPMI_POWER_METRIC)
    server_total_watts = _sum_values(ipmi_results)

    kepler_results = await prometheus_client.instant("kepler_node_cpu_watts")
    cpu_total_watts = _kepler_total_watts(kepler_results)

    accelerators: list[dict] = []
    accel_total = 0.0
    accel_found = False
    efficiencies: list[float] = []

    for vendor, power_query in ACCEL_POWER_QUERIES.items():
        power_results = await prometheus_client.instant(power_query)
        power_watts = _sum_values(power_results)

        util_results = await prometheus_client.instant(ACCEL_UTIL_QUERIES[vendor])
        util_values = [v for v in (first_finite([item]) for item in util_results) if v is not None]
        utilization_pct = sum(util_values) / len(util_values) if util_values else None

        avg_power = power_watts / (len(power_results) or 1) if power_watts is not None else None
        tdp_watts = KNOWN_TDP_WATTS.get(vendor)
        tdp_ratio_pct: Optional[float] = None
        if tdp_watts is None:
            if "TDP_UNVERIFIED" not in warnings:
                warnings.append("TDP_UNVERIFIED")
        elif avg_power is not None:
            tdp_ratio_pct = (avg_power / tdp_watts) * 100

        # 사용률 ÷ 카드 1장 평균 전력. 카드 크기가 달라 벤더 간 비교용으로는 부정확
        efficiency: Optional[float] = None
        if utilization_pct is not None and avg_power:
            efficiency = utilization_pct / avg_power
            efficiencies.append(efficiency)

        if power_watts is not None:
            accel_total += power_watts
            accel_found = True

        accelerators.append(
            {
                "vendor": vendor,
                "power_watts": power_watts,
                "utilization_pct": utilization_pct,
                "tdp_watts": tdp_watts,
                "tdp_ratio_pct": tdp_ratio_pct,
                "efficiency_pct_per_watt": efficiency,
            }
        )

    pue_estimate: Optional[float] = None
    denominator = (cpu_total_watts or 0.0) + (accel_total if accel_found else 0.0)
    if server_total_watts is not None and denominator > 0:
        pue_estimate = server_total_watts / denominator
    else:
        warnings.append("NO_DATA_PUE")

    data = {
        "pue_estimate": pue_estimate,
        "avg_efficiency_pct_per_watt": sum(efficiencies) / len(efficiencies) if efficiencies else None,
        "accelerators": accelerators,
    }

    return {"status": _status_from_warnings(warnings), "data": data, "warnings": warnings}


async def power_efficiency_timeseries(start: str, end: str, step: str) -> dict:
    """가속기 전력 효율 시계열 — 벤더별 (평균 사용률 ÷ 카드 1장 평균 전력)의 시간 변화.

    power_efficiency()의 efficiency_pct_per_watt 산식을 range 쿼리로 시간축에 펼친 것.
    "all" 시리즈는 값이 있는 벤더들의 효율 평균(현재값 API의 avg_efficiency_pct_per_watt에 대응).
    """
    warnings: list[str] = []
    series: list[dict] = []
    per_ts_all: dict[str, list[float]] = {}

    vendors = list(ACCEL_POWER_QUERIES)
    power_tasks = [
        prometheus_client.range_query(f"avg({ACCEL_POWER_QUERIES[v]})", start, end, step) for v in vendors
    ]
    util_tasks = [
        prometheus_client.range_query(f"avg({ACCEL_UTIL_QUERIES[v]})", start, end, step) for v in vendors
    ]
    results = await asyncio.gather(*power_tasks, *util_tasks)
    power_results = results[: len(vendors)]
    util_results = results[len(vendors):]

    for vendor, p_res, u_res in zip(vendors, power_results, util_results):
        power_by_ts = {ts: parse_finite(raw) for ts, raw in _range_values(p_res)}
        util_by_ts = {ts: parse_finite(raw) for ts, raw in _range_values(u_res)}
        values: list[tuple[str, str]] = []
        for ts in sorted(power_by_ts.keys() & util_by_ts.keys()):
            power = power_by_ts[ts]
            util = util_by_ts[ts]
            if power is None or util is None or power <= 0:
                continue
            eff = util / power
            values.append((ts, str(eff)))
            per_ts_all.setdefault(ts, []).append(eff)
        if not values:
            warnings.append(f"NO_DATA_{vendor.upper()}")
        series.append({"vendor": vendor, "values": values})

    all_values = [(ts, str(sum(v) / len(v))) for ts, v in sorted(per_ts_all.items())]
    series.insert(0, {"vendor": "all", "values": all_values})

    return {"status": _status_from_warnings(warnings), "data": series, "warnings": warnings}


def _card_host(metric: dict, default: Optional[str] = "unknown") -> Optional[str]:
    """카드가 꽂힌 호스트. 벤더마다 라벨 이름이 달라 차례로 찾는다 (퓨리오사는 node)."""
    return (
        metric.get("Hostname") or metric.get("hostname") or metric.get("node")
        or metric.get("instance") or default
    )


async def accelerator_utilization(
    by: Optional[str] = None, limit: Optional[int] = None, sort_order: str = "desc"
) -> dict:
    """가속기 사용률 — 전체 카드 평균(카드 1장 = 1표) + 벤더별 평균. by="node"면 호스트별 목록도(sort_order 순, limit 전에 정렬)."""
    warnings: list[str] = []
    vendors: list[dict] = []
    all_values: list[float] = []
    host_values: dict[tuple[str, str], list[float]] = {}

    for vendor, query in ACCEL_UTIL_QUERIES.items():
        results = await prometheus_client.instant(query)
        values = []
        for item in results:
            value = first_finite([item])
            if value is None:
                continue
            values.append(value)
            host_values.setdefault((vendor, _card_host(item.get("metric", {}))), []).append(value)
        if not values:
            warnings.append(f"NO_DATA_{vendor.upper()}")
        all_values.extend(values)
        vendors.append(
            {
                "vendor": vendor,
                "card_count": len(values),
                "utilization_pct": sum(values) / len(values) if values else None,
            }
        )

    data = {
        "avg_utilization_pct": sum(all_values) / len(all_values) if all_values else None,
        "card_count": len(all_values),
        "vendors": vendors,
        "hosts": [],
    }
    if by == "node":
        hosts = [
            {"host": host, "vendor": vendor, "card_count": len(v), "utilization_pct": sum(v) / len(v)}
            for (vendor, host), v in host_values.items()
        ]
        hosts.sort(key=lambda h: h["utilization_pct"], reverse=sort_order == "desc")
        data["hosts"] = hosts[:limit] if limit else hosts
    return {"status": _status_from_warnings(warnings), "data": data, "warnings": warnings}


def _avg_series(per_ts: dict[float, list[float]]) -> list[tuple[str, str]]:
    """{epoch: [값...]} → 시각순 (ISO 8601 UTC, 평균값) 목록."""
    return [
        (ts_iso(ts), str(sum(v) / len(v))) for ts, v in sorted(per_ts.items())
    ]


async def accelerator_utilization_timeseries(start: str, end: str, step: str, by: Optional[str] = None) -> dict:
    """가속기 사용률 시계열 — accelerator_utilization()의 평균 산식을 시간축에 펼친 것.

    카드별 시계열을 받아 시각마다 평균한다. "all"은 카드 1장 = 1표 평균(현재값 API의 avg_utilization_pct에 대응).
    by="node"면 호스트별 시계열도 만든다.
    """
    warnings: list[str] = []
    vendors = list(ACCEL_UTIL_QUERIES)
    results = await asyncio.gather(
        *(prometheus_client.range_query(ACCEL_UTIL_QUERIES[v], start, end, step) for v in vendors)
    )

    all_ts: dict[float, list[float]] = {}
    host_ts: dict[tuple[str, str], dict[float, list[float]]] = {}
    series: list[dict] = []
    for vendor, cards in zip(vendors, results):
        vendor_ts: dict[float, list[float]] = {}
        for item in cards:
            host = host_ts.setdefault((vendor, _card_host(item.get("metric", {}))), {})
            for v in item.get("values", []):
                try:
                    ts = float(v[0])
                except (IndexError, TypeError, ValueError):
                    continue
                value = parse_finite(v[1])
                if value is None:
                    continue
                for bucket in (vendor_ts, all_ts, host):
                    bucket.setdefault(ts, []).append(value)
        if not vendor_ts:
            warnings.append(f"NO_DATA_{vendor.upper()}")
        series.append({"vendor": vendor, "values": _avg_series(vendor_ts)})
    series.insert(0, {"vendor": "all", "values": _avg_series(all_ts)})

    hosts: list[dict] = []
    if by == "node":
        hosts = [
            {"host": host, "vendor": vendor, "values": _avg_series(per_ts)}
            for (vendor, host), per_ts in sorted(host_ts.items())
            if per_ts
        ]
    return {"status": _status_from_warnings(warnings), "data": {"series": series, "hosts": hosts}, "warnings": warnings}
