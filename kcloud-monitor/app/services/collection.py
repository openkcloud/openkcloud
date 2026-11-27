"""
수집 현황 계산.

Prometheus 는 수집 지점마다 자동 메트릭(up, scrape_samples_post_metric_relabeling)을 남긴다.
pull 대상은 /api/v1/targets 의 라벨 묶음이 이 자동 메트릭의 라벨 묶음과 정확히 같으므로
그것으로 짝을 맞추고, 짝이 없는 나머지는 원격 수집기(Alloy)가 보낸 push 로 본다.
"""
import asyncio
import copy
import json
import logging
import re
import statistics
import uuid
from datetime import datetime, timedelta, timezone
from functools import lru_cache
from pathlib import Path
from typing import Any, Optional

from app.config import settings
from app.services.k8s import K8sError, k8s_client
from app.services.prometheus import esc, prometheus_client, sample_value

logger = logging.getLogger(__name__)

_ESTIMATE_WINDOW_SEC = 600  # push 주기 추정에 쓰는 구간 (count_over_time(up[10m]))
_DURATION_PART = re.compile(r"(\d+(?:\.\d+)?)(ms|s|m|h|d)")
_DURATION_UNIT_SEC = {"ms": 0.001, "s": 1, "m": 60, "h": 3600, "d": 86400}


def parse_duration(text: Optional[str]) -> Optional[float]:
    """Prometheus 기간 표기("15s", "1m30s") → 초. 해석 실패 시 None."""
    parts = _DURATION_PART.findall(text or "")
    if not parts:
        return None
    return sum(float(num) * _DURATION_UNIT_SEC[unit] for num, unit in parts)


def managed_ids() -> set[str]:
    return {t.strip() for t in settings.COLLECTION_MANAGED_TARGETS.split(",") if t.strip()}


def _key(labels: dict) -> frozenset:
    return frozenset((k, v) for k, v in labels.items() if k != "__name__")


def _by_key(results: list[dict]) -> dict[frozenset, float]:
    out = {}
    for item in results:
        val = sample_value(item)
        if val is not None:
            out[_key(item.get("metric", {}))] = val
    return out


def _new_group(gid: str, job: str, cluster: Optional[str], source: Optional[str], mode: Optional[str]) -> dict:
    return {
        "id": gid, "job": job, "cluster": cluster, "source": source, "mode": mode,
        "targets": 0, "targets_up": 0, "samples": [], "intervals": [], "timeouts": [], "durations": [],
        "label_sets": [],
    }


def _finish(group: dict, allowed: set[str]) -> dict:
    """모아 둔 원시값을 응답 필드로 정리."""
    intervals = group.pop("intervals")
    samples = group.pop("samples")
    timeouts = group.pop("timeouts")
    durations = group.pop("durations")
    # 묶음의 모든 수집 지점이 공유하는 라벨 = 이 묶음의 시계열만 고르는 조건 (응답에는 안 나감)
    label_sets = group.pop("label_sets")
    common = dict(frozenset.intersection(*label_sets)) if label_sets else {"job": group["job"]}
    common.pop("instance", None)
    group["selector"] = common
    interval = statistics.median(intervals) if intervals else None
    per_scrape = sum(samples) if samples else None
    group["interval_seconds"] = round(interval, 1) if interval else None
    group["interval_estimated"] = group["mode"] != "pull"
    group["timeout_seconds"] = max(timeouts) if timeouts else None
    group["max_scrape_duration_seconds"] = round(max(durations), 3) if durations else None
    group["samples_per_scrape"] = per_scrape
    group["samples_per_second"] = (
        round(per_scrape / interval, 1) if per_scrape is not None and interval else None
    )
    if group["mode"] == "push":
        group["adjustable"], group["lock_reason"] = False, "push"
    elif group["mode"] == "pull" and group["id"] in allowed:
        group["adjustable"], group["lock_reason"] = True, None
    else:
        group["adjustable"], group["lock_reason"] = False, "not_managed"
    return group


