"""
KCloud Faults — 장애 정보 분석.

새 수집기나 저장소 없이 기존 부품을 묶는다.
  1. 탐지 : (a) 알람 평가기가 낸 firing 알람과 알람 이력, (b) 알람 정책 없이도 보는 고정 PromQL 신호
  2. 분류 : 알람 지표 키, 신호 키 → 장애 유형
  3. 증거 : 장애 시점 앞뒤 메트릭, 로그(XID, OOM 구조화 필드), 영향 자원(자원 추적)
  4. 분석 : 증거로 추정 원인과 기여 요인을 내는 규칙 (순수 함수라 DB, Prometheus 없이 검증)

외부 시스템은 바꾸지 않는다. 조회와 분석 결과 제공까지가 범위이고 조치는 관리자가 직접 한다.
ponytail: 장애 전용 테이블이 없어 이력은 알람 이력에서만 나온다. 고정 신호는 지금 걸려 있는 것만 보이고,
          신호가 사라지면 이력에 남지 않는다. 이력이 필요해지면 신호를 알람 지표로 옮기거나 장애 테이블을 둔다.
"""
from __future__ import annotations

import asyncio
import hashlib
import json
import logging
from datetime import datetime, timedelta, timezone
from typing import Optional

from fastapi import HTTPException

from app.services import alerts as alert_service
from app.services.cluster_discovery import DEFAULT_CLUSTER_NAME, cluster_label
from app.services.loki import loki_client
from app.services.prometheus import esc, parse_finite, prometheus_client

logger = logging.getLogger(__name__)

ACTIVE, RESOLVED, UNKNOWN = "active", "resolved", "unknown"
HISTORY_DAYS = 30  # 장애 ID 로 찾을 때 거슬러 올라가는 기간

FAULT_TYPES: dict[str, str] = {
    "accelerator_error": "GPU 오류 (XID, 상태 이상, 과열, 수집 중단)",
    "npu_fault": "NPU 오류 (응답 없음, 상태 이상, 과열, 수집 중단)",
    "workload_oom": "워크로드 메모리 부족 종료",
    "workload_crash_loop": "워크로드 재시작 반복",
    "node_not_ready": "노드 NotReady",
    "vm_down": "VM 비정상",
    "hardware_fault": "서버 하드웨어 오류",
}

# ---------------------------------------------------------------------------
# 1. 분류
# ---------------------------------------------------------------------------

_ACCEL = "accelerator"  # 벤더에 따라 accelerator_error | npu_fault
# 알람 지표 키 → 장애 유형. 여기 없는 지표(사용률, 메모리 포화, SLO, 오류 로그 건수)는 성능 알람이라 장애로 보지 않음
ALERT_FAULTS: dict[str, str] = {
    "unhealthy": _ACCEL, "temperature_celsius": _ACCEL, "stale_seconds": _ACCEL,
    "log_gpu_xid": "accelerator_error", "log_oom": "workload_oom", "log_crash_loop": "workload_crash_loop",
    "log_hw_error": "hardware_fault", "log_disk_error": "hardware_fault",
}

_POD = {"namespace": "namespace", "pod": "pod", "container": "container"}
# 알람 정책이 없어도 보는 고정 신호. target 은 응답 필드 ← 메트릭 라벨, value 는 표본 값의 뜻
SIGNALS: dict[str, dict] = {
    # 값이 마지막 XID 코드인 게이지. 최근 1시간 안에 코드가 바뀐 카드만
    "xid": {"fault_type": "accelerator_error", "severity": "critical", "value": "xid_code", "vendor": "nvidia",
            "query": "DCGM_FI_DEV_XID_ERRORS > 0 and changes(DCGM_FI_DEV_XID_ERRORS[1h]) > 0",
            "target": {"node": "Hostname", "accelerator_id": "UUID", "model": "modelName"}},
    # 값이 마지막 종료 시각(epoch). 최근 1시간 안에 OOMKilled 로 끝난 컨테이너
    "oom_killed": {"fault_type": "workload_oom", "severity": "warning", "value": "terminated_at",
                   "query": "(kube_pod_container_status_last_terminated_timestamp > time() - 3600) "
                            "and on (cluster, namespace, pod, container) "
                            '(kube_pod_container_status_last_terminated_reason{reason="OOMKilled"} == 1)',
                   "target": _POD},
    "restart_loop": {"fault_type": "workload_crash_loop", "severity": "warning", "value": "restarts_1h",
                     "query": "increase(kube_pod_container_status_restarts_total[1h]) >= 3", "target": _POD},
    # NPU 는 XID 가 없어 벤더 exporter 의 상태 지표로 본다. 가속기 API 의 정상 판정(alive 1, HEALTH 0)과 같은 기준
    "npu_dead": {"fault_type": "npu_fault", "severity": "critical", "value": "", "vendor": "furiosa",
                 "query": "furiosa_npu_alive == 0",
                 "target": {"node": "hostname", "accelerator_id": "device"}},
    "npu_unhealthy": {"fault_type": "npu_fault", "severity": "critical", "value": "health_code", "vendor": "rebellions",
                      "query": "RBLN_DEVICE_STATUS:HEALTH != 0",
                      "target": {"node": "hostname", "accelerator_id": "uuid", "model": "card"}},
    "node_not_ready": {"fault_type": "node_not_ready", "severity": "critical", "value": "",
                       "query": 'kube_node_status_condition{condition="Ready",status="true"} == 0',
                       "target": {"node": "node"}},
    # ponytail: SHUTOFF 는 관리자가 끈 VM 과 구분할 수 없어 ERROR 만 장애로 본다
    "vm_error": {"fault_type": "vm_down", "severity": "critical", "value": "",
                 "query": 'openstack_nova_server_status{status="ERROR"}',
                 "target": {"vm": "name", "vm_id": "uuid", "physical_server": "hypervisor_hostname"}},
}

