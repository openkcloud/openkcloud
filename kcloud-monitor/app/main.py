"""KCloud Monitor API v2"""
import asyncio
from contextlib import asynccontextmanager
from datetime import datetime, timezone

from fastapi import Depends, FastAPI, Request, status
from fastapi.exceptions import RequestValidationError
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse

from app.api.v2 import (
    accelerators,
    alerts,
    auth,
    clusters,
    collection,
    export,
    faults,
    logs,
    metering,
    monitoring,
    nodes,
    openstack,
    recovery,
    recovery_policies,
    resource_map,
    slo,
    storage,
    system,
    workloads,
    workloads_global,
)
from app.auth import verify_token_or_api_key
from app.config import settings
from app.logging_config import configure_logging

from app.services import alerts as alert_service
from app.services import collection as collection_service
from app.services import metering as metering_service
from app.services import recovery_policies as recovery_policy_service
from app.middleware import MetricsMiddleware, RateLimitMiddleware, RequestIDMiddleware

APP_VERSION = "0.2.0"


@asynccontextmanager
async def lifespan(app: FastAPI):
    configure_logging(settings.LOG_LEVEL)
    print("KCloud Monitor API v2 - Starting up")
    print(f"Version: {APP_VERSION} | Docs: /docs | Metrics: /api/v2/system/metrics")
    loop_task = meter_task = collection_task = None
    if settings.DATABASE_URL:
        try:
            await alert_service.store.connect()
            loop_task = asyncio.create_task(alert_service.run_loop())
        except Exception as exc:  # DB 가 아직 안 떠도 조회 API 는 살아야 함
            print(f"alert store unavailable: {exc}")
        if alert_service.store.ready:
            try:
                await collection_service.store.connect(alert_service.store.pool)
                collection_task = asyncio.create_task(collection_service.run_loop())
            except Exception as exc:  # 수집 정책 테이블을 못 만들어도 나머지 API 는 살아야 함
                print(f"collection store unavailable: {exc}")
        try:
            await metering_service.store.connect()
            meter_task = asyncio.create_task(metering_service.run_loop())
        except Exception as exc:
            print(f"metering store unavailable: {exc}")
        try:
            await recovery_policy_service.store.connect()
        except Exception as exc:
            print(f"recovery policy store unavailable: {exc}")
    else:
        print("DATABASE_URL 미설정: 알람, 미터링 계측, 단가 정책, 예산, 후속 조치 정책 수정, 수집 정책 관리 기능 비활성")
    yield
    for task in (loop_task, meter_task, collection_task):
        if task:
            task.cancel()
    collection_service.store.close()
    await alert_service.store.close()
    await metering_service.store.close()
    await recovery_policy_service.store.close()
    print("Application shutdown")


