"""
미터링(Metering) API v2 Pydantic 스키마. 사용량, 단가 정책, 비용 예측, 예산.

청구 기본 단위는 가속기 점유 시간(allocated hours). 금액은 원(_krw), 과금 월과 일의 경계는 한국 시간(KST).
예산 판정은 현재 누적 기준(utilization_percent)과 월말 예측 기준(estimated_overrun_krw)을 따로 둔다.
"""
from datetime import date, datetime
from typing import Any, Literal, Optional

from pydantic import BaseModel, Field

from app.schemas._common import DESC_OBSERVED_AT, DESC_STATUS, DESC_TOTAL, DESC_WARNINGS, _now

Vendor = Literal["nvidia", "furiosa", "rebellions"]
ResourceType = Literal["accelerator", "cpu", "memory", "storage", "network", "power"]
BillingUnit = Literal["allocated_hours", "usage_hours", "weighted_hours"]
ContractType = Literal["on_demand", "reserved", "spot"]
BudgetState = Literal["ok", "warning", "forecast_exceeded", "exceeded", "unknown"]
DESC_BUDGET_STATE = (
    "예산 판정 (ok | warning | forecast_exceeded | exceeded | unknown). "
    "exceeded: 누적이 예산 이상, forecast_exceeded: 월말 예측이 예산 초과, "
    "warning: 누적 소진율이 alert_percent 이상, unknown: 단가 없음 등으로 누적 비용 미계산"
)


class Scope(BaseModel):
    """조회 범위. 둘 다 null 이면 전체."""

    project: Optional[str] = Field(None, description="OpenStack 프로젝트 이름(과금 단위). null 이면 전체")
    cluster: Optional[str] = Field(None, description="클러스터 이름. null 이면 전체")


class PriceInfo(BaseModel):
    """적용한 단가 정책."""

    policy_id: str = Field(..., description="단가 정책 ID")
    name: str = Field("", description="정책 이름")
    resource_type: str = Field("accelerator", description="자원 유형")
    vendor: str = Field(..., description="가속기 벤더 (nvidia | furiosa | rebellions). 빈 문자열이면 벤더 무관")
    model: str = Field(..., description="적용 모델 이름 조각 (예: L40S). 빈 문자열이면 벤더 전체")
    contract_type: str = Field("on_demand", description="계약 형태 (on_demand | reserved | spot)")
    billing_unit: str = Field("allocated_hours", description="과금 시간 기준 (allocated_hours | usage_hours | weighted_hours)")
    unit_price_krw: float = Field(..., description="billing_unit 1단위 단가(원). 가속기는 1장·1시간")
    effective_from: str = Field(..., description="유효 시작일 (YYYY-MM-DD)")
    effective_to: Optional[str] = Field(None, description="이 날 전까지 유효 (YYYY-MM-DD). null 이면 계속")
    source: str = Field("", description="단가 근거와 조사 시점")


class CostItem(BaseModel):
    """클러스터 x 가속기 모델 하나의 이번 달 비용."""

    cluster: str = Field(..., description="클러스터 이름")
    project: Optional[str] = Field(None, description="OpenStack 프로젝트 이름. 매핑을 못 찾으면 null")
    vendor: str = Field(..., description="가속기 벤더 (nvidia | furiosa | rebellions)")
    model: str = Field(..., description="가속기 모델 이름. 모델 라벨이 없는 벤더는 빈 문자열")
    allocated_hours: float = Field(..., description="이번 달 점유 시간 합계(가속기·시간). 보관 기간 안의 날만")
    price: Optional[PriceInfo] = Field(None, description="가장 최근 날에 적용한 단가 정책. 단가가 없으면 null")
    accumulated_cost_krw: Optional[float] = Field(None, description="이번 달 누적 비용(원). 단가가 없으면 null")
    daily_avg_cost_krw: Optional[float] = Field(None, description="최근 window_days 일평균 비용(원). 단가가 없으면 null")
    projected_cost_krw: Optional[float] = Field(
        None, description="이 항목의 월말 비용(원). 항목별 값은 method 와 무관하게 이동 평균. 단가가 없으면 null"
    )


