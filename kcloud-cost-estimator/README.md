# kcloud-cost-estimator

**Kubernetes 클러스터 전력 데이터 수집 및 에너지 예측 모듈**

## 주요 기능

### Power 메트릭 수집
- 실시간 전력 데이터: 컨테이너/노드별 전력 소비량
- GPU/NPU 전력 모니터링: AI 가속기 특화 메트릭
- 워크로드별 전력 프로파일링: 작업 유형별 전력 패턴 분석

### 데이터 처리 파이프라인
- 메트릭 정규화: Power raw data → 표준화된 전력 메트릭
- 비용 환산: 전력 소비량 → 운용 비용 계산
- 실시간 스트리밍: Redis/DB를 통한 데이터 전송

### 에너지 예측 (Energy Prediction)
- ARIMA 기반 워크로드 예측: 과거 패턴 기반 미래 CPU 사용량 예측
- 4단계 예측 프레임워크: 컨테이너 → 노드 → 전력 → 분배
- 자동 모델 보정: 실측 데이터 기반 선형 회귀 파라미터 자동 조정
- 배포 전 에너지 예측: 컨테이너 배포 전 전력 소비량 사전 파악

**참고 논문**: Alzamil, I., & Djemame, K. (2017). Energy Prediction for Cloud Workload Patterns. GECON 2016: Economics of Grids, Clouds, Systems, and Services, pp. 160-174.

## 아키텍처

```
Power Exporter (Prometheus)
    ↓ HTTP/Prometheus API
PowerClient → PowerMetrics → DataProcessor
    ↓                ↓             ↓
  수집          정규화/집계      비용환산
    ↓                ↓             ↓
Redis Queue ← InfluxDB ← analyzer/predictor 모듈
```

## 핵심 메트릭

### Power 메트릭 매핑
```yaml
power_metrics:
  container_power:
    - kepler_container_joules_total          # 컨테이너 총 전력
    - kepler_container_core_joules_total     # CPU 전력
    - kepler_container_dram_joules_total     # DRAM 전력
    - kepler_container_gpu_joules_total      # GPU 전력
    - kepler_container_other_joules_total    # 기타 하드웨어

  node_power:
    - kepler_node_platform_joules_total      # 노드 플랫폼 전력
    - kepler_node_core_joules_total          # 노드 CPU 전력
    - kepler_node_dram_joules_total          # 노드 DRAM 전력

  workload_classification:
    - pod_name, namespace, workload_type
    - container_name, image, command
```

### 비용 환산 공식
```python
# src/power_metrics/cost_calculator.py
def calculate_power_cost(power_watts, duration_hours):
    electricity_cost = power_watts * (duration_hours / 1000) * ELECTRICITY_RATE
    cooling_overhead = electricity_cost * COOLING_FACTOR
    carbon_cost = (power_watts * duration_hours / 1000) * CARBON_RATE
    return electricity_cost + cooling_overhead + carbon_cost
```

## 설정

### 환경변수
```bash
# Prometheus 연결
POWER_PROMETHEUS_URL=http://prometheus:9090
POWER_METRICS_INTERVAL=15

# 비용 계산
ELECTRICITY_RATE=0.12  # $/kWh
COOLING_FACTOR=1.3     # 냉각 오버헤드
CARBON_RATE=0.05       # $/kg CO2

# 데이터 저장
REDIS_URL=redis://localhost:6379
INFLUXDB_URL=http://influxdb:8086
INFLUXDB_BUCKET=power_metrics
```

## API 엔드포인트

### 전력 데이터 수집
```bash
# 실시간 전력 데이터
GET /power/current?workload=ml-training&namespace=default
GET /power/containers?namespace=kcloud-workloads
GET /power/nodes

# 비용 분석 데이터
GET /cost/current?namespace=default
GET /cost/hourly?start=2025-01-01T00:00:00Z
GET /cost/workload/{workload_id}

# 전력 프로파일
GET /profile/workload-types
POST /profile/classify
```

### 에너지 예측
```bash
# 컨테이너 에너지 예측
POST /predict/energy
# Request Body:
{
  "container_name": "ml-trainer",
  "pod_name": "ml-trainer-abc123",
  "namespace": "default",
  "historical_cpu_cores": [0.8, 0.85, 0.9, ...],
  "container_cpu_request": 1.0,
  "node_current_util": 45.0,
  "node_idle_util": 5.0,
  "containers_on_node": [],
  "prediction_horizon_minutes": 30
}
# Response:
{
  "prediction": {
    "predicted_power_watts": 12.5,
    "confidence_interval": [11.2, 13.8],
    "prediction_timestamp": "2025-01-26T10:00:00Z"
  }
}

# 모델 보정
POST /calibrate
# Request Body:
{
  "container_node_data": [
    {"container_cpu_cores": 0.5, "node_cpu_util_percent": 15.2},
    {"container_cpu_cores": 1.0, "node_cpu_util_percent": 28.5}
  ],
  "node_power_data": [
    {"node_cpu_util_percent": 0, "node_power_watts": 54.0},
    {"node_cpu_util_percent": 50, "node_power_watts": 85.2}
  ]
}
# Response:
{
  "calibration": {
    "container_to_node_slope": 23.993,
    "container_to_node_intercept": 4.124,
    "node_util_to_power_slope": 0.7254,
    "node_util_to_power_intercept": 53.97
  }
}

# 보정 설정 조회
GET /calibration/config
# Response: 현재 사용 중인 calibration 파라미터
```

