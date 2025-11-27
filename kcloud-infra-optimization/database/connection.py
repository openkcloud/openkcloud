#!/usr/bin/env python3
"""
"""
import os
import sys
import asyncio
import logging
from typing import Dict, Any, Optional, AsyncGenerator
from contextlib import asynccontextmanager
from datetime import datetime, timedelta
try:
    import asyncpg
    import redis.asyncio as aioredis
    from asyncpg import Pool
    from redis.asyncio import Redis
except ImportError:
    raise ImportError("Database libraries (asyncpg, redis) not found. Please install them or set PYTHONPATH")
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)
class DatabaseConfig:
"""
    """
    
    def __init__(self):

        self.postgres_host = os.getenv('POSTGRES_HOST', 'localhost')
        self.postgres_port = int(os.getenv('POSTGRES_PORT', 5432))
        self.postgres_db = os.getenv('POSTGRES_DB', 'kcloud_opt')
        self.postgres_user = os.getenv('POSTGRES_USER', 'kcloud_user')
        self.postgres_password = os.getenv('POSTGRES_PASSWORD', '')
        

        self.redis_host = os.getenv('REDIS_HOST', 'localhost')
        self.redis_port = int(os.getenv('REDIS_PORT', 6379))
        self.redis_db = int(os.getenv('REDIS_DB', 0))
        self.redis_password = os.getenv('REDIS_PASSWORD', None)
        

        self.postgres_min_connections = int(os.getenv('POSTGRES_MIN_CONN', 10))
        self.postgres_max_connections = int(os.getenv('POSTGRES_MAX_CONN', 50))
        

        self.connection_timeout = int(os.getenv('DB_CONNECTION_TIMEOUT', 30))
        self.query_timeout = int(os.getenv('DB_QUERY_TIMEOUT', 60))
    
    @property
    def postgres_dsn(self) -> str:
        """
        return f"postgresql://{self.postgres_user}:{self.postgres_password}@{self.postgres_host}:{self.postgres_port}/{self.postgres_db}"
    
    @property
    def redis_url(self) -> str:
        """
        auth = f":{self.redis_password}@" if self.redis_password else ""
        return f"redis://{auth}{self.redis_host}:{self.redis_port}/{self.redis_db}"


class DatabaseManager:
    """
    
    def __init__(self, config: DatabaseConfig = None):
        self.config = config or DatabaseConfig()
        self.postgres_pool: Optional[Pool] = None
        self.redis_client: Optional[Redis] = None
        self._connected = False
    
    async def connect(self):
        """try:


            await self._connect_postgres()


            await self._connect_redis()


            await self._verify_connections()

            self._connected = True

        except Exception as e:
            await self.disconnect()
            raise
    
    async def _connect_postgres(self):
"""
        try:
            self.postgres_pool = await asyncpg.create_pool(
                self.config.postgres_dsn,
                min_size=self.config.postgres_min_connections,
                max_size=self.config.postgres_max_connections,
                command_timeout=self.config.query_timeout,
                server_settings={
                    'application_name': 'kcloud-opt',
                    'search_path': 'public',
                }
            )
        except Exception as e:
            raise
    async def _connect_redis(self):
"""
        """try:
            self.redis_client = aioredis.from_url(
                self.config.redis_url,
                decode_responses=True,
                socket_timeout=self.config.connection_timeout,
                socket_connect_timeout=self.config.connection_timeout,
                retry_on_timeout=True,
                health_check_interval=30
            )
            

            await self.redis_client.ping()

        except Exception as e:
            raise
    
    async def _verify_connections(self):
"""
        try:
            async with self.postgres_pool.acquire() as conn:
                version = await conn.fetchval("SELECT version()")
                timescale = await conn.fetchval(
                    "SELECT installed_version FROM pg_available_extensions WHERE name = 'timescaledb'"
                )
                if timescale:
                else:
            redis_info = await self.redis_client.info()
        except Exception as e:
            raise
    async def disconnect(self):