async def list_targets() -> tuple[list[dict], list[str]]:
    """수집 대상 묶음 목록과 경고 코드. 초당 저장량이 많은 순."""
    targets, ups, samples, counts = await asyncio.gather(
        prometheus_client.active_targets(),
        prometheus_client.instant("up"),
        prometheus_client.instant("scrape_samples_post_metric_relabeling"),
        prometheus_client.instant(f"count_over_time(up[{_ESTIMATE_WINDOW_SEC}s])"),
    )
    warnings: list[str] = []
    if targets is None:
        warnings.append("UPSTREAM_ERROR")
    sample_by_key = _by_key(samples)
    count_by_key = _by_key(counts)

    groups: dict[str, dict] = {}
    pull_keys: set[frozenset] = set()

    for target in targets or []:
        labels = target.get("labels", {})
        key = _key(labels)
        pull_keys.add(key)
        pool = target.get("scrapePool") or labels.get("job", "")
        group = groups.setdefault(
            pool, _new_group(pool, labels.get("job", ""), labels.get("cluster"), labels.get("source"), "pull")
        )
        group["targets"] += 1
        group["label_sets"].append(key)
        group["targets_up"] += target.get("health") == "up"
        for field, raw in (("intervals", target.get("scrapeInterval")), ("timeouts", target.get("scrapeTimeout"))):
            seconds = parse_duration(raw)
            if seconds:
                group[field].append(seconds)
        if isinstance(target.get("lastScrapeDuration"), (int, float)):
            group["durations"].append(target["lastScrapeDuration"])
        if key in sample_by_key:
            group["samples"].append(sample_by_key[key])

    # targets 목록에 없는 up 시계열 = 원격 수집기가 보낸 것. 목록 조회가 실패했으면 방식을 알 수 없음
    mode = "push" if targets is not None else None
    for item in ups:
        labels = item.get("metric", {})
        key = _key(labels)
        if key in pull_keys:
            continue
        job, cluster, source = labels.get("job", ""), labels.get("cluster"), labels.get("source")
        gid = f"push/{cluster or '-'}/{job}"
        group = groups.setdefault(gid, _new_group(gid, job, cluster, source, mode))
        group["targets"] += 1
        group["label_sets"].append(key)
        group["targets_up"] += sample_value(item) == 1
        # 10분 동안 값이 n 번 들어왔으면 주기는 약 600/n 초. 2번 미만이면 추정 불가
        if count_by_key.get(key, 0) >= 2:
            group["intervals"].append(_ESTIMATE_WINDOW_SEC / count_by_key[key])
        if key in sample_by_key:
            group["samples"].append(sample_by_key[key])

    allowed = managed_ids()
    items = [_finish(g, allowed) for g in groups.values()]
    total_rate = sum(i["samples_per_second"] or 0 for i in items)
    for item in items:
        rate = item["samples_per_second"]
        item["share_percent"] = round(rate / total_rate * 100, 1) if rate is not None and total_rate else None
    items.sort(key=lambda i: (-(i["samples_per_second"] or 0), i["id"]))
    if not items:
        warnings.append("NO_DATA")
    return items, warnings


def summarize(items: list[dict]) -> dict:
    rates = [i["samples_per_second"] for i in items if i["samples_per_second"] is not None]
    return {
        "target_groups": len(items),
        "pull_groups": sum(i["mode"] == "pull" for i in items),
        "push_groups": sum(i["mode"] == "push" for i in items),
        "adjustable_groups": sum(i["adjustable"] for i in items),
        "samples_per_second": round(sum(rates), 1) if rates else None,
    }


async def top_metrics(job: str, cluster: Optional[str], limit: int) -> tuple[list[dict], Optional[int]]:
    """job 하나에서 시계열이 많은 메트릭 순위와 전체 시계열 개수."""
    selector = f'job="{esc(job)}"' + (f', cluster="{esc(cluster)}"' if cluster else "")
    top, total = await asyncio.gather(
        prometheus_client.instant(f"topk({limit}, count by (__name__) ({{{selector}}}))"),
        prometheus_client.instant(f"count({{{selector}}})"),
    )
    total_series = int(sample_value(total[0])) if total and sample_value(total[0]) is not None else None
    items = []
    for row in top:
        series = sample_value(row)
        if series is None:
            continue
        name = row.get("metric", {}).get("__name__", "")
        items.append({
            "metric": name,
            "series": int(series),
            "protected": is_protected(name),
            "share_percent": round(series / total_series * 100, 1) if total_series else None,
        })
    items.sort(key=lambda i: (-i["series"], i["metric"]))
    return items, total_series


