#!/usr/bin/env python3
"""OpenStack Virtual Cluster CRUD Controller
"""

import os
import time
import json
import asyncio
from typing import Dict, List, Optional, Any
from datetime import datetime
from dataclasses import dataclass, asdict
from enum import Enum

import openstack
from openstack.connection import Connection
from openstack.exceptions import SDKException, ResourceNotFound

# Logging
import logging
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)


class ClusterStatus(Enum):
    """
    CREATING = "CREATE_IN_PROGRESS"
    ACTIVE = "CREATE_COMPLETE"
    UPDATING = "UPDATE_IN_PROGRESS"
    DELETING = "DELETE_IN_PROGRESS"
    ERROR = "CREATE_FAILED"
    DELETED = "DELETED"
    UNKNOWN = "UNKNOWN"


@dataclass
class ClusterConfig:
    """
    name: str
    cluster_template_id: str
    keypair: str = "ketilinux"
    master_count: int = 1
    node_count: int = 2
    master_flavor: Optional[str] = None
    flavor: Optional[str] = None
    docker_volume_size: int = 50
    labels: Dict[str, str] = None
    fixed_network: Optional[str] = None
    fixed_subnet: Optional[str] = None
    floating_ip_enabled: bool = True
    
    def __post_init__(self):
        if self.labels is None:
            self.labels = {
                "kube_dashboard_enabled": "true",
                "prometheus_monitoring": "true",
                "auto_scaling_enabled": "true",
                "min_node_count": "1",
                "max_node_count": "10"
            }


