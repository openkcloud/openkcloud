"""PAAS 워커가 실행하는 Magnum·K8s 작업 — MNT-CORE 의 클러스터 프로비저닝 워크플로(KubernetesProvision)가 이름(paas.*)으로 부른다.

실패 구분은 IAAS 작업과 같다:
  - 다시 해도 소용없는 실패(이름 중복, 생성 거절, 다른 프로젝트 네임스페이스 등) → ApplicationError(non_retryable)
  - 연결 끊김 같은 일시 오류 → 그대로 올려 워크플로의 재시도 정책을 따른다(생성 요청은 워크플로가 재시도하지 않음)
조회는 서비스 계정 admin 스코프(다른 프로젝트 클러스터도 조회·삭제 가능 — 2026-10-19 시험), 생성은 소유 프로젝트 스코프.
"""
import re
import time
from urllib.parse import urlparse

import httpx
from temporalio import activity
from temporalio.exceptions import ApplicationError

from ..config import settings
from ..k8s_client import k8s, k8s_get, k8s_request
from ..openstack_client import for_project, magnum
from ..routers.clusters import NAME_RULE, NAME_TAKEN, name_rule_ok, name_taken

PROJECT_LABEL = "openkcloud.io/project-id"  # AI 워크스페이스 네임스페이스와 같은 키
PROJECT_NAME_ANNOTATION = "openkcloud.io/project-name"
CREATE_TIMEOUT_MIN = 60  # Magnum create_timeout(분) — 워크플로의 생성 대기(45분)보다 길게
DIAG_SECONDS = 30        # 진단(노드 콘솔·클라우드 컨트롤러) 간격 — 상태 확인(10초)보다 드물게, 그 사이엔 지난 결과를 쓴다
# 노드 콘솔에 나오면 노드가 IP·메타데이터를 받지 못한 것(2026-10-19 1차 시험 — DHCP 꺼진 서브넷)
_CONSOLE_FATAL = re.compile(r"Failed to start .*Wait for Network to be Configured|No active metadata service found")
# 클라우드 컨트롤러(OCCM)가 OpenStack API·DNS 에 닿지 못한 로그(2026-10-20 2~5차 시험 — lookup keystone-api… i/o timeout)
_OCCM_UNREACHABLE = re.compile(r"i/o timeout|no such host|dial tcp|connection refused|context deadline exceeded")


def _fail(msg: str) -> ApplicationError:
    return ApplicationError(msg, non_retryable=True)


def _detail(e: httpx.HTTPStatusError) -> str:
    """Magnum 오류 본문의 메시지({"errors": [{"detail"}]}) — Nova 는 {"badRequest": {"message"}}."""
    try:
        d = e.response.json()
        return ((d.get("errors") or [{}])[0].get("detail") or d.get("title")
                or next((v.get("message") for v in d.values() if isinstance(v, dict)), None) or e.response.text)
    except Exception:  # noqa: BLE001
        return e.response.text or f"HTTP {e.response.status_code}"


def _same_name(name: str, project_id: str) -> list[str]:
    """그 프로젝트의 같은 이름 클러스터 ID — 목록에는 project_id 가 없어 상세로 확인한다."""
    ids = [x["uuid"] for x in magnum.get("/clusters").get("clusters", []) if x["name"] == name]
    return [cid for cid in ids if magnum.get(f"/clusters/{cid}").get("project_id") == project_id]


@activity.defn(name="paas.check_name")
def check_name(name: str) -> str:
    """클러스터 이름 규칙·중복(모든 프로젝트) 확인 → 이름. 이전 회차 클러스터가 아직 삭제 중이어도 중복이다."""
    if not name_rule_ok(name):
        raise _fail(NAME_RULE)
    if name_taken(name):
        raise _fail(f"{NAME_TAKEN} 이전 회차 클러스터가 삭제 중이면 끝난 뒤 다시 시도하세요.")
    return name


def _flavor_name(ref: str) -> str:
    """플레이버 ID → 이름. magnum_capi_helm 드라이버(CAPO)는 플레이버를 이름으로 찾는다 — ID 를 넘기면 Magnum 은 받아도
    VM 을 만들지 못한다('no flavors were found: name=<ID>', 2026-10-21 req-261021-004). 이미 이름이면(Nova 404) 그대로."""
    try:
        return magnum.service_get("nova", "compute", "/v2.1", f"/flavors/{ref}")["flavor"]["name"]
    except httpx.HTTPStatusError as e:
        if e.response.status_code == 404:
            return ref
        raise _fail(f"플레이버 확인 실패: {_detail(e)}")


