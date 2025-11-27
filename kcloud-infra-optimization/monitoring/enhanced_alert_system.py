#!/usr/bin/env python3
"""
"""
import sys
import json
import uuid
import asyncio
import logging
from datetime import datetime, timedelta
from typing import Dict, List, Optional, Any, Callable
from dataclasses import dataclass, asdict
try:
    from infrastructure.monitoring.alert_system import AlertSystem as BaseAlertSystem, Alert, AlertRule
    from infrastructure.monitoring.enhanced_metrics_collector import EnhancedClusterMetrics
    from infrastructure.database.connection import get_database_manager, DatabaseManager
    from infrastructure.database.redis_keys import RedisKeys, RedisPubSubChannels, RedisDataTypes, RedisExpirePolicy
except ImportError:
    try:
        from .alert_system import AlertSystem as BaseAlertSystem, Alert, AlertRule
        from .enhanced_metrics_collector import EnhancedClusterMetrics
        from database.connection import get_database_manager, DatabaseManager
        from database.redis_keys import RedisKeys, RedisPubSubChannels, RedisDataTypes, RedisExpirePolicy
    except ImportError:
        raise ImportError("Required modules not found. Please ensure they're in PYTHONPATH or install the package")
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)
@dataclass
class EnhancedAlert(Alert):
"""
    """
    

    alert_uuid: str = None
    rule_uuid: str = None
    cluster_uuid: str = None
    escalation_level: int = 0
    auto_resolve_at: Optional[str] = None
    notification_channels: List[str] = None
    
    def __post_init__(self):
        if not self.alert_uuid:
            self.alert_uuid = str(uuid.uuid4())
        if not self.notification_channels:
            self.notification_channels = []
    
    def to_db_dict(self) -> Dict[str, Any]:
        """
        return {
            'id': self.alert_uuid,
            'rule_name': self.rule_name,
            'cluster_name': self.cluster_name,
            'severity': self.severity,
            'message': self.message,
            'triggered_at': self.timestamp,
            'acknowledged': self.acknowledged,
            'resolved': self.resolved,
            'metadata': {
                'rule_uuid': self.rule_uuid,
                'cluster_uuid': self.cluster_uuid,
                'escalation_level': self.escalation_level,
                'auto_resolve_at': self.auto_resolve_at,
                'notification_channels': self.notification_channels,
                'original_id': self.id
            }
        }

class EnhancedAlertSystem:
    """
    
    def __init__(self, db_manager: DatabaseManager = None):
        self.base_system = BaseAlertSystem()
        self.db_manager = db_manager or get_database_manager()
        self.notification_handlers = []
        self.alert_rules_cache = {}
        self.last_rules_reload = None
        
    async def initialize(self):
        """
        try:
            await self._load_alert_rules_from_db()
            await self._setup_notification_handlers()
        except Exception as e:
            self.base_system.setup_default_rules()
    async def _load_alert_rules_from_db(self):
"""
        """try:
            if not self.db_manager.is_connected:
                self.base_system.setup_default_rules()
                return
            
            rules = await self.db_manager.execute_query(
                "SELECT * FROM alert_rules WHERE is_enabled = true ORDER BY name"
            )
            
            self.alert_rules_cache = {}
            for rule_data in rules:
                rule = AlertRule(
                    name=rule_data['name'],
                    condition=rule_data['condition_expression'],
                    severity=rule_data['severity'],
                    message_template=rule_data['message_template'],
                    cooldown_minutes=rule_data['cooldown_minutes'],
                    enabled=rule_data['is_enabled']
                )
                self.alert_rules_cache[rule.name] = {
                    'rule': rule,
                    'uuid': str(rule_data['id']),
                    'description': rule_data['description'],
                    'applies_to': rule_data['applies_to']
                }
            
            self.last_rules_reload = datetime.now()
            
        except Exception as e:

            self.base_system.setup_default_rules()
    
    async def _setup_notification_handlers(self):
"""
        self.notification_handlers = [
            self._console_handler,
            self._redis_handler,
            self._database_handler
        ]
    async def process_metrics_alerts(self, metrics: EnhancedClusterMetrics) -> List[EnhancedAlert]:
"""
        """try:

            await self._check_rules_reload()
            

            triggered_alerts = await self._evaluate_alert_conditions(metrics)
            

            for alert in triggered_alerts:
                await self._process_single_alert(alert)
            
            return triggered_alerts
            
        except Exception as e:
            return []
    
    async def _check_rules_reload(self):
