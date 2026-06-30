# UPGRADE.md: NPU Operator Helm Chart 업그레이드 가이드
# 생성일: 2026-04-10 | 수정일: 2026-04-22

# NPU Operator Upgrade Guide

## v0.2.0 → v0.3.0

---

## Prerequisites

- Helm v3.2.0+
- kubectl v1.21+
- 클러스터 관리자 권한
- 기존 v0.2.0 설치 확인: `helm list -n npu-operator`

---

## Breaking Changes

- **CRD 추가**: `DriverInstallPolicy` CRD가 새로 추가되었습니다.
  Helm upgrade 전에 CRD를 수동으로 적용해야 합니다.
- **clusterPolicy.namespace 기본값 변경**: `default` → `""` (Release.Namespace로 fallback)
  기존에 명시적으로 namespace를 지정하지 않은 경우 values.yaml에서 확인 필요.

---

## Upgrade Procedure

### 1단계: CRD 먼저 적용

```bash
# chart 디렉토리에서 실행
kubectl apply -f ./crds/

# 적용 확인
kubectl get crd | grep npu.ai
```

### 2단계: Helm Upgrade

```bash
helm upgrade npu-operator ./npu-operator \
  --namespace npu-operator \
  --values my-values.yaml
```

### 3단계: 업그레이드 확인

```bash
# Pod 상태 확인
kubectl get pods -n npu-operator

# NPUClusterPolicy 확인
kubectl get npuclusterpolicy -A

# DriverInstallPolicy 확인 (신규)
kubectl get dip

# NodeDeviceReport 확인
kubectl get ndr -A
```

---

## Rollback

업그레이드 실패 시:

```bash
# Helm rollback
helm rollback npu-operator 1 --namespace npu-operator

# CRD rollback (주의: 데이터 손실 가능)
# CRD 삭제는 해당 CR 데이터도 함께 삭제됩니다.
# 반드시 백업 후 진행하세요.
kubectl get dip -A -o yaml > dip-backup.yaml
kubectl delete crd driverinstallpolicies.npu.ai
```

---

## v0.3.0 → v0.3.1-rename

### 변경 내용

- Go 모듈 경로 `npu-operator` → `kcloud-operator` (내부 코드 정합성 개선)
- K8s 리소스 이름, namespace, 라벨, image repo는 변경 없음
- CRD schema 변경 없음 (make manifests 재생성에 의한 minor 포맷 변경만)

### Breaking Changes

없음. 이 버전은 내부 코드 리팩토링만 포함합니다. 기존 CR, CRD, RBAC 모두 호환됩니다.

### Upgrade Procedure

#### 1단계: 이미지 빌드 (선택 — 직접 빌드하는 경우)

```bash
cd kcloud-operator
REGISTRY=<your-registry>  # 예: <your-registry>/kcloud  (Harbor 프로젝트 포함)
sudo docker build -t $REGISTRY/npu-operator:v0.3.1-rename .
sudo docker push $REGISTRY/npu-operator:v0.3.1-rename
```

#### 2단계: CRD 적용 (안전을 위해)

```bash
kubectl apply -f ./crds/
```

#### 3단계: Helm Upgrade

```bash
helm upgrade npu-operator ./helm/kcloud-operator \
  -n npu-operator \
  --set image.tag=v0.3.1-rename \
  --wait --timeout 3m
```

#### 4단계: 검증

```bash
# Pod 상태 확인
kubectl -n npu-operator get pods

# 이미지 태그 확인
kubectl -n npu-operator get pod -l app.kubernetes.io/name=npu-operator \
  -o jsonpath='{.items[*].spec.containers[*].image}'
# 예상: <your-registry>/npu-operator:v0.3.1-rename

# 로그 확인
kubectl -n npu-operator logs deploy/npu-operator-controller-manager --tail=50

# CR 정상 확인
kubectl get npuclusterpolicy,dip -A
```