@dataclass
class ClusterInfo:
    """
    id: str
    name: str
    status: str
    stack_id: str
    master_count: int
    node_count: int
    keypair: str
    cluster_template_id: str
    api_address: Optional[str]
    coe_version: Optional[str]
    created_at: str
    updated_at: Optional[str]
    health_status: Optional[str]
    health_status_reason: Optional[str]
    project_id: str
    user_id: str
    node_addresses: List[str]
    master_addresses: List[str]
    

class OpenStackClusterCRUD:
    """
    
    def __init__(self, cloud_name: str = "openstack"):
        """Args:
"""
        try:
            self.conn = openstack.connect(cloud=cloud_name)

            if hasattr(self.conn, 'current_project_id'):
                self.project_id = self.conn.current_project_id
            elif hasattr(self.conn, 'auth') and 'project_id' in self.conn.auth:
                self.project_id = self.conn.auth['project_id']
            else:

                project = self.conn.identity.find_project("cloud-platform")
                self.project_id = project.id if project else "unknown"
            
            logger.info(f"Connected to OpenStack cloud: {cloud_name}")
            logger.info(f"Project ID: {self.project_id}")
        except Exception as e:
            logger.error(f"Failed to connect to OpenStack: {e}")
            raise
            
    def _wait_for_cluster_status(
        self,
        cluster_id: str,
        target_status: List[str],
        timeout: int = 3600,
        check_interval: int = 30
    ) -> Dict:
        """Args:
            
        Returns:
"""
        start_time = time.time()
        
        while time.time() - start_time < timeout:
            try:
                cluster = self.conn.container_infra.get_cluster(cluster_id)
                current_status = cluster.status
                
                logger.info(f"Cluster {cluster.name} status: {current_status}")
                
                if current_status in target_status:
                    return cluster
                    
                if "FAILED" in current_status or "ERROR" in current_status:
                    raise Exception(f"Cluster operation failed: {current_status}")
                    
            except ResourceNotFound:
                if "DELETED" in target_status:
                    return {"status": "DELETED"}
                raise
                
            time.sleep(check_interval)
            
        raise TimeoutError(f"Cluster operation timed out after {timeout} seconds")
    

    def create_cluster(self, config: ClusterConfig) -> ClusterInfo:
        """Args:
            
        Returns:
"""
        logger.info(f"Creating cluster: {config.name}")
        
        try:

            cluster_data = {
                "name": config.name,
                "cluster_template_id": config.cluster_template_id,
                "keypair": config.keypair,
                "master_count": config.master_count,
                "node_count": config.node_count,
                "docker_volume_size": config.docker_volume_size,
                "labels": config.labels,
                "floating_ip_enabled": config.floating_ip_enabled
            }
            

            if config.master_flavor:
                cluster_data["master_flavor_id"] = config.master_flavor
            if config.flavor:
                cluster_data["flavor_id"] = config.flavor
            if config.fixed_network:
                cluster_data["fixed_network"] = config.fixed_network
            if config.fixed_subnet:
                cluster_data["fixed_subnet"] = config.fixed_subnet
                

            cluster = self.conn.container_infra.create_cluster(**cluster_data)
            logger.info(f"Cluster creation initiated: {cluster.id}")
            

            cluster = self._wait_for_cluster_status(
                cluster.id,
                ["CREATE_COMPLETE"],
                timeout=3600
            )
            
            return self._cluster_to_info(cluster)
            
        except Exception as e:
            logger.error(f"Failed to create cluster: {e}")
            raise
    

    def get_cluster(self, cluster_id: str = None, cluster_name: str = None) -> ClusterInfo:
        """Args:
            
        Returns:
"""
        try:
            if cluster_id:
                cluster = self.conn.container_infra.get_cluster(cluster_id)
            elif cluster_name:
                cluster = self.conn.container_infra.find_cluster(cluster_name)
                if not cluster:
                    raise ResourceNotFound(f"Cluster not found: {cluster_name}")
            else:
                raise ValueError("Either cluster_id or cluster_name must be provided")
                
            return self._cluster_to_info(cluster)
            
        except Exception as e:
            logger.error(f"Failed to get cluster: {e}")
            raise
    
    def list_clusters(self, filters: Optional[Dict] = None) -> List[ClusterInfo]:
        """Args:
            
        Returns:
"""
        try:
            clusters = self.conn.container_infra.clusters()
            cluster_list = []
            
            for cluster in clusters:
                cluster_info = self._cluster_to_info(cluster)
                

                if filters:
                    match = True
                    for key, value in filters.items():
                        if getattr(cluster_info, key, None) != value:
                            match = False
                            break
                    if not match:
                        continue
                        
                cluster_list.append(cluster_info)
                
            logger.info(f"Found {len(cluster_list)} clusters")
            return cluster_list
            
        except Exception as e:
            logger.error(f"Failed to list clusters: {e}")
            raise
    

    def update_cluster(
        self,
        cluster_id: str,
        node_count: Optional[int] = None,
        max_node_count: Optional[int] = None,
        min_node_count: Optional[int] = None
    ) -> ClusterInfo:
        """Args:
            
        Returns:
"""
        logger.info(f"Updating cluster: {cluster_id}")
        
        try:

            cluster = self.conn.container_infra.get_cluster(cluster_id)
            

            patch = []
            
            if node_count is not None:
                patch.append({
                    "op": "replace",
                    "path": "/node_count",
                    "value": node_count
                })
                
            if max_node_count is not None:
                patch.append({
                    "op": "replace",
                    "path": "/labels/max_node_count",
                    "value": str(max_node_count)
                })
                
            if min_node_count is not None:
                patch.append({
                    "op": "replace",
                    "path": "/labels/min_node_count",
                    "value": str(min_node_count)
                })
            
            if not patch:
                logger.warning("No updates to apply")
                return self._cluster_to_info(cluster)
            

            self.conn.container_infra.update_cluster(cluster_id, patch)
            logger.info(f"Cluster update initiated: {patch}")
            

            cluster = self._wait_for_cluster_status(
                cluster_id,
                ["UPDATE_COMPLETE", "CREATE_COMPLETE"],
                timeout=1800
            )
            
            return self._cluster_to_info(cluster)
            
        except Exception as e:
            logger.error(f"Failed to update cluster: {e}")
            raise
    
    def resize_cluster(self, cluster_id: str, node_count: int) -> ClusterInfo:
        """Args:
            
        Returns:
"""
        return self.update_cluster(cluster_id, node_count=node_count)
    

    def delete_cluster(self, cluster_id: str, force: bool = False) -> bool:
        """Args:
            
        Returns:
"""
        logger.info(f"Deleting cluster: {cluster_id}")
        
        try:

            cluster = self.conn.container_infra.get_cluster(cluster_id)
            cluster_name = cluster.name
            

            self.conn.container_infra.delete_cluster(cluster_id)
            logger.info(f"Cluster deletion initiated: {cluster_name}")
            

            self._wait_for_cluster_status(
                cluster_id,
                ["DELETED"],
                timeout=1800
            )
            
            logger.info(f"Cluster deleted successfully: {cluster_name}")
            return True
            
        except ResourceNotFound:
            logger.warning(f"Cluster not found: {cluster_id}")
            return True if force else False
            
        except Exception as e:
            logger.error(f"Failed to delete cluster: {e}")
            if force:
                logger.warning("Force delete requested, marking as deleted")
                return True
            raise
    

    def _cluster_to_info(self, cluster: Any) -> ClusterInfo:
        """Args:
            
        Returns:
"""
        return ClusterInfo(
            id=cluster.id,
            name=cluster.name,
            status=cluster.status,
            stack_id=getattr(cluster, 'stack_id', ''),
            master_count=getattr(cluster, 'master_count', 0),
            node_count=getattr(cluster, 'node_count', 0),
            keypair=getattr(cluster, 'keypair', ''),
            cluster_template_id=getattr(cluster, 'cluster_template_id', ''),
            api_address=getattr(cluster, 'api_address', None),
            coe_version=getattr(cluster, 'coe_version', None),
            created_at=str(getattr(cluster, 'created_at', '')),
            updated_at=str(getattr(cluster, 'updated_at', None)),
            health_status=getattr(cluster, 'health_status', None),
            health_status_reason=getattr(cluster, 'health_status_reason', None),
            project_id=getattr(cluster, 'project_id', self.project_id),
            user_id=getattr(cluster, 'user_id', ''),
            node_addresses=getattr(cluster, 'node_addresses', []),
            master_addresses=getattr(cluster, 'master_addresses', [])
        )
    
    def get_cluster_credentials(self, cluster_id: str) -> Dict:
        """Args:
            
        Returns:
"""
        try:
            config = self.conn.container_infra.get_cluster_config(cluster_id)
            return config
        except Exception as e:
            logger.error(f"Failed to get cluster credentials: {e}")
            raise
    
    def get_cluster_templates(self) -> List[Dict]:
        """Returns:
"""
        try:
            templates = self.conn.container_infra.cluster_templates()
            template_list = []
            
            for template in templates:
                template_list.append({
                    "id": template.id,
                    "name": template.name,
                    "coe": getattr(template, 'coe', 'kubernetes'),
                    "image_id": getattr(template, 'image_id', ''),
                    "flavor_id": getattr(template, 'flavor_id', ''),
                    "master_flavor_id": getattr(template, 'master_flavor_id', ''),
                    "keypair_id": getattr(template, 'keypair_id', ''),
                    "public": getattr(template, 'public', False),
                    "created_at": str(getattr(template, 'created_at', ''))
                })
                
            return template_list
            
        except Exception as e:
            logger.error(f"Failed to list cluster templates: {e}")
            raise
    
    def cleanup_stuck_clusters(self, hours: int = 24) -> List[str]:
        """Args:
            
        Returns:
"""
        from datetime import datetime, timedelta
        deleted = []
        stuck_statuses = ["CREATE_IN_PROGRESS", "DELETE_IN_PROGRESS", "UPDATE_IN_PROGRESS"]
        cutoff_time = datetime.now() - timedelta(hours=hours)
        try:
            clusters = self.list_clusters()
            for cluster in clusters:
                if cluster.status not in stuck_statuses:
                    continue
                if cluster.created_at and cluster.created_at != 'None':
                    created_at = datetime.fromisoformat(cluster.created_at.replace('Z', '+00:00'))
                    if created_at > cutoff_time:
                        continue
                else:
                    logger.warning(f"No creation time for cluster {cluster.name}, treating as old")
                logger.warning(f"Cleaning up stuck cluster: {cluster.name} ({cluster.status})")
                if self.delete_cluster(cluster.id, force=True):
                    deleted.append(cluster.id)
            logger.info(f"Cleaned up {len(deleted)} stuck clusters")
            return deleted
        except Exception as e:
            logger.error(f"Failed to cleanup stuck clusters: {e}")
            raise
if __name__ == "__main__":
    crud = OpenStackClusterCRUD(cloud_name="openstack")
    print("\n=== Available Templates ===")
    templates = crud.get_cluster_templates()
    for tmpl in templates:
        print(f"- {tmpl['name']} ({tmpl['id']}): {tmpl['coe']}")
    print("\n=== Current Clusters ===")
    clusters = crud.list_clusters()
    for cluster in clusters:
        print(f"- {cluster.name}: {cluster.status} (Nodes: {cluster.node_count})")
"""
    """config = ClusterConfig(
        name="test-cluster-crud",
        cluster_template_id="k8s-1.21-cpu-template",
        node_count=2,
        fixed_network="cloud-platform-selfservice",
        fixed_subnet="cloud-platform-selfservice-subnet"
    )
    
    new_cluster = crud.create_cluster(config)
    print(f"Created cluster: {new_cluster.name} ({new_cluster.id})")
    
    updated = crud.resize_cluster(new_cluster.id, node_count=3)
    print(f"Resized cluster to {updated.node_count} nodes")
    
    if crud.delete_cluster(new_cluster.id):
        print("Cluster deleted successfully")
"""