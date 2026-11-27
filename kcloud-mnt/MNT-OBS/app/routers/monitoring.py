"""인프라 모니터링 — KCloud Monitor v2 중개·집계.

가속기 공통 지표: 가속기는 mgmt 의 자식 서비스 클러스터(furiosa/furiosa-okc-nb/l40s/rebellions)
노드에 있으므로, 해당 클러스터들을 순회해 가속기를 모아 벤더별로 집계한다.
"""
from __future__ import annotations

import httpx
from fastapi import APIRouter, Body, HTTPException, Query
from fastapi.responses import Response, StreamingResponse

from ..monitor_client import monitor

router = APIRouter(tags=["monitoring"])

# 벤더 → 화면 표기(목업 3열과 일치)
VENDOR_LABEL = {
    "furiosa": "Furiosa RNGD (NPU)",
    "rebellions": "Rebellions ATOM (NPU)",
    "nvidia": "NVIDIA L40S (GPU)",
}


def _collect_accelerators(cluster: str) -> list[dict]:
    """cluster(mgmt) 및 그 자식 서비스 클러스터의 모든 가속기를 모은다."""
    clusters = monitor.get("/clusters").get("data", [])
    targets = [
        c["name"] for c in clusters
        if c.get("name") == cluster or c.get("parent_cluster") == cluster
    ]
    accels: list[dict] = []
    for sc in targets:
        nodes = monitor.get(f"/clusters/{sc}/nodes").get("nodes", [])
        for n in nodes:
            nm = n.get("nodename")
            if not nm:
                continue
            data = monitor.get(f"/clusters/{sc}/nodes/{nm}/accelerators").get("data", [])
            accels.extend(data)
    return accels


def _avg(xs: list) -> float:
    vals = [x for x in xs if isinstance(x, (int, float))]
    return round(sum(vals) / len(vals), 2) if vals else 0.0


def _avg_or_none(xs: list) -> float | None:
    """수치가 하나도 없으면 None(미수집), 있으면 평균(0 포함).
    메모리처럼 '값이 0' 과 '미수집'을 구분해야 하는 지표용(프론트: None→'—', 0→'0.0 GB')."""
    vals = [x for x in xs if isinstance(x, (int, float))]
    return round(sum(vals) / len(vals), 2) if vals else None


def _series_points(values) -> list[dict]:
    """[[ts, "val"], ...] → [{i, v}] (스파크라인용, 문자열 수치화)."""
    out = []
    for i, p in enumerate(values or []):
        if isinstance(p, (list, tuple)) and len(p) >= 2:
            try:
                out.append({"i": i, "v": round(float(p[1]), 2)})
            except (TypeError, ValueError):
                pass
    return out


@router.get("/monitoring/accelerator-summary")
def accelerator_summary(cluster: str = Query("mgmt")):
    """벤더별 가속기 공통 6종 + 파생 2종 집계."""
    try:
        accels = _collect_accelerators(cluster)
    except Exception as e:  # noqa: BLE001 — 상류(Monitor v2) 오류를 502로 전달
        raise HTTPException(status_code=502, detail=f"monitor api error: {type(e).__name__}: {e}")

    groups: dict[str, list[dict]] = {}
    for a in accels:
        groups.setdefault(a.get("vendor") or "unknown", []).append(a)

    vendors = {}
    for vendor, items in groups.items():
        vendors[vendor] = {
            "vendor": vendor,
            "model": items[0].get("model"),
            "label": VENDOR_LABEL.get(vendor, vendor),
            "count": len(items),
            # 공통 6종
            "temperature_celsius": _avg([x.get("temperature_celsius") for x in items]),
            "power_watts": _avg([x.get("power_watts") for x in items]),
            "utilization_percent": _avg([x.get("utilization_percent") for x in items]),
            "memory_used_bytes": _avg_or_none([x.get("memory_used_bytes") for x in items]),
            "memory_total_bytes": items[0].get("memory_total_bytes"),
            "healthy_count": sum(1 for x in items if x.get("healthy")),
            # 파생 교체(2026-10): 전력모드/캡상태 · Throttling
            "power_state": next((x.get("power_state") for x in items if x.get("power_state")), None),
            "power_limit_percent": _avg([x.get("power_limit_percent") for x in items]),
            "capped_count": sum(1 for x in items if x.get("power_capped")),
            "throttled_count": sum(1 for x in items if x.get("throttled")),
            "throttle_reasons": sorted({r for x in items for r in (x.get("throttle_reasons") or [])}),
        }

    return {
        "cluster": cluster,
        "vendors": vendors,
        "accelerator_count": len(accels),
        "throttled_count": sum(1 for a in accels if a.get("throttled")),
        # 집계에 포함된 실제 클러스터(인포 아이콘 표시용)
        "source_clusters": sorted({a.get("cluster") for a in accels if a.get("cluster")}),
    }


