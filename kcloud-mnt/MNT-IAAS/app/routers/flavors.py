"""IaaS 플레이버 — OpenStack Nova 중개. 가속기는 flavor extra_specs(accel:device_profile)=Cyborg device-profile.

플레이버는 '전역(admin)' 자원 — 프로젝트 소속 아님(프로젝트 컬럼 없음).
환경독립: override=template.format(service=...)+버전, catalog=service_type.
"""
import json
from urllib.parse import urlencode

from fastapi import APIRouter, Depends, HTTPException, Query
from pydantic import BaseModel

from ..auth import project_member, require_admin
from ..config import settings
from ..openstack_client import get_conn, os_request
from ..openstack_quota import get_project_quota

router = APIRouter(tags=["flavors"])


def _os_req(service: str, stype: str, ver: str, method: str, path: str, *, json=None, headers=None):
    conn = get_conn()
    if settings.os_endpoint_mode == "catalog":
        r = os_request(conn, f"{ver}{path}", method, json=json, headers=headers, raise_exc=False,
                       endpoint_filter={"service_type": stype, "interface": settings.os_interface}, timeout=20)
    else:  # override
        base = settings.os_endpoint_template.format(service=service) + ver
        r = os_request(conn, f"{base}{path}", method, json=json, headers=headers, raise_exc=False, timeout=20)
    if not r.ok:
        raise RuntimeError((r.text or f"HTTP {r.status_code}")[:300])
    return r


def _nova(method: str, path: str, *, json=None):
    return _os_req("nova", "compute", "/v2.1", method, path, json=json)


# Cyborg microversion — 값은 '숫자만'(접두어 금지). 이름 조회·일부 기능에 2.2+ 필요(미충족 시 406).
def _cyborg(method: str, path: str, *, json=None):
    return _os_req("cyborg", "accelerator", "/v2", method, path, json=json,
                   headers={"OpenStack-API-Version": "2.2"})


# Placement — 경로에 버전 세그먼트 없음(루트). microversion 은 'placement X.Y' 형식(traits 는 1.6+).
def _placement(method: str, path: str, mv: str = "1.6"):
    return _os_req("placement", "placement", "", method, path,
                   headers={"OpenStack-API-Version": f"placement {mv}"})


# device-profile group 의 resource class → 화면 가속기 종류
_ACCEL_KIND = {"PGPU": "GPU", "CUSTOM_AICHIP": "NPU"}


def _dp_accelerators(groups: list[dict] | None) -> list[dict]:
    """device-profile groups → [{kind, count}]. 예) [{"resources:CUSTOM_AICHIP": "1", ...}] → [{NPU, 1}].
    group 마다 resources:<class>=<개수> 를 종류별로 합산. 모르는 class 는 class 이름 그대로 표시."""
    counts: dict[str, int] = {}
    for g in groups or []:
        for key, val in g.items():
            if key.startswith("resources:"):
                rc = key.split(":", 1)[1]
                kind = _ACCEL_KIND.get(rc, rc)
                counts[kind] = counts.get(kind, 0) + int(val)
    return [{"kind": k, "count": c} for k, c in counts.items()]


def _to_flavor(f: dict, accel: str | None, accelerators: list[dict] | None = None, dp_missing: bool = False) -> dict:
    return {
        "id": f["id"],
        "name": f.get("name") or f["id"],
        "vcpus": f.get("vcpus", 0),
        "ramMb": f.get("ram", 0),
        "diskGb": f.get("disk", 0),
        "isPublic": bool(f.get("os-flavor-access:is_public", True)),
        "accelerator": accel,  # 가속기 device-profile(없으면 None)
        "accelerators": accelerators or [],  # device-profile 의 가속기 종류·개수 [{kind: GPU|NPU, count}]
        "deviceProfileMissing": dp_missing,  # 가리키는 device-profile 이 Cyborg 에 없음 — 이 플레이버로는 VM 을 만들 수 없다
    }


