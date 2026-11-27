"""로그 조회 라우터

Loki에 LogQL 쿼리를 보내 로그를 검색.
수집기(Alloy)가 붙이는 라벨: cluster, node, level(error|warning|info|debug), job
(loki.source.kubernetes.pods | loki.source.journal). 파드 로그는 namespace, pod, container,
journal 로그는 unit, transport가 추가로 붙음.
"""
import asyncio
import csv
import io
import json
import logging
import re
import time
from datetime import datetime, timedelta, timezone
from typing import Any, Optional

from fastapi import APIRouter, HTTPException, Query, Request
from fastapi.responses import StreamingResponse

from app.api.v2.accelerators import _instant_metric
from app.schemas.logs import (
    AcceleratorLogResponse,
    LabelListResponse,
    LabelValuesResponse,
    LogEntry,
    LogPagination,
    LogSearchResponse,
    NodeLogResponse,
    PodLogResponse,
    VolumeEntry,
    VolumeResponse,
)
from app.services.cluster_discovery import cluster_discovery
from app.services.loki import loki_client
from app.services.power import _card_host

logger = logging.getLogger(__name__)

router = APIRouter()

# ---------------------------------------------------------------------------
# 내부 헬퍼
# ---------------------------------------------------------------------------

_LABEL_VALUE_SAFE = re.compile(r"^[\w.\-/:@ ]+$")


def _require_loki() -> None:
    if not loki_client.configured:
        raise HTTPException(status_code=503, detail="LOKI_URL 미설정")


def _sanitize_label(value: str) -> str:
    """LogQL 라벨 값 이스케이프 — 주입 방지."""
    if not value or not _LABEL_VALUE_SAFE.match(value):
        raise HTTPException(status_code=400, detail=f"잘못된 라벨 값: {value!r}")
    return value.replace("\\", "\\\\").replace('"', '\\"')


_LEVELS = ("error", "warning", "info", "debug")


def _level(value: Optional[str]) -> Optional[str]:
    if not value:
        return None
    v = value.lower()
    if v not in _LEVELS:
        raise HTTPException(status_code=400, detail=f"log_level은 {'|'.join(_LEVELS)} 중 하나: {value!r}")
    return v


def _scoped(query: Optional[str], **labels: Optional[str]) -> str:
    """LogQL 맨 앞 셀렉터 {...}에 라벨 조건을 끼워 넣음. 값이 비어 있는 라벨은 건너뜀.

    Loki가 라벨로 먼저 골라 limit만큼 주므로, 받은 뒤 거를 때처럼 행이 줄지 않음.
    """
    matchers = ", ".join(f'{k}="{_sanitize_label(v)}"' for k, v in labels.items() if v)
    q = (query or "").strip()
    if not matchers:
        return q
    if q.startswith("{"):
        rest = q[1:].lstrip()
        return "{" + matchers + ("" if rest.startswith("}") else ", ") + rest
    return f"{{{matchers}}} {q}".rstrip()


def _default_start() -> str:
    return (datetime.now(timezone.utc) - timedelta(hours=1)).isoformat()


def _default_end() -> str:
    return datetime.now(timezone.utc).isoformat()


def _ns_to_iso(ns_str: str) -> str:
    """Loki 나노초 타임스탬프 → ISO 8601."""
    ns = int(ns_str)
    seconds = ns // 1_000_000_000
    micros = (ns % 1_000_000_000) // 1_000
    dt = datetime.fromtimestamp(seconds, tz=timezone.utc).replace(microsecond=micros)
    return dt.isoformat()


def _extract_log_level(line: str, labels: dict[str, str]) -> str:
    for key in ("detected_level", "level", "log_level", "severity"):
        if key in labels:
            val = labels[key].lower()
            if val in ("error", "err"):
                return "error"
            if val in ("warning", "warn"):
                return "warning"
            if val == "debug":
                return "debug"
            return "info"
    ll = line[:512].lower()
    if "level=error" in ll or "[error]" in ll:
        return "error"
    if "level=warn" in ll or "[warn" in ll:
        return "warning"
    if "level=debug" in ll or "[debug]" in ll:
        return "debug"
    return "info"


# GPU 로그 구조화 필드 파서
_XID_RE = re.compile(r"NVRM: Xid \(PCI:([0-9a-fA-F:\.]+)\):\s*(\d+)")
_OOM_RE = re.compile(
    r"CUDA out of memory.*?allocate\s+(\d+\.?\d*)\s*GiB.*?(\d+\.?\d*)\s*GiB already allocated",
    re.I,
)
# 리눅스 커널 OOM killer. "Out of memory: Killed process 20481 (python3) ..." 또는
# "Memory cgroup out of memory: Killed process ..." (컨테이너 메모리 한도 초과)
_KERNEL_OOM_RE = re.compile(r"Killed process (\d+) \(([^)]+)\)")
# 벤더별 드라이버 로그를 골라내는 LogQL 정규식 (대소문자 무시)
_DRIVER_PATTERNS = {
    "nvidia": "(?i)nvrm|nvidia",
    "rebellions": "(?i)rbln|rebellions",
    "furiosa": "(?i)furiosa|npu",
}
_CRITICAL_XID_CODES = frozenset(
    {13, 31, 43, 45, 48, 61, 62, 63, 64, 68, 69, 74, 79, 92, 94, 95, 119, 120}
)