# NVIDIA XID 코드 → (설명, 주된 원인 후보). NVIDIA XID 문서의 원인 열을 한 가지로 줄인 것이라 확정이 아니다
XID_CODES: dict[int, tuple[str, str]] = {
    13: ("Graphics Engine Exception", "애플리케이션"), 31: ("GPU memory page fault", "애플리케이션"),
    43: ("GPU stopped processing", "애플리케이션"), 45: ("Preemptive cleanup, due to previous errors", "애플리케이션"),
    48: ("Double Bit ECC Error", "하드웨어"), 61: ("Internal micro-controller breakpoint/warning", "펌웨어"),
    62: ("Internal micro-controller halt", "펌웨어"),
    63: ("ECC page retirement or row remapping recording event", "하드웨어"),
    64: ("ECC page retirement or row remapper recording failure", "하드웨어"),
    68: ("NVDEC0 Exception", "애플리케이션"), 69: ("Graphics Engine class error", "애플리케이션"),
    74: ("NVLINK Error", "하드웨어"), 79: ("GPU has fallen off the bus", "하드웨어"),
    92: ("High single-bit ECC error rate", "하드웨어"), 94: ("Contained ECC error", "하드웨어"),
    95: ("Uncontained ECC error", "하드웨어"), 119: ("GSP RPC Timeout", "펌웨어"), 120: ("GSP Error", "펌웨어"),
}


def classify_alert(conditions: list[dict], vendor: Optional[str]) -> Optional[tuple[str, str]]:
    """알람 정책의 조건 지표로 (장애 유형, 근거 지표). 장애로 보지 않는 알람이면 None.

    >>> classify_alert([{"metric": "utilization_percent"}, {"metric": "log_oom"}], None)
    ('workload_oom', 'log_oom')
    >>> classify_alert([{"metric": "unhealthy"}], "furiosa"), classify_alert([{"metric": "unhealthy"}], "nvidia")
    (('npu_fault', 'unhealthy'), ('accelerator_error', 'unhealthy'))
    >>> classify_alert([{"metric": "slo_ttft_p95_ms"}], None) is None
    True
    """
    for c in conditions:
        kind = ALERT_FAULTS.get(c["metric"])
        if kind == _ACCEL:
            kind = "npu_fault" if vendor in ("furiosa", "rebellions") else "accelerator_error"
        if kind:
            return kind, c["metric"]
    return None


def fault_id(source: str, key: str, target: dict, detected_at: Optional[datetime]) -> str:
    """같은 장애면 조회할 때마다 같은 ID. 알람은 발생 시각까지 넣어 재발을 다른 장애로 구분."""
    raw = "|".join([source, key, json.dumps(target, sort_keys=True), detected_at.isoformat() if detected_at else ""])
    return "flt-" + hashlib.sha1(raw.encode()).hexdigest()[:12]


def _iso(dt: Optional[datetime]) -> Optional[str]:
    return dt.isoformat() if dt else None


def signal_fault(key: str, item: dict) -> dict:
    """고정 신호의 표본 하나 → 장애.

    >>> f = signal_fault("xid", {"metric": {"cluster": "l40s", "Hostname": "n1", "UUID": "GPU-1"}, "value": [0, "79"]})
    >>> (f["fault_type"], f["severity"], f["target"]["accelerator_id"], f["xid_code"], f["summary"])
    ('accelerator_error', 'critical', 'GPU-1', 79, 'GPU GPU-1 XID 79: GPU has fallen off the bus')
    >>> signal_fault("xid", {"metric": {"UUID": "GPU-1"}, "value": [0, "8"]})["severity"]  # 치명 목록에 없는 코드
    'warning'
    >>> f = signal_fault("oom_killed", {"metric": {"namespace": "ml", "pod": "train-0", "container": "main"},
    ...                                 "value": [0, "1794366000"]})
    >>> (f["target"]["cluster"], f["detected_at"], f["summary"])
    ('mgmt', '2026-11-11T03:00:00+00:00', 'ml/train-0 컨테이너 main 메모리 부족 종료 (OOMKilled)')
    >>> f = signal_fault("npu_dead", {"metric": {"cluster": "furiosa", "hostname": "npu-node-1", "device": "npu0"},
    ...                               "value": [0, "0"]})
    >>> (f["fault_type"], f["target"]["vendor"], f["target"]["node"], f["summary"])
    ('npu_fault', 'furiosa', 'npu-node-1', 'NPU npu0 응답 없음 (alive 0)')
    >>> f = signal_fault("npu_unhealthy", {"metric": {"hostname": "npu-node-2", "uuid": "U-1"}, "value": [0, "2"]})
    >>> (f["target"]["accelerator_id"], f["value"], f["summary"])
    ('U-1', 2.0, 'NPU U-1 상태 이상 (HEALTH 2)')
    """
    from app.api.v2.logs import _CRITICAL_XID_CODES

    sig, m = SIGNALS[key], item.get("metric", {})
    target = {"cluster": m.get("cluster") or DEFAULT_CLUSTER_NAME,
              **{field: m[label] for field, label in sig["target"].items() if m.get(label)}}
    if sig.get("vendor"):
        target["vendor"] = sig["vendor"]
    value = parse_finite(item.get("value", [None, None])[1])
    severity, detected, extra = sig["severity"], None, {}
    where = "/".join(str(target[k]) for k in ("namespace", "pod") if target.get(k))
    if key == "xid":
        code = int(value or 0)
        extra["xid_code"] = code
        severity = "critical" if code in _CRITICAL_XID_CODES else "warning"
        desc = XID_CODES.get(code, ("", ""))[0]
        summary = f"GPU {target.get('accelerator_id', '?')} XID {code}" + (f": {desc}" if desc else "")
    elif key == "oom_killed":
        detected = datetime.fromtimestamp(value, timezone.utc) if value else None
        summary = f"{where} 컨테이너 {target.get('container', '?')} 메모리 부족 종료 (OOMKilled)"
    elif key == "restart_loop":
        summary = f"{where} 컨테이너 {target.get('container', '?')} 최근 1시간 재시작 {value or 0:.0f}회"
    elif key == "npu_dead":
        summary = f"NPU {target.get('accelerator_id', '?')} 응답 없음 (alive 0)"
    elif key == "npu_unhealthy":
        summary = f"NPU {target.get('accelerator_id', '?')} 상태 이상 (HEALTH {value or 0:g})"
    elif key == "node_not_ready":
        summary = f"노드 {target.get('node', '?')} NotReady"
    else:
        summary = f"VM {target.get('vm', '?')} 상태 ERROR"
    return {"fault_id": fault_id("signal", key, target, detected), "fault_type": sig["fault_type"],
            "severity": severity, "status": ACTIVE, "source": "signal", "trigger": key,
            "detected_at": _iso(detected), "resolved_at": None, "target": target, "summary": summary,
            "value": value if sig["value"] not in ("", "terminated_at") else None, "source_alerts": [], **extra}


