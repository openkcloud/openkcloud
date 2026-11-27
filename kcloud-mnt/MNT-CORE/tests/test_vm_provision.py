"""VM 프로비저닝 워크플로 — 부팅 볼륨 준비 대기(5분씩 2번, 넘기면 직접 정리). 로컬 Temporal 개발 서버 + 가짜 작업."""
import asyncio
import shutil
from concurrent.futures import ThreadPoolExecutor

import pytest
from temporalio import activity
from temporalio.client import WorkflowFailureError
from temporalio.exceptions import ApplicationError
from temporalio.testing import WorkflowEnvironment
from temporalio.worker import UnsandboxedWorkflowRunner, Worker

from app.workflows import vm_provision

PLAN = [{"step": "create_server", "sub": s}
        for s in ("check_flavor", "check_image", "set_keypair", "create_boot_volume", "send_request", "schedule_host")]


@pytest.mark.skipif(not shutil.which("temporal"), reason="temporal CLI 없음 — brew install temporal")
def test_부팅_볼륨_준비_대기():
    asyncio.run(_scenarios())


async def _scenarios():
    calls: list[tuple] = []
    waits = {"wf-ready": [None], "wf-late": ["downloading", None], "wf-timeout": ["downloading", "downloading"]}

    @activity.defn(name="core.init_steps")
    def init_steps(ticket_id, attempt, has_accelerator, has_fip, limit=None):
        return PLAN

    @activity.defn(name="core.update_steps")
    def update_steps(ticket_id, attempt, changes):
        pass

    @activity.defn(name="core.finish")
    def finish(ticket_id, status, result, error):
        calls.append(("finish", ticket_id, status, result, error))

    @activity.defn(name="iaas.check_flavor")
    def check_flavor(flavor_id):
        return "m1.small"

    @activity.defn(name="iaas.check_image")
    def check_image(image_id):
        return "ubuntu"

    @activity.defn(name="iaas.create_boot_volume")
    def create_boot_volume(project_id, name, size_gb, image_id):
        return "vol-1"

    @activity.defn(name="iaas.wait_volume")
    def wait_volume(project_id, volume_id, deadline):
        tid = activity.info().workflow_id
        calls.append(("wait", tid))
        return waits[tid].pop(0)

    @activity.defn(name="iaas.create_server")
    def create_server(project_id, spec, volume_id):
        calls.append(("create", spec.get("keypairName")))
        raise ApplicationError("VM 생성 중단(시험)", non_retryable=True)  # 볼륨 다음 단계까지 왔는지만 본다

    @activity.defn(name="iaas.cleanup")
    def cleanup(project_id, res, deadline):
        calls.append(("cleanup", res))
        return ["부팅 볼륨"]

    def inp(tid):
        return {"ticketId": tid, "attempt": 1, "hasAccelerator": False, "projectId": "p-1", "flavorId": "f-1",
                "imageId": "i-1", "keypairLabel": "미지정", "vmName": "vm-1", "volumeGb": 20, "networkId": "net-1",
                "fixedIp": None, "securityGroups": [], "publicKey": "ssh-ed25519 AAAA", "keypairName": "ek-key",
                "prev": {}}

    async with await WorkflowEnvironment.start_local(dev_server_existing_path=shutil.which("temporal")) as env:
        with ThreadPoolExecutor(4) as pool:
            async with Worker(env.client, task_queue=vm_provision.CORE_QUEUE, workflows=[vm_provision.VmProvision],
                              activities=[init_steps, update_steps, finish], activity_executor=pool,
                              workflow_runner=UnsandboxedWorkflowRunner()), \
                       Worker(env.client, task_queue=vm_provision.IAAS_QUEUE, activity_executor=pool,
                              activities=[check_flavor, check_image, create_boot_volume, wait_volume, create_server,
                                          cleanup]):
                for tid in waits:
                    with pytest.raises(WorkflowFailureError):
                        await env.client.execute_workflow(vm_provision.VmProvision.run, inp(tid), id=tid,
                                                          task_queue=vm_provision.CORE_QUEUE)

    def fin(tid):
        return next(c for c in calls if c[:2] == ("finish", tid))

    # 1번째 대기 안에 준비 / 2번째 대기 안에 준비 — 다음 단계(VM 생성 요청)로 이어지고, 그 실패는 평소처럼 정리
    assert calls.count(("wait", "wf-ready")) == 1 and calls.count(("wait", "wf-late")) == 2
    for tid in ("wf-ready", "wf-late"):
        assert fin(tid)[4].startswith("VM 생성 중단(시험)") and "정리함" in fin(tid)[4]
    assert [c for c in calls if c[0] == "create"] == [("create", "ek-key")] * 2  # 키 이름을 생성 요청에 넘긴다(상세 표시)
    # 2번 다 넘김 — 안내 문구로 실패, 만드는 중인 볼륨은 정리하지 않고 결과에 남긴다
    assert calls.count(("wait", "wf-timeout")) == 2
    _, _, status, result, error = fin("wf-timeout")
    assert status == "failed" and result == {"volumeId": "vol-1"}
    assert error == "볼륨 생성 시간이 초과되었습니다. 관리자에게 문의하세요 — 부팅 볼륨 직접 정리 필요(volumeId=vol-1)"
    assert len([c for c in calls if c[0] == "cleanup"]) == 2  # wf-ready·wf-late 만
