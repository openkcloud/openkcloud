#!/usr/bin/env python3
"""
"""
import sys
import time
import json
import logging
from datetime import datetime
from typing import Dict, List, Optional, Tuple
from dataclasses import dataclass, asdict
try:
    from magnumclient import client as magnum_client
    from keystoneauth1 import loading, session
    import openstack
except ImportError:
    raise ImportError("OpenStack client libraries not found. Please install them or set PYTHONPATH")
try:
    from monitoring.config import get_openstack_config, get_monitoring_config, get_cluster_template
except ImportError:
    try:
        from .config import get_openstack_config, get_monitoring_config, get_cluster_template
    except ImportError:
        raise ImportError("monitoring.config not found. Please ensure it's in PYTHONPATH")
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)
@dataclass
class ClusterMetrics:
"""
    """
    cluster_name: str
    timestamp: str
    status: str
    health_status: str
    node_count: int
    master_count: int
    template_id: str
    api_address: Optional[str] = None
    

    cpu_usage: float = 0.0
    memory_usage: float = 0.0
    gpu_usage: float = 0.0
    disk_usage: float = 0.0
    network_io_mbps: float = 0.0
    

    running_pods: int = 0
    failed_pods: int = 0
    pending_pods: int = 0
    workload_count: int = 0
    

    power_consumption_watts: float = 0.0
    cost_per_hour: float = 0.0
    estimated_monthly_cost: float = 0.0
    

    health_score: float = 0.0
    efficiency_score: float = 0.0
    
    def to_dict(self) -> Dict:
        """
        return asdict(self)

class MetricsCollector:
    """
    
    def __init__(self):
        self.openstack_config = get_openstack_config()
        self.monitoring_config = get_monitoring_config()
        self.setup_clients()
        
    def setup_clients(self):
        """
        try:
            auth_config = {
                'auth_url': self.openstack_config.auth_url,
                'username': self.openstack_config.username,
                'password': self.openstack_config.password,
                'project_name': self.openstack_config.project_name,
                'project_domain_name': self.openstack_config.project_domain_name,
                'user_domain_name': self.openstack_config.user_domain_name
            }
            loader = loading.get_plugin_loader('password')
            auth = loader.load_from_options(**auth_config)
            sess = session.Session(auth=auth)
            self.magnum = magnum_client.Client('1', session=sess)
            # OpenStack SDK
            self.conn = openstack.connect(**auth_config)
        except Exception as e:
            raise
    def collect_cluster_basic_info(self, cluster_name: str) -> ClusterMetrics:
"""
        """try:
            cluster = self.magnum.clusters.get(cluster_name)
            
            metrics = ClusterMetrics(
                cluster_name=cluster_name,
                timestamp=datetime.now().isoformat(),
                status=cluster.status,
                health_status=cluster.health_status or "UNKNOWN",
                node_count=cluster.node_count,
                master_count=cluster.master_count,
                template_id=cluster.cluster_template_id,
                api_address=cluster.api_address
            )
            
            return metrics
            
        except Exception as e:
            

            return ClusterMetrics(
                cluster_name=cluster_name,
                timestamp=datetime.now().isoformat(),
                status="ERROR",
                health_status="ERROR",
                node_count=0,
                master_count=0,
                template_id="unknown"
            )
    
    def collect_resource_metrics(self, metrics: ClusterMetrics) -> ClusterMetrics:
"""
        if metrics.status != "CREATE_COMPLETE":
            return metrics
        try:
            template_info = get_cluster_template(metrics.template_id)
            if not template_info:
                template_info = get_cluster_template("dev-k8s-template")
            import random
            if template_info.has_gpu:
                metrics.cpu_usage = random.uniform(60.0, 95.0)
                metrics.memory_usage = random.uniform(70.0, 90.0)
                metrics.gpu_usage = random.uniform(40.0, 95.0)
                metrics.network_io_mbps = random.uniform(100.0, 800.0)
                metrics.running_pods = random.randint(8, 30)
                metrics.workload_count = random.randint(3, 10)
            else:
                metrics.cpu_usage = random.uniform(20.0, 70.0)
                metrics.memory_usage = random.uniform(30.0, 80.0)
                metrics.gpu_usage = 0.0
                metrics.network_io_mbps = random.uniform(50.0, 300.0)
                metrics.running_pods = random.randint(5, 20)
                metrics.workload_count = random.randint(2, 8)
            metrics.disk_usage = random.uniform(30.0, 85.0)
            metrics.failed_pods = random.randint(0, 2)
            metrics.pending_pods = random.randint(0, 5)
        except Exception as e:
        return metrics
    def calculate_power_and_cost(self, metrics: ClusterMetrics) -> ClusterMetrics:
