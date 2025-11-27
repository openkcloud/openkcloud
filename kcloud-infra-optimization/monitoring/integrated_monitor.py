#!/usr/bin/env python3
"""
"""
import sys
import time
import threading
from datetime import datetime
from typing import Dict, List, Optional
try:
    from infrastructure.monitoring.metrics_collector import MetricsCollector, ClusterMetrics
    from infrastructure.monitoring.alert_system import AlertSystem, console_handler, file_handler
    from infrastructure.monitoring.realtime_dashboard import RealTimeDashboard
except ImportError:
    try:
        from .metrics_collector import MetricsCollector, ClusterMetrics
        from .alert_system import AlertSystem, console_handler, file_handler
        from .realtime_dashboard import RealTimeDashboard
    except ImportError:
        raise ImportError("monitoring modules not found. Please ensure they're in PYTHONPATH or install the package")
class IntegratedMonitor:
"""
    """def __init__(self, update_interval: int = 30):
        self.update_interval = update_interval
        self.running = False
        

        self.metrics_collector = MetricsCollector()
        self.alert_system = AlertSystem()
        self.dashboard = RealTimeDashboard(update_interval)
        

        self.setup_alert_handlers()
        
    
    def setup_alert_handlers(self):
"""
        self.alert_system.add_notification_handler(console_handler)
        self.alert_system.add_notification_handler(file_handler)
        

        def dashboard_handler(alert):
            """
            self.dashboard.alerts.append(f"{alert.severity}: {alert.message}")
        
        self.alert_system.add_notification_handler(dashboard_handler)
    
    def monitor_clusters(self, cluster_names: List[str]) -> Dict[str, ClusterMetrics]:
        """
        cluster_metrics = {}
        for cluster_name in cluster_names:
            try:
                metrics = self.metrics_collector.collect_full_metrics(cluster_name)
                cluster_metrics[cluster_name] = metrics
                alerts = self.alert_system.process_metrics(metrics)
                if alerts:
            except Exception as e:
        return cluster_metrics
    def run_continuous_monitoring(self, cluster_names: List[str]):
"""
        """self.running = True
        
        try:
            while self.running:
                print(f"\n{'='*60}")
                print('='*60)
                

                cluster_metrics = self.monitor_clusters(cluster_names)
                

                self.print_monitoring_summary(cluster_metrics)
                

                time.sleep(self.update_interval)
                
        except KeyboardInterrupt:
            self.running = False
        except Exception as e:
            self.running = False
    
    def print_monitoring_summary(self, cluster_metrics: Dict[str, ClusterMetrics]):
"""
        if not cluster_metrics:
            return
        total_cost = 0.0
        total_power = 0.0
        active_clusters = 0
        for cluster_name, metrics in cluster_metrics.items():
            status_indicator = self.get_status_indicator(metrics.status)
            print(f"  {status_indicator} {cluster_name}")
            if metrics.status == 'CREATE_COMPLETE':
                if metrics.gpu_usage > 0:
                    print(f"    GPU: {metrics.gpu_usage:.1f}%")
                active_clusters += 1
            total_cost += metrics.cost_per_hour
            total_power += metrics.power_consumption_watts
            print()
        alert_summary = self.alert_system.get_alert_summary()
        if alert_summary['total_active'] > 0:
        else:
    def get_status_icon(self, status: str) -> str:
"""
        """
        return self.get_status_indicator(status)
    
    def get_status_indicator(self, status: str) -> str:
        """
        indicators = {
            'CREATE_COMPLETE': '[OK]',
            'CREATE_IN_PROGRESS': '[IN_PROGRESS]',
            'CREATE_FAILED': '[FAILED]',
            'DELETE_IN_PROGRESS': '[DELETING]',
            'ERROR': '[ERROR]'
        }
        return indicators.get(status, '[UNKNOWN]')
    
    def run_dashboard_mode(self, cluster_names: List[str]):
        """self.dashboard.run_dashboard(cluster_names)
    
    def generate_report(self, cluster_names: List[str]) -> Dict:
"""
        cluster_metrics = self.monitor_clusters(cluster_names)
        alert_summary = self.alert_system.get_alert_summary()
        report = {
            'timestamp': datetime.now().isoformat(),
            'clusters': {name: metrics.to_dict() for name, metrics in cluster_metrics.items()},
            'alerts': alert_summary,
            'summary': {
                'total_cost_per_hour': sum(m.cost_per_hour for m in cluster_metrics.values()),
                'total_power_consumption': sum(m.power_consumption_watts for m in cluster_metrics.values()),
                'active_clusters': len([m for m in cluster_metrics.values() if m.status == 'CREATE_COMPLETE']),
                'total_clusters': len(cluster_metrics)
            },
            'recommendations': self.generate_recommendations(cluster_metrics)
        }
        return report
    def generate_recommendations(self, cluster_metrics: Dict[str, ClusterMetrics]) -> List[str]:
"""
        """recommendations = []
        
        active_metrics = [m for m in cluster_metrics.values() if m.status == 'CREATE_COMPLETE']
        
        if not active_metrics:
        

        high_cost_clusters = [m for m in active_metrics if m.cost_per_hour > 10.0]
        if high_cost_clusters:
        

        low_efficiency_clusters = [m for m in active_metrics if m.efficiency_score < 40.0]
        if low_efficiency_clusters:
        

        gpu_clusters = [m for m in active_metrics if m.gpu_usage > 0]
        if gpu_clusters:
            avg_gpu_usage = sum(m.gpu_usage for m in gpu_clusters) / len(gpu_clusters)
            if avg_gpu_usage < 30:
        

        unhealthy_clusters = [m for m in active_metrics if m.health_score < 70.0]
        if unhealthy_clusters:
        
        if not recommendations:
        
        return recommendations
    
    def save_report(self, report: Dict, filename: Optional[str] = None):
"""
        if not filename:
            timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
            filename = f"monitoring_report_{timestamp}.json"
        import json
        with open(filename, 'w') as f:
            json.dump(report, f, indent=2)
    def stop_monitoring(self):
"""
        """
        self.running = False
        self.dashboard.stop_dashboard()

def main():
    """
    import argparse
    
    parser = argparse.ArgumentParser(description='kcloud-opt 통합 모니터링 시스템')
    parser.add_argument('--mode', choices=['continuous', 'dashboard', 'report'],
                       default='continuous', help='실행 모드')
    parser.add_argument('--interval', type=int, default=30, help='업데이트 주기(초)')
    parser.add_argument('--clusters', nargs='+', default=['kcloud-ai-cluster-v2'],
                       help='모니터링할 클러스터 목록')
    
    args = parser.parse_args()
    
    print("kcloud-opt 통합 모니터링 시스템")
    print("=" * 50)
    
    monitor = IntegratedMonitor(update_interval=args.interval)
    
    if args.mode == 'continuous':
        monitor.run_continuous_monitoring(args.clusters)
        
    elif args.mode == 'dashboard':
        monitor.run_dashboard_mode(args.clusters)
        
    elif args.mode == 'report':
        report = monitor.generate_report(args.clusters)
        monitor.save_report(report)
        
        print(f"\n리포트 요약:")
        print(f"  총 비용: ${report['summary']['total_cost_per_hour']:.2f}/시간")
        print(f"  총 전력: {report['summary']['total_power_consumption']:.0f}W")
        print(f"  활성 알림: {report['alerts']['total_active']}개")
        print(f"\n권장사항:")
        for rec in report['recommendations']:
            print(f"  - {rec}")

if __name__ == "__main__":
    main()