# ---------------------------------------------------------------------------
# 보호 지표: 이 API 서버의 코드가 쓰는 메트릭은 수집에서 빼지 못하게 막는다
# ---------------------------------------------------------------------------

_METRIC_NAME = re.compile(r"^[a-zA-Z_:][a-zA-Z0-9_:]*$")
_NAME_CHAR = "A-Za-z0-9_:"
_NAME_REGEX_IN_QUERY = re.compile(r'__name__\s*=~\s*"([^"]+)"')
MIN_INTERVAL_SEC = 5
# 조회 쿼리 대부분이 최근 5분 구간이라 값이 4개 이상 들어오려면 주기는 1분 이하여야 함
MAX_INTERVAL_SEC = 60
MAX_DROP_METRICS = 50


@lru_cache(maxsize=1)
def _app_source() -> tuple[str, tuple[str, ...]]:
    """app/ 아래 소스 전체와, 쿼리에서 이름 패턴(__name__=~"...")으로 고르는 정규식 목록."""
    root = Path(__file__).resolve().parents[1]
    text = "\n".join(
        p.read_text(encoding="utf-8", errors="ignore")
        for p in root.rglob("*.py") if p.name != "collection.py"
    )
    return text, tuple(_NAME_REGEX_IN_QUERY.findall(text))


def is_protected(name: str) -> bool:
    """메트릭 이름이 서버 코드에 그대로 나오거나 코드의 이름 패턴에 걸리면 보호 대상."""
    if name == "up" or name.startswith("scrape_"):
        return True  # 수집 상태를 알려 주는 자동 메트릭. 이 API 도 이것으로 현황을 계산
    text, patterns = _app_source()
    if re.search(rf"(?<![{_NAME_CHAR}]){re.escape(name)}(?![{_NAME_CHAR}])", text):
        return True
    for pattern in patterns:
        try:
            if re.fullmatch(pattern, name):
                return True
        except re.error:  # f-string 조각이 섞인 패턴은 정규식이 아닐 수 있음
            continue
    return False


# ---------------------------------------------------------------------------
# 수집 정책 저장소. 저장만 하고 실제 수집 설정은 건드리지 않는다
# ---------------------------------------------------------------------------

SCHEMA_SQL = """
CREATE TABLE IF NOT EXISTS collection_policies (
    id               TEXT PRIMARY KEY,
    name             TEXT NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    target_id        TEXT NOT NULL UNIQUE,
    interval_seconds INTEGER,
    drop_metrics     JSONB NOT NULL DEFAULT '[]'::jsonb,
    state            TEXT NOT NULL DEFAULT 'draft' CHECK (state IN ('draft','pending','applied')),
    snapshot         JSONB,
    applied_at       TIMESTAMPTZ,
    confirm_deadline TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS collection_history (
    id            BIGSERIAL PRIMARY KEY,
    ts            TIMESTAMPTZ NOT NULL DEFAULT now(),
    policy_id     TEXT NOT NULL,
    target_id     TEXT NOT NULL,
    action        TEXT NOT NULL,
    reason        TEXT NOT NULL DEFAULT '',
    actor         TEXT NOT NULL DEFAULT '',
    before        JSONB,
    after         JSONB
);
CREATE INDEX IF NOT EXISTS collection_history_ts_idx ON collection_history(ts DESC);
"""


def _row(r: Any) -> dict:
    d = dict(r)
    for key in ("drop_metrics", "snapshot", "before", "after"):
        if isinstance(d.get(key), str):
            d[key] = json.loads(d[key])
    return d


def _utcnow() -> datetime:
    return datetime.now(timezone.utc)


def _json(value: Any) -> Optional[str]:
    return None if value is None else json.dumps(value)


