"""
알람 API v2 — 정책, 채널, 활성 알람, 이력.

포탈의 알람 탭 3개(활성 알람 / 알람 정책 / 채널)와 1:1 로 대응한다.
평가는 app.services.alerts.run_loop 가 서버 안에서 주기적으로 수행하고, 여기서는 저장소를 읽고 쓴다.
DATABASE_URL 이 없으면 모든 경로가 503 ALERT_STORE_NOT_CONFIGURED.
"""
from datetime import datetime, timezone
import re
from typing import Optional

from fastapi import APIRouter, HTTPException, Query

from app.schemas.alerts import (
    ActiveAlert,
    AlertEvent,
    Channel,
    ChannelCreate,
    ChannelPatch,
    ItemResponse,
    ListResponse,
    MetricItem,
    Policy,
    PolicyCreate,
    PolicyPatch,
    PreviewItem,
)
from app.services import alerts
from app.services.cluster_discovery import cluster_discovery
from app.services.alerts import LOG_METRICS, RESOURCE_METRICS, SLO_METRICS, metric_kind, store

router = APIRouter(prefix="/alerts")


def _require_store() -> None:
    if not store.ready:
        raise HTTPException(status_code=503, detail="ALERT_STORE_NOT_CONFIGURED: DATABASE_URL 을 설정하세요")


async def _policy_or_404(policy_id: str) -> dict:
    row = await store.get_policy(policy_id)
    if not row:
        raise HTTPException(status_code=404, detail="POLICY_NOT_FOUND")
    return row


_SELECTOR_KEYS = {
    "resource": {"cluster", "node", "acc_id"},
    "log": {"cluster", "unit", "container"},
    "slo": {"cluster", "model"},
    "composite": {"cluster", "unit", "container"},  # 로그가 클러스터 단위라 조합도 클러스터로 묶음
}
_LEGACY = ("metric", "op", "threshold", "window_min")  # 조건 1개를 적는 짧은 형식
# 대상 값은 PromQL, LogQL 셀렉터에 그대로 들어가므로 라벨 값에 쓰이는 문자만 허용 (주입 방지)
_SELECTOR_VALUE = re.compile(r"^[A-Za-z0-9_.:\-/]+$")
_DURATION = re.compile(r"^(\d+)m$")


def _minutes(text: str) -> int:
    """'5m' → 5. 평가 주기가 30초라 분 단위만 받음."""
    m = _DURATION.match(text.strip())
    if not m:
        raise HTTPException(status_code=422, detail=f"duration 형식은 숫자+m (예: 5m): {text!r}")
    n = int(m.group(1))
    if not 0 <= n <= 1440:
        raise HTTPException(status_code=422, detail="duration 은 0m 이상 1440m 이하")
    return n


def _target(selector: str, kind: str) -> dict:
    """'cluster=l40s,node=x' → {'cluster': 'l40s', 'node': 'x'}"""
    out: dict[str, str] = {}
    for part in filter(None, (p.strip() for p in selector.split(","))):
        k, sep, v = part.partition("=")
        k, v = k.strip(), v.strip()
        if not sep or not v or k not in _SELECTOR_KEYS[kind]:
            raise HTTPException(status_code=422, detail=f"대상 필터 {part!r}: {kind} 지표는 {sorted(_SELECTOR_KEYS[kind])} 키만 key=value 로 지정")
        if not _SELECTOR_VALUE.match(v):
            raise HTTPException(status_code=422, detail=f"대상 필터 값 {v!r}: 영문, 숫자, _ . : - / 만 사용")
        out[k] = v
    return out


def _channel_kind(ch: dict) -> str:
    if ch["type"] == "email":
        return "email"
    return "slack" if (ch.get("config") or {}).get("format") == "slack" else "webhook"


async def _channel_ids(types: list[str]) -> list[str]:
    """채널 종류 목록 → 등록된 해당 종류 채널 ID 전체. 종류에 맞는 채널이 하나도 없으면 422."""
    if not types:
        return []
    channels = await store.list_channels()
    ids = [c["id"] for c in channels if _channel_kind(c) in set(types)]
    missing = sorted(set(types) - {_channel_kind(c) for c in channels})
    if missing:
        raise HTTPException(status_code=422, detail=f"등록된 채널이 없는 종류: {missing}. 채널을 먼저 등록")
    return ids