def _alert_target(t: dict) -> dict:
    out = {k: v for k, v in t.items() if v not in (None, "", "*") and k != "acc_id"}
    if t.get("acc_id"):
        out["accelerator_id"] = t["acc_id"]
    return out


def build_alert_faults(policies: dict[str, dict], firing: dict[str, dict], events: list[dict]) -> tuple[list[dict], list[str]]:
    """알람 이력(fire, resolve)과 지금 firing 인 알람을 장애로 묶는다. 최신 순.

    fire 가 장애를 열고 resolve 가 닫는다. 열린 채 남았는데 알람 행이 없으면(정책 삭제 등) 해제 시각을 모르는 unknown.
    이력 조회 구간보다 먼저 발생해 fire 이력이 없는 firing 알람은 알람 행의 발생 시각으로 만든다.

    >>> t0 = datetime(2026, 11, 11, tzinfo=timezone.utc)
    >>> pol = {"p1": {"id": "p1", "name": "OOM", "conditions": [{"metric": "log_oom"}]},
    ...        "p2": {"id": "p2", "name": "사용률", "conditions": [{"metric": "utilization_percent"}]}}
    >>> ev = lambda m, kind, pid="p1", fp="p1:l40s/*/*": {"ts": t0 + timedelta(minutes=m), "policy_id": pid,
    ...     "fingerprint": fp, "kind": kind, "severity": "warning", "target": {"cluster": "l40s"}, "value": 3.0,
    ...     "message": "msg"}
    >>> events = [ev(0, "fire"), ev(5, "notified"), ev(9, "resolve"), ev(20, "fire"), ev(1, "fire", "p2", "p2:x"),
    ...           ev(2, "fire", "gone", "gone:x")]
    >>> faults, w = build_alert_faults(pol, {"p1:l40s/*/*": {"fired_at": t0 + timedelta(minutes=20)}}, events)
    >>> [(f["status"], f["fault_type"], f["detected_at"][11:16], (f["resolved_at"] or "")[11:16]) for f in faults]
    [('active', 'workload_oom', '00:20', ''), ('resolved', 'workload_oom', '00:00', '00:09')]
    >>> w, faults[0]["source_alerts"][0]["policy_name"], faults[0]["target"]
    (['ALERT_POLICY_MISSING'], 'OOM', {'cluster': 'l40s'})
    >>> build_alert_faults(pol, {}, [ev(0, "fire")])[0][0]["status"]
    'unknown'
    """
    warnings: list[str] = []
    faults: list[dict] = []
    open_by_fp: dict[str, dict] = {}

    def make(policy_id: str, fp: str, row: dict, detected: Optional[datetime]) -> Optional[dict]:
        policy = policies.get(policy_id)
        if policy is None:
            if "ALERT_POLICY_MISSING" not in warnings:
                warnings.append("ALERT_POLICY_MISSING")
            return None
        target = row.get("target") or {}
        kind = classify_alert(policy.get("conditions") or [policy], target.get("vendor"))
        if kind is None:
            return None
        return {"fault_id": fault_id("alert", fp, {}, detected), "fault_type": kind[0], "severity": row["severity"],
                "status": ACTIVE, "source": "alert", "trigger": kind[1], "detected_at": _iso(detected),
                "resolved_at": None, "target": _alert_target(target), "summary": row.get("message", ""),
                "value": row.get("value"),
                "source_alerts": [{"fingerprint": fp, "policy_id": policy_id, "policy_name": policy.get("name")}]}

    for e in sorted(events, key=lambda e: e["ts"]):
        fp = e["fingerprint"]
        if e["kind"] == "fire":
            f = make(e["policy_id"], fp, e, e["ts"])
            if f:
                open_by_fp[fp] = f
                faults.append(f)
        elif e["kind"] == "resolve" and fp in open_by_fp:
            f = open_by_fp.pop(fp)
            f["status"], f["resolved_at"] = RESOLVED, _iso(e["ts"])
    for fp, f in open_by_fp.items():
        if fp not in firing:
            f["status"] = UNKNOWN
    for fp, row in firing.items():
        if fp not in open_by_fp:
            f = make(row["policy_id"], fp, row, row.get("fired_at"))
            if f:
                faults.append(f)
    faults.sort(key=lambda f: f["detected_at"] or "", reverse=True)
    return faults, warnings


