#!/usr/bin/env python3
"""
"""
import sys
import json
import asyncio
import logging
from datetime import datetime, timedelta
from typing import Dict, List, Optional, Any
from contextlib import asynccontextmanager
try:
    from infrastructure.database.connection import get_database_manager, init_database, close_database
    from infrastructure.monitoring.enhanced_metrics_collector import EnhancedMetricsCollector, EnhancedClusterMetrics
    from infrastructure.monitoring.enhanced_alert_system import EnhancedAlertSystem, EnhancedAlert
    from infrastructure.database.redis_keys import RedisKeys, RedisPubSubChannels, RedisDataTypes
    from infrastructure.monitoring.realtime_dashboard import RealTimeDashboard
except ImportError:
    try:
        from database.connection import get_database_manager, init_database, close_database
        from enhanced_metrics_collector import EnhancedMetricsCollector, EnhancedClusterMetrics
        from enhanced_alert_system import EnhancedAlertSystem, EnhancedAlert
        from database.redis_keys import RedisKeys, RedisPubSubChannels, RedisDataTypes
        from realtime_dashboard import RealTimeDashboard
    except ImportError:
        raise ImportError("Required modules not found. Please ensure they're in PYTHONPATH or install the package")
try:
    from infrastructure.monitoring.integrated_monitor import IntegratedMonitor
except ImportError:
    try:
        from .integrated_monitor import IntegratedMonitor
    except ImportError:
        IntegratedMonitor = None
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)
class DatabaseIntegratedMonitor:
"""
    """
    
    def __init__(self, update_interval: int = 30, use_database: bool = True):
        self.update_interval = update_interval
        self.use_database = use_database
        self.running = False
        

        self.db_manager = None
        self.metrics_collector = None
        self.alert_system = None
        self.dashboard = None
        

        self.fallback_monitor = None
        

        self.last_health_check = None
        self.error_count = 0
        self.max_errors = 5
    
    async def initialize(self):
        """
        try:
            if self.use_database:
                await self._initialize_database_components()
            else:
                await self._initialize_fallback_system()
        except Exception as e:
            if self.use_database:
                await self._initialize_fallback_system()
            else:
                raise
    async def _initialize_database_components(self):
"""
        """try:

            self.db_manager = await init_database()
            

            self.metrics_collector = EnhancedMetricsCollector(self.db_manager)
            self.alert_system = EnhancedAlertSystem(self.db_manager)
            await self.alert_system.initialize()
            

            self.dashboard = RealTimeDashboard(self.update_interval)
            
            
        except Exception as e:
            raise
    
    async def _initialize_fallback_system(self):
"""
        try:
            self.fallback_monitor = IntegratedMonitor(self.update_interval)
        except Exception as e:
            raise
    async def monitor_clusters_enhanced(self, cluster_names: List[str]) -> Dict[str, Any]:
"""
        """if not self.use_database or not self.db_manager:
            return await self._monitor_clusters_fallback(cluster_names)
        
        try:
            

            metrics_list = await self.metrics_collector.collect_multiple_clusters_async(cluster_names)
            

            all_alerts = []
            for metrics in metrics_list:
                alerts = await self.alert_system.process_metrics_alerts(metrics)
                all_alerts.extend(alerts)
            

            summary = await self._generate_enhanced_summary(metrics_list, all_alerts)
            

            await self._update_dashboard_cache(summary)
            
            self.error_count = 0
            return summary
            
        except Exception as e:
            self.error_count += 1
            

            if self.error_count >= self.max_errors:
                return await self._monitor_clusters_fallback(cluster_names)
            
            return await self._generate_error_summary(str(e))
    
    async def _monitor_clusters_fallback(self, cluster_names: List[str]) -> Dict[str, Any]:
