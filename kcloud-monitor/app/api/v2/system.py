"""서비스 자체 상태 조회 라우터

인증 없이 접근 가능. Kubernetes probe와 배포 파이프라인이 사용.
"""
from datetime import datetime, timezone

from fastapi import APIRouter, Request, Response

from app.middleware import get_metrics_content_type, get_metrics_text
from app.services.prometheus import prometheus_client

router = APIRouter()


@router.get("/system/health", summary="헬스체크")
async def get_health(request: Request):
    """API 서버와 데이터소스 연결 상태 확인

    입력 예시

    * `GET /api/v2/system/health`

    응답

    * `status`: 서버 상태 `healthy` | `degraded`
    * `backends.prometheus`: Prometheus 연결 상태 `connected` | `unreachable`
    * `observed_at`: 확인 시각 (ISO 8601, UTC)

    상태

    * `healthy`: 서버와 Prometheus 모두 정상
    * `degraded`: 서버는 동작 중이나 Prometheus에 연결되지 않음

    참고

    * 인증 없이 호출 가능
    """
    prometheus_ok = await prometheus_client.ping()
    return {
        # 앱은 살아 있으나 데이터소스 미도달이면 degraded로 강등
        "status": "healthy" if prometheus_ok else "degraded",
        "backends": {
            "prometheus": "connected" if prometheus_ok else "unreachable",
        },
        "observed_at": datetime.now(timezone.utc).isoformat(),
    }


@router.get("/system/version", summary="버전 정보")
async def get_version(request: Request):
    """배포된 서비스와 API 버전 조회

    입력 예시

    * `GET /api/v2/system/version`

    응답

    * `status`: 처리 결과
    * `service`: 서비스 이름
    * `version`: 서비스 버전
    * `api_version`: API 버전
    * `observed_at`: 응답 생성 시각 (ISO 8601, UTC)

    참고

    * 인증 없이 호출 가능
    """
    return {
        "status": "success",
        "service": "kcloud-monitor",
        "version": request.app.version,
        "api_version": "v2",
        "observed_at": datetime.now(timezone.utc).isoformat(),
    }


@router.get("/system/metrics", summary="API 서버 자체 메트릭")
async def get_self_metrics():
    """API 서버가 처리한 요청 수와 응답 지연 메트릭 조회

    입력 예시

    * `GET /api/v2/system/metrics`

    응답

    * Prometheus 텍스트 형식 (JSON 아님)
    * 경로별 요청 수, 상태 코드별 요청 수, 응답 지연 분포

    참고

    * 인증 없이 호출 가능
    """
    return Response(content=get_metrics_text(), media_type=get_metrics_content_type())
