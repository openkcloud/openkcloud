"""AI 워크스페이스 신청 — 서비스 클러스터 선택(신청자 토큰을 AINB 에 넘김·프로젝트 연결 확인·공유 클러스터 네임스페이스)·
생성 시작(워크플로 입력의 클러스터) + 프로비저닝 워크플로가 고른 클러스터를 AINB 작업에 넘기는지(로컬 Temporal 개발 서버)."""
import asyncio
import shutil
from concurrent.futures import ThreadPoolExecutor

import pytest
from conftest import PROJECT, add_member, bearer, make_token, sql
from fastapi import HTTPException
from temporalio import activity
from temporalio.client import WorkflowFailureError
from temporalio.exceptions import ApplicationError
from temporalio.testing import WorkflowEnvironment
from temporalio.worker import Worker

from app.config import settings
from app.routers import tickets
from app.workflows import ai_workspace_provision as awp
from app.workflows import runtime
from app.workflows.provision_base import CORE_QUEUE

ADMIN = bearer(make_token("admin"))
BODY = {"projectId": PROJECT, "purpose": "시험", "cpu": 2, "memoryGi": 4, "acceleratorCount": 0, "storageGi": 10}


@pytest.fixture(autouse=True)
def _clean_ws_tickets():
    yield
    sql("DELETE FROM tkt_request WHERE type = 'ai-workspace'")


@pytest.fixture
def fake(monkeypatch):
    """MNT-IAAS·MNT-PAAS·MNT-AINB 응답 — AINB 호출(경로·토큰)을 모은다."""
    state = {"ainb": [], "check": {"available": True, "reason": None}}

    def iaas(path, token):
        assert path == "/projects", path
        return {"items": [{"id": PROJECT, "name": "okc_test_prj"}]}

    def paas(path, token):
        if path == "/clusters/c-one":
            return {"id": "c-one", "name": "gpu-cluster"}
        raise HTTPException(404, "없음")

    def ainb(path, token=None):
        state["ainb"].append((path, token))
        return state["check"]

    monkeypatch.setattr(tickets, "_iaas", iaas)
    monkeypatch.setattr(tickets, "_paas", paas)
    monkeypatch.setattr(tickets, "_ainb", ainb)
    return state


def _user():
    add_member("ek.lee")
    token = make_token("ek.lee")
    return token, bearer(token)


def test_클러스터를_고르지_않으면_예전처럼_신청(client, fake):
    token, headers = _user()
    r = client.post("/api/core/tickets/ai-workspace", json=BODY, headers=headers)
    assert r.status_code == 201, r.text
    item = r.json()
    assert item["workspaceName"] == "okc-test-prj" and "clusterId" not in item
    path, sent = fake["ainb"][0]
    assert path == f"/workspace-name-check?name=okc-test-prj&projectId={PROJECT}"
    assert sent == token  # 신청자 토큰을 넘긴다(AINB 가 클러스터 접속 정보를 받을 때 쓴다)


def test_프로젝트에_연결된_클러스터를_고르면_신청에_남는다(client, fake):
    sql("INSERT INTO paas_project_cluster (project_id, cluster_id) VALUES (:p, 'c-one')", p=PROJECT)
    token, headers = _user()
    r = client.post("/api/core/tickets/ai-workspace", json={**BODY, "clusterId": "c-one"}, headers=headers)
    assert r.status_code == 201, r.text
    item = r.json()
    assert (item["clusterId"], item["clusterName"], item["workspaceName"]) == ("c-one", "gpu-cluster", "okc-test-prj")
    path, sent = fake["ainb"][0]
    assert path.endswith("&clusterId=c-one") and sent == token


def test_공유_클러스터는_연결된_네임스페이스를_워크스페이스로(client, fake):
    sql("INSERT INTO paas_project_cluster (project_id, cluster_id, namespace) VALUES (:p, 'c-one', 'shared-ns')",
        p=PROJECT)
    _, headers = _user()
    r = client.post("/api/core/tickets/ai-workspace", json={**BODY, "clusterId": "c-one"}, headers=headers)
    assert r.status_code == 201, r.text
    assert r.json()["workspaceName"] == "shared-ns"
    assert fake["ainb"][0][0].startswith("/workspace-name-check?name=shared-ns&")


def test_연결되지_않은_클러스터는_사용자는_거부_관리자는_허용(client, fake):
    _, headers = _user()
    r = client.post("/api/core/tickets/ai-workspace", json={**BODY, "clusterId": "c-one"}, headers=headers)
    assert r.status_code == 400 and "연결된 서비스 클러스터가 아닙니다" in r.json()["detail"]
    assert fake["ainb"] == []  # 거부되면 AINB 를 부르지 않는다
    r = client.post("/api/core/tickets/ai-workspace", json={**BODY, "clusterId": "c-one"}, headers=ADMIN)
    assert r.status_code == 201, r.text
    assert r.json()["clusterId"] == "c-one"


def test_AINB_가_만들_수_없다고_하면_사유와_함께_거부(client, fake):
    fake["check"] = {"available": False, "reason": "이 서비스 클러스터에 접근할 권한이 없습니다"}
    sql("INSERT INTO paas_project_cluster (project_id, cluster_id) VALUES (:p, 'c-one')", p=PROJECT)
    _, headers = _user()
    r = client.post("/api/core/tickets/ai-workspace", json={**BODY, "clusterId": "c-one"}, headers=headers)
    assert r.status_code == 409 and "접근할 권한이 없습니다" in r.json()["detail"]


