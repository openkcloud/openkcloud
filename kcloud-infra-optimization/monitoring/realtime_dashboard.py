#!/usr/bin/env python3
"""
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
    """
    
    def __init__(self, update_interval: int = 15):
        self.update_interval = update_interval
        self.collector = MetricsCollector()
        self.running = False
        self.metrics_history = {}
        self.alerts = deque(maxlen=10)
        
    def clear_screen(self):
        """
        import os
        os.system('clear' if os.name == 'posix' else 'cls')
    
    def draw_progress_bar(self, percentage: float, width: int = 25) -> str:
        """
        percentage = max(0, min(100, percentage))
        filled = int(width * percentage / 100)
        

        if percentage < 30:
            color_start = "\033[92m"
        elif percentage < 70:
            color_start = "\033[93m"
        else:
            color_start = "\033[91m"
        
        color_end = "\033[0m"
        
        bar = '█' * filled + '░' * (width - filled)
        return f"{color_start}[{bar}]{color_end} {percentage:5.1f}%"
    
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
    
    def format_cost(self, cost: float) -> str:
        """
        if cost < 1:
            return f"${cost:.2f}"
        elif cost < 100:
            return f"${cost:.1f}"
        else:
            return f"${cost:.0f}"
    
    def format_power(self, watts: float) -> str:
        """
        if watts < 1000:
            return f"{watts:.0f}W"
        else:
            return f"{watts/1000:.1f}kW"
    
    def check_alerts(self, metrics: ClusterMetrics):
        """alerts = []
        timestamp = datetime.now().strftime("%H:%M:%S")
        

        if metrics.cost_per_hour > 15.0:
        

        if metrics.health_score < 50:
        

        if metrics.failed_pods > 0:
        

        if metrics.cpu_usage > 90:
        
        if metrics.memory_usage > 90:
        

        for alert in alerts:
            self.alerts.append(alert)
    
    def display_cluster_summary(self, metrics: ClusterMetrics):
"""
        status_indicator = self.get_status_indicator(metrics.status)
        print(f"  {status_indicator} {metrics.cluster_name}")
        if metrics.status == 'CREATE_COMPLETE':
            print(f"       CPU:    {self.draw_progress_bar(metrics.cpu_usage)}")
            if metrics.gpu_usage > 0:
                print(f"       GPU:    {self.draw_progress_bar(metrics.gpu_usage)}")
            if metrics.failed_pods > 0 or metrics.pending_pods > 0:
        else:
    def display_dashboard(self, cluster_names: List[str]):
"""
        """self.clear_screen()
        
        print("=" * 80)
        print()
        
        total_cost = 0.0
        total_power = 0.0
        active_clusters = 0
        total_clusters = len(cluster_names)
        
        print("-" * 40)
        

        all_metrics = []
        for cluster_name in cluster_names:
            try:
                metrics = self.collector.collect_full_metrics(cluster_name)
                all_metrics.append(metrics)
                

                if cluster_name not in self.metrics_history:
                    self.metrics_history[cluster_name] = deque(maxlen=20)
                
                self.metrics_history[cluster_name].append(metrics)
                

                self.check_alerts(metrics)
                

                self.display_cluster_summary(metrics)
                

                total_cost += metrics.cost_per_hour
                total_power += metrics.power_consumption_watts
                
                if metrics.status == 'CREATE_COMPLETE':
                    active_clusters += 1
                
                print()
                
            except Exception as e:
                print()
        

        print("=" * 80)
        print("-" * 20)
        
        if active_clusters > 0:
            avg_cpu = sum(m.cpu_usage for m in all_metrics if m.status == 'CREATE_COMPLETE') / active_clusters
            avg_memory = sum(m.memory_usage for m in all_metrics if m.status == 'CREATE_COMPLETE') / active_clusters
            avg_health = sum(m.health_score for m in all_metrics if m.status == 'CREATE_COMPLETE') / active_clusters
            avg_efficiency = sum(m.efficiency_score for m in all_metrics if m.status == 'CREATE_COMPLETE') / active_clusters
            
            print(f"   CPU:    {self.draw_progress_bar(avg_cpu)}")
        

        if self.alerts:
            print("-" * 30)
            for alert in list(self.alerts)[-5:]:
                print(f"  {alert}")
        
    
    def run_dashboard(self, cluster_names: List[str]):
"""
        time.sleep(2)
        self.running = True
        try:
            while self.running:
                self.display_dashboard(cluster_names)
                time.sleep(self.update_interval)
        except KeyboardInterrupt:
            self.running = False
        except Exception as e:
            self.running = False
    def stop_dashboard(self):
"""
        """
        self.running = False
    
    def get_metrics_summary(self, cluster_names: List[str]) -> Dict:
        """
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
    """
    import argparse
    
    parser = argparse.ArgumentParser(description='kcloud-opt real-time monitoring dashboard')
    parser.add_argument('--interval', type=int, default=15, help='update interval (seconds)')
    parser.add_argument('--clusters', nargs='+', default=['kcloud-ai-cluster-v2'],
                       help='cluster names to monitor')
    parser.add_argument('--mode', choices=['dashboard', 'once'], default='dashboard',
                       help='run mode (dashboard: real-time, once: single run)')
    
    args = parser.parse_args()
    
    dashboard = RealTimeDashboard(update_interval=args.interval)
    
    if args.mode == 'dashboard':
        dashboard.run_dashboard(args.clusters)
    else:

        summary = dashboard.get_metrics_summary(args.clusters)
        
        print(" Current Cluster Status Summary")
        print("=" * 40)
        
        for cluster_name, metrics in summary['clusters'].items():
            if 'error' in metrics:
                print(f"[ERROR] {cluster_name}: {metrics['error']}")
            else:
                print(f" {cluster_name}")
                print(f"  status: {metrics['status']}")
                print(f"  cost: ${metrics['cost_per_hour']:.2f}/hour")
                print(f"  power: {metrics['power_consumption_watts']:.0f}W")
                print(f"  health: {metrics['health_score']:.1f}/100")
                print()
        
        totals = summary['totals']
        print(f" total cost: ${totals['cost_per_hour']:.2f}/hour")
        print(f" total power: {totals['power_consumption']:.0f}W")
        print(f" active clusters: {totals['active_clusters']}/{totals['total_clusters']}")

if __name__ == "__main__":
    main()
