"""IaaS 네트워크 조회 — OpenStack Neutron 중개(읽기 전용).

프로젝트와 동일한 환경독립 패턴:
  - override : 진입 URL(ingress) 직접. neutron base = template.format(service="neutron")+"/v2.0"
  - catalog  : 카탈로그 자동발견(파드). endpoint_filter service_type="network".

프론트 IaasNetwork(목록)·NetworkDetail(상세) 형태로 정규화해 반환.
라우터·보안그룹·FIP 는 네트워크가 아니라 '프로젝트' 소속이므로 네트워크의 project_id 기준으로 묶는다.
대역폭(telemetry)은 Neutron 범위 밖 → null.
"""
import ipaddress

from fastapi import APIRouter, Depends, HTTPException
from fastapi.concurrency import run_in_threadpool
from pydantic import BaseModel

from ..auth import Caller, caller, ensure_project, project_member, require_admin, scoped
from ..config import settings
from ..openstack_client import get_conn, os_request
from .volumes import _cinder_err

router = APIRouter(tags=["networks"])


class NeutronError(RuntimeError):
    """Neutron 이 거절한 요청 — 오류 본문의 원래 메시지를 담는다
    (keystoneauth 는 Neutron 오류 형식을 읽지 못해 'Unrecognized schema in response body' 로 가린다)."""


def _neutron_req(method: str, path: str, *, params: dict | None = None, json: dict | None = None):
    """Neutron 요청 — override/catalog 분기. path 는 '/networks' 처럼 /v2.0 이후 경로.
    조회는 연결 끊김 시 1회 재시도, 생성·변경은 재시도 없이 새 연결로 보낸다(os_request)."""
    conn = get_conn()
    if settings.os_endpoint_mode == "catalog":
        r = os_request(conn, path, method, params=params, json=json, raise_exc=False,
                       endpoint_filter={"service_type": "network", "interface": settings.os_interface}, timeout=15)
    else:  # override
        base = settings.os_endpoint_template.format(service="neutron") + "/v2.0"
        r = os_request(conn, f"{base}{path}", method, params=params, json=json, timeout=15, raise_exc=False)
    if not r.ok:
        raise NeutronError(f"{_cinder_err(r)} (HTTP {r.status_code})")  # 오류 본문 메시지 추출은 Cinder 와 같은 형식
    return r


def _neutron_get(path: str, params: dict | None = None) -> dict:
    return _neutron_req("GET", path, params=params).json()


def _net_type(n: dict) -> str:
    if n.get("router:external"):
        return "external"
    if n.get("shared"):
        return "shared"
    return "tenant"


def _to_network(n: dict, subnet_count: int, port_count: int, cidr: str = "") -> dict:
    """Neutron network → 프론트 IaasNetwork(목록 요약)."""
    return {
        "id": n["id"],
        "name": n.get("name") or n["id"],
        "tenantId": n.get("project_id") or n.get("tenant_id") or "",
        "poolId": n.get("provider:physical_network") or "",
        "cidr": cidr,  # 첫 서브넷 CIDR(여러 개면 대표값)
        "vlan": n.get("provider:segmentation_id"),
        "type": _net_type(n),
        "shared": bool(n.get("shared")),
        "subnets": subnet_count,
        "ports": port_count,
        "status": n.get("status", ""),
    }


def network_info(network_id: str) -> tuple[str, str]:
    """네트워크의 (프로젝트 ID, 종류 tenant|shared|external) — 권한 확인용."""
    try:
        n = _neutron_get(f"/networks/{network_id}")["network"]
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"네트워크 조회 실패: {type(e).__name__}: {e}")
    return n.get("project_id") or n.get("tenant_id") or "", _net_type(n)


async def network_access(network_id: str, c: Caller = Depends(caller)) -> Caller:
    """경로의 네트워크가 소속 프로젝트 것인지(관리자는 통과). 공유·외부망 상세는 다른 프로젝트 포트가 보이므로 관리자만."""
    if not c.is_admin:
        ensure_project(c, (await run_in_threadpool(network_info, network_id))[0])
    return c


def _sg_project(sg_id: str) -> str:
    try:
        sg = _neutron_get(f"/security-groups/{sg_id}")["security_group"]
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"보안 그룹 조회 실패: {type(e).__name__}: {e}")
    return sg.get("project_id") or sg.get("tenant_id") or ""


async def sg_access(sg_id: str, c: Caller = Depends(caller)) -> Caller:
    """경로의 보안 그룹이 소속 프로젝트 것인지(관리자는 통과)."""
    if not c.is_admin:
        ensure_project(c, await run_in_threadpool(_sg_project, sg_id))
    return c