def test_생성_시작은_고른_클러스터를_워크플로에_넘긴다(client, fake, monkeypatch):
    started: list[tuple] = []

    async def start(workflow_id, inp):
        started.append((workflow_id, inp))

    monkeypatch.setattr(settings, "temporal_api_base", "temporal.test:7233")
    monkeypatch.setattr(runtime, "start_ai_workspace_provision", start)
    sql("INSERT INTO paas_project_cluster (project_id, cluster_id) VALUES (:p, 'c-one')", p=PROJECT)
    _, headers = _user()
    tid = client.post("/api/core/tickets/ai-workspace", json={**BODY, "clusterId": "c-one"}, headers=headers).json()["id"]
    sql("UPDATE tkt_request SET status = 'approved' WHERE id = :id", id=tid)
    r = client.post(f"/api/core/tickets/{tid}/provision-ai-workspace",
                    json={"processedBy": "admin", "expectedStatus": "approved"}, headers=ADMIN)
    assert r.status_code == 200, r.text
    workflow_id, inp = started[0]
    assert workflow_id == f"ai-workspace-provision-{tid}-1"
    assert (inp["clusterId"], inp["workspaceName"], inp["cpu"]) == ("c-one", "okc-test-prj", 2)


# ── 프로비저닝 워크플로 — 고른 클러스터가 AINB 작업(생성·대기·정리)에 넘어가는지 ─────────

PLAN = ([{"step": "check_request", "sub": s} for s in ("check_name", "check_quota")]
        + [{"step": "create_workspace", "sub": s} for s in ("create_profile", "apply_quota")]
        + [{"step": "check_ready", "sub": "check_namespace"}])


@pytest.mark.skipif(not shutil.which("temporal"), reason="temporal CLI 없음 — brew install temporal")
def test_프로비저닝_워크플로는_클러스터를_AINB_작업에_넘긴다():
    calls: list[tuple] = []

    def wid():
        return activity.info().workflow_id

    @activity.defn(name="core.init_steps_ainb")
    def init_steps_ainb(ticket_id, attempt):
        return PLAN

    @activity.defn(name="core.update_steps")
    def update_steps(ticket_id, attempt, changes):
        pass

    @activity.defn(name="core.finish")
    def finish(ticket_id, status, result, error):
        calls.append(("finish", wid(), status, error))

    @activity.defn(name="ainb.create_workspace")
    def create_workspace(spec):
        calls.append(("create", wid(), spec["name"], spec["clusterId"]))
        return {"namespace": spec["name"], "created": True}

    @activity.defn(name="ainb.wait_ready")
    def wait_ready(name, deadline, cluster_id):
        calls.append(("wait", wid(), name, cluster_id))
        if wid() == "wf-not-ready":
            raise ApplicationError("제한 시간 안에 준비되지 않았습니다(시험)", non_retryable=True)
        return {"namespace": name, "quota": {"cpu": "2"}, "accelerator": "rebellions.ai/ATOM"}

    @activity.defn(name="ainb.delete_workspace")
    def delete_workspace(name, project_id, cluster_id):
        calls.append(("delete", wid(), name, project_id, cluster_id))
        return True

    def inp(tid, cluster):
        return {"ticketId": tid, "attempt": 1, "projectId": "p-1", "projectName": "okc_test_prj",
                "workspaceName": "okc-test-prj", "ownerEmail": "ek.lee@test", "cpu": 2, "memoryGi": 4,
                "acceleratorCount": 0, "storageGi": 10, **({"clusterId": cluster} if cluster else {})}

    async def run():
        async with await WorkflowEnvironment.start_local(dev_server_existing_path=shutil.which("temporal")) as env:
            with ThreadPoolExecutor(4) as pool:
                async with Worker(env.client, task_queue=CORE_QUEUE, workflows=[awp.AiWorkspaceProvision],
                                  activities=[init_steps_ainb, update_steps, finish], activity_executor=pool), \
                           Worker(env.client, task_queue=awp.AINB_QUEUE, activity_executor=pool,
                                  activities=[create_workspace, wait_ready, delete_workspace]):
                    ok = await env.client.execute_workflow(awp.AiWorkspaceProvision.run, inp("wf-ok", "c-one"),
                                                           id="wf-ok", task_queue=CORE_QUEUE)
                    old = await env.client.execute_workflow(awp.AiWorkspaceProvision.run, inp("wf-old", None),
                                                            id="wf-old", task_queue=CORE_QUEUE)
                    with pytest.raises(WorkflowFailureError):
                        await env.client.execute_workflow(awp.AiWorkspaceProvision.run, inp("wf-not-ready", "c-one"),
                                                          id="wf-not-ready", task_queue=CORE_QUEUE)
                    return ok, old

    ok, old = asyncio.run(run())
    assert ok["namespace"] == old["namespace"] == "okc-test-prj"
    assert ("create", "wf-ok", "okc-test-prj", "c-one") in calls
    assert ("wait", "wf-ok", "okc-test-prj", "c-one") in calls
    # 클러스터 없이 접수된 예전 신청 — 빈 값(AINB 기본 클러스터)
    assert ("create", "wf-old", "okc-test-prj", "") in calls and ("wait", "wf-old", "okc-test-prj", "") in calls
    # 준비 실패 — 이번 회차가 만든 워크스페이스를 같은 클러스터에서 정리
    assert ("delete", "wf-not-ready", "okc-test-prj", "p-1", "c-one") in calls
    fin = next(c for c in calls if c[:2] == ("finish", "wf-not-ready"))
    assert fin[2] == "failed" and "정리함" in fin[3]
