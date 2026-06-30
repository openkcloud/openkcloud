# Cost-Based Scheduler Architecture

## Overview

Cost-Based Scheduler는 Kubernetes 클러스터에서 GPU/NPU 워크로드를 비용 및 전력 효율성 기반으로 스케줄링하는 커스텀 스케줄러입니다. 플러그인 기반 아키텍처를 사용하여 확장성과 유지보수성을 보장합니다.

```
┌──────────────────────────────────────────────────────────────────────────────┐
│                            Cost-Based Scheduler                               │
├──────────────────────────────────────────────────────────────────────────────┤
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  ┌──────────────────┐ │
│  │  PreFilter   │─▶│    Filter    │─▶│    Score     │─▶│  Permit + Bind   │ │
│  │   Plugins    │  │   Plugins    │  │   Plugins    │  │    Plugins       │ │
│  └──────────────┘  └──────────────┘  └──────────────┘  └──────────────────┘ │
│         │                  │                  │                  │            │
│         ▼                  ▼                  ▼                  ▼            │
│  DurationAware      TaintToleration    CostEfficiency     GangScheduling     │
│  (long/short        NodeSelector       (0- 50 pts)        (gang-size)        │
│   노드 사전 확인)    NodeResourcesFit   PowerEfficiency                        │
│                     [+HAMi extender    (0- 30 pts)        DefaultBinder      │
│                      GPU slice 필터]   PodAffinity        (CPU workloads)    │
│                                        (0- 20 pts)                            │
│                                        BinPacking         HAMi Extender      │
│                                        (0-100 pts)        Bind               │
│                                        DurationAware      (GPU workloads)    │
│                                        (0- 20 pts)                            │
│                                        WorkloadAwareCost                      │
│                                        (0- 25 pts)                            │
│                                        WorkloadAwarePower                     │
│                                        (0- 15 pts)                            │
│                                                                               │
│                     AcceleratorMetricsProvider (accelerator-config.yaml)     │
└──────────────────────────────────────────────────────────────────────────────┘
```

---

## Directory Structure

```
cost-based-scheduler/
├── cmd/
│   └── main.go                      # 엔트리포인트
├── config/
│   └── accelerator-config.yaml      # GPU/NPU 스펙 설정
├── internal/
│   ├── backend/
│   │   ├── heap/                    # 힙 자료구조
│   │   ├── log/                     # 로깅
│   │   └── queue/                   # 스케줄링 큐
│   ├── config/
│   │   └── config.go                # 스케줄러 설정
│   ├── framework/
│   │   ├── interface.go             # 플러그인 인터페이스
│   │   ├── framework.go             # 프레임워크 구현
│   │   ├── plugin/
│   │   │   ├── costefficiency.go    # 비용 효율성 플러그인
│   │   │   ├── powerefficiency.go   # 전력 효율성 플러그인
│   │   │   ├── podaffinity.go       # Pod 친화도 플러그인
│   │   │   ├── noderesourcesfit.go  # 리소스 필터 플러그인
│   │   │   └── defaultbinder.go     # 바인딩 플러그인
│   │   └── utils/
│   │       ├── accelerator.go       # Accelerator 메트릭 Provider
│   │       ├── cache.go             # 노드/Pod 캐시
│   │       ├── nodeinfo.go          # 노드 정보
│   │       └── result.go            # 스코어링 결과
│   └── scheduler/
│       ├── scheduler.go             # 스케줄러 라이프사이클
│       ├── schedule_one.go          # 스케줄링 사이클
│       ├── eventhandler.go          # 이벤트 핸들러
│       └── events.go                # 이벤트 정의
└── deployments/                     # Kubernetes 매니페스트
```

---

## Core Components

### 1. Scheduler

스케줄러의 메인 컴포넌트로, Pod 스케줄링 라이프사이클을 관리합니다.

```go
type Scheduler struct {
    schedulerConfig *config.SchedulerConfig
    Cache           *utils.Cache              // 노드/Pod 캐시
    SchedulingQueue *internalqueue.SchedulingQueue
    fwk             framework.Framework       // 플러그인 프레임워크
}
```

**주요 기능:**
- Pod 이벤트 감시 및 큐 관리
- 스케줄링 사이클 실행
- GPU 메트릭 주기적 갱신

### 2. Framework

플러그인들을 실행하는 프레임워크입니다.

```go
type Framework interface {
    RunFilterPlugins(ctx, pod, nodeInfo) PluginResultMap
    RunScorePlugins(ctx, pod, nodes) (PluginResultMap, *Status)
    RunBindPlugin(ctx, pod, nodeName) *Status
}
```

