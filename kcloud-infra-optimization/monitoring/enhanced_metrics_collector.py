#!/usr/bin/env python3
"""
"""
import sys
import json
import asyncio
import logging
from datetime import datetime, timedelta
from typing import Dict, List, Optional, Any
from dataclasses import dataclass, asdict
try:
    from infrastructure.monitoring.metrics_collector import MetricsCollector as BaseMetricsCollector, ClusterMetrics
    from infrastructure.database.connection import get_database_manager, DatabaseManager
    from infrastructure.database.redis_keys import RedisKeys, RedisPubSubChannels, RedisDataTypes, RedisExpirePolicy
except ImportError:
    try:
        from .metrics_collector import MetricsCollector as BaseMetricsCollector, ClusterMetrics
        from database.connection import get_database_manager, DatabaseManager
        from database.redis_keys import RedisKeys, RedisPubSubChannels, RedisDataTypes, RedisExpirePolicy
    except ImportError:
        raise ImportError("Required modules not found. Please ensure they're in PYTHONPATH or install the package")
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)
@dataclass
class EnhancedClusterMetrics(ClusterMetrics):
"""
    """
    

    cluster_id: Optional[str] = None
    collection_id: Optional[str] = None
    data_source: str = "openstack_magnum"
    processing_time_ms: float = 0.0
    
    def to_db_dict(self) -> Dict[str, Any]:
        """
        db_data = asdict(self)
        

        db_data['time'] = datetime.now()
        

        db_data['metadata'] = {
            'collection_id': self.collection_id,
            'data_source': self.data_source,
            'processing_time_ms': self.processing_time_ms,
            'openstack_uuid': db_data.get('uuid'),
            'template_info': {
                'template_id': self.template_id,
                'api_address': self.api_address
            }
        }
        

        for field in ['uuid', 'collection_id', 'data_source', 'processing_time_ms']:
            db_data.pop(field, None)
        
        return db_data

class EnhancedMetricsCollector:
    """
    
    def __init__(self, db_manager: DatabaseManager = None):
        self.base_collector = BaseMetricsCollector()
        self.db_manager = db_manager or get_database_manager()
        self.collection_session_id = datetime.now().strftime("%Y%m%d_%H%M%S")
        
    async def collect_and_store_metrics(self, cluster_name: str) -> EnhancedClusterMetrics:
        """
        start_time = datetime.now()
        try:
            base_metrics = self.base_collector.collect_full_metrics(cluster_name)
            enhanced_metrics = self._enhance_metrics(base_metrics)
            enhanced_metrics.processing_time_ms = (datetime.now() - start_time).total_seconds() * 1000
            if self.db_manager.is_connected:
                await self._store_to_database(enhanced_metrics)
                await self._update_redis_cache(enhanced_metrics)
                await self._publish_metrics_update(enhanced_metrics)
            else:
            return enhanced_metrics
        except Exception as e:
            return self._create_error_metrics(cluster_name, str(e))
    def _enhance_metrics(self, base_metrics: ClusterMetrics) -> EnhancedClusterMetrics:
"""
        """
        enhanced = EnhancedClusterMetrics(**asdict(base_metrics))
        enhanced.collection_id = f"{self.collection_session_id}_{base_metrics.cluster_name}"
        return enhanced
    
    async def _store_to_database(self, metrics: EnhancedClusterMetrics):
        """
        try:
            db_data = metrics.to_db_dict()
            

            cluster_id = await self._ensure_cluster_exists(metrics.cluster_name, metrics.template_id)
            db_data['cluster_id'] = cluster_id
            

            insert_query = """
            INSERT INTO cluster_metrics (
                time, cluster_name, cluster_id, status, health_status,
                node_count, master_count, cpu_usage, memory_usage, gpu_usage,
                disk_usage, network_io_mbps, running_pods, failed_pods, pending_pods,
                workload_count, power_consumption_watts, cost_per_hour,
                estimated_monthly_cost, health_score, efficiency_score, metadata
            ) VALUES (
                $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,
                $16, $17, $18, $19, $20, $21, $22
            )
            """
            await self.db_manager.execute_query(
                insert_query,
                db_data['time'], db_data['cluster_name'], db_data['cluster_id'],
                db_data['status'], db_data['health_status'], db_data['node_count'],
                db_data['master_count'], db_data['cpu_usage'], db_data['memory_usage'],
                db_data['gpu_usage'], db_data['disk_usage'], db_data['network_io_mbps'],
                db_data['running_pods'], db_data['failed_pods'], db_data['pending_pods'],
                db_data['workload_count'], db_data['power_consumption_watts'],
                db_data['cost_per_hour'], db_data['estimated_monthly_cost'],
                db_data['health_score'], db_data['efficiency_score'],
                json.dumps(db_data['metadata'])
            )
        except Exception as e:
            raise
    async def _ensure_cluster_exists(self, cluster_name: str, template_id: str) -> str:
"""
        """try:
            cluster = await self.db_manager.execute_query(
                "SELECT id FROM clusters WHERE name = $1",
                cluster_name, fetch='one'
            )
            
            if cluster:
                return str(cluster['id'])
            
            cluster_id = await self.db_manager.execute_query(
"""
                INSERT INTO clusters (name, template_id, project_id, status)
                VALUES ($1, $2, $3, $4)
                RETURNING id
                """,
                cluster_name, template_id, "a6ce5f91a73544c09414fdcae43a129f", "UNKNOWN",
                fetch='val'
            )
            
            return str(cluster_id)
            
        except Exception as e:
            return "unknown"
    
    async def _update_redis_cache(self, metrics: EnhancedClusterMetrics):
