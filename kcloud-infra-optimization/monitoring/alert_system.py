#!/usr/bin/env python3
"""
"""

import sys
import time
import json
import smtplib
from datetime import datetime, timedelta
from typing import Dict, List, Optional, Callable
from dataclasses import dataclass, asdict
from collections import defaultdict
from email.mime.text import MIMEText
from email.mime.multipart import MIMEMultipart

try:
    from infrastructure.monitoring.metrics_collector import ClusterMetrics
except ImportError:
    try:
        from .metrics_collector import ClusterMetrics
    except ImportError:
        raise ImportError("ClusterMetrics not found. Please ensure it's in PYTHONPATH")

@dataclass
class AlertRule:
    """
    name: str
    condition: str
    severity: str   # INFO, WARNING, CRITICAL
    message_template: str
    cooldown_minutes: int = 5
    enabled: bool = True

@dataclass
class Alert:
    """
    id: str
    rule_name: str
    cluster_name: str
    severity: str
    message: str
    timestamp: str
    acknowledged: bool = False
    resolved: bool = False
    
    def to_dict(self) -> Dict:
        return asdict(self)

class AlertSystem:
    """
    
    def __init__(self):
        self.alert_rules = []
        self.active_alerts = []
        self.alert_history = []
        self.last_alert_time = defaultdict(datetime)
        self.notification_handlers = []
        

        self.setup_default_rules()
        
    def setup_default_rules(self):
        """
        default_rules = [
            AlertRule(
                name="high_cost",
                condition="cost_per_hour > 20.0",
                severity="WARNING",
                cooldown_minutes=10
            ),
            AlertRule(
                name="very_high_cost",
                condition="cost_per_hour > 50.0",
                severity="CRITICAL",
                cooldown_minutes=5
            ),
            AlertRule(
                name="low_health",
                condition="health_score < 50.0 and status == 'CREATE_COMPLETE'",
                severity="WARNING",
                cooldown_minutes=15
            ),
            AlertRule(
                name="critical_health",
                condition="health_score < 20.0 and status == 'CREATE_COMPLETE'",
                severity="CRITICAL",
                cooldown_minutes=5
            ),
            AlertRule(
                name="failed_pods",
                condition="failed_pods > 0",
                severity="WARNING",
                cooldown_minutes=10
            ),
            AlertRule(
                name="many_failed_pods",
                condition="failed_pods > 5",
                severity="CRITICAL",
                cooldown_minutes=5
            ),
            AlertRule(
                name="high_cpu",
                condition="cpu_usage > 90.0 and status == 'CREATE_COMPLETE'",
                severity="WARNING",
                cooldown_minutes=15
            ),
            AlertRule(
                name="high_memory",
                condition="memory_usage > 90.0 and status == 'CREATE_COMPLETE'",
                severity="WARNING",
                cooldown_minutes=15
            ),
            AlertRule(
                name="low_efficiency",
                condition="efficiency_score < 30.0 and status == 'CREATE_COMPLETE'",
                severity="INFO",
                cooldown_minutes=30
            ),
            AlertRule(
                name="cluster_creation_failed",
                condition="status == 'CREATE_FAILED'",
                severity="CRITICAL",
                cooldown_minutes=0
            ),
            AlertRule(
                name="high_power_consumption",
                condition="power_consumption_watts > 5000.0",
                severity="INFO",
                cooldown_minutes=60
            )
        ]
        self.alert_rules = default_rules
    def add_rule(self, rule: AlertRule):
"""
        """self.alert_rules.append(rule)
    
    def remove_rule(self, rule_name: str):
"""
        self.alert_rules = [r for r in self.alert_rules if r.name != rule_name]
    def evaluate_conditions(self, metrics: ClusterMetrics) -> List[Alert]:
"""
        """triggered_alerts = []
        current_time = datetime.now()
        
        for rule in self.alert_rules:
            if not rule.enabled:
                continue
            
            try:

                eval_vars = {
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
                

                if eval(rule.condition, {"__builtins__": {}}, eval_vars):

                    alert_key = f"{rule.name}_{metrics.cluster_name}"
                    last_alert = self.last_alert_time.get(alert_key, datetime.min)
                    
                    if current_time - last_alert >= timedelta(minutes=rule.cooldown_minutes):

                        alert_message = rule.message_template.format(**eval_vars)
                        
                        alert = Alert(
                            id=f"{alert_key}_{int(current_time.timestamp())}",
                            rule_name=rule.name,
                            cluster_name=metrics.cluster_name,
                            severity=rule.severity,
                            message=alert_message,
                            timestamp=current_time.isoformat()
                        )
                        
                        triggered_alerts.append(alert)
                        self.last_alert_time[alert_key] = current_time
                        
            except Exception as e:
        
        return triggered_alerts
    
    def process_metrics(self, metrics: ClusterMetrics):
"""
        triggered_alerts = self.evaluate_conditions(metrics)
        for alert in triggered_alerts:
            self.active_alerts.append(alert)
            self.alert_history.append(alert)
            print(f"[ALERT] [{alert.severity}] {alert.message}")
            for handler in self.notification_handlers:
                try:
                    handler(alert)
                except Exception as e:
        self.cleanup_resolved_alerts()
        return triggered_alerts
    def cleanup_resolved_alerts(self):