async def _policies_out(rows: list[dict]) -> list[Policy]:
    """저장된 정책 + 연결된 채널을 종류별로 묶은 것, 대상과 지속 시간을 입력 형식 문자로 (화면 표시용)"""
    chs = {c["id"]: c for c in await store.list_channels()}
    out = []
    for r in rows:
        grouped: dict[str, list[dict]] = {}
        for cid in r.get("channel_ids") or []:
            if ch := chs.get(cid):
                grouped.setdefault(_channel_kind(ch), []).append(
                    {"id": cid, "name": ch["name"], "enabled": ch["enabled"]})
        selector = ",".join(f"{k}={v}" for k, v in (r.get("target") or {}).items() if v)
        out.append(Policy(**r, channels=grouped, selector=selector, duration=f"{r['for_min']}m"))
    return out


async def _check(body: dict, cur: Optional[dict] = None) -> dict:
    """화면 입력 → 저장 형식. 형식 오류와 대상 오타를 칸 이름과 함께 모두 모아 422 한 번으로 반환

    조건은 conditions 목록으로 받고, 없으면 metric, op, threshold, window_min 으로 첫 조건을 만든다(생성)
    또는 고친다(수정). cur 가 있으면 수정: 보낸 항목만 검사하고, 비운 항목은 저장된 값을 쓴다.
    """
    errors: list[dict] = []

    def err(field: str, message: str) -> None:
        errors.append({"field": field, "message": message})

    # 1) 조건 목록
    if body.get("conditions"):
        conds = [dict(c) for c in body["conditions"]]
        field = lambda i, k: f"conditions[{i}].{k}"
    else:
        first = dict(cur["conditions"][0]) if cur else {"op": ">=", "window_min": 5}
        first.update({k: body[k] for k in _LEGACY if body.get(k) is not None})
        conds = [first, *(cur["conditions"][1:] if cur else [])]
        field = lambda i, k: k
    touched = bool(body.get("conditions")) or any(body.get(k) is not None for k in _LEGACY)
    kinds = []
    for i, c in enumerate(conds):
        # 수정 요청은 보낸 항목만 오므로(exclude_unset) 빠진 칸은 기본값으로 채움
        c.setdefault("op", ">=")
        c.setdefault("window_min", 5)
        if not c.get("metric"):
            err(field(i, "metric"), "지표는 GET /alerts/metrics 목록 중 하나를 지정")
        elif metric_kind(c["metric"]) is None:
            err(field(i, "metric"), f"지표는 GET /alerts/metrics 목록 중 하나: {c['metric']!r}")
        if c.get("threshold") is None:
            err(field(i, "threshold"), "임계값 숫자 필요")
        kinds.append(metric_kind(c.get("metric") or ""))
    kind = "composite" if len(conds) > 1 else kinds[0]
    kind_changed = bool(cur) and kind != cur["kind"]

    out = {k: v for k, v in body.items() if k not in ("duration", "selector", "channels", "conditions", *_LEGACY)}
    if touched or not cur:
        out["conditions"] = conds
        out.update({k: conds[0].get(k) for k in _LEGACY})
        out["kind"] = kind

    # 2) 지속 시간, 대상, 채널
    if "duration" in body:
        try:
            out["for_min"] = _minutes(body["duration"])
        except HTTPException as e:
            err("duration", e.detail)
    target = {k: v for k, v in ((cur or {}).get("target") or {}).items() if v}
    if ("selector" in body or kind_changed) and kind:
        # 종류가 바뀌면 저장된 대상 키가 새 종류에 안 맞을 수 있어 다시 검사
        selector = body["selector"] if "selector" in body else ",".join(f"{k}={v}" for k, v in target.items())
        try:
            target = out["target"] = _target(selector, kind)
        except HTTPException as e:
            err("selector", e.detail)
    if "channels" in body:
        try:
            out["channel_ids"] = await _channel_ids(body["channels"])
        except HTTPException as e:
            err("channels", e.detail)

    # 3) 지표와 대상 형식이 맞으면 다른 칸 오류와 함께 대상 오타도 확인
    # 로그만으로 된 정책은 0건이면 데이터가 비어 오타와 구별할 수 없어 클러스터만 봄
    target_ok = kind and not any(e["field"].endswith(("metric", "threshold")) or e["field"] == "selector" for e in errors)
    if target_ok and (touched or "selector" in body):
        cluster = target.get("cluster")
        if cluster and cluster not in await cluster_discovery.get_clusters():
            err("selector", f"수집 중인 클러스터가 아님: {cluster!r}")
        elif not all(k == "log" for k in kinds):
            merged = {**(cur or {}), **out, "id": "check", "kind": kind, "conditions": conds, "target": target}
            merged.update({k: conds[0].get(k) for k in _LEGACY})
            results, warns = await alerts.evaluate_policy(merged)
            # Loki 가 죽어 있으면 로그 조건 때문에 결과가 비는 것이라 오타로 보지 않음
            if not results and "LOKI_UNAVAILABLE" not in warns:
                err("selector", "이 대상과 맞는 데이터가 지금 없음. 클러스터, 노드, 가속기, 모델 이름 확인")
    if errors:
        raise HTTPException(status_code=422, detail={"errors": errors})
    return out


