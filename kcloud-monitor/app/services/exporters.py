"""
KCloud Monitor v2 — Export 도메인 서비스.

Prometheus range_query 결과를 전력/메트릭 내보내기용 행(row)으로 직렬화하고,
csv(stdlib)로 CSV 문자열을 만든다. pandas/openpyxl/pyarrow 등 외부 의존 없음
(csv/json만 지원).
"""
import csv
import io

from app.services.power import ACCEL_POWER_QUERIES, IPMI_POWER_METRIC
from app.services.prometheus import parse_float, prometheus_client, ts_iso

CPU_POWER_QUERY = 'kepler_node_cpu_watts{zone="psys"}'


def _points(results: list[dict]):
    """range 결과의 점마다 (시리즈 라벨, ISO 시각, 값). 모양이 깨진 점은 건너뜀."""
    for series in results:
        labels = series.get("metric", {})
        for point in series.get("values", []):
            try:
                ts, raw = point[0], point[1]
            except (IndexError, TypeError):
                continue
            yield labels, ts_iso(ts), parse_float(raw)


def _power_rows(results: list[dict], layer: str, node_of) -> list[dict]:
    """range 결과 → timestamp,node,layer,watts 행. node_of(라벨)로 노드 이름을 고름."""
    return [
        {"timestamp": ts, "node": node_of(labels), "layer": layer, "watts": watts}
        for labels, ts, watts in _points(results)
    ]


async def power_export_rows(start: str, end: str, step: str) -> tuple[list[dict], list[str]]:
    """전력 3계층(server/cpu/accelerator) range 쿼리 → timestamp,node,layer,watts 행 목록.

    power_timeseries()는 클러스터 전체를 sum()해 node 라벨을 잃으므로, 여기서는 원본
    시계열(비집계)을 직접 조회해 node 단위 행을 만든다.
    """
    warnings: list[str] = []

    server_results = await prometheus_client.range_query(IPMI_POWER_METRIC, start, end, step)
    if not server_results:
        warnings.append("NO_DATA_SERVER_POWER")
    rows = _power_rows(server_results, "server", lambda m: m.get("node", "unknown"))

    cpu_results = await prometheus_client.range_query(CPU_POWER_QUERY, start, end, step)
    if not cpu_results:
        warnings.append("NO_DATA_CPU_POWER")
    rows += _power_rows(cpu_results, "cpu", lambda m: m.get("node_name", "unknown"))

    any_accel = False
    for vendor, query in ACCEL_POWER_QUERIES.items():
        accel_results = await prometheus_client.range_query(query, start, end, step)
        if accel_results:
            any_accel = True
        rows += _power_rows(
            accel_results, f"accelerator:{vendor}",
            lambda m: m.get("instance") or m.get("hostname") or m.get("node") or "unknown",
        )
    if not any_accel:
        warnings.append("NO_DATA_ACCELERATOR")

    return rows, warnings


async def metric_export_rows(promql: str, start: str, end: str, step: str) -> tuple[list[dict], list[str]]:
    """단일 메트릭 range 쿼리 → timestamp,labels,value 행 목록."""
    results = await prometheus_client.range_query(promql, start, end, step)
    if not results:
        return [], ["NO_DATA"]
    return [{"timestamp": ts, "labels": labels, "value": value} for labels, ts, value in _points(results)], []


def rows_to_csv(rows: list[dict], fieldnames: list[str]) -> str:
    """딕셔너리 행 목록 → CSV 문자열(헤더 포함).

    labels 값이 dict인 열은 "k=v;k2=v2" 형태 문자열로 평탄화한다(CSV는 중첩 구조 미지원).
    """
    buf = io.StringIO()
    writer = csv.DictWriter(buf, fieldnames=fieldnames)
    writer.writeheader()
    for row in rows:
        flat = dict(row)
        if isinstance(flat.get("labels"), dict):
            flat["labels"] = ";".join(f"{k}={v}" for k, v in flat["labels"].items())
        writer.writerow(flat)
    return buf.getvalue()