def _parse_detected_fields(line: str) -> dict[str, Any]:
    fields: dict[str, Any] = {}
    m = _XID_RE.search(line)
    if m:
        xid = int(m.group(2))
        fields["xid_code"] = xid
        fields["gpu_pci_bdf"] = m.group(1)
        fields["severity"] = "critical" if xid in _CRITICAL_XID_CODES else "warning"
    m = _OOM_RE.search(line)
    if m:
        fields["error_type"] = "CUDA OOM"
        fields["requested_gb"] = float(m.group(1))
        fields["allocated_gb"] = float(m.group(2))
        fields["severity"] = "warning"  # 프로그램이 예외를 받고 살아 있을 수 있음
    m = _KERNEL_OOM_RE.search(line)
    if m:
        fields["error_type"] = "OOM"
        fields["pid"] = int(m.group(1))
        fields["process"] = m.group(2)
        fields["severity"] = "critical"  # 커널이 프로세스를 강제 종료함
    return fields


def _to_entry(ts_ns: str, line: str, labels: dict[str, str]) -> LogEntry:
    return LogEntry(
        timestamp=_ns_to_iso(ts_ns),
        log_level=_extract_log_level(line, labels),
        message=line.rstrip("\n"),
        labels=labels,
        detected_fields=_parse_detected_fields(line),
        trace_id=labels.get("traceID", ""),
        span_id=labels.get("spanID", ""),
    )


def _sorted_rows(loki_resp: dict, direction: str) -> list[tuple[int, str, dict[str, str]]]:
    """Loki는 스트림별로 묶어 주므로 시각 기준으로 다시 정렬. backward면 최신부터."""
    rows = [
        (int(ts_ns), line, stream.get("stream", {}))
        for stream in loki_resp.get("data", {}).get("result", [])
        for ts_ns, line in stream.get("values", [])
    ]
    # 같은 나노초 줄끼리도 순서가 고정돼야 커서의 건너뛰기 수가 맞음
    rows.sort(key=lambda r: (r[0], sorted(r[2].items()), r[1]), reverse=direction == "backward")
    return rows


def _transform_loki_response(
    loki_resp: dict, limit: int, direction: str = "backward",
) -> tuple[list[LogEntry], int]:
    """Loki query_range 응답 → 시각순 LogEntry 플랫 리스트."""
    rows = _sorted_rows(loki_resp, direction)
    return [_to_entry(str(ts), line, labels) for ts, line, labels in rows[:limit]], len(rows)


def _new_stream_rows(rows: list, last_ns: int, seen: set) -> tuple[list, int, set]:
    """오름차순 rows 중 아직 안 보낸 줄만 추림.

    다음 폴링이 last_ns부터 다시 조회하므로, last_ns와 같은 시각의 줄은
    (원문, 라벨)로 기억해 두었다가 중복만 거름. 같은 시각의 다른 줄은 살림.
    """
    fresh = []
    for ts, line, labels in rows:
        key = (line, tuple(sorted(labels.items())))
        if ts < last_ns or (ts == last_ns and key in seen):
            continue
        if ts > last_ns:
            last_ns, seen = ts, set()
        seen.add(key)
        fresh.append((ts, line, labels))
    return fresh, last_ns, seen


# ponytail: Loki 기본 max_entries_limit_per_query. 서버 설정을 올리면 같이 올림
_LOKI_MAX_LIMIT = 5000


def _parse_cursor(cursor: str) -> tuple[int, int]:
    try:
        ts, skip = cursor.split(":")
        return int(ts), int(skip)
    except ValueError:
        raise HTTPException(status_code=400, detail=f"잘못된 cursor: {cursor!r}")


async def _query_page(
    logql: str, start: Optional[str], end: Optional[str], limit: int, direction: str,
    cursor: Optional[str],
) -> tuple[list[LogEntry], LogPagination]:
    """시각 기준 페이지 조회. cursor는 "마지막 행 나노초:그 시각에 이미 보낸 행 수".

    Loki 구간은 start 포함, end 미포함이라 backward는 end를 커서 시각+1ns로,
    forward는 start를 커서 시각으로 잡아 경계 시각의 행을 다시 받은 뒤 보낸 만큼 건너뜀.
    limit보다 1행 더 받아 다음 페이지 존재 여부를 판단.
    """
    s = start or _default_start()
    e = end or _default_end()
    cur_ts, skip = _parse_cursor(cursor) if cursor else (None, 0)
    if cur_ts is not None:
        if direction == "backward":
            e = str(cur_ts + 1)
        else:
            s = str(cur_ts)

    want = min(limit + skip + 1, _LOKI_MAX_LIMIT)
    resp = await loki_client.query_range(logql, s, e, want, direction)
    rows = _sorted_rows(resp, direction)
    fetched = len(rows)

    if cur_ts is not None:
        at_cursor = next((i for i, r in enumerate(rows) if r[0] != cur_ts), len(rows))
        rows = rows[min(skip, at_cursor):]

    page = rows[:limit]
    has_next = len(rows) > limit or fetched == want == _LOKI_MAX_LIMIT
    next_cursor = None
    if has_next and page:
        last_ts = page[-1][0]
        sent = sum(1 for r in page if r[0] == last_ts) + (skip if last_ts == cur_ts else 0)
        next_cursor = f"{last_ts}:{sent}"

    entries = [_to_entry(str(ts), line, labels) for ts, line, labels in page]
    pagination = LogPagination(
        total=len(entries), limit=limit, has_next=has_next, next_cursor=next_cursor,
    )
    return entries, pagination