class CollectionStore:
    """알람 저장소가 연 DB 연결 풀을 같이 쓴다."""

    def __init__(self) -> None:
        self.pool = None

    @property
    def ready(self) -> bool:
        return self.pool is not None

    async def connect(self, pool) -> None:
        async with pool.acquire() as con:
            await con.execute(SCHEMA_SQL)
        self.pool = pool

    def close(self) -> None:
        self.pool = None

    async def list_policies(self) -> list[dict]:
        return [_row(r) for r in await self.pool.fetch("SELECT * FROM collection_policies ORDER BY created_at, id")]

    async def get_policy(self, pid: str) -> Optional[dict]:
        r = await self.pool.fetchrow("SELECT * FROM collection_policies WHERE id=$1", pid)
        return _row(r) if r else None

    async def get_by_target(self, target_id: str) -> Optional[dict]:
        r = await self.pool.fetchrow("SELECT * FROM collection_policies WHERE target_id=$1", target_id)
        return _row(r) if r else None

    async def create_policy(self, p: dict) -> dict:
        pid = f"col-{uuid.uuid4().hex[:6]}"
        await self.pool.execute(
            """INSERT INTO collection_policies (id,name,description,target_id,interval_seconds,drop_metrics)
               VALUES ($1,$2,$3,$4,$5,$6::jsonb)""",
            pid, p["name"], p.get("description") or "", p["target_id"], p.get("interval_seconds"),
            json.dumps(p.get("drop_metrics") or []),
        )
        return await self.get_policy(pid)

    async def update_policy(self, pid: str, p: dict) -> Optional[dict]:
        await self.pool.execute(
            """UPDATE collection_policies SET name=$2,description=$3,target_id=$4,interval_seconds=$5,
               drop_metrics=$6::jsonb,updated_at=now() WHERE id=$1""",
            pid, p["name"], p.get("description") or "", p["target_id"], p.get("interval_seconds"),
            json.dumps(p.get("drop_metrics") or []),
        )
        return await self.get_policy(pid)

    async def delete_policy(self, pid: str) -> bool:
        return (await self.pool.execute("DELETE FROM collection_policies WHERE id=$1", pid)).endswith("1")


    async def set_state(
        self, pid: str, state: str, snapshot: Optional[dict],
        applied_at: Optional[datetime], confirm_deadline: Optional[datetime],
    ) -> None:
        await self.pool.execute(
            """UPDATE collection_policies SET state=$2,snapshot=$3::jsonb,applied_at=$4,confirm_deadline=$5,
               updated_at=now() WHERE id=$1""",
            pid, state, _json(snapshot), applied_at, confirm_deadline,
        )

    async def list_pending(self) -> list[dict]:
        rows = await self.pool.fetch("SELECT * FROM collection_policies WHERE state='pending' ORDER BY applied_at")
        return [_row(r) for r in rows]

    async def add_history(
        self, policy_id: str, target_id: str, action: str, reason: str, actor: str,
        before: Optional[dict], after: Optional[dict],
    ) -> None:
        await self.pool.execute(
            """INSERT INTO collection_history (policy_id,target_id,action,reason,actor,before,after)
               VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7::jsonb)""",
            policy_id, target_id, action, reason, actor, _json(before), _json(after),
        )

    async def list_history(self, policy_id: Optional[str], limit: int) -> list[dict]:
        if policy_id:
            rows = await self.pool.fetch(
                "SELECT * FROM collection_history WHERE policy_id=$1 ORDER BY ts DESC, id DESC LIMIT $2",
                policy_id, limit,
            )
        else:
            rows = await self.pool.fetch("SELECT * FROM collection_history ORDER BY ts DESC, id DESC LIMIT $1", limit)
        return [_row(r) for r in rows]


store = CollectionStore()


# ---------------------------------------------------------------------------
# 정책 검사와 미리보기
# ---------------------------------------------------------------------------

async def find_target(target_id: str) -> tuple[Optional[dict], list[dict], list[str]]:
    """수집 대상 1건, 전체 목록, 경고 코드."""
    items, warnings = await list_targets()
    return next((i for i in items if i["id"] == target_id), None), items, warnings