# ---------------------------------------------------------------------------
# 2. 탐지 (조회)
# ---------------------------------------------------------------------------

async def signal_faults() -> tuple[list[dict], list[str]]:
    results = await asyncio.gather(*(prometheus_client.instant(s["query"]) for s in SIGNALS.values()))
    faults = [signal_fault(key, item) for key, result in zip(SIGNALS, results) for item in result]
    # instant() 는 오류 때도 빈 목록이라, 통째로 비면 Prometheus 장애인지 확인
    if not faults and not await prometheus_client.instant("vector(1)"):
        return [], ["PROMETHEUS_UNAVAILABLE"]
    return faults, []


async def alert_faults(since: datetime) -> tuple[list[dict], list[str]]:
    store = alert_service.store
    if not store.ready:
        return [], ["ALERT_STORE_NOT_CONFIGURED"]
    policies = {p["id"]: p for p in await store.list_policies()}
    firing = {r["fingerprint"]: r for r in await store.list_active(alert_service.FIRING)}
    # ponytail: 구간 안 이력이 5000건을 넘으면 오래된 쪽이 잘린다. 넘기 시작하면 fire, resolve 만 조회
    events = await store.list_events(None, since, 5000)
    return build_alert_faults(policies, firing, events)


def _since(days: int = HISTORY_DAYS) -> datetime:
    return datetime.now(timezone.utc) - timedelta(days=days)


async def active_faults() -> tuple[list[dict], list[str]]:
    """지금 걸려 있는 장애: firing 알람 + 고정 신호."""
    (from_alerts, w1), (from_signals, w2) = await asyncio.gather(alert_faults(_since()), signal_faults())
    faults = [f for f in from_alerts if f["status"] == ACTIVE] + from_signals
    faults.sort(key=lambda f: (f["severity"] != "critical", f["detected_at"] is None, f["detected_at"] or ""))
    return faults, w1 + w2


async def get_fault(fid: str) -> tuple[Optional[dict], list[str]]:
    (from_alerts, w1), (from_signals, w2) = await asyncio.gather(alert_faults(_since()), signal_faults())
    return next((f for f in from_alerts + from_signals if f["fault_id"] == fid), None), w1 + w2


# ---------------------------------------------------------------------------
# 3. 증거
# ---------------------------------------------------------------------------

BEFORE, AFTER = timedelta(minutes=30), timedelta(minutes=10)
MAX_SERIES, MAX_LOGS, MAX_PODS = 8, 20, 100


def evidence_window(detected_at: Optional[str], now: datetime) -> tuple[datetime, datetime]:
    """증거를 모을 구간. 발생 30분 전부터 10분 뒤(지금을 넘지 않음), 발생 시각을 모르면 최근 30분.

    >>> now = datetime(2026, 11, 11, 3, tzinfo=timezone.utc)
    >>> [d.strftime("%H:%M") for d in evidence_window("2026-11-11T02:00:00+00:00", now)]
    ['01:30', '02:10']
    >>> [d.strftime("%H:%M") for d in evidence_window("2026-11-11T02:55:00+00:00", now)]
    ['02:25', '03:00']
    >>> [d.strftime("%H:%M") for d in evidence_window(None, now)]
    ['02:30', '03:00']
    """
    if not detected_at:
        return now - BEFORE, now
    at = datetime.fromisoformat(detected_at)
    return at - BEFORE, min(at + AFTER, now)


def _sel(target: dict, **labels: str) -> str:
    """PromQL 셀렉터. labels 는 메트릭 라벨 이름 = target 키. target 에 없는 키는 뺀다."""
    parts = [f'cluster="{esc(cluster_label(target.get("cluster", DEFAULT_CLUSTER_NAME)))}"']
    parts += [f'{label}="{esc(str(target[key]))}"' for label, key in labels.items() if target.get(key)]
    return ",".join(parts)


