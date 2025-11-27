"""
Unit tests for Cache Service

Tests the SimpleCache implementation including:
- Basic get/set operations
- TTL expiration
- Cache size management
- Expired entry purging
"""

import pytest
import asyncio
from datetime import datetime, timedelta

from app.services.cache import SimpleCache, CacheEntry


class TestCacheEntry:
    """Test CacheEntry class"""

    def test_cache_entry_creation(self):
        """Test creating a cache entry"""
        data = {"key": "value"}
        ttl = 60
        entry = CacheEntry(data, ttl)

        assert entry.data == data
        assert isinstance(entry.created_at, datetime)
        assert entry.ttl == timedelta(seconds=ttl)

    def test_cache_entry_not_expired(self):
        """Test that a fresh entry is not expired"""
        entry = CacheEntry("data", 60)
        assert not entry.is_expired()

    def test_cache_entry_expired(self):
        """Test that an old entry is expired"""
        entry = CacheEntry("data", 0)
        entry.created_at = datetime.utcnow() - timedelta(seconds=1)
        assert entry.is_expired()


class TestSimpleCache:
    """Test SimpleCache class"""

    @pytest.mark.asyncio
    async def test_cache_initialization(self):
        """Test cache initializes empty"""
        cache = SimpleCache()
        size = await cache.size()
        assert size == 0

    @pytest.mark.asyncio
    async def test_cache_set_and_get(self):
        """Test setting and getting a value"""
        cache = SimpleCache()
        key = "test_key"
        value = {"data": "test_value"}

        await cache.set(key, value, ttl=60)
        retrieved = await cache.get(key)

        assert retrieved == value
        assert await cache.size() == 1

    @pytest.mark.asyncio
    async def test_cache_get_nonexistent(self):
        """Test getting a key that doesn't exist"""
        cache = SimpleCache()
        result = await cache.get("nonexistent")
        assert result is None

    @pytest.mark.asyncio
    async def test_cache_ttl_expiration(self):
        """Test that entries expire after TTL"""
        cache = SimpleCache()
        key = "expiring_key"
        value = "expiring_value"

        # Set with 1 second TTL
        await cache.set(key, value, ttl=1)

        # Should be retrievable immediately
        assert await cache.get(key) == value

        # Wait for expiration
        await asyncio.sleep(1.2)

        # Should be None after expiration
        assert await cache.get(key) is None

        # Cache should be empty after expired entry removed
        assert await cache.size() == 0

    @pytest.mark.asyncio
    async def test_cache_overwrite(self):
        """Test overwriting an existing key"""
        cache = SimpleCache()
        key = "test_key"

        await cache.set(key, "value1", ttl=60)
        await cache.set(key, "value2", ttl=60)

        result = await cache.get(key)
        assert result == "value2"
        assert await cache.size() == 1

    @pytest.mark.asyncio
    async def test_cache_multiple_entries(self):
        """Test storing multiple entries"""
        cache = SimpleCache()

        entries = {
            "key1": "value1",
            "key2": "value2",
            "key3": "value3"
        }

        for key, value in entries.items():
            await cache.set(key, value, ttl=60)

        assert await cache.size() == 3

        for key, expected_value in entries.items():
            assert await cache.get(key) == expected_value

    @pytest.mark.asyncio
    async def test_cache_purge_expired(self):
        """Test purging expired entries"""
        cache = SimpleCache()

        # Add entries with different TTLs
        await cache.set("short_lived1", "value1", ttl=1)
        await cache.set("short_lived2", "value2", ttl=1)
        await cache.set("long_lived", "value3", ttl=60)

        assert await cache.size() == 3

        # Wait for short-lived entries to expire
        await asyncio.sleep(1.2)

        # Purge expired entries
        purged_count = await cache.purge_expired()

        assert purged_count == 2
        assert await cache.size() == 1
        assert await cache.get("long_lived") == "value3"
        assert await cache.get("short_lived1") is None
        assert await cache.get("short_lived2") is None

    @pytest.mark.asyncio
    async def test_cache_complex_data_types(self):
        """Test storing complex data types"""
        cache = SimpleCache()

        # Test list
        await cache.set("list", [1, 2, 3], ttl=60)
        assert await cache.get("list") == [1, 2, 3]

        # Test dict
        await cache.set("dict", {"a": 1, "b": 2}, ttl=60)
        assert await cache.get("dict") == {"a": 1, "b": 2}

        # Test nested structure
        nested = {
            "data": [1, 2, 3],
            "meta": {"count": 3}
        }
        await cache.set("nested", nested, ttl=60)
        assert await cache.get("nested") == nested

    @pytest.mark.asyncio
    async def test_cache_zero_ttl(self):
        """Test that zero TTL entries expire immediately"""
        cache = SimpleCache()

        await cache.set("instant_expire", "value", ttl=0)

        # Should expire almost immediately
        await asyncio.sleep(0.1)
        assert await cache.get("instant_expire") is None

    @pytest.mark.asyncio
    async def test_cache_concurrent_access(self):
        """Test concurrent cache access"""
        cache = SimpleCache()

        async def writer(key, value):
            await cache.set(key, value, ttl=60)

        async def reader(key):
            return await cache.get(key)

        # Concurrent writes
        await asyncio.gather(
            writer("key1", "value1"),
            writer("key2", "value2"),
            writer("key3", "value3")
        )

        assert await cache.size() == 3

        # Concurrent reads
        results = await asyncio.gather(
            reader("key1"),
            reader("key2"),
            reader("key3")
        )

        assert results == ["value1", "value2", "value3"]
