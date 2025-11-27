# 아키텍처 개요

KCloud Monitor의 7-도메인 아키텍처 개요입니다.

## 시스템 개요

KCloud Monitor는 FastAPI 기반의 통합 모니터링 API 서버로, 외부 Prometheus 서버와 연동하여 AI 가속기, 쿠버네티스 인프라, 물리 하드웨어의 전력 및 성능 정보를 제공합니다.

**핵심 특징**:
- 멀티 클러스터 지원
- 다양한 가속기 지원 (GPU, NPU)
- 통합 인프라 모니터링
- 실시간 데이터 스트리밍
- 외부 API 제공

## 전체 아키텍처

```
┌──────────────────────────────────────────────────────┐
│           외부 Prometheus 생태계                       │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐           │
│  │  Kepler  │  │   DCGM   │  │   IPMI   │           │
│  │(Pod 전력)│  │(GPU 메트릭)│  │(하드웨어)│           │
│  └──────────┘  └──────────┘  └──────────┘           │
│  ┌──────────────────────────────────────┐            │
│  │      Prometheus (멀티 클러스터)       │            │
│  └──────────────────────────────────────┘            │
└──────────────────────────────────────────────────────┘
                    │
                    │ HTTP/HTTPS Query
                    ▼
┌──────────────────────────────────────────────────────┐
│              FastAPI Monitoring Server                │
│  ┌────────────────────────────────────────────────┐  │
│  │           API Layer (7-Domain)                 │  │
│  │  • Accelerators (GPU, NPU)                    │  │
│  │  • Infrastructure (Node, Pod, Container, VM)  │  │
│  │  • Hardware (IPMI)                            │  │
│  │  • Clusters (멀티 클러스터)                    │  │
│  │  • Monitoring (통합 모니터링 + 스트리밍)        │  │
│  │  • Export (다양한 포맷)                        │  │
│  │  • System (헬스체크, 메트릭)                   │  │
│  └────────────────────────────────────────────────┘  │
│  ┌────────────────────────────────────────────────┐  │
│  │           Service Layer                        │  │
│  │  • Prometheus Client (쿼리 실행)              │  │
│  │  • Cache Service (TTL 기반 캐싱)              │  │
│  │  • Stream Service (WebSocket/SSE)             │  │
│  │  • Cluster Registry (멀티 클러스터 관리)       │  │
│  │  • Exporters (CSV, Excel, Parquet, PDF)      │  │
│  └────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────┘
                    │
                    │ REST API (JSON/CSV/Stream)
                    ▼
┌──────────────────────────────────────────────────────┐
│                 API Clients                           │
│  • Dashboards (Grafana)                              │
│  • External Partners (API)                           │
│  • CLI Scripts                                       │
└──────────────────────────────────────────────────────┘
```

## 7-도메인 구조

### 1. Accelerators (AI 가속기)
AI 가속기 모니터링 도메인

**리소스**:
- GPU: NVIDIA GPU 전력, 성능, 온도 (DCGM 기반)
- NPU: Furiosa AI, Rebellions 등 국산 NPU

**데이터 소스**:
- DCGM Exporter: GPU 상세 메트릭
- Kepler: Pod 레벨 GPU 전력

**주요 API**:
- `/api/v1/accelerators/gpus` - GPU 목록
- `/api/v1/accelerators/gpus/{gpu_id}/metrics` - GPU 메트릭
- `/api/v1/accelerators/npus` - NPU 목록

### 2. Infrastructure (인프라)
쿠버네티스 인프라 모니터링 도메인

**리소스**:
- Nodes: 쿠버네티스 노드 전력 및 리소스
- Pods: Pod 레벨 전력 소비 및 워크로드
- Containers: 컨테이너 레벨 메트릭
- VMs: OpenStack 가상 머신 (Placeholder)

**데이터 소스**:
- Kepler: 노드/Pod 전력 데이터
- kube-state-metrics: 쿠버네티스 메타데이터
- cAdvisor: 컨테이너 메트릭