def evidence_queries(fault: dict) -> dict[str, str]:
    """장애 유형과 대상에 맞는 메트릭 조회식. 대상을 좁힐 수 없으면 빈 사전.

    >>> q = evidence_queries({"fault_type": "workload_oom",
    ...                       "target": {"cluster": "mgmt", "namespace": "ml", "pod": "train-0", "container": "main"}})
    >>> q["memory_working_set_bytes"]
    'container_memory_working_set_bytes{cluster="",namespace="ml",pod="train-0",container="main"}'
    >>> sorted(q)
    ['memory_limit_bytes', 'memory_working_set_bytes', 'restarts_total']
    >>> evidence_queries({"fault_type": "workload_oom", "target": {"cluster": "l40s"}})
    {}
    >>> evidence_queries({"fault_type": "accelerator_error", "target": {
    ...     "cluster": "l40s", "vendor": "nvidia", "accelerator_id": "GPU-1"}})["temperature_celsius"]
    'DCGM_FI_DEV_GPU_TEMP{cluster="l40s",UUID="GPU-1"}'
    >>> q = evidence_queries({"fault_type": "npu_fault", "target": {
    ...     "cluster": "furiosa", "vendor": "furiosa", "accelerator_id": "npu0"}})
    >>> q["throttle_events"]  # 쓰로틀 이력은 지표가 있는 벤더만
    'furiosa_npu_throttling_events_count{label!="idle",cluster="furiosa",device="npu0"}'
    """
    from app.api.v2.accelerators import VENDOR_CONFIG

    kind, t = fault["fault_type"], fault["target"]
    if kind in ("accelerator_error", "npu_fault"):
        cfg = VENDOR_CONFIG.get(t.get("vendor") or ("nvidia" if kind == "accelerator_error" else ""))
        if not cfg or not (t.get("accelerator_id") or t.get("node")):
            return {}
        # ponytail: 노드만 아는 장애(로그 알람)는 호스트 라벨이 벤더마다 달라 클러스터 전체 카드를 MAX_SERIES 까지 본다
        extra = f',{cfg["id_label"]}="{esc(t["accelerator_id"])}"' if t.get("accelerator_id") else ""
        cluster = esc(cluster_label(t.get("cluster", DEFAULT_CLUSTER_NAME)))
        return {name: cfg[key].format(cluster=cluster, extra=extra)
                for name, key in (("utilization_percent", "util"), ("temperature_celsius", "temp"),
                                  ("power_watts", "power"), ("memory_used", "mem_used"),
                                  ("throttle_events", "throttle_events")) if key in cfg}
    if kind in ("workload_oom", "workload_crash_loop"):
        if not t.get("pod"):
            return {}
        sel = _sel(t, namespace="namespace", pod="pod", container="container")
        return {"memory_working_set_bytes": f"container_memory_working_set_bytes{{{sel}}}",
                "memory_limit_bytes": f'kube_pod_container_resource_limits{{resource="memory",{sel}}}',
                "restarts_total": f"kube_pod_container_status_restarts_total{{{sel}}}"}
    if kind == "node_not_ready" and t.get("node"):
        sel = _sel(t, node="node")
        return {"node_ready": f'kube_node_status_condition{{{sel},condition="Ready",status="true"}}',
                "node_pressure": f'kube_node_status_condition{{{sel},condition=~"MemoryPressure|DiskPressure|PIDPressure",'
                                 'status="true"}'}
    return {}


def summarize_series(name: str, result: list[dict]) -> list[dict]:
    """범위 조회 결과를 시계열별 최소, 최대, 마지막 값과 표본으로. 값이 없는 시계열은 뺀다.

    >>> summarize_series("temperature_celsius", [{"metric": {"__name__": "t", "UUID": "GPU-1"},
    ...                                           "values": [[1794366000, "70"], [1794366060, "NaN"], [1794366120, "88"]]}])
    [{'metric': 'temperature_celsius', 'labels': {'UUID': 'GPU-1'}, 'min': 70.0, 'max': 88.0, 'last': 88.0, \
'points': [['2026-11-11T03:00:00+00:00', 70.0], ['2026-11-11T03:02:00+00:00', 88.0]]}]
    >>> [s["labels"]["label"] for s in summarize_series("throttle_events", [  # 쓰로틀 사유별 시계열을 구분
    ...     {"metric": {"device": "npu0", "label": r}, "values": [[1794366000, "0"]]} for r in ("hw_power_cap", "thermal_slowdown")])]
    ['hw_power_cap', 'thermal_slowdown']
    """
    out = []
    for series in result[:MAX_SERIES]:
        points = [[datetime.fromtimestamp(float(ts), timezone.utc).isoformat(), v]
                  for ts, raw in series.get("values", []) if (v := parse_finite(raw)) is not None]
        if not points:
            continue
        values = [p[1] for p in points]
        # label 은 Furiosa 쓰로틀 지표의 사유(hw_power_cap 등). 없으면 사유별 시계열이 같은 라벨로 보여 구분되지 않음
        labels = {k: v for k, v in series.get("metric", {}).items()
                  if k in ("UUID", "device", "uuid", "Hostname", "hostname", "container", "condition", "node", "label")}
        out.append({"metric": name, "labels": labels, "min": min(values), "max": max(values), "last": values[-1],
                    "points": points})
    return out


def log_pattern(fault: dict) -> Optional[str]:
    """장애 유형에 맞는 로그 정규식. 알람 로그 지표와 로그 API 의 드라이버 패턴을 그대로 쓴다."""
    from app.api.v2.logs import _DRIVER_PATTERNS

    rx = {k: v[1] for k, v in alert_service.LOG_METRICS.items()}
    kind = fault["fault_type"]
    if kind == "accelerator_error":
        return rx["log_gpu_xid"]
    if kind == "npu_fault":
        return _DRIVER_PATTERNS.get(fault["target"].get("vendor"), "(?i)furiosa|rbln|npu")
    return {"workload_oom": rx["log_oom"], "workload_crash_loop": rx["log_crash_loop"],
            "hardware_fault": f"{rx['log_hw_error']}|{rx['log_disk_error']}",
            "node_not_ready": r"(?i)(node|kubelet).*(not ?ready|stopped posting)"}.get(kind)


async def _metrics(fault: dict, start: datetime, end: datetime) -> tuple[list[dict], str]:
    queries = evidence_queries(fault)
    if not queries:
        return [], "skipped"
    results = await asyncio.gather(*(
        prometheus_client.range_query(q, start.isoformat(), end.isoformat(), "60s") for q in queries.values()))
    series = [s for name, result in zip(queries, results) for s in summarize_series(name, result)]
    return series, "ok" if series else "empty"