---

## v0.3.1-rename → v0.5.4-nvrtc 업그레이드 주의사항

### ⚠️ 사고 사례: NPUClusterPolicy CR 일시 삭제 (2026-04-22)

이전 chart 버전(0.4.4 이하)에서 `helm upgrade` 수행 시 다음 시나리오로 장애가 발생했습니다.

1. `clusterpolicy.yaml`에 설정된 `helm.sh/hook-delete-policy: before-hook-creation` 으로 인해
   `helm upgrade` 시작 시점에 기존 `NPUClusterPolicy` CR이 자동 삭제됩니다.
2. `post-upgrade` hook이 CR 재생성에 실패하면 `NPUClusterPolicy` CR이 부재 상태가 됩니다.
3. Operator가 CR 부재를 감지하고 `kube-system`의 모든 `npu-op-device-plugin-*` DaemonSet을
   일시 삭제하는 사고가 발생했습니다.

### 변경 내용 (chart 0.4.5 / v0.5.4-nvrtc)

- `clusterpolicy.yaml`에서 helm hook annotation 전체 제거
- `NPUClusterPolicy` CR을 일반 Helm 관리 리소스로 전환하여 upgrade 중 삭제 리스크 제거

### 사전 점검 (업그레이드 전)

기존 버전에서 업그레이드하기 전에 반드시 CR을 백업하세요:

```bash
kubectl get npuclusterpolicy -A -o yaml > /tmp/npuclusterpolicy-backup.yaml
```

### CR 복구 절차 (업그레이드 후 CR이 사라진 경우)

```bash
helm template npu-operator ./helm/kcloud-operator \
  -n npu-operator \
  -f <values.yaml> \
  --show-only templates/clusterpolicy.yaml \
  | kubectl apply -f -
```

복구 후 device-plugin DaemonSet 상태 확인:

```bash
kubectl get ds -n kube-system | grep npu-op
```

---

## v0.5.6 → v0.5.7 (chart 0.5.0 → 0.5.1, 2026-04-22)

### 변경 내용

**DeviceSpec (DS) 이름 재정렬 (US-R1/R2)**

벤더별 device plugin DaemonSet 이름을 일관된 패턴으로 변경합니다:
- 이전: `npu-op-<vendor>-device-plugin` (예: `npu-op-nvidia-device-plugin`)
- 신규: `npu-op-device-plugin-<vendor>` (예: `npu-op-device-plugin-nvidia`)

| 벤더 | 구 이름 | 신 이름 |
|------|--------|--------|
| NVIDIA | `npu-op-nvidia-device-plugin` | `npu-op-device-plugin-nvidia` |
| Furiosa | `npu-op-furiosa-device-plugin` | `npu-op-device-plugin-furiosa` |
| RNGD | `npu-op-furiosa-rngd-device-plugin` | `npu-op-device-plugin-furiosa-rngd` |
| Rebellions | `npu-op-rbln-device-plugin` | `npu-op-device-plugin-rbln` |

**Tester 샘플 Pod namespace 변경 (US-R5)**

- 이전: `npu-operator` (운영자 namespace)
- 신규: `default` (사용자 workload namespace)

모든 tester 샘플 Pod의 namespace가 `default`로 통일되어, 실제 workload 배포와
동일한 환경에서 검증할 수 있습니다.

**chart version**: `0.5.0` → `0.5.1`
**operator 이미지**: `v0.5.6` → `v0.5.7`

### Breaking Changes ⚠️

**DaemonSet 이름 변경으로 인한 이전 DS 정리 필수**

K8s의 DaemonSet은 `spec.selector`가 immutable이므로, 직접 rename은 불가능합니다.
따라서 **구 DS를 삭제하고 신 DS를 생성**해야 합니다.

#### Pre-upgrade Hook (자동 정리)

이 chart 버전부터 `pre-upgrade` hook Job이 자동으로 다음을 수행합니다:

