"""Floating IP 연결 가능 여부(fip-reachable) — 서브넷 게이트웨이 IP 를 가진 라우터가 FIP 풀에 게이트웨이를 둬야 가능.
망 유형(인터넷망·내부망, 클러스터 프로비저닝 망 표)도 같은 기준 — 게이트웨이 IP 를 가진 라우터에 외부 게이트웨이가 있으면 인터넷망.

VM·노드는 기본 게이트웨이(서브넷 게이트웨이 IP)로 응답을 보내므로, 외부망 라우터가 서브넷에 붙어 있어도
게이트웨이 IP 를 다른(외부 게이트웨이 없는) 라우터가 가졌으면 들어온 요청의 응답이 나가지 못한다(비대칭).
"""
from conftest import ADMIN, client_as

from app.routers import networks

P = "/api/iaas/networks/net/fip-reachable?floatingNetworkId=pub"
SUBNETS = [{"id": "s1", "name": "a", "cidr": "10.0.1.0/24", "ip_version": 4, "gateway_ip": "10.0.1.1"},
           {"id": "s2", "name": "b", "cidr": "10.0.0.0/24", "ip_version": 4, "gateway_ip": "10.0.0.1"}]
ROUTERS = [{"id": "r-ext", "external_gateway_info": {"network_id": "pub"}},
           {"id": "r-int", "external_gateway_info": None}]


def _iface(router: str, subnet: str, ip: str) -> dict:
    return {"id": f"{router}-{subnet}", "device_owner": "network:router_interface", "device_id": router,
            "fixed_ips": [{"subnet_id": subnet, "ip_address": ip}]}


def _check(monkeypatch, ports, subnets=SUBNETS):
    fake = {"/subnets": {"subnets": subnets}, "/ports": {"ports": ports}, "/routers": {"routers": ROUTERS}}
    monkeypatch.setattr(networks, "_neutron_get", lambda path, params=None: fake[path])
    return client_as(ADMIN).get(P).json()


def test_게이트웨이_IP_를_가진_라우터가_외부망에_연결되면_가능(monkeypatch):
    r = _check(monkeypatch, [_iface("r-ext", "s1", "10.0.1.1"), _iface("r-ext", "s2", "10.0.0.1")])
    assert r == {"reachable": True, "reason": None}


def test_외부망_라우터가_게이트웨이가_아닌_IP_로만_붙어_있으면_불가(monkeypatch):
    # 2026-10-19 okc_test_prj 구성 — 게이트웨이(.1)는 내부 라우터, 외부 라우터는 .3
    ports = [_iface("r-int", "s1", "10.0.1.1"), _iface("r-int", "s2", "10.0.0.1"),
             _iface("r-ext", "s1", "10.0.1.3"), _iface("r-ext", "s2", "10.0.0.3")]
    r = _check(monkeypatch, ports)
    assert r["reachable"] is False
    assert "a(10.0.1.0/24)" in r["reason"] and "b(10.0.0.0/24)" in r["reason"]


def test_외부망_라우터와_연결되지_않은_서브넷이_있으면_불가(monkeypatch):
    r = _check(monkeypatch, [_iface("r-ext", "s1", "10.0.1.1")])
    assert r["reachable"] is False and "b(10.0.0.0/24)" in r["reason"] and "a(" not in r["reason"]


def test_게이트웨이가_없는_서브넷은_불가(monkeypatch):
    subnets = [{**SUBNETS[0], "gateway_ip": None}]
    r = _check(monkeypatch, [_iface("r-ext", "s1", "10.0.1.3")], subnets)
    assert r["reachable"] is False and "a(10.0.1.0/24)" in r["reason"]


def test_서브넷을_지정하면_그_서브넷만_본다(monkeypatch):
    # 2026-10-20 okc_test_prj 구성 — 10.0.1.0/24 만 외부망 라우터(게이트웨이 .1), 10.0.0.0/24 는 외부 게이트웨이 없는 라우터
    ports = [_iface("r-ext", "s1", "10.0.1.1"), _iface("r-int", "s2", "10.0.0.1")]
    assert _check(monkeypatch, ports)["reachable"] is False  # 망 전체 기준(VM)은 불가
    fake = {"/subnets": {"subnets": SUBNETS}, "/ports": {"ports": ports}, "/routers": {"routers": ROUTERS}}
    monkeypatch.setattr(networks, "_neutron_get", lambda path, params=None: fake[path])
    assert client_as(ADMIN).get(P + "&subnetId=s1").json() == {"reachable": True, "reason": None}
    r = client_as(ADMIN).get(P + "&subnetId=s2").json()
    assert r["reachable"] is False and "b(10.0.0.0/24)" in r["reason"]
    r = client_as(ADMIN).get(P + "&subnetId=nope").json()
    assert r == {"reachable": False, "reason": "고른 서브넷이 이 네트워크의 IPv4 서브넷이 아닙니다."}


# ── 망 유형(인터넷망·내부망) ─────────────────────────────────────

def test_승인용_망_목록의_유형은_서브넷마다(monkeypatch):
    # okc_test_prj my-network — 10.0.1.0/24 는 외부 게이트웨이 라우터, 10.0.0.0/24 는 외부 게이트웨이 없는 라우터 → 둘 다
    nets = [{"id": "net", "name": "my-network", "project_id": "p1"}, {"id": "int", "name": "only-int", "project_id": "p1"},
            {"id": "none", "name": "no-subnet", "project_id": "p1"}]
    subnets = [{**SUBNETS[0], "network_id": "net"}, {**SUBNETS[1], "network_id": "net"},
               {"id": "s3", "network_id": "int", "cidr": "10.9.0.0/24", "ip_version": 4, "gateway_ip": "10.9.0.1"}]
    ports = [_iface("r-ext", "s1", "10.0.1.1"), _iface("r-int", "s2", "10.0.0.1"), _iface("r-ext", "s3", "10.9.0.3")]
    fake = {"/networks": {"networks": nets}, "/subnets": {"subnets": subnets}, "/ports": {"ports": ports},
            "/routers": {"routers": ROUTERS}, "/rbac-policies": {"rbac_policies": []}}
    monkeypatch.setattr(networks, "_neutron_get", lambda path, params=None: fake[path])
    items = client_as(ADMIN).get("/api/iaas/projects/p1/networks").json()["items"]
    # only-int 는 외부 게이트웨이 라우터가 붙어 있어도 게이트웨이 IP(.1)가 아니라 내부망
    assert {n["id"]: n["access"] for n in items} == {"net": ["internet", "internal"], "int": ["internal"], "none": []}


def test_망_상세의_서브넷_유형(monkeypatch):
    ports = [_iface("r-ext", "s1", "10.0.1.1"), _iface("r-int", "s2", "10.0.0.1")]
    fake = {"/networks/net": {"network": {"id": "net", "name": "my-network", "project_id": "p1"}},
            "/subnets": {"subnets": SUBNETS}, "/ports": {"ports": ports}, "/routers": {"routers": ROUTERS},
            "/security-groups": {"security_groups": []}, "/floatingips": {"floatingips": []}}
    monkeypatch.setattr(networks, "_neutron_get", lambda path, params=None: fake[path])
    subs = client_as(ADMIN).get("/api/iaas/networks/net").json()["subnets"]
    assert {sn["cidr"]: sn["internet"] for sn in subs} == {"10.0.1.0/24": True, "10.0.0.0/24": False}
