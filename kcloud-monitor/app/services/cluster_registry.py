"""
Cluster Registry Service - Multi-cluster Prometheus management.

This module provides cluster registration, discovery, and health monitoring
for multi-cluster environments.

Configuration:
- Single cluster: Use PROMETHEUS_URL environment variable (backward compatible)
- Multi-cluster: Use PROMETHEUS_CLUSTERS JSON environment variable

Example PROMETHEUS_CLUSTERS:
[
  {
    "name": "cluster1",
    "url": "http://prometheus1:9090",
    "region": "us-east-1",
    "description": "Production cluster US East"
  },
  {
    "name": "cluster2",
    "url": "http://prometheus2:9090",
    "region": "eu-west-1",
    "description": "Production cluster EU West"
  }
]
"""

from typing import Dict, List, Optional, Tuple
from dataclasses import dataclass
from datetime import datetime
import json
import logging

from app.config import settings
from app.services.prometheus import PrometheusClient, PrometheusException

logger = logging.getLogger(__name__)


@dataclass
class ClusterInfo:
    """Information about a registered cluster."""
    name: str
    url: str
    region: Optional[str] = None
    description: Optional[str] = None
    prometheus_client: Optional[PrometheusClient] = None
    last_health_check: Optional[datetime] = None
    health_status: str = "unknown"  # connected, disconnected, unknown


