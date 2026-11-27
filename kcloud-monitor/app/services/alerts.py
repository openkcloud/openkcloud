"""
KCloud Monitor v2 — 알람 서비스.

네 부품으로 구성된다.
  1. AlertStore   : PostgreSQL(asyncpg) 저장소. policies / channels / alerts / events 4개 테이블.
  2. 평가기        : kind=resource 는 가속기 메트릭(Prometheus), kind=log 는 로그 단어 건수(Loki),
                     kind=slo 는 vLLM 서빙 지연(TTFT, TPOT p95)과 대기 요청 수(Prometheus),
                     kind=composite 는 조건 2개 이상을 같은 클러스터에서 모두 만족할 때(그리고).
  3. 상태머신      : pending -> firing -> resolved. plan_transition() 은 순수 함수라 DB 없이 테스트한다.
  4. 발송기        : webhook(httpx), email(smtplib).

run_loop() 가 ALERT_EVAL_INTERVAL_SEC 마다 정책을 전부 평가한다. 이 모듈은 Alerter 가 별도 서비스로
분리될 때 통째로 들어낼 수 있도록 다른 라우터에 의존하지 않는다(가속기 수집 함수만 재사용).
"""
from __future__ import annotations

import asyncio
import json
import logging
import smtplib
import uuid
from datetime import datetime, timedelta, timezone
from email.message import EmailMessage
from typing import Any, Optional

import httpx

from app.api.v2.slo import TPOT, TTFT, _selector
from app.config import settings

logger = logging.getLogger(__name__)

PENDING, FIRING, RESOLVED = "pending", "firing", "resolved"

# 리소스 지표 키. 값은 (설명, 단위). 평가기 _resource_value() 와 1:1.
RESOURCE_METRICS: dict[str, tuple[str, str]] = {
    "utilization_percent": ("가속기 사용률", "%"),
    "memory_used_percent": ("가속기 메모리 사용률", "%"),
    "temperature_celsius": ("가속기 온도", "°C"),
    "power_watts": ("가속기 전력", "W"),
    "power_tdp_percent": ("TDP 대비 전력", "%"),
    "unhealthy": ("하드웨어 오류 (1=오류)", ""),
    "stale_seconds": ("마지막 수집 후 경과 시간", "s"),
}

# 로그 지표 키. 값은 (설명, 정규식). 화면은 키만 고르고 정규식은 서버가 붙임. 평가기 _logql() 과 1:1.
LOG_METRICS: dict[str, tuple[str, str]] = {
    "log_error_count": ("오류 로그 건수", r"(?i)\b(error|err|fatal|panic)\b"),
    "log_gpu_xid": ("GPU XID 오류 건수", r"NVRM: Xid"),
    "log_oom": ("메모리 부족 종료 건수", r"(?i)(Out of memory|oom-kill|OOMKilled|Killed process)"),
    "log_disk_error": ("디스크 오류 건수", r"(?i)(I/O error|EXT4-fs error|read-only file system)"),
    "log_crash_loop": ("비정상 종료 반복 건수", r"(?i)(segfault|Failed to start|Back-off restarting)"),
    "log_hw_error": ("커널 하드웨어 오류 건수", r"(Hardware Error|mce:)"),
    "log_total_count": ("전체 로그 건수", ""),  # 정규식 없음. 작업 로그가 멈췄는지(0건) 볼 때 씀
}

# 로그 지표별 제외 정규식. 단어는 걸리지만 오류가 아닌 줄을 뺀다.
# 빈 오류 필드("error":"", err=<nil>)가 있는 접속 기록과 스스로 info, debug 라고 밝힌 줄 (2026-10-21 mgmt 실측 5분 213 -> 148건)
LOG_EXCLUDE: dict[str, str] = {
    "log_error_count": r'(?i)("err(or)?"\s*:\s*(""|null)|\berr(or)?=(""|<nil>)|\blevel=(info|debug)\b|"level"\s*:\s*"(info|debug)")',
}


# SLO 지표 키. 값은 (설명, 단위, PromQL). {sel} 은 대상 셀렉터, {w} 는 window_min 분. 대상은 (cluster, model_name).
_TTFT_P95 = "histogram_quantile(0.95, sum by (le, cluster, model_name) (rate({m}_bucket{{sel}}[{{w}}m]))) * 1000"
SLO_METRICS: dict[str, tuple[str, str, str]] = {
    "slo_ttft_p95_ms": ("LLM 첫 글자까지 걸린 시간 p95 (TTFT)", "ms",
                        _TTFT_P95.format(m=TTFT)),
    "slo_tpot_p95_ms": ("LLM 글자 하나당 걸린 시간 p95 (TPOT)", "ms",
                        _TTFT_P95.format(m=TPOT)),
    "slo_requests_waiting": ("LLM 대기 요청 수", "건",
                             "sum by (cluster, model_name) (vllm:num_requests_waiting{sel})"),
}


def metric_info(key: str) -> tuple[str, str]:
    """지표 키 → (화면 이름, 단위). 로그 지표는 건"""
    if key in RESOURCE_METRICS:
        return RESOURCE_METRICS[key]
    if key in SLO_METRICS:
        return SLO_METRICS[key][:2]
    if key in LOG_METRICS:
        return LOG_METRICS[key][0], "건"
    return key, ""


