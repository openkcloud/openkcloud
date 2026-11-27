"""서비스 클러스터 프로비저닝 워크플로 — 정상 완료·생성 실패 정리·생성 요청 실패·사전 확인 실패·정리 실패·조기 실패(노드 VM
ERROR)·K8s API 접근 실패·로컬 설정 단계 건너뜀 + 조기 실패 판정(judge).
로컬 Temporal 개발 서버 + 가짜 작업. 운영과 같은 샌드박스 러너로 띄워 공통 파일 포함 import 위반도 확인한다."""
import asyncio
import shutil
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timedelta

import pytest
from temporalio import activity
from temporalio.client import WorkflowFailureError
from temporalio.exceptions import ApplicationError
from temporalio.testing import WorkflowEnvironment
from temporalio.worker import Worker

from app.workflows import kubernetes_provision as kp
from app.workflows import vm_provision
from app.workflows.provision_base import CORE_QUEUE

PLAN = ([{"step": "check_creatable", "sub": s} for s in ("check_name", "check_quota", "check_network")]
        + [{"step": "create_cluster", "sub": s} for s in ("send_request", "prepare_api", "create_cp_vm", "init_control_plane",
                                                          "create_workers", "create_control_plane", "finish_cluster")]
        + [{"step": "setup_cluster", "sub": s} for s in ("check_api", "create_namespace", "grant_access")])
API = "https://192.0.2.1:6443"


def _snap(status, master=False, cp=0, workers=0, api="", wvms=None, problem=None):
    return {"status": status, "reason": "Stack 실패(시험)" if status.endswith("FAILED") else "", "masterReady": master,
            "cpVms": cp, "cpActive": cp, "workerVms": workers if wvms is None else wvms, "workersActive": workers,
            "apiAddress": api, "problem": problem, "key": f"{status}/{master}/{cp}/{workers}/{api}/{problem}"}


WATCH = {
    # 실제 순서(2026-10-21 시험) — API 주소 → CP VM → Worker VM 생성 시작(초기화 끝) → Worker VM ACTIVE → 노드 준비 → 완료
    "wf-ok": [_snap(""), _snap("CREATE_IN_PROGRESS"), _snap("CREATE_IN_PROGRESS", api=API),
              _snap("CREATE_IN_PROGRESS", cp=1, api=API), _snap("CREATE_IN_PROGRESS", cp=1, api=API, wvms=2),
              _snap("CREATE_IN_PROGRESS", cp=1, workers=2, api=API), _snap("CREATE_IN_PROGRESS", True, 1, 2, API),
              _snap("CREATE_COMPLETE", True, 1, 2, API)],
    "wf-failed": [_snap("CREATE_IN_PROGRESS"), _snap("CREATE_FAILED")],
    "wf-cleanup-fail": [_snap("CREATE_FAILED")],
    "wf-vm-error": [_snap("CREATE_IN_PROGRESS", cp=1, api=API, wvms=1, problem={
        "code": "vm_error", "message": "노드 VM 생성 실패(default-worker-a): No valid host was found."})],
    "wf-api-fail": [_snap("CREATE_COMPLETE", True, 1, 2, API)],
    "wf-skip-setup": [_snap("CREATE_COMPLETE", True, 1, 2, API)],
}


@pytest.mark.skipif(not shutil.which("temporal"), reason="temporal CLI 없음 — brew install temporal")
def test_클러스터_프로비저닝():
    asyncio.run(_scenarios())


