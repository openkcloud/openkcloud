"""미터링(Metering) 라우터 — 사용량, 단가 정책, 비용 예측, 예산.

사용량 조회는 미터링 엔진이 매시 적재한 사용량 레코드(불변)를 읽는다. 비용 = 사용량 x 그날 유효 단가.
사용량, 단가 정책, 예산 경로는 DATABASE_URL 이 없으면 503 METERING_STORE_NOT_CONFIGURED.
월말 예측과 가상 시나리오는 DB 가 없어도 Prometheus 임시 경로와 초기 단가로 동작한다.
"""
import uuid
from dataclasses import replace
from datetime import date, datetime, time, timedelta
from typing import Optional

from fastapi import APIRouter, Depends, HTTPException, Query

from app.schemas.metering import (
    BudgetBody,
    BudgetListResponse,
    BudgetResponse,
    BudgetStatusResponse,
    MonthlyPredictionResponse,
    PolicyBody,
    PolicyListResponse,
    PolicyResponse,
    PolicyUpdate,
    ResourceTypesResponse,
    SimulateRequest,
    SimulateResponse,
    UsageGroupsResponse,
    UsageRecordsResponse,
    UsageSummaryResponse,
    WhatIfRequest,
    WhatIfResponse,
)
from app.services import metering
from app.services.metering import KST, PolicyConflict, PricePolicy, UsageRow, store

router = APIRouter(prefix="/metering")

_PROJECT = Query(None, description="OpenStack 프로젝트 이름(과금 단위). 없으면 전체")
_CLUSTER = Query(None, description="클러스터 이름. 없으면 전체")
_WINDOW = Query(7, ge=1, le=31, description="일평균, 추세 계산 기간(일) (기본 7)")
_CONFIDENCE = Query(90.0, ge=50, le=99.9, description="신뢰구간 수준(%) (기본 90)")


def _doc(extra: str):
    """여러 경로에 같은 설명 단락을 덧붙임. 문자열 덧셈은 docstring 으로 인식되지 않아 데코레이터로 붙임."""

    def deco(fn):
        fn.__doc__ = (fn.__doc__ or "") + extra
        return fn
    return deco


def _require_store() -> None:
    if not store.ready:
        raise HTTPException(status_code=503, detail="METERING_STORE_NOT_CONFIGURED: DATABASE_URL 을 설정하세요")


def _status(warnings: list[str]) -> str:
    return "partial" if warnings else "success"


# ── 사용량 ──

class UsageParams:
    """사용량 조회 공통 조건."""

    def __init__(
        self,
        period: str = Query("current_month", pattern="^(current_month|last_month)$",
                            description="조회 기간 (current_month | last_month, 기본 current_month). start 를 주면 무시"),
        start: Optional[str] = Query(None, description="구간 시작 (ISO 8601 날짜나 시각, 날짜만 주면 KST 0시)"),
        end: Optional[str] = Query(None, description="구간 끝, 미포함 (ISO 8601). 없으면 기간 끝"),
        project: Optional[str] = _PROJECT,
        cluster: Optional[str] = _CLUSTER,
        resource_type: Optional[str] = Query(
            None, pattern="^(accelerator|cpu|memory|storage|network|power)$",
            description="자원 유형 필터 (지금 계측하는 것은 accelerator 뿐). 없으면 전체"),
    ):
        self.period, self.start, self.end = period, start, end
        self.project, self.cluster, self.resource_type = project, cluster, resource_type

    def window(self) -> tuple[datetime, datetime]:
        return _window(self.period, self.start, self.end)


def _window(period: str, start: Optional[str], end: Optional[str]) -> tuple[datetime, datetime]:
    try:
        s, e = metering.resolve_period(period, start, end)
    except ValueError:
        raise HTTPException(status_code=422, detail="start, end 는 ISO 8601 날짜나 시각 (예: 2026-10-01)")
    if s >= e:
        raise HTTPException(status_code=422, detail="start 는 end 보다 앞서야 함")
    return s, e


async def _usage(p: UsageParams, bucket: str = "day") -> tuple[list[UsageRow], dict, list[str], list]:
    _require_store()
    start, end = p.window()
    rows = await store.aggregate(start, end, bucket, p.project, p.cluster, p.resource_type)
    window, warnings = await metering.usage_window(start, end)
    if not rows:
        warnings.append("NO_DATA")
    if any(r.project is None for r in rows):
        warnings.append("NO_PROJECT_MAPPING")
    return rows, window, warnings, await metering.load_policies()


