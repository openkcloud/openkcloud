"""IaaS 이미지 — OpenStack Glance 중개. 라이브 조회(포털 DB 캐시 없음 = 카탈로그 동기화 불필요).

환경독립: override=template.format(service="glance")+"/v2", catalog=service_type="image".
"""
import json as _json
import math

from fastapi import APIRouter, Depends, HTTPException
from pydantic import BaseModel

from ..auth import Caller, caller, ensure_project, project_member, require_admin, scoped
from ..config import settings
from ..openstack_client import get_conn, os_request

router = APIRouter(tags=["images"])

_GB = 1024 ** 3


def _glance_req(method: str, path: str, *, params=None, data=None, headers=None):
    conn = get_conn()
    if settings.os_endpoint_mode == "catalog":
        r = os_request(conn, path, method, params=params, data=data, headers=headers, raise_exc=False,
                       endpoint_filter={"service_type": "image", "interface": settings.os_interface}, timeout=20)
    else:  # override
        base = settings.os_endpoint_template.format(service="glance") + "/v2"
        r = os_request(conn, f"{base}{path}", method, params=params, data=data, headers=headers, raise_exc=False,
                       timeout=20)
    if not r.ok:
        raise RuntimeError((r.text or f"HTTP {r.status_code}")[:300])
    return r


def _to_image(i: dict) -> dict:
    return {
        "id": i["id"],
        "name": i.get("name") or i["id"],
        "tenantId": i.get("owner") or "",
        "os": i.get("os_distro") or "",
        "osVersion": i.get("os_version") or "",      # Glance 속성 os_version (예: 22.04)
        "kubeVersion": i.get("kube_version") or "",  # Glance 속성 kube_version (클러스터 템플릿용, 예: 1.36.2)
        "diskFormat": i.get("disk_format") or "",
        "containerFormat": i.get("container_format") or "",
        "sizeGb": round((i.get("size") or 0) / _GB, 2),
        "visibility": i.get("visibility") or "",
        "status": i.get("status") or "",
        "protected": bool(i.get("protected")),
        "minDisk": i.get("min_disk", 0),
        # 부팅 볼륨 최소 크기 = max(min_disk, 가상 크기, 파일 크기) 올림 — Cinder 는 둘 다 볼륨 크기와 비교.
        # 가상 크기가 없는 이미지(location 등록·오래된 업로드 등)는 파일 크기로 대신한다
        "minVolumeGb": max(i.get("min_disk") or 0,
                           math.ceil((i.get("virtual_size") or i.get("size") or 0) / _GB),
                           math.ceil((i.get("size") or 0) / _GB)),
        "minRam": i.get("min_ram", 0),
        "createdAt": i.get("created_at") or "",
    }


# Glance 가 관리하는 읽기전용/구조 키 — 나머지는 편집 가능한 '속성(메타데이터)'(os_distro 등)
_RESERVED_KEYS = {
    "id", "name", "owner", "status", "visibility", "protected",
    "disk_format", "container_format", "min_disk", "min_ram",
    "size", "virtual_size", "checksum", "created_at", "updated_at",
    "file", "self", "schema", "tags", "direct_url", "locations",
    "os_hash_algo", "os_hash_value", "os_hidden",
}


def _properties(i: dict) -> dict:
    """편집 가능한 스칼라 속성만(os_distro·os_version·architecture·hw_*·okc_* 등)."""
    return {k: str(v) for k, v in i.items() if k not in _RESERVED_KEYS and not isinstance(v, (dict, list))}


_VIS = ("public", "private", "shared", "community")


_PUBLIC = ("public", "community")  # 모든 프로젝트가 쓰는 이미지


@router.get("/images")
def list_images(c: Caller = Depends(caller)):
    """이미지 목록 — 사용자는 공개 이미지 + 소속 프로젝트 소유 이미지."""
    try:
        d = _glance_req("GET", "/images", params={"limit": 200}).json()
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"이미지 조회 실패: {e}")
    return scoped(c, [_to_image(i) for i in d.get("images", [])], also=lambda x: x["visibility"] in _PUBLIC)


@router.get("/projects/{project_id}/images", dependencies=[Depends(project_member)])
def list_project_images(project_id: str):
    """프로젝트가 쓸 수 있는 active 이미지 — public + 프로젝트 소유 + 공유받은 것(거절 제외). 자원신청 템플릿용.
    서비스 계정이 관리자라 Glance 가 전체를 돌려주므로 여기서 거른다.
    cloudInit: 기본 true, Glance 속성 okc_cloud_init=false 인 이미지만 false(키페어 주입 불가)."""
    try:
        images, params = [], {"limit": 200, "status": "active"}
        while True:  # 관리자 계정은 전체 이미지가 보이므로 페이지(next)를 끝까지 따라간다
            page = _glance_req("GET", "/images", params=params).json()
            images += page.get("images", [])
            if not page.get("next") or not page.get("images"):
                break
            params["marker"] = images[-1]["id"]
        out = []
        for i in images:
            if i.get("visibility") == "public" or i.get("owner") == project_id:
                out.append(i)
            elif i.get("visibility") == "shared":
                members = _glance_req("GET", f"/images/{i['id']}/members").json().get("members", [])
                if any(m.get("member_id") == project_id and m.get("status") != "rejected" for m in members):
                    out.append(i)
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"이미지 조회 실패: {e}")
    items = [{**_to_image(i), "cloudInit": str(i.get("okc_cloud_init", "")).lower() != "false"} for i in out]
    return {"items": items, "count": len(items)}


