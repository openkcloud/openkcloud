"""
가속기(GPU/NPU) API v2 Pydantic 스키마.

공통 응답 정책(docs/API_GUIDE.md §공통-응답-정책):
  - status: "success" | "partial" | "error"
  - observed_at: ISO 8601 수집 시각
  - warnings[]: NO_DATA, ACCELERATOR_NOT_FOUND, PARTITION_DATA_NOT_AVAILABLE 등
메모리는 벤더별 원본 단위(L40S=MiB, Furiosa/Rebellions=bytes)를 bytes로 통일해 반환한다.
"""
from typing import Optional

from pydantic import BaseModel, Field

from app.schemas._common import DESC_OBSERVED_AT, DESC_STATUS, DESC_TOTAL, DESC_WARNINGS, _now


_DESC_ACC_ID = "가속기 ID. NVIDIA, Rebellions 는 카드 UUID, Furiosa 는 장치 이름(예: npu0)"
_DESC_VENDOR = "가속기 벤더 (nvidia | furiosa | rebellions)"
_DESC_LABELS = "원본 메트릭 라벨 (키: 라벨 이름, 값: 라벨 값)"
_DESC_UTIL = "사용률(%). 미수집 시 null"
_DESC_TEMP = "온도(°C). 미수집 시 null"
_DESC_POWER = "전력(W). 미수집 시 null"
_DESC_MEM_USED = "사용 메모리(bytes). 벤더마다 다른 원본 단위를 bytes 로 통일, 미수집 시 null"
_DESC_MEM_TOTAL = "총 메모리(bytes). 벤더마다 다른 원본 단위를 bytes 로 통일, 미수집 시 null"
_DESC_HEALTHY = (
    "정상 동작 여부. NVIDIA: 최근 10분 XID 오류 없음, Furiosa: 장치 응답 정상, "
    "Rebellions: 장치 상태 정상. 미수집 시 null"
)
_DESC_POWER_LIMIT = "전력 상한(W). 카드에서 읽은 설정값, 없으면 제조사 규격 TDP. 둘 다 없으면 null"
_DESC_POWER_LIMIT_SOURCE = (
    "전력 상한 출처 (measured | spec_tdp). measured: 카드 설정값, spec_tdp: 제조사 규격 TDP. "
    "상한이 없으면 null"
)
_DESC_POWER_LIMIT_PERCENT = "전력 상한 대비 현재 전력(%). 판정 불가 시 null"
_DESC_POWER_CAPPED = (
    "전력 상한 도달 여부. 클럭 제한 사유가 없으면 상한 대비 95% 이상일 때 true, 판정 불가 시 null"
)
_DESC_THROTTLED = "쓰로틀링 여부. 판정에 필요한 값이 비면 null"
_DESC_THROTTLE_SOURCE = (
    "쓰로틀링 판정 근거 (clock_reason | inferred). clock_reason: 클럭 제한 사유 직접 관측(NVIDIA, Furiosa), "
    "inferred: 사용률 90% 이상 유지, 최근 10분 고온, 전력 하락으로 간접 판정. 판정 불가 시 null"
)
_DESC_POWER_STATE = (
    "전력 모드. NVIDIA 는 성능 단계 (P0 | P1 | ... | P15), P0 이 최고 성능, 숫자가 클수록 절전, 쉬는 중이면 보통 P8. "
    "Furiosa 는 governor 모드 이름 (예: performance). Rebellions 나 미수집 시 null"
)
_DESC_THROTTLE_REASONS = (
    "클럭 제한 사유 목록. NVIDIA (sw_power_cap | hw_slowdown | sw_thermal | hw_thermal | hw_power_brake), "
    "Furiosa 최근 5분 (thermal_slowdown | app_power_cap | app_clock_cap | hw_clock_cap | hw_bus_limit | hw_power_cap | other_reason). "
    "판정 근거가 clock_reason 일 때만 채움"
)
_DESC_SERIES_VALUES = "(시각, 값) 쌍 목록. 시각은 ISO 8601 UTC, 값은 전력(W) 숫자 문자열"