API_DESCRIPTION = """
이기종 AI 반도체(NVIDIA GPU, Furiosa NPU, Rebellions NPU) 인프라의 자원, 전력, 워크로드 모니터링 API.

## 인증
- `POST /api/v2/auth/login` 에 아이디와 비밀번호(JSON)를 보내 JWT 발급. 응답의 `access_token` 을 `Authorization: Bearer <토큰>` 헤더로 전달
- 토큰 유효 시간 : 응답의 `expires_in`(초). 기본 3600초
- 서버 간 호출 : 서버에 API 키가 설정된 경우 `X-API-Key` 헤더 사용 가능
- 인증 없이 접근 가능한 경로 : `/api/v2/auth/*`, `/api/v2/system/*`

## 공통 응답
- `status` : 처리 결과 (success | partial | error | not_implemented). Pod 추적(Resource Map)만 ambiguous(같은 이름 Pod 여러 개) 추가
- `partial` : 일부 데이터가 비었거나 근사치. 원인은 `warnings` 코드로 안내
- `not_implemented` : 데이터 미수집 기능. `data` 는 null
- `observed_at` : 응답 생성 시각 (ISO 8601, UTC)
- `warnings` : 원인 코드 목록. 정상이면 빈 목록
- 목록 응답 : `total` 은 필터 적용 후 전체 개수, `limit` 과 `offset` 으로 페이지 이동. 로그는 `pagination.next_cursor` 를 다음 요청의 `cursor` 로 넘겨 이어 받기
- `_links.self`, `_links.canonical` : 짧은 경로나 클러스터 지정 경로로 조회한 응답에 포함. 이번 요청 경로와 클러스터를 포함한 정식 경로
- 모든 응답 헤더에 `X-Request-ID` 포함. 문의 시 이 값 전달

## 에러 응답
- 입력 검증 실패(422) : `{status: "error", error: {code: "VALIDATION_ERROR", message, retryable}, request_id, observed_at}`
- 요청 한도 초과(429) : `{status: "error", error: {code: "RATE_LIMITED", message, retryable}}`, `Retry-After` 헤더 포함
- 인증 실패(401), 대상 없음(404), 저장소 미설정이나 조회 실패(503) : `{detail: "<사유>"}`
- 요청 한도 기능이 켜진 경우 모든 응답 헤더에 `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset` 포함

## 경고 코드
데이터 없음
- `NO_DATA` : 조회 대상 메트릭이나 자원 정보 없음
- `NO_DATA_SERVER_POWER`, `NO_DATA_CPU_POWER`, `NO_DATA_ACCELERATOR` : 서버 전원 장치(IPMI) 전력, CPU 전력, 가속기 전력 없음
- `NO_DATA_POWER`, `NO_DATA_UTILIZATION`, `NO_DATA_TEMPERATURE` : 가속기 목록의 전력, 사용률, 온도 없음
- `NO_DATA_PUE` : PUE 계산에 필요한 전력 없음
- `NO_DATA_NVIDIA`, `NO_DATA_FURIOSA`, `NO_DATA_REBELLIONS` : 해당 벤더의 온도 시계열 없음
- `NO_DATA_LIBVIRT` : VM 가상화 계층(libvirt) 사용량 없음
- `NO_POWER_DATA` : 노드, Pod, 서비스 전력 없음
- `NO_HOST` : VM이 배치된 물리 서버 미확인
- `METRIC_NOT_FOUND` : 수집 정책에서 빼려는 메트릭이 그 수집 대상에 없음
- `NO_LOG_SOURCE`, `NO_NODE_LOGS`, `NO_CLUSTER_LOGS` : 가속기 드라이버 로그, 노드 로그, 클러스터 로그 없음

외부 연동
- `NOT_CONFIGURED` : 외부 시스템 접속 정보나 생성 기능 미설정 (OpenStack, 리포트 생성)
- `UPSTREAM_ERROR` : 외부 시스템 호출 실패
- `LOKI_UNAVAILABLE` : 로그 저장소(Loki) 호출 실패
- `IPMI_NOT_AVAILABLE` : 서버 하드웨어 센서(IPMI) 값 없음
- `TEMPERATURE_UNAVAILABLE` : 온도 메트릭을 제공하지 않는 클러스터
- `TOPOLOGY_NOT_AVAILABLE` : 카드 간 연결 정보 미제공 (NVIDIA 외 클러스터)
- `PARTITION_DATA_NOT_AVAILABLE` : 가속기 파티션 정보 미수집
- `POD_ACCELERATOR_DATA_NOT_AVAILABLE` : Pod 가속기 배정 정보 없음 (NVIDIA 외 클러스터 또는 배정 카드 없음)
- `CLUSTER_LABEL_NOT_FOUND` : 지정 클러스터 이름으로 찾은 로그 없음

근사값과 보정값
- `TDP_UNVERIFIED` : 가속기 정격 전력(TDP) 미확인으로 정격 대비 비율 미계산
- `PUE_COOLING_UNMEASURED_ESTIMATE` : 냉방 전력을 포함하지 않은 PUE 근사치
- `POWER_ATTRIBUTION_CPU_PROPORTIONAL` : 서버 전력을 CPU 사용 비율로 나눈 VM 전력 추정치
- `OTHER_WATTS_NEGATIVE_CLAMPED` : 기타 전력 계산값이 음수라 0으로 보정
- `REBELLIONS_SCALE_CORRECTED_X1000` : Rebellions 온도 원본 값을 1000배로 보정
- `NODE_CPU_TOTAL_ZERO` : 같은 서버 VM 전체 CPU 사용량이 0이라 비율 계산 불가

미터링, 비용 예측
- `PRICE_NOT_DEFINED` : 단가 정책이 없는 가속기. 그 비용은 합계에서 빠짐
- `PARTIAL_HISTORY` : 조회 구간 일부를 계측하지 못함. 예측에서는 일평균으로 채움
- `INSUFFICIENT_HISTORY` : 완결된 날이 없어 예측 불가
- `TREND_FALLBACK_MOVING_AVERAGE` : 추세 계산에 쓸 날이 부족해 이동 평균으로 계산
- `NO_PROJECT_MAPPING` : 클러스터와 OpenStack 프로젝트 매핑 없음
- `NO_BUDGET` : 적용되는 예산 없음
- `METERING_STORE_UNAVAILABLE` : 예산 조회나 예측 이력 저장 실패

입력과 대상
- `UNKNOWN_CLUSTER` : 없는 클러스터 이름
- `ACCELERATOR_NOT_FOUND` : 없는 가속기
- `NOT_FOUND` : 없는 OpenStack 자원 (VM, 프로젝트, 물리 서버)
- `UNKNOWN_DIMENSION` : 지원하지 않는 집계 기준
- `MULTIPLE_PODS_MATCHED` : 같은 이름 Pod 여러 개
- `INVALID_UUID` : VM ID 형식 오류

미지원
- `NOT_IMPLEMENTED` : 데이터 미수집 기능

## 시각과 단위
- 응답 시각 : ISO 8601, UTC (예: 2026-10-06T01:23:45.123456+00:00). 메트릭 즉시 조회 결과의 value 만 Unix 시각(초)
- 입력 시각(start, end) : ISO 8601. 시간대 포함 권장
- 시계열 : `[시각, 값]` 쌍 목록. 값은 숫자 문자열
- 단위 : 필드 이름 끝과 설명 괄호로 표시. `_watts`(W), `_percent` `_pct`(%), `_celsius`(°C), `_bytes`(bytes), `_ms`(ms), CPU 사용량(코어)
- 값을 구하지 못한 숫자 필드는 null

## 실시간 스트림(SSE)
- 모든 스트림 공통 `heartbeat` 이벤트(약 15초 간격)와 `error` 이벤트 사용
- 데이터 이벤트 이름 : 전력 `power`, 메트릭 `metric`, 로그 `log`

## 알람
- `/alerts` 경로는 서버에 알람 저장소(DB) 설정이 없으면 모두 503, `detail` 에 ALERT_STORE_NOT_CONFIGURED

## 미터링
- 서버가 매시 가속기 카드별 점유, 사용, 가중 시간과 전력량을 계측해 사용량 레코드로 쌓음. 레코드는 적재 후 바뀌지 않음
- 비용 = 사용량 x 그날 유효 단가. 단가는 덮어쓰지 않고 새 버전을 추가하며 소급 적용 불가(409)
- 금액은 원(`_krw`), 과금 월과 일의 경계는 한국 시간(KST). 지금은 가속기 비용만 포함
- `/metering/usage/*`, `/metering/policies`(자원 유형 목록 제외), `/metering/budgets`, `/metering/predictions/budget-status` 는
  저장소(DB) 설정이 없으면 503, `detail` 에 METERING_STORE_NOT_CONFIGURED

## 수집 정책
- `/monitoring/collection/policies` 경로는 서버에 저장소(DB) 설정이 없으면 모두 503, `detail` 에 COLLECTION_STORE_NOT_CONFIGURED
- 정책 저장과 미리보기는 실제 수집 설정을 바꾸지 않음
- 적용(`apply`)은 서버의 허용 목록에 있는 수집 대상만 가능. 적용 후 정해진 시간 안에 확정(`confirm`)하지 않거나 수집이 줄면 자동으로 원래 설정 복구
- 적용, 확정, 되돌리기는 Kubernetes 접속 정보가 없으면 503, `detail` 에 K8S_NOT_CONFIGURED

## 태그 구성
- Authentication : 토큰 발급과 검증
- System : 서비스 헬스, 버전, 서버 자체 메트릭
- Clusters : 클러스터 목록, 상세, 자원 합계, 구성도, 전력
- Nodes : 노드 목록, 상세, CPU, 메모리, 디스크, 네트워크, 전력, 하드웨어 센서
- Accelerators : 가속기 목록, 상세, 실시간 메트릭, 전력, 온도, 파티션
- Workloads : 클러스터를 지정한 Pod, 컨테이너, 네임스페이스 조회
- Workloads Global : 클러스터 구분 없이 전체 Pod, 서비스 조회
- Monitoring : 전체 현황, 전력 요약과 효율, 사용률, 쓰로틀링, 메트릭 시계열, 실시간 스트림(SSE), 수집 대상별 수집 주기와 저장량, 수집 정책
- Storage : Ceph 상태, 용량, OSD, 풀, PG
- OpenStack : 물리 서버(하이퍼바이저), VM, 프로젝트, VM 사용량과 추정 전력
- Logs : Loki 기반 로그 검색, 라벨, 실시간 스트리밍
- Export : 전력, 메트릭, 리포트를 CSV 또는 JSON으로 내보내기
- Resource Map : Pod 하나가 올라간 노드, VM, 물리 서버 추적
- Alerts : 알람 정책, 알림 채널, 활성 알람, 이력
- SLO : LLM 서빙 응답 속도(TTFT, TPOT) 목표 달성률, 오류 예산, 시계열
- Metering : 사용량(점유, 사용, 가중 시간, 전력량, 비용), 단가 정책, 월말 비용 예측, 예산 대비 현황, 가상 시나리오, 예산
- Faults : 진행 중 장애, 장애 이력, 장애별 증거(메트릭, 로그, 영향 자원)와 원인 분석
- Recovery Policies : 장애 유형별 후속 조치 정책(조치 절차) 설정과 관리. 권고 전용, 자동 실행 없음
- Recovery : 진행 중 장애별 복구 정보 생성 (조치 절차, 추정 원인, 영향 범위, 확인 방법)
"""