# ---------------------------------------------------------------------------
# 로그 검색 (6 endpoints)
# ---------------------------------------------------------------------------


@router.get("/logs/search", summary="로그 검색(LogQL)", response_model=LogSearchResponse)
async def log_search(
    request: Request,
    query: str = Query(..., description="LogQL 쿼리, 예 `{namespace=\"openstack\"} |= \"ERROR\"` (필수)"),
    start: Optional[str] = Query(None, description="시작 시각, ISO 8601 형식 (기본 1시간 전)"),
    end: Optional[str] = Query(None, description="종료 시각, ISO 8601 형식 (기본 현재 시각)"),
    limit: int = Query(100, ge=1, le=5000, description="한 번에 받을 개수 (1 ~ 5000, 기본 100)"),
    direction: str = Query("backward", pattern="^(forward|backward)$", description="정렬 방향, `backward` 는 최신 로그부터 (기본 `backward`)"),
    cursor: Optional[str] = Query(None, description="다음 페이지 커서, 직전 응답의 `pagination.next_cursor` 값, 형식 `<나노초>:<건수>`"),
    cluster: Optional[str] = Query(None, description="클러스터 이름, 예 `mgmt` (기본 전체)"),
    log_level: Optional[str] = Query(None, description="로그 레벨 (`error` | `warning` | `info` | `debug`, 기본 전체)"),
):
    """LogQL 쿼리로 로그 검색

    입력 예시

    * `GET /api/v2/logs/search?query={namespace="openstack"} |= "ERROR"`
    * `GET /api/v2/logs/search?query={namespace="openstack"} |= "ERROR"&cluster=mgmt&log_level=error&limit=50`

    입력 옵션

    * `query`: LogQL 쿼리 (필수)
    * `cluster`: 클러스터 이름, 예 `mgmt` (선택, 기본 전체)
    * `start`: 시작 시각, ISO 8601 형식 (선택, 기본 1시간 전)
    * `end`: 종료 시각, ISO 8601 형식 (선택, 기본 현재 시각)
    * `limit`: 한 번에 받을 개수 `1` ~ `5000` (기본 `100`)
    * `direction`: `backward` | `forward` (기본 `backward`)
    * `cursor`: 다음 페이지 커서, 직전 응답의 `pagination.next_cursor` 값, 형식 `"<나노초>:<건수>"` (선택)
    * `log_level`: `error` | `warning` | `info` | `debug` (선택, 기본 전체)

    응답

    * `data`: 로그 목록, 항목마다 `timestamp`, `log_level`, `message`, `labels`, `detected_fields`, `trace_id`, `span_id`
    * `pagination`: `total`, `limit`, `offset`, `has_next`, `next_cursor`
    * `direction` 순서로 정렬, `backward` 는 최신순
    * 다음 페이지는 `cursor` 에 `next_cursor` 값을 넣어 요청

    경고

    * `CLUSTER_LABEL_NOT_FOUND`: `cluster` 를 지정했는데 결과가 없음

    오류

    * 400: LogQL 오류, 잘못된 `cursor`, `log_level`, 라벨 값
    * 502: Loki 응답 오류
    * 504: Loki 연결 실패
    * 503: `LOKI_URL` 미설정
    """
    _require_loki()
    warnings: list[str] = []

    logql = _scoped(query, cluster=cluster, level=_level(log_level))
    entries, pagination = await _query_page(logql, start, end, limit, direction, cursor)

    if cluster and not entries:
        warnings.append("CLUSTER_LABEL_NOT_FOUND")

    return LogSearchResponse(
        status="partial" if warnings else "success",
        data=entries,
        pagination=pagination,
        warnings=warnings,
    )


