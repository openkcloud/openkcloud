"""역할별 권한 — API 75개 전부. 사용자(gy.yang)는 소속 프로젝트(p-mine) 자원의 조회와
가상서버 상세의 사용자 기능(콘솔·전원·네트워크/볼륨 연결·분리)만, 나머지 쓰기와 라우터는 관리자만."""
import httpx
import pytest
from conftest import ADMIN, MINE, OTHER, USER, _blocked, client_as

from app import auth
from app.routers import flavors, images, networks, projects, servers

A, D = "허용", "거부"
P = "/api/iaas"

CASES = [
    # 가상서버
    ("GET", "/servers", None, A),
    ("GET", f"/projects/{MINE}/server-names", None, A), ("GET", f"/projects/{OTHER}/server-names", None, D),
    ("POST", f"/projects/{MINE}/servers", {"name": "v", "flavorId": "f", "imageId": "i", "volumeGb": 1, "networkId": "n"}, D),
    ("GET", "/servers/mine-s/accelerator-requests", None, A), ("GET", "/servers/other-s/accelerator-requests", None, D),
    ("GET", "/servers/mine-s", None, A), ("GET", "/servers/other-s", None, D),
    ("POST", "/servers/mine-s/interfaces", {"networkId": "mine-net"}, A),
    ("POST", "/servers/mine-s/interfaces", {"networkId": "ext-net"}, A),       # 외부망(다른 프로젝트 소유)은 연결 가능
    ("POST", "/servers/mine-s/interfaces", {"networkId": "other-net"}, D),
    ("POST", "/servers/other-s/interfaces", {"networkId": "mine-net"}, D),
    ("DELETE", "/servers/mine-s/interfaces/port1", None, A), ("DELETE", "/servers/other-s/interfaces/port1", None, D),
    # 보안그룹 추가·제거 — 서버 접근 권한과 동일(server_access)
    ("POST", "/servers/mine-s/security-groups", {"name": "sg"}, A), ("POST", "/servers/other-s/security-groups", {"name": "sg"}, D),
    ("DELETE", "/servers/mine-s/security-groups/sg", None, A), ("DELETE", "/servers/other-s/security-groups/sg", None, D),
    ("POST", "/servers/mine-s/resize", {"flavorId": "f"}, D),
    ("POST", "/servers/mine-s/resize-confirm", None, D), ("POST", "/servers/mine-s/resize-revert", None, D),
    ("GET", "/servers/mine-s/diagnostics", None, A), ("GET", "/servers/other-s/diagnostics", None, D),
    ("POST", "/servers/mine-s/power", {"action": "stop"}, A), ("POST", "/servers/other-s/power", {"action": "stop"}, D),
    ("GET", "/servers/mine-s/console", None, A), ("GET", "/servers/other-s/console", None, D),
    # 인스턴스 스냅샷 — 서버 접근 권한(server_access)
    ("POST", "/servers/mine-s/snapshot", {"name": "snap"}, A), ("POST", "/servers/other-s/snapshot", {"name": "snap"}, D),
    ("DELETE", "/servers/mine-s", None, D),
    ("POST", "/servers/mine-s/volumes", {"volumeId": "mine-v"}, A),
    ("POST", "/servers/mine-s/volumes", {"volumeId": "other-v"}, D),
    ("DELETE", "/servers/mine-s/volumes/mine-v", None, A), ("DELETE", "/servers/other-s/volumes/v", None, D),
    # 볼륨
    ("GET", "/volumes", None, A), ("GET", "/volume-types", None, A), ("GET", "/storage-capacity", None, A),
    ("GET", "/volumes/mine-v", None, A), ("GET", "/volumes/other-v", None, D),
    ("POST", "/volumes", {"name": "x", "size": 1, "projectId": MINE}, D),
    ("DELETE", "/volumes/mine-v", None, D), ("POST", "/volumes/mine-v/extend", {"newSize": 2}, D),
    ("POST", "/snapshots", {"volumeId": "mine-v", "name": "s"}, D), ("DELETE", "/snapshots/sn", None, D),
    # 네트워크·보안그룹·FIP·라우터
    ("GET", "/networks", None, A),
    ("POST", "/networks", {"name": "n", "projectId": MINE}, D),
    ("POST", "/networks/mine-net/subnets", {"name": "s", "cidr": "10.0.0.0/24"}, D),
    ("DELETE", "/networks/mine-net", None, D),
    ("GET", "/networks/mine-net", None, A), ("GET", "/networks/other-net", None, D),
    ("GET", "/networks/ext-net", None, D),                                     # 공유·외부망 상세(다른 프로젝트 포트)는 관리자만
    ("GET", "/security-groups", None, A),
    ("GET", "/security-groups/mine-sg", None, A), ("GET", "/security-groups/other-sg", None, D),
    ("POST", "/security-groups", {"name": "g", "projectId": MINE}, D),
    ("POST", "/security-groups/mine-sg/rules", {"direction": "ingress"}, D),   # 보안그룹은 사용자 조회만
    ("DELETE", "/security-group-rules/r1", None, D), ("DELETE", "/security-groups/mine-sg", None, D),
    ("GET", f"/projects/{MINE}/fip-targets", None, A), ("GET", f"/projects/{OTHER}/fip-targets", None, D),
    ("POST", "/floatingips", {"projectId": MINE}, D), ("DELETE", "/floatingips/f1", None, D),
    ("PUT", "/floatingips/f1", {"portId": "p"}, D),
    ("GET", "/routers", None, D), ("GET", "/routers/r1", None, D),
    ("POST", "/routers", {"name": "r", "projectId": MINE}, D), ("DELETE", "/routers/r1", None, D),
    ("PUT", "/routers/r1/add-interface", {"subnetId": "s"}, D), ("PUT", "/routers/r1/remove-interface", {"subnetId": "s"}, D),
    ("GET", f"/projects/{MINE}/subnets", None, A), ("GET", f"/projects/{OTHER}/subnets", None, D),
    ("GET", f"/projects/{MINE}/networks", None, A), ("GET", f"/projects/{OTHER}/networks", None, D),
    ("GET", f"/projects/{MINE}/security-groups", None, A), ("GET", f"/projects/{OTHER}/security-groups", None, D),
    ("GET", "/networks/mine-net/ip-check?ip=10.0.0.1", None, D),
    ("GET", "/networks/mine-net/fip-reachable?floatingNetworkId=x", None, D),
    # 프로젝트
    ("GET", "/projects", None, A),
    ("POST", "/projects", {"name": "p"}, D), ("PATCH", f"/projects/{MINE}", {"description": "d"}, D),
    ("DELETE", f"/projects/{MINE}", None, D),
    ("GET", f"/projects/{MINE}", None, A), ("GET", f"/projects/{OTHER}", None, D),
    # 이미지 (GET /images/{id} 는 공개 여부를 봐야 해서 아래 따로)
    ("GET", "/images", None, A),
    ("GET", f"/projects/{MINE}/images", None, A), ("GET", f"/projects/{OTHER}/images", None, D),
    ("PATCH", "/images/i1", {"visibility": "public"}, D), ("DELETE", "/images/i1", None, D),
    ("POST", "/images", {"name": "i", "url": "http://x/i.qcow2"}, D),           # URL 가져오기 등록
    ("PATCH", "/images/i1/properties", {"set": {"os_distro": "ubuntu"}}, D),     # 속성(메타데이터) 편집
    # 플레이버
    ("GET", "/flavors", None, A), ("GET", "/device-profiles", None, A),
    ("GET", f"/projects/{MINE}/flavors", None, A), ("GET", f"/projects/{OTHER}/flavors", None, D),
    ("GET", "/placement-preview?flavorId=f1&projectId=p1", None, D),
    ("POST", "/flavors", {"name": "f", "vcpus": 1, "ramMb": 512, "diskGb": 1}, D), ("DELETE", "/flavors/f1", None, D),
    ("POST", "/device-profiles", {"name": "dp", "accelerators": []}, D), ("DELETE", "/device-profiles/dp1", None, D),
    ("GET", "/accelerator-devices", None, D),             # 연결 VM 에 모든 프로젝트 VM 이 보여 관리자만
    # 키페어 — 조회는 클러스터 신청에서 고르므로 사용자도, 등록·삭제는 관리자만
    ("GET", "/keypairs", None, A),
    ("POST", "/keypairs", {"name": "k", "publicKey": "ssh-ed25519 AAAA"}, D), ("DELETE", "/keypairs/k1", None, D),
]


