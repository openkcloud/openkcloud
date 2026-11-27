"""IaaS Nova keypair — 클러스터 노드 VM SSH 접속용 인프라 키(관리자).

사용자 포털 DB SSH키(MNT-CORE iam_ssh_key)와는 별개 자원이다. 공개키 import 만(개인키 서버 비노출).
클러스터 템플릿의 keypair 선택지로도 쓰인다.
"""
from fastapi import APIRouter, Depends, HTTPException
from pydantic import BaseModel

from ..auth import require_admin
from ..config import settings
from ..openstack_client import get_conn, os_request

router = APIRouter(tags=["keypairs"])


def _nova(method: str, path: str, *, json=None):
    conn = get_conn()
    if settings.os_endpoint_mode == "catalog":
        r = os_request(conn, f"/v2.1{path}", method, json=json, raise_exc=False,
                       endpoint_filter={"service_type": "compute", "interface": settings.os_interface}, timeout=20)
    else:  # override
        base = settings.os_endpoint_template.format(service="nova") + "/v2.1"
        r = os_request(conn, f"{base}{path}", method, json=json, raise_exc=False, timeout=20)
    if not r.ok:
        raise RuntimeError((r.text or f"HTTP {r.status_code}")[:300])
    return r


def _to_keypair(k: dict) -> dict:
    return {"name": k.get("name"), "fingerprint": k.get("fingerprint"),
            "publicKey": k.get("public_key"), "type": k.get("type", "ssh")}


@router.get("/keypairs")
def list_keypairs():
    """Nova keypair 목록 — 클러스터 템플릿 선택·관리자 화면용."""
    try:
        data = _nova("GET", "/os-keypairs").json().get("keypairs", [])
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"키페어 조회 실패: {e}")
    # os-keypairs 는 각 항목을 {"keypair": {...}} 로 감싸 돌려준다
    out = [_to_keypair(item.get("keypair", item)) for item in data]
    return {"items": out, "count": len(out)}


class ImportKeypairBody(BaseModel):
    name: str
    publicKey: str


@router.post("/keypairs", status_code=201, dependencies=[Depends(require_admin)])
def import_keypair(body: ImportKeypairBody):
    if not body.name.strip() or not body.publicKey.strip():
        raise HTTPException(400, "이름과 공개키가 필요합니다")
    payload = {"keypair": {"name": body.name.strip(), "public_key": body.publicKey.strip()}}
    try:
        k = _nova("POST", "/os-keypairs", json=payload).json()["keypair"]
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"키페어 등록 실패: {e}")
    return _to_keypair(k)


@router.delete("/keypairs/{name}", status_code=204, dependencies=[Depends(require_admin)])
def delete_keypair(name: str):
    try:
        _nova("DELETE", f"/os-keypairs/{name}")
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"키페어 삭제 실패: {e}")
    return None