"""
        if (not self.last_rules_reload or
            datetime.now() - self.last_rules_reload > timedelta(minutes=5)):
            await self._load_alert_rules_from_db()
    
    async def _evaluate_alert_conditions(self, metrics: EnhancedClusterMetrics) -> List[EnhancedAlert]:
        """triggered_alerts = []
        current_time = datetime.now()
        

        rules_to_check = self.alert_rules_cache if self.alert_rules_cache else self.base_system.alert_rules
        
        for rule_name, rule_data in (self.alert_rules_cache.items() if self.alert_rules_cache
                                   else [(r.name, {'rule': r, 'uuid': None}) for r in self.base_system.alert_rules]):
            
            rule = rule_data['rule']
            if not rule.enabled:
                continue
            
            try:

                cooldown_key = RedisKeys.alert_cooldown(rule.name, metrics.cluster_name)
                is_in_cooldown = await self.db_manager.redis_get(cooldown_key) is not None
                
                if is_in_cooldown:
                    continue
                

                eval_vars = self._prepare_eval_vars(metrics)
                if eval(rule.condition, {"__builtins__": {}}, eval_vars):

                    alert = self._create_enhanced_alert(rule, rule_data.get('uuid'), metrics, eval_vars)
                    triggered_alerts.append(alert)
                    

                    if rule.cooldown_minutes > 0:
                        await self.db_manager.redis_set(
                            cooldown_key,
                            current_time.isoformat(),
                            RedisExpirePolicy.alert_cooldown_ttl(rule.cooldown_minutes)
                        )
                
            except Exception as e:
        
        return triggered_alerts
    
    def _prepare_eval_vars(self, metrics: EnhancedClusterMetrics) -> Dict[str, Any]:
"""
        return {
            'cluster_name': metrics.cluster_name,
            'status': metrics.status,
            'cost_per_hour': metrics.cost_per_hour,
            'health_score': metrics.health_score,
            'efficiency_score': metrics.efficiency_score,
            'failed_pods': metrics.failed_pods,
            'pending_pods': metrics.pending_pods,
            'cpu_usage': metrics.cpu_usage,
            'memory_usage': metrics.memory_usage,
            'gpu_usage': metrics.gpu_usage,
            'power_consumption_watts': metrics.power_consumption_watts,
            'node_count': metrics.node_count
        }
    
    def _create_enhanced_alert(self, rule: AlertRule, rule_uuid: Optional[str],
                             metrics: EnhancedClusterMetrics, eval_vars: Dict) -> EnhancedAlert:
        """
        alert_message = rule.message_template.format(**eval_vars)
        
        return EnhancedAlert(
            id=f"{rule.name}_{metrics.cluster_name}_{int(datetime.now().timestamp())}",
            rule_name=rule.name,
            cluster_name=metrics.cluster_name,
            severity=rule.severity,
            message=alert_message,
            timestamp=datetime.now().isoformat(),
            rule_uuid=rule_uuid,
            cluster_uuid=metrics.cluster_id,
            notification_channels=['console', 'redis', 'database']
        )
    
    async def _process_single_alert(self, alert: EnhancedAlert):
        """
        try:
            for handler in self.notification_handlers:
                await handler(alert)
            logger.info(f"[ALERT] [{alert.severity}] {alert.cluster_name}: {alert.message}")
        except Exception as e:
    async def _console_handler(self, alert: EnhancedAlert):
"""
        """
        severity_icons = {
            'INFO': 'ℹ️',
            'WARNING': '⚠️',
            'CRITICAL': '🚨'
        }
        icon = severity_icons.get(alert.severity, '❓')
        timestamp = datetime.fromisoformat(alert.timestamp).strftime('%H:%M:%S')
        print(f"{icon} [{timestamp}] {alert.message}")
    
    async def _redis_handler(self, alert: EnhancedAlert):
        """
        try:
            alert_score = datetime.fromisoformat(alert.timestamp).timestamp()
            await self.db_manager.redis_client.zadd(
                RedisKeys.alerts_active(),
                {alert.alert_uuid: alert_score}
            )
            await self.db_manager.redis_client.sadd(
                RedisKeys.alerts_by_cluster(alert.cluster_name),
                alert.alert_uuid
            )
            await self.db_manager.redis_client.sadd(
                RedisKeys.alerts_by_severity(alert.severity),
                alert.alert_uuid
            )
            alert_detail_key = f"kcloud:alert:detail:{alert.alert_uuid}"
            await self.db_manager.redis_set(
                alert_detail_key,
                RedisDataTypes.create_alert_payload(
                    alert.alert_uuid, alert.cluster_name,
                    alert.severity, alert.message,
                    {'rule_name': alert.rule_name, 'timestamp': alert.timestamp}
                ),
                RedisExpirePolicy.ALERTS_ACTIVE
            )
            await self.db_manager.redis_publish(
                RedisPubSubChannels.ALERTS_NEW,
                RedisDataTypes.create_alert_payload(
                    alert.alert_uuid, alert.cluster_name,
                    alert.severity, alert.message
                )
            )
        except Exception as e:
    async def _database_handler(self, alert: EnhancedAlert):
