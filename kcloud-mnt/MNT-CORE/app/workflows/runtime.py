"""CORE 의 Temporal 연결 — 클라이언트(프로비저닝 API 가 워크플로 시작) + 워커(워크플로 코드 + DB 기록 작업).

서버 주소는 TEMPORAL_API_BASE(로컬: MNT-WORKFLOW/run-local.sh, 파드: mnt-workflow:7233).
Temporal 이 꺼져 있어도 CORE 는 정상 기동하고, 워커는 5초마다 다시 연결을 시도한다.
"""
import asyncio
import logging
import re
from concurrent.futures import ThreadPoolExecutor
from datetime import timedelta

from temporalio.client import Client, WorkflowExecutionStatus
from temporalio.worker import Worker

from ..config import settings
from . import activities
from .ai_workspace_provision import WORKFLOW_MAX as AI_WORKSPACE_MAX, AiWorkspaceProvision
from .kubernetes_provision import RUN_MAX as CLUSTER_RUN_MAX, KubernetesProvision
from .vm_provision import CORE_QUEUE, RUN_MAX, VmProvision

NAMESPACE = "default"
MAX_ACTIVITIES = 8  # 동시에 실행할 작업 수 = 스레드 수(넘치면 작업이 스레드를 기다리며 시간 제한을 못 지킨다)
log = logging.getLogger("uvicorn.error")
_client: Client | None = None


def _target() -> str:
    """gRPC 주소(host:port) — 다른 *_API_BASE 처럼 http:// 를 붙여 써도 되게 떼어 낸다."""
    return re.sub(r"^\w+://", "", settings.temporal_api_base).rstrip("/")


async def get_client() -> Client:
    global _client
    if _client is None:
        _client = await Client.connect(_target(), namespace=NAMESPACE)
    return _client


async def start_vm_provision(workflow_id: str, inp: dict) -> None:
    """VM 프로비저닝 워크플로 시작 — 같은 workflow_id 가 실행 중이면 Temporal 이 거절한다(중복 실행 방지).
    시작 요청이 오류를 냈어도 실제로는 시작됐을 수 있으므로(응답 유실), 그 워크플로가 실행 중이면 시작된 것으로 본다.
    (시작 전 취소한 회차는 번호를 다시 쓰므로, 같은 ID 의 끝난 기록은 시작된 것으로 보지 않는다)"""
    client = await get_client()
    try:
        # 워크플로 안에서 대기·진행 제한으로 끊고 실패를 기록한다 — 이 제한은 그마저 못 할 때를 위한 안전장치
        await client.start_workflow(VmProvision.run, inp, id=workflow_id, task_queue=CORE_QUEUE,
                                    execution_timeout=RUN_MAX)
    except Exception:
        try:
            if (await client.get_workflow_handle(workflow_id).describe()).status == WorkflowExecutionStatus.RUNNING:
                return
        except Exception:  # noqa: BLE001 — 정말 시작되지 않음
            pass
        raise


async def cancel_vm_queue(workflow_id: str) -> None:
    """VM 프로비저닝 시작 대기(예약) 취소 신호 — 시작 전이면 워크플로가 신청을 되돌리고 끝낸다(시작 후엔 무시)."""
    client = await get_client()
    await client.get_workflow_handle(workflow_id).signal(VmProvision.cancel_queue)


async def start_ai_workspace_provision(workflow_id: str, inp: dict) -> None:
    """AI 워크스페이스 프로비저닝 워크플로 시작 — 중복·응답 유실 처리는 VM 과 같다."""
    client = await get_client()
    try:
        await client.start_workflow(AiWorkspaceProvision.run, inp, id=workflow_id, task_queue=CORE_QUEUE,
                                    execution_timeout=AI_WORKSPACE_MAX + timedelta(minutes=5))
    except Exception:
        try:
            await client.get_workflow_handle(workflow_id).describe()
            return
        except Exception:  # noqa: BLE001 — 정말 시작되지 않음
            pass
        raise


async def start_cluster_provision(workflow_id: str, inp: dict) -> None:
    """서비스 클러스터 프로비저닝 워크플로 시작 — 중복·응답 유실 처리는 VM 과 같다(실행 중이면 시작된 것으로 본다)."""
    client = await get_client()
    try:
        await client.start_workflow(KubernetesProvision.run, inp, id=workflow_id, task_queue=CORE_QUEUE,
                                    execution_timeout=CLUSTER_RUN_MAX)
    except Exception:
        try:
            if (await client.get_workflow_handle(workflow_id).describe()).status == WorkflowExecutionStatus.RUNNING:
                return
        except Exception:  # noqa: BLE001 — 정말 시작되지 않음
            pass
        raise


async def run_worker():
    """CORE 워커 — 연결 실패·서버 재시작·네임스페이스 준비 전이면 5초 뒤 다시 시도(CORE API 기동은 막지 않음)."""
    with ThreadPoolExecutor(max_workers=MAX_ACTIVITIES) as pool:  # DB 기록 작업은 동기 함수 — 스레드에서 실행
        while True:
            try:
                client = await get_client()
                log.info("Temporal 워커 시작: %s (큐 %s)", _target(), CORE_QUEUE)
                await Worker(client, task_queue=CORE_QUEUE, workflows=[VmProvision, AiWorkspaceProvision, KubernetesProvision],
                             activities=activities.ALL,
                             activity_executor=pool, max_concurrent_activities=MAX_ACTIVITIES).run()
            except asyncio.CancelledError:
                raise
            except Exception as e:  # noqa: BLE001 — 다시 시도
                log.warning("Temporal 워커 중단 — 5초 뒤 다시 시도: %s", e)
            await asyncio.sleep(5)