@router.get("/networks")
def list_networks(c: Caller = Depends(caller)):
    """네트워크 목록 + 각 네트워크의 서브넷·포트 수. 사용자는 소속 프로젝트 망 + 공유·외부망(VM 연결 후보)."""
    try:
        nets = _neutron_get("/networks")["networks"]
        subnets = _neutron_get("/subnets", {"fields": ["id", "network_id", "cidr"]})["subnets"]
        ports = _neutron_get("/ports", {"fields": ["id", "network_id"]})["ports"]
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"네트워크 조회 실패: {type(e).__name__}: {e}")

    from collections import Counter
    sc = Counter(s["network_id"] for s in subnets)
    pc = Counter(p["network_id"] for p in ports)
    first_cidr: dict[str, str] = {}
    for s in subnets:
        first_cidr.setdefault(s["network_id"], s.get("cidr", ""))
    items = [
        _to_network(n, sc.get(n["id"], 0), pc.get(n["id"], 0), first_cidr.get(n["id"], ""))
        for n in nets
    ]
    return scoped(c, items, also=lambda x: x["type"] != "tenant")


class CreateNetworkBody(BaseModel):
    name: str
    projectId: str                 # 대상 프로젝트(관리자가 대신 생성)
    cidr: str                      # 서브넷 CIDR (예: 10.0.0.0/24)
    subnetName: str | None = None  # 미지정 시 {네트워크명}-subnet
    gatewayIp: str | None = None   # 미지정 시 Neutron 자동(서브넷 첫 주소)
    enableDhcp: bool = True
    dnsNameservers: list[str] = []
    shared: bool = False


@router.post("/networks", status_code=201, dependencies=[Depends(require_admin)])
def create_network(body: CreateNetworkBody):
    """테넌트 네트워크 + 서브넷을 함께 생성(관리자). 서브넷 실패 시 네트워크 롤백."""
    name = body.name.strip()
    if not name:
        raise HTTPException(400, "네트워크 이름이 필요합니다")
    if not body.cidr.strip():
        raise HTTPException(400, "서브넷 CIDR이 필요합니다")

    # 1) 네트워크 생성
    net_body = {"network": {
        "name": name, "project_id": body.projectId,
        "admin_state_up": True, "shared": body.shared,
    }}
    try:
        net = _neutron_req("POST", "/networks", json=net_body).json()["network"]
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"네트워크 생성 실패: {type(e).__name__}: {e}")

    # 2) 서브넷 생성 (실패 시 네트워크 롤백)
    subnet: dict = {
        "name": (body.subnetName or f"{name}-subnet"), "network_id": net["id"], "project_id": body.projectId,
        "cidr": body.cidr.strip(), "ip_version": 4,
        "enable_dhcp": body.enableDhcp,
    }
    if body.gatewayIp:
        subnet["gateway_ip"] = body.gatewayIp.strip()
    if body.dnsNameservers:
        subnet["dns_nameservers"] = body.dnsNameservers
    try:
        _neutron_req("POST", "/subnets", json={"subnet": subnet})
    except Exception as e:  # noqa: BLE001
        try:
            _neutron_req("DELETE", f"/networks/{net['id']}")
        except Exception:  # noqa: BLE001 — 롤백 실패는 원인 에러를 가리지 않도록 무시
            pass
        raise HTTPException(502, f"서브넷 생성 실패(네트워크 롤백됨): {type(e).__name__}: {e}")

    return get_network(net["id"])


class AddSubnetBody(BaseModel):
    name: str | None = None
    cidr: str
    gatewayIp: str | None = None
    enableDhcp: bool = True
    dnsNameservers: list[str] = []


@router.post("/networks/{network_id}/subnets", status_code=201, dependencies=[Depends(require_admin)])
def add_subnet(network_id: str, body: AddSubnetBody):
    """기존 네트워크에 서브넷 추가(관리자). project_id 는 네트워크에서 상속."""
    if not body.cidr.strip():
        raise HTTPException(400, "CIDR이 필요합니다")
    try:
        net = _neutron_get(f"/networks/{network_id}")["network"]
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"네트워크 조회 실패: {type(e).__name__}: {e}")
    pid = net.get("project_id") or net.get("tenant_id")
    subnet: dict = {
        "name": (body.name or f"{net.get('name') or network_id}-subnet"),
        "network_id": network_id, "project_id": pid,
        "cidr": body.cidr.strip(), "ip_version": 4, "enable_dhcp": body.enableDhcp,
    }
    if body.gatewayIp:
        subnet["gateway_ip"] = body.gatewayIp.strip()
    if body.dnsNameservers:
        subnet["dns_nameservers"] = body.dnsNameservers
    try:
        _neutron_req("POST", "/subnets", json={"subnet": subnet})
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"서브넷 추가 실패: {type(e).__name__}: {e}")
    return get_network(network_id)