app = FastAPI(
    title="KCloud Monitor API",
    description=API_DESCRIPTION,
    version=APP_VERSION,
    lifespan=lifespan,
)

# ============================================================================
# Middleware
# ============================================================================


cors_allow_origins = [o.strip() for o in settings.CORS_ALLOW_ORIGINS.split(",") if o.strip()]
app.add_middleware(
    CORSMiddleware,
    allow_origins=cors_allow_origins,
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)
app.add_middleware(MetricsMiddleware)
app.add_middleware(RateLimitMiddleware, limit_per_minute=settings.RATE_LIMIT_PER_MINUTE)
app.add_middleware(RequestIDMiddleware)

# ============================================================================
# Exception Handlers
# ============================================================================



def _error_body(request: Request, code: str, message: str, retryable: bool = False) -> dict:
    return {
        "status": "error",
        "error": {"code": code, "message": message, "retryable": retryable},
        "request_id": getattr(request.state, "request_id", None),
        "observed_at": datetime.now(timezone.utc).isoformat(),
    }


@app.exception_handler(RequestValidationError)
async def validation_exception_handler(request: Request, exc: RequestValidationError):
    return JSONResponse(
        status_code=status.HTTP_422_UNPROCESSABLE_ENTITY,
        content=_error_body(request, "VALIDATION_ERROR", str(exc)),
    )


