"""공통코드 조회 — 상태·유형 등 업무 코드의 값·표시명·배지톤을 내려준다.

프론트는 코드값(예: 'pending')을 로직에 쓰고, 표시는 name/attr1 로 렌더링한다.
"""
from fastapi import APIRouter, Depends, HTTPException
from sqlalchemy import text
from sqlalchemy.orm import Session

from ..db import get_db

router = APIRouter(tags=["codes"])


@router.get("/codes")
def get_codes(group: str, db: Session = Depends(get_db)):
    """그룹별 공통코드 목록 (예: group=TKT_STATUS)."""
    rows = db.execute(
        text(
            """
            SELECT c.code, c.name, c.description, c.sort_order, c.attr1, c.attr2
              FROM cmn_code c
              JOIN cmn_code_group g ON g.group_code = c.group_code AND g.is_active
             WHERE c.group_code = :grp AND c.is_active
             ORDER BY c.sort_order, c.code
            """
        ),
        {"grp": group},
    ).mappings().all()
    if not rows:
        raise HTTPException(404, f"코드 그룹 '{group}' 없음")
    return {"group": group, "codes": [dict(r) for r in rows]}


@router.get("/code-groups")
def get_code_groups(db: Session = Depends(get_db)):
    """코드 그룹 목록."""
    rows = db.execute(
        text(
            "SELECT group_code, name, description FROM cmn_code_group WHERE is_active ORDER BY group_code"
        )
    ).mappings().all()
    return {"groups": [dict(r) for r in rows]}