def _flavor_items(flavors: list[dict]) -> list[dict]:
    """Nova 플레이버 → 화면 형식 + 가속기(device_profile) extra_spec + 가속기 종류(GPU/NPU)."""
    try:
        # 가속기 종류는 부가정보 — Cyborg 조회가 실패해도 플레이버 목록은 돌려준다
        dps = {d["name"]: _dp_accelerators(d.get("groups"))
               for d in _cyborg("GET", "/device_profiles").json().get("device_profiles", [])}
    except Exception:  # noqa: BLE001
        dps = None  # 조회 실패 — device-profile 이 없는지 알 수 없다
    items = []
    for f in flavors:
        specs = _nova("GET", f"/flavors/{f['id']}/os-extra_specs").json().get("extra_specs", {})
        dp = specs.get("accel:device_profile")
        missing = bool(dp) and dps is not None and dp not in dps
        items.append(_to_flavor(f, dp, (dps or {}).get(dp), missing))
    return items


@router.get("/flavors")
def list_flavors():
    """플레이버 목록 + 가속기(device_profile) extra_spec + 가속기 종류(GPU/NPU)."""
    try:
        items = _flavor_items(_nova("GET", "/flavors/detail").json()["flavors"])
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"플레이버 조회 실패: {e}")
    return {"items": items, "count": len(items)}


@router.get("/projects/{project_id}/flavors", dependencies=[Depends(project_member)])
def list_project_flavors(project_id: str):
    """프로젝트가 쓸 수 있는 플레이버 — public + 그 프로젝트에 접근이 허용된 비공개. VM·클러스터 신청용.
    Nova 기본 조회는 공개만 주므로 is_public=None(관리자)으로 전부 받아 비공개는 접근 목록(os-flavor-access)으로 거른다."""
    try:
        usable = []
        for f in _nova("GET", "/flavors/detail?is_public=None").json()["flavors"]:
            if f.get("os-flavor-access:is_public", True):
                usable.append(f)
                continue
            access = _nova("GET", f"/flavors/{f['id']}/os-flavor-access").json().get("flavor_access", [])
            if any(a.get("tenant_id") == project_id for a in access):
                usable.append(f)
        items = _flavor_items(usable)
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"플레이버 조회 실패: {e}")
    return {"items": items, "count": len(items)}


@router.get("/device-profiles")
def list_device_profiles():
    """Cyborg device-profiles — 플레이버 생성 시 선택용 + 디바이스 프로파일 관리 화면용."""
    try:
        dps = _cyborg("GET", "/device_profiles").json().get("device_profiles", [])
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"device-profile 조회 실패: {e}")
    return {"items": [{
        "name": d["name"],
        "uuid": d.get("uuid"),
        "description": d.get("description"),
        "accelerators": _dp_accelerators(d.get("groups")),  # [{kind, count}]
        "groups": d.get("groups") or [],                    # 원본(화면 상세용)
    } for d in dps]}


class DpTrait(BaseModel):
    name: str                   # 예: CUSTOM_FURIOSA_0001
    policy: str = "required"    # required | forbidden


# 가속기 사양 — Cyborg 규칙상 group 1개 = 장치 1개(개수 고정 1)라, 수량은 group 복제 수로 표현한다.
class DpAccelerator(BaseModel):
    resourceClass: str          # 예: PGPU(GPU), CUSTOM_AICHIP(NPU)
    quantity: int = 1           # 장치 수 — 이 수만큼 count=1 group 을 만든다
    traits: list[DpTrait] = []


class CreateDeviceProfileBody(BaseModel):
    name: str
    description: str | None = None
    accelerators: list[DpAccelerator]


