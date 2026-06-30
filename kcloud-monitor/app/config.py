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
    API_KEY: Optional[str] = Field(None, description="Optional API key for X-API-Key header auth (parallel to JWT); unset disables")

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

    # Power efficiency (facility-level power is external; see open_issues D-4)
    PUE_COOLING_FACTOR: float = Field(
        0.35, ge=0,
        description="Cooling/overhead power as a fraction of IT power for PUE estimation. "
                    "Facility data is external (BMS/PDU); replace when integrated (design D-4).",
    )

    # Redis (Phase 11.1 - shared cache for multi-worker/K8s; unset = in-memory cache)
    REDIS_URL: Optional[str] = Field(None, description="Redis URL for shared cache (e.g. redis://host:6379/0); unset uses in-memory cache")

    # Rate limiting (Phase 11.2)
    RATE_LIMIT_ENABLED: bool = Field(False, description="Enable API rate limiting")
    RATE_LIMIT_PER_MINUTE: int = Field(120, ge=1, description="Requests per minute per client when rate limiting is enabled")

    # CORS (Phase 11.2 - restrict origins in production)
    CORS_ALLOW_ORIGINS: str = Field("*", description="Comma-separated allowed origins, or * for all")

    # IPMI hardware sensors (open_issues H-1; local ipmitool + node_exporter textfile via Prometheus)
    IPMI_ENABLED: bool = Field(False, description="Enable IPMI hardware sensor endpoints")

    # OpenStack (Phase 4.4 - VM collector; unset = VM data disabled)
    OPENSTACK_AUTH_URL: Optional[str] = Field(None, description="OpenStack Keystone auth URL (e.g. http://host:5000/v3)")
    OPENSTACK_USERNAME: Optional[str] = Field(None, description="OpenStack username")
    OPENSTACK_PASSWORD: Optional[str] = Field(None, description="OpenStack password")
    OPENSTACK_PROJECT_NAME: Optional[str] = Field(None, description="OpenStack project name")

    # Logging
    LOG_LEVEL: str = Field("INFO", description="Logging level")


settings = Settings()