def _call(client, method, path, body):
    return client.request(method, P + path, json=body)


@pytest.mark.parametrize("method,path,body,expected", CASES, ids=[f"{m} {p} {b or ''}" for m, p, b, _ in CASES])
def test_사용자_권한(method, path, body, expected):
    r = _call(client_as(USER), method, path, body)
    if expected == D:
        assert r.status_code == 403 and r.json()["code"] == "FORBIDDEN", r.text
    else:
        assert r.status_code not in (401, 403), r.text  # 권한 통과 — 이후 OpenStack 단계는 막혀 502 등


@pytest.mark.parametrize("method,path,body,expected", CASES, ids=[f"{m} {p} {b or ''}" for m, p, b, _ in CASES])
def test_관리자는_모두_통과(method, path, body, expected):
    r = _call(client_as(ADMIN), method, path, body)
    assert r.status_code not in (401, 403), r.text


def test_모든_API가_시험표에_있음():
    """API 를 새로 추가하면 이 표에도 권한 규칙을 넣게 한다(경로 템플릿 {x} 를 한 마디로 맞춰 본다)."""
    import re
    from app.main import app
    called = {(m, P + p.split("?")[0]) for m, p, *_ in CASES} | {("GET", P + "/images/pub")}
    missing = []
    for path, ops in app.openapi()["paths"].items():
        if path == "/healthz":
            continue
        rx = re.compile("^" + re.sub(r"\{[^}]+\}", "[^/]+", path) + "$")
        missing += [(m.upper(), path) for m in ops if not any(cm == m.upper() and rx.match(cp) for cm, cp in called)]
    assert not missing, f"권한 시험표에 없는 API: {missing}"