"""
        """

        cutoff_time = datetime.now() - timedelta(hours=24)
        
        for alert in self.active_alerts:
            alert_time = datetime.fromisoformat(alert.timestamp.replace('Z', ''))
            if alert_time < cutoff_time:
                alert.resolved = True
        

        self.active_alerts = [a for a in self.active_alerts if not a.resolved]
    
    def acknowledge_alert(self, alert_id: str):
        """
        for alert in self.active_alerts:
            if alert.id == alert_id:
                alert.acknowledged = True
                return True
        return False
    def resolve_alert(self, alert_id: str):
"""
        """for alert in self.active_alerts:
            if alert.id == alert_id:
                alert.resolved = True
                return True
        return False
    
    def get_active_alerts(self, severity: Optional[str] = None) -> List[Alert]:
"""
        alerts = [a for a in self.active_alerts if not a.resolved]
        
        if severity:
            alerts = [a for a in alerts if a.severity == severity]
        
        return alerts
    
    def get_alert_summary(self) -> Dict:
        """
        active_alerts = self.get_active_alerts()
        
        summary = {
            'timestamp': datetime.now().isoformat(),
            'total_active': len(active_alerts),
            'by_severity': {
                'CRITICAL': len([a for a in active_alerts if a.severity == 'CRITICAL']),
                'WARNING': len([a for a in active_alerts if a.severity == 'WARNING']),
                'INFO': len([a for a in active_alerts if a.severity == 'INFO'])
            },
            'by_cluster': {},
            'recent_alerts': [a.to_dict() for a in active_alerts[-10:]]
        }
        

        for alert in active_alerts:
            cluster = alert.cluster_name
            if cluster not in summary['by_cluster']:
                summary['by_cluster'][cluster] = 0
            summary['by_cluster'][cluster] += 1
        
        return summary
    
    def add_notification_handler(self, handler: Callable[[Alert], None]):
        """
        self.notification_handlers.append(handler)
    def save_alert_history(self, filename: Optional[str] = None):
"""
        """if not filename:
            timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
            filename = f"alert_history_{timestamp}.json"
        
        data = {
            'export_time': datetime.now().isoformat(),
            'alert_count': len(self.alert_history),
            'alerts': [alert.to_dict() for alert in self.alert_history]
        }
        
        with open(filename, 'w') as f:
            json.dump(data, f, indent=2)
        

def console_handler(alert: Alert):
"""
    severity_icons = {
        'INFO': 'ℹ️',
        'WARNING': '⚠️',
        'CRITICAL': '🚨'
    }
    
    icon = severity_icons.get(alert.severity, '❓')
    timestamp = datetime.fromisoformat(alert.timestamp).strftime('%H:%M:%S')
    print(f"{icon} [{timestamp}] {alert.message}")

def file_handler(alert: Alert):
    """
    timestamp = datetime.now().strftime('%Y-%m-%d %H:%M:%S')
    log_entry = f"[{timestamp}] [{alert.severity}] {alert.cluster_name}: {alert.message}\n"
    
    with open('kcloud_alerts.log', 'a') as f:
        f.write(log_entry)

def webhook_handler(alert: Alert):
    """
def main():
"""
    """
    print("[ALERT] kcloud-opt 알림 시스템 테스트")
    print("=" * 40)
    

    alert_system = AlertSystem()
    

    alert_system.add_notification_handler(console_handler)
    alert_system.add_notification_handler(file_handler)
    

    test_metrics = ClusterMetrics(
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
        template_id="ai-k8s-template"
    )
    
    print(f"\n 테스트 메트릭 처리 중...")
    alerts = alert_system.process_metrics(test_metrics)
    
    print(f"\n 생성된 알림: {len(alerts)}개")
    for alert in alerts:
        print(f"  [ALERT] {alert.severity}: {alert.message}")
    

    summary = alert_system.get_alert_summary()
    print(f"\n 알림 요약:")
    print(f"  활성 알림: {summary['total_active']}개")
    print(f"  CRITICAL: {summary['by_severity']['CRITICAL']}개")
    print(f"  WARNING: {summary['by_severity']['WARNING']}개")
    print(f"  INFO: {summary['by_severity']['INFO']}개")
    

    alert_system.save_alert_history()
    
    print(f"\n[OK] 알림 시스템 테스트 완료")

if __name__ == "__main__":
    main()
