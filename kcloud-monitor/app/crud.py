
from datetime import datetime, timedelta
from typing import Dict, List, Any, Optional, Tuple
import json
import io
import csv
import re
import logging
from collections import defaultdict

from app.utils.prometheus_validation import (
    sanitize_label_value,
    build_label_matcher,
    build_label_filter,
    PromQLValidationError
)

logger = logging.getLogger(__name__)

from app.models.responses import (
    GPUPowerResponse,
    GPUSummary,
    GPUInfo,
    WorkloadPower,
    TimeSeriesResponse,
    TimeSeriesMetrics,
    TimeSeriesPoint,
    ClusterInfoResponse,
    ClusterInfo,
    NodeInfo,
    PodPowerResponse as LegacyPodPowerResponse,
    PodInfo as LegacyPodInfo,
    PodDetailResponse as LegacyPodDetailResponse,
    ClusterTotalPowerResponse,
    ClusterPowerTimeSeriesResponse,
    PowerBreakdown,
    EfficiencyMetrics,
    DCGMGPUInfo,
    DCGMGPUMetrics,
    DCGMGPUInfoResponse,
    DCGMGPUMetricsResponse,
    DCGMGPUSummary,
    DCGMGPUTemperature,
    DCGMGPUTemperatureResponse
)
from app.models.infrastructure.nodes import NodeStatus, NodeRole
from app.models.infrastructure.pods import (
    PodInfo as InfraPodInfo,
    PodPowerData as InfraPodPowerData,
    PodPowerSample,
    PodPowerCurrent,
    PodPowerStatistics,
    PodMetrics as InfraPodMetrics,
    PodContainerDetail,
    PodSummary as InfraPodSummary
)
from app.models.infrastructure.containers import (
    ContainerInfo as InfraContainerInfo,
    ContainerMetrics as InfraContainerMetrics
)
from app.models.queries import (
    GPUQueryParams,
    TimeSeriesQueryParams,
    PodQueryParams,
    ClusterTotalQueryParams,
    ContainerQueryParams
)
from app.services import prometheus_client

def _parse_step_to_seconds(step: str) -> int:
    """Convert step string to seconds."""
    # Predefined step values
    step_map = {
        "1m": 60,
        "5m": 300,
        "15m": 900,
        "30m": 1800,
        "1h": 3600,
        "6h": 21600,
        "12h": 43200,
        "1d": 86400
    }
    
    # Check predefined values first
    if step in step_map:
        return step_map[step]
    
    # Parse custom format like "60s", "120m", etc.
    import re
    match = re.match(r'^(\d+)([smhd])$', step)
    if match:
        value = int(match.group(1))
        unit = match.group(2)
        
        unit_multipliers = {
            's': 1,
            'm': 60,
            'h': 3600,
            'd': 86400
        }
        
        return value * unit_multipliers.get(unit, 1)
    
    # Default to 5 minutes
    return 300

def parse_metric(result: List[Dict[str, Any]], metric_name: str) -> Dict[str, float]:
    """Parses a Prometheus metric result and returns a dictionary mapping instance/node to value."""
    data = {}
    for res in result:
        labels = res.get('metric', {})
        # For Kepler metrics, use exported_instance as the primary identifier
        instance = labels.get('exported_instance') or labels.get('instance', 'unknown')
        # Since Kepler provides node-level data, use package or source as differentiation
        package = labels.get('package', 'default')
        key = f"{instance}-{package}"
        value = float(res.get('value', [0, '0'])[1])
        data[key] = value
    return data

async def get_gpu_power_data(params: GPUQueryParams) -> GPUPowerResponse:
    """Fetches and processes GPU power data from Prometheus."""
    
    # This is a simplified implementation. A real one would be more robust and parallel.
    power_query = prometheus_client.build_query("gpu_power", params.instance)
    util_query = prometheus_client.build_query("gpu_utilization", params.instance)
    temp_query = prometheus_client.build_query("gpu_temperature", params.instance)
    mem_used_query = prometheus_client.build_query("gpu_memory_used", params.instance)
    mem_total_query = prometheus_client.build_query("gpu_memory_total", params.instance)

    power_data = parse_metric(prometheus_client.query(power_query).get('data', {}).get('result', []), "gpu_power")
    util_data = parse_metric(prometheus_client.query(util_query).get('data', {}).get('result', []), "gpu_utilization")
    temp_data = parse_metric(prometheus_client.query(temp_query).get('data', {}).get('result', []), "gpu_temperature")
    mem_used_data = parse_metric(prometheus_client.query(mem_used_query).get('data', {}).get('result', []), "gpu_memory_used")
    mem_total_data = parse_metric(prometheus_client.query(mem_total_query).get('data', {}).get('result', []), "gpu_memory_total")

    gpus: List[GPUInfo] = []
    total_power = 0
    total_util = 0

    all_keys = set(power_data.keys())
    for key in all_keys:
        instance, package = key.split('-', 1)
        power = power_data.get(key, 0)
        total_power += power
        total_util += util_data.get(key, 0)

        # For Kepler data, treat each energy package as a "GPU" equivalent
        gpus.append(GPUInfo(
            gpu_id=f"Package-{package}",
            instance=instance,
            power_draw_watts=power,
            utilization_percent=util_data.get(key, 0),  # Will be 0 since not available
            temperature_celsius=temp_data.get(key, 0),  # Will be 0 since not available
            memory_used_mb=int(mem_used_data.get(key, 0) / (1024*1024)) if mem_used_data.get(key) else 0,
            memory_total_mb=int(mem_total_data.get(key, 0) / (1024*1024)) if mem_total_data.get(key) else 0,
        ))

    summary = GPUSummary(
        total_power_watts=total_power,
        avg_power_watts=total_power / len(gpus) if gpus else 0,
        max_power_watts=max(power_data.values()) if power_data else 0,
        avg_utilization_percent=total_util / len(gpus) if gpus else 0,
    )

    return GPUPowerResponse(
        period=params.period.value,
        total_gpus=len(gpus),
        gpus=gpus,
        summary=summary,
        workload_power=WorkloadPower() # Placeholder for now
    )

async def get_timeseries_data(params: TimeSeriesQueryParams) -> TimeSeriesResponse:
    """Fetches and processes enhanced time series data with flexible time ranges."""

    # Determine time range
    if params.start and params.end:
        start_time = params.start
        end_time = params.end
        period_str = None
    elif params.samples:
        # Calculate time range based on samples and step
        end_time = datetime.utcnow()
        step_seconds = _parse_step_to_seconds(params.step)
        total_seconds = step_seconds * params.samples
        start_time = end_time - timedelta(seconds=total_seconds)
        period_str = f"{params.samples} samples"
    else:
        # Use period or default to 1 hour
        end_time = datetime.utcnow()
        period_map = {"1h": timedelta(hours=1), "1d": timedelta(days=1), "1w": timedelta(weeks=1), "1m": timedelta(days=30)}
        period_value = params.period.value if params.period else "1h"
        start_time = end_time - period_map.get(period_value, timedelta(hours=1))
        period_str = period_value

    # Build Kepler query with filters (secure version)
    base_query = "sum(rate(kepler_node_platform_joules_total[5m]))"

    if params.instance or params.cluster or params.node:
        # Build filters using secure validation
        filter_dict = {}
        try:
            if params.instance:
                filter_dict['exported_instance'] = sanitize_label_value(params.instance)
            if params.cluster:
                filter_dict['cluster'] = sanitize_label_value(params.cluster)
            if params.node:
                filter_dict['node'] = sanitize_label_value(params.node)
        except PromQLValidationError as e:
            logger.error(f"Invalid filter value: {e}")
            raise ValueError(f"Invalid filter parameter: {e}")

        # Build safe label filter
        label_filter = build_label_filter(filter_dict)
        query = f"sum(rate(kepler_node_platform_joules_total{label_filter}[5m]))"
    else:
        query = base_query

    result = prometheus_client.query_range(query, start_time, end_time, params.step)

    points = []
    if result.get('data', {}).get('result', []):
        for res in result['data']['result'][0].get('values', []):
            points.append(TimeSeriesPoint(timestamp=datetime.fromtimestamp(res[0]), value=float(res[1])))

    return TimeSeriesResponse(
        period=period_str,
        step=params.step,
        start_time=start_time,
        end_time=end_time,
        total_samples=len(points),
        metrics=TimeSeriesMetrics(gpu_total_power=points)
    )

def format_data_for_export(data: GPUPowerResponse, format_type: str):
    """Formats data into JSON or CSV for export."""
    if format_type == "json":
        # Pydantic's model_dump_json is a convenient way to get a JSON string
        return data.model_dump_json(indent=2)
    
    if format_type == "csv":
        output = io.StringIO()
        writer = csv.writer(output)
        
        writer.writerow(["timestamp", "gpu_id", "instance", "power_draw_watts", "utilization_percent", "temperature_celsius", "memory_used_mb", "memory_total_mb"])
        
        for gpu in data.gpus:
            writer.writerow([data.timestamp.isoformat(), gpu.gpu_id, gpu.instance, gpu.power_draw_watts, gpu.utilization_percent, gpu.temperature_celsius, gpu.memory_used_mb, gpu.memory_total_mb])
            
        return output.getvalue()
    
    raise ValueError("Unsupported format")

# Cluster and Pod Management Functions

async def get_cluster_info() -> ClusterInfoResponse:
    """
    Fetches cluster information including nodes and pods.

    Uses kube_node_info as primary source (includes all nodes including masters),
    and enriches with kepler_node_info data when available.
    """

    # Step 1: Get all nodes from kube_node_info (includes master nodes)
    kube_query = "kube_node_info"
    kube_result = prometheus_client.query(kube_query).get('data', {}).get('result', [])

    # Step 2: Get Kepler data for nodes that have it installed
    kepler_query = "kepler_node_info"
    kepler_result = prometheus_client.query(kepler_query).get('data', {}).get('result', [])

    # Build a map of kepler data by node name
    kepler_data_map = {}
    for kepler_data in kepler_result:
        labels = kepler_data.get('metric', {})
        instance = labels.get('instance', 'unknown')
        node_ip = instance.split(':')[0] if ':' in instance else instance
        kepler_data_map[node_ip] = {
            'instance': instance,
            'cpu_architecture': labels.get('cpu_architecture'),
            'power_source': labels.get('platform_power_source')
        }

    nodes = []
    node_instances = set()

    # Step 3: Process kube_node_info results and enrich with Kepler data
    for node_data in kube_result:
        labels = node_data.get('metric', {})
        node_name = labels.get('node', 'unknown')
        internal_ip = labels.get('internal_ip', 'unknown')
        node_instances.add(internal_ip)

        # Determine node role from node name
        # Master nodes typically have 'master' in their name
        role = "master" if "master" in node_name.lower() else "worker"

        # Check if Kepler data is available for this node
        kepler_info = kepler_data_map.get(internal_ip, {})
        has_kepler = internal_ip in kepler_data_map

        # Extract container runtime version (format: "cri-o://1.31.0" -> "cri-o 1.31.0")
        container_runtime_raw = labels.get('container_runtime_version', '')
        container_runtime = container_runtime_raw.replace('://', ' ') if container_runtime_raw else None

        nodes.append(NodeInfo(
            node_name=node_name,
            instance=kepler_info.get('instance', f"{internal_ip}:9102"),  # Use Kepler instance if available
            internal_ip=internal_ip,
            role=role,
            cpu_architecture=kepler_info.get('cpu_architecture'),  # From Kepler if available
            power_source=kepler_info.get('power_source'),  # From Kepler if available
            os_image=labels.get('os_image'),
            kernel_version=labels.get('kernel_version'),
            kubelet_version=labels.get('kubelet_version'),
            container_runtime=container_runtime,
            status="active",
            has_kepler=has_kepler
        ))

    # Get pod information from container metrics
    pod_query = "sum(rate(kepler_container_package_joules_total[5m])) by (pod_name, container_namespace)"
    pod_result = prometheus_client.query(pod_query).get('data', {}).get('result', [])

    active_pods = len(pod_result)
    namespaces = set()

    for pod_data in pod_result:
        labels = pod_data.get('metric', {})
        namespace = labels.get('container_namespace', 'unknown')
        namespaces.add(namespace)

    cluster = ClusterInfo(
        cluster_name="default",
        nodes=nodes,
        total_nodes=len(nodes),
        active_pods=active_pods,
        namespaces=sorted(list(namespaces))
    )

    return ClusterInfoResponse(cluster=cluster)

async def get_pod_power_data(params: PodQueryParams) -> LegacyPodPowerResponse:
    """Fetches pod-level power consumption data."""

    # Build query with namespace filter if provided
    if params.namespace:
        query = f'sum(rate(kepler_container_package_joules_total{{container_namespace="{params.namespace}"}}[5m])) by (pod_name, container_namespace)'
    else:
        query = "sum(rate(kepler_container_package_joules_total[5m])) by (pod_name, container_namespace)"

    result = prometheus_client.query(query).get('data', {}).get('result', [])

    pods = []
    total_power = 0
    namespace_power = {}

    for pod_data in result:
        labels = pod_data.get('metric', {})
        power_value = float(pod_data.get('value', [0, '0'])[1])

        pod_name = labels.get('pod_name', 'unknown')
        namespace = labels.get('container_namespace', 'unknown')

        # Apply power filtering if specified
        if params.min_power is not None and power_value < params.min_power:
            continue
        if params.max_power is not None and power_value > params.max_power:
            continue

        pods.append(LegacyPodInfo(
            pod_name=pod_name,
            namespace=namespace,
            container_namespace=namespace,
            power_watts=power_value
        ))

        total_power += power_value
        namespace_power[namespace] = namespace_power.get(namespace, 0) + power_value

    return LegacyPodPowerResponse(
        cluster_name=params.cluster or "default",
        namespace_filter=params.namespace,
        total_pods=len(pods),
        pods=pods,
        total_power_watts=total_power,
        namespaces_summary=namespace_power
    )

async def get_pod_detail(namespace: str, pod_name: str) -> LegacyPodDetailResponse:
    """Fetches detailed power information for a specific pod."""

    # Query for specific pod containers
    query = f'rate(kepler_container_package_joules_total{{pod_name="{pod_name}", container_namespace="{namespace}"}}[5m])'

    result = prometheus_client.query(query).get('data', {}).get('result', [])

    containers = []
    total_power = 0

    for container_data in result:
        labels = container_data.get('metric', {})
        power_value = float(container_data.get('value', [0, '0'])[1])

        container_name = labels.get('container_name', 'unknown')
        containers.append({container_name: power_value})
        total_power += power_value

    return LegacyPodDetailResponse(
        pod_name=pod_name,
        namespace=namespace,
        power_watts=total_power,
        containers=containers
    )


# ============================================================================
# New Pod Monitoring Functions (Phase 4.2)
# ============================================================================

def _phase_to_status(phase: Optional[str]) -> str:
    """Map Kubernetes pod phase to PodStatus value."""
    mapping = {
        "Running": "running",
        "Pending": "pending",
        "Succeeded": "succeeded",
        "Failed": "failed",
        "Unknown": "unknown"
    }
    return mapping.get(phase or "Unknown", "unknown")


async def get_pod_list(
    params: PodQueryParams,
    include_metrics: bool = False,
    include_power: bool = True
) -> Dict[str, Any]:
    """Return pod metadata enriched with optional metrics and power information."""
    cluster_param = params.cluster or "default"
    namespace_filter = params.namespace
    node_filter = params.node
    label_selector = _parse_label_selector(params.label_selector)

    pod_info_result = prometheus_client.query("kube_pod_info").get('data', {}).get('result', [])
    container_info_result = prometheus_client.query("kube_pod_container_info").get('data', {}).get('result', [])

    container_names_map: Dict[Tuple[str, str], List[str]] = defaultdict(list)
    for res in container_info_result:
        labels = res.get('metric', {})
        namespace = labels.get('namespace')
        pod = labels.get('pod') or labels.get('pod_name')
        container = labels.get('container') or labels.get('container_name')
        if not namespace or not pod or not container:
            continue
        container_names_map[(namespace, pod)].append(container)

    # Build power query filters (secure version)
    filter_dict = {}
    try:
        if namespace_filter:
            filter_dict['container_namespace'] = sanitize_label_value(namespace_filter)
        if node_filter:
            filter_dict['node'] = sanitize_label_value(node_filter)
    except PromQLValidationError as e:
        logger.error(f"Invalid filter value in get_pod_list: {e}")
        raise ValueError(f"Invalid filter parameter: {e}")

    power_selector = build_label_filter(filter_dict)
    power_query = f'sum(rate(kepler_container_package_joules_total{power_selector}[5m])) by (container_namespace, pod_name)'
    power_result = prometheus_client.query(power_query).get('data', {}).get('result', [])
    power_map = _map_namespace_pod(power_result, namespace_label="container_namespace", pod_label="pod_name")

    cpu_request_map = _map_namespace_pod(
        prometheus_client.query("sum(kube_pod_container_resource_requests_cpu_cores) by (namespace,pod)").get('data', {}).get('result', [])
    )
    cpu_limit_map = _map_namespace_pod(
        prometheus_client.query("sum(kube_pod_container_resource_limits_cpu_cores) by (namespace,pod)").get('data', {}).get('result', [])
    )
    memory_request_map = {
        key: _bytes_to_mb(value)
        for key, value in _map_namespace_pod(
            prometheus_client.query("sum(kube_pod_container_resource_requests_memory_bytes) by (namespace,pod)").get('data', {}).get('result', [])
        ).items()
        if value is not None
    }
    memory_limit_map = {
        key: _bytes_to_mb(value)
        for key, value in _map_namespace_pod(
            prometheus_client.query("sum(kube_pod_container_resource_limits_memory_bytes) by (namespace,pod)").get('data', {}).get('result', [])
        ).items()
        if value is not None
    }
    gpu_request_map = {
        key: int(value) if value is not None else 0
        for key, value in _map_namespace_pod(
            prometheus_client.query('sum(kube_pod_container_resource_requests{resource="nvidia.com/gpu"}) by (namespace,pod)').get('data', {}).get('result', [])
        ).items()
    }

    phase_result = prometheus_client.query("kube_pod_status_phase").get('data', {}).get('result', [])
    phase_map: Dict[Tuple[str, str], str] = {}
    for res in phase_result:
        labels = res.get('metric', {})
        namespace = labels.get('namespace')
        pod = labels.get('pod')
        if not namespace or not pod:
            continue
        value = _safe_float(res.get('value', [0, '0'])[1])
        if value != 1:
            continue
        phase_map[(namespace, pod)] = labels.get('phase', 'Unknown')

    cpu_usage_millicores: Dict[Tuple[str, str], int] = {}
    memory_used_mb: Dict[Tuple[str, str], int] = {}
    if include_metrics:
        cpu_usage_result = prometheus_client.query(
            'sum(rate(container_cpu_usage_seconds_total{namespace!="" ,pod!=""}[5m])) by (namespace,pod)'
        ).get('data', {}).get('result', [])
        for key, value in _map_namespace_pod(cpu_usage_result).items():
            if value is None:
                continue
            cpu_usage_millicores[key] = int(max(value, 0) * 1000)

        memory_usage_result = prometheus_client.query(
            'sum(container_memory_working_set_bytes{namespace!="" ,pod!=""}) by (namespace,pod)'
        ).get('data', {}).get('result', [])
        for key, value in _map_namespace_pod(memory_usage_result).items():
            if value is None:
                continue
            memory_used_mb[key] = _bytes_to_mb(value)

    pods: List[Dict[str, Any]] = []
    namespaces_summary: Dict[str, int] = {}
    pods_by_status: Dict[str, int] = {}
    total_power = 0.0

    for res in pod_info_result:
        labels = res.get('metric', {})
        namespace = labels.get('namespace')
        pod_name = labels.get('pod')
        if not namespace or not pod_name:
            continue

        node_name = labels.get('node')
        cluster_label = labels.get('cluster') or labels.get('cluster_name')
        cluster_value = cluster_label or cluster_param

        if namespace_filter and namespace != namespace_filter:
            continue
        if node_filter and node_name and node_name != node_filter:
            continue
        if params.cluster and cluster_label and cluster_label != params.cluster:
            continue

        label_dict = _extract_k8s_labels(labels)
        if label_selector and not _labels_match_selector(label_dict, label_selector):
            continue

        key = (namespace, pod_name)
        current_power = power_map.get(key)
        if include_power:
            if params.min_power is not None and (current_power is None or current_power < params.min_power):
                continue
            if params.max_power is not None and current_power is not None and current_power > params.max_power:
                continue
        else:
            current_power = None

        phase_value = phase_map.get(key, "Unknown")
        status_value = _phase_to_status(phase_value)

        pod_record = {
            'pod_name': pod_name,
            'namespace': namespace,
            'uid': labels.get('uid'),
            'cluster': cluster_value,
            'node_name': node_name,
            'status': status_value,
            'phase': phase_value,
            'container_count': len(container_names_map.get(key, [])),
            'container_names': container_names_map.get(key),
            'cpu_request': _format_cpu_value(cpu_request_map.get(key)),
            'cpu_limit': _format_cpu_value(cpu_limit_map.get(key)),
            'memory_request_mb': memory_request_map.get(key),
            'memory_limit_mb': memory_limit_map.get(key),
            'gpu_count': gpu_request_map.get(key, 0) or 0,
            'npu_count': 0,
            'labels': label_dict or None,
            'annotations': None,
            'workload_type': labels.get('created_by_kind'),
            'workload_name': labels.get('created_by_name'),
            'created_at': None,
            'started_at': None,
            'current_power_watts': current_power,
            'cpu_usage_millicores': cpu_usage_millicores.get(key) if include_metrics else None,
            'memory_used_mb': memory_used_mb.get(key) if include_metrics else None
        }

        pods.append(pod_record)
        namespaces_summary[namespace] = namespaces_summary.get(namespace, 0) + 1
        pods_by_status[status_value] = pods_by_status.get(status_value, 0) + 1

        if include_power and current_power is not None:
            total_power += current_power

    return {
        'cluster': cluster_param,
        'namespace_filter': namespace_filter,
        'node_filter': node_filter,
        'total_pods': len(pods),
        'pods': pods,
        'total_power_watts': total_power if include_power else None,
        'namespaces_summary': namespaces_summary,
        'pods_by_status': pods_by_status
    }