def check_policy(policy: dict, target: Optional[dict]) -> list[dict]:
    """정책 한 건의 잘못된 칸 목록. 비어 있으면 저장 가능."""
    errors: list[dict] = []

    def err(field: str, message: str) -> None:
        errors.append({"field": field, "message": message})

    if not (policy.get("name") or "").strip():
        err("name", "이름은 비울 수 없음")
    if target is None:
        err("target_id", f"수집 중이 아닌 대상: {policy.get('target_id')!r}")
    elif target["mode"] == "push":
        err("target_id", "push 대상은 원격 수집기 설정 파일로만 변경 가능")

    interval = policy.get("interval_seconds")
    drops = policy.get("drop_metrics") or []
    if interval is None and not drops:
        err("interval_seconds", "interval_seconds 와 drop_metrics 중 하나는 필요")
    if interval is not None and not MIN_INTERVAL_SEC <= interval <= MAX_INTERVAL_SEC:
        err("interval_seconds", f"수집 주기는 {MIN_INTERVAL_SEC}초 이상 {MAX_INTERVAL_SEC}초 이하")
    elif interval is not None and target and (target.get("timeout_seconds") or 0) > interval:
        # Prometheus 는 제한 시간이 주기보다 긴 설정을 받아들이지 않음
        err("interval_seconds", f"수집 주기는 이 대상의 수집 제한 시간({target['timeout_seconds']:g}초) 이상")
    if len(drops) > MAX_DROP_METRICS:
        err("drop_metrics", f"뺄 메트릭은 {MAX_DROP_METRICS}개 이하")
    if len(set(drops)) != len(drops):
        err("drop_metrics", "같은 메트릭 이름이 두 번 들어 있음")
    for idx, name in enumerate(drops):
        if not _METRIC_NAME.match(name):
            err(f"drop_metrics[{idx}]", f"메트릭 이름을 정확히 적어야 함 (패턴 불가): {name!r}")
        elif is_protected(name):
            err(f"drop_metrics[{idx}]", f"이 API 가 조회에 쓰는 메트릭이라 뺄 수 없음: {name}")
    return errors


async def preview(policy: dict) -> tuple[Optional[dict], list[str]]:
    """정책을 적용했을 때 예상 저장량. 대상이 지금 수집 중이 아니면 None."""
    target, items, warnings = await find_target(policy["target_id"])
    if target is None:
        return None, warnings
    drops = policy.get("drop_metrics") or []
    dropped: list[dict] = []
    if drops:
        matchers = ", ".join(f'{k}="{esc(v)}"' for k, v in sorted(target["selector"].items()))
        names = "|".join(drops)  # 저장 때 이름 형식만 허용했으므로 정규식 특수문자 없음
        rows = await prometheus_client.instant(f'count by (__name__) ({{__name__=~"{names}", {matchers}}})')
        found = {r.get("metric", {}).get("__name__"): int(sample_value(r) or 0) for r in rows}
        dropped = [{"metric": name, "series": found.get(name, 0)} for name in drops]
        if any(d["series"] == 0 for d in dropped):
            warnings = [*warnings, "METRIC_NOT_FOUND"]

    cur_interval, cur_scrape = target["interval_seconds"], target["samples_per_scrape"]
    new_interval = policy.get("interval_seconds") or cur_interval
    new_scrape = max(cur_scrape - sum(d["series"] for d in dropped), 0) if cur_scrape is not None else None
    new_rate = round(new_scrape / new_interval, 1) if new_scrape is not None and new_interval else None
    cur_rate = target["samples_per_second"]
    total_rate = sum(i["samples_per_second"] or 0 for i in items)
    saved = round(cur_rate - new_rate, 1) if cur_rate is not None and new_rate is not None else None
    return {
        "policy_id": policy["id"],
        "target_id": target["id"],
        "applicable": target["adjustable"],
        "lock_reason": target["lock_reason"],
        "current": {"interval_seconds": cur_interval, "samples_per_scrape": cur_scrape, "samples_per_second": cur_rate},
        "expected": {"interval_seconds": new_interval, "samples_per_scrape": new_scrape, "samples_per_second": new_rate},
        "dropped": dropped,
        "saved_samples_per_second": saved,
        "saved_target_percent": round(saved / cur_rate * 100, 1) if saved is not None and cur_rate else None,
        "saved_total_percent": round(saved / total_rate * 100, 1) if saved is not None and total_rate else None,
    }, warnings