@router.delete("/networks/{network_id}", status_code=204, dependencies=[Depends(require_admin)])
def delete_network(network_id: str):
    """네트워크 삭제(관리자). 포트가 남아있으면 Neutron 이 거부 → 에러 그대로 전달."""
    try:
        _neutron_req("DELETE", f"/networks/{network_id}")
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"네트워크 삭제 실패(포트·자원 정리 필요): {type(e).__name__}: {e}")
    return None


def _to_sg_rule(r: dict) -> dict:
    return {
        "id": r["id"],
        "direction": r.get("direction", ""),
        "ethertype": r.get("ethertype", ""),
        "protocol": r.get("protocol"),
        "portMin": r.get("port_range_min"),
        "portMax": r.get("port_range_max"),
        "remoteCidr": r.get("remote_ip_prefix"),
        "remoteGroup": r.get("remote_group_id"),
    }


@router.get("/security-groups/{sg_id}", dependencies=[Depends(sg_access)])
def get_security_group(sg_id: str):
    """보안 그룹 + 규칙 상세."""
    try:
        sg = _neutron_get(f"/security-groups/{sg_id}")["security_group"]
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"보안 그룹 조회 실패: {type(e).__name__}: {e}")
    return {
        "id": sg["id"], "name": sg.get("name") or sg["id"],
        "desc": sg.get("description") or "",
        "rules": [_to_sg_rule(r) for r in sg.get("security_group_rules") or []],
    }


class AddSgRuleBody(BaseModel):
    direction: str = "ingress"          # ingress/egress
    ethertype: str = "IPv4"
    protocol: str | None = None         # tcp/udp/icmp/null(any)
    portMin: int | None = None
    portMax: int | None = None
    remoteCidr: str | None = None       # 예: 0.0.0.0/0


@router.post("/security-groups/{sg_id}/rules", status_code=201, dependencies=[Depends(require_admin)])
def add_sg_rule(sg_id: str, body: AddSgRuleBody):
    """보안 그룹 규칙 추가."""
    rule: dict = {"security_group_id": sg_id, "direction": body.direction, "ethertype": body.ethertype}
    if body.protocol:
        rule["protocol"] = body.protocol
    if body.portMin is not None:
        rule["port_range_min"] = body.portMin
    if body.portMax is not None:
        rule["port_range_max"] = body.portMax
    if body.remoteCidr:
        rule["remote_ip_prefix"] = body.remoteCidr
    try:
        _neutron_req("POST", "/security-group-rules", json={"security_group_rule": rule})
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"규칙 추가 실패: {type(e).__name__}: {e}")
    return get_security_group(sg_id)


@router.delete("/security-group-rules/{rule_id}", status_code=204, dependencies=[Depends(require_admin)])
def delete_sg_rule(rule_id: str):
    """보안 그룹 규칙 삭제."""
    try:
        _neutron_req("DELETE", f"/security-group-rules/{rule_id}")
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"규칙 삭제 실패: {type(e).__name__}: {e}")
    return None


