from fastapi import FastAPI, Request, status, WebSocket, Query, Depends
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse
from fastapi.exceptions import RequestValidationError
from fastapi.security import HTTPBearer
from contextlib import asynccontextmanager
from typing import Optional
import asyncio

from app.api.v1 import accelerators, infrastructure, hardware, clusters, monitoring, export, system, auth
from app.api import system as legacy_system, power as legacy_power, cluster as legacy_cluster, gpu as legacy_gpu
from app.models.responses import ErrorResponse, ErrorDetail
from app.services.prometheus import PrometheusException
from app.services.stream import power_stream_handler, metrics_stream_handler
from app.middleware import MetricsMiddleware, RequestIDMiddleware, RateLimitMiddleware
from app.auth import verify_token, verify_token_or_api_key
from app.config import settings
from app.logging_config import configure_logging
from app.services.warmup import warmup_cache

@asynccontextmanager
async def lifespan(app: FastAPI):
    configure_logging(settings.LOG_LEVEL)
    print("AI Accelerator & Infrastructure Monitoring API - Starting up")
    print("API Version: 0.1.0")
    print("Metrics middleware enabled - Prometheus metrics available at /api/v1/system/metrics")
    # Non-blocking cache warmup (Phase 11.1); failures never affect startup.
    app.state.warmup_task = asyncio.create_task(warmup_cache())
    yield
    print("Application shutdown")

API_DESCRIPTION = """
**AI Accelerator & Infrastructure Monitoring API** — 7-domain (Accelerators, Infrastructure, Hardware, Clusters, Monitoring, Export, System).

## 응답 정책 (design_contracts §6)
- `status`: `success` | `partial` | `error`. `partial`은 `warnings[]`·`partial_sources[]` 포함.
- 모든 응답에 `observed_at`, `is_stale`(지연 시 `STALE_DATA`), `request_id` 및 `X-Request-ID` 헤더.
- 에러 스키마: `{status, error:{code, message, retryable}, request_id, observed_at}`.

## 성능 목표 (NFR, design_contracts §2)
- 일반 조회 P95 ≤ 2초, 무거운 집계(토폴로지/대형 summary) P95 ≤ 5초.
- SSE 첫 이벤트 ≤ 5초(heartbeat 또는 데이터), 이후 15초 heartbeat. 실시간 메트릭 지연 ≤ 60초.

## 인증
- JWT Bearer (`POST /api/v1/auth/login`). System 도메인 헬스/메트릭은 공개.
"""

app = FastAPI(
    title="AI Accelerator & Infrastructure Monitoring API",
    description=API_DESCRIPTION,
    version="0.1.0",
    lifespan=lifespan
)

# ============================================================================
# Middleware
# ============================================================================

# CORS middleware - origins configurable for production via CORS_ALLOW_ORIGINS (default "*")
cors_allow_origins = [o.strip() for o in settings.CORS_ALLOW_ORIGINS.split(",") if o.strip()]
app.add_middleware(
    CORSMiddleware,
    allow_origins=cors_allow_origins,
    allow_credentials=True,
    allow_methods=["*"],  # Allow all methods (GET, POST, etc.)
    allow_headers=["*"],  # Allow all headers
)

# Add metrics middleware for request tracking
app.add_middleware(MetricsMiddleware)

# Rate limiting (Phase 11.2) - no-op unless RATE_LIMIT_ENABLED is set
app.add_middleware(RateLimitMiddleware, limit_per_minute=settings.RATE_LIMIT_PER_MINUTE)

# Request ID middleware (outermost) - correlation id on every request/response
app.add_middleware(RequestIDMiddleware)

# ============================================================================
# Exception Handlers
# ============================================================================

@app.exception_handler(PrometheusException)
async def prometheus_exception_handler(request: Request, exc: PrometheusException):
    return JSONResponse(
        status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
        content=ErrorResponse(error=ErrorDetail(code="PROMETHEUS_ERROR", message=str(exc))).model_dump(mode='json')
    )

@app.exception_handler(RequestValidationError)
async def validation_exception_handler(request: Request, exc: RequestValidationError):
    return JSONResponse(
        status_code=status.HTTP_422_UNPROCESSABLE_ENTITY,
        content=ErrorResponse(error=ErrorDetail(code="VALIDATION_ERROR", message=str(exc))).model_dump(mode='json')
    )

# ============================================================================
# API v1 Routers (7-Domain Architecture)
# ============================================================================

# 0. Authentication - Login and token management (No auth required)
app.include_router(auth.router, prefix="/api/v1", tags=["Authentication"])

# 1. Accelerators - GPU and NPU monitoring
app.include_router(accelerators.router, prefix="/api/v1", tags=["Accelerators"], dependencies=[Depends(verify_token_or_api_key)])

