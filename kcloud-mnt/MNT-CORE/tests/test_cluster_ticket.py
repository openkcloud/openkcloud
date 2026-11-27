"""서비스 클러스터 신청 — 신청·이름 확인·프로비저닝 API(가짜 MNT-IAAS·MNT-PAAS·Temporal 시작) + 단계 등록·접근 권한 작업."""
import pytest
from conftest import PROJECT, add_member, bearer, make_token, sql

from app.config import settings
from app.routers import tickets
from app.workflows import activities, runtime

ADMIN = bearer(make_token("admin"))
FLAVORS = [
    {"id": "m1.large", "name": "m1.large", "vcpus": 4, "ramMb": 8192, "accelerator": None, "accelerators": []},
    {"id": "m1.rngd", "name": "m1.large.rngd", "vcpus": 4, "ramMb": 8192, "accelerator": "dp-rngd",
     "accelerators": [{"kind": "NPU", "count": 1}]},
    {"id": "m1.gone", "name": "m1.gone", "vcpus": 4, "ramMb": 8192, "accelerator": "dp-x", "deviceProfileMissing": True},
]
TEMPLATES = [
    {"id": "t1", "name": "ct-k8s-1.36.2", "kubeVersion": "1.36.2", "public": True, "hidden": False,
     "external_network_id": "public"},
    {"id": "t-hidden", "name": "hidden", "public": True, "hidden": True},
    # 외부 네트워크를 이름으로 저장한 템플릿(예전 포털 템플릿 화면) · 없는 외부 네트워크
    {"id": "t-byname", "name": "byname", "public": True, "hidden": False, "external_network_id": "public-shared"},
    {"id": "t-badext", "name": "badext", "public": True, "hidden": False, "external_network_id": "nope"},
    {"id": "t-mine", "name": "mine", "public": False, "hidden": False, "project_id": PROJECT},
    {"id": "t-other", "name": "other", "public": False, "hidden": False, "project_id": "other-prj"},
]
SUBNETS = [{"id": "sub-on", "cidr": "10.0.1.0/24", "dhcp": True}, {"id": "sub-off", "cidr": "10.0.0.0/24", "dhcp": False}]
QUOTA = {"instances": {"quota": 10, "used": 2}, "vcpu": {"quota": 20, "used": 3}, "ramGb": {"quota": 50, "used": 6}}
BODY = {"projectId": PROJECT, "clusterName": "test-cl", "templateId": "t1", "masterCount": 1, "masterFlavorId": "m1.large",
        "nodeCount": 2, "flavorId": "m1.rngd", "purpose": "시험"}


@pytest.fixture(autouse=True)
def _clean_cluster_tickets():
    yield
    sql("DELETE FROM tkt_request WHERE type = 'cluster' AND request_spec->>'clusterName' LIKE 'test-%'")


@pytest.fixture
def fake(monkeypatch):
    """MNT-IAAS·MNT-PAAS 응답(바꿔 가며 시험) — 경로 앞부분으로 고른다."""
    state = {"taken": set(), "fip": {"reachable": True, "reason": None}, "quota": QUOTA, "paas": [], "iaas": [],
             "keypairs": [{"name": "gy-yang-pub", "publicKey": "ssh-ed25519 AAAA nova@h", "fingerprint": "SHA256:nova"}]}

    def iaas(path, token):
        state["iaas"].append(path)
        if path == "/projects":
            return {"items": [{"id": PROJECT, "name": "okc_test_prj"}]}
        if path.endswith("/flavors"):
            return {"items": FLAVORS}
        if path.endswith("/networks"):
            return {"items": [{"id": "net", "name": "my-network", "kind": "project"},
                              {"id": "public", "name": "public", "kind": "external"},
                              {"id": "pub-shared", "name": "public-shared", "kind": "external", "shared": True}]}
        if "/fip-reachable" in path:
            return state["fip"]
        if path.startswith("/placement-preview"):
            return {"quota": state["quota"]}
        if path == "/networks/net":
            return {"subnets": SUBNETS}
        if path == "/keypairs":
            return {"items": state["keypairs"]}
        raise AssertionError(path)

    def paas(path, token):
        state["paas"].append(path)
        if path.startswith("/cluster-name-check"):
            name = path.split("=", 1)[1]
            return {"available": name not in state["taken"],
                    "reason": "이미 사용 중인 클러스터 이름입니다." if name in state["taken"] else None}
        if path == "/clustertemplates":
            return {"items": TEMPLATES}
        raise AssertionError(path)

    monkeypatch.setattr(tickets, "_iaas", iaas)
    monkeypatch.setattr(tickets, "_paas", paas)
    return state