def metric_kind(key: str) -> Optional[str]:
    """지표 키로 정책 종류를 정함. 목록에 없으면 None."""
    if key in RESOURCE_METRICS:
        return "resource"
    if key in LOG_METRICS:
        return "log"
    if key in SLO_METRICS:
        return "slo"
    return None


OPS = {
    ">=": lambda a, b: a >= b,
    ">": lambda a, b: a > b,
    "<=": lambda a, b: a <= b,
    "<": lambda a, b: a < b,
    "==": lambda a, b: a == b,
    "!=": lambda a, b: a != b,
}

SCHEMA_SQL = """
CREATE TABLE IF NOT EXISTS alert_policies (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    kind          TEXT NOT NULL CHECK (kind IN ('resource','log','slo','composite')),
    metric        TEXT NOT NULL,
    op            TEXT NOT NULL DEFAULT '>=',
    threshold     DOUBLE PRECISION NOT NULL,
    window_min    INTEGER NOT NULL DEFAULT 5,
    for_min       INTEGER NOT NULL DEFAULT 0,
    severity      TEXT NOT NULL DEFAULT 'warning',
    target        JSONB NOT NULL DEFAULT '{}'::jsonb,
    channel_ids   JSONB NOT NULL DEFAULT '[]'::jsonb,
    enabled       BOOLEAN NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS alert_channels (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    type          TEXT NOT NULL CHECK (type IN ('webhook','email')),
    config        JSONB NOT NULL DEFAULT '{}'::jsonb,
    notify_resolved BOOLEAN NOT NULL DEFAULT TRUE,
    enabled       BOOLEAN NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS alert_active (
    fingerprint   TEXT PRIMARY KEY,
    policy_id     TEXT NOT NULL REFERENCES alert_policies(id) ON DELETE CASCADE,
    state         TEXT NOT NULL,
    severity      TEXT NOT NULL,
    target        JSONB NOT NULL,
    value         DOUBLE PRECISION,
    message       TEXT NOT NULL DEFAULT '',
    started_at    TIMESTAMPTZ NOT NULL,
    fired_at      TIMESTAMPTZ,
    resolved_at   TIMESTAMPTZ,
    last_notified_at TIMESTAMPTZ,
    acked_at      TIMESTAMPTZ,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS alert_active_policy_idx ON alert_active(policy_id, state);
CREATE TABLE IF NOT EXISTS alert_events (
    id            BIGSERIAL PRIMARY KEY,
    ts            TIMESTAMPTZ NOT NULL DEFAULT now(),
    policy_id     TEXT NOT NULL,
    fingerprint   TEXT NOT NULL,
    kind          TEXT NOT NULL,
    severity      TEXT NOT NULL,
    target        JSONB NOT NULL,
    value         DOUBLE PRECISION,
    message       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS alert_events_ts_idx ON alert_events(ts DESC);
-- slo, composite 종류 추가 전에 만든 테이블의 CHECK 제약 갱신
ALTER TABLE alert_policies DROP CONSTRAINT IF EXISTS alert_policies_kind_check;
ALTER TABLE alert_policies ADD CONSTRAINT alert_policies_kind_check CHECK (kind IN ('resource','log','slo','composite'));
-- 조건 목록(metric, op, threshold, window_min 의 배열). 첫 조건은 기존 열에도 같이 써서 옛 화면과 호환
ALTER TABLE alert_policies ADD COLUMN IF NOT EXISTS conditions JSONB NOT NULL DEFAULT '[]'::jsonb;
"""