class PredictionCurrent(BaseModel):
    """지금까지의 실적."""

    date: str = Field(..., description="기준 날짜 (YYYY-MM-DD, KST)")
    days_in_month: int = Field(..., description="이번 달 일수")
    days_elapsed: float = Field(..., description="지난 일수 (소수, KST 0시 기준)")
    days_remaining: float = Field(..., description="남은 일수 (소수)")
    missing_days: int = Field(
        ..., description="보관 기간(10일) 밖이라 사용량을 못 본 이번 달 지난 날 수. 누적에서 빠지고 예측에서 일평균으로 채움"
    )
    history_days: int = Field(..., description="일평균, 추세 계산에 쓴 완결된 날 수")
    accumulated_cost_krw: Optional[float] = Field(None, description="이번 달 누적 비용(원). 단가 있는 사용량이 없으면 null")
    daily_avg_cost_krw: Optional[float] = Field(None, description="최근 history_days 일평균 비용(원)")


class ConfidenceInterval(BaseModel):
    low_krw: float = Field(..., description="월말 비용 하한(원)")
    high_krw: float = Field(..., description="월말 비용 상한(원)")


class PredictedCost(BaseModel):
    """월말 예측. 확정 금액이 아니라 남은 기간 사용 패턴 가정에 기댄 추정."""

    month_end_cost_krw: float = Field(..., description="월말 예측 비용(원) = 누적 + 남은 기간 예측")
    confidence_interval: Optional[ConfidenceInterval] = Field(
        None, description="신뢰구간. 일별 비용 변동이 클수록 넓어짐. 완결된 날이 1일뿐이면 변동을 알 수 없어 null")
    confidence_percent: float = Field(..., description="신뢰수준(%)")


class BudgetJudgement(BaseModel):
    """예산 판정. 누적 기준과 예측 기준을 따로 본다."""

    amount_krw: float = Field(..., description="예산(원)")
    alert_percent: float = Field(..., description="누적 소진율 경고 기준(%)")
    utilization_percent: Optional[float] = Field(None, description="예산 소진율(%) = 현재 누적 / 예산. 월말 예측이 아님")
    forecast_percent: Optional[float] = Field(None, description="월말 예측 / 예산(%)")
    estimated_overrun_krw: Optional[float] = Field(None, description="월말 예측 기준 초과 예상액(원). 초과하지 않으면 0")
    status: BudgetState = Field(..., description=DESC_BUDGET_STATE)


class PredictionBudget(BudgetJudgement):
    budget_id: str = Field(..., description="예산 ID")
    name: str = Field(..., description="예산 이름")


class MonthlyPrediction(BaseModel):
    """이번 달 월말 비용 예측."""

    scope: Scope = Field(..., description="조회 범위")
    month: str = Field(..., description="대상 월 (YYYY-MM, KST)")
    method: str = Field(..., description="실제 쓴 예측 방법 (moving_average | linear_trend)")
    included_resource_types: list[str] = Field(
        ..., description="비용에 넣은 자원 유형. 지금은 accelerator 만. CPU, 메모리, 스토리지, 전력은 미터링 엔진 이후"
    )
    current: PredictionCurrent = Field(..., description="지금까지의 실적")
    predicted: Optional[PredictedCost] = Field(None, description="월말 예측. 단가 있는 사용량이나 이력이 없으면 null")
    budget: Optional[PredictionBudget] = Field(None, description="같은 범위의 이번 달 예산 판정. 예산이 없거나 저장소 미설정이면 null")
    breakdown: list[CostItem] = Field(default_factory=list, description="클러스터, 벤더, 모델 순으로 정렬한 항목별 비용")


class MonthlyPredictionResponse(BaseModel):
    status: str = Field(..., description=DESC_STATUS)
    data: MonthlyPrediction = Field(..., description="월말 비용 예측")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field(default_factory=list, description=DESC_WARNINGS)


class BudgetStatusItem(BudgetJudgement):
    """예산 1건의 현황."""

    budget_id: str = Field(..., description="예산 ID")
    name: str = Field(..., description="예산 이름")
    scope: Scope = Field(..., description="예산 범위")
    month: str = Field(..., description="대상 월 (YYYY-MM, KST)")
    accumulated_cost_krw: Optional[float] = Field(None, description="이번 달 누적 비용(원)")
    predicted_month_end_cost_krw: Optional[float] = Field(None, description="월말 예측 비용(원), 이동 평균")


class BudgetStatusResponse(BaseModel):
    status: str = Field(..., description=DESC_STATUS)
    items: list[BudgetStatusItem] = Field(default_factory=list, description="예산별 현황")
    total: int = Field(0, description=DESC_TOTAL)
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field(default_factory=list, description=DESC_WARNINGS)