"""
        """if self.postgres_pool:
            await self.postgres_pool.close()
            self.postgres_pool = None


        if self.redis_client:
            await self.redis_client.close()
            self.redis_client = None

        self._connected = False
    
    @asynccontextmanager
    async def postgres_transaction(self) -> AsyncGenerator[asyncpg.Connection, None]:
"""
        if not self._connected:
        async with self.postgres_pool.acquire() as conn:
            async with conn.transaction():
                yield conn
    @asynccontextmanager
    async def postgres_connection(self) -> AsyncGenerator[asyncpg.Connection, None]:
"""
        """if not self._connected:
        
        async with self.postgres_pool.acquire() as conn:
            yield conn
    
    async def execute_query(self, query: str, *args, **kwargs) -> Any:
"""
        async with self.postgres_connection() as conn:
            if kwargs.get('fetch', 'all') == 'one':
                return await conn.fetchrow(query, *args)
            elif kwargs.get('fetch') == 'val':
                return await conn.fetchval(query, *args)
            elif kwargs.get('fetch') == 'all':
                return await conn.fetch(query, *args)
            else:
                return await conn.execute(query, *args)
    
    async def redis_get(self, key: str, default=None) -> Any:
        """if not self._connected:
        
        try:
            value = await self.redis_client.get(key)
            return value if value is not None else default
        except Exception as e:
            return default
    
    async def redis_set(self, key: str, value: str, expire: int = None) -> bool:
"""
        if not self._connected:
        try:
            result = await self.redis_client.set(key, value, ex=expire)
            return bool(result)
        except Exception as e:
            return False
    async def redis_delete(self, *keys: str) -> int:
"""
        """if not self._connected:
        
        try:
            return await self.redis_client.delete(*keys)
        except Exception as e:
            return 0
    
    async def redis_publish(self, channel: str, message: str) -> int:
"""
        if not self._connected:
        try:
            return await self.redis_client.publish(channel, message)
        except Exception as e:
            return 0
    async def health_check(self) -> Dict[str, Any]:
"""
        """status = {
            'connected': self._connected,
            'postgres': False,
            'redis': False,
            'timestamp': datetime.now().isoformat()
        }
        
        if not self._connected:
            return status
        
        try:

            async with self.postgres_connection() as conn:
                await conn.fetchval("SELECT 1")
                status['postgres'] = True
        except Exception as e:
        
        try:

            await self.redis_client.ping()
            status['redis'] = True
        except Exception as e:
        
        return status
    
    @property
    def is_connected(self) -> bool:
"""
        return self._connected
_db_manager: Optional[DatabaseManager] = None
def get_database_manager() -> DatabaseManager:
"""
    """
    global _db_manager
    if _db_manager is None:
        _db_manager = DatabaseManager()
    return _db_manager

async def init_database():
    """
    db_manager = get_database_manager()
    await db_manager.connect()
    return db_manager

async def close_database():
    """global _db_manager
    if _db_manager:
        await _db_manager.disconnect()
        _db_manager = None


@asynccontextmanager
async def database_context():
"""
    db_manager = None
    try:
        db_manager = await init_database()
        yield db_manager
    finally:
        if db_manager:
            await db_manager.disconnect()


if __name__ == "__main__":
    async def test_connection():
        """
        print("데이터베이스 연결 테스트")
        print("=" * 40)

        async with database_context() as db:

            health = await db.health_check()
            print(f"연결 상태: {health}")


            try:
                tables = await db.execute_query(
                    "SELECT table_name FROM information_schema.tables WHERE table_schema = 'public'"
                )
                print(f"테이블 수: {len(tables)}")
                for table in tables[:5]:
                    print(f"  - {table['table_name']}")
            except Exception as e:
                print(f"PostgreSQL 테스트 실패: {e}")


            try:
                await db.redis_set("test:connection", "success", 60)
                value = await db.redis_get("test:connection")
                print(f"Redis 테스트: {value}")
                await db.redis_delete("test:connection")
            except Exception as e:
                print(f"Redis 테스트 실패: {e}")
    

    asyncio.run(test_connection())
