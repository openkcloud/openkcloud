"""VM 프로비저닝 시작 대기(예약) — 시작 자리 판정·예상 대기·되돌리기·진행 조회·예약 취소 API·워크플로 흐름."""
import asyncio
import shutil
from concurrent.futures import ThreadPoolExecutor
from datetime import timedelta

import pytest
from conftest import PROJECT, bearer, make_token, sql
from temporalio import activity
from temporalio.client import WorkflowFailureError
from temporalio.exceptions import ApplicationError
from temporalio.testing import WorkflowEnvironment
from temporalio.worker import UnsandboxedWorkflowRunner, Worker

from app.config import settings
from app.db import SessionLocal
from app.workflows import activities, provision_queue, runtime, vm_provision
from app.workflows.provision_queue import LEFT_MIN, estimate, expected_wait, has_room

SPEC = {"networkId": "net-1"}


def admin() -> dict:
    return bearer(make_token("admin"))


def ticket(tid: str, status: str = "provisioning", ago: float = 0, image: str = "img-a", attempt: int = 1,
           expected: int | None = None):
    """VM 신청 1건 — ago 초 전에 프로비저닝을 누른 것으로(그때 예상 대기 expected 초)."""
    sql("""INSERT INTO tkt_request (id, project_id, requested_by, request_spec, type, status, provisioning_at, workflow_id,
                                    queue_expected_sec)
           SELECT :id, :p, id, jsonb_build_object('imageId', CAST(:img AS text), 'flavorId', 'f-1'), 'vm', :st,
                  now() - make_interval(secs => :ago), :wf, :exp
             FROM iam_user WHERE username = 'admin'""",
        id=tid, p=PROJECT, img=image, st=status, ago=ago, wf=f"vm-provision-{tid}-{attempt}", exp=expected)


def running(tid: str, image: str = "img-a", elapsed: float | None = None):
    """진행 중(현재 회차 단계 등록) — elapsed 초 전에 첫 단계 시작."""
    ticket(tid, image=image)
    activities.init_steps(tid, 1, False, False)
    if elapsed is not None:
        sql("UPDATE tkt_provision_step SET started_at = now() - make_interval(secs => :s) WHERE request_id = :id",
            id=tid, s=elapsed)


def completed(tid: str, image: str, sec: float):
    """완료 기록 — 진행 시간 sec 초."""
    ticket(tid, status="completed", image=image)
    activities.init_steps(tid, 1, False, False)
    sql("""UPDATE tkt_provision_step SET started_at = now() - make_interval(secs => :s), ended_at = now()
            WHERE request_id = :id""", id=tid, s=sec)


def test_예상_대기_계산():
    assert estimate([], [], 8) == 0                 # 자리 있음
    assert estimate([30, 50], [], 2) == 30          # 먼저 끝나는 자리
    assert estimate([30, 50], [40], 2) == 50        # 앞선 1건이 30초 자리에 들어가면 다음 자리는 50초
    assert estimate([30, 50, 90], [], 2) == 50      # 자리보다 1건 많음 — 2건이 끝나야 자리가 난다
    assert estimate([-5], [], 1) == LEFT_MIN        # 평소보다 오래 걸리는 중 — 곧 끝난다고 본다


def test_시작_자리는_먼저_누른_순서():
    running("req-990101-101")
    ticket("req-990101-102", ago=50)
    ticket("req-990101-103", ago=10)
    with SessionLocal() as db:
        assert has_room(db, "req-990101-102", 2) and not has_room(db, "req-990101-103", 2)
    assert activities.init_steps("req-990101-103", 1, False, False, 2) is None  # 뒤에 누른 신청은 기다린다
    assert activities.init_steps("req-990101-102", 1, False, False, 2)          # 먼저 누른 신청 시작
    assert activities.init_steps("req-990101-103", 1, False, False, 2) is None  # 이제 2건 진행 중
    assert activities.init_steps("req-990101-102", 1, False, False, 2)          # 다시 불러도(재시도) 등록된 plan