class AcceleratorItem(BaseModel):
    """가속기 한 장의 현재 상태."""

    acc_id: str = Field(..., description=_DESC_ACC_ID)
    vendor: str = Field(..., description=_DESC_VENDOR)
    cluster: str = Field(..., description="소속 클러스터 이름")
    node: Optional[str] = Field(None, description="카드가 장착된 노드(호스트) 이름. 확인 불가 시 null")
    model: Optional[str] = Field(None, description="모델명 (예: NVIDIA L40S, RNGD). 확인 불가 시 null")
    utilization_percent: Optional[float] = Field(None, description=_DESC_UTIL)
    temperature_celsius: Optional[float] = Field(None, description=_DESC_TEMP)
    power_watts: Optional[float] = Field(None, description=_DESC_POWER)
    memory_used_bytes: Optional[float] = Field(None, description=_DESC_MEM_USED)
    memory_total_bytes: Optional[float] = Field(None, description=_DESC_MEM_TOTAL)
    healthy: Optional[bool] = Field(None, description=_DESC_HEALTHY)
    power_limit_watts: Optional[float] = Field(None, description=_DESC_POWER_LIMIT)
    power_limit_source: Optional[str] = Field(None, description=_DESC_POWER_LIMIT_SOURCE)
    power_limit_percent: Optional[float] = Field(None, description=_DESC_POWER_LIMIT_PERCENT)
    power_capped: Optional[bool] = Field(None, description=_DESC_POWER_CAPPED)
    throttled: Optional[bool] = Field(None, description=_DESC_THROTTLED)
    throttle_source: Optional[str] = Field(None, description=_DESC_THROTTLE_SOURCE)
    throttle_reasons: list[str] = Field(default_factory=list, description=_DESC_THROTTLE_REASONS)
    power_state: Optional[str] = Field(None, description=_DESC_POWER_STATE)
    labels: dict[str, str] = Field({}, description=_DESC_LABELS)


class AcceleratorSummaryData(BaseModel):
    """가속기 집계 요약."""

    count: int = Field(..., description="집계에 포함된 가속기(카드) 수")
    vendor: Optional[str] = Field(None, description=_DESC_VENDOR)
    avg_utilization_percent: Optional[float] = Field(None, description="평균 사용률(%). 미수집 시 null")
    avg_temperature_celsius: Optional[float] = Field(None, description="평균 온도(°C). 미수집 시 null")
    avg_power_watts: Optional[float] = Field(None, description="평균 전력(W). 미수집 시 null")
    total_power_watts: Optional[float] = Field(None, description="전력 합계(W). 미수집 시 null")


class AcceleratorListResponse(BaseModel):
    """가속기 목록 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: list[AcceleratorItem] = Field([], description="가속기 목록. limit, offset 으로 자른 페이지")
    total: int = Field(0, description=DESC_TOTAL)
    summary: Optional[AcceleratorSummaryData] = Field(
        None, description="노드 가속기 집계. 카드 수, 벤더, 평균 사용률, 평균 온도, 평균 전력, 전력 합계"
    )
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class TopologyLinkItem(BaseModel):
    """가속기 간 통신 링크 하나."""

    metric_labels: dict[str, str] = Field(
        ..., description="원본 메트릭 라벨 (키: 라벨 이름, 값: 라벨 값). 출발 카드와 도착 카드 정보 포함"
    )
    value: Optional[float] = Field(None, description="링크 대역폭 값. 벤더 exporter 원본 값, 미수집 시 null")


class AcceleratorTopologyData(BaseModel):
    """가속기 간 연결 토폴로지."""

    vendor: Optional[str] = Field(None, description=_DESC_VENDOR)
    links: list[TopologyLinkItem] = Field([], description="링크 목록. NPU 클러스터는 빈 목록")


class AcceleratorTopologyResponse(BaseModel):
    """가속기 토폴로지 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: AcceleratorTopologyData = Field(..., description="가속기 간 연결 토폴로지")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class AcceleratorInfo(BaseModel):
    """가속기 한 장의 고정 정보."""

    acc_id: str = Field(..., description=_DESC_ACC_ID)
    vendor: str = Field(..., description=_DESC_VENDOR)
    cluster: str = Field(..., description="소속 클러스터 이름")
    node: Optional[str] = Field(None, description="카드가 장착된 노드(호스트) 이름. 확인 불가 시 null")
    model: Optional[str] = Field(None, description="모델명 (예: NVIDIA L40S, RNGD). 확인 불가 시 null")
    memory_total_bytes: Optional[float] = Field(None, description=_DESC_MEM_TOTAL)
    power_limit_watts: Optional[float] = Field(None, description=_DESC_POWER_LIMIT)
    power_limit_source: Optional[str] = Field(None, description=_DESC_POWER_LIMIT_SOURCE)
    labels: dict[str, str] = Field({}, description=_DESC_LABELS)


