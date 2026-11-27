#!/usr/bin/env python3
"""
kcloud-opt 실시간 모니터링 대시보드
터미널 기반 실시간 UI
"""

import sys
import time
import threading
from datetime import datetime
from typing import Dict, List, Optional
from collections import deque

try:
    from infrastructure.monitoring.metrics_collector import MetricsCollector, ClusterMetrics
except ImportError:
    try:
        from .metrics_collector import MetricsCollector, ClusterMetrics
    except ImportError:
        raise ImportError("metrics_collector not found. Please ensure it's in PYTHONPATH")

class RealTimeDashboard:
    """실시간 모니터링 대시보드"""
    
    def __init__(self, update_interval: int = 15):
        self.update_interval = update_interval
        self.collector = MetricsCollector()
        self.running = False
        self.metrics_history = {}  # 클러스터별 히스토리
        self.alerts = deque(maxlen=10)  # 최근 10개 알림
        
    def clear_screen(self):
        """화면 지우기"""
        import os
        os.system('clear' if os.name == 'posix' else 'cls')
    
    def draw_progress_bar(self, percentage: float, width: int = 25) -> str:
        """진행 막대 생성"""
        percentage = max(0, min(100, percentage))
        filled = int(width * percentage / 100)
        
        # 색상 결정
        if percentage < 30:
            color_start = "\033[92m"  # 녹색
        elif percentage < 70:
            color_start = "\033[93m"  # 노란색
        else:
            color_start = "\033[91m"  # 빨간색
        
        color_end = "\033[0m"
        
        bar = '█' * filled + '░' * (width - filled)
        return f"{color_start}[{bar}]{color_end} {percentage:5.1f}%"
    
    def get_status_indicator(self, status: str) -> str:
        """상태 표시기"""
        indicators = {
            'CREATE_COMPLETE': '🟢',
            'CREATE_IN_PROGRESS': '🟡',
            'CREATE_FAILED': '🔴',
            'DELETE_IN_PROGRESS': '🟠',
            'ERROR': '⚠️'
        }
        return indicators.get(status, '❓')
    
    def format_cost(self, cost: float) -> str:
        """비용 포맷팅"""
        if cost < 1:
            return f"${cost:.2f}"
        elif cost < 100:
            return f"${cost:.1f}"
        else:
            return f"${cost:.0f}"
    
    def format_power(self, watts: float) -> str:
        """전력 포맷팅"""
        if watts < 1000:
            return f"{watts:.0f}W"
        else:
            return f"{watts/1000:.1f}kW"
    
    def check_alerts(self, metrics: ClusterMetrics):
        """알림 조건 체크"""
        alerts = []
        timestamp = datetime.now().strftime("%H:%M:%S")
        
        # 높은 비용 알림
        if metrics.cost_per_hour > 15.0:
            alerts.append(f"{timestamp} [ALERT] [{metrics.cluster_name}] 높은 비용: {self.format_cost(metrics.cost_per_hour)}/시간")
        
        # 낮은 헬스 스코어
        if metrics.health_score < 50:
            alerts.append(f"{timestamp} [WARNING] [{metrics.cluster_name}] 헬스 주의: {metrics.health_score:.1f}/100")
        
        # 실패한 포드
        if metrics.failed_pods > 0:
            alerts.append(f"{timestamp} [FAILED] [{metrics.cluster_name}] 실패한 포드: {metrics.failed_pods}개")
        
        # 높은 자원 사용률
        if metrics.cpu_usage > 90:
            alerts.append(f"{timestamp}  [{metrics.cluster_name}] 높은 CPU: {metrics.cpu_usage:.1f}%")
        
        if metrics.memory_usage > 90:
            alerts.append(f"{timestamp}  [{metrics.cluster_name}] 높은 메모리: {metrics.memory_usage:.1f}%")
        
        # 알림 저장
        for alert in alerts:
            self.alerts.append(alert)
    
    def display_cluster_summary(self, metrics: ClusterMetrics):
        """클러스터 요약 표시"""
        status_indicator = self.get_status_indicator(metrics.status)
        
        print(f"  {status_indicator} {metrics.cluster_name}")
        print(f"      상태: {metrics.status} | 노드: {metrics.node_count}개")
        
        if metrics.status == 'CREATE_COMPLETE':
            print(f"       비용: {self.format_cost(metrics.cost_per_hour)}/시간")
            print(f"       전력: {self.format_power(metrics.power_consumption_watts)}")
            print(f"       CPU:    {self.draw_progress_bar(metrics.cpu_usage)}")
            print(f"       메모리: {self.draw_progress_bar(metrics.memory_usage)}")
            
            if metrics.gpu_usage > 0:
                print(f"       GPU:    {self.draw_progress_bar(metrics.gpu_usage)}")
            
            print(f"       헬스: {metrics.health_score:5.1f}/100 |  효율성: {metrics.efficiency_score:5.1f}/100")
            
            if metrics.failed_pods > 0 or metrics.pending_pods > 0:
                print(f"      🏃 포드: 실행중 {metrics.running_pods} | 실패 {metrics.failed_pods} | 대기 {metrics.pending_pods}")
        else:
            print(f"       클러스터 생성/삭제 진행 중...")
    
    def display_dashboard(self, cluster_names: List[str]):
        """대시보드 화면 표시"""
        self.clear_screen()
        
        print(" kcloud-opt 실시간 클러스터 모니터링 대시보드")
        print("=" * 80)
        print(f" 업데이트: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')} |  {self.update_interval}초마다 갱신")
        print()
        
        total_cost = 0.0
        total_power = 0.0
        active_clusters = 0
        total_clusters = len(cluster_names)
        
        print(" 클러스터 현황")
        print("-" * 40)
        
        # 각 클러스터 정보 표시
        all_metrics = []
        for cluster_name in cluster_names:
            try:
                metrics = self.collector.collect_full_metrics(cluster_name)
                all_metrics.append(metrics)
                
                # 히스토리에 저장
                if cluster_name not in self.metrics_history:
                    self.metrics_history[cluster_name] = deque(maxlen=20)
                
                self.metrics_history[cluster_name].append(metrics)
                
                # 알림 체크
                self.check_alerts(metrics)
                
                # 클러스터 정보 표시
                self.display_cluster_summary(metrics)
                
                # 전체 통계 업데이트
                total_cost += metrics.cost_per_hour
                total_power += metrics.power_consumption_watts
                
                if metrics.status == 'CREATE_COMPLETE':
                    active_clusters += 1
                
                print()
                
            except Exception as e:
                print(f"  [ERROR] {cluster_name}: 메트릭 수집 실패 - {e}")
                print()
        
        # 전체 요약
        print("=" * 80)
        print(" 전체 요약")
        print("-" * 20)
        print(f" 클러스터: {total_clusters}개 (활성: {active_clusters}개)")
        print(f" 총 비용: {self.format_cost(total_cost)}/시간 |  예상 월비용: {self.format_cost(total_cost * 24 * 30)}")
        print(f" 총 전력: {self.format_power(total_power)}")
        
        if active_clusters > 0:
            avg_cpu = sum(m.cpu_usage for m in all_metrics if m.status == 'CREATE_COMPLETE') / active_clusters
            avg_memory = sum(m.memory_usage for m in all_metrics if m.status == 'CREATE_COMPLETE') / active_clusters
            avg_health = sum(m.health_score for m in all_metrics if m.status == 'CREATE_COMPLETE') / active_clusters
            avg_efficiency = sum(m.efficiency_score for m in all_metrics if m.status == 'CREATE_COMPLETE') / active_clusters
            
            print(f" 평균 활용률:")
            print(f"   CPU:    {self.draw_progress_bar(avg_cpu)}")
            print(f"   메모리: {self.draw_progress_bar(avg_memory)}")
            print(f" 평균 헬스: {avg_health:5.1f}/100 |  평균 효율성: {avg_efficiency:5.1f}/100")
        
        # 최근 알림
        if self.alerts:
            print(f"\n[ALERT] 최근 알림 ({len(self.alerts)}개)")
            print("-" * 30)
            for alert in list(self.alerts)[-5:]:  # 최근 5개만 표시
                print(f"  {alert}")
        
        print(f"\n 다음 업데이트: {self.update_interval}초 후 | 종료: Ctrl+C")
    
    def run_dashboard(self, cluster_names: List[str]):
        """대시보드 실행"""
        print(f" 실시간 대시보드 시작 - {len(cluster_names)}개 클러스터 모니터링")
        print(f" 업데이트 주기: {self.update_interval}초")
        print("잠시 후 대시보드가 시작됩니다...")
        time.sleep(2)
        
        self.running = True
        
        try:
            while self.running:
                self.display_dashboard(cluster_names)
                time.sleep(self.update_interval)
                
        except KeyboardInterrupt:
            print("\n\n 대시보드 종료")
            self.running = False
        except Exception as e:
            print(f"\n[ERROR] 대시보드 오류: {e}")
            self.running = False
    
    def stop_dashboard(self):
        """대시보드 중지"""
        self.running = False
    
    def get_metrics_summary(self, cluster_names: List[str]) -> Dict:
        """현재 메트릭 요약 반환"""
        summary = {
            'timestamp': datetime.now().isoformat(),
            'clusters': {},
            'totals': {
                'cost_per_hour': 0.0,
                'power_consumption': 0.0,
                'active_clusters': 0,
                'total_clusters': len(cluster_names)
            }
        }
        
        for cluster_name in cluster_names:
            try:
                metrics = self.collector.collect_full_metrics(cluster_name)
                summary['clusters'][cluster_name] = metrics.to_dict()
                
                summary['totals']['cost_per_hour'] += metrics.cost_per_hour
                summary['totals']['power_consumption'] += metrics.power_consumption_watts
                
                if metrics.status == 'CREATE_COMPLETE':
                    summary['totals']['active_clusters'] += 1
                    
            except Exception as e:
                summary['clusters'][cluster_name] = {'error': str(e)}
        
        return summary