@router.get("/networks/{network_id}", dependencies=[Depends(network_access)])
def get_network(network_id: str):
    """네트워크 상세 — 서브넷·포트(네트워크 기준) + 라우터·보안그룹·FIP(프로젝트 기준)."""
    try:
        n = _neutron_get(f"/networks/{network_id}")["network"]
        subnets = _neutron_get("/subnets", {"network_id": network_id})["subnets"]
        ports = _neutron_get("/ports", {"network_id": network_id})["ports"]
        pid = n.get("project_id") or n.get("tenant_id")
        pf = {"project_id": pid} if pid else None
        routers = _neutron_get("/routers", pf)["routers"]
        sgs = _neutron_get("/security-groups", pf)["security_groups"]
        fips = _neutron_get("/floatingips", pf)["floatingips"]
        # 서브넷 유형 — 이 망에 붙은 라우터(다른 프로젝트 소유일 수 있어 따로 조회)
        iface_routers = sorted({p["device_id"] for p in ports if p.get("device_owner") in _ROUTER_IFACE_OWNERS})
        gw_routers = _neutron_get("/routers", {"id": iface_routers})["routers"] if iface_routers else []
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"네트워크 상세 조회 실패: {type(e).__name__}: {e}")
    internet = _internet_subnet(ports, gw_routers)

    # Neutron 프로젝트 쿼터(사용량 포함) — 실패해도 상세는 반환(0 placeholder)
    quota = {}
    if pid:
        try:
            quota = _neutron_get(f"/quotas/{pid}/details")["quota"]
        except Exception:  # noqa: BLE001
            quota = {}

    def qv(key: str) -> dict:
        v = quota.get(key) or {}
        return {"used": v.get("used", 0), "max": v.get("limit", 0)}

    def _pool(s: dict) -> str:
        ap = s.get("allocation_pools") or []
        return ", ".join(f"{p['start']} ~ {p['end']}" for p in ap)

    return {
        "id": n["id"],
        "name": n.get("name") or n["id"],
        "tenantId": pid or "",
        "type": _net_type(n),
        "vlan": n.get("provider:segmentation_id"),
        "status": n.get("status", ""),
        "subnets": [
            {
                "id": s["id"], "name": s.get("name") or s["id"], "cidr": s.get("cidr", ""),
                "gateway": s.get("gateway_ip") or "", "dhcp": bool(s.get("enable_dhcp")),
                "dns": s.get("dns_nameservers") or [], "allocationPool": _pool(s), "internet": internet(s),
            }
            for s in subnets
        ],
        "ports": [
            {
                "id": p["id"], "name": p.get("name") or p["id"],
                "status": p.get("status", "DOWN"),
                "mac": p.get("mac_address", ""),
                "ip": (p.get("fixed_ips") or [{}])[0].get("ip_address", ""),
                "deviceOwner": p.get("device_owner", ""),
                "vm": p.get("device_id") or None,
            }
            for p in ports
        ],
        "securityGroups": [
            {
                "id": sg["id"], "name": sg.get("name") or sg["id"],
                "rules": len(sg.get("security_group_rules") or []),
                "desc": sg.get("description") or "",
            }
            for sg in sgs
        ],
        "routers": [
            {
                "id": r["id"], "name": r.get("name") or r["id"],
                "status": r.get("status", "DOWN"),
                "gatewayIp": (((r.get("external_gateway_info") or {}).get("external_fixed_ips") or [{}])[0].get("ip_address") or ""),
                "routes": len(r.get("routes") or []),
                "ha": bool(r.get("ha")),
            }
            for r in routers
        ],
        "floatingIps": [
            {
                "id": f["id"],
                "ip": f.get("floating_ip_address", ""),
                "portId": f.get("port_id"),
                "mappedTo": f.get("fixed_ip_address") or "(unmapped)",
                "status": f.get("status", "DOWN"),
            }
            for f in fips
        ],
        "quotas": {
            "ports": qv("port"),
            "floatingIps": qv("floatingip"),
            "subnets": qv("subnet"),
        },
    }


# ────── Floating IP 생명주기 (확보/연결/해제/반납) ──────

def _external_network_id() -> str:
    nets = _neutron_get("/networks", {"router:external": True})["networks"]
    if not nets:
        raise HTTPException(409, "외부 네트워크가 없어 공인 IP를 확보할 수 없습니다")
    return nets[0]["id"]


@router.get("/projects/{project_id}/fip-targets", dependencies=[Depends(project_member)])
def list_fip_targets(project_id: str):
    """공인 IP를 연결할 수 있는 대상 포트 — 프로젝트의 compute 포트(고정 IP 보유)."""
    try:
        ports = _neutron_get("/ports", {"project_id": project_id})["ports"]
        nets = _neutron_get("/networks", {"fields": ["id", "name", "router:external"]})["networks"]
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"대상 포트 조회 실패: {type(e).__name__}: {e}")
    net_name = {n["id"]: n.get("name") or n["id"] for n in nets}
    external = {n["id"] for n in nets if n.get("router:external")}
    targets = []
    for p in ports:
        owner = p.get("device_owner", "")
        fixed = p.get("fixed_ips") or []
        # compute(또는 미바인딩) 포트 + 고정 IP 보유 + 내부망만
        if not fixed or p.get("network_id") in external:
            continue
        if owner and not owner.startswith("compute:"):
            continue
        targets.append({
            "portId": p["id"],
            "ip": fixed[0].get("ip_address", ""),
            "networkName": net_name.get(p.get("network_id"), ""),
            "vm": p.get("device_id") or None,
        })
    return {"items": targets, "count": len(targets)}


class AllocateFipBody(BaseModel):
    projectId: str
    floatingNetworkId: str | None = None  # 미지정 시 외부망 자동


@router.post("/floatingips", status_code=201, dependencies=[Depends(require_admin)])
def allocate_fip(body: AllocateFipBody):
    """외부 풀에서 공인 IP를 프로젝트로 확보(allocate)."""
    net_id = body.floatingNetworkId or _external_network_id()
    payload = {"floatingip": {"floating_network_id": net_id, "project_id": body.projectId}}
    try:
        return _neutron_req("POST", "/floatingips", json=payload).json()["floatingip"]
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"공인 IP 확보 실패: {type(e).__name__}: {e}")


