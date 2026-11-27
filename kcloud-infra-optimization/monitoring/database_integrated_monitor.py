#!/usr/bin/env python3
"""
kcloud-opt 데이터베이스 통합 모니터링 시스템
PostgreSQL + TimescaleDB + Redis를 활용한 완전한 모니터링 솔루션
"""

import sys
import json
import asyncio
import logging
from datetime import datetime, timedelta
from typing import Dict, List, Optional, Any
from contextlib import asynccontextmanager

# 패키지 import - 환경에 설치되어 있거나 PYTHONPATH에 있어야 합니다
try:
    from infrastructure.database.connection import get_database_manager, init_database, close_database
    from infrastructure.monitoring.enhanced_metrics_collector import EnhancedMetricsCollector, EnhancedClusterMetrics
    from infrastructure.monitoring.enhanced_alert_system import EnhancedAlertSystem, EnhancedAlert
    from infrastructure.database.redis_keys import RedisKeys, RedisPubSubChannels, RedisDataTypes
    from infrastructure.monitoring.realtime_dashboard import RealTimeDashboard
except ImportError:
    # 상대 import 시도
    try:
        from database.connection import get_database_manager, init_database, close_database
        from enhanced_metrics_collector import EnhancedMetricsCollector, EnhancedClusterMetrics
        from enhanced_alert_system import EnhancedAlertSystem, EnhancedAlert
        from database.redis_keys import RedisKeys, RedisPubSubChannels, RedisDataTypes
        from realtime_dashboard import RealTimeDashboard
    except ImportError:
        raise ImportError("Required modules not found. Please ensure they're in PYTHONPATH or install the package")

# 폴백용 IntegratedMonitor (선택적)
try:
    from infrastructure.monitoring.integrated_monitor import IntegratedMonitor
except ImportError:
    try:
        from .integrated_monitor import IntegratedMonitor
    except ImportError:
        IntegratedMonitor = None  # 폴백 시스템에서는 선택적

# 로깅 설정
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