## 사용 예시

### Python 클라이언트
```python
from src.power_client import PowerClient
from src.power_metrics import PowerCalculator

# 전력 데이터 수집
client = PowerClient(prometheus_url="http://prometheus:9090")
power_data = client.get_container_power_metrics(
    namespace="kcloud-workloads",
    time_range="5m"
)

# 비용 계산
calculator = PowerCalculator()
cost = calculator.calculate_total_cost(power_data)
print(f"워크로드 운용 비용: ${cost:.2f}/hour")
```

### 에너지 예측 사용
```python
from src.predictor import EnergyPredictor, HistoricalData
from datetime import datetime, timedelta

# 과거 CPU 사용량 데이터 준비
historical_data = HistoricalData(
    timestamps=[datetime.utcnow() - timedelta(minutes=i) for i in range(90, 0, -1)],
    values=[0.8] * 90,  # 90분간 0.8 cores 사용
    metric_name="cpu_cores"
)

# 예측 실행
predictor = EnergyPredictor()
prediction = predictor.predict_container_energy(
    container_name="nginx-server",
    pod_name="nginx-deploy-abc123",
    namespace="production",
    historical_workload=historical_data,
    container_cpu_request=1.0,
    node_current_util=45.0,
    node_idle_util=5.0,
    containers_on_node=[],
    prediction_horizon_minutes=30
)

print(f"예상 전력 소비: {prediction.predicted_power_watts:.2f} watts")
```

## 배포

### 로컬 개발
```bash
# 가상환경 생성 및 의존성 설치
make install

# 개발 모드 실행
make run

# 테스트 실행
make test
```

### Docker 실행
```bash
# Docker 이미지 빌드
make docker-build

# Docker 컨테이너 실행
make docker-run

# 로그 확인
make logs

# 컨테이너 정리
make docker-clean
```

### Kubernetes 배포
```bash
# 1. Docker 이미지 빌드
make docker-build

# 2. K8s 리소스 배포 (네임스페이스, RBAC, ConfigMap, Deployment, Service, HPA)
make k8s-deploy

# 3. 배포 상태 확인
make k8s-status

# 4. 포트 포워딩으로 로컬 접근
make k8s-port-forward

# 5. Health Check
curl -f http://localhost:8001/health

# 6. 로그 확인
make k8s-logs

# 7. 재배포 (코드 변경 후)
make k8s-build-deploy

# 8. 전체 삭제
make k8s-delete
```

#### 배포된 리소스
- **Namespace**: kcloud-system
- **ServiceAccount**: RBAC 권한 부여 (노드/파드 메트릭 조회)
- **ConfigMap**: 환경 변수 설정 (Prometheus URL, 비용 계산 파라미터 등)
- **Deployment**: 2개 레플리카로 시작 (리소스 제한: 100m-500m CPU, 256Mi-512Mi 메모리)
- **Service**: ClusterIP로 내부 통신 (8001 포트)
- **HPA**: CPU/메모리 사용량 기반 오토스케일링 (2-5 레플리카)

## 프로젝트 구조

```
kcloud-cost-estimator/
├── src/
│   ├── main.py                    # FastAPI 애플리케이션
│   ├── power_client/              # 전력 데이터 수집
│   │   └── client.py
│   ├── power_metrics/             # 메트릭 처리
│   ├── data_processor/            # 데이터 프로세싱
│   ├── predictor/                 # 에너지 예측 모듈
│   │   ├── models.py              # 데이터 모델
│   │   ├── workload_predictor.py  # ARIMA 워크로드 예측
│   │   ├── energy_predictor.py    # 4단계 에너지 예측
│   │   ├── calibration.py         # 모델 보정 도구
│   │   └── prometheus_helper.py   # Prometheus 쿼리 헬퍼
│   └── config/
│       └── settings.py
├── config/                        # 설정 파일
│   ├── __init__.py
│   └── settings.py                # 환경 변수 설정
├── demo/                          # 예제 및 데모 코드
│   ├── example_prediction.py      # 예측 사용 예제
│   └── predictor/                 # 예측 모듈 테스트
├── deployment/                    # K8s 배포 매니페스트
│   ├── namespace.yaml             # kcloud-system 네임스페이스
│   ├── rbac.yaml                  # ServiceAccount, ClusterRole, ClusterRoleBinding
│   ├── configmap.yaml             # 환경 변수 설정
│   ├── deployment.yaml            # Pod 배포 설정
│   ├── service.yaml               # ClusterIP 서비스
│   └── hpa.yaml                   # Horizontal Pod Autoscaler
├── .dockerignore
├── Dockerfile
├── requirements.txt
├── Makefile
└── README.md
```

## 라이센스

Apache License 2.0