class ClusterRegistry:
    """
    Registry for managing multiple Prometheus clusters.

    Supports both single-cluster (backward compatible) and multi-cluster modes.
    """

    def __init__(self):
        """Initialize cluster registry from configuration."""
        self._clusters: Dict[str, ClusterInfo] = {}
        self._default_cluster_name: str = settings.DEFAULT_CLUSTER
        self._load_clusters()

    def _load_clusters(self):
        """Load cluster configurations from environment variables."""
        # Try to load multi-cluster configuration
        if settings.PROMETHEUS_CLUSTERS:
            try:
                clusters_config = json.loads(settings.PROMETHEUS_CLUSTERS)
                logger.info(f"Loading {len(clusters_config)} clusters from PROMETHEUS_CLUSTERS")

                for cluster_config in clusters_config:
                    name = cluster_config.get('name')
                    url = cluster_config.get('url')

                    if not name or not url:
                        logger.warning(f"Skipping invalid cluster config: {cluster_config}")
                        continue

                    # Create cluster info
                    cluster = ClusterInfo(
                        name=name,
                        url=url,
                        region=cluster_config.get('region'),
                        description=cluster_config.get('description')
                    )

                    # Create Prometheus client for this cluster
                    cluster.prometheus_client = self._create_prom_client_for_cluster(cluster_config)

                    self._clusters[name] = cluster
                    logger.info(f"Registered cluster: {name} at {url}")

                # Set first cluster as default if not specified
                if self._clusters and self._default_cluster_name not in self._clusters:
                    self._default_cluster_name = list(self._clusters.keys())[0]
                    logger.info(f"Setting default cluster to: {self._default_cluster_name}")

            except json.JSONDecodeError as e:
                logger.error(f"Failed to parse PROMETHEUS_CLUSTERS JSON: {e}")
                logger.info("Falling back to single-cluster mode")
                self._load_default_cluster()
        else:
            # Single cluster mode (backward compatible)
            logger.info("PROMETHEUS_CLUSTERS not set, using single-cluster mode")
            self._load_default_cluster()

    def _load_default_cluster(self):
        """Load default cluster from PROMETHEUS_URL."""
        from app.services import prometheus_client as default_prom_client

        cluster = ClusterInfo(
            name=self._default_cluster_name,
            url=settings.PROMETHEUS_URL,
            description="Default Prometheus cluster",
            prometheus_client=default_prom_client
        )

        self._clusters[self._default_cluster_name] = cluster
        logger.info(f"Registered default cluster: {self._default_cluster_name} at {settings.PROMETHEUS_URL}")

    def _create_prom_client_for_cluster(self, cluster_config: Dict) -> PrometheusClient:
        """
        Create a Prometheus client for a specific cluster.

        Args:
            cluster_config: Cluster configuration dict

        Returns:
            PrometheusClient instance
        """
        # Create a mock settings object with cluster-specific config
        from types import SimpleNamespace

        cluster_settings = SimpleNamespace(
            PROMETHEUS_URL=cluster_config.get('url'),
            PROMETHEUS_TIMEOUT=cluster_config.get('timeout', settings.PROMETHEUS_TIMEOUT),
            PROMETHEUS_USERNAME=cluster_config.get('username', settings.PROMETHEUS_USERNAME),
            PROMETHEUS_PASSWORD=cluster_config.get('password', settings.PROMETHEUS_PASSWORD),
            PROMETHEUS_CA_BUNDLE=cluster_config.get('ca_bundle', settings.PROMETHEUS_CA_BUNDLE)
        )

        return PrometheusClient(cluster_settings)

    def get_cluster(self, cluster_name: Optional[str] = None) -> Optional[ClusterInfo]:
        """
        Get cluster information by name.

        Args:
            cluster_name: Cluster name (uses default if None)

        Returns:
            ClusterInfo if found, None otherwise
        """
        if cluster_name is None:
            cluster_name = self._default_cluster_name

        return self._clusters.get(cluster_name)

    def get_prometheus_client(self, cluster_name: Optional[str] = None) -> Optional[PrometheusClient]:
        """
        Get Prometheus client for a specific cluster.

        Args:
            cluster_name: Cluster name (uses default if None)

        Returns:
            PrometheusClient if cluster found, None otherwise
        """
        cluster = self.get_cluster(cluster_name)
        return cluster.prometheus_client if cluster else None

    def list_clusters(self) -> List[ClusterInfo]:
        """
        Get list of all registered clusters.

        Returns:
            List of ClusterInfo objects
        """
        return list(self._clusters.values())

    def get_cluster_names(self) -> List[str]:
        """
        Get list of all registered cluster names.

        Returns:
            List of cluster names
        """
        return list(self._clusters.keys())

    def get_default_cluster_name(self) -> str:
        """
        Get default cluster name.

        Returns:
            Default cluster name
        """
        return self._default_cluster_name

    def is_multi_cluster(self) -> bool:
        """
        Check if multi-cluster mode is enabled.

        Returns:
            True if more than one cluster is registered
        """
        return len(self._clusters) > 1

    async def check_cluster_health(self, cluster_name: Optional[str] = None) -> Tuple[str, str]:
        """
        Check health of a specific cluster.

        Args:
            cluster_name: Cluster name (checks all if None)

        Returns:
            Tuple of (cluster_name, health_status)
        """
        cluster = self.get_cluster(cluster_name)
        if not cluster:
            return (cluster_name or "unknown", "not_found")

        try:
            status = cluster.prometheus_client.check_health()
            cluster.health_status = status
            cluster.last_health_check = datetime.utcnow()
            logger.debug(f"Cluster {cluster.name} health: {status}")
            return (cluster.name, status)
        except Exception as e:
            logger.error(f"Health check failed for cluster {cluster.name}: {e}")
            cluster.health_status = "error"
            cluster.last_health_check = datetime.utcnow()
            return (cluster.name, "error")

    async def check_all_clusters_health(self) -> Dict[str, str]:
        """
        Check health of all registered clusters.

        Returns:
            Dict mapping cluster names to health status
        """
        health_status = {}

        for cluster_name in self._clusters.keys():
            name, status = await self.check_cluster_health(cluster_name)
            health_status[name] = status

        return health_status

    def get_cluster_summary(self) -> Dict:
        """
        Get summary of all clusters.

        Returns:
            Dict with cluster summary information
        """
        return {
            "total_clusters": len(self._clusters),
            "default_cluster": self._default_cluster_name,
            "multi_cluster_enabled": self.is_multi_cluster(),
            "clusters": [
                {
                    "name": cluster.name,
                    "url": cluster.url,
                    "region": cluster.region,
                    "description": cluster.description,
                    "health_status": cluster.health_status,
                    "last_health_check": cluster.last_health_check.isoformat() if cluster.last_health_check else None
                }
                for cluster in self._clusters.values()
            ]
        }


# Global cluster registry instance
cluster_registry = ClusterRegistry()