# ── 신청 ─────────────────────────────────────────────────────────

def test_클러스터_신청(client, fake):
    r = client.post("/api/core/tickets/cluster", json=BODY, headers=ADMIN)
    assert r.status_code == 201, r.text
    t = r.json()
    assert (t["type"], t["status"], t["requestedBy"]) == ("cluster", "pending", "admin")
    assert (t["clusterName"], t["templateName"], t["kubeVersion"]) == ("test-cl", "ct-k8s-1.36.2", "1.36.2")
    assert (t["masterCount"], t["masterFlavorName"], t["nodeCount"], t["flavorName"]) == (1, "m1.large", 2, "m1.large.rngd")
    # 희망 망(내부망/인터넷망)은 받지 않는다 — 실제 망·서브넷은 관리자가 프로비저닝 때 고른다(헷갈릴 위험, 2026-10-22)
    assert t["accelerators"] == [{"kind": "NPU", "count": 1}] and "networkType" not in t
    assert t["namespace"] == "okc-test-prj" and t["purpose"] == "시험"  # 프로젝트 이름 규칙(AI 워크스페이스와 같음)
    # 진행 중 신청과 같은 이름은 쓸 수 없다(모든 프로젝트 기준)
    chk = client.get("/api/core/tickets/cluster-name-check", params={"name": "test-cl"}, headers=ADMIN).json()
    assert chk == {"available": False, "reason": "같은 이름으로 진행 중인 클러스터 신청이 있습니다."}
    assert client.post("/api/core/tickets/cluster", json=BODY, headers=ADMIN).status_code == 409


def test_클러스터_신청_프로젝트_소유_템플릿(client, fake):
    # 공개가 아니어도 신청 프로젝트 소유 템플릿은 신청할 수 있다(다른 프로젝트 소유는 거부 — 신청 확인 시험)
    r = client.post("/api/core/tickets/cluster", json={**BODY, "templateId": "t-mine"}, headers=ADMIN)
    assert r.status_code == 201, r.text
    assert r.json()["templateName"] == "mine"


def test_클러스터_신청_권한(client, fake):
    assert client.post("/api/core/tickets/cluster", json=BODY, headers=bearer(make_token())).status_code == 403
    add_member("ek.lee", role="viewer")
    assert client.post("/api/core/tickets/cluster", json=BODY, headers=bearer(make_token())).status_code == 403


@pytest.mark.parametrize("change,status,detail", [
    ({"masterCount": 2}, 400, "Control Plane 수는 1 또는 3 이어야 합니다"),
    ({"nodeCount": 0}, 400, "Worker 수는 1 ~ 20 이어야 합니다"),
    ({"clusterName": "Bad_Name"}, 400, tickets._CLUSTER_NAME_RULE),
    ({"clusterName": "test-taken"}, 409, "이미 사용 중인 클러스터 이름입니다."),
    ({"templateId": "t-hidden"}, 400, "신청할 수 없는 클러스터 템플릿입니다"),
    ({"templateId": "t-other"}, 400, "신청할 수 없는 클러스터 템플릿입니다"),  # 다른 프로젝트 소유(비공개)
    ({"masterFlavorId": "m1.rngd"}, 400, "Control Plane 에는 가속기 스펙을 쓸 수 없습니다"),
    ({"flavorId": "m1.gone"}, 400, "연결된 디바이스 프로파일이 존재하지 않습니다"),
    ({"flavorId": "nope"}, 400, "선택한 스펙(플레이버)을 찾을 수 없습니다"),
])
def test_클러스터_신청_확인(client, fake, change, status, detail):
    fake["taken"].add("test-taken")
    r = client.post("/api/core/tickets/cluster", json={**BODY, **change}, headers=ADMIN)
    assert (r.status_code, r.json()["detail"]) == (status, detail)