@activity.defn(name="paas.create_cluster")
def create_cluster(project_id: str, spec: dict) -> str:
    """클러스터 생성 요청(Magnum 1회, 소유 프로젝트 스코프 — API LB·Floating IP 도 그 프로젝트 소유로 생긴다) → 클러스터 ID.
    spec: name·templateId·masterCount·masterFlavorId·nodeCount·flavorId·networkId·subnetId·keypair(Nova 키페어 이름,
    없으면 템플릿 키페어).
    키페어는 새로 만들지 않는다 — 넘어온 이름을 Nova 에서 찾아 그대로 쓰고, 없으면 생성 요청을 보내지 않는다.
    본문은 2026-10-20 6차 시험(정상 완료)과 같다. 노드 Floating IP 는 이 드라이버가 지원하지 않아 끈다(API LB 에는 붙는다).
    플레이버는 ID 로 받아 이름으로 넘긴다(_flavor_name)."""
    body = {
        "name": spec["name"],
        "cluster_template_id": spec["templateId"],
        "master_count": spec["masterCount"],
        "master_flavor_id": _flavor_name(spec["masterFlavorId"]),
        "node_count": spec["nodeCount"],
        "flavor_id": _flavor_name(spec["flavorId"]),
        "fixed_network": spec["networkId"],
        "fixed_subnet": spec["subnetId"],
        "master_lb_enabled": True,
        "floating_ip_enabled": False,
        "create_timeout": CREATE_TIMEOUT_MIN,
        "merge_labels": True,
        "labels": {"availability_zone": settings.cluster_availability_zone,
                   "auto_scaling_enabled": "False", "auto_healing_enabled": "False"},
    }
    if spec.get("keypair"):  # Nova 키페어는 사용자(서비스 계정) 소유 — Magnum 이 같은 사용자로 찾는다
        try:
            magnum.service_get("nova", "compute", "/v2.1", f"/os-keypairs/{spec['keypair']}")
        except httpx.HTTPStatusError as e:
            if e.response.status_code == 404:
                raise _fail(f"키페어({spec['keypair']})를 OpenStack 에서 찾을 수 없습니다")
            raise _fail(f"키페어 확인 실패: {_detail(e)}")
        body["keypair"] = spec["keypair"]
    try:
        return for_project(project_id).request("POST", "/clusters", json=body)["uuid"]
    except httpx.HTTPStatusError as e:
        raise _fail(f"클러스터 생성 요청이 거절되었습니다: {_detail(e)}")


def _console_problem(servers: list[dict]) -> dict | None:
    """Control Plane VM 콘솔에서 '노드가 IP·메타데이터를 받지 못함' 문구를 찾는다(조회 실패는 판단하지 않음)."""
    for sv in servers:
        if sv.get("status") != "ACTIVE":
            continue
        try:
            out = magnum.service_request("POST", "nova", "compute", "/v2.1", f"/servers/{sv['id']}/action",
                                         json={"os-getConsoleOutput": {"length": 300}}).get("output") or ""
        except httpx.HTTPError:
            continue
        m = _CONSOLE_FATAL.search(out)
        if m:
            return {"code": "node_network", "message": f"노드가 IP·메타데이터를 받지 못했습니다 — 서브넷 DHCP·메타데이터 확인 (콘솔: {m.group(0)})"}
    return None


def _occm_problem(cluster_id: str) -> dict | None:
    """클라우드 컨트롤러(OCCM, openstack-system 의 DaemonSet)가 2번 이상 재시작했으면 직전 로그에서 OpenStack 연결 오류를 찾는다.
    K8s API 에 닿지 않으면(로컬 개발 환경 등) 판단하지 않는다 — 진단은 보조 신호라 오류를 올리지 않는다."""
    try:
        cli = k8s(cluster_id)
        r = cli.get("/api/v1/namespaces/openstack-system/pods", timeout=5)
        r.raise_for_status()
        for pod in r.json().get("items", []):
            name = pod["metadata"]["name"]
            if not name.startswith("openstack-cloud-controller-manager"):
                continue
            for c in (pod.get("status") or {}).get("containerStatuses") or []:
                if c.get("restartCount", 0) < 2:
                    continue
                log = cli.get(f"/api/v1/namespaces/openstack-system/pods/{name}/log", timeout=5,
                              params={"container": c["name"], "previous": "true", "tailLines": 50}).text
                hits = [line.strip() for line in log.splitlines() if _OCCM_UNREACHABLE.search(line)]
                if hits:
                    return {"code": "occm", "message": f"클라우드 컨트롤러(OCCM)가 OpenStack API·DNS 에 닿지 못합니다 ({hits[-1][:200]})"}
                return {"code": "occm", "message": f"클라우드 컨트롤러(OCCM)가 계속 재시작합니다 (재시작 {c['restartCount']}회)"}
    except Exception:  # noqa: BLE001
        return None
    return None


