"""IaaS 볼륨·스토리지 — OpenStack Cinder 중개. 백엔드는 Ceph(RBD)지만 포털은 Cinder만 호출.

환경독립(프로젝트 쿼터와 동일):
  - catalog : 카탈로그 endpoint가 이미 /v3/{project} 스코프 → 상대경로만 붙임.
  - override: base = cinder_template + /v3/{admin_pid} + path.
관리자 전체 조회는 all_tenants=1. 볼륨 유형=Ceph 풀/티어, 용량=scheduler-stats(get_pools).
"""
from fastapi import APIRouter, Depends, HTTPException
from fastapi.concurrency import run_in_threadpool
from pydantic import BaseModel

from ..auth import Caller, caller, ensure_project, require_admin, scoped
from ..config import settings
from ..openstack_client import get_conn, get_conn_for_project, os_request

router = APIRouter(tags=["volumes"])


def _cinder_err(r) -> str:
    """Cinder 에러 응답 body에서 사람이 읽을 메시지 추출(openstacksdk의 모호한 래핑 회피)."""
    try:
        body = r.json()
        if isinstance(body, dict):
            for v in body.values():
                if isinstance(v, dict) and v.get("message"):
                    return v["message"]
            if body.get("message"):
                return body["message"]
        return (r.text or f"HTTP {r.status_code}")[:300]
    except Exception:  # noqa: BLE001
        return (r.text or f"HTTP {r.status_code}")[:300]


def _cinder_req(method: str, path: str, *, json: dict | None = None, project_id: str | None = None):
    """project_id 지정 시 그 프로젝트로 스코프해 호출(생성이 그 프로젝트 소유로 들어감).
    raise_exc=False 로 받아 Cinder 원본 에러 메시지를 그대로 노출한다.
    조회는 연결 끊김 시 1회 재시도, 생성·변경은 재시도 없이 새 연결로 보낸다(os_request)."""
    conn = get_conn_for_project(project_id) if project_id else get_conn()
    if settings.os_endpoint_mode == "catalog":
        r = os_request(conn, path, method, json=json, raise_exc=False,
                       endpoint_filter={"service_type": "block-storage", "interface": settings.os_interface},
                       timeout=20)
    else:  # override
        pid = project_id or conn.session.get_project_id()
        base = settings.os_endpoint_template.format(service="cinder") + f"/v3/{pid}"
        r = os_request(conn, f"{base}{path}", method, json=json, timeout=20, raise_exc=False)
    if not r.ok:
        raise RuntimeError(_cinder_err(r))
    return r


def _cinder_get(path: str) -> dict:
    return _cinder_req("GET", path).json()


def _to_volume(v: dict) -> dict:
    atts = v.get("attachments") or []
    return {
        "id": v["id"],
        "name": v.get("name") or v["id"],
        "tenantId": v.get("os-vol-tenant-attr:tenant_id") or "",
        "size": v.get("size", 0),
        "type": v.get("volume_type") or "",
        "status": v.get("status", ""),
        "bootable": str(v.get("bootable")).lower() == "true",
        "attachments": [{"serverId": a.get("server_id"), "device": a.get("device")} for a in atts],
        "description": v.get("description") or "",
        "createdAt": v.get("created_at") or "",
    }


def volume_project(volume_id: str) -> str:
    """볼륨이 속한 프로젝트 ID(권한 확인용)."""
    try:
        return _cinder_get(f"/volumes/{volume_id}")["volume"].get("os-vol-tenant-attr:tenant_id") or ""
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"볼륨 조회 실패: {type(e).__name__}: {e}")


async def volume_access(volume_id: str, c: Caller = Depends(caller)) -> Caller:
    """경로의 볼륨이 소속 프로젝트 것인지(관리자는 통과)."""
    if not c.is_admin:
        ensure_project(c, await run_in_threadpool(volume_project, volume_id))
    return c


@router.get("/volumes")
def list_volumes(c: Caller = Depends(caller)):
    """볼륨 목록 — 관리자는 전체 프로젝트, 사용자는 소속 프로젝트."""
    try:
        vols = _cinder_get("/volumes/detail?all_tenants=1")["volumes"]
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"볼륨 조회 실패: {type(e).__name__}: {e}")
    return scoped(c, [_to_volume(v) for v in vols])


@router.get("/volume-types")
def list_volume_types():
    """볼륨 유형(Ceph 풀/티어) — 생성 드롭다운용."""
    try:
        types = _cinder_get("/types")["volume_types"]
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"볼륨 유형 조회 실패: {type(e).__name__}: {e}")
    return {"items": [{"id": t["id"], "name": t.get("name") or t["id"]} for t in types]}