# 첫 기동 시 시드. 포탈에서 숫자만 바꿔 쓰는 것을 전제로 한다.
# 시험장에서 확실히 재현되는 것만 둔다. 지표 키는 모두 남아 있어 필요하면 화면에서 만들면 된다.
# - 실제 고장이 나야 울려 뺌: 하드웨어 오류(res-005), GPU XID(log-002), 디스크 오류(log-004), 커널 하드웨어 오류(log-006)
# - 부하로 닿는다는 보장이 없어 뺌: 온도 80°C(res-003, 실측 최고 58°C), TDP 90%(res-004, 실측 최고 80%),
#   비정상 종료 반복(log-005), 작업 로그 멈춤 조합(VM 도커 로그 미수집)
# ponytail: 쓰로틀링(res-006)·ROW_REMAP(res-007)·PCIe(res-008)는 원천 지표가 더 필요해 뺐다.
SEED_POLICIES: list[dict] = [
    dict(id="res-001", name="가속기 사용률 포화", kind="resource", metric="utilization_percent", op=">=",
         threshold=90, for_min=10, severity="warning", description="가속기가 꽉 차서 더 못 받는 상태"),
    dict(id="res-002", name="가속기 메모리 포화", kind="resource", metric="memory_used_percent", op=">=",
         threshold=90, for_min=10, severity="warning", description="메모리 부족으로 작업 실패 직전"),
    dict(id="res-009", name="가속기 수집 중단", kind="resource", metric="stale_seconds", op=">=",
         threshold=300, for_min=0, severity="critical", description="exporter나 VM이 죽어 메트릭이 안 들어옴"),
    dict(id="log-001", name="오류 로그 급증", kind="log", metric="log_error_count", op=">=",
         threshold=20, window_min=5, for_min=0, severity="warning", description="5분 동안 오류 단어 20건 이상"),
    dict(id="log-003", name="메모리 부족 종료", kind="log", metric="log_oom",
         op=">=", threshold=1, window_min=5, for_min=0, severity="warning",
         description="프로세스가 메모리 부족으로 강제 종료됨"),
    dict(id="slo-001", name="LLM 첫 응답 지연", kind="slo", metric="slo_ttft_p95_ms", op=">=",
         threshold=500, window_min=5, for_min=2, severity="warning",
         description="최근 5분 TTFT p95 가 목표 500ms 이상. 대기 줄이나 KV 캐시 부족 의심"),
    dict(id="slo-002", name="LLM 생성 속도 저하", kind="slo", metric="slo_tpot_p95_ms", op=">=",
         threshold=50, window_min=5, for_min=2, severity="warning",
         description="최근 5분 TPOT p95 가 목표 50ms 이상. 동시 처리 과다나 GPU 쓰로틀링 의심"),
    # 조합: 사용률 100% 자체는 정상 학습일 수 있어, 로그나 응답 속도를 같이 봐야 진짜 문제만 잡힘
    dict(id="pol-001", name="GPU 점유 중 오류 반복", kind="composite",
         conditions=[dict(metric="utilization_percent", op=">=", threshold=95, window_min=5),
                     dict(metric="log_error_count", op=">=", threshold=20, window_min=5)],
         for_min=3, severity="critical", target={"cluster": "l40s"},
         description="가속기는 꽉 찼는데 같은 클러스터에서 5분 동안 오류 로그 20건 이상. 결과 없이 자원만 쓰는 상태 의심"),
    dict(id="pol-002", name="GPU 과부하로 서비스 지연", kind="composite",
         conditions=[dict(metric="utilization_percent", op=">=", threshold=95, window_min=5),
                     dict(metric="slo_ttft_p95_ms", op=">=", threshold=500, window_min=5)],
         for_min=3, severity="critical", target={"cluster": "l40s"},
         description="가속기가 꽉 찬 상태에서 LLM 첫 응답까지 5분 p95 가 500ms 이상. 요청이 처리 능력을 넘은 상태"),
]


def _now() -> datetime:
    return datetime.now(timezone.utc)


def _new_id(prefix: str) -> str:
    return f"{prefix}-{uuid.uuid4().hex[:6]}"


def _conditions_of(p: dict) -> list[dict]:
    """정책의 조건 목록. conditions 가 비어 있으면(옛 행, 시드) 단일 조건 열에서 만든다."""
    conds = p.get("conditions") or [p]
    return [{"metric": c["metric"], "op": c.get("op", ">="), "threshold": float(c["threshold"]),
             "window_min": int(c.get("window_min", 5))} for c in conds]


def _row(r: Any) -> dict:
    """asyncpg Record -> dict. JSONB 는 문자열로 오므로 복원."""
    d = dict(r)
    for k in ("target", "channel_ids", "config", "conditions"):
        if isinstance(d.get(k), str):
            d[k] = json.loads(d[k])
    if "metric" in d:
        d["conditions"] = _conditions_of(d)
    return d


# ---------------------------------------------------------------------------
# 1. 저장소
# ---------------------------------------------------------------------------