# ── 목록 거르기 ──────────────────────────────────────────────────

def test_가상서버_목록은_소속_프로젝트만(monkeypatch):
    fake = {"/servers/detail?all_tenants=1": {"servers": [{"id": "a", "tenant_id": MINE}, {"id": "b", "tenant_id": OTHER}]},
            "/flavors/detail": {"flavors": []}}
    monkeypatch.setattr(servers, "_nova_req", lambda m, path, **k: type("R", (), {"json": lambda self: fake[path]})())
    assert [s["id"] for s in client_as(USER).get(P + "/servers").json()["items"]] == ["a"]
    assert len(client_as(ADMIN).get(P + "/servers").json()["items"]) == 2


def test_네트워크_목록은_소속_망과_공유_외부망(monkeypatch):
    nets = [{"id": "mine", "project_id": MINE}, {"id": "other", "project_id": OTHER},
            {"id": "ext", "project_id": OTHER, "router:external": True}, {"id": "shr", "project_id": OTHER, "shared": True}]
    fake = {"/networks": {"networks": nets}, "/subnets": {"subnets": []}, "/ports": {"ports": []}}
    monkeypatch.setattr(networks, "_neutron_get", lambda path, params=None: fake[path])
    assert [n["id"] for n in client_as(USER).get(P + "/networks").json()["items"]] == ["mine", "ext", "shr"]


def test_승인용_네트워크의_공유_여부(monkeypatch):
    # 공유된 외부망(pub)에는 노드를 바로 붙일 수 있다 — 공유 안 된 외부망(ext)은 안 된다. RBAC 공유(rbac)도 공유
    nets = [{"id": "mine", "project_id": MINE}, {"id": "ext", "project_id": OTHER, "router:external": True},
            {"id": "pub", "project_id": OTHER, "router:external": True, "shared": True}, {"id": "rbac", "project_id": OTHER}]
    fake = {"/networks": {"networks": nets}, "/subnets": {"subnets": []},
            "/rbac-policies": {"rbac_policies": [{"object_id": "rbac", "target_tenant": MINE}]},
            "/ports": {"ports": []}, "/routers": {"routers": []}}
    monkeypatch.setattr(networks, "_neutron_get", lambda path, params=None: fake[path])
    items = client_as(USER).get(P + f"/projects/{MINE}/networks").json()["items"]
    assert {n["id"]: (n["kind"], n["shared"]) for n in items} == {
        "mine": ("project", False), "ext": ("external", False), "pub": ("external", True), "rbac": ("shared", True)}


def test_이미지는_공개와_소속_프로젝트_것(monkeypatch):
    imgs = [{"id": "pub", "owner": OTHER, "visibility": "public"}, {"id": "mine", "owner": MINE, "visibility": "private"},
            {"id": "oth", "owner": OTHER, "visibility": "private"}]

    def glance(method, path, **k):
        data = {"images": imgs} if path == "/images" else next(i for i in imgs if path.endswith(i["id"]))
        return type("R", (), {"json": lambda self: data})()

    monkeypatch.setattr(images, "_glance_req", glance)
    user = client_as(USER)
    assert [i["id"] for i in user.get(P + "/images").json()["items"]] == ["pub", "mine"]
    assert user.get(P + "/images/pub").status_code == 200
    assert user.get(P + "/images/oth").status_code == 403


