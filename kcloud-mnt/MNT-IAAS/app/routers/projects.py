"""IaaS 프로젝트 조회 — OpenStack Keystone 프로젝트 + Nova/Cinder 쿼터를 중개.

identity(프로젝트)는 진입 URL(os_auth_url)로 직접 질의(카탈로그 우회).
쿼터·사용량은 openstack_quota.get_project_quota (환경별 override/catalog 분기).
프론트 Tenant(flat) 형태로 정규화해 프로젝트 목록·신청 화면이 공통 사용.
"""
from fastapi import APIRouter, Depends, HTTPException
from pydantic import BaseModel

from ..auth import Caller, caller, project_member, require_admin
from ..config import settings
from ..openstack_client import get_conn, os_request
from ..openstack_quota import get_project_quota, set_project_quota

router = APIRouter(tags=["projects"])


def _to_tenant(p: dict, quota: dict | None) -> dict:
    """Keystone 프로젝트(+선택적 쿼터)를 프론트 Tenant(flat)로 변환."""
    t = {
        "id": p.get("id"),
        "name": p.get("name"),
        "description": p.get("description") or "",
        "orgId": p.get("domain_id") or "",
        "enabled": p.get("enabled", True),
        # 쿼터·사용량 (없으면 0 placeholder)
        "quotaVcpu": 0, "quotaRamGb": 0, "quotaDiskTb": 0, "quotaAcc": 0,
        "usedVcpu": 0, "usedRamGb": 0, "usedDiskTb": 0, "usedAcc": 0,
        "quotaDiskGb": 0, "usedDiskGb": 0,  # Cinder 원래 단위(GB) — 신청 화면 남은 Disk
        "members": 0,       # 추후 Keystone role assignment
        "createdAt": "",    # Keystone 프로젝트엔 생성일 없음
    }
    if quota:
        t["quotaVcpu"] = quota["vcpu"]["quota"]
        t["usedVcpu"] = quota["vcpu"]["used"]
        t["quotaRamGb"] = quota["ramGb"]["quota"]
        t["usedRamGb"] = quota["ramGb"]["used"]
        # Cinder 는 GB → 화면 Tenant 는 TB
        t["quotaDiskTb"] = round(quota["diskGb"]["quota"] / 1024, 2)
        t["usedDiskTb"] = round(quota["diskGb"]["used"] / 1024, 2)
        t["quotaDiskGb"] = quota["diskGb"]["quota"]
        t["usedDiskGb"] = quota["diskGb"]["used"]
        # 가속기(quotaAcc)는 Cyborg 연동 시 — 지금은 0
    return t


def _fetch_projects() -> list[dict]:
    conn = get_conn()
    url = settings.os_auth_url.rstrip("/") + "/projects"
    r = os_request(conn, url, "GET", timeout=15)
    r.raise_for_status()
    return r.json().get("projects", [])


@router.get("/projects")
def list_projects(detail: bool = False, c: Caller = Depends(caller)):
    """프로젝트 목록. detail=true 면 각 프로젝트 쿼터까지 합쳐 반환(N+1). 사용자는 소속 프로젝트만."""
    try:
        raw = _fetch_projects()
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"OpenStack 프로젝트 조회 실패: {type(e).__name__}: {e}")
    raw = [p for p in raw if c.can(p["id"])]  # 쿼터 조회 전에 거른다
    items = [_to_tenant(p, get_project_quota(p["id"]) if detail else None) for p in raw]
    return {"items": items, "count": len(items)}


class CreateProjectBody(BaseModel):
    name: str
    description: str = ""
    quotaVcpu: int = 0
    quotaRamGb: int = 0
    quotaDiskGb: int = 0
    quotaAcc: int = -1   # 가속기 쿼터는 OpenStack 미지원 → 무시(포털 DB 관리 예정)