def _snapshot(cluster_id: str, diagnose: bool = False) -> dict:
    """진행 상태 — 클러스터 상태·Control Plane 노드그룹 완료·노드 VM(Control Plane·Worker 수와 ACTIVE 수)·API 주소 + 문제.
    문제(problem: code·message): VM ERROR 는 매번, 노드 콘솔(초기화 전)·클라우드 컨트롤러 진단은 diagnose 일 때만 본다.
    생성 직후 몇 초는 GET 이 404·400 이라 status 를 비워 돌려준다(얼마나 견딜지는 워크플로가 정함)."""
    empty = {"status": "", "reason": "", "masterReady": False, "cpVms": 0, "cpActive": 0, "workerVms": 0,
             "workersActive": 0, "apiAddress": "", "problem": None}
    try:
        c = magnum.get(f"/clusters/{cluster_id}")
        ngs = magnum.get(f"/clusters/{cluster_id}/nodegroups").get("nodegroups", [])
    except httpx.HTTPStatusError as e:
        if e.response.status_code in (400, 404):
            return empty
        raise
    master = next((g for g in ngs if g.get("role") == "master"), {})
    master_ready = master.get("status") == "CREATE_COMPLETE"
    stack = c.get("stack_id") or ""
    servers = magnum.service_get("nova", "compute", "/v2.1", "/servers/detail", {  # VM 이름 = {stack_id}-{노드그룹}-…
        "all_tenants": 1, "name": f"^{re.escape(stack)}-"}).get("servers", []) if stack else []
    cp = [sv for sv in servers if "-control-plane-" in sv["name"]]
    workers = [sv for sv in servers if "-control-plane-" not in sv["name"]]
    api = c.get("api_address") or ""
    api = api if urlparse(api).hostname else ""  # 생성 직후 잠깐 'https://:0'
    problem = None
    failed = next((sv for sv in servers if sv.get("status") == "ERROR"), None)
    if failed:
        problem = {"code": "vm_error", "message": f"노드 VM 생성 실패({failed['name'].removeprefix(stack + '-')}): "
                                                  f"{(failed.get('fault') or {}).get('message') or '사유 없음'}"}
    elif diagnose and cp and not master_ready:
        problem = (_console_problem(cp) if not workers else None) or (_occm_problem(cluster_id) if api else None)
    return {
        "status": c.get("status") or "",
        "reason": c.get("status_reason") or "",
        "masterReady": master_ready,
        "cpVms": len(cp),
        "cpActive": sum(1 for sv in cp if sv.get("status") == "ACTIVE"),
        "workerVms": len(workers),
        "workersActive": sum(1 for sv in workers if sv.get("status") == "ACTIVE"),
        "apiAddress": api,
        "problem": problem,
    }


@activity.defn(name="paas.watch_cluster")
def watch_cluster(cluster_id: str, last: str, max_seconds: float, interval: int) -> dict:
    """진행 상태가 last 와 달라지면 바로, 아니면 max_seconds 뒤 현재 상태를 돌려준다(IAAS watch_server 와 같은 방식).
    Worker 진행은 노드그룹 상태가 아니라 Nova 의 Worker VM 으로 본다 — Worker 노드그룹은 VM 이 생기기도 전에
    완료로 바뀐다(2026-10-19 시험). Control Plane 노드그룹 완료는 실제 준비 시점과 맞다.
    문제(problem)가 생기거나 바뀌어도 바로 돌려준다 — 실패 판정(몇 분 계속되면 실패)은 워크플로가 한다."""
    end = time.monotonic() + max_seconds
    next_diag, diag = 0.0, None
    while True:
        deep = time.monotonic() >= next_diag
        snap = _snapshot(cluster_id, deep)
        if deep:
            next_diag, diag = time.monotonic() + DIAG_SECONDS, snap["problem"]
        snap["problem"] = snap["problem"] or diag
        p = snap["problem"]
        key = (f"{snap['status']}/{snap['masterReady']}/{snap['cpActive']}:{snap['cpVms']}/"
               f"{snap['workersActive']}:{snap['workerVms']}/{snap['apiAddress']}/{p['code'] if p else ''}")
        if key != last or time.monotonic() >= end:
            return {**snap, "key": key}
        activity.heartbeat(key)
        time.sleep(interval)