"""
        """
        try:
            if not self.db_manager.is_connected:
                return
            

            await self.db_manager.execute_query(
                """
                INSERT INTO alerts (
                    id, rule_id, cluster_name, severity, message,
                    triggered_at, metadata
                ) VALUES (
                    $1, $2, $3, $4, $5, $6, $7
                ) ON CONFLICT (id) DO NOTHING
                """,
                alert.alert_uuid,
                alert.rule_uuid,
                alert.cluster_name,
                alert.severity,
                alert.message,
                alert.timestamp,
                json.dumps(alert.to_db_dict()['metadata'])
            )
            
        except Exception as e:
    
    async def get_active_alerts(self, cluster_name: str = None,
                              severity: str = None, limit: int = 100) -> List[Dict[str, Any]]:
"""
        try:
            if cluster_name:
                alert_ids = await self.db_manager.redis_client.smembers(
                    RedisKeys.alerts_by_cluster(cluster_name)
                )
            elif severity:
                alert_ids = await self.db_manager.redis_client.smembers(
                    RedisKeys.alerts_by_severity(severity)
                )
            else:
                alert_ids = await self.db_manager.redis_client.zrevrange(
                    RedisKeys.alerts_active(), 0, limit - 1
                )
            alerts = []
            for alert_id in alert_ids:
                detail_key = f"kcloud:alert:detail:{alert_id}"
                alert_data = await self.db_manager.redis_get(detail_key)
                if alert_data:
                    alerts.append(json.loads(alert_data))
            return alerts[:limit]
        except Exception as e:
            return []
    async def acknowledge_alert(self, alert_id: str, user_id: str = None) -> bool:
"""
        """
        try:

            await self.db_manager.redis_client.zrem(RedisKeys.alerts_active(), alert_id)
            

            if self.db_manager.is_connected:
                await self.db_manager.execute_query(
                    """
                    UPDATE alerts SET
                        acknowledged_at = NOW(),
                        acknowledged_by = $2
                    WHERE id = $1
                    """,
                    alert_id, user_id
                )
            
            return True
            
        except Exception as e:
            return False
    
    async def get_alert_summary(self) -> Dict[str, Any]:
"""
        try:
            total_active = await self.db_manager.redis_client.zcard(RedisKeys.alerts_active())
            summary = {
                'timestamp': datetime.now().isoformat(),
                'total_active': total_active,
                'by_severity': {
                    'CRITICAL': await self.db_manager.redis_client.scard(RedisKeys.alerts_by_severity('CRITICAL')),
                    'WARNING': await self.db_manager.redis_client.scard(RedisKeys.alerts_by_severity('WARNING')),
                    'INFO': await self.db_manager.redis_client.scard(RedisKeys.alerts_by_severity('INFO'))
                },
                'by_cluster': {},
                'recent_alerts': []
            }
            cluster_keys = await self.db_manager.redis_client.keys(RedisKeys.alerts_by_cluster("*"))
            for key in cluster_keys[:10]:
                cluster_name = key.split(":")[-1]
                count = await self.db_manager.redis_client.scard(key)
                if count > 0:
                    summary['by_cluster'][cluster_name] = count
            summary['recent_alerts'] = await self.get_active_alerts(limit=5)
            return summary
        except Exception as e:
            return {
                'timestamp': datetime.now().isoformat(),
                'total_active': 0,
                'by_severity': {'CRITICAL': 0, 'WARNING': 0, 'INFO': 0},
                'by_cluster': {},
                'recent_alerts': []
            }
async def test_enhanced_alert_system():
"""
    """
    print("[ALERT] 향상된 알림 시스템 테스트")
    print("=" * 50)
    
    try:

        alert_system = EnhancedAlertSystem()
        await alert_system.initialize()
        

        from infrastructure.monitoring.enhanced_metrics_collector import EnhancedClusterMetrics
        test_metrics = EnhancedClusterMetrics(
            cluster_name="test-cluster",
            timestamp=datetime.now().isoformat(),
            status="CREATE_COMPLETE",
            health_score=25.0,
            cost_per_hour=25.0,
            failed_pods=3,
            cpu_usage=95.0,
            efficiency_score=20.0,
            health_status="HEALTHY",
            node_count=2,
            master_count=1,
            template_id="test-template",
            cluster_id="test-cluster-id"
        )
        
        print(f"\n 테스트 메트릭으로 알림 처리 중...")
        alerts = await alert_system.process_metrics_alerts(test_metrics)
        
        print(f"\n 생성된 알림: {len(alerts)}개")
        for alert in alerts[:3]:
            print(f"  [ALERT] {alert.severity}: {alert.message}")
        

        summary = await alert_system.get_alert_summary()
        print(f"\n 알림 요약:")
        print(f"  활성 알림: {summary['total_active']}개")
        print(f"  CRITICAL: {summary['by_severity']['CRITICAL']}개")
        print(f"  WARNING: {summary['by_severity']['WARNING']}개")
        
        print(f"\n[OK] 향상된 알림 시스템 테스트 완료")
        
    except Exception as e:
        print(f"[ERROR] 테스트 실패: {e}")

if __name__ == "__main__":
    asyncio.run(test_enhanced_alert_system())
