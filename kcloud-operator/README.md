<!-- README.md: kcloud operator 저장소 안내 — 지원 가속기, 빌드, 배포, CRD 목록, 개발 진입점 | 생성일: 2025-07-04 | 수정일: 2026-10-28 -->
<!-- cdocs-profile: blog -->

# kcloud operator

`kcloud operator` 는 Kubernetes 클러스터의 NPU·GPU 가속기를 관리하는 Kubernetes Operator 입니다.  
클러스터 관리자가 CR 을 등록하면 operator 가 노드마다 드라이버를 설치하고, 벤더 device-plugin 을  
배치하고, 장치 분할·공유·health 상태를 유지합니다.

관리 대상은 다섯 벤더입니다. 노드의 장치 감지는 각 노드에 상주하는 `kcloud-node-manager` 가 맡고,  
operator 는 그 결과인 `NodeDeviceReport` 와 `kcloud.ai/*` 노드 라벨을 읽어 DaemonSet 과 Job 을  
만듭니다. 설치 산출물은 `deploy/helm` 의 Helm 차트입니다.

## 🚀 빠른 설치

차트와 이미지는 `ghcr.io/openkcloud` 에 공개돼 있어 helm 명령 하나로 설치합니다. 클러스터의  
Kubernetes 버전에 맞는 명령 하나를 고릅니다.

```bash
# Kubernetes 1.31~1.34
helm upgrade --install kcloud-operator oci://ghcr.io/openkcloud/charts/kcloud-operator \
  --version 0.8.4 -n kcloud --create-namespace \
  -f https://raw.githubusercontent.com/openkcloud/kcloud-operator/main/deploy/helm/values-k8s1.34.yaml

# Kubernetes 1.26~1.30
helm upgrade --install kcloud-operator oci://ghcr.io/openkcloud/charts/kcloud-operator \
  --version 0.6.3 -n kcloud --create-namespace \
  -f https://raw.githubusercontent.com/openkcloud/kcloud-operator/release/k8s-1.28/deploy/helm/values-k8s1.28.yaml
```

```bash
kubectl -n kcloud get pods                       # operator 와 kcloud-node-manager 가 Running
kubectl get nodes -L kcloud.ai/nvidia.present    # 장치가 있는 노드에 벤더 라벨이 붙는다
helm uninstall kcloud-operator -n kcloud         # 가속기를 쓰는 Pod 가 없을 때만 삭제된다
```

