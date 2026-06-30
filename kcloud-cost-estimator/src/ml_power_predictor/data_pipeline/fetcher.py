"""
Database metrics fetcher

This module connects to TimescaleDB to load raw workload metrics
for ML training and analysis.
"""
import logging
import os
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
    energy_joules, sm_clock_mhz, mem_clock_mhz,
    encoder_util_pct, decoder_util_pct, mem_copy_util_pct, mem_util_pct,
    cpu_pkg_watts, dram_watts,
    model_name, batch_size, precision, model_params_b, estimated_tflops
FROM workload_metrics
WHERE time >= NOW() - INTERVAL %s
ORDER BY time DESC
"""

def _connect():
    return psycopg2.connect(DATABASE_URL)

def fetch_raw(interval: str = "1 hour") -> pd.DataFrame:
    try:
        conn = _connect()
        df = pd.read_sql(QUERY_METRICS, conn, params=(interval,))
        conn.close()
        return df
    except Exception as e:
        logger.error(f"Failed to load data: {e}")
        return pd.DataFrame()
