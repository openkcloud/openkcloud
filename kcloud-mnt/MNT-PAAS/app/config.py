"""MNT-PAAS 설정 — Magnum/K8s 중개. 전부 env 주입(파드: ConfigMap/Secret)."""
from pydantic_settings import BaseSettings


class Settings(BaseSettings):
    service_name: str = "mnt-paas"
    api_prefix: str = "/api/paas"
    cors_origins: str = ""

    # Magnum(클러스터 생성) — OpenStack 인증 재사용
    os_auth_url: str = ""
    os_username: str = ""
    os_password: str = ""
    os_project_name: str = ""
    os_user_domain_name: str = "Default"
    os_project_domain_name: str = "Default"
    # TLS 검증 — 사내 root CA 신뢰 전까지는 true 로 우회 (MNT-IAAS 와 동일)
    os_insecure: bool = True
    # 서비스 엔드포인트 해석 — override(진입 URL 직접) / catalog(카탈로그 자동발견)
    os_endpoint_mode: str = "override"
    os_endpoint_template: str = "https://{service}-api.openstack.internal:25443"

    # 대상 K8s 접근 — kubeconfig 파일 경로 (파드: Secret 볼륨 마운트)
    kubeconfig_path: str = ""

    core_api_base: str = ""

    # 클러스터 프로비저닝 작업(Temporal 워커, 큐 okc-paas) — 비우면 워커를 띄우지 않는다(로컬: MNT-WORKFLOW/run-local.sh)
    temporal_api_base: str = ""
    # 새 클러스터 노드의 가용 영역(라벨 availability_zone) — 기존 클러스터와 같은 값
    cluster_availability_zone: str = "az1"

    class Config:
        env_file = "config.env"
        env_file_encoding = "utf-8"


settings = Settings()
