# keti-cost-scheduler Architecture

## Overview

keti-cost-scheduler(이전 이름 cost-based-scheduler)는 Kubernetes 클러스터에서 GPU/NPU 워크로드를 비용·전력 효율 기준으로 배치하는 커스텀 스케줄러입니다. `schedulerName: keti-cost-scheduler`인 Pod를 처리하며, 기존 매니페스트 마이그레이션 기간에는 `cost-based-scheduler`도 함께 받습니다.

플러그인은 `internal/config/config.go`에서 다음 순서로 등록됩니다.

```
PreFilter ─▶ Filter ─▶ (HAMi Filter) ─▶ PreScore ─▶ Score ─▶ Select ─▶ Permit ─▶ Bind
    │          │                          │           │          │          │        │
    ▼          ▼                          ▼           ▼          ▼          ▼        ▼
Duration   TaintToleration           CatalogAware  CostEfficiency  최고점   Gang     DefaultBinder
Aware      NodeSelector              Score         PowerEfficiency 노드     Scheduling (CPU)
           NodeResourcesFit          (Catalog 조회) PodAffinity    선택 +            HAMi Bind
           AcceleratorModelFit                     BinPacking     배치 결정          (GPU 분할)
           + 필수 배치 검사 7종                     DurationAware  기록
                                                   WorkloadAwareCost
                                                   WorkloadAwarePower
                                                   CatalogAwareScore
```

| 단계 | 플러그인 | 역할 |
|---|---|---|
| PreFilter | DurationAwareScheduling | long/short 클래스 노드 확인 |
| Filter | TaintToleration | NoSchedule/NoExecute taint 노드 제외 |
| Filter | NodeSelector | nodeSelector / nodeName 조건 |
| Filter | NodeResourcesFit | CPU·Memory·GPU·NPU 요청량 |
| Filter | AcceleratorModelFit | `sched.ai` GPU/NPU 모델과 Node 모델 일치 |
| PreScore | CatalogAwareScore | 후보 노드별 Catalog 조회를 시도 단위로 한 번 준비 |
| Score | CostEfficiency (0–50) | 추론당 비용 |
| Score | PowerEfficiency (0–30) | 와트당 성능 |
| Score | PodAffinity (0–20) | job/queue 동일 배치 또는 분산 |
| Score | BinPacking (0–100) | 노드 집적률 |
| Score | DurationAwareScheduling (0–20) | 장기/단기 클래스 일치 |
| Score | WorkloadAwareCost (0–25) | 예측 비용 보완 |
| Score | WorkloadAwarePower (0–15) | 예측 전력 보완 |
| Score | CatalogAwareScore | 실측 Profile 기반 점수. 기본은 Shadow(가중치 0) |
| Permit | GangScheduling | `sched.ai/gang-size` 기준 묶음 배치 |
| Bind | DefaultBinder / HAMi | CPU 작업은 DefaultBinder, GPU 분할 작업은 HAMi Extender |

DurationAwareScheduling은 Node에 long/short 클래스 Label이 있을 때만 점수 차이를 만듭니다.

---

## Directory Structure

```
cost-based-scheduler/
├── cmd/main.go                       # 엔트리포인트
├── config/accelerator-config.yaml    # GPU/NPU 스펙 설정
├── deployments/                      # Kubernetes 매니페스트
├── internal/
│   ├── acceleratorfit/               # GPU/NPU 모델 일치 판단
│   ├── backend/{heap,log,queue}/     # 스케줄링 큐·로깅
│   ├── catalogclient/                # Catalog API 클라이언트(조회·캐시·시간초과)
│   ├── config/config.go              # 플러그인 등록과 스케줄러 설정
│   ├── decisionrecorder/             # 배치 결정 JSONL 비동기 기록
│   ├── fixture/                      # 오프라인 시험 데이터 로더
│   ├── framework/
│   │   ├── interface.go              # PreFilter·Filter·PreScore·Score·Permit·Bind 인터페이스
│   │   ├── framework.go              # 단계별 플러그인 실행
│   │   ├── plugin/                   # 플러그인 구현
│   │   └── utils/                    # 캐시·노드 정보·시도 상태
│   ├── placementchecks/              # 필수 배치 검사 7종
│   ├── scheduler/                    # 스케줄링 사이클·이벤트·단계별 시간 기록
│   └── workloadcontext/              # Catalog 조회 키(environment_key) 생성
└── scripts/                          # 빌드·배포·로그 확인 스크립트
```

---

## Core Components

### 1. Scheduler

`internal/scheduler`는 Pod 이벤트를 받아 큐에 넣고, `ScheduleOne`으로 한 Pod씩 배치합니다.

- Informer 기반 Pod/Node 이벤트 처리
- 스케줄링 큐와 재시도
- 배치 결정 기록(`SCHED_DECISION_JSONL`)과 단계별 시간 기록

GPU 실측 수집기(`fetchNodeGPUMetrics`)는 아직 연결되지 않았습니다. 실측값이 없으면 관련 검사는 UNEVALUATED로 남습니다.

### 2. Framework

```go
type Framework interface {
    RunPreFilterPlugins(ctx, pod) *Status
    RunFilterPlugins(ctx, pod, nodeInfo) PluginResultMap
    RunPreScorePlugins(ctx, pod, nodes) *Status
    RunScorePlugins(ctx, pod, nodes) (PluginResultMap, *Status)
    CommitPlacementDecisions(pod, selectedHost, selectErr)
    RunPermitPlugins(ctx, pod, nodeName) *Status
    RunBindPlugin(ctx, pod, nodeName) *Status
}
```