def _timing(row: dict, for_min: int) -> dict:
    end = row.get("resolved_at") or datetime.now(timezone.utc)
    return {"duration_seconds": int((end - row["started_at"]).total_seconds()), "for_seconds": for_min * 60}


# ── 지표 ──────────────────────────────────────────────────────────────────

@router.get("/metrics", summary="알람 지표 목록", response_model=ListResponse)
async def list_metrics():
    """알람 정책에 쓸 수 있는 지표 목록 조회

    입력 예시

    * `GET /api/v2/alerts/metrics`

    응답

    * 지표별 `key`, 종류(`resource` | `log` | `slo`), 이름, 단위
    * `total`: 지표 개수

    사용법

    * 정책 생성 시 `metric` 에 `key` 를 그대로 넣음
    * `resource`: 가속기 값 (온도, 사용률 등)
    * `log`: 정해진 시간 동안 특정 로그가 나온 건수
    * `slo`: vLLM 서빙의 최근 `window_min` 분 TTFT, TPOT p95(ms)와 지금 대기 요청 수
    * `log_total_count`: 단어 구분 없이 전체 로그 건수, 작업 로그가 멈췄는지(`<= 0`) 볼 때 사용
    """
    data = [MetricItem(key=k, kind="resource", label=v[0], unit=v[1]) for k, v in RESOURCE_METRICS.items()]
    data += [MetricItem(key=k, kind="log", label=v[0], unit="건") for k, v in LOG_METRICS.items()]
    data += [MetricItem(key=k, kind="slo", label=v[0], unit=v[1]) for k, v in SLO_METRICS.items()]
    return ListResponse(data=data, total=len(data))


# ── 정책 ──────────────────────────────────────────────────────────────────

@router.get("/policies", summary="알람 정책 목록", response_model=ListResponse)
async def list_policies():
    """알람 정책 전체 조회

    입력 예시

    * `GET /api/v2/alerts/policies`

    응답

    * 정책별 ID, 이름, 종류(`resource` | `log` | `slo` | `composite`)
    * `conditions`: 조건 목록 (지표, 연산자, 임계값, 집계 기간), 2개 이상이면 모두 만족할 때 발생
    * `metric`, `op`, `threshold`, `window_min`: 첫 번째 조건과 같은 값 (옛 화면 호환용)
    * 지속 시간(분)
    * 심각도, 감시 대상, 채널 ID 목록, 사용 여부
    * `selector`: 감시 대상을 입력 형식으로 (예: `cluster=l40s`), 빈 값이면 전체
    * `duration`: 지속 시간을 입력 형식으로 (예: `5m`)
    * `channels`: 연결된 채널을 종류(`email` | `slack` | `webhook`)별로 묶은 것, 채널마다 이름과 켜짐 여부
    * `total`: 정책 개수
    * 정책 ID 순으로 정렬
    """
    _require_store()
    rows = await store.list_policies()
    return ListResponse(data=await _policies_out(rows), total=len(rows))