@router.get("/monitoring/overview")
def overview(cluster: str = Query("mgmt")):
    """상단 KPI — 2026-10 이노그리드 업데이트 매핑:
    전력효율=/monitoring/power/efficiency, 가속기사용률=/monitoring/accelerators/utilization,
    서버총전력=/monitoring/power/summary, 정상노드=/monitoring/overview,
    저장소=/storage/ceph/capacity, 쓰로틀=/monitoring/throttling/timeseries(summary).
    """
    try:
        eff = monitor.get("/monitoring/power/efficiency").get("data", {})
        psum = monitor.get("/monitoring/power/summary").get("data", {})
        mov = monitor.get("/monitoring/overview").get("data", {})
        util = monitor.get("/monitoring/accelerators/utilization").get("data", {})
        ceph = monitor.get("/storage/ceph/capacity").get("data", {})
        thr = monitor.get("/monitoring/throttling/timeseries", params={"period": "24h", "step": "1h"}).get("summary", {})
    except Exception as e:  # noqa: BLE001
        raise HTTPException(status_code=502, detail=f"monitor api error: {type(e).__name__}: {e}")

    nodes = mov.get("nodes", {}) or {}
    return {
        "cluster": cluster,
        "power_efficiency_pct_per_watt": eff.get("avg_efficiency_pct_per_watt"),
        "accelerator_utilization_percent": util.get("avg_utilization_pct"),
        "total_power_watts": psum.get("server_total_watts"),
        "nodes_ready": nodes.get("healthy"),
        "nodes_total": nodes.get("total"),
        "storage_usage_percent": ceph.get("usage_percent"),
        "throttle_events": thr.get("throttle_events"),
        "cards_throttled": thr.get("cards_throttled"),
        "cards_total": thr.get("cards_total"),
    }


@router.get("/monitoring/top-hosts")
def top_hosts(limit: int = Query(5)):
    """상위 호스트 — /monitoring/accelerators/utilization?by=node 정렬."""
    try:
        d = monitor.get(
            "/monitoring/accelerators/utilization",
            params={"by": "node", "limit": limit, "sort_order": "desc"},
        ).get("data", {})
    except Exception as e:  # noqa: BLE001
        raise HTTPException(status_code=502, detail=f"monitor api error: {type(e).__name__}: {e}")
    return {"hosts": d.get("hosts", [])}


@router.get("/monitoring/error-logs")
def error_logs(limit: int = Query(4)):
    """최근 오류 로그 — /logs/search level=error 최신순."""
    try:
        d = monitor.get("/logs/search", params={"query": '{level="error"}', "limit": limit})
    except Exception as e:  # noqa: BLE001
        raise HTTPException(status_code=502, detail=f"monitor api error: {type(e).__name__}: {e}")
    return {"logs": d.get("data", [])}


# ── 로그·이벤트 (LogQL 검색/라벨/볼륨/내보내기/스트림) — Monitor v2 /logs/* 중개 ──
def _log_params(query: str, log_level: str | None, start: str | None,
                end: str | None, direction: str, limit: int) -> dict:
    """search/stream/export 공통 파라미터. 전체 레벨(log_level 없음)은 생략."""
    p: dict = {"query": query, "direction": direction, "limit": limit}
    if log_level:
        p["log_level"] = log_level
    if start:
        p["start"] = start
    if end:
        p["end"] = end
    return p


@router.get("/monitoring/logs/search")
def logs_search(
    query: str = Query(...),
    log_level: str | None = Query(None),
    start: str | None = Query(None),
    end: str | None = Query(None),
    direction: str = Query("backward"),
    limit: int = Query(100),
):
    """LogQL 검색 — /logs/search 중개."""
    try:
        d = monitor.get("/logs/search", params=_log_params(query, log_level, start, end, direction, limit))
    except Exception as e:  # noqa: BLE001
        raise HTTPException(status_code=502, detail=f"monitor api error: {type(e).__name__}: {e}")
    return {"logs": d.get("data", []), "pagination": d.get("pagination", {}), "warnings": d.get("warnings", [])}