def _ssh_key(owner: str, name: str = "k1") -> int:
    return sql("""INSERT INTO iam_ssh_key (owner, name, public_key, fingerprint)
                  VALUES (:o, :n, :k, 'SHA256:test') RETURNING id""", o=owner, n=name, k=f"ssh-ed25519 AAAA {owner}@h").scalar()


def test_클러스터_신청_키페어는_본인_키만(client, fake):
    mine, other = _ssh_key("admin"), _ssh_key("ek.lee")
    r = client.post("/api/core/tickets/cluster", json={**BODY, "keypairId": other}, headers=ADMIN)
    assert (r.status_code, r.json()["detail"]) == (400, "본인 키페어가 아닙니다")
    t = client.post("/api/core/tickets/cluster", json={**BODY, "keypairId": mine}, headers=ADMIN).json()
    assert (t["keypairId"], t["keypairName"]) == (str(mine), "k1")


def test_네임스페이스_이름은_쿠버네티스_예약어를_피한다():
    pid = "4ee114f1-b9f1-423a-91bf-40dec2fd05fb"
    assert tickets._namespace_name("okc_test_prj", pid) == "okc-test-prj"
    assert tickets._namespace_name("Default", pid) == "ws-4ee114f1"
    assert tickets._namespace_name("kube-lab", pid) == "ws-4ee114f1"


def test_클러스터_신청_OpenStack_키페어는_관리자만(client, fake):
    add_member("ek.lee")
    r = client.post("/api/core/tickets/cluster", json={**BODY, "novaKeypair": "gy-yang-pub"}, headers=bearer(make_token()))
    assert (r.status_code, r.json()["detail"]) == (403, "OpenStack 키페어는 관리자만 지정할 수 있습니다")
    r = client.post("/api/core/tickets/cluster", json={**BODY, "novaKeypair": "nope"}, headers=ADMIN)
    assert (r.status_code, r.json()["detail"]) == (400, "OpenStack 키페어를 찾을 수 없습니다")
    r = client.post("/api/core/tickets/cluster", json={**BODY, "novaKeypair": "gy-yang-pub", "keypairId": _ssh_key("admin")},
                    headers=ADMIN)
    assert (r.status_code, r.json()["detail"]) == (400, "키페어는 하나만 지정할 수 있습니다")
    t = client.post("/api/core/tickets/cluster", json={**BODY, "novaKeypair": "gy-yang-pub"}, headers=ADMIN).json()
    assert (t["novaKeypair"], t["keypairName"], "keypairId" in t) == ("gy-yang-pub", "gy-yang-pub", False)


# ── 프로비저닝 ───────────────────────────────────────────────────

def _cluster_ticket(tid="req-990101-301", status="approved", requester="admin"):
    sql("""INSERT INTO tkt_request (id, type, status, project_id, requested_by, request_spec)
           SELECT :id, 'cluster', :st, :p, id, CAST(:spec AS jsonb) FROM iam_user WHERE username = :u""",
        id=tid, st=status, p=PROJECT, u=requester,
        spec='{"projectName": "okc_test_prj", "clusterName": "test-cl", "templateId": "t1", "masterCount": 1, '
             '"masterFlavorId": "m1.large", "nodeCount": 2, "flavorId": "m1.rngd", "namespace": "okc-test-prj"}')
    return tid


