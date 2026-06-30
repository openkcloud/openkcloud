"""
Performance Catalog gRPC Server

This service provides a gRPC interface to receive and store benchmark results
from various hardware accelerators into the central database.
"""

import os
import time
import logging
from concurrent import futures
from datetime import datetime, timezone

import grpc
import psycopg2
import psycopg2.pool

import benchmark_pb2
import benchmark_pb2_grpc

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
logger = logging.getLogger(__name__)


DB_DSN = os.getenv("DATABASE_URL",
    "postgresql://platform:platform1234@timescaledb.data-pipeline.svc.cluster.local:5432/workload_data"
)

INSERT_SQL = 

def get_pool():
    for attempt in range(10):
        try:
            pool = psycopg2.pool.SimpleConnectionPool(1, 5, dsn=DB_DSN)
            logger.info("DB connection pool created")
            return pool
        except Exception as e:
            logger.warning(f"DB connect attempt {attempt+1}/10 failed: {e}")
            time.sleep(3)
    raise RuntimeError("Could not connect to TimescaleDB")



class CatalogServicer(benchmark_pb2_grpc.CatalogServiceServicer):
    def __init__(self, pool: psycopg2.pool.SimpleConnectionPool):
        self.pool = pool

    def SubmitResult(self, request: benchmark_pb2.BenchmarkResult, context):
        ts = datetime.fromtimestamp(request.timestamp or time.time(), tz=timezone.utc)
        hw = request.hardware
        wl = request.workload
        mt = request.metrics

        logger.info(
            f"recv | node={request.node_id} device={hw.device_type} "
            f"model={wl.model_name} batch={wl.batch_size} "
            f"qps={mt.throughput_qps:.1f} power={mt.power_w:.1f}W source={request.source}"
        )

        conn = self.pool.getconn()
        try:
            with conn.cursor() as cur:
                cur.execute(INSERT_SQL, (
                    ts, request.node_id,
                    hw.device_name, hw.device_type, hw.vram_mb or None,
                    hw.memory_bandwidth_gbs or None, hw.driver_version or None,
                    wl.model_name, wl.task_type, wl.precision, wl.batch_size, wl.framework,
                    mt.throughput_qps, mt.latency_p50_ms, mt.latency_p95_ms, mt.latency_p99_ms,
                    mt.power_w, mt.memory_used_mb,
                    request.source or "measured",
                ))
            conn.commit()
            return benchmark_pb2.SubmitResponse(success=True, message="stored")
        except Exception as e:
            conn.rollback()
            logger.error(f"DB insert failed: {e}")
            return benchmark_pb2.SubmitResponse(success=False, message=str(e))
        finally:
            self.pool.putconn(conn)



def serve():
    pool = get_pool()
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    benchmark_pb2_grpc.add_CatalogServiceServicer_to_server(CatalogServicer(pool), server)

    port = os.getenv("GRPC_PORT", "50051")
    server.add_insecure_port(f"[::]:{port}")
    server.start()
    logger.info(f"CatalogService (gRPC) started on port {port}")
    server.wait_for_termination()


if __name__ == "__main__":
    serve()