@router.delete("/floatingips/{fip_id}", status_code=204, dependencies=[Depends(require_admin)])
def release_fip(fip_id: str):
    """공인 IP를 외부 풀로 반납(release)."""
    try:
        _neutron_req("DELETE", f"/floatingips/{fip_id}")
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"공인 IP 반납 실패: {type(e).__name__}: {e}")
    return None


class AssociateFipBody(BaseModel):
    portId: str | None = None  # 포트 지정=연결(associate), null=연결해제(disassociate)


@router.put("/floatingips/{fip_id}", dependencies=[Depends(require_admin)])
def update_fip(fip_id: str, body: AssociateFipBody):
    """공인 IP를 VM 포트에 연결(portId 지정) 또는 연결 해제(portId=null)."""
    payload = {"floatingip": {"port_id": body.portId}}
    try:
        return _neutron_req("PUT", f"/floatingips/{fip_id}", json=payload).json()["floatingip"]
    except Exception as e:  # noqa: BLE001
        verb = "연결" if body.portId else "연결 해제"
        raise HTTPException(502, f"공인 IP {verb} 실패: {type(e).__name__}: {e}")


# ────── 보안 그룹 목록/생성/삭제 (규칙 편집은 위 security-groups/{id} 참고) ──────

@router.get("/security-groups")
def list_security_groups(c: Caller = Depends(caller)):
    """보안 그룹 목록 — 프로젝트·규칙 수 포함. 사용자는 소속 프로젝트(조회만)."""
    try:
        sgs = _neutron_get("/security-groups")["security_groups"]
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"보안 그룹 조회 실패: {type(e).__name__}: {e}")
    items = [
        {
            "id": sg["id"], "name": sg.get("name") or sg["id"],
            "tenantId": sg.get("project_id") or sg.get("tenant_id") or "",
            "desc": sg.get("description") or "",
            "rules": len(sg.get("security_group_rules") or []),
        }
        for sg in sgs
    ]
    return scoped(c, items)


class CreateSgBody(BaseModel):
    name: str
    projectId: str
    description: str = ""


@router.post("/security-groups", status_code=201, dependencies=[Depends(require_admin)])
def create_security_group(body: CreateSgBody):
    """보안 그룹 생성."""
    if not body.name.strip():
        raise HTTPException(400, "보안 그룹 이름이 필요합니다")
    payload = {"security_group": {
        "name": body.name.strip(), "description": body.description or "", "project_id": body.projectId,
    }}
    try:
        sg = _neutron_req("POST", "/security-groups", json=payload).json()["security_group"]
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"보안 그룹 생성 실패: {type(e).__name__}: {e}")
    return get_security_group(sg["id"])


@router.delete("/security-groups/{sg_id}", status_code=204, dependencies=[Depends(require_admin)])
def delete_security_group(sg_id: str):
    """보안 그룹 삭제(사용 중이면 Neutron 이 거부)."""
    try:
        _neutron_req("DELETE", f"/security-groups/{sg_id}")
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"보안 그룹 삭제 실패: {type(e).__name__}: {e}")
    return None


# ────── 라우터 (L3) ──────

def _to_router(r: dict, net_name: dict, iface_count: int) -> dict:
    gw = r.get("external_gateway_info") or {}
    ext_net = gw.get("network_id")
    ext_ip = (gw.get("external_fixed_ips") or [{}])[0].get("ip_address", "") if gw else ""
    return {
        "id": r["id"], "name": r.get("name") or r["id"],
        "tenantId": r.get("project_id") or r.get("tenant_id") or "",
        "status": r.get("status", ""),
        "adminStateUp": bool(r.get("admin_state_up", True)),
        "externalNetwork": net_name.get(ext_net, "") if ext_net else "",
        "externalIp": ext_ip,
        "interfaces": iface_count,
        "ha": bool(r.get("ha")),
        "routes": len(r.get("routes") or []),
    }


@router.get("/routers", dependencies=[Depends(require_admin)])
def list_routers():
    """라우터 목록 + 인터페이스 수·외부망."""
    try:
        routers = _neutron_get("/routers")["routers"]
        nets = _neutron_get("/networks", {"fields": ["id", "name"]})["networks"]
        iports = _neutron_get("/ports", {"device_owner": "network:router_interface", "fields": ["device_id"]})["ports"]
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"라우터 조회 실패: {type(e).__name__}: {e}")
    net_name = {n["id"]: n.get("name") or n["id"] for n in nets}
    from collections import Counter
    ic = Counter(p["device_id"] for p in iports)
    items = [_to_router(r, net_name, ic.get(r["id"], 0)) for r in routers]
    return {"items": items, "count": len(items)}