@router.get("/monitoring/logs/labels")
def logs_labels():
    """사용 가능한 라벨 키 목록 — /logs/labels."""
    try:
        return {"labels": monitor.get("/logs/labels").get("data", [])}
    except Exception as e:  # noqa: BLE001
        raise HTTPException(status_code=502, detail=f"monitor api error: {type(e).__name__}: {e}")


@router.get("/monitoring/logs/label-values")
def logs_label_values(label: str = Query(...)):
    """라벨 값 목록 — /logs/label-values?label=."""
    try:
        d = monitor.get("/logs/label-values", params={"label": label})
    except Exception as e:  # noqa: BLE001
        raise HTTPException(status_code=502, detail=f"monitor api error: {type(e).__name__}: {e}")
    return {"label": label, "values": d.get("data", [])}


@router.get("/monitoring/logs/volume")
def logs_volume(
    query: str | None = Query(None),
    start: str | None = Query(None),
    end: str | None = Query(None),
):
    """라벨 조합별 로그 볼륨(bytes) — /logs/volume."""
    params: dict = {}
    if query:
        params["query"] = query
    if start:
        params["start"] = start
    if end:
        params["end"] = end
    try:
        d = monitor.get("/logs/volume", params=params or None)
    except Exception as e:  # noqa: BLE001
        raise HTTPException(status_code=502, detail=f"monitor api error: {type(e).__name__}: {e}")
    return {"volume": d.get("data", [])}


@router.get("/monitoring/logs/export")
def logs_export(
    format: str = Query("csv"),
    query: str = Query(...),
    log_level: str | None = Query(None),
    start: str | None = Query(None),
    end: str | None = Query(None),
    direction: str = Query("backward"),
    limit: int = Query(1000),
):
    """CSV/JSON 내보내기 — /logs/export 원시 응답 중계(파일 다운로드)."""
    params = _log_params(query, log_level, start, end, direction, limit)
    params["format"] = format
    try:
        r = monitor.get_raw("/logs/export", params=params)
    except Exception as e:  # noqa: BLE001
        raise HTTPException(status_code=502, detail=f"monitor api error: {type(e).__name__}: {e}")
    ext = "csv" if format == "csv" else "json"
    media = "text/csv" if format == "csv" else "application/json"
    return Response(
        content=r.content,
        media_type=media,
        headers={"Content-Disposition": f'attachment; filename="logs.{ext}"'},
    )


@router.get("/monitoring/logs/stream")
def logs_stream(
    query: str = Query(...),
    log_level: str | None = Query(None),
    direction: str = Query("backward"),
    limit: int = Query(100),
):
    """실시간 로그 스트림(SSE) — /logs/stream 중계."""
    params = _log_params(query, log_level, None, None, direction, limit)
    return StreamingResponse(
        monitor.iter_sse("/logs/stream", params=params),
        media_type="text/event-stream",
        headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no", "Connection": "keep-alive"},
    )


# ── 알람 (활성 알람·채널·정책) — Monitor v2 /alerts/* 중개(stateless 프록시) ──
def _mon_err(e: Exception) -> HTTPException:  # noqa: ANN001
    return HTTPException(status_code=502, detail=f"monitor api error: {type(e).__name__}: {e}")


def _passthrough(e: Exception) -> HTTPException:  # noqa: ANN001
    """정책·채널 생성 등 — 업스트림 4xx(특히 422 detail.errors)를 상태코드·본문 그대로 전달.
    그 외(연결 실패 등)는 502."""
    if isinstance(e, httpx.HTTPStatusError):
        try:
            body = e.response.json() if e.response.content else None
        except ValueError:
            body = None
        detail = body.get("detail", body) if isinstance(body, dict) else (body or str(e))
        return HTTPException(status_code=e.response.status_code, detail=detail)
    return HTTPException(status_code=502, detail=f"monitor api error: {type(e).__name__}: {e}")