def _groups(rows, policies, key, names, warnings) -> list[dict]:
    items = metering.summarize(rows, policies, key, names)
    if any(i["unpriced_hours"] for i in items):
        warnings.append("PRICE_NOT_DEFINED")
    return items


_USAGE_WARNINGS = """
    경고

    * `NO_DATA`: 구간 안에 사용량 레코드 없음
    * `PARTIAL_HISTORY`: 구간 일부가 계측되지 않음 (`window.metered_hours` < `expected_hours`). 서버가 꺼져 있었거나
      보관 기간(10일) 밖이었거나 그 시각 Prometheus 에 데이터가 없었음. 계측되지 않은 구간은 사용량 0 이 아니라 결측
    * `NO_PROJECT_MAPPING`: 프로젝트 매핑이 없던 사용량 포함 (`project` 가 null, 과금 보류)
    * `PRICE_NOT_DEFINED`: 단가 정책이 없는 사용량 포함. 그 점유 시간은 `unpriced_hours` 로 따로 셈
"""


@router.get("/usage/summary", summary="사용량 요약", response_model=UsageSummaryResponse)
@_doc(_USAGE_WARNINGS)
async def usage_summary(p: UsageParams = Depends()):
    """기간 전체의 점유, 사용, 가중 시간과 유휴 시간, 전력량, 비용 합계

    입력 예시

    * `GET /api/v2/metering/usage/summary`
    * `GET /api/v2/metering/usage/summary?period=last_month&cluster=l40s`

    입력 옵션

    * `period`: `current_month`(이번 달 1일부터 지금까지) | `last_month` (기본 `current_month`)
    * `start`, `end`: 구간 직접 지정, ISO 8601 (선택, 주면 `period` 무시. 날짜만 주면 KST 0시)
    * `project`, `cluster`, `resource_type`: 범위 필터 (선택)

    응답

    * `window`: 구간과 계측된 시간 수
    * `totals`: 점유(청구 기본), 사용, 가중, 유휴 시간, 점유 대비 실사용률(%), 전력량(kWh), 비용(원)

    참고

    * 점유 시간은 카드 사용률 지표가 수집된 분의 비율. 수집이 끊긴 분과 꺼진 VM 에 붙은 카드(지표 없음)는 점유로 세지 않음
    * 프로젝트는 클러스터의 Magnum 매핑, 없으면 카드 호스트와 같은 이름의 VM 프로젝트. 물리 노드에 꽂힌 카드는 `project` null
    """
    rows, window, warnings, policies = await _usage(p)
    items = _groups(rows, policies, lambda r: (), (), warnings)
    totals = items[0] if items else {"allocated_hours": 0.0}
    return UsageSummaryResponse(
        status=_status(warnings), warnings=warnings,
        data={"scope": {"project": p.project, "cluster": p.cluster}, "resource_type": p.resource_type,
              "window": window, "totals": totals})


@router.get("/usage/by-resource", summary="자원 유형별 사용량", response_model=UsageGroupsResponse)
@_doc(_USAGE_WARNINGS)
async def usage_by_resource(p: UsageParams = Depends()):
    """자원 유형, 벤더, 모델별 사용량과 비용

    입력 예시

    * `GET /api/v2/metering/usage/by-resource`

    입력 옵션

    * 사용량 요약과 같음 (`period`, `start`, `end`, `project`, `cluster`, `resource_type`)

    응답

    * `items`: `resource_type`, `vendor`, `model` 별 시간, 전력량, 비용. 비용 큰 순
    """
    rows, window, warnings, policies = await _usage(p)
    items = _groups(rows, policies, lambda r: (r.resource_type, r.vendor, r.model),
                    ("resource_type", "vendor", "model"), warnings)
    items.sort(key=lambda i: -(i["cost_krw"] or 0))
    return UsageGroupsResponse(status=_status(warnings), window=window, items=items, total=len(items),
                               warnings=warnings)


