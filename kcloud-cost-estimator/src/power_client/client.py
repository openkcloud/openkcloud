"""
Power Client - Power metrics collection via Prometheus API
"""

import asyncio
import aiohttp
import logging
from datetime import datetime, timedelta
from typing import Dict, List, Optional, Any
from urllib.parse import urljoin

from .metrics import PowerMetrics
from .models import PowerData, ContainerPowerData, NodePowerData

logger = logging.getLogger(__name__)

class PowerClient:
    """Power Prometheus metrics collection client"""
    
    def __init__(self, prometheus_url: str, metrics_interval: str = "30s"):
        """
        Initialize Power client
        
        Args:
            prometheus_url: Prometheus server URL
            metrics_interval: Metrics collection interval
        """
        self.prometheus_url = prometheus_url.rstrip('/')
        self.metrics_interval = metrics_interval
        self.session = None
        
        # Load Power metrics definitions
        self.metrics = PowerMetrics()

        logger.info(f"PowerClient initialized: {prometheus_url}")
    
    async def __aenter__(self):
        """Enter async context manager"""
        await self._ensure_session()
        return self
    
    async def __aexit__(self, exc_type, exc_val, exc_tb):
        """Exit async context manager"""
        await self.close()
    
    async def _ensure_session(self):
        """Create HTTP session"""
        if self.session is None:
            connector = aiohttp.TCPConnector(limit=10)
            timeout = aiohttp.ClientTimeout(total=30)
            self.session = aiohttp.ClientSession(
                connector=connector,
                timeout=timeout
            )
    
    async def close(self):
        """Close HTTP session"""
        if self.session:
            await self.session.close()
            self.session = None
    
    async def health_check(self) -> Dict[str, Any]:
        """Check Power/Prometheus connection status"""
        try:
            await self._ensure_session()
            
            # Check Prometheus status
            health_url = urljoin(self.prometheus_url, "/-/healthy")
            async with self.session.get(health_url) as response:
                prometheus_healthy = response.status == 200
            
            # Check for Power metrics existence
            query = "up{job=~\"power.*\"}"
            power_metrics = await self._prometheus_query(query)
            power_healthy = len(power_metrics.get("data", {}).get("result", [])) > 0
            
            return {
                "prometheus": prometheus_healthy,
                "power": power_healthy,
                "status": "healthy" if prometheus_healthy and power_healthy else "unhealthy",
                "timestamp": datetime.utcnow().isoformat()
            }
            
        except Exception as e:
            logger.error(f"Health check failed: {e}")
            return {
                "prometheus": False,
                "power": False,
                "status": "unhealthy",
                "error": str(e),
                "timestamp": datetime.utcnow().isoformat()
            }
    
    async def _prometheus_query(self, query: str, time_range: Optional[str] = None) -> Dict[str, Any]:
        """Execute Prometheus query"""
        await self._ensure_session()
        
        query_url = urljoin(self.prometheus_url, "/api/v1/query")
        if time_range:
            query_url = urljoin(self.prometheus_url, "/api/v1/query_range")
        
        params = {"query": query}
        
        if time_range:
            # Add time range parameters
            end_time = datetime.utcnow()
            start_time = end_time - self._parse_time_range(time_range)
            
            params.update({
                "start": start_time.timestamp(),
                "end": end_time.timestamp(),
                "step": "30s"  # Collect data at 30-second intervals
            })
        
        try:
            async with self.session.get(query_url, params=params) as response:
                response.raise_for_status()
                return await response.json()
                
        except aiohttp.ClientError as e:
            logger.error(f"Prometheus query failed: {query}, error: {e}")
            raise
    
    def _parse_time_range(self, time_range: str) -> timedelta:
        """Convert time range string to timedelta"""
        if time_range.endswith('m'):
            return timedelta(minutes=int(time_range[:-1]))
        elif time_range.endswith('h'):
            return timedelta(hours=int(time_range[:-1]))
        elif time_range.endswith('d'):
            return timedelta(days=int(time_range[:-1]))
        else:
            return timedelta(minutes=5)  # Default: 5 minutes
    
    async def get_container_power_metrics(
        self,
        namespace: Optional[str] = None,
        workload: Optional[str] = None,
        time_range: str = "5m"
    ) -> List[ContainerPowerData]:
        """Collect power metrics by container"""
        try:
            # Base query
            base_query = "kepler_container_joules_total"
            
            # Add filter conditions
            filters = []
            if namespace:
                filters.append(f'container_namespace="{namespace}"')
            if workload:
                filters.append(f'pod_name=~".*{workload}.*"')
            
            if filters:
                query = f'{base_query}{{{",".join(filters)}}}'
            else:
                query = base_query
            
            # Execute Prometheus query
            result = await self._prometheus_query(query, time_range)
            
            # Parse results
            containers = []
            for series in result.get("data", {}).get("result", []):
                metric = series.get("metric", {})
                values = series.get("values", [])
                
                if values:
                    # Use latest value
                    latest_value = values[-1]
                    timestamp = datetime.fromtimestamp(float(latest_value[0]))
                    power_joules = float(latest_value[1])
                    
                    container_data = ContainerPowerData(
                        container_name=metric.get("container_name", "unknown"),
                        pod_name=metric.get("pod_name", "unknown"),
                        namespace=metric.get("container_namespace", "unknown"),
                        node_name=metric.get("instance", "unknown"),
                        power_joules=power_joules,
                        timestamp=timestamp,
                        labels=metric
                    )
                    containers.append(container_data)
            
            logger.info(f"Container power metrics collection completed: {len(containers)} items")
            return containers
            
        except Exception as e:
            logger.error(f"Container power metrics collection failed: {e}")
            raise
    
    async def get_all_container_metrics(
        self,
        namespace: Optional[str] = None,
        limit: int = 100
    ) -> List[ContainerPowerData]:
        """Collect power metrics for all containers"""
        try:
            # Collect various power metrics
            queries = {
                "total": "kepler_container_joules_total",
                "cpu": "kepler_container_core_joules_total",
                "gpu": "kepler_container_gpu_joules_total",
                "memory": "kepler_container_dram_joules_total",
                "other": "kepler_container_other_joules_total"
            }
            
            containers_map = {}
            
            for metric_type, base_query in queries.items():
                # Apply namespace filter
                if namespace:
                    query = f'{base_query}{{container_namespace="{namespace}"}}'
                else:
                    query = base_query
                
                result = await self._prometheus_query(query)
                
                # Process results
                for series in result.get("data", {}).get("result", []):
                    metric = series.get("metric", {})
                    value_data = series.get("value", [])
                    
                    if value_data and len(value_data) > 1:
                        container_key = (
                            metric.get("container_name", "unknown"),
                            metric.get("pod_name", "unknown"),
                            metric.get("container_namespace", "unknown")
                        )
                        