@pytest.fixture
def started(monkeypatch):
    calls: list[tuple] = []

    async def start(workflow_id, inp):
        calls.append((workflow_id, inp))

    monkeypatch.setattr(settings, "temporal_api_base", "temporal.test:7233")
    monkeypatch.setattr(settings, "cluster_skip_setup", False)  # 로컬 config.env 값과 무관하게
    monkeypatch.setattr(runtime, "start_cluster_provision", start)
    return calls


PROVISION = {"expectedStatus": "approved", "provisionSpec": {"networkId": "net", "subnetId": "sub-on"}}


def test_클러스터_프로비저닝(client, fake, started):
    tid = _cluster_ticket()
    r = client.post(f"/api/core/tickets/{tid}/provision-cluster", json=PROVISION, headers=ADMIN)
    assert r.status_code == 200, r.text
    assert r.json()["status"] == "provisioning"
    workflow_id, inp = started[0]
    assert workflow_id == f"cluster-provision-{tid}-1"
    assert inp | {"need": None} == {
        "ticketId": tid, "attempt": 1, "projectId": PROJECT, "projectName": "okc_test_prj", "clusterName": "test-cl",
        "templateId": "t1", "masterCount": 1, "masterFlavorId": "m1.large", "nodeCount": 2, "flavorId": "m1.rngd",
        "networkId": "net", "subnetId": "sub-on", "floatingNetworkId": "public",
        "keypair": None, "namespace": "okc-test-prj", "need": None, "skipSetup": False}  # keypair 없음 = 템플릿 키페어
    assert inp["need"] == {"instances": 3, "vcpu": 12, "ramGb": 24}
    assert any("subnetId=sub-on" in p for p in fake["iaas"])  # Floating IP 는 고른 서브넷 기준으로 판정
    spec = sql("SELECT provision_spec FROM tkt_request WHERE id = :id", id=tid).scalar()
    assert (spec["networkName"], spec["subnetCidr"], spec["flavorName"]) == ("my-network", "10.0.1.0/24", "m1.large.rngd")


def test_클러스터_프로비저닝_템플릿_외부망이_이름이면_ID_로(client, fake, started):
    # Floating IP 확인(라우터 게이트웨이 = 네트워크 ID 비교)·워크플로에는 외부망 ID 를 넘긴다
    tid = _cluster_ticket()
    sql("UPDATE tkt_request SET request_spec = request_spec || '{\"templateId\": \"t-byname\"}' WHERE id = :id", id=tid)
    assert client.post(f"/api/core/tickets/{tid}/provision-cluster", json=PROVISION, headers=ADMIN).status_code == 200
    assert started[0][1]["floatingNetworkId"] == "pub-shared"
    assert any("floatingNetworkId=pub-shared" in p for p in fake["iaas"])
    # 없는 외부망 — 시작하지 않는다
    sql("UPDATE tkt_request SET status = 'approved', request_spec = request_spec || '{\"templateId\": \"t-badext\"}' "
        "WHERE id = :id", id=tid)
    r = client.post(f"/api/core/tickets/{tid}/provision-cluster", json=PROVISION, headers=ADMIN)
    assert (r.status_code, r.json()["detail"]) == (400, "클러스터 템플릿의 외부 네트워크(nope)를 찾을 수 없습니다")
    assert len(started) == 1


def test_마감_기록은_프로비저닝_시작자와_시각_재시도면_비움(client, fake, started):
    tid = _cluster_ticket()
    assert client.post(f"/api/core/tickets/{tid}/provision-cluster", json=PROVISION, headers=ADMIN).status_code == 200
    activities.finish(tid, "failed", None, "시험 실패")  # 워크플로가 마감 — 처리자 = 프로비저닝을 시작한 관리자
    row = sql("""SELECT finished_by = provisioning_by AS same, finished_at IS NOT NULL AS stamped
                   FROM tkt_request WHERE id = :id""", id=tid).mappings().one()
    assert row["same"] and row["stamped"]
    item = next(x for x in client.get("/api/core/tickets", headers=ADMIN).json()["items"] if x["id"] == tid)
    assert item["finishedBy"] == "admin" and item["finishedAt"]
    # 재시도(새 회차)를 시작하면 비운다
    retry = {"expectedStatus": "failed", "provisionSpec": PROVISION["provisionSpec"]}
    assert client.post(f"/api/core/tickets/{tid}/provision-cluster", json=retry, headers=ADMIN).status_code == 200
    assert sql("SELECT finished_by IS NULL AND finished_at IS NULL FROM tkt_request WHERE id = :id", id=tid).scalar()