async def _collect_pod_containers(namespace: str, pod_name: str) -> List[Dict[str, Any]]:
    """Collect container details for a single pod."""
    container_info_result = prometheus_client.query(
        f'kube_pod_container_info{{namespace="{namespace}",pod="{pod_name}"}}'
    ).get('data', {}).get('result', [])

    if not container_info_result:
        return []

    lookup_keys: List[Tuple[str, str, str]] = []
    container_records: Dict[Tuple[str, str, str], Dict[str, Any]] = {}
    for res in container_info_result:
        labels = res.get('metric', {})
        container = labels.get('container') or labels.get('container_name')
        if not container:
            continue
        key = (namespace, pod_name, container)
        lookup_keys.append(key)
        container_records[key] = {
            'name': container,
            'image': labels.get('image'),
            'status': None,
            'cpu_request': None,
            'cpu_limit': None,
            'memory_request_mb': None,
            'memory_limit_mb': None,
            'gpu_request': None,
            'restarts': None
        }

    def _update_records(metric_query: str, attr: str, formatter=None):
        result = prometheus_client.query(metric_query).get('data', {}).get('result', [])
        value_map = _map_namespace_pod_container(result)
        for key, value in value_map.items():
            if key not in container_records or value is None:
                continue
            container_records[key][attr] = formatter(value) if formatter else value

    _update_records(
        f'sum(kube_pod_container_resource_requests_cpu_cores{{namespace="{namespace}",pod="{pod_name}"}}) by (namespace,pod,container)',
        'cpu_request',
        _format_cpu_value
    )
    _update_records(
        f'sum(kube_pod_container_resource_limits_cpu_cores{{namespace="{namespace}",pod="{pod_name}"}}) by (namespace,pod,container)',
        'cpu_limit',
        _format_cpu_value
    )
    _update_records(
        f'sum(kube_pod_container_resource_requests_memory_bytes{{namespace="{namespace}",pod="{pod_name}"}}) by (namespace,pod,container)',
        'memory_request_mb',
        lambda v: _bytes_to_mb(v)
    )
    _update_records(
        f'sum(kube_pod_container_resource_limits_memory_bytes{{namespace="{namespace}",pod="{pod_name}"}}) by (namespace,pod,container)',
        'memory_limit_mb',
        lambda v: _bytes_to_mb(v)
    )
    _update_records(
        f'sum(kube_pod_container_resource_requests{{namespace="{namespace}",pod="{pod_name}",resource="nvidia.com/gpu"}}) by (namespace,pod,container)',
        'gpu_request',
        lambda v: int(v)
    )
    _update_records(
        f'sum(kube_pod_container_status_restarts_total{{namespace="{namespace}",pod="{pod_name}"}}) by (namespace,pod,container)',
        'restarts',
        lambda v: int(v)
    )

    ready_result = prometheus_client.query(
        f'kube_pod_container_status_ready{{namespace="{namespace}",pod="{pod_name}"}}'
    ).get('data', {}).get('result', [])
    ready_map = _map_namespace_pod_container(ready_result)

    waiting_result = prometheus_client.query(
        f'kube_pod_container_status_waiting_reason{{namespace="{namespace}",pod="{pod_name}"}}'
    ).get('data', {}).get('result', [])
    waiting_map = _map_namespace_pod_container(waiting_result)

    terminated_result = prometheus_client.query(
        f'kube_pod_container_status_terminated_reason{{namespace="{namespace}",pod="{pod_name}"}}'
    ).get('data', {}).get('result', [])
    terminated_map = _map_namespace_pod_container(terminated_result)

    container_details: List[Dict[str, Any]] = []
    for key in lookup_keys:
        record = container_records.get(key)
        if not record:
            continue

        ready_value = ready_map.get(key)
        waiting_value = waiting_map.get(key)
        terminated_value = terminated_map.get(key)

        status = "unknown"
        if ready_value is not None and ready_value >= 1:
            status = "running"
        elif waiting_value is not None and waiting_value >= 1:
            status = "waiting"
        elif terminated_value is not None and terminated_value >= 1:
            status = "terminated"

        record['status'] = status

        container_details.append(PodContainerDetail(**record).dict())

    return container_details


async def _collect_pod_metrics(namespace: str, pod_name: str) -> Dict[str, Any]:
    """Collect pod-level resource metrics."""
    filter_selector = f'{{namespace="{namespace}",pod="{pod_name}"}}'

    cpu_usage_result = prometheus_client.query(
        f'sum(rate(container_cpu_usage_seconds_total{filter_selector}[5m]))'
    ).get('data', {}).get('result', [])
    cpu_usage_value = _safe_float(cpu_usage_result[0].get('value', [0, '0'])[1]) if cpu_usage_result else None

    memory_used_result = prometheus_client.query(
        f'sum(container_memory_usage_bytes{filter_selector})'
    ).get('data', {}).get('result', [])
    memory_used_value = _safe_float(memory_used_result[0].get('value', [0, '0'])[1]) if memory_used_result else None

    memory_working_set_result = prometheus_client.query(
        f'sum(container_memory_working_set_bytes{filter_selector})'
    ).get('data', {}).get('result', [])
    memory_working_set_value = _safe_float(memory_working_set_result[0].get('value', [0, '0'])[1]) if memory_working_set_result else None

    network_rx_result = prometheus_client.query(
        f'sum(rate(container_network_receive_bytes_total{filter_selector}[5m]))'
    ).get('data', {}).get('result', [])
    network_tx_result = prometheus_client.query(
        f'sum(rate(container_network_transmit_bytes_total{filter_selector}[5m]))'
    ).get('data', {}).get('result', [])

    fs_usage_result = prometheus_client.query(
        f'sum(container_fs_usage_bytes{filter_selector})'
    ).get('data', {}).get('result', [])

    ready_containers_result = prometheus_client.query(
        f'sum(kube_pod_container_status_ready{{namespace="{namespace}",pod="{pod_name}"}})'
    ).get('data', {}).get('result', [])

    restart_count_result = prometheus_client.query(
        f'sum(kube_pod_container_status_restarts_total{{namespace="{namespace}",pod="{pod_name}"}})'
    ).get('data', {}).get('result', [])

    cpu_usage_millicores = int(cpu_usage_value * 1000) if cpu_usage_value is not None else None
    network_rx_mbps = (network_rx_result and _safe_float(network_rx_result[0].get('value', [0, '0'])[1])) or None
    network_tx_mbps = (network_tx_result and _safe_float(network_tx_result[0].get('value', [0, '0'])[1])) or None

    if network_rx_mbps is not None:
        network_rx_mbps = network_rx_mbps * 8 / 1_000_000
    if network_tx_mbps is not None:
        network_tx_mbps = network_tx_mbps * 8 / 1_000_000

    metrics = InfraPodMetrics(
        pod_name=pod_name,
        namespace=namespace,
        timestamp=datetime.utcnow(),
        cpu_usage_millicores=cpu_usage_millicores,
        cpu_utilization_percent=None,
        memory_used_mb=_bytes_to_mb(memory_used_value) if memory_used_value is not None else None,
        memory_working_set_mb=_bytes_to_mb(memory_working_set_value) if memory_working_set_value is not None else None,
        memory_utilization_percent=None,
        network_rx_mbps=network_rx_mbps,
        network_tx_mbps=network_tx_mbps,
        fs_used_mb=_bytes_to_mb(_safe_float(fs_usage_result[0].get('value', [0, '0'])[1])) if fs_usage_result else None,
        container_count=len(await _collect_pod_containers(namespace, pod_name)),
        ready_containers=int(_safe_float(ready_containers_result[0].get('value', [0, '0'])[1])) if ready_containers_result else 0,
        restarts=int(_safe_float(restart_count_result[0].get('value', [0, '0'])[1])) if restart_count_result else 0
    )
    return metrics.dict()


async def get_pod_detail_extended(
    namespace: str,
    pod_name: str,
    include_metrics: bool = True,
    include_power: bool = True
) -> Dict[str, Any]:
    """Return detailed information for a specific pod."""
    params = PodQueryParams(namespace=namespace, cluster=None)
    list_data = await get_pod_list(params, include_metrics=include_metrics, include_power=include_power)

    pod_entry = next(
        (pod for pod in list_data['pods'] if pod.get('pod_name') == pod_name and pod.get('namespace') == namespace),
        None
    )
    if not pod_entry:
        raise ValueError(f"Pod {namespace}/{pod_name} not found")

    metrics = await _collect_pod_metrics(namespace, pod_name) if include_metrics else None
    power = await get_pod_power(namespace, pod_name) if include_power else None
    containers = await _collect_pod_containers(namespace, pod_name)

    return {
        'pod': pod_entry,
        'metrics': metrics,
        'power': InfraPodPowerData(**power).dict() if power else None,
        'containers': containers
    }


async def get_pod_power(namespace: str, pod_name: str, period: Optional[str] = "1h") -> Dict[str, Any]:
    """Return power data for a specific pod with timeseries breakdown."""
    end_time = datetime.utcnow()
    period_map = {
        "1h": timedelta(hours=1),
        "1d": timedelta(days=1),
        "1w": timedelta(weeks=1),
        "1m": timedelta(days=30)
    }
    duration = period_map.get(period, timedelta(hours=1))
    start_time = end_time - duration

    selector = f'{{pod_name="{pod_name}",container_namespace="{namespace}"}}'
    total_power_query = f'sum(rate(kepler_container_package_joules_total{selector}[5m]))'
    cpu_power_query = f'sum(rate(kepler_container_core_joules_total{selector}[5m]))'
    dram_power_query = f'sum(rate(kepler_container_dram_joules_total{selector}[5m]))'
    accelerator_power_query = f'sum(rate(kepler_container_accelerator_joules_total{selector}[5m]))'

    total_result = prometheus_client.query(total_power_query).get('data', {}).get('result', [])
    cpu_result = prometheus_client.query(cpu_power_query).get('data', {}).get('result', [])
    dram_result = prometheus_client.query(dram_power_query).get('data', {}).get('result', [])
    accel_result = prometheus_client.query(accelerator_power_query).get('data', {}).get('result', [])

    total_power = _safe_float(total_result[0].get('value', [0, '0'])[1]) if total_result else 0.0
    cpu_power = _safe_float(cpu_result[0].get('value', [0, '0'])[1]) if cpu_result else None
    dram_power = _safe_float(dram_result[0].get('value', [0, '0'])[1]) if dram_result else None
    accel_power = _safe_float(accel_result[0].get('value', [0, '0'])[1]) if accel_result else None

    container_breakdown_query = f'sum(rate(kepler_container_package_joules_total{selector}[5m])) by (container_name)'
    container_breakdown_result = prometheus_client.query(container_breakdown_query).get('data', {}).get('result', [])
    container_power: Dict[str, float] = {}
    for res in container_breakdown_result:
        labels = res.get('metric', {})
        container_name = labels.get('container_name')
        value = _safe_float(res.get('value', [0, '0'])[1])
        if container_name and value is not None:
            container_power[container_name] = value

    timeseries_result = prometheus_client.query_range(
        total_power_query,
        start_time,
        end_time,
        "5m"
    )

    timeseries_points: List[PodPowerSample] = []
    power_values: List[float] = []
    series = timeseries_result.get('data', {}).get('result', [])
    if series:
        for timestamp, value in series[0].get('values', []):
            numeric_value = _safe_float(value)
            if numeric_value is None:
                continue
            power_values.append(numeric_value)
            timeseries_points.append(PodPowerSample(
                timestamp=datetime.fromtimestamp(timestamp),
                power_watts=numeric_value
            ))

    avg_power = sum(power_values) / len(power_values) if power_values else total_power
    max_power = max(power_values) if power_values else total_power
    min_power = min(power_values) if power_values else total_power
    runtime_hours = (end_time - start_time).total_seconds() / 3600
    total_energy_kwh = (avg_power * runtime_hours) / 1000 if avg_power is not None else None

    power_data = InfraPodPowerData(
        pod_name=pod_name,
        namespace=namespace,
        period=period,
        start_time=start_time,
        end_time=end_time,
        current=PodPowerCurrent(
            total_power_watts=total_power or 0.0,
            cpu_power_watts=cpu_power,
            dram_power_watts=dram_power,
            gpu_power_watts=accel_power,
            container_power_watts=container_power or None
        ),
        statistics=PodPowerStatistics(
            avg_power_watts=avg_power,
            max_power_watts=max_power,
            min_power_watts=min_power,
            total_energy_kwh=total_energy_kwh,
            runtime_hours=runtime_hours
        ),
        timeseries=[sample.dict() for sample in timeseries_points] if timeseries_points else None
    )

    return power_data.dict()


async def get_pod_summary(cluster: Optional[str] = None, namespace: Optional[str] = None) -> Dict[str, Any]:
    """Return aggregated pod summary statistics."""
    params = PodQueryParams(cluster=cluster, namespace=namespace)
    pod_list = await get_pod_list(params, include_metrics=False, include_power=True)
    pods = pod_list['pods']

    total_pods = len(pods)
    total_power = pod_list.get('total_power_watts') or 0.0
    namespaces_summary = pod_list.get('namespaces_summary') or {}
    pods_by_status = pod_list.get('pods_by_status') or {}

    running_pods = pods_by_status.get('running', 0)
    pending_pods = pods_by_status.get('pending', 0)
    failed_pods = pods_by_status.get('failed', 0)
    avg_power = total_power / total_pods if total_pods > 0 else None

    top_pods = sorted(
        pods,
        key=lambda pod: pod.get('current_power_watts') or 0.0,
        reverse=True
    )[:5]

    top_pods_models = [InfraPodInfo(**pod) for pod in top_pods] if top_pods else None

    summary = InfraPodSummary(
        total_pods=total_pods,
        namespaces=len(namespaces_summary),
        running_pods=running_pods,
        pending_pods=pending_pods,
        failed_pods=failed_pods,
        total_power_watts=total_power,
        avg_power_per_pod_watts=avg_power,
        top_pods_by_power=top_pods_models
    )

    return {
        'timestamp': datetime.utcnow(),
        'cluster': pod_list['cluster'],
        'namespace_filter': namespace,
        'summary': summary.dict()
    }


# ============================================================================
# Container Monitoring Functions (Phase 4.3)
# ============================================================================

def _collect_container_base_info() -> Tuple[Dict[str, Dict[str, Any]], Dict[Tuple[str, str, str], str]]:
    """Collect base container metadata from kube_pod_container_info."""
    result = prometheus_client.query("kube_pod_container_info").get('data', {}).get('result', [])
    containers: Dict[str, Dict[str, Any]] = {}
    lookup_index: Dict[Tuple[str, str, str], str] = {}

    for res in result:
        labels = res.get('metric', {})
        namespace = labels.get('namespace')
        pod = labels.get('pod') or labels.get('pod_name')
        container = labels.get('container') or labels.get('container_name')
        if not namespace or not pod or not container:
            continue

        container_id = labels.get('container_id') or labels.get('id') or f"{namespace}/{pod}/{container}"
        lookup_key = (namespace, pod, container)

        containers[container_id] = {
            'container_id': container_id,
            'container_name': container,
            'pod_name': pod,
            'namespace': namespace,
            'cluster': labels.get('cluster') or labels.get('cluster_name') or "default",
            'image': labels.get('image', ''),
            'image_id': labels.get('image_id'),
            'node_name': labels.get('node'),
            'status': "unknown",
            'cpu_request': None,
            'cpu_limit': None,
            'memory_request_mb': None,
            'memory_limit_mb': None,
            'restart_policy': None,
            'restart_count': 0,
            'created_at': None,
            'started_at': None,
            'finished_at': None,
            'current_power_watts': None,
            '_lookup_key': lookup_key
        }
        lookup_index[lookup_key] = container_id

    return containers, lookup_index


def _apply_container_status(
    containers_map: Dict[str, Dict[str, Any]],
    ready_map: Dict[Tuple[str, str, str], float],
    waiting_map: Dict[Tuple[str, str, str], float],
    terminated_map: Dict[Tuple[str, str, str], float]
) -> None:
    """Apply status values to container records."""
    for record in containers_map.values():
        lookup_key = record.get('_lookup_key')
        if not lookup_key:
            continue
        ready_value = ready_map.get(lookup_key)
        waiting_value = waiting_map.get(lookup_key)
        terminated_value = terminated_map.get(lookup_key)

        status = "unknown"
        if ready_value is not None and ready_value >= 1:
            status = "running"
        elif waiting_value is not None and waiting_value >= 1:
            status = "waiting"
        elif terminated_value is not None and terminated_value >= 1:
            status = "terminated"

        record['status'] = status