class AlertStore:
    def __init__(self) -> None:
        self.pool = None

    @property
    def ready(self) -> bool:
        return self.pool is not None

    async def connect(self) -> None:
        import asyncpg  # 선택 의존성. DATABASE_URL 미설정 환경에서는 import 자체를 피함

        self.pool = await asyncpg.create_pool(settings.DATABASE_URL, min_size=1, max_size=4)
        async with self.pool.acquire() as con:
            await con.execute(SCHEMA_SQL)
            # 종류별로 정책이 하나도 없을 때만 시드. 사용자가 지운 시드가 재기동 때 되살아나지 않게 함
            for kind in ("resource", "log", "slo", "composite"):
                if await con.fetchval("SELECT count(*) FROM alert_policies WHERE kind=$1", kind) == 0:
                    seeds = [p for p in SEED_POLICIES if p["kind"] == kind]
                    for p in seeds:
                        await self.create_policy(p)
                    logger.info("alert policies seeded: kind=%s %d", kind, len(seeds))

    async def close(self) -> None:
        if self.pool:
            await self.pool.close()
            self.pool = None

    # ── policies ──
    async def list_policies(self) -> list[dict]:
        rows = await self.pool.fetch("SELECT * FROM alert_policies ORDER BY id")
        return [_row(r) for r in rows]

    async def get_policy(self, pid: str) -> Optional[dict]:
        r = await self.pool.fetchrow("SELECT * FROM alert_policies WHERE id=$1", pid)
        return _row(r) if r else None

    async def create_policy(self, p: dict) -> dict:
        p = {**p}
        p.setdefault("id", _new_id("pol"))
        conds = _conditions_of(p)
        first = conds[0]
        kind = p.get("kind") or ("composite" if len(conds) > 1 else metric_kind(first["metric"]))
        await self.pool.execute(
            """INSERT INTO alert_policies
               (id,name,description,kind,metric,op,threshold,window_min,for_min,severity,target,channel_ids,enabled,
                conditions)
               VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb,$12::jsonb,$13,$14::jsonb)""",
            p["id"], p["name"], p.get("description", ""), kind, first["metric"], first["op"],
            first["threshold"], first["window_min"], int(p.get("for_min", 0)),
            p.get("severity", "warning"), json.dumps(p.get("target") or {}),
            json.dumps(p.get("channel_ids") or []), bool(p.get("enabled", True)), json.dumps(conds),
        )
        return await self.get_policy(p["id"])

    async def update_policy(self, pid: str, patch: dict) -> Optional[dict]:
        cur = await self.get_policy(pid)
        if not cur:
            return None
        merged = {**cur, **{k: v for k, v in patch.items() if v is not None}}
        conds = _conditions_of(merged)
        first = conds[0]
        await self.pool.execute(
            """UPDATE alert_policies SET name=$2,description=$3,kind=$4,metric=$5,op=$6,threshold=$7,window_min=$8,
               for_min=$9,severity=$10,target=$11::jsonb,channel_ids=$12::jsonb,enabled=$13,conditions=$14::jsonb,
               updated_at=now()
               WHERE id=$1""",
            pid, merged["name"], merged["description"], merged["kind"], first["metric"], first["op"],
            first["threshold"], first["window_min"], int(merged["for_min"]), merged["severity"],
            json.dumps(merged["target"]), json.dumps(merged["channel_ids"]), bool(merged["enabled"]),
            json.dumps(conds),
        )
        return await self.get_policy(pid)

    async def delete_policy(self, pid: str) -> bool:
        return (await self.pool.execute("DELETE FROM alert_policies WHERE id=$1", pid)).endswith("1")

    async def clear_active(self, pid: str) -> None:
        """정책 종류가 바뀌어 대상 단위가 달라지면 옛 알람은 영영 안 풀리므로 지운다."""
        await self.pool.execute("DELETE FROM alert_active WHERE policy_id=$1", pid)

    # ── channels ──
    async def list_channels(self) -> list[dict]:
        return [_row(r) for r in await self.pool.fetch("SELECT * FROM alert_channels ORDER BY created_at")]

    async def get_channel(self, cid: str) -> Optional[dict]:
        r = await self.pool.fetchrow("SELECT * FROM alert_channels WHERE id=$1", cid)
        return _row(r) if r else None

    async def create_channel(self, c: dict) -> dict:
        cid = c.get("id") or _new_id("ch")
        await self.pool.execute(
            "INSERT INTO alert_channels (id,name,type,config,notify_resolved,enabled) VALUES ($1,$2,$3,$4::jsonb,$5,$6)",
            cid, c["name"], c["type"], json.dumps(c.get("config") or {}),
            bool(c.get("notify_resolved", True)), bool(c.get("enabled", True)),
        )
        return await self.get_channel(cid)

    async def update_channel(self, cid: str, patch: dict) -> Optional[dict]:
        cur = await self.get_channel(cid)
        if not cur:
            return None
        m = {**cur, **{k: v for k, v in patch.items() if v is not None}}
        await self.pool.execute(
            "UPDATE alert_channels SET name=$2,config=$3::jsonb,notify_resolved=$4,enabled=$5 WHERE id=$1",
            cid, m["name"], json.dumps(m["config"]), bool(m["notify_resolved"]), bool(m["enabled"]),
        )
        return await self.get_channel(cid)

    async def delete_channel(self, cid: str) -> bool:
        return (await self.pool.execute("DELETE FROM alert_channels WHERE id=$1", cid)).endswith("1")

    # ── active / events ──
    async def active_for_policy(self, pid: str) -> dict[str, dict]:
        rows = await self.pool.fetch(
            "SELECT * FROM alert_active WHERE policy_id=$1 AND state IN ($2,$3)", pid, PENDING, FIRING)
        return {r["fingerprint"]: _row(r) for r in rows}

    async def list_active(self, state: Optional[str] = None) -> list[dict]:
        if state:
            rows = await self.pool.fetch(
                "SELECT * FROM alert_active WHERE state=$1 ORDER BY started_at DESC", state)
        else:
            rows = await self.pool.fetch(
                "SELECT * FROM alert_active WHERE state IN ($1,$2) ORDER BY started_at DESC", PENDING, FIRING)
        return [_row(r) for r in rows]

    async def upsert_active(self, a: dict) -> None:
        await self.pool.execute(
            """INSERT INTO alert_active
               (fingerprint,policy_id,state,severity,target,value,message,started_at,fired_at,resolved_at,
                last_notified_at,acked_at,updated_at)
               VALUES ($1,$2,$3,$4,$5::jsonb,$6,$7,$8,$9,$10,$11,$12,now())
               ON CONFLICT (fingerprint) DO UPDATE SET state=EXCLUDED.state, severity=EXCLUDED.severity,
                 target=EXCLUDED.target, value=EXCLUDED.value, message=EXCLUDED.message,
                 started_at=EXCLUDED.started_at, fired_at=EXCLUDED.fired_at, resolved_at=EXCLUDED.resolved_at,
                 last_notified_at=EXCLUDED.last_notified_at, acked_at=EXCLUDED.acked_at, updated_at=now()""",
            a["fingerprint"], a["policy_id"], a["state"], a["severity"], json.dumps(a["target"]),
            a.get("value"), a.get("message", ""), a["started_at"], a.get("fired_at"), a.get("resolved_at"),
            a.get("last_notified_at"), a.get("acked_at"),
        )

    async def delete_active(self, fingerprint: str) -> None:
        await self.pool.execute("DELETE FROM alert_active WHERE fingerprint=$1", fingerprint)

    async def ack(self, fingerprint: str) -> Optional[dict]:
        r = await self.pool.fetchrow(
            "UPDATE alert_active SET acked_at=now(), updated_at=now() WHERE fingerprint=$1 RETURNING *", fingerprint)
        return _row(r) if r else None

    async def add_event(self, policy_id: str, fingerprint: str, kind: str, severity: str, target: dict,
                        value: Optional[float], message: str) -> None:
        await self.pool.execute(
            "INSERT INTO alert_events (policy_id,fingerprint,kind,severity,target,value,message) "
            "VALUES ($1,$2,$3,$4,$5::jsonb,$6,$7)",
            policy_id, fingerprint, kind, severity, json.dumps(target), value, message)

    async def list_events(self, policy_id: Optional[str], since: Optional[datetime], limit: int) -> list[dict]:
        rows = await self.pool.fetch(
            """SELECT * FROM alert_events
               WHERE ($1::text IS NULL OR policy_id=$1) AND ($2::timestamptz IS NULL OR ts>=$2)
               ORDER BY ts DESC LIMIT $3""", policy_id, since, limit)
        return [_row(r) for r in rows]