@router.get("/monitoring/alerts/active")
def alerts_active():
    """현재 발생 중인 알람 — /alerts/active."""
    try:
        d = monitor.get("/alerts/active")
    except Exception as e:  # noqa: BLE001
        raise _mon_err(e)
    return {"alerts": d.get("data", []), "total": d.get("total", 0)}


@router.get("/monitoring/alerts/metrics")
def alerts_metrics():
    """알람 지표 목록 — /alerts/metrics."""
    try:
        return {"metrics": monitor.get("/alerts/metrics").get("data", [])}
    except Exception as e:  # noqa: BLE001
        raise _mon_err(e)


@router.get("/monitoring/alerts/channels")
def alerts_channels():
    """알림 채널 목록 — /alerts/channels."""
    try:
        return {"channels": monitor.get("/alerts/channels").get("data", [])}
    except Exception as e:  # noqa: BLE001
        raise _mon_err(e)


@router.post("/monitoring/alerts/channels")
def alerts_channel_create(body: dict = Body(...)):
    try:
        return monitor.request("POST", "/alerts/channels", json=body)
    except Exception as e:  # noqa: BLE001
        raise _passthrough(e)


@router.patch("/monitoring/alerts/channels/{channel_id}")
def alerts_channel_update(channel_id: str, body: dict = Body(...)):
    try:
        return monitor.request("PATCH", f"/alerts/channels/{channel_id}", json=body)
    except Exception as e:  # noqa: BLE001
        raise _mon_err(e)


@router.delete("/monitoring/alerts/channels/{channel_id}")
def alerts_channel_delete(channel_id: str):
    try:
        return monitor.request("DELETE", f"/alerts/channels/{channel_id}")
    except Exception as e:  # noqa: BLE001
        raise _mon_err(e)


@router.get("/monitoring/alerts/policies")
def alerts_policies():
    """알람 정책 목록 — /alerts/policies."""
    try:
        return {"policies": monitor.get("/alerts/policies").get("data", [])}
    except Exception as e:  # noqa: BLE001
        raise _mon_err(e)


@router.get("/monitoring/alerts/policies/{policy_id}")
def alerts_policy_detail(policy_id: str):
    try:
        return {"policy": monitor.get(f"/alerts/policies/{policy_id}").get("data", {})}
    except Exception as e:  # noqa: BLE001
        raise _mon_err(e)


@router.post("/monitoring/alerts/policies")
def alerts_policy_create(body: dict = Body(...)):
    """정책 생성 — 성공 201 {data}, 검증 실패 422 {detail:{errors}} 를 그대로 전달."""
    try:
        return monitor.request("POST", "/alerts/policies", json=body)
    except Exception as e:  # noqa: BLE001
        raise _passthrough(e)


@router.post("/monitoring/alerts/policies/{policy_id}/preview")
def alerts_policy_preview(policy_id: str, body: dict = Body(default={})):
    """저장된 정책 미리 평가 — 현재 수집값 기준 조건 충족 여부."""
    try:
        return monitor.request("POST", f"/alerts/policies/{policy_id}/preview", json=body or {})
    except Exception as e:  # noqa: BLE001
        raise _passthrough(e)


@router.patch("/monitoring/alerts/policies/{policy_id}")
def alerts_policy_update(policy_id: str, body: dict = Body(...)):
    """정책 수정(활성 토글 포함) — PATCH {enabled} 등."""
    try:
        return monitor.request("PATCH", f"/alerts/policies/{policy_id}", json=body)
    except Exception as e:  # noqa: BLE001
        raise _mon_err(e)


@router.delete("/monitoring/alerts/policies/{policy_id}")
def alerts_policy_delete(policy_id: str):
    try:
        return monitor.request("DELETE", f"/alerts/policies/{policy_id}")
    except Exception as e:  # noqa: BLE001
        raise _mon_err(e)


# ── 정책 생성 폼 감시대상(selector) 옵션용 — Monitor v2 중개 ──
@router.get("/monitoring/clusters/{cluster}/nodes/{node}/accelerators")
def node_accelerators(cluster: str, node: str):
    """노드의 가속기 카드 목록(acc_id) — selector '가속기 카드' 옵션."""
    try:
        return {"accelerators": monitor.get(f"/clusters/{cluster}/nodes/{node}/accelerators").get("data", [])}
    except Exception as e:  # noqa: BLE001
        raise _mon_err(e)


