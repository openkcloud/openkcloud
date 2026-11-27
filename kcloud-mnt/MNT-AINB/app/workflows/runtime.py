"""AINB 의 Temporal 워커 — Kubeflow 작업(activities)만 실행한다. 워크플로 코드는 MNT-CORE 워커가 실행.

서버 주소는 TEMPORAL_API_BASE(로컬: MNT-WORKFLOW/run-local.sh, 파드: mnt-workflow:7233).
Temporal 이 꺼져 있어도 AINB 는 정상 기동하고, 워커는 5초마다 다시 연결을 시도한다.
"""
import asyncio
import logging
import re
from concurrent.futures import ThreadPoolExecutor

from temporalio.client import Client
from temporalio.worker import Worker

from . import activities

NAMESPACE = "default"
TASK_QUEUE = "okc-ainb"  # MNT-CORE 워크플로가 Kubeflow 작업을 넣는 큐
MAX_ACTIVITIES = 4
log = logging.getLogger("uvicorn.error")


def _target(base: str) -> str:
    """gRPC 주소(host:port) — 다른 *_API_BASE 처럼 http:// 를 붙여 써도 되게 떼어 낸다."""
    return re.sub(r"^\w+://", "", base).rstrip("/")


async def run_worker(base: str):
    """AINB 워커 — 연결 실패·서버 재시작·네임스페이스 준비 전이면 5초 뒤 다시 시도(AINB API 기동은 막지 않음)."""
    with ThreadPoolExecutor(max_workers=MAX_ACTIVITIES) as pool:  # 쿠버네티스 호출은 동기 함수 — 스레드에서 실행
        while True:
            try:
                client = await Client.connect(_target(base), namespace=NAMESPACE)
                log.info("Temporal 워커 시작: %s (큐 %s)", _target(base), TASK_QUEUE)
                await Worker(client, task_queue=TASK_QUEUE, activities=activities.ALL, activity_executor=pool,
                             max_concurrent_activities=MAX_ACTIVITIES).run()
            except asyncio.CancelledError:
                raise
            except Exception as e:  # noqa: BLE001 — 다시 시도
                log.warning("Temporal 워커 중단 — 5초 뒤 다시 시도: %s", e)
            await asyncio.sleep(5)