def test_대기_제한이_지난_기록은_자리를_막지_않는다():
    ticket("req-990101-104", ago=vm_provision.QUEUE_WAIT_MAX.total_seconds() + 120)
    ticket("req-990101-105", ago=5)
    with SessionLocal() as db:
        assert has_room(db, "req-990101-105", 1)


def test_예상_대기는_같은_이미지의_완료_기록으로(monkeypatch):
    monkeypatch.setattr(provision_queue, "PROVISION_MAX", 1)
    completed("req-990101-106", "img-a", 200)
    completed("req-990101-107", "img-b", 20)
    running("req-990101-108", image="img-a", elapsed=50)
    with SessionLocal() as db:
        assert 148 <= expected_wait(db) <= 152  # img-a 200초 중 50초 지남


def test_진행_조회는_시작_대기면_대기_정보(client, monkeypatch):
    monkeypatch.setattr(provision_queue, "PROVISION_MAX", 1)
    running("req-990101-109")
    ticket("req-990101-110", ago=7, expected=5)
    r = client.get("/api/core/tickets/req-990101-110/steps", headers=admin()).json()
    assert r["items"] == [] and r["queue"]["waitedSec"] in (7, 8)
    assert r["queue"]["expectedSec"] == 5  # 누른 시점의 예상 그대로(지금까지 기다린 시간이 넘어도)
    assert client.get("/api/core/tickets/req-990101-109/steps", headers=admin()).json()["queue"] is None
    # 누를 때는 자리가 있었는데(0) 그사이 다른 신청이 먼저 들어감 — 기다린 시간 + 지금 본 예상
    # (완료 기록 없음 → 1건당 기본값, 진행 중 109 뒤에 먼저 기다린 110 까지)
    ticket("req-990101-117", ago=4, expected=0)
    r = client.get("/api/core/tickets/req-990101-117/steps", headers=admin()).json()["queue"]
    assert r["expectedSec"] == r["waitedSec"] + 2 * provision_queue.TYPICAL_DEFAULT


def test_예상_대기가_대기_제한_이상이면_예약하지_않는다(client, monkeypatch):
    monkeypatch.setattr(settings, "temporal_api_base", "temporal.test:7233")
    monkeypatch.setattr(provision_queue, "PROVISION_MAX", 1)
    monkeypatch.setattr(provision_queue, "TYPICAL_DEFAULT", 600)
    running("req-990101-111")
    ticket("req-990101-112", status="approved")
    r = client.post("/api/core/tickets/req-990101-112/provision", json={"provisionSpec": SPEC}, headers=admin())
    assert r.status_code == 409
    assert r.json()["detail"] == "워크플로가 모두 동작중 입니다. 다음에 다시 시도해주세요(예상 대기시간 5분 이상)"
    assert sql("SELECT status FROM tkt_request WHERE id = 'req-990101-112'").scalar() == "approved"


def test_시작_전_취소는_누르기_전으로_되돌린다():
    ticket("req-990101-113")
    prev = {"status": "failed", "provisionSpec": {"networkId": "net-0"}, "provisioningBy": None,
            "provisioningAt": "2026-10-01T09:00:00+09:00", "workflowId": "vm-provision-req-990101-113-0",
            "errorMessage": "이전 실패", "result": {"volumeId": "v-1"}}
    activities.cancel_queued("req-990101-113", "vm-provision-req-990101-113-9", prev)  # 다른 회차 — 그대로
    assert sql("SELECT status FROM tkt_request WHERE id = 'req-990101-113'").scalar() == "provisioning"
    activities.cancel_queued("req-990101-113", "vm-provision-req-990101-113-1", prev)
    row = sql("""SELECT status, provision_spec, workflow_id, error_message, result, provisioning_at = :at AS same_at
                   FROM tkt_request WHERE id = 'req-990101-113'""", at=prev["provisioningAt"]).mappings().one()
    assert dict(row) == {"status": "failed", "provision_spec": {"networkId": "net-0"},
                         "workflow_id": "vm-provision-req-990101-113-0", "error_message": "이전 실패",
                         "result": {"volumeId": "v-1"}, "same_at": True}