@router.get("/routers/{router_id}", dependencies=[Depends(require_admin)])
def get_router(router_id: str):
    """라우터 상세 — 인터페이스(서브넷)·정적 라우트·외부 게이트웨이."""
    try:
        r = _neutron_get(f"/routers/{router_id}")["router"]
        ports = _neutron_get("/ports", {"device_id": router_id})["ports"]
        subnets = _neutron_get("/subnets", {"fields": ["id", "name", "cidr"]})["subnets"]
        nets = _neutron_get("/networks", {"fields": ["id", "name"]})["networks"]
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"라우터 상세 조회 실패: {type(e).__name__}: {e}")
    subnet_map = {s["id"]: s for s in subnets}
    net_name = {n["id"]: n.get("name") or n["id"] for n in nets}
    gw = r.get("external_gateway_info") or {}
    interfaces = []
    for p in ports:
        if not p.get("device_owner", "").startswith("network:router_interface"):
            continue
        for fx in p.get("fixed_ips") or []:
            sn = subnet_map.get(fx.get("subnet_id"), {})
            interfaces.append({
                "portId": p["id"], "subnetId": fx.get("subnet_id"),
                "subnetName": sn.get("name", ""), "cidr": sn.get("cidr", ""),
                "ip": fx.get("ip_address", ""),
            })
    return {
        "id": r["id"], "name": r.get("name") or r["id"],
        "tenantId": r.get("project_id") or r.get("tenant_id") or "",
        "status": r.get("status", ""), "adminStateUp": bool(r.get("admin_state_up", True)),
        "externalNetwork": net_name.get(gw.get("network_id"), "") if gw else "",
        "externalIp": ((gw.get("external_fixed_ips") or [{}])[0].get("ip_address", "")) if gw else "",
        "ha": bool(r.get("ha")),
        "interfaces": interfaces,
        "routes": [{"destination": rt.get("destination"), "nexthop": rt.get("nexthop")} for rt in r.get("routes") or []],
    }


class CreateRouterBody(BaseModel):
    name: str
    projectId: str
    externalNetworkId: str | None = None


@router.post("/routers", status_code=201, dependencies=[Depends(require_admin)])
def create_router(body: CreateRouterBody):
    """라우터 생성(+선택적 외부 게이트웨이)."""
    if not body.name.strip():
        raise HTTPException(400, "라우터 이름이 필요합니다")
    r_body: dict = {"router": {"name": body.name.strip(), "project_id": body.projectId, "admin_state_up": True}}
    if body.externalNetworkId:
        r_body["router"]["external_gateway_info"] = {"network_id": body.externalNetworkId}
    try:
        r = _neutron_req("POST", "/routers", json=r_body).json()["router"]
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"라우터 생성 실패: {type(e).__name__}: {e}")
    return get_router(r["id"])


@router.delete("/routers/{router_id}", status_code=204, dependencies=[Depends(require_admin)])
def delete_router(router_id: str):
    """라우터 삭제(인터페이스·게이트웨이 남아있으면 Neutron 거부)."""
    try:
        _neutron_req("DELETE", f"/routers/{router_id}")
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"라우터 삭제 실패(인터페이스 먼저 해제): {type(e).__name__}: {e}")
    return None


class RouterInterfaceBody(BaseModel):
    subnetId: str


@router.put("/routers/{router_id}/add-interface", dependencies=[Depends(require_admin)])
def add_router_interface(router_id: str, body: RouterInterfaceBody):
    """라우터에 서브넷 인터페이스 연결."""
    try:
        _neutron_req("PUT", f"/routers/{router_id}/add_router_interface", json={"subnet_id": body.subnetId})
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"서브넷 연결 실패: {type(e).__name__}: {e}")
    return get_router(router_id)


@router.put("/routers/{router_id}/remove-interface", dependencies=[Depends(require_admin)])
def remove_router_interface(router_id: str, body: RouterInterfaceBody):
    """라우터에서 서브넷 인터페이스 해제."""
    try:
        _neutron_req("PUT", f"/routers/{router_id}/remove_router_interface", json={"subnet_id": body.subnetId})
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"서브넷 해제 실패: {type(e).__name__}: {e}")
    return get_router(router_id)


@router.get("/projects/{project_id}/subnets", dependencies=[Depends(project_member)])
def list_project_subnets(project_id: str):
    """프로젝트의 서브넷 목록 — 라우터 인터페이스 연결 대상 선택용."""
    try:
        subnets = _neutron_get("/subnets", {"project_id": project_id})["subnets"]
        nets = _neutron_get("/networks", {"fields": ["id", "name"]})["networks"]
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"서브넷 조회 실패: {type(e).__name__}: {e}")
    net_name = {n["id"]: n.get("name") or n["id"] for n in nets}
    items = [
        {
            "id": s["id"], "name": s.get("name") or s["id"], "cidr": s.get("cidr", ""),
            "networkName": net_name.get(s.get("network_id"), ""),
        }
        for s in subnets
    ]
    return {"items": items, "count": len(items)}