# ---------------------------------------------------------------------------
# 적용, 확정, 되돌리기
#
# 상태: draft(저장만) → pending(적용됨, 확정 대기) → applied(확정)
# pending 은 제한 시간 안에 확정하지 않거나 수집이 줄면 자동으로 draft 로 되돌아간다.
# 되돌릴 때는 적용 직전에 떠 둔 원래 값(snapshot)을 그대로 다시 쓴다.
# ---------------------------------------------------------------------------

_HEALTH_GRACE_SEC = 90  # 적용 후 Prometheus 가 설정을 다시 읽고 첫 수집을 마칠 때까지 기다리는 시간
_LOOP_INTERVAL_SEC = 30
_ENDPOINT_LIST_KEY = {"serviceMonitor": "endpoints", "podMonitor": "podMetricsEndpoints"}


def parse_target_id(target_id: str) -> Optional[tuple[str, str, str, Optional[int]]]:
    """수집 대상 ID → (종류, 네임스페이스, 이름, endpoint 순번). 고칠 수 없는 형식이면 None."""
    parts = target_id.split("/")
    if parts[0] in _ENDPOINT_LIST_KEY and len(parts) == 4 and parts[3].isdigit():
        return parts[0], parts[1], parts[2], int(parts[3])
    if parts[0] == "scrapeConfig" and len(parts) == 3:
        return parts[0], parts[1], parts[2], None
    return None


def _interval_key(kind: str) -> str:
    return "scrapeInterval" if kind == "scrapeConfig" else "interval"


def _endpoint(kind: str, obj: dict, index: Optional[int]) -> dict:
    """수집 설정 안에서 주기와 필터가 적힌 부분."""
    spec = obj.get("spec") or {}
    if kind == "scrapeConfig":
        return spec
    endpoints = spec.get(_ENDPOINT_LIST_KEY[kind]) or []
    if index is None or index >= len(endpoints):
        raise K8sError(f"수집 설정에 {index}번 endpoint 없음")
    return endpoints[index]


def _patch_body(snapshot: dict, obj: dict, interval: Optional[str], relabelings: Optional[list]) -> dict:
    """주기와 필터만 바꾸는 merge patch. resourceVersion 을 넣어 그 사이 누가 고쳤으면 실패하게 함."""
    kind, index = snapshot["kind"], snapshot["index"]
    values = {_interval_key(kind): interval, "metricRelabelings": relabelings}
    meta = {"resourceVersion": obj.get("metadata", {}).get("resourceVersion")}
    if kind == "scrapeConfig":
        return {"metadata": meta, "spec": values}  # merge patch 에서 null 은 그 키 삭제
    _endpoint(kind, obj, index)  # 순번 검사
    endpoints = copy.deepcopy(obj["spec"][_ENDPOINT_LIST_KEY[kind]])
    for key, value in values.items():  # 목록은 통째로 바뀌므로 없앨 키는 직접 지움
        if value is None:
            endpoints[index].pop(key, None)
        else:
            endpoints[index][key] = value
    return {"metadata": meta, "spec": {_ENDPOINT_LIST_KEY[kind]: endpoints}}


def _desired(policy: dict, snapshot: dict) -> tuple[Optional[str], Optional[list]]:
    """정책을 적용한 뒤의 주기와 필터. 원래 있던 필터 뒤에 우리 규칙 하나를 덧붙임."""
    interval = f"{policy['interval_seconds']}s" if policy.get("interval_seconds") else snapshot["interval"]
    relabelings = snapshot["metric_relabelings"]
    if policy.get("drop_metrics"):
        rule = {"sourceLabels": ["__name__"], "regex": "|".join(policy["drop_metrics"]), "action": "drop"}
        relabelings = [*(relabelings or []), rule]
    return interval, relabelings