@router.get("/monitoring/slo/services")
def slo_services(cluster: str = Query(...)):
    """클러스터의 LLM 모델 목록 — selector '모델' 옵션. 응답 ~4초, 데이터 없으면 warnings=[NO_DATA]."""
    try:
        d = monitor.get("/slo/services", params={"cluster": cluster})
    except Exception as e:  # noqa: BLE001
        raise _mon_err(e)
    return {"items": d.get("items", []), "warnings": d.get("warnings", [])}


@router.get("/monitoring/power/breakdown")
def power_breakdown(dimension: str = Query("vendor")):
    """전력 분해 — dimension(vendor|cluster|node|accelerator)별 watts/layer 목록."""
    try:
        d = monitor.get("/monitoring/power/breakdown", params={"dimension": dimension})
    except Exception as e:  # noqa: BLE001
        raise HTTPException(status_code=502, detail=f"monitor api error: {type(e).__name__}: {e}")
    return {"dimension": d.get("dimension", dimension), "items": d.get("items", [])}


@router.get("/monitoring/power/timeseries")
def power_timeseries(period: str = Query("24h"), step: str = Query("1h"), aggregation: str = Query("avg")):
    """전력 시계열 3계층(server/cpu/accelerator) — 타임스탬프 기준으로 병합, 문자열 watts→수치."""
    try:
        d = monitor.get(
            "/monitoring/power/timeseries",
            params={"period": period, "step": step, "aggregation": aggregation},
        )
    except Exception as e:  # noqa: BLE001
        raise HTTPException(status_code=502, detail=f"monitor api error: {type(e).__name__}: {e}")
    merged: dict[str, dict] = {}
    for layer in d.get("layers", []):
        name = layer.get("layer")
        for point in layer.get("values", []):
            if not isinstance(point, (list, tuple)) or len(point) < 2:
                continue
            ts, val = point[0], point[1]
            row = merged.setdefault(ts, {"t": ts[11:16] if isinstance(ts, str) else ts})
            try:
                row[name] = round(float(val), 1)
            except (TypeError, ValueError):
                pass
    series = [merged[k] for k in sorted(merged.keys())]
    return {"series": series}


@router.get("/monitoring/clusters")
def clusters():
    """클러스터 자원 현황 — /clusters 목록 + 클러스터별 /resource 병합.
    서비스 클러스터는 IPMI 없어 power.total_watts=null(화면 '—')."""
    try:
        lst = monitor.get("/clusters").get("data", [])
    except Exception as e:  # noqa: BLE001
        raise HTTPException(status_code=502, detail=f"monitor api error: {type(e).__name__}: {e}")
    rows = []
    for c in lst:
        name = c.get("name")
        res: dict = {}
        try:
            res = monitor.get(f"/clusters/{name}/resource").get("data", {})
        except Exception:  # noqa: BLE001 — 개별 클러스터 실패는 건너뜀
            pass
        resources = res.get("resources", {}) or {}
        nodes = resources.get("nodes", {}) or {}
        cpu = resources.get("cpu", {}) or {}
        mem = resources.get("memory", {}) or {}
        accel = resources.get("accelerators", {}) or {}
        power = res.get("power", {}) or {}
        rows.append({
            "cluster": name,
            "type": c.get("type"),
            "parent_cluster": c.get("parent_cluster"),
            "nodes_ready": nodes.get("ready"),
            "nodes_total": nodes.get("total"),
            "cpu_percent": cpu.get("utilization_percent"),
            "memory_percent": mem.get("utilization_percent"),
            "accelerator_total": accel.get("total"),
            "accelerator_util_percent": accel.get("avg_utilization_percent"),
            "power_watts": power.get("total_watts"),
            "status": c.get("status"),
        })
    return {"clusters": rows}


