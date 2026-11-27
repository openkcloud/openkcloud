"""
모니터링 API v2 Pydantic 스키마 및 메트릭 허용 목록.

공통 응답 정책(docs/API_GUIDE.md §공통-응답-정책):
  - status: "success" | "partial" | "error"
  - observed_at: ISO 8601 수집 시각
  - warnings[]: STALE_DATA, PARTIAL_SOURCE, ESTIMATED_POWER 등 (정상 시 빈 목록)
"""
from typing import Optional

from pydantic import BaseModel, Field

from app.config import npu_util
from app.schemas._common import DESC_OBSERVED_AT, DESC_STATUS, DESC_WARNINGS, _now

# ---------------------------------------------------------------------------
# 메트릭 허용 목록 (METRIC_ALLOWLIST)
# ---------------------------------------------------------------------------
# 헬스 방향 주의:
#   furiosa_alive    → 1 = 정상
#   rebellions_health → 0 = 정상 (방향 반전!)
#   gpu_xid_errors   → 0 = 정상 (오류 카운트)
# 메모리 단위 주의:
#   L40S GPU → MiB
#   Furiosa / Rebellions NPU → bytes
# ---------------------------------------------------------------------------
METRIC_ALLOWLIST: dict[str, str] = {
    # 온도 (Temperature)
    "gpu_temperature": 'DCGM_FI_DEV_GPU_TEMP{cluster="l40s"}',
    "furiosa_temperature": 'furiosa_npu_hw_temperature{label="peak",cluster=~"furiosa.*"}',
    # 사용률 (Utilization)
    "gpu_utilization": 'DCGM_FI_DEV_GPU_UTIL{cluster="l40s"}',
    "gpu_utilization_prof": 'DCGM_FI_PROF_GR_ENGINE_ACTIVE{cluster="l40s"} * 100',
    "furiosa_utilization": npu_util('furiosa_npu_core_utilization{cluster=~"furiosa.*"}', "furiosa"),
    "rebellions_utilization": npu_util('RBLN_DEVICE_STATUS:UTILIZATION{cluster="rebellions"}', "rebellions"),
    # 메모리 (Memory). 단위 주의: L40S=MiB, NPU=bytes
    "gpu_memory_used_mib": 'DCGM_FI_DEV_FB_USED{cluster="l40s"}',
    "gpu_memory_free_mib": 'DCGM_FI_DEV_FB_FREE{cluster="l40s"}',
    "furiosa_memory_used_bytes": 'furiosa_npu_dram_usage{cluster=~"furiosa.*"}',
    "rebellions_memory_used_bytes": 'RBLN_DEVICE_STATUS:DRAM_USED{cluster="rebellions"}',
    # 헬스 (Health). 방향 주의!
    "furiosa_alive": 'furiosa_npu_alive{cluster=~"furiosa.*"}',  # 1=정상
    "rebellions_health": 'RBLN_DEVICE_STATUS:HEALTH{cluster="rebellions"}',    # 0=정상!
    "gpu_xid_errors": 'changes(DCGM_FI_DEV_XID_ERRORS{cluster="l40s"}[10m])', # 0=정상
}


# ---------------------------------------------------------------------------
# 모델
# ---------------------------------------------------------------------------

class ClusterCounts(BaseModel):
    """클러스터 구분별 개수"""

    total: int = Field(..., description="전체 클러스터 수. 관리 클러스터와 서비스 클러스터의 합")
    management: int = Field(..., description="관리 클러스터 수. 물리 서버와 OpenStack 을 운영하는 클러스터")
    service: int = Field(..., description="서비스 클러스터 수. 관리 클러스터의 VM 위에 구성된 Kubernetes 클러스터")


class NodeCounts(BaseModel):
    """노드 구분별 개수와 정상 여부 집계"""

    total: int = Field(..., description="전체 노드 수. 물리 노드와 가상 노드의 합")
    physical: int = Field(..., description="물리 노드 수. 관리 클러스터에 등록된 노드 기준")
    virtual: int = Field(..., description="가상(VM) 노드 수. 서비스 클러스터의 Kubernetes 노드와 단독 VM 의 합")
    healthy: int = Field(
        ..., description="정상 노드 수. Kubernetes 노드는 Ready 조건이 참, 쿠버네티스 없는 단독 VM 은 5분 안에 보고가 온 것"
    )
    unhealthy: int = Field(..., description="비정상 노드 수. 전체 노드 수에서 정상 노드 수를 뺀 값")