async def _logs(fault: dict, node: Optional[str], start: datetime, end: datetime) -> tuple[list[dict], str]:
    from app.api.v2.logs import _scoped, _transform_loki_response

    pattern = log_pattern(fault)
    if pattern is None:
        return [], "skipped"
    if not loki_client.configured:
        return [], "not_configured"
    t = fault["target"]

    def q(rx: str) -> str:
        return rx.replace("\\", "\\\\").replace('"', '\\"')

    selector = f'{{container!="loki"}} |~ "{q(pattern)}"'
    if fault["fault_type"] == "npu_fault":
        # NPU 드라이버 패턴(furiosa, npu 등)은 operator 의 정상 로그까지 걸려 상한(MAX_LOGS)을 INFO 줄로 채운다.
        # 오류 로그 알람과 같은 오류 단어, 제외 규칙으로 오류 줄만 남긴다
        selector += (f' |~ "{q(alert_service.LOG_METRICS["log_error_count"][1])}"'
                     f' !~ "{q(alert_service.LOG_EXCLUDE["log_error_count"])}"')
    try:
        # 커널 OOM, XID 는 노드 저널에 남으므로 Pod 가 아니라 노드로 좁힌다. Loki 자신의 쿼리 기록은 제외
        logql = _scoped(selector, cluster=t.get("cluster"), node=node or t.get("node"))
        resp = await loki_client.query_range(logql, start.isoformat(), end.isoformat(), MAX_LOGS)
    except HTTPException as exc:
        logger.warning("fault logs unavailable: %s", exc.detail)
        return [], "unavailable"
    entries, _ = _transform_loki_response(resp, MAX_LOGS)
    keep = ("node", "namespace", "pod", "container", "unit")
    out = [{"timestamp": e.timestamp, "message": e.message[:500],
            "labels": {k: v for k, v in e.labels.items() if k in keep}, "detected_fields": e.detected_fields}
           for e in entries]
    return out, "ok" if out else "empty"


async def _pods(selector: str, via: str) -> list[dict]:
    """노드에서 지금 돌거나 대기 중인 Pod. 끝난 Job Pod(Succeeded, Failed)는 영향 대상이 아니라 뺀다."""
    rows = await prometheus_client.instant(
        f"kube_pod_info{{{selector}}} * on (cluster, namespace, pod) group_left() "
        '(kube_pod_status_phase{phase=~"Running|Pending"} == 1)')
    return [{"namespace": m.get("namespace"), "pod": m.get("pod"), "via": via,
             "workload": f"{m['created_by_kind']}/{m['created_by_name']}" if m.get("created_by_kind") else None}
            for m in (r.get("metric", {}) for r in rows)]


async def _k8s_cluster_of(node: str) -> Optional[str]:
    """K8s 노드 이름이 있는 클러스터(API 이름). 없거나 여러 클러스터에 같은 이름이 있으면 None.

    VM 장애는 OpenStack 지표에서 나와 cluster 라벨이 없어 mgmt 로 잡히지만, 그 VM 이 서비스 클러스터의
    워커 노드면 영향을 받는 Pod 는 그 클러스터에 있다.
    """
    rows = await prometheus_client.instant(f'count by (cluster) (kube_node_info{{node="{esc(node)}"}})')
    names = {r.get("metric", {}).get("cluster") or DEFAULT_CLUSTER_NAME for r in rows}
    return names.pop() if len(names) == 1 else None


async def _related(fault: dict) -> tuple[dict, str]:
    """영향 자원: 대상에서 물리서버까지의 경로와 영향을 받는 Pod. 범위를 정할 수 없으면 unknown (0 으로 채우지 않음)."""
    from app.api.v2.resource_map import trace_node, trace_pod

    t = fault["target"]
    cluster = t.get("cluster", DEFAULT_CLUSTER_NAME)
    empty = {"scope": None, "path": [], "affected_pod_count": None, "affected_pods": None}
    try:
        if t.get("pod"):
            trace = await trace_pod(t["pod"], cluster, t.get("namespace"))
            if trace.status == "ambiguous":
                return empty, "unknown"
            path = [h.model_dump(exclude_none=True) for h in trace.path]
            workload = next((h.name for h in trace.path if h.layer == "workload" and h.status == "ok"), None)
            return {"scope": "pod", "path": path, "affected_pod_count": 1,
                    "affected_pods": [{"namespace": trace.namespace, "pod": trace.pod, "workload": workload,
                                       "via": "장애 대상"}]}, "ok"
        node = t.get("node") or t.get("vm")  # VM 은 K8s 노드 이름과 같을 때만 Pod 를 찾을 수 있음
        if not node:
            return empty, "unknown"
        if not t.get("node"):  # VM 대상: 같은 이름의 K8s 노드가 있는 클러스터에서 찾음
            cluster = await _k8s_cluster_of(node) or cluster
        hops = await trace_node(cluster, node)
        path = [h.model_dump(exclude_none=True) for h in hops]
        if not any(h.layer == "k8s_node" and h.status == "ok" for h in hops):
            # K8s 노드가 아니면(쿠버네티스 없는 단독 VM, 지운 VM) Pod 로 영향을 셀 수 없어 0 이 아니라 null.
            # VM 까지 찾았으면 범위는 VM, 경로를 하나도 못 찾았으면 unknown
            vm_ok = any(h.layer == "vm" and h.status == "ok" for h in hops)
            return ({"scope": "vm" if vm_ok else None, "path": path, "affected_pod_count": None, "affected_pods": None},
                    "ok" if any(h.status == "ok" for h in hops) else "unknown")
        scope, pods = "node", None
        if t.get("accelerator_id") and t.get("vendor") == "nvidia":  # 그 카드를 쓰는 Pod 만
            rows = await prometheus_client.instant(
                f'DCGM_FI_DEV_GPU_UTIL{{{_sel(t, UUID="accelerator_id")},exported_pod!=""}}')
            if rows:
                scope = "accelerator"
                pods = [{"namespace": r["metric"].get("exported_namespace"), "pod": r["metric"].get("exported_pod"),
                         "via": "DCGM exported_pod", "workload": None} for r in rows]
            elif await prometheus_client.instant(
                    f'count(DCGM_FI_DEV_GPU_UTIL{{{_sel({**t, "cluster": cluster})},exported_pod!=""}})'):
                # 이 클러스터 exporter 는 카드별 Pod 를 알려 주는데 이 카드에는 없음: 카드를 쓰는 Pod 0.
                # 카드별 Pod 를 아예 알려 주지 않는 exporter 면 아래에서 노드의 Pod 전체로 넓힘 (scope node)
                scope, pods = "accelerator", []
        if pods is None:
            pods = await _pods(_sel({**t, "cluster": cluster, "node": node}, node="node"), "kube_pod_info.node")
        return {"scope": scope, "path": path, "affected_pod_count": len(pods), "affected_pods": pods[:MAX_PODS]}, "ok"
    except HTTPException as exc:  # 404: Pod 가 이미 없음, 503: Prometheus 조회 실패
        return empty, "unknown" if exc.status_code == 404 else "unavailable"


