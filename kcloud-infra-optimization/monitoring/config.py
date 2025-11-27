#!/usr/bin/env python3
"""
"""

import os
from dataclasses import dataclass
from typing import Dict, List, Optional

@dataclass
class OpenStackConfig:
    """
    auth_url: str = os.getenv('OS_AUTH_URL', "http://10.0.4.200:5000/v3")
    username: str = os.getenv('OS_USERNAME', "admin")
    password: str = os.getenv('OS_PASSWORD', "")
    project_name: str = os.getenv('OS_PROJECT_NAME', "cloud-platform")
    project_domain_name: str = os.getenv('OS_PROJECT_DOMAIN_NAME', "Default")
    user_domain_name: str = os.getenv('OS_USER_DOMAIN_NAME', "Default")
    region_name: str = os.getenv('OS_REGION_NAME', "RegionOne")
    interface: str = os.getenv('OS_INTERFACE', "public")
    identity_api_version: str = os.getenv('OS_IDENTITY_API_VERSION', "3")

@dataclass
class MonitoringConfig:
    """
    update_interval: int = 30
    history_retention: int = 100
    alert_retention: int = 50
    

    high_cost_threshold: float = 20.0
    low_efficiency_threshold: float = 30.0  # %
    low_health_threshold: float = 50.0      # %
    high_power_threshold: float = 10000.0   # W
    

    electricity_rate: float = 0.12  # $/kWh
    cooling_overhead: float = 1.3   # PUE (Power Usage Effectiveness)

@dataclass
class ClusterTemplate:
    """template_id: str
    name: str
    base_cost_per_hour: float
    has_gpu: bool
    estimated_power_per_node: float  # watts

CLUSTER_TEMPLATES = {
    "ai-k8s-template": ClusterTemplate(
        template_id="ai-k8s-template",
        base_cost_per_hour=1.20,
        has_gpu=True,
        estimated_power_per_node=1200.0
    ),
    "dev-k8s-template": ClusterTemplate(
        template_id="dev-k8s-template",
        base_cost_per_hour=0.15,
        has_gpu=False,
        estimated_power_per_node=300.0
    ),
    "prod-k8s-template": ClusterTemplate(
        template_id="prod-k8s-template",
        base_cost_per_hour=0.30,
        has_gpu=False,
        estimated_power_per_node=500.0
    )
}

openstack_config = OpenStackConfig()
monitoring_config = MonitoringConfig()

def get_openstack_config() -> OpenStackConfig:
"""
    Get current OpenStack configuration.

    Returns:
        OpenStackConfig: Current OpenStack configuration instance
    """
    return openstack_config

def get_monitoring_config() -> MonitoringConfig:
    """
    Get current monitoring configuration.

    Returns:
        MonitoringConfig: Current monitoring configuration instance
    """
    return monitoring_config

def get_cluster_template(template_name: str) -> Optional[ClusterTemplate]:
    """
    Get cluster template by name.

    Args:
        template_name: Name of the cluster template

    Returns:
        ClusterTemplate if found, None otherwise
    """
    return CLUSTER_TEMPLATES.get(template_name)

def update_config_from_env() -> None:
    """
    Update configuration from environment variables.

    Reads environment variables and updates the global configuration
    instances for OpenStack and monitoring settings.
    """
    global openstack_config, monitoring_config
    

    if os.getenv('OS_AUTH_URL'):
        openstack_config.auth_url = os.getenv('OS_AUTH_URL')
    if os.getenv('OS_USERNAME'):
        openstack_config.username = os.getenv('OS_USERNAME')
    if os.getenv('OS_PASSWORD'):
        openstack_config.password = os.getenv('OS_PASSWORD')
    if os.getenv('OS_PROJECT_NAME'):
        openstack_config.project_name = os.getenv('OS_PROJECT_NAME')
    

    if os.getenv('MONITORING_UPDATE_INTERVAL'):
        monitoring_config.update_interval = int(os.getenv('MONITORING_UPDATE_INTERVAL'))
    if os.getenv('HIGH_COST_THRESHOLD'):
        monitoring_config.high_cost_threshold = float(os.getenv('HIGH_COST_THRESHOLD'))

# 초기화 시 환경 변수 로드
update_config_from_env()