@router.get("/usage/by-project", summary="프로젝트별 사용량", response_model=UsageGroupsResponse)
@_doc(_USAGE_WARNINGS)
async def usage_by_project(p: UsageParams = Depends()):
    """과금 단위인 OpenStack 프로젝트별 사용량과 비용

    입력 예시

    * `GET /api/v2/metering/usage/by-project`

    입력 옵션

    * 사용량 요약과 같음

    응답

    * `items`: `project` 별 시간, 유휴 시간, 실사용률, 전력량, 비용. 비용 큰 순
    * `project` 가 null 인 항목은 매핑이 없어 과금 보류된 사용량

    참고

    * 점유 대비 실사용률이 낮은 프로젝트가 유휴 자원 회수 후보 (회수 판단은 이 API 범위 밖)
    """
    rows, window, warnings, policies = await _usage(p)
    items = _groups(rows, policies, lambda r: (r.project,), ("project",), warnings)
    items.sort(key=lambda i: -(i["cost_krw"] or 0))
    return UsageGroupsResponse(status=_status(warnings), window=window, items=items, total=len(items),
                               warnings=warnings)


@router.get("/usage/by-cluster", summary="클러스터별 사용량", response_model=UsageGroupsResponse)
@_doc(_USAGE_WARNINGS)
async def usage_by_cluster(p: UsageParams = Depends()):
    """클러스터별 사용량과 비용. 클러스터는 조회 구분이고 과금 단위는 프로젝트

    입력 예시

    * `GET /api/v2/metering/usage/by-cluster`

    입력 옵션

    * 사용량 요약과 같음

    응답

    * `items`: `cluster`, `project` 별 시간, 전력량, 비용. 비용 큰 순
    """
    rows, window, warnings, policies = await _usage(p)
    items = _groups(rows, policies, lambda r: (r.cluster, r.project), ("cluster", "project"), warnings)
    items.sort(key=lambda i: -(i["cost_krw"] or 0))
    return UsageGroupsResponse(status=_status(warnings), window=window, items=items, total=len(items),
                               warnings=warnings)


@router.get("/usage/timeseries", summary="사용량 시계열", response_model=UsageGroupsResponse)
@_doc(_USAGE_WARNINGS)
async def usage_timeseries(
    p: UsageParams = Depends(),
    step: str = Query("1d", pattern="^(1h|1d)$", description="구간 간격 (1h | 1d, 기본 1d). 일 경계는 KST"),
):
    """시간 또는 일 단위 사용량과 비용 추이

    입력 예시

    * `GET /api/v2/metering/usage/timeseries`
    * `GET /api/v2/metering/usage/timeseries?step=1h&start=2026-10-22&end=2026-10-23`

    입력 옵션

    * 사용량 요약과 같음, 그리고 `step`: `1h` | `1d` (기본 `1d`)

    응답

    * `items`: 구간 시작 `time`(KST) 별 시간, 전력량, 비용. 시각순
    * 레코드가 없는 구간은 항목이 없음 (0 으로 채우지 않음)
    """
    bucket = "hour" if step == "1h" else "day"
    rows, window, warnings, policies = await _usage(p, bucket)

    def at(r: UsageRow) -> str:
        return (r.bucket or datetime.combine(r.day, time(), KST)).isoformat()

    items = _groups(rows, policies, lambda r: (at(r),), ("time",), warnings)
    items.sort(key=lambda i: i["time"])
    return UsageGroupsResponse(status=_status(warnings), window=window, items=items, total=len(items),
                               warnings=warnings)


@router.get("/usage/details", summary="미터링 레코드", response_model=UsageRecordsResponse)
async def usage_details(
    p: UsageParams = Depends(),
    limit: int = Query(100, ge=1, le=1000, description="한 번에 받을 개수 (1 ~ 1000, 기본 100)"),
    offset: int = Query(0, ge=0, description="건너뛸 개수 (기본 0)"),
):
    """자원 1개 x 1시간 단위의 원본 미터링 레코드

    입력 예시

    * `GET /api/v2/metering/usage/details?cluster=l40s&limit=50`

    입력 옵션

    * 사용량 요약과 같음, 그리고 `limit`, `offset`

    응답

    * `items`: 구간 시작, 자원 ID(카드 UUID), 클러스터, 프로젝트, 모델, 노드, 점유, 사용, 가중 시간,
      평균, 최대 사용률, 전력량과 산출 등급, 계측 경고. 최근 구간부터
    * 레코드는 적재 후 바뀌지 않음. 단가가 바뀌어도 사용량은 다시 계산하지 않음

    경고

    * `NO_DATA`, `PARTIAL_HISTORY`: 사용량 요약과 같음
    """
    _require_store()
    start, end = p.window()
    rows, total = await store.records(start, end, p.project, p.cluster, p.resource_type, limit, offset)
    window, warnings = await metering.usage_window(start, end)
    if not total:
        warnings.append("NO_DATA")
    return UsageRecordsResponse(status=_status(warnings), window=window, items=rows, total=total,
                                limit=limit, offset=offset, warnings=warnings)


