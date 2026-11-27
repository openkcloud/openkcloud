"""가속기 모델명 매핑(포털 원장) — product_id → 모델명.

Cyborg 가 신형 가속기 모델명을 안 줘서(pci.ids 미등재), 포털에서 매핑을 관리한다.
키는 product_id(트레잇·Cyborg 장치 공통). 조회는 로그인 사용자, 변경은 관리자.
"""
from fastapi import APIRouter, Depends, HTTPException
from pydantic import BaseModel
from sqlalchemy import text
from sqlalchemy.orm import Session

from ..auth import require_admin
from ..db import get_db

router = APIRouter(tags=["accel_model_map"])


@router.get("/accel-models")
def list_accel_models(db: Session = Depends(get_db)):
    """매핑 전체 — 화면 표시·관리용."""
    rows = db.execute(
        text("SELECT product_id, vendor, model FROM accel_model_map ORDER BY model"),
    ).mappings().all()
    return {"items": [{"productId": r["product_id"], "vendor": r["vendor"], "model": r["model"]} for r in rows]}


class AccelModelBody(BaseModel):
    productId: str
    vendor: str | None = None
    model: str


@router.put("/accel-models", dependencies=[Depends(require_admin)])
def upsert_accel_model(body: AccelModelBody, db: Session = Depends(get_db)):
    """product_id 매핑 추가·수정(멱등)."""
    pid = body.productId.strip().lower()
    model = body.model.strip()
    if not pid or not model:
        raise HTTPException(400, "productId 와 model 이 필요합니다")
    db.execute(
        text("""
            INSERT INTO accel_model_map (product_id, vendor, model, updated_at)
            VALUES (:p, :v, :m, now())
            ON CONFLICT (product_id) DO UPDATE SET vendor = EXCLUDED.vendor, model = EXCLUDED.model, updated_at = now()
        """),
        {"p": pid, "v": (body.vendor or "").strip().lower() or None, "m": model},
    )
    db.commit()
    return {"productId": pid, "vendor": body.vendor, "model": model}


@router.delete("/accel-models/{product_id}", status_code=204, dependencies=[Depends(require_admin)])
def delete_accel_model(product_id: str, db: Session = Depends(get_db)):
    db.execute(text("DELETE FROM accel_model_map WHERE product_id = :p"), {"p": product_id.strip().lower()})
    db.commit()
    return None
