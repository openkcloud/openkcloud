"""
Cache warmup (Phase 11.1): preheat frequently-used data on startup.

Runs as a non-blocking background task from the app lifespan; each loader is
guarded so failures never affect startup.

ponytail: caches under dedicated ``warmup:*`` keys (warms the Prometheus
connection + data path). Preheating exact endpoint cache keys is a follow-up.
"""

import logging
from typing import Any, Callable, Dict, Awaitable

import app.crud as crud
from app.services import cache_service

logger = logging.getLogger(__name__)


async def warmup_cache(ttl: int = 30) -> int:
    """Preheat the cache with common reads. Returns the number warmed."""
    loaders: Dict[str, Callable[[], Awaitable[Any]]] = {
        "warmup:gpus": crud.get_dcgm_gpu_info,
        "warmup:npus": crud.get_npu_info,
        "warmup:unified_power": crud.get_unified_power,
    }
    warmed = 0
    for key, loader in loaders.items():
        try:
            data = await loader()
            await cache_service.set(key, data, ttl)
            warmed += 1
        except Exception as e:  # never let warmup break startup
            logger.warning(f"cache warmup failed for {key}: {e}")
    logger.info(f"cache warmup complete: {warmed}/{len(loaders)}")
    return warmed