async def collect_evidence(fault: dict, now: Optional[datetime] = None) -> dict:
    """장애 하나의 증거 스냅샷. 수집에 실패한 부분은 비워 두고 collection_status 에 이유를 남긴다."""
    start, end = evidence_window(fault["detected_at"], now or datetime.now(timezone.utc))
    (related, r_status), (metrics, m_status) = await asyncio.gather(_related(fault), _metrics(fault, start, end))
    node = next((h.get("name") for h in related["path"] if h.get("layer") == "k8s_node"), None)
    logs, l_status = await _logs(fault, node, start, end)
    return {"window": {"start": start.isoformat(), "end": end.isoformat()},
            "metrics_snapshot": metrics, "logs_snapshot": logs, "related_resources": related,
            "collection_status": {"metrics": m_status, "logs": l_status, "resource_map": r_status}}


# ---------------------------------------------------------------------------
# 4. 분석
# ---------------------------------------------------------------------------

THROTTLE_TEMP_C = 85  # 벤더를 모를 때 쓰는 값. 알면 가속기 API 의 벤더별 쓰로틀 진입 온도를 쓴다


def _peak(evidence: dict, metric: str) -> Optional[float]:
    values = [s["max"] for s in evidence["metrics_snapshot"] if s["metric"] == metric]
    return max(values) if values else None


