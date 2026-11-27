"""IaaS 가상서버 — OpenStack Nova 중개(운영: 조회·전원·삭제·볼륨 연결/분리).

생성(POST projects/{id}/servers)은 자원요청→승인 흐름에서만 호출한다.
환경독립: override=template.format(service="nova")+"/v2.1", catalog=service_type="compute".
가속기는 flavor extra_spec(accel:device_profile)에서 도출.
"""
import base64
import json as _json
import re
import time
from urllib.parse import quote, urlsplit, urlunsplit

from fastapi import APIRouter, Depends, HTTPException
from fastapi.concurrency import run_in_threadpool
from pydantic import BaseModel

from ..auth import Caller, caller, ensure_project, project_member, require_admin, scoped
from ..config import settings
from ..openstack_client import get_conn, get_conn_for_project, os_request
from .flavors import _cyborg
from .networks import network_info
from .volumes import CreateVolumeBody, _cinder_req, create_volume, delete_volume, volume_project

router = APIRouter(tags=["servers"])

_NOVA_MV = "2.72"  # flavor.original_name·host 등 포함 마이크로버전


def _nova_req(method: str, path: str, *, json=None, project_id: str | None = None, microversion: str = _NOVA_MV,
              timeout: int = 20):
    """project_id 지정 시 그 프로젝트로 스코프해 호출(생성이 그 프로젝트 소유로 들어감 — volumes._cinder_req 와 같은 방식).
    조회는 연결 끊김 시 1회 재시도, 생성·조작은 재시도 없이 새 연결로 보낸다(os_request)."""
    conn = get_conn_for_project(project_id) if project_id else get_conn()
    headers = {"X-OpenStack-Nova-API-Version": microversion}
    if settings.os_endpoint_mode == "catalog":
        r = os_request(conn, path, method, json=json, headers=headers, raise_exc=False,
                       endpoint_filter={"service_type": "compute", "interface": settings.os_interface}, timeout=timeout)
    else:  # override
        base = settings.os_endpoint_template.format(service="nova") + "/v2.1"
        r = os_request(conn, f"{base}{path}", method, json=json, headers=headers, raise_exc=False, timeout=timeout)
    if not r.ok:
        raise RuntimeError((r.text or f"HTTP {r.status_code}")[:300])
    return r


def _accel_map() -> dict:
    """flavor 이름 → 가속기 device_profile (없으면 None)."""
    out: dict = {}
    try:
        flavors = _nova_req("GET", "/flavors/detail").json()["flavors"]
        for f in flavors:
            specs = _nova_req("GET", f"/flavors/{f['id']}/os-extra_specs").json().get("extra_specs", {})
            out[f.get("name")] = specs.get("accel:device_profile")
    except Exception:  # noqa: BLE001 — 가속기 표기는 부가정보
        pass
    return out


def _addresses(s: dict) -> list[dict]:
    out = []
    for net, addrs in (s.get("addresses") or {}).items():
        for a in addrs:
            out.append({"network": net, "ip": a.get("addr"), "type": a.get("OS-EXT-IPS:type")})
    return out


def _to_server(s: dict, accel_map: dict) -> dict:
    f = s.get("flavor") or {}
    fname = f.get("original_name") or f.get("id") or ""
    vols = s.get("os-extended-volumes:volumes_attached") or []
    return {
        "id": s["id"],
        "name": s.get("name") or s["id"],
        "tenantId": s.get("tenant_id") or "",
        "status": s.get("status", ""),
        "vmState": s.get("OS-EXT-STS:vm_state", ""),
        "taskState": s.get("OS-EXT-STS:task_state"),  # scheduling·networking·block_device_mapping·spawning … (진행 중일 때)
        "fault": (s.get("fault") or {}).get("message"),  # 빌드 실패 사유(ERROR 일 때)
        "powerState": s.get("OS-EXT-STS:power_state", 0),
        "flavorName": fname,
        "vcpus": f.get("vcpus", 0),
        "ramMb": f.get("ram", 0),
        "accelerator": accel_map.get(fname),
        "host": s.get("OS-EXT-SRV-ATTR:host") or "",
        # 포털 생성 VM 은 키를 cloud-init 으로 넣어 key_name 이 없다 — 생성 때 메타데이터에 남긴 키 이름
        "keyName": s.get("key_name") or (s.get("metadata") or {}).get("okc_keypair"),
        "imageId": (s.get("image") or {}).get("id") if isinstance(s.get("image"), dict) else None,
        "addresses": _addresses(s),
        "volumeCount": len(vols),
        "createdAt": s.get("created") or "",
    }


