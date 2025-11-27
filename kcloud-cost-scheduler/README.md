# KCloud Workload Optimizer Scheduler

[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8.svg)](https://golang.org/)
[![Kubernetes](https://img.shields.io/badge/Kubernetes-1.19+-326CE5.svg)](https://kubernetes.io/)
[![Helm](https://img.shields.io/badge/Helm-3.0+-0F1689.svg)](https://helm.sh/)

Kubernetes Scheduler for AI Semiconductor Workload Optimization

## 개요

KCloud Workload Optimizer Scheduler는 Kubernetes 환경에서 AI 반도체 워크로드의 비용 및 전력 최적화를 자동화하는 Kubernetes Scheduler입니다. Custom Resource Definitions(CRD)를 통해 워크로드 최적화 정책을 선언적으로 관리하고, Controller 패턴으로 실제 최적화를 실행합니다.

### 주요 특징

-  **비용 최적화**: 워크로드별 비용 및 전력 최적화 자동 실행
-  **실시간 모니터링**: Prometheus 메트릭을 통한 실시간 모니터링
-  **유연한 정책**: CostPolicy와 PowerPolicy를 통한 세밀한 제어
-  **스마트 스케줄링**: 고급 스케줄링 알고리즘으로 최적 노드 배치
-  **보안 강화**: Admission Webhook을 통한 자동 정책 적용
-  **Helm 지원**: 원클릭 배포를 위한 완전한 Helm 차트

## 주요 기능

### Custom Resource 관리
- **WorkloadOptimizer CRD**: 워크로드별 최적화 정책 정의
- **CostPolicy CRD**: 비용 제약사항 및 예산 관리
- **PowerPolicy CRD**: 전력 사용량 제약사항 관리

### 자동화된 최적화
- **스케줄링 최적화**: 비용/전력 효율적인 노드 배치
- **Auto-scaling**: 예측 기반 워크로드 스케일링
- **리소스 재배치**: 실시간 최적화를 통한 워크로드 마이그레이션

### Kubernetes 네이티브
- **Admission Webhook**: 워크로드 생성 시 자동 최적화 정책 적용
- **Finalizer**: 워크로드 삭제 시 리소스 정리
- **Event 기반**: Kubernetes 이벤트 기반 반응형 최적화


## CRD 정의

### WorkloadOptimizer

```yaml
apiVersion: kcloud.io/v1alpha1
kind: WorkloadOptimizer
metadata:
  name: ml-training-optimizer
  namespace: ai-workloads
spec:
  workloadType: "training"
  priority: 80
  resources:
    cpu: "16"
    memory: "64Gi"
    gpu: 4
    npu: 0
  costConstraints:
    maxCostPerHour: 50.0
    preferSpot: true
    budgetLimit: 1200.0
  powerConstraints:
    maxPowerUsage: 2000.0  # Watts
    preferGreen: true
  placementPolicy:
    nodeSelector:
      accelerator: nvidia-gpu
    affinity:
    - type: "gpu_workload"
      key: "gpu.nvidia.com/class"
      value: "compute"
      weight: 100
  autoScaling:
    minReplicas: 1
    maxReplicas: 10
    metrics:
    - type: "cost"
      threshold: 80
    - type: "power"
      threshold: 1800
status:
  phase: "Optimizing"
  currentCost: 42.5
  currentPower: 1650.0
  assignedNode: "gpu-node-001"
  optimizationScore: 0.87
  conditions:
  - type: "CostOptimized"
    status: "True"
    reason: "WithinBudget"
```

## 설치 및 배포

### 빠른 시작

```bash
# Helm을 사용한 설치 (권장)
helm install kcloud-operator ./charts/kcloud-operator \
  --namespace kcloud-operator-system \
  --create-namespace

# 설치 확인
kubectl get pods -n kcloud-operator-system
kubectl get crd | grep kcloud.io
```

### 개발 환경

```bash
# 의존성 다운로드
make deps

# CRD 생성
make generate

# 빌드
make build

# 로컬 실행 (kubeconfig 필요)
make run
```

### Kubernetes 배포

```bash
# CRD 설치
make install

# Operator 배포
make deploy

# 확인
kubectl get pods -n kcloud-operator-system
kubectl get crd | grep kcloud.io
```

### 상세한 배포 가이드

자세한 배포 방법은 [배포 가이드](docs/DEPLOYMENT_GUIDE.md)를 참조하세요.

## 설정

### 환경변수

- `WATCH_NAMESPACE`: 감시할 네임스페이스 (빈 값 = 모든 네임스페이스)
- `POD_NAME`: Operator 파드 이름
- `OPERATOR_NAME`: Operator 식별자
- `CORE_SCHEDULER_URL`: Core Scheduler 서비스 URL

### RBAC 권한

```yaml
# 필요한 권한 예시
rules:
- apiGroups: [""]
  resources: ["pods", "nodes", "services"]
  verbs: ["get", "list", "watch", "create", "update", "patch"]
- apiGroups: ["apps"]
  resources: ["deployments", "replicasets"]
  verbs: ["get", "list", "watch", "create", "update", "patch"]
- apiGroups: ["kcloud.io"]
  resources: ["workloadoptimizers"]
  verbs: ["get", "list", "watch", "create", "update", "patch"]
```

## Controller 로직

### WorkloadOptimizer Controller

```go
func (r *WorkloadOptimizerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // 1. WorkloadOptimizer 리소스 조회
    var wo kcloudv1alpha1.WorkloadOptimizer
    if err := r.Get(ctx, req.NamespacedName, &wo); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // 2. 현재 상태 분석
    currentState, err := r.analyzeCurrentState(ctx, &wo)
    if err != nil {
        return ctrl.Result{}, err
    }

    // 3. 최적화 실행
    optimized, err := r.optimizeWorkload(ctx, &wo, currentState)
    if err != nil {
        return ctrl.Result{}, err
    }

    // 4. 상태 업데이트
    if err := r.updateStatus(ctx, &wo, optimized); err != nil {
        return ctrl.Result{}, err
    }

    return ctrl.Result{RequeueAfter: time.Minute * 5}, nil
}
```

## Admission Webhook

### Mutating Webhook

자동으로 WorkloadOptimizer 정책을 Pod에 적용:

```go
func (w *WorkloadMutator) Handle(ctx context.Context, req admission.Request) admission.Response {
    pod := &corev1.Pod{}
    if err := w.Decoder.Decode(req, pod); err != nil {
        return admission.Errored(http.StatusBadRequest, err)
    }

    // 워크로드 타입 추론
    workloadType := w.inferWorkloadType(pod)
    
    // 최적화 정책 적용
    optimized := w.applyOptimizationPolicy(pod, workloadType)
    
    return admission.PatchResponseFromRaw(req.Object.Raw, optimized)
}
```

### Validating Webhook

리소스 생성/수정 시 정책 검증:

```go
func (w *WorkloadValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
    wo := &kcloudv1alpha1.WorkloadOptimizer{}
    if err := w.Decoder.Decode(req, wo); err != nil {
        return admission.Errored(http.StatusBadRequest, err)
    }

    // 비용 제약사항 검증
    if err := w.validateCostConstraints(wo); err != nil {
        return admission.Denied(err.Error())
    }

    // 전력 제약사항 검증
    if err := w.validatePowerConstraints(wo); err != nil {
        return admission.Denied(err.Error())
    }

    return admission.Allowed("")
}
```

## 사용 예시

### 1. ML 트레이닝 워크로드 최적화

```bash
# WorkloadOptimizer 생성
kubectl apply -f - <<EOF
apiVersion: kcloud.io/v1alpha1
kind: WorkloadOptimizer
metadata:
  name: bert-training
  namespace: ml-workloads
spec:
  workloadType: "training"
  resources:
    cpu: "32"
    memory: "128Gi"
    gpu: 8
  costConstraints:
    maxCostPerHour: 100.0
    preferSpot: true
  powerConstraints:
    maxPowerUsage: 4000.0
EOF

# 상태 확인
kubectl get wo bert-training -o yaml
kubectl describe wo bert-training
```

### 2. 추론 서빙 워크로드 최적화

```bash
kubectl apply -f - <<EOF
apiVersion: kcloud.io/v1alpha1  
kind: WorkloadOptimizer
metadata:
  name: llm-serving
spec:
  workloadType: "serving"
  resources:
    cpu: "8"
    memory: "32Gi"
    gpu: 2
  costConstraints:
    maxCostPerHour: 25.0
  autoScaling:
    minReplicas: 2
    maxReplicas: 20
    metrics:
    - type: "latency"
      threshold: 100  # ms
EOF
```

## 모니터링

### Prometheus 메트릭

- `kcloud_workload_optimizations_total`: 최적화 실행 횟수
- `kcloud_cost_savings_total`: 비용 절약 누적 금액
- `kcloud_power_savings_watts`: 전력 절약량
- `kcloud_optimization_score`: 최적화 점수

### 이벤트

```bash
# Kubernetes 이벤트 확인
kubectl get events --field-selector reason=WorkloadOptimized

# Operator 로그
kubectl logs -n kcloud-system deployment/kcloud-operator -f
```

## 개발

### 요구사항

- Go 1.21+
- Kubernetes 1.25+
- controller-runtime
- operator-sdk (선택사항)

### 코드 생성

```bash
# Controller/Client 코드 생성
make generate

# CRD 매니페스트 생성
make manifests

# 모든 코드 생성
make all
```

### 테스트

```bash
# 단위 테스트
make test

# 통합 테스트 (envtest)
make test-integration

# E2E 테스트
make test-e2e
```

## 문서

- **[API 문서](docs/API.md)**: CRD 스펙 및 API 레퍼런스
- **[사용 예시](docs/EXAMPLES.md)**: 다양한 워크로드 최적화 예시
- **[개발자 가이드](docs/DEVELOPER_GUIDE.md)**: 개발 환경 설정 및 기여 가이드
- **[배포 가이드](docs/DEPLOYMENT_GUIDE.md)**: 프로덕션 배포 및 운영 가이드
- **[Helm 차트](charts/kcloud-operator/README.md)**: Helm 차트 사용법

## 기여하기

프로젝트에 기여하고 싶으시다면 [개발자 가이드](docs/DEVELOPER_GUIDE.md)를 참조하세요.

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add some amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## 라이선스

Apache License 2.0