class WhatIfChange(BaseModel):
    """남은 기간에 바꿀 가속기 구성 1건."""

    vendor: Vendor = Field(..., description="가속기 벤더 (nvidia | furiosa | rebellions)")
    model: str = Field(..., min_length=1, description="가속기 모델 이름 조각 (예: L40S). 단가 정책과 기존 항목을 이 글자로 찾음 "
                                                          "(단어 단위라 A10 은 A100 에 맞지 않음)")
    cluster: Optional[str] = Field(None, description="이 클러스터 항목만 바꿈. 없으면 범위 안 같은 모델 전체")
    count: int = Field(..., ge=0, description="바꾼 뒤 장수. 0 이면 반납")
    hours_per_day: float = Field(..., ge=0, le=24, description="바꾼 뒤 하루 점유 시간 (0 ~ 24)")


class WhatIfRequest(BaseModel):
    """가상 시나리오 요청. 입력 규격은 초안이라 바뀔 수 있음."""

    project: Optional[str] = Field(None, description="OpenStack 프로젝트 이름. 없으면 전체")
    cluster: Optional[str] = Field(None, description="클러스터 이름. 없으면 전체")
    window_days: int = Field(7, ge=1, le=31, description="기준선 일평균 계산 기간(일)")
    changes: list[WhatIfChange] = Field(..., min_length=1, description="바꿀 구성 목록")


class WhatIfChangeResult(WhatIfChange):
    price: Optional[PriceInfo] = Field(None, description="적용 단가 정책. 단가가 없으면 null 이고 이 변경은 계산에서 빠짐")
    scenario_daily_cost_krw: Optional[float] = Field(None, description="바꾼 뒤 하루 비용(원) = 장수 x 시간 x 단가")
    replaced_daily_cost_krw: float = Field(..., description="대체되는 기존 항목들의 일평균 비용(원)")
    replaced_items: int = Field(..., description="대체되는 기존 항목 수. 0 이면 새 자원 추가")


class WhatIfResult(BaseModel):
    scope: Scope = Field(..., description="조회 범위")
    month: str = Field(..., description="대상 월 (YYYY-MM, KST)")
    days_remaining: float = Field(..., description="변경이 적용되는 남은 일수")
    baseline_month_end_cost_krw: float = Field(..., description="지금 추세(이동 평균) 그대로일 때 월말 비용(원)")
    scenario_month_end_cost_krw: float = Field(..., description="변경을 적용했을 때 월말 비용(원)")
    difference_krw: float = Field(..., description="시나리오 - 기준선(원). 음수면 절감")
    savings_krw: float = Field(..., description="절감액(원). 늘어나면 0")
    changes: list[WhatIfChangeResult] = Field(default_factory=list, description="변경별 계산 내역")


class WhatIfResponse(BaseModel):
    status: str = Field(..., description=DESC_STATUS)
    data: WhatIfResult = Field(..., description="가상 시나리오 결과")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field(default_factory=list, description=DESC_WARNINGS)


class BudgetBody(BaseModel):
    """예산 생성, 교체 요청."""

    name: str = Field(..., min_length=1, description="예산 이름")
    project: Optional[str] = Field(None, description="OpenStack 프로젝트 이름. 없으면 전체")
    cluster: Optional[str] = Field(None, description="클러스터 이름. 없으면 전체")
    month: Optional[str] = Field(
        None, pattern=r"^\d{4}-(0[1-9]|1[0-2])$",
        description="적용 월 (YYYY-MM). 없으면 매월. 같은 범위에 그 달 예산이 있으면 매월 예산보다 우선",
    )
    amount_krw: float = Field(..., gt=0, description="예산(원)")
    alert_percent: float = Field(80, gt=0, le=100, description="누적 소진율 경고 기준(%) (기본 80)")


class Budget(BudgetBody):
    id: str = Field(..., description="예산 ID")
    created_at: datetime = Field(..., description="생성 시각 (ISO 8601, UTC)")
    updated_at: datetime = Field(..., description="마지막 수정 시각 (ISO 8601, UTC)")


class BudgetResponse(BaseModel):
    status: str = Field("success", description=DESC_STATUS)
    data: Budget = Field(..., description="예산")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field(default_factory=list, description=DESC_WARNINGS)


class BudgetListResponse(BaseModel):
    status: str = Field("success", description=DESC_STATUS)
    items: list[Budget] = Field(default_factory=list, description="예산 목록")
    total: int = Field(0, description=DESC_TOTAL)
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field(default_factory=list, description=DESC_WARNINGS)