@router.post("/device-profiles", status_code=201, dependencies=[Depends(require_admin)])
def create_device_profile(body: CreateDeviceProfileBody):
    if not body.name.strip():
        raise HTTPException(400, "프로파일 이름이 필요합니다")
    if not body.accelerators:
        raise HTTPException(400, "가속기를 하나 이상 지정해야 합니다")
    groups: list[dict] = []
    for a in body.accelerators:
        rc = a.resourceClass.strip()
        if not rc or a.quantity < 1:
            raise HTTPException(400, "리소스 클래스와 1 이상의 수량이 필요합니다")
        traits: dict[str, str] = {}
        for t in a.traits:
            if not t.name.strip():
                continue
            if t.policy not in ("required", "forbidden"):
                raise HTTPException(400, "트레잇 정책은 required 또는 forbidden 이어야 합니다")
            traits[f"trait:{t.name.strip()}"] = t.policy
        # group 1개 = 장치 1개(재고 1) → 수량만큼 동일 group 복제, 각 개수는 반드시 "1"
        for _ in range(a.quantity):
            groups.append({f"resources:{rc}": "1", **traits})
    payload = {"name": body.name.strip(), "groups": groups}
    if body.description:
        payload["description"] = body.description.strip()
    try:
        # Cyborg POST /device_profiles 는 '원소 1개 배열' 바디를 받는다(객체 직접 전송 시 400)
        r = _cyborg("POST", "/device_profiles", json=[payload]).json()
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"device-profile 생성 실패: {e}")
    created = r.get("device_profiles", r) if isinstance(r, dict) else r
    first = created[0] if isinstance(created, list) and created else created
    return {"name": body.name.strip(), "uuid": (first or {}).get("uuid") if isinstance(first, dict) else None}


def _flavors_using(dp_name: str) -> list[str]:
    """accel:device_profile 로 이 프로파일을 참조하는 플레이버 이름 목록."""
    out: list[str] = []
    for f in _nova("GET", "/flavors/detail").json()["flavors"]:
        specs = _nova("GET", f"/flavors/{f['id']}/os-extra_specs").json().get("extra_specs", {})
        if specs.get("accel:device_profile") == dp_name:
            out.append(f.get("name") or f["id"])
    return out


@router.delete("/device-profiles/{uuid}", status_code=204, dependencies=[Depends(require_admin)])
def delete_device_profile(uuid: str, force: bool = Query(False)):
    # 참조 무결성: Cyborg 는 참조 중인 프로파일도 즉시 지운다 → 삭제 전 flavor 참조를 선검증(가이드 권장).
    if not force:
        try:
            dps = _cyborg("GET", "/device_profiles").json().get("device_profiles", [])
            name = next((d["name"] for d in dps if d.get("uuid") == uuid), None)
            used = _flavors_using(name) if name else []
        except Exception:  # noqa: BLE001
            used = []  # 조회 실패 시 삭제를 막지는 않는다(아래에서 시도)
        if used:
            raise HTTPException(409, f"이 프로파일을 참조하는 플레이버가 있습니다: {', '.join(used)}. 먼저 연결을 해제하거나 강제 삭제하세요.")
    try:
        _cyborg("DELETE", f"/device_profiles/{uuid}")
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"device-profile 삭제 실패: {e}")
    return None