store = AlertStore()


# ---------------------------------------------------------------------------
# 2. 평가기
# ---------------------------------------------------------------------------

def _fp(policy_id: str, target: dict) -> str:
    fp = policy_id + ":" + "/".join(str(target.get(k, "*")) for k in ("cluster", "node", "acc_id"))
    return fp + f"/{target['model']}" if target.get("model") else fp


def _resource_value(metric: str, entry: dict, vendor: str) -> Optional[float]:
    """가속기 1장의 수집 결과(entry)에서 지표 값 1개를 뽑는다. 값이 없으면 None(평가 건너뜀)."""
    if metric in ("utilization_percent", "temperature_celsius", "power_watts"):
        return entry.get(metric)
    if metric == "memory_used_percent":
        used, total = entry.get("memory_used_bytes"), entry.get("memory_total_bytes")
        return used / total * 100 if used is not None and total else None
    if metric == "power_tdp_percent":
        from app.services.power import KNOWN_TDP_WATTS
        p, tdp = entry.get("power_watts"), KNOWN_TDP_WATTS.get(vendor)
        return p / tdp * 100 if p is not None and tdp else None
    if metric == "unhealthy":
        h = entry.get("healthy")
        return None if h is None else (0.0 if h else 1.0)
    return None


async def _stale_seconds(vendor: str, cluster: str) -> dict[str, dict]:
    """가속기별 마지막 샘플 이후 경과 초.

    5분 안에 샘플이 있으면 timestamp() 로 정확히 재고, 5분 넘게 끊긴 카드만 1일 서브쿼리로 찾는다.
    서브쿼리만 쓰면 1분 단위로 반올림돼 정상 카드도 40~70초로 보여 기준 60초에서 오탐이 났다 (2026-10-22).
    """
    from app.api.v2.accelerators import VENDOR_CONFIG
    from app.services.prometheus import esc
    from app.services.prometheus import prometheus_client

    cfg = VENDOR_CONFIG[vendor]
    raw = {"nvidia": "DCGM_FI_DEV_GPU_UTIL", "furiosa": "furiosa_npu_core_utilization",
           "rebellions": "RBLN_DEVICE_STATUS:UTILIZATION"}[vendor]
    idl = cfg["id_label"]
    sel = f'{raw}{{cluster="{esc(cluster)}"}}'
    q = (f'(time() - max by ({idl}, instance) (timestamp({sel}))) '
         f'or (time() - max by ({idl}, instance) (max_over_time(timestamp({sel})[1d:1m])))')
    out: dict[str, dict] = {}
    for item in await prometheus_client.instant(q):
        m = item.get("metric", {})
        acc = m.get(idl)
        if acc:
            try:
                # 장치 ID(npu0 등)는 노드 안에서만 유일. 노드가 재생성되면 옛 노드와 겹치므로 instance 까지 키로 쓴다
                out[f"{acc}@{m.get('instance', '')}"] = {"acc_id": acc, "labels": m,
                                                        "stale_seconds": float(item["value"][1])}
            except (KeyError, ValueError, TypeError):
                pass
    return out