@router.get("/storage-capacity")
def storage_capacity():
    """Ceph(Cinder 백엔드) 풀 용량 — 총/사용/가용(GB)."""
    try:
        pools = _cinder_get("/scheduler-stats/get_pools?detail=True")["pools"]
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"스토리지 용량 조회 실패: {type(e).__name__}: {e}")
    out = []
    for p in pools:
        c = p.get("capabilities", {})
        total = c.get("total_capacity_gb") or 0
        free = c.get("free_capacity_gb") or 0
        out.append({
            "name": p.get("name", ""),
            "backend": c.get("volume_backend_name", ""),
            "protocol": c.get("storage_protocol", ""),
            "totalGb": round(float(total), 1) if isinstance(total, (int, float)) else 0,
            "freeGb": round(float(free), 1) if isinstance(free, (int, float)) else 0,
        })
    return {"items": out}


@router.get("/volumes/{volume_id}", dependencies=[Depends(volume_access)])
def get_volume(volume_id: str):
    """볼륨 상세 + 스냅샷."""
    try:
        v = _cinder_get(f"/volumes/{volume_id}")["volume"]
        snaps = _cinder_get(f"/snapshots/detail?all_tenants=1&volume_id={volume_id}")["snapshots"]
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"볼륨 상세 조회 실패: {type(e).__name__}: {e}")
    d = _to_volume(v)
    d["snapshots"] = [
        {"id": s["id"], "name": s.get("name") or s["id"], "size": s.get("size", 0),
         "status": s.get("status", ""), "createdAt": s.get("created_at") or ""}
        for s in snaps
    ]
    return d


class CreateVolumeBody(BaseModel):
    name: str
    size: int
    projectId: str                  # 생성 대상 프로젝트(그 프로젝트로 스코프해 생성)
    volumeType: str | None = None
    description: str = ""
    imageRef: str | None = None     # 이미지에서 부팅 볼륨
    snapshotId: str | None = None   # 스냅샷에서 복제


@router.post("/volumes", status_code=201, dependencies=[Depends(require_admin)])
def create_volume(body: CreateVolumeBody):
    if body.size <= 0:
        raise HTTPException(400, "크기(GB)는 1 이상이어야 합니다")
    if not body.projectId.strip():
        raise HTTPException(400, "대상 프로젝트가 필요합니다")
    vol: dict = {"name": body.name.strip() or None, "size": body.size, "description": body.description or ""}
    if body.volumeType:
        vol["volume_type"] = body.volumeType
    if body.imageRef:
        vol["imageRef"] = body.imageRef
    if body.snapshotId:
        vol["snapshot_id"] = body.snapshotId
    try:
        # 대상 프로젝트로 스코프해 생성 → 볼륨이 그 프로젝트 소유로 들어감
        created = _cinder_req("POST", "/volumes", json={"volume": vol}, project_id=body.projectId).json()["volume"]
    except Exception as e:  # noqa: BLE001
        msg = str(e)
        if "401" in msg or "Unauthorized" in msg or "authentication" in msg.lower():
            raise HTTPException(409, "대상 프로젝트로 접근할 수 없습니다 — 프로젝트가 비활성(disabled) 상태이거나 접근 권한이 없습니다. 프로젝트를 활성화한 뒤 다시 시도하세요.")
        raise HTTPException(502, f"볼륨 생성 실패: {msg}")
    return _to_volume(created)


@router.delete("/volumes/{volume_id}", status_code=204, dependencies=[Depends(require_admin)])
def delete_volume(volume_id: str, cascade: bool = False):
    """볼륨 삭제. cascade=true면 딸린 스냅샷도 함께 삭제. in-use면 Cinder 거부."""
    path = f"/volumes/{volume_id}" + ("?cascade=true" if cascade else "")
    try:
        _cinder_req("DELETE", path)
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"볼륨 삭제 실패: {e}")
    return None


class ExtendBody(BaseModel):
    newSize: int


@router.post("/volumes/{volume_id}/extend", dependencies=[Depends(require_admin)])
def extend_volume(volume_id: str, body: ExtendBody):
    """볼륨 크기 확장(축소 불가)."""
    try:
        _cinder_req("POST", f"/volumes/{volume_id}/action", json={"os-extend": {"new_size": body.newSize}})
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"볼륨 확장 실패: {type(e).__name__}: {e}")
    return get_volume(volume_id)


class SnapshotBody(BaseModel):
    volumeId: str
    name: str
    force: bool = True   # in-use 볼륨도 스냅샷(기본 허용)


@router.post("/snapshots", status_code=201, dependencies=[Depends(require_admin)])
def create_snapshot(body: SnapshotBody):
    snap = {"volume_id": body.volumeId, "name": body.name.strip() or None, "force": body.force}
    try:
        s = _cinder_req("POST", "/snapshots", json={"snapshot": snap}).json()["snapshot"]
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"스냅샷 생성 실패: {type(e).__name__}: {e}")
    return {"id": s["id"], "name": s.get("name"), "size": s.get("size"), "status": s.get("status")}


@router.delete("/snapshots/{snapshot_id}", status_code=204, dependencies=[Depends(require_admin)])
def delete_snapshot(snapshot_id: str):
    try:
        _cinder_req("DELETE", f"/snapshots/{snapshot_id}")
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"스냅샷 삭제 실패: {type(e).__name__}: {e}")
    return None
