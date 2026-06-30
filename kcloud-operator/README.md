# kcloud operator

## 📖 Overview

`kcloud operator`는 Kubernetes 환경에서 **NPU/GPU 가속기 장치의 드라이버 및 디바이스 플러그인 배포를 자동화**하기 위한 Kubernetes Operator입니다.

단일 CRD(`NPUClusterPolicy`)를 통해 4개 벤더(NVIDIA, Furiosa Warboy, Furiosa RNGD, Rebellions ATOM)의 디바이스 플러그인을 통합 관리합니다.

**핵심 기능:**
- 노드 라벨 기반 자동 감지 및 벤더별 DaemonSet 자동 생성
- CRD 기반 드라이버 설치 정책(`DriverInstallPolicy`)으로 버전 검증 및 자동 업그레이드
- 사설 레지스트리 지원 (`global.registry` 설정으로 이미지 경로 prefix 지정)
- Helm Chart 기반 설치 (`deploy/helm`, Chart v0.5.16, appVersion v0.5.24)

---

## Prerequisites

- **Kubernetes**: v1.24+
- **Helm**: 3.8+
- **kubectl**: 1.28+
- **Container runtime**: containerd (사설/HTTP 레지스트리 시 insecure-registry 설정 필요)
- **Go**: 1.24+ (소스 빌드 시)
- **Docker**: 17.03+ (이미지 빌드 시)

**Node labels**:

| 가속기 | 라벨 |
|--------|------|
| NVIDIA GPU | `nvidia.com/gpu.present=true` |
| Furiosa Warboy | `furiosa=true` |
| Furiosa RNGD | `furiosa-rngd=true` |
| Rebellions ATOM | `rebellions-atom=true` |

---

## Build

### 빌드 및 푸시

소스에서 operator 이미지를 빌드하려면:

```bash
make docker-build docker-push IMG=<registry>/npu-operator:v0.5.24 CONTAINER_TOOL="sudo docker"
```

- `IMG`: 완전한 레지스트리 경로 (예: `<your-registry>/kcloud/npu-operator:v0.5.24`)
- `CONTAINER_TOOL`: 기본값 `docker` (필요시 `sudo docker` 또는 `podman`)

---

## Deploy

### 주 흐름: Helm 차트 (Source 기반)

operator는 `deploy/helm` 에 Helm 차트를 포함합니다.

#### 옵션 A: helm 직접 실행

```bash
helm upgrade --install npu-operator deploy/helm -n npu-operator --create-namespace \
  --set global.registry=<host:port>
```

#### 옵션 B: 래퍼 스크립트 (반복 배포 시 권장)

```bash
# 1. 환경 파일 준비
cp deploy/helm/deploy.env.example deploy/helm/deploy.env
vi deploy/helm/deploy.env    # REGISTRY=<host:port> 설정

# 2. 설치
bash deploy/helm/install.sh
```

#### 옵션 C: airgap/사설 미러

모든 이미지를 단일 사설 미러로 배포하려면:

```bash
cp deploy/helm/values-airgap.example.yaml values-airgap.yaml
vi values-airgap.yaml    # <your-registry> 를 실제 주소로 변경

helm upgrade --install npu-operator deploy/helm -n npu-operator --create-namespace \
  -f values-airgap.yaml
```

**레지스트리 메커니즘:**
- `global.registry`: 사설 이미지(operator/detector/device-plugin/driver) 의 prefix
  - 기본값: `<your-registry>` (placeholder)
  - 공개 이미지(nvcr.io, ghcr.io) 에는 적용되지 않음
- 비어있으면("") 사설 이미지는 Docker Hub 기본값으로 렌더됨

### 설치 후 확인

```bash
# Operator pod 확인 (1/1 Running)
kubectl get pod -n npu-operator

# NPUClusterPolicy CR 확인 (Ready=True)
kubectl get npuclusterpolicy -A

# 벤더별 device-plugin DaemonSet 및 driver DaemonSet 확인
kubectl get ds -n kube-system | grep -E "kcloud-|device-plugin"

# 노드 allocatable 리소스 확인
kubectl get nodes -o custom-columns='NODE:.metadata.name,GPU:.status.allocatable.nvidia\.com/gpu,RNGD:.status.allocatable.furiosa\.ai/rngd'

# 드라이버 업그레이드 상태 확인 (모두 Idle 이어야 정상)
kubectl get driverupgradestate
```

---

## Uninstall

NPUClusterPolicy 에는 finalizer(`npu.ai/cleanup`)가 있어 안전한 정리를 위해 **CR 을 먼저 삭제**해야 합니다.