async def evaluate_resource(policy: dict) -> tuple[dict[str, dict], list[str]]:
    """반환: {fingerprint: {target, value, cond}}, warnings."""
    from app.api.v2.accelerators import _collect_accelerators
    from app.services.cluster_discovery import cluster_discovery

    target, metric = policy.get("target") or {}, policy["metric"]
    results: dict[str, dict] = {}
    warnings: list[str] = []
    clusters = await cluster_discovery.get_clusters()
    for name, info in clusters.items():
        if not info.vendor or (target.get("cluster") and target["cluster"] != name):
            continue
        if metric == "stale_seconds":
            acc_map = await _stale_seconds(info.vendor, name)
        else:
            acc_map, w = await _collect_accelerators(name, info.vendor, node=target.get("node"))
            warnings += [f"{name}:{x}" for x in w if x == "NO_DATA"]
        for key, entry in acc_map.items():
            acc_id = entry.get("acc_id", key)
            if target.get("acc_id") and target["acc_id"] != acc_id:
                continue
            value = entry.get("stale_seconds") if metric == "stale_seconds" else _resource_value(metric, entry, info.vendor)
            if value is None:
                continue
            labels = entry.get("labels", {})
            t = {"cluster": name, "node": target.get("node") or labels.get("instance") or labels.get("node"),
                 "acc_id": acc_id, "vendor": info.vendor}
            results[_fp(policy["id"], t)] = {"target": t, "value": value,
                                              "cond": OPS[policy["op"]](value, policy["threshold"])}
    return results, warnings


def _logql(policy: dict) -> str:
    target = policy.get("target") or {}
    # Loki 는 자기가 실행한 쿼리를 로그로 남기므로 우리 정규식이 그 줄에 다시 걸림. Loki 컨테이너 로그는 제외
    sel = [f'cluster="{target["cluster"]}"' if target.get("cluster") else 'cluster=~".+"', 'container!="loki"']
    # 작업 단위로 좁힐 때: VM 은 systemd unit, 파드와 도커 컨테이너는 container 라벨
    sel += [f'{k}="{target[k]}"' for k in ("unit", "container") if target.get(k)]
    esc = lambda r: r.replace("\\", "\\\\").replace('"', '\\"')
    regex, exclude = LOG_METRICS[policy["metric"]][1], LOG_EXCLUDE.get(policy["metric"])
    line = (' |~ "' + esc(regex) + '"' if regex else "") + (' !~ "' + esc(exclude) + '"' if exclude else "")
    return f'sum by (cluster) (count_over_time({{{", ".join(sel)}}}{line} [{int(policy.get("window_min", 5))}m]))'


async def evaluate_slo(policy: dict) -> tuple[dict[str, dict], list[str]]:
    """vLLM 서빙(cluster, model_name)별 값. 기간 안에 요청이 없어 p95 가 NaN 이면 지연 없음(0)으로 본다.

    NaN 은 vLLM 지표는 들어오는데 요청만 0건인 상태라 결측이 아니다. 건너뛰면 부하가 끝난 뒤
    요청이 끊겼을 때 걸려 있던 지연 알람이 해제되지 않는다. vLLM 이 내려가 지표 자체가 없으면 결과가 비어 결측으로 처리된다.
    """
    from app.services.prometheus import prometheus_client

    target = policy.get("target") or {}
    sel = _selector(target.get("cluster"), target.get("model"))
    q = SLO_METRICS[policy["metric"]][2].format(sel=sel, w=int(policy.get("window_min", 5)))
    results: dict[str, dict] = {}
    for item in await prometheus_client.instant(q):
        m = item.get("metric", {})
        try:
            value = float(item["value"][1])
        except (KeyError, ValueError, TypeError):
            continue
        if value != value:  # NaN: 요청 0건
            value = 0.0
        t = {"cluster": m.get("cluster", "*"), "model": m.get("model_name", "*")}
        results[_fp(policy["id"], t)] = {"target": t, "value": round(value, 2),
                                          "cond": OPS[policy["op"]](value, policy["threshold"])}
    return results, [] if results else ["NO_DATA"]


async def evaluate_log(policy: dict) -> tuple[dict[str, dict], list[str]]:
    from app.services.loki import loki_client

    vec = await loki_client.query_instant(_logql(policy))
    if vec is None:
        return {}, ["LOKI_UNAVAILABLE"]
    results: dict[str, dict] = {}
    for item in vec:
        t = {"cluster": item.get("metric", {}).get("cluster", "*")}
        try:
            value = float(item["value"][1])
        except (KeyError, ValueError, TypeError):
            continue
        results[_fp(policy["id"], t)] = {"target": t, "value": value,
                                          "cond": OPS[policy["op"]](value, policy["threshold"])}
    # Loki 는 0건인 스트림을 아예 돌려주지 않음. 지금 걸려 있는 알람 중 결과에 없는 대상은 0건으로 채워 해제되게 함
    for fp, row in (await store.active_for_policy(policy["id"])).items():
        if fp not in results:
            results[fp] = {"target": row["target"], "value": 0.0, "cond": OPS[policy["op"]](0.0, policy["threshold"])}
    return results, []