async def _scenarios():
    calls: list[tuple] = []

    def wid():
        return activity.info().workflow_id

    @activity.defn(name="core.init_steps_paas")
    def init_steps_paas(ticket_id, attempt):
        return PLAN

    @activity.defn(name="core.update_steps")
    def update_steps(ticket_id, attempt, changes):
        calls.append(("steps", ticket_id, changes))

    @activity.defn(name="core.finish")
    def finish(ticket_id, status, result, error):
        calls.append(("finish", ticket_id, status, result, error))

    @activity.defn(name="core.grant_cluster_access")
    def grant_cluster_access(project_id, cluster_id):
        calls.append(("grant", wid(), project_id, cluster_id))

    @activity.defn(name="iaas.check_quota")
    def check_quota(project_id, need):
        if wid() == "wf-quota":
            raise ApplicationError("프로젝트 쿼터가 부족합니다 — VM 필요 4대 · 남음 1대", non_retryable=True)
        return f"VM {need['instances']}대 · vCPU {need['vcpu']} · RAM {need['ramGb']}GB"

    @activity.defn(name="iaas.check_network")
    def check_network(network_id, subnet_id, floating_network_id):
        return "my-network / 10.0.1.0/24"

    @activity.defn(name="paas.check_name")
    def check_name(name):
        return name

    @activity.defn(name="paas.create_cluster")
    def create_cluster(project_id, spec):
        calls.append(("create", wid(), project_id, spec))
        if wid() == "wf-request-lost":
            raise ApplicationError("생성 요청 응답 끊김(시험)", non_retryable=True)
        return f"c-{wid()}"

    @activity.defn(name="paas.watch_cluster")
    def watch_cluster(cluster_id, last, max_seconds, interval):
        return WATCH[wid()].pop(0)

    @activity.defn(name="paas.check_api")
    def check_api(cluster_id):
        if wid() in ("wf-api-fail", "wf-skip-setup"):  # 건너뛰면 부르지 않는다
            raise ApplicationError("timed out", non_retryable=True)
        return API

    @activity.defn(name="paas.create_namespace")
    def create_namespace(cluster_id, name, project_id, project_name):
        calls.append(("namespace", wid(), cluster_id, name, project_id, project_name))
        return name

    @activity.defn(name="paas.delete_cluster")
    def delete_cluster(project_id, name, cluster_id, deadline):
        calls.append(("delete", wid(), project_id, name, cluster_id))
        if wid() == "wf-cleanup-fail":
            raise ApplicationError("클러스터 삭제가 제한 시간 안에 끝나지 않았습니다 — 삭제 진행 중, 확인 필요",
                                   non_retryable=True)
        return cluster_id is not None

    def inp(tid):
        return {"ticketId": tid, "attempt": 1, "projectId": "p-1", "projectName": "okc_test_prj",
                "clusterName": f"cl-{tid}", "templateId": "t-1", "masterCount": 1, "masterFlavorId": "m1.large",
                "nodeCount": 2, "flavorId": "m1.large", "networkId": "net-1", "subnetId": "sub-1",
                "floatingNetworkId": "pub", "keypair": "gy-yang-pub", "namespace": "okc-test-prj",
                "need": {"instances": 3, "vcpu": 12, "ramGb": 24}}

    async with await WorkflowEnvironment.start_local(dev_server_existing_path=shutil.which("temporal")) as env:
        with ThreadPoolExecutor(4) as pool:
            # 운영과 같은 샌드박스 러너 — 워크플로 파일이 외부 모듈을 가져오면 여기서 실패한다(VmProvision 도 함께 확인)
            async with Worker(env.client, task_queue=CORE_QUEUE,
                              workflows=[kp.KubernetesProvision, vm_provision.VmProvision],
                              activities=[init_steps_paas, update_steps, finish, grant_cluster_access],
                              activity_executor=pool), \
                       Worker(env.client, task_queue=kp.IAAS_QUEUE, activity_executor=pool,
                              activities=[check_quota, check_network]), \
                       Worker(env.client, task_queue=kp.PAAS_QUEUE, activity_executor=pool,
                              activities=[check_name, create_cluster, watch_cluster, check_api, create_namespace,
                                          delete_cluster]):
                ok = await env.client.execute_workflow(kp.KubernetesProvision.run, inp("wf-ok"), id="wf-ok",
                                                       task_queue=CORE_QUEUE)
                skipped = await env.client.execute_workflow(kp.KubernetesProvision.run,
                                                            {**inp("wf-skip-setup"), "skipSetup": True},
                                                            id="wf-skip-setup", task_queue=CORE_QUEUE)
                for tid in ("wf-failed", "wf-request-lost", "wf-quota", "wf-cleanup-fail", "wf-vm-error", "wf-api-fail"):
                    with pytest.raises(WorkflowFailureError):
                        await env.client.execute_workflow(kp.KubernetesProvision.run, inp(tid), id=tid,
                                                          task_queue=CORE_QUEUE)

    def fin(tid):
        return next(c for c in calls if c[:2] == ("finish", tid))[2:]

    def sub_values(tid):
        return {c["sub"]: c["value"] for x in calls if x[:2] == ("steps", tid) for c in x[2]
                if "sub" in c and c.get("value")}

    # 정상 완료 — 결과·접근 권한(소유 프로젝트)·네임스페이스, 단계 값
    assert ok == {"clusterId": "c-wf-ok", "clusterName": "cl-wf-ok", "namespace": "okc-test-prj",
                  "apiAddress": "https://192.0.2.1:6443"}
    assert fin("wf-ok") == ("completed", ok, None)
    assert ("grant", "wf-ok", "p-1", "c-wf-ok") in calls
    assert ("namespace", "wf-ok", "c-wf-ok", "okc-test-prj", "p-1", "okc_test_prj") in calls
    create = next(c for c in calls if c[:2] == ("create", "wf-ok"))
    assert create[2] == "p-1" and create[3]["name"] == "cl-wf-ok" and create[3]["subnetId"] == "sub-1"
    assert create[3]["keypair"] == "gy-yang-pub"  # OpenStack 키페어 이름 그대로 — PAAS 가 찾아서 Magnum 에 넘긴다
    assert "externalNetwork" not in create[3]  # 노드는 프로젝트·공유 망만(라우터 + API Floating IP)
    assert sub_values("wf-ok") == {"check_name": "cl-wf-ok", "check_quota": "VM 3대 · vCPU 12 · RAM 24GB",
                                   "check_network": "my-network / 10.0.1.0/24", "send_request": "c-wf-ok",
                                   "prepare_api": API, "create_cp_vm": "1/1", "create_workers": "2/2",
                                   "check_api": "https://192.0.2.1:6443", "create_namespace": "okc-test-prj",
                                   "grant_access": "okc_test_prj"}

    # 로컬(skipSetup) — K8s API 연결·네임스페이스·접근 권한을 건너뜀으로 기록하고 완료(네임스페이스·접근 권한 없음)
    assert skipped == {"clusterId": "c-wf-skip-setup", "clusterName": "cl-wf-skip-setup", "apiAddress": API}
    assert fin("wf-skip-setup") == ("completed", skipped, None)
    assert not any(c[:2] in (("namespace", "wf-skip-setup"), ("grant", "wf-skip-setup")) for c in calls)
    last = {c.get("sub") or c["step"]: c["status"] for x in calls if x[:2] == ("steps", "wf-skip-setup") for c in x[2]}
    # 설정 단계는 한 번도 '진행 중'이 되지 않는다(건너뜀에 시작 시각이 남지 않게)
    assert not any(c.get("status") == "running" and (c.get("sub") or c.get("step")) in ("check_api", "setup_cluster")
                   for x in calls if x[:2] == ("steps", "wf-skip-setup") for c in x[2])
    assert {k: last[k] for k in ("finish_cluster", "create_cluster", "check_api", "create_namespace", "grant_access",
                                 "setup_cluster")} == {"finish_cluster": "done", "create_cluster": "done", "check_api": "skipped",
                                                       "create_namespace": "skipped", "grant_access": "skipped",
                                                       "setup_cluster": "skipped"}

    # 생성 중 실패 — 만든 클러스터(ID)를 지우고 실패
    status, result, error = fin("wf-failed")
    assert status == "failed" and result is None
    assert error == "클러스터 생성 실패(CREATE_FAILED): Stack 실패(시험) — 생성한 클러스터는 삭제함"
    assert ("delete", "wf-failed", "p-1", "cl-wf-failed", "c-wf-failed") in calls

    # 생성 요청 응답이 끊김 — ID 없이 이름으로 찾아 정리(없었으면 덧붙일 말 없음)
    assert ("delete", "wf-request-lost", "p-1", "cl-wf-request-lost", None) in calls
    assert fin("wf-request-lost") == ("failed", None, "생성 요청 응답 끊김(시험)")

    # 사전 확인 실패 — 만든 것이 없어 생성·정리 없음
    assert fin("wf-quota") == ("failed", None, "프로젝트 쿼터가 부족합니다 — VM 필요 4대 · 남음 1대")
    assert not any(c[:2] in (("create", "wf-quota"), ("delete", "wf-quota")) for c in calls)

    # 정리 실패 — 클러스터 ID 를 결과에 남긴다
    status, result, error = fin("wf-cleanup-fail")
    assert status == "failed" and result == {"clusterId": "c-wf-cleanup-fail", "clusterName": "cl-wf-cleanup-fail"}
    assert "생성한 클러스터 정리 실패, 확인 필요(clusterId=c-wf-cleanup-fail)" in error

    # 조기 실패 — 노드 VM ERROR 는 바로 실패(45분 기다리지 않음), 만든 클러스터는 정리
    assert fin("wf-vm-error")[2] == "노드 VM 생성 실패(default-worker-a): No valid host was found. — 생성한 클러스터는 삭제함"
    # K8s API 에 닿지 않음 — 정해 둔 문구로(원래 오류 'timed out' 대신)
    assert fin("wf-api-fail")[2] == "포털이 클러스터 K8s API 에 접근하지 못했습니다. — 생성한 클러스터는 삭제함"