def test_프로젝트_목록은_소속만(monkeypatch):
    monkeypatch.setattr(projects, "_fetch_projects", lambda: [{"id": MINE, "name": "m"}, {"id": OTHER, "name": "o"}])
    assert [p["id"] for p in client_as(USER).get(P + "/projects").json()["items"]] == [MINE]


def test_승인용_네트워크_보안그룹에서_Magnum_클러스터_자원_제외(monkeypatch):
    mag = f"magnum-{MINE}-okc-test-clstr-1-7mdm"
    fake = {
        "/networks": {"networks": [{"id": "n1", "name": "my-network", "project_id": MINE},
                                   {"id": "n2", "name": f"k8s-clusterapi-cluster-{mag}", "project_id": MINE}]},
        "/subnets": {"subnets": []}, "/rbac-policies": {"rbac_policies": []}, "/ports": {"ports": []}, "/routers": {"routers": []},
        "/security-groups": {"security_groups": [{"id": "s1", "name": "default"},
                                                 {"id": "s2", "name": f"k8s-cluster-{mag}-secgroup-worker"},
                                                 {"id": "s3", "name": f"k8s-cluster-{mag}-secgroup-controlplane"}]},
    }
    monkeypatch.setattr(networks, "_neutron_get", lambda path, params=None: fake[path])
    user = client_as(USER)
    assert [n["name"] for n in user.get(P + f"/projects/{MINE}/networks").json()["items"]] == ["my-network"]
    assert [g["name"] for g in user.get(P + f"/projects/{MINE}/security-groups").json()["items"]] == ["default"]


def test_배치_가용량_미리보기(monkeypatch):
    from urllib.parse import parse_qs
    nova = {"/flavors/f1": {"flavor": {"id": "f1", "vcpus": 4, "ram": 8192, "disk": 80}},
            "/flavors/f1/os-extra_specs": {"extra_specs": {"accel:device_profile": "dp-rngd"}}}
    dps = {"device_profiles": [{"name": "dp-rngd", "groups": [
        {"resources:CUSTOM_AICHIP": "1", "trait:CUSTOM_FURIOSA_0001": "required"}]}]}
    host = lambda vu, mu: {"resources": {"VCPU": {"capacity": 64, "used": vu}, "MEMORY_MB": {"capacity": 65536, "used": mu}},
                           "traits": []}
    dev = lambda root, used: {"resources": {"CUSTOM_AICHIP": {"capacity": 1, "used": used}}, "root_provider_uuid": root,
                              "traits": ["CUSTOM_FURIOSA_0001"]}
    sums = {"r1": {**host(10, 8192), "root_provider_uuid": "r1"},
            "r2": {**host(40, 32768), "root_provider_uuid": "r2", "traits": ["COMPUTE_STATUS_DISABLED"]},  # 꺼 둔 노드
            "d1": dev("r1", 0), "d2": dev("r2", 1)}
    names = {"resource_providers": [{"uuid": u, "name": n} for u, n in
                                    [("r1", "compute1"), ("r2", "compute2"), ("d1", "compute1_dev"), ("d2", "compute2_dev")]]}
    queries = []

    def placement(method, path, mv="1.6"):
        if path.startswith("/allocation_candidates"):
            q = parse_qs(path.split("?", 1)[1])
            queries.append(q)
            reqs = [{"allocations": {"r1": {}, "d1": {}}}] if "resources1" in q else \
                [{"allocations": {"r1": {}}}, {"allocations": {"r2": {}}}]
            data = {"allocation_requests": reqs, "provider_summaries": sums}
        else:
            data = names
        return type("R", (), {"json": lambda self: data})()

    monkeypatch.setattr(flavors, "_nova", lambda m, path, **k: type("R", (), {"json": lambda self: nova[path]})())
    monkeypatch.setattr(flavors, "_cyborg", lambda m, path, **k: type("R", (), {"json": lambda self: dps})())
    monkeypatch.setattr(flavors, "_placement", placement)
    monkeypatch.setattr(flavors, "get_project_quota", lambda pid: {"vcpu": {"quota": 20, "used": 8}})
    monkeypatch.setattr(servers, "can_pin_host", lambda pid: True)
    d = client_as(ADMIN).get(P + "/placement-preview?flavorId=f1&projectId=p1").json()
    # Nova 와 같은 질의 — 노드 디스크는 빼고(부팅 볼륨), 끈 노드 제외, 가속기는 숫자 그룹(자원+trait)
    assert queries[0]["resources"] == ["VCPU:4,MEMORY_MB:8192"] and queries[0]["required"] == ["!COMPUTE_STATUS_DISABLED"]
    assert queries[0]["resources1"] == ["CUSTOM_AICHIP:1"] and queries[0]["required1"] == ["CUSTOM_FURIOSA_0001"]
    assert queries[1] == {"resources": ["VCPU:1"]}  # 후보가 아닌 노드까지 — 모든 컴퓨트 노드
    # 후보 먼저, 후보가 아닌(꺼 둔) 노드도 함께
    assert d["nodes"] == [
        {"host": "compute1", "candidate": True, "disabled": False, "vcpu": {"used": 10, "capacity": 64},
         "ramMb": {"used": 8192, "capacity": 65536}, "accel": [{"used": 0, "capacity": 1}]},
        {"host": "compute2", "candidate": False, "disabled": True, "vcpu": {"used": 40, "capacity": 64},
         "ramMb": {"used": 32768, "capacity": 65536}, "accel": [{"used": 1, "capacity": 1}]},
    ]
    # 가속기 전체 여유는 꺼 둔 노드를 뺀다
    assert d["accelerators"] == [{"kind": "NPU", "resourceClass": "CUSTOM_AICHIP", "traits": ["CUSTOM_FURIOSA_0001"],
                                 "count": 1, "free": 1, "total": 1}]
    assert d["quota"] == {"vcpu": {"quota": 20, "used": 8}} and d["quotaError"] is None and d["hostPinning"] is True

    def no_quota(pid):
        raise flavors.HTTPException(502, "쿼터 조회 실패")
    monkeypatch.setattr(flavors, "get_project_quota", no_quota)  # 쿼터를 못 읽어도 노드 정보는 보여준다
    monkeypatch.setattr(servers, "can_pin_host", _blocked)  # 노드 지정 권한을 확인 못 하면 지정하지 않는다
    d = client_as(ADMIN).get(P + "/placement-preview?flavorId=f1&projectId=p1").json()
    assert d["quota"] is None and d["quotaError"] == "쿼터 조회 실패" and len(d["nodes"]) == 2
    assert d["hostPinning"] is False