async def get_container_list(
    params: ContainerQueryParams,
    include_metrics: bool = False
) -> Dict[str, Any]:
    """Return container list with optional power filtering."""
    containers_map, lookup_index = _collect_container_base_info()
    if not containers_map:
        return {
            'cluster': params.cluster or "default",
            'pod_filter': params.pod,
            'namespace_filter': params.namespace,
            'total_containers': 0,
            'containers': []
        }

    # Build supporting metric maps
    ready_map = _map_namespace_pod_container(
        prometheus_client.query("kube_pod_container_status_ready").get('data', {}).get('result', [])
    )
    waiting_map = _map_namespace_pod_container(
        prometheus_client.query("kube_pod_container_status_waiting_reason").get('data', {}).get('result', [])
    )
    terminated_map = _map_namespace_pod_container(
        prometheus_client.query("kube_pod_container_status_terminated_reason").get('data', {}).get('result', [])
    )
    _apply_container_status(containers_map, ready_map, waiting_map, terminated_map)

    cpu_request_map = _map_namespace_pod_container(
        prometheus_client.query("kube_pod_container_resource_requests_cpu_cores").get('data', {}).get('result', [])
    )
    cpu_limit_map = _map_namespace_pod_container(
        prometheus_client.query("kube_pod_container_resource_limits_cpu_cores").get('data', {}).get('result', [])
    )
    memory_request_map = {
        key: _bytes_to_mb(value)
        for key, value in _map_namespace_pod_container(
            prometheus_client.query("kube_pod_container_resource_requests_memory_bytes").get('data', {}).get('result', [])
        ).items()
        if value is not None
    }
    memory_limit_map = {
        key: _bytes_to_mb(value)
        for key, value in _map_namespace_pod_container(
            prometheus_client.query("kube_pod_container_resource_limits_memory_bytes").get('data', {}).get('result', [])
        ).items()
        if value is not None
    }
    restart_map = {
        key: int(value) if value is not None else 0
        for key, value in _map_namespace_pod_container(
            prometheus_client.query("kube_pod_container_status_restarts_total").get('data', {}).get('result', [])
        ).items()
    }

    power_result = prometheus_client.query(
        "sum(rate(kepler_container_package_joules_total[5m])) by (container_id, container_name, container_namespace, pod_name)"
    ).get('data', {}).get('result', [])
    power_by_id: Dict[str, float] = {}
    power_by_lookup: Dict[Tuple[str, str, str], float] = {}
    for res in power_result:
        labels = res.get('metric', {})
        value = _safe_float(res.get('value', [0, '0'])[1])
        if value is None:
            continue
        cid = labels.get('container_id')
        namespace = labels.get('container_namespace') or labels.get('namespace')
        pod = labels.get('pod_name') or labels.get('pod')
        container = labels.get('container_name') or labels.get('container')
        if cid:
            power_by_id[cid] = value
        if namespace and pod and container:
            power_by_lookup[(namespace, pod, container)] = value

    containers: List[Dict[str, Any]] = []
    for container_id, record in containers_map.items():
        namespace, pod_name, container_name = record['_lookup_key']

        if params.namespace and namespace != params.namespace:
            continue
        if params.pod and pod_name != params.pod:
            continue
        if params.node and record.get('node_name') and record.get('node_name') != params.node:
            continue
        if params.cluster and record.get('cluster') and record.get('cluster') != params.cluster:
            continue

        status = record.get('status', 'unknown')
        if not params.include_terminated and status == "terminated":
            continue

        lookup_key = record['_lookup_key']
        record['cpu_request'] = _format_cpu_value(cpu_request_map.get(lookup_key))
        record['cpu_limit'] = _format_cpu_value(cpu_limit_map.get(lookup_key))
        record['memory_request_mb'] = memory_request_map.get(lookup_key)
        record['memory_limit_mb'] = memory_limit_map.get(lookup_key)
        record['restart_count'] = restart_map.get(lookup_key, 0)

        current_power = power_by_id.get(container_id)
        if current_power is None:
            current_power = power_by_lookup.get(lookup_key)

        record['current_power_watts'] = current_power

        if params.min_power is not None and (current_power is None or current_power < params.min_power):
            continue
        if params.max_power is not None and current_power is not None and current_power > params.max_power:
            continue

        sanitized_record = {k: v for k, v in record.items() if k != '_lookup_key'}
        containers.append(sanitized_record)

    return {
        'cluster': params.cluster or "default",
        'pod_filter': params.pod,
        'namespace_filter': params.namespace,
        'total_containers': len(containers),
        'containers': containers
    }


async def get_container_detail(
    container_id: str,
    include_metrics: bool = True
) -> Dict[str, Any]:
    """Return detailed information for a specific container."""
    listing = await get_container_list(ContainerQueryParams(), include_metrics=False)
    container_entry = next(
        (container for container in listing['containers'] if container.get('container_id') == container_id),
        None
    )

    if not container_entry:
        # Allow lookup by namespace/pod/container name fallback
        fallback = next(
            (
                container for container in listing['containers']
                if f"{container.get('namespace')}/{container.get('pod_name')}/{container.get('container_name')}" == container_id
            ),
            None
        )
        if fallback:
            container_entry = fallback
            container_id = fallback.get('container_id')

    if not container_entry:
        raise ValueError(f"Container {container_id} not found")

    metrics = await get_container_metrics(container_entry.get('container_id')) if include_metrics else None

    return {
        'container': container_entry,
        'metrics': metrics if metrics else None
    }


async def get_container_metrics(container_id: str) -> Dict[str, Any]:
    """Return metrics for a specific container."""
    containers_map, lookup_index = _collect_container_base_info()
    record = containers_map.get(container_id)

    if not record:
        # Attempt to resolve using fallback key
        for entry in containers_map.values():
            if entry['container_id'] == container_id:
                record = entry
                break
        if not record:
            parts = container_id.split("/")
            if len(parts) == 3:
                lookup_key = (parts[0], parts[1], parts[2])
                actual_id = lookup_index.get(lookup_key)
                if actual_id and actual_id in containers_map:
                    record = containers_map[actual_id]
                    container_id = actual_id

    if not record:
        raise ValueError(f"Container {container_id} not found")

    namespace, pod_name, container_name = record['_lookup_key']
    filter_selector = f'{{namespace="{namespace}",pod="{pod_name}",container="{container_name}"}}'

    cpu_usage_result = prometheus_client.query(
        f'sum(rate(container_cpu_usage_seconds_total{filter_selector}[5m]))'
    ).get('data', {}).get('result', [])
    cpu_usage_value = _safe_float(cpu_usage_result[0].get('value', [0, '0'])[1]) if cpu_usage_result else None

    cpu_util_result = prometheus_client.query(
        f'sum(rate(container_cpu_usage_seconds_total{filter_selector}[1m]))'
    ).get('data', {}).get('result', [])
    cpu_util_value = _safe_float(cpu_util_result[0].get('value', [0, '0'])[1]) if cpu_util_result else None

    memory_used_result = prometheus_client.query(
        f'container_memory_usage_bytes{filter_selector}'
    ).get('data', {}).get('result', [])
    memory_working_set_result = prometheus_client.query(
        f'container_memory_working_set_bytes{filter_selector}'
    ).get('data', {}).get('result', [])
    memory_rss_result = prometheus_client.query(
        f'container_memory_rss{filter_selector}'
    ).get('data', {}).get('result', [])
    memory_cache_result = prometheus_client.query(
        f'container_memory_cache{filter_selector}'
    ).get('data', {}).get('result', [])

    fs_reads_result = prometheus_client.query(
        f'sum(rate(container_fs_reads_bytes_total{filter_selector}[5m]))'
    ).get('data', {}).get('result', [])
    fs_writes_result = prometheus_client.query(
        f'sum(rate(container_fs_writes_bytes_total{filter_selector}[5m]))'
    ).get('data', {}).get('result', [])
    fs_used_result = prometheus_client.query(
        f'container_fs_usage_bytes{filter_selector}'
    ).get('data', {}).get('result', [])

    network_rx_result = prometheus_client.query(
        f'sum(rate(container_network_receive_bytes_total{{namespace="{namespace}",pod="{pod_name}"}}[5m]))'
    ).get('data', {}).get('result', [])
    network_tx_result = prometheus_client.query(
        f'sum(rate(container_network_transmit_bytes_total{{namespace="{namespace}",pod="{pod_name}"}}[5m]))'
    ).get('data', {}).get('result', [])

    power_query = f'sum(rate(kepler_container_package_joules_total{{container_id="{container_id}"}}[5m]))'
    power_result = prometheus_client.query(power_query).get('data', {}).get('result', [])
    cpu_power_query = f'sum(rate(kepler_container_core_joules_total{{container_id="{container_id}"}}[5m]))'
    cpu_power_result = prometheus_client.query(cpu_power_query).get('data', {}).get('result', [])
    dram_power_query = f'sum(rate(kepler_container_dram_joules_total{{container_id="{container_id}"}}[5m]))'
    dram_power_result = prometheus_client.query(dram_power_query).get('data', {}).get('result', [])

    cpu_usage_millicores = int(cpu_usage_value * 1000) if cpu_usage_value is not None else None
    cpu_util_percent = cpu_util_value * 100 if cpu_util_value is not None else None

    network_rx_mbps = (network_rx_result and _safe_float(network_rx_result[0].get('value', [0, '0'])[1])) or None
    network_tx_mbps = (network_tx_result and _safe_float(network_tx_result[0].get('value', [0, '0'])[1])) or None
    if network_rx_mbps is not None:
        network_rx_mbps = network_rx_mbps * 8 / 1_000_000
    if network_tx_mbps is not None:
        network_tx_mbps = network_tx_mbps * 8 / 1_000_000

    metrics = InfraContainerMetrics(
        container_id=container_id,
        container_name=container_name,
        timestamp=datetime.utcnow(),
        cpu_usage_millicores=cpu_usage_millicores,
        cpu_utilization_percent=cpu_util_percent,
        memory_used_mb=_bytes_to_mb(_safe_float(memory_used_result[0].get('value', [0, '0'])[1])) if memory_used_result else None,
        memory_working_set_mb=_bytes_to_mb(_safe_float(memory_working_set_result[0].get('value', [0, '0'])[1])) if memory_working_set_result else None,
        memory_utilization_percent=None,
        memory_rss_mb=_bytes_to_mb(_safe_float(memory_rss_result[0].get('value', [0, '0'])[1])) if memory_rss_result else None,
        memory_cache_mb=_bytes_to_mb(_safe_float(memory_cache_result[0].get('value', [0, '0'])[1])) if memory_cache_result else None,
        fs_reads_mb=_bytes_to_mb(_safe_float(fs_reads_result[0].get('value', [0, '0'])[1])) if fs_reads_result else None,
        fs_writes_mb=_bytes_to_mb(_safe_float(fs_writes_result[0].get('value', [0, '0'])[1])) if fs_writes_result else None,
        fs_used_mb=_bytes_to_mb(_safe_float(fs_used_result[0].get('value', [0, '0'])[1])) if fs_used_result else None,
        network_rx_mbps=network_rx_mbps,
        network_tx_mbps=network_tx_mbps,
        power_watts=_safe_float(power_result[0].get('value', [0, '0'])[1]) if power_result else None,
        cpu_power_watts=_safe_float(cpu_power_result[0].get('value', [0, '0'])[1]) if cpu_power_result else None,
        dram_power_watts=_safe_float(dram_power_result[0].get('value', [0, '0'])[1]) if dram_power_result else None
    )

    return metrics.dict()
# Enhanced Cluster Total Power Monitoring

async def get_cluster_total_power(params: ClusterTotalQueryParams) -> ClusterTotalPowerResponse:
    """Fetches total cluster power consumption with optional breakdown."""

    cluster_name = params.cluster or "default"
    measurement_time = datetime.utcnow()

    # Get total cluster power
    total_power_query = "sum(rate(kepler_node_platform_joules_total[5m]))"
    total_result = prometheus_client.query(total_power_query).get('data', {}).get('result', [])
    total_power = float(total_result[0].get('value', [0, '0'])[1]) if total_result else 0

    # Get node and pod counts
    node_query = "count(kepler_node_info)"
    node_result = prometheus_client.query(node_query).get('data', {}).get('result', [])
    node_count = int(float(node_result[0].get('value', [0, '0'])[1])) if node_result else 0

    pod_query = "count(sum(rate(kepler_container_package_joules_total[5m])) by (pod_name, container_namespace))"
    pod_result = prometheus_client.query(pod_query).get('data', {}).get('result', [])
    pod_count = int(float(pod_result[0].get('value', [0, '0'])[1])) if pod_result else 0

    # Initialize response
    response = ClusterTotalPowerResponse(
        cluster_name=cluster_name,
        total_power_watts=total_power,
        measurement_time=measurement_time,
        node_count=node_count,
        pod_count=pod_count
    )

    # Generate breakdown if requested
    if params.breakdown_by:
        breakdown = await _generate_power_breakdown(params.breakdown_by, total_power)
        response.breakdown = breakdown

    # Generate efficiency metrics if requested
    if params.include_efficiency:
        efficiency = await _generate_efficiency_metrics(total_power, node_count, pod_count)
        response.efficiency = efficiency

    return response

async def get_cluster_power_timeseries(params: ClusterTotalQueryParams) -> ClusterPowerTimeSeriesResponse:
    """Fetches cluster power consumption over time with optional breakdown."""

    cluster_name = params.cluster or "default"

    # Determine time range (similar to get_timeseries_data)
    if params.start and params.end:
        start_time = params.start
        end_time = params.end
        period_str = None
    else:
        # Use period or default to 1 hour
        end_time = datetime.utcnow()
        period_map = {"1h": timedelta(hours=1), "1d": timedelta(days=1), "1w": timedelta(weeks=1), "1m": timedelta(days=30)}
        period_value = params.period.value if params.period else "1h"
        start_time = end_time - period_map.get(period_value, timedelta(hours=1))
        period_str = period_value

    # Get total cluster power over time
    total_power_query = "sum(rate(kepler_node_platform_joules_total[5m]))"
    
    # Convert step to seconds for Prometheus
    step_seconds = _parse_step_to_seconds(params.step)
    result = prometheus_client.query_range(total_power_query, start_time, end_time, step_seconds)

    total_power_points = []
    if result.get('data', {}).get('result', []):
        for res in result['data']['result'][0].get('values', []):
            total_power_points.append(TimeSeriesPoint(timestamp=datetime.fromtimestamp(res[0]), value=float(res[1])))

    response = ClusterPowerTimeSeriesResponse(
        cluster_name=cluster_name,
        period=period_str,
        step=params.step,
        start_time=start_time,
        end_time=end_time,
        total_samples=len(total_power_points),
        total_power_timeseries=total_power_points
    )

    # Generate breakdown timeseries if requested
    if params.breakdown_by:
        breakdown_timeseries = await _generate_breakdown_timeseries(params.breakdown_by, start_time, end_time, params.step)
        response.breakdown_timeseries = breakdown_timeseries

    return response

async def _generate_power_breakdown(breakdown_by: str, total_power: float) -> List[PowerBreakdown]:
    """Generate power breakdown by specified category."""
    breakdown = []

    if breakdown_by == "node":
        # Power by node
        query = "sum(rate(kepler_node_platform_joules_total[5m])) by (exported_instance)"
        result = prometheus_client.query(query).get('data', {}).get('result', [])

        for res in result:
            instance = res.get('metric', {}).get('exported_instance', 'unknown')
            power = float(res.get('value', [0, '0'])[1])
            percentage = (power / total_power * 100) if total_power > 0 else 0

            breakdown.append(PowerBreakdown(
                category=f"node-{instance}",
                power_watts=power,
                percentage=percentage
            ))

    elif breakdown_by == "namespace":
        # Power by namespace
        query = "sum(rate(kepler_container_package_joules_total[5m])) by (container_namespace)"
        result = prometheus_client.query(query).get('data', {}).get('result', [])

        namespace_power = 0
        for res in result:
            namespace = res.get('metric', {}).get('container_namespace', 'unknown')
            power = float(res.get('value', [0, '0'])[1])
            percentage = (power / total_power * 100) if total_power > 0 else 0

            breakdown.append(PowerBreakdown(
                category=f"namespace-{namespace}",
                power_watts=power,
                percentage=percentage
            ))
            namespace_power += power

        # Add node-level power (non-pod power)
        node_only_power = total_power - namespace_power
        if node_only_power > 0:
            percentage = (node_only_power / total_power * 100) if total_power > 0 else 0
            breakdown.append(PowerBreakdown(
                category="system-overhead",
                power_watts=node_only_power,
                percentage=percentage
            ))

    elif breakdown_by == "workload_type":
        # Simplified workload type classification
        system_namespaces = ['kube-system', 'kube-public', 'kube-node-lease', 'default']

        system_power = 0
        user_power = 0

        query = "sum(rate(kepler_container_package_joules_total[5m])) by (container_namespace)"
        result = prometheus_client.query(query).get('data', {}).get('result', [])

        for res in result:
            namespace = res.get('metric', {}).get('container_namespace', 'unknown')
            power = float(res.get('value', [0, '0'])[1])

            if namespace in system_namespaces:
                system_power += power
            else:
                user_power += power

        # Add system and user workload breakdown
        if system_power > 0:
            breakdown.append(PowerBreakdown(
                category="system-workloads",
                power_watts=system_power,
                percentage=(system_power / total_power * 100) if total_power > 0 else 0
            ))

        if user_power > 0:
            breakdown.append(PowerBreakdown(
                category="user-workloads",
                power_watts=user_power,
                percentage=(user_power / total_power * 100) if total_power > 0 else 0
            ))

        # Add infrastructure overhead
        overhead_power = total_power - system_power - user_power
        if overhead_power > 0:
            breakdown.append(PowerBreakdown(
                category="infrastructure-overhead",
                power_watts=overhead_power,
                percentage=(overhead_power / total_power * 100) if total_power > 0 else 0
            ))

    return breakdown

async def _generate_efficiency_metrics(total_power: float, node_count: int, pod_count: int) -> EfficiencyMetrics:
    """Generate cluster efficiency metrics."""

    # Get namespace count
    namespace_query = "count(sum(rate(kepler_container_package_joules_total[5m])) by (container_namespace))"
    namespace_result = prometheus_client.query(namespace_query).get('data', {}).get('result', [])
    namespace_count = int(float(namespace_result[0].get('value', [0, '0'])[1])) if namespace_result else 0

    return EfficiencyMetrics(
        power_per_pod=total_power / pod_count if pod_count > 0 else 0,
        power_per_namespace=total_power / namespace_count if namespace_count > 0 else 0,
        total_workloads=pod_count,
        active_namespaces=namespace_count
    )

async def _generate_breakdown_timeseries(breakdown_by: str, start_time: datetime, end_time: datetime, step: str) -> Dict[str, List[TimeSeriesPoint]]:
    """Generate breakdown timeseries data."""
    breakdown_timeseries = {}

    if breakdown_by == "node":
        query = "sum(rate(kepler_node_platform_joules_total[5m])) by (exported_instance)"
        result = prometheus_client.query_range(query, start_time, end_time, step)

        for res in result.get('data', {}).get('result', []):
            instance = res.get('metric', {}).get('exported_instance', 'unknown')
            points = []
            for values in res.get('values', []):
                points.append(TimeSeriesPoint(timestamp=datetime.fromtimestamp(values[0]), value=float(values[1])))
            breakdown_timeseries[f"node-{instance}"] = points

    elif breakdown_by == "namespace":
        query = "sum(rate(kepler_container_package_joules_total[5m])) by (container_namespace)"
        result = prometheus_client.query_range(query, start_time, end_time, step)

        for res in result.get('data', {}).get('result', []):
            namespace = res.get('metric', {}).get('container_namespace', 'unknown')
            points = []
            for values in res.get('values', []):
                points.append(TimeSeriesPoint(timestamp=datetime.fromtimestamp(values[0]), value=float(values[1])))
            breakdown_timeseries[f"namespace-{namespace}"] = points

    return breakdown_timeseries


# DCGM GPU Monitoring Functions