**주요 API**:
- `/api/v1/infrastructure/nodes` - 노드 목록
- `/api/v1/infrastructure/pods` - Pod 목록
- `/api/v1/infrastructure/containers` - 컨테이너 목록

### 3. Hardware (물리 하드웨어)
하드웨어 센서 모니터링 도메인

**리소스**:
- IPMI: 서버 하드웨어 센서 (전력, 온도, 팬, 전압)
- BMC: Baseboard Management Controller

**데이터 소스**:
- IPMI Exporter: 하드웨어 센서 데이터

**주요 API**:
- `/api/v1/hardware/ipmi/sensors` - 전체 IPMI 센서
- `/api/v1/hardware/ipmi/power` - 전력 센서
- `/api/v1/hardware/ipmi/temperature` - 온도 센서

### 4. Clusters (멀티 클러스터)
멀티 클러스터 관리 도메인

**기능**:
- 클러스터별 리소스 관리
- 통합 뷰 및 비교 분석
- 토폴로지 시각화

**주요 API**:
- `/api/v1/clusters` - 클러스터 목록
- `/api/v1/clusters/{cluster_name}/summary` - 클러스터 요약
- `/api/v1/clusters/{cluster_name}/power` - 클러스터 전력

### 5. Monitoring (통합 모니터링)
크로스 도메인 통합 모니터링 및 실시간 스트리밍

**기능**:
- 통합 전력 모니터링 및 분해 분석
- 시계열 데이터 조회
- 실시간 WebSocket/SSE 스트리밍

**주요 API**:
- `/api/v1/monitoring/power` - 통합 전력
- `/api/v1/monitoring/power/breakdown` - 전력 분해
- `/api/v1/monitoring/timeseries/power` - 전력 시계열
- `WS /api/v1/monitoring/stream/power` - 전력 스트리밍

### 6. Export (데이터 내보내기)
다양한 포맷으로 데이터 내보내기

**지원 포맷**:
- JSON: 기본 포맷
- CSV: 범용 데이터 분석
- Parquet: 빅데이터 분석
- Excel: 비즈니스 리포트
- PDF: 프레젠테이션 리포트

**주요 API**:
- `/api/v1/export/power` - 전력 데이터 내보내기
- `/api/v1/export/metrics` - 메트릭 내보내기
- `/api/v1/export/report` - 종합 리포트

### 7. System (시스템)
시스템 정보 및 메트릭 노출

**기능**:
- 헬스체크
- API 버전 정보
- Prometheus 메트릭 노출

**주요 API**:
- `/api/v1/system/health` - 헬스체크 (인증 불필요)
- `/api/v1/system/version` - 버전 정보
- `/api/v1/system/metrics` - API 메트릭 (Prometheus 형식)

## 핵심 컴포넌트

### 1. Prometheus Client
외부 Prometheus 서버와 통신하는 클라이언트

**기능**:
- PromQL 쿼리 실행
- 멀티 클러스터 쿼리 지원
- 연결 풀링 및 재시도 로직
- 타임아웃 관리

**구현**: `app/services/prometheus.py`

### 2. Cache Service
메모리 기반 TTL 캐싱 서비스

**캐싱 전략**:
- 정적 데이터: 1시간 (GPU 정보, 클러스터 정보)
- 실시간 데이터: 30초 (메트릭, 전력, 온도)
- 시계열 데이터: 5분
- 요약 통계: 60초

**구현**: `app/services/cache.py`

### 3. Cluster Registry
멀티 클러스터 관리 레지스트리

**기능**:
- 클러스터별 Prometheus 클라이언트 관리
- 동적 클러스터 추가/제거
- 헬스체크 및 자동 복구

**구현**: `app/services/cluster_registry.py`

### 4. Stream Service
실시간 데이터 스트리밍 서비스

**프로토콜**:
- WebSocket: 양방향 통신
- SSE (Server-Sent Events): 단방향 이벤트 스트림

