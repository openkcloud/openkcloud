#!/usr/bin/env python3
"""
"""

import sys
import time
from datetime import datetime

try:
    from infrastructure.monitoring.integrated_monitor import IntegratedMonitor
except ImportError:
    try:
        from .integrated_monitor import IntegratedMonitor
    except ImportError:
        raise ImportError("IntegratedMonitor not found. Please ensure it's in PYTHONPATH")

def demo_monitoring_features():
    """print("=" * 60)
    
    monitor = IntegratedMonitor(update_interval=10)
    

    test_clusters = ['kcloud-ai-cluster-v2']
    
    print("-" * 30)
    
    report = monitor.generate_report(test_clusters)
    monitor.save_report(report)
    
    
    print("-" * 30)
    

    start_time = time.time()
    update_count = 0
    
    try:
        while time.time() - start_time < 30:
            
            cluster_metrics = monitor.monitor_clusters(test_clusters)
            monitor.print_monitoring_summary(cluster_metrics)
            
            update_count += 1
            
            time.sleep(monitor.update_interval)
            
    except KeyboardInterrupt:
    
    
    print("-" * 30)
    
    alert_summary = monitor.alert_system.get_alert_summary()
    
    if alert_summary['total_active'] > 0:
    else:
    
    print("-" * 30)
    
    for rec in report['recommendations']:
        print(f"  - {rec}")
    
    print("-" * 30)
    
    print("=" * 60)

def show_usage_examples():
"""
    print("=" * 50)
    examples = [
    ]
    for name, command in examples:
        print(f"\n📌 {name}:")
        print(f"   {command}")
"""
    print("""from infrastructure.monitoring.integrated_monitor import IntegratedMonitor

monitor = IntegratedMonitor()

report = monitor.generate_report(['cluster1', 'cluster2'])

monitor.run_continuous_monitoring(['cluster1'])
""")

if __name__ == "__main__":
    demo_monitoring_features()
    show_usage_examples()