class OverviewData(BaseModel):
    """전체 인프라 현황 요약값"""

    clusters: ClusterCounts = Field(..., description="클러스터 수 요약. 전체, 관리, 서비스 클러스터 수")
    nodes: NodeCounts = Field(..., description="노드 수 요약. 전체, 물리, 가상, 정상, 비정상 노드 수")
    accelerator_count: int = Field(
        ..., description="가속기 클러스터(l40s, furiosa, rebellions)의 메트릭 수집 대상 수. 카드 수와 다를 수 있음"
    )
    healthy_count: int = Field(..., description="accelerator_count 중 응답 중인(up) 수집 대상 수")
    avg_temperature: dict[str, Optional[float]] = Field(
        ..., description="클러스터별 평균 가속기 온도(°C). 키: 클러스터 이름(l40s | furiosa | rebellions), 미수집 시 값 null"
    )


class OverviewResponse(BaseModel):
    """전체 인프라 현황 응답"""

    status: str = Field(..., description=DESC_STATUS)
    data: OverviewData = Field(..., description="전체 인프라 현황 요약값")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class NodeCountSeriesItem(BaseModel):
    """노드 수 한 종류의 시계열"""

    metric: str = Field(..., description="노드 수 종류 (total | healthy). total: 전체 노드 수, healthy: 정상 노드 수")
    values: list[tuple[str, str]] = Field(
        ..., description="(시각, 값) 쌍 목록. 시각은 ISO 8601 UTC, 값은 노드 수 숫자 문자열"
    )


class NodesTimeseriesResponse(BaseModel):
    """노드 수 시계열 응답"""

    status: str = Field(..., description=DESC_STATUS)
    series: list[NodeCountSeriesItem] = Field(..., description="노드 수 종류별 시계열 목록")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class MetricSample(BaseModel):
    """메트릭 시리즈 하나의 현재값"""

    metric: dict[str, str] = Field(..., description="원본 메트릭 라벨 (키: 라벨 이름, 값: 라벨 값)")
    value: tuple[float, str] = Field(
        ..., description="현재값 (Unix 시각(초), 값 문자열). 시각이 ISO 문자열이 아닌 숫자"
    )


class MetricSeries(BaseModel):
    """메트릭 시리즈 하나의 시계열"""

    metric: dict[str, str] = Field(..., description="원본 메트릭 라벨 (키: 라벨 이름, 값: 라벨 값)")
    values: list[tuple[str, str]] = Field(
        ..., description="(시각, 값) 쌍 목록. 시각은 ISO 8601 UTC, 값은 숫자 문자열"
    )


class MetricsQueryResponse(BaseModel):
    """메트릭 현재값 응답"""

    status: str = Field(..., description=DESC_STATUS)
    metric: str = Field(..., description="조회한 메트릭 이름")
    results: list[MetricSample] = Field(..., description="메트릭 조회 결과 목록. 시리즈별 라벨과 현재값(value)")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class TimeseriesResponse(BaseModel):
    """메트릭 시계열 응답"""

    status: str = Field(..., description=DESC_STATUS)
    metric: str = Field(..., description="조회한 메트릭 이름")
    series: list[MetricSeries] = Field(..., description="시리즈별 시계열 목록. 시리즈별 라벨과 (시각, 값) 목록(values)")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class TemperatureSeriesItem(BaseModel):
    """장치 하나의 온도 시계열"""

    vendor: str = Field(..., description="가속기 벤더 (nvidia | furiosa | rebellions)")
    cluster: str = Field(..., description="클러스터 이름")
    metric_labels: dict[str, str] = Field(..., description="원본 메트릭 라벨 (키: 라벨 이름, 값: 라벨 값)")
    values: list[tuple[str, str]] = Field(
        ..., description="(시각, 값) 쌍 목록. 시각은 ISO 8601 UTC, 값은 온도(°C) 숫자 문자열"
    )


class TemperatureTimeseriesResponse(BaseModel):
    """가속기 온도 시계열 응답"""

    status: str = Field(..., description=DESC_STATUS)
    series: list[TemperatureSeriesItem] = Field(..., description="장치별 온도 시계열 목록")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


# ---------------------------------------------------------------------------
# 전력(Power)
# ---------------------------------------------------------------------------

