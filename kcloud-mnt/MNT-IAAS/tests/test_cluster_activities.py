"""클러스터 프로비저닝이 쓰는 IAAS 작업 — 쿼터 여유(iaas.check_quota)·노드 망(iaas.check_network)."""
import pytest
from temporalio.exceptions import ApplicationError

from app.routers import networks
from app.workflows import activities

NEED = {"instances": 4, "vcpu": 16, "ramGb": 32}


def _quota(instances=(10, 2), vcpu=(20, 3), ram=(50, 6)):
    q = lambda t: {"quota": t[0], "used": t[1]}  # noqa: E731
    return {"instances": q(instances), "vcpu": q(vcpu), "ramGb": q(ram)}


def test_쿼터_여유가_있으면_표시_값(monkeypatch):
    monkeypatch.setattr(activities, "get_project_quota", lambda pid: _quota())
    assert activities.check_quota("p1", NEED) == "VM 4대 · vCPU 16 · RAM 32GB"


def test_쿼터가_모자라면_무엇이_얼마나_모자란지(monkeypatch):
    monkeypatch.setattr(activities, "get_project_quota", lambda pid: _quota(vcpu=(20, 10), ram=(50, 30)))
    with pytest.raises(ApplicationError) as e:
        activities.check_quota("p1", NEED)
    assert e.value.non_retryable
    assert str(e.value) == "프로젝트 쿼터가 부족합니다 — vCPU 필요 16 · 남음 10, RAM 필요 32GB · 남음 20GB"


def test_쿼터_무제한(monkeypatch):
    monkeypatch.setattr(activities, "get_project_quota", lambda pid: _quota(instances=(-1, 50), vcpu=(-1, 99)))
    assert activities.check_quota("p1", NEED).startswith("VM 4대")


NET = {"id": "net", "name": "my-network"}
SUBNET = {"id": "sub", "network_id": "net", "cidr": "10.0.1.0/24", "enable_dhcp": True}


def _neutron(monkeypatch, subnet=SUBNET, reachable=(True, None)):
    data = {"/networks/net": {"network": NET}, "/subnets/sub": {"subnet": subnet}}

    def get(path, params=None):
        if path not in data:
            raise networks.NeutronError("Subnet nope could not be found. (HTTP 404)")
        return data[path]

    monkeypatch.setattr(networks, "_neutron_get", get)
    monkeypatch.setattr(networks, "fip_reachable",
                        lambda n, f, s: {"reachable": reachable[0], "reason": reachable[1]})


def test_노드_망_정상(monkeypatch):
    _neutron(monkeypatch)
    assert activities.check_network("net", "sub", "pub") == "my-network / 10.0.1.0/24"


@pytest.mark.parametrize("subnet,reachable,msg", [
    ({**SUBNET, "network_id": "other"}, (True, None), "서브넷이 고른 네트워크에 속하지 않습니다."),
    ({**SUBNET, "enable_dhcp": False}, (True, None), "DHCP 가 꺼진 서브넷에는 노드가 주소를 받지 못합니다."),
    (SUBNET, (False, "게이트웨이 없음"), "클러스터 API 에 Floating IP 를 붙일 수 없는 네트워크입니다 — 게이트웨이 없음"),
])
def test_노드_망_불가(monkeypatch, subnet, reachable, msg):
    _neutron(monkeypatch, subnet, reachable)
    with pytest.raises(ApplicationError) as e:
        activities.check_network("net", "sub", "pub")
    assert e.value.non_retryable and str(e.value) == msg


def test_외부망도_Floating_IP_확인을_함(monkeypatch):
    # 외부망에 노드를 바로 붙이면 API 로드밸런서 주소(OVN)에 밖에서 닿지 못한다(2026-10-22 req-261022-007) — 예외 없음
    _neutron(monkeypatch, reachable=(False, "게이트웨이 없음"))
    real = networks._neutron_get
    monkeypatch.setattr(networks, "_neutron_get", lambda path, params=None: (
        {"network": {**NET, "name": "public", "router:external": True}} if path == "/networks/net" else real(path, params)))
    with pytest.raises(ApplicationError) as e:
        activities.check_network("net", "sub", "pub")
    assert str(e.value) == "클러스터 API 에 Floating IP 를 붙일 수 없는 네트워크입니다 — 게이트웨이 없음"


def test_없는_서브넷(monkeypatch):
    _neutron(monkeypatch)
    with pytest.raises(ApplicationError) as e:
        activities.check_network("net", "nope", "pub")
    assert e.value.non_retryable and "could not be found" in str(e.value)