"""
        if not self.fallback_monitor:
            await self._initialize_fallback_system()
        cluster_metrics = self.fallback_monitor.monitor_clusters(cluster_names)
        return {
            'timestamp': datetime.now().isoformat(),
            'mode': 'fallback',
            'clusters': {name: metrics.to_dict() for name, metrics in cluster_metrics.items()},
            'summary': self._generate_basic_summary(cluster_metrics),
            'alerts': {'total_active': 0, 'recent_alerts': []},
            'recommendations': self._generate_basic_recommendations(cluster_metrics)
        }
    async def _generate_enhanced_summary(self, metrics_list: List[EnhancedClusterMetrics],
                                       alerts: List[EnhancedAlert]) -> Dict[str, Any]:
"""
        """
        total_cost = sum(m.cost_per_hour for m in metrics_list)
        total_power = sum(m.power_consumption_watts for m in metrics_list)
        active_clusters = len([m for m in metrics_list if m.status == 'CREATE_COMPLETE'])
        

        alert_summary = await self.alert_system.get_alert_summary()
        

        performance_analysis = await self._analyze_cluster_performance(metrics_list)
        
        return {
            'timestamp': datetime.now().isoformat(),
            'mode': 'database_integrated',
            'clusters': {m.cluster_name: m.to_db_dict() for m in metrics_list},
            'summary': {
                'total_cost_per_hour': total_cost,
                'total_power_consumption': total_power,
                'active_clusters': active_clusters,
                'total_clusters': len(metrics_list),
                'avg_health_score': sum(m.health_score for m in metrics_list if m.status == 'CREATE_COMPLETE') / max(active_clusters, 1),
                'avg_efficiency_score': sum(m.efficiency_score for m in metrics_list if m.status == 'CREATE_COMPLETE') / max(active_clusters, 1)
            },
            'alerts': alert_summary,
            'performance': performance_analysis,
            'recommendations': await self._generate_smart_recommendations(metrics_list, alerts),
            'database_stats': await self._get_database_stats()
        }
    
    async def _analyze_cluster_performance(self, metrics_list: List[EnhancedClusterMetrics]) -> Dict[str, Any]:
        """
        active_metrics = [m for m in metrics_list if m.status == 'CREATE_COMPLETE']
        
        if not active_metrics:
            return {'status': 'no_active_clusters'}
        
        analysis = {
            'cpu': {
                'avg': sum(m.cpu_usage for m in active_metrics) / len(active_metrics),
                'max': max(m.cpu_usage for m in active_metrics),
                'min': min(m.cpu_usage for m in active_metrics)
            },
            'memory': {
                'avg': sum(m.memory_usage for m in active_metrics) / len(active_metrics),
                'max': max(m.memory_usage for m in active_metrics),
                'min': min(m.memory_usage for m in active_metrics)
            },
            'cost_efficiency': {
                'cost_per_performance': sum(m.cost_per_hour / max(m.efficiency_score, 1) for m in active_metrics) / len(active_metrics),
                'high_cost_clusters': [m.cluster_name for m in active_metrics if m.cost_per_hour > 10.0],
                'low_efficiency_clusters': [m.cluster_name for m in active_metrics if m.efficiency_score < 40.0]
            },
            'health_trends': {
                'healthy_clusters': len([m for m in active_metrics if m.health_score > 80]),
                'warning_clusters': len([m for m in active_metrics if 50 <= m.health_score <= 80]),
                'critical_clusters': len([m for m in active_metrics if m.health_score < 50])
            }
        }
        
        return analysis
    
    async def _generate_smart_recommendations(self, metrics_list: List[EnhancedClusterMetrics],
                                            alerts: List[EnhancedAlert]) -> List[str]:
        """recommendations = []
        active_metrics = [m for m in metrics_list if m.status == 'CREATE_COMPLETE']
        
        if not active_metrics:
        

        high_cost = [m for m in active_metrics if m.cost_per_hour > 15.0]
        if high_cost:
            total_potential_savings = sum(m.cost_per_hour * 0.3 for m in high_cost)
        

        low_cpu = [m for m in active_metrics if m.cpu_usage < 20.0]
        if len(low_cpu) > 1:
        

        gpu_clusters = [m for m in active_metrics if m.gpu_usage > 0]
        if gpu_clusters:
            avg_gpu = sum(m.gpu_usage for m in gpu_clusters) / len(gpu_clusters)
            if avg_gpu < 30:
        

        critical_alerts = [a for a in alerts if a.severity == 'CRITICAL']
        if critical_alerts:
        

        unhealthy = [m for m in active_metrics if m.health_score < 70]
        if unhealthy:
        
        if not recommendations:
        
        return recommendations
    
    async def _get_database_stats(self) -> Dict[str, Any]:
"""
        if not self.db_manager or not self.db_manager.is_connected:
            return {'status': 'disconnected'}
        
        try:

            pg_stats = await self.db_manager.execute_query(
                """
                SELECT
                    (SELECT count(*) FROM cluster_metrics WHERE time >= NOW() - INTERVAL '1 hour') as metrics_1h,
                    (SELECT count(*) FROM clusters) as total_clusters,
                    (SELECT count(*) FROM alerts WHERE triggered_at >= NOW() - INTERVAL '24 hours') as alerts_24h,
                    (SELECT pg_database_size(current_database())) as db_size_bytes
                """
                fetch='one'
            )
            redis_info = await self.db_manager.redis_client.info()
            return {
                'status': 'connected',
                'postgresql': {
                    'metrics_last_hour': pg_stats['metrics_1h'],
                    'total_clusters': pg_stats['total_clusters'],
                    'alerts_24h': pg_stats['alerts_24h'],
                    'database_size_mb': round(pg_stats['db_size_bytes'] / 1024 / 1024, 2)
                },
                'redis': {
                    'memory_used_mb': round(redis_info['used_memory'] / 1024 / 1024, 2),
                    'keys_count': redis_info['db0']['keys'] if 'db0' in redis_info else 0,
                    'hit_rate': f"{redis_info.get('keyspace_hit_rate', 0):.2f}%"
                }
            }
        except Exception as e:
            return {'status': 'error', 'message': str(e)}
    async def _update_dashboard_cache(self, summary: Dict[str, Any]):