class AcceleratorDetailResponse(BaseModel):
    """가속기 상세 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: Optional[AcceleratorInfo] = Field(None, description="가속기 상세. 대상이 없으면 null")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class AcceleratorMetricsData(BaseModel):
    """가속기 실시간 메트릭."""

    utilization_percent: Optional[float] = Field(None, description=_DESC_UTIL)
    memory_used_bytes: Optional[float] = Field(None, description=_DESC_MEM_USED)
    memory_total_bytes: Optional[float] = Field(None, description=_DESC_MEM_TOTAL)
    power_watts: Optional[float] = Field(None, description=_DESC_POWER)
    temperature_celsius: Optional[float] = Field(None, description=_DESC_TEMP)
    healthy: Optional[bool] = Field(None, description=_DESC_HEALTHY)
    power_limit_percent: Optional[float] = Field(None, description=_DESC_POWER_LIMIT_PERCENT)
    power_capped: Optional[bool] = Field(None, description=_DESC_POWER_CAPPED)
    throttled: Optional[bool] = Field(None, description=_DESC_THROTTLED)
    throttle_source: Optional[str] = Field(None, description=_DESC_THROTTLE_SOURCE)
    throttle_reasons: list[str] = Field(default_factory=list, description=_DESC_THROTTLE_REASONS)
    power_state: Optional[str] = Field(None, description=_DESC_POWER_STATE)
    extra: dict[str, float] = Field(
        default_factory=dict,
        description="벤더별 부가 메트릭. 키: sm_clock, mem_clock(MHz), mem_copy_util, dec_util, enc_util(%), "
        "pcie_replay(누적 횟수). 해당 메트릭이 없는 벤더는 빈 객체",
    )


class AcceleratorMetricsResponse(BaseModel):
    """가속기 실시간 메트릭 응답."""

    status: str = Field(..., description=DESC_STATUS)
    acc_id: str = Field(..., description=_DESC_ACC_ID)
    vendor: Optional[str] = Field(None, description=_DESC_VENDOR + ". 모르는 클러스터면 null")
    data: AcceleratorMetricsData = Field(..., description="가속기 실시간 메트릭. 대상이 없으면 모든 값 null")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class PowerData(BaseModel):
    """가속기 전력 현재값."""

    power_watts: Optional[float] = Field(None, description="전력(W). 벤더 exporter 실측값, 미수집 시 null")


class AcceleratorPowerResponse(BaseModel):
    """가속기 전력 응답."""

    status: str = Field(..., description=DESC_STATUS)
    acc_id: str = Field(..., description=_DESC_ACC_ID)
    data: PowerData = Field(..., description="가속기 전력 현재값")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class PowerSeriesItem(BaseModel):
    """전력 시계열 하나."""

    metric_labels: dict[str, str] = Field(..., description=_DESC_LABELS)
    values: list[tuple[str, str]] = Field(..., description=_DESC_SERIES_VALUES)


class AcceleratorPowerTimeseriesResponse(BaseModel):
    """가속기 전력 시계열 응답."""

    status: str = Field(..., description=DESC_STATUS)
    acc_id: str = Field(..., description=_DESC_ACC_ID)
    series: list[PowerSeriesItem] = Field([], description="메트릭 라벨별 전력 시계열 목록")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class TemperatureData(BaseModel):
    """가속기 온도 현재값."""

    temperature_celsius: Optional[float] = Field(None, description=_DESC_TEMP)


class AcceleratorTemperatureResponse(BaseModel):
    """가속기 온도 응답."""

    status: str = Field(..., description=DESC_STATUS)
    acc_id: str = Field(..., description=_DESC_ACC_ID)
    data: TemperatureData = Field(..., description="가속기 온도 현재값")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class PartitionItem(BaseModel):
    """가속기를 나눈 파티션 한 개의 정보."""

    partition_id: str = Field(..., description="파티션 ID")
    profile: Optional[str] = Field(None, description="파티션 프로파일 이름. 미수집 시 null")
    utilization_percent: Optional[float] = Field(None, description=_DESC_UTIL)


class PartitionListResponse(BaseModel):
    """파티션 목록 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: list[PartitionItem] = Field([], description="파티션 목록")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class PartitionDetailResponse(BaseModel):
    """파티션 상세 응답."""

    status: str = Field(..., description=DESC_STATUS)
    data: Optional[PartitionItem] = Field(None, description="파티션 상세. 대상이 없으면 null")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class PartitionPowerData(BaseModel):
    """파티션 한 개에 배분된 전력 추정값."""

    power_watts: Optional[float] = Field(
        None, description="카드 전력을 파티션 점유 비율로 나눈 추정 전력(W). 미수집 시 null"
    )


