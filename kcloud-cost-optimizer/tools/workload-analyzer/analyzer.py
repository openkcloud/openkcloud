"""
workload_metrics 테이블을 읽어 GPU/NPU/CPU 워크로드의 전력·비용 패턴을 분석하는 모듈.

TimescaleDB에 저장된 30초 간격 메트릭 데이터를 집계하여
워크로드별·노드별 전력 소비량, 비용, 효율성 지표를 산출한다.

Author: hyo
Created: 2026-06-12
Related: openkcloud-opt-cst cost-analytics-engine
"""

import logging
import os
from dataclasses import dataclass
from typing import Optional

import pandas as pd
import psycopg2

logger = logging.getLogger(__name__)

DATABASE_URL = os.getenv(
    "DATABASE_URL",
    "postgresql://platform:platform1234@timescaledb.data-pipeline.svc.cluster.local:5432/workload_data",
)

QUERY_METRICS = """
SELECT
    time, workload_id, namespace, job_type,
    device_type, device_id, node_name,
    power_watts, gpu_util_pct, memory_used_mb, temperature_c,
    energy_joules, sm_clock_mhz,
    encoder_util_pct, decoder_util_pct, mem_copy_util_pct,
    cpu_pkg_watts, dram_watts,
    model_name, batch_size
FROM workload_metrics
WHERE time >= NOW() - INTERVAL %s
ORDER BY time DESC
"""

# kWh 단가 — 환경변수 미설정 시 한국 산업용 기준값 사용
ELECTRICITY_RATE_USD_KWH = float(os.getenv("ELECTRICITY_RATE_USD_KWH", "0.12"))


@dataclass
class WorkloadProfile:
    """단일 워크로드의 전력·비용·효율성 집계 결과."""

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
    # GPU 사용률 / 전력의 비율 — 높을수록 전력 대비 연산 효율 우수
    efficiency_score: float
    cost_per_hour_usd: float


def _connect():
    return psycopg2.connect(DATABASE_URL)


def fetch_raw(interval: str = "1 hour") -> pd.DataFrame:
    """workload_metrics 테이블에서 지정 구간의 원시 데이터를 로드한다.

    Args:
        interval (str): PostgreSQL INTERVAL 표현식. 예: '1 hour', '7 days'.

    Returns:
        pd.DataFrame: workload_metrics 스키마의 DataFrame. DB 오류 시 빈 DataFrame.

    Raises:
        없음 — 예외는 내부에서 처리하고 빈 DataFrame 반환.
    """
    try:
        conn = _connect()
        df = pd.read_sql(QUERY_METRICS, conn, params=(interval,))
        conn.close()
        return df
    except Exception as e:
        logger.error(f"DB 데이터 로드 실패: {e}")
        return pd.DataFrame()


def analyze(interval: str = "1 hour") -> list[WorkloadProfile]:
    """지정 구간의 워크로드별 전력·비용 프로파일을 생성한다.

    workload_id / namespace / job_type / device_type 기준으로 그룹핑하여
    평균 전력, 피크 전력, 효율성 점수, 시간당 비용을 계산한다.

    Args:
        interval (str): 분석 대상 시간 범위. PostgreSQL INTERVAL 표현식.

    Returns:
        list[WorkloadProfile]: 워크로드별 프로파일 목록. 데이터 없으면 빈 리스트.
    """
    df = fetch_raw(interval)
    if df.empty:
        return []

    profiles = []
    for (wid, ns, jtype, dtype), grp in df.groupby(
        ["workload_id", "namespace", "job_type", "device_type"]
    ):
        avg_power = grp["power_watts"].mean() or 0
        peak_power = grp["power_watts"].max() or 0
        avg_util = grp["gpu_util_pct"].mean() or 0
        avg_mem = grp["memory_used_mb"].mean() or 0
        total_energy = grp["energy_joules"].sum() or 0
        avg_temp = grp["temperature_c"].mean() or 0
        model_name = (
            grp["model_name"].dropna().iloc[0]
            if not grp["model_name"].dropna().empty
            else None
        )

        efficiency = (avg_util / avg_power) if avg_power > 0 else 0

        # 수집 주기 30초 가정 → 샘플 수로 실제 측정 시간 역산
        duration_hours = len(grp) * 30 / 3600
        cost_per_hour = (
            (avg_power / 1000) * ELECTRICITY_RATE_USD_KWH
            if duration_hours > 0
            else 0
        )

        profiles.append(
            WorkloadProfile(
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
        )

    return profiles


def node_summary(interval: str = "1 hour") -> pd.DataFrame:
    """노드별·디바이스 타입별 총 전력·비용 요약 DataFrame을 반환한다.

    Args:
        interval (str): 집계 대상 시간 범위. PostgreSQL INTERVAL 표현식.

    Returns:
        pd.DataFrame: node_name, device_type, avg_power, peak_power,
                      total_energy, avg_util, sample_count, cost_per_hour_usd 컬럼.
                      데이터 없으면 빈 DataFrame.
    """
    df = fetch_raw(interval)
    if df.empty:
        return pd.DataFrame()

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

    summary["cost_per_hour_usd"] = (
        (summary["avg_power"] / 1000) * ELECTRICITY_RATE_USD_KWH
    ).round(6)

    return summary
