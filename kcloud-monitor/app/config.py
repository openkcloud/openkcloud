from pydantic import Field, field_validator
from pydantic_settings import BaseSettings, SettingsConfigDict
from typing import Optional, Dict, Any
import json

class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_file='.env', env_file_encoding='utf-8', extra='ignore')

    # API Authentication
    API_AUTH_USERNAME: str = Field("admin", description="API username")
    API_AUTH_PASSWORD: str = Field("changeme", description="API password")
    JWT_SECRET_KEY: str = Field("change-this-secret-key", description="JWT secret key for token signing")
    JWT_ALGORITHM: str = Field("HS256", description="JWT algorithm")
    JWT_ACCESS_TOKEN_EXPIRE_MINUTES: int = Field(60, description="JWT token expiration in minutes")

    # Prometheus (Single cluster - backward compatibility)
    PROMETHEUS_URL: str = Field("http://localhost:9090", description="URL of the Prometheus server")
    PROMETHEUS_TIMEOUT: int = Field(30, description="Timeout in seconds for Prometheus queries")
    PROMETHEUS_USERNAME: Optional[str] = Field(None, description="Username for Prometheus basic auth")
    PROMETHEUS_PASSWORD: Optional[str] = Field(None, description="Password for Prometheus basic auth")
    PROMETHEUS_CA_BUNDLE: Optional[str] = Field(None, description="Path to a CA bundle for verifying Prometheus TLS")

    # Multi-cluster Prometheus Configuration (Phase 6)
    PROMETHEUS_CLUSTERS: Optional[str] = Field(
        None,
        description="JSON string with cluster configurations. Example: "
        '[{"name":"cluster1","url":"http://prom1:9090","region":"us-east"},{"name":"cluster2","url":"http://prom2:9090","region":"us-west"}]'
    )
    DEFAULT_CLUSTER: str = Field("default", description="Default cluster name when PROMETHEUS_CLUSTERS is not set")

    # Cache
    CACHE_TTL_GPU_CURRENT: int = Field(30, description="Cache TTL for current GPU data")
    CACHE_TTL_GPU_TIMESERIES: int = Field(300, description="Cache TTL for GPU time-series data")
    CACHE_TTL_POWER_SUMMARY: int = Field(60, description="Cache TTL for power summary data")

    # Logging
    LOG_LEVEL: str = Field("INFO", description="Logging level")


settings = Settings()