```bash
# 1. 구 device plugin DaemonSet 4종 삭제 (orchestrated cleanup)
kubectl delete ds -n kube-system \
  npu-op-nvidia-device-plugin \
  npu-op-furiosa-device-plugin \
  npu-op-furiosa-rngd-device-plugin \
  npu-op-rbln-device-plugin \
  --ignore-not-found

# 2. Pod 안전 종료 대기 (gracefulTerminationPeriod)
# 3. 신 이름의 DaemonSet 자동 생성 (reconciliation)
```

### Upgrade Procedure

#### 1단계: 백업 (권장)

```bash
# 현재 DS 상태 백업
kubectl get ds -n kube-system -o yaml > /tmp/ds-backup.yaml

# 현재 Pod 상태 백업
kubectl get pod -n kube-system -o yaml | grep -E "^metadata:|name:|namespace:" > /tmp/pods-backup.txt
```

#### 2단계: CRD 적용

```bash
kubectl apply -f ./crds/
```

#### 3단계: Helm Upgrade (pre-upgrade hook 자동 실행)

```bash
helm upgrade npu-operator ./helm/kcloud-operator \
  -n npu-operator \
  --reuse-values \
  --set image.tag=v0.5.7 \
  --wait --timeout 5m
```

#### 4단계: 신 DS 배포 확인 (rollout 대기)

```bash
# Operator 배포 완료 확인
kubectl rollout status deploy/npu-operator-controller-manager -n npu-operator --timeout=3m

# 구 DS 삭제 확인 (0 expected)
kubectl -n kube-system get ds | grep "npu-op-" | wc -l

# 신 이름 DS 생성 확인 (4 expected if all vendors enabled)
kubectl -n kube-system get ds -l app.kubernetes.io/name=npu-operator -o custom-columns=NAME:.metadata.name | grep "npu-op-device-plugin-"
```

#### 5단계: Device Plugin Pod 준비 확인

```bash
# 각 노드에서 device plugin pod이 ready인지 확인
kubectl -n kube-system get pod -l app.kubernetes.io/part-of=npu-operator -w

# 예상 출력 (모든 pod이 1/1 Running):
# NAME                                      READY   STATUS    RESTARTS   AGE
# npu-op-detector-xxxxx                     1/1     Running   0          1m
# npu-op-device-plugin-nvidia-xxxxx         1/1     Running   0          1m
# npu-op-device-plugin-furiosa-xxxxx        1/1     Running   0          1m
# ...
```

#### 6단계: Allocatable 리소스 확인

```bash
# 각 벤더의 리소스가 정상 할당되었는지 확인
kubectl describe node k8s-worker1 | grep -A 20 "Allocatable" | grep -E "nvidia|furiosa|rebellions"

# 예상 출력:
# furiosa.ai/rngd:  1
# nvidia.com/gpu:   2
# rebellions.ai/ATOM: 1
```

#### 7단계: 샘플 Pod 테스트 (default namespace)

```bash
# v0.5.7부터 샘플 Pod이 default namespace에서 실행됩니다
kubectl apply -f util/tester/nvidia/sample-pod.yaml
kubectl apply -f util/tester/furiosa/sample-pod.yaml
kubectl apply -f util/tester/rngd/sample-pod.yaml
kubectl apply -f util/tester/rebellions/sample-pod.yaml

# 각 Pod의 준비 확인
kubectl get pod -n default | grep -E "nvidia|furiosa|rngd|rebellions"
kubectl wait pod/<pod-name> --for=condition=ready --timeout=120s -n default
```

### Rollback (Pre-upgrade Hook 실패 시)

만약 pre-upgrade hook이 실패했거나 구 DS가 여전히 존재한다면:

#### 옵션 1: Helm Rollback (권장)