def main():
    """메인 실행 함수"""
    import argparse
    
    parser = argparse.ArgumentParser(description='kcloud-opt 실시간 모니터링 대시보드')
    parser.add_argument('--interval', type=int, default=15, help='업데이트 주기(초)')
    parser.add_argument('--clusters', nargs='+', default=['kcloud-ai-cluster-v2'], 
                       help='모니터링할 클러스터 이름들')
    parser.add_argument('--mode', choices=['dashboard', 'once'], default='dashboard',
                       help='실행 모드 (dashboard: 실시간, once: 1회만)')
    
    args = parser.parse_args()
    
    dashboard = RealTimeDashboard(update_interval=args.interval)
    
    if args.mode == 'dashboard':
        dashboard.run_dashboard(args.clusters)
    else:
        # 1회만 실행
        summary = dashboard.get_metrics_summary(args.clusters)
        
        print(" 현재 클러스터 상태 요약")
        print("=" * 40)
        
        for cluster_name, metrics in summary['clusters'].items():
            if 'error' in metrics:
                print(f"[ERROR] {cluster_name}: {metrics['error']}")
            else:
                print(f" {cluster_name}")
                print(f"  상태: {metrics['status']}")
                print(f"  비용: ${metrics['cost_per_hour']:.2f}/시간")
                print(f"  전력: {metrics['power_consumption_watts']:.0f}W")
                print(f"  헬스: {metrics['health_score']:.1f}/100")
                print()
        
        totals = summary['totals']
        print(f" 총 비용: ${totals['cost_per_hour']:.2f}/시간")
        print(f" 총 전력: {totals['power_consumption']:.0f}W")
        print(f" 활성 클러스터: {totals['active_clusters']}/{totals['total_clusters']}개")

if __name__ == "__main__":
    main()