### 3. Cache

클러스터 상태를 메모리에 캐싱합니다.

```go
type Cache struct {
    nodes       map[string]*NodeInfo    // 노드 정보
    podStates   map[string]*podState    // Pod 상태
    assumedPods sets.Set[string]        // 가정된(스케줄 예정) Pod
}
```

### 4. AcceleratorMetricsProvider

GPU/NPU 메트릭을 제공하는 인터페이스입니다.

```go
type AcceleratorMetricsProvider interface {
    GetMetrics(node *corev1.Node) AcceleratorMetrics
    GetMetricsByGPUInfo(gpuType, gpuModel string) AcceleratorMetrics
    GetConfig() *AcceleratorConfig
}
```

---

## Scheduling Cycle

### Phase 1: Filter (필터링)

리소스 요구사항을 충족하지 못하는 노드를 제외합니다.

```
Pod Request: nvidia.com/gpu: 1, cpu: 4, memory: 8Gi

Node A: nvidia.com/gpu: 2 (available: 1) ✓ Pass
Node B: nvidia.com/gpu: 0 (available: 0) ✗ Filtered
Node C: nvidia.com/gpu: 4 (available: 2) ✓ Pass
```

### Phase 2: Score (스코어링)

통과한 노드들에 대해 점수를 계산합니다.

```
┌────────────────────────────────────────────────────────────────┐
│                    Total Score (0-100)                          │
├────────────────────────────────────────────────────────────────┤
│  CostEfficiency   │  PowerEfficiency  │    PodAffinity         │
│     (0-50)        │      (0-30)       │      (0-20)            │
├───────────────────┼───────────────────┼────────────────────────┤
│ cost/inference    │   perf/watt       │  job/queue co-location │
│ 기반 점수          │   기반 점수        │  또는 spreading        │
└───────────────────┴───────────────────┴────────────────────────┘
```

### Phase 3: Select (선택)

가장 높은 점수를 받은 노드를 선택합니다.

### Phase 4: Bind (바인딩)

선택된 노드에 Pod를 바인딩합니다.

---

## Scoring Algorithms

### 1. CostEfficiency Plugin (0-50 points)

**목적:** 추론 비용이 낮은 노드를 선호

**알고리즘:**
```
costPerInference = costPerHour / perfScore

normalizedCost = costPerInference / 0.001  (max 1.0으로 cap)

score = 50 × (1.0 - normalizedCost)
```

**예시:**
| GPU | Cost/Hour | PerfScore | Cost/Inference | Score |
|-----|-----------|-----------|----------------|-------|
| H100 | $4.0 | 2000 | 0.002 | 0 |
| A100 | $2.0 | 1000 | 0.002 | 0 |
| MIG 1g.24gb | $0.3 | 150 | 0.002 | 0 |
| T4 | $0.35 | 200 | 0.00175 | 12.5 |
| Furiosa Warboy | $0.5 | 500 | 0.001 | 0 |

**해석:** 낮은 cost/inference → 높은 점수

---

### 2. PowerEfficiency Plugin (0-30 points)

**목적:** 전력 효율이 높은 노드를 선호

**알고리즘:**
```
perfPerWatt = perfScore / powerWatts

normalizedPerfPerWatt = perfPerWatt / 20.0  (max 1.0으로 cap)

score = 30 × normalizedPerfPerWatt
```

**예시:**
| GPU | PerfScore | PowerWatts | Perf/Watt | Score |
|-----|-----------|------------|-----------|-------|
| H100 | 2000 | 700 | 2.86 | 4.3 |
| A100 | 1000 | 400 | 2.5 | 3.75 |
| T4 | 200 | 70 | 2.86 | 4.3 |
| L4 | 350 | 72 | 4.86 | 7.3 |
| Furiosa Warboy | 500 | 100 | 5.0 | 7.5 |
| Furiosa RNGD | 800 | 150 | 5.33 | 8.0 |

**해석:** 높은 perf/watt → 높은 점수 (NPU가 유리)

---

### 3. PodAffinity Plugin (0-20 points)

**목적:** 관련 Pod들의 배치 전략 (co-location 또는 spreading)

**모드:**

#### Mode: `none`
```
score = 10 (neutral)
```

#### Mode: `preferred` (Co-location)
```
if sameJobPods > 0:
    bonus = sameJobPods × 2.0
    score = min(10 + bonus, 20)
elif sameQueuePods > 0:
    bonus = sameQueuePods × 1.0
    score = min(10 + bonus, 20)
else:
    score = 10
```