@router.get("/images/{image_id}")
def get_image(image_id: str, c: Caller = Depends(caller)):
    try:
        i = _glance_req("GET", f"/images/{image_id}").json()
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"이미지 상세 조회 실패: {e}")
    d = _to_image(i)
    if d["visibility"] not in _PUBLIC:
        ensure_project(c, d["tenantId"])
    d["properties"] = _properties(i)
    return d


def _image_detail(image_id: str) -> dict:
    """admin 변경 후 재조회(스코프 검사 없이) — 상세 + 속성."""
    i = _glance_req("GET", f"/images/{image_id}").json()
    return {**_to_image(i), "properties": _properties(i)}


class VisibilityBody(BaseModel):
    visibility: str  # public | private | shared | community


@router.patch("/images/{image_id}", dependencies=[Depends(require_admin)])
def set_visibility(image_id: str, body: VisibilityBody):
    """이미지 가시성 변경(Glance JSON-Patch)."""
    if body.visibility not in _VIS:
        raise HTTPException(400, "가시성 값이 올바르지 않습니다")
    patch = [{"op": "replace", "path": "/visibility", "value": body.visibility}]
    try:
        _glance_req(
            "PATCH", f"/images/{image_id}",
            data=_json.dumps(patch),
            headers={"Content-Type": "application/openstack-images-v2.1-json-patch"},
        )
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"가시성 변경 실패: {e}")
    return _image_detail(image_id)


class PropertiesBody(BaseModel):
    set: dict[str, str] = {}      # 추가·수정할 속성
    remove: list[str] = []        # 삭제할 속성 키


@router.patch("/images/{image_id}/properties", dependencies=[Depends(require_admin)])
def update_properties(image_id: str, body: PropertiesBody):
    """이미지 속성(메타데이터) 추가·수정·삭제 — os_distro 등(Glance JSON-Patch).

    PaaS 클러스터 템플릿이 요구하는 os_distro 등을 CLI 없이 포털에서 설정하기 위함.
    """
    bad = [k for k in list(body.set) + body.remove if k in _RESERVED_KEYS or not k.strip()]
    if bad:
        raise HTTPException(400, f"속성으로 수정할 수 없는 키: {', '.join(bad)}")
    if not body.set and not body.remove:
        raise HTTPException(400, "변경할 속성이 없습니다")
    # Glance JSON-Patch: 커스텀 속성은 add(기존 키면 덮어씀)·remove
    patch = [{"op": "add", "path": f"/{k}", "value": str(v)} for k, v in body.set.items()]
    patch += [{"op": "remove", "path": f"/{k}"} for k in body.remove]
    try:
        _glance_req(
            "PATCH", f"/images/{image_id}",
            data=_json.dumps(patch),
            headers={"Content-Type": "application/openstack-images-v2.1-json-patch"},
        )
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"속성 변경 실패: {e}")
    return _image_detail(image_id)


class RegisterBody(BaseModel):
    name: str
    url: str                          # web-download 로 Glance 가 직접 받아올 qcow2 등 URL
    diskFormat: str = "qcow2"
    containerFormat: str = "bare"
    visibility: str = "private"
    minDisk: int = 0
    minRam: int = 0
    properties: dict[str, str] = {}   # 등록 시 함께 지정할 속성(os_distro 등)


@router.post("/images", dependencies=[Depends(require_admin)], status_code=201)
def register_image(body: RegisterBody):
    """URL 가져오기(web-download) 등록 — 메타데이터 생성 후 Glance 가 URL 에서 직접 내려받음.

    대용량 바이너리가 포털을 경유하지 않아 안정적. Glance 가 web-download import 를
    지원해야 하며(enabled_import_methods), 반환 직후 상태는 보통 importing(비동기).
    """
    if not body.name.strip() or not body.url.strip():
        raise HTTPException(400, "이름과 URL 이 필요합니다")
    vis = body.visibility if body.visibility in _VIS else "private"
    payload: dict = {
        "name": body.name.strip(),
        "disk_format": body.diskFormat,
        "container_format": body.containerFormat,
        "visibility": vis,
    }
    if body.minDisk:
        payload["min_disk"] = int(body.minDisk)
    if body.minRam:
        payload["min_ram"] = int(body.minRam)
    for k, v in body.properties.items():
        if k.strip() and k not in _RESERVED_KEYS:
            payload[k] = str(v)
    try:
        created = _glance_req("POST", "/images", data=_json.dumps(payload),
                              headers={"Content-Type": "application/json"}).json()
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"이미지 생성 실패: {e}")
    image_id = created["id"]
    import_payload = {"method": {"name": "web-download", "uri": body.url.strip()}}
    try:
        _glance_req("POST", f"/images/{image_id}/import", data=_json.dumps(import_payload),
                    headers={"Content-Type": "application/json"})
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"이미지 가져오기 시작 실패(Glance 가 web-download 를 지원하지 않을 수 있음): {e}")
    return _image_detail(image_id)


@router.delete("/images/{image_id}", status_code=204, dependencies=[Depends(require_admin)])
def delete_image(image_id: str):
    try:
        _glance_req("DELETE", f"/images/{image_id}")
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"이미지 삭제 실패(보호(protected) 이미지면 해제 먼저): {e}")
    return None