async def get_dcgm_gpu_info(node: Optional[str] = None) -> List[Dict[str, Any]]:
    """
    Fetch GPU information from DCGM metrics (static hardware info only).
    
    Supports both physical GPUs and VM-based GPUs with passthrough.
    For VM GPUs, includes hypervisor_host and vm_type labels.
    
    Power source logic:
    - Kubernetes Pod allocation: Use Kepler (pod power)
    - VM passthrough: Use DCGM (VM power)
    """
    # Build DCGM query (secure version)
    if node:
        try:
            safe_node = sanitize_label_value(node)
            label_matcher = build_label_matcher("Hostname", safe_node)
            query = f'DCGM_FI_DEV_GPU_UTIL{{{label_matcher}}}'
        except PromQLValidationError as e:
            logger.error(f"Invalid node value in get_dcgm_gpu_info: {e}")
            raise ValueError(f"Invalid node parameter: {e}")
    else:
        query = 'DCGM_FI_DEV_GPU_UTIL'

    result = prometheus_client.query(query).get('data', {}).get('result', [])
    
    # Query for memory total
    memory_query = 'DCGM_FI_DEV_FB_TOTAL'
    memory_result = prometheus_client.query(memory_query).get('data', {}).get('result', [])
    
    # Query for compute capability
    compute_query = 'DCGM_FI_DEV_CUDA_COMPUTE_CAPABILITY'
    compute_result = prometheus_client.query(compute_query).get('data', {}).get('result', [])
    
    # Build maps by device
    memory_map = {}
    for res in memory_result:
        labels = res.get('metric', {})
        device = labels.get('device', 'unknown')
        memory_mb = _safe_float(res.get('value', [0, '0'])[1])
        memory_map[device] = memory_mb
    
    compute_map = {}
    for res in compute_result:
        labels = res.get('metric', {})
        device = labels.get('device', 'unknown')
        compute_encoded = _safe_float(res.get('value', [0, '0'])[1])
        compute_capability = _decode_compute_capability(compute_encoded) if compute_encoded else None
        compute_map[device] = compute_capability

    # Remove duplicates by UUID (Prometheus may return same GPU multiple times)
    seen_uuids = set()
    gpus = []
    
    for res in result:
        labels = res.get('metric', {})
        
        # Check for duplicates by UUID
        uuid = labels.get('UUID', 'unknown')
        if uuid in seen_uuids:
            continue  # Skip duplicate
        seen_uuids.add(uuid)
        
        # Check if this is a VM-based GPU
        hypervisor_host = labels.get('hypervisor_host')
        vm_type = labels.get('vm_type')
        gpu_allocation = labels.get('gpu_allocation', 'unknown')
        is_vm_gpu = hypervisor_host is not None
        
        # Build hostname info
        hostname = labels.get('Hostname', 'unknown')
        if is_vm_gpu:
            # For VM GPUs, show both VM hostname and physical host
            hostname_display = f"{hostname} (VM on {hypervisor_host})"
        else:
            hostname_display = hostname
        
        # Get model name and infer architecture
        model_name = labels.get('modelName', 'unknown')
        architecture = _infer_gpu_architecture(model_name)
        
        # Get device and related info
        device = labels.get('device', 'unknown')
        memory_total_mb = memory_map.get(device)
        compute_capability_dcgm = compute_map.get(device)
        
        # Use DCGM compute capability if available, otherwise infer from model
        if compute_capability_dcgm:
            compute_capability = compute_capability_dcgm
        else:
            compute_capability = _infer_compute_capability(model_name)
        
        # Get driver version from LABEL (not from separate metric)
        driver_version = labels.get('driver_version')
        
        # Determine power source based on allocation
        # - kubernetes: GPU allocated to Kubernetes Pod (use Kepler)
        # - passthrough: GPU passed through to VM (use DCGM)
        if gpu_allocation == 'kubernetes':
            power_source = 'kepler'  # Kubernetes Pod allocation
        elif gpu_allocation == 'passthrough' or is_vm_gpu:
            power_source = 'dcgm'  # VM passthrough
        else:
            power_source = 'dcgm'  # Default to DCGM
        
        gpu_info = {
            # Common fields
            'gpu_id': device,
            'uuid': uuid,
            'model_name': model_name,
            'driver_version': driver_version,  # From label
            'hostname': hostname,
            'hostname_display': hostname_display,
            'pci_bus_id': labels.get('pci_bus_id', 'unknown'),
            'device_index': int(labels.get('gpu', '0')),
            'instance': labels.get('instance'),  # Prometheus instance (IP:port)
            
            # VM-specific fields
            'is_vm_gpu': is_vm_gpu,
            'hypervisor_host': hypervisor_host,
            'vm_type': vm_type,
            'physical_node': hypervisor_host if is_vm_gpu else hostname,
            'gpu_allocation': gpu_allocation,  # kubernetes | passthrough
            
            # Capacity fields
            'memory_total_mb': int(memory_total_mb) if memory_total_mb else None,
            'compute_capability': compute_capability,
            'architecture': architecture,
            
            # Power source (for monitoring API)
            'power_source': power_source,  # kepler | dcgm
        }
        gpus.append(gpu_info)

    return gpus


def _infer_gpu_architecture(model_name: str) -> Optional[str]:
    """Infer GPU architecture from model name."""
    model_upper = model_name.upper()
    
    # NVIDIA architectures
    if 'H100' in model_upper or 'H200' in model_upper:
        return 'Hopper'
    elif 'A100' in model_upper or 'A30' in model_upper or 'A40' in model_upper or 'A10' in model_upper or 'A16' in model_upper or 'A2' in model_upper:
        return 'Ampere'
    elif 'V100' in model_upper:
        return 'Volta'
    elif 'P100' in model_upper or 'P40' in model_upper or 'P4' in model_upper:
        return 'Pascal'
    elif 'T4' in model_upper:
        return 'Turing'
    elif 'L4' in model_upper or 'L40' in model_upper:
        return 'Ada Lovelace'
    
    return None


def _infer_compute_capability(model_name: str) -> Optional[str]:
    """Infer CUDA compute capability from model name."""
    model_upper = model_name.upper()
    
    # NVIDIA compute capabilities
    if 'H100' in model_upper or 'H200' in model_upper:
        return '9.0'
    elif 'A100' in model_upper or 'A30' in model_upper:
        return '8.0'
    elif 'A40' in model_upper or 'A10' in model_upper or 'A16' in model_upper:
        return '8.6'
    elif 'V100' in model_upper:
        return '7.0'
    elif 'P100' in model_upper:
        return '6.0'
    elif 'T4' in model_upper:
        return '7.5'
    elif 'L4' in model_upper or 'L40' in model_upper:
        return '8.9'
    
    return None


def _decode_compute_capability(encoded_value: float) -> Optional[str]:
    """
    Decode DCGM compute capability value.
    
    DCGM encodes compute capability as: major * 65536 + minor
    Example: 8.0 = 8 * 65536 + 0 = 524288
    """
    try:
        encoded_int = int(encoded_value)
        major = encoded_int // 65536
        minor = encoded_int % 65536
        return f"{major}.{minor}"
    except (ValueError, TypeError):
        return None

async def get_dcgm_gpu_metrics(node: Optional[str] = None, gpu_id: Optional[str] = None) -> List[Dict[str, Any]]:
    """Fetch comprehensive GPU metrics from DCGM.
    
    Args:
        node: Filter by hostname
        gpu_id: Filter by device ID (e.g., 'nvidia0') or UUID (e.g., 'GPU-xxx')
    """

    # Build filter for queries
    filters = []
    if node:
        filters.append(f'Hostname="{node}"')
    
    # Check if gpu_id is a UUID (starts with 'GPU-') or device ID
    if gpu_id:
        if gpu_id.startswith('GPU-'):
            filters.append(f'UUID="{gpu_id}"')
        else:
            filters.append(f'device="{gpu_id}"')

    filter_str = "{" + ",".join(filters) + "}" if filters else ""

    # Define DCGM metric queries (only include metrics that are actually available)
    metrics_queries = {
        'gpu_utilization': f'DCGM_FI_DEV_GPU_UTIL{filter_str}',
        'memory_copy_utilization': f'DCGM_FI_DEV_MEM_COPY_UTIL{filter_str}',
        'gpu_temperature': f'DCGM_FI_DEV_GPU_TEMP{filter_str}',
        'power_usage': f'DCGM_FI_DEV_POWER_USAGE{filter_str}',
        'total_energy': f'DCGM_FI_DEV_TOTAL_ENERGY_CONSUMPTION{filter_str}',
        'memory_used': f'DCGM_FI_DEV_FB_USED{filter_str}',
        'sm_clock': f'DCGM_FI_DEV_SM_CLOCK{filter_str}',
        'memory_clock': f'DCGM_FI_DEV_MEM_CLOCK{filter_str}',
        # Removed unavailable metrics:
        # - DCGM_FI_DEV_DEC_UTIL (decoder_utilization)
        # - DCGM_FI_DEV_ENC_UTIL (encoder_utilization)
        # - DCGM_FI_DEV_MEMORY_TEMP (memory_temperature)
        # - DCGM_FI_DEV_FB_FREE (memory_free)
        # - DCGM_FI_DEV_FB_RESERVED (memory_reserved)
        # - DCGM_FI_DEV_XID_ERRORS (xid_errors)
        # - DCGM_FI_DEV_PCIE_REPLAY_COUNTER (pcie_replay_counter)
        # - DCGM_FI_DEV_CORRECTABLE_REMAPPED_ROWS (correctable_remapped_rows)
        # - DCGM_FI_DEV_UNCORRECTABLE_REMAPPED_ROWS (uncorrectable_remapped_rows)
    }

    # Fetch all metrics
    metrics_data = {}
    for metric_name, query in metrics_queries.items():
        try:
            result = prometheus_client.query(query).get('data', {}).get('result', [])
            metrics_data[metric_name] = result
        except Exception as e:
            print(f"Error fetching {metric_name}: {e}")
            metrics_data[metric_name] = []

    # Organize by GPU
    gpu_metrics = {}

    # Process each metric type
    for metric_name, results in metrics_data.items():
        for res in results:
            labels = res.get('metric', {})
            gpu_device = labels.get('device', 'unknown')
            hostname = labels.get('Hostname', 'unknown')
            gpu_key = f"{hostname}-{gpu_device}"

            if gpu_key not in gpu_metrics:
                gpu_metrics[gpu_key] = {
                    'gpu_id': gpu_device,
                    'hostname': hostname,
                    'timestamp': datetime.utcnow(),
                    'uuid': labels.get('UUID', 'unknown'),
                }

            value = float(res.get('value', [0, '0'])[1])

            # Map metric names to response fields
            field_mapping = {
                'gpu_utilization': 'gpu_utilization_percent',
                'decoder_utilization': 'decoder_utilization_percent',
                'encoder_utilization': 'encoder_utilization_percent',
                'memory_copy_utilization': 'memory_copy_utilization_percent',
                'gpu_temperature': 'gpu_temperature_celsius',
                'memory_temperature': 'memory_temperature_celsius',
                'power_usage': 'power_usage_watts',
                'total_energy': 'total_energy_joules',
                'memory_used': 'memory_used_mb',
                'memory_free': 'memory_free_mb',
                'memory_reserved': 'memory_reserved_mb',
                'sm_clock': 'sm_clock_mhz',
                'memory_clock': 'memory_clock_mhz',
                'xid_errors': 'xid_errors',
                'pcie_replay_counter': 'pcie_replay_counter',
                'correctable_remapped_rows': 'correctable_remapped_rows',
                'uncorrectable_remapped_rows': 'uncorrectable_remapped_rows'
            }

            field_name = field_mapping.get(metric_name)
            if field_name:
                gpu_metrics[gpu_key][field_name] = value

    return list(gpu_metrics.values())


async def get_kepler_gpu_info(node: Optional[str] = None) -> List[Dict[str, Any]]:
    """
    Fetch GPU information from Kepler and node_exporter metrics.
    
    Returns data in the same format as DCGM for frontend consistency.
    
    Since DCGM is not available, we combine:
    1. Kepler node info for CPU architecture and power source
    2. Node exporter hwmon for temperature and power sensors
    3. Kepler platform power for energy consumption
    
    Note: This provides node-level aggregated data, not per-GPU details.
    """
    # Get node information from Kepler (secure version)
    node_info_query = 'kepler_node_info'
    if node:
        try:
            safe_node = sanitize_label_value(node)
            # Use regex matcher for partial matching
            node_info_query = f'kepler_node_info{{instance=~".*{safe_node}.*"}}'
        except PromQLValidationError as e:
            logger.error(f"Invalid node value in get_kepler_gpu_info: {e}")
            raise ValueError(f"Invalid node parameter: {e}")

    node_info_result = prometheus_client.query(node_info_query).get('data', {}).get('result', [])

    # Get power data from Kepler (secure version)
    if node:
        try:
            safe_node = sanitize_label_value(node)
            label_matcher = build_label_matcher("exported_instance", safe_node)
            power_query = f'rate(kepler_node_platform_joules_total{{{label_matcher}}}[5m])'
        except PromQLValidationError as e:
            logger.error(f"Invalid node value in power query: {e}")
            raise ValueError(f"Invalid node parameter: {e}")
    else:
        power_query = 'rate(kepler_node_platform_joules_total[5m])'

    power_result = prometheus_client.query(power_query).get('data', {}).get('result', [])

    # Get temperature from node_exporter hwmon (secure version)
    temp_query = 'node_hwmon_temp_celsius{sensor=~"temp.*"}'
    if node:
        try:
            safe_node = sanitize_label_value(node)
            # Use regex matcher for partial matching with sensor filter
            temp_query = f'node_hwmon_temp_celsius{{instance=~".*{safe_node}.*",sensor=~"temp.*"}}'
        except PromQLValidationError as e:
            logger.error(f"Invalid node value in temperature query: {e}")
            raise ValueError(f"Invalid node parameter: {e}")
    
    temp_result = prometheus_client.query(temp_query).get('data', {}).get('result', [])
    
    # Build node info map using pod name as key (to match with power data)
    node_map_by_pod = {}
    node_map_by_instance = {}
    for res in node_info_result:
        labels = res.get('metric', {})
        pod = labels.get('pod', 'unknown')
        instance = labels.get('instance', 'unknown')
        
        info = {
            'cpu_architecture': labels.get('cpu_architecture', 'Unknown'),
            'platform_power_source': labels.get('platform_power_source', 'unknown'),
            'components_power_source': labels.get('components_power_source', 'unknown'),
        }
        
        node_map_by_pod[pod] = info
        node_map_by_instance[instance] = info
    
    # Build temperature map (average per node)
    temp_map = {}
    for res in temp_result:
        labels = res.get('metric', {})
        instance = labels.get('instance', 'unknown')
        hostname = instance.split(':')[0] if ':' in instance else instance
        temp_value = _safe_float(res.get('value', [0, '0'])[1])
        
        if hostname not in temp_map:
            temp_map[hostname] = []
        if temp_value:
            temp_map[hostname].append(temp_value)
    
    # Calculate average temperature per node
    avg_temp_map = {}
    for hostname, temps in temp_map.items():
        if temps:
            avg_temp_map[hostname] = sum(temps) / len(temps)
    
    # Build GPU info from power data
    gpus = []
    seen_instances = set()
    
    for res in power_result:
        labels = res.get('metric', {})
        exported_instance = labels.get('exported_instance', 'unknown')
        package = labels.get('package', 'energy1')
        source = labels.get('source', 'unknown')
        pod = labels.get('pod', 'unknown')
        instance = labels.get('instance', 'unknown')
        
        gpu_key = f"{exported_instance}-{package}"
        
        if gpu_key not in seen_instances:
            seen_instances.add(gpu_key)
            
            # Get node info - try pod first, then instance
            node_info = node_map_by_pod.get(pod, node_map_by_instance.get(instance, {}))
            cpu_arch = node_info.get('cpu_architecture', 'Unknown')
            power_source = node_info.get('platform_power_source', source)
            
            # Get average temperature
            avg_temp = avg_temp_map.get(exported_instance)
            
            # Create GPU info with SAME FIELDS as DCGM for consistency
            gpu_info = {
                # Common fields (same as DCGM)
                'gpu_id': f"{exported_instance}-{package}",
                'uuid': None,  # Not available without DCGM
                'model_name': f"Accelerator ({cpu_arch})" if cpu_arch != 'Unknown' else "Accelerator (Kepler)",
                'driver_version': None,  # Not available without DCGM
                'hostname': exported_instance,
                'hostname_display': exported_instance,  # Same as hostname for physical nodes
                'pci_bus_id': None,  # Not available without DCGM
                'device_index': None,  # Not available without DCGM
                'instance': None,  # Not available in Kepler
                
                # VM-specific fields (null for physical nodes)
                'is_vm_gpu': False,  # Physical node, not VM
                'hypervisor_host': None,  # Not a VM
                'vm_type': None,  # Not a VM
                'physical_node': exported_instance,  # Physical node is itself
                'gpu_allocation': 'kubernetes',  # Kepler monitors Kubernetes workloads
                
                # Capacity fields
                'memory_total_mb': None,  # Not available in Kepler
                'compute_capability': None,  # Not available in Kepler
                'architecture': cpu_arch,
                
                # Power monitoring source
                'power_source': 'kepler',  # Always Kepler for node-level monitoring
            }
            gpus.append(gpu_info)
    
    return gpus


async def get_kepler_gpu_metrics(node: Optional[str] = None) -> List[Dict[str, Any]]:
    """
    Fetch GPU metrics from Kepler and node_exporter.
    
    Combines:
    1. Kepler platform power (watts)
    2. Node exporter hwmon temperature
    3. Node exporter hwmon power sensors
    """
    # Build filter for queries (secure version)
    filter_str = ''
    if node:
        try:
            safe_node = sanitize_label_value(node)
            label_matcher = build_label_matcher("exported_instance", safe_node)
            filter_str = f'{{{label_matcher}}}'
        except PromQLValidationError as e:
            logger.error(f"Invalid node value in get_node_metrics: {e}")
            raise ValueError(f"Invalid node parameter: {e}")

    # Query Kepler power metrics
    power_query = f'rate(kepler_node_platform_joules_total{filter_str}[5m])'
    power_result = prometheus_client.query(power_query).get('data', {}).get('result', [])

    # Query node_exporter temperature (secure version)
    temp_query = 'node_hwmon_temp_celsius{sensor=~"temp.*"}'
    if node:
        try:
            safe_node = sanitize_label_value(node)
            temp_query = f'node_hwmon_temp_celsius{{instance=~".*{safe_node}.*",sensor=~"temp.*"}}'
        except PromQLValidationError as e:
            logger.error(f"Invalid node value in temperature query: {e}")
            raise ValueError(f"Invalid node parameter: {e}")
    temp_result = prometheus_client.query(temp_query).get('data', {}).get('result', [])

    # Query node_exporter power sensors (secure version)
    hwmon_power_query = 'node_hwmon_power_average_watt'
    if node:
        try:
            safe_node = sanitize_label_value(node)
            hwmon_power_query = f'node_hwmon_power_average_watt{{instance=~".*{safe_node}.*"}}'
        except PromQLValidationError as e:
            logger.error(f"Invalid node value in hwmon power query: {e}")
            raise ValueError(f"Invalid node parameter: {e}")
    hwmon_power_result = prometheus_client.query(hwmon_power_query).get('data', {}).get('result', [])

    gpu_metrics = {}

    # Process Kepler power data
    for res in power_result:
        labels = res.get('metric', {})
        instance = labels.get('exported_instance', 'unknown')
        package = labels.get('package', 'energy1')
        gpu_key = f"{instance}-{package}"

        if gpu_key not in gpu_metrics:
            gpu_metrics[gpu_key] = {
                'gpu_id': f"{instance}-{package}",
                'hostname': instance,
                'timestamp': datetime.utcnow(),
                'uuid': None,
            }

        # Power in watts (Kepler provides rate of joules)
        power_value = _safe_float(res.get('value', [0, '0'])[1])
        gpu_metrics[gpu_key]['power_usage_watts'] = power_value or 0

    # Add temperature data (average per node)
    temp_by_node = {}
    for res in temp_result:
        labels = res.get('metric', {})
        instance = labels.get('instance', 'unknown')
        hostname = instance.split(':')[0] if ':' in instance else instance
        temp_value = _safe_float(res.get('value', [0, '0'])[1])
        
        if hostname not in temp_by_node:
            temp_by_node[hostname] = []
        if temp_value:
            temp_by_node[hostname].append(temp_value)
    
    # Calculate average temperature and add to metrics
    for gpu_key, metric in gpu_metrics.items():
        hostname = metric['hostname']
        if hostname in temp_by_node and temp_by_node[hostname]:
            avg_temp = sum(temp_by_node[hostname]) / len(temp_by_node[hostname])
            metric['gpu_temperature_celsius'] = round(avg_temp, 2)
        else:
            metric['gpu_temperature_celsius'] = None

    # Add hwmon power data if available
    hwmon_power_by_node = {}
    for res in hwmon_power_result:
        labels = res.get('metric', {})
        instance = labels.get('instance', 'unknown')
        hostname = instance.split(':')[0] if ':' in instance else instance
        power_value = _safe_float(res.get('value', [0, '0'])[1])
        
        if hostname not in hwmon_power_by_node:
            hwmon_power_by_node[hostname] = 0
        if power_value:
            hwmon_power_by_node[hostname] += power_value
    
    # Add hwmon power as additional info
    for gpu_key, metric in gpu_metrics.items():
        hostname = metric['hostname']
        if hostname in hwmon_power_by_node:
            metric['hwmon_power_watts'] = hwmon_power_by_node[hostname]
        
        # Set unavailable metrics to None (not 0, to indicate no data)
        metric['gpu_utilization_percent'] = None
        metric['memory_used_mb'] = None
        metric['memory_free_mb'] = None

    return list(gpu_metrics.values())