# ── 사용량 ──

class UsageWindow(BaseModel):
    """조회 구간과 계측 이력."""

    start: str = Field(..., description="구간 시작 (ISO 8601)")
    end: str = Field(..., description="구간 끝, 미포함 (ISO 8601)")
    metered_hours: int = Field(..., description="구간 안에서 계측을 마친 1시간 구간 수")
    expected_hours: int = Field(..., description="구간 안의 완결된 1시간 구간 수. metered_hours 가 작으면 그만큼 결측")


class UsageMetrics(BaseModel):
    """시간, 전력, 비용 합계. 값을 구하지 못한 항목은 0 이 아니라 null."""

    allocated_hours: float = Field(..., description="점유 시간 합계(가속기·시간). 청구 기본 단위")
    usage_hours: Optional[float] = Field(None, description="사용 시간 합계. 사용률이 0보다 큰 시간")
    weighted_hours: Optional[float] = Field(None, description="사용률 가중 시간 합계 = 사용 시간 x 평균 사용률")
    idle_hours: Optional[float] = Field(None, description="유휴 시간 = 점유 - 사용. 붙잡고 쓰지 않은 시간")
    utilization_percent: Optional[float] = Field(None, description="점유 대비 실사용률(%) = 가중 / 점유 x 100")
    power_kwh: Optional[float] = Field(None, description="가속기 전력량(kWh). 카드 드라이버 실측(direct)")
    cost_krw: Optional[float] = Field(None, description="비용(원) = 사용량 x 그날 유효 단가. 단가 있는 사용량이 없으면 null")
    unpriced_hours: float = Field(0, description="단가 정책이 없어 비용에서 빠진 점유 시간")


class UsageGroup(UsageMetrics):
    """묶음 1개의 합계. 묶음 기준에 해당하는 키만 채워짐."""

    project: Optional[str] = Field(None, description="OpenStack 프로젝트 이름. 매핑이 없으면 null")
    cluster: Optional[str] = Field(None, description="클러스터 이름")
    resource_type: Optional[str] = Field(None, description="자원 유형")
    vendor: Optional[str] = Field(None, description="가속기 벤더")
    model: Optional[str] = Field(None, description="가속기 모델")
    time: Optional[str] = Field(None, description="시계열 구간 시작 (ISO 8601, KST)")


class UsageSummaryData(BaseModel):
    scope: Scope = Field(..., description="조회 범위")
    resource_type: Optional[str] = Field(None, description="자원 유형 필터. null 이면 전체")
    window: UsageWindow = Field(..., description="조회 구간과 계측 이력")
    totals: UsageMetrics = Field(..., description="전체 합계")


class UsageSummaryResponse(BaseModel):
    status: str = Field(..., description=DESC_STATUS)
    data: UsageSummaryData = Field(..., description="사용량 요약")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field(default_factory=list, description=DESC_WARNINGS)


class UsageGroupsResponse(BaseModel):
    status: str = Field(..., description=DESC_STATUS)
    window: UsageWindow = Field(..., description="조회 구간과 계측 이력")
    items: list[UsageGroup] = Field(default_factory=list, description="묶음별 합계")
    total: int = Field(0, description=DESC_TOTAL)
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field(default_factory=list, description=DESC_WARNINGS)


class UsageRecord(BaseModel):
    """미터링 레코드. 자원 1개 x 1시간. 적재 후 바뀌지 않음."""

    record_id: str = Field(..., description="레코드 ID (meter-<KST 시각>-<자원 ID>)")
    ts: datetime = Field(..., description="1시간 구간 시작 (ISO 8601)")
    resource_type: str = Field(..., description="자원 유형")
    resource_id: str = Field(..., description="자원 ID. 가속기는 카드 UUID")
    cluster: str = Field(..., description="클러스터 이름")
    project: Optional[str] = Field(None, description="OpenStack 프로젝트. 계측 시점에 매핑이 없었으면 null(과금 보류)")
    vendor: str = Field(..., description="가속기 벤더")
    model: str = Field(..., description="가속기 모델")
    node: Optional[str] = Field(None, description="노드(호스트) 이름")
    allocated_hours: float = Field(..., description="점유 시간 (0 ~ 1)")
    usage_hours: Optional[float] = Field(None, description="사용 시간 (0 ~ 1)")
    weighted_hours: Optional[float] = Field(None, description="사용률 가중 시간 (0 ~ 1)")
    avg_utilization_percent: Optional[float] = Field(None, description="구간 평균 사용률(%)")
    peak_utilization_percent: Optional[float] = Field(None, description="구간 최대 사용률(%)")
    power_kwh: Optional[float] = Field(None, description="전력량(kWh)")
    power_estimation: Optional[str] = Field(None, description="전력 산출 등급 (direct: 실측 | attributed: 귀속 | proportional: 비례 추정)")
    warnings: list[str] = Field(default_factory=list, description="계측 시 경고 (NO_PROJECT_MAPPING, NO_POWER_DATA)")
    created_at: datetime = Field(..., description="적재 시각")