@router.get("/logs/stream", summary="실시간 로그 스트림(SSE)")
async def log_stream(
    request: Request,
    query: str = Query(..., description="LogQL 쿼리, 예 `{namespace=\"openstack\"} |= \"ERROR\"` (필수)"),
    limit: int = Query(50, ge=1, le=500, description="2초마다 확인할 때 한 번에 받을 개수 (1 ~ 500, 기본 50)"),
    cluster: Optional[str] = Query(None, description="클러스터 이름, 예 `mgmt` (기본 전체)"),
    log_level: Optional[str] = Query(None, description="로그 레벨 (`error` | `warning` | `info` | `debug`, 기본 전체)"),
):
    """새로 들어오는 로그를 실시간으로 받는 SSE 스트림

    입력 예시

    * `GET /api/v2/logs/stream?query={namespace="openstack"} |= "ERROR"`
    * `GET /api/v2/logs/stream?query={namespace="openstack"} |= "ERROR"&cluster=mgmt&log_level=error&limit=50`

    입력 옵션

    * `query`: LogQL 쿼리 (필수)
    * `limit`: 2초마다 확인할 때 한 번에 받을 개수 `1` ~ `500` (기본 `50`)
    * `cluster`: 클러스터 이름, 예 `mgmt` (선택, 기본 전체)
    * `log_level`: `error` | `warning` | `info` | `debug` (선택, 기본 전체)

    응답

    * `event: log`: 로그 1건, `timestamp`, `log_level`, `message`, `labels`, `detected_fields`, `trace_id`, `span_id`
    * `event: heartbeat`: 약 15초마다 `observed_at` 전송
    * `event: error`: 쿼리 오류나 Loki 장애 시 `status_code`, `detail` 전송 후 스트림 종료

    참고

    * 2초 간격으로 새 로그 확인
    * 연결한 시점 이후의 로그만 전송
    * 스트림이 시작된 뒤의 오류는 HTTP 코드 대신 `error` 이벤트로 전달

    오류

    * 400: 잘못된 `log_level`, 라벨 값
    * 503: `LOKI_URL` 미설정
    """
    _require_loki()
    query = _scoped(query, cluster=cluster, level=_level(log_level))

    async def _generate():
        last_ns = int(time.time() * 1e9)
        seen: set = set()
        heartbeat_acc = 0

        while True:
            if await request.is_disconnected():
                break

            now_ns = int(time.time() * 1e9)
            try:
                resp = await loki_client.query_range(
                    query, str(last_ns), str(now_ns), limit, "forward",
                )
            except HTTPException as exc:
                # 응답이 이미 시작돼 HTTP 코드로 못 알리므로 error 이벤트 후 종료. 반복 재시도 방지
                err = {"status_code": exc.status_code, "detail": exc.detail}
                yield f"event: error\ndata: {json.dumps(err, ensure_ascii=False)}\n\n"
                break
            rows, last_ns, seen = _new_stream_rows(_sorted_rows(resp, "forward"), last_ns, seen)
            for ts, line, labels in rows:
                entry = _to_entry(str(ts), line, labels)
                yield f"event: log\ndata: {json.dumps(entry.model_dump(), ensure_ascii=False)}\n\n"

            heartbeat_acc += 2
            if heartbeat_acc >= 15:
                ts = datetime.now(timezone.utc).isoformat()
                yield f"event: heartbeat\ndata: {json.dumps({'observed_at': ts})}\n\n"
                heartbeat_acc = 0

            await asyncio.sleep(2)

    return StreamingResponse(_generate(), media_type="text/event-stream")


@router.get("/logs/export", summary="로그 내보내기(CSV/JSON)")
async def log_export(
    request: Request,
    query: str = Query(..., description="LogQL 쿼리, 예 `{namespace=\"openstack\"} |= \"ERROR\"` (필수)"),
    start: Optional[str] = Query(None, description="시작 시각, ISO 8601 형식 (기본 1시간 전)"),
    end: Optional[str] = Query(None, description="종료 시각, ISO 8601 형식 (기본 현재 시각)"),
    limit: int = Query(1000, ge=1, le=5000, description="한 번에 받을 개수 (1 ~ 5000, 기본 1000)"),
    direction: str = Query("backward", pattern="^(forward|backward)$", description="정렬 방향, `backward` 는 최신 로그부터 (기본 `backward`)"),
    export_format: str = Query("json", alias="format", pattern="^(json|csv)$", description="내려받을 형식 (`json` | `csv`, 기본 `json`)"),
    cluster: Optional[str] = Query(None, description="클러스터 이름, 예 `mgmt` (기본 전체)"),
    log_level: Optional[str] = Query(None, description="로그 레벨 (`error` | `warning` | `info` | `debug`, 기본 전체)"),
):
    """검색한 로그를 CSV 또는 JSON 첨부 파일로 내려받기

    입력 예시

    * `GET /api/v2/logs/export?query={namespace="openstack"} |= "ERROR"`
    * `GET /api/v2/logs/export?query={namespace="openstack"} |= "ERROR"&format=csv&cluster=mgmt&limit=500`

    입력 옵션

    * `query`: LogQL 쿼리 (필수)
    * `start`: 시작 시각, ISO 8601 형식 (선택, 기본 1시간 전)
    * `end`: 종료 시각, ISO 8601 형식 (선택, 기본 현재 시각)
    * `limit`: 한 번에 받을 개수 `1` ~ `5000` (기본 `1000`)
    * `direction`: `backward` | `forward` (기본 `backward`)
    * `format`: `json` | `csv` (기본 `json`)
    * `cluster`: 클러스터 이름, 예 `mgmt` (선택, 기본 전체)
    * `log_level`: `error` | `warning` | `info` | `debug` (선택, 기본 전체)

    응답

    * `format=csv`: `logs_export.csv` 첨부 파일, 열은 `timestamp`, `log_level`, `message`, `labels`
    * `format=json`: `logs_export.json` 첨부 파일, 로그별 `timestamp`, `log_level`, `message`, `labels`, `detected_fields`, `trace_id`, `span_id` 배열
    * `direction` 순서로 정렬, `backward` 는 최신순

    참고

    * `status`, `observed_at` 같은 공통 응답 형식 없이 파일 본문만 반환
    * 페이지 나눔 없이 `limit` 개수까지 한 번에 반환

    오류

    * 400: LogQL 오류, 잘못된 `log_level`, 라벨 값
    * 502: Loki 응답 오류
    * 504: Loki 연결 실패
    * 503: `LOKI_URL` 미설정
    """
    _require_loki()

    logql = _scoped(query, cluster=cluster, level=_level(log_level))
    s = start or _default_start()
    e = end or _default_end()
    resp = await loki_client.query_range(logql, s, e, limit, direction)
    entries, _ = _transform_loki_response(resp, limit, direction)

    if export_format == "csv":
        buf = io.StringIO()
        writer = csv.writer(buf)
        writer.writerow(["timestamp", "log_level", "message", "labels"])
        for en in entries:
            writer.writerow([
                en.timestamp,
                en.log_level,
                en.message,
                json.dumps(en.labels, ensure_ascii=False),
            ])
        return StreamingResponse(
            iter([buf.getvalue()]),
            media_type="text/csv",
            headers={"Content-Disposition": "attachment; filename=logs_export.csv"},
        )

    payload = json.dumps([en.model_dump() for en in entries], ensure_ascii=False, indent=2)
    return StreamingResponse(
        iter([payload]),
        media_type="application/json",
        headers={"Content-Disposition": "attachment; filename=logs_export.json"},
    )


