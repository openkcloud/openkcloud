from .prometheus import PrometheusClient
from .cache import SimpleCache
from app.config import settings

# Create singleton instances of the services
prometheus_client = PrometheusClient(settings)
cache_service = SimpleCache()