class UsageRecordsResponse(BaseModel):
    status: str = Field(..., description=DESC_STATUS)
    window: UsageWindow = Field(..., description="조회 구간과 계측 이력")
    items: list[UsageRecord] = Field(default_factory=list, description="레코드 목록, 최근 구간부터")
    total: int = Field(0, description=DESC_TOTAL)
    limit: int = Field(..., description="페이지 크기")
    offset: int = Field(..., description="건너뛴 개수")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field(default_factory=list, description=DESC_WARNINGS)


# ── 단가 정책 ──

_DESC_BILLING_UNIT = (
    "과금 시간 기준 (allocated_hours | usage_hours | weighted_hours). "
    "allocated_hours: 점유 시간(기본), usage_hours: 사용 시간(유휴 미과금), weighted_hours: 사용률 가중 시간"
)


class PolicyBody(BaseModel):
    """단가 정책 새 버전."""

    name: str = Field(..., min_length=1, description="정책 이름")
    resource_type: ResourceType = Field("accelerator", description="자원 유형. 지금 계측하는 것은 accelerator 뿐")
    vendor: Literal["", "nvidia", "furiosa", "rebellions"] = Field(
        "", description="가속기 벤더 (nvidia | furiosa | rebellions). 빈 문자열이면 벤더 무관")
    model: str = Field(
        "", description="모델 이름에 단어로 들어 있는 글자 (예: L40S, 대소문자 무시. A10 은 A100 에 맞지 않음). "
                        "빈 문자열이면 벤더 전체. 여러 정책이 맞으면 긴 쪽 우선")
    contract_type: ContractType = Field(
        "on_demand", description="계약 형태 (on_demand | reserved | spot). 지금 비용 계산은 on_demand 만 사용")
    billing_unit: BillingUnit = Field("allocated_hours", description=_DESC_BILLING_UNIT)
    unit_price_krw: float = Field(..., ge=0, description="billing_unit 1단위 단가(원). 가속기는 1장·1시간")
    effective_from: Optional[date] = Field(
        None, description="유효 시작일 (YYYY-MM-DD, KST). 내일 이후만 허용(소급 금지). 없으면 내일")
    effective_to: Optional[date] = Field(None, description="이 날 전까지 유효 (YYYY-MM-DD). 없으면 계속")
    source: str = Field("", description="단가 근거와 조사 시점")


class PolicyUpdate(BaseModel):
    """아직 시작 전인 버전의 수정. 대상 자원과 시작일은 못 바꿈(바꾸려면 삭제 후 새로 추가)."""

    name: str = Field(..., min_length=1, description="정책 이름")
    billing_unit: BillingUnit = Field("allocated_hours", description=_DESC_BILLING_UNIT)
    unit_price_krw: float = Field(..., ge=0, description="billing_unit 1단위 단가(원)")
    effective_to: Optional[date] = Field(None, description="이 날 전까지 유효. 없으면 계속")
    source: str = Field("", description="단가 근거와 조사 시점")


class Policy(BaseModel):
    """단가 정책 1개 버전."""

    id: str = Field(..., description="정책 ID")
    name: str = Field(..., description="정책 이름")
    resource_type: str = Field(..., description="자원 유형")
    vendor: str = Field(..., description="가속기 벤더. 빈 문자열이면 벤더 무관")
    model: str = Field(..., description="모델 이름 조각. 빈 문자열이면 벤더 전체")
    contract_type: str = Field(..., description="계약 형태 (on_demand | reserved | spot)")
    billing_unit: str = Field(..., description=_DESC_BILLING_UNIT)
    unit_price_krw: float = Field(..., description="billing_unit 1단위 단가(원)")
    effective_from: date = Field(..., description="유효 시작일 (KST)")
    effective_to: Optional[date] = Field(None, description="이 날 전까지 유효. null 이면 계속")
    source: str = Field("", description="단가 근거와 조사 시점")
    created_at: datetime = Field(..., description="생성 시각 (ISO 8601, UTC)")
    updated_at: datetime = Field(..., description="마지막 수정 시각 (ISO 8601, UTC)")