def analyze(fault: dict, evidence: dict) -> dict:
    """증거로 추정 원인, 기여 요인, 확신 정도를 낸다. 규칙 기반이고 추정이다.

    확신: 코드나 종료 사유처럼 원인을 직접 가리키는 증거가 있으면 high, 탐지 신호뿐이면 medium,
    증거 수집이 빠져 확인하지 못했으면 low.

    >>> ev = {"metrics_snapshot": [{"metric": "temperature_celsius", "max": 91.0}],
    ...       "logs_snapshot": [{"detected_fields": {"xid_code": 79}}],
    ...       "collection_status": {"metrics": "ok", "logs": "ok", "resource_map": "ok"}}
    >>> a = analyze({"fault_type": "accelerator_error", "trigger": "log_gpu_xid"}, ev)
    >>> (a["probable_cause"], a["confidence"], a["contributing_factors"], a["evidence_basis"])
    ('XID 79: GPU has fallen off the bus (원인 후보: 하드웨어)', 'high', ['장애 구간 최고 온도 91°C (쓰로틀 기준 85°C 이상)'], ['logs', 'metrics'])
    >>> ev = {"metrics_snapshot": [{"metric": "memory_working_set_bytes", "max": 950.0},
    ...                            {"metric": "memory_limit_bytes", "max": 1000.0}],
    ...       "logs_snapshot": [], "collection_status": {"metrics": "ok", "logs": "unavailable", "resource_map": "ok"}}
    >>> a = analyze({"fault_type": "workload_oom", "trigger": "oom_killed"}, ev)
    >>> (a["probable_cause"], a["confidence"], a["contributing_factors"])
    ('컨테이너가 메모리 한도를 넘어 종료됨 (OOMKilled)', 'high', ['메모리 사용량이 한도의 95%까지 도달'])
    >>> ev = {"metrics_snapshot": [], "logs_snapshot": [],
    ...       "collection_status": {"metrics": "skipped", "logs": "unavailable", "resource_map": "unknown"}}
    >>> a = analyze({"fault_type": "accelerator_error", "trigger": "stale_seconds"}, ev)
    >>> (a["probable_cause"], a["confidence"])
    ('메트릭 수집이 끊김. exporter, 노드, VM 중 하나가 멈춘 상태로 추정', 'low')
    >>> ev = {"metrics_snapshot": [{"metric": "temperature_celsius", "max": 87.0}, {"metric": "throttle_events", "max": 2.0}],
    ...       "logs_snapshot": [], "collection_status": {"metrics": "ok", "logs": "empty", "resource_map": "ok"}}
    >>> a = analyze({"fault_type": "npu_fault", "trigger": "npu_unhealthy", "target": {"vendor": "rebellions"}}, ev)
    >>> (a["confidence"], a["contributing_factors"], a["evidence_basis"])  # 리벨리온 쓰로틀 기준은 90°C 라 온도는 빠짐
    ('medium', ['장애 구간에 쓰로틀(클럭 제한) 발생'], ['metrics', 'signal'])
    >>> ev = {"metrics_snapshot": [{"metric": "temperature_celsius", "max": 40.0}], "logs_snapshot": [],
    ...       "collection_status": {"metrics": "ok", "logs": "empty", "resource_map": "ok"}}
    >>> analyze({"fault_type": "npu_fault", "trigger": "temperature_celsius", "source": "alert",
    ...          "target": {"vendor": "furiosa"}}, ev)["evidence_basis"]  # 알람으로 잡은 장애는 알람이 근거
    ['alert']
    """
    kind, trigger = fault["fault_type"], fault.get("trigger")
    fields = [e["detected_fields"] for e in evidence["logs_snapshot"] if e.get("detected_fields")]
    factors: list[str] = []
    basis: set[str] = set()
    direct = False  # 원인을 직접 가리키는 증거가 있는가
    cause = FAULT_TYPES[kind]

    if kind in ("accelerator_error", "npu_fault"):
        code = fault.get("xid_code") or next((f["xid_code"] for f in fields if "xid_code" in f), None)
        if code:
            desc, origin = XID_CODES.get(code, ("", ""))
            cause = f"XID {code}" + (f": {desc} (원인 후보: {origin})" if desc else " (목록에 없는 코드)")
            direct = bool(desc)
            basis.add("signal" if fault.get("xid_code") else "logs")
        elif trigger == "stale_seconds":
            cause = "메트릭 수집이 끊김. exporter, 노드, VM 중 하나가 멈춘 상태로 추정"
        elif trigger == "temperature_celsius":
            cause = "가속기 온도가 정책 임계값을 넘음"
        elif trigger == "unhealthy":
            cause = "가속기 상태 지표가 정상값이 아님"
        elif trigger == "npu_dead":
            cause = "NPU 가 응답하지 않음 (exporter 가 alive 0 으로 보고). 장치, 드라이버, PCI 연결 중 하나의 문제로 추정"
            basis.add("signal")
        elif trigger == "npu_unhealthy":
            cause = "NPU 드라이버가 장치 상태를 비정상으로 보고 (HEALTH 가 0 이 아님)"
            basis.add("signal")
        from app.api.v2.accelerators import VENDOR_CONFIG

        limit = VENDOR_CONFIG.get((fault.get("target") or {}).get("vendor"), {}).get("throttle_temp", THROTTLE_TEMP_C)
        temp = _peak(evidence, "temperature_celsius")
        if temp is not None and temp >= limit:
            factors.append(f"장애 구간 최고 온도 {temp:g}°C (쓰로틀 기준 {limit}°C 이상)")
            basis.add("metrics")
        if _peak(evidence, "throttle_events"):
            factors.append("장애 구간에 쓰로틀(클럭 제한) 발생")
            basis.add("metrics")
    elif kind == "workload_oom":
        cuda = next((f for f in fields if f.get("error_type") == "CUDA OOM"), None)
        kernel = next((f for f in fields if f.get("error_type") == "OOM"), None)
        if cuda:
            cause = (f"가속기 메모리 부족 (CUDA OOM, 요청 {cuda['requested_gb']:g}GiB, "
                     f"이미 할당 {cuda['allocated_gb']:g}GiB)")
            direct = True
            basis.add("logs")
        elif kernel:
            cause = f"커널 OOM killer 가 프로세스 {kernel['process']}(pid {kernel['pid']}) 종료"
            direct = True
            basis.add("logs")
        elif trigger == "oom_killed":
            cause, direct = "컨테이너가 메모리 한도를 넘어 종료됨 (OOMKilled)", True
            basis.add("signal")
        used, limit = _peak(evidence, "memory_working_set_bytes"), _peak(evidence, "memory_limit_bytes")
        if used and limit and used / limit >= 0.9:
            factors.append(f"메모리 사용량이 한도의 {used / limit * 100:.0f}%까지 도달")
            basis.add("metrics")
    elif kind == "workload_crash_loop":
        cause = "컨테이너가 시작 후 반복해서 종료됨"
        if any(f.get("error_type") in ("OOM", "CUDA OOM") for f in fields):
            factors.append("같은 구간에 메모리 부족 종료 로그가 있음")
            basis.add("logs")
    elif kind == "node_not_ready":
        pressure = sorted({s["labels"].get("condition") for s in evidence["metrics_snapshot"]
                           if s["metric"] == "node_pressure" and s["max"] >= 1 and s["labels"].get("condition")})
        if pressure:
            cause, direct = f"노드 자원 압박 ({', '.join(pressure)})", True
            basis.add("metrics")
        else:
            cause = "kubelet 이 상태를 보고하지 않음. 노드 중단이나 네트워크 단절로 추정"
    elif kind == "vm_down":
        cause, direct = "OpenStack 이 VM 상태를 ERROR 로 보고", True
        basis.add("signal")
    elif kind == "hardware_fault":
        cause = "커널이 하드웨어 또는 디스크 오류를 기록"
    if fault.get("source") in ("alert", "signal"):  # 장애를 잡은 탐지(알람, 고정 신호) 자체도 원인 판단의 근거
        basis.add(fault["source"])

    status = evidence["collection_status"]
    missing = [k for k in ("metrics", "logs", "resource_map") if status.get(k) in ("unavailable", "unknown")]
    confidence = "high" if direct else "low" if missing else "medium"
    return {"probable_cause": cause, "contributing_factors": factors, "confidence": confidence,
            "evidence_basis": sorted(basis), "unverified": missing}