def _safe_float(value: Any) -> Optional[float]:
    """Safely convert value to float, return None if conversion fails."""
    try:
        return float(value) if value is not None else None
    except (ValueError, TypeError):
        return None

def _safe_int(value: Any) -> Optional[int]:
    """Safely convert value to int, return None if conversion fails."""
    try:
        return int(float(value)) if value is not None else None
    except (ValueError, TypeError):
        return None

async def get_dcgm_gpu_temperatures(node: Optional[str] = None, gpu_id: Optional[str] = None) -> List[Dict[str, Any]]:
    """Fetch GPU temperature data from DCGM metrics.
    
    Args:
        node: Filter by hostname
        gpu_id: Filter by device ID (e.g., 'nvidia0') or UUID (e.g., 'GPU-xxx')
    """

    # Build filter for queries
    filters = []
    if node:
        filters.append(f'Hostname="{node}"')
    
    # Check if gpu_id is a UUID (starts with 'GPU-') or device ID
    if gpu_id:
        if gpu_id.startswith('GPU-'):
            filters.append(f'UUID="{gpu_id}"')
        else:
            filters.append(f'device="{gpu_id}"')

    filter_str = "{" + ",".join(filters) + "}" if filters else ""

    # Define temperature metric queries
    temp_queries = {
        'gpu_temperature': f'DCGM_FI_DEV_GPU_TEMP{filter_str}',
        'memory_temperature': f'DCGM_FI_DEV_MEMORY_TEMP{filter_str}'
    }

    # Fetch temperature metrics
    temp_data = {}
    for metric_name, query in temp_queries.items():
        try:
            result = prometheus_client.query(query).get('data', {}).get('result', [])
            temp_data[metric_name] = result
        except Exception as e:
            print(f"Error fetching {metric_name}: {e}")
            temp_data[metric_name] = []

    # Organize by GPU
    gpu_temperatures = {}

    # Process each temperature metric
    for metric_name, results in temp_data.items():
        for res in results:
            labels = res.get('metric', {})
            gpu_device = labels.get('device', 'unknown')
            hostname = labels.get('Hostname', 'unknown')
            gpu_key = f"{hostname}-{gpu_device}"

            if gpu_key not in gpu_temperatures:
                gpu_temperatures[gpu_key] = {
                    'gpu_id': gpu_device,
                    'hostname': hostname,
                    'timestamp': datetime.utcnow(),
                }

            value = _safe_float(res.get('value', [0, '0'])[1])

            if metric_name == 'gpu_temperature':
                gpu_temperatures[gpu_key]['gpu_temperature_celsius'] = value
            elif metric_name == 'memory_temperature':
                gpu_temperatures[gpu_key]['memory_temperature_celsius'] = value

    # Add temperature status and limits
    for gpu_key, temp_data in gpu_temperatures.items():
        gpu_temp = temp_data.get('gpu_temperature_celsius')
        mem_temp = temp_data.get('memory_temperature_celsius')

        # Determine temperature status (typical GPU limits)
        temp_status = "normal"
        max_temp = 0

        if gpu_temp is not None:
            max_temp = max(max_temp, gpu_temp)
            if gpu_temp >= 85:  # Critical temperature for most GPUs
                temp_status = "critical"
            elif gpu_temp >= 75:  # Warning temperature
                temp_status = "warning"

        if mem_temp is not None:
            max_temp = max(max_temp, mem_temp)
            if mem_temp >= 95:  # Memory critical temperature
                temp_status = "critical"
            elif mem_temp >= 85:  # Memory warning temperature
                temp_status = "warning"

        temp_data['temperature_status'] = temp_status
        temp_data['temperature_limit_celsius'] = 90.0  # A30 temperature limit

    return list(gpu_temperatures.values())


async def get_gpu_power_stats(gpu_id: str, period: str = "5m") -> Dict[str, Optional[float]]:
    """
    Get GPU power statistics over a time period.
    
    Args:
        gpu_id: GPU identifier (device ID or UUID)
        period: Time period (e.g., '5m', '1h', '24h')
    
    Returns:
        Dictionary with avg_power, max_power, min_power
    """
    # Build filter for UUID or device ID
    if gpu_id.startswith('GPU-'):
        filter_str = f'{{UUID="{gpu_id}"}}'
    else:
        filter_str = f'{{device="{gpu_id}"}}'
    
    # Query Prometheus for power statistics over the period
    queries = {
        'avg': f'avg_over_time(DCGM_FI_DEV_POWER_USAGE{filter_str}[{period}])',
        'max': f'max_over_time(DCGM_FI_DEV_POWER_USAGE{filter_str}[{period}])',
        'min': f'min_over_time(DCGM_FI_DEV_POWER_USAGE{filter_str}[{period}])'
    }
    
    stats = {}
    for stat_name, query in queries.items():
        try:
            result = prometheus_client.query(query).get('data', {}).get('result', [])
            if result:
                value = _safe_float(result[0].get('value', [0, '0'])[1])
                stats[f'{stat_name}_power'] = value
            else:
                stats[f'{stat_name}_power'] = None
        except Exception as e:
            print(f"Error fetching {stat_name} power: {e}")
            stats[f'{stat_name}_power'] = None
    
    return stats


async def get_enhanced_gpu_power_data(params: GPUQueryParams) -> GPUPowerResponse:
    """Enhanced GPU power data with DCGM integration."""

    # Get existing Kepler-based data
    kepler_data = await get_gpu_power_data(params)

    try:
        # Get DCGM data for the same instance/node
        dcgm_metrics = await get_dcgm_gpu_metrics(params.instance)
        dcgm_info = await get_dcgm_gpu_info(params.instance)

        # Create lookup dictionaries for DCGM data
        dcgm_metrics_map = {}
        dcgm_info_map = {}

        for metric in dcgm_metrics:
            hostname = metric.get('hostname', 'unknown')
            gpu_id = metric.get('gpu_id', 'unknown')
            key = f"{hostname}-{gpu_id}"
            dcgm_metrics_map[key] = metric

        for info in dcgm_info:
            hostname = info.get('hostname', 'unknown')
            gpu_id = info.get('gpu_id', 'unknown')
            key = f"{hostname}-{gpu_id}"
            dcgm_info_map[key] = info

        # Enhance existing GPU data with DCGM information
        enhanced_gpus = []

        for gpu in kepler_data.gpus:
            # Try to match DCGM data based on instance and GPU ID
            instance = gpu.instance
            gpu_device_id = gpu.gpu_id.lower()  # nvidia0, nvidia1, etc.

            # Look for matching DCGM data
            dcgm_key = f"{instance}-{gpu_device_id}"
            dcgm_metric = dcgm_metrics_map.get(dcgm_key)
            dcgm_gpu_info = dcgm_info_map.get(dcgm_key)

            # Determine data source
            data_source = "kepler"
            if dcgm_metric and dcgm_gpu_info:
                data_source = "hybrid"
            elif dcgm_metric or dcgm_gpu_info:
                data_source = "partial-dcgm"

            # Create enhanced GPU info
            enhanced_gpu = GPUInfo(
                gpu_id=gpu.gpu_id,
                instance=gpu.instance,
                power_draw_watts=gpu.power_draw_watts,
                utilization_percent=gpu.utilization_percent,
                temperature_celsius=gpu.temperature_celsius,
                memory_used_mb=gpu.memory_used_mb,
                memory_total_mb=gpu.memory_total_mb,

                # DCGM enhancements
                dcgm_uuid=dcgm_gpu_info.get('uuid') if dcgm_gpu_info else None,
                dcgm_model_name=dcgm_gpu_info.get('model_name') if dcgm_gpu_info else None,
                dcgm_driver_version=dcgm_gpu_info.get('driver_version') if dcgm_gpu_info else None,
                dcgm_power_watts=_safe_float(dcgm_metric.get('power_usage_watts')) if dcgm_metric else None,
                dcgm_temperature_celsius=_safe_float(dcgm_metric.get('gpu_temperature_celsius')) if dcgm_metric else None,
                dcgm_memory_used_mb=_safe_int(dcgm_metric.get('memory_used_mb')) if dcgm_metric else None,
                dcgm_utilization_percent=_safe_float(dcgm_metric.get('gpu_utilization_percent')) if dcgm_metric else None,
                data_source=data_source
            )
            enhanced_gpus.append(enhanced_gpu)

        # Calculate enhanced summary with DCGM data
        dcgm_power_total = sum(_safe_float(m.get('power_usage_watts')) or 0 for m in dcgm_metrics)

        enhanced_summary = GPUSummary(
            total_power_watts=kepler_data.summary.total_power_watts + dcgm_power_total,
            avg_power_watts=kepler_data.summary.avg_power_watts,
            max_power_watts=max(kepler_data.summary.max_power_watts, dcgm_power_total),
            avg_utilization_percent=kepler_data.summary.avg_utilization_percent
        )

        # Create enhanced workload power data
        enhanced_workload = None
        if kepler_data.workload_power:
            enhanced_workload = WorkloadPower(
                cluster_power_watts=kepler_data.workload_power.cluster_power_watts,
                pod_power_watts=kepler_data.workload_power.pod_power_watts,
                namespace=kepler_data.workload_power.namespace,
                workload=kepler_data.workload_power.workload,
                sample_count=kepler_data.workload_power.sample_count
            )

        # Return enhanced response
        return GPUPowerResponse(
            timestamp=datetime.utcnow(),
            period=kepler_data.period,
            total_gpus=len(enhanced_gpus),
            gpus=enhanced_gpus,
            summary=enhanced_summary,
            workload_power=enhanced_workload
        )

    except Exception as e:
        # If DCGM integration fails, return original Kepler data
        print(f"DCGM integration failed, returning Kepler-only data: {e}")
        return kepler_data


# ============================================================================
# NPU Monitoring Functions (Placeholder - Phase 3.2)
# ============================================================================

async def get_npu_info(node: Optional[str] = None, vendor: Optional[str] = None) -> List[Dict[str, Any]]:
    """
    Fetch NPU information from NPU-specific Prometheus exporters.

    NOTE: This is a placeholder implementation. Actual implementation will require:
    - Furiosa AI NPU Exporter setup and metrics
    - Rebellions NPU Exporter setup and metrics
    - Proper metric name mapping for each vendor

    Args:
        node: Optional node hostname filter
        vendor: Optional vendor filter (furiosa/rebellions)

    Returns:
        List of NPU information dictionaries
    """
    # Placeholder: Return empty list since NPU exporters are not yet configured
    # Real implementation would query Prometheus metrics like:
    # - furiosa_npu_info{...}
    # - rebellions_npu_info{...}
    return []


async def get_npu_metrics(node: Optional[str] = None, npu_id: Optional[str] = None, vendor: Optional[str] = None) -> List[Dict[str, Any]]:
    """
    Fetch comprehensive NPU metrics from NPU-specific exporters.

    NOTE: This is a placeholder implementation. Real metrics would include:
    - NPU utilization percentage
    - Memory utilization and usage
    - Power consumption (watts)
    - Temperature (celsius)
    - Core status (for Furiosa multi-core NPUs)
    - Throughput (FPS) and latency (ms)
    - Error counts

    Args:
        node: Optional node hostname filter
        npu_id: Optional NPU device ID filter
        vendor: Optional vendor filter (furiosa/rebellions)

    Returns:
        List of NPU metrics dictionaries
    """
    # Placeholder: Return empty list
    # Real implementation would query metrics like:
    # Furiosa:
    #   - furiosa_npu_utilization_percent
    #   - furiosa_npu_power_watts
    #   - furiosa_npu_temperature_celsius
    #   - furiosa_npu_memory_used_bytes
    #   - furiosa_npu_core_status
    # Rebellions:
    #   - rebellions_npu_utilization_percent
    #   - rebellions_npu_power_watts
    #   - rebellions_npu_temperature_celsius
    return []


async def get_npu_core_status(node: Optional[str] = None, npu_id: Optional[str] = None) -> List[Dict[str, Any]]:
    """
    Fetch Furiosa NPU core-level status.

    NOTE: This is Furiosa-specific functionality for multi-core NPUs.
    Returns detailed information about individual NPU cores including:
    - Core state (idle/running/error)
    - Core utilization
    - Core temperature
    - Process information running on each core

    Args:
        node: Optional node hostname filter
        npu_id: Optional NPU device ID filter

    Returns:
        List of NPU core status dictionaries
    """
    # Placeholder: Return empty list
    # Real implementation would query Furiosa-specific metrics:
    #   - furiosa_npu_core_state{npu_id="...", core_id="..."}
    #   - furiosa_npu_core_utilization_percent{...}
    #   - furiosa_npu_core_temperature_celsius{...}
    return []


# ============================================================================
# Node Monitoring Functions (Phase 4.1)
# ============================================================================

_NODE_LABEL_CANDIDATES = [
    "node",
    "Hostname",
    "hostname",
    "kubernetes_io_hostname",
    "kubernetes_node",
    "kubernetes_node_name"
]
_INSTANCE_LABEL_CANDIDATES = ["exported_instance", "instance"]
_RESERVED_NODE_LABEL_KEYS = {
    "__name__",
    "instance",
    "exported_instance",
    "job",
    "node",
    "Hostname",
    "service",
    "pod"
}


def _first_present_label(labels: Dict[str, Any], keys: List[str]) -> Optional[str]:
    """Return the first non-empty label value for the provided keys."""
    for key in keys:
        value = labels.get(key)
        if value:
            return value
    return None


def _get_ip_to_node_mapping() -> Dict[str, str]:
    """
    Get IP to Kubernetes node name mapping from kube_node_info.
    
    Returns:
        Dictionary mapping IP addresses to node names
    """
    try:
        result = prometheus_client.query("kube_node_info").get('data', {}).get('result', [])
        mapping = {}
        
        for res in result:
            labels = res.get('metric', {})
            node_name = labels.get('node')
            internal_ip = labels.get('internal_ip')
            
            if node_name and internal_ip:
                mapping[internal_ip] = node_name
        
        return mapping
    except Exception as e:
        print(f"Warning: Could not get IP to node mapping: {e}")
        return {}


def _extract_node_name(labels: Dict[str, Any]) -> Optional[str]:
    """Derive a node name from Prometheus metric labels."""
    # First, try to get node name from standard labels
    node = _first_present_label(labels, _NODE_LABEL_CANDIDATES)
    if node:
        return node

    # If not found, try to map IP to node name
    instance = _first_present_label(labels, _INSTANCE_LABEL_CANDIDATES)
    if instance:
        ip = instance.split(":")[0]
        
        # Try to map IP to actual node name
        ip_mapping = _get_ip_to_node_mapping()
        if ip in ip_mapping:
            return ip_mapping[ip]
        
        # Fallback to IP if mapping not available
        return ip

    return None


def _sanitize_node_labels(labels: Dict[str, Any]) -> Optional[Dict[str, str]]:
    """Remove reserved/system labels and return a clean label dictionary."""
    sanitized: Dict[str, str] = {}
    for key, value in labels.items():
        if not value or key in _RESERVED_NODE_LABEL_KEYS:
            continue
        sanitized[key] = value
    return sanitized or None


def _bytes_to_mb(value: Optional[float]) -> Optional[int]:
    """Convert bytes to megabytes."""
    if value is None:
        return None
    return int(value / (1024 * 1024))


def _normalize_status_filter(status: Optional[str]) -> Optional[str]:
    """Normalize status filter strings to NodeStatus values."""
    if not status:
        return None
    normalized = status.lower().replace(" ", "_").replace("-", "_")
    mapping = {
        "ready": NodeStatus.READY.value,
        "notready": NodeStatus.NOT_READY.value,
        "not_ready": NodeStatus.NOT_READY.value,
        "unknown": NodeStatus.UNKNOWN.value,
        "maintenance": NodeStatus.MAINTENANCE.value
    }
    return mapping.get(normalized, normalized)


def _normalize_role_filter(role: Optional[str]) -> Optional[str]:
    """Normalize role filter strings to NodeRole values."""
    if not role:
        return None
    normalized = role.lower().replace(" ", "_").replace("-", "_")
    mapping = {
        "gpu": NodeRole.GPU_WORKER.value,
        "gpu_worker": NodeRole.GPU_WORKER.value,
        "npu": NodeRole.NPU_WORKER.value,
        "npu_worker": NodeRole.NPU_WORKER.value,
        "master": NodeRole.MASTER.value,
        "control_plane": NodeRole.MASTER.value,
        "controlplane": NodeRole.MASTER.value,
        "worker": NodeRole.WORKER.value
    }
    return mapping.get(normalized, normalized)


def _role_filter_matches(filter_value: Optional[str], node_role: NodeRole) -> bool:
    """Check if a node role matches the requested filter value."""
    if not filter_value:
        return True
    if filter_value == NodeRole.WORKER.value:
        return node_role in {NodeRole.WORKER, NodeRole.GPU_WORKER, NodeRole.NPU_WORKER}
    return node_role.value == filter_value


def _determine_node_role(labels: Dict[str, Any], gpu_count: int) -> NodeRole:
    """Infer the node role using Kubernetes label semantics and accelerator counts."""
    label_keys = {key.replace("-", "_") for key in labels.keys()}

    if gpu_count > 0:
        return NodeRole.GPU_WORKER

    if any(key in label_keys for key in ("node_role_kubernetes_io_master", "node_role_kubernetes_io_control_plane")):
        return NodeRole.MASTER

    if "node_role_kubernetes_io_worker" in label_keys or "node_role_kubernetes_io_node" in label_keys:
        return NodeRole.WORKER

    return NodeRole.WORKER


