"""
KCloud Monitor v2 설정 (.env 또는 환경변수).
"""
from typing import Optional

from pydantic import Field
from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_file=".env", env_file_encoding="utf-8", extra="ignore")

    # ── 인증 (API Gateway 도입 전 개발용 단일 계정 + API Key) ─────────────
    API_AUTH_USERNAME: str = Field("admin", description="API username")
    API_AUTH_PASSWORD: str = Field("changeme", description="API password")
    JWT_SECRET_KEY: str = Field("change-this-secret-key", description="JWT signing secret")
    JWT_ALGORITHM: str = Field("HS256", description="JWT algorithm")
    JWT_ACCESS_TOKEN_EXPIRE_MINUTES: int = Field(60, description="JWT expiration (minutes)")
    API_KEY: Optional[str] = Field(
        None, description="X-API-Key 병행 인증 키. 미설정 시 JWT만 허용"
    )

    # ── 운영 ──────────────────────────────────────────────────────────────
    CORS_ALLOW_ORIGINS: str = Field("*", description="허용 오리진(콤마 구분) 또는 *")
    RATE_LIMIT_ENABLED: bool = Field(False, description="레이트리밋 활성화")
    RATE_LIMIT_PER_MINUTE: int = Field(120, ge=1, description="클라이언트당 분당 요청 수")
    LOG_LEVEL: str = Field("INFO", description="로그 레벨")

    # ── v2 데이터소스 ─────────────────────────────────────────────────────
    PROMETHEUS_URL: Optional[str] = Field("http://localhost:9090", description="Prometheus HTTP API URL",)
    LOKI_URL: Optional[str] = Field(None, description="Loki HTTP API URL")
    DATABASE_URL: Optional[str] = Field(
        None, description="PostgreSQL — 알람 저장소(정책/채널/활성 알람/이력), resource-map 원장(예정)"
    )
    # ── 수집 관리 ─────────────────────────────────────────────────────────
    COLLECTION_MANAGED_TARGETS: str = Field(
        "", description="API 로 수집 주기와 필터를 바꿔도 되는 수집 대상 ID(콤마 구분). 비우면 전부 잠금"
    )
    COLLECTION_CONFIRM_TIMEOUT_MIN: int = Field(
        10, ge=1, description="수집 정책 적용 후 이 시간(분) 안에 확정하지 않으면 자동으로 되돌림"
    )
    K8S_API_URL: Optional[str] = Field(
        None, description="관리 클러스터 Kubernetes API 주소 (예: https://host:6443). Pod 안에서 돌면 생략 가능"
    )
    K8S_TOKEN: Optional[str] = Field(None, description="수집 설정 3종만 고칠 수 있는 서비스 계정 토큰")
    K8S_CA_FILE: Optional[str] = Field(None, description="Kubernetes API 인증서 검증용 CA 파일 경로")
    K8S_VERIFY_TLS: bool = Field(True, description="K8S_CA_FILE 이 없을 때 TLS 인증서 검증 여부")
    # ── 알람 ──────────────────────────────────────────────────────────────
    ALERT_EVAL_INTERVAL_SEC: int = Field(30, ge=5, description="알람 정책 평가 주기(초)")
    ALERT_REPEAT_INTERVAL_MIN: int = Field(240, ge=1, description="firing 유지 중 재알림 간격(분)")
    ALERT_TARGET_GONE_MIN: int = Field(
        60, ge=5, description="평가 결과에서 이 시간(분) 넘게 안 보인 대상의 알람은 대상 소멸로 보고 해제"
    )
    # 0~1 비율로 내보내는 exporter 면 100. 리허설 부하에서 furiosa-smi·rbln-stat 의 % 와 대조해 정함
    NPU_UTIL_SCALE_FURIOSA: float = Field(1.0, gt=0, description="Furiosa 사용률 원시값에 곱할 배율")
    NPU_UTIL_SCALE_REBELLIONS: float = Field(1.0, gt=0, description="Rebellions 사용률 원시값에 곱할 배율")
    SMTP_HOST: Optional[str] = Field(None, description="알람 메일 SMTP 호스트")
    SMTP_PORT: int = Field(587, description="SMTP 포트")
    SMTP_USER: Optional[str] = Field(None, description="SMTP 계정")
    SMTP_PASSWORD: Optional[str] = Field(None, description="SMTP 비밀번호")
    SMTP_FROM: Optional[str] = Field(None, description="알람 메일 발신 주소")
    OPENSTACK_AUTH_URL: Optional[str] = Field(
        None, description="Keystone auth URL (예: http://host:5000/v3)"
    )
    OPENSTACK_USERNAME: Optional[str] = Field(None, description="OpenStack 계정(admin/system-reader 필요)")
    OPENSTACK_PASSWORD: Optional[str] = Field(None, description="OpenStack 비밀번호")
    OPENSTACK_PROJECT_NAME: Optional[str] = Field(None, description="OpenStack 프로젝트")
    OPENSTACK_USER_DOMAIN: str = Field("Default", description="Keystone 사용자 도메인")
    OPENSTACK_PROJECT_DOMAIN: str = Field("Default", description="Keystone 프로젝트 도메인")
    OPENSTACK_NOVA_URL: Optional[str] = Field(None, description="Nova compute API URL 오버라이드")
    OPENSTACK_MAGNUM_URL: Optional[str] = Field(None, description="Magnum(container-infra) API URL 오버라이드")
    OPENSTACK_CYBORG_URL: Optional[str] = Field(
        None, description="Cyborg(accelerator) API URL 오버라이드 (예: http://host:6666/accelerator/v2)"
    )


settings = Settings()


def npu_util(query: str, vendor: str) -> str:
    """NPU 사용률 쿼리에 배율을 붙인다. 배율 1 이면 그대로."""
    scale = {"furiosa": settings.NPU_UTIL_SCALE_FURIOSA, "rebellions": settings.NPU_UTIL_SCALE_REBELLIONS}.get(vendor, 1.0)
    return query if scale == 1.0 else f"({query}) * {scale:g}"