# ── 단가 정책 ──

_POLICY_RULES = """
    정책 규칙

    * 단가는 덮어쓰지 않고 새 버전을 추가. 같은 자원(유형, 벤더, 모델, 계약 형태)의 끝이 열린 이전 버전은
      새 버전 시작일에 자동 종료
    * 소급 금지: 시작일은 내일(KST) 이후만. 이미 시작한 버전은 수정, 삭제 불가 (지난 비용을 그때 단가로 재현하기 위함)
    * 409 사유: `POLICY_BACKDATED`(시작일이 오늘이거나 그 전), `POLICY_OVERLAP`(같은 자원의 다른 버전과 기간 겹침),
      `POLICY_ALREADY_EFFECTIVE`(이미 시작한 버전), `POLICY_INVALID_PERIOD`(끝이 시작보다 빠름),
      `POLICY_GAP`(이전 버전을 대신하는 버전에 종료일을 넣어 그 뒤 단가가 비게 됨. 수정에서만)
    * 이미 예약된 미래 버전보다 앞에 시작하는 버전은 추가할 수 없음(`POLICY_OVERLAP`). 뒤 버전을 지우고 시작일 순서대로 다시 추가
"""


@router.get("/policies/resource-types", summary="과금 자원 유형", response_model=ResourceTypesResponse)
async def policy_resource_types():
    """단가 정책 입력에 쓰는 자원 유형, 과금 시간 기준, 계약 형태 목록

    입력 예시

    * `GET /api/v2/metering/policies/resource-types`

    응답

    * `resource_types`: 유형 키, 과금 단위, 지금 계측 여부, 설명
    * `billing_units`: `allocated_hours`(점유, 기본) | `usage_hours`(사용) | `weighted_hours`(가중)
    * `contract_types`: `on_demand` | `reserved` | `spot`
    """
    return ResourceTypesResponse(data={
        "resource_types": [{"resource_type": k, "unit": u, "metered": m, "description": d}
                           for k, (u, m, d) in metering.RESOURCE_TYPES.items()],
        "billing_units": [{"key": k, "description": d} for k, d in metering.BILLING_UNITS.items()],
        "contract_types": [{"key": k, "description": d} for k, d in metering.CONTRACT_TYPES.items()],
    })


@router.post("/policies/simulate", summary="단가 변경 시뮬레이션", response_model=SimulateResponse)
@_doc(_USAGE_WARNINGS)
async def simulate_policy(body: SimulateRequest):
    """후보 단가를 지난 사용량에 대입해 지금 단가와 비용 비교. 결과는 저장하지 않음

    입력 예시

    * `POST /api/v2/metering/policies/simulate`
    * 본문 `{"vendor": "nvidia", "model": "L40S", "unit_price_krw": 2000, "period": "last_month"}`

    입력 옵션

    * 후보 단가: `resource_type`, `vendor`, `model`, `billing_unit`, `unit_price_krw`
    * 대상 사용량: `period` 또는 `start`, `end`, 그리고 `project`, `cluster` (선택)

    응답

    * `current_cost_krw`, `simulated_cost_krw`, `difference_krw`, `difference_percent`
    * `by_project`: 프로젝트, 클러스터별 증감. 많이 줄어드는 순

    참고

    * 후보는 대상 구간 전체에 적용. 후보가 맞지 않는 사용량은 지금 단가 그대로
    * 지금 단가가 없던 사용량에 후보가 맞으면 비용이 새로 생겨 증가로 보임
    """
    _require_store()
    start, end = _window(body.period, body.start, body.end)
    rows = await store.aggregate(start, end, "day", body.project, body.cluster)
    window, warnings = await metering.usage_window(start, end)
    policies = await metering.load_policies()
    if not rows:
        warnings.append("NO_DATA")
    if any(r.project is None for r in rows):
        warnings.append("NO_PROJECT_MAPPING")
    if any(metering.cost_of(r, policies)[0] is None for r in rows):
        warnings.append("PRICE_NOT_DEFINED")
    cand = PricePolicy("simulation", "시뮬레이션", body.resource_type, body.vendor, body.model,
                       body.unit_price_krw, date.min, billing_unit=body.billing_unit)
    result = metering.simulate_policy(rows, policies, cand)
    candidate = body.model_dump(include={"resource_type", "vendor", "model", "billing_unit", "unit_price_krw"})
    return SimulateResponse(status=_status(warnings), warnings=warnings,
                            data={"window": window, "candidate": candidate, **result})


