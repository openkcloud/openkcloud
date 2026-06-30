"""
워크로드 메트릭 분석 및 전력/비용 프로파일 생성 모듈.

TimescaleDB에서 수집된 데이터를 바탕으로 워크로드별 효율성을 분석하고,
노드별 요약 통계를 생성한다. 이 모듈은 ml-power-predictor의 
데이터 파이프라인이자 분석 엔진 역할을 수행한다.

Author: hyo <미정>
Created: 2026-06-12
Related: openkcloud-opt-cst ml-power-predictor
"""

import logging
from dataclasses import dataclass, asdict
from typing import Optional

import pandas as pd
from .fetcher import fetch_raw, logger as fetcher_logger

logger = logging.getLogger(__name__)

# kWh 단가 — 전역 설정 또는 환경 변수에서 로드
from ..power_predictor.predictor import ELECTRICITY_RATE


@dataclass
class WorkloadProfile:
    """단일 워크로드의 전력 소비 및 비용 효율성 지표 모델."""
    workload_id: str
    namespace: str
    job_type: str
    device_type: str
    model_name: Optional[str]
    sample_count: int
    avg_power_watts: float
    peak_power_watts: float
    avg_util_pct: float
    avg_memory_mb: float
    total_energy_joules: float
    avg_temperature_c: float
    efficiency_score: float  # 전력 대비 연산 효율 (util/watt)
    cost_per_hour_usd: float


def analyze_workloads(interval: str = "1 hour") -> list[dict]:
    """지정 기간의 데이터를 분석하여 워크로드별 프로파일 리스트를 생성한다.

    Args:
        interval (str): 분석 대상 기간 (PostgreSQL INTERVAL). Defaults to "1 hour".

    Returns:
        list[dict]: 워크로드별 분석 결과 리스트.
    """
    df = fetch_raw(interval)
    if df.empty:
        return []

    profiles = []
    # 주요 식별자로 그룹화하여 통계 산출
    groups = df.groupby(["workload_id", "namespace", "job_type", "device_type"])
    
    for (wid, ns, jtype, dtype), grp in groups:
        avg_power = grp["power_watts"].mean() or 0
        peak_power = grp["power_watts"].max() or 0
        avg_util = grp["gpu_util_pct"].mean() or 0
        avg_mem = grp["memory_used_mb"].mean() or 0
        total_energy = grp["energy_joules"].sum() or 0
        avg_temp = grp["temperature_c"].mean() or 0
        
        # 모델명은 유효한 첫 번째 값 사용
        model_name = grp["model_name"].dropna().iloc[0] if not grp["model_name"].dropna().empty else None

        # 효율성 점수: Watt당 이용률 (높을수록 효율적)
        efficiency = (avg_util / avg_power) if avg_power > 0 else 0

        # 시간당 비용 계산 (W -> kW 변환)
        cost_per_hour = (avg_power / 1000) * ELECTRICITY_RATE

        profile = WorkloadProfile(
            workload_id=wid,
            namespace=ns,
            job_type=jtype,
            device_type=dtype,
            model_name=model_name,
            sample_count=len(grp),
            avg_power_watts=round(avg_power, 2),
            peak_power_watts=round(peak_power, 2),
            avg_util_pct=round(avg_util, 2),
            avg_memory_mb=round(avg_mem, 1),
            total_energy_joules=round(total_energy, 2),
            avg_temperature_c=round(avg_temp, 1),
            efficiency_score=round(efficiency, 4),
            cost_per_hour_usd=round(cost_per_hour, 6),
        )
        profiles.append(asdict(profile))

    return profiles


def get_node_summary(interval: str = "1 hour") -> list[dict]:
    """노드 및 디바이스 타입별 전력 사용 현황 요약을 생성한다.

    Args:
        interval (str): 집계 대상 기간. Defaults to "1 hour".

    Returns:
        list[dict]: 노드별 요약 데이터 리스트.
    """
    df = fetch_raw(interval)
    if df.empty:
        return []

    summary = (
        df.groupby(["node_name", "device_type"])
        .agg(
            avg_power=("power_watts", "mean"),
            peak_power=("power_watts", "max"),
            total_energy=("energy_joules", "sum"),
            avg_util=("gpu_util_pct", "mean"),
            sample_count=("power_watts", "count"),
        )
        .reset_index()
    )

    # 시간당 추정 비용 추가
    summary["cost_per_hour_usd"] = (
        (summary["avg_power"] / 1000) * ELECTRICITY_RATE
    ).round(6)

    return summary.to_dict(orient="records")