@router.get("/logs/labels", summary="로그 라벨 이름 목록", response_model=LabelListResponse)
async def get_labels(
    request: Request,
    start: Optional[str] = Query(None, description="시작 시각, ISO 8601 형식 (기본 최근 6시간)"),
    end: Optional[str] = Query(None, description="종료 시각, ISO 8601 형식 (기본 현재 시각)"),
):
    """로그에 붙은 라벨 이름 전체 조회

    입력 예시

    * `GET /api/v2/logs/labels`
    * `GET /api/v2/logs/labels?start=2026-10-07T00:00:00Z&end=2026-10-07T06:00:00Z`

    입력 옵션

    * `start`: 시작 시각, ISO 8601 형식 (선택, 기본 최근 6시간)
    * `end`: 종료 시각, ISO 8601 형식 (선택, 기본 현재 시각)

    응답

    * `data`: 라벨 이름 목록, 예 `namespace`, `job`, `pod`

    경고

    * `LOKI_UNAVAILABLE`: Loki 조회 실패, `data` 가 빈 목록이고 `status` 는 `partial`

    참고

    * 검색 쿼리를 쓰기 전에 걸러낼 수 있는 라벨을 확인하는 용도

    오류

    * 503: `LOKI_URL` 미설정
    """
    _require_loki()
    data = await loki_client.labels(start, end)
    if data is None:
        return LabelListResponse(status="partial", warnings=["LOKI_UNAVAILABLE"])
    return LabelListResponse(data=data)


@router.get("/logs/label-values", summary="로그 라벨 값 목록", response_model=LabelValuesResponse)
async def get_label_values(
    request: Request,
    label: str = Query(..., description="라벨 이름, 예 `namespace` (필수)"),
    start: Optional[str] = Query(None, description="시작 시각, ISO 8601 형식 (기본 최근 6시간)"),
    end: Optional[str] = Query(None, description="종료 시각, ISO 8601 형식 (기본 현재 시각)"),
):
    """라벨 하나에 들어 있는 값 전체 조회

    입력 예시

    * `GET /api/v2/logs/label-values?label=namespace`
    * `GET /api/v2/logs/label-values?label=namespace&start=2026-10-07T00:00:00Z&end=2026-10-07T06:00:00Z`

    입력 옵션

    * `label`: 라벨 이름, 예 `namespace` (필수)
    * `start`: 시작 시각, ISO 8601 형식 (선택, 기본 최근 6시간)
    * `end`: 종료 시각, ISO 8601 형식 (선택, 기본 현재 시각)

    응답

    * `label`: 조회한 라벨 이름
    * `data`: 그 라벨의 값 목록

    경고

    * `LOKI_UNAVAILABLE`: Loki 조회 실패, `data` 가 빈 목록이고 `status` 는 `partial`

    오류

    * 503: `LOKI_URL` 미설정
    """
    _require_loki()
    data = await loki_client.label_values(label, start, end)
    if data is None:
        return LabelValuesResponse(status="partial", label=label, warnings=["LOKI_UNAVAILABLE"])
    return LabelValuesResponse(label=label, data=data)


@router.get("/logs/volume", summary="로그 볼륨 통계", response_model=VolumeResponse)
async def get_volume(
    request: Request,
    query: str = Query('{job=~".+"}', description="LogQL 스트림 셀렉터, 예 `{namespace=\"default\"}` (기본 `{job=~\".+\"}`)"),
    start: Optional[str] = Query(None, description="시작 시각, ISO 8601 형식 (기본 1시간 전)"),
    end: Optional[str] = Query(None, description="종료 시각, ISO 8601 형식 (기본 현재 시각)"),
    limit: int = Query(100, ge=1, le=1000, description="한 번에 받을 라벨 조합 개수 (1 ~ 1000, 기본 100)"),
):
    """라벨 조합별로 쌓인 로그 양 조회

    입력 예시

    * `GET /api/v2/logs/volume`
    * `GET /api/v2/logs/volume?query={namespace="openstack"}&limit=20`

    입력 옵션

    * `query`: LogQL 스트림 셀렉터, 예 `{namespace="default"}` (선택, 기본 `{job=~".+"}`)
    * `start`: 시작 시각, ISO 8601 형식 (선택, 기본 1시간 전)
    * `end`: 종료 시각, ISO 8601 형식 (선택, 기본 현재 시각)
    * `limit`: 한 번에 받을 라벨 조합 개수 `1` ~ `1000` (기본 `100`)

    응답

    * `data`: 라벨 조합 목록, 항목마다 `labels`, `volume` (로그 크기, bytes 숫자 문자열)

    경고

    * `LOKI_UNAVAILABLE`: Loki 조회 실패, `data` 가 빈 목록이고 `status` 는 `partial`

    참고

    * 로그를 많이 내는 대상을 찾는 용도

    오류

    * 503: `LOKI_URL` 미설정
    """
    _require_loki()

    s = start or _default_start()
    e = end or _default_end()
    result = await loki_client.volume(query, s, e, limit)

    vol_entries: list[VolumeEntry] = []
    for stream in result.get("data", {}).get("result", []):
        value = stream.get("value", [None, "0"])
        vol_entries.append(
            VolumeEntry(
                labels=stream.get("metric", {}),
                volume=value[1] if isinstance(value, list) and len(value) > 1 else "0",
            )
        )

    if not result:
        return VolumeResponse(status="partial", warnings=["LOKI_UNAVAILABLE"])
    return VolumeResponse(data=vol_entries)


