# npu-operator (Helm Chart)

NVIDIA GPU + Furiosa(Warboy/RNGD) + Rebellions ATOM+ 를 단일 Operator 로
관리하는 Kubernetes NPU/GPU Operator 의 Helm 차트.

차트가 배포하는 것:
- **Operator**(controller-manager) Deployment + RBAC + (옵션) Leader election
- **NPUClusterPolicy** CR → operator 가 벤더별 **device-plugin DaemonSet** + **detector** 를 reconcile
- **DriverInstallPolicy** CR(벤더별) → operator 가 **driver 설치 DaemonSet** 을 reconcile (mode=daemonset)
- CRD(`npu.ai/*`) 4종, NVIDIA RuntimeClass, pre-upgrade hook(CRD apply / 구 DS cleanup)

> 드라이버는 호스트에 설치되며, 이미 일치하는 버전이 깔려 있으면 **idempotent skip(무재부팅)**.

## Prerequisites

- Kubernetes ≥ 1.24, Helm ≥ 3.8 (OCI 사용 시)
- 컨테이너 런타임: containerd. 사설/HTTP 레지스트리 사용 시 노드에 insecure-registry 설정
- 차트가 참조하는 **이미지들을 클러스터 노드가 pull 가능**해야 함 (가장 흔한 실패: ImagePullBackOff)
- **노드 라벨** (보유 가속기에 맞게 부여):

  | 가속기 | 라벨 |
  |--------|------|
  | NVIDIA GPU | `nvidia.com/gpu.present=true` |
  | Furiosa Warboy | `furiosa=true` |
  | Furiosa RNGD | `furiosa-rngd=true` |
  | Rebellions ATOM+ | `rebellions-atom=true` |

## Installing

```bash
# 디렉토리에서
helm install npu-operator deploy/helm -n npu-operator --create-namespace

# 또는 OCI 레지스트리에서 (HTTP 레지스트리는 --plain-http)
helm install npu-operator oci://<registry>/charts/npu-operator \
  --version <chart-version> -n npu-operator --create-namespace --plain-http
```

설치 직후 확인:
```bash
helm list -n npu-operator
kubectl get pod -n npu-operator                       # controller-manager 1/1 Running
kubectl get npuclusterpolicy -A                       # Ready=True
# v0.5.23+: 신 이름 — kcloud-detector / {nvidia,furiosa,furiosa-rngd,rbln}-device-plugin / kcloud-*-driver
kubectl get ds -n kube-system | grep -E "kcloud-|device-plugin"
kubectl get nodes -o custom-columns='NODE:.metadata.name,GPU:.status.allocatable.nvidia\.com/gpu,RNGD:.status.allocatable.furiosa\.ai/rngd'
kubectl get driverupgradestate                        # 각 노드 Idle 이어야 정상
```

## Upgrading

```bash
helm upgrade npu-operator deploy/helm -n npu-operator --reuse-values --set image.tag=<new>
```
- `crdUpgrade.enabled=true`(기본): helm upgrade 시 operator 의 `apply-crds` 서브명령 Job 이 CRD 를
  자동 적용(Helm 은 crds/ 를 install 전용으로 다루므로). 비활성화 시 수동 `kubectl apply -f crds/` 필요.

## Uninstalling

> NPUClusterPolicy 에 finalizer(`npu.ai/cleanup`)가 있어 **CR 을 먼저** 삭제(operator 가 살아있는 동안)해야
> 정리가 끝납니다. operator 가 먼저 죽으면 CR 이 Terminating 에 고착됩니다.

```bash
kubectl delete npuclusterpolicy --all -n npu-operator   # device-plugin/detector 정리
kubectl delete driverinstallpolicy --all                # driver DS 정리 (v0.5.22+ 는 ownerRef 로 GC cascade)
helm uninstall npu-operator -n npu-operator             # operator/RBAC/RuntimeClass 제거
# (선택) 완전 클린 — helm 은 crds/ 를 지우지 않음
kubectl delete crd npuclusterpolicies.npu.ai driverinstallpolicies.npu.ai \
  driverupgradestates.npu.ai nodedevicereports.npu.ai
```
> 고착 CR: `kubectl patch <cr> -p '{"metadata":{"finalizers":[]}}' --type=merge`

## Configuration

주요 파라미터(전체는 `values.yaml` 참조):