# ============================================================================
# API v2 Routers
# ============================================================================

PROTECTED = [Depends(verify_token_or_api_key)]
V2 = "/api/v2"

app.include_router(auth.router, prefix=V2, tags=["Authentication"])
app.include_router(system.router, prefix=V2, tags=["System"])
app.include_router(clusters.router, prefix=V2, tags=["Clusters"], dependencies=PROTECTED)
app.include_router(nodes.router, prefix=V2, tags=["Nodes"], dependencies=PROTECTED)
app.include_router(accelerators.router, prefix=V2, tags=["Accelerators"], dependencies=PROTECTED)
app.include_router(storage.router, prefix=V2, tags=["Storage"], dependencies=PROTECTED)
app.include_router(openstack.router, prefix=V2, tags=["OpenStack"], dependencies=PROTECTED)
app.include_router(workloads.router, prefix=V2, tags=["Workloads"], dependencies=PROTECTED)
app.include_router(workloads_global.router, prefix=V2, tags=["Workloads Global"], dependencies=PROTECTED)
app.include_router(monitoring.router, prefix=V2, tags=["Monitoring"], dependencies=PROTECTED)
app.include_router(logs.router, prefix=V2, tags=["Logs"], dependencies=PROTECTED)
app.include_router(export.router, prefix=V2, tags=["Export"], dependencies=PROTECTED)
app.include_router(resource_map.router, prefix=V2, tags=["Resource Map"], dependencies=PROTECTED)
app.include_router(alerts.router, prefix=V2, tags=["Alerts"], dependencies=PROTECTED)
app.include_router(slo.router, prefix=V2, tags=["SLO"], dependencies=PROTECTED)
app.include_router(metering.router, prefix=V2, tags=["Metering"], dependencies=PROTECTED)
app.include_router(faults.router, prefix=V2, tags=["Faults"], dependencies=PROTECTED)
app.include_router(recovery_policies.router, prefix=V2, tags=["Recovery Policies"], dependencies=PROTECTED)
app.include_router(recovery.router, prefix=V2, tags=["Recovery"], dependencies=PROTECTED)
app.include_router(collection.router, prefix=V2, tags=["Monitoring"], dependencies=PROTECTED)

