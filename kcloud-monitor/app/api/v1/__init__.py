"""
API v1 Module - 7-Domain Architecture

This module exports all API v1 routers following the 7-domain structure:

1. Accelerators - GPU and NPU monitoring
2. Infrastructure - Nodes, Pods, Containers, VMs
3. Hardware - Physical hardware (IPMI)
4. Clusters - Multi-cluster management
5. Monitoring - Cross-domain monitoring (power, timeseries, streaming)
6. Export - Data export and reporting
7. System - Health, info, capabilities
"""

from app.api.v1 import (
    accelerators,
    infrastructure,
    hardware,
    clusters,
    monitoring,
    export,
    system
)

__all__ = [
    "accelerators",
    "infrastructure",
    "hardware",
    "clusters",
    "monitoring",
    "export",
    "system"
]
