#!/usr/bin/env python3
"""
kcloud-opt 알림 시스템
비용, 성능, 헬스 상태 기반 알림
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
    """알림 규칙"""
    name: str
    condition: str  # 조건 (예: "cost_per_hour > 20")
    severity: str   # INFO, WARNING, CRITICAL
    message_template: str
    cooldown_minutes: int = 5  # 동일 알림 재발송 방지 시간
    enabled: bool = True

@dataclass
class Alert:
    """알림 데이터"""
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
    """알림 시스템 클래스"""
    
    def __init__(self):
        self.alert_rules = []
        self.active_alerts = []
        self.alert_history = []
        self.last_alert_time = defaultdict(datetime)  # 쿨다운 관리
        self.notification_handlers = []
        
        # 기본 알림 규칙 설정
        self.setup_default_rules()
        
    def setup_default_rules(self):
        """기본 알림 규칙 설정"""
        default_rules = [
            AlertRule(
                name="high_cost",
                condition="cost_per_hour > 20.0",
                severity="WARNING",
                message_template="높은 비용 감지: {cluster_name} - ${cost_per_hour:.2f}/시간",
                cooldown_minutes=10
            ),
            AlertRule(
                name="very_high_cost", 
                condition="cost_per_hour > 50.0",
                severity="CRITICAL",
                message_template="매우 높은 비용: {cluster_name} - ${cost_per_hour:.2f}/시간 즉시 확인 필요!",
                cooldown_minutes=5
            ),
            AlertRule(
                name="low_health",
                condition="health_score < 50.0 and status == 'CREATE_COMPLETE'",
                severity="WARNING", 
                message_template="헬스 문제: {cluster_name} - {health_score:.1f}/100점",
                cooldown_minutes=15
            ),
            AlertRule(
                name="critical_health",
                condition="health_score < 20.0 and status == 'CREATE_COMPLETE'",
                severity="CRITICAL",
                message_template="심각한 헬스 문제: {cluster_name} - {health_score:.1f}/100점",
                cooldown_minutes=5
            ),
            AlertRule(
                name="failed_pods",
                condition="failed_pods > 0",
                severity="WARNING",
                message_template="실패한 포드 감지: {cluster_name} - {failed_pods}개",
                cooldown_minutes=10
            ),
            AlertRule(
                name="many_failed_pods",
                condition="failed_pods > 5",
                severity="CRITICAL", 
                message_template="다수 포드 실패: {cluster_name} - {failed_pods}개 포드 실패",
                cooldown_minutes=5
            ),
            AlertRule(
                name="high_cpu",
                condition="cpu_usage > 90.0 and status == 'CREATE_COMPLETE'",
                severity="WARNING",
                message_template="높은 CPU 사용률: {cluster_name} - {cpu_usage:.1f}%",
                cooldown_minutes=15
            ),
            AlertRule(
                name="high_memory",
                condition="memory_usage > 90.0 and status == 'CREATE_COMPLETE'",
                severity="WARNING",
                message_template="높은 메모리 사용률: {cluster_name} - {memory_usage:.1f}%",
                cooldown_minutes=15
            ),
            AlertRule(
                name="low_efficiency",
                condition="efficiency_score < 30.0 and status == 'CREATE_COMPLETE'",
                severity="INFO",
                message_template="낮은 효율성: {cluster_name} - {efficiency_score:.1f}/100 최적화 필요",
                cooldown_minutes=30
            ),
            AlertRule(
                name="cluster_creation_failed",
                condition="status == 'CREATE_FAILED'",
                severity="CRITICAL",
                message_template="클러스터 생성 실패: {cluster_name}",
                cooldown_minutes=0  # 즉시 알림
            ),
            AlertRule(
                name="high_power_consumption",
                condition="power_consumption_watts > 5000.0",
                severity="INFO",
                message_template="높은 전력 소비: {cluster_name} - {power_consumption_watts:.0f}W",
                cooldown_minutes=60
            )
        ]
        
        self.alert_rules = default_rules
        print(f"[OK] 기본 알림 규칙 {len(default_rules)}개 설정 완료")
    
    def add_rule(self, rule: AlertRule):
        """알림 규칙 추가"""
        self.alert_rules.append(rule)
        print(f"📝 알림 규칙 추가: {rule.name}")
    
    def remove_rule(self, rule_name: str):
        """알림 규칙 제거"""
        self.alert_rules = [r for r in self.alert_rules if r.name != rule_name]
        print(f" 알림 규칙 제거: {rule_name}")
    
    def evaluate_conditions(self, metrics: ClusterMetrics) -> List[Alert]:
        """메트릭에 대해 알림 조건 평가"""
        triggered_alerts = []
        current_time = datetime.now()
        
        for rule in self.alert_rules:
            if not rule.enabled:
                continue
            
            try:
                # 조건 평가를 위한 변수 준비
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
                
                # 조건 평가
                if eval(rule.condition, {"__builtins__": {}}, eval_vars):
                    # 쿨다운 체크
                    alert_key = f"{rule.name}_{metrics.cluster_name}"
                    last_alert = self.last_alert_time.get(alert_key, datetime.min)
                    
                    if current_time - last_alert >= timedelta(minutes=rule.cooldown_minutes):
                        # 알림 생성
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
                print(f"[WARNING] 알림 규칙 '{rule.name}' 평가 실패: {e}")
        
        return triggered_alerts
    
    def process_metrics(self, metrics: ClusterMetrics):
        """메트릭 처리 및 알림 생성"""
        triggered_alerts = self.evaluate_conditions(metrics)
        
        for alert in triggered_alerts:
            self.active_alerts.append(alert)
            self.alert_history.append(alert)
            
            print(f"[ALERT] [{alert.severity}] {alert.message}")
            
            # 알림 핸들러 실행
            for handler in self.notification_handlers:
                try:
                    handler(alert)
                except Exception as e:
                    print(f"[ERROR] 알림 핸들러 실행 실패: {e}")
        
        # 활성 알림 정리 (해결된 알림 제거)
        self.cleanup_resolved_alerts()
        
        return triggered_alerts
    
    def cleanup_resolved_alerts(self):
        """해결된 알림 정리"""
        # 24시간 이전 알림은 자동 해결로 처리
        cutoff_time = datetime.now() - timedelta(hours=24)
        
        for alert in self.active_alerts:
            alert_time = datetime.fromisoformat(alert.timestamp.replace('Z', ''))
            if alert_time < cutoff_time:
                alert.resolved = True
        
        # 해결된 알림 제거
        self.active_alerts = [a for a in self.active_alerts if not a.resolved]
    
    def acknowledge_alert(self, alert_id: str):
        """알림 확인 처리"""
        for alert in self.active_alerts:
            if alert.id == alert_id:
                alert.acknowledged = True
                print(f"[OK] 알림 확인: {alert_id}")
                return True
        return False
    
    def resolve_alert(self, alert_id: str):
        """알림 해결 처리"""
        for alert in self.active_alerts:
            if alert.id == alert_id:
                alert.resolved = True
                print(f"[OK] 알림 해결: {alert_id}")
                return True
        return False
    
    def get_active_alerts(self, severity: Optional[str] = None) -> List[Alert]:
        """활성 알림 조회"""
        alerts = [a for a in self.active_alerts if not a.resolved]
        
        if severity:
            alerts = [a for a in alerts if a.severity == severity]
        
        return alerts
    
    def get_alert_summary(self) -> Dict:
        """알림 요약 반환"""
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
        
        # 클러스터별 알림 개수
        for alert in active_alerts:
            cluster = alert.cluster_name
            if cluster not in summary['by_cluster']:
                summary['by_cluster'][cluster] = 0
            summary['by_cluster'][cluster] += 1
        
        return summary
    
    def add_notification_handler(self, handler: Callable[[Alert], None]):
        """알림 핸들러 추가"""
        self.notification_handlers.append(handler)
        print(f"📢 알림 핸들러 추가됨")
    
    def save_alert_history(self, filename: Optional[str] = None):
        """알림 히스토리 저장"""
        if not filename:
            timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
            filename = f"alert_history_{timestamp}.json"
        
        data = {
            'export_time': datetime.now().isoformat(),
            'alert_count': len(self.alert_history),
            'alerts': [alert.to_dict() for alert in self.alert_history]
        }
        
        with open(filename, 'w') as f:
            json.dump(data, f, indent=2)
        
        print(f" 알림 히스토리 저장: {filename}")

# 알림 핸들러 예시들
def console_handler(alert: Alert):
    """콘솔 출력 핸들러"""
    severity_icons = {
        'INFO': 'ℹ️',
        'WARNING': '⚠️', 
        'CRITICAL': '🚨'
    }
    
    icon = severity_icons.get(alert.severity, '❓')
    timestamp = datetime.fromisoformat(alert.timestamp).strftime('%H:%M:%S')
    print(f"{icon} [{timestamp}] {alert.message}")

def file_handler(alert: Alert):
    """파일 로그 핸들러"""
    timestamp = datetime.now().strftime('%Y-%m-%d %H:%M:%S')
    log_entry = f"[{timestamp}] [{alert.severity}] {alert.cluster_name}: {alert.message}\n"
    
    with open('kcloud_alerts.log', 'a') as f:
        f.write(log_entry)

def webhook_handler(alert: Alert):
    """웹훅 핸들러 (Slack, Discord 등)"""
    # 실제 구현에서는 requests 라이브러리 사용
    print(f" 웹훅 전송: {alert.message}")

def main():
    """알림 시스템 테스트"""
    print("[ALERT] kcloud-opt 알림 시스템 테스트")
    print("=" * 40)
    
    # 알림 시스템 초기화
    alert_system = AlertSystem()
    
    # 핸들러 추가
    alert_system.add_notification_handler(console_handler)
    alert_system.add_notification_handler(file_handler)
    
    # 테스트용 메트릭
    test_metrics = ClusterMetrics(
        cluster_name="test-cluster",
        timestamp=datetime.now().isoformat(),
        status="CREATE_COMPLETE",
        health_score=25.0,  # 낮은 헬스 (알림 트리거)
        cost_per_hour=25.0,  # 높은 비용 (알림 트리거)
        failed_pods=3,       # 실패한 포드 (알림 트리거)
        cpu_usage=95.0,      # 높은 CPU (알림 트리거)
        efficiency_score=20.0, # 낮은 효율성 (알림 트리거)
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
    
    # 알림 요약
    summary = alert_system.get_alert_summary()
    print(f"\n 알림 요약:")
    print(f"  활성 알림: {summary['total_active']}개")
    print(f"  CRITICAL: {summary['by_severity']['CRITICAL']}개")
    print(f"  WARNING: {summary['by_severity']['WARNING']}개")
    print(f"  INFO: {summary['by_severity']['INFO']}개")
    
    # 히스토리 저장
    alert_system.save_alert_history()
    
    print(f"\n[OK] 알림 시스템 테스트 완료")

if __name__ == "__main__":
    main()