def server_project(server_id: str) -> str:
    """가상서버가 속한 프로젝트 ID(권한 확인용)."""
    try:
        return _nova_req("GET", f"/servers/{server_id}").json()["server"].get("tenant_id") or ""
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"가상서버 조회 실패: {e}")


async def server_access(server_id: str, c: Caller = Depends(caller)) -> Caller:
    """경로의 가상서버가 소속 프로젝트 것인지(관리자는 통과) — 상세·콘솔·전원·연결/분리."""
    if not c.is_admin:
        ensure_project(c, await run_in_threadpool(server_project, server_id))
    return c


@router.get("/servers")
def list_servers(c: Caller = Depends(caller)):
    """가상서버 목록 — 관리자는 전체 프로젝트, 사용자는 소속 프로젝트."""
    try:
        servers = _nova_req("GET", "/servers/detail?all_tenants=1").json()["servers"]
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"가상서버 조회 실패: {e}")
    amap = _accel_map()
    return scoped(c, [_to_server(s, amap) for s in servers])


@router.get("/projects/{project_id}/server-names", dependencies=[Depends(project_member)])
def list_server_names(project_id: str, name: str | None = None):
    """프로젝트의 가상서버 이름 목록(경량, Nova 1회) — 자원신청 VM 이름 중복 확인용.
    name 을 주면 Nova 이름 필터(정규식 ^이름$)로 그 이름의 서버만 조회 — VM 이 많아도 결과 0~1건, 목록 1000건 제한과 무관."""
    query = f"all_tenants=1&project_id={quote(project_id, safe='')}"
    if name:
        query += f"&name={quote('^' + re.escape(name) + '$', safe='')}"
    try:
        servers = _nova_req("GET", f"/servers?{query}").json()["servers"]
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"가상서버 조회 실패: {e}")
    return {"names": [s.get("name") for s in servers]}


class CreateServerBody(BaseModel):
    name: str                         # 서버 이름 = hostname
    flavorId: str
    imageId: str
    volumeGb: int                     # 부팅 볼륨 크기(GB) — 항상 Cinder 부팅 볼륨
    networkId: str
    fixedIp: str | None = None        # 고정 IP(없으면 자동 할당)
    securityGroups: list[str] = []    # 보안그룹 이름(프로젝트 기준)
    publicKey: str | None = None      # cloud-init 으로 주입할 SSH 공개키
    keypairName: str | None = None    # 그 키 이름 — Nova 메타데이터(okc_keypair)로 남겨 상세에 표시
    host: str | None = None           # 배치할 컴퓨트 노드(hypervisor_hostname) — 없으면 스케줄러가 고름. can_pin_host 필요


def _cloud_init_user_data(public_key: str) -> str:
    """SSH 공개키를 등록하는 cloud-config(base64). Nova key_name 대신 사용 — 키는 포털 DB 가 원장."""
    # 키를 JSON 문자열(= 유효한 YAML 문자열)로 감싼다 — 키 주석에 ': '·줄바꿈이 있어도 YAML 이 깨지지 않게
    cfg = "#cloud-config\nssh_authorized_keys:\n  - " + _json.dumps(public_key.strip()) + "\n"
    return base64.b64encode(cfg.encode()).decode()


class VolumeNotReady(RuntimeError):
    """볼륨이 error 가 됐거나 제한 시간 안에 준비되지 않음 — 다시 확인해도 소용없는 실패(조회 오류와 구분)."""


class VolumeTimeout(VolumeNotReady):
    """제한 시간 안에 준비되지 않음 — Cinder 는 계속 만드는 중(status = 그때 상태, 예: downloading)."""

    def __init__(self, timeout: float, status: str):
        super().__init__(f"{round(timeout)}초 안에 준비되지 않음(status={status})")
        self.status = status


def _wait_volume_available(project_id: str, volume_id: str, *, timeout: float = 300, interval: int = 3, tick=None):
    """볼륨이 사용 가능(available)해질 때까지 대기 — 이미지 내려받기·변환(creating·downloading) 포함.
    tick 은 확인할 때마다 호출(워크플로 작업의 heartbeat)."""
    deadline = time.monotonic() + timeout
    while True:
        status = _cinder_req("GET", f"/volumes/{volume_id}", project_id=project_id).json()["volume"]["status"]
        if status == "available":
            return
        if status == "error":
            raise VolumeNotReady("Cinder 볼륨 생성 오류(status=error)")
        if time.monotonic() > deadline:
            raise VolumeTimeout(timeout, status)
        if tick:
            tick(status)
        time.sleep(interval)