@activity.defn(name="paas.check_api")
def check_api(cluster_id: str) -> str:
    """포털이 새 클러스터 K8s API 에 접속되는지 → API 주소. 접속에 쓰는 인증서는 소유 프로젝트 스코프로 발급되고
    클라이언트는 캐시된다(포털 화면·네임스페이스 생성이 그대로 쓴다). 실패는 일시 오류로 보고 다시 시도하게 둔다."""
    k8s_get(cluster_id, "/version")
    return magnum.get(f"/clusters/{cluster_id}").get("api_address") or ""


@activity.defn(name="paas.create_namespace")
def create_namespace(cluster_id: str, name: str, project_id: str, project_name: str) -> str:
    """프로젝트 네임스페이스 생성(라벨 project-id, 주석 project-name) → 이름.
    이미 있으면 같은 프로젝트 것인지만 확인한다(응답 유실 후 재시도해도 결과가 같게)."""
    manifest = {"apiVersion": "v1", "kind": "Namespace", "metadata": {
        "name": name, "labels": {PROJECT_LABEL: project_id}, "annotations": {PROJECT_NAME_ANNOTATION: project_name}}}
    try:
        k8s_request(cluster_id, "POST", "/api/v1/namespaces", json=manifest)
    except httpx.HTTPStatusError as e:
        if e.response.status_code != 409:
            if e.response.status_code < 500:
                raise _fail(f"네임스페이스 생성 실패: {e.response.text[:300]}")
            raise
        labels = k8s_get(cluster_id, f"/api/v1/namespaces/{name}")["metadata"].get("labels") or {}
        if labels.get(PROJECT_LABEL) != project_id:
            raise _fail(f"네임스페이스 {name} 가 이미 있고 이 프로젝트 것이 아닙니다.")
    return name


@activity.defn(name="paas.delete_cluster")
def delete_cluster(project_id: str, name: str, cluster_id: str | None, deadline: float) -> bool:
    """실패한 회차의 클러스터 삭제 + 사라질 때까지 대기. deadline(epoch 초)은 워크플로가 정한 절대 시각.
    생성 요청 응답을 못 받았으면(cluster_id 없음) 그 프로젝트에서 이름으로 찾는다. 404·DELETE_COMPLETE 를 삭제됨으로 본다
    (사라지기 전 DELETE_COMPLETE 가 잠깐 보인다 — 2026-10-19 시험). 지울 클러스터가 없었으면 False."""
    ids = [cluster_id] if cluster_id else _same_name(name, project_id)
    for cid in ids:
        try:
            magnum.request("DELETE", f"/clusters/{cid}")
        except httpx.HTTPStatusError as e:
            if e.response.status_code == 404:
                continue
            status = magnum.get(f"/clusters/{cid}").get("status")
            if status not in ("DELETE_IN_PROGRESS", "DELETE_COMPLETE"):  # 이미 지우는 중이면 기다리면 된다
                raise _fail(f"클러스터 삭제 요청이 거절되었습니다: {_detail(e)}")
        while True:
            try:
                status = magnum.get(f"/clusters/{cid}").get("status")
            except httpx.HTTPStatusError as e:
                if e.response.status_code == 404:
                    break
                raise
            if status == "DELETE_COMPLETE":
                break
            if status == "DELETE_FAILED":
                raise _fail("클러스터 삭제에 실패했습니다(DELETE_FAILED) — 확인 필요")
            if time.time() > deadline:
                raise _fail("클러스터 삭제가 제한 시간 안에 끝나지 않았습니다 — 삭제 진행 중, 확인 필요")
            activity.heartbeat(status)
            time.sleep(10)
    return bool(ids)


ALL = [check_name, create_cluster, watch_cluster, check_api, create_namespace, delete_cluster]