@router.get("/policies", summary="단가 정책 목록", response_model=PolicyListResponse)
async def list_policies():
    """단가 정책 전체 버전 조회 (지난 버전 포함)

    입력 예시

    * `GET /api/v2/metering/policies`

    응답

    * `items`: 정책 ID, 이름, 자원 유형, 벤더, 모델, 계약 형태, 과금 시간 기준, 단가, 유효 구간, 근거.
      자원 유형, 벤더, 모델, 시작일 순

    참고

    * 처음 기동할 때 초기 단가 4건을 넣음. GPU 는 시장 온디맨드 중앙값 x 조회일 환율,
      NPU 는 공개 단가가 없어 제조사가 비교 대상으로 삼은 GPU(RNGD는 L40S, ATOM은 T4) 값을 대입한 임시값
    * 근거는 각 정책의 `source`
    """
    _require_store()
    rows = await store.policy_rows()
    return PolicyListResponse(items=rows, total=len(rows))


@router.post("/policies", summary="단가 정책 추가", response_model=PolicyResponse, status_code=201)
@_doc(_POLICY_RULES)
async def create_policy(body: PolicyBody):
    """단가 정책 새 버전 추가

    입력 예시

    * `POST /api/v2/metering/policies`
    * 본문 `{"name": "L40S 11월 단가", "vendor": "nvidia", "model": "L40S", "unit_price_krw": 2200, "effective_from": "2026-11-01"}`

    입력 옵션

    * `name`, `unit_price_krw` (필수)
    * `resource_type`(기본 `accelerator`), `vendor`, `model`, `contract_type`(기본 `on_demand`),
      `billing_unit`(기본 `allocated_hours`), `effective_from`(없으면 내일), `effective_to`, `source`
    """
    _require_store()
    today = metering.today_kst()
    p = PricePolicy(f"price-{uuid.uuid4().hex[:6]}", body.name, body.resource_type, body.vendor, body.model,
                    body.unit_price_krw, body.effective_from or today + timedelta(days=1), body.effective_to,
                    body.contract_type, body.billing_unit, body.source)
    try:
        close = metering.check_new_policy(p, await store.list_policies(), today)
    except PolicyConflict as exc:
        raise HTTPException(status_code=409, detail=str(exc))
    return PolicyResponse(data=await store.add_policy_version(p, close))


async def _future_policy(policy_id: str) -> PricePolicy:
    row = await store.get_policy_row(policy_id)
    if not row:
        raise HTTPException(status_code=404, detail="POLICY_NOT_FOUND")
    p = metering.to_policy(row)
    if p.effective_from <= metering.today_kst():
        raise HTTPException(status_code=409, detail="POLICY_ALREADY_EFFECTIVE")
    return p


@router.get("/policies/{policy_id}", summary="단가 정책 상세", response_model=PolicyResponse)
async def get_policy(policy_id: str):
    """단가 정책 1개 버전 조회

    입력 예시

    * `GET /api/v2/metering/policies/price-l40s-v1`
    """
    _require_store()
    row = await store.get_policy_row(policy_id)
    if not row:
        raise HTTPException(status_code=404, detail="POLICY_NOT_FOUND")
    return PolicyResponse(data=row)