def _discard_volume(volume_id: str) -> str:
    """생성 실패 시 부팅 볼륨 삭제 — 결과를 오류 메시지에 덧붙일 문구로 돌려준다.
    Nova 가 이미 볼륨을 잡았거나(reserved·in-use) 아직 만드는 중이면 Cinder 가 삭제를 거부한다(남은 볼륨은 직접 정리)."""
    try:
        delete_volume(volume_id)
        return f" — 부팅 볼륨({volume_id})은 삭제함"
    except HTTPException as e:
        return f" — 부팅 볼륨({volume_id})이 남아 있음({e.detail})"


@router.post("/projects/{project_id}/servers", status_code=202, dependencies=[Depends(require_admin)])
def create_server(project_id: str, body: CreateServerBody):
    """VM 생성 — ① 부팅 볼륨(이미지→Cinder 볼륨, 이름 `{이름}-boot-vol`)을 만들어 사용 가능해질 때까지 기다린 뒤
    ② Nova 1회 요청으로 그 볼륨(인스턴스 삭제 시 볼륨 삭제)·네트워크(+고정 IP)·보안그룹·hostname·SSH 공개키(cloud-init)를
    넣어 생성한다. ①·②가 실패하면 ①의 볼륨을 지운다. 진행 상태는 GET /servers/{id} 의 taskState 로 확인.
    hostname 지정은 마이크로버전 2.90 이상."""
    vol = create_volume(CreateVolumeBody(name=f"{body.name}-boot-vol", size=body.volumeGb, projectId=project_id,
                                         imageRef=body.imageId))
    try:
        _wait_volume_available(project_id, vol["id"])
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"부팅 볼륨 준비 실패: {e}" + _discard_volume(vol["id"]))
    return {"id": _boot_server(project_id, body, vol["id"])}


def _boot_server(project_id: str, body: CreateServerBody, volume_id: str) -> str:
    """준비된 부팅 볼륨으로 Nova 생성 요청(1회) → 서버 ID. 실패하면 그 볼륨을 지우고 502."""
    network = {"uuid": body.networkId, **({"fixed_ip": body.fixedIp} if body.fixedIp else {})}
    server = {
        "name": body.name,
        "hostname": body.name,
        "flavorRef": body.flavorId,
        "block_device_mapping_v2": [{
            "boot_index": 0, "uuid": volume_id, "source_type": "volume", "destination_type": "volume",
            "delete_on_termination": True,
        }],
        "networks": [network],
        **({"security_groups": [{"name": n} for n in body.securityGroups]} if body.securityGroups else {}),
        **({"user_data": _cloud_init_user_data(body.publicKey)} if body.publicKey else {}),
        **({"metadata": {"okc_keypair": body.keypairName}} if body.keypairName else {}),
        **({"hypervisor_hostname": body.host} if body.host else {}),  # 2.74+ — 그 노드로 스케줄러가 다시 확인
    }
    try:
        # 응답 대기 60초 — 짧으면 Nova 는 만들었는데 포털은 실패로 알 수 있다(재시도 전 같은 이름 서버 확인은 호출부 몫)
        s = _nova_req("POST", "/servers", json={"server": server}, project_id=project_id, microversion="2.90",
                      timeout=60).json()["server"]
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"가상서버 생성 요청 실패: {e}" + _discard_volume(volume_id))
    return s["id"]


_PIN_TTL = 300  # 초
_pin_cache: dict[str, tuple[float, bool]] = {}