@router.post("/policies", summary="알람 정책 생성", response_model=ItemResponse, status_code=201)
async def create_policy(body: PolicyCreate):
    """알람 정책 생성

    입력 예시

    * 조건 1개: `{"name": "L40S 온도", "metric": "temperature_celsius", "op": ">=", "threshold": 80, "duration": "5m", "selector": "cluster=l40s", "severity": "warning", "channels": ["email"]}`
    * 조건 2개(조합): `{"name": "GPU 점유 중 오류 반복", "conditions": [{"metric": "utilization_percent", "op": ">=", "threshold": 95}, {"metric": "log_error_count", "op": ">=", "threshold": 20, "window_min": 5}], "duration": "3m", "selector": "cluster=l40s", "severity": "critical", "channels": ["email"]}`

    입력 옵션

    * `name`: 정책 이름 (필수)
    * `conditions`: 조건 목록, 조건마다 `metric`, `op`, `threshold`, `window_min` (조건 2개 이상이면 필수)
    * `metric`: 지표 키, `GET /alerts/metrics` 의 `key` 중 하나 (조건 1개일 때, `conditions` 대신 사용)
    * `op`: `>=` | `>` | `<=` | `<` | `==` | `!=` (기본 `>=`)
    * `threshold`: 임계값 숫자 (조건 1개일 때 필수)
    * `window_min`: `log`, `slo` 지표만, 건수를 세거나 p95 를 계산하는 기간(분) (기본 `5`)
    * `duration`: 이 시간 동안 계속되면 발생, `5m` 처럼 숫자 + `m` (기본 `0m`, 즉시)
    * `selector`: 감시 대상, `cluster=l40s,node=gpu-node-1` (기본 전체)
    * `log` 지표의 `selector` 예: `cluster=l40s,container=vllm-slo` (`unit` 은 systemd 유닛)
    * `slo` 지표의 `selector` 예: `cluster=l40s,model=qwen2.5-3b`
    * 조건 2개 이상이면 `selector` 는 `cluster`, `unit`, `container` 만 가능
    * `severity`: `info` | `warning` | `critical` (기본 `warning`)
    * `channels`: `email` | `slack` | `webhook` 중 여러 개 (기본 없음)

    조합 정책 동작

    * 조건마다 따로 평가한 뒤 같은 클러스터에서 모두 만족하면 발생 (그리고)
    * 가속기, SLO 값은 클러스터 안에서 가장 나쁜 값으로 대표 (`>=` 면 최대, `<=` 면 최소)
    * 로그 조건은 해당 로그가 없으면 0건으로 계산

    응답

    * 저장된 정책 (ID, 종류, 조건 목록, 지속 시간, 심각도, 대상, 채널 ID 목록)
    * `kind`: 조건 1개면 지표 종류, 2개 이상이면 `composite`
    * `channels`: 연결된 채널을 종류별로 묶은 것, 채널마다 이름과 켜짐 여부

    오류

    * 422: 저장하지 않고 잘못된 칸을 모두 한 번에 반환
    * 응답 형식: `{"detail": {"errors": [{"field": "duration", "message": "..."}]}}`
    * `field` 는 입력 칸 이름 (`metric` | `threshold` | `duration` | `selector` | `channels`), 조건 목록이면 `conditions[0].metric` 처럼 번호 포함
    * 형식 오류: 목록에 없는 지표, `duration` 형식, 그 종류에서 못 쓰는 대상 키, 등록되지 않은 채널 종류
    * 대상 오타: 수집 중이 아닌 클러스터, 지금 데이터가 없는 노드, 가속기, 모델
    * `log` 지표의 `selector` 는 `cluster`, `unit`, `container` 만 가능
    * `slo` 지표의 `selector` 는 `cluster`, `model` 만 가능
    """
    _require_store()
    row = await store.create_policy(await _check(body.model_dump()))
    return ItemResponse(data=(await _policies_out([row]))[0])


@router.get("/policies/{policy_id}", summary="알람 정책 상세", response_model=ItemResponse)
async def get_policy(policy_id: str):
    """알람 정책 1건 조회

    입력 예시

    * `GET /api/v2/alerts/policies/res-003`

    응답

    * ID, 이름, 종류(`resource` | `log` | `slo` | `composite`)
    * `conditions`: 조건 목록 (지표, 연산자, 임계값, 집계 기간)
    * 지속 시간(분)
    * 심각도, 감시 대상, 채널 ID 목록, 사용 여부, 생성·수정 시각
    * `selector`, `duration`: 대상과 지속 시간을 입력 형식으로
    * `channels`: 연결된 채널을 종류(`email` | `slack` | `webhook`)별로 묶은 것, 채널마다 이름과 켜짐 여부

    오류

    * 404: 없는 정책
    """
    _require_store()
    row = await _policy_or_404(policy_id)
    return ItemResponse(data=(await _policies_out([row]))[0])