```bash
# 마지막 성공한 버전으로 롤백
helm rollback npu-operator -n npu-operator

# 상태 확인
kubectl -n kube-system get ds | grep npu-op
```

#### 옵션 2: 수동 정리 + 재시도

구 DS가 여전히 존재한다면 수동으로 정리:

```bash
# 구 DS 확인
kubectl -n kube-system get ds | grep "npu-op-" | grep -v "npu-op-device-plugin-"

# 구 DS 삭제 (일시적으로 device plugin Pod이 중단될 수 있습니다)
kubectl delete ds -n kube-system \
  npu-op-nvidia-device-plugin \
  npu-op-furiosa-device-plugin \
  npu-op-furiosa-rngd-device-plugin \
  npu-op-rbln-device-plugin \
  --ignore-not-found

# Pod 안전 종료 대기 (최대 30초)
sleep 30

# Helm upgrade 재시도
helm upgrade npu-operator ./helm/kcloud-operator \
  -n npu-operator \
  --reuse-values \
  --set image.tag=v0.5.7 \
  --wait --timeout 5m
```

#### 옵션 3: 완전 재설치

```bash
# 기존 설치 완전 제거 (finalizer 관리 필요)
helm uninstall npu-operator -n npu-operator

# CRD 백업
kubectl get npuclusterpolicy -A -o yaml > /tmp/npucp-backup.yaml

# CRD 삭제 (주의: 데이터 손실)
kubectl delete crd npuclusterpolicies.npu.ai nodedevicereports.npu.ai driverinstallpolicies.npu.ai

# 재설치
helm install npu-operator ./helm/kcloud-operator \
  -n npu-operator \
  -f <values.yaml>

# CR 복구
kubectl apply -f /tmp/npucp-backup.yaml
```

### 주의사항

1. **Workload 연속성**: Upgrade 중 device plugin Pod이 순차적으로 재시작됩니다.
   실행 중인 workload가 있으면 일시적으로 리소스 할당 불가 상태가 될 수 있습니다.
   
2. **Pod 재스케줄링**: 기존 device plugin Pod이 종료되면, kubelet이 새 Pod을 스케줄합니다.
   동일 노드에 새 Pod이 생성됩니다 (nodeselector 유지).

3. **Pre-upgrade Hook 타임아웃**: 대규모 클러스터에서 구 Pod 종료가 오래 걸릴 수 있으므로
   `--timeout`을 충분히 크게 설정하세요 (권장: 5m 이상).

---

## Known Issues

- **DriverInstallPolicy hook-weight**: ClusterPolicy(weight=10)보다 먼저(weight=5) 생성됩니다.
  드라이버 설치 완료 후 ClusterPolicy가 적용되는 순서를 의도한 것입니다.
- **reboot 전략**: `rebootStrategy: IfNeeded` 설정 시 노드 재부팅이 발생할 수 있습니다.
  유지보수 윈도우 내에 업그레이드를 진행하세요.
- **kernelAllowlist**: 커널 버전이 allowlist에 없는 노드는 드라이버 설치가 스킵됩니다.
  `kubectl get dip -o yaml`로 status를 확인하세요.
- **Chart.yaml name 변경 금지**: `Chart.yaml`의 `name: npu-operator`를 변경하면
  `_helpers.tpl`이 생성하는 `app.kubernetes.io/name` 라벨이 바뀌고,
  Deployment `spec.selector.matchLabels`가 변경됩니다. K8s는 `spec.selector`를
  immutable로 취급하므로 `helm upgrade`가 실패합니다.
  chart name 변경이 필요한 경우 반드시 `helm uninstall` → `helm install` 경로를
  사용하세요 (기존 CR finalizer 관리, 다운타임 계획 필요).

---

## v0.5.9 → v0.5.10 (chart 0.5.9 → 0.5.10, 2026-05-11)

### 변경 내용

