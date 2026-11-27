"""클러스터 프로비저닝 작업(paas.*)·이름 확인 API·템플릿 K8s 버전 — 가짜 Magnum·Nova·K8s 로."""
import httpx
import pytest
from conftest import ADMIN, USER, client_as
from temporalio.exceptions import ApplicationError
from temporalio.testing import ActivityEnvironment

from app.openstack_client import magnum
from app.routers import templates
from app.workflows import activities

P = "/api/paas"


def _http_error(status: int, body: dict | None = None) -> httpx.HTTPStatusError:
    req = httpx.Request("GET", "http://magnum.test")
    return httpx.HTTPStatusError("x", request=req, response=httpx.Response(status, json=body or {}, request=req))


def _magnum(monkeypatch, data: dict):
    """경로 → 응답(값이 예외면 던지고, 리스트면 차례로 꺼낸다)."""
    def get(path):
        v = data[path]
        if isinstance(v, list):
            v = v.pop(0) if len(v) > 1 else v[0]
        if isinstance(v, Exception):
            raise v
        return v
    monkeypatch.setattr(magnum, "get", get)


FLAVOR_NAMES = {"f-uuid-m": "m1.large", "f-uuid-w": "m1.large.atomp"}  # Nova 플레이버 ID → 이름(그 밖은 404 = 이미 이름)
KEYPAIRS = {"gy-yang-pub", "k8s-key"}  # Nova 에 있는 키페어(서비스 계정 소유)


def _nova_lookup(monkeypatch) -> list:
    """가짜 Nova 조회 — 플레이버(FLAVOR_NAMES)·키페어(KEYPAIRS). 그 밖의 호출은 기록만 한다(키페어를 만들거나 지우면 안 됨)."""
    calls = []

    def service_get(service, stype, ver, path, params=None):
        kind, _, ref = path.strip("/").partition("/")
        if kind == "flavors":
            if ref not in FLAVOR_NAMES:
                raise _http_error(404)
            return {"flavor": {"id": ref, "name": FLAVOR_NAMES[ref]}}
        if kind == "os-keypairs":
            if ref not in KEYPAIRS:
                raise _http_error(404)
            return {"keypair": {"name": ref}}
        raise AssertionError(path)

    def service_request(method, service, stype, ver, path, params=None, json=None):
        calls.append((method, path, json))
        return {}
    monkeypatch.setattr(magnum, "service_get", service_get)
    monkeypatch.setattr(magnum, "service_request", service_request)
    return calls


# ── 이름 ─────────────────────────────────────────────────────────

CLUSTERS = {"/clusters": {"clusters": [{"uuid": "c1", "name": "taken"}]}}


@pytest.mark.parametrize("name,expected", [
    ("new-cluster", {"available": True, "reason": None}),
    ("taken", {"available": False, "reason": "이미 사용 중인 클러스터 이름입니다."}),
    ("Bad_Name", {"available": False, "reason": activities.NAME_RULE}),
    ("a" * 41, {"available": False, "reason": activities.NAME_RULE}),
    ("ends-", {"available": False, "reason": activities.NAME_RULE}),
])
def test_이름_확인_API(monkeypatch, name, expected):
    _magnum(monkeypatch, CLUSTERS)
    assert client_as(USER).get(f"{P}/cluster-name-check", params={"name": name}).json() == expected


def test_이름_확인_작업(monkeypatch):
    _magnum(monkeypatch, CLUSTERS)
    assert activities.check_name("new-cluster") == "new-cluster"
    for bad in ("taken", "Bad"):
        with pytest.raises(ApplicationError) as e:
            activities.check_name(bad)
        assert e.value.non_retryable


# ── 생성 요청 ─────────────────────────────────────────────────────

SPEC = {"name": "c", "templateId": "t1", "masterCount": 1, "masterFlavorId": "m1.large", "nodeCount": 2,
        "flavorId": "m1.xlarge", "networkId": "net", "subnetId": "sub"}