def test_클러스터_프로비저닝_로컬은_설정_단계_건너뜀(client, fake, started, monkeypatch):
    monkeypatch.setattr(settings, "cluster_skip_setup", True)  # CLUSTER_SKIP_SETUP=true → 워크플로 입력 skipSetup
    tid = _cluster_ticket()
    assert client.post(f"/api/core/tickets/{tid}/provision-cluster", json=PROVISION, headers=ADMIN).status_code == 200
    assert started[0][1]["skipSetup"] is True


def test_클러스터_프로비저닝_노드_조정(client, fake, started):
    tid = _cluster_ticket()
    body = {"provisionSpec": {"networkId": "net", "subnetId": "sub-on", "nodeCount": 3, "flavorId": "m1.large"}}
    assert client.post(f"/api/core/tickets/{tid}/provision-cluster", json=body, headers=ADMIN).status_code == 200
    inp = started[0][1]
    assert (inp["nodeCount"], inp["flavorId"], inp["need"]) == (3, "m1.large", {"instances": 4, "vcpu": 16, "ramGb": 32})


def test_클러스터_프로비저닝_키페어는_이름으로_찾아_넘김(client, fake, started):
    # 신청 키페어 이름(관리자 = OpenStack 키페어, 사용자 = 포털 SSH 키)을 OpenStack 에서 찾아 그 이름을 넘긴다 — 새로 만들지 않음
    tid = _cluster_ticket()
    sql("""UPDATE tkt_request SET request_spec = request_spec || '{"novaKeypair": "gy-yang-pub", "keypairName": "gy-yang-pub"}'
            WHERE id = :id""", id=tid)
    assert client.post(f"/api/core/tickets/{tid}/provision-cluster", json=PROVISION, headers=ADMIN).status_code == 200
    assert started[0][1]["keypair"] == "gy-yang-pub" and "publicKey" not in started[0][1]
    spec = sql("SELECT provision_spec FROM tkt_request WHERE id = :id", id=tid).scalar()
    assert spec["keypairName"] == "gy-yang-pub"


def test_클러스터_프로비저닝_신청_키페어가_OpenStack_에_없으면(client, fake, started):
    # 사용자 포털 SSH 키 — 같은 이름의 OpenStack 키페어가 없으면 시작하지 않는다(관리자가 직접 선택으로 바꾼다)
    tid = _cluster_ticket(requester="ek.lee")
    key = _ssh_key("ek.lee", "k1")
    sql("""UPDATE tkt_request SET request_spec = request_spec || jsonb_build_object('keypairId', CAST(:k AS text),
                                                                                    'keypairName', 'k1')
            WHERE id = :id""", k=key, id=tid)
    r = client.post(f"/api/core/tickets/{tid}/provision-cluster", json=PROVISION, headers=ADMIN)
    assert (r.status_code, r.json()["detail"]) == (
        409, "신청한 키페어(k1)가 OpenStack 에 없어 노드에 넣을 수 없습니다 — 프로비저닝 설정에서 다른 키페어를 고르세요")
    fake["keypairs"].append({"name": "k1", "publicKey": "ssh-ed25519 AAAA k1@h", "fingerprint": "SHA256:k1"})
    assert client.post(f"/api/core/tickets/{tid}/provision-cluster", json=PROVISION, headers=ADMIN).status_code == 200
    assert started[0][1]["keypair"] == "k1"