# ────── 자원신청 승인용 — 티켓 프로젝트 기준 라이브 조회 ──────

# Magnum(CAPI 드라이버)이 클러스터마다 만드는 네트워크·보안그룹은 승인 목록에서 뺀다 — VM 을 붙일 자원이 아니다.
# 이름 규칙 = cluster-api-provider-openstack(네임스페이스 magnum-<프로젝트 ID>):
#   네트워크 k8s-clusterapi-cluster-magnum-…, 보안그룹 k8s-cluster-magnum-…-secgroup-{controlplane|worker}
_MAGNUM_NET_PREFIX = "k8s-clusterapi-cluster-magnum-"
_MAGNUM_SG_PREFIX = "k8s-cluster-magnum-"


@router.get("/projects/{project_id}/networks", dependencies=[Depends(project_member)])
def list_project_networks(project_id: str):
    """승인 시 고를 네트워크 — 프로젝트 소유 + 공유 + 외부(external), Magnum 클러스터 네트워크 제외. kind: project|shared|external.
    공유는 전체 공유(shared) + 이 프로젝트에만 RBAC 로 공유된 것(관리자 조회 시 shared=false 로 보이므로 따로 확인).
    shared = 이 프로젝트가 포트를 만들 수 있는 공유 망인지(외부망도 공유돼 있으면 노드를 바로 붙일 수 있다).
    cidr 는 서브넷 CIDR(여러 개면 ', ' 로 연결). access = IPv4 서브넷 유형(internet·internal, 서브넷마다 — _internet_subnet)."""
    try:
        nets = _neutron_get("/networks")["networks"]
        subnets = _neutron_get("/subnets")["subnets"]
        rbac = _neutron_get("/rbac-policies", {"object_type": "network", "action": "access_as_shared"})["rbac_policies"]
        iports = _neutron_get("/ports", {"device_owner": list(_ROUTER_IFACE_OWNERS)})["ports"]
        routers = _neutron_get("/routers")["routers"]
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"네트워크 조회 실패: {type(e).__name__}: {e}")
    shared_to_project = {p["object_id"] for p in rbac if p.get("target_tenant") in (project_id, "*")}
    internet = _internet_subnet(iports, routers)
    items = []
    for n in nets:
        if (n.get("name") or "").startswith(_MAGNUM_NET_PREFIX):
            continue
        kind = "project" if _net_type(n) == "tenant" else _net_type(n)
        owner = n.get("project_id") or n.get("tenant_id") or ""
        if kind == "project" and owner != project_id:
            if n["id"] not in shared_to_project:
                continue
            kind = "shared"
        cidrs = [sn["cidr"] for sn in subnets if sn["network_id"] == n["id"]]
        types = {internet(sn) for sn in subnets if sn["network_id"] == n["id"] and sn.get("ip_version") == 4}
        items.append({"id": n["id"], "name": n.get("name") or n["id"], "kind": kind,
                      "shared": bool(n.get("shared")) or n["id"] in shared_to_project,
                      "projectId": owner, "cidr": ", ".join(cidrs),
                      "access": [t for t, on in (("internet", True), ("internal", False)) if on in types]})
    return {"items": items, "count": len(items)}


@router.get("/projects/{project_id}/security-groups", dependencies=[Depends(project_member)])
def list_project_security_groups(project_id: str):
    """승인 시 고를 보안그룹 — 프로젝트 소유분(Magnum 클러스터 보안그룹 제외)."""
    try:
        sgs = _neutron_get("/security-groups", {"project_id": project_id})["security_groups"]
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"보안 그룹 조회 실패: {type(e).__name__}: {e}")
    items = [{"id": sg["id"], "name": sg.get("name") or sg["id"], "projectId": project_id,
              "description": sg.get("description") or "",
              "rules": len(sg.get("security_group_rules") or []),
              "ruleList": [_to_sg_rule(r) for r in sg.get("security_group_rules") or []]}
             for sg in sgs
             if not (sg.get("name") or "").startswith(_MAGNUM_SG_PREFIX)]
    return {"items": items, "count": len(items)}


