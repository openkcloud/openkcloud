"""MNT-CORE — openKcloud 포털 코어 백엔드.

소유: IAM(사용자·역할)·메뉴·자원신청 워크플로우·마켓·감사 (PostgreSQL 유일 소유자).
자원의 실시간 상태는 저장하지 않는다 — IaaS/PaaS 모듈이 상위 시스템을 라이브 조회.

실행(로컬):
  pip install -r requirements.txt
  cp config.env.example config.env   # 값 수정
  uvicorn app.main:app --reload --port 8000
  (TEMPORAL_API_BASE 가 있으면 Temporal 워커(VM 프로비저닝 워크플로 + DB 기록)도 함께 시작)
"""
import asyncio
import logging
from contextlib import asynccontextmanager, suppress

from fastapi import Depends, FastAPI, Request
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse
from sqlalchemy import text

from .auth import AuthError, current_user
from .config import settings
from .db import engine
from .routers import accel_models, auth, cluster_access, codes, members, menus, ssh_keys, tickets
from .workflows.runtime import run_worker


class SkipSessionPoll(logging.Filter):
    """화면이 5초마다 부르는 세션 상태 확인(/auth/session)은 접근 로그에서 뺀다 — 다른 요청 로그가 묻히지 않게."""

    def filter(self, record: logging.LogRecord) -> bool:
        args = record.args  # uvicorn 접근 로그: (클라이언트, 메서드, 경로, HTTP 버전, 상태)
        return not (isinstance(args, tuple) and len(args) >= 3
                    and str(args[2]).startswith(f"{settings.api_prefix}/auth/session"))


logging.getLogger("uvicorn.access").addFilter(SkipSessionPoll())


@asynccontextmanager
async def lifespan(_app: FastAPI):
    worker = asyncio.create_task(run_worker()) if settings.temporal_api_base else None
    yield
    if worker:
        worker.cancel()
        with suppress(asyncio.CancelledError):
            await worker


app = FastAPI(title="MNT-CORE", version="0.1.0", lifespan=lifespan)

if settings.cors_origins:
    app.add_middleware(
        CORSMiddleware,
        allow_origins=[o.strip() for o in settings.cors_origins.split(",") if o.strip()],
        allow_credentials=True,
        allow_methods=["*"],
        allow_headers=["*"],
    )


@app.exception_handler(AuthError)
async def auth_error(_request: Request, e: AuthError):
    """인증·권한 오류 — detail 은 화면 표시 문구(기존 형식), code·reason 으로 화면이 원인별 안내를 고른다."""
    headers = {"WWW-Authenticate": "Bearer"} if e.status == 401 else None
    return JSONResponse({"detail": e.detail, "code": e.code, **e.extra}, status_code=e.status, headers=headers)


@app.get("/healthz")
def healthz():
    """liveness/readiness — DB 연결까지 확인한다."""
    try:
        with engine.connect() as conn:
            conn.execute(text("SELECT 1"))
        db_status = "ok"
    except Exception as e:  # noqa: BLE001 — 상태 보고 목적
        db_status = f"error: {type(e).__name__}"
    return {"service": settings.service_name, "status": "ok", "db": db_status}


# 로그인(토큰) 필수 — 관리자 전용 등 세부 권한은 각 라우터에서. auth 라우터는 경로마다 따로 확인한다.
_login = [Depends(current_user)]
app.include_router(menus.router, prefix=settings.api_prefix, dependencies=_login)
app.include_router(codes.router, prefix=settings.api_prefix, dependencies=_login)
app.include_router(ssh_keys.router, prefix=settings.api_prefix, dependencies=_login)
app.include_router(members.router, prefix=settings.api_prefix, dependencies=_login)
app.include_router(cluster_access.router, prefix=settings.api_prefix, dependencies=_login)
app.include_router(accel_models.router, prefix=settings.api_prefix, dependencies=_login)
app.include_router(auth.router, prefix=settings.api_prefix)
app.include_router(tickets.router, prefix=settings.api_prefix, dependencies=_login)