# ---------------------------------------------------------------------------
# 클러스터 범위 로그 (4 endpoints)
# ---------------------------------------------------------------------------


@router.get(
    "/logs/clusters/{cluster}/search",
    summary="클러스터 로그 검색",
    response_model=LogSearchResponse,
)
async def cluster_log_search(
    request: Request,
    cluster: str,
    query: Optional[str] = Query(None, description="추가로 걸러낼 LogQL 조건 (기본 전체)"),
    start: Optional[str] = Query(None, description="시작 시각, ISO 8601 형식 (기본 1시간 전)"),
    end: Optional[str] = Query(None, description="종료 시각, ISO 8601 형식 (기본 현재 시각)"),
    limit: int = Query(100, ge=1, le=5000, description="한 번에 받을 개수 (1 ~ 5000, 기본 100)"),
    direction: str = Query("backward", pattern="^(forward|backward)$", description="정렬 방향, `backward` 는 최신 로그부터 (기본 `backward`)"),
    cursor: Optional[str] = Query(None, description="다음 페이지 커서, 직전 응답의 `pagination.next_cursor` 값, 형식 `<나노초>:<건수>`"),
    log_level: Optional[str] = Query(None, description="로그 레벨 (`error` | `warning` | `info` | `debug`, 기본 전체)"),
):
    """클러스터 한 개의 로그만 골라 검색

    입력 예시

    * `GET /api/v2/logs/clusters/mgmt/search`
    * `GET /api/v2/logs/clusters/mgmt/search?query=|= "ERROR"&log_level=error&limit=50`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `mgmt` (경로, 필수)
    * `query`: 추가로 걸러낼 LogQL 조건 (선택, 기본 전체)
    * `start`: 시작 시각, ISO 8601 형식 (선택, 기본 1시간 전)
    * `end`: 종료 시각, ISO 8601 형식 (선택, 기본 현재 시각)
    * `limit`: 한 번에 받을 개수 `1` ~ `5000` (기본 `100`)
    * `direction`: `backward` | `forward` (기본 `backward`)
    * `cursor`: 다음 페이지 커서, 직전 응답의 `pagination.next_cursor` 값, 형식 `"<나노초>:<건수>"` (선택)
    * `log_level`: `error` | `warning` | `info` | `debug` (선택, 기본 전체)

    응답

    * `data`: 로그 목록, 항목마다 `timestamp`, `log_level`, `message`, `labels`, `detected_fields`, `trace_id`, `span_id`
    * `pagination`: `total`, `limit`, `offset`, `has_next`, `next_cursor`
    * `direction` 순서로 정렬, `backward` 는 최신순
    * 로그의 `cluster` 라벨 기준으로 필터

    경고

    * `NO_CLUSTER_LOGS`: 결과가 없음

    오류

    * 400: LogQL 오류, 잘못된 `cursor`, `log_level`, 라벨 값
    * 502: Loki 응답 오류
    * 504: Loki 연결 실패
    * 503: `LOKI_URL` 미설정
    """
    _require_loki()
    warnings: list[str] = []

    logql = _scoped(query, cluster=cluster, level=_level(log_level))
    entries, pagination = await _query_page(logql, start, end, limit, direction, cursor)

    if not entries:
        warnings.append("NO_CLUSTER_LOGS")

    return LogSearchResponse(
        status="partial" if warnings else "success",
        data=entries,
        pagination=pagination,
        warnings=warnings,
    )


