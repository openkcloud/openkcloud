"""
Application settings and environment configuration
"""
import os
from typing import Optional


class Settings:
    """Application settings"""

    def __init__(self):
        # Prometheus connection
        self.power_prometheus_url = os.getenv(
            "POWER_PROMETHEUS_URL",
            "http://prometheus:9090"
        )
        self.power_metrics_interval = int(os.getenv("POWER_METRICS_INTERVAL", "15"))

        # Cost calculation parameters
        self.electricity_rate = float(os.getenv("ELECTRICITY_RATE", "0.12"))
        self.cooling_factor = float(os.getenv("COOLING_FACTOR", "1.3"))
        self.carbon_rate = float(os.getenv("CARBON_RATE", "0.05"))

        # Data storage
        self.redis_url = os.getenv("REDIS_URL", "redis://localhost:6379")
        self.influxdb_url = os.getenv("INFLUXDB_URL", "http://influxdb:8086")
        self.influxdb_bucket = os.getenv("INFLUXDB_BUCKET", "power_metrics")

        # API settings
        self.api_host = os.getenv("API_HOST", "0.0.0.0")
        self.api_port = int(os.getenv("API_PORT", "8001"))
        # CORS
        self.cors_allow_origins = [o.strip() for o in os.getenv("CORS_ALLOW_ORIGINS", "*").split(",")]
        self.cors_allow_methods = [m.strip() for m in os.getenv("CORS_ALLOW_METHODS", "GET,POST,PUT,DELETE,OPTIONS").split(",")]
        self.cors_allow_headers = [h.strip() for h in os.getenv("CORS_ALLOW_HEADERS", "Origin,Content-Type,Accept,Authorization,X-Requested-With").split(",")]

        # Logging
        self.log_level = os.getenv("LOG_LEVEL", "INFO")


_settings: Optional[Settings] = None


def get_settings() -> Settings:
    """Get application settings (singleton)"""
    global _settings
    if _settings is None:
        _settings = Settings()
    return _settings
