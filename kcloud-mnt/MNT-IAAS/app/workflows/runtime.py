"""IAAS 의 Temporal 워커 — OpenStack 작업(activities)만 실행한다. 워크플로 코드는 MNT-CORE 워커가 실행.

서버 주소는 TEMPORAL_API_BASE(로컬: MNT-WORKFLOW/run-local.sh, 파드: mnt-workflow:7233).
Temporal 이 꺼져 있어도 IAAS 는 정상 기동하고, 워커는 5초마다 다시 연결을 시도한다.
"""
import asyncio
import logging
import re
from concurrent.futures import ThreadPoolExecutor
from datetime import timedelta

from temporalio.client import Client
from temporalio.worker import Worker

from ..config import settings
from . import activities

NAMESPACE = "default"
TASK_QUEUE = "okc-iaas"  # MNT-CORE 워크플로가 OpenStack 작업을 넣는 큐
MAX_ACTIVITIES = 8  # 동시에 실행할 작업 수 = 스레드 수(넘치면 작업이 스레드를 기다리며 시간 제한을 못 지킨다)
# 재기동(--reload·파드 교체) 때 실행 중인 작업이 끝나기를 기다리는 시간 — 생성 요청이 중간에 끊겨 만들어진 VM·볼륨을
# 놓치지 않게. 파드 기본 종료 유예(terminationGracePeriodSeconds 30초) 안에 끝나도록 25초 — 그 뒤에는 강제 종료된다.
# 생성 요청은 보통 몇 초면 끝나고(Nova 요청 제한은 60초), 대기 작업(watch·wait·cleanup)은 끊겨도 하트비트가 끊기면
# 다른 파드에서 다시 실행된다.
GRACEFUL_SHUTDOWN = timedelta(seconds=25)
log = logging.getLogger("uvicorn.error")


def _target() -> str:
    """gRPC 주소(host:port) — 다른 *_API_BASE 처럼 http:// 를 붙여 써도 되게 떼어 낸다."""
    return re.sub(r"^\w+://", "", settings.temporal_api_base).rstrip("/")


async def run_worker():
    """IAAS 워커 — 연결 실패·서버 재시작·네임스페이스 준비 전이면 5초 뒤 다시 시도(IAAS API 기동은 막지 않음)."""
    with ThreadPoolExecutor(max_workers=MAX_ACTIVITIES) as pool:  # OpenStack 호출은 동기 함수 — 스레드에서 실행
        while True:
            try:
                client = await Client.connect(_target(), namespace=NAMESPACE)
                log.info("Temporal 워커 시작: %s (큐 %s)", _target(), TASK_QUEUE)
                await Worker(client, task_queue=TASK_QUEUE, activities=activities.ALL, activity_executor=pool,
                             max_concurrent_activities=MAX_ACTIVITIES, graceful_shutdown_timeout=GRACEFUL_SHUTDOWN).run()
            except asyncio.CancelledError:
                raise
            except Exception as e:  # noqa: BLE001 — 다시 시도
                log.warning("Temporal 워커 중단 — 5초 뒤 다시 시도: %s", e)
            await asyncio.sleep(5)