# ============================================================================
# Root
# ============================================================================


@app.get("/")
def read_root():
    """API 진입점과 주요 경로 안내

    입력 예시

    * `GET /`

    응답

    * `service`, `version`: 서비스 이름과 버전
    * `docs`: Swagger 문서 경로
    * `endpoints`: 도메인별 대표 경로와 한 줄 설명
    """
    return {
        "service": "KCloud Monitor API",
        "version": APP_VERSION,
        "docs": "/docs",
        "endpoints": {
            "auth": "POST /api/v2/auth/login - 토큰 발급",
            "clusters": "GET /api/v2/clusters - 클러스터 목록과 상태",
            "nodes": "GET /api/v2/clusters/{cluster}/nodes - 노드 자원 정보",
            "accelerators": "GET /api/v2/clusters/{cluster}/nodes/{node}/accelerators - 가속기 상태",
            "workloads": "GET /api/v2/workloads/pods - 전체 Pod 목록",
            "monitoring": "GET /api/v2/monitoring/overview - 전체 시스템 현황",
            "storage": "GET /api/v2/storage/ceph/summary - Ceph 스토리지 상태",
            "openstack": "GET /api/v2/openstack/vms - OpenStack VM 목록과 집계",
            "logs": "GET /api/v2/logs/search - 로그 검색",
            "export": "GET /api/v2/export/power - 데이터 내보내기",
            "resource_map": "GET /api/v2/resource-map/pods/{pod} - Pod에서 물리서버까지 자원 추적",
            "metering": "GET /api/v2/metering/usage/summary - 사용량과 비용, /metering/predictions/monthly - 월말 비용 예측",
            "faults": "GET /api/v2/faults/active - 진행 중 장애, /faults/{fault_id} - 증거와 원인 분석",
            "recovery": "GET /api/v2/recovery/recommendations - 장애별 복구 정보, /recovery/policies - 후속 조치 정책",
            "system": "GET /api/v2/system/health - 서비스 헬스체크",
        },
    }