async def apply_policy(policy: dict, target: dict, actor: str) -> dict:
    """수집 설정에 정책 반영. 실패하면 상태를 draft 로 돌려놓고 K8sError."""
    kind, namespace, name, index = parse_target_id(policy["target_id"])
    obj = await k8s_client.get(kind, namespace, name)
    endpoint = _endpoint(kind, obj, index)
    snapshot = {
        "kind": kind, "namespace": namespace, "name": name, "index": index,
        "interval": endpoint.get(_interval_key(kind)),
        "metric_relabelings": endpoint.get("metricRelabelings"),
        "targets_up": target["targets_up"],
    }
    interval, relabelings = _desired(policy, snapshot)
    now = _utcnow()
    deadline = now + timedelta(minutes=settings.COLLECTION_CONFIRM_TIMEOUT_MIN)
    # 원래 값을 먼저 저장. 이 뒤에 서버가 죽어도 되돌리기는 원래 값을 다시 쓰는 것이라 안전
    await store.set_state(policy["id"], "pending", snapshot, now, deadline)
    try:
        await k8s_client.merge_patch(kind, namespace, name, _patch_body(snapshot, obj, interval, relabelings))
    except K8sError:
        await store.set_state(policy["id"], "draft", None, None, None)
        raise
    await store.add_history(
        policy["id"], policy["target_id"], "apply", "", actor,
        {"interval": snapshot["interval"], "metric_relabelings": snapshot["metric_relabelings"]},
        {"interval": interval, "metric_relabelings": relabelings},
    )
    return await store.get_policy(policy["id"])


async def confirm_policy(policy: dict, actor: str) -> dict:
    await store.set_state(policy["id"], "applied", policy["snapshot"], policy["applied_at"], None)
    await store.add_history(policy["id"], policy["target_id"], "confirm", "", actor, None, None)
    return await store.get_policy(policy["id"])


async def rollback_policy(policy: dict, action: str, reason: str, actor: str) -> dict:
    """수집 설정을 적용 전 값으로 복구. 설정 자체가 사라졌으면 복구할 것이 없으므로 상태만 정리."""
    snapshot = policy["snapshot"]
    kind, namespace, name = snapshot["kind"], snapshot["namespace"], snapshot["name"]
    before = None
    try:
        obj = await k8s_client.get(kind, namespace, name)
        endpoint = _endpoint(kind, obj, snapshot["index"])
        before = {"interval": endpoint.get(_interval_key(kind)), "metric_relabelings": endpoint.get("metricRelabelings")}
        await k8s_client.merge_patch(
            kind, namespace, name, _patch_body(snapshot, obj, snapshot["interval"], snapshot["metric_relabelings"])
        )
    except K8sError as exc:
        if exc.status != 404:
            raise
        reason = reason or "config_gone"
    await store.set_state(policy["id"], "draft", None, None, None)
    await store.add_history(
        policy["id"], policy["target_id"], action, reason, actor, before,
        {"interval": snapshot["interval"], "metric_relabelings": snapshot["metric_relabelings"]},
    )
    return await store.get_policy(policy["id"])


async def check_pending() -> None:
    """확정 대기 중인 정책 점검. 시간이 지났거나 수집이 줄었으면 자동으로 되돌림."""
    pending = await store.list_pending()
    if not pending:
        return
    items, warnings = await list_targets()
    now = _utcnow()
    for policy in pending:
        try:
            if now >= policy["confirm_deadline"]:
                await rollback_policy(policy, "auto_rollback", "confirm_timeout", "system")
                continue
            # 수집 대상 목록을 못 읽었으면 판단 불가. 다음 차례에 다시 봄
            if "UPSTREAM_ERROR" in warnings or (now - policy["applied_at"]).total_seconds() < _HEALTH_GRACE_SEC:
                continue
            target = next((i for i in items if i["id"] == policy["target_id"]), None)
            if target is None or target["targets_up"] < policy["snapshot"].get("targets_up", 0):
                await rollback_policy(policy, "auto_rollback", "health_check_failed", "system")
        except Exception:
            logger.exception("collection policy auto check failed: %s", policy["id"])


async def run_loop() -> None:
    while True:
        try:
            await check_pending()
        except asyncio.CancelledError:
            raise
        except Exception:
            logger.exception("collection loop error")
        await asyncio.sleep(_LOOP_INTERVAL_SEC)