def test_생성_요청은_소유_프로젝트_스코프로(monkeypatch):
    sent = {}

    class Owner:
        def request(self, method, path, json=None):
            sent.update(method=method, path=path, body=json)
            return {"uuid": "new-id"}

    monkeypatch.setattr(activities, "for_project", lambda pid: sent.setdefault("project", pid) and Owner())
    nova = _nova_lookup(monkeypatch)
    assert activities.create_cluster("p1", SPEC) == "new-id"
    b = sent["body"]
    assert sent["project"] == "p1" and (sent["method"], sent["path"]) == ("POST", "/clusters")
    assert (b["cluster_template_id"], b["master_count"], b["node_count"]) == ("t1", 1, 2)
    assert (b["master_flavor_id"], b["flavor_id"], b["fixed_network"], b["fixed_subnet"]) == ("m1.large", "m1.xlarge", "net", "sub")
    assert b["master_lb_enabled"] is True and b["merge_labels"] is True and b["labels"]["availability_zone"] == "az1"
    assert "keypair" not in b and nova == []  # 키페어가 없으면 템플릿 키페어
    assert "master_lb_floating_ip_enabled" not in b["labels"]  # 프로젝트 망 — API 에 Floating IP(템플릿 기본)


def test_플레이버는_이름으로_넘김(monkeypatch):
    # CAPO 는 플레이버를 이름으로 찾는다 — ID 를 그대로 넘기면 Control Plane VM 을 못 만든다(2026-10-21 req-261021-004)
    sent = {}

    class Owner:
        def request(self, method, path, json=None):
            sent["body"] = json
            return {"uuid": "new-id"}

    monkeypatch.setattr(activities, "for_project", lambda pid: Owner())
    _nova_lookup(monkeypatch)
    assert activities.create_cluster("p1", {**SPEC, "masterFlavorId": "f-uuid-m", "flavorId": "f-uuid-w"}) == "new-id"
    assert (sent["body"]["master_flavor_id"], sent["body"]["flavor_id"]) == ("m1.large", "m1.large.atomp")


def test_키페어는_이름으로_찾아_그대로_넘김(monkeypatch):
    sent = {}

    class Owner:
        def request(self, method, path, json=None):
            sent["body"] = json
            return {"uuid": "new-id"}

    monkeypatch.setattr(activities, "for_project", lambda pid: Owner())
    nova = _nova_lookup(monkeypatch)
    assert activities.create_cluster("p1", {**SPEC, "keypair": "k8s-key"}) == "new-id"
    assert sent["body"]["keypair"] == "k8s-key"
    assert nova == []  # 키페어를 만들거나 지우지 않는다


def test_없는_키페어면_생성_요청을_보내지_않음(monkeypatch):
    class Owner:
        def request(self, *a, **k):
            raise AssertionError("생성 요청을 보내면 안 됨")

    monkeypatch.setattr(activities, "for_project", lambda pid: Owner())
    _nova_lookup(monkeypatch)
    with pytest.raises(ApplicationError) as e:
        activities.create_cluster("p1", {**SPEC, "keypair": "nope"})
    assert e.value.non_retryable and str(e.value) == "키페어(nope)를 OpenStack 에서 찾을 수 없습니다"


def test_생성_요청_거절은_재시도하지_않음(monkeypatch):
    class Owner:
        def request(self, *a, **k):
            raise _http_error(400, {"errors": [{"detail": "Quota exceeded"}]})

    monkeypatch.setattr(activities, "for_project", lambda pid: Owner())
    _nova_lookup(monkeypatch)
    with pytest.raises(ApplicationError) as e:
        activities.create_cluster("p1", SPEC)
    assert e.value.non_retryable and str(e.value) == "클러스터 생성 요청이 거절되었습니다: Quota exceeded"


# ── 진행 감시 ─────────────────────────────────────────────────────

def _cluster(status="CREATE_IN_PROGRESS", api="https://:0", master="CREATE_IN_PROGRESS"):
    return {"/clusters/c1": {"status": status, "status_reason": None, "api_address": api, "stack_id": "st-1"},
            "/clusters/c1/nodegroups": {"nodegroups": [{"role": "master", "status": master},
                                                       {"role": "worker", "status": "CREATE_COMPLETE"}]}}