# ── 조기 실패 판정(judge) ─────────────────────────────────────────

T0 = datetime(2026, 10, 21, 18, 0)


def _s(cp=1, workers=1, problem=None, active=None, master=False):
    return {"cpVms": cp, "cpActive": cp if active is None else active, "workerVms": workers, "masterReady": master,
            "problem": problem}


def test_노드_VM_이_안_생기면_2분부터_주의_5분에_실패():
    seen: dict = {"no_cp_vm": T0}  # 워크플로가 생성 요청 시각으로 넣어 둔다
    assert kp.judge(_s(cp=0), T0 + timedelta(minutes=1), seen) == (None, None)  # 성공 때는 31초
    assert kp.judge(_s(cp=0), T0 + timedelta(minutes=2), seen) == (
        None, "주의: Control Plane VM 이 아직 만들어지지 않았습니다 (2분째)")
    fail, _ = kp.judge(_s(cp=0), T0 + timedelta(minutes=5), seen)
    assert fail.startswith("Control Plane VM 이 5분 안에 만들어지지 않았습니다")
    # Worker VM — 노드 준비(Control Plane 완료) 뒤부터 센다
    assert kp.judge(_s(workers=0, master=True), T0 + timedelta(minutes=4), seen) == (None, None)
    fail, _ = kp.judge(_s(workers=0, master=True), T0 + timedelta(minutes=9), seen)
    assert fail.startswith("Worker VM 이 5분 안에 만들어지지 않았습니다")