#### Mode: `anti` (Spreading)
```
if sameJobPods > 0:
    penalty = sameJobPods × 5.0
    score = max(10 - penalty, 0)
elif sameQueuePods > 0:
    penalty = sameQueuePods × 2.0
    score = max(10 - penalty, 0)
else:
    score = 20  # 빈 노드 = perfect spreading
```

**예시 (preferred mode):**
| Node | Same Job Pods | Same Queue Pods | Score |
|------|---------------|-----------------|-------|
| A | 3 | 0 | min(10 + 6, 20) = 16 |
| B | 0 | 5 | min(10 + 5, 20) = 15 |
| C | 0 | 0 | 10 |

**예시 (anti mode):**
| Node | Same Job Pods | Same Queue Pods | Score |
|------|---------------|-----------------|-------|
| A | 3 | 0 | max(10 - 15, 0) = 0 |
| B | 0 | 2 | max(10 - 4, 0) = 6 |
| C | 0 | 0 | 20 |

---

## Accelerator Metrics Provider

### GPU 매칭 우선순위

```
1. Node Labels (정확 매칭)
   └─ accelerator.type + accelerator.model

2. Resource Detection (자동 감지)
   ├─ nvidia.com/gpu → type: "gpu"
   ├─ nvidia.com/mig-* → type: "mig", model: "1g.24gb" 등
   └─ furiosa.ai/warboy → type: "npu", model: "warboy"

3. GPU Product Label (부분 매칭)
   └─ nvidia.com/gpu.product: "NVIDIA-A100-SXM4-80GB"

4. Fallback (이전 호환성)
   └─ accelerator.cost-per-hour, accelerator.perf-score 등 라벨
```

### Config File Structure

```yaml
accelerators:
  - name: "NVIDIA A100 80GB"
    type: "gpu"
    vendor: "nvidia"
    model: "a100-80gb"
    memory_gb: 80
    specs:
      cost_per_hour: 2.0        # $/hour
      perf_score: 1000          # Relative score
      power_watts: 400          # TDP (Watts)
      perf_per_watt: 2.5        # Calculated
      inference_affinity: 0.7   # 0-1
      training_affinity: 1.0    # 0-1
```

---

## Data Flow

```
┌──────────────────────────────────────────────────────────────────────┐
│                          Scheduling Flow                              │
└──────────────────────────────────────────────────────────────────────┘

                    ┌─────────────┐
                    │  Pending    │
                    │    Pod      │
                    └──────┬──────┘
                           │
                           ▼
                    ┌─────────────┐
                    │  Scheduling │
                    │    Queue    │
                    └──────┬──────┘
                           │
                           ▼
┌──────────────────────────────────────────────────────────────────────┐
│                      ScheduleOne()                                    │
│  ┌────────────────────────────────────────────────────────────────┐  │
│  │ 1. Pop pod from queue                                          │  │
│  │ 2. Get framework for pod                                       │  │
│  │ 3. Run scheduling cycle                                        │  │
│  └────────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────┘
                           │
                           ▼
┌──────────────────────────────────────────────────────────────────────┐
│                    schedulingCycle()                                  │
│  ┌─────────────┐    ┌─────────────┐    ┌─────────────┐              │
│  │   Filter    │───▶│    Score    │───▶│   Select    │              │
│  │   Phase     │    │    Phase    │    │    Best     │              │
│  └─────────────┘    └─────────────┘    └─────────────┘              │
│        │                  │                   │                      │
│        ▼                  ▼                   ▼                      │
│   NodeResources      CostEfficiency      Highest Score              │
│       Fit            PowerEfficiency         Node                    │
│                      PodAffinity                                     │
└──────────────────────────────────────────────────────────────────────┘
                           │
                           ▼
┌──────────────────────────────────────────────────────────────────────┐
│                      bindingCycle()                                   │
│  ┌────────────────────────────────────────────────────────────────┐  │
│  │ 1. Assume pod (optimistic binding)                             │  │
│  │ 2. Run bind plugin                                             │  │
│  │ 3. Update cache                                                │  │
│  └────────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────┘
                           │
                           ▼
                    ┌─────────────┐
                    │   Bound     │
                    │    Pod      │
                    └─────────────┘
```

---

## Score Calculation Example

### Scenario

- **Pod:** inference 워크로드, `job-name: ml-job-1`, `queue-name: inference-queue`
- **Affinity Mode:** `preferred`