```bash
# 1. Custom Resource 삭제 (operator가 finalizer 처리하는 동안 대기)
kubectl delete npuclusterpolicy --all -n npu-operator
kubectl delete driverinstallpolicy --all

# 2. orphan driver DaemonSet 정리 (필요시)
kubectl delete ds -n kube-system -l app.kubernetes.io/component=driver

# 3. Helm uninstall
helm uninstall npu-operator -n npu-operator

# 4. (선택) 완전 클린 — CRD 삭제
kubectl delete crd npuclusterpolicies.npu.ai driverinstallpolicies.npu.ai \
  driverupgradestates.npu.ai nodedevicereports.npu.ai
```

**래퍼 스크립트 사용:**

```bash
bash deploy/helm/uninstall.sh              # CR + helm uninstall
bash deploy/helm/uninstall.sh --purge-crds # + CRD 삭제
```

---

## Configuration

주요 Helm 파라미터 (전체는 `deploy/helm/values.yaml` 참조):

| 파라미터 | 기본값 | 설명 |
|----------|--------|------|
| `global.registry` | `<your-registry>` | 사설 이미지 레지스트리 prefix |
| `image.tag` | `v0.5.24` | operator 이미지 태그 |
| `nvidia.enabled` | `true` | NVIDIA device-plugin 활성화 |
| `furiosa.enabled` | `true` | Furiosa Warboy device-plugin 활성화 |
| `furiosa.rngd.enabled` | `true` | Furiosa RNGD device-plugin 활성화 |
| `furiosa.rngd.partitionPolicy` | `dual-core` | RNGD 파티션 정책 (none/quad-core/dual-core/single-core) |
| `rebellions.enabled` | `true` | Rebellions ATOM device-plugin 활성화 |
| `deployClusterPolicy` | `true` | NPUClusterPolicy CR 자동 생성 |
| `driverInstallPolicies.<vendor>.enabled` | `true` | 벤더별 driver 설치 정책 CR 생성 |

벤더별 driver 버전 설정:

```bash
# Furiosa Warboy driver 버전 변경
helm upgrade npu-operator deploy/helm -n npu-operator --reuse-values \
  --set driverInstallPolicies.furiosa.driver.version=1.9.8-3

# RNGD 파티션 정책 변경 (1 instance/card)
helm upgrade npu-operator deploy/helm -n npu-operator --reuse-values \
  --set furiosa.rngd.partitionPolicy=none
```

---

## Custom Resources

### NPUClusterPolicy

device-plugin 과 detector 를 관리합니다.

```yaml
apiVersion: npu.ai/v1alpha1
kind: NPUClusterPolicy
metadata:
  name: npuclusterpolicy-sample
spec:
  nvidia:
    enabled: true
    devicePluginImage: "nvcr.io/nvidia/k8s-device-plugin:v0.17.1"
  furiosa:
    enabled: true
    devicePluginImage: "ghcr.io/furiosa-ai/k8s-device-plugin:0.10.1"
    rngd:
      enabled: true
      devicePluginImage: "<your-registry>/furiosa-device-plugin-mi:v0.1.0"
      partitionPolicy: "dual-core"
  rebellions:
    enabled: true
    devicePluginImage: "<your-registry>/rebellions/k8s-device-plugin:v0.3.6"
```

### DriverInstallPolicy

드라이버 설치 및 자동 업그레이드를 관리합니다.

```yaml
apiVersion: npu.ai/v1alpha1
kind: DriverInstallPolicy
metadata:
  name: furiosa-warboy-ds
spec:
  vendor: furiosa
  model: warboy
  driver:
    version: "1.9.8-3"
    mode: daemonset
  rebootStrategy: IfNeeded
  verifiedVersions:
    - "1.7.8"
    - "1.9.8-3"
    - "1.9.9-3"
```

---

## 관련 문서

- `deploy/helm/README.md` — 레지스트리 구성, airgap 배포, Helm 파라미터 상세
- `UPGRADE.md` — Operator 버전 업그레이드 가이드
- `../tester.md` — 라이브 라이프사이클 가이드 (배포 검증, 문제 해결)

---

## 🛠 Development Notes

- **CRD**: `NPUClusterPolicy`, `DriverInstallPolicy`, `DriverUpgradeState`, `NodeDeviceReport` (npu.ai/v1alpha1)
- **Controller**: `NPUClusterPolicyReconciler`, `DriverInstallPolicyReconciler`
- **관리 대상**: Device-plugin DaemonSet, driver DaemonSet, detector, RuntimeClass
- **RBAC**: DaemonSet, ConfigMap, Pod 생성/삭제 권한

---

## 🤝 Contributing

1. Fork the repository
2. Create a feature branch
3. Submit a PR with test results

---

## 📚 References

- [Kubebuilder Documentation](https://book.kubebuilder.io)
- [Operator SDK](https://sdk.operatorframework.io)
- [NVIDIA k8s-device-plugin](https://github.com/NVIDIA/k8s-device-plugin)
- [Furiosa Device Plugin](https://github.com/furiosa-ai/furiosa-device-plugin)
- [Rebellions Device Plugin Documentation](https://docs.rebellions.ai)

---

## 📄 License

Apache License 2.0