**구현**: `app/services/stream.py`

### 5. Exporters
다양한 포맷 내보내기 서비스

**구현**:
- `app/services/exporters/csv_exporter.py`
- `app/services/exporters/excel_exporter.py`
- `app/services/exporters/parquet_exporter.py`
- `app/services/exporters/pdf_exporter.py`

## 데이터 흐름

### 1. 일반 API 요청
```
Client → FastAPI → Auth Middleware → API Router → CRUD Helper
  → Cache Check → (Cache Miss) → Prometheus Query → Data Processing
  → Cache Update → Response
```

### 2. 실시간 스트리밍
```
Client → WebSocket/SSE Connection → Stream Service
  → Periodic Prometheus Query → Data Processing
  → Push to Client (Interval: 5s)
```

### 3. 데이터 내보내기
```
Client → Export API → CRUD Helper → Prometheus Query
  → Data Processing → Exporter (CSV/Excel/PDF)
  → File Generation → Response (Download)
```

### 4. 멀티 클러스터 쿼리
```
Client → API → Cluster Registry → Get Prometheus Clients
  → Parallel Queries (각 클러스터) → Aggregate Results
  → Response
```

## 데이터 모델

### 명명 규칙
모든 필드는 단위를 명시합니다:

```python
# 전력
power_watts: float
total_energy_joules: float

# 온도
temperature_celsius: float
gpu_temperature_celsius: float

# 메모리
memory_used_mb: int
memory_total_mb: int

# 사용률
cpu_utilization_percent: float
gpu_utilization_percent: float

# 클럭
sm_clock_mhz: int
memory_clock_mhz: int

# 네트워크
network_rx_mbps: float
network_tx_mbps: float

# 기타
fan_speed_rpm: int
latency_ms: float
throughput_fps: float
```

## 보안

### 인증
- Basic Authentication (개발환경)
- 환경 변수 기반 사용자명/비밀번호
- System 엔드포인트는 인증 불필요

### 권한
- 현재: 단일 관리자 계정
- 향후: RBAC (Role-Based Access Control) 지원 예정

### CORS
- 개발: 모든 도메인 허용
- 프로덕션: 특정 도메인만 허용

## 성능 최적화

### 캐싱
- In-Memory TTL 캐싱
- 데이터 특성별 TTL 조정
- 캐시 히트율 모니터링

### 병렬 처리
- 멀티 클러스터 병렬 쿼리
- AsyncIO 기반 비동기 처리

### 연결 풀링
- HTTP 연결 풀링
- Prometheus 클라이언트 재사용

## 확장성

### 수평 확장
- Stateless API 서버 (캐시 제외)
- 로드 밸런서 뒤 다중 인스턴스
- Prometheus 페더레이션

### 수직 확장
- 캐시 크기 조정
- 워커 프로세스 증가
- 연결 풀 크기 조정

### 데이터 소스 확장
- 새로운 Exporter 추가
- 새로운 가속기 지원 (NPU, TPU, IPU)
- OpenStack VM 모니터링

## 모니터링

### API 메트릭
API 서버 자체를 Prometheus가 모니터링:

```
api_requests_total              # 총 요청 수
api_request_duration_seconds    # 요청 지연시간
api_errors_total                # 에러 수
cache_hits_total                # 캐시 히트
cache_misses_total              # 캐시 미스
websocket_connections           # WebSocket 연결 수
prometheus_query_duration_seconds  # Prometheus 쿼리 시간
```

### 헬스체크
- `/api/v1/system/health`: API 서버 및 Prometheus 연결 상태
- Prometheus 응답 시간 측정
- 캐시 히트율 모니터링

## 프로젝트 구조