def _query_metric_map(
    queries: List[str],
    label_candidates: Optional[List[str]] = None,
    value_transform=_safe_float,
    stop_on_first_success: bool = True
) -> Dict[str, Any]:
    """
    Execute one or more PromQL queries and build a node -> value map.

    Args:
        queries: List of PromQL queries to execute.
        label_candidates: Optional label keys to prioritise when selecting the node identifier.
        value_transform: Callable applied to scalar values.
        stop_on_first_success: Stop after the first query that yields data when True.
    """
    data: Dict[str, Any] = {}

    for query in queries:
        try:
            result = prometheus_client.query(query).get('data', {}).get('result', [])
        except Exception:
            result = []

        current: Dict[str, Any] = {}
        for res in result:
            labels = res.get('metric', {})
            node_name = None

            if label_candidates:
                node_name = _first_present_label(labels, label_candidates)

            if not node_name:
                node_name = _extract_node_name(labels)

            if not node_name:
                continue

            value = value_transform(res.get('value', [0, '0'])[1])
            if value is None:
                continue

            current[node_name] = value

        data.update(current)

        if stop_on_first_success and data:
            break

    return data


def _parse_label_selector(selector: Optional[str]) -> Dict[str, str]:
    """Parse a Kubernetes-style label selector string into a dictionary."""
    if not selector:
        return {}
    parsed: Dict[str, str] = {}
    for raw_part in selector.split(","):
        part = raw_part.strip()
        if not part or "=" not in part:
            continue
        key, value = part.split("=", 1)
        key = key.strip()
        value = value.strip()
        if key:
            parsed[key] = value
    return parsed


def _extract_k8s_labels(labels: Dict[str, Any]) -> Dict[str, str]:
    """Extract Kubernetes labels from kube-state-metrics style label_* entries."""
    extracted: Dict[str, str] = {}
    for key, value in labels.items():
        if not value:
            continue
        if key.startswith("label_"):
            extracted[key[len("label_"):]] = value
    return extracted


def _labels_match_selector(labels: Dict[str, str], selector: Dict[str, str]) -> bool:
    """Return True if all selector requirements are satisfied by labels."""
    if not selector:
        return True
    for key, expected in selector.items():
        actual = labels.get(key)
        if actual != expected:
            return False
    return True


def _map_namespace_pod(
    result: List[Dict[str, Any]],
    namespace_label: str = "namespace",
    pod_label: str = "pod",
    value_transform=_safe_float
) -> Dict[Tuple[str, str], Any]:
    """Create a (namespace, pod) -> value map from Prometheus results."""
    data: Dict[Tuple[str, str], Any] = {}
    for res in result:
        labels = res.get('metric', {})
        namespace = labels.get(namespace_label)
        pod = labels.get(pod_label)
        if not namespace or not pod:
            continue
        value = value_transform(res.get('value', [0, '0'])[1])
        if value is None:
            continue
        data[(namespace, pod)] = value
    return data


def _map_namespace_pod_container(
    result: List[Dict[str, Any]],
    namespace_label: str = "namespace",
    pod_label: str = "pod",
    container_label: str = "container",
    value_transform=_safe_float
) -> Dict[Tuple[str, str, str], Any]:
    """Create a (namespace, pod, container) -> value map from Prometheus results."""
    data: Dict[Tuple[str, str, str], Any] = {}
    for res in result:
        labels = res.get('metric', {})
        namespace = labels.get(namespace_label)
        pod = labels.get(pod_label)
        container = labels.get(container_label)
        if not namespace or not pod or not container:
            continue
        value = value_transform(res.get('value', [0, '0'])[1])
        if value is None:
            continue
        data[(namespace, pod, container)] = value
    return data


def _format_cpu_value(value: Optional[float]) -> Optional[str]:
    """Format CPU values (cores) into canonical string representation."""
    if value is None:
        return None
    # Represent values below 1 core as millicores
    if 0 < value < 1:
        return f"{int(value * 1000)}m"
    if value.is_integer():
        return str(int(value))
    return f"{value:.3f}".rstrip("0").rstrip(".")


def _build_node_status_map() -> Dict[str, str]:
    """Build a map of node readiness status from kube-state-metrics."""
    status_map: Dict[str, str] = {}
    try:
        result = prometheus_client.query('kube_node_status_condition{condition="Ready"}').get('data', {}).get('result', [])
    except Exception:
        result = []

    for res in result:
        labels = res.get('metric', {})
        node_name = _extract_node_name(labels)
        if not node_name:
            continue

        recorded_status = labels.get('status')
        value = _safe_float(res.get('value', [0, '0'])[1])
        if value != 1:
            continue

        if recorded_status == "true":
            status_map[node_name] = NodeStatus.READY.value
        elif recorded_status == "false":
            status_map[node_name] = NodeStatus.NOT_READY.value
        elif recorded_status == "unknown":
            status_map[node_name] = NodeStatus.UNKNOWN.value

    return status_map


async def get_node_list(cluster: Optional[str] = None, role: Optional[str] = None, status: Optional[str] = None) -> List[Dict[str, Any]]:
    """
    Fetch list of nodes from Kubernetes and Kepler metrics.

    Data source: 
        - Primary: kube_node_info (all nodes with full metadata)
        - Secondary: kepler_node_info (power source, CPU architecture for nodes with Kepler)
        - Power: kepler_node_platform_joules_total

    Args:
        cluster: Optional cluster filter
        role: Optional role filter (master/worker/gpu/npu)
        status: Optional status filter (ready/notready)

    Returns:
        List of node information dictionaries
    """
    role_filter = _normalize_role_filter(role)
    status_filter = _normalize_status_filter(status)

    # Primary data source: kube_node_info (includes all nodes)
    kube_node_result = prometheus_client.query("kube_node_info").get('data', {}).get('result', [])
    
    if not kube_node_result:
        return []
    
    # Secondary data source: kepler_node_info (additional info for nodes with Kepler)
    kepler_node_result = prometheus_client.query("kepler_node_info").get('data', {}).get('result', [])
    
    # Build Kepler info map by node name
    kepler_info_map = {}
    for kepler_data in kepler_node_result:
        labels = kepler_data.get('metric', {})
        instance = labels.get('instance', '')
        ip = instance.split(':')[0] if ':' in instance else instance
        
        # Get node name from IP mapping
        ip_mapping = _get_ip_to_node_mapping()
        node_name = ip_mapping.get(ip)
        
        if node_name:
            kepler_info_map[node_name] = {
                'cpu_architecture': labels.get('cpu_architecture'),
                'power_source': labels.get('platform_power_source'),
                'components_power_source': labels.get('components_power_source'),
                'instance': instance
            }

    status_map = _build_node_status_map()
    power_map = _query_metric_map(
        ['sum(rate(kepler_node_platform_joules_total[5m])) by (exported_instance, node)'],
        stop_on_first_success=True
    )
    gpu_count_map = _query_metric_map(
        ['count(DCGM_FI_DEV_GPU_UTIL) by (Hostname)'],
        label_candidates=["Hostname", "hostname", "node"],
        value_transform=_safe_int,
        stop_on_first_success=True
    )
    gpu_capacity_map = _query_metric_map(
        ['sum(kube_node_status_capacity{resource="nvidia.com/gpu"}) by (node)'],
        value_transform=_safe_int,
        stop_on_first_success=False
    )
    for node_key, value in gpu_capacity_map.items():
        if value is None:
            continue
        existing = gpu_count_map.get(node_key) or 0
        gpu_count_map[node_key] = max(existing, int(value))

    cpu_capacity_map = _query_metric_map(
        [
            'kube_node_status_capacity_cpu_cores',
            'sum(kube_node_status_capacity{resource="cpu"}) by (node)'
        ],
        value_transform=_safe_float
    )
    cpu_allocatable_map = _query_metric_map(
        [
            'kube_node_status_allocatable_cpu_cores',
            'sum(kube_node_status_allocatable{resource="cpu"}) by (node)'
        ],
        value_transform=_safe_float
    )
    memory_capacity_map = _query_metric_map(
        [
            'kube_node_status_capacity_memory_bytes',
            'sum(kube_node_status_capacity{resource="memory"}) by (node)'
        ],
        value_transform=_safe_float
    )
    memory_allocatable_map = _query_metric_map(
        [
            'kube_node_status_allocatable_memory_bytes',
            'sum(kube_node_status_allocatable{resource="memory"}) by (node)'
        ],
        value_transform=_safe_float
    )
    gpu_allocatable_map = _query_metric_map(
        ['sum(kube_node_status_allocatable{resource="nvidia.com/gpu"}) by (node)'],
        value_transform=_safe_int,
        stop_on_first_success=False
    )

    nodes: List[Dict[str, Any]] = []
    for node_data in kube_node_result:
        labels = node_data.get('metric', {})
        # Get node name directly from kube_node_info
        node_name = labels.get('node')
        if not node_name:
            continue

        cluster_label = labels.get('cluster') or labels.get('cluster_name')
        if cluster and cluster_label and cluster_label != cluster:
            continue

        cluster_value = cluster_label or cluster or 'default'

        gpu_count = gpu_count_map.get(node_name, 0) or 0
        node_role = _determine_node_role(labels, gpu_count)

        if not _role_filter_matches(role_filter, node_role):
            continue

        node_status = status_map.get(node_name, NodeStatus.READY.value)
        if status_filter and node_status != status_filter:
            continue

        # Get Kepler info if available
        kepler_info = kepler_info_map.get(node_name, {})
        
        # Use Kepler instance if available, otherwise construct from internal_ip
        internal_ip = labels.get('internal_ip', node_name)
        instance = kepler_info.get('instance') or f"{internal_ip}:9102"
        
        hostname = node_name  # Use node name as hostname
        container_runtime = labels.get('container_runtime_version')

        cpu_cores = cpu_capacity_map.get(node_name)
        alloc_cpu = cpu_allocatable_map.get(node_name)
        memory_total_mb = _bytes_to_mb(memory_capacity_map.get(node_name))
        alloc_memory_mb = _bytes_to_mb(memory_allocatable_map.get(node_name))
        alloc_gpu_count = gpu_allocatable_map.get(node_name)
        current_power = power_map.get(node_name)
        if current_power is not None:
            current_power = round(float(current_power), 3)

        # Merge kube_node_info and kepler_node_info data
        node_info = {
            'node_name': node_name,
            'instance': instance,
            'cluster': cluster_value,
            'hostname': hostname,
            'role': node_role.value,
            'status': node_status,
            # Prefer Kepler CPU architecture, fallback to kube_node_info
            'cpu_architecture': kepler_info.get('cpu_architecture') or labels.get('cpu_architecture'),
            'kernel_version': labels.get('kernel_version'),
            'os_image': labels.get('os_image'),
            'container_runtime': container_runtime,
            'cpu_cores': cpu_cores,
            'memory_total_mb': memory_total_mb,
            'gpu_count': int(gpu_count),
            'npu_count': 0,
            'allocatable_cpu_cores': alloc_cpu,
            'allocatable_memory_mb': alloc_memory_mb,
            'allocatable_gpu_count': alloc_gpu_count,
            'current_power_watts': current_power,
            # Use Kepler power source if available
            'power_source': kepler_info.get('power_source') or labels.get('platform_power_source'),
            'labels': _sanitize_node_labels(labels),
            'annotations': None
        }
        nodes.append(node_info)

    return nodes


async def get_node_detail(node_name: str) -> Dict[str, Any]:
    """
    Fetch detailed information for a specific node.

    Args:
        node_name: Node hostname

    Returns:
        Dictionary with node information, metrics, and power data
    """
    nodes = await get_node_list()
    node_info = next((node for node in nodes if node.get('node_name') == node_name), None)

    if not node_info:
        raise ValueError(f"Node {node_name} not found")

    # Build detail query (secure version)
    try:
        safe_node_name = sanitize_label_value(node_name)
        label_matcher = build_label_matcher("node", safe_node_name)
        detail_query = f'kepler_node_info{{{label_matcher}}}'
    except PromQLValidationError as e:
        logger.error(f"Invalid node_name in get_node_detail: {e}")
        raise ValueError(f"Invalid node_name parameter: {e}")

    detail_result = prometheus_client.query(detail_query).get('data', {}).get('result', [])

    if detail_result:
        labels = detail_result[0].get('metric', {})
        node_info['labels'] = _sanitize_node_labels(labels)
        node_info['container_runtime'] = labels.get('container_runtime_version') or node_info.get('container_runtime')
        node_info['kernel_version'] = labels.get('kernel_version') or node_info.get('kernel_version')
        node_info['os_image'] = labels.get('os_image') or node_info.get('os_image')
        node_info['power_source'] = labels.get('platform_power_source') or node_info.get('power_source')

        hostname = _first_present_label(labels, ["hostname", "kubernetes_io_hostname"])
        if hostname:
            node_info['hostname'] = hostname

        cluster_label = labels.get('cluster') or labels.get('cluster_name')
        if cluster_label:
            node_info['cluster'] = cluster_label

    return node_info


async def get_node_power(node_name: str, period: Optional[str] = "1h") -> Dict[str, Any]:
    """
    Fetch power consumption data for a specific node.

    Args:
        node_name: Node hostname
        period: Time period for historical data (1h/1d/1w)

    Returns:
        Dictionary with node power data including component breakdown
    """
    # Determine time range
    end_time = datetime.utcnow()
    period_map = {"1h": timedelta(hours=1), "1d": timedelta(days=1), "1w": timedelta(weeks=1), "1m": timedelta(days=30)}
    start_time = end_time - period_map.get(period, timedelta(hours=1))

    total_power_query = f'sum(rate(kepler_node_platform_joules_total{{node="{node_name}"}}[5m]))'
    total_result = prometheus_client.query(total_power_query).get('data', {}).get('result', [])
    total_power = _safe_float(total_result[0].get('value', [0, '0'])[1]) if total_result else 0.0

    cpu_power_query = f'sum(rate(kepler_node_core_joules_total{{node="{node_name}"}}[5m]))'
    dram_power_query = f'sum(rate(kepler_node_dram_joules_total{{node="{node_name}"}}[5m]))'
    accelerator_power_query = f'sum(rate(kepler_node_accelerator_joules_total{{node="{node_name}"}}[5m]))'

    cpu_result = prometheus_client.query(cpu_power_query).get('data', {}).get('result', [])
    dram_result = prometheus_client.query(dram_power_query).get('data', {}).get('result', [])
    accelerator_result = prometheus_client.query(accelerator_power_query).get('data', {}).get('result', [])

    cpu_power = _safe_float(cpu_result[0].get('value', [0, '0'])[1]) if cpu_result else None
    dram_power = _safe_float(dram_result[0].get('value', [0, '0'])[1]) if dram_result else None
    accelerator_power = _safe_float(accelerator_result[0].get('value', [0, '0'])[1]) if accelerator_result else None

    components_sum = sum(value for value in [cpu_power, dram_power, accelerator_power] if value is not None)
    other_power = (total_power - components_sum) if components_sum is not None else None

    timeseries_result = prometheus_client.query_range(
        total_power_query,
        start_time,
        end_time,
        "5m"
    )

    timeseries_points: List[Dict[str, Any]] = []
    power_values: List[float] = []
    result_series = timeseries_result.get('data', {}).get('result', [])
    if result_series:
        for timestamp, value in result_series[0].get('values', []):
            power_value = _safe_float(value)
            if power_value is None:
                continue
            timeseries_points.append({
                'timestamp': datetime.fromtimestamp(timestamp),
                'power_watts': power_value
            })
            power_values.append(power_value)

    avg_power = sum(power_values) / len(power_values) if power_values else total_power
    max_power = max(power_values) if power_values else total_power
    min_power = min(power_values) if power_values else total_power

    duration_hours = (end_time - start_time).total_seconds() / 3600
    total_energy_kwh = (avg_power * duration_hours) / 1000 if avg_power is not None else None

    power_data = {
        'node_name': node_name,
        'period': period,
        'start_time': start_time,
        'end_time': end_time,
        'current': {
            'total_power_watts': total_power or 0.0,
            'cpu_power_watts': cpu_power,
            'dram_power_watts': dram_power,
            'gpu_power_watts': accelerator_power,
            'other_power_watts': other_power
        },
        'statistics': {
            'avg_power_watts': avg_power,
            'max_power_watts': max_power,
            'min_power_watts': min_power,
            'total_energy_kwh': total_energy_kwh
        },
        'timeseries': timeseries_points or None
    }

    return power_data


async def get_node_metrics(node_name: str) -> Dict[str, Any]:
    """
    Fetch resource usage metrics for a specific node.

    Args:
        node_name: Node hostname

    Returns:
        Dictionary with node CPU, memory, disk, network metrics
    """
    # Don't escape dots in node_name for Prometheus regex matching
    # IP addresses like 192.168.1.100 should work as-is in regex
    node_regex = node_name

    cpu_query = f'100 - (avg by (instance) (rate(node_cpu_seconds_total{{mode="idle",instance=~".*{node_regex}.*"}}[5m])) * 100)'
    cpu_result = prometheus_client.query(cpu_query).get('data', {}).get('result', [])
    cpu_utilization = _safe_float(cpu_result[0].get('value', [0, '0'])[1]) if cpu_result else None

    load1_query = f'avg(node_load1{{instance=~".*{node_regex}.*"}})'
    load5_query = f'avg(node_load5{{instance=~".*{node_regex}.*"}})'
    load15_query = f'avg(node_load15{{instance=~".*{node_regex}.*"}})'

    load1_result = prometheus_client.query(load1_query).get('data', {}).get('result', [])
    load5_result = prometheus_client.query(load5_query).get('data', {}).get('result', [])
    load15_result = prometheus_client.query(load15_query).get('data', {}).get('result', [])

    cpu_load_1 = _safe_float(load1_result[0].get('value', [0, '0'])[1]) if load1_result else None
    cpu_load_5 = _safe_float(load5_result[0].get('value', [0, '0'])[1]) if load5_result else None
    cpu_load_15 = _safe_float(load15_result[0].get('value', [0, '0'])[1]) if load15_result else None

    mem_total_query = f'avg(node_memory_MemTotal_bytes{{instance=~".*{node_regex}.*"}})'
    mem_available_query = f'avg(node_memory_MemAvailable_bytes{{instance=~".*{node_regex}.*"}})'

    mem_total_result = prometheus_client.query(mem_total_query).get('data', {}).get('result', [])
    mem_available_result = prometheus_client.query(mem_available_query).get('data', {}).get('result', [])

    memory_total_mb = _bytes_to_mb(_safe_float(mem_total_result[0].get('value', [0, '0'])[1])) if mem_total_result else None
    memory_available_mb = _bytes_to_mb(_safe_float(mem_available_result[0].get('value', [0, '0'])[1])) if mem_available_result else None

    memory_used_mb = None
    memory_utilization = None
    if memory_total_mb is not None and memory_available_mb is not None:
        memory_used_mb = max(memory_total_mb - memory_available_mb, 0)
        memory_utilization = (memory_used_mb / memory_total_mb * 100) if memory_total_mb > 0 else 0

    disk_total_query = (
        f'sum(node_filesystem_size_bytes{{instance=~".*{node_regex}.*",fstype!~"tmpfs|fuse.lxcfs|squashfs|overlay|rpc_pipefs|proc"}})'
    )
    disk_free_query = (
        f'sum(node_filesystem_free_bytes{{instance=~".*{node_regex}.*",fstype!~"tmpfs|fuse.lxcfs|squashfs|overlay|rpc_pipefs|proc"}})'
    )

    disk_total_result = prometheus_client.query(disk_total_query).get('data', {}).get('result', [])
    disk_free_result = prometheus_client.query(disk_free_query).get('data', {}).get('result', [])

    disk_total_mb = _bytes_to_mb(_safe_float(disk_total_result[0].get('value', [0, '0'])[1])) if disk_total_result else None
    disk_free_mb = _bytes_to_mb(_safe_float(disk_free_result[0].get('value', [0, '0'])[1])) if disk_free_result else None

    disk_used_mb = None
    disk_avail_mb = disk_free_mb
    disk_util_percent = None
    if disk_total_mb is not None and disk_free_mb is not None:
        disk_used_mb = max(disk_total_mb - disk_free_mb, 0)
        disk_util_percent = (disk_used_mb / disk_total_mb * 100) if disk_total_mb > 0 else 0

    network_rx_query = (
        f'sum(rate(node_network_receive_bytes_total{{instance=~".*{node_regex}.*",device!~"lo|docker.*|cni.*|flannel.*|veth.*"}}[5m]))'
    )
    network_tx_query = (
        f'sum(rate(node_network_transmit_bytes_total{{instance=~".*{node_regex}.*",device!~"lo|docker.*|cni.*|flannel.*|veth.*"}}[5m]))'
    )

    network_rx_result = prometheus_client.query(network_rx_query).get('data', {}).get('result', [])
    network_tx_result = prometheus_client.query(network_tx_query).get('data', {}).get('result', [])

    network_rx_bytes = _safe_float(network_rx_result[0].get('value', [0, '0'])[1]) if network_rx_result else None
    network_tx_bytes = _safe_float(network_tx_result[0].get('value', [0, '0'])[1]) if network_tx_result else None

    network_rx_mbps = (network_rx_bytes * 8 / 1_000_000) if network_rx_bytes is not None else None
    network_tx_mbps = (network_tx_bytes * 8 / 1_000_000) if network_tx_bytes is not None else None

    pod_query = f'count(count(kepler_container_package_joules_total{{node="{node_name}"}}) by (pod_name))'
    pod_result = prometheus_client.query(pod_query).get('data', {}).get('result', [])
    pod_count = _safe_int(pod_result[0].get('value', [0, '0'])[1]) if pod_result else 0

    container_query = f'count(count(kepler_container_package_joules_total{{node="{node_name}"}}) by (container_id))'
    container_result = prometheus_client.query(container_query).get('data', {}).get('result', [])
    container_count = _safe_int(container_result[0].get('value', [0, '0'])[1]) if container_result else 0

    metrics = {
        'node_name': node_name,
        'timestamp': datetime.utcnow(),
        'cpu_utilization_percent': cpu_utilization,
        'cpu_load_1min': cpu_load_1,
        'cpu_load_5min': cpu_load_5,
        'cpu_load_15min': cpu_load_15,
        'memory_total_mb': memory_total_mb,
        'memory_available_mb': memory_available_mb,
        'memory_used_mb': memory_used_mb,
        'memory_utilization_percent': memory_utilization,
        'disk_total_mb': disk_total_mb,
        'disk_available_mb': disk_avail_mb,
        'disk_used_mb': disk_used_mb,
        'disk_utilization_percent': disk_util_percent,
        'network_rx_mbps': network_rx_mbps,
        'network_tx_mbps': network_tx_mbps,
        'pod_count': pod_count or 0,
        'container_count': container_count or 0
    }

    return metrics


