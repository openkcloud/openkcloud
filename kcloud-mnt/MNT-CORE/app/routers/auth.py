"""로그인·세션 — Keycloak 토큰 기준(/auth/me·/auth/session·/auth/logout·/auth/backchannel-logout).

로그인 자체는 브라우저가 Keycloak 에서 한다(PKCE). 이 라우터는 토큰의 사용자·세션 상태를 알려 주고 끝낸다.
역할은 iam_user_role 기준 — admin 역할이 있으면 admin, 아니면 user. (포털 DB 비밀번호 로그인 /auth/login 은 Keycloak 전환으로 제거)
"""
from urllib.parse import parse_qs

from fastapi import APIRouter, Depends, Request, Response
from fastapi.concurrency import run_in_threadpool
from pydantic import BaseModel
from sqlalchemy.orm import Session

from ..auth import CurrentUser, bearer_token, current_user, end_session, end_session_by_logout_token, session_status
from ..db import SessionLocal, get_db
from .cluster_access import project_cluster_ids
from .members import member_project_ids

router = APIRouter(tags=["auth"])


@router.get("/auth/me")
def me(user: CurrentUser = Depends(current_user), db: Session = Depends(get_db)):
    """토큰의 포털 사용자 — 화면 사용자 정보, MNT-IAAS 권한 판정(역할·소속 프로젝트),
    MNT-PAAS 권한 판정(역할·접근 클러스터)에 쓴다."""
    project_ids = member_project_ids(db, user.username)
    return {
        "id": str(user.id),
        "username": user.username,
        "name": user.name,
        "email": user.email,
        "org": user.org,
        "role": user.role,
        "projectIds": project_ids,
        "clusterIds": project_cluster_ids(db, project_ids),
    }


@router.get("/auth/session")
def session(request: Request, db: Session = Depends(get_db)):
    """세션 상태 — 화면이 주기적으로 확인(다른 곳 로그인·관리자 강제 종료 안내). 만료된 토큰도 받는다."""
    return session_status(db, bearer_token(request))


class LogoutBody(BaseModel):
    reason: str = "logout"  # logout(직접 로그아웃) | idle(미사용 시간 초과 — 화면이 판단)


@router.post("/auth/logout", status_code=204)
def logout(request: Request, body: LogoutBody | None = None, db: Session = Depends(get_db)):
    """화면이 세션을 끝낸다 — 원인 기록(다른 탭·같은 토큰은 그 원인으로 거부). Keycloak 세션은 화면이 이어서 끝낸다."""
    end_session(db, bearer_token(request), (body or LogoutBody()).reason)
    return None


@router.post("/auth/backchannel-logout")
async def backchannel_logout(request: Request):
    """Keycloak 백채널 로그아웃 통보(관리자 강제 종료 등). form 의 logout_token 을 검증하고 그 세션을 끝낸다."""
    form = parse_qs((await request.body()).decode())
    token = (form.get("logout_token") or [""])[0]

    def _end():
        db = SessionLocal()
        try:
            end_session_by_logout_token(db, token)
        finally:
            db.close()

    await run_in_threadpool(_end)
    return Response(status_code=200, headers={"Cache-Control": "no-store"})
