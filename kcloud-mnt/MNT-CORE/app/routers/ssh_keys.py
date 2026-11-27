"""사용자 SSH 공개키(키페어) — self-service.

사용자 스코프(owner=포털 username = 토큰 사용자). 프로젝트/OpenStack과 무관하게 포털 DB가 소유.
개인키(private)는 절대 받지도 저장하지도 않는다 — 공개키만 보관.
연결된 VM 수는 자원신청(tkt) 연동 후 집계 예정 — 현재는 0.
"""
import base64
import binascii
import hashlib

from fastapi import APIRouter, Depends, HTTPException
from pydantic import BaseModel
from sqlalchemy import text
from sqlalchemy.orm import Session

from ..auth import CurrentUser, current_user
from ..db import get_db

router = APIRouter(tags=["iam_ssh_key"])

_ALLOWED = ("ssh-rsa", "ssh-ed25519", "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521")


def _fingerprint(public_key: str) -> str:
    """OpenSSH 공개키 → SHA256 지문(ssh-keygen -lf 와 동일 형식)."""
    parts = public_key.strip().split()
    if len(parts) < 2 or parts[0] not in _ALLOWED:
        raise HTTPException(400, "유효한 OpenSSH 공개키가 아닙니다 (ssh-rsa/ssh-ed25519 …)")
    try:
        blob = base64.b64decode(parts[1], validate=True)
    except (binascii.Error, ValueError):
        raise HTTPException(400, "공개키 본문(base64) 형식이 올바르지 않습니다")
    digest = hashlib.sha256(blob).digest()
    return "SHA256:" + base64.b64encode(digest).decode().rstrip("=")


def _row(r) -> dict:
    return {
        "id": str(r["id"]),
        "name": r["name"],
        "publicKey": r["public_key"],
        "fingerprint": r["fingerprint"],
        "createdAt": r["created_at"].isoformat() if r["created_at"] else "",
        "vmCount": 0,  # TODO: tkt(자원신청) 연동 후 이 키로 프로비저닝된 VM 수 집계
    }


@router.get("/ssh-keys")
def list_ssh_keys(user: CurrentUser = Depends(current_user), db: Session = Depends(get_db)):
    """내 SSH 공개키 목록."""
    rows = db.execute(
        text("SELECT id, name, public_key, fingerprint, created_at FROM iam_ssh_key "
             "WHERE owner = :o ORDER BY created_at DESC"),
        {"o": user.username},
    ).mappings().all()
    return {"items": [_row(r) for r in rows], "count": len(rows)}


class CreateSshKeyBody(BaseModel):
    owner: str | None = None  # 옛 화면 호환용 — 쓰지 않음(소유자 = 토큰 사용자)
    name: str
    publicKey: str


@router.post("/ssh-keys", status_code=201)
def create_ssh_key(body: CreateSshKeyBody, user: CurrentUser = Depends(current_user), db: Session = Depends(get_db)):
    """공개키 등록(생성/가져오기 공통). 개인키는 받지 않음."""
    owner = user.username
    name = body.name.strip()
    if not name:
        raise HTTPException(400, "키 이름이 필요합니다")
    fp = _fingerprint(body.publicKey)
    dup = db.execute(
        text("SELECT 1 FROM iam_ssh_key WHERE owner = :o AND name = :n"),
        {"o": owner, "n": name},
    ).first()
    if dup:
        raise HTTPException(409, f"이미 '{name}' 이름의 키가 있습니다")
    row = db.execute(
        text("INSERT INTO iam_ssh_key (owner, name, public_key, fingerprint) "
             "VALUES (:o, :n, :p, :f) RETURNING id, name, public_key, fingerprint, created_at"),
        {"o": owner, "n": name, "p": body.publicKey.strip(), "f": fp},
    ).mappings().one()
    db.commit()
    return _row(row)


@router.delete("/ssh-keys/{key_id}", status_code=204)
def delete_ssh_key(key_id: int, user: CurrentUser = Depends(current_user), db: Session = Depends(get_db)):
    """내 키 삭제(소유자 범위로 한정 — 남의 키 삭제 방지)."""
    res = db.execute(
        text("DELETE FROM iam_ssh_key WHERE id = :id AND owner = :o"),
        {"id": key_id, "o": user.username},
    )
    db.commit()
    if res.rowcount == 0:
        raise HTTPException(404, "키를 찾을 수 없습니다")
    return None