@router.patch("/policies/{policy_id}", summary="알람 정책 수정", response_model=ItemResponse)
async def patch_policy(policy_id: str, body: PolicyPatch):
    """알람 정책 수정

    입력 예시

    * `PATCH /api/v2/alerts/policies/res-003`
    * `{"threshold": 85, "duration": "10m"}`
    * 조건 추가: `{"conditions": [{"metric": "utilization_percent", "op": ">=", "threshold": 95}, {"metric": "log_error_count", "op": ">=", "threshold": 20}], "selector": "cluster=l40s"}`

    입력 옵션

    * 정책 생성과 같은 항목 중 바꿀 것만 보냄
    * `metric`, `op`, `threshold`, `window_min`: 첫 번째 조건만 고침
    * `conditions`: 조건 목록을 통째로 교체, 1개로 줄이면 단일 정책, 2개 이상이면 조합 정책
    * `enabled`: `false` 면 정책 끄기

    응답

    * 수정된 정책

    참고

    * 종류(`kind`)가 바뀌면 대상 단위가 달라지므로 이 정책으로 걸려 있던 알람은 지움
    * 종류가 바뀌는데 `selector` 를 안 보내면 저장된 대상을 새 종류 기준으로 다시 검사

    오류

    * 422: 정책 생성과 같은 검사, 보낸 칸만 검사하고 잘못된 칸을 모두 한 번에 반환
    * 404: 없는 정책
    """
    _require_store()
    cur = await _policy_or_404(policy_id)
    patch = {k: v for k, v in body.model_dump(exclude_unset=True).items() if v is not None}
    row = await store.update_policy(policy_id, await _check(patch, cur))
    if row["kind"] != cur["kind"]:
        await store.clear_active(policy_id)
    return ItemResponse(data=(await _policies_out([row]))[0])


@router.delete("/policies/{policy_id}", summary="알람 정책 삭제", status_code=204)
async def delete_policy(policy_id: str):
    """알람 정책 삭제

    입력 예시

    * `DELETE /api/v2/alerts/policies/pol-1a2b3c`

    응답

    * 성공 시 본문 없이 204

    참고

    * 이 정책으로 걸린 알람도 함께 삭제
    * 알람 이력은 남음
    * 404: 없는 정책
    """
    _require_store()
    if not await store.delete_policy(policy_id):
        raise HTTPException(status_code=404, detail="POLICY_NOT_FOUND")


@router.post("/policies/{policy_id}/preview", summary="알람 정책 미리 평가", response_model=ListResponse)
async def preview_policy(policy_id: str):
    """알람 정책을 지금 값으로 미리 평가

    입력 예시

    * `POST /api/v2/alerts/policies/res-003/preview`

    응답

    * 대상별 현재 값, 조건 충족 여부(`cond`)
    * 조합 정책은 `values` 에 조건별 지표 키, 현재 값, 충족 여부, `cond` 는 모두 충족 여부
    * `total`: 평가한 대상 개수

    참고

    * 알림 발송, 이력 기록 없음
    * 임계값을 정할 때 지금 어느 가속기가 걸리는지 확인하는 용도
    """
    _require_store()
    policy = await _policy_or_404(policy_id)
    results, warnings = await alerts.evaluate_policy(policy)
    data = [PreviewItem(**r) for r in results.values()]
    return ListResponse(status="partial" if warnings else "success", data=data, total=len(data), warnings=warnings)


# ── 채널 ──────────────────────────────────────────────────────────────────

@router.get("/channels", summary="알림 채널 목록", response_model=ListResponse)
async def list_channels():
    """알림 채널 전체 조회

    입력 예시

    * `GET /api/v2/alerts/channels`

    응답

    * 채널별 ID, 이름, 종류(`email` | `webhook`), 설정(`config`)
    * 해제 알림 여부, 사용 여부, 생성 시각
    * `total`: 채널 개수
    * 만든 순서대로 정렬
    """
    _require_store()
    rows = await store.list_channels()
    return ListResponse(data=[Channel(**r) for r in rows], total=len(rows))


