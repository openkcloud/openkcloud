#!/usr/bin/env python3
"""
kcloud-opt 통합 모니터링 시스템
메트릭 수집 + 실시간 대시보드 + 알림 통합
"""

import sys
import time
import threading
from datetime import datetime
from typing import Dict, List, Optional

# 상대 import 또는 설치된 패키지 사용
try:
    from infrastructure.monitoring.metrics_collector import MetricsCollector, ClusterMetrics
    from infrastructure.monitoring.alert_system import AlertSystem, console_handler, file_handler
    from infrastructure.monitoring.realtime_dashboard import RealTimeDashboard
except ImportError:
    # 상대 import 시도
    try:
        from .metrics_collector import MetricsCollector, ClusterMetrics
        from .alert_system import AlertSystem, console_handler, file_handler
        from .realtime_dashboard import RealTimeDashboard
    except ImportError:
        raise ImportError("monitoring modules not found. Please ensure they're in PYTHONPATH or install the package")

class IntegratedMonitor:
    """통합 모니터링 시스템"""
    
    def __init__(self, update_interval: int = 30):
        self.update_interval = update_interval
        self.running = False
        
        # 구성 요소 초기화
        self.metrics_collector = MetricsCollector()
        self.alert_system = AlertSystem()
        self.dashboard = RealTimeDashboard(update_interval)
        
        # 알림 핸들러 설정
        self.setup_alert_handlers()
        
        print("통합 모니터링 시스템 초기화 완료")
    
    def setup_alert_handlers(self):
        """알림 핸들러 설정"""
        self.alert_system.add_notification_handler(console_handler)
        self.alert_system.add_notification_handler(file_handler)
        
        # 커스텀 핸들러 추가
        def dashboard_handler(alert):
            """대시보드용 알림 핸들러"""
            self.dashboard.alerts.append(f"{alert.severity}: {alert.message}")
        
        self.alert_system.add_notification_handler(dashboard_handler)
    
    def monitor_clusters(self, cluster_names: List[str]) -> Dict[str, ClusterMetrics]:
        """클러스터들 모니터링"""
        print(f"{len(cluster_names)}개 클러스터 모니터링 중...")
        
        cluster_metrics = {}
        
        for cluster_name in cluster_names:
            try:
                # 메트릭 수집
                metrics = self.metrics_collector.collect_full_metrics(cluster_name)
                cluster_metrics[cluster_name] = metrics
                
                # 알림 처리
                alerts = self.alert_system.process_metrics(metrics)
                if alerts:
                    print(f"[ALERT] {cluster_name}: {len(alerts)}개 알림 생성")
                
            except Exception as e:
                print(f"[ERROR] {cluster_name} 모니터링 실패: {e}")
        
        return cluster_metrics
    
    def run_continuous_monitoring(self, cluster_names: List[str]):
        """연속 모니터링 실행"""
        print(f"연속 모니터링 시작 - {self.update_interval}초 간격")
        print("종료하려면 Ctrl+C를 누르세요\n")
        
        self.running = True
        
        try:
            while self.running:
                print(f"\n{'='*60}")
                print(f"모니터링 업데이트: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}")
                print('='*60)
                
                # 클러스터 모니터링
                cluster_metrics = self.monitor_clusters(cluster_names)
                
                # 요약 정보 출력
                self.print_monitoring_summary(cluster_metrics)
                
                # 다음 업데이트까지 대기
                print(f"\n{self.update_interval}초 후 다음 업데이트...")
                time.sleep(self.update_interval)
                
        except KeyboardInterrupt:
            print(f"\n\n모니터링 중지됨")
            self.running = False
        except Exception as e:
            print(f"\n[ERROR] 모니터링 오류: {e}")
            self.running = False
    
    def print_monitoring_summary(self, cluster_metrics: Dict[str, ClusterMetrics]):
        """모니터링 요약 출력"""
        if not cluster_metrics:
            print("[ERROR] 수집된 메트릭이 없습니다")
            return
        
        total_cost = 0.0
        total_power = 0.0
        active_clusters = 0
        
        print(f"\n클러스터 상태:")
        for cluster_name, metrics in cluster_metrics.items():
            status_indicator = self.get_status_indicator(metrics.status)
            
            print(f"  {status_indicator} {cluster_name}")
            print(f"    상태: {metrics.status}")
            print(f"    노드: {metrics.node_count}개")
            print(f"    비용: ${metrics.cost_per_hour:.2f}/시간")
            print(f"    전력: {metrics.power_consumption_watts:.0f}W")
            
            if metrics.status == 'CREATE_COMPLETE':
                print(f"    CPU: {metrics.cpu_usage:.1f}% | 메모리: {metrics.memory_usage:.1f}%")
                if metrics.gpu_usage > 0:
                    print(f"    GPU: {metrics.gpu_usage:.1f}%")
                print(f"    헬스: {metrics.health_score:.1f}/100 | 효율성: {metrics.efficiency_score:.1f}/100")
                active_clusters += 1
            
            total_cost += metrics.cost_per_hour
            total_power += metrics.power_consumption_watts
            print()
        
        # 전체 요약
        print(f"총 비용: ${total_cost:.2f}/시간 | 예상 월비용: ${total_cost * 24 * 30:.0f}")
        print(f"총 전력: {total_power:.0f}W")
        print(f"활성 클러스터: {active_clusters}/{len(cluster_metrics)}개")
        
        # 알림 요약
        alert_summary = self.alert_system.get_alert_summary()
        if alert_summary['total_active'] > 0:
            print(f"\n[ALERT] 활성 알림: {alert_summary['total_active']}개")
            print(f"  CRITICAL: {alert_summary['by_severity']['CRITICAL']}개")
            print(f"  WARNING: {alert_summary['by_severity']['WARNING']}개")
            print(f"  INFO: {alert_summary['by_severity']['INFO']}개")
        else:
            print(f"\n[OK] 활성 알림 없음")
    
    def get_status_icon(self, status: str) -> str:
        """상태 아이콘 반환 (deprecated, use get_status_indicator instead)"""
        return self.get_status_indicator(status)
    
    def get_status_indicator(self, status: str) -> str:
        """상태 표시기 반환"""
        indicators = {
            'CREATE_COMPLETE': '[OK]',
            'CREATE_IN_PROGRESS': '[IN_PROGRESS]',
            'CREATE_FAILED': '[FAILED]',
            'DELETE_IN_PROGRESS': '[DELETING]',
            'ERROR': '[ERROR]'
        }
        return indicators.get(status, '[UNKNOWN]')
    
    def run_dashboard_mode(self, cluster_names: List[str]):
        """대시보드 모드 실행"""
        print("실시간 대시보드 모드 시작...")
        self.dashboard.run_dashboard(cluster_names)
    
    def generate_report(self, cluster_names: List[str]) -> Dict:
        """모니터링 리포트 생성"""
        print("모니터링 리포트 생성 중...")
        
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
        """최적화 권장사항 생성"""
        recommendations = []
        
        active_metrics = [m for m in cluster_metrics.values() if m.status == 'CREATE_COMPLETE']
        
        if not active_metrics:
            return ["현재 활성 클러스터가 없습니다"]
        
        # 비용 최적화 권장사항
        high_cost_clusters = [m for m in active_metrics if m.cost_per_hour > 10.0]
        if high_cost_clusters:
            recommendations.append(f"높은 비용 클러스터 {len(high_cost_clusters)}개: 스케일 다운 검토 권장")
        
        # 효율성 권장사항
        low_efficiency_clusters = [m for m in active_metrics if m.efficiency_score < 40.0]
        if low_efficiency_clusters:
            recommendations.append(f"낮은 효율성 클러스터 {len(low_efficiency_clusters)}개: 워크로드 재배치 권장")
        
        # GPU 활용률 권장사항
        gpu_clusters = [m for m in active_metrics if m.gpu_usage > 0]
        if gpu_clusters:
            avg_gpu_usage = sum(m.gpu_usage for m in gpu_clusters) / len(gpu_clusters)
            if avg_gpu_usage < 30:
                recommendations.append("GPU 활용률 낮음: GPU 노드 수 감소 또는 워크로드 통합 권장")
        
        # 헬스 권장사항
        unhealthy_clusters = [m for m in active_metrics if m.health_score < 70.0]
        if unhealthy_clusters:
            recommendations.append(f"헬스 문제 클러스터 {len(unhealthy_clusters)}개: 장애 대응 필요")
        
        if not recommendations:
            recommendations.append("현재 최적화 상태 양호")
        
        return recommendations
    
    def save_report(self, report: Dict, filename: Optional[str] = None):
        """리포트 저장"""
        if not filename:
            timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
            filename = f"monitoring_report_{timestamp}.json"
        
        import json
        with open(filename, 'w') as f:
            json.dump(report, f, indent=2)
        
        print(f"모니터링 리포트 저장: {filename}")
    
    def stop_monitoring(self):
        """모니터링 중지"""
        self.running = False
        self.dashboard.stop_dashboard()

def main():
    """메인 실행 함수"""
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