class DatabaseIntegratedMonitor:
    """데이터베이스 통합 모니터링 시스템"""
    
    def __init__(self, update_interval: int = 30, use_database: bool = True):
        self.update_interval = update_interval
        self.use_database = use_database
        self.running = False
        
        # 컴포넌트 초기화
        self.db_manager = None
        self.metrics_collector = None
        self.alert_system = None
        self.dashboard = None
        
        # 폴백 시스템 (DB 연결 실패 시)
        self.fallback_monitor = None
        
        # 상태 관리
        self.last_health_check = None
        self.error_count = 0
        self.max_errors = 5
    
    async def initialize(self):
        """시스템 초기화"""
        try:
            logger.info("🚀 데이터베이스 통합 모니터링 시스템 초기화")
            
            if self.use_database:
                # 데이터베이스 연결 시도
                await self._initialize_database_components()
            else:
                # 폴백 시스템으로 초기화
                await self._initialize_fallback_system()
            
            logger.info("✅ 모니터링 시스템 초기화 완료")
            
        except Exception as e:
            logger.error(f"[ERROR] 초기화 실패: {e}")
            if self.use_database:
                logger.info("🔄 폴백 시스템으로 전환")
                await self._initialize_fallback_system()
            else:
                raise
    
    async def _initialize_database_components(self):
        """데이터베이스 연결 시스템 초기화"""
        try:
            # 데이터베이스 연결
            self.db_manager = await init_database()
            
            # 향상된 컴포넌트 초기화
            self.metrics_collector = EnhancedMetricsCollector(self.db_manager)
            self.alert_system = EnhancedAlertSystem(self.db_manager)
            await self.alert_system.initialize()
            
            # 대시보드 (기존 시스템 재사용)
            self.dashboard = RealTimeDashboard(self.update_interval)
            
            logger.info("🗄️ 데이터베이스 통합 시스템 활성화")
            
        except Exception as e:
            logger.error(f"데이터베이스 컴포넌트 초기화 실패: {e}")
            raise
    
    async def _initialize_fallback_system(self):
        """폴백 시스템 초기화"""
        try:
            self.fallback_monitor = IntegratedMonitor(self.update_interval)
            logger.info("🔄 폴백 시스템 활성화")
        except Exception as e:
            logger.error(f"폴백 시스템 초기화 실패: {e}")
            raise
    
    async def monitor_clusters_enhanced(self, cluster_names: List[str]) -> Dict[str, Any]:
        """향상된 클러스터 모니터링"""
        if not self.use_database or not self.db_manager:
            return await self._monitor_clusters_fallback(cluster_names)
        
        try:
            logger.info(f" 향상된 모니터링: {len(cluster_names)}개 클러스터")
            
            # 비동기 메트릭 수집
            metrics_list = await self.metrics_collector.collect_multiple_clusters_async(cluster_names)
            
            # 알림 처리
            all_alerts = []
            for metrics in metrics_list:
                alerts = await self.alert_system.process_metrics_alerts(metrics)
                all_alerts.extend(alerts)
            
            # 요약 생성
            summary = await self._generate_enhanced_summary(metrics_list, all_alerts)
            
            # 대시보드 캐시 업데이트
            await self._update_dashboard_cache(summary)
            
            self.error_count = 0  # 성공 시 에러 카운트 리셋
            return summary
            
        except Exception as e:
            logger.error(f"향상된 모니터링 실패: {e}")
            self.error_count += 1
            
            # 에러가 많으면 폴백 시스템으로 전환
            if self.error_count >= self.max_errors:
                logger.warning("🔄 에러 한계 도달 - 폴백 시스템으로 전환")
                return await self._monitor_clusters_fallback(cluster_names)
            
            return await self._generate_error_summary(str(e))
    
    async def _monitor_clusters_fallback(self, cluster_names: List[str]) -> Dict[str, Any]:
        """폴백 모니터링"""
        if not self.fallback_monitor:
            await self._initialize_fallback_system()
        
        logger.info(f" 폴백 모니터링: {len(cluster_names)}개 클러스터")
        cluster_metrics = self.fallback_monitor.monitor_clusters(cluster_names)
        
        # 기존 형식을 새 형식으로 변환
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
        """향상된 요약 생성"""
        total_cost = sum(m.cost_per_hour for m in metrics_list)
        total_power = sum(m.power_consumption_watts for m in metrics_list)
        active_clusters = len([m for m in metrics_list if m.status == 'CREATE_COMPLETE'])
        
        # 알림 요약
        alert_summary = await self.alert_system.get_alert_summary()
        
        # 성능 분석
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
        """클러스터 성능 분석"""
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
        """스마트 권장사항 생성"""
        recommendations = []
        active_metrics = [m for m in metrics_list if m.status == 'CREATE_COMPLETE']
        
        if not active_metrics:
            return ["현재 활성 클러스터가 없습니다"]
        
        # 비용 분석
        high_cost = [m for m in active_metrics if m.cost_per_hour > 15.0]
        if high_cost:
            total_potential_savings = sum(m.cost_per_hour * 0.3 for m in high_cost)
            recommendations.append(f"고비용 클러스터 {len(high_cost)}개 최적화로 월 ${total_potential_savings * 24 * 30:.0f} 절약 가능")
        
        # 성능 분석
        low_cpu = [m for m in active_metrics if m.cpu_usage < 20.0]
        if len(low_cpu) > 1:
            recommendations.append(f"저활용 클러스터 {len(low_cpu)}개 통합으로 인프라 비용 절약 검토")
        
        # GPU 분석
        gpu_clusters = [m for m in active_metrics if m.gpu_usage > 0]
        if gpu_clusters:
            avg_gpu = sum(m.gpu_usage for m in gpu_clusters) / len(gpu_clusters)
            if avg_gpu < 30:
                recommendations.append(f"GPU 활용률 {avg_gpu:.1f}% - GPU 노드 수 조정 권장")
        
        # 알림 기반 권장사항
        critical_alerts = [a for a in alerts if a.severity == 'CRITICAL']
        if critical_alerts:
            recommendations.append(f"긴급 알림 {len(critical_alerts)}개 - 즉시 대응 필요")
        
        # 헬스 기반 권장사항
        unhealthy = [m for m in active_metrics if m.health_score < 70]
        if unhealthy:
            recommendations.append(f"헬스 문제 클러스터 {len(unhealthy)}개 - 장애 예방 점검 권장")
        
        if not recommendations:
            recommendations.append("현재 시스템 상태 양호 - 정기 모니터링 유지")
        
        return recommendations
    
    async def _get_database_stats(self) -> Dict[str, Any]:
        """데이터베이스 통계"""
        if not self.db_manager or not self.db_manager.is_connected:
            return {'status': 'disconnected'}
        
        try:
            # PostgreSQL 통계
            pg_stats = await self.db_manager.execute_query(
                """
                SELECT 
                    (SELECT count(*) FROM cluster_metrics WHERE time >= NOW() - INTERVAL '1 hour') as metrics_1h,
                    (SELECT count(*) FROM clusters) as total_clusters,
                    (SELECT count(*) FROM alerts WHERE triggered_at >= NOW() - INTERVAL '24 hours') as alerts_24h,
                    (SELECT pg_database_size(current_database())) as db_size_bytes
                """,
                fetch='one'
            )
            
            # Redis 통계
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
            logger.error(f"데이터베이스 통계 조회 실패: {e}")
            return {'status': 'error', 'message': str(e)}
    
    async def _update_dashboard_cache(self, summary: Dict[str, Any]):
        """대시보드 캐시 업데이트"""
        try:
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
            logger.error(f"대시보드 캐시 업데이트 실패: {e}")
    
    async def run_continuous_monitoring(self, cluster_names: List[str]):
        """연속 모니터링 실행"""
        logger.info(f" 데이터베이스 통합 연속 모니터링 시작")
        logger.info(f" 모니터링 대상: {len(cluster_names)}개 클러스터")
        logger.info(f"⏱️  업데이트 주기: {self.update_interval}초")
        print("종료하려면 Ctrl+C를 누르세요\n")
        
        self.running = True
        
        try:
            while self.running:
                print(f"\n{'='*80}")
                print(f" 모니터링 업데이트: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}")
                if self.use_database and self.db_manager:
                    print(" 모드: 데이터베이스 통합")
                else:
                    print(" 모드: 폴백 시스템")
                print('='*80)
                
                # 모니터링 실행
                summary = await self.monitor_clusters_enhanced(cluster_names)
                
                # 결과 출력
                self._print_monitoring_summary(summary)
                
                # 헬스 체크
                if datetime.now() - (self.last_health_check or datetime.min) > timedelta(minutes=5):
                    await self._perform_health_check()
                
                # 다음 업데이트까지 대기
                print(f"\n {self.update_interval}초 후 다음 업데이트...")
                await asyncio.sleep(self.update_interval)
                
        except KeyboardInterrupt:
            print(f"\n\n 모니터링 중지됨")
            self.running = False
        except Exception as e:
            print(f"\n[ERROR] 모니터링 오류: {e}")
            logger.error(f"연속 모니터링 실패: {e}")
            self.running = False
    
    def _print_monitoring_summary(self, summary: Dict[str, Any]):
        """모니터링 요약 출력"""
        if not summary.get('clusters'):
            print("[ERROR] 수집된 메트릭이 없습니다")
            return
        
        # 기본 정보
        summary_data = summary['summary']
        print(f"\n 클러스터 상태:")
        print(f"  활성: {summary_data['active_clusters']}/{summary_data['total_clusters']}개")
        print(f"   총 비용: ${summary_data['total_cost_per_hour']:.2f}/시간")
        print(f"   예상 월비용: ${summary_data['total_cost_per_hour'] * 24 * 30:.0f}")
        print(f"   총 전력: {summary_data['total_power_consumption']:.0f}W")
        
        if summary_data['active_clusters'] > 0:
            print(f"   평균 헬스: {summary_data.get('avg_health_score', 0):.1f}/100")
            print(f"   평균 효율성: {summary_data.get('avg_efficiency_score', 0):.1f}/100")
        
        # 알림 정보
        alerts = summary['alerts']
        if alerts['total_active'] > 0:
            print(f"\n[ALERT] 활성 알림: {alerts['total_active']}개")
            print(f"  CRITICAL: {alerts['by_severity']['CRITICAL']}개")
            print(f"  WARNING: {alerts['by_severity']['WARNING']}개")
            print(f"  INFO: {alerts['by_severity']['INFO']}개")
        else:
            print(f"\n[OK] 활성 알림 없음")
        
        # 성능 분석 (향상된 모드만)
        if 'performance' in summary:
            perf = summary['performance']
            if 'cpu' in perf:
                print(f"\n 성능 분석:")
                print(f"  CPU 평균: {perf['cpu']['avg']:.1f}% (최대: {perf['cpu']['max']:.1f}%)")
                print(f"  메모리 평균: {perf['memory']['avg']:.1f}% (최대: {perf['memory']['max']:.1f}%)")
        
        # 데이터베이스 통계 (향상된 모드만)
        if 'database_stats' in summary and summary['database_stats'].get('status') == 'connected':
            db_stats = summary['database_stats']
            print(f"\n 데이터베이스:")
            print(f"  메트릭 (1시간): {db_stats['postgresql']['metrics_last_hour']}개")
            print(f"  Redis 메모리: {db_stats['redis']['memory_used_mb']}MB")
        
        # 권장사항
        recommendations = summary.get('recommendations', [])
        if recommendations:
            print(f"\n 권장사항:")
            for rec in recommendations[:3]:  # 상위 3개만
                print(f"  - {rec}")
    
    async def _perform_health_check(self):
        """시스템 헬스 체크"""
        try:
            if self.db_manager:
                health = await self.db_manager.health_check()
                if not health['postgres'] or not health['redis']:
                    logger.warning(f"데이터베이스 헬스 체크 경고: {health}")
            
            self.last_health_check = datetime.now()
            
        except Exception as e:
            logger.error(f"헬스 체크 실패: {e}")
    
    async def cleanup(self):
        """리소스 정리"""
        logger.info("🧹 시스템 정리 중...")
        self.running = False
        
        if self.db_manager:
            await close_database()
        
        logger.info("✅ 정리 완료")
    
    # 유틸리티 메서드들 (기존 시스템 호환성)
    def _generate_basic_summary(self, cluster_metrics: Dict) -> Dict[str, Any]:
        """기본 요약 생성 (폴백용)"""
        return {
            'total_cost_per_hour': sum(m.cost_per_hour for m in cluster_metrics.values()),
            'total_power_consumption': sum(m.power_consumption_watts for m in cluster_metrics.values()),
            'active_clusters': len([m for m in cluster_metrics.values() if m.status == 'CREATE_COMPLETE']),
            'total_clusters': len(cluster_metrics)
        }
    
    def _generate_basic_recommendations(self, cluster_metrics: Dict) -> List[str]:
        """기본 권장사항 생성 (폴백용)"""
        active_metrics = [m for m in cluster_metrics.values() if m.status == 'CREATE_COMPLETE']
        
        if not active_metrics:
            return ["현재 활성 클러스터가 없습니다"]
        
        recommendations = []
        
        high_cost = [m for m in active_metrics if m.cost_per_hour > 10.0]
        if high_cost:
            recommendations.append(f"높은 비용 클러스터 {len(high_cost)}개 최적화 필요")
        
        if not recommendations:
            recommendations.append("현재 최적화 상태 양호")
        
        return recommendations
    
    async def _generate_error_summary(self, error_msg: str) -> Dict[str, Any]:
        """에러 요약 생성"""
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
            'recommendations': ["시스템 오류로 인해 모니터링 일시 중단"]
        }


# 컨텍스트 매니저
@asynccontextmanager
async def database_monitoring_context(cluster_names: List[str], 
                                    update_interval: int = 30,
                                    use_database: bool = True):
    """데이터베이스 통합 모니터링 컨텍스트"""
    monitor = DatabaseIntegratedMonitor(update_interval, use_database)
    try:
        await monitor.initialize()
        yield monitor
    finally:
        await monitor.cleanup()


async def main():
    """메인 실행 함수"""
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