def test_클러스터_프로비저닝_템플릿_키페어가_OpenStack_에_없으면(client, fake, started, monkeypatch):
    # 키페어를 넘기지 않으면 Magnum 이 템플릿 키페어를 넣는다 — 없으면 시작하지 않는다(관리자가 다른 키페어를 고른다)
    monkeypatch.setitem(TEMPLATES[0], "keypair_id", "k8s-key")
    tid = _cluster_ticket()
    r = client.post(f"/api/core/tickets/{tid}/provision-cluster", json=PROVISION, headers=ADMIN)
    assert (r.status_code, r.json()["detail"]) == (
        409, "템플릿 키페어(k8s-key)가 OpenStack 에 없어 노드에 넣을 수 없습니다 — 프로비저닝 설정에서 다른 키페어를 고르세요")
    body = {"provisionSpec": {**PROVISION["provisionSpec"], "novaKeypair": "gy-yang-pub"}}  # 다른 키페어를 고르면 시작
    assert client.post(f"/api/core/tickets/{tid}/provision-cluster", json=body, headers=ADMIN).status_code == 200
    assert started[0][1]["keypair"] == "gy-yang-pub"


def test_클러스터_프로비저닝_키페어_바꾸기(client, fake, started):
    # 관리자가 직접 고른 OpenStack 키페어 — 없으면 400, null 이면 템플릿 키페어
    tid = _cluster_ticket(requester="ek.lee")

    def provision(**key):
        return client.post(f"/api/core/tickets/{tid}/provision-cluster",
                           json={"provisionSpec": {**PROVISION["provisionSpec"], **key}}, headers=ADMIN)

    r = provision(novaKeypair="nope")
    assert (r.status_code, r.json()["detail"]) == (400, "OpenStack 키페어를 찾을 수 없습니다")
    assert provision(novaKeypair="gy-yang-pub").status_code == 200
    assert started[0][1]["keypair"] == "gy-yang-pub"
    sql("UPDATE tkt_request SET status = 'approved' WHERE id = :id", id=tid)
    assert provision(novaKeypair=None).status_code == 200
    assert started[1][1]["keypair"] is None
    assert sql("SELECT provision_spec FROM tkt_request WHERE id = :id", id=tid).scalar()["keypairName"] is None


@pytest.mark.parametrize("spec,setup,status,detail", [
    ({"subnetId": "sub-off"}, None, 400, "DHCP 가 꺼진 서브넷에는 노드가 주소를 받지 못합니다"),
    ({"subnetId": "nope"}, None, 400, "서브넷이 고른 네트워크에 속하지 않습니다"),
    ({"networkId": "nope"}, None, 400, "이 프로젝트에서 사용할 수 없는 네트워크입니다"),
    # 외부망은 공유돼 있어도 안 된다 — API 로드밸런서(OVN) 주소에 밖에서 닿지 못함(2026-10-22 req-261022-007)
    ({"networkId": "public"}, None, 400, "외부망에는 클러스터를 만들 수 없습니다 — 노드를 외부망에 직접 연결하면 클러스터 외부에서 API 주소(OVN 로드밸런서)에 접근할 수 없습니다"),
    ({"networkId": "pub-shared", "subnetId": "sub-pub"}, None, 400, "외부망에는 클러스터를 만들 수 없습니다 — 노드를 외부망에 직접 연결하면 클러스터 외부에서 API 주소(OVN 로드밸런서)에 접근할 수 없습니다"),
    ({}, ("fip", {"reachable": False, "reason": "게이트웨이 없음"}), 400,
     "클러스터 API 에 Floating IP 를 붙일 수 없는 네트워크입니다\n게이트웨이 없음"),
    ({}, ("quota", {**QUOTA, "instances": {"quota": 4, "used": 2}}), 409,
     "프로젝트 쿼터가 부족합니다 — VM 필요 3대 · 남음 2대"),
    ({}, ("taken", {"test-cl"}), 409, "이미 사용 중인 클러스터 이름입니다."),
])
def test_클러스터_프로비저닝_확인(client, fake, started, spec, setup, status, detail):
    tid = _cluster_ticket()
    if setup:
        fake[setup[0]] = setup[1]
    body = {"provisionSpec": {**PROVISION["provisionSpec"], **spec}}
    r = client.post(f"/api/core/tickets/{tid}/provision-cluster", json=body, headers=ADMIN)
    assert (r.status_code, r.json()["detail"]) == (status, detail)
    assert not started and sql("SELECT status FROM tkt_request WHERE id = :id", id=tid).scalar() == "approved"


