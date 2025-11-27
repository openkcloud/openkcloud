# API 사용 가이드

KCloud Monitor API의 핵심 엔드포인트 상세 가이드입니다.

## 목차
- [기본 정보](#기본-정보)
- [1. Accelerators API](#1-accelerators-api)
- [2. Infrastructure API](#2-infrastructure-api)
- [3. Hardware API](#3-hardware-api)
- [4. Monitoring API](#4-monitoring-api)
- [5. Export API](#5-export-api)
- [6. Clusters API](#6-clusters-api)
- [7. System API](#7-system-api)

## 기본 정보

### Base URL
```
Development: http://localhost:8001/api/v1
Production:  https://api.example.com/api/v1
```

### 인증
```bash
# Basic Authentication
curl -u username:password [URL]

# 또는 헤더로 직접
curl -H "Authorization: Basic <base64(username:password)>" [URL]
```

### 공통 응답 구조
```json
{
  "timestamp": "2024-01-01T12:00:00Z",
  "cluster": "default",
  "resource_type": "gpu",
  "data": { ... },
  "metadata": {
    "query_time_ms": 45,
    "cache_hit": true,
    "data_source": "dcgm"
  }
}
```

### 공통 쿼리 파라미터

| 파라미터 | 타입 | 설명 | 예시 |
|---------|------|------|------|
| `cluster` | string | 클러스터 필터 | `?cluster=prod` |
| `period` | string | 조회 기간 | `?period=1h` (1h, 1d, 1w, 1m) |
| `start_time` | ISO 8601 | 시작 시각 | `?start_time=2024-01-01T00:00:00Z` |
| `end_time` | ISO 8601 | 종료 시각 | `?end_time=2024-01-01T23:59:59Z` |
| `step` | string | 시계열 간격 | `?step=5m` (1m, 5m, 15m, 1h) |
| `limit` | integer | 결과 개수 제한 | `?limit=100` |
| `offset` | integer | 페이지네이션 | `?offset=100` |

---

## 1. Accelerators API

AI 가속기(GPU, NPU) 모니터링 API

### 1.1 GPU 목록

**엔드포인트**: `GET /api/v1/accelerators/gpus`

**설명**: 전체 GPU 목록 조회

**쿼리 파라미터**:
- `cluster`: 클러스터 필터
- `node`: 노드 필터
- `status`: 상태 필터 (active, idle, error)
- `include_metrics`: 메트릭 포함 여부 (기본: false)

**예시**:
```bash
# 기본 목록
curl -u admin:changeme http://localhost:8001/api/v1/accelerators/gpus

# 특정 노드의 GPU만
curl -u admin:changeme "http://localhost:8001/api/v1/accelerators/gpus?node=worker1"

# 메트릭 포함
curl -u admin:changeme "http://localhost:8001/api/v1/accelerators/gpus?include_metrics=true"
```

**응답**:
```json
{
  "timestamp": "2024-01-01T12:00:00Z",
  "cluster": "default",
  "resource_type": "gpu",
  "data": [
    {
      "gpu_id": "nvidia0",
      "uuid": "GPU-abc123...",
      "node": "worker1",
      "model": "NVIDIA A30",
      "driver_version": "535.183.01",
      "cuda_version": "12.2",
      "power_watts": 183.5,
      "temperature_celsius": 45.0,
      "utilization_percent": 85.3,
      "memory_used_mb": 18432,
      "memory_total_mb": 24576,
      "status": "active"
    },
    {
      "gpu_id": "nvidia1",
      "uuid": "GPU-def456...",
      "node": "worker2",
      "model": "NVIDIA A30",
      "power_watts": 281.2,
      "temperature_celsius": 52.0,
      "utilization_percent": 92.1
    }
  ],
  "metadata": {
    "total_count": 2,
    "query_time_ms": 45
  }
}
```

### 1.2 GPU 상세 정보

**엔드포인트**: `GET /api/v1/accelerators/gpus/{gpu_id}`

**설명**: 특정 GPU의 상세 정보

**예시**:
```bash
curl -u admin:changeme http://localhost:8001/api/v1/accelerators/gpus/nvidia0
```

**응답**:
```json
{
  "timestamp": "2024-01-01T12:00:00Z",
  "cluster": "default",
  "resource_type": "gpu",
  "data": {
    "gpu_id": "nvidia0",
    "uuid": "GPU-abc123...",
    "node": "worker1",
    "model": "NVIDIA A30",
    "architecture": "Ampere",
    "compute_capability": "8.0",
    "driver_version": "535.183.01",
    "cuda_version": "12.2",
    "vbios_version": "92.00.36.00.01",
    "pcie_generation": 4,
    "pcie_link_width": 16,
    "power_limit_watts": 230.0,
    "power_default_watts": 165.0,
    "memory_total_mb": 24576,
    "memory_bus_width": 384,
    "ecc_enabled": true
  }
}
```

### 1.3 GPU 메트릭

**엔드포인트**: `GET /api/v1/accelerators/gpus/{gpu_id}/metrics`

**설명**: GPU 실시간 성능 메트릭

**쿼리 파라미터**:
- `period`: 조회 기간 (기본: 1h)
- `step`: 샘플링 간격 (기본: 5m)

**예시**:
```bash
# 현재 메트릭
curl -u admin:changeme http://localhost:8001/api/v1/accelerators/gpus/nvidia0/metrics

# 1시간 시계열
curl -u admin:changeme "http://localhost:8001/api/v1/accelerators/gpus/nvidia0/metrics?period=1h&step=1m"
```

**응답**:
```json
{
  "timestamp": "2024-01-01T12:00:00Z",
  "cluster": "default",
  "resource_type": "gpu_metrics",
  "data": {
    "gpu_id": "nvidia0",
    "node": "worker1",
    "current": {
      "power_watts": 183.5,
      "temperature_celsius": 45.0,
      "gpu_utilization_percent": 85.3,
      "memory_utilization_percent": 75.0,
      "sm_clock_mhz": 1410,
      "memory_clock_mhz": 1215,
      "pcie_throughput_rx_mbps": 1250.5,
      "pcie_throughput_tx_mbps": 850.3,
      "fan_speed_rpm": 2400
    },
    "timeseries": [
      {
        "timestamp": "2024-01-01T11:00:00Z",
        "power_watts": 180.2,
        "temperature_celsius": 44.0,
        "utilization_percent": 83.5
      }
    ]
  }
}
```

### 1.4 GPU 전력

**엔드포인트**: `GET /api/v1/accelerators/gpus/{gpu_id}/power`

**설명**: GPU 전력 소비 상세 데이터

**예시**:
```bash
curl -u admin:changeme http://localhost:8001/api/v1/accelerators/gpus/nvidia0/power
```

**응답**:
```json
{
  "timestamp": "2024-01-01T12:00:00Z",
  "cluster": "default",
  "resource_type": "gpu_power",
  "data": {
    "gpu_id": "nvidia0",
    "current_power_watts": 183.5,
    "average_power_watts": 178.2,
    "max_power_watts": 195.0,
    "min_power_watts": 165.3,
    "power_limit_watts": 230.0,
    "power_usage_percent": 79.8,
    "energy_consumed_joules": 641340.0,
    "efficiency_gflops_per_watt": 42.5
  }
}
```

### 1.5 GPU 요약

**엔드포인트**: `GET /api/v1/accelerators/gpus/summary`

**설명**: 전체 GPU 요약 통계

**예시**:
```bash
curl -u admin:changeme http://localhost:8001/api/v1/accelerators/gpus/summary
```

**응답**:
```json
{
  "timestamp": "2024-01-01T12:00:00Z",
  "cluster": "default",
  "resource_type": "gpu_summary",
  "data": {
    "total_gpus": 2,
    "active_gpus": 2,
    "idle_gpus": 0,
    "total_power_watts": 464.7,
    "average_utilization_percent": 88.7,
    "average_temperature_celsius": 48.5,
    "total_memory_mb": 49152,
    "used_memory_mb": 36864,
    "memory_utilization_percent": 75.0,
    "nodes": ["worker1", "worker2"]
  }
}
```

### 1.6 NPU (Placeholder)

**엔드포인트**: `GET /api/v1/accelerators/npus`

**설명**: NPU 목록 (Furiosa AI, Rebellions 등)

**참고**: 현재는 Placeholder. 실제 NPU 연동 시 활성화됩니다.

---

## 2. Infrastructure API

쿠버네티스 인프라 모니터링 API

### 2.1 노드 목록

**엔드포인트**: `GET /api/v1/infrastructure/nodes`

**설명**: 전체 노드 목록

**쿼리 파라미터**:
- `cluster`: 클러스터 필터
- `role`: 노드 역할 (master, worker)
- `status`: 상태 필터 (ready, not_ready)

**예시**:
```bash
# 전체 노드
curl -u admin:changeme http://localhost:8001/api/v1/infrastructure/nodes

# Worker 노드만
curl -u admin:changeme "http://localhost:8001/api/v1/infrastructure/nodes?role=worker"
```

**응답**:
```json
{
  "timestamp": "2024-01-01T12:00:00Z",
  "cluster": "default",
  "resource_type": "node",
  "data": [
    {
      "node_name": "master1",
      "role": "master",
      "status": "ready",
      "cpu_cores": 16,
      "memory_gb": 64,
      "power_watts": 120.5,
      "cpu_utilization_percent": 45.2,
      "memory_utilization_percent": 62.3,
      "pods_count": 35,
      "gpus_count": 0
    },
    {
      "node_name": "worker1",
      "role": "worker",
      "status": "ready",
      "cpu_cores": 48,
      "memory_gb": 256,
      "power_watts": 285.3,
      "cpu_utilization_percent": 78.5,
      "memory_utilization_percent": 85.1,
      "pods_count": 120,
      "gpus_count": 2
    }
  ]
}
```

### 2.2 노드 상세

**엔드포인트**: `GET /api/v1/infrastructure/nodes/{node_name}`

**설명**: 특정 노드 상세 정보

**예시**:
```bash
curl -u admin:changeme http://localhost:8001/api/v1/infrastructure/nodes/worker1
```

**응답**:
```json
{
  "timestamp": "2024-01-01T12:00:00Z",
  "cluster": "default",
  "resource_type": "node",
  "data": {
    "node_name": "worker1",
    "role": "worker",
    "status": "ready",
    "kubernetes_version": "v1.28.0",
    "os_image": "Ubuntu 22.04.3 LTS",
    "kernel_version": "5.15.0-91-generic",
    "container_runtime": "containerd://1.7.2",
    "cpu_cores": 48,
    "cpu_architecture": "x86_64",
    "memory_total_gb": 256,
    "memory_available_gb": 38,
    "disk_total_gb": 1024,
    "disk_available_gb": 512,
    "power_watts": 285.3,
    "pods_count": 120,
    "pods_capacity": 250,
    "gpus": ["nvidia0", "nvidia1"]
  }
}
```

### 2.3 Pod 목록

**엔드포인트**: `GET /api/v1/infrastructure/pods`

**설명**: 전체 Pod 목록

**쿼리 파라미터**:
- `cluster`: 클러스터 필터
- `namespace`: 네임스페이스 필터
- `node`: 노드 필터
- `status`: 상태 필터 (running, pending, failed)

**예시**:
```bash
# 전체 Pod
curl -u admin:changeme http://localhost:8001/api/v1/infrastructure/pods

# 특정 네임스페이스
curl -u admin:changeme "http://localhost:8001/api/v1/infrastructure/pods?namespace=default"

# GPU 사용 Pod만
curl -u admin:changeme "http://localhost:8001/api/v1/infrastructure/pods?has_gpu=true"
```

**응답**:
```json
{
  "timestamp": "2024-01-01T12:00:00Z",
  "cluster": "default",
  "resource_type": "pod",
  "data": [
    {
      "pod_name": "training-job-1",
      "namespace": "ml-workloads",
      "node": "worker1",
      "status": "running",
      "power_watts": 195.2,
      "cpu_utilization_percent": 85.3,
      "memory_used_mb": 8192,
      "gpu_count": 1,
      "gpu_ids": ["nvidia0"],
      "created_at": "2024-01-01T10:00:00Z"
    }
  ]
}
```

### 2.4 Pod 상세

**엔드포인트**: `GET /api/v1/infrastructure/pods/{namespace}/{pod_name}`

**설명**: 특정 Pod 상세 정보

**예시**:
```bash
curl -u admin:changeme http://localhost:8001/api/v1/infrastructure/pods/default/training-job-1
```

**응답**:
```json
{
  "timestamp": "2024-01-01T12:00:00Z",
  "cluster": "default",
  "resource_type": "pod",
  "data": {
    "pod_name": "training-job-1",
    "namespace": "ml-workloads",
    "node": "worker1",
    "status": "running",
    "ip": "10.244.1.15",
    "power_watts": 195.2,
    "cpu_request": "16",
    "cpu_limit": "32",
    "cpu_utilization_percent": 85.3,
    "memory_request_mb": 16384,
    "memory_limit_mb": 32768,
    "memory_used_mb": 20480,
    "gpu_count": 1,
    "gpu_ids": ["nvidia0"],
    "containers": [
      {
        "name": "pytorch-trainer",
        "image": "pytorch/pytorch:2.1.0-cuda12.1",
        "status": "running",
        "restart_count": 0
      }
    ],
    "labels": {
      "app": "training",
      "workload": "ml"
    }
  }
}
```

---

## 3. Hardware API

물리 하드웨어 센서(IPMI) 모니터링 API

### 3.1 전체 IPMI 센서

**엔드포인트**: `GET /api/v1/hardware/ipmi/sensors`

**설명**: 전체 노드의 IPMI 센서 데이터

**예시**:
```bash
curl -u admin:changeme http://localhost:8001/api/v1/hardware/ipmi/sensors
```

**응답**:
```json
{
  "timestamp": "2024-01-01T12:00:00Z",
  "resource_type": "ipmi_sensors",
  "data": [
    {
      "node": "worker1",
      "sensors": {
        "power": [
          {"name": "PSU1_Power", "value": 145.0, "unit": "watts"},
          {"name": "PSU2_Power", "value": 140.3, "unit": "watts"}
        ],
        "temperature": [
          {"name": "CPU1_Temp", "value": 55.0, "unit": "celsius"},
          {"name": "CPU2_Temp", "value": 58.0, "unit": "celsius"}
        ],
        "fans": [
          {"name": "Fan1", "value": 3200, "unit": "rpm"},
          {"name": "Fan2", "value": 3150, "unit": "rpm"}
        ],
        "voltage": [
          {"name": "12V", "value": 12.05, "unit": "volts"},
          {"name": "5V", "value": 5.02, "unit": "volts"}
        ]
      }
    }
  ]
}
```

### 3.2 노드별 IPMI 센서

**엔드포인트**: `GET /api/v1/hardware/ipmi/sensors/{node_name}`

**설명**: 특정 노드의 IPMI 센서

**예시**:
```bash
curl -u admin:changeme http://localhost:8001/api/v1/hardware/ipmi/sensors/worker1
```

### 3.3 전력 센서

**엔드포인트**: `GET /api/v1/hardware/ipmi/power`

**설명**: 전체 노드의 전력 센서만

**예시**:
```bash
curl -u admin:changeme http://localhost:8001/api/v1/hardware/ipmi/power
```

**응답**:
```json
{
  "timestamp": "2024-01-01T12:00:00Z",
  "resource_type": "ipmi_power",
  "data": [
    {
      "node": "worker1",
      "total_power_watts": 285.3,
      "psu1_power_watts": 145.0,
      "psu2_power_watts": 140.3,
      "psu_redundancy": "active"
    },
    {
      "node": "worker2",
      "total_power_watts": 298.7,
      "psu1_power_watts": 152.3,
      "psu2_power_watts": 146.4
    }
  ],
  "summary": {
    "total_power_watts": 584.0,
    "node_count": 2
  }
}
```

### 3.4 온도 센서

**엔드포인트**: `GET /api/v1/hardware/ipmi/temperature`

**설명**: 전체 노드의 온도 센서

**예시**:
```bash
curl -u admin:changeme http://localhost:8001/api/v1/hardware/ipmi/temperature
```

---

## 4. Monitoring API

통합 모니터링 및 실시간 스트리밍 API

### 4.1 통합 전력 모니터링

**엔드포인트**: `GET /api/v1/monitoring/power`

**설명**: 전체 시스템 통합 전력 소비

**예시**:
```bash
curl -u admin:changeme http://localhost:8001/api/v1/monitoring/power
```

**응답**:
```json
{
  "timestamp": "2024-01-01T12:00:00Z",
  "resource_type": "integrated_power",
  "data": {
    "total_power_watts": 1534.7,
    "gpu_power_watts": 464.7,
    "cpu_power_watts": 685.3,
    "memory_power_watts": 284.5,
    "other_power_watts": 100.2,
    "breakdown": {
      "accelerators": {
        "gpus": 464.7,
        "npus": 0.0
      },
      "infrastructure": {
        "nodes": 685.3,
        "pods": 384.5
      },
      "hardware": {
        "ipmi_total": 285.3
      }
    },
    "efficiency": {
      "pue": 1.15,
      "gpu_utilization_percent": 88.7,
      "cpu_utilization_percent": 65.2
    }
  }
}
```

### 4.2 전력 분해 분석

**엔드포인트**: `GET /api/v1/monitoring/power/breakdown`

**설명**: 전력 소비 분해 분석

**쿼리 파라미터**:
- `breakdown_by`: 분해 기준 (cluster, node, namespace, workload)

**예시**:
```bash
# 클러스터별 분해
curl -u admin:changeme "http://localhost:8001/api/v1/monitoring/power/breakdown?breakdown_by=cluster"

# 노드별 분해
curl -u admin:changeme "http://localhost:8001/api/v1/monitoring/power/breakdown?breakdown_by=node"

# 네임스페이스별 분해
curl -u admin:changeme "http://localhost:8001/api/v1/monitoring/power/breakdown?breakdown_by=namespace"
```

**응답 (노드별 예시)**:
```json
{
  "timestamp": "2024-01-01T12:00:00Z",
  "resource_type": "power_breakdown",
  "breakdown_by": "node",
  "data": [
    {
      "node": "master1",
      "power_watts": 120.5,
      "percentage": 7.9,
      "components": {
        "cpu": 85.3,
        "memory": 25.2,
        "other": 10.0
      }
    },
    {
      "node": "worker1",
      "power_watts": 285.3,
      "percentage": 18.6,
      "components": {
        "gpu": 183.5,
        "cpu": 75.8,
        "memory": 26.0
      }
    },
    {
      "node": "worker2",
      "power_watts": 298.7,
      "percentage": 19.5,
      "components": {
        "gpu": 281.2,
        "cpu": 12.5,
        "memory": 5.0
      }
    }
  ],
  "summary": {
    "total_power_watts": 1534.7,
    "node_count": 3
  }
}
```

### 4.3 전력 시계열

**엔드포인트**: `GET /api/v1/monitoring/timeseries/power`

**설명**: 전력 소비 시계열 데이터

**쿼리 파라미터**:
- `period`: 조회 기간 (1h, 1d, 1w, 1m)
- `step`: 샘플링 간격 (1m, 5m, 15m, 1h)

**예시**:
```bash
# 1시간, 1분 간격
curl -u admin:changeme "http://localhost:8001/api/v1/monitoring/timeseries/power?period=1h&step=1m"

# 1일, 5분 간격
curl -u admin:changeme "http://localhost:8001/api/v1/monitoring/timeseries/power?period=1d&step=5m"
```

**응답**:
```json
{
  "timestamp": "2024-01-01T12:00:00Z",
  "resource_type": "power_timeseries",
  "query": {
    "period": "1h",
    "step": "1m",
    "start_time": "2024-01-01T11:00:00Z",
    "end_time": "2024-01-01T12:00:00Z"
  },
  "data": [
    {
      "timestamp": "2024-01-01T11:00:00Z",
      "total_power_watts": 1520.3,
      "gpu_power_watts": 450.2,
      "cpu_power_watts": 680.1,
      "memory_power_watts": 280.0,
      "other_power_watts": 110.0
    },
    {
      "timestamp": "2024-01-01T11:01:00Z",
      "total_power_watts": 1525.7,
      "gpu_power_watts": 455.3,
      "cpu_power_watts": 682.4
    }
  ],
  "summary": {
    "total_samples": 60,
    "average_power_watts": 1532.5,
    "max_power_watts": 1580.2,
    "min_power_watts": 1485.3
  }
}
```

### 4.4 WebSocket 스트리밍

**엔드포인트**: `WS /api/v1/monitoring/stream/power`

**설명**: 실시간 전력 데이터 WebSocket 스트림

**쿼리 파라미터**:
- `interval`: 업데이트 간격 (초, 기본: 5)

**예시 (JavaScript)**:
```javascript
const ws = new WebSocket('ws://localhost:8001/api/v1/monitoring/stream/power?interval=5');

ws.onopen = () => {
  console.log('WebSocket connected');
};

ws.onmessage = (event) => {
  const data = JSON.parse(event.data);
  console.log('Power Update:', data);
  /*
  {
    "timestamp": "2024-01-01T12:00:05Z",
    "total_power_watts": 1534.7,
    "gpu_power_watts": 464.7,
    "cpu_power_watts": 685.3,
    "breakdown": { ... }
  }
  */
};

ws.onerror = (error) => {
  console.error('WebSocket error:', error);
};

ws.onclose = () => {
  console.log('WebSocket disconnected');
};
```

### 4.5 SSE 이벤트 스트림

**엔드포인트**: `GET /api/v1/monitoring/events/power`

**설명**: Server-Sent Events 전력 이벤트 스트림

**예시 (JavaScript)**:
```javascript
const eventSource = new EventSource('http://localhost:8001/api/v1/monitoring/events/power');

// 전력 업데이트 이벤트
eventSource.addEventListener('power_update', (event) => {
  const data = JSON.parse(event.data);
  console.log('Power:', data.total_power_watts, 'W');
});

// 임계값 초과 이벤트
eventSource.addEventListener('threshold_exceeded', (event) => {
  const alert = JSON.parse(event.data);
  console.warn('Alert:', alert.message);
  /*
  {
    "type": "threshold_exceeded",
    "message": "Total power exceeded 1500W",
    "current_value": 1534.7,
    "threshold": 1500.0,
    "severity": "warning"
  }
  */
});

eventSource.onerror = (error) => {
  console.error('SSE error:', error);
  eventSource.close();
};
```

---

## 5. Export API

데이터 내보내기 API

### 5.1 전력 데이터 내보내기

**엔드포인트**: `GET /api/v1/export/power`

**설명**: 전력 데이터를 다양한 포맷으로 내보내기

**쿼리 파라미터**:
- `format`: 출력 포맷 (json, csv, excel, parquet, pdf)
- `period`: 조회 기간
- `breakdown_by`: 분해 기준 (옵션)

**예시**:
```bash
# JSON 내보내기 (기본)
curl -u admin:changeme "http://localhost:8001/api/v1/export/power?format=json&period=1h" > power.json

# CSV 내보내기
curl -u admin:changeme "http://localhost:8001/api/v1/export/power?format=csv&period=1h" > power.csv

# Excel 내보내기
curl -u admin:changeme "http://localhost:8001/api/v1/export/power?format=excel&period=1h" > power.xlsx

# Parquet 내보내기 (빅데이터 분석용)
curl -u admin:changeme "http://localhost:8001/api/v1/export/power?format=parquet&period=1h" > power.parquet

# PDF 리포트
curl -u admin:changeme "http://localhost:8001/api/v1/export/power?format=pdf&period=1d" > power_report.pdf
```

### 5.2 메트릭 데이터 내보내기

**엔드포인트**: `GET /api/v1/export/metrics`

**설명**: 성능 메트릭 데이터 내보내기

**쿼리 파라미터**:
- `format`: 출력 포맷
- `resource_type`: 리소스 타입 (gpu, node, pod)
- `period`: 조회 기간

**예시**:
```bash
# GPU 메트릭 CSV
curl -u admin:changeme "http://localhost:8001/api/v1/export/metrics?format=csv&resource_type=gpu&period=1h" > gpu_metrics.csv

# 노드 메트릭 Excel
curl -u admin:changeme "http://localhost:8001/api/v1/export/metrics?format=excel&resource_type=node&period=1d" > node_metrics.xlsx
```

### 5.3 리포트 생성

**엔드포인트**: `GET /api/v1/export/report`

**설명**: 종합 리포트 생성

**쿼리 파라미터**:
- `template`: 리포트 템플릿 (daily, weekly, monthly)
- `format`: 출력 포맷 (pdf, excel)

**예시**:
```bash
# 일일 리포트
curl -u admin:changeme "http://localhost:8001/api/v1/export/report?template=daily&format=pdf" > daily_report.pdf

# 주간 리포트
curl -u admin:changeme "http://localhost:8001/api/v1/export/report?template=weekly&format=excel" > weekly_report.xlsx

# 월간 리포트
curl -u admin:changeme "http://localhost:8001/api/v1/export/report?template=monthly&format=pdf" > monthly_report.pdf
```

---

## 6. Clusters API

멀티 클러스터 관리 API

### 6.1 클러스터 목록

**엔드포인트**: `GET /api/v1/clusters`

**설명**: 전체 클러스터 목록

**예시**:
```bash
curl -u admin:changeme http://localhost:8001/api/v1/clusters
```

**응답**:
```json
{
  "timestamp": "2024-01-01T12:00:00Z",
  "resource_type": "cluster",
  "data": [
    {
      "name": "prod",
      "url": "http://prom-prod:9090",
      "region": "us-east-1",
      "status": "connected",
      "nodes_count": 15,
      "gpus_count": 30,
      "total_power_watts": 3500.5
    },
    {
      "name": "dev",
      "url": "http://prom-dev:9090",
      "region": "us-west-1",
      "status": "connected",
      "nodes_count": 5,
      "gpus_count": 8
    }
  ]
}
```

### 6.2 클러스터 요약

**엔드포인트**: `GET /api/v1/clusters/{cluster_name}/summary`

**설명**: 특정 클러스터 요약

**예시**:
```bash
curl -u admin:changeme http://localhost:8001/api/v1/clusters/prod/summary
```

**응답**:
```json
{
  "timestamp": "2024-01-01T12:00:00Z",
  "cluster": "prod",
  "resource_type": "cluster_summary",
  "data": {
    "name": "prod",
    "region": "us-east-1",
    "status": "healthy",
    "nodes": {
      "total": 15,
      "ready": 15,
      "masters": 3,
      "workers": 12
    },
    "gpus": {
      "total": 30,
      "active": 28,
      "models": ["NVIDIA A30", "NVIDIA A100"]
    },
    "pods": {
      "total": 450,
      "running": 445,
      "pending": 5
    },
    "power": {
      "total_watts": 3500.5,
      "gpu_watts": 2100.3,
      "cpu_watts": 1200.2,
      "efficiency_pue": 1.12
    }
  }
}
```

### 6.3 클러스터 전력

**엔드포인트**: `GET /api/v1/clusters/{cluster_name}/power`

**설명**: 클러스터별 전력 소비

**예시**:
```bash
curl -u admin:changeme http://localhost:8001/api/v1/clusters/prod/power
```

---

## 7. System API

시스템 정보 및 메트릭 API

### 7.1 헬스체크

**엔드포인트**: `GET /api/v1/system/health`

**설명**: API 서버 및 Prometheus 연결 상태 확인

**인증**: 불필요

**예시**:
```bash
curl http://localhost:8001/api/v1/system/health
```

**응답**:
```json
{
  "status": "healthy",
  "timestamp": "2024-01-01T12:00:00Z",
  "version": "0.1.0",
  "prometheus": {
    "connected": true,
    "url": "http://prometheus:9090",
    "response_time_ms": 12
  },
  "cache": {
    "enabled": true,
    "hit_rate_percent": 78.5
  },
  "uptime_seconds": 86400
}
```

### 7.2 버전 정보

**엔드포인트**: `GET /api/v1/system/version`

**설명**: API 버전 및 기능 정보

**예시**:
```bash
curl -u admin:changeme http://localhost:8001/api/v1/system/version
```

**응답**:
```json
{
  "timestamp": "2024-01-01T12:00:00Z",
  "data": {
    "api_version": "0.1.0",
    "api_name": "KCloud Monitor",
    "build_date": "2024-01-01",
    "python_version": "3.12.0",
    "fastapi_version": "0.104.1"
  }
}
```

### 7.3 지원 기능

**엔드포인트**: `GET /api/v1/system/capabilities`

**설명**: API가 지원하는 기능 목록

**예시**:
```bash
curl -u admin:changeme http://localhost:8001/api/v1/system/capabilities
```

**응답**:
```json
{
  "timestamp": "2024-01-01T12:00:00Z",
  "data": {
    "accelerators": ["nvidia_gpu"],
    "infrastructure": ["kubernetes_nodes", "pods", "containers"],
    "hardware": ["ipmi"],
    "data_sources": ["dcgm", "kepler", "ipmi"],
    "export_formats": ["json", "csv", "excel", "parquet", "pdf"],
    "streaming": ["websocket", "sse"],
    "multi_cluster": true,
    "real_time_monitoring": true
  }
}
```

### 7.4 API 메트릭 (Prometheus)

**엔드포인트**: `GET /api/v1/system/metrics`

**설명**: API 서버 메트릭 (Prometheus 형식)

**인증**: 필요

**예시**:
```bash
curl -u admin:changeme http://localhost:8001/api/v1/system/metrics
```

**응답**:
```
# HELP api_requests_total Total API requests
# TYPE api_requests_total counter
api_requests_total{method="GET",endpoint="/api/v1/accelerators/gpus",status="200"} 1234

# HELP api_request_duration_seconds API request duration
# TYPE api_request_duration_seconds histogram
api_request_duration_seconds_bucket{le="0.1"} 850
api_request_duration_seconds_bucket{le="0.5"} 1180
api_request_duration_seconds_sum 450.5
api_request_duration_seconds_count 1234

# HELP cache_hits_total Cache hits
# TYPE cache_hits_total counter
cache_hits_total 968

# HELP cache_misses_total Cache misses
# TYPE cache_misses_total counter
cache_misses_total 266

# HELP prometheus_query_duration_seconds Prometheus query duration
# TYPE prometheus_query_duration_seconds histogram
prometheus_query_duration_seconds_sum 125.3
prometheus_query_duration_seconds_count 1500
```

---

## 오류 처리

### 오류 응답 구조
```json
{
  "error": {
    "code": "ERROR_CODE",
    "message": "Human-readable error message",
    "details": { ... }
  },
  "timestamp": "2024-01-01T12:00:00Z"
}
```

### 일반 오류 코드

| 코드 | 상태 | 설명 |
|------|------|------|
| `UNAUTHORIZED` | 401 | 인증 실패 |
| `FORBIDDEN` | 403 | 권한 없음 |
| `NOT_FOUND` | 404 | 리소스 없음 |
| `INVALID_PARAMETER` | 400 | 잘못된 파라미터 |
| `PROMETHEUS_ERROR` | 502 | Prometheus 연결 실패 |
| `INTERNAL_ERROR` | 500 | 서버 내부 오류 |

---

## 추가 리소스

- 빠른 시작: [QUICK_START.md](./QUICK_START.md)
- 아키텍처: [ARCHITECTURE_OVERVIEW.md](./ARCHITECTURE_OVERVIEW.md)
- Swagger UI: http://localhost:8001/docs
- ReDoc: http://localhost:8001/redoc