def can_pin_host(project_id: str) -> bool:
    """이 프로젝트로 VM 을 만들 때 노드 지정(hypervisor_hostname)이 허용되는지(5분 캐시).
    Nova 정책(기본 admin 전용)은 API 로 조회할 수 없어, 없는 플레이버로 생성 요청을 보내 본다 — Nova 는 노드 지정 권한을
    플레이버·이미지 확인보다 먼저 보므로 정책 거절(403)이면 권한 없음, 요청 내용 거절(400)이면 허용. 어느 쪽이든 만들어지는 것은 없다."""
    hit = _pin_cache.get(project_id)
    if hit and time.monotonic() - hit[0] < _PIN_TTL:
        return hit[1]
    probe = {"server": {"name": "okc-host-pin-check", "flavorRef": "okc-host-pin-check", "networks": "none",
                        "hypervisor_hostname": "okc-host-pin-check"}}
    try:
        _nova_req("POST", "/servers", json=probe, project_id=project_id, microversion="2.90")
        ok = True  # 이미지가 없어 성공할 수 없는 요청 — 오면 정책은 통과한 것
    except RuntimeError as e:
        if "requested_destination" in str(e):  # 정책 거절(403)
            ok = False
        elif '"badRequest"' in str(e):  # 정책 통과 뒤 요청 내용 거절(400)
            ok = True
        else:
            raise  # 일시 오류 등 — 판정·캐시하지 않는다
    _pin_cache[project_id] = (time.monotonic(), ok)
    return ok


@router.get("/servers/{server_id}/accelerator-requests", dependencies=[Depends(server_access)])
def list_server_arqs(server_id: str):
    """가상서버의 가속기 요청(Cyborg ARQ) — 상태: Initial → Bound(바인딩 완료) 등."""
    try:
        arqs = _cyborg("GET", f"/accelerator_requests?instance={quote(server_id, safe='')}").json().get("arqs", [])
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"가속기 요청 조회 실패: {e}")
    return {"items": [{"deviceProfile": a.get("device_profile_name"), "state": a.get("state"), "host": a.get("hostname")}
                      for a in arqs]}


@router.get("/servers/{server_id}", dependencies=[Depends(server_access)])
def get_server(server_id: str):
    return _server_detail(server_id, _accel_map())


def _server_detail(server_id: str, accel_map: dict) -> dict:
    """서버 + 연결 볼륨·인터페이스·보안그룹."""
    try:
        s = _nova_req("GET", f"/servers/{server_id}").json()["server"]
        vols = _nova_req("GET", f"/servers/{server_id}/os-volume_attachments").json().get("volumeAttachments", [])
        ifaces = _nova_req("GET", f"/servers/{server_id}/os-interface").json().get("interfaceAttachments", [])
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"가상서버 상세 조회 실패: {e}")
    d = _to_server(s, accel_map)
    d["attachedVolumes"] = [{"volumeId": v.get("volumeId") or v.get("id"), "device": v.get("device")} for v in vols]
    d["interfaces"] = [
        {"portId": i.get("port_id"), "netId": i.get("net_id"), "state": i.get("port_state"),
         "ips": [fx.get("ip_address") for fx in (i.get("fixed_ips") or [])]}
        for i in ifaces
    ]
    # Nova 는 NIC(포트)마다 같은 보안그룹을 중복으로 줄 수 있어 이름 기준 중복 제거(순서 유지)
    d["securityGroups"] = list(dict.fromkeys(g.get("name") for g in (s.get("security_groups") or []) if g.get("name")))
    # 볼륨 부팅 VM 은 Nova 에 이미지 참조가 없다 — 부팅 볼륨(vda)의 원본 이미지(volume_image_metadata)로 보완
    if not d["imageId"] and vols:
        boot = next((v for v in vols if (v.get("device") or "").endswith("a")), vols[0])
        try:
            vol = _cinder_req("GET", f"/volumes/{boot.get('volumeId') or boot.get('id')}",
                              project_id=s.get("tenant_id")).json()["volume"]
            meta = vol.get("volume_image_metadata") or {}
            d["imageId"] = meta.get("image_id")
            d["imageName"] = meta.get("image_name")   # 화면이 "이름 (볼륨 부팅)" 으로 표시
            d["bootVolumeId"] = vol.get("id")
        except Exception:  # noqa: BLE001 — 보완 정보일 뿐 — 실패해도 상세는 그대로("(볼륨 부팅)" 표기 유지)
            pass
    return d


class AttachInterfaceBody(BaseModel):
    networkId: str


@router.post("/servers/{server_id}/interfaces")
def attach_interface(server_id: str, body: AttachInterfaceBody, c: Caller = Depends(server_access)):
    """VM에 네트워크 인터페이스(NIC) 연결 — 포털 네트워크 선택. 사용자는 소속 프로젝트 망 또는 공유·외부망만."""
    if not c.is_admin:
        project_id, kind = network_info(body.networkId)
        if kind == "tenant":
            ensure_project(c, project_id)
    try:
        _nova_req("POST", f"/servers/{server_id}/os-interface",
                  json={"interfaceAttachment": {"net_id": body.networkId}})
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"네트워크 연결 실패: {e}")
    return get_server(server_id)


