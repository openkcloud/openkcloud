import asyncio
from datetime import datetime, timedelta
from typing import Any, Dict, Optional

class CacheEntry:
    """An entry in the cache with a TTL."""
    def __init__(self, data: Any, ttl_seconds: int):
        self.data = data
        self.created_at = datetime.utcnow()
        self.ttl = timedelta(seconds=ttl_seconds)

    def is_expired(self) -> bool:
        """Check if the cache entry has expired."""
        return datetime.utcnow() > self.created_at + self.ttl

class SimpleCache:
    """A simple, thread-safe, in-memory cache with TTL support."""
    def __init__(self):
        self._cache: Dict[str, CacheEntry] = {}
        self._lock = asyncio.Lock()

    async def get(self, key: str) -> Optional[Any]:
        """Retrieves an item from the cache if it exists and has not expired."""
        async with self._lock:
            entry = self._cache.get(key)
            if not entry:
                return None
            
            if entry.is_expired():
                del self._cache[key]
                return None
            
            return entry.data

    async def set(self, key: str, value: Any, ttl: int):
        """Adds an item to the cache with a specified TTL."""
        async with self._lock:
            self._cache[key] = CacheEntry(value, ttl)

    async def size(self) -> int:
        """Returns the number of items in the cache."""
        async with self._lock:
            return len(self._cache)

    async def purge_expired(self) -> int:
        """Removes all expired items from the cache."""
        async with self._lock:
            expired_keys = [k for k, v in self._cache.items() if v.is_expired()]
            for key in expired_keys:
                del self._cache[key]
            return len(expired_keys)