class PowerSummaryData(BaseModel):
    """전력 요약 데이터. 서버 총 전력, CPU, 벤더별 가속기, 기타 전력으로 구분"""

    server_total_watts: Optional[float] = Field(..., description="서버 총 전력(W). IPMI 기준. 미수집 시 null")
    cpu_total_watts: Optional[float] = Field(..., description="CPU 전력(W). Kepler 기준. 미수집 시 null")
    accelerator_total_watts: Optional[float] = Field(..., description="가속기 전력 합계(W). 미수집 시 null")
    accelerator_by_vendor: dict[str, Optional[float]] = Field(
        ..., description="벤더별 가속기 전력 합계(W). 키: 벤더(nvidia | furiosa | rebellions), 미수집 시 값 null"
    )
    other_watts: Optional[float] = Field(..., description="기타 전력(W). 서버 총 전력 - CPU - 가속기. 계산 불가 시 null")


class PowerSummaryResponse(BaseModel):
    """전력 요약 응답"""

    status: str = Field(..., description=DESC_STATUS)
    data: PowerSummaryData = Field(..., description="전력 요약 집계 결과")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class PowerBreakdownItem(BaseModel):
    """기준값 하나의 전력"""

    key: str = Field(
        ..., description="기준값 이름. 벤더, 클러스터, 노드 이름 또는 카드 식별값(인스턴스/장치 번호/클러스터)"
    )
    watts: Optional[float] = Field(..., description="전력(W). 미수집 시 null")
    layer: Optional[str] = Field(
        None, description="측정 구분 (server | cpu | accelerator). server: IPMI 서버 전력, cpu: Kepler CPU 전력"
    )


class PowerBreakdownResponse(BaseModel):
    """기준별 전력 분포 응답"""

    status: str = Field(..., description=DESC_STATUS)
    dimension: str = Field(..., description="적용된 기준 (vendor | cluster | node | accelerator)")
    items: list[PowerBreakdownItem] = Field(..., description="기준값별 전력 목록. 전력 값 없는 항목은 맨 뒤")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class PowerTimeseriesLayer(BaseModel):
    """측정 구분 하나의 전력 시계열"""

    layer: str = Field(..., description="측정 구분 (server | cpu | accelerator)")
    values: list[tuple[str, str]] = Field(
        ..., description="(시각, 값) 쌍 목록. 시각은 ISO 8601 UTC, 값은 전력(W) 숫자 문자열"
    )


class PowerTimeseriesResponse(BaseModel):
    """전체 전력 시계열 응답"""

    status: str = Field(..., description=DESC_STATUS)
    layers: list[PowerTimeseriesLayer] = Field(..., description="측정 구분별 시계열 목록")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class AcceleratorEfficiency(BaseModel):
    """벤더 하나의 가속기 전력 효율"""

    vendor: str = Field(..., description="가속기 벤더 (nvidia | furiosa | rebellions)")
    power_watts: Optional[float] = Field(
        ..., description="가속기 전력(W). 해당 벤더 모든 카드의 합계, 조회 시점 값. 미수집 시 null"
    )
    utilization_pct: Optional[float] = Field(
        ...,
        description="가속기 사용률(%). 해당 벤더 모든 카드(퓨리오사는 모든 코어) 값의 평균. 시간 평균이 아닌 조회 시점 순간값. 미수집 시 null",
    )
    tdp_watts: Optional[float] = Field(
        ...,
        description="카드 1장의 제조사 규격 최대 전력(W). 벤더 표기에 따라 TDP 또는 최대 소비전력 값",
    )
    tdp_ratio_pct: Optional[float] = Field(
        ..., description="규격 대비 전력 비중(%). 카드 1장 평균 전력(power_watts ÷ 카드 수) ÷ 규격 최대 전력. 계산 불가 시 null"
    )
    efficiency_pct_per_watt: Optional[float] = Field(
        None,
        description="전력 1W당 사용률(%/W). utilization_pct ÷ 카드 1장 평균 전력. 카드 규격이 달라 같은 벤더끼리 시간 비교용. 계산 불가 시 null",
    )


class PowerEfficiencyData(BaseModel):
    """전력 효율 데이터. 전력 사용 효율 추정치와 벤더별 가속기 효율"""

    pue_estimate: Optional[float] = Field(
        ...,
        description="전력 사용 효율 추정치(단위 없음). 서버 전체 전력(IPMI) ÷ (CPU 전력(Kepler) + 가속기 전력). 냉방 전력 미포함 근사치. 계산 불가 시 null",
    )
    avg_efficiency_pct_per_watt: Optional[float] = Field(
        None,
        description="값이 있는 벤더들의 efficiency_pct_per_watt 평균(%/W). 조회 시점 순간값. 계산 불가 시 null",
    )
    accelerators: list[AcceleratorEfficiency] = Field(..., description="벤더별 가속기 전력 효율 목록")