@router.put("/policies/{policy_id}", summary="단가 정책 수정", response_model=PolicyResponse)
@_doc(_POLICY_RULES)
async def update_policy(policy_id: str, body: PolicyUpdate):
    """아직 시작 전인 버전의 이름, 과금 시간 기준, 단가, 종료일, 근거를 통째로 교체

    입력 예시

    * `PUT /api/v2/metering/policies/price-1a2b3c`
    * 본문 `{"name": "L40S 11월 단가", "unit_price_krw": 2100}`

    입력 옵션

    * `name`, `unit_price_krw` (필수), `billing_unit`, `effective_to`, `source`. 보내지 않은 선택 항목은 기본값으로 바뀜
    * 대상 자원과 시작일은 못 바꿈. 바꾸려면 삭제 후 새로 추가
    * 이전 버전을 대신하는 버전에 종료일을 넣으려면 그 날 시작하는 다음 버전이 먼저 있어야 함 (없으면 `POLICY_GAP`)
    """
    _require_store()
    cur = await _future_policy(policy_id)
    new = replace(cur, name=body.name, billing_unit=body.billing_unit, unit_price_krw=body.unit_price_krw,
                  effective_to=body.effective_to, source=body.source)
    rows = await store.policy_rows()
    existing = [metering.to_policy(r) for r in rows]
    try:
        metering.check_new_policy(new, existing, metering.today_kst())
    except PolicyConflict as exc:
        raise HTTPException(status_code=409, detail=str(exc))
    if metering.leaves_gap(new, existing, {r["id"] for r in rows if r.get("closed_by") == policy_id}):
        raise HTTPException(status_code=409, detail="POLICY_GAP")
    return PolicyResponse(data=await store.update_policy(new))


@router.delete("/policies/{policy_id}", summary="단가 정책 삭제", status_code=204)
@_doc(_POLICY_RULES)
async def delete_policy(policy_id: str):
    """아직 시작 전인 버전 삭제. 이 버전 때문에 닫혔던 이전 버전은 다시 열림

    입력 예시

    * `DELETE /api/v2/metering/policies/price-1a2b3c`
    """
    _require_store()
    await store.delete_policy(await _future_policy(policy_id))


# ── 비용 예측 ──

@router.get("/predictions/monthly", summary="월말 비용 예측", response_model=MonthlyPredictionResponse)
async def predict_monthly(
    project: Optional[str] = _PROJECT,
    cluster: Optional[str] = _CLUSTER,
    method: str = Query("moving_average", pattern="^(moving_average|linear_trend)$",
                        description="예측 방법 (moving_average | linear_trend, 기본 moving_average)"),
    window_days: int = _WINDOW,
    confidence_percent: float = _CONFIDENCE,
):
    """이번 달 누적 가속기 비용과 월말 예상 비용 조회

    입력 예시

    * `GET /api/v2/metering/predictions/monthly`
    * `GET /api/v2/metering/predictions/monthly?cluster=l40s&method=linear_trend`

    입력 옵션

    * `project`: OpenStack 프로젝트 이름 (선택, 없으면 전체)
    * `cluster`: 클러스터 이름, 예 `l40s` (선택, 없으면 전체)
    * `method`: `moving_average`(최근 일평균 x 남은 일수) | `linear_trend`(최근 추세 직선 연장, 3일 미만이면 이동 평균)
    * `window_days`: 일평균, 추세 계산 기간(일) (기본 `7`)
    * `confidence_percent`: 신뢰구간 수준, % (기본 `90`)

    응답

    * `current`: 지난 일수, 남은 일수, 이번 달 누적 비용, 최근 일평균 비용
    * `predicted`: 월말 예상 비용과 신뢰구간. 단가 있는 사용량이 없으면 `null`. 신뢰구간은 완결된 날이 1일뿐이면 `null`
    * `budget`: 같은 범위 예산이 있으면 소진율(누적 기준)과 초과 예상액(예측 기준)
    * `breakdown`: 클러스터, 가속기 모델별 점유 시간, 적용 단가, 누적과 월말 비용

    경고

    * `NO_DATA`: 범위 안에 가속기 사용량 없음
    * `PRICE_NOT_DEFINED`: 단가 정책이 없는 가속기가 있어 그 비용은 빠짐
    * `PARTIAL_HISTORY`: 이번 달 지난 날 중 하루 통째로 계측하지 못한 날이 있음 (`current.missing_days`).
      그날은 누적에서 빠지고 예측에서 일평균으로 채움
    * `UPSTREAM_ERROR`: 임시 경로에서 일부 벤더의 Prometheus 조회 실패. 그 벤더 비용은 빠짐
    * `INSUFFICIENT_HISTORY`: 완결된 날이 없어 예측 불가
    * `TREND_FALLBACK_MOVING_AVERAGE`: 추세 계산에 쓸 날이 3일 미만이라 이동 평균으로 계산
    * `NO_PROJECT_MAPPING`: 클러스터와 OpenStack 프로젝트 매핑이 없어 project 로 거를 수 없음
    * `METERING_STORE_UNAVAILABLE`: 저장소 장애. 사용량은 Prometheus 임시 경로, 단가는 초기 단가로 계산했거나
      예산 판정을 하지 못함

    참고

    * 사용량은 미터링 레코드(DB 가 있고 계측 이력이 있을 때) 또는 Prometheus 에서 1시간마다 센 카드 장수(임시 경로)
    * 비용 = 사용량 x 그날 유효 단가. 과금 월과 일의 경계는 한국 시간(KST). 지금은 가속기 비용만 포함
    """
    data, warnings = await metering.predict_monthly(
        project, cluster, method, window_days, confidence_percent,
        save_history=(window_days == 7 and confidence_percent == 90.0))
    return MonthlyPredictionResponse(status=_status(warnings), data=data, warnings=warnings)