노드의 장치를 `kcloud-node-manager` 가 감지해 그 벤더의 드라이버와 device-plugin 만 배치합니다.  
사설 미러에서 받으려면 [Deploy](#-deploy) 절의 레지스트리 값을 더합니다.

위 브랜치 주소는 최신 프리셋을 확인하는 용도입니다. Magnum 같은 운영 자동화는 내용이 바뀌지 않도록
`deploy/integration/release.yaml` 의 커밋 SHA 로 고정된 `valuesURL` 을 씁니다.

사전 확인부터 자원 광고 확인까지의 단계별 절차는 [deploy/helm/INSTALL.md](deploy/helm/INSTALL.md) 에 있습니다.

---

## 🧩 지원 가속기

| 가속기 | allocatable 리소스명 | node-manager 자동 라벨 | Helm 값 블록 |
|--------|----------------------|------------------------|--------------|
| NVIDIA GPU | `nvidia.com/gpu` | `kcloud.ai/nvidia.present` | `nvidia` |
| Furiosa Warboy | `beta.furiosa.ai/npu` | `kcloud.ai/furiosa.present` | `furiosa` |
| Furiosa RNGD | `furiosa.ai/rngd` | `kcloud.ai/rngd.present` | `furiosa.rngd` |
| Rebellions ATOM | `rebellions.ai/ATOM` | `kcloud.ai/rebellions.present` | `rebellions` |
| Tenstorrent Blackhole | `tenstorrent.com/blackhole` | `kcloud.ai/tenstorrent.present` | `tenstorrent` |

라벨은 `kcloud-node-manager` 가 PCI 조회 결과로 직접 붙이므로 관리자가 직접 부여하지 않습니다.  
NFD 설치 여부와도 무관합니다.

---

## 📋 Prerequisites

- **Kubernetes**: `main` 브랜치 릴리스는 1.31~1.34, `release/k8s-1.28` 브랜치 릴리스는 1.26~1.30.
- **Helm**: 3.8+
- **Container runtime**: containerd. 사설 HTTP 레지스트리를 쓰면 노드에 insecure-registry 설정 필요
- **Go**: 1.24.5+ (소스 빌드 시, `go.mod`)
- **Container tool**: docker 또는 podman (이미지 빌드 시)

---

## 🔨 Build

operator 매니저 이미지를 소스에서 빌드합니다.

```bash
make docker-build docker-push IMG=<registry>/kcloud/kcloud-operator:<tag> CONTAINER_TOOL="sudo docker"
```

- `IMG`: 완전한 레지스트리 경로
- `CONTAINER_TOOL`: 기본값 `docker`

`docker-build` 는 생성된 CRD 를 `internal/crdapply/crd/` 로 복사하는 `embed-crds` 를 먼저 실행합니다.  
Helm 은 `crds/` 를 install 에서만 처리하므로, upgrade 때는 차트의 pre-upgrade Job 이 같은  
operator 이미지의 `apply-crds` 서브명령으로 이 embed 된 CRD 를 반영합니다(`crdUpgrade.enabled`, 기본 켜짐).  
CRD 를 고친 뒤에는 이미지를 다시 빌드해야 upgrade 경로가 새 스키마를 반영합니다.

operator 가 배포하는 드라이버 설치 이미지와 device-plugin 이미지는 이 저장소가 아니라 별도  
저장소(`images/kcloud-operator-images`)에서 빌드하며, Helm 값의 태그로 결합됩니다.

CLI 플러그인은 `cmd/kubectl-npu` 의 독립 모듈입니다.

```bash
cd cmd/kubectl-npu && go build -o kubectl-npu .
```

---

## 📦 Deploy

### 옵션 A: 공개 GHCR 에서 helm 직접 실행

```bash
helm upgrade --install kcloud-operator oci://ghcr.io/openkcloud/charts/kcloud-operator \
  --version 0.8.4 -n kcloud --create-namespace \
  -f deploy/helm/values-k8s1.34.yaml
```

차트 버전과 프리셋의 대응은 [빠른 설치](#-빠른-설치) 와 같습니다. 프리셋은 저장소 브랜치의 raw URL 로도  
받을 수 있으므로 소스 체크아웃 없이 `-f <URL>` 로 지정할 수 있습니다.

기본값으로 kcloud 가 빌드한 이미지는 `ghcr.io/openkcloud/<이름>:<태그>` 에서, 벤더 이미지는 각  
벤더의 공개 레지스트리(`nvcr.io`, `docker.io/furiosaai` 등)에서 받습니다. 소스 체크아웃에서 설치할  
때는 차트 경로 `deploy/helm` 을 대신 지정합니다.

K8s 1.26~1.30 클러스터에는 차트 0.6.x 와 `values-k8s1.28.yaml` 프리셋을 씁니다. 그 프리셋은 차트  
0.6.x 를 담은 `release/k8s-1.28` 브랜치에 있고 이 차트(0.8.x)에는 없습니다 — 0.8.x 는 `kubeVersion`  
으로 1.31 미만을 거절하므로 1.26~1.30 클러스터에 설치되지 않습니다. 프리셋은 그 릴리스 라인의  
operator 이미지 태그와 기능 토글을 담으므로, 프리셋 없이 설치하면 라이브와 다른 구성이 배포됩니다.

### 옵션 B: 래퍼 스크립트

```bash
cp deploy/helm/deploy.env.example deploy/helm/deploy.env
vi deploy/helm/deploy.env    # REGISTRY(kcloud 이미지 미러)·VENDOR_REGISTRY(벤더 이미지 미러) 설정
bash deploy/helm/install.sh
```

`install.sh` 는 `deploy.env` 의 `REGISTRY` 를 `global.registry` 로, `VENDOR_REGISTRY` 를  
`global.vendorRegistry` 로 주입합니다(`VENDOR_REGISTRY` 가 비면 `REGISTRY` 를 같이 씁니다). 릴리스명과  
네임스페이스는 `deploy.env` 의 `RELEASE`, `NAMESPACE` 가 정하고, 두 값이 없을 때만 스크립트가  
`kcloud-operator`·`kcloud` 를 씁니다. `deploy.env.example` 도 같은 두 값을 담으므로 예시를  
그대로 복사해도 스크립트 기본값과 일치합니다.  
로컬에 `deploy/helm/values-dev.yaml` 을 두면 `-f` 로 함께 병합합니다(예시는 `values-dev.example.yaml`, 그 파일은 커밋하지 않습니다).

### 옵션 C: 폐쇄망(airgap) 사설 미러

```bash
cp deploy/helm/values-airgap.example.yaml values-airgap.yaml
vi values-airgap.yaml    # <your-registry> 를 실제 주소로 변경

helm upgrade --install kcloud-operator deploy/helm -n kcloud --create-namespace \
  -f values-airgap.yaml
```

### 레지스트리 설정 규칙

레지스트리 값은 둘입니다.

| 값 | 대상 | 기본값 | 조립 결과 |
|---|---|---|---|
| `global.registry` | kcloud 가 빌드한 이미지(operator, node-manager, kcloud-host-exec, 드라이버 설치기, Tenstorrent device-plugin, Furiosa exporter) | `ghcr.io/openkcloud` | `<global.registry>/<repository>:<tag>` |
| `global.vendorRegistry` | 벤더 이미지(RNGD device-plugin, MPS control daemon, DRA 드라이버, CRD 갱신용 kubectl 등) | 빈 값 | 비면 각 값의 `upstream` 호스트 그대로, 채우면 `<global.vendorRegistry>/<org>/<image>:<tag>` |

air-gap 클러스터는 두 값을 모두 자체 미러로 지정합니다. `nvidia.devicePluginImage` 처럼 완성 경로를 받는  
필드는 조립하지 않으므로 미러 경로를 사이트 값 파일에 직접 적습니다. 어떤 이미지를 어느 경로로 미러해야  
하는지는 `deploy/helm/README.md` 의 레지스트리 절에 표로 있습니다.

### 설치 후 확인

```bash
# operator pod (1/1 Running)
kubectl get pod -n kcloud

# NPUClusterPolicy Ready 조건 (get 출력에는 READY 열이 없습니다)
kubectl -n kcloud get npuclusterpolicy npuclusterpolicy-sample -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'

# node-manager·exporter 는 kcloud, 벤더 device-plugin 은 kube-system 에 있습니다
kubectl get ds -A | grep -E "kcloud-|device-plugin"

# 노드 allocatable
kubectl get nodes -o custom-columns='NODE:.metadata.name,GPU:.status.allocatable.nvidia\.com/gpu,RNGD:.status.allocatable.furiosa\.ai/rngd'

# 드라이버 업그레이드 상태 (모든 노드 Idle 이면 정상)
kubectl get driverupgradestate
```

---

## 🗑️ Uninstall

`helm uninstall` 하나로 끝납니다. 차트의 pre-delete hook 이 가속기 자원을 쓰는 Pod 가 있는지 확인하고,  
있으면 uninstall 이 실패하며 hook 로그에 Pod 목록을 남깁니다(보류). 없으면 `npu.ai` CR 을 지워 operator 의  
finalizer 처리를 기다린 뒤 차트 리소스를 삭제하고, post-delete hook 이 `npu.ai` CRD 13종을 지웁니다.

```bash
helm uninstall kcloud-operator -n kcloud
```

같은 클러스터에 다시 설치할 예정이면 CRD 를 남깁니다. 클러스터 자체를 지우는 경로에서는 사용 중 검사를  
건너뜁니다. 두 값 모두 삭제 전에 `helm upgrade` 로 바꿔 둡니다.

```bash
helm upgrade kcloud-operator ... --set uninstall.purgeCRDs=false
helm upgrade kcloud-operator ... --set uninstall.gate.skipUsageCheck=true
```

hook 없이 지워야 할 때(`--no-hooks`)는 CR → DaemonSet → helm uninstall 순서로 직접 지웁니다. 래퍼 스크립트  
`deploy/helm/uninstall.sh` 가 이 순서를 대신하며, `--purge-crds` 를 주면 정책·상태 CRD 4종  
(`npuclusterpolicies`·`driverinstallpolicies`·`driverupgradestates`·`nodedevicereports`)까지 지웁니다.  
`uninstall.sh` 는 `install.sh` 와 같은 `deploy.env` 를 읽고, 값이 없으면 같은 기본값(`kcloud-operator`·`kcloud`)을 씁니다.

```bash
bash deploy/helm/uninstall.sh              # CR + helm uninstall
bash deploy/helm/uninstall.sh --purge-crds # + CRD 4종 삭제
RELEASE=kcloud-operator NAMESPACE=kcloud bash deploy/helm/uninstall.sh
```

---

## ⚙️ Configuration

전체 값은 `deploy/helm/values.yaml` 에 주석과 함께 있습니다. 자주 바꾸는 항목만 옮깁니다.

| 파라미터 | 기본값 | 설명 |
|----------|--------|------|
| `global.registry` | `ghcr.io/openkcloud` | kcloud 이미지 레지스트리 prefix |
| `global.vendorRegistry` | 빈 값 | 벤더 이미지 미러(비면 upstream 직접) |
| `image.tag` | Chart `appVersion` 과 동일 | operator 이미지 태그 |
| `deployClusterPolicy` | `true` | `NPUClusterPolicy` CR 자동 생성 |
| `<vendor>.enabled` | `true` | 벤더별 device-plugin 배치 |
| `<vendor>.advertiseBy` | `""` | 장치 광고 주체. `dra` 로 바꾸면 벤더 DRA 드라이버가 광고 |
| `furiosa.rngd.partitionPolicy` | `none` | RNGD 파티션. none/quad-core/dual-core/single-core |
| `furiosa.unified.enabled` | `false` | Warboy·RNGD 를 통합 device-plugin 하나로 배치 |
| `driverInstallPolicies.<vendor>.enabled` | `true` | 벤더별 `DriverInstallPolicy` CR 생성 |
| `webhook.enabled` | `false` | DIP·NCP validating webhook 과 opt-in Pod mutating webhook |
| `webhook.injectScope` | `pod` | `namespace` 면 `webhook.injectLabel` 이 `true` 인 네임스페이스의 `nvidia.com/*` 요청 Pod 에도 runtimeClass 를 주입(Pod 라벨 opt-in 은 유지) |
| `api.enabled` | `false` | 관리 REST API 와 웹 콘솔(`:9444` HTTPS) |
| `api.console.extraServiceAccounts` | `[]` | 외부 화면용 SA·권한 묶음 목록(name, role=viewer\|operator) |
| `metrics.enabled` | `false` | controller-runtime 지표 노출 |
| `operationCoordinator.mode` | `off` | `delegate` 면 장치 변경을 `AcceleratorOperation` 트랜잭션으로 직렬화 |
| `nvidia.dcgmExporter.enabled` | `false` | NVIDIA 텔레메트리 exporter(`:9400`) |
| `furiosa.exporter.enabled` | `false` | RNGD 텔레메트리 exporter(`:9410`) |
| `furiosa.exporter.kubeResourcesLabel` | `true` | exporter 지표에 장치를 할당받은 파드의 `namespace`·`pod`·`container` 라벨 추가(kubelet pod-resources 읽기 전용 마운트, 수집 설정은 `honorLabels: true`) |

기본값이 `false` 인 항목은 켠 상태로 배포됐을 때 영향 범위가 커서 opt-in 으로 두었습니다.  
`operationCoordinator.mode` 를 끄는 절차에는 선결 조건이 있습니다. `values.yaml` 의 해당 주석을  
읽고 진행합니다.

값 변경 예:

```bash
# Warboy 드라이버 버전 변경
helm upgrade kcloud-operator deploy/helm -n kcloud --reset-then-reuse-values \
  --set driverInstallPolicies.furiosa.driver.version=1.9.8-3

# RNGD 파티션 사용 (4 instance/card, 파티션 지원 내부 이미지 필요)
helm upgrade kcloud-operator deploy/helm -n kcloud --reset-then-reuse-values \
  --set furiosa.rngd.partitionPolicy=dual-core
```

---

## 📐 Custom Resources

CRD 는 `npu.ai/v1alpha1` 그룹의 13종입니다. 정의는 `api/v1alpha1/`, 생성된 매니페스트는  
`config/crd/bases/` 와 `deploy/helm/crds/` 에 있습니다.

| Kind | 축약 | scope | 역할 |
|------|------|-------|------|
| `NPUClusterPolicy` | — | Namespaced | 벤더별 device-plugin 과 node-manager 배치 |
| `DriverInstallPolicy` | `dip` | Cluster | 벤더별 드라이버 설치·업그레이드 정책 |
| `DriverUpgradeState` | `dus` | Cluster | 노드별 드라이버 업그레이드 진행 상태 |
| `NodeDeviceReport` | `ndr` | Cluster | node-manager 가 보고하는 노드 장치 목록 |
| `AcceleratorPartitionPolicy` | `acpp` | Cluster | MIG·RNGD 파티션과 MPS·time-slicing 공유 설정 |
| `AcceleratorOperation` | `aop` | Cluster | 장치 변경 트랜잭션. 충돌 판정과 노드 잠금 |
| `AcceleratorHealth` | `ah` | Cluster | 장치 단위 health 상태 |
| `AcceleratorHealthPolicy` | `ahp` | Cluster | 노드 selector 별 health 임계값과 상태별 조치 |
| `AcceleratorClass` | `aclass` | Cluster | 장치를 지정하지 않는 등급 선언. 요구 조건과 벤더 profile 매핑 |
| `AcceleratorWorkload` | `aw` | Namespaced | 벤더를 지정하지 않는 가속기 워크로드 요청 |
| `AcceleratorDescriptor` | `adesc` | Cluster | 장치 하나를 backend 중립으로 기술. 할당 단위와 식별자 고정 |
| `AcceleratorEvidence` | `aev` | Cluster | 노드별 검증 근거. 관측 등급, 환경 지문, 유효 기간 |
| `AcceleratorVerificationPolicy` | `avp` | Cluster | 실행할 체크 목록, 근거 신뢰 기간, 무효화 조건 |

### NPUClusterPolicy

device-plugin 과 `kcloud-node-manager` 를 관리합니다.

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
      devicePluginImage: "docker.io/furiosaai/furiosa-device-plugin:2026.1.1"
      partitionPolicy: "none"
      debugMode: false
  rebellions:
    enabled: true
    devicePluginImage: "docker.io/rebellions/k8s-device-plugin:v0.3.6"
  tenstorrent:
    enabled: true
    devicePluginImage: "ghcr.io/openkcloud/kcloud-tt-device-plugin:v0.1.0"
```

`partitionPolicy` 가 `none` 이 아니면 operator 가 device-plugin 에 `--policy` 를 넘깁니다.  
그 플래그를 지원하는 `furiosa-device-plugin-mi` 이미지가 필요합니다.

### DriverInstallPolicy

드라이버 설치와 자동 업그레이드를 관리합니다.

```yaml
apiVersion: npu.ai/v1alpha1
kind: DriverInstallPolicy
metadata:
  name: furiosa-warboy-ds
spec:
  vendor: furiosa
  model: warboy
  driver:
    version: "1.9.9-3"
    mode: job
  rebootStrategy: IfNeeded
  verifiedVersions:
    - "1.7.8"
    - "1.9.8-3"
    - "1.9.9-3"
```

`mode: job` 은 설치를 일회성 Job 으로 실행해 특권 DaemonSet 을 상주시키지 않습니다.  
`mode: daemonset` 경로는 회귀 대비로 남아 있습니다. `verifiedVersions` 가 비어 있으면 버전 검증을  
건너뛰고, 값이 있으면 목록 밖 버전에 대해 `DriverUpgradeState` 를 `UnverifiedVersion` 으로  
전이시킵니다.

---

## 💻 CLI

`kubectl-npu` 는 노드별 상태 조회와 정책 patch 를 묶은 kubectl 플러그인입니다.

```bash
kubectl npu status                                # 노드×벤더 상태 요약
kubectl npu status --json                         # 같은 내용을 JSON 으로
kubectl npu driver-version                        # 노드별 설치된 드라이버 버전
kubectl npu describe node [<노드>] [--json]        # 장치·광고량·health·evidence·정책
kubectl npu upgrade nvidia --version 580.126.10 --auto
kubectl npu toggle rngd --enabled true
```

---

## 🛠️ Development

컨트롤러는 `internal/controller/` 에 있고 `cmd/main.go` 가 등록합니다.

| Reconciler | 대상 |
|------------|------|
| `NPUClusterPolicyReconciler` | device-plugin·node-manager DaemonSet 과 그 ServiceAccount |
| `DriverDaemonSetReconciler` | 드라이버 설치 DaemonSet 과 Job |
| `ToolkitDaemonSetReconciler` | NVIDIA container-toolkit DaemonSet |
| `DriverUpgradeReconciler` | 노드별 drain·설치·재부팅·검증 순서 제어 |
| `AcceleratorPartitionPolicyReconciler` | MIG·RNGD 파티션과 MPS·time-slicing 적용 |
| `AcceleratorOperationReconciler` | 장치 변경 트랜잭션 조정 |
| `AcceleratorHealthReconciler` | 장치 health 판정 |
| `AcceleratorWorkloadReconciler` | 추상 워크로드 요청 번역 |
| `MigObservationReconciler` | 정책과 무관한 MIG 모드 관측 |

DaemonSet·Job 이름은 `internal/naming/` 이 한 곳에서 정합니다. 자주 쓰는 make 타깃은 다음과 같습니다.

```bash
make help          # 전체 타깃 목록
make manifests generate   # CRD·DeepCopy 재생성
make sync-helm-crds       # 생성된 CRD 를 deploy/helm/crds/ 로 복사
make lint test            # 커밋 전 검사
make test-e2e             # Kind 클러스터 e2e
```

RBAC 은 DaemonSet, Job, ConfigMap, Pod, Node 라벨 patch 권한을 포함합니다. 상세는  
`config/rbac/` 와 `deploy/helm/templates/` 를 참조합니다.

---

## 📚 관련 문서

- [deploy/helm/README.md](deploy/helm/README.md): 차트 파라미터 상세
- [deploy/helm/UPGRADE.md](deploy/helm/UPGRADE.md): operator 버전 업그레이드 절차

---

## 🤝 Contributing

작업 브랜치에서 `make lint test` 를 통과시킨 뒤 PR 을 엽니다. 검토자와 승인자는 `OWNERS` 에 있습니다.

---

## 🔗 References

- [Kubebuilder Documentation](https://book.kubebuilder.io)
- [Operator SDK](https://sdk.operatorframework.io)
- [NVIDIA k8s-device-plugin](https://github.com/NVIDIA/k8s-device-plugin)
- [Furiosa Device Plugin](https://github.com/furiosa-ai/furiosa-device-plugin)
- [Rebellions Device Plugin Documentation](https://docs.rebellions.ai)

---

## 📄 License

Apache License 2.0 입니다. 전문은 저장소 루트의 `LICENSE` 파일에 있습니다.
