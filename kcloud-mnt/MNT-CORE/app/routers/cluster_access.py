"""PaaS 서비스 클러스터 접근 권한(포털) — 프로젝트 ↔ 클러스터 N:N 매핑.

서비스 클러스터는 인프라상 OpenStack 프로젝트 1개 소유지만, 포털에선 여러 프로젝트에
"볼 권한"을 부여한다(소유 ≠ 사용권한). 클러스터 목록 가시성은 이 매핑으로 필터한다.
namespace = NULL 이면 클러스터 전체(전용), 값이 있으면 그 네임스페이스만(공유, 2단계 내부 격리용).

권한: 부여·회수는 관리자. 내 접근 클러스터 조회(/my-cluster-ids)는 로그인 사용자 본인.
"""
from fastapi import APIRouter, Depends, HTTPException
from pydantic import BaseModel
from sqlalchemy import text
from sqlalchemy.orm import Session

from ..auth import CurrentUser, current_user, require_admin
from ..db import get_db
from .members import member_project_ids

router = APIRouter(tags=["paas_project_cluster"])


@router.get("/clusters/{cluster_id}/access")
def list_cluster_access(cluster_id: str, db: Session = Depends(get_db)):
    """클러스터에 접근 가능한 프로젝트 목록(+표시명). 표시명은 없으면 project_id 그대로."""
    rows = db.execute(
        text("""
            SELECT pc.project_id, pc.namespace, pc.created_at
              FROM paas_project_cluster pc
             WHERE pc.cluster_id = :c
             ORDER BY pc.project_id, pc.namespace NULLS FIRST
        """),
        {"c": cluster_id},
    ).mappings().all()
    return {"items": [{"projectId": r["project_id"], "namespace": r["namespace"],
                       "createdAt": r["created_at"].isoformat() if r["created_at"] else None} for r in rows]}


def project_cluster_ids(db: Session, project_ids: list[str]) -> list[str]:
    """프로젝트들에 매핑된 클러스터 ID — 사용자 화면 가시성·MNT-PAAS 권한 판정(/auth/me)의 기준."""
    if not project_ids:
        return []
    return list(db.execute(
        text("SELECT DISTINCT cluster_id FROM paas_project_cluster WHERE project_id = ANY(:ps)"),
        {"ps": project_ids},
    ).scalars())


@router.get("/my-cluster-ids")
def my_cluster_ids(user: CurrentUser = Depends(current_user), db: Session = Depends(get_db)):
    """내가 접근 가능한 클러스터 ID 목록(사용자 화면 가시성용). 내 프로젝트에 매핑된 클러스터."""
    return {"clusterIds": project_cluster_ids(db, member_project_ids(db, user.username))}


class GrantBody(BaseModel):
    projectId: str
    namespace: str | None = None   # NULL=클러스터 전체(전용), 값=그 네임스페이스만(공유)


@router.post("/clusters/{cluster_id}/access", status_code=201, dependencies=[Depends(require_admin)])
def grant_cluster_access(cluster_id: str, body: GrantBody, db: Session = Depends(get_db)):
    if not body.projectId.strip():
        raise HTTPException(400, "projectId 가 필요합니다")
    ns = (body.namespace or "").strip() or None
    # 멱등 — 같은 (project, cluster, namespace) 가 이미 있으면 무시
    db.execute(
        text("""
            INSERT INTO paas_project_cluster (project_id, cluster_id, namespace)
            VALUES (:p, :c, :n)
            ON CONFLICT (project_id, cluster_id, namespace) DO NOTHING
        """),
        {"p": body.projectId.strip(), "c": cluster_id, "n": ns},
    )
    db.commit()
    return list_cluster_access(cluster_id, db)


class ReconcileBody(BaseModel):
    existingClusterIds: list[str]   # 현재 Magnum 에 실재하는 클러스터 ID 목록


@router.post("/clusters/access/reconcile", dependencies=[Depends(require_admin)])
def reconcile_cluster_access(body: ReconcileBody, db: Session = Depends(get_db)):
    """실재 목록에 없는 cluster_id 의 매핑 행을 모두 삭제(고아 정리). 관리자 클러스터 화면 로드 시 호출.
    안전: 빈 목록이면 아무것도 지우지 않는다(Magnum 일시적 빈 응답에 전체 삭제되는 사고 방지)."""
    ids = [c for c in body.existingClusterIds if c]
    if not ids:
        return {"purged": 0}
    res = db.execute(
        text("DELETE FROM paas_project_cluster WHERE cluster_id <> ALL(:ids)"),
        {"ids": ids},
    )
    db.commit()
    return {"purged": res.rowcount}


@router.delete("/clusters/{cluster_id}/access", status_code=204, dependencies=[Depends(require_admin)])
def revoke_cluster_access(cluster_id: str, projectId: str, namespace: str | None = None,
                          db: Session = Depends(get_db)):
    ns = (namespace or "").strip() or None
    # namespace 가 NULL 인 행은 '= NULL' 로 못 지우므로 IS NOT DISTINCT FROM 사용
    res = db.execute(
        text("""
            DELETE FROM paas_project_cluster
             WHERE cluster_id = :c AND project_id = :p
               AND namespace IS NOT DISTINCT FROM :n
        """),
        {"c": cluster_id, "p": projectId, "n": ns},
    )
    db.commit()
    if res.rowcount == 0:
        raise HTTPException(404, "매핑을 찾을 수 없습니다")
    return None