@router.post("/channels", summary="알림 채널 생성", response_model=ItemResponse, status_code=201)
async def create_channel(body: ChannelCreate):
    """알림 채널 생성

    입력 예시

    * 메일: `{"name": "운영팀 메일", "type": "email", "config": {"to": ["ops@example.com"]}}`
    * Slack: `{"name": "개발 Slack", "type": "webhook", "config": {"url": "https://hooks.slack.com/services/...", "format": "slack"}}`

    입력 옵션

    * `name`: 채널 이름 (필수)
    * `type`: `email` | `webhook` (필수)
    * `config.to`: 메일 수신 주소 목록 (`email` 필수)
    * `config.url`: 알림을 받을 주소 (`webhook` 필수)
    * `config.format`: `generic` | `slack` (기본 `generic`)
    * `config.headers`: 함께 보낼 HTTP 헤더 (선택)
    * `notify_resolved`: 해제 알림도 보낼지 (기본 `true`)
    * `enabled`: 채널 사용 여부 (기본 `true`)

    응답

    * 저장된 채널 (ID, 이름, 종류, 설정, 해제 알림 여부, 사용 여부)

    참고

    * 422: 필수 설정 누락
    * 메일 발송은 서버에 메일 서버(SMTP) 설정 필요
    """
    _require_store()
    c = body.model_dump()
    if c["type"] == "webhook" and not c["config"].get("url"):
        raise HTTPException(status_code=422, detail="webhook 채널은 config.url 이 필요")
    if c["type"] == "email" and not c["config"].get("to"):
        raise HTTPException(status_code=422, detail="email 채널은 config.to 수신자 목록이 필요")
    return ItemResponse(data=Channel(**await store.create_channel(c)))


@router.patch("/channels/{channel_id}", summary="알림 채널 수정", response_model=ItemResponse)
async def patch_channel(channel_id: str, body: ChannelPatch):
    """알림 채널 수정

    입력 예시

    * `PATCH /api/v2/alerts/channels/ch-1a2b3c`
    * `{"enabled": false}`

    입력 옵션

    * `name`, `config`, `notify_resolved`, `enabled` 중 바꿀 것만 보냄
    * `config` 는 보낸 값으로 통째로 교체
    * `type` 은 변경 불가

    응답

    * 수정된 채널

    오류

    * 404: 없는 채널
    """
    _require_store()
    row = await store.update_channel(channel_id, body.model_dump(exclude_unset=True))
    if not row:
        raise HTTPException(status_code=404, detail="CHANNEL_NOT_FOUND")
    return ItemResponse(data=Channel(**row))


@router.delete("/channels/{channel_id}", summary="알림 채널 삭제", status_code=204)
async def delete_channel(channel_id: str):
    """알림 채널 삭제

    입력 예시

    * `DELETE /api/v2/alerts/channels/ch-1a2b3c`

    응답

    * 성공 시 본문 없이 204

    오류

    * 404: 없는 채널
    """
    _require_store()
    if not await store.delete_channel(channel_id):
        raise HTTPException(status_code=404, detail="CHANNEL_NOT_FOUND")


@router.post("/channels/{channel_id}/test", summary="알림 채널 시험 발송", response_model=ItemResponse)
async def test_channel(channel_id: str):
    """알림 채널로 시험 메시지 발송

    입력 예시

    * `POST /api/v2/alerts/channels/ch-1a2b3c/test`

    응답

    * 발송 성공 여부(`sent`), 채널 ID

    참고

    * 정책에 연결하기 전 설정 확인용
    * 502: 발송 실패, 실패 이유 함께 반환
    """
    _require_store()
    ch = await store.get_channel(channel_id)
    if not ch:
        raise HTTPException(status_code=404, detail="CHANNEL_NOT_FOUND")
    payload = {"status": "test", "policy_id": None, "policy_name": "시험 발송", "severity": "info",
               "target": {}, "value": None, "message": f"[test] KCloud Monitor 알림 채널 '{ch['name']}' 시험 발송",
               "started_at": None, "observed_at": datetime.now().astimezone().isoformat()}
    try:
        await alerts.send_channel(ch, payload)
    except Exception as exc:
        raise HTTPException(status_code=502, detail=f"CHANNEL_SEND_FAILED: {exc}")
    return ItemResponse(data={"sent": True, "channel_id": channel_id})