@router.get("/accelerator-devices", dependencies=[Depends(require_admin)])
def accelerator_devices(type: str | None = Query(None), vendor: str | None = Query(None),
                        hostname: str | None = Query(None)):
    """물리 가속기 장치 현황 — 인벤토리 + Resource Class/Trait + 연결 VM + 사용 상태(조회 전용, 관리자 — 모든 프로젝트 VM 이 보임)."""
    try:
        devices = _cyborg("GET", "/devices").json().get("devices", [])
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"가속기 장치 조회 실패: {e}")
    # deployables·arqs 조회 실패 시 상태 '알 수 없음'(절대 '사용 가능'으로 보지 않는다)
    unknown = False
    try:
        deployables = _cyborg("GET", "/deployables").json().get("deployables", [])
    except Exception:  # noqa: BLE001
        deployables, unknown = [], True
    try:
        arqs = _cyborg("GET", "/accelerator_requests").json().get("arqs", [])
    except Exception:  # noqa: BLE001
        arqs, unknown = [], True

    device_rps: dict = {}  # device_id → [rp_uuid]
    for d in deployables:
        device_rps.setdefault(d.get("device_id"), []).append(d.get("rp_uuid"))
    rp_instances: dict = {}  # rp_uuid → [instance_uuid] (바인딩된 VM)
    for a in arqs:
        rp, inst = a.get("device_rp_uuid"), a.get("instance_uuid")
        if rp and inst:
            rp_instances.setdefault(rp, []).append(inst)
    try:
        servers = _nova("GET", "/servers/detail?all_tenants=1").json().get("servers", [])
        inst_name = {s["id"]: s.get("name") for s in servers}
    except Exception:  # noqa: BLE001
        inst_name = {}

    rc_cache: dict = {}
    tr_cache: dict = {}

    def _rp_rc(rp: str) -> list[str]:
        if rp not in rc_cache:
            try:
                rc_cache[rp] = list(_placement("GET", f"/resource_providers/{rp}/inventories").json().get("inventories", {}).keys())
            except Exception:  # noqa: BLE001
                rc_cache[rp] = []
        return rc_cache[rp]

    def _rp_traits(rp: str) -> list[str]:
        if rp not in tr_cache:
            try:
                tr_cache[rp] = [t for t in _placement("GET", f"/resource_providers/{rp}/traits").json().get("traits", []) if t.startswith("CUSTOM_")]
            except Exception:  # noqa: BLE001
                tr_cache[rp] = []
        return tr_cache[rp]

    items = []
    for dev in devices:
        did, duuid = dev.get("id"), dev.get("uuid")
        rps = device_rps.get(did) or device_rps.get(duuid) or []
        rcs: list[str] = []
        traits: list[str] = []
        instances: list[dict] = []
        for rp in rps:
            rcs += _rp_rc(rp)
            traits += _rp_traits(rp)
            for inst in rp_instances.get(rp, []):
                instances.append({"id": inst, "name": inst_name.get(inst)})
        if instances:
            usage = "used"
        elif unknown or not rps:
            usage = "unknown"
        else:
            usage = "available"
        # std_board_info(JSON 문자열) → product_id — 포털 모델명 매핑 키
        sbi = dev.get("std_board_info")
        product_id = None
        if isinstance(sbi, str):
            try:
                product_id = (json.loads(sbi) or {}).get("product_id")
            except ValueError:
                product_id = None
        elif isinstance(sbi, dict):
            product_id = sbi.get("product_id")
        items.append({
            "id": did, "uuid": duuid, "hostname": dev.get("hostname"),
            "type": dev.get("type"), "model": dev.get("model"), "vendor": dev.get("vendor"),
            "productId": (product_id or "").lower() or None,
            "resourceClasses": sorted(set(rcs)), "traits": sorted(set(traits)),
            "usage": usage, "instances": instances,
        })

    def _ok(it: dict) -> bool:
        return ((not type or (it["type"] or "").lower() == type.lower())
                and (not vendor or (it["vendor"] or "").lower() == vendor.lower())
                and (not hostname or (it["hostname"] or "") == hostname))

    items = [it for it in items if _ok(it)]
    return {"items": items, "count": len(items)}


class CreateFlavorBody(BaseModel):
    name: str
    vcpus: int
    ramMb: int
    diskGb: int
    isPublic: bool = True
    deviceProfile: str | None = None  # 가속기 device-profile 이름(선택)


# ── 배치·가용량 미리보기(자원신청 승인 판단용) ────────────────────────────────
# Placement allocation_candidates = Nova 스케줄러가 쓰는 '자원이 맞는 노드' 질의. 최종 노드는 스케줄러 필터·가중치가 정한다.
# 1.36: 숫자 그룹(가속기, 1.25+)·금지 trait(!, 1.22+)·provider_summaries 의 root/traits(1.29+/1.17+). 테스트배드 최대 1.39.
_PREVIEW_MV = "1.36"