def test_클러스터_프로비저닝은_관리자만_토큰으로(client, fake, started):
    tid = _cluster_ticket()
    add_member("ek.lee", role="owner")
    body = {**PROVISION, "processedBy": "admin"}  # 본문의 이름은 쓰지 않는다
    assert client.post(f"/api/core/tickets/{tid}/provision-cluster", json=body, headers=bearer(make_token())).status_code == 403


def test_클러스터_프로비저닝_시작_실패면_되돌림(client, fake, monkeypatch):
    tid = _cluster_ticket(status="failed")
    sql("UPDATE tkt_request SET error_message = '이전 실패', workflow_id = :wf WHERE id = :id",
        id=tid, wf=f"cluster-provision-{tid}-1")

    async def start(workflow_id, inp):
        raise RuntimeError("Temporal 연결 실패(시험)")

    monkeypatch.setattr(settings, "temporal_api_base", "temporal.test:7233")
    monkeypatch.setattr(runtime, "start_cluster_provision", start)
    body = {"provisionSpec": PROVISION["provisionSpec"]}
    r = client.post(f"/api/core/tickets/{tid}/provision-cluster", json=body, headers=ADMIN)
    assert r.status_code == 503
    row = sql("SELECT status, error_message, workflow_id FROM tkt_request WHERE id = :id", id=tid).one()
    assert tuple(row) == ("failed", "이전 실패", f"cluster-provision-{tid}-1")


# ── 워크플로 DB 작업 ─────────────────────────────────────────────

def test_단계_등록과_접근_권한(client):
    tid = _cluster_ticket(status="provisioning")
    plan = activities.init_steps_paas(tid, 1)
    assert [p["sub"] for p in plan] == ["check_name", "check_quota", "check_network", "send_request", "prepare_api",
                                        "create_cp_vm", "init_control_plane", "create_workers", "create_control_plane",
                                        "finish_cluster", "check_api", "create_namespace", "grant_access"]
    assert {p["step"] for p in plan[:3]} == {"check_creatable"} and plan[9]["step"] == "create_cluster"
    assert plan[10] == {"step": "setup_cluster", "sub": "check_api"}  # K8s API 연결은 클러스터를 다 만든 뒤 단계
    assert activities.init_steps_paas(tid, 1) == plan  # 다시 불러도 같은 결과(재시도)
    assert sql("SELECT count(*) FROM tkt_provision_step WHERE request_id = :id", id=tid).scalar() == 3
    # 진행 조회 API 가 공통코드 이름으로 보여 준다
    steps = client.get(f"/api/core/tickets/{tid}/steps", headers=ADMIN).json()
    assert [s["message"] for s in steps["items"]] == ["클러스터 생성 가능 여부 확인중", "클러스터 생성", "네임스페이스 및 접근 권한"]
    assert [d["label"] for d in steps["items"][1]["details"]] == [
        "생성 요청 전송", "API 로드밸런서·공인 IP", "Control Plane VM 생성", "Control Plane 초기화", "Worker VM 생성", "노드 준비",
        "클러스터 마무리"]
    assert [d["label"] for d in steps["items"][2]["details"]][0] == "K8s API 연결"

    for _ in range(2):  # 멱등 — NULL namespace 도 한 줄만
        activities.grant_cluster_access(PROJECT, "c-1")
    assert sql("SELECT count(*) FROM paas_project_cluster WHERE cluster_id = 'c-1' AND namespace IS NULL").scalar() == 1