def test_노드_지정_권한_확인과_생성_요청(monkeypatch):
    # 권한 확인 — 없는 플레이버로 생성 요청: 정책 거절(403)이면 없음, 요청 내용 거절(400)이면 있음, 그 밖은 판정·캐시 안 함
    replies = {
        "p1": '{"forbidden": {"code": 403, "message": "Policy doesn\'t allow compute:servers:create:requested_destination '
              'to be performed."}}',
        "p2": '{"badRequest": {"code": 400, "message": "Missing imageRef attribute"}}',
        "p3": '{"computeFault": {"code": 500, "message": "Unexpected API Error."}}',
    }
    calls = []

    def probe(method, path, *, json=None, project_id=None, **k):
        calls.append((method, path, project_id, json["server"]))
        raise RuntimeError(replies[project_id])
    monkeypatch.setattr(servers, "_nova_req", probe)
    monkeypatch.setattr(servers, "_pin_cache", {})
    assert servers.can_pin_host("p1") is False and servers.can_pin_host("p1") is False  # 두 번째는 캐시
    assert servers.can_pin_host("p2") is True
    with pytest.raises(RuntimeError):
        servers.can_pin_host("p3")
    assert [c[2] for c in calls] == ["p1", "p2", "p3"] and "p3" not in servers._pin_cache
    assert calls[0][:2] == ("POST", "/servers") and calls[0][3]["hypervisor_hostname"] and calls[0][3]["networks"] == "none"

    # 생성 요청 — 노드를 고르면 hypervisor_hostname 을 넣고, 아니면 넣지 않는다(스케줄러가 고름)
    sent = []

    def boot(method, path, *, json=None, **k):
        sent.append(json["server"])
        return type("R", (), {"json": lambda self: {"server": {"id": "s1"}}})()
    monkeypatch.setattr(servers, "_nova_req", boot)
    body = {"name": "v", "flavorId": "f", "imageId": "i", "volumeGb": 1, "networkId": "n"}
    servers._boot_server("p1", servers.CreateServerBody(**body, host="compute1"), "vol")
    servers._boot_server("p1", servers.CreateServerBody(**body), "vol")
    assert sent[0]["hypervisor_hostname"] == "compute1" and "hypervisor_hostname" not in sent[1]