| 파라미터 | 기본값 | 설명 |
|----------|--------|------|
| `image.repository` / `image.tag` | `…/npu-operator` / `v0.5.23` | operator 이미지 |
| `image.pullPolicy` | `IfNotPresent` | |
| `imagePullSecrets` | `[]` | 사설 레지스트리 인증 시 |
| `deployClusterPolicy` | `true` | NPUClusterPolicy CR 자동 생성 |
| `detector.image` | `…/npu-detector:0.4.3` | 노드 디바이스 감지기 |
| `nvidia.enabled` / `nvidia.devicePluginImage` | `true` / `nvcr.io/nvidia/k8s-device-plugin:v0.17.1` | NVIDIA device-plugin |
| `furiosa.enabled` / `furiosa.devicePluginImage` | `true` / `ghcr.io/furiosa-ai/k8s-device-plugin:0.10.1` | Warboy device-plugin |
| `furiosa.rngd.enabled` | `true` | RNGD device-plugin |
| `furiosa.rngd.devicePluginImage` | `…/kcloud/furiosa-device-plugin-mi:v0.1.0` | ⚠️ partition 사용 시 **파티션 지원 `-mi` 이미지 필수** |
| `furiosa.rngd.partitionPolicy` | `dual-core` | `none`(1)/`quad-core`(2)/`dual-core`(4)/`single-core`(8) instance/card |
| `rebellions.enabled` / `rebellions.devicePluginImage` | `true` / `…/rebellions/k8s-device-plugin:v0.3.6` | ATOM+ device-plugin |
| `driverInstallPolicies.<vendor>.enabled` | `true` | 벤더별 driver 설치 정책 CR 생성 |
| `driverInstallPolicies.<vendor>.driver.version` / `.image` | 벤더별 | 설치할 드라이버 버전·이미지 |
| `driverInstallPolicies.<vendor>.driver.mode` | `daemonset` | (job 모드는 legacy) |
| `driverInstallPolicies.<vendor>.upgradePolicy.autoUpgrade` | `true` | 버전 불일치 시 자동 업그레이드 |
| `driverInstallPolicies.<vendor>.verifiedVersions` | 벤더별 | 검증된 버전 화이트리스트(비면 검증 skip) |
| `crdUpgrade.enabled` | `true` | upgrade 시 CRD 자동 적용 Job |
| `leaderElection.enabled` | `true` | controller-runtime Lease |
| `resources` | 100m/128Mi ~ 500m/256Mi | operator 리소스 |

벤더 `<vendor>` = `nvidia` / `furiosa`(Warboy) / `rngd`.

## 레지스트리 구성 (Registry Configuration)

### 개요

차트는 **한 줄로 레지스트리 주소를 바꾸는** `global.registry` 메커니즘을 지원합니다.

- **기본값**: `<your-registry>` (placeholder — 실제 IP 노출 없음)
- **사설 클러스터 배포**: `--set global.registry=<host:port>` 또는 `deploy.env` + `install.sh` 사용
- **공개 이미지** (nvcr.io, ghcr.io, bitnami/kubectl): `global.registry` prefix 미적용 (그대로 두면 public 레지스트리 사용)

### 빠른 시작 (Quick Install)

#### 옵션 A: 한 줄 --set (CLI)

```bash
helm install npu-operator deploy/helm -n npu-operator --create-namespace \
  --set global.registry=<your-registry>/kcloud
```

#### 옵션 B: deploy.env + install.sh (권장 — 반복 배포)

1. `deploy.env` 파일 준비:
```bash
cp deploy/helm/deploy.env.example deploy.env
vi deploy.env    # REGISTRY=<host:port> 설정 필수
```

2. `install.sh` 실행:
```bash
bash deploy/helm/install.sh            # 실제 설치
bash deploy/helm/install.sh --dry-run  # 미리보기
```

`install.sh`는 `deploy.env`에서 `REGISTRY` 값을 읽어 `--set global.registry=...` 로 자동 주입합니다.

#### 옵션 C: airgap/사설 미러 (모든 이미지 단일 미러)

`values-airgap.example.yaml` 사용:

```bash
cp deploy/helm/values-airgap.example.yaml values-airgap.yaml
# <your-registry> 를 실제 미러 주소(예: registry.internal:5000)로 치환
vi values-airgap.yaml

helm install npu-operator deploy/helm -n npu-operator --create-namespace \
  -f values-airgap.yaml
```

> 또는 `deploy.env` 에서 EXTRA_ARGS 로 override:
> ```bash
> REGISTRY=registry.internal:5000
> EXTRA_ARGS="-f values-airgap.yaml"
> bash deploy/helm/install.sh
> ```

### 업그레이드 시 레지스트리 변경

```bash
# 방법 1: --set (한 줄)
helm upgrade npu-operator deploy/helm -n npu-operator --reuse-values \
  --set global.registry=new-registry.internal:5000

# 방법 2: deploy.env (권장)
# 1. deploy.env 의 REGISTRY 변경
# 2. bash deploy/helm/install.sh --reuse-values
```

## Examples

```bash
# 특정 벤더만 끄기 (예: Rebellions 비활성)
helm install npu-operator deploy/helm -n npu-operator --create-namespace \
  --set rebellions.enabled=false --set driverInstallPolicies.rebellions.enabled=false

# RNGD 파티션 변경 (1 instance/card)
helm upgrade npu-operator deploy/helm -n npu-operator --reuse-values \
  --set furiosa.rngd.partitionPolicy=none

# 외부(다른) 레지스트리로 배포 — 레지스트리 경로 override (-f values 파일 권장)
helm install npu-operator oci://<reg>/charts/npu-operator --version <v> \
  -n npu-operator --create-namespace --plain-http -f values-new-registry.yaml
```

## Notes / Gotchas

- **RNGD partition**: `partitionPolicy != none` 은 operator 가 device-plugin 에 `--policy` 를 전달 →
  반드시 `furiosa-device-plugin-mi`(파티션 지원) 이미지 사용. 일반 이미지는 `--policy` 미지원 → CrashLoop.
- **driver DS 수명주기**(v0.5.22+): driver DaemonSet 은 DriverInstallPolicy 에 ownerReference 가 걸려
  DIP 삭제 시 K8s GC 가 cascade 삭제.
- **레지스트리 도달성**: 다른 클러스터 배포 시 노드가 이미지 레지스트리에 도달 가능해야 함. 상세 절차는
  운영 노트(`operator/tester.md` §5) 참조.
- **CRD**: helm uninstall 은 `crds/` 의 CRD 를 삭제하지 않음(의도된 동작).