@router.delete("/servers/{server_id}/interfaces/{port_id}", dependencies=[Depends(server_access)])
def detach_interface(server_id: str, port_id: str):
    """VM에서 네트워크 인터페이스 분리."""
    try:
        _nova_req("DELETE", f"/servers/{server_id}/os-interface/{port_id}")
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"네트워크 분리 실패: {e}")
    return {"ok": True}


class ResizeBody(BaseModel):
    flavorId: str


@router.post("/servers/{server_id}/resize", dependencies=[Depends(require_admin)])
def resize_server(server_id: str, body: ResizeBody):
    """스펙(플레이버) 변경 시작 → VM이 VERIFY_RESIZE 상태가 되면 확정/되돌리기 필요(재부팅 동반)."""
    try:
        _nova_req("POST", f"/servers/{server_id}/action", json={"resize": {"flavorRef": body.flavorId}})
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"스펙 변경 실패: {e}")
    return {"ok": True}


@router.post("/servers/{server_id}/resize-confirm", dependencies=[Depends(require_admin)])
def resize_confirm(server_id: str):
    try:
        _nova_req("POST", f"/servers/{server_id}/action", json={"confirmResize": None})
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"스펙 변경 확정 실패: {e}")
    return {"ok": True}


@router.post("/servers/{server_id}/resize-revert", dependencies=[Depends(require_admin)])
def resize_revert(server_id: str):
    try:
        _nova_req("POST", f"/servers/{server_id}/action", json={"revertResize": None})
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"스펙 변경 되돌리기 실패: {e}")
    return {"ok": True}


class ServerSecGroupBody(BaseModel):
    name: str  # 보안그룹 이름 — Nova addSecurityGroup 은 이름 기준


@router.post("/servers/{server_id}/security-groups", dependencies=[Depends(server_access)])
def add_server_security_group(server_id: str, body: ServerSecGroupBody):
    """가상서버에 보안그룹 추가(Nova addSecurityGroup, 이름 기준). Horizon '보안 그룹 편집'과 동일.

    클러스터 노드처럼 Magnum/CAPI 가 관리하는 보안그룹의 규칙은 되돌려지므로,
    별도 보안그룹을 서버에 직접 붙여 접속 규칙을 유지하는 용도."""
    try:
        _nova_req("POST", f"/servers/{server_id}/action", json={"addSecurityGroup": {"name": body.name}})
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"보안그룹 추가 실패: {e}")
    return {"ok": True}


@router.delete("/servers/{server_id}/security-groups/{name}", dependencies=[Depends(server_access)])
def remove_server_security_group(server_id: str, name: str):
    """가상서버에서 보안그룹 제거(Nova removeSecurityGroup, 이름 기준)."""
    try:
        _nova_req("POST", f"/servers/{server_id}/action", json={"removeSecurityGroup": {"name": name}})
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"보안그룹 제거 실패: {e}")
    return {"ok": True}


@router.get("/servers/{server_id}/diagnostics", dependencies=[Depends(server_access)])
def diagnostics(server_id: str):
    """하이퍼바이저 진단(현재 카운터) — CPU time·메모리·디스크/NIC 누적. 시계열 아님(프론트가 폴링해 속도 계산).
    실행 중(ACTIVE)이 아니면 Nova가 거부할 수 있음."""
    try:
        d = _nova_req("GET", f"/servers/{server_id}/diagnostics").json()
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"진단 조회 실패(실행 중이 아닐 수 있음): {e}")
    cpu = d.get("cpu_details") or []
    mem = d.get("memory_details") or {}
    disk = d.get("disk_details") or []
    nic = d.get("nic_details") or []
    return {
        "state": d.get("state", ""),
        "uptime": d.get("uptime", 0),
        "numCpus": d.get("num_cpus") or len(cpu),
        "cpuTimeNs": sum((c.get("time") or 0) for c in cpu),
        "memoryMb": mem.get("maximum") or 0,
        "memoryUsedMb": mem.get("used") or 0,
        "diskReadBytes": sum((x.get("read_bytes") or 0) for x in disk),
        "diskWriteBytes": sum((x.get("write_bytes") or 0) for x in disk),
        "nicRxBytes": sum((x.get("rx_octets") or 0) for x in nic),
        "nicTxBytes": sum((x.get("tx_octets") or 0) for x in nic),
    }


