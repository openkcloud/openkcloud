"""MNT-OBS — 스캐폴드. 비즈니스 로직은 추후 구현.

실행(로컬): uvicorn app.main:app --reload --port 8000
"""
from fastapi import APIRouter, FastAPI
from fastapi.middleware.cors import CORSMiddleware

from .config import settings
from .routers import monitoring

app = FastAPI(title="MNT-OBS", version="0.1.0")

if settings.cors_origins:
    app.add_middleware(
        CORSMiddleware,
        allow_origins=[o.strip() for o in settings.cors_origins.split(",") if o.strip()],
        allow_credentials=True,
        allow_methods=["*"],
        allow_headers=["*"],
    )


@app.get("/healthz")
def healthz():
    return {"service": settings.service_name, "status": "ok"}


router = APIRouter()


@router.get("/")
def root():
    return {"service": settings.service_name, "message": "MNT-OBS"}


app.include_router(router, prefix=settings.api_prefix)
app.include_router(monitoring.router, prefix=settings.api_prefix)