async def evaluate_composite(policy: dict) -> tuple[dict[str, dict], list[str]]:
    """조건마다 따로 평가한 뒤 클러스터 단위로 모아 모두 만족하는지 본다.

    리소스, SLO 값은 클러스터 안에서 가장 나쁜 값(>= 면 최대, <= 면 최소)으로 대표한다.
    로그 조건은 Loki 가 0건 스트림을 안 주므로 다른 조건에 있는 클러스터는 0건으로 채운다.
    한 조건이라도 값이 없는 클러스터는 결과에서 빼서(결측) 상태를 유지한다.
    """
    target = policy.get("target") or {}
    conds = policy["conditions"]
    per_cond: list[dict[str, float]] = []
    zero_ok: list[bool] = []  # 로그 조건이고 Loki 응답이 있었으면 결과에 없는 클러스터는 0건
    warnings: list[str] = []
    for c in conds:
        kind = metric_kind(c["metric"])
        sub_target = {"cluster": target.get("cluster")}
        if kind == "log":
            sub_target.update({k: target.get(k) for k in ("unit", "container")})
        sub = {**policy, **c, "kind": kind, "target": sub_target}
        res, w = await evaluate_policy(sub)
        warnings += w
        zero_ok.append(kind == "log" and "LOKI_UNAVAILABLE" not in w)
        worst_is_max = c["op"] in (">=", ">", "==", "!=")
        by: dict[str, float] = {}
        for r in res.values():
            cl, v = r["target"].get("cluster", "*"), r["value"]
            if cl not in by or (v > by[cl] if worst_is_max else v < by[cl]):
                by[cl] = v
        per_cond.append(by)

    results: dict[str, dict] = {}
    for cl in sorted(set().union(*per_cond)):
        values = []
        for c, by, fill_zero in zip(conds, per_cond, zero_ok):
            if cl in by:
                v = by[cl]
            elif fill_zero:
                v = 0.0
            else:
                break
            values.append({"metric": c["metric"], "value": v, "cond": OPS[c["op"]](v, c["threshold"])})
        else:
            t = {"cluster": cl}
            results[_fp(policy["id"], t)] = {"target": t, "value": values[0]["value"],
                                              "cond": all(x["cond"] for x in values), "values": values}
    return results, warnings


# ---------------------------------------------------------------------------
# 3. 상태머신 (순수 함수)
# ---------------------------------------------------------------------------

def plan_transition(existing: Optional[dict], cond: bool, for_min: int, repeat_min: int, now: datetime) -> tuple[str, Optional[str]]:
    """현재 행(existing, 없으면 None)과 조건 참/거짓으로 다음 상태와 해야 할 일을 정한다.

    반환 (state, action). state 는 pending|firing|resolved|none(행 삭제), action 은 fire|resolve|repeat|None.
    """
    state = existing["state"] if existing else None
    if cond:
        if state == FIRING:
            last = existing.get("last_notified_at")
            if not existing.get("acked_at") and last and now - last >= timedelta(minutes=repeat_min):
                return FIRING, "repeat"
            return FIRING, None
        started = existing["started_at"] if existing else now
        if now - started >= timedelta(minutes=for_min):
            return FIRING, "fire"
        return PENDING, None
    if state == FIRING:
        return RESOLVED, "resolve"
    return "none", None  # pending 중 거짓이 되면 행만 지움. 알림 없음


def _cond_text(c: dict, value: float, with_label: bool) -> str:
    label, unit = metric_info(c["metric"])
    head = f"{label} " if with_label else ""
    return f"{head}{value:g}{unit} ({c['op']} {c['threshold']:g})"


def _message(policy: dict, target: dict, value: float, state: str, values: Optional[list[dict]] = None) -> str:
    where = " ".join(str(target[k]) for k in ("cluster", "node", "acc_id", "model") if target.get(k))
    head = "[해제] " if state == RESOLVED else f"[{policy['severity']}] "
    conds = policy.get("conditions") or [policy]
    if values and len(values) > 1:
        body = " 그리고 ".join(_cond_text(c, v["value"], True) for c, v in zip(conds, values))
    else:
        body = _cond_text(conds[0], value, False)
    return f"{head}{policy['name']} - {where}: {body}"