def test_예약_취소_API(client, monkeypatch):
    sent: list[str] = []

    async def fake_cancel(workflow_id: str):
        sent.append(workflow_id)

    monkeypatch.setattr(runtime, "cancel_vm_queue", fake_cancel)
    ticket("req-990101-114")
    running("req-990101-115")
    assert client.post("/api/core/tickets/req-990101-114/provision/cancel",
                       headers=bearer(make_token())).status_code == 403  # 관리자만
    assert client.post("/api/core/tickets/req-990101-114/provision/cancel", headers=admin()).status_code == 202
    assert sent == ["vm-provision-req-990101-114-1"]
    r = client.post("/api/core/tickets/req-990101-115/provision/cancel", headers=admin())
    assert r.status_code == 409 and r.json()["detail"] == "이미 시작되어 취소할 수 없습니다"


# ── 워크플로 흐름(로컬 Temporal 개발 서버 + 가짜 작업) ─────────────────────
@pytest.mark.skipif(not shutil.which("temporal"), reason="temporal CLI 없음 — brew install temporal")
def test_워크플로_시작_대기(monkeypatch):
    monkeypatch.setattr(vm_provision, "ADMIT_POLL", timedelta(milliseconds=200))
    monkeypatch.setattr(vm_provision, "QUEUE_WAIT_MAX", timedelta(seconds=3))
    asyncio.run(_workflow_scenarios())


async def _workflow_scenarios():
    calls: list[tuple] = []
    full = {"wf-admit": 2, "wf-cancel": 10**6, "wf-timeout": 10**6}  # 자리가 없다고 답할 횟수

    @activity.defn(name="core.init_steps")
    def init_steps(ticket_id, attempt, has_accelerator, has_fip, limit=None):
        calls.append(("init", ticket_id, limit))
        if full[ticket_id] > 0:
            full[ticket_id] -= 1
            return None
        return [{"step": "create_server", "sub": "check_flavor"}]

    @activity.defn(name="core.cancel_queued")
    def cancel_queued(ticket_id, workflow_id, prev):
        calls.append(("cancel", ticket_id, workflow_id, prev))

    @activity.defn(name="core.update_steps")
    def update_steps(ticket_id, attempt, changes):
        pass

    @activity.defn(name="core.finish")
    def finish(ticket_id, status, result, error):
        calls.append(("finish", ticket_id, status, error))

    @activity.defn(name="iaas.check_flavor")
    def check_flavor(flavor_id):
        raise ApplicationError("플레이버 없음(시험)", non_retryable=True)

    def inp(tid):
        return {"ticketId": tid, "attempt": 1, "hasAccelerator": False, "flavorId": "f-1", "prev": {"status": "approved"}}

    async with await WorkflowEnvironment.start_local(dev_server_existing_path=shutil.which("temporal")) as env:
        with ThreadPoolExecutor(4) as pool:
            async with Worker(env.client, task_queue=vm_provision.CORE_QUEUE, workflows=[vm_provision.VmProvision],
                              activities=[init_steps, cancel_queued, update_steps, finish], activity_executor=pool,
                              workflow_runner=UnsandboxedWorkflowRunner()), \
                       Worker(env.client, task_queue=vm_provision.IAAS_QUEUE, activities=[check_flavor],
                              activity_executor=pool):
                run = vm_provision.VmProvision.run
                # 1) 두 번 기다린 뒤 자리가 나면 시작 — 이후 단계(가짜 실패)까지 진행
                with pytest.raises(WorkflowFailureError):
                    await env.client.execute_workflow(run, inp("wf-admit"), id="wf-admit", task_queue=vm_provision.CORE_QUEUE)
                assert [c for c in calls if c[1] == "wf-admit" and c[0] == "init"] == [("init", "wf-admit", 8)] * 3
                assert ("finish", "wf-admit", "failed", "플레이버 없음(시험)") in calls

                # 2) 기다리는 중 취소 — 되돌리기 작업을 부르고 실패 기록 없이 끝
                h = await env.client.start_workflow(run, inp("wf-cancel"), id="wf-cancel", task_queue=vm_provision.CORE_QUEUE)
                while not any(c[:2] == ("init", "wf-cancel") for c in calls):
                    await asyncio.sleep(0.05)
                await h.signal(vm_provision.VmProvision.cancel_queue)
                assert await h.result() == {"cancelled": True}
                assert ("cancel", "wf-cancel", "wf-cancel", {"status": "approved"}) in calls
                assert not any(c[:2] == ("finish", "wf-cancel") for c in calls)

                # 3) 대기 제한 안에 자리가 안 나면 실패로 마감
                with pytest.raises(WorkflowFailureError):
                    await env.client.execute_workflow(run, inp("wf-timeout"), id="wf-timeout", task_queue=vm_provision.CORE_QUEUE)
                fin = [c for c in calls if c[:2] == ("finish", "wf-timeout")]
                assert len(fin) == 1 and fin[0][2] == "failed" and "안에 시작하지 못했습니다" in fin[0][3]