"""
        """try:
            template_info = get_cluster_template(metrics.template_id)
            if not template_info:
                template_info = get_cluster_template("dev-k8s-template")
            

            base_power_per_node = template_info.estimated_power_per_node
            

            utilization_factor = (metrics.cpu_usage + metrics.memory_usage) / 200.0
            if metrics.gpu_usage > 0:
                utilization_factor = (metrics.cpu_usage + metrics.memory_usage + metrics.gpu_usage) / 300.0
            

            actual_power_per_node = base_power_per_node * (0.3 + 0.7 * utilization_factor)
            

            total_power = actual_power_per_node * (metrics.node_count + metrics.master_count)
            

            metrics.power_consumption_watts = total_power * self.monitoring_config.cooling_overhead
            


            power_cost_per_hour = (metrics.power_consumption_watts / 1000.0) * self.monitoring_config.electricity_rate
            infrastructure_cost_per_hour = template_info.base_cost_per_hour * metrics.node_count
            
            metrics.cost_per_hour = power_cost_per_hour + infrastructure_cost_per_hour
            metrics.estimated_monthly_cost = metrics.cost_per_hour * 24 * 30
            
            
        except Exception as e:
        
        return metrics
    
    def calculate_scores(self, metrics: ClusterMetrics) -> ClusterMetrics:
"""
        try:
            health_score = 100.0
            if metrics.status != "CREATE_COMPLETE":
                health_score = 0.0
            else:
                if metrics.failed_pods > 0:
                    health_score -= metrics.failed_pods * 15
                if metrics.pending_pods > 5:
                    health_score -= (metrics.pending_pods - 5) * 10
                if metrics.cpu_usage > 90:
                    health_score -= 20
                if metrics.memory_usage > 90:
                    health_score -= 20
                if not metrics.api_address:
                    health_score -= 10
            metrics.health_score = max(0.0, min(100.0, health_score))
            if metrics.status == "CREATE_COMPLETE" and metrics.power_consumption_watts > 0:
                utilization_score = (metrics.cpu_usage + metrics.memory_usage) / 2
                if metrics.gpu_usage > 0:
                    utilization_score = (metrics.cpu_usage + metrics.memory_usage + metrics.gpu_usage) / 3
                power_efficiency = utilization_score / (metrics.power_consumption_watts / 1000.0)
                efficiency_score = min(100.0, power_efficiency * 20)
                metrics.efficiency_score = max(0.0, efficiency_score)
            else:
                metrics.efficiency_score = 0.0
        except Exception as e:
            metrics.health_score = 0.0
            metrics.efficiency_score = 0.0
        return metrics
    def collect_full_metrics(self, cluster_name: str) -> ClusterMetrics:
"""
        """metrics = self.collect_cluster_basic_info(cluster_name)
        

        metrics = self.collect_resource_metrics(metrics)
        

        metrics = self.calculate_power_and_cost(metrics)
        

        metrics = self.calculate_scores(metrics)
        
        return metrics
    
    def collect_multiple_clusters(self, cluster_names: List[str]) -> List[ClusterMetrics]:
"""
        metrics_list = []
        for cluster_name in cluster_names:
            try:
                metrics = self.collect_full_metrics(cluster_name)
                metrics_list.append(metrics)
            except Exception as e:
        return metrics_list
    def save_metrics(self, metrics: ClusterMetrics, filename: Optional[str] = None):
"""
        """if not filename:
            timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
            filename = f"metrics_{metrics.cluster_name}_{timestamp}.json"
        
        try:
            with open(filename, 'w') as f:
                json.dump(metrics.to_dict(), f, indent=2)
            
            
        except Exception as e:

def main():
"""
    print(" 클러스터 메트릭 수집기 테스트")
    print("=" * 50)
    
    collector = MetricsCollector()
    

    test_cluster = "kcloud-ai-cluster-v2"
    
    print(f"\n 클러스터 '{test_cluster}' 메트릭 수집 중...")
    
    try:
        metrics = collector.collect_full_metrics(test_cluster)
        
        print(f"\n 수집된 메트릭:")
        print(f"  클러스터: {metrics.cluster_name}")
        print(f"  상태: {metrics.status}")
        print(f"  노드 수: {metrics.node_count}개")
        print(f"  CPU 사용률: {metrics.cpu_usage:.1f}%")
        print(f"  메모리 사용률: {metrics.memory_usage:.1f}%")
        print(f"  GPU 사용률: {metrics.gpu_usage:.1f}%")
        print(f"  전력 소비: {metrics.power_consumption_watts:.0f}W")
        print(f"  시간당 비용: ${metrics.cost_per_hour:.2f}")
        print(f"  헬스 스코어: {metrics.health_score:.1f}/100")
        print(f"  효율성 스코어: {metrics.efficiency_score:.1f}/100")
        

        collector.save_metrics(metrics)
        
        print(f"\n[OK] 메트릭 수집 테스트 완료")
        
    except Exception as e:
        print(f"[ERROR] 테스트 실패: {e}")

if __name__ == "__main__":
    main()