async def _apply(policy: dict, results: dict[str, dict], now: datetime) -> None:
    existing = await store.active_for_policy(policy["id"])
    for fp, r in results.items():
        cur = existing.get(fp)
        state, action = plan_transition(cur, r["cond"], policy["for_min"], settings.ALERT_REPEAT_INTERVAL_MIN, now)
        if state == "none":
            if cur:
                await store.delete_active(fp)
            continue
        row = {**(cur or {}), "fingerprint": fp, "policy_id": policy["id"], "state": state,
               "severity": policy["severity"], "target": r["target"], "value": r["value"],
               "started_at": (cur or {}).get("started_at") or now}
        row["message"] = _message(policy, r["target"], r["value"], state, r.get("values"))
        if action == "fire":
            row["fired_at"] = now
        if action == "resolve":
            row["resolved_at"] = now
        if action in ("fire", "repeat", "resolve"):
            row["last_notified_at"] = now
            await store.add_event(policy["id"], fp, action if action != "repeat" else "notified",
                                  policy["severity"], r["target"], r["value"], row["message"])
            await notify(policy, row, resolved=(action == "resolve"))
        await store.upsert_active(row)
    # 결과에 없는 대상(데이터 결측)은 상태를 유지한다. 결측을 해제로 오판하지 않기 위함.
    # 단 ALERT_TARGET_GONE_MIN 넘게 계속 없으면 대상이 사라진 것(삭제된 클러스터, 재생성 전 노드)으로 보고 해제.
    # updated_at 은 결과에 나타날 때마다 upsert 로 갱신되므로 '마지막으로 보인 시각'이다.
    # 결과가 통째로 비면 Prometheus·Loki 장애일 수 있어 건드리지 않는다(조회 오류도 빈 결과로 온다).
    if not results:
        return
    gone_after = timedelta(minutes=settings.ALERT_TARGET_GONE_MIN)
    for fp, cur in existing.items():
        if fp in results or not cur.get("updated_at") or now - cur["updated_at"] < gone_after:
            continue
        if cur["state"] != FIRING:
            await store.delete_active(fp)
            continue
        row = {**cur, "state": RESOLVED, "resolved_at": now, "last_notified_at": now}
        row["message"] = _message(policy, cur["target"], cur.get("value") or 0.0, RESOLVED) + " (대상 소멸)"
        await store.add_event(policy["id"], fp, "resolve", policy["severity"], cur["target"],
                              cur.get("value"), row["message"])
        await notify(policy, row, resolved=True)
        await store.upsert_active(row)


async def evaluate_policy(policy: dict) -> tuple[dict[str, dict], list[str]]:
    evaluate = {"log": evaluate_log, "slo": evaluate_slo, "composite": evaluate_composite}.get(policy["kind"], evaluate_resource)
    return await evaluate(policy)


# ---------------------------------------------------------------------------
# 4. 발송기
# ---------------------------------------------------------------------------

def _payload(policy: dict, row: dict, resolved: bool) -> dict:
    return {
        "status": "resolved" if resolved else "firing",
        "policy_id": policy["id"], "policy_name": policy["name"], "severity": policy["severity"],
        "target": row["target"], "value": row.get("value"), "message": row["message"],
        "started_at": row["started_at"].isoformat() if row.get("started_at") else None,
        "observed_at": _now().isoformat(),
    }


async def send_channel(channel: dict, payload: dict) -> None:
    """채널 1곳에 발송. 실패는 예외로 올린다(호출자가 이벤트로 기록)."""
    cfg = channel.get("config") or {}
    if channel["type"] == "webhook":
        body = {"text": payload["message"]} if cfg.get("format") == "slack" else payload
        async with httpx.AsyncClient(timeout=10.0) as client:
            r = await client.post(cfg["url"], json=body, headers=cfg.get("headers") or {})
            r.raise_for_status()
        return
    if channel["type"] == "email":
        if not settings.SMTP_HOST:
            raise RuntimeError("SMTP_HOST 미설정")
        msg = EmailMessage()
        msg["Subject"] = payload["message"][:120]
        msg["From"] = settings.SMTP_FROM or settings.SMTP_USER or "kcloud-monitor@localhost"
        msg["To"] = ", ".join(cfg.get("to") or [])
        msg.set_content(json.dumps(payload, ensure_ascii=False, indent=2))

        def _send() -> None:
            with smtplib.SMTP(settings.SMTP_HOST, settings.SMTP_PORT, timeout=10) as s:
                s.starttls()
                if settings.SMTP_USER:
                    s.login(settings.SMTP_USER, settings.SMTP_PASSWORD or "")
                s.send_message(msg)

        await asyncio.to_thread(_send)
        return
    raise RuntimeError(f"알 수 없는 채널 타입 {channel['type']}")


async def notify(policy: dict, row: dict, resolved: bool) -> None:
    payload = _payload(policy, row, resolved)
    for cid in policy.get("channel_ids") or []:
        ch = await store.get_channel(cid)
        if not ch or not ch["enabled"] or (resolved and not ch["notify_resolved"]):
            continue
        try:
            await send_channel(ch, payload)
        except Exception as exc:  # 발송 실패가 평가 루프를 멈추면 안 됨
            logger.warning("alert notify failed: channel=%s %s", cid, exc)
            await store.add_event(policy["id"], row["fingerprint"], "notify_failed", policy["severity"],
                                  row["target"], row.get("value"), f"{ch['name']}: {exc}")


# ---------------------------------------------------------------------------
# 루프
# ---------------------------------------------------------------------------

async def evaluate_all() -> None:
    now = _now()
    for policy in await store.list_policies():
        if not policy["enabled"]:
            continue
        try:
            results, _ = await evaluate_policy(policy)
            await _apply(policy, results, now)
        except Exception as exc:
            logger.exception("alert policy %s evaluation failed: %s", policy["id"], exc)


async def run_loop() -> None:
    logger.info("alert loop started (interval=%ss)", settings.ALERT_EVAL_INTERVAL_SEC)
    while True:
        try:
            await evaluate_all()
        except Exception as exc:
            logger.exception("alert loop iteration failed: %s", exc)
        await asyncio.sleep(settings.ALERT_EVAL_INTERVAL_SEC)