def test_키페어_이름은_서버_메타데이터로(monkeypatch):
    # 포털은 공개키를 cloud-init 으로 넣어 Nova key_name 이 비므로, 넣은 키 이름을 메타데이터(okc_keypair)로 남긴다
    sent = []

    def boot(method, path, *, json=None, **k):
        sent.append(json["server"])
        return type("R", (), {"json": lambda self: {"server": {"id": "s1"}}})()
    monkeypatch.setattr(servers, "_nova_req", boot)
    body = {"name": "v", "flavorId": "f", "imageId": "i", "volumeGb": 1, "networkId": "n"}
    servers._boot_server("p1", servers.CreateServerBody(**body, publicKey="ssh-ed25519 AAAA", keypairName="ek-key"),
                         "vol")
    servers._boot_server("p1", servers.CreateServerBody(**body), "vol")
    assert sent[0]["metadata"] == {"okc_keypair": "ek-key"} and "metadata" not in sent[1]

    # 상세 키페어 — key_name 이 있으면 그 값(Horizon·Magnum 생성), 없으면 메타데이터
    def key(s):
        return servers._to_server({"id": "s1", **s}, {})["keyName"]
    assert key({"key_name": "nova-key", "metadata": {"okc_keypair": "ek-key"}}) == "nova-key"
    assert key({"key_name": None, "metadata": {"okc_keypair": "ek-key"}}) == "ek-key"
    assert key({"key_name": None, "metadata": {}}) is None


def test_플레이버가_가리키는_디바이스_프로파일이_없음(monkeypatch):
    fl = [{"id": "f1", "name": "rngd"}, {"id": "f2", "name": "2rngd"}, {"id": "f3", "name": "plain"}]
    specs = {"f1": {"accel:device_profile": "dp-rngd"}, "f2": {"accel:device_profile": "dp-2rngd"}, "f3": {}}

    def nova(method, path, **k):
        data = {"flavors": fl} if path == "/flavors/detail" else {"extra_specs": specs[path.split("/")[2]]}
        return type("R", (), {"json": lambda self: data})()

    def cyborg(method, path):
        data = {"device_profiles": [{"name": "dp-rngd", "groups": [{"resources:CUSTOM_AICHIP": "1"}]}]}
        return type("R", (), {"json": lambda self: data})()

    monkeypatch.setattr(flavors, "_nova", nova)
    monkeypatch.setattr(flavors, "_cyborg", cyborg)
    items = client_as(USER).get(P + "/flavors").json()["items"]
    assert {f["name"]: f["deviceProfileMissing"] for f in items} == {"rngd": False, "2rngd": True, "plain": False}

    monkeypatch.setattr(flavors, "_cyborg", _blocked)  # Cyborg 조회 실패면 없다고 단정하지 않는다
    assert not any(f["deviceProfileMissing"] for f in client_as(USER).get(P + "/flavors").json()["items"])


# ── 사용자 확인(MNT-CORE /auth/me) ───────────────────────────────

def _core(monkeypatch, handler):
    calls = []

    def h(request):
        calls.append(request.headers.get("authorization"))
        return handler(request)

    monkeypatch.setattr(auth, "_client", httpx.AsyncClient(transport=httpx.MockTransport(h)))
    return calls


def test_CORE_판정과_캐시(monkeypatch):
    from app.main import app
    from fastapi.testclient import TestClient
    calls = _core(monkeypatch, lambda r: httpx.Response(200, json={"username": "gy.yang", "role": "user",
                                                                   "projectIds": [MINE]}))
    c = TestClient(app)
    for _ in range(2):
        assert c.get(P + f"/projects/{OTHER}/subnets", headers={"Authorization": "Bearer t1"}).status_code == 403
    assert calls == ["Bearer t1"]  # 같은 토큰은 캐시(5초)


def test_CORE_거부는_그대로_전달(monkeypatch):
    from app.main import app
    from fastapi.testclient import TestClient
    _core(monkeypatch, lambda r: httpx.Response(401, json={"detail": "다른곳에서 로그인되었습니다.",
                                                           "code": "SESSION_ENDED", "reason": "duplicate"}))
    r = TestClient(app).get(P + "/flavors", headers={"Authorization": "Bearer t2"})
    assert r.status_code == 401
    assert r.json() == {"detail": "다른곳에서 로그인되었습니다.", "code": "SESSION_ENDED", "reason": "duplicate"}
    assert TestClient(app).get(P + "/flavors").json()["code"] == "TOKEN_REQUIRED"