"""
        """try:
            cache_data = RedisDataTypes.create_dashboard_cache(
                summary['clusters'],
                summary['summary'],
                summary['alerts']['total_active']
            )
            
            await self.db_manager.redis_set(
                RedisKeys.dashboard_cache(),
                cache_data,
                RedisExpirePolicy.DASHBOARD_CACHE
            )
            
        except Exception as e:
    
    async def run_continuous_monitoring(self, cluster_names: List[str]):
"""
        self.running = True
        try:
            while self.running:
                print(f"\n{'='*80}")
                if self.use_database and self.db_manager:
                else:
                print('='*80)
                summary = await self.monitor_clusters_enhanced(cluster_names)
                self._print_monitoring_summary(summary)
                if datetime.now() - (self.last_health_check or datetime.min) > timedelta(minutes=5):
                    await self._perform_health_check()
                await asyncio.sleep(self.update_interval)
        except KeyboardInterrupt:
            self.running = False
        except Exception as e:
            self.running = False
    def _print_monitoring_summary(self, summary: Dict[str, Any]):
"""
        """if not summary.get('clusters'):
            return
        

        summary_data = summary['summary']
        
        if summary_data['active_clusters'] > 0:
        

        alerts = summary['alerts']
        if alerts['total_active'] > 0:
        else:
        

        if 'performance' in summary:
            perf = summary['performance']
            if 'cpu' in perf:
        

        if 'database_stats' in summary and summary['database_stats'].get('status') == 'connected':
            db_stats = summary['database_stats']
        

        recommendations = summary.get('recommendations', [])
        if recommendations:
            for rec in recommendations[:3]:
                print(f"  - {rec}")
    
    async def _perform_health_check(self):
"""
        try:
            if self.db_manager:
                health = await self.db_manager.health_check()
                if not health['postgres'] or not health['redis']:
            self.last_health_check = datetime.now()
        except Exception as e:
    async def cleanup(self):
"""
        """self.running = False
        
        if self.db_manager:
            await close_database()
        
    

    def _generate_basic_summary(self, cluster_metrics: Dict) -> Dict[str, Any]:
"""
        return {
            'total_cost_per_hour': sum(m.cost_per_hour for m in cluster_metrics.values()),
            'total_power_consumption': sum(m.power_consumption_watts for m in cluster_metrics.values()),
            'active_clusters': len([m for m in cluster_metrics.values() if m.status == 'CREATE_COMPLETE']),
            'total_clusters': len(cluster_metrics)
        }
    
    def _generate_basic_recommendations(self, cluster_metrics: Dict) -> List[str]:
        """active_metrics = [m for m in cluster_metrics.values() if m.status == 'CREATE_COMPLETE']
        
        if not active_metrics:
        
        recommendations = []
        
        high_cost = [m for m in active_metrics if m.cost_per_hour > 10.0]
        if high_cost:
        
        if not recommendations:
        
        return recommendations
    
    async def _generate_error_summary(self, error_msg: str) -> Dict[str, Any]:
"""
        return {
            'timestamp': datetime.now().isoformat(),
            'mode': 'error',
            'error': error_msg,
            'clusters': {},
            'summary': {
                'total_cost_per_hour': 0.0,
                'total_power_consumption': 0.0,
                'active_clusters': 0,
                'total_clusters': 0
            },
            'alerts': {'total_active': 0, 'recent_alerts': []},
        }
@asynccontextmanager
async def database_monitoring_context(cluster_names: List[str],
                                    update_interval: int = 30,
                                    use_database: bool = True):
"""
    """
    monitor = DatabaseIntegratedMonitor(update_interval, use_database)
    try:
        await monitor.initialize()
        yield monitor
    finally:
        await monitor.cleanup()


async def main():
    """
    import argparse
    
    parser = argparse.ArgumentParser(description='kcloud-opt 데이터베이스 통합 모니터링')
    parser.add_argument('--mode', choices=['continuous', 'once', 'test'], default='continuous')
    parser.add_argument('--interval', type=int, default=30, help='업데이트 주기(초)')
    parser.add_argument('--clusters', nargs='+', default=['kcloud-dev-cluster'])
    parser.add_argument('--no-database', action='store_true', help='데이터베이스 없이 실행')
    
    args = parser.parse_args()
    
    print(" kcloud-opt 데이터베이스 통합 모니터링 시스템")
    print("=" * 60)
    
    use_database = not args.no_database
    
    async with database_monitoring_context(args.clusters, args.interval, use_database) as monitor:
        if args.mode == 'continuous':
            await monitor.run_continuous_monitoring(args.clusters)
        elif args.mode == 'once':
            summary = await monitor.monitor_clusters_enhanced(args.clusters)
            monitor._print_monitoring_summary(summary)
        elif args.mode == 'test':
            print("🧪 시스템 테스트 모드")
            summary = await monitor.monitor_clusters_enhanced(args.clusters)
            print(f" 테스트 결과: {len(summary['clusters'])}개 클러스터 모니터링 완료")
            if monitor.db_manager:
                health = await monitor.db_manager.health_check()
                print(f"🏥 데이터베이스 상태: {health}")


if __name__ == "__main__":
    asyncio.run(main())