async def get_nodes_summary(cluster: Optional[str] = None) -> Dict[str, Any]:
    """
    Fetch summary statistics for all nodes.

    Args:
        cluster: Optional cluster filter

    Returns:
        Dictionary with nodes summary including counts, capacity, and power
    """
    nodes = await get_node_list(cluster=cluster)

    total_nodes = len(nodes)
    total_gpus = sum(node.get('gpu_count', 0) or 0 for node in nodes)
    total_npus = sum(node.get('npu_count', 0) or 0 for node in nodes)

    ready_nodes = sum(1 for node in nodes if node.get('status') == NodeStatus.READY.value)
    not_ready_nodes = sum(1 for node in nodes if node.get('status') == NodeStatus.NOT_READY.value)
    unknown_nodes = sum(1 for node in nodes if node.get('status') == NodeStatus.UNKNOWN.value)

    total_power = sum(_safe_float(node.get('current_power_watts')) or 0.0 for node in nodes)
    if total_power == 0 and total_nodes > 0:
        total_power_query = "sum(rate(kepler_node_platform_joules_total[5m]))"
        total_result = prometheus_client.query(total_power_query).get('data', {}).get('result', [])
        total_power = _safe_float(total_result[0].get('value', [0, '0'])[1]) if total_result else 0.0

    avg_power_per_node = total_power / total_nodes if total_nodes > 0 else 0.0

    roles: Dict[str, int] = {}
    for node in nodes:
        role_value = node.get('role', NodeRole.WORKER.value)
        roles[role_value] = roles.get(role_value, 0) + 1

    top_nodes = sorted(
        nodes,
        key=lambda node: node.get('current_power_watts') or 0,
        reverse=True
    )[:5]

    summary = {
        'timestamp': datetime.utcnow(),
        'cluster': cluster or 'default',
        'summary': {
            'total_nodes': total_nodes,
            'ready_nodes': ready_nodes,
            'not_ready_nodes': not_ready_nodes,
            'unknown_nodes': unknown_nodes,
            'total_gpus': total_gpus,
            'total_npus': total_npus,
            'total_power_watts': total_power,
            'avg_power_per_node_watts': avg_power_per_node,
            'nodes_by_role': roles
        },
        'top_nodes_by_power': top_nodes
    }

    return summary


# ============================================================================
# IPMI Hardware Monitoring Helper Functions (Phase 5)
# ============================================================================

async def get_ipmi_all_sensors(
    node_filter: Optional[str] = None,
    sensor_type_filter: Optional[str] = None
) -> List[Dict[str, Any]]:
    """
    Get all IPMI sensors from Prometheus.

    Args:
        node_filter: Filter by node hostname
        sensor_type_filter: Filter by sensor type (temperature/power/fan/voltage)

    Returns:
        List of all IPMI sensors with metadata
    """
    from app.services.collectors.ipmi import IPMICollector
    from app.models.hardware.ipmi import IPMISensorType

    collector = IPMICollector(prometheus_client)

    # Parse sensor type filter
    sensor_type_enum = None
    if sensor_type_filter:
        try:
            sensor_type_enum = IPMISensorType(sensor_type_filter.lower())
        except ValueError:
            # Invalid sensor type, will return empty
            pass

    sensors = await collector.get_all_sensors(
        node_filter=node_filter,
        sensor_type_filter=sensor_type_enum
    )

    return [sensor.dict() for sensor in sensors]


async def get_ipmi_power(
    node_filter: Optional[str] = None
) -> List[Dict[str, Any]]:
    """
    Get IPMI power sensor data from Prometheus.

    Args:
        node_filter: Filter by node hostname

    Returns:
        List of power data per node
    """
    from app.services.collectors.ipmi import IPMICollector

    collector = IPMICollector(prometheus_client)
    power_data = await collector.get_power_data(node_filter=node_filter)

    return [data.dict() for data in power_data]


async def get_ipmi_temperature(
    node_filter: Optional[str] = None
) -> List[Dict[str, Any]]:
    """
    Get IPMI temperature sensor data from Prometheus.

    Args:
        node_filter: Filter by node hostname

    Returns:
        List of temperature data per node
    """
    from app.services.collectors.ipmi import IPMICollector

    collector = IPMICollector(prometheus_client)
    temp_data = await collector.get_temperature_data(node_filter=node_filter)

    return [data.dict() for data in temp_data]


async def get_ipmi_fans(
    node_filter: Optional[str] = None
) -> List[Dict[str, Any]]:
    """
    Get IPMI fan sensor data from Prometheus.

    Args:
        node_filter: Filter by node hostname

    Returns:
        List of fan data per node
    """
    from app.services.collectors.ipmi import IPMICollector

    collector = IPMICollector(prometheus_client)
    fan_data = await collector.get_fan_data(node_filter=node_filter)

    return [data.dict() for data in fan_data]


async def get_ipmi_voltage(
    node_filter: Optional[str] = None
) -> List[Dict[str, Any]]:
    """
    Get IPMI voltage sensor data from Prometheus.

    Args:
        node_filter: Filter by node hostname

    Returns:
        List of voltage data per node
    """
    from app.services.collectors.ipmi import IPMICollector

    collector = IPMICollector(prometheus_client)
    voltage_data = await collector.get_voltage_data(node_filter=node_filter)

    return [data.dict() for data in voltage_data]


async def get_ipmi_summary(
    node_filter: Optional[str] = None
) -> Dict[str, Any]:
    """
    Get IPMI summary statistics from Prometheus.

    Args:
        node_filter: Filter by node hostname

    Returns:
        Summary statistics including power, temperature, and fan status
    """
    from app.services.collectors.ipmi import IPMICollector
    from app.models.hardware.ipmi import IPMISensorStatus

    collector = IPMICollector(prometheus_client)

    # Collect all data
    power_data = await collector.get_power_data(node_filter=node_filter)
    temp_data = await collector.get_temperature_data(node_filter=node_filter)
    fan_data = await collector.get_fan_data(node_filter=node_filter)

    # Calculate summary statistics
    total_nodes = len(power_data)

    # Power summary
    total_power_watts = sum(p.total_power_watts for p in power_data)
    avg_power_watts = total_power_watts / total_nodes if total_nodes > 0 else 0.0

    # Temperature summary
    all_temps = [t.highest_temperature_celsius for t in temp_data if t.highest_temperature_celsius]
    highest_temperature_celsius = max(all_temps) if all_temps else None
    avg_temperature_celsius = sum(all_temps) / len(all_temps) if all_temps else None

    critical_temperature_count = sum(t.critical_temperature_count for t in temp_data)
    warning_temperature_count = sum(t.warning_temperature_count for t in temp_data)

    # Fan summary
    total_fans = sum(f.avg_fan_speed_rpm is not None for f in fan_data) * 6  # Estimate
    failed_fans = sum(f.fan_failure_count for f in fan_data)
    all_fan_speeds = [f.avg_fan_speed_rpm for f in fan_data if f.avg_fan_speed_rpm]
    avg_fan_speed_rpm = sum(all_fan_speeds) / len(all_fan_speeds) if all_fan_speeds else None

    # Health status
    critical_nodes = sum(
        1 for t in temp_data if t.overall_temperature_status == IPMISensorStatus.CRITICAL
    ) + sum(
        1 for f in fan_data if f.overall_fan_status == IPMISensorStatus.CRITICAL
    )

    warning_nodes = sum(
        1 for t in temp_data if t.overall_temperature_status == IPMISensorStatus.WARNING
    ) + sum(
        1 for f in fan_data if f.overall_fan_status == IPMISensorStatus.WARNING
    )

    healthy_nodes = total_nodes - critical_nodes - warning_nodes

    return {
        'timestamp': datetime.utcnow(),
        'total_nodes': total_nodes,
        'total_power_watts': total_power_watts,
        'avg_power_watts': avg_power_watts,
        'highest_temperature_celsius': highest_temperature_celsius,
        'avg_temperature_celsius': avg_temperature_celsius,
        'critical_temperature_count': critical_temperature_count,
        'warning_temperature_count': warning_temperature_count,
        'total_fans': total_fans,
        'failed_fans': failed_fans,
        'avg_fan_speed_rpm': avg_fan_speed_rpm,
        'critical_nodes': critical_nodes,
        'warning_nodes': warning_nodes,
        'healthy_nodes': healthy_nodes
    }


# ============================================================================
# Unified Power Monitoring (Phase 7.1)
# ============================================================================

async def get_unified_power(
    cluster: Optional[str] = None,
    resource_types: Optional[List[str]] = None
) -> Dict[str, Any]:
    """
    Get unified power consumption across all resource types.

    Args:
        cluster: Cluster name filter
        resource_types: List of resource types to include (gpus, npus, nodes, pods, vms)

    Returns:
        Dict with total power and breakdown by resource type
    """
    from datetime import datetime

    # Default to all resource types
    if resource_types is None:
        resource_types = ['gpus', 'nodes', 'pods']

    breakdown = {
        'accelerators': {},
        'infrastructure': {},
        'hardware': {}
    }
    total_power = 0.0

    # GPU power (DCGM/Kepler)
    if 'gpus' in resource_types:
        try:
            gpu_summary = await get_gpu_summary()
            gpu_power = gpu_summary.get('total_power_watts', 0.0)
            breakdown['accelerators']['gpus'] = gpu_power
            total_power += gpu_power
        except Exception as e:
            logger.warning(f"Failed to get GPU power: {e}")
            breakdown['accelerators']['gpus'] = 0.0

    # NPU power (Placeholder)
    if 'npus' in resource_types:
        try:
            npu_summary = await get_npu_summary()
            npu_power = npu_summary.get('total_power_watts', 0.0)
            breakdown['accelerators']['npus'] = npu_power
            total_power += npu_power
        except Exception as e:
            logger.warning(f"Failed to get NPU power: {e}")
            breakdown['accelerators']['npus'] = 0.0

    # Node power (Kepler)
    if 'nodes' in resource_types:
        try:
            node_summary = await get_node_summary()
            node_power = node_summary.get('total_power_watts', 0.0)
            breakdown['infrastructure']['nodes'] = node_power
            total_power += node_power
        except Exception as e:
            logger.warning(f"Failed to get node power: {e}")
            breakdown['infrastructure']['nodes'] = 0.0

    # Pod power (Kepler)
    if 'pods' in resource_types:
        try:
            pod_summary = await get_pod_summary()
            pod_power = pod_summary.get('total_power_watts', 0.0)
            breakdown['infrastructure']['pods'] = pod_power
            # Note: Don't add to total as it's already included in node power
        except Exception as e:
            logger.warning(f"Failed to get pod power: {e}")
            breakdown['infrastructure']['pods'] = 0.0

    # VM power (OpenStack - Placeholder)
    if 'vms' in resource_types:
        breakdown['infrastructure']['vms'] = 0.0  # Not implemented yet

    # IPMI hardware power (if available)
    try:
        ipmi_summary = await get_ipmi_summary()
        ipmi_power = ipmi_summary.get('total_power_watts', 0.0)
        if ipmi_power > 0:
            breakdown['hardware']['ipmi_measured'] = ipmi_power
    except Exception:
        pass

    return {
        'timestamp': datetime.utcnow(),
        'data': {
            'total_power_watts': total_power,
            'breakdown': breakdown
        }
    }


async def get_accelerator_power(cluster: Optional[str] = None) -> Dict[str, Any]:
    """Get power consumption from accelerators only (GPUs + NPUs)."""
    from datetime import datetime

    total_power = 0.0
    breakdown = {}

    # GPU power
    try:
        gpu_summary = await get_gpu_summary()
        gpu_power = gpu_summary.get('total_power_watts', 0.0)
        breakdown['gpus'] = {
            'power_watts': gpu_power,
            'count': gpu_summary.get('total_gpus', 0),
            'avg_power_watts': gpu_summary.get('avg_power_watts', 0.0)
        }
        total_power += gpu_power
    except Exception as e:
        logger.warning(f"Failed to get GPU power: {e}")
        breakdown['gpus'] = {'power_watts': 0.0, 'count': 0, 'avg_power_watts': 0.0}

    # NPU power
    try:
        npu_summary = await get_npu_summary()
        npu_power = npu_summary.get('total_power_watts', 0.0)
        breakdown['npus'] = {
            'power_watts': npu_power,
            'count': npu_summary.get('total_npus', 0),
            'avg_power_watts': npu_summary.get('avg_power_watts', 0.0)
        }
        total_power += npu_power
    except Exception as e:
        logger.warning(f"Failed to get NPU power: {e}")
        breakdown['npus'] = {'power_watts': 0.0, 'count': 0, 'avg_power_watts': 0.0}

    return {
        'timestamp': datetime.utcnow(),
        'data': {
            'total_power_watts': total_power,
            'breakdown': breakdown
        }
    }


async def get_infrastructure_power(cluster: Optional[str] = None) -> Dict[str, Any]:
    """Get power consumption from infrastructure (Nodes + Pods + VMs)."""
    from datetime import datetime

    total_power = 0.0
    breakdown = {}

    # Node power
    try:
        node_summary = await get_node_summary()
        node_power = node_summary.get('total_power_watts', 0.0)
        breakdown['nodes'] = {
            'power_watts': node_power,
            'count': node_summary.get('total_nodes', 0),
            'avg_power_watts': node_summary.get('avg_power_watts', 0.0)
        }
        total_power += node_power
    except Exception as e:
        logger.warning(f"Failed to get node power: {e}")
        breakdown['nodes'] = {'power_watts': 0.0, 'count': 0, 'avg_power_watts': 0.0}

    # Pod power (for informational purposes - already included in node power)
    try:
        pod_summary = await get_pod_summary()
        breakdown['pods'] = {
            'power_watts': pod_summary.get('total_power_watts', 0.0),
            'count': pod_summary.get('total_pods', 0),
            'note': 'Included in node power, shown for breakdown only'
        }
    except Exception as e:
        logger.warning(f"Failed to get pod power: {e}")
        breakdown['pods'] = {'power_watts': 0.0, 'count': 0}

    # VM power (Placeholder)
    breakdown['vms'] = {'power_watts': 0.0, 'count': 0, 'status': 'not_implemented'}

    return {
        'timestamp': datetime.utcnow(),
        'data': {
            'total_power_watts': total_power,
            'breakdown': breakdown
        }
    }


async def get_power_breakdown(
    breakdown_by: str,
    cluster: Optional[str] = None
) -> Dict[str, Any]:
    """
    Get detailed power breakdown by specified dimension.

    Args:
        breakdown_by: Breakdown dimension (cluster/node/namespace/vendor/resource_type)
        cluster: Cluster name filter

    Returns:
        Dict with power breakdown data
    """
    from datetime import datetime

    breakdowns = []
    total_power = 0.0

    if breakdown_by == 'node':
        # Get power breakdown by node
        try:
            nodes = await get_nodes()
            for node in nodes:
                node_power = node.get('total_power_watts', 0.0)
                breakdowns.append({
                    'node': node.get('node_name'),
                    'power_watts': node_power,
                    'role': node.get('role', 'worker'),
                    'gpus': node.get('gpu_count', 0)
                })
                total_power += node_power
        except Exception as e:
            logger.error(f"Failed to get node power breakdown: {e}")

    elif breakdown_by == 'namespace':
        # Get power breakdown by namespace
        try:
            pods = await get_pods()
            namespace_power = {}
            for pod in pods:
                ns = pod.get('namespace', 'default')
                power = pod.get('total_power_watts', 0.0)
                if ns not in namespace_power:
                    namespace_power[ns] = {'power': 0.0, 'pods': 0}
                namespace_power[ns]['power'] += power
                namespace_power[ns]['pods'] += 1
                total_power += power

            for ns, data in namespace_power.items():
                percentage = (data['power'] / total_power * 100) if total_power > 0 else 0
                breakdowns.append({
                    'namespace': ns,
                    'power_watts': data['power'],
                    'pods': data['pods'],
                    'percentage': round(percentage, 2)
                })
        except Exception as e:
            logger.error(f"Failed to get namespace power breakdown: {e}")

    elif breakdown_by == 'vendor':
        # Get power breakdown by GPU vendor
        try:
            gpu_info = await get_gpu_info()
            vendor_power = {}
            for gpu in gpu_info:
                vendor = gpu.get('vendor', 'Unknown')
                power = gpu.get('power_draw_watts', 0.0)
                if vendor not in vendor_power:
                    vendor_power[vendor] = {'power': 0.0, 'count': 0}
                vendor_power[vendor]['power'] += power
                vendor_power[vendor]['count'] += 1
                total_power += power

            for vendor, data in vendor_power.items():
                percentage = (data['power'] / total_power * 100) if total_power > 0 else 0
                breakdowns.append({
                    'vendor': vendor,
                    'power_watts': data['power'],
                    'count': data['count'],
                    'percentage': round(percentage, 2)
                })
        except Exception as e:
            logger.error(f"Failed to get vendor power breakdown: {e}")

    elif breakdown_by == 'resource_type':
        # Get power breakdown by resource type
        unified = await get_unified_power(cluster)
        accelerator_data = unified['data']['breakdown'].get('accelerators', {})
        infrastructure_data = unified['data']['breakdown'].get('infrastructure', {})

        for resource_type, power in accelerator_data.items():
            if power > 0:
                percentage = (power / total_power * 100) if total_power > 0 else 0
                breakdowns.append({
                    'resource_type': resource_type,
                    'category': 'accelerators',
                    'power_watts': power,
                    'percentage': round(percentage, 2)
                })

        for resource_type, power in infrastructure_data.items():
            if power > 0:
                percentage = (power / total_power * 100) if total_power > 0 else 0
                breakdowns.append({
                    'resource_type': resource_type,
                    'category': 'infrastructure',
                    'power_watts': power,
                    'percentage': round(percentage, 2)
                })

        total_power = unified['data']['total_power_watts']

    elif breakdown_by == 'cluster':
        # Placeholder for multi-cluster breakdown
        breakdowns.append({
            'cluster': cluster or 'default',
            'power_watts': total_power,
            'status': 'single_cluster_mode'
        })

    else:
        raise ValueError(f"Invalid breakdown_by value: {breakdown_by}")

    # Sort by power (descending)
    breakdowns.sort(key=lambda x: x.get('power_watts', 0), reverse=True)

    return {
        'timestamp': datetime.utcnow(),
        'breakdown_by': breakdown_by,
        'data': {
            'breakdowns': breakdowns,
            'total_power_watts': total_power
        }
    }