def _nova(monkeypatch, servers):
    sent = {}

    def service_get(service, stype, ver, path, params=None):
        sent.update(service=service, path=path, params=params)
        return {"servers": servers}
    monkeypatch.setattr(magnum, "service_get", service_get)
    return sent


def test_진행_상태(monkeypatch):
    _magnum(monkeypatch, _cluster(api="https://192.0.2.1:6443", master="CREATE_COMPLETE"))
    sent = _nova(monkeypatch, [{"name": "st-1-control-plane-a", "status": "ACTIVE"},
                               {"name": "st-1-default-worker-b", "status": "ACTIVE"}, {"name": "st-1-default-worker-c", "status": "BUILD"}])
    r = activities.watch_cluster("c1", "", 60, 1)  # last 와 다르면 바로 돌려준다
    assert (r["masterReady"], r["apiAddress"], r["problem"]) == (True, "https://192.0.2.1:6443", None)
    assert (r["cpActive"], r["cpVms"], r["workersActive"], r["workerVms"]) == (1, 1, 1, 2)  # Worker 는 노드그룹 상태가 아니라 VM
    assert sent["params"]["name"] == "^st\\-1-"


def test_진행_상태_API_주소_임시값과_생성_직후_404(monkeypatch):
    _magnum(monkeypatch, _cluster())
    _nova(monkeypatch, [])
    assert activities.watch_cluster("c1", "", 60, 1)["apiAddress"] == ""  # 'https://:0' 은 아직 없음
    _magnum(monkeypatch, {"/clusters/c1": _http_error(404), "/clusters/c1/nodegroups": {}})
    assert activities.watch_cluster("c1", "x", 60, 1)["status"] == ""


def test_노드_VM_ERROR_는_바로_문제로(monkeypatch):
    _magnum(monkeypatch, _cluster())
    _nova(monkeypatch, [{"name": "st-1-control-plane-a", "status": "ACTIVE"},
                        {"name": "st-1-default-worker-b", "status": "ERROR", "fault": {"message": "No valid host was found."}}])
    p = activities.watch_cluster("c1", "", 60, 1)["problem"]
    assert p == {"code": "vm_error", "message": "노드 VM 생성 실패(default-worker-b): No valid host was found."}


def test_노드_콘솔에_네트워크_실패가_보이면_문제로(monkeypatch):
    # Control Plane VM 은 떴는데 초기화 전(Worker VM 없음) — 콘솔에 IP·메타데이터 실패 문구(2026-10-19 1차 시험)
    _magnum(monkeypatch, _cluster())
    _nova(monkeypatch, [{"id": "v1", "name": "st-1-control-plane-a", "status": "ACTIVE"}])
    out = "...\n[FAILED] Failed to start systemd-networkd-wait-online.service - Wait for Network to be Configured.\n..."
    monkeypatch.setattr(magnum, "service_request", lambda m, svc, st, ver, path, params=None, json=None: {"output": out})
    p = activities.watch_cluster("c1", "", 60, 1)["problem"]
    assert p["code"] == "node_network" and "Wait for Network to be Configured" in p["message"]


class _K8s:
    """가짜 워크로드 K8s API — openstack-system 파드 목록과 직전 로그(Magnum 클러스터의 OCCM 은 이 네임스페이스의 DaemonSet)."""
    def __init__(self, restarts, log):
        self.restarts, self.log = restarts, log

    def get(self, path, params=None, timeout=None):
        if path.endswith("/log"):
            return httpx.Response(200, text=self.log, request=httpx.Request("GET", "http://k8s.test"))
        occm = path.startswith("/api/v1/namespaces/openstack-system/")
        pods = {"items": [{"metadata": {"name": "openstack-cloud-controller-manager-x"},
                           "status": {"containerStatuses": [{"name": "occm", "restartCount": self.restarts}]}}] if occm else []}
        return httpx.Response(200, json=pods, request=httpx.Request("GET", "http://k8s.test"))


