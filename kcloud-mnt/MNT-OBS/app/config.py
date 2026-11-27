"""MNT-OBS 설정 — Prometheus/Loki 질의. 전부 env 주입(파드: ConfigMap)."""
from pydantic_settings import BaseSettings


class Settings(BaseSettings):
    service_name: str = "mnt-obs"
    api_prefix: str = "/api/obs"
    cors_origins: str = ""

    prometheus_url: str = ""       # 예: http://prometheus.monitoring.svc:9090
    loki_url: str = ""             # 예: http://loki.monitoring.svc:3100

    # KCloud Monitor v2 (이노그리드) — 로컬 dev는 SSH 터널(localhost:8000),
    # 파드는 master3 노드IP(예: http://172.29.236.153:8000/api/v2) 또는 Service.
    monitor_base_url: str = "http://localhost:8000/api/v2"
    monitor_username: str = "admin"      # 파드: Secret 주입
    monitor_password: str = "changeme"   # 파드: Secret 주입
    monitor_timeout: float = 10.0

    core_api_base: str = ""

    class Config:
        env_file = "config.env"
        env_file_encoding = "utf-8"


settings = Settings()