"""
        try:
            metrics_data = RedisDataTypes.serialize_cluster_metrics(asdict(metrics))
            await self.db_manager.redis_set(
                RedisKeys.metrics_latest(metrics.cluster_name),
                metrics_data,
                RedisExpirePolicy.METRICS_LATEST
            )
            current_status = {
                'cluster_name': metrics.cluster_name,
                'status': metrics.status,
                'health_score': metrics.health_score,
                'cost_per_hour': metrics.cost_per_hour,
                'last_update': datetime.now().isoformat()
            }
            await self.db_manager.redis_set(
                RedisKeys.cluster_current(metrics.cluster_name),
                json.dumps(current_status),
                RedisExpirePolicy.CLUSTER_CURRENT
            )
            history_key = RedisKeys.metrics_history(metrics.cluster_name, "1h")
            await self.db_manager.redis_client.lpush(history_key, metrics_data)
            await self.db_manager.redis_client.expire(history_key, RedisExpirePolicy.METRICS_HISTORY)
            await self.db_manager.redis_client.sadd(RedisKeys.cluster_list(), metrics.cluster_name)
        except Exception as e:
    async def _publish_metrics_update(self, metrics: EnhancedClusterMetrics):
"""
        """try:
            update_message = {
                'cluster_name': metrics.cluster_name,
                'status': metrics.status,
                'health_score': metrics.health_score,
                'timestamp': datetime.now().isoformat(),
                'event_type': 'metrics_updated'
            }
            
            await self.db_manager.redis_publish(
                RedisPubSubChannels.METRICS_UPDATED,
                json.dumps(update_message)
            )
            
            cluster_channel = f"kcloud:events:cluster:{metrics.cluster_name}:metrics"
            await self.db_manager.redis_publish(cluster_channel, json.dumps(update_message))
            
        except Exception as e:
    
    def _create_error_metrics(self, cluster_name: str, error_msg: str) -> EnhancedClusterMetrics:
"""
        return EnhancedClusterMetrics(
            cluster_name=cluster_name,
            timestamp=datetime.now().isoformat(),
            status="ERROR",
            health_status="ERROR",
            node_count=0,
            master_count=0,
            template_id="unknown",
            health_score=0.0,
            efficiency_score=0.0,
            collection_id=f"error_{datetime.now().strftime('%H%M%S')}",
            data_source="error_handler",
            processing_time_ms=0.0
        )
    
    async def collect_multiple_clusters_async(self, cluster_names: List[str]) -> List[EnhancedClusterMetrics]:
        """tasks = [self.collect_and_store_metrics(name) for name in cluster_names]
        results = await asyncio.gather(*tasks, return_exceptions=True)
        
        metrics_list = []
        for i, result in enumerate(results):
            if isinstance(result, Exception):
                metrics_list.append(self._create_error_metrics(cluster_names[i], str(result)))
            else:
                metrics_list.append(result)
        
        return metrics_list
    
    async def get_metrics_from_cache(self, cluster_name: str) -> Optional[Dict[str, Any]]:
"""
        try:
            cached_data = await self.db_manager.redis_get(RedisKeys.metrics_latest(cluster_name))
            if cached_data:
                return RedisDataTypes.deserialize_cluster_metrics(cached_data)
            return None
        except Exception as e:
            return None
    async def get_metrics_history(self, cluster_name: str, hours: int = 1) -> List[Dict[str, Any]]:
"""
        """try:
            if hours <= 1:
                history_data = await self.db_manager.redis_client.lrange(
                    RedisKeys.metrics_history(cluster_name, "1h"), 0, -1
                )
                return [RedisDataTypes.deserialize_cluster_metrics(data) for data in history_data]
            
            since_time = datetime.now() - timedelta(hours=hours)
            history = await self.db_manager.execute_query(
"""
                SELECT * FROM cluster_metrics
                WHERE cluster_name = $1 AND time >= $2
                ORDER BY time DESC
                LIMIT 1000
                """,
                cluster_name, since_time
            )
            
            return [dict(row) for row in history]
            
        except Exception as e:
            return []


async def test_enhanced_collector():
"""
    print(" 향상된 메트릭 수집기 테스트")
    print("=" * 50)
    
    try:

        collector = EnhancedMetricsCollector()
        

        cluster_name = "kcloud-dev-cluster"
        print(f"\n 단일 클러스터 메트릭 수집: {cluster_name}")
        

        base_metrics = collector.base_collector.collect_full_metrics(cluster_name)
        enhanced = collector._enhance_metrics(base_metrics)
        
        print(f"[OK] 수집 완료:")
        print(f"  클러스터: {enhanced.cluster_name}")
        print(f"  상태: {enhanced.status}")
        print(f"  비용: ${enhanced.cost_per_hour:.2f}/시간")
        print(f"  헬스: {enhanced.health_score:.1f}/100")
        print(f"  수집 ID: {enhanced.collection_id}")
        

        print(f"\n 다중 클러스터 수집 준비 완료")
        print(f"  수집기 세션: {collector.collection_session_id}")
        
    except Exception as e:
        print(f"[ERROR] 테스트 실패: {e}")

if __name__ == "__main__":
    asyncio.run(test_enhanced_collector())