def test_노드_VM_이_있다가_사라지면_사라진_때부터_센다():
    # 2026-10-22 req-261022-007 — 생성 44분 뒤 Control Plane VM 이 지워지자 '5분 안에 안 생김'이 바로 걸렸다
    seen: dict = {"no_cp_vm": T0}
    assert kp.judge(_s(cp=1, workers=1), T0 + timedelta(minutes=4), seen) == (None, None)
    assert kp.judge(_s(cp=0, workers=1), T0 + timedelta(minutes=44), seen) == (None, None)
    assert kp.judge(_s(cp=0, workers=1), T0 + timedelta(minutes=46), seen)[1] == (
        "주의: Control Plane VM 이 아직 만들어지지 않았습니다 (2분째)")
    fail, _ = kp.judge(_s(cp=0, workers=1), T0 + timedelta(minutes=49), seen)
    assert fail.startswith("Control Plane VM 이 5분 안에 만들어지지 않았습니다")
    # Worker VM 도 같다 — 노드 준비 뒤 있다가 사라진 때부터
    seen = {}
    assert kp.judge(_s(workers=2, master=True), T0, seen) == (None, None)
    assert kp.judge(_s(workers=0, master=True), T0 + timedelta(minutes=20), seen) == (None, None)


def test_Control_Plane_VM_이_뜬_뒤_Worker_VM_이_안_생기면_5분부터_주의_10분에_실패():
    # 2026-10-22 req-261022-007 — kubeadm init 은 끝났지만 API 주소(외부망 LB)에 닿지 못해 Worker 가 안 생김, 45분 대기
    # 성공 때 초기화는 2~2.5분(Control Plane VM ACTIVE → Worker VM 생성 시작)
    seen: dict = {"no_cp_vm": T0}
    assert kp.judge(_s(cp=1, active=0, workers=0), T0 + timedelta(minutes=2), seen) == (None, None)  # 아직 BUILD
    assert kp.judge(_s(cp=1, workers=0), T0 + timedelta(minutes=4), seen) == (None, None)
    assert kp.judge(_s(cp=1, workers=0), T0 + timedelta(minutes=8), seen)[0] is None
    assert kp.judge(_s(cp=1, workers=0), T0 + timedelta(minutes=9), seen) == (
        None, "주의: Control Plane VM 이 뜬 뒤 Worker VM 이 아직 만들어지지 않았습니다 (5분째)")
    fail, _ = kp.judge(_s(cp=1, workers=0), T0 + timedelta(minutes=14), seen)
    assert fail.startswith("Control Plane VM 이 뜬 뒤 10분 동안 Worker VM 이 만들어지지 않았습니다")
    # Worker VM 이 생기거나 노드 준비가 끝나면 풀린다
    seen = {"no_cp_vm": T0}
    assert kp.judge(_s(cp=1, workers=0), T0 + timedelta(minutes=4), seen) == (None, None)
    assert kp.judge(_s(cp=1, workers=1), T0 + timedelta(minutes=6), seen) == (None, None)
    assert kp.judge(_s(cp=1, workers=0, master=True), T0 + timedelta(minutes=15), seen) == (None, None)


def test_클라우드_컨트롤러_문제는_3분_계속되면_실패():
    occm = {"code": "occm", "message": "클라우드 컨트롤러(OCCM)가 OpenStack API·DNS 에 닿지 못합니다 (i/o timeout)"}
    seen: dict = {}
    assert kp.judge(_s(problem=occm), T0, seen) == (None, f"주의: {occm['message']} (0분째)")
    assert kp.judge(_s(problem=occm), T0 + timedelta(minutes=2), seen)[0] is None
    # 사라졌다 다시 생기면 처음부터 센다
    assert kp.judge(_s(), T0 + timedelta(minutes=2, seconds=30), seen) == (None, None)
    assert kp.judge(_s(problem=occm), T0 + timedelta(minutes=3), seen)[0] is None
    assert kp.judge(_s(problem=occm), T0 + timedelta(minutes=6), seen) == (occm["message"], None)


@pytest.mark.parametrize("code", ["vm_error", "node_network"])
def test_확정된_문제는_바로_실패(code):
    p = {"code": code, "message": "노드 문제(시험)"}
    assert kp.judge(_s(problem=p), T0, {}) == ("노드 문제(시험)", None)