@router.get(
    "/logs/clusters/{cluster}/nodes/{node}/logs",
    summary="노드 시스템 로그",
    response_model=NodeLogResponse,
)
async def node_logs(
    request: Request,
    cluster: str,
    node: str,
    start: Optional[str] = Query(None, description="시작 시각, ISO 8601 형식 (기본 1시간 전)"),
    end: Optional[str] = Query(None, description="종료 시각, ISO 8601 형식 (기본 현재 시각)"),
    limit: int = Query(100, ge=1, le=5000, description="한 번에 받을 개수 (1 ~ 5000, 기본 100)"),
    direction: str = Query("backward", pattern="^(forward|backward)$", description="정렬 방향, `backward` 는 최신 로그부터 (기본 `backward`)"),
    cursor: Optional[str] = Query(None, description="다음 페이지 커서, 직전 응답의 `pagination.next_cursor` 값, 형식 `<나노초>:<건수>`"),
    log_level: Optional[str] = Query(None, description="로그 레벨 (`error` | `warning` | `info` | `debug`, 기본 전체)"),
):
    """노드 한 대의 운영체제 시스템 로그(journal) 조회

    입력 예시

    * `GET /api/v2/logs/clusters/mgmt/nodes/controller/logs`
    * `GET /api/v2/logs/clusters/mgmt/nodes/controller/logs?log_level=error&limit=50`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `mgmt` (경로, 필수)
    * `node`: 노드 이름, 예 `controller` (경로, 필수)
    * `start`: 시작 시각, ISO 8601 형식 (선택, 기본 1시간 전)
    * `end`: 종료 시각, ISO 8601 형식 (선택, 기본 현재 시각)
    * `limit`: 한 번에 받을 개수 `1` ~ `5000` (기본 `100`)
    * `direction`: `backward` | `forward` (기본 `backward`)
    * `cursor`: 다음 페이지 커서, 직전 응답의 `pagination.next_cursor` 값, 형식 `"<나노초>:<건수>"` (선택)
    * `log_level`: `error` | `warning` | `info` | `debug` (선택, 기본 전체)

    응답

    * `cluster`, `node`: 클러스터 이름, 노드 이름
    * `data`: 로그 목록, 항목마다 `timestamp`, `log_level`, `message`, `labels`, `detected_fields`, `trace_id`, `span_id`
    * `pagination`: `total`, `limit`, `offset`, `has_next`, `next_cursor`
    * `direction` 순서로 정렬, `backward` 는 최신순

    경고

    * `NO_NODE_LOGS`: 결과가 없음

    참고

    * 로그의 `cluster`, `node` 라벨 기준으로 필터

    오류

    * 400: LogQL 오류, 잘못된 `cursor`, `log_level`, 라벨 값
    * 502: Loki 응답 오류
    * 504: Loki 연결 실패
    * 503: `LOKI_URL` 미설정
    """
    _require_loki()

    logql = _scoped(
        '{job="loki.source.journal"}', cluster=cluster, node=node, level=_level(log_level),
    )

    warnings: list[str] = []
    entries, pagination = await _query_page(logql, start, end, limit, direction, cursor)

    if not entries:
        warnings.append("NO_NODE_LOGS")

    return NodeLogResponse(
        status="partial" if warnings else "success",
        cluster=cluster,
        node=node,
        data=entries,
        pagination=pagination,
        warnings=warnings,
    )


@router.get(
    "/logs/clusters/{cluster}/pods/{namespace}/{pod}/logs",
    summary="Pod 로그",
    response_model=PodLogResponse,
)
async def pod_logs(
    request: Request,
    cluster: str,
    namespace: str,
    pod: str,
    start: Optional[str] = Query(None, description="시작 시각, ISO 8601 형식 (기본 1시간 전)"),
    end: Optional[str] = Query(None, description="종료 시각, ISO 8601 형식 (기본 현재 시각)"),
    limit: int = Query(100, ge=1, le=5000, description="한 번에 받을 개수 (1 ~ 5000, 기본 100)"),
    direction: str = Query("backward", pattern="^(forward|backward)$", description="정렬 방향, `backward` 는 최신 로그부터 (기본 `backward`)"),
    cursor: Optional[str] = Query(None, description="다음 페이지 커서, 직전 응답의 `pagination.next_cursor` 값, 형식 `<나노초>:<건수>`"),
    log_level: Optional[str] = Query(None, description="로그 레벨 (`error` | `warning` | `info` | `debug`, 기본 전체)"),
    container: Optional[str] = Query(None, description="컨테이너 이름, 예 `nova-api` (기본 Pod 의 모든 컨테이너)"),
):
    """Pod 한 개의 컨테이너 로그 조회

    입력 예시

    * `GET /api/v2/logs/clusters/mgmt/pods/openstack/nova-api-0/logs`
    * `GET /api/v2/logs/clusters/mgmt/pods/openstack/nova-api-0/logs?container=nova-api&log_level=error&limit=50`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `mgmt` (경로, 필수)
    * `namespace`: 네임스페이스, 예 `openstack` (경로, 필수)
    * `pod`: Pod 이름, 예 `nova-api-0` (경로, 필수)
    * `start`: 시작 시각, ISO 8601 형식 (선택, 기본 1시간 전)
    * `end`: 종료 시각, ISO 8601 형식 (선택, 기본 현재 시각)
    * `limit`: 한 번에 받을 개수 `1` ~ `5000` (기본 `100`)
    * `direction`: `backward` | `forward` (기본 `backward`)
    * `cursor`: 다음 페이지 커서, 직전 응답의 `pagination.next_cursor` 값, 형식 `"<나노초>:<건수>"` (선택)
    * `log_level`: `error` | `warning` | `info` | `debug` (선택, 기본 전체)
    * `container`: 컨테이너 이름, 예 `nova-api` (선택, 기본 Pod 의 모든 컨테이너)

    응답

    * `cluster`, `namespace`, `pod`: 클러스터 이름, 네임스페이스, Pod 이름
    * `data`: 로그 목록, 항목마다 `timestamp`, `log_level`, `message`, `labels`, `detected_fields`, `trace_id`, `span_id`
    * `pagination`: `total`, `limit`, `offset`, `has_next`, `next_cursor`
    * `direction` 순서로 정렬, `backward` 는 최신순

    참고

    * `container` 를 지정하면 그 컨테이너 로그만 반환

    오류

    * 400: LogQL 오류, 잘못된 `cursor`, `log_level`, 라벨 값
    * 502: Loki 응답 오류
    * 504: Loki 연결 실패
    * 503: `LOKI_URL` 미설정
    """
    _require_loki()

    logql = _scoped(
        "", cluster=cluster, namespace=namespace, pod=pod, container=container,
        level=_level(log_level),
    )

    entries, pagination = await _query_page(logql, start, end, limit, direction, cursor)

    return PodLogResponse(
        cluster=cluster,
        namespace=namespace,
        pod=pod,
        data=entries,
        pagination=pagination,
    )