- **RNGD driver 이미지 전환**: `driverInstallPolicies.rngd.driver.image` 를
  `furiosa-driver-ds-rngd:2026.1.0-v2` → `furiosa-rngd-driver-installer:2026.1.0` 로 변경.
  신규 installer 이미지는 `furiosa-smi` 패키지 추가 설치 및 동일 host driver 설치 결과를
  E2E 검증(2026-05-11) 완료한 이미지입니다.

### Breaking Changes

없음. 기존 NVIDIA/Furiosa-Warboy/Rebellions DS는 영향을 받지 않습니다.

### Upgrade Procedure

```bash
# CRD 적용 (schema 변경 없음 — 안전을 위해 권장)
kubectl apply -f ./crds/

# Helm Upgrade
helm upgrade npu-operator ./helm/kcloud-operator \
  -n npu-operator \
  --reuse-values
```

---

## v0.5.22 → v0.5.23 (chart 0.5.13 → 0.5.14, 2026-06-02)

### 변경 내용 — 관리 리소스 이름 + values 스키마 전면 변경

operator 가 생성·관리하는 DaemonSet / SA / ClusterRole / ConfigMap 이름이 아래 표와 같이 변경됩니다.
네이밍 원칙: detector/driver 는 `kcloud-` prefix, device-plugin 은 vendor 명.

| 종류 | 구 이름 (≤v0.5.22) | 신 이름 (v0.5.23+) |
|------|-------------------|-------------------|
| detector | `npu-op-detector` | `kcloud-detector` |
| DP nvidia | `npu-op-device-plugin-nvidia` | `nvidia-device-plugin` |
| DP furiosa(warboy) | `npu-op-device-plugin-furiosa` | `furiosa-device-plugin` |
| DP furiosa-rngd | `npu-op-device-plugin-furiosa-rngd` | `furiosa-rngd-device-plugin` |
| DP rebellions | `npu-op-device-plugin-rbln` | `rbln-device-plugin` |
| driver nvidia | `npu-op-driver-nvidia-generic` | `kcloud-nvidia-driver` |
| driver furiosa-warboy | `npu-op-driver-furiosa-warboy` | `kcloud-furiosa-warboy-driver` |
| driver furiosa-rngd | `npu-op-driver-furiosa-rngd` | `kcloud-furiosa-rngd-driver` |
| rbln SA/ClusterRole/ConfigMap | `npu-op-rbln-device-plugin*` | `rbln-device-plugin*` |

### 값 스키마 변경 (Values Schema)

**`global.registry`를 통한 일괄 레지스트리 prefix 지원**

- **신규**: `values.yaml` 상단에 `global.registry: "<your-registry>"` 필드 추가
- **효과**: operator/detector/device-plugin/driver 이미지의 repository 필드가 모두 이 prefix를 공유
  ```yaml
  global:
    registry: "<your-registry>"  # 또는 "" (비어있으면 registry-relative path 사용)
  
  image:
    repository: npu-operator      # → 최종: <global.registry>/npu-operator:<tag>
    tag: v0.5.23
  
  detector:
    repository: "npu-detector"    # → 최종: <global.registry>/npu-detector:<tag>
  ```
- **업그레이드 시**: 레지스트리 변경 후 다시 배포하려면 한 줄로 충분
  ```bash
  helm upgrade npu-operator deploy/helm -n npu-operator --reuse-values \
    --set global.registry=new-registry.internal:5000
  ```
- **airgap 배포**: `values-airgap.example.yaml` 참조 (모든 public 이미지도 full 경로로 override)

### Breaking Changes ⚠️

- **DaemonSet selector immutable**: 구 DS 를 그대로 두고 upgrade 하면 신 이름 DS 신규 생성 시 충돌 발생.
  pre-upgrade hook Job 이 구 이름 DS 전체를 자동 삭제 후 신 이름 DS 를 재생성함.