class PolicyResponse(BaseModel):
    status: str = Field("success", description=DESC_STATUS)
    data: Policy = Field(..., description="단가 정책")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field(default_factory=list, description=DESC_WARNINGS)


class PolicyListResponse(BaseModel):
    status: str = Field("success", description=DESC_STATUS)
    items: list[Policy] = Field(default_factory=list, description="단가 정책 목록 (자원 유형, 벤더, 모델, 시작일 순)")
    total: int = Field(0, description=DESC_TOTAL)
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field(default_factory=list, description=DESC_WARNINGS)


class ResourceTypeInfo(BaseModel):
    resource_type: str = Field(..., description="자원 유형 키")
    unit: str = Field(..., description="과금 단위")
    metered: bool = Field(..., description="지금 계측해 비용을 내는지 여부")
    description: str = Field(..., description="설명")


class OptionInfo(BaseModel):
    key: str = Field(..., description="값")
    description: str = Field(..., description="설명")


class ResourceTypesData(BaseModel):
    resource_types: list[ResourceTypeInfo] = Field(..., description="과금 자원 유형")
    billing_units: list[OptionInfo] = Field(..., description="과금 시간 기준")
    contract_types: list[OptionInfo] = Field(..., description="계약 형태")


class ResourceTypesResponse(BaseModel):
    status: str = Field("success", description=DESC_STATUS)
    data: ResourceTypesData = Field(..., description="정책 입력에 쓰는 값 목록")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field(default_factory=list, description=DESC_WARNINGS)


class SimulateRequest(BaseModel):
    """후보 단가로 지난 사용량의 비용을 다시 계산. 결과는 저장하지 않음."""

    resource_type: ResourceType = Field("accelerator", description="자원 유형")
    vendor: Literal["", "nvidia", "furiosa", "rebellions"] = Field("", description="가속기 벤더. 빈 문자열이면 벤더 무관")
    model: str = Field("", description="모델 이름 조각 (예: L40S)")
    billing_unit: BillingUnit = Field("allocated_hours", description=_DESC_BILLING_UNIT)
    unit_price_krw: float = Field(..., ge=0, description="후보 단가(원)")
    period: Literal["current_month", "last_month"] = Field("current_month", description="대상 기간")
    start: Optional[str] = Field(None, description="구간 시작 (ISO 8601 날짜나 시각). 주면 period 대신 사용")
    end: Optional[str] = Field(None, description="구간 끝, 미포함 (ISO 8601)")
    project: Optional[str] = Field(None, description="OpenStack 프로젝트 이름. 없으면 전체")
    cluster: Optional[str] = Field(None, description="클러스터 이름. 없으면 전체")


class SimulateProjectDiff(BaseModel):
    project: Optional[str] = Field(None, description="OpenStack 프로젝트. 매핑이 없으면 null")
    cluster: str = Field(..., description="클러스터 이름")
    current_cost_krw: float = Field(..., description="지금 단가 비용(원)")
    simulated_cost_krw: float = Field(..., description="후보 단가 비용(원)")
    difference_krw: float = Field(..., description="후보 - 지금(원). 음수면 줄어듦")


class SimulateResult(BaseModel):
    window: UsageWindow = Field(..., description="대상 구간과 계측 이력")
    candidate: dict[str, Any] = Field(..., description="대입한 후보 단가")
    current_cost_krw: float = Field(..., description="지금 단가로 계산한 비용(원)")
    simulated_cost_krw: float = Field(..., description="후보 단가를 대입한 비용(원)")
    difference_krw: float = Field(..., description="후보 - 지금(원)")
    difference_percent: Optional[float] = Field(None, description="지금 대비 증감(%). 지금 비용이 0 이면 null")
    by_project: list[SimulateProjectDiff] = Field(default_factory=list, description="프로젝트, 클러스터별 증감. 많이 줄어드는 순")


class SimulateResponse(BaseModel):
    status: str = Field(..., description=DESC_STATUS)
    data: SimulateResult = Field(..., description="단가 변경 시뮬레이션 결과")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field(default_factory=list, description=DESC_WARNINGS)