@router.get("/predictions/budget-status", summary="예산 대비 현황", response_model=BudgetStatusResponse)
async def budget_status(
    project: Optional[str] = _PROJECT,
    cluster: Optional[str] = _CLUSTER,
    window_days: int = _WINDOW,
    confidence_percent: float = _CONFIDENCE,
):
    """이번 달에 적용되는 예산마다 소진율과 초과 예상 판정

    입력 예시

    * `GET /api/v2/metering/predictions/budget-status`
    * `GET /api/v2/metering/predictions/budget-status?cluster=l40s`

    입력 옵션

    * `project`, `cluster`: 이 범위의 예산만 (선택, 없으면 모든 예산)
    * `window_days`: 월말 예측의 일평균 계산 기간(일) (기본 `7`)
    * `confidence_percent`: 신뢰구간 수준, % (기본 `90`)

    응답

    * `items`: 예산별 누적 비용, 월말 예측 비용, 소진율(누적 기준), 월말 예측 비율, 초과 예상액, 판정
    * 판정: `exceeded`(누적이 예산 이상) > `forecast_exceeded`(월말 예측이 예산 초과) > `warning`(소진율이 경고 기준 이상) > `ok`

    경고

    * `NO_BUDGET`: 이번 달에 적용되는 예산 없음
    * 그 밖의 경고는 월말 비용 예측과 같음

    참고

    * 소진율과 초과 예상액은 기준이 다름. 알림은 두 값을 따로 임계로 둠
    * 알림 발송은 알람 기능 담당. 여기서는 판정만 제공
    """
    _require_store()
    items, warnings = await metering.budget_statuses(project, cluster, window_days, confidence_percent)
    return BudgetStatusResponse(status=_status(warnings), items=items, total=len(items), warnings=warnings)