@router.get("/networks/{network_id}/ip-check", dependencies=[Depends(require_admin)])
def ip_check(network_id: str, ip: str):
    """Static IP 사용 가능 여부 — 서브넷 범위·네트워크/브로드캐스트·게이트웨이·사용 중 포트."""
    try:
        addr = ipaddress.ip_address(ip)
    except ValueError:
        return {"available": False, "reason": "IP 주소 형식이 올바르지 않습니다."}
    try:
        subnets = _neutron_get("/subnets", {"network_id": network_id})["subnets"]
        sub = next((sn for sn in subnets if addr in ipaddress.ip_network(sn["cidr"])), None)
        if sub is None:
            return {"available": False, "reason": "네트워크의 서브넷 범위를 벗어난 IP입니다."}
        cidr = ipaddress.ip_network(sub["cidr"])
        if addr in (cidr.network_address, cidr.broadcast_address):
            return {"available": False, "reason": "네트워크·브로드캐스트 주소는 쓸 수 없습니다."}
        if sub.get("gateway_ip") == str(addr):
            return {"available": False, "reason": "게이트웨이 주소입니다."}
        ports = _neutron_get("/ports", {"network_id": network_id, "fixed_ips": f"ip_address={addr}"})["ports"]
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"IP 사용 여부 조회 실패: {type(e).__name__}: {e}")
    if ports:
        return {"available": False, "reason": "이미 사용 중인 IP입니다."}
    return {"available": True, "reason": None}


# 라우터 인터페이스 포트 종류 — 일반·분산(DVR)·HA 라우터
_ROUTER_IFACE_OWNERS = ("network:router_interface", "network:router_interface_distributed",
                        "network:ha_router_replicated_interface")


def _internet_subnet(ports: list[dict], routers: list[dict]):
    """서브넷 → 인터넷망(True)·내부망(False) 판정 함수. 게이트웨이 IP 를 가진 라우터에 외부 게이트웨이가 있으면 인터넷망
    (fip_reachable 과 같은 기준 — VM·노드는 서브넷 게이트웨이 IP 로 나간다). ports 중 라우터 인터페이스만 본다."""
    gw_routers = {r["id"] for r in routers if (r.get("external_gateway_info") or {}).get("network_id")}
    reached = {(ip["subnet_id"], ip["ip_address"]) for pt in ports
               if pt.get("device_owner") in _ROUTER_IFACE_OWNERS and pt.get("device_id") in gw_routers
               for ip in pt.get("fixed_ips") or []}
    return lambda sn: (sn["id"], sn.get("gateway_ip")) in reached


@router.get("/networks/{network_id}/fip-reachable", dependencies=[Depends(require_admin)])
def fip_reachable(network_id: str, floatingNetworkId: str, subnetId: str | None = None):
    """이 네트워크의 VM 에 floatingNetworkId 풀의 Floating IP 를 붙일 수 있는지.
    Neutron 은 VM 포트 IP 의 서브넷이 'FIP 풀에 게이트웨이를 둔 라우터'에 연결돼 있어야 연결을 허용한다.
    다만 VM 은 응답을 서브넷 게이트웨이 IP 로 보내므로, 그 IP 를 가진 라우터가 FIP 풀에 게이트웨이를 둬야 실제로 통신된다
    (외부망 라우터가 게이트웨이가 아닌 IP 로만 붙어 있으면 연결은 되지만 응답이 나가지 못한다).
    VM 이 어느 서브넷에서 IP 를 받을지 미리 알 수 없으므로 IPv4 서브넷 전부가 그래야 가능으로 본다.
    subnetId 를 주면 그 서브넷만 본다(클러스터 노드는 고른 서브넷에서만 IP 를 받는다)."""
    try:
        subnets = [sn for sn in _neutron_get("/subnets", {"network_id": network_id})["subnets"]
                   if sn.get("ip_version") == 4 and (subnetId is None or sn["id"] == subnetId)]
        ports = [pt for pt in _neutron_get("/ports", {"network_id": network_id})["ports"]
                 if pt.get("device_owner") in _ROUTER_IFACE_OWNERS]
        gw_routers = {r["id"] for r in _neutron_get("/routers")["routers"]
                      if (r.get("external_gateway_info") or {}).get("network_id") == floatingNetworkId}
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"Floating IP 연결 가능 여부 조회 실패: {type(e).__name__}: {e}")
    if not subnets:
        return {"reachable": False, "reason": "네트워크에 IPv4 서브넷이 없습니다." if subnetId is None
                else "고른 서브넷이 이 네트워크의 IPv4 서브넷이 아닙니다."}
    reached = {(ip["subnet_id"], ip["ip_address"]) for pt in ports if pt.get("device_id") in gw_routers
               for ip in pt.get("fixed_ips") or []}
    missing = [f"{sn.get('name') or sn['id'][:8]}({sn['cidr']})" for sn in subnets
               if (sn["id"], sn.get("gateway_ip")) not in reached]
    if missing:
        return {"reachable": False,
                "reason": f"서브넷 {', '.join(missing)} 의 게이트웨이 IP 를 가진 라우터가 Floating IP 풀에 게이트웨이를 두고 있지 않습니다."}
    return {"reachable": True, "reason": None}