class PowerEfficiencyResponse(BaseModel):
    """가속기 전력 효율 응답"""

    status: str = Field(..., description=DESC_STATUS)
    data: PowerEfficiencyData = Field(..., description="전력 효율 집계 결과")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class PowerEfficiencySeriesItem(BaseModel):
    """벤더 하나의 전력 효율 시계열"""

    vendor: str = Field(..., description="가속기 벤더 (all | nvidia | furiosa | rebellions). all: 값이 있는 벤더들의 평균")
    values: list[tuple[str, str]] = Field(
        ..., description="(시각, 값) 쌍 목록. 시각은 ISO 8601 UTC, 값은 전력 1W당 사용률(%/W) 숫자 문자열"
    )


class PowerEfficiencyTimeseriesResponse(BaseModel):
    """가속기 전력 효율 시계열 응답"""

    status: str = Field(..., description=DESC_STATUS)
    series: list[PowerEfficiencySeriesItem] = Field(..., description="벤더별 시계열 목록")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class VendorUtilization(BaseModel):
    """벤더 하나의 가속기 사용률"""

    vendor: str = Field(..., description="가속기 벤더 (nvidia | furiosa | rebellions)")
    card_count: int = Field(..., description="값이 수집된 카드 수")
    utilization_pct: Optional[float] = Field(
        None,
        description="해당 벤더 카드들의 사용률 평균(%). NVIDIA 연산 엔진 활성 비율, 퓨리오사 코어 8개 평균, 리벨리온 장치 사용률. 미수집 시 null",
    )


class HostUtilization(BaseModel):
    """호스트 하나의 가속기 사용률"""

    host: str = Field(..., description="카드가 꽂힌 호스트 이름")
    vendor: str = Field(..., description="가속기 벤더 (nvidia | furiosa | rebellions)")
    card_count: int = Field(..., description="이 호스트에서 값이 수집된 카드 수")
    utilization_pct: float = Field(..., description="이 호스트 카드들의 사용률 평균(%)")


class AcceleratorUtilizationData(BaseModel):
    """전체 가속기 사용률 데이터"""

    avg_utilization_pct: Optional[float] = Field(
        None, description="전체 카드 사용률 평균(%). 벤더 구분 없이 카드 1장씩 같은 비중으로 평균. 미수집 시 null"
    )
    card_count: int = Field(..., description="값이 수집된 전체 카드 수")
    vendors: list[VendorUtilization] = Field(..., description="벤더별 사용률 목록")
    hosts: list[HostUtilization] = Field(
        default_factory=list, description="호스트별 사용률 목록. sort_order 순(기본 높은 순), by=node 일 때만 채움"
    )


class AcceleratorUtilizationResponse(BaseModel):
    """가속기 사용률 응답"""

    status: str = Field(..., description=DESC_STATUS)
    data: AcceleratorUtilizationData = Field(..., description="가속기 사용률 집계 결과")
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)


class UtilizationSeriesItem(BaseModel):
    """벤더 하나의 가속기 사용률 시계열"""

    vendor: str = Field(..., description="가속기 벤더 (all | nvidia | furiosa | rebellions). all: 벤더 구분 없이 카드 1장씩 같은 비중으로 평균")
    values: list[tuple[str, str]] = Field(
        ..., description="(시각, 값) 쌍 목록. 시각은 ISO 8601 UTC, 값은 사용률 평균(%) 숫자 문자열"
    )


class HostUtilizationSeriesItem(BaseModel):
    """호스트 하나의 가속기 사용률 시계열"""

    host: str = Field(..., description="카드가 꽂힌 호스트 이름")
    vendor: str = Field(..., description="가속기 벤더 (nvidia | furiosa | rebellions)")
    values: list[tuple[str, str]] = Field(
        ..., description="(시각, 값) 쌍 목록. 시각은 ISO 8601 UTC, 값은 이 호스트 카드들의 사용률 평균(%) 숫자 문자열"
    )


class AcceleratorUtilizationTimeseriesResponse(BaseModel):
    """가속기 사용률 시계열 응답"""

    status: str = Field(..., description=DESC_STATUS)
    series: list[UtilizationSeriesItem] = Field(..., description="벤더별 시계열 목록")
    hosts: list[HostUtilizationSeriesItem] = Field(
        default_factory=list, description="호스트별 시계열 목록. by=node 일 때만 채움"
    )
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
    warnings: list[str] = Field([], description=DESC_WARNINGS)