def _req_groups(specs: dict) -> tuple[dict[str, int], list[str]]:
    """'resources:<RC>'=개수 · 'trait:<T>'=required|forbidden 형식(플레이버 extra_specs, device-profile group) → 자원·trait."""
    res = {k.split(":", 1)[1]: int(v) for k, v in specs.items() if k.startswith("resources:")}
    req = [("" if v == "required" else "!") + k.split(":", 1)[1]
           for k, v in specs.items() if k.startswith("trait:") and v in ("required", "forbidden")]
    return res, req


def _candidates(resources: dict[str, int], required: list[str], groups: list[tuple[dict[str, int], list[str]]]) -> dict:
    q = {"resources": ",".join(f"{k}:{v}" for k, v in resources.items() if v > 0), "required": ",".join(required)}
    for i, (res, req) in enumerate(groups, 1):
        q[f"resources{i}"] = ",".join(f"{k}:{v}" for k, v in res.items())
        q[f"required{i}"] = ",".join(req)
    if len(groups) > 1:
        q["group_policy"] = "none"
    qs = urlencode({k: v for k, v in q.items() if v})
    return _placement("GET", f"/allocation_candidates?{qs}", _PREVIEW_MV).json()


def _matches(summary: dict, res: dict[str, int], req: list[str]) -> bool:
    traits = set(summary.get("traits") or [])
    return (any(rc in summary["resources"] for rc in res)
            and all((t[1:] not in traits) if t.startswith("!") else (t in traits) for t in req))