### Node Scores

| Node | GPU | Cost Score | Power Score | Affinity Score | Total |
|------|-----|------------|-------------|----------------|-------|
| node-1 | A100 | 0 | 3.75 | 16 (3 same-job) | 19.75 |
| node-2 | MIG 1g.24gb | 0 | 3.75 | 10 | 13.75 |
| node-3 | T4 | 12.5 | 4.3 | 12 (2 same-queue) | 28.8 |
| node-4 | Warboy | 0 | 7.5 | 10 | 17.5 |

**결과:** `node-3` (T4) 선택 - 비용 효율성 + 적절한 affinity

---

## Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `ACCELERATOR_CONFIG_PATH` | Config file path | `/etc/scheduler/accelerator-config.yaml` |
| `KUBECONFIG` | Kubeconfig path (out-of-cluster) | `~/.kube/config` |

### Pod Annotations

| Annotation | Values | Description |
|------------|--------|-------------|
| `scheduler.affinity-mode` | `none`, `preferred`, `anti` | Pod 배치 전략 |

### Pod Labels

| Label | Description |
|-------|-------------|
| `job-name` | Job 이름 (affinity 계산용) |
| `queue-name` | Queue 이름 (affinity 계산용) |

### Node Labels (Fallback)

| Label | Description |
|-------|-------------|
| `accelerator.type` | GPU 타입 (gpu, mig, npu) |
| `accelerator.model` | GPU 모델 (a100, h100, warboy) |
| `accelerator.cost-per-hour` | 시간당 비용 (fallback) |
| `accelerator.perf-score` | 성능 점수 (fallback) |
| `accelerator.power-watts` | 전력 소비 (fallback) |

---

## Extending the Scheduler

### Adding a New Score Plugin

1. `internal/framework/plugin/` 에 새 파일 생성:

```go
package plugin

type MyPlugin struct {
    cache *utils.Cache
}

func NewMyPlugin(cache *utils.Cache) *MyPlugin {
    return &MyPlugin{cache: cache}
}

func (p *MyPlugin) Name() string {
    return "MyPlugin"
}

func (p *MyPlugin) Score(ctx context.Context, pod *v1.Pod, nodeName string) (int64, *utils.Status) {
    // 스코어 계산 로직
    return score, utils.NewStatus(utils.Success, "")
}

func (p *MyPlugin) ScoreExtensions() framework.ScoreExtensions {
    return p
}

func (p *MyPlugin) NormalizeScore(ctx context.Context, pod *v1.Pod, scores utils.PluginResult) *utils.Status {
    return utils.NewStatus(utils.Success, "")
}
```

2. `internal/config/config.go` 에서 플러그인 등록:

```go
scorePlugins := []framework.ScorePlugin{
    plugin.NewCostEfficiency(cache, metricsProvider),
    plugin.NewPowerEfficiency(cache, metricsProvider),
    plugin.NewPodAffinity(cache),
    plugin.NewMyPlugin(cache),  // 새 플러그인 추가
}
```

### Adding a New Accelerator

`config/accelerator-config.yaml` 에 추가:

```yaml
accelerators:
  - name: "New GPU Model"
    type: "gpu"
    vendor: "nvidia"
    model: "new-model"
    memory_gb: 48
    specs:
      cost_per_hour: 1.5
      perf_score: 600
      power_watts: 250
      perf_per_watt: 2.4
      inference_affinity: 0.8
      training_affinity: 0.9
```

---

## Deployment

### Kubernetes Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: cost-based-scheduler
  namespace: kube-system
spec:
  replicas: 1
  selector:
    matchLabels:
      app: cost-based-scheduler
  template:
    spec:
      serviceAccountName: cost-based-scheduler
      containers:
      - name: scheduler
        image: cost-based-scheduler:latest
        env:
        - name: ACCELERATOR_CONFIG_PATH
          value: /etc/scheduler/accelerator-config.yaml
        volumeMounts:
        - name: config
          mountPath: /etc/scheduler
      volumes:
      - name: config
        configMap:
          name: accelerator-config
```

### Using the Scheduler

Pod에서 커스텀 스케줄러 지정:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: my-inference-pod
  labels:
    job-name: inference-job-1
    queue-name: inference-queue
  annotations:
    scheduler.affinity-mode: preferred
spec:
  schedulerName: cost-based-scheduler  # 커스텀 스케줄러 사용
  containers:
  - name: inference
    image: my-inference-image
    resources:
      requests:
        nvidia.com/gpu: 1
```