PreScore는 `PreScorePlugin`을 함께 구현한 Score 플러그인만 실행합니다. PreScore에서 준비한 Catalog 결과는 같은 시도의 Score가 그대로 사용하며, Score에서 HTTP를 다시 호출하지 않습니다. 조회 실패·누락·시간초과도 같은 시도 안에서는 재조회하지 않고 미평가로 처리합니다.

### 3. Cache

노드·Pod 상태와 배치 예정(assumed) Pod를 메모리에 유지합니다.

### 4. AcceleratorMetricsProvider

`accelerator-config.yaml`의 GPU/NPU 스펙(비용·성능·전력)을 Score 플러그인에 제공합니다.

---

## Scheduling Cycle

1. **PreFilter**: Pod 단위 사전 조건 확인
2. **Filter**: 노드별 조건 확인과 필수 배치 검사 7종 평가. GPU 분할 요청은 HAMi Extender Filter를 추가로 거침
3. **PreScore**: Catalog 조회 준비(조회 키 기준 중복 제거)
4. **Score**: 플러그인 점수 합산. 정규화는 `ScoreExtensions`에서 수행
5. **Select**: 최고점 노드 선택, 배치 결정 기록
6. **Permit**: Gang 조건 확인(대기·거부 가능)
7. **Bind**: DefaultBinder 또는 HAMi Bind. 실패하면 backoff 큐로 재시도

### 필수 배치 검사

`internal/placementchecks`는 다음 순서로 7종을 평가합니다. 결과는 PASS / REJECT / UNEVALUATED입니다.

| 순서 | ID | 내용 |
|---|---|---|
| 1 | accelerator | 장치 종류·모델 |
| 2 | memory-headroom | 장치 메모리 여유(`memory_safety_margin_mib`) |
| 3 | runtime | 런타임 조건 |
| 4 | topology | 장치 배치 조건 |
| 5 | thermal-power | 온도·전력 상한(`max_gpu_temp_c`, `max_gpu_power_w`, `max_server_power_w`) |
| 6 | data-freshness | 측정값 수집 시각(`max_age_seconds`, `max_future_skew_seconds`) |
| 7 | policy | 운영 정책 |

검사별 모드는 `off`·`record`·`enforce`이며 기본값은 `record`입니다. `record`는 결과만 기록하고 배치를 막지 않습니다. 설정 파일은 `SCHED_REQUIRED_CHECKS_CONFIG`로 지정하며, 파일 오류가 있으면 스케줄러가 시작하지 않습니다. 메모리 여유값을 정하지 않으면 memory-headroom은 UNEVALUATED입니다.

### 큐 재시도

배치하지 못한 Pod는 unschedulable 큐에서 기다립니다. 다음 이벤트가 오면 active/backoff 큐로 다시 옮깁니다.

- Node 추가·변경
- 배정된 Pod의 종료(Succeeded/Failed) 또는 삭제
- unschedulable 큐에서 5분 넘게 기다린 Pod(30초마다 확인)

`SCHED_POD_MAX_SCHEDULING_ATTEMPTS`로 Pod별 최대 시도 횟수를 제한할 수 있습니다.

### 단계별 시간 기록

`SCHED_DECISION_JSONL`을 지정하면 시도마다 `prefilter_ms`, `filter_ms`, `hami_filter_ms`, `prescore_ms`, `score_ms`, `select_ms`, `bind_ms`, `attempt_to_terminal_ms`를 기록합니다. 기록은 별도 고루틴의 큐(`SCHED_DECISION_QUEUE_SIZE`, 기본 256)로 쓰므로 스케줄링 경로를 막지 않습니다. 이 값은 검증용이며, 기본 스케줄러와 같은 측정 경계를 맞추기 전에는 공식 성능 지표로 쓰지 않습니다.

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
| `ACCELERATOR_CONFIG_PATH` | GPU/NPU 스펙 설정 파일 | `/etc/scheduler/accelerator-config.yaml` |
| `KUBECONFIG` | 클러스터 밖에서 실행할 때 kubeconfig | `~/.kube/config` |
| `SCHED_REQUIRED_CHECKS_CONFIG` | 필수 배치 검사 설정 파일 | 없음(모든 검사 `record`) |
| `SCHED_POD_MAX_SCHEDULING_ATTEMPTS` | Pod별 최대 시도 횟수 | 제한 없음 |
| `SCHED_DECISION_JSONL` | 배치 결정·단계별 시간 기록 파일 | 없음(기록 안 함) |
| `SCHED_DECISION_QUEUE_SIZE` | 기록 큐 크기 | `256` |
| `SCHED_CATALOG_MODE` | Catalog 연동: `off`, `shadow`, `enforce` | `off` |
| `SCHED_CATALOG_URL` | Catalog API 주소 | 없음 |
| `SCHED_CATALOG_TIMEOUT_MS` | Catalog 조회 시간초과 | `100` |
| `SCHED_CATALOG_CACHE_TTL_SEC` | Catalog 조회 캐시 유지 시간 | `30` |
| `SCHED_SHADOW_SCORE_WEIGHT` | CatalogAwareScore 반영 가중치 | `0` |
| `SCHED_PREDICTION_MIN_CONFIDENCE` | 점수에 쓸 최소 신뢰 등급 | `MEASURED` |
| `SCHED_EXPERIMENT_ID` | 배치 결정 기록에 붙는 실험 ID | 없음 |

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
  name: keti-cost-scheduler
  namespace: kcp-control-plane
spec:
  replicas: 1
  selector:
    matchLabels:
      app: keti-cost-scheduler
  template:
    spec:
      serviceAccountName: keti-cost-scheduler
      containers:
      - name: scheduler
        image: ketidevit2/keti-cost-scheduler:0.4.2
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
  schedulerName: keti-cost-scheduler  # 커스텀 스케줄러 사용
  containers:
  - name: inference
    image: my-inference-image
    resources:
      requests:
        nvidia.com/gpu: 1
```
