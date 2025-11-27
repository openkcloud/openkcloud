# KCloud Monitor

> Unified Monitoring Platform for AI Semiconductors

KCloud Monitor is a FastAPI-based REST API service for unified monitoring of AI semiconductors (GPU, NPU) and cloud infrastructure power consumption and performance. It collects various metrics through Prometheus including DCGM, Kepler, and IPMI, providing real-time streaming and data export capabilities.

![GitHub license](https://img.shields.io/badge/license-Apache%202.0-blue.svg)
![Python version](https://img.shields.io/badge/python-3.12-green.svg)
![FastAPI](https://img.shields.io/badge/FastAPI-0.119%2B-teal.svg)
![API Version](https://img.shields.io/badge/API-v0.1.0-blue)

[![Korean](https://img.shields.io/badge/lang-한국어-red)](README.md)
[![English](https://img.shields.io/badge/lang-English-blue)](README_EN.md)

## Overview

KCloud Monitor is an enterprise-grade monitoring platform designed for AI semiconductor infrastructure, providing comprehensive power consumption analysis and performance monitoring across GPU/NPU, Kubernetes resources, and physical hardware. Built on a domain-based microservice architecture, it delivers high performance, scalability, and real-time insights through WebSocket streaming and multi-format data export.

## Key Features

### 🚀 Core Capabilities

- **AI Semiconductor Monitoring**: Performance and power monitoring for GPU (NVIDIA DCGM) and NPU (Furiosa/Rebellions)
- **Infrastructure Monitoring**: Kubernetes Nodes, Pods, Containers resource and power breakdown (Kepler)
- **Hardware Sensors**: IPMI-based physical server sensor monitoring (power, temperature, fan, voltage)
- **Multi-Cluster Support**: Unified management and monitoring across multiple Kubernetes clusters
- **Unified Power Analysis**: Power breakdown analysis by cluster/node/namespace/resource type and PUE calculation
- **Real-time Streaming**: Real-time metric push via WebSocket and SSE
- **Data Export**: Support for JSON, CSV, Parquet, Excel, PDF formats
- **API Metrics**: Prometheus-format API server metrics exposure

### 📊 Domain Architecture

1. **Accelerators** - AI semiconductors (GPU, NPU, TPU)
2. **Infrastructure** - Infrastructure resources (Nodes, Pods, Containers, VMs)
3. **Hardware** - Physical hardware sensors (IPMI)
4. **Clusters** - Multi-cluster management
5. **Monitoring** - Unified monitoring and streaming
6. **Export** - Data export and reporting
7. **System** - System information and health checks

## Quick Start

### 1. Setup

```bash
# 1. Clone repository
git clone https://github.com/openkcloud/kcloud-monitor.git
cd kcloud-monitor

# 2. Create and activate virtual environment
python3.12 -m venv .venv
source .venv/bin/activate  # Linux/Mac
# .venv\Scripts\activate   # Windows

# 3. Install dependencies
pip install -r requirements.txt

# 4. Configure environment variables
cp .env.example .env
# Edit .env file to set Prometheus URL and authentication credentials
```

### 2. Run with Docker (Recommended)

```bash
# 1. Configure environment variables
cp .env.example .env
# Edit .env file to set Prometheus URL and authentication credentials

# 2. Start with Docker Compose
docker-compose up -d

# 3. View logs
docker-compose logs -f api

# 4. Check service health
curl http://localhost:8000/api/v1/system/health

# 5. Access API documentation
# http://localhost:8000/docs (Swagger UI)
# http://localhost:8000/redoc (ReDoc)

# 6. Stop service
docker-compose down
```

### 3. Run Local Development Server

```bash
# Run development server
uvicorn app.main:app --host 127.0.0.1 --port 8001 --reload

# Access API documentation
# http://127.0.0.1:8001/docs (Swagger UI)
# http://127.0.0.1:8001/redoc (ReDoc)
```

### 4. Basic Usage Examples

```bash
# Health check
curl http://127.0.0.1:8001/api/v1/system/health

# GPU monitoring
curl -u admin:changeme http://127.0.0.1:8001/api/v1/accelerators/gpus

# Unified power monitoring
curl -u admin:changeme http://127.0.0.1:8001/api/v1/monitoring/power

# Data export (CSV)
curl -u admin:changeme "http://127.0.0.1:8001/api/v1/export/power?period=1h&format=csv" > power.csv
```

## API Endpoints

| Domain | Main Endpoints | Data Source | Status |
|--------|----------------|-------------|--------|
| **Accelerators** | `/api/v1/accelerators/gpus` | DCGM | ✅ |
| | `/api/v1/accelerators/npus` | NPU Exporter | ⚠️ |
| **Infrastructure** | `/api/v1/infrastructure/nodes` | Kepler | ✅ |
| | `/api/v1/infrastructure/pods` | Kepler | ✅ |
| **Hardware** | `/api/v1/hardware/ipmi/sensors` | IPMI Exporter | ⚠️ |
| **Clusters** | `/api/v1/clusters` | Prometheus | ✅ |
| **Monitoring** | `/api/v1/monitoring/power` | Kepler + DCGM | ✅ |
| | `/api/v1/monitoring/stream/power` (WS) | Prometheus | ✅ |
| **Export** | `/api/v1/export/power?format=csv` | - | ✅ |
| **System** | `/api/v1/system/health` | - | ✅ |

> 📖 **Detailed API Documentation**: [spec/API_SPECIFICATION.md](spec/API_SPECIFICATION.md)
> 📋 **Endpoint Mapping**: [docs/API_ENDPOINT_MAPPING.md](tests/test_docs/API_ENDPOINT_MAPPING.md)

## System Architecture

```
┌─────────────────────────────────────────┐
│         Client Applications              │
│   (Dashboard, CLI, Export Tools)         │
└────────────────┬────────────────────────┘
                 │ REST API / WebSocket
┌────────────────┴────────────────────────┐
│         FastAPI Application              │
│  Accelerators│Infrastructure│Hardware│  │
│  Clusters│Monitoring│Export│System      │
└────────────────┬────────────────────────┘
                 │ Prometheus Query API
┌────────────────┴────────────────────────┐
│       Prometheus (Multi-Cluster)         │
└────────────────┬────────────────────────┘
                 │ Metrics Collection
┌────────────────┴────────────────────────┐
│  DCGM│Kepler│IPMI│NPU│OpenStack        │
└────────────────┴────────────────────────┘
                 │ Hardware Metrics
┌────────────────┴────────────────────────┐
│  GPU│NPU│Nodes│Pods│Containers│VMs     │
└─────────────────────────────────────────┘
```

### Data Sources

| Data Source | Purpose | Status |
|------------|---------|--------|
| **DCGM Exporter** | NVIDIA GPU monitoring | ✅ |
| **Kepler** | Node/Pod power breakdown | ✅ |
| **IPMI Exporter** | Physical server sensors | ⚠️ Setup Required |
| **NPU Exporter** | Furiosa/Rebellions NPU | ⚠️ Setup Required |
| **OpenStack** | VM monitoring | ❌ Not Implemented |

## Roadmap

### ✅ Completed (v0.1.0)

- Domain-based architecture design and implementation
- GPU monitoring (DCGM)
- Infrastructure monitoring (Kepler - Nodes/Pods/Containers)
- Multi-cluster support
- Unified power monitoring and breakdown analysis
- Real-time streaming (WebSocket, SSE)
- Multi-format data export (JSON, CSV, Parquet, Excel, PDF)
- API metrics exposure (Prometheus)

### 🚧 In Progress

- IPMI Exporter setup
- NPU Exporter setup
- Test code completion (113 tests, 66 passed)

### 📋 Future Plans

- NPU monitoring (Furiosa, Rebellions)
- OpenStack VM monitoring
- Kubernetes deployment automation
- Grafana dashboards
- Alert Manager integration

## Configuration

Key environment variables:

```bash
# Prometheus connection
PROMETHEUS_URL=http://prometheus-server:9090

# Authentication
API_AUTH_USERNAME=admin
API_AUTH_PASSWORD=changeme

# Multi-cluster (optional)
# PROMETHEUS_CLUSTERS='[{"name":"cluster1","url":"http://prom-cluster1:9090"}]'
```

> 📖 **Detailed Configuration Guide**: [spec/PROMETHEUS_SETUP.md](spec/PROMETHEUS_SETUP.md)

## Documentation

### 📚 Project Documentation

| Document | Description |
|----------|-------------|
| [API Specification](spec/API_SPECIFICATION.md) | Complete API endpoint documentation |
| [Architecture Design](spec/ARCHITECTURE.md) | Domain-based architecture philosophy |
| [Data Models](spec/DATA_MODELS.md) | Pydantic data model specifications |
| [Prometheus Setup](spec/PROMETHEUS_SETUP.md) | Exporter setup and integration guide |
| [Developer Guide](CLAUDE.md) | Detailed guide for developers |
| [Endpoint Mapping](tests/test_docs/API_ENDPOINT_MAPPING.md) | Implementation status by endpoint |

### 🔧 Tech Stack

- **Framework**: FastAPI 0.119+, Python 3.12
- **Data Validation**: Pydantic 2.12+
- **Metrics**: Prometheus Client, DCGM, Kepler
- **Export**: pyarrow, openpyxl, reportlab
- **Server**: Uvicorn (ASGI)

## Contributing

Bug reports and feature suggestions are welcome through issues.

### Development Setup

```bash
# 1. Fork and clone
git clone https://github.com/yourusername/kcloud-monitor.git

# 2. Create branch
git checkout -b feature/your-feature

# 3. Commit changes
git commit -m "Add your feature"

# 4. Push and create PR
git push origin feature/your-feature
```

## License

This project is distributed under the Apache License 2.0. See [LICENSE](LICENSE) file for details.

```
Copyright 2025 OpenKCloud Community

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
```

## Contact

- **Development**: OpenKCloud Community
- **Issues**: [GitHub Issues](https://github.com/openkcloud/kcloud-monitor/issues)
- **Documentation**: [Project Wiki](https://github.com/openkcloud/kcloud-monitor/wiki)

## Acknowledgments

- All contributors
- OpenKCloud community
- Prometheus, Kepler, and DCGM projects

---

**KCloud Monitor v0.1.0** | Unified Monitoring Platform for AI Semiconductors

Project Link: [https://github.com/openkcloud/kcloud-monitor](https://github.com/openkcloud/kcloud-monitor)