class PartitionPowerResponse(BaseModel):
    """파티션 전력 응답."""

    status: str = Field(..., description=DESC_STATUS)
    acc_id: str = Field(..., description=_DESC_ACC_ID)
    partition_id: str = Field(..., description="파티션 ID")
    data: PartitionPowerData = Field(..., description="파티션 전력 추정값")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class PartitionPowerTimeseriesResponse(BaseModel):
    """파티션 전력 시계열 응답."""

    status: str = Field(..., description=DESC_STATUS)
    acc_id: str = Field(..., description=_DESC_ACC_ID)
    partition_id: str = Field(..., description="파티션 ID")
    series: list[PowerSeriesItem] = Field([], description="메트릭 라벨별 전력 시계열 목록")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class ThrottlingSeriesItem(BaseModel):
    """카드 한 장의 기간 내 쓰로틀링 판정 시계열."""

    vendor: str = Field(..., description=_DESC_VENDOR)
    cluster: str = Field(..., description="소속 클러스터 이름")
    node: Optional[str] = Field(None, description="카드가 장착된 노드(호스트) 이름. 확인 불가 시 null")
    acc_id: str = Field(..., description=_DESC_ACC_ID)
    values: list[tuple[str, str]] = Field(
        ...,
        description="(시각, 판정값) 쌍 목록. 시각은 ISO 8601 UTC, 판정값 (1 | 0). 1: 쓰로틀링, 0: 정상. "
        "판정에 필요한 값이 빠진 시각은 점 없음",
    )
    throttle_events: int = Field(..., description="기간 안에서 쓰로틀링이 시작된 횟수")
    throttled_minutes: float = Field(
        ..., description="기간 안에서 쓰로틀링 상태였던 시간(분). 판정값 1인 점 수와 step 의 곱"
    )


class ThrottlingSummary(BaseModel):
    """조회 대상 카드 전체의 쓰로틀링 합계."""

    cards_total: int = Field(..., description="판정 값이 있는 카드 수")
    cards_throttled: int = Field(..., description="기간 안에 한 번이라도 쓰로틀링이 난 카드 수")
    throttle_events: int = Field(..., description="쓰로틀링 시작 횟수 합계")
    throttled_minutes: float = Field(..., description="쓰로틀링 시간 합계(분). 카드별 시간을 더한 값")
    step_seconds: float = Field(..., description="판정 간격(초). 이보다 짧게 끝난 쓰로틀링은 놓칠 수 있음")


class ThrottlingTimelinePoint(BaseModel):
    """한 시각(또는 bucket 구간)의 전체 카드 쓰로틀링 합계."""

    time: str = Field(..., description="시각, ISO 8601 UTC. bucket 지정 시 구간 시작 시각")
    throttled_cards: int = Field(..., description="쓰로틀링 중인 카드 수. bucket 지정 시 구간 안 최대값")
    events: int = Field(..., description="이 시각(구간)에 쓰로틀링이 시작된 횟수")
    throttled_minutes: float = Field(..., description="이 시각(구간)에 쓰로틀링 상태였던 시간(분). 카드별 시간의 합")


class ThrottlingTimeseriesResponse(BaseModel):
    """쓰로틀링 시계열 응답."""

    status: str = Field(..., description=DESC_STATUS)
    series: list[ThrottlingSeriesItem] = Field(
        default_factory=list, description="카드별 쓰로틀링 판정 시계열 목록. detail=true 일 때만 채움"
    )
    summary: Optional[ThrottlingSummary] = Field(
        None, description="조회 대상 카드 전체의 쓰로틀링 합계. 판정 값이 없으면 null"
    )
    timeline: list[ThrottlingTimelinePoint] = Field(
        default_factory=list, description="시각별 전체 카드 쓰로틀링 합계. 판정 값이 없으면 빈 목록"
    )
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)