- **전체 클린(clean slate) 권장**: 기존 클러스터가 있으면 `helm uninstall` → 잔존 DS 완전 삭제 → `helm install` 순서를 권장.
- **values.yaml** `rebellions.configMapName` 이 `npu-op-rbln-device-plugin-config` → `rbln-device-plugin-config` 로 변경.
  커스텀 override 가 있으면 함께 갱신 필요.

### Pre-upgrade Hook (자동 정리)

`helm upgrade` 시 pre-upgrade Job 이 구 이름 DS 전체를 `--ignore-not-found` 로 안전 삭제합니다.
삭제 대상: `npu-op-detector`, `npu-op-device-plugin-{nvidia,furiosa,furiosa-rngd,rbln}`,
`npu-op-driver-{nvidia-generic,furiosa-warboy,furiosa-rngd}`,
`npu-op-{nvidia,furiosa,furiosa-rngd,rbln}-device-plugin` (pre-v0.5.7 세대 포함).

### Upgrade Procedure

#### 방법 A: Full Clean + Fresh Install (권장)

```bash
# 1. NPUClusterPolicy/DIP 삭제 (operator finalizer 정리)
kubectl delete npuclusterpolicy --all -A
kubectl delete driverinstallpolicy --all -A

# 2. Helm uninstall
helm uninstall npu-operator -n npu-operator

# 3. 잔존 구 이름 DS 완전 삭제
kubectl -n kube-system delete ds --ignore-not-found \
  npu-op-detector \
  npu-op-device-plugin-nvidia npu-op-device-plugin-furiosa \
  npu-op-device-plugin-furiosa-rngd npu-op-device-plugin-rbln \
  npu-op-driver-nvidia-generic npu-op-driver-furiosa-warboy \
  npu-op-driver-furiosa-rngd

# 4. 구 이름 보조 리소스(rebellions) 삭제
kubectl delete sa,clusterrole,clusterrolebinding,configmap \
  -l app.kubernetes.io/name=npu-op-rbln-device-plugin --ignore-not-found -A

# 5. CRD 적용
kubectl apply -f ./crds/

# 6. Fresh install (신 이름으로 모든 리소스 자동 생성)
helm install npu-operator deploy/helm -n npu-operator --create-namespace \
  -f <values.yaml>
```

#### 방법 B: Helm Upgrade (pre-upgrade hook 자동 실행)

```bash
# 1. CRD 적용
kubectl apply -f ./crds/

# 2. Helm upgrade (pre-upgrade hook 이 구 DS 자동 삭제)
helm upgrade npu-operator deploy/helm \
  -n npu-operator \
  --reuse-values \
  --set image.tag=v0.5.23 \
  --wait --timeout 10m
```

### 검증

```bash
# 구 이름 DS 잔존 0 확인 (0 lines expected)
kubectl -n kube-system get ds | grep "npu-op-"

# 신 이름 DS 확인
kubectl -n kube-system get ds | grep -E "kcloud-detector|device-plugin|kcloud-.*-driver"

# 예상 출력:
# kcloud-detector                         1/1   ...
# nvidia-device-plugin                    1/1   ...
# furiosa-device-plugin                   1/1   ...
# furiosa-rngd-device-plugin              1/1   ...
# rbln-device-plugin                      1/1   ...
# kcloud-nvidia-driver                    1/1   ...
# kcloud-furiosa-warboy-driver            1/1   ...
# kcloud-furiosa-rngd-driver              1/1   ...

# allocatable 복원 확인
kubectl get node -o custom-columns='NODE:.metadata.name,GPU:.status.allocatable.nvidia\.com/gpu,RNGD:.status.allocatable.furiosa\.ai/rngd'

# DUS(DriverUpgradeState) Idle 확인 (드라이버 상태머신이 신규 DS명 인식)
kubectl get driverupgradestate -A
```

### Rollback

```bash
helm rollback npu-operator -n npu-operator
# 이후 구 DS 수동 정리 + helm upgrade 재시도
```