@router.get("/monitoring/kpi-timeseries")
def kpi_timeseries(period: str = Query("24h"), step: str = Query("1h")):
    """상단 KPI 스파크라인용 시계열 — 전력효율·저장소·가속기사용률·서버전력.
    (개별 실패는 빈 배열로 → 프론트는 해당 스파크라인 생략)"""
    def safe(path: str, **params):
        try:
            return monitor.get(path, params=params)
        except Exception:  # noqa: BLE001
            return {}

    def first_values(resp: dict):
        s = resp.get("series", [])
        return s[0].get("values", []) if s else []

    eff = safe("/monitoring/power/efficiency/timeseries", period=period, step=step)
    sto = safe("/storage/ceph/capacity/timeseries", period=period, step=step)
    acc = safe("/monitoring/accelerators/utilization/timeseries", period=period, step=step)
    pwr = safe("/monitoring/power/timeseries", period=period, step=step, aggregation="avg")
    thr = safe("/monitoring/throttling/timeseries", period=period, step=step)

    server_vals = []
    for layer in pwr.get("layers", []):
        if layer.get("layer") == "server":
            server_vals = layer.get("values", [])
            break

    # 쓰로틀링은 timeline[{time, events, ...}] 구조 → events 를 스파크라인 포인트로
    throttle = [{"i": i, "v": e.get("events", 0)}
                for i, e in enumerate(thr.get("timeline", []) or []) if isinstance(e, dict)]

    return {
        "power_efficiency": _series_points(first_values(eff)),
        "storage": _series_points(first_values(sto)),
        "accel_util": _series_points(first_values(acc)),
        "server_power": _series_points(server_vals),
        "throttle": throttle,
    }


@router.get("/monitoring/nodes")
def nodes(cluster: str = Query("mgmt")):
    """노드 목록(전력 포함) — 호스트 맵 대체용(util 대신 power_watts 색상)."""
    try:
        d = monitor.get(f"/clusters/{cluster}/nodes")
    except Exception as e:  # noqa: BLE001
        raise HTTPException(status_code=502, detail=f"monitor api error: {type(e).__name__}: {e}")
    return {
        "nodes": [
            {
                "nodename": n.get("nodename"),
                "role": n.get("role"),
                "up": n.get("up"),
                "power_watts": n.get("power_watts"),
                "utilization_percent": n.get("utilization_percent"),
            }
            for n in d.get("nodes", [])
        ]
    }


@router.get("/monitoring/nodes/timeseries")
def nodes_timeseries(cluster: str = Query("mgmt"), period: str = Query("24h"), step: str = Query("1h")):
    """정상 노드 추세(상단 '정상 노드' 카드 미니 스파크라인) — /monitoring/nodes/timeseries 중개.
    series 중 ready/healthy 우선, 없으면 첫 series(total 등)."""
    try:
        d = monitor.get("/monitoring/nodes/timeseries",
                        params={"cluster": cluster, "period": period, "step": step})
    except Exception as e:  # noqa: BLE001
        raise HTTPException(status_code=502, detail=f"monitor api error: {type(e).__name__}: {e}")
    series = d.get("series", []) or []
    pick = next((s for s in series if s.get("metric") in ("ready", "healthy")), None) or (series[0] if series else {})
    return {"series": _series_points(pick.get("values", []))}


@router.get("/monitoring/util-timeseries")
def util_timeseries(period: str = Query("24h"), step: str = Query("1h")):
    """가속기 사용률 히트맵 — 호스트 × 시각. 벤더 metric 3종을 by=hostname 으로 조회해 호스트별 series 수집.
    이노그리드가 Hostname/hostname/node 라벨을 hostname 으로 통합 → series[].metric.hostname."""
    metrics = {"gpu": "gpu_utilization", "furiosa": "furiosa_utilization", "rebellions": "rebellions_utilization"}
    times: list[str] = []
    hosts: list[dict] = []
    for vendor, metric in metrics.items():
        try:
            resp = monitor.get(
                "/monitoring/metrics/timeseries",
                params={"metric": metric, "period": period, "step": step, "by": "hostname"},
            )
        except Exception:  # noqa: BLE001 — 해당 벤더 metric 부재 시 건너뜀
            continue
        for s in resp.get("series", []) or []:
            host = (s.get("metric") or {}).get("hostname")
            if not host:
                continue
            cells: list = []
            labels: list[str] = []
            for p in s.get("values", []) or []:
                if isinstance(p, (list, tuple)) and len(p) >= 2:
                    ts, val = p[0], p[1]
                    labels.append(ts[11:16] if isinstance(ts, str) else "")
                    try:
                        cells.append(round(float(val), 1))
                    except (TypeError, ValueError):
                        cells.append(None)
            if len(labels) > len(times):  # 가장 긴 series 의 시각을 공통 축으로
                times = labels
            hosts.append({"host": host, "vendor": vendor, "cells": cells})
    return {"times": times, "hosts": hosts}