class PowerBody(BaseModel):
    action: str  # start | stop | reboot
    hard: bool = False  # reboot 시 HARD 여부


@router.post("/servers/{server_id}/power", dependencies=[Depends(server_access)])
def power_action(server_id: str, body: PowerBody):
    """전원 제어 — start/stop/reboot."""
    act = body.action
    if act == "start":
        payload = {"os-start": None}
    elif act == "stop":
        payload = {"os-stop": None}
    elif act == "reboot":
        payload = {"reboot": {"type": "HARD" if body.hard else "SOFT"}}
    else:
        raise HTTPException(400, "action은 start|stop|reboot 중 하나")
    try:
        _nova_req("POST", f"/servers/{server_id}/action", json=payload)
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"전원 제어({act}) 실패: {e}")
    return {"ok": True}


@router.get("/servers/{server_id}/console", dependencies=[Depends(server_access)])
def server_console(server_id: str, type: str = "novnc", protocol: str = "vnc"):
    """웹 콘솔 URL 발급 — Nova remote-consoles(noVNC). 호출마다 토큰 URL 새로 발급."""
    try:
        d = _nova_req(
            "POST",
            f"/servers/{server_id}/remote-consoles",
            json={"remote_console": {"protocol": protocol, "type": type}},
        ).json()
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"콘솔 URL 발급 실패: {e}")
    rc = d.get("remote_console", {})
    url = rc.get("url")
    # Nova 가 내부주소/포트(443)로 주면 브라우저가 못 여니 외부 접속용 base 로 치환.
    if url and settings.os_console_public_base:
        base = urlsplit(settings.os_console_public_base)
        orig = urlsplit(url)
        url = urlunsplit((base.scheme or orig.scheme, base.netloc or orig.netloc, orig.path, orig.query, orig.fragment))
    return {"url": url, "type": rc.get("type"), "protocol": rc.get("protocol")}


class SnapshotBody(BaseModel):
    name: str  # 만들 이미지(스냅샷) 이름


@router.post("/servers/{server_id}/snapshot", status_code=202, dependencies=[Depends(server_access)])
def create_snapshot(server_id: str, body: SnapshotBody):
    """인스턴스 스냅샷 — Nova createImage. 현재 서버 상태를 이미지로 저장(그 이미지로 새 VM 생성 가능).
    포털 VM 은 볼륨 부팅이라 Nova 가 부팅 볼륨의 Cinder 스냅샷을 뜨고, 이미지에는 그 매핑만 담긴다
    (Horizon '인스턴스 스냅샷 생성'과 동일). 응답 Location 헤더로 만들어진 이미지 ID 를 돌려준다."""
    name = body.name.strip()
    if not name:
        raise HTTPException(400, "스냅샷 이름을 입력하세요")
    try:
        r = _nova_req("POST", f"/servers/{server_id}/action", json={"createImage": {"name": name}})
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"스냅샷 생성 실패: {e}")
    # Nova 는 생성된 이미지 ID 를 Location 헤더(.../images/<id>)로 준다
    image_id = (r.headers.get("Location") or "").rstrip("/").rsplit("/", 1)[-1] or None
    return {"ok": True, "imageId": image_id, "name": name}


@router.delete("/servers/{server_id}", status_code=204, dependencies=[Depends(require_admin)])
def delete_server(server_id: str):
    try:
        _nova_req("DELETE", f"/servers/{server_id}")
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"가상서버 삭제 실패: {e}")
    return None


class AttachVolumeBody(BaseModel):
    volumeId: str


@router.post("/servers/{server_id}/volumes")
def attach_volume(server_id: str, body: AttachVolumeBody, c: Caller = Depends(server_access)):
    """볼륨을 VM에 연결(attach). 사용자는 소속 프로젝트 볼륨만."""
    if not c.is_admin:
        ensure_project(c, volume_project(body.volumeId))
    try:
        _nova_req("POST", f"/servers/{server_id}/os-volume_attachments",
                  json={"volumeAttachment": {"volumeId": body.volumeId}})
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"볼륨 연결 실패: {e}")
    return get_server(server_id)


@router.delete("/servers/{server_id}/volumes/{volume_id}", dependencies=[Depends(server_access)])
def detach_volume(server_id: str, volume_id: str):
    """볼륨을 VM에서 분리(detach)."""
    try:
        _nova_req("DELETE", f"/servers/{server_id}/os-volume_attachments/{volume_id}")
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"볼륨 분리 실패: {e}")
    return get_server(server_id)