@router.post("/predictions/what-if", summary="가상 시나리오 비용 계산", response_model=WhatIfResponse)
async def what_if(body: WhatIfRequest):
    """남은 기간 동안 가속기 장수나 사용 시간을 바꾸면 월말 비용이 얼마가 되는지 계산

    입력 예시

    * `POST /api/v2/metering/predictions/what-if`
    * 본문 `{"cluster": "l40s", "changes": [{"vendor": "nvidia", "model": "L40S", "count": 1, "hours_per_day": 8}]}`
      (업무 시간에만 L40S 1장 기동)

    입력 옵션

    * `project`, `cluster`: 계산 범위 (선택, 없으면 전체)
    * `window_days`: 기준선 일평균 계산 기간(일) (기본 `7`)
    * `changes`: 바꿀 구성 목록. 항목마다 `vendor`, `model`, 장수 `count`, 하루 점유 시간 `hours_per_day`,
      선택 `cluster`. 같은 모델의 기존 항목을 대체하고, 맞는 항목이 없으면 새 자원 추가로 계산

    응답

    * `baseline_month_end_cost_krw`: 지금 추세(이동 평균) 그대로일 때 월말 비용
    * `scenario_month_end_cost_krw`: 변경을 남은 기간에 적용한 월말 비용
    * `difference_krw`, `savings_krw`: 차이와 절감액
    * `changes`: 변경별 적용 단가, 바꾼 뒤 하루 비용, 대체한 기존 비용

    경고

    * `PRICE_NOT_DEFINED`: 단가가 없는 변경은 계산에서 빠짐
    * 그 밖의 경고는 월말 비용 예측과 같음

    참고

    * 입력 규격은 초안이라 바뀔 수 있음
    * 기술 적합성(메모리, 성능)은 판단하지 않음. 비용만 계산
    """
    pred, warnings = await metering.predict_monthly(body.project, body.cluster, "moving_average",
                                                    body.window_days, 90.0)
    result, w = metering.what_if(pred, [c.model_dump() for c in body.changes], metering.today_kst(),
                                 await metering.load_policies())
    warnings += [x for x in w if x not in warnings]
    return WhatIfResponse(status=_status(warnings),
                          data={"scope": pred["scope"], "month": pred["month"], **result}, warnings=warnings)


# ── 예산 ──

@router.get("/budgets", summary="예산 목록", response_model=BudgetListResponse)
async def list_budgets():
    """예산 전체 조회

    입력 예시

    * `GET /api/v2/metering/budgets`

    응답

    * `items`: 예산 ID, 이름, 범위(project, cluster), 적용 월(null 이면 매월), 금액, 경고 기준
    """
    _require_store()
    rows = await store.list_budgets()
    return BudgetListResponse(items=rows, total=len(rows))


@router.post("/budgets", summary="예산 생성", response_model=BudgetResponse, status_code=201)
async def create_budget(body: BudgetBody):
    """예산 생성

    입력 예시

    * `POST /api/v2/metering/budgets`
    * 본문 `{"name": "L40S 월 예산", "cluster": "l40s", "amount_krw": 2000000, "alert_percent": 80}`

    입력 옵션

    * `name`: 예산 이름 (필수)
    * `project`, `cluster`: 범위 (선택, 둘 다 없으면 전체)
    * `month`: 적용 월 `YYYY-MM` (선택, 없으면 매월)
    * `amount_krw`: 예산 금액, 원 (필수)
    * `alert_percent`: 누적 소진율 경고 기준, % (기본 `80`)

    참고

    * 같은 범위에 그 달 예산과 매월 예산이 함께 있으면 그 달 예산을 씀
    """
    _require_store()
    return BudgetResponse(data=await store.create_budget(body.model_dump()))


@router.get("/budgets/{budget_id}", summary="예산 상세", response_model=BudgetResponse)
async def get_budget(budget_id: str):
    """예산 1건 조회

    입력 예시

    * `GET /api/v2/metering/budgets/bgt-1a2b3c`
    """
    _require_store()
    row = await store.get_budget(budget_id)
    if not row:
        raise HTTPException(status_code=404, detail="BUDGET_NOT_FOUND")
    return BudgetResponse(data=row)


@router.put("/budgets/{budget_id}", summary="예산 수정", response_model=BudgetResponse)
async def replace_budget(budget_id: str, body: BudgetBody):
    """예산 통째로 교체

    입력 예시

    * `PUT /api/v2/metering/budgets/bgt-1a2b3c`
    * 본문은 예산 생성과 같음. 보내지 않은 선택 항목은 비워짐
    """
    _require_store()
    row = await store.replace_budget(budget_id, body.model_dump())
    if not row:
        raise HTTPException(status_code=404, detail="BUDGET_NOT_FOUND")
    return BudgetResponse(data=row)


@router.delete("/budgets/{budget_id}", summary="예산 삭제", status_code=204)
async def delete_budget(budget_id: str):
    """예산 삭제

    입력 예시

    * `DELETE /api/v2/metering/budgets/bgt-1a2b3c`
    """
    _require_store()
    if not await store.delete_budget(budget_id):
        raise HTTPException(status_code=404, detail="BUDGET_NOT_FOUND")