```
ai-chip-monitor/
├── app/
│   ├── main.py                 # FastAPI 애플리케이션
│   ├── config.py               # 설정 관리
│   ├── deps.py                 # 의존성
│   ├── crud.py                 # CRUD 로직 (3700+ lines)
│   ├── api/v1/                 # API 라우터 (7-Domain)
│   │   ├── accelerators.py     # GPU/NPU API
│   │   ├── infrastructure.py   # Nodes/Pods/Containers/VMs
│   │   ├── hardware.py         # IPMI 센서
│   │   ├── clusters.py         # 멀티 클러스터
│   │   ├── monitoring.py       # 통합 모니터링 + 스트리밍
│   │   ├── export.py           # 데이터 내보내기
│   │   └── system.py           # 시스템 정보
│   ├── models/                 # Pydantic 데이터 모델
│   │   ├── accelerators/       # GPU/NPU 모델
│   │   ├── infrastructure/     # Nodes/Pods/Containers/VMs
│   │   ├── hardware/           # IPMI 센서
│   │   └── common/             # 공통 모델
│   ├── services/               # 서비스 계층
│   │   ├── prometheus.py       # Prometheus 클라이언트
│   │   ├── cache.py            # 캐싱
│   │   ├── cluster_registry.py # 멀티 클러스터
│   │   ├── stream.py           # 스트리밍
│   │   └── exporters/          # 내보내기
│   └── middleware/
│       └── metrics.py          # API 메트릭 수집
├── tests/                      # 테스트 코드 (113 tests)
│   ├── services/               # 서비스 단위 테스트 (55 tests)
│   └── api/                    # API 통합 테스트 (46 tests)
├── docs/                       # 문서
├── spec/                       # 설계 문서
├── .env                        # 환경 변수
└── requirements.txt            # Python 의존성
```

## 기술 스택

### Backend
- **FastAPI**: 웹 프레임워크
- **Uvicorn**: ASGI 서버
- **Pydantic**: 데이터 검증
- **Requests/HTTPX**: HTTP 클라이언트

### 데이터 처리
- **PyArrow**: Parquet 내보내기
- **OpenPyXL**: Excel 내보내기
- **ReportLab**: PDF 생성
- **Pandas**: 데이터 처리 (옵션)

### 모니터링
- **Prometheus Client**: 메트릭 노출
- **Prometheus**: 외부 데이터 소스

### 테스트
- **Pytest**: 테스트 프레임워크
- **Pytest-AsyncIO**: 비동기 테스트

## 배포

### 개발 환경
```bash
uvicorn app.main:app --host 0.0.0.0 --port 8001 --reload
```

### 프로덕션 환경
```bash
# 다중 워커
uvicorn app.main:app --host 0.0.0.0 --port 8001 --workers 4

# 또는 Gunicorn + Uvicorn
gunicorn app.main:app -w 4 -k uvicorn.workers.UvicornWorker
```

### Docker (계획)
```dockerfile
FROM python:3.12-slim
WORKDIR /app
COPY requirements.txt .
RUN pip install -r requirements.txt
COPY app/ app/
CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8001"]
```

### Kubernetes (계획)
- Deployment: API 서버 다중 인스턴스
- Service: LoadBalancer 또는 ClusterIP
- ConfigMap: 환경 변수
- Secret: 인증 정보

## 향후 계획

### Phase 11: 프로덕션 준비
- Prometheus 쿼리 병렬화
- Rate Limiting
- 구조화된 로깅
- Kubernetes 배포 매니페스트

### Phase 12: 기능 확장
- NPU 모니터링 (Furiosa, Rebellions)
- OpenStack VM 모니터링
- 알림 시스템 (Alerting)
- 대시보드 템플릿 (Grafana)

### Phase 13: 고급 기능
- RBAC (Role-Based Access Control)
- API Key 인증
- 배치 쿼리 API
- 데이터 집계 서비스

## 참고 문서

- 빠른 시작: [QUICK_START.md](./QUICK_START.md)
- API 가이드: [API_GUIDE.md](./API_GUIDE.md)
- 전체 명세: [../spec/](../spec/)
- API 문서: http://localhost:8001/docs

## 지원

- GitHub Issues
- 문서: `/docs` 폴더
- Swagger UI: http://localhost:8001/docs