# ── 활성 알람 / 이력 ──────────────────────────────────────────────────────

@router.get("/active", summary="활성 알람 목록", response_model=ListResponse)
async def list_active(
    state: Optional[str] = Query(None, pattern="^(pending|firing|resolved)$",
                                 description="알람 상태 (pending | firing | resolved), 미지정 시 pending + firing"),
):
    """지금 걸려 있는 알람 목록 조회

    입력 예시

    * `GET /api/v2/alerts/active`
    * `GET /api/v2/alerts/active?state=firing`

    입력 옵션

    * `state`: `pending` | `firing` | `resolved` (기본 `pending` + `firing`)

    응답

    * 알람별 식별값(`fingerprint`), 정책 ID, 상태, 심각도
    * 대상, 현재 값, 메시지
    * 지속된 시간(`duration_seconds`), 정책 기준 시간(`for_seconds`)
    * 시작, 발생, 해제, 마지막 알림, 확인 시각
    * `total`: 알람 개수
    * 조건이 시작된 시각 최신순으로 정렬

    상태

    * `pending`: 기준 시간을 채우는 중
    * `firing`: 발생
    * `resolved`: 해제
    """
    _require_store()
    rows = await store.list_active(state)
    for_min = {p["id"]: p["for_min"] for p in await store.list_policies()}
    data = [ActiveAlert(**r, **_timing(r, for_min.get(r["policy_id"], 0))) for r in rows]
    return ListResponse(data=data, total=len(rows))


# fingerprint 에 / 가 들어 있어(log-001:mgmt/*/*) 일반 경로 변수로는 매칭 안 됨. path 변환기로 받음
@router.post("/active/{fingerprint:path}/ack", summary="활성 알람 확인 처리", response_model=ItemResponse)
async def ack_alert(fingerprint: str):
    """알람 확인 처리

    입력 예시

    * `POST /api/v2/alerts/active/log-001:mgmt/*/*/ack`
    * `POST /api/v2/alerts/active/res-003:l40s/gpu-node-1/GPU-00000000-0000-0000-0000-000000000001/ack`

    입력 옵션

    * `fingerprint`: 활성 알람 목록(`GET /api/v2/alerts/active`) 응답의 `fingerprint` 값 그대로 (경로, 필수)
    * 형식은 `정책ID:클러스터/노드/가속기ID`, 해당 없는 칸은 `*`

    응답

    * 확인 처리된 알람

    참고

    * 해제될 때까지 재알림 중지
    * 404: 없는 알람
    """
    _require_store()
    row = await store.ack(fingerprint)
    if not row:
        raise HTTPException(status_code=404, detail="ALERT_NOT_FOUND")
    pol = await store.get_policy(row["policy_id"])
    return ItemResponse(data=ActiveAlert(**row, **_timing(row, pol["for_min"] if pol else 0)))


@router.get("/events", summary="알람 이력", response_model=ListResponse)
async def list_events(
    policy_id: Optional[str] = Query(None, description="특정 정책만 조회할 때 정책 ID"),
    since: Optional[datetime] = Query(None, description="이 시각 이후 이력만 (ISO 8601, 예: 2026-10-07T00:00:00Z)"),
    limit: int = Query(100, ge=1, le=1000, description="최대 개수 (1 ~ 1000, 기본 100)"),
):
    """알람 이력 조회

    입력 예시

    * `GET /api/v2/alerts/events?limit=50`
    * `GET /api/v2/alerts/events?policy_id=res-003&since=2026-10-07T00:00:00Z`

    입력 옵션

    * `policy_id`: 특정 정책만 (선택)
    * `since`: 이 시각 이후만, ISO 8601 (선택)
    * `limit`: 최대 개수 `1` ~ `1000` (기본 `100`)

    응답

    * 이력별 시각, 정책 ID, 종류, 심각도, 대상, 값, 메시지
    * `total`: 이력 개수
    * 기록 시각 최신순으로 정렬

    종류

    * `fire`: 발생
    * `resolve`: 해제
    * `notified`: 재알림
    * `notify_failed`: 발송 실패 (메시지에 이유)
    """
    _require_store()
    rows = await store.list_events(policy_id, since, limit)
    return ListResponse(data=[AlertEvent(**r) for r in rows], total=len(rows))