# 2. Infrastructure - Nodes, Pods, Containers, VMs
app.include_router(infrastructure.router, prefix="/api/v1", tags=["Infrastructure"], dependencies=[Depends(verify_token_or_api_key)])

# 3. Hardware - Physical hardware (IPMI)
app.include_router(hardware.router, prefix="/api/v1", tags=["Hardware"], dependencies=[Depends(verify_token_or_api_key)])

# 4. Clusters - Multi-cluster management
app.include_router(clusters.router, prefix="/api/v1", tags=["Clusters"], dependencies=[Depends(verify_token_or_api_key)])

# 5. Monitoring - Cross-domain monitoring (power, timeseries, streaming)
app.include_router(monitoring.router, prefix="/api/v1", tags=["Monitoring"], dependencies=[Depends(verify_token_or_api_key)])

# 6. Export - Data export and reporting
app.include_router(export.router, prefix="/api/v1", tags=["Export"], dependencies=[Depends(verify_token_or_api_key)])

# 7. System - Health, info, capabilities (public endpoints for health/metrics)
app.include_router(system.router, prefix="/api/v1", tags=["System"])

# ============================================================================
# Legacy API Routers (Backward Compatibility - Deprecated)
# ============================================================================
# These will be removed in a future version. Use v1 routes instead.

app.include_router(legacy_system.router, prefix="/api/v1", tags=["Legacy-System"], dependencies=[Depends(verify_token_or_api_key)])
app.include_router(legacy_power.router, prefix="/api/v1", tags=["Legacy-Power"], dependencies=[Depends(verify_token_or_api_key)])
app.include_router(legacy_cluster.router, prefix="/api/v1", tags=["Legacy-Cluster"], dependencies=[Depends(verify_token_or_api_key)])
app.include_router(legacy_gpu.router, prefix="/api/v1", tags=["Legacy-GPU"], dependencies=[Depends(verify_token_or_api_key)])

# ============================================================================
# WebSocket Endpoints (Phase 7.3)
# ============================================================================

@app.websocket("/api/v1/monitoring/stream/power")
async def websocket_power_stream(
    websocket: WebSocket,
    cluster: Optional[str] = Query(None),
    resource_type: Optional[str] = Query(None),
    interval: int = Query(5, ge=1, le=60)
):
    """
    WebSocket endpoint for real-time power consumption data.

    Query Parameters:
    - cluster: Cluster name filter
    - resource_type: Resource type filter (accelerators, infrastructure)
    - interval: Update interval in seconds (1-60, default: 5)

    Connection Example:
    ```javascript
    const ws = new WebSocket('ws://localhost:8000/api/v1/monitoring/stream/power?interval=5');
    ws.onmessage = (event) => {
      const data = JSON.parse(event.data);
      console.log('Power update:', data);
    };
    ```
    """
    await power_stream_handler(websocket, cluster, resource_type, interval)


@app.websocket("/api/v1/monitoring/stream/metrics")
async def websocket_metrics_stream(
    websocket: WebSocket,
    metric_name: str = Query("utilization"),
    resource_type: Optional[str] = Query(None),
    interval: int = Query(5, ge=1, le=60)
):
    """
    WebSocket endpoint for real-time performance metrics.

    Query Parameters:
    - metric_name: Metric name (utilization, temperature, memory_usage)
    - resource_type: Resource type filter (gpus, npus, nodes)
    - interval: Update interval in seconds (1-60, default: 5)

    Connection Example:
    ```javascript
    const ws = new WebSocket('ws://localhost:8000/api/v1/monitoring/stream/metrics?metric_name=utilization&resource_type=gpus');
    ws.onmessage = (event) => {
      const data = JSON.parse(event.data);
      console.log('Metrics update:', data);
    };
    ```
    """
    await metrics_stream_handler(websocket, metric_name, resource_type, interval)


# ============================================================================
# Root Endpoint
# ============================================================================

@app.get("/")
def read_root():
    return {
        "message": "AI Accelerator & Infrastructure Monitoring API",
        "version": "0.1.0",
        "architecture": "7-Domain Structure",
        "docs": "/docs",
        "authentication": {
            "login": "POST /api/v1/auth/login",
            "description": "Get JWT token with username and password"
        },
        "domains": {
            "accelerators": "/api/v1/accelerators/* (requires auth)",
            "infrastructure": "/api/v1/infrastructure/* (requires auth)",
            "hardware": "/api/v1/hardware/* (requires auth)",
            "clusters": "/api/v1/clusters/* (requires auth)",
            "monitoring": "/api/v1/monitoring/* (requires auth)",
            "export": "/api/v1/export/* (requires auth)",
            "system": "/api/v1/system/* (requires auth)"
        },
        "legacy_endpoints": {
            "power": "/api/v1/power/* (deprecated, requires auth)",
            "gpu": "/api/v1/gpu/* (deprecated, requires auth)",
            "cluster": "/api/v1/cluster/* (deprecated, requires auth)"
        }
    }
