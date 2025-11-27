# Quick Start Guide

KCloud Monitor (AI Accelerator & Infrastructure Monitoring API)의 빠른 시작 가이드입니다.

## 개요

KCloud Monitor는 AI 가속기(GPU, NPU)와 쿠버네티스 인프라의 전력 및 성능을 모니터링하는 FastAPI 기반 REST API 서버입니다.

**주요 기능**:
- GPU/NPU 전력 및 성능 모니터링
- 노드/Pod 레벨 인프라 모니터링
- IPMI 하드웨어 센서 데이터
- 멀티 클러스터 지원
- 실시간 데이터 스트리밍 (WebSocket/SSE)
- 다양한 포맷 내보내기 (JSON, CSV, Excel, PDF)

## 설치 및 실행

### 1. 환경 설정

```bash
# 가상환경 활성화
source .venv/bin/activate  # Linux/Mac
.venv\Scripts\activate     # Windows

# 환경 변수 설정 (.env 파일)
cat > .env << EOF
PROMETHEUS_URL=http://prometheus:9090
PROMETHEUS_TIMEOUT=30
API_AUTH_USERNAME=admin
API_AUTH_PASSWORD=changeme
EOF
```

### 2. 서버 실행

```bash
# 개발 모드
uvicorn app.main:app --host 0.0.0.0 --port 8001 --reload

# 프로덕션 모드
uvicorn app.main:app --host 0.0.0.0 --port 8001 --workers 4
```

### 3. 헬스체크

```bash
# 서버 상태 확인 (인증 불필요)
curl http://localhost:8001/api/v1/system/health

# 예상 응답
{
  "status": "healthy",
  "timestamp": "2024-01-01T12:00:00Z",
  "version": "0.1.0",
  "prometheus": {
    "connected": true,
    "url": "http://prometheus:9090"
  }
}
```

## 기본 사용법

### 인증

모든 API 요청은 Basic Authentication이 필요합니다 (system 엔드포인트 제외).

```bash
# 사용자 인증 (-u username:password)
curl -u admin:changeme http://localhost:8001/api/v1/accelerators/gpus
```

### GPU 모니터링

```bash
# 1. 전체 GPU 목록
curl -u admin:changeme http://localhost:8001/api/v1/accelerators/gpus

# 2. 특정 GPU 상세 정보
curl -u admin:changeme http://localhost:8001/api/v1/accelerators/gpus/nvidia0

# 3. GPU 메트릭 (사용률, 전력, 온도)
curl -u admin:changeme http://localhost:8001/api/v1/accelerators/gpus/nvidia0/metrics

# 4. GPU 요약 통계
curl -u admin:changeme http://localhost:8001/api/v1/accelerators/gpus/summary
```

**응답 예시**:
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
      "power_watts": 183.5,
      "temperature_celsius": 45.0,
      "utilization_percent": 85.3,
      "memory_used_mb": 18432,
      "memory_total_mb": 24576
    }
  ]
}
```

### 통합 전력 모니터링

```bash
# 1. 전체 시스템 전력 소비
curl -u admin:changeme http://localhost:8001/api/v1/monitoring/power

# 2. 클러스터별 전력 분해
curl -u admin:changeme "http://localhost:8001/api/v1/monitoring/power/breakdown?breakdown_by=cluster"

# 3. 전력 효율성 (PUE)
curl -u admin:changeme http://localhost:8001/api/v1/monitoring/power/efficiency

# 4. 전력 시계열 (1시간, 1분 간격)
curl -u admin:changeme "http://localhost:8001/api/v1/monitoring/timeseries/power?period=1h&step=1m"
```

### 노드/Pod 인프라 모니터링

```bash
# 노드 목록
curl -u admin:changeme http://localhost:8001/api/v1/infrastructure/nodes

# 특정 노드 상세
curl -u admin:changeme http://localhost:8001/api/v1/infrastructure/nodes/worker1

# Pod 목록
curl -u admin:changeme http://localhost:8001/api/v1/infrastructure/pods

# 특정 Pod 상세
curl -u admin:changeme http://localhost:8001/api/v1/infrastructure/pods/default/my-pod
```

### 데이터 내보내기

```bash
# CSV 내보내기
curl -u admin:changeme "http://localhost:8001/api/v1/export/power?format=csv&period=1h" > power_data.csv

# Excel 내보내기
curl -u admin:changeme "http://localhost:8001/api/v1/export/power?format=excel&period=1h" > power_data.xlsx

# 일일 리포트 (PDF)
curl -u admin:changeme "http://localhost:8001/api/v1/export/report?template=daily&format=pdf" > report.pdf
```

## 실시간 데이터 스트리밍

### WebSocket

```javascript
// 전력 데이터 실시간 스트리밍
const ws = new WebSocket('ws://localhost:8001/api/v1/monitoring/stream/power?interval=5');

ws.onopen = () => {
  console.log('Connected');
};