@router.get("/placement-preview", dependencies=[Depends(require_admin)])
def placement_preview(flavorId: str, projectId: str):
    """컴퓨트 노드별 vCPU·메모리·가속기 사용/용량과 후보 여부(신청 스펙이 들어가는지) + 프로젝트 쿼터
    + 노드 지정 가능 여부(hostPinning — 없으면 스케줄러가 후보 중에서 고른다).
    부팅 디스크는 항상 Cinder 볼륨이라 노드 디스크(DISK_GB)는 세지 않는다. Nova 처럼 꺼 둔 노드(COMPUTE_STATUS_DISABLED)는 후보에서 뺀다."""
    try:
        f = _nova("GET", f"/flavors/{flavorId}").json()["flavor"]
        specs = _nova("GET", f"/flavors/{flavorId}/os-extra_specs").json().get("extra_specs", {})
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"플레이버 조회 실패: {e}")
    over, required = _req_groups(specs)  # 플레이버가 덮어쓴 자원(예: PCPU)·필수 trait
    resources = {"VCPU": f.get("vcpus", 0), "MEMORY_MB": f.get("ram", 0), **over}
    required.append("!COMPUTE_STATUS_DISABLED")
    groups = []
    if dp := specs.get("accel:device_profile"):
        try:
            prof = next((d for d in _cyborg("GET", "/device_profiles").json().get("device_profiles", [])
                         if d["name"] == dp), None)
        except Exception as e:  # noqa: BLE001
            raise HTTPException(502, f"디바이스 프로파일 조회 실패: {e}")
        if prof is None:
            raise HTTPException(400, "연결된 디바이스 프로파일이 존재하지 않습니다")
        groups = [g for g in map(_req_groups, prof.get("groups") or []) if g[0]]
    try:
        cand = _candidates(resources, required, groups)
        every = _candidates({"VCPU": 1}, [], [])  # 후보가 아닌 노드도 보여주려고 모든 컴퓨트 노드(꺼 둔 노드 포함)를 함께 읽는다
        names = {rp["uuid"]: rp["name"] for rp in _placement("GET", "/resource_providers").json()["resource_providers"]}
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"배치 후보 조회 실패: {e}")

    sums = {**every["provider_summaries"], **cand["provider_summaries"]}
    ok = {sums[rp]["root_provider_uuid"]  # 후보 노드(컴퓨트 RP) — 가속기 RP 의 루트
          for a in cand["allocation_requests"] for rp in a["allocations"]}
    roots = list(dict.fromkeys(s["root_provider_uuid"] for s in sums.values()))
    disabled = {r for r in roots if "COMPUTE_STATUS_DISABLED" in (sums[r].get("traits") or [])}
    # 가속기는 종류(자원+trait)별로 합쳐 센다 — 예) RNGD 1장 그룹 2개 = RNGD 2장
    kinds: dict[tuple[str, tuple[str, ...]], int] = {}
    for res, req in groups:
        for rc, n in res.items():
            kinds[(rc, tuple(req))] = kinds.get((rc, tuple(req)), 0) + n

    def accel_use(root: str, rc: str, n: int, req: tuple[str, ...]) -> dict[str, int]:  # 노드의 해당 종류 가속기 사용/용량
        ds = [s for s in sums.values() if s["root_provider_uuid"] == root and _matches(s, {rc: n}, list(req))]
        return {k: sum(s["resources"][rc][k] for s in ds) for k in ("used", "capacity")}

    nodes = [{
        "host": names.get(root, root), "candidate": root in ok, "disabled": root in disabled,
        "vcpu": {k: sums[root]["resources"].get("VCPU", {}).get(k, 0) for k in ("used", "capacity")},
        "ramMb": {k: sums[root]["resources"].get("MEMORY_MB", {}).get(k, 0) for k in ("used", "capacity")},
        "accel": [accel_use(root, rc, n, req) for (rc, req), n in kinds.items()],
    } for root in roots]

    def room(c: dict) -> float:  # vCPU·메모리 중 덜 남은 쪽 비율
        return min(1 - c[k]["used"] / c[k]["capacity"] if c[k]["capacity"] else 0 for k in ("vcpu", "ramMb"))
    nodes.sort(key=lambda c: (c["candidate"], room(c)), reverse=True)  # 후보 먼저, 그 안에서 여유가 큰 노드부터
    accelerators = []
    for (rc, req), n in kinds.items():  # 종류별 전체 여유 — 꺼 둔 노드는 뺀다
        use = [accel_use(r, rc, n, req) for r in roots if r not in disabled]
        accelerators.append({"kind": _ACCEL_KIND.get(rc, rc), "resourceClass": rc, "traits": list(req), "count": n,
                             "free": sum(u["capacity"] - u["used"] for u in use), "total": sum(u["capacity"] for u in use)})

    try:
        quota, quota_error = get_project_quota(projectId), None
    except HTTPException as e:  # 쿼터를 못 읽어도 노드 정보는 보여준다
        quota, quota_error = None, e.detail
    from .servers import can_pin_host  # servers 가 이 모듈을 불러 쓰므로(순환) 여기서
    try:
        pinning = can_pin_host(projectId)
    except Exception:  # noqa: BLE001 — 확인을 못 하면 지정하지 않는다(스케줄러가 고름)
        pinning = False
    return {"nodes": nodes, "accelerators": accelerators, "hostPinning": pinning,
            "quota": quota, "quotaError": quota_error}


@router.post("/flavors", status_code=201, dependencies=[Depends(require_admin)])
def create_flavor(body: CreateFlavorBody):
    if not body.name.strip():
        raise HTTPException(400, "플레이버 이름이 필요합니다")
    if body.vcpus <= 0 or body.ramMb <= 0:
        raise HTTPException(400, "vCPU/RAM 은 1 이상이어야 합니다")
    payload = {"flavor": {
        "name": body.name.strip(), "vcpus": body.vcpus, "ram": body.ramMb,
        "disk": body.diskGb, "os-flavor-access:is_public": body.isPublic,
    }}
    try:
        f = _nova("POST", "/flavors", json=payload).json()["flavor"]
        if body.deviceProfile:
            _nova("POST", f"/flavors/{f['id']}/os-extra_specs",
                  json={"extra_specs": {"accel:device_profile": body.deviceProfile}})
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"플레이버 생성 실패: {e}")
    return _to_flavor(f, body.deviceProfile)


@router.delete("/flavors/{flavor_id}", status_code=204, dependencies=[Depends(require_admin)])
def delete_flavor(flavor_id: str):
    try:
        _nova("DELETE", f"/flavors/{flavor_id}")
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"플레이버 삭제 실패: {e}")
    return None
