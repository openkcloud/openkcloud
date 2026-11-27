# KCloud Monitor

> Unified Monitoring Platform for AI Semiconductors (v2)

KCloud Monitor is a FastAPI-based REST API service for unified monitoring of AI semiconductors (GPU, NPU) and cloud infrastructure (Kubernetes, OpenStack), covering resources and power attribution.

![GitHub license](https://img.shields.io/badge/license-Apache%202.0-blue.svg)
![Python version](https://img.shields.io/badge/python-3.12-green.svg)
![FastAPI](https://img.shields.io/badge/FastAPI-0.119%2B-teal.svg)
![API Version](https://img.shields.io/badge/API-v2-blue)

[![Korean](https://img.shields.io/badge/lang-한국어-red)](README.md)
[![English](https://img.shields.io/badge/lang-English-blue)](README_EN.md)

## Current Status

The v1 API (prototype) has been retired. **155 routes** are served under `/api/v2`.
Only `GET /openstack/vms/{vm_id}/gpu-passthrough` still returns a stub response (`status: not_implemented`).

- Metrics: Prometheus (`PROMETHEUS_URL`)
- Logs: Loki (`LOKI_URL`)
- Alert, metering, fault and recovery store: PostgreSQL (`DATABASE_URL`)
- VMs, hypervisors, projects: OpenStack API (`OPENSTACK_*`)

`PROMETHEUS_URL` is required. Areas whose other data sources are not configured return 503, or empty data with a `NOT_CONFIGURED` warning. See the [API guide](docs/API_GUIDE.md) (Korean) for per-area requirements.

## API Structure (`/api/v2`)

| Area | Path | Description | Count |
|------|------|-------------|-------|
| Clusters | `/clusters/{c}` | Cluster detail, resources, topology, power [P1] | 5 |
| Nodes & Hardware | `/clusters/{c}/nodes/{n}/*` | Node metrics, power [P2], IPMI sensors [P3] | 12 |
| Accelerators & Partitions | `.../accelerators/{id}/*` | Unified GPU/NPU, partitions (MIG/vGPU/slice) [P4·P5] | 11 (+2 aliases) |
| Storage (Ceph) | `/storage/ceph/*` | Rook-Ceph summary, capacity, OSDs, pools, PGs | 9 |
| OpenStack | `/openstack/*` | Projects, hypervisors, VMs, power attribution [P6] | 10 |
| Workloads | `/clusters/{c}/workloads/*`, `/clusters/{c}/namespaces` | Pods/Containers/Namespaces [P7] | 11 (+2 aliases) |
| Workloads (Global) | `/workloads/*` | Portal-facing global entry (`_links.canonical`) | 10 |
| Monitoring | `/monitoring/*` | Cross-cluster power [P8], utilization, temperature, throttling timeseries, SSE streams | 15 |
| Logs | `/logs/*` | Loki log search, stream, export | 10 |
| Export | `/export/*` | Power/metrics export (csv, json), operations report | 3 |
| Resource-Map | `/resource-map/pods/{pod}` | Trace Pod → node → VM → physical server | 1 |
| Alerts | `/alerts/*` | Alert policies, channels, active alerts, history | 15 |
| SLO | `/slo/*` | LLM serving SLO (TTFT, TPOT) | 3 |
| Metering | `/metering/*` | Usage, price policies, cost prediction, budgets | 21 |
| Faults / Recovery | `/faults/*`, `/recovery/*` | Fault analysis, follow-up policies, recovery info | 3 / 7 |
| System | `/system/*` | Health, version, self metrics (public) | 3 |
| Auth | `/auth/*` | JWT login and verification (dev-only until API Gateway) | 2 |

## Quick Start

```bash
python3.12 -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt
cp .env.example .env   # edit PROMETHEUS_URL, API_AUTH_PASSWORD, JWT_SECRET_KEY, ...
python run.py --port 8000
```

```bash
# Health check (public)
curl http://localhost:8000/api/v2/system/health

# Login → token (API_AUTH_USERNAME / API_AUTH_PASSWORD from .env)
curl -X POST http://localhost:8000/api/v2/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"<API_AUTH_PASSWORD>"}'

# Example query (power summary)
curl -H "Authorization: Bearer <TOKEN>" \
  http://localhost:8000/api/v2/monitoring/power/summary
```

- Swagger UI: http://localhost:8000/docs
- For Docker Compose, see the [quick start guide](docs/QUICK_START.md#docker) (Korean). `POSTGRES_*` values are required.

## Architecture

| Backend | Purpose | Status |
|---------|---------|--------|
| **Prometheus** (PromQL) | Metrics — DCGM, Furiosa/Rebellions, Kepler, node-exporter, IPMI, kube-state-metrics, Ceph, vLLM | ✅ in use |
| **Loki** (LogQL) | Log search, stream, log-based alerts | ✅ in use |
| **PostgreSQL** | Alert policies/history, metering, fault history, follow-up policies | ✅ in use |
| **OpenStack API** | VM, hypervisor, project metadata, VM power attribution | ✅ in use |
| **Mimir** | Central metrics store (per-cluster Alloy remote_write) | 📋 target — any PromQL-compatible backend can be set via `PROMETHEUS_URL` today |
| **Redis (+Streams)** | Cache + inter-service event bus | 📋 target |
| **libvirt** | GPU passthrough confirmation | 📋 not implemented |

## Roadmap

- ✅ v2 routing, authentication, path structure
- ✅ clusters, nodes, accelerators, storage (Ceph), openstack, workloads, monitoring, export
- ✅ Log search (Loki), alerts, LLM serving SLO, metering and cost prediction, fault analysis and recovery info
- 🚧 VM GPU passthrough (libvirt), resource-map expansion (accelerator/VM lineage, history)
- 📋 resource-map discovery collector, power attribution recording rules

## Documentation

| Document | Description |
|----------|-------------|
| [Quick Start](docs/QUICK_START.md) | Install, run, login, API calls, Docker |
| [API Guide](docs/API_GUIDE.md) | All 155 routes, auth, common parameters, response policy, SSE |
| [Architecture Overview](docs/ARCHITECTURE_OVERVIEW.md) | 6-layer resource model, data sources, power layers P1–P8, resource-map |
| [NPU Collection Setup](docs/PROMETHEUS_NPU_SETUP.md) | Furiosa exporter + hwmon collection |

The documents under `docs/` are written in Korean.

## Contributing

Please submit bug reports and feature requests through issues.

### Branch Strategy

- `main`: release branch (merged from `develop` only when bumping a release version)
- `develop`: integration branch (target of every PR)
- Work branches are created from `develop`, with a prefix for their purpose:
  - `feat/<name>`: new feature
  - `fix/<name>`: bug fix
  - `docs/<name>`: documentation

### Workflow

```bash
# 1. Fork the repository on GitHub, then clone your fork
git clone https://github.com/<your-id>/kcloud-monitor.git
cd kcloud-monitor
git remote add upstream https://github.com/openkcloud/kcloud-monitor.git

# 2. Create a work branch from the latest develop
git fetch upstream
git checkout -b feat/your-feature upstream/develop

# 3. Commit (see the commit message convention below)
git commit -m "✨ 추가(scope): summary of the change"

# 4. Push to your fork, then open a PR against develop of openkcloud/kcloud-monitor
git push origin feat/your-feature
```

### Commit Message Convention

Use the format `<emoji> <type>(<scope>): <summary>`.

| Emoji | Type | Use |
|-------|------|-----|
| ✨ | 추가 | Add a feature or test |
| 🔧 | 수정 | Change existing behavior or settings |
| 🔧 | docs | Documentation |

Examples: `✨ 추가(crud): get_npu_summary 정의`, `🔧 docs(readme): 문서 링크 정리`

## License

This project is distributed under the Apache License 2.0. See [LICENSE](LICENSE) for details.

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

---

**KCloud Monitor v2** | Unified Monitoring Platform for AI Semiconductors