def fake_provision(client, monkeypatch, tid: str):
    """프로비저닝 API 를 MNT-IAAS 확인·Temporal 시작 없이 — 부를 때마다 (워크플로 ID, 워크플로 입력)을 돌려준다."""
    from app.routers import tickets
    started: list[tuple[str, dict]] = []

    async def fake_start(workflow_id: str, inp: dict):
        started.append((workflow_id, inp))

    monkeypatch.setattr(settings, "temporal_api_base", "temporal.test:7233")
    monkeypatch.setattr(runtime, "start_vm_provision", fake_start)
    monkeypatch.setattr(tickets, "_verified_provision_spec", lambda *a, **k: {
        "networkId": "net-1", "networkName": "net", "networkKind": "internal", "securityGroups": []})
    monkeypatch.setattr(tickets, "_server_name_taken", lambda *a, **k: False)
    sql("UPDATE tkt_request SET request_spec = request_spec || '{\"vmName\": \"vm-x\", \"diskGb\": 10}' WHERE id = :id",
        id=tid)

    def provision():
        r = client.post(f"/api/core/tickets/{tid}/provision", json={"provisionSpec": SPEC}, headers=admin())
        assert r.status_code == 200
        return started[-1]
    return provision


def test_프로비저닝을_누른_시점의_예상_대기를_남긴다(client, monkeypatch):
    monkeypatch.setattr(provision_queue, "PROVISION_MAX", 1)
    ticket("req-990101-118", status="approved")
    provision = fake_provision(client, monkeypatch, "req-990101-118")
    provision()  # 자리 있음 — 바로 시작
    assert sql("SELECT queue_expected_sec FROM tkt_request WHERE id = 'req-990101-118'").scalar() == 0
    running("req-990101-119")
    ticket("req-990101-120", status="approved")
    # 진행 중 119 + 앞서 기다리는 118(단계 등록 전) 뒤 — 완료 기록 없음 → 1건당 기본값
    fake_provision(client, monkeypatch, "req-990101-120")()
    assert sql("SELECT queue_expected_sec FROM tkt_request WHERE id = 'req-990101-120'").scalar() \
        == 2 * provision_queue.TYPICAL_DEFAULT


def test_예약_취소로_되돌리면_시도_횟수는_그대로(client, monkeypatch):
    tid = "req-990101-116"
    ticket(tid, status="failed")  # 1회차 실패
    activities.init_steps(tid, 1, False, False)
    retry = fake_provision(client, monkeypatch, tid)

    wf, inp = retry()
    assert wf == f"vm-provision-{tid}-2"
    activities.cancel_queued(tid, wf, inp["prev"])  # 시작 전 취소 — 실패로 되돌림
    r = client.get(f"/api/core/tickets/{tid}/steps", headers=admin()).json()
    assert r["status"] == "failed" and r["attempt"] == 1
    assert retry()[0] == f"vm-provision-{tid}-2"  # 다시 재시도해도 2회차(취소한 회차는 세지 않는다)