@pytest.mark.parametrize("restarts,log,expected", [
    (3, "E1 failed\nE2 lookup keystone-api.openstack.internal on 192.168.90.253:53: read udp 10.0.1.38: i/o timeout\n",
     "클라우드 컨트롤러(OCCM)가 OpenStack API·DNS 에 닿지 못합니다 (E2 lookup keystone-api.openstack.internal on "
     "192.168.90.253:53: read udp 10.0.1.38: i/o timeout)"),
    (2, "panic: something else", "클라우드 컨트롤러(OCCM)가 계속 재시작합니다 (재시작 2회)"),
    (1, "i/o timeout", None),  # 한 번 재시작은 부팅 중 일시 오류일 수 있어 보지 않는다
])
def test_클라우드_컨트롤러가_OpenStack_에_닿지_못하면_문제로(monkeypatch, restarts, log, expected):
    # 2026-10-20 2~5차 시험(compute2 게이트웨이) — OCCM 이 Keystone 주소를 찾지 못해 계속 재시작
    _magnum(monkeypatch, _cluster(api="https://192.0.2.1:6443"))
    _nova(monkeypatch, [{"name": "st-1-control-plane-a", "status": "ACTIVE"}, {"name": "st-1-default-worker-b", "status": "BUILD"}])
    monkeypatch.setattr(activities, "k8s", lambda cid: _K8s(restarts, log))
    p = activities.watch_cluster("c1", "", 60, 1)["problem"]
    assert (p and p["message"]) == expected


def test_K8s_API_에_닿지_않으면_진단하지_않음(monkeypatch):
    _magnum(monkeypatch, _cluster(api="https://192.0.2.1:6443"))
    _nova(monkeypatch, [{"name": "st-1-control-plane-a", "status": "ACTIVE"}, {"name": "st-1-default-worker-b", "status": "BUILD"}])

    def unreachable(cid):
        raise httpx.ConnectTimeout("timed out")
    monkeypatch.setattr(activities, "k8s", unreachable)
    assert activities.watch_cluster("c1", "", 60, 1)["problem"] is None


def test_진행_상태가_바뀔_때까지_기다림(monkeypatch):
    first, second = _cluster(), _cluster(master="CREATE_COMPLETE")
    data = {k: [first[k], second[k]] for k in first}
    _magnum(monkeypatch, data)
    _nova(monkeypatch, [])
    monkeypatch.setattr(activities.time, "sleep", lambda s: None)
    beats = []
    env = ActivityEnvironment()
    env.on_heartbeat = lambda *d: beats.append(d)
    r = env.run(activities.watch_cluster, "c1", "CREATE_IN_PROGRESS/False/0:0/0:0//", 60, 1)
    assert r["masterReady"] is True and len(beats) == 1


# ── 네임스페이스 ─────────────────────────────────────────────────

def test_네임스페이스_생성(monkeypatch):
    sent = []
    monkeypatch.setattr(activities, "k8s_request", lambda cid, m, path, json=None: sent.append((m, path, json)) or {})
    assert activities.create_namespace("c1", "okc-test-prj", "p1", "okc_test_prj") == "okc-test-prj"
    meta = sent[0][2]["metadata"]
    assert sent[0][:2] == ("POST", "/api/v1/namespaces")
    assert meta["labels"] == {"openkcloud.io/project-id": "p1"}
    assert meta["annotations"] == {"openkcloud.io/project-name": "okc_test_prj"}


@pytest.mark.parametrize("owner,ok", [("p1", True), ("p2", False)])
def test_네임스페이스가_이미_있으면_같은_프로젝트인지(monkeypatch, owner, ok):
    def post(*a, **k):
        raise _http_error(409)
    monkeypatch.setattr(activities, "k8s_request", post)
    monkeypatch.setattr(activities, "k8s_get", lambda cid, path: {"metadata": {"labels": {"openkcloud.io/project-id": owner}}})
    if ok:
        assert activities.create_namespace("c1", "ns", "p1", "prj") == "ns"
    else:
        with pytest.raises(ApplicationError) as e:
            activities.create_namespace("c1", "ns", "p1", "prj")
        assert e.value.non_retryable