@router.post("/projects", status_code=201, dependencies=[Depends(require_admin)])
def create_project(body: CreateProjectBody):
    """Keystone 프로젝트 생성 + Nova/Cinder 쿼터 설정(관리자). 가속기 쿼터는 미설정."""
    conn = get_conn()
    name = body.name.strip()
    if not name:
        raise HTTPException(400, "프로젝트 이름이 필요합니다")
    # 1) Keystone 프로젝트 생성 (default 도메인)
    try:
        url = settings.os_auth_url.rstrip("/") + "/projects"
        payload = {"project": {"name": name, "description": body.description or "",
                               "domain_id": "default", "enabled": True}}
        r = os_request(conn, url, "POST", json=payload, timeout=15)
        r.raise_for_status()
        proj = r.json()["project"]
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"프로젝트 생성 실패: {type(e).__name__}: {e}")

    # 2) Nova/Cinder 쿼터 설정 (값이 있을 때만)
    if body.quotaVcpu or body.quotaRamGb or body.quotaDiskGb:
        set_project_quota(proj["id"], vcpu=body.quotaVcpu, ram_gb=body.quotaRamGb, disk_gb=body.quotaDiskGb)

    return _to_tenant(proj, get_project_quota(proj["id"]))


class UpdateProjectBody(BaseModel):
    name: str | None = None
    description: str | None = None
    enabled: bool | None = None
    # 쿼터는 셋 다 함께 보낼 때만 반영(부분 전송 시 무시)
    quotaVcpu: int | None = None
    quotaRamGb: int | None = None
    quotaDiskGb: int | None = None


@router.patch("/projects/{project_id}", dependencies=[Depends(require_admin)])
def update_project(project_id: str, body: UpdateProjectBody):
    """프로젝트 메타(이름·설명·활성) + 쿼터 수정(관리자). 멤버는 포털 DB 관리."""
    conn = get_conn()
    # 1) Keystone 메타 수정 (전달된 필드만)
    patch: dict = {}
    if body.name is not None:
        if not body.name.strip():
            raise HTTPException(400, "프로젝트 이름은 비울 수 없습니다")
        patch["name"] = body.name.strip()
    if body.description is not None:
        patch["description"] = body.description
    if body.enabled is not None:
        patch["enabled"] = body.enabled
    if patch:
        try:
            url = settings.os_auth_url.rstrip("/") + f"/projects/{project_id}"
            r = os_request(conn, url, "PATCH", json={"project": patch}, timeout=15)
            r.raise_for_status()
        except Exception as e:  # noqa: BLE001
            raise HTTPException(502, f"프로젝트 수정 실패: {type(e).__name__}: {e}")

    # 2) 쿼터 수정 (셋 다 있을 때만)
    if body.quotaVcpu is not None and body.quotaRamGb is not None and body.quotaDiskGb is not None:
        set_project_quota(project_id, vcpu=body.quotaVcpu, ram_gb=body.quotaRamGb, disk_gb=body.quotaDiskGb)

    return get_project(project_id)


@router.delete("/projects/{project_id}", status_code=204, dependencies=[Depends(require_admin)])
def delete_project(project_id: str):
    """Keystone 프로젝트 삭제(관리자). OpenStack 자원은 사전 정리 전제."""
    conn = get_conn()
    try:
        url = settings.os_auth_url.rstrip("/") + f"/projects/{project_id}"
        r = os_request(conn, url, "DELETE", timeout=15)
        r.raise_for_status()
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"프로젝트 삭제 실패: {type(e).__name__}: {e}")
    return None


@router.get("/projects/{project_id}", dependencies=[Depends(project_member)])
def get_project(project_id: str):
    """프로젝트 상세 + 쿼터·사용량."""
    try:
        raw = _fetch_projects()
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"OpenStack 프로젝트 조회 실패: {type(e).__name__}: {e}")
    p = next((x for x in raw if x["id"] == project_id), None)
    if not p:
        raise HTTPException(404, f"프로젝트 없음: {project_id}")
    return _to_tenant(p, get_project_quota(project_id))