ws.onmessage = (event) => {
  const data = JSON.parse(event.data);
  console.log('Total Power:', data.total_power_watts, 'W');
  console.log('GPU Power:', data.gpu_power_watts, 'W');
  console.log('Infrastructure:', data.infrastructure_power_watts, 'W');
};

ws.onerror = (error) => {
  console.error('WebSocket error:', error);
};
```

### SSE (Server-Sent Events)

```javascript
// 전력 이벤트 스트림
const eventSource = new EventSource('http://localhost:8001/api/v1/monitoring/events/power');

eventSource.addEventListener('power_update', (event) => {
  const data = JSON.parse(event.data);
  console.log('Power update:', data);
});

eventSource.addEventListener('threshold_exceeded', (event) => {
  const alert = JSON.parse(event.data);
  console.warn('Alert:', alert.message);
});
```

## 멀티 클러스터 사용

### 환경 변수 설정

```bash
# .env 파일에 멀티 클러스터 설정
PROMETHEUS_CLUSTERS=[
  {"name":"cluster1","url":"http://prom1:9090","region":"us-east-1"},
  {"name":"cluster2","url":"http://prom2:9090","region":"eu-west-1"}
]
```

### API 사용

```bash
# 클러스터 목록
curl -u admin:changeme http://localhost:8001/api/v1/clusters

# 특정 클러스터 요약
curl -u admin:changeme http://localhost:8001/api/v1/clusters/cluster1/summary

# 클러스터별 GPU 필터링
curl -u admin:changeme "http://localhost:8001/api/v1/accelerators/gpus?cluster=cluster1"
```

## 공통 쿼리 파라미터

모든 API에서 사용 가능한 공통 파라미터:

| 파라미터 | 설명 | 예시 |
|---------|------|------|
| `cluster` | 클러스터 필터 | `?cluster=cluster1` |
| `period` | 조회 기간 | `?period=1h` (1h, 1d, 1w, 1m) |
| `step` | 시계열 간격 | `?step=1m` (1m, 5m, 15m, 1h) |
| `limit` | 결과 개수 제한 | `?limit=50` |
| `offset` | 페이지네이션 오프셋 | `?offset=100` |

## API 문서

### Swagger UI
http://localhost:8001/docs

### ReDoc
http://localhost:8001/redoc

### OpenAPI JSON
http://localhost:8001/openapi.json

## 주요 API 엔드포인트

### 1. Accelerators (가속기)
- `GET /api/v1/accelerators/gpus` - GPU 목록
- `GET /api/v1/accelerators/gpus/{gpu_id}` - GPU 상세
- `GET /api/v1/accelerators/npus` - NPU 목록

### 2. Infrastructure (인프라)
- `GET /api/v1/infrastructure/nodes` - 노드 목록
- `GET /api/v1/infrastructure/pods` - Pod 목록
- `GET /api/v1/infrastructure/containers` - 컨테이너 목록

### 3. Hardware (하드웨어)
- `GET /api/v1/hardware/ipmi/sensors` - IPMI 센서
- `GET /api/v1/hardware/ipmi/power` - 전력 센서
- `GET /api/v1/hardware/ipmi/temperature` - 온도 센서

### 4. Monitoring (통합 모니터링)
- `GET /api/v1/monitoring/power` - 통합 전력
- `GET /api/v1/monitoring/timeseries/power` - 전력 시계열
- `WS /api/v1/monitoring/stream/power` - 전력 스트리밍

### 5. Export (데이터 내보내기)
- `GET /api/v1/export/power` - 전력 데이터 내보내기
- `GET /api/v1/export/report` - 리포트 생성

### 6. System (시스템)
- `GET /api/v1/system/health` - 헬스체크
- `GET /api/v1/system/version` - 버전 정보
- `GET /api/v1/system/metrics` - API 메트릭 (Prometheus 형식)

## 다음 단계

- 자세한 API 명세: [API_GUIDE.md](./API_GUIDE.md)
- 아키텍처 이해: [ARCHITECTURE_OVERVIEW.md](./ARCHITECTURE_OVERVIEW.md)
- 전체 명세: [../spec/](../spec/)

## 문제 해결

### Prometheus 연결 실패
```bash
# 헬스체크로 Prometheus 연결 상태 확인
curl http://localhost:8001/api/v1/system/health

# Prometheus URL 확인
echo $PROMETHEUS_URL
```

### 인증 오류
```bash
# 환경 변수 확인
echo $API_AUTH_USERNAME
echo $API_AUTH_PASSWORD

# .env 파일 다시 로드
source .env
```

### 빈 응답 또는 데이터 없음
- Prometheus에 메트릭이 수집되고 있는지 확인
- 쿼리 기간(period)을 늘려보기
- 클러스터 필터가 올바른지 확인

## 지원

- 이슈 리포트: GitHub Issues
- 문서: `/docs` 폴더
- API 문서: http://localhost:8001/docs