# ── 정리(삭제) ───────────────────────────────────────────────────

def test_삭제는_사라질_때까지_기다림(monkeypatch):
    deleted = []
    monkeypatch.setattr(magnum, "request", lambda m, path, json=None: deleted.append((m, path)) or {})
    _magnum(monkeypatch, {"/clusters/c1": [{"status": "DELETE_IN_PROGRESS"}, {"status": "DELETE_COMPLETE"}]})
    monkeypatch.setattr(activities.time, "sleep", lambda s: None)
    nova = _nova_lookup(monkeypatch)
    env = ActivityEnvironment()
    assert env.run(activities.delete_cluster, "p1", "c", "c1", 9e9) is True
    assert deleted == [("DELETE", "/clusters/c1")]
    assert nova == []  # 키페어는 지우지 않는다(만든 적 없음 — 신청자·템플릿 키페어 그대로)


def test_삭제는_ID_가_없으면_그_프로젝트에서_이름으로(monkeypatch):
    deleted = []
    monkeypatch.setattr(magnum, "request", lambda m, path, json=None: deleted.append(path) or {})
    _magnum(monkeypatch, {"/clusters": {"clusters": [{"uuid": "a", "name": "c"}, {"uuid": "b", "name": "c"}]},
                          "/clusters/a": [{"project_id": "p-other"}],
                          "/clusters/b": [{"project_id": "p1"}, _http_error(404)]})
    assert activities.delete_cluster("p1", "c", None, 9e9) is True
    assert deleted == ["/clusters/b"]  # 다른 프로젝트의 같은 이름 클러스터는 건드리지 않는다
    _magnum(monkeypatch, {"/clusters": {"clusters": []}})
    assert activities.delete_cluster("p1", "c", None, 9e9) is False


def test_삭제_API_는_키페어를_지우지_않음(monkeypatch):
    deleted = []
    monkeypatch.setattr(magnum, "request", lambda m, path, json=None: deleted.append((m, path)) or {})
    nova = _nova_lookup(monkeypatch)
    assert client_as(ADMIN).delete(f"{P}/clusters/c1").status_code == 200
    assert deleted == [("DELETE", "/clusters/c1")] and nova == []


def test_삭제_제한_시간_초과(monkeypatch):
    monkeypatch.setattr(magnum, "request", lambda *a, **k: {})
    _magnum(monkeypatch, {"/clusters/c1": [{"status": "DELETE_IN_PROGRESS"}]})
    with pytest.raises(ApplicationError) as e:
        activities.delete_cluster("p1", "c", "c1", 0)
    assert e.value.non_retryable and "삭제 진행 중, 확인 필요" in str(e.value)


# ── 템플릿 K8s 버전 ──────────────────────────────────────────────

def test_템플릿_K8s_버전은_이미지_속성(monkeypatch):
    templates._kube_versions.clear()
    img1 = "4f4afa4d-eadf-4b15-bf65-23c5343dd3a8"
    _magnum(monkeypatch, {"/clustertemplates": {"clustertemplates": [
        {"uuid": "t1", "name": "a", "image_id": img1}, {"uuid": "t2", "name": "b", "image_id": "ubuntu-kube-v1.36.2"},
        {"uuid": "t3", "name": "c", "image_id": "no-such-image"}]}})

    calls = []

    def glance(service, stype, ver, path, params=None):
        calls.append((path, params))
        if path == f"/images/{img1}":
            return {"kube_version": "v1.34.8"}
        return {"images": [{"kube_version": "1.36.2"}] if params == {"name": "ubuntu-kube-v1.36.2"} else []}

    monkeypatch.setattr(magnum, "service_get", glance)
    items = client_as(USER).get(f"{P}/clustertemplates").json()["items"]
    assert [t["kubeVersion"] for t in items] == ["1.34.8", "1.36.2", ""]  # image_id 가 이미지 이름이면 이름으로 찾는다
    client_as(USER).get(f"{P}/clustertemplates")
    assert len(calls) == 3  # 이미지별 한 번만 조회
