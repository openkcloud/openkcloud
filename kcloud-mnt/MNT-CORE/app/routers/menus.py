"""역할별 메뉴 조회 — DB(mnu_menu·mnu_role_menu)에서 LNB 트리를 만들어 준다.

프론트 nav.ts 의 정적 정의를 DB로 옮긴 형태. 역할별 노출·정렬·라벨은
mnu_role_menu(sort_order, label_override)가 결정한다. 역할은 토큰 사용자의 역할(포털 DB).
"""
from fastapi import APIRouter, Depends, HTTPException
from pydantic import BaseModel
from sqlalchemy import text
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from ..auth import CurrentUser, current_user, require_admin
from ..db import get_db

router = APIRouter(tags=["mnu_menu"])


@router.get("/menus")
def get_menus(user: CurrentUser = Depends(current_user), db: Session = Depends(get_db)):
    """내 역할(admin|user)에 노출되는 메뉴 트리."""
    role = user.role
    rows = db.execute(
        text(
            """
            SELECT m.id, m.parent_id, m.menu_type, m.code,
                   COALESCE(rm.label_override, m.label) AS label,
                   m.path, m.icon,
                   COALESCE(rm.sort_order, m.sort_order) AS sort_order
              FROM mnu_role_menu rm
              JOIN mnu_menu m ON m.id = rm.menu_id AND m.is_active
              JOIN iam_role r ON r.id = rm.role_id
             WHERE r.code = :role
             ORDER BY sort_order, m.id
            """
        ),
        {"role": role},
    ).mappings().all()

    if not rows:
        raise HTTPException(404, f"role '{role}' 메뉴 없음")

    by_id = {r["id"]: dict(r, children=[]) for r in rows}
    roots: list[dict] = []
    for item in by_id.values():
        parent = by_id.get(item["parent_id"])
        (parent["children"] if parent else roots).append(item)

    def strip(node: dict) -> dict:
        out = {k: node[k] for k in ("code", "label", "path", "icon", "menu_type")}
        if node["children"]:
            out["children"] = [strip(c) for c in node["children"]]
        return out

    return {"role": role, "sections": [strip(n) for n in roots]}


# ════════════════════════════════════════════════════════════════════
# 메뉴 관리(관리자) — mnu_menu 원장 CRUD + 순서변경 + 역할 노출(mnu_role_menu)
# 프론트 /settings 에서 사용. 관리자만(토큰 사용자 역할).
# ════════════════════════════════════════════════════════════════════
class MenuIn(BaseModel):
    parent_id: int | None = None
    menu_type: str = "submenu"           # section | menu | submenu
    code: str
    label: str
    path: str | None = None
    icon: str | None = None
    sort_order: int = 0
    is_active: bool = True
    roles: list[str] = []                # 노출 역할 코드(admin|user)


class MenuPatch(BaseModel):
    parent_id: int | None = None
    menu_type: str | None = None
    label: str | None = None
    path: str | None = None
    icon: str | None = None
    is_active: bool | None = None
    roles: list[str] | None = None       # 주면 전체 교체


class ReorderItem(BaseModel):
    id: int
    sort_order: int


def _set_roles(db: Session, menu_id: int, role_codes: list[str]) -> None:
    db.execute(text("DELETE FROM mnu_role_menu WHERE menu_id = :m"), {"m": menu_id})
    for code in role_codes:
        db.execute(
            text(
                """INSERT INTO mnu_role_menu (role_id, menu_id)
                   SELECT r.id, :m FROM iam_role r WHERE r.code = :c"""
            ),
            {"m": menu_id, "c": code},
        )


@router.get("/menus/all", dependencies=[Depends(require_admin)])
def list_all_menus(db: Session = Depends(get_db)):
    """관리용 — 전체 메뉴(비활성 포함) + 노출 역할. 프론트가 트리로 조립."""
    rows = db.execute(
        text(
            """
            SELECT m.id, m.parent_id, m.menu_type, m.code, m.label, m.path, m.icon,
                   m.sort_order, m.is_active,
                   COALESCE(array_agg(r.code ORDER BY r.code) FILTER (WHERE r.code IS NOT NULL), '{}') AS roles
              FROM mnu_menu m
              LEFT JOIN mnu_role_menu rm ON rm.menu_id = m.id
              LEFT JOIN iam_role r ON r.id = rm.role_id
             GROUP BY m.id
             ORDER BY m.parent_id NULLS FIRST, m.sort_order, m.id
            """
        )
    ).mappings().all()
    return {"items": [dict(r) for r in rows]}


@router.post("/menus", status_code=201, dependencies=[Depends(require_admin)])
def create_menu(body: MenuIn, db: Session = Depends(get_db)):
    try:
        mid = db.execute(
            text(
                """INSERT INTO mnu_menu (parent_id, menu_type, code, label, path, icon, sort_order, is_active)
                   VALUES (:parent_id, :menu_type, :code, :label, :path, :icon, :sort_order, :is_active)
                   RETURNING id"""
            ),
            body.model_dump(exclude={"roles"}),
        ).scalar()
        _set_roles(db, mid, body.roles)
        db.commit()
    except IntegrityError:
        db.rollback()
        raise HTTPException(409, f"코드 '{body.code}' 가 이미 있습니다.")
    return {"id": mid}


@router.patch("/menus/{menu_id}", dependencies=[Depends(require_admin)])
def update_menu(menu_id: int, body: MenuPatch, db: Session = Depends(get_db)):
    fields = body.model_dump(exclude_unset=True, exclude={"roles"})
    try:
        if fields:
            sets = ", ".join(f"{k} = :{k}" for k in fields)
            db.execute(text(f"UPDATE mnu_menu SET {sets} WHERE id = :id"), {**fields, "id": menu_id})
        if body.roles is not None:
            _set_roles(db, menu_id, body.roles)
        db.commit()
    except IntegrityError as e:
        db.rollback()
        raise HTTPException(409, str(e.orig))
    return {"id": menu_id}


@router.delete("/menus/{menu_id}", dependencies=[Depends(require_admin)])
def delete_menu(menu_id: int, db: Session = Depends(get_db)):
    """삭제 — 하위 메뉴·역할 매핑은 FK ON DELETE CASCADE 로 함께 제거."""
    n = db.execute(text("DELETE FROM mnu_menu WHERE id = :id"), {"id": menu_id}).rowcount
    db.commit()
    if not n:
        raise HTTPException(404, "메뉴 없음")
    return {"deleted": menu_id}


@router.post("/menus/reorder", dependencies=[Depends(require_admin)])
def reorder_menus(items: list[ReorderItem], db: Session = Depends(get_db)):
    """형제 내 순서 일괄 저장(mnu_menu.sort_order)."""
    for it in items:
        db.execute(text("UPDATE mnu_menu SET sort_order = :s WHERE id = :id"), {"s": it.sort_order, "id": it.id})
    db.commit()
    return {"updated": len(items)}