@router.get(
    "/logs/clusters/{cluster}/accelerators/{accelerator_id}/logs",
    summary="가속기 드라이버 로그",
    response_model=AcceleratorLogResponse,
)
async def accelerator_logs(
    request: Request,
    cluster: str,
    accelerator_id: str,
    start: Optional[str] = Query(None, description="시작 시각, ISO 8601 형식 (기본 1시간 전)"),
    end: Optional[str] = Query(None, description="종료 시각, ISO 8601 형식 (기본 현재 시각)"),
    limit: int = Query(100, ge=1, le=5000, description="한 번에 받을 개수 (1 ~ 5000, 기본 100)"),
    direction: str = Query("backward", pattern="^(forward|backward)$", description="정렬 방향, `backward` 는 최신 로그부터 (기본 `backward`)"),
    cursor: Optional[str] = Query(None, description="다음 페이지 커서, 직전 응답의 `pagination.next_cursor` 값, 형식 `<나노초>:<건수>`"),
    log_level: Optional[str] = Query(None, description="로그 레벨 (`error` | `warning` | `info` | `debug`, 기본 전체)"),
):
    """가속기 드라이버가 남긴 로그 조회

    입력 예시

    * `GET /api/v2/logs/clusters/furiosa/accelerators/npu0/logs`
    * `GET /api/v2/logs/clusters/furiosa/accelerators/npu0/logs?log_level=error&limit=50`

    입력 옵션

    * `cluster`: 클러스터 이름, 예 `furiosa` (경로, 필수)
    * `accelerator_id`: 가속기 ID, 예 `npu0` (경로, 필수)
    * `start`: 시작 시각, ISO 8601 형식 (선택, 기본 1시간 전)
    * `end`: 종료 시각, ISO 8601 형식 (선택, 기본 현재 시각)
    * `limit`: 한 번에 받을 개수 `1` ~ `5000` (기본 `100`)
    * `direction`: `backward` | `forward` (기본 `backward`)
    * `cursor`: 다음 페이지 커서, 직전 응답의 `pagination.next_cursor` 값, 형식 `"<나노초>:<건수>"` (선택)
    * `log_level`: `error` | `warning` | `info` | `debug` (선택, 기본 전체)

    응답

    * `cluster`, `accelerator_id`: 클러스터 이름, 가속기 ID
    * `node`: 카드가 장착된 노드 이름 (못 찾으면 `null`)
    * `data`: 로그 목록, 항목마다 `timestamp`, `log_level`, `message`, `labels`, `detected_fields`, `trace_id`, `span_id`
    * `pagination`: `total`, `limit`, `offset`, `has_next`, `next_cursor`
    * `direction` 순서로 정렬, `backward` 는 최신순

    경고

    * `ACCELERATOR_NOT_FOUND`: 가속기 ID 로 카드를 찾지 못함
    * `NO_LOG_SOURCE`: 조건에 맞는 로그가 없음

    참고

    * 카드가 장착된 노드의 시스템 로그와 Pod 로그 중 벤더 드라이버 관련 줄만 추출
    * 같은 노드의 다른 카드 로그도 함께 포함
    * 하드웨어 오류(XID), 메모리 오류(ECC), 발열 성능 제한 기록 포함

    오류

    * 404: 가속기가 없는 클러스터
    * 400: 잘못된 `cursor`, `log_level`
    * 502: Loki 응답 오류
    * 504: Loki 연결 실패
    * 503: `LOKI_URL` 미설정
    """
    _require_loki()

    info = await cluster_discovery.get_cluster(cluster)
    if info is None or info.vendor not in _DRIVER_PATTERNS:
        raise HTTPException(status_code=404, detail=f"가속기가 없는 클러스터: {cluster}")
    level = _level(log_level)

    # 카드 ID → 노드. 가속기 목록 API와 같은 메트릭(전력)과 호스트 라벨 순서를 씀
    node = None
    for item in await _instant_metric(info.vendor, cluster, "power", acc_id=accelerator_id):
        node = _card_host(item.get("metric", {}), None)
        if node:
            break
    if node is None:
        return AcceleratorLogResponse(
            status="partial", cluster=cluster, accelerator_id=accelerator_id,
            pagination=LogPagination(total=0, limit=limit), warnings=["ACCELERATOR_NOT_FOUND"],
        )

    logql = _scoped(
        f'{{}} |~ "{_DRIVER_PATTERNS[info.vendor]}"', cluster=cluster, node=node, level=level,
    )
    entries, pagination = await _query_page(logql, start, end, limit, direction, cursor)

    warnings = [] if entries else ["NO_LOG_SOURCE"]
    return AcceleratorLogResponse(
        status="partial" if warnings else "success",
        cluster=cluster,
        accelerator_id=accelerator_id,
        node=node,
        data=entries,
        pagination=pagination,
        warnings=warnings,
    )
