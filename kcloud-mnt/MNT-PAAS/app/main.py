"""MNT-PAAS — Magnum(서비스 클러스터)/K8s 중개.

실행(로컬): uvicorn app.main:app --reload --port 8002
  (TEMPORAL_API_BASE 가 있으면 Temporal 워커(클러스터 프로비저닝의 Magnum·K8s 작업)도 함께 시작)
"""
import asyncio
from contextlib import asynccontextmanager, suppress

from fastapi import APIRouter, Depends, FastAPI, Request
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse

from .auth import AuthError, caller, cluster_member
from .config import settings
from .routers import clusters, k8s, templates
from .workflows.runtime import run_worker


@asynccontextmanager
async def lifespan(_app: FastAPI):
    worker = asyncio.create_task(run_worker()) if settings.temporal_api_base else None
    yield
    if worker:
        worker.cancel()
        with suppress(asyncio.CancelledError):
            await worker


app = FastAPI(title="MNT-PAAS", version="0.1.0", lifespan=lifespan)

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
    """인증·권한 오류 — CORE 와 같은 형식(detail 은 화면 표시 문구, code·reason 은 원인)."""
    headers = {"WWW-Authenticate": "Bearer"} if e.status == 401 else None
    return JSONResponse({"detail": e.detail, "code": e.code, **e.extra}, status_code=e.status, headers=headers)


@app.get("/healthz")
def healthz():
    return {"service": settings.service_name, "status": "ok"}


router = APIRouter()


@router.get("/")
def root():
    return {"service": settings.service_name, "message": "MNT-PAAS"}


app.include_router(router, prefix=settings.api_prefix)
# 로그인(토큰) 필수 — 관리자 전용·클러스터 접근 확인은 각 라우터에서. k8s 는 전부 /clusters/{cluster_id}/... 라 라우터 전체에
app.include_router(clusters.router, prefix=settings.api_prefix, dependencies=[Depends(caller)])
app.include_router(k8s.router, prefix=settings.api_prefix, dependencies=[Depends(cluster_member)])
app.include_router(templates.router, prefix=settings.api_prefix, dependencies=[Depends(caller)])