async def get_power_efficiency(cluster: Optional[str] = None) -> Dict[str, Any]:
    """
    Get power efficiency metrics including PUE.

    Note: PUE calculation requires total facility power data which may not be available
    from Prometheus. This implementation provides estimated values.
    """
    from datetime import datetime

    # Get IT power (compute equipment)
    unified_power = await get_unified_power(cluster)
    it_power = unified_power['data']['total_power_watts']

    # Estimate cooling and overhead power (typically 30-50% of IT power)
    # This should be replaced with actual facility power metrics
    cooling_factor = 0.35  # 35% overhead estimate
    cooling_power = it_power * cooling_factor
    total_facility_power = it_power + cooling_power

    # Calculate PUE (Power Usage Effectiveness)
    # PUE = Total Facility Power / IT Equipment Power
    # Good: 1.2, Acceptable: 1.5, Poor: 2.0+
    pue = total_facility_power / it_power if it_power > 0 else 0.0

    # Calculate additional efficiency metrics
    gpu_summary = await get_gpu_summary()
    total_gpus = gpu_summary.get('total_gpus', 0)
    avg_gpu_utilization = gpu_summary.get('avg_utilization_percent', 0.0)

    # Compute per watt (operations per watt - simplified)
    # This should be based on actual workload metrics
    compute_per_watt = (total_gpus * avg_gpu_utilization) / it_power if it_power > 0 else 0.0

    # GPU efficiency (utilization vs power consumption)
    gpu_efficiency = avg_gpu_utilization if avg_gpu_utilization > 0 else 0.0

    return {
        'timestamp': datetime.utcnow(),
        'data': {
            'pue': round(pue, 2),
            'it_power_watts': it_power,
            'total_facility_power_watts': total_facility_power,
            'cooling_power_watts': cooling_power,
            'efficiency_metrics': {
                'compute_per_watt': round(compute_per_watt, 2),
                'gpu_efficiency_percent': round(gpu_efficiency, 2)
            },
            'note': 'PUE calculated with estimated cooling power. Deploy facility power monitoring for accurate values.'
        }
    }


# ============================================================================
# Timeseries Data (Phase 7.2)
# ============================================================================

async def get_metrics_timeseries(
    metric_name: str,
    resource_type: Optional[str] = None,
    period: str = "1h",
    step: Optional[str] = "5m"
) -> Dict[str, Any]:
    """
    Get performance metrics timeseries data.

    Args:
        metric_name: Metric name (utilization, temperature, memory_usage)
        resource_type: Resource type filter (gpus, npus, nodes)
        period: Time period
        step: Sampling interval

    Returns:
        Dict with metrics timeseries data
    """
    from datetime import datetime

    # Map metric names to Prometheus queries
    metric_queries = {
        'utilization': {
            'gpus': 'DCGM_FI_DEV_GPU_UTIL',
            'nodes': 'rate(kepler_node_platform_joules_total[5m])',
        },
        'temperature': {
            'gpus': 'DCGM_FI_DEV_GPU_TEMP',
            'nodes': 'ipmi_temperature_celsius',
        },
        'memory_usage': {
            'gpus': 'DCGM_FI_DEV_FB_USED',
            'nodes': 'node_memory_MemTotal_bytes - node_memory_MemAvailable_bytes',
        }
    }

    # Get query for specified metric and resource type
    if metric_name not in metric_queries:
        raise ValueError(f"Invalid metric name: {metric_name}. Valid values: {list(metric_queries.keys())}")

    if resource_type and resource_type not in ['gpus', 'npus', 'nodes']:
        raise ValueError(f"Invalid resource type: {resource_type}. Valid values: gpus, npus, nodes")

    # Parse period and step
    period_seconds = parse_period(period)
    step_str = step or "5m"

    # Build query
    if resource_type:
        if resource_type not in metric_queries[metric_name]:
            raise ValueError(f"Metric '{metric_name}' not available for resource type '{resource_type}'")
        query = metric_queries[metric_name][resource_type]
    else:
        # Default to GPUs if no resource type specified
        query = metric_queries[metric_name].get('gpus', list(metric_queries[metric_name].values())[0])

    # Execute Prometheus query
    try:
        result = prometheus_client.query_range(query, period_seconds, step_str)

        # Transform to timeseries format
        timeseries = []
        for series in result:
            resource_id = series['metric'].get('gpu', series['metric'].get('instance', series['metric'].get('node', 'unknown')))
            datapoints = [
                {'timestamp': ts, 'value': float(value)}
                for ts, value in series['values']
            ]
            timeseries.append({
                'resource_id': resource_id,
                'metric': metric_name,
                'resource_type': resource_type or 'unknown',
                'datapoints': datapoints
            })

        return {
            'timestamp': datetime.utcnow(),
            'metric_name': metric_name,
            'resource_type': resource_type,
            'period': period,
            'step': step_str,
            'data': timeseries
        }

    except Exception as e:
        logger.error(f"Failed to query metrics timeseries: {e}")
        raise


async def get_temperature_timeseries(
    resource_type: Optional[str] = None,
    period: str = "1h",
    step: Optional[str] = "5m"
) -> Dict[str, Any]:
    """
    Get temperature timeseries data.

    Args:
        resource_type: Resource type filter (gpus, npus, nodes)
        period: Time period
        step: Sampling interval

    Returns:
        Dict with temperature timeseries data
    """
    # Reuse metrics timeseries with temperature metric
    return await get_metrics_timeseries('temperature', resource_type, period, step)

# ============================================================================
# Cluster-specific resource queries (Phase 6)
# ============================================================================

async def get_cluster_topology(
    cluster_name: str, 
    include_pods: bool = True,
    namespace: Optional[str] = None
) -> Dict[str, Any]:
    """
    Get cluster topology showing node-pod relationships from Kubernetes metrics.
    
    Uses kube_node_info for all nodes (including master) and kube_pod_info for pods.
    
    Args:
        cluster_name: Cluster name
        include_pods: Include pod information
        namespace: Filter pods by namespace
    
    Returns:
        Dict with topology data (nodes, pods, connections)
    """
    from datetime import datetime
    
    try:
        # Query kube_node_info for all nodes
        query = 'kube_node_info'
        result = prometheus_client.query(query)
        
        if not result or 'data' not in result or 'result' not in result['data']:
            return {
                'nodes': [],
                'pods': [],
                'connections': [],
                'summary': {
                    'total_nodes': 0,
                    'total_pods': 0,
                    'master_nodes': 0,
                    'worker_nodes': 0
                }
            }
        
        # Get kepler_node_info for power source info
        kepler_query = 'kepler_node_info'
        kepler_result = prometheus_client.query(kepler_query)
        kepler_data = {}
        if kepler_result and 'data' in kepler_result and 'result' in kepler_result['data']:
            for item in kepler_result['data']['result']:
                metric = item.get('metric', {})
                instance = metric.get('instance', '')
                # Extract IP from instance (format: "IP:PORT")
                node_ip = instance.split(':')[0] if ':' in instance else instance
                kepler_data[node_ip] = {
                    'cpu_architecture': metric.get('cpu_architecture'),
                    'power_source': metric.get('platform_power_source'),
                    'components_power_source': metric.get('components_power_source')
                }
        
        # Build nodes list
        nodes = []
        master_count = 0
        worker_count = 0
        
        for item in result['data']['result']:
            metric = item.get('metric', {})
            node_name = metric.get('node', 'unknown')
            internal_ip = metric.get('internal_ip', '')
            
            # Determine role (master if name contains 'master', otherwise worker)
            role = 'master' if 'master' in node_name.lower() else 'worker'
            if role == 'master':
                master_count += 1
            else:
                worker_count += 1
            
            # Get kepler info if available
            kepler_info = kepler_data.get(internal_ip, {})
            
            node_entry = {
                'name': node_name,
                'internal_ip': internal_ip,
                'role': role,
                'os_image': metric.get('os_image'),
                'kernel_version': metric.get('kernel_version'),
                'kubelet_version': metric.get('kubelet_version'),
                'container_runtime': metric.get('container_runtime_version'),
                'system_uuid': metric.get('system_uuid'),
                'pod_cidr': metric.get('pod_cidr'),
                'cpu_architecture': kepler_info.get('cpu_architecture'),
                'power_source': kepler_info.get('power_source'),
                'has_kepler': bool(kepler_info),
                'pods': []
            }
            
            nodes.append(node_entry)
        
        # Get pods if requested
        pods_list = []
        connections = []
        
        if include_pods:
            # Query kube_pod_info (secure version)
            pod_query = 'kube_pod_info'
            if namespace:
                try:
                    safe_namespace = sanitize_label_value(namespace)
                    label_matcher = build_label_matcher("namespace", safe_namespace)
                    pod_query = f'kube_pod_info{{{label_matcher}}}'
                except PromQLValidationError as e:
                    logger.error(f"Invalid namespace in get_cluster_summary: {e}")
                    raise ValueError(f"Invalid namespace parameter: {e}")
            
            pod_result = prometheus_client.query(pod_query)
            
            if pod_result and 'data' in pod_result and 'result' in pod_result['data']:
                for item in pod_result['data']['result']:
                    metric = item.get('metric', {})
                    pod_name = metric.get('pod', 'unknown')
                    pod_namespace = metric.get('namespace', 'default')
                    node_name = metric.get('node', 'unknown')
                    
                    pod_entry = {
                        'name': pod_name,
                        'namespace': pod_namespace,
                        'node': node_name,
                        'host_ip': metric.get('host_ip'),
                        'pod_ip': metric.get('pod_ip'),
                        'uid': metric.get('uid'),
                        'created_by_kind': metric.get('created_by_kind'),
                        'created_by_name': metric.get('created_by_name')
                    }
                    
                    pods_list.append(pod_entry)
                    
                    # Add to node's pod list
                    for node in nodes:
                        if node['name'] == node_name:
                            node['pods'].append({
                                'name': pod_name,
                                'namespace': pod_namespace
                            })
                            break
                    
                    # Create connection
                    connections.append({
                        'source': f"{pod_namespace}/{pod_name}",
                        'target': node_name,
                        'type': 'pod_to_node'
                    })
        
        # Add pod counts to nodes
        for node in nodes:
            node['pod_count'] = len(node['pods'])
        
        return {
            'nodes': nodes,
            'pods': pods_list if include_pods else [],
            'connections': connections,
            'summary': {
                'total_nodes': len(nodes),
                'total_pods': len(pods_list),
                'master_nodes': master_count,
                'worker_nodes': worker_count,
                'nodes_with_kepler': sum(1 for n in nodes if n.get('has_kepler'))
            }
        }
        
    except Exception as e:
        logger.error(f"Failed to get cluster topology: {e}")
        raise Exception(f"Failed to get cluster topology for {cluster_name}: {e}")


async def get_cluster_accelerators(cluster_name: str, include_metrics: bool = False) -> Dict[str, Any]:
    """
    Get all accelerators (GPUs/NPUs) in a specific cluster.
    
    Args:
        cluster_name: Cluster name
        include_metrics: Include real-time metrics
    
    Returns:
        Dict with accelerators data and summary
    """
    from datetime import datetime
    
    try:
        # Get GPU data
        gpu_data = await get_dcgm_gpu_info(node=None)
        gpu_metrics = await get_dcgm_gpu_metrics(node=None) if include_metrics else []
        
        # Get NPU data (placeholder - will return empty until NPU exporters configured)
        npu_data = await get_npu_info(node=None, vendor=None)
        npu_metrics = await get_npu_metrics(node=None, npu_id=None, vendor=None) if include_metrics else []
        
        # Build GPU list
        gpus = []
        total_gpu_power = 0
        for gpu in (gpu_data or []):
            gpu_id = gpu.get('gpu_id', 'unknown')
            gpu_entry = {
                'type': 'gpu',
                'id': gpu_id,
                'uuid': gpu.get('uuid'),
                'model': gpu.get('model_name', 'unknown'),
                'vendor': 'nvidia',
                'hostname': gpu.get('hostname', 'unknown'),
                'pci_bus_id': gpu.get('pci_bus_id')
            }
            
            # Add metrics if requested
            if include_metrics:
                gpu_metric = next((m for m in gpu_metrics if m.get('gpu_id') == gpu_id), None)
                if gpu_metric:
                    power = _safe_float(gpu_metric.get('power_usage_watts')) or 0
                    total_gpu_power += power
                    gpu_entry['metrics'] = {
                        'power_watts': power,
                        'utilization_percent': _safe_float(gpu_metric.get('gpu_utilization_percent')),
                        'temperature_celsius': _safe_float(gpu_metric.get('gpu_temperature_celsius')),
                        'memory_used_mb': _safe_int(gpu_metric.get('memory_used_mb'))
                    }
            
            gpus.append(gpu_entry)
        
        # Build NPU list
        npus = []
        total_npu_power = 0
        for npu in (npu_data or []):
            npu_id = npu.get('npu_id', 'unknown')
            npu_entry = {
                'type': 'npu',
                'id': npu_id,
                'uuid': npu.get('uuid'),
                'model': npu.get('model_name', 'unknown'),
                'vendor': npu.get('vendor', 'unknown'),
                'hostname': npu.get('hostname', 'unknown'),
                'pci_bus_id': npu.get('pci_bus_id')
            }
            
            # Add metrics if requested and available
            if include_metrics:
                npu_metric = next((m for m in npu_metrics if m.get('npu_id') == npu_id), None)
                if npu_metric:
                    power = _safe_float(npu_metric.get('power_usage_watts')) or 0
                    total_npu_power += power
                    npu_entry['metrics'] = {
                        'power_watts': power,
                        'utilization_percent': _safe_float(npu_metric.get('npu_utilization_percent')),
                        'temperature_celsius': _safe_float(npu_metric.get('npu_temperature_celsius')),
                        'memory_used_mb': _safe_int(npu_metric.get('memory_used_mb'))
                    }
            
            npus.append(npu_entry)
        
        # Combine all accelerators
        all_accelerators = gpus + npus
        
        # Build summary
        summary = {
            'total_accelerators': len(all_accelerators),
            'total_gpus': len(gpus),
            'total_npus': len(npus),
        }
        
        if include_metrics:
            summary['total_power_watts'] = total_gpu_power + total_npu_power
            summary['gpu_power_watts'] = total_gpu_power
            summary['npu_power_watts'] = total_npu_power
        
        return {
            'summary': summary,
            'accelerators': all_accelerators
        }
        
    except Exception as e:
        raise Exception(f"Failed to get cluster accelerators for {cluster_name}: {e}")


async def get_cluster_nodes(cluster_name: str, role: Optional[str] = None, include_power: bool = False) -> Dict[str, Any]:
    """
    Get all nodes in a specific cluster.
    
    Args:
        cluster_name: Cluster name
        role: Node role filter
        include_power: Include power data
    
    Returns:
        Dict with nodes data and summary
    """
    from datetime import datetime
    
    try:
        # Get nodes list
        nodes_list = await get_node_list(cluster=cluster_name, role=role, status=None)
        
        # Build nodes array
        nodes = []
        total_power = 0
        total_gpus = 0
        
        for node_data in nodes_list:
            node_name = node_data.get('node_name', 'unknown')
            node_entry = {
                'name': node_name,
                'role': node_data.get('role', 'worker'),
                'status': node_data.get('status', 'unknown'),
                'labels': node_data.get('labels', {}),
                'capacity': {
                    'cpu': node_data.get('cpu_capacity'),
                    'memory_gb': node_data.get('memory_capacity_gb'),
                    'gpus': node_data.get('gpu_count', 0)
                },
                'allocatable': {
                    'cpu': node_data.get('cpu_allocatable'),
                    'memory_gb': node_data.get('memory_allocatable_gb'),
                    'gpus': node_data.get('gpu_count', 0)
                }
            }
            
            total_gpus += node_data.get('gpu_count', 0)
            
            # Add power if requested
            if include_power:
                try:
                    power_data = await get_node_power(node_name, period="1h")
                    current_power = power_data.get('current', {}).get('total_power_watts', 0)
                    node_entry['power_watts'] = current_power
                    total_power += current_power
                except Exception:
                    node_entry['power_watts'] = None
            
            nodes.append(node_entry)
        
        # Build summary
        summary = {
            'total_nodes': len(nodes),
            'total_gpus': total_gpus,
        }
        
        if include_power:
            summary['total_power_watts'] = total_power
            summary['avg_power_watts'] = total_power / len(nodes) if nodes else 0
        
        return {
            'summary': summary,
            'nodes': nodes
        }
        
    except Exception as e:
        raise Exception(f"Failed to get cluster nodes for {cluster_name}: {e}")


async def get_cluster_pods(
    cluster_name: str,
    namespace: Optional[str] = None,
    min_power: Optional[float] = None,
    include_power: bool = True
) -> Dict[str, Any]:
    """
    Get all pods in a specific cluster.
    
    Args:
        cluster_name: Cluster name
        namespace: Namespace filter
        min_power: Minimum power filter (watts)
        include_power: Include power data
    
    Returns:
        Dict with pods data and summary
    """
    from datetime import datetime
    from app.models.queries import PodQueryParams
    
    try:
        # Get pods list
        params = PodQueryParams(
            cluster=cluster_name,
            namespace=namespace,
            min_power=min_power
        )
        pods_response = await get_pod_list(params, include_metrics=False, include_power=include_power)
        pods_list = pods_response.get('pods', [])
        
        # Build pods array
        pods = []
        total_power = 0
        namespace_count = {}
        
        for pod in pods_list:
            pod_entry = {
                'name': pod.pod_name,
                'namespace': pod.namespace,
                'node': pod.node,
                'status': pod.status,
                'labels': pod.labels or {}
            }
            
            # Track namespace distribution
            ns = pod.namespace
            namespace_count[ns] = namespace_count.get(ns, 0) + 1
            
            # Add power if available
            if include_power and hasattr(pod, 'power_watts'):
                power = pod.power_watts or 0
                pod_entry['power_watts'] = power
                total_power += power
            
            pods.append(pod_entry)
        
        # Build summary
        summary = {
            'total_pods': len(pods),
            'namespaces': len(namespace_count),
            'namespace_distribution': namespace_count
        }
        
        if include_power:
            summary['total_power_watts'] = total_power
            summary['avg_power_watts'] = total_power / len(pods) if pods else 0
        
        return {
            'summary': summary,
            'pods': pods
        }
        
    except Exception as e:
        raise Exception(f"Failed to get cluster pods for {cluster_name}: